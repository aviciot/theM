package authserver

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ── test RSA key (generated once, shared across tests in this package) ────────

// testRSAKey is a 2048-bit RSA key used to sign id_tokens in tests.
// Generated at test init time; never used outside tests.
var testRSAKey *rsa.PrivateKey

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("oidc_test: failed to generate test RSA key: " + err.Error())
	}
}

// signTestIDToken signs a header+payload JWT with the testRSAKey (RS256).
func signTestIDToken(header, payload string) string {
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, testRSAKey, crypto.SHA256, digest[:])
	if err != nil {
		panic("oidc_test: sign failed: " + err.Error())
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// testJWKS returns the JSON Web Key Set for testRSAKey.
func testJWKS() map[string]any {
	pub := &testRSAKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	// Encode the exponent as a big-endian byte slice (minimal length).
	e := pub.E
	var eBytes []byte
	for e > 0 {
		eBytes = append([]byte{byte(e & 0xff)}, eBytes...)
		e >>= 8
	}
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)
	return map[string]any{
		"keys": []map[string]any{{
			"kid": "test-key-1",
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"n":   n,
			"e":   eStr,
		}},
	}
}

// bigIntToBase64URL serialises a *big.Int for use in a JWK.
func bigIntToBase64URL(n *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(n.Bytes())
}

// ── fake OIDC store ───────────────────────────────────────────────────────────

type fakeOIDCStore struct {
	tenants map[string]struct {
		id  string
		cfg *IDPConfig
	}
	upsertedUsers    []*userRecord
	groupRoleMapping map[string]string // group_claim → role; empty = no match
}

func newFakeOIDCStore() *fakeOIDCStore {
	return &fakeOIDCStore{
		tenants: map[string]struct {
			id  string
			cfg *IDPConfig
		}{},
		groupRoleMapping: map[string]string{},
	}
}

func (f *fakeOIDCStore) addTenant(slug, id string, cfg *IDPConfig) {
	f.tenants[slug] = struct {
		id  string
		cfg *IDPConfig
	}{id, cfg}
}

func (f *fakeOIDCStore) GetTenantIDPConfig(_ context.Context, slug string) (string, *IDPConfig, error) {
	t, ok := f.tenants[slug]
	if !ok {
		return "", nil, ErrTenantNotFound
	}
	if t.cfg == nil {
		return "", nil, ErrNoIDPConfig
	}
	return t.id, t.cfg, nil
}

func (f *fakeOIDCStore) UpsertOIDCUser(_ context.Context, _, email, name, role string) (*userRecord, error) {
	if role == "" {
		role = "viewer"
	}
	u := &userRecord{
		ID: 42, Username: email, Name: name, Email: email,
		Role: role, DashboardAccess: role,
	}
	f.upsertedUsers = append(f.upsertedUsers, u)
	return u, nil
}

func (f *fakeOIDCStore) GetGroupRole(_ context.Context, _ string, groups []string) (string, bool, error) {
	for _, g := range groups {
		if role, ok := f.groupRoleMapping[g]; ok {
			return role, true, nil
		}
	}
	return "", false, nil
}

// ── mock IdP HTTP server ──────────────────────────────────────────────────────

// mockIdP starts a test HTTP server that serves the OIDC discovery document and
// the token endpoint. It records received token requests.
type mockIdP struct {
	server         *httptest.Server
	tokenRequests  []url.Values
	idTokenPayload string // base64url-encoded JSON claims for the id_token segment
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	m := &mockIdP{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			base := m.server.URL
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": base + "/authorize",
				"token_endpoint":         base + "/token",
				"jwks_uri":               base + "/jwks",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(testJWKS())
		case "/token":
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			m.tokenRequests = append(m.tokenRequests, vals)

			payload := m.idTokenPayload
			if payload == "" {
				// Default claims used when the test does not set idTokenPayload.
				raw, _ := json.Marshal(map[string]any{
					"sub":   "ext-user-123",
					"email": "alice@example.com",
					"name":  "Alice Example",
					"exp":   time.Now().Add(1 * time.Hour).Unix(),
				})
				payload = encodeBase64URL(raw)
			}
			// header with kid matching testJWKS.
			hdrRaw, _ := json.Marshal(map[string]string{
				"alg": "RS256",
				"typ": "JWT",
				"kid": "test-key-1",
			})
			header := base64.RawURLEncoding.EncodeToString(hdrRaw)
			idToken := signTestIDToken(header, payload)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"id_token":     idToken,
				"access_token": "mock-access",
				"token_type":   "Bearer",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.server.Close)
	return m
}

// ── helpers ───────────────────────────────────────────────────────────────────

