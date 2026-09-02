package authserver

// OIDC authorization-code flow with PKCE and signed state (CSRF protection).
//
// Flow:
//   GET /auth/oidc/start?tenant={slug}
//     → generate code_verifier (PKCE), sign state HMAC, redirect to IdP authorize endpoint
//   GET /auth/oidc/callback?code=...&state=...
//     → verify state HMAC, exchange code for ID token (PKCE), validate claims,
//       upsert user, issue internal JWT, set cookie, redirect to UI.
//
// External OIDC token is never exposed outside this package. The rest of the
// system only ever sees the internal HS256 JWT.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// oidcStateCookie holds the PKCE code_verifier and is cleared after callback.
	oidcStateCookie = "them_oidc_state"
	// oidcStateMaxAge is how long the state cookie lives.
	oidcStateMaxAge = 600 // 10 minutes
)

// OIDCHandlers provides the two OIDC endpoints. It is separate from Handlers to
// keep the file under 400 lines and give OIDC a clear seam for testing.
type OIDCHandlers struct {
	oidcStore  OIDCStore
	signer     *tokenSigner
	cfg        *Config
	log        *slog.Logger
	httpClient *http.Client  // injectable for tests
	jwks       jwksFetcher   // injectable for tests
}

// NewOIDCHandlers builds the OIDC handler set.
func NewOIDCHandlers(oidcStore OIDCStore, signer *tokenSigner, cfg *Config, log *slog.Logger) *OIDCHandlers {
	client := &http.Client{Timeout: 10 * time.Second}
	return &OIDCHandlers{
		oidcStore:  oidcStore,
		signer:     signer,
		cfg:        cfg,
		log:        log,
		httpClient: client,
		jwks:       &httpJWKSFetcher{client: client},
	}
}

// ── OIDC state cookie value: base64url(code_verifier) + "." + base64url(HMAC) ──

// signState produces a signed state token embedding the tenant slug and a random nonce.
// Format: base64url(slug + ":" + nonce) + "." + base64url(HMAC-SHA256(signing_input, key))
func signState(tenantSlug, nonce string, key []byte) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(tenantSlug + ":" + nonce))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// verifyState verifies the HMAC and extracts the tenant slug. Returns an error
// if the signature is invalid or the format is malformed.
func verifyState(state string, key []byte) (tenantSlug string, err error) {
	parts := strings.SplitN(state, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid state format")
	}
	payload, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", fmt.Errorf("state signature invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("state payload decode failed")
	}
	colonIdx := strings.IndexByte(string(decoded), ':')
	if colonIdx < 0 {
		return "", fmt.Errorf("state payload missing separator")
	}
	return string(decoded[:colonIdx]), nil
}

// ── PKCE helpers ──────────────────────────────────────────────────────────────

// newCodeVerifier generates a 43-byte (256-bit) PKCE code_verifier.
func newCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallenge returns the S256 code_challenge for the given verifier.
func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ── OIDC discovery + token endpoint helpers ───────────────────────────────────

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func (h *OIDCHandlers) discover(ctx context.Context, discoveryURL string) (*oidcDiscovery, error) {
	u := strings.TrimRight(discoveryURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery returned %d", resp.StatusCode)
	}
	var d oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&d); err != nil {
		return nil, err
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, fmt.Errorf("discovery document missing endpoints")
	}
	return &d, nil
}

// idTokenClaims is the minimal set of claims the callback validates.
type idTokenClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Exp   int64  `json:"exp"`
}

type tokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

