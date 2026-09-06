package authserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── in-memory store for user management tests ─────────────────────────────────

type fakeUserStore struct {
	fakeStore                // embed for non-management Store methods
	users    map[int64]*ManagedUser
	nextID   int64
	tenants  []TenantSummary
}

func newFakeUserStore() *fakeUserStore {
	s := &fakeUserStore{
		fakeStore: *newFakeStore(),
		users:     map[int64]*ManagedUser{},
		nextID:    1,
		tenants: []TenantSummary{
			{ID: "00000000-0000-0000-0000-000000000001", Slug: "default", DisplayName: "Default"},
		},
	}
	// Seed a super_admin user so requireSuperAdmin can issue a real token.
	s.fakeStore.addUser(&userRecord{
		ID: 99, Username: "sadmin", Name: "Super", Email: "sa@them.local",
		Role: "super_admin", DashboardAccess: "admin", TokenExpiry: 3600,
	}, "pw99", "")
	return s
}

func (f *fakeUserStore) ListUsers(_ context.Context) ([]ManagedUser, error) {
	out := make([]ManagedUser, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeUserStore) GetManagedUser(_ context.Context, id int64) (*ManagedUser, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}

func (f *fakeUserStore) CreateUser(_ context.Context, in UserCreateInput) (*ManagedUser, error) {
	for _, u := range f.users {
		if u.Username == in.Username {
			return nil, ErrUserConflict
		}
	}
	id := f.nextID
	f.nextID++
	u := &ManagedUser{
		ID:       id,
		Username: in.Username,
		Name:     in.Name,
		Email:    in.Email,
		Role:     in.RoleName,
		Active:   true,
		TenantID: in.TenantID,
		TenantRole: in.TenantRole,
		CreatedAt: time.Now(),
	}
	f.users[id] = u
	return u, nil
}

func (f *fakeUserStore) UpdateUser(_ context.Context, id int64, in UserUpdateInput) (*ManagedUser, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	if in.Name != nil {
		u.Name = *in.Name
	}
	if in.Email != nil {
		u.Email = *in.Email
	}
	if in.Active != nil {
		u.Active = *in.Active
	}
	return u, nil
}

func (f *fakeUserStore) DeleteUser(_ context.Context, id int64) error {
	if _, ok := f.users[id]; !ok {
		return ErrUserNotFound
	}
	delete(f.users, id)
	return nil
}

func (f *fakeUserStore) ResetPassword(_ context.Context, id int64, _ string) error {
	if _, ok := f.users[id]; !ok {
		return ErrUserNotFound
	}
	return nil
}

func (f *fakeUserStore) ListTenants(_ context.Context) ([]TenantSummary, error) {
	return f.tenants, nil
}

// ── test router ───────────────────────────────────────────────────────────────

func testUserMgmtRouter(t *testing.T) (http.Handler, *fakeUserStore) {
	t.Helper()
	store := newFakeUserStore()
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	umh := NewUserMgmtHandlers(store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Reuse main router builder (NewRouterWithAdmin) with a minimal Handlers.
	svc := NewService(&store.fakeStore, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandlers(svc, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := NewRouterWithAdmin(h, nil, umh, &store.fakeStore, "test")
	return router, store
}

// superAdminToken mints a valid access token for the sadmin fixture user.
func superAdminToken(t *testing.T) string {
	t.Helper()
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	signer := NewTokenSigner(cfg)
	tok, _, err := signer.IssueAccessToken(99, "sadmin", "Super", "super_admin", "00000000-0000-0000-0000-000000000001", 0)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

func doAdmin(t *testing.T, router http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	var r *http.Request
	if len(bodyBytes) > 0 {
		r = httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// ── UM-01: list users — empty store ──────────────────────────────────────────

func TestUserMgmt_ListUsers_Empty(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/users", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out []ManagedUser
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %d", len(out))
	}
}

// ── UM-02: create user ────────────────────────────────────────────────────────

func TestUserMgmt_CreateUser(t *testing.T) {
	router, store := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	w := doAdmin(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "alice", "name": "Alice", "password": "pass123",
		"role": "viewer", "tenant_id": "00000000-0000-0000-0000-000000000001", "tenant_role": "member",
	}, tok)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out ManagedUser
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Username != "alice" || out.Name != "Alice" {
		t.Fatalf("unexpected user: %+v", out)
	}
	if _, ok := store.users[out.ID]; !ok {
		t.Fatalf("user not in store")
	}
}

// ── UM-03: create user — duplicate username → 409 ─────────────────────────────

func TestUserMgmt_CreateUser_Conflict(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	body := map[string]any{"username": "alice", "name": "Alice", "password": "pass123", "role": "viewer"}
	doAdmin(t, router, http.MethodPost, "/api/v1/admin/users", body, tok)
	w := doAdmin(t, router, http.MethodPost, "/api/v1/admin/users", body, tok)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// ── UM-04: create user — missing required fields → 400 ───────────────────────

func TestUserMgmt_CreateUser_MissingFields(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	w := doAdmin(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{"username": "u"}, tok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── UM-05: get user ───────────────────────────────────────────────────────────

func TestUserMgmt_GetUser(t *testing.T) {
	router, store := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	// Seed
	store.users[7] = &ManagedUser{ID: 7, Username: "bob", Name: "Bob", Role: "viewer", Active: true, CreatedAt: time.Now()}
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/users/7", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out ManagedUser
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Username != "bob" {
		t.Fatalf("unexpected user: %+v", out)
	}
}

// ── UM-06: get user — not found → 404 ────────────────────────────────────────

func TestUserMgmt_GetUser_NotFound(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/users/999", nil, tok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── UM-07: update user ────────────────────────────────────────────────────────

func TestUserMgmt_UpdateUser(t *testing.T) {
	router, store := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	store.users[3] = &ManagedUser{ID: 3, Username: "carol", Name: "Carol", Role: "viewer", Active: true, CreatedAt: time.Now()}
	newName := "Carol Updated"
	w := doAdmin(t, router, http.MethodPatch, "/api/v1/admin/users/3", map[string]any{"name": newName}, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out ManagedUser
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Name != newName {
		t.Fatalf("name not updated: %q", out.Name)
	}
}

// ── UM-08: delete user ────────────────────────────────────────────────────────

func TestUserMgmt_DeleteUser(t *testing.T) {
	router, store := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	store.users[5] = &ManagedUser{ID: 5, Username: "dan", Name: "Dan", Role: "viewer", Active: true, CreatedAt: time.Now()}
	w := doAdmin(t, router, http.MethodDelete, "/api/v1/admin/users/5", nil, tok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if _, ok := store.users[5]; ok {
		t.Fatalf("user still in store after delete")
	}
}

// ── UM-09: reset password ─────────────────────────────────────────────────────

func TestUserMgmt_ResetPassword(t *testing.T) {
	router, store := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	store.users[6] = &ManagedUser{ID: 6, Username: "eve", Name: "Eve", Role: "viewer", Active: true, CreatedAt: time.Now()}
	w := doAdmin(t, router, http.MethodPost, "/api/v1/admin/users/6/reset-password",
		map[string]any{"password": "newpass"}, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// ── UM-10: list tenants ───────────────────────────────────────────────────────

func TestUserMgmt_ListTenants(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	tok := superAdminToken(t)
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/tenants", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out []TenantSummary
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out) == 0 {
		t.Fatalf("expected at least one tenant")
	}
}

// ── UM-11: no token → 401 ─────────────────────────────────────────────────────

func TestUserMgmt_NoToken_Unauthorized(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/users", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ── UM-12: non-super_admin token → 403 ───────────────────────────────────────

func TestUserMgmt_NonSuperAdmin_Forbidden(t *testing.T) {
	router, _ := testUserMgmtRouter(t)
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	signer := NewTokenSigner(cfg)
	tok, _, _ := signer.IssueAccessToken(2, "viewer", "Viewer", "viewer", "00000000-0000-0000-0000-000000000001", 0)
	w := doAdmin(t, router, http.MethodGet, "/api/v1/admin/users", nil, tok)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
