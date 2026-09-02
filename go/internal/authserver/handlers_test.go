package authserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRouter(t *testing.T) (http.Handler, *fakeStore) {
	t.Helper()
	svc, store := testService(t)
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	h := NewHandlers(svc, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewRouter(h, nil, store, "test"), store
}

func do(t *testing.T, router http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func cookieFrom(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestHTTPLoginSetsCookiesAndBody(t *testing.T) {
	router, _ := testRouter(t)
	w := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out tokenPairResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" || out.ExpiresIn != 3600 {
		t.Fatalf("bad body: %+v", out)
	}
	resp := w.Result()
	if cookieFrom(resp, accessCookie) == nil || cookieFrom(resp, refreshCookie) == nil {
		t.Fatal("login must set both auth cookies")
	}
	ac := cookieFrom(resp, accessCookie)
	if !ac.HttpOnly {
		t.Fatal("access cookie must be HttpOnly")
	}
}

func TestHTTPLoginBadPassword401(t *testing.T) {
	router, _ := testRouter(t)
	w := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
	var e errBody
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.Detail == "" {
		t.Fatal("expected {detail:...} error body")
	}
}

func TestHTTPMeFlow(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	ac := cookieFrom(lw.Result(), accessCookie)

	// /me with the cookie
	mw := do(t, router, http.MethodGet, "/api/v1/auth/me", "", ac)
	if mw.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", mw.Code, mw.Body.String())
	}
	var me meResponse
	if err := json.Unmarshal(mw.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Username != "admin" || me.Email != "admin@them.local" || me.Role != "super_admin" {
		t.Fatalf("unexpected me: %+v", me)
	}
}

func TestHTTPMeNoCookie401(t *testing.T) {
	router, _ := testRouter(t)
	w := do(t, router, http.MethodGet, "/api/v1/auth/me", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHTTPRefreshFlow(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	rc := cookieFrom(lw.Result(), refreshCookie)
	w := do(t, router, http.MethodPost, "/api/v1/auth/refresh", "", rc)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", w.Code, w.Body.String())
	}
	var out tokenPairResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.AccessToken == "" {
		t.Fatal("refresh returned no access token")
	}
}

func TestHTTPLogoutRevokes(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	ac := cookieFrom(lw.Result(), accessCookie)

	ow := do(t, router, http.MethodPost, "/api/v1/auth/logout", "", ac)
	if ow.Code != http.StatusOK {
		t.Fatalf("logout status = %d", ow.Code)
	}
	// logout must clear cookies (MaxAge < 0)
	if c := cookieFrom(ow.Result(), accessCookie); c == nil || c.MaxAge >= 0 {
		t.Fatal("logout must expire the access cookie")
	}
	// subsequent /me with the old token must be rejected (revoked)
	mw := do(t, router, http.MethodGet, "/api/v1/auth/me", "", ac)
	if mw.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, want 401", mw.Code)
	}
}

func TestHTTPVerify(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	var lp tokenPairResponse
	_ = json.Unmarshal(lw.Body.Bytes(), &lp)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", nil)
	r.Header.Set("Authorization", "Bearer "+lp.AccessToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d", w.Code)
	}
	var v verifyResponse
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.UserID != 1 || v.Role != "super_admin" || v.Sub != "1" {
		t.Fatalf("unexpected verify: %+v", v)
	}
}

func TestHTTPValidateHeaders(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	var lp tokenPairResponse
	_ = json.Unmarshal(lw.Body.Bytes(), &lp)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/validate", nil)
	r.Header.Set("Authorization", "Bearer "+lp.AccessToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("validate status = %d", w.Code)
	}
	if w.Header().Get("X-User-Id") != "1" || w.Header().Get("X-User-Role") != "super_admin" {
		t.Fatalf("missing forwardAuth headers: %v", w.Header())
	}
}

// TestHTTPAuthMirror confirms the Traefik /auth/* mirror also serves login.
func TestHTTPAuthMirror(t *testing.T) {
	router, _ := testRouter(t)
	w := do(t, router, http.MethodPost, "/auth/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("mirror login status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestHTTPHealth(t *testing.T) {
	router, _ := testRouter(t)
	for _, p := range []string{"/health", "/health/live", "/health/ready"} {
		w := do(t, router, http.MethodGet, p, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", p, w.Code)
		}
	}
}

func TestHTTPHealthReadyDBDown(t *testing.T) {
	router, store := testRouter(t)
	store.failPing = true
	w := do(t, router, http.MethodGet, "/health/ready", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", w.Code)
	}
}

// TestHTTPMeReturnsTenantID verifies that GET /auth/me returns the tenant_id
// from the access token claims. This is Step 16 RBAC: the UI can display
// which tenant the current session belongs to without a separate DB query.
func TestHTTPMeReturnsTenantID(t *testing.T) {
	router, _ := testRouter(t)
	lw := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`)
	ac := cookieFrom(lw.Result(), accessCookie)

	mw := do(t, router, http.MethodGet, "/api/v1/auth/me", "", ac)
	if mw.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", mw.Code, mw.Body.String())
	}
	var me meResponse
	if err := json.Unmarshal(mw.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.TenantID != testBootstrapTenantID {
		t.Fatalf("me.tenant_id = %q, want %q", me.TenantID, testBootstrapTenantID)
	}
}

// TestHTTPLoginWithTenantSlug verifies that supplying a matching tenant_slug
// in the login body selects the correct membership. An unknown slug returns 403.
func TestHTTPLoginWithTenantSlug(t *testing.T) {
	router, _ := testRouter(t)

	// Known slug → success
	w := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123","tenant_slug":"default"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login with valid tenant_slug: status = %d, body = %s", w.Code, w.Body.String())
	}

	// Unknown slug → 403 (no membership for that tenant)
	w2 := do(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123","tenant_slug":"nonexistent"}`)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("login with unknown tenant_slug: status = %d, want 403", w2.Code)
	}
}

// OIDC-21: TenantLookup returns 200 with slug, display_name, idp_configured when domain matches.
func TestHTTPTenantLookup_Found(t *testing.T) {
	router, store := testRouter(t)
	store.domainLookup["acme.com"] = TenantLookupResult{
		Slug:          "acme",
		DisplayName:   "Acme Corp",
		IDPConfigured: true,
	}

	w := do(t, router, http.MethodGet, "/api/v1/auth/tenant-lookup?email=alice%40acme.com", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out TenantLookupResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Slug != "acme" || out.DisplayName != "Acme Corp" || !out.IDPConfigured {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// OIDC-22: TenantLookup returns 404 when domain has no tenant.
func TestHTTPTenantLookup_NotFound(t *testing.T) {
	router, _ := testRouter(t)

	w := do(t, router, http.MethodGet, "/api/v1/auth/tenant-lookup?email=unknown%40unknown.org", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// OIDC-23: TenantLookup returns 400 when email query param is missing.
func TestHTTPTenantLookup_MissingEmail(t *testing.T) {
	router, _ := testRouter(t)

	w := do(t, router, http.MethodGet, "/api/v1/auth/tenant-lookup", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// OIDC-24: TenantLookup returns 400 when email has no @ symbol.
func TestHTTPTenantLookup_InvalidEmail(t *testing.T) {
	router, _ := testRouter(t)

	w := do(t, router, http.MethodGet, "/api/v1/auth/tenant-lookup?email=notanemail", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