func (h *OIDCHandlers) exchangeCode(ctx context.Context, tokenEndpoint, code, redirectURI, clientID, clientSecret, verifier string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

// OIDCStart handles GET /auth/oidc/start?tenant={slug}
// Loads the tenant IdP config, generates PKCE + signed state, sets a short-lived
// cookie with the code_verifier, and redirects the browser to the IdP.
func (h *OIDCHandlers) OIDCStart(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("tenant")
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "tenant parameter required")
		return
	}

	_, idpCfg, err := h.oidcStore.GetTenantIDPConfig(r.Context(), slug)
	switch {
	case err == ErrTenantNotFound:
		writeErr(w, http.StatusNotFound, "tenant not found")
		return
	case err == ErrNoIDPConfig:
		writeErr(w, http.StatusUnprocessableEntity, "tenant has no OIDC configuration")
		return
	case err != nil:
		h.log.Error("oidc start: idp config lookup failed", "tenant", slug)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	disc, err := h.discover(r.Context(), idpCfg.DiscoveryURL)
	if err != nil {
		h.log.Error("oidc start: discovery failed", "tenant", slug)
		writeErr(w, http.StatusBadGateway, "IdP discovery failed")
		return
	}

	verifier, err := newCodeVerifier()
	if err != nil {
		h.log.Error("oidc start: code verifier generation failed")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	challenge := codeChallenge(verifier)

	// Nonce for state: random 16 bytes.
	nonceBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		h.log.Error("oidc start: nonce generation failed")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	state := signState(slug, nonce, []byte(h.cfg.JWTSecret))

	// Store verifier in a short-lived HttpOnly cookie so the callback can read it.
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    verifier,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode, // Lax required for cross-site redirect back
		MaxAge:   oidcStateMaxAge,
	})

	authURL := disc.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {idpCfg.ClientID},
		"redirect_uri":          {idpCfg.RedirectURI},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallback handles GET /auth/oidc/callback?code=...&state=...
// Verifies the state HMAC, exchanges the code for an ID token (with PKCE),
// parses the email claim, upserts the user, issues an internal JWT, sets the
// auth cookies, and redirects to the UI.
func (h *OIDCHandlers) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		writeErr(w, http.StatusBadRequest, "state and code are required")
		return
	}

	// Verify state HMAC.
	slug, err := verifyState(state, []byte(h.cfg.JWTSecret))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid state parameter")
		return
	}

	// Retrieve code_verifier from cookie.
	verifier := cookieValue(r, oidcStateCookie)
	if verifier == "" {
		writeErr(w, http.StatusBadRequest, "OIDC state cookie missing or expired")
		return
	}
	// Clear the state cookie immediately.
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})

	tenantUUID, idpCfg, err := h.oidcStore.GetTenantIDPConfig(r.Context(), slug)
	switch {
	case err == ErrTenantNotFound:
		writeErr(w, http.StatusNotFound, "tenant not found")
		return
	case err == ErrNoIDPConfig:
		writeErr(w, http.StatusUnprocessableEntity, "tenant has no OIDC configuration")
		return
	case err != nil:
		h.log.Error("oidc callback: idp config lookup failed", "tenant", slug)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	disc, err := h.discover(r.Context(), idpCfg.DiscoveryURL)
	if err != nil {
		h.log.Error("oidc callback: discovery failed", "tenant", slug)
		writeErr(w, http.StatusBadGateway, "IdP discovery failed")
		return
	}

	tr, err := h.exchangeCode(r.Context(), disc.TokenEndpoint, code,
		idpCfg.RedirectURI, idpCfg.ClientID, idpCfg.ClientSecret, verifier)
	if err != nil {
		h.log.Error("oidc callback: code exchange failed", "tenant", slug)
		writeErr(w, http.StatusBadGateway, "IdP token exchange failed")
		return
	}
	if tr.IDToken == "" {
		h.log.Error("oidc callback: id_token missing in token response", "tenant", slug)
		writeErr(w, http.StatusBadGateway, "IdP did not return an id_token")
		return
	}

	claims, err := verifyRS256IDToken(r.Context(), h.jwks, disc.JWKSURI, tr.IDToken)
	if err != nil {
		h.log.Error("oidc callback: id_token verification failed", "tenant", slug)
		writeErr(w, http.StatusBadGateway, "invalid id_token from IdP")
		return
	}

	user, err := h.oidcStore.UpsertOIDCUser(r.Context(), tenantUUID, claims.Email, claims.Name)
	if err != nil {
		h.log.Error("oidc callback: user upsert failed", "tenant", slug, "email", claims.Email)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	access, expiresIn, err := h.signer.IssueAccessToken(user.ID, user.Username, user.Name, user.Role, tenantUUID, 0)
	if err != nil {
		h.log.Error("oidc callback: token issue failed", "tenant", slug)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	refresh, err := h.signer.IssueRefreshToken(user.ID)
	if err != nil {
		h.log.Error("oidc callback: refresh token issue failed", "tenant", slug)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: accessCookie, Value: access, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: expiresIn,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: refresh, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: h.cfg.RefreshTokenExpiry,
	})

	http.Redirect(w, r, "/", http.StatusFound)
}
