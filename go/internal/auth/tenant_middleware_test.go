package auth_test

// R-4b: Tenant identity middleware tests.
//
// Tests cover:
//   TM-01  valid bearer token with TenantID → 200 + tenant in context
//   TM-02  missing token → 401
//   TM-03  invalid/unknown token → 401
//   TM-04  valid token without TenantID → 403
//   TM-05  empty string TenantID stored in DB → 403
//   TM-06  arbitrary X-Tenant-ID header cannot override TenantID
//   TM-07  two tenants resolve independently (no cross-contamination)
//   TM-08  HS256 JWT with TenantID → 200 + tenant in context
//   TM-09  HS256 JWT without TenantID claim → 403
//   TM-10  missing token on HS256TenantMiddleware → 401
//   TM-11  no secret value appears in error responses
//   TM-12  RS256 JWT TenantID round-trip via ValidateJWT
//   TM-13  JWT without tenant_id claim returns empty TenantID
//   TM-14  HS256 TenantID round-trip via ValidateHS256JWT
//   TM-15  TenantID flows through token cache (L1 miss → DB → L1)

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// Constants
// ──────────────────────────────────────────────────────────────────────────────

const (
	tenantAlpha = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantBravo = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// ──────────────────────────────────────────────────────────────────────────────
// Fakes
// ──────────────────────────────────────────────────────────────────────────────

// tmTokenQuerier fakes them.access_tokens with TenantID support.
type tmTokenQuerier struct {
	mu   sync.Mutex
	rows map[string]*auth.TokenRow
}

func newTMQuerier() *tmTokenQuerier {
	return &tmTokenQuerier{rows: make(map[string]*auth.TokenRow)}
}

func (q *tmTokenQuerier) addRaw(rawToken, tenantID string) {
	h := sha256.Sum256([]byte(rawToken))
	hash := fmt.Sprintf("%x", h)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows[hash] = &auth.TokenRow{
		ID:        1,
		TenantID:  tenantID,
		CreatedAt: time.Now(),
	}
}

func (q *tmTokenQuerier) QueryToken(_ context.Context, hashHex string) (*auth.TokenRow, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	row, ok := q.rows[hashHex]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return row, nil
}

// tmRedis is an in-process Redis stub (no network).
type tmRedis struct {
	mu    sync.Mutex
	store map[string][]byte
}

func newTMRedis() *tmRedis { return &tmRedis{store: make(map[string][]byte)} }

func (r *tmRedis) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.store[key]
	return v, ok, nil
}
func (r *tmRedis) SetEX(_ context.Context, key string, val []byte, _ time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = val
	return nil
}
func (r *tmRedis) Del(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, key)
	return nil
}
func (r *tmRedis) Publish(_ context.Context, _, _ string) error            { return nil }
func (r *tmRedis) Subscribe(_ context.Context, _ string, _ func(string)) error { return nil }

// tenantCapture records the tenant ID placed in context by middleware.
type tenantCapture struct {
	tenantID string
}

