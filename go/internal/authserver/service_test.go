package authserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fakeStore is an in-memory Store for service/handler tests. It records
// blacklist entries and sessions so revocation and best-effort writes can be
// asserted without a database.
type fakeStore struct {
	byLogin     map[string]*userRecord
	byAPIHash   map[string]*userRecord
	byID        map[int64]*userRecord
	blacklist   map[string]time.Time
	memberships map[int64]struct{ tenantID, role string } // userID → membership
	sessions    int
	touched     int
	failPing    bool
}

const testBootstrapTenantID = "00000000-0000-0000-0000-000000000001"

func newFakeStore() *fakeStore {
	return &fakeStore{
		byLogin:     map[string]*userRecord{},
		byAPIHash:   map[string]*userRecord{},
		byID:        map[int64]*userRecord{},
		blacklist:   map[string]time.Time{},
		memberships: map[int64]struct{ tenantID, role string }{},
	}
}

func (f *fakeStore) addUser(u *userRecord, rawPassword, rawAPIKey string) {
	if rawPassword != "" {
		h, _ := hashPassword(rawPassword)
		u.PasswordHash = h
	}
	f.byLogin[u.Username] = u
	if u.Email != "" {
		f.byLogin[u.Email] = u
	}
	f.byID[u.ID] = u
	if rawAPIKey != "" {
		f.byAPIHash[hashToken(rawAPIKey)] = u
	}
	// Default: assign to bootstrap tenant with the user's role.
	f.memberships[u.ID] = struct{ tenantID, role string }{testBootstrapTenantID, u.Role}
}

func (f *fakeStore) GetTenantMembership(_ context.Context, userID int64) (string, string, error) {
	m, ok := f.memberships[userID]
	if !ok {
		return "", "", ErrNoMembership
	}
	return m.tenantID, m.role, nil
}

func (f *fakeStore) GetUserByLogin(_ context.Context, login string) (*userRecord, error) {
	if u, ok := f.byLogin[login]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}
func (f *fakeStore) GetUserByAPIKeyHash(_ context.Context, h string) (*userRecord, error) {
	if u, ok := f.byAPIHash[h]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}
func (f *fakeStore) GetUserByID(_ context.Context, id int64) (*userRecord, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, ErrUserNotFound
}
func (f *fakeStore) TouchLastLogin(_ context.Context, _ int64) error { f.touched++; return nil }
func (f *fakeStore) InsertSession(_ context.Context, _ int64, _ string, _ time.Time) error {
	f.sessions++
	return nil
}
func (f *fakeStore) IsBlacklisted(_ context.Context, h string) (bool, error) {
	exp, ok := f.blacklist[h]
	return ok && exp.After(time.Now()), nil
}
func (f *fakeStore) Blacklist(_ context.Context, h string, exp time.Time) error {
	f.blacklist[h] = exp
	return nil
}
func (f *fakeStore) Ping(_ context.Context) error {
	if f.failPing {
		return io.EOF
	}
	return nil
}

func (f *fakeStore) GetPreferences(_ context.Context, _ int64) ([]byte, error) {
	return []byte(`{}`), nil
}

func (f *fakeStore) SetPreferences(_ context.Context, _ int64, _ []byte) error {
	return nil
}

func testService(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	store.addUser(&userRecord{
		ID: 1, Username: "admin", Name: "Admin", Email: "admin@them.local",
		Role: "super_admin", DashboardAccess: "admin", TokenExpiry: 3600,
	}, "admin123", "ak_secretkey")
	store.addUser(&userRecord{
		ID: 2, Username: "viewer", Name: "Viewer", Email: "viewer@them.local",
		Role: "viewer", DashboardAccess: "none", TokenExpiry: 3600,
	}, "viewpass", "")
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	svc := NewService(store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, store
}

func TestLoginPasswordSuccess(t *testing.T) {
	svc, store := testService(t)
	pair, err := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.ExpiresIn != 3600 {
		t.Fatalf("bad pair: %+v", pair)
	}
	if store.sessions != 1 || store.touched != 1 {
		t.Fatalf("expected session+last_login recorded, got sessions=%d touched=%d", store.sessions, store.touched)
	}
}

func TestLoginByEmail(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{Username: "admin@them.local", Password: "admin123"}); err != nil {
		t.Fatalf("email login failed: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "nope"}); err != ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginUnknownUser(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{Username: "ghost", Password: "x"}); err != ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
}

func TestLoginAPIKey(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{APIKey: "ak_secretkey"}); err != nil {
		t.Fatalf("api key login failed: %v", err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{APIKey: "ak_wrong"}); err != ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials for bad api key, got %v", err)
	}
}

func TestLoginMissingCredentials(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{}); err != ErrMissingCredentials {
		t.Fatalf("want ErrMissingCredentials, got %v", err)
	}
}

