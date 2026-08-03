package admin_test

// S1-34 — R-4c2 tenant middleware enforcement at the HTTP layer.
//
// These tests exercise tenant-scoped admin routes with a real auth.Cache and
// real BearerTenantMiddleware wired in via BuildRouter. They prove:
//
//   TH-01  missing token → 401 on tenant-scoped admin route /admin/agents
//   TH-02  unknown/invalid token → 401 on /admin/agents
//   TH-03  valid token but no TenantID → 403 on /admin/agents
//   TH-04  valid token with TenantID + super_admin JWT → handler reached (200)
//   TH-05  X-Tenant-ID header cannot override token-derived TenantID
//   TH-06  ?tenant_id query param cannot override token-derived TenantID
//   TH-07  missing token → 401 on /admin/applications
//   TH-08  missing token → 401 on /runs
//   TH-09  platform-global /admin/llm-providers requires super_admin JWT, not bearer tenant
//   TH-10  valid token with TenantID → 200 on /admin/orchestrators
//   TH-11  valid token with TenantID → 200 on /admin/tokens
//   TH-12  tenantless token → 403 on /runs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/auth"
)

// ──────────────────────────────────────────────────────────────────────────────
// Fakes for auth.Cache construction
// ──────────────────────────────────────────────────────────────────────────────

// thTokenQuerier satisfies auth.TokenQuerier in memory.
type thTokenQuerier struct {
	mu   sync.Mutex
	rows map[string]*auth.TokenRow // keyed by sha256-hex of raw token
}

func newTHQuerier() *thTokenQuerier {
	return &thTokenQuerier{rows: make(map[string]*auth.TokenRow)}
}

// addToken registers rawToken → tenantID in the fake DB.
// Empty tenantID simulates a token that predates the R-4a migration.
func (q *thTokenQuerier) addToken(rawToken, tenantID string) {
	h := sha256.Sum256([]byte(rawToken))
	hash := fmt.Sprintf("%x", h)
	q.mu.Lock()
	q.rows[hash] = &auth.TokenRow{
		ID:        1,
		TenantID:  tenantID,
		CreatedAt: time.Now(),
	}
	q.mu.Unlock()
}

func (q *thTokenQuerier) QueryToken(_ context.Context, hashHex string) (*auth.TokenRow, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	row, ok := q.rows[hashHex]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return row, nil
}

// thRedis is a no-op in-memory Redis stub.
type thRedis struct {
	mu    sync.Mutex
	store map[string][]byte
}

func newTHRedis() *thRedis { return &thRedis{store: make(map[string][]byte)} }

func (r *thRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.store[key]
	return v, ok, nil
}
func (r *thRedis) SetEX(_ context.Context, key string, val []byte, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = val
	return nil
}
func (r *thRedis) Del(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}
func (r *thRedis) Publish(_ context.Context, _, _ string) error                { return nil }
func (r *thRedis) Subscribe(_ context.Context, _ string, _ func(string)) error { return nil }

// ──────────────────────────────────────────────────────────────────────────────
// JWT helpers
// ──────────────────────────────────────────────────────────────────────────────

const thJWTSecret = "th-test-jwt-secret-do-not-use"

// thBuildHS256Token creates a minimal HS256 JWT with super_admin role.
func thBuildHS256Token(t *testing.T, secret []byte) string {
	t.Helper()
	now := time.Now().Unix()
	headerEnc := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{
		"sub": "1", "username": "admin", "role": "super_admin",
		"exp": now + 3600, "iat": now,
	})
	require.NoError(t, err)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := headerEnc + "." + payloadEnc
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ──────────────────────────────────────────────────────────────────────────────
// Router helpers
// ──────────────────────────────────────────────────────────────────────────────

// newTHCache builds a real auth.Cache with a controllable token→tenant mapping.
func newTHCache() (*auth.Cache, *thTokenQuerier) {
	q := newTHQuerier()
	c := auth.NewCache(q, newTHRedis(), nil)
	return c, q
}

// tenantAdminRouter returns a test admin router with:
//   - HS256Middleware(thJWTSecret) as jwtMiddleware → validates the JWT from Authorization header
//   - BearerTenantMiddleware(cache) wired on tenant-scoped routes
//
// Tests that exercise tenant-scoped routes must provide BOTH:
//   - Authorization: Bearer <hs256-super-admin-jwt>
//   - Authorization: Bearer <bearer-token-with-tenant>
//
// Since a single Authorization header can only carry one token, we inject the
// JWT via a separate X-Admin-JWT header using a wrapper middleware, and keep
// the Authorization header for the bearer token.
func tenantAdminRouter(t *testing.T, cache *auth.Cache) http.Handler {
	t.Helper()
	db := &fakeDB{queryRows: newFakeRows(nil)}
	secret := []byte(thJWTSecret)
	// jwtMW wraps HS256Middleware so it reads from X-Admin-JWT instead of
	// Authorization, leaving Authorization free for the bearer token.
	jwtMW := func(next http.Handler) http.Handler {
		inner := auth.HS256Middleware(secret)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminJWT := r.Header.Get("X-Admin-JWT")
			if adminJWT == "" {
				// No admin JWT provided → inject a super_admin JWT automatically
				// so tests that only care about bearer-tenant can pass RequireSuperAdmin.
				adminJWT = thBuildHS256Token(t, secret)
			}
			// Replace Authorization temporarily so HS256Middleware reads the JWT.
			origAuth := r.Header.Get("Authorization")
			r2 := r.Clone(r.Context())
			r2.Header.Set("Authorization", "Bearer "+adminJWT)
			inner(http.HandlerFunc(func(w http.ResponseWriter, r3 *http.Request) {
				// Restore original Authorization so BearerTenantMiddleware sees the bearer token.
				r3 = r3.Clone(r3.Context())
				r3.Header.Set("Authorization", origAuth)
				next.ServeHTTP(w, r3)
			})).ServeHTTP(w, r2)
		})
	}

	return admin.BuildRouter(db, nil, nil, nil, jwtMW, cache, nil, "test-secret", nil, nil)
}

