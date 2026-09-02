package authserver

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Handlers adapts the Service to HTTP. It owns request parsing, cookie handling,
// and JSON response shapes — all kept byte-compatible with the Python auth
// service so the existing Next.js proxy routes need no changes.
type Handlers struct {
	svc *Service
	cfg *Config
	log *slog.Logger
}

// NewHandlers builds the HTTP handler set.
func NewHandlers(svc *Service, cfg *Config, log *slog.Logger) *Handlers {
	return &Handlers{svc: svc, cfg: cfg, log: log}
}

const (
	accessCookie  = "them_access_token"
	refreshCookie = "them_refresh_token"
)

// ── request/response bodies (JSON field names match Python) ──────────────────

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

type tokenPairResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type meResponse struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type verifyResponse struct {
	Sub      string `json:"sub"`
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Email    string `json:"email"`
}

// errBody mirrors FastAPI's {"detail": "..."} error shape.
type errBody struct {
	Detail string `json:"detail"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

// Login handles POST /api/v1/auth/login.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pair, err := h.svc.Login(r.Context(), LoginInput{
		Username: req.Username, Password: req.Password, APIKey: req.APIKey,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	h.setAuthCookies(w, pair)
	writeJSON(w, http.StatusOK, tokenPairResponse{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresIn: pair.ExpiresIn,
	})
}

// Me handles GET /api/v1/auth/me — reads the access token from the cookie.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, accessCookie)
	user, err := h.svc.Me(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		ID: user.ID, Email: user.Email, Name: user.Name, Username: user.Username, Role: user.Role,
	})
}

// Refresh handles POST /api/v1/auth/refresh — accepts Bearer header or cookie.
func (h *Handlers) Refresh(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		token = cookieValue(r, refreshCookie)
	}
	pair, err := h.svc.Refresh(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.setAuthCookies(w, pair)
	writeJSON(w, http.StatusOK, tokenPairResponse{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, ExpiresIn: pair.ExpiresIn,
	})
}

// Logout handles POST /api/v1/auth/logout — accepts Bearer header or cookie.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		token = cookieValue(r, accessCookie)
	}
	h.svc.Logout(r.Context(), token)
	h.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

// Verify handles POST /api/v1/auth/verify — Bearer access token; service-to-service.
func (h *Handlers) Verify(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "Authorization header required")
		return
	}
	user, err := h.svc.Verify(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, verifyResponse{
		Sub: itoa(user.ID), UserID: user.ID, Username: user.Username,
		Name: user.Name, Role: user.Role, Email: user.Email,
	})
}

// GetPreferences handles GET /api/v1/auth/me/preferences.
func (h *Handlers) GetPreferences(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, accessCookie)
	raw, err := h.svc.GetPreferences(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// SetPreferences handles PUT /api/v1/auth/me/preferences.
func (h *Handlers) SetPreferences(w http.ResponseWriter, r *http.Request) {
	token := cookieValue(r, accessCookie)
	if r.ContentLength > maxPrefsBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "preferences payload too large")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPrefsBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if !json.Valid(raw) {
		writeErr(w, http.StatusBadRequest, "preferences must be a valid JSON object")
		return
	}
	if err := h.svc.SetPreferences(r.Context(), token, raw); err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// Validate handles GET /api/v1/auth/validate — Traefik forwardAuth parity. On
// success returns 200 with X-User-* headers.
func (h *Handlers) Validate(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "Authorization header required")
		return
	}
	user, err := h.svc.Verify(r.Context(), token)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.Header().Set("X-User-Id", itoa(user.ID))
	w.Header().Set("X-User-Username", user.Username)
	w.Header().Set("X-User-Role", user.Role)
	w.WriteHeader(http.StatusOK)
}

// ── cookie helpers ───────────────────────────────────────────────────────────

func (h *Handlers) setAuthCookies(w http.ResponseWriter, pair *TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name: accessCookie, Value: pair.AccessToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: pair.ExpiresIn,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: pair.RefreshToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: h.cfg.RefreshTokenExpiry,
	})
}

func (h *Handlers) clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// ── shared error mapping ─────────────────────────────────────────────────────

// writeServiceError maps every service sentinel to an HTTP status + detail. It
// MUST cover every error a service method it fronts can return.
func (h *Handlers) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeErr(w, http.StatusUnauthorized, "Invalid email or password")
	case errors.Is(err, ErrMissingCredentials):
		writeErr(w, http.StatusBadRequest, "Either api_key or username+password required")
	case errors.Is(err, ErrDashboardAccessDenied):
		writeErr(w, http.StatusForbidden, "Your role does not have access to the dashboard")
	case errors.Is(err, ErrNotAuthenticated):
		writeErr(w, http.StatusUnauthorized, "Not authenticated")
	case errors.Is(err, ErrTokenRevoked):
		writeErr(w, http.StatusUnauthorized, "Token has been revoked")
	case errors.Is(err, ErrWrongTokenType):
		writeErr(w, http.StatusUnauthorized, "Invalid token type")
	case errors.Is(err, ErrPreferencesTooLarge):
		writeErr(w, http.StatusBadRequest, "preferences payload too large")
	case errors.Is(err, ErrNoTenantMembership):
		writeErr(w, http.StatusForbidden, "User has no tenant membership; contact your administrator")
	default:
		// DB or unexpected error — never leak the underlying message.
		h.log.Error("auth service error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}

// ── low-level helpers ────────────────────────────────────────────────────────

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, detail string) {
	writeJSON(w, code, errBody{Detail: detail})
}

func itoa(n int64) string {
	// Small dependency-free int64→string (avoids importing strconv twice over).
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