func TestLoginDashboardAccessDenied(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Login(context.Background(), LoginInput{Username: "viewer", Password: "viewpass"}); err != ErrDashboardAccessDenied {
		t.Fatalf("want ErrDashboardAccessDenied, got %v", err)
	}
}

func TestMeAndRefreshFlow(t *testing.T) {
	svc, _ := testService(t)
	pair, err := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	if err != nil {
		t.Fatal(err)
	}
	// Me from access token
	u, err := svc.Me(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if u.Username != "admin" || u.Role != "super_admin" || u.ID != 1 {
		t.Fatalf("unexpected me: %+v", u)
	}
	// Refresh from refresh token
	newPair, err := svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if newPair.AccessToken == "" {
		t.Fatal("refresh returned empty access token")
	}
}

func TestRefreshRejectsAccessToken(t *testing.T) {
	svc, _ := testService(t)
	pair, _ := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	if _, err := svc.Refresh(context.Background(), pair.AccessToken); err != ErrWrongTokenType {
		t.Fatalf("want ErrWrongTokenType, got %v", err)
	}
}

func TestMeRejectsEmptyToken(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.Me(context.Background(), ""); err != ErrNotAuthenticated {
		t.Fatalf("want ErrNotAuthenticated, got %v", err)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	svc, store := testService(t)
	pair, _ := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	svc.Logout(context.Background(), pair.AccessToken)
	if len(store.blacklist) != 1 {
		t.Fatalf("expected 1 blacklisted token, got %d", len(store.blacklist))
	}
	// Me must now reject the revoked token.
	if _, err := svc.Me(context.Background(), pair.AccessToken); err != ErrTokenRevoked {
		t.Fatalf("want ErrTokenRevoked after logout, got %v", err)
	}
}

// TestLoginEmbedsTenantID verifies the issued access token carries the tenant_id
// claim from the tenant_memberships table. This is the core invariant of Step 1.
func TestLoginEmbedsTenantID(t *testing.T) {
	svc, _ := testService(t)
	pair, err := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Decode the access token payload directly to inspect the tenant_id claim.
	parts := splitJWT(pair.AccessToken)
	if len(parts) != 3 {
		t.Fatalf("token does not have 3 parts")
	}
	var payload struct {
		TenantID string `json:"tenant_id"`
		Role     string `json:"role"`
	}
	import_json_unmarshal(t, parts[1], &payload)
	if payload.TenantID != testBootstrapTenantID {
		t.Fatalf("token tenant_id = %q, want %q", payload.TenantID, testBootstrapTenantID)
	}
	if payload.Role != "super_admin" {
		t.Fatalf("token role = %q, want super_admin", payload.Role)
	}
}

// TestLoginNoMembershipBlocked verifies that a user with no tenant_membership row
// cannot log in. This enforces the new membership requirement.
func TestLoginNoMembershipBlocked(t *testing.T) {
	svc, store := testService(t)
	// Remove the admin user's membership to simulate a user with no tenant.
	delete(store.memberships, 1)
	_, err := svc.Login(context.Background(), LoginInput{Username: "admin", Password: "admin123"})
	if err != ErrNoTenantMembership {
		t.Fatalf("want ErrNoTenantMembership, got %v", err)
	}
}

// splitJWT splits a JWT into its three base64url parts and base64-decodes the payload.
func splitJWT(tok string) [][]byte {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([][]byte, 3)
	for i, p := range parts {
		switch len(p) % 4 {
		case 2:
			p += "=="
		case 3:
			p += "="
		}
		b, err := base64.URLEncoding.DecodeString(p)
		if err != nil {
			return nil
		}
		out[i] = b
	}
	return out
}

func import_json_unmarshal(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
}