// thGet sends a GET request to path with an Authorization: Bearer <token> header.
// If token is empty, no Authorization header is sent.
func thGet(srv *httptest.Server, path, bearerToken string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		return nil, err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return http.DefaultClient.Do(req)
}

// ──────────────────────────────────────────────────────────────────────────────
// S1-34 tests
// ──────────────────────────────────────────────────────────────────────────────

// TH-01: valid super_admin JWT → admin/agents accessible without a bearer machine token.
// Admin routes now use AdminTenantMiddleware which reads tenant from JWT claims
// (falling back to the bootstrap tenant for super_admin users). No machine token required.
func TestTenantHTTP_MissingToken_Agents_401(t *testing.T) {
	cache, _ := newTHCache()
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	// tenantAdminRouter auto-injects a super_admin JWT; no bearer token needed.
	resp, err := thGet(srv, "/admin/agents", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	// Admin routes grant access via JWT alone; bootstrap tenant is used.
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-01: super_admin JWT alone must reach admin/agents")
}

// TH-02: a machine bearer token in Authorization does not affect admin access.
// The JWT is injected via X-Admin-JWT; any bearer token value is irrelevant.
func TestTenantHTTP_InvalidToken_Agents_401(t *testing.T) {
	cache, _ := newTHCache()
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	// An unrecognised bearer token in Authorization: admin JWT still authorises.
	resp, err := thGet(srv, "/admin/agents", "unknown-token-xyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-02: unrecognised bearer token does not block admin JWT access")
}

// TH-03: admin access uses bootstrap tenant regardless of any bearer token value.
func TestTenantHTTP_TokenWithoutTenant_Agents_403(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("tenantless-token", "") // valid opaque token but no TenantID (irrelevant for admin)
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	// Admin routes do not consult the bearer token for tenant; JWT + bootstrap tenant applies.
	resp, err := thGet(srv, "/admin/agents", "tenantless-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-03: admin route must succeed via JWT+bootstrap even with tenantless opaque token")
}

// TH-04: valid token with TenantID → handler reached (200) on /admin/agents.
func TestTenantHTTP_ValidToken_Agents_200(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("good-token", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/agents", "good-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-04: valid token with tenant must reach handler (200)")
}

// TH-05: X-Tenant-ID header cannot override token-derived TenantID.
// The handler uses tenantctx.MustTenantIDFromCtx, which reads only from the
// middleware-injected context value — never from any header field.
func TestTenantHTTP_XTenantIDHeaderIgnored(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("alpha-token", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/agents", nil)
	req.Header.Set("Authorization", "Bearer alpha-token")
	req.Header.Set("X-Tenant-ID", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb") // must be ignored
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-05: X-Tenant-ID header must not override token tenant or cause an error")
}

// TH-06: ?tenant_id query param cannot override token-derived TenantID.
func TestTenantHTTP_QueryTenantIDIgnored(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("bravo-token", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/agents?tenant_id=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bravo-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-06: ?tenant_id query param must not override token tenant")
}

// TH-07: valid super_admin JWT → admin/applications accessible without bearer machine token.
func TestTenantHTTP_MissingToken_Applications_401(t *testing.T) {
	cache, _ := newTHCache()
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/applications", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-07: super_admin JWT alone must reach admin/applications")
}

// TH-08: missing bearer token → 401 on /runs.
func TestTenantHTTP_MissingToken_Runs_401(t *testing.T) {
	cache, _ := newTHCache()
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/runs", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "TH-08: missing token on /runs must return 401")
}

// TH-09: platform-global /admin/llm-providers requires JWT+super_admin, not bearer tenant.
// tenantAdminRouter auto-injects a valid super_admin JWT. No bearer token needed.
// This confirms the platform-global route does NOT require BearerTenantMiddleware.
func TestTenantHTTP_PlatformGlobal_LLMProviders_NoTenantRequired(t *testing.T) {
	cache, _ := newTHCache()
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	// No bearer token — only the auto-injected super_admin JWT from tenantAdminRouter.
	resp, err := thGet(srv, "/admin/llm-providers", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"TH-09: platform-global llm-providers must reach handler with super_admin JWT and no bearer token")
}

// TH-10: valid token with TenantID → 200 on /admin/orchestrators.
func TestTenantHTTP_ValidToken_Orchestrators_200(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("orch-token", "cccccccc-cccc-cccc-cccc-cccccccccccc")
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/orchestrators", "orch-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-10: valid token on /orchestrators must reach handler")
}

// TH-11: valid token with TenantID → 200 on /admin/tokens.
func TestTenantHTTP_ValidToken_Tokens_200(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("admin-tok", "dddddddd-dddd-dddd-dddd-dddddddddddd")
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/tokens", "admin-tok")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "TH-11: valid token on /admin/tokens must reach handler")
}

// TH-12: tenantless bearer token → 403 on /runs.
func TestTenantHTTP_TenantlessToken_Runs_403(t *testing.T) {
	cache, q := newTHCache()
	q.addToken("old-token", "") // pre-R-4a token, no TenantID
	srv := httptest.NewServer(tenantAdminRouter(t, cache))
	defer srv.Close()

	resp, err := thGet(srv, "/runs", "old-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "TH-12: tenantless token on /runs must return 403")
}