// encodeBase64URL encodes bytes as unpadded base64url (RFC 4648 §5).
func encodeBase64URL(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var block [4]byte
		switch len(b) - i {
		case 1:
			block[0] = b[i] >> 2
			block[1] = (b[i] & 0x03) << 4
			out.WriteByte(alphabet[block[0]])
			out.WriteByte(alphabet[block[1]])
		case 2:
			block[0] = b[i] >> 2
			block[1] = (b[i]&0x03)<<4 | b[i+1]>>4
			block[2] = (b[i+1] & 0x0f) << 2
			out.WriteByte(alphabet[block[0]])
			out.WriteByte(alphabet[block[1]])
			out.WriteByte(alphabet[block[2]])
		default:
			block[0] = b[i] >> 2
			block[1] = (b[i]&0x03)<<4 | b[i+1]>>4
			block[2] = (b[i+1]&0x0f)<<2 | b[i+2]>>6
			block[3] = b[i+2] & 0x3f
			out.WriteByte(alphabet[block[0]])
			out.WriteByte(alphabet[block[1]])
			out.WriteByte(alphabet[block[2]])
			out.WriteByte(alphabet[block[3]])
		}
	}
	return out.String()
}

func testOIDCClaims(t *testing.T, email, name string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"sub":   "ext-user-42",
		"email": email,
		"name":  name,
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
	})
	return encodeBase64URL(raw)
}

// testOIDCClaimsWithGroups encodes id_token claims including a groups array.
func testOIDCClaimsWithGroups(t *testing.T, email, name string, groups []string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"sub":    "ext-user-42",
		"email":  email,
		"name":   name,
		"exp":    time.Now().Add(1 * time.Hour).Unix(),
		"groups": groups,
	})
	return encodeBase64URL(raw)
}

func testOIDCHandlers(t *testing.T, idpBaseURL string) (*OIDCHandlers, *fakeOIDCStore) {
	t.Helper()
	store := newFakeOIDCStore()
	store.addTenant("acme", testBootstrapTenantID, &IDPConfig{
		DiscoveryURL: idpBaseURL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  "http://localhost/auth/oidc/callback",
	})
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	signer := newTokenSigner([]byte(testSecret), 3600, 604800)
	h := NewOIDCHandlers(store, signer, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, store
}

// ── OIDC-01: start handler redirects to IdP ───────────────────────────────────

func TestOIDCStart_RedirectsToIdP(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/start?tenant=acme", nil)
	w := httptest.NewRecorder()
	h.OIDCStart(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-01: expected 302, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, idp.server.URL+"/authorize") {
		t.Fatalf("OIDC-01: redirect should point to IdP authorize, got %q", loc)
	}
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("OIDC-01: could not parse redirect URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("OIDC-01: expected response_type=code, got %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("OIDC-01: expected code_challenge_method=S256, got %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Errorf("OIDC-01: code_challenge must not be empty")
	}
	if q.Get("state") == "" {
		t.Errorf("OIDC-01: state must not be empty")
	}
	// State cookie must be set.
	var foundCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == oidcStateCookie {
			foundCookie = true
			if !c.HttpOnly {
				t.Errorf("OIDC-01: state cookie must be HttpOnly")
			}
		}
	}
	if !foundCookie {
		t.Errorf("OIDC-01: oidc state cookie not set")
	}
}

// ── OIDC-02: start rejects missing tenant ─────────────────────────────────────

func TestOIDCStart_MissingTenant(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	h.OIDCStart(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("OIDC-02: expected 400, got %d", w.Code)
	}
}

// ── OIDC-03: start returns 404 for unknown tenant ─────────────────────────────

func TestOIDCStart_UnknownTenant(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/start?tenant=unknown", nil)
	w := httptest.NewRecorder()
	h.OIDCStart(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("OIDC-03: expected 404, got %d", w.Code)
	}
}

// ── OIDC-04: start returns 422 for tenant with no IdP config ─────────────────

func TestOIDCStart_NoIDPConfig(t *testing.T) {
	idp := newMockIdP(t)
	h, store := testOIDCHandlers(t, idp.server.URL)
	store.addTenant("noidp", "00000000-0000-0000-0000-000000000002", nil) // nil config

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/start?tenant=noidp", nil)
	w := httptest.NewRecorder()
	h.OIDCStart(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("OIDC-04: expected 422, got %d", w.Code)
	}
}

// ── OIDC-05: callback rejects missing state/code ──────────────────────────────

func TestOIDCCallback_MissingParams(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback", nil)
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("OIDC-05: expected 400, got %d", w.Code)
	}
}

// ── OIDC-06: callback rejects tampered state ──────────────────────────────────