func (c *tenantCapture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := tenantctx.TenantIDFromCtx(r.Context()); err == nil {
			c.tenantID = id
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func makeCache(q auth.TokenQuerier) *auth.Cache {
	return auth.NewCache(q, newTMRedis(), nil)
}

func withBearer(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func responseBody(w *httptest.ResponseRecorder) string { return w.Body.String() }

// ──────────────────────────────────────────────────────────────────────────────
// TM-01 valid bearer token with TenantID → 200 + tenant in context
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_ValidToken(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("token-alpha", tenantAlpha)
	cap := &tenantCapture{}
	w := httptest.NewRecorder()
	auth.BearerTenantMiddleware(makeCache(q))(cap.handler()).ServeHTTP(w, withBearer("token-alpha"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, tenantAlpha, cap.tenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-02 missing token → 401
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_MissingToken(t *testing.T) {
	cache := makeCache(newTMQuerier())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil) // no Authorization header
	auth.BearerTenantMiddleware(cache)(mustNotCallHandler(t)).ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-03 unknown/invalid token → 401
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_InvalidToken(t *testing.T) {
	cache := makeCache(newTMQuerier()) // empty — no tokens registered
	w := httptest.NewRecorder()
	auth.BearerTenantMiddleware(cache)(mustNotCallHandler(t)).ServeHTTP(w, withBearer("unknown-token"))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-04 valid token without TenantID → 403
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_TokenWithoutTenant(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("no-tenant-token", "") // empty TenantID
	w := httptest.NewRecorder()
	auth.BearerTenantMiddleware(makeCache(q))(mustNotCallHandler(t)).ServeHTTP(w, withBearer("no-tenant-token"))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-05 empty string TenantID stored → 403
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_EmptyTenantID(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("empty-tenant-token", "")
	w := httptest.NewRecorder()
	auth.BearerTenantMiddleware(makeCache(q))(mustNotCallHandler(t)).ServeHTTP(w, withBearer("empty-tenant-token"))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-06 X-Tenant-ID header cannot override TenantID
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_HeaderCannotOverride(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("real-alpha-token", tenantAlpha)
	cap := &tenantCapture{}

	r := withBearer("real-alpha-token")
	// Attacker-controlled headers attempting to claim bravo tenant.
	r.Header.Set("X-Tenant-ID", tenantBravo)
	r.Header.Set("Tenant-ID", tenantBravo)
	r.Header.Set("tenant_id", tenantBravo)

	w := httptest.NewRecorder()
	auth.BearerTenantMiddleware(makeCache(q))(cap.handler()).ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, tenantAlpha, cap.tenantID,
		"tenant must come from the token, not from request headers")
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-07 two tenants resolve independently
// ──────────────────────────────────────────────────────────────────────────────

func TestBearerTenant_TwoTenantsIndependent(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("token-for-alpha", tenantAlpha)
	q.addRaw("token-for-bravo", tenantBravo)
	cache := makeCache(q)
	mw := auth.BearerTenantMiddleware(cache)

	capA := &tenantCapture{}
	wA := httptest.NewRecorder()
	mw(capA.handler()).ServeHTTP(wA, withBearer("token-for-alpha"))
	assert.Equal(t, http.StatusOK, wA.Code)
	assert.Equal(t, tenantAlpha, capA.tenantID)

	capB := &tenantCapture{}
	wB := httptest.NewRecorder()
	mw(capB.handler()).ServeHTTP(wB, withBearer("token-for-bravo"))
	assert.Equal(t, http.StatusOK, wB.Code)
	assert.Equal(t, tenantBravo, capB.tenantID)

	assert.NotEqual(t, capA.tenantID, capB.tenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-08 HS256 JWT with TenantID → 200 + tenant in context
// ──────────────────────────────────────────────────────────────────────────────

func TestHS256Tenant_ValidJWTWithTenant(t *testing.T) {
	secret := []byte("hs256-test-secret")
	now := time.Now().Unix()
	token := buildHS256Token(t, map[string]any{
		"sub": "7", "username": "alice", "role": "user",
		"tenant_id": tenantAlpha,
		"exp": now + 3600, "iat": now,
	}, secret)

	cap := &tenantCapture{}
	w := httptest.NewRecorder()
	auth.HS256TenantMiddleware(secret)(cap.handler()).ServeHTTP(w, withBearer(token))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, tenantAlpha, cap.tenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-09 HS256 JWT without tenant_id claim → 403
// ──────────────────────────────────────────────────────────────────────────────

func TestHS256Tenant_JWTWithoutTenant(t *testing.T) {
	secret := []byte("hs256-test-secret")
	now := time.Now().Unix()
	token := buildHS256Token(t, map[string]any{
		"sub": "7", "username": "alice", "role": "user",
		// no tenant_id
		"exp": now + 3600, "iat": now,
	}, secret)

	w := httptest.NewRecorder()
	auth.HS256TenantMiddleware(secret)(mustNotCallHandler(t)).ServeHTTP(w, withBearer(token))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-10 missing token on HS256TenantMiddleware → 401
// ──────────────────────────────────────────────────────────────────────────────

func TestHS256Tenant_MissingToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	auth.HS256TenantMiddleware([]byte("secret"))(mustNotCallHandler(t)).ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-11 no secret value in error responses
// ──────────────────────────────────────────────────────────────────────────────

func TestTenantMiddleware_NoSecretInErrors(t *testing.T) {
	const secretVal = "super-sensitive-signing-key"
	mw := auth.HS256TenantMiddleware([]byte(secretVal))

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).
		ServeHTTP(w, withBearer("garbage-token"))

	body := responseBody(w)
	assert.NotContains(t, body, secretVal, "secret key must not appear in error response")

	// Verify response is JSON with an error field, not raw error messages.
	var resp map[string]string
	if err := json.Unmarshal([]byte(body), &resp); err == nil {
		msg := resp["error"]
		assert.NotContains(t, msg, secretVal)
		assert.NotEmpty(t, msg, "error field must be non-empty")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-12 RS256 JWT TenantID round-trip
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateJWT_TenantIDRoundTrip(t *testing.T) {
	claims := auth.Claims{
		UserID:    7,
		Username:  "bob",
		Roles:     []string{"user"},
		TenantID:  tenantAlpha,
		ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	}
	token, err := auth.IssueJWT(claims, testPrivKey)
	require.NoError(t, err)

	got, err := auth.ValidateJWT(token, testPubKey)
	require.NoError(t, err)
	assert.Equal(t, tenantAlpha, got.TenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-13 JWT without tenant_id returns empty TenantID (not an error)
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateJWT_NoTenantID(t *testing.T) {
	claims := auth.Claims{
		UserID:    8,
		Username:  "carol",
		Roles:     []string{"user"},
		ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	}
	token, err := auth.IssueJWT(claims, testPrivKey)
	require.NoError(t, err)

	got, err := auth.ValidateJWT(token, testPubKey)
	require.NoError(t, err)
	assert.Empty(t, got.TenantID,
		"JWT without tenant_id claim must return empty TenantID, not an error")
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-14 HS256 TenantID round-trip
// ──────────────────────────────────────────────────────────────────────────────

func TestValidateHS256JWT_TenantIDRoundTrip(t *testing.T) {
	secret := []byte("round-trip-secret-hs256")
	now := time.Now().Unix()
	token := buildHS256Token(t, map[string]any{
		"sub": "99", "username": "dave", "role": "user",
		"tenant_id": tenantBravo,
		"exp": now + 3600, "iat": now,
	}, secret)

	claims, err := auth.ValidateHS256JWT(token, secret)
	require.NoError(t, err)
	assert.Equal(t, tenantBravo, claims.TenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// TM-15 TenantID flows through token cache (L1 miss → DB → L1)
// ──────────────────────────────────────────────────────────────────────────────

func TestTokenCache_TenantIDFlows(t *testing.T) {
	q := newTMQuerier()
	q.addRaw("cached-token", tenantAlpha)
	cache := makeCache(q)

	// First call: DB hit, should populate L1.
	info, err := cache.Validate(context.Background(), "cached-token")
	require.NoError(t, err)
	assert.Equal(t, tenantAlpha, info.TenantID)

	// Second call: L1 hit (same TenantID from cache).
	info2, err := cache.Validate(context.Background(), "cached-token")
	require.NoError(t, err)
	assert.Equal(t, tenantAlpha, info2.TenantID)
}

// ──────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────────────────

func mustNotCallHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next handler must not be called")
	})
}