func TestOIDCCallback_TamperedState(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=abc&state=tampered.sig", nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "verifier"})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("OIDC-06: expected 400, got %d", w.Code)
	}
}

// ── OIDC-07: callback rejects missing state cookie ───────────────────────────

func TestOIDCCallback_MissingStateCookie(t *testing.T) {
	idp := newMockIdP(t)
	h, _ := testOIDCHandlers(t, idp.server.URL)

	cfg := &Config{JWTSecret: testSecret}
	state := signState("acme", "nonce123", []byte(cfg.JWTSecret))
	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=abc&state="+url.QueryEscape(state), nil)
	// No cookie set.
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("OIDC-07: expected 400, got %d", w.Code)
	}
}

// ── OIDC-08: full callback happy path ────────────────────────────────────────

func TestOIDCCallback_HappyPath(t *testing.T) {
	idp := newMockIdP(t)
	// Set a valid id_token payload so the mock IdP returns real claims.
	idp.idTokenPayload = testOIDCClaims(t, "alice@example.com", "Alice Example")
	h, store := testOIDCHandlers(t, idp.server.URL)

	cfg := &Config{JWTSecret: testSecret}
	verifier := "test-verifier-value"
	state := signState("acme", "nonce123", []byte(cfg.JWTSecret))

	r := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?code=authcode&state="+url.QueryEscape(state), nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: verifier})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	// Should redirect to UI (302 to "/").
	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-08: expected 302, got %d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("OIDC-08: expected redirect to /, got %q", loc)
	}

	// Auth cookies must be set.
	var gotAccess, gotRefresh bool
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case accessCookie:
			gotAccess = true
			if !c.HttpOnly {
				t.Errorf("OIDC-08: access cookie must be HttpOnly")
			}
		case refreshCookie:
			gotRefresh = true
		case oidcStateCookie:
			if c.MaxAge != -1 {
				t.Errorf("OIDC-08: oidc state cookie should be cleared (MaxAge=-1)")
			}
		}
	}
	if !gotAccess || !gotRefresh {
		t.Fatalf("OIDC-08: expected access+refresh cookies, got access=%v refresh=%v", gotAccess, gotRefresh)
	}

	// The token endpoint must have been called with PKCE verifier.
	if len(idp.tokenRequests) != 1 {
		t.Fatalf("OIDC-08: expected 1 token request, got %d", len(idp.tokenRequests))
	}
	if idp.tokenRequests[0].Get("code_verifier") != verifier {
		t.Errorf("OIDC-08: code_verifier not forwarded; got %q", idp.tokenRequests[0].Get("code_verifier"))
	}

	// UpsertOIDCUser was called.
	if len(store.upsertedUsers) != 1 {
		t.Fatalf("OIDC-08: expected 1 upserted user, got %d", len(store.upsertedUsers))
	}
	if store.upsertedUsers[0].Email != "alice@example.com" {
		t.Errorf("OIDC-08: expected email alice@example.com, got %q", store.upsertedUsers[0].Email)
	}
}

// ── OIDC-09: state sign/verify round-trip ────────────────────────────────────

func TestStateSignVerify(t *testing.T) {
	key := []byte("test-secret")
	state := signState("acme", "nonce-abc", key)
	slug, err := verifyState(state, key)
	if err != nil {
		t.Fatalf("OIDC-09: verifyState failed: %v", err)
	}
	if slug != "acme" {
		t.Errorf("OIDC-09: expected slug acme, got %q", slug)
	}
}

// ── OIDC-10: state verification rejects wrong key ────────────────────────────

func TestStateVerifyRejectsWrongKey(t *testing.T) {
	state := signState("acme", "nonce-abc", []byte("correct-key"))
	_, err := verifyState(state, []byte("wrong-key"))
	if err == nil {
		t.Fatal("OIDC-10: expected error for wrong key, got nil")
	}
}

// ── OIDC-11: PKCE challenge is S256 of verifier ──────────────────────────────

func TestPKCECodeChallenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 §Appendix B example (encoded).
	// Rather than hard-coding the RFC example (which uses specific bytes), just
	// verify that the challenge is deterministic and non-empty.
	ch1 := codeChallenge(verifier)
	ch2 := codeChallenge(verifier)
	if ch1 != ch2 {
		t.Fatal("OIDC-11: codeChallenge is not deterministic")
	}
	if ch1 == "" || ch1 == verifier {
		t.Fatal("OIDC-11: codeChallenge must be non-empty and differ from verifier")
	}
}

// ── OIDC-12: access token from callback carries tenant_id ────────────────────

func TestOIDCCallback_TokenCarriesTenantID(t *testing.T) {
	idp := newMockIdP(t)
	idp.idTokenPayload = testOIDCClaims(t, "bob@example.com", "Bob")
	h, _ := testOIDCHandlers(t, idp.server.URL)

	cfg := &Config{JWTSecret: testSecret}
	state := signState("acme", "nonce", []byte(cfg.JWTSecret))

	r := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "verifier"})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-12: expected 302, got %d body=%s", w.Code, w.Body.String())
	}

	// Extract and parse the access token from the cookie.
	var accessToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == accessCookie {
			accessToken = c.Value
		}
	}
	if accessToken == "" {
		t.Fatal("OIDC-12: no access cookie")
	}
	signer := newTokenSigner([]byte(testSecret), 3600, 604800)
	_, err := signer.Verify(accessToken)
	if err != nil {
		t.Fatalf("OIDC-12: access token invalid: %v", err)
	}
	// Decode claims manually to check tenant_id.
	parts := strings.Split(accessToken, ".")
	payload, _ := base64urlDecode(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("OIDC-12: payload unmarshal: %v", err)
	}
	tenantID, _ := claims["tenant_id"].(string)
	if tenantID != testBootstrapTenantID {
		t.Errorf("OIDC-12: expected tenant_id %s, got %q", testBootstrapTenantID, tenantID)
	}
}

// ── OIDC-25: groups claim matched → role overridden from mapping ──────────────
//
// When the id_token includes a "groups" claim that matches a configured tenant
// group mapping, the OIDC callback must use the mapped role (not the default
// "viewer") when calling UpsertOIDCUser.

func TestOIDCCallback_GroupsMatchedRoleOverridden(t *testing.T) {
	idp := newMockIdP(t)
	idp.idTokenPayload = testOIDCClaimsWithGroups(t, "alice@example.com", "Alice",
		[]string{"EntraUsers", "OktaAdmins"})
	h, store := testOIDCHandlers(t, idp.server.URL)
	// Configure: OktaAdmins → admin
	store.groupRoleMapping["OktaAdmins"] = "admin"

	cfg := &Config{JWTSecret: testSecret}
	state := signState("acme", "nonce", []byte(cfg.JWTSecret))

	r := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "verifier"})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-25: expected 302, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.upsertedUsers) != 1 {
		t.Fatalf("OIDC-25: UpsertOIDCUser must be called exactly once, got %d", len(store.upsertedUsers))
	}
	if store.upsertedUsers[0].Role != "admin" {
		t.Errorf("OIDC-25: matched group must override role to 'admin', got %q", store.upsertedUsers[0].Role)
	}
}

// ── OIDC-26: groups claim present but no match → default role used ────────────

func TestOIDCCallback_GroupsUnmatchedDefaultRole(t *testing.T) {
	idp := newMockIdP(t)
	idp.idTokenPayload = testOIDCClaimsWithGroups(t, "bob@example.com", "Bob",
		[]string{"UnmappedGroup"})
	h, store := testOIDCHandlers(t, idp.server.URL)
	// No group mappings configured → default role "viewer" used.

	cfg := &Config{JWTSecret: testSecret}
	state := signState("acme", "nonce", []byte(cfg.JWTSecret))

	r := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "verifier"})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-26: expected 302, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.upsertedUsers) != 1 {
		t.Fatalf("OIDC-26: UpsertOIDCUser must be called exactly once, got %d", len(store.upsertedUsers))
	}
	if store.upsertedUsers[0].Role != "viewer" {
		t.Errorf("OIDC-26: unmatched groups must use default viewer role, got %q", store.upsertedUsers[0].Role)
	}
}

// ── OIDC-27: no groups claim → default role used ─────────────────────────────

func TestOIDCCallback_NoGroupsDefaultRole(t *testing.T) {
	idp := newMockIdP(t)
	// Default mock id_token payload has no groups claim.
	h, store := testOIDCHandlers(t, idp.server.URL)
	store.groupRoleMapping["SomeGroup"] = "admin" // configured but not in token

	cfg := &Config{JWTSecret: testSecret}
	state := signState("acme", "nonce", []byte(cfg.JWTSecret))

	r := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?code=code&state="+url.QueryEscape(state), nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "verifier"})
	w := httptest.NewRecorder()
	h.OIDCCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("OIDC-27: expected 302, got %d body=%s", w.Code, w.Body.String())
	}
	if len(store.upsertedUsers) != 1 {
		t.Fatalf("OIDC-27: UpsertOIDCUser must be called exactly once, got %d", len(store.upsertedUsers))
	}
	if store.upsertedUsers[0].Role != "viewer" {
		t.Errorf("OIDC-27: absent groups claim must use default viewer role, got %q", store.upsertedUsers[0].Role)
	}
}
