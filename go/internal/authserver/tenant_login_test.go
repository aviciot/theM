package authserver

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// twoTenantStore is a minimal Store for two-tenant isolation tests.
// It stores per-user memberships independently of the shared fakeStore.
type twoTenantStore struct {
	fakeStore
	membershipsByID map[int64]struct{ tenantID, role string }
}

func newTwoTenantStore() *twoTenantStore {
	s := &twoTenantStore{
		fakeStore: fakeStore{
			byLogin:      map[string]*userRecord{},
			byAPIHash:    map[string]*userRecord{},
			byID:         map[int64]*userRecord{},
			blacklist:    map[string]time.Time{},
			memberships:  map[int64]struct{ tenantID, role string }{},
			domainLookup: map[string]TenantLookupResult{},
		},
		membershipsByID: map[int64]struct{ tenantID, role string }{},
	}
	return s
}

func (s *twoTenantStore) addMember(u *userRecord, rawPassword, tenantID, role string) {
	h, _ := hashPassword(rawPassword)
	u.PasswordHash = h
	s.byLogin[u.Username] = u
	s.byID[u.ID] = u
	s.membershipsByID[u.ID] = struct{ tenantID, role string }{tenantID, role}
}

func (s *twoTenantStore) GetTenantMembership(_ context.Context, userID int64, _ string) (string, string, error) {
	m, ok := s.membershipsByID[userID]
	if !ok {
		return "", "", ErrNoMembership
	}
	return m.tenantID, m.role, nil
}

func newTwoTenantService(t *testing.T) (*Service, *twoTenantStore) {
	t.Helper()
	store := newTwoTenantStore()
	cfg := &Config{JWTSecret: testSecret, AccessTokenExpiry: 3600, RefreshTokenExpiry: 604800}
	svc := NewService(store, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, store
}

// UM-13: TestTenantLogin_TwoTenantIsolation — two users in different tenants each
// receive a JWT scoped to their own tenant. A user with no membership is blocked.
// /me returns the membership role from the JWT, not the global DB role.
func TestTenantLogin_TwoTenantIsolation(t *testing.T) {
	const tenantA = "aaaaaaaa-0000-0000-0000-000000000001"
	const tenantB = "bbbbbbbb-0000-0000-0000-000000000002"

	svc, store := newTwoTenantService(t)

	// User 1: tenant A, membership role "admin", global DB role "developer"
	store.addMember(&userRecord{
		ID: 10, Username: "alice", Name: "Alice",
		Role: "developer", DashboardAccess: "admin", TokenExpiry: 3600,
	}, "alicepass", tenantA, "admin")

	// User 2: tenant B, membership role "member", global DB role "analyst"
	store.addMember(&userRecord{
		ID: 20, Username: "bob", Name: "Bob",
		Role: "analyst", DashboardAccess: "admin", TokenExpiry: 3600,
	}, "bobpass", tenantB, "member")

	// User 3: no membership → blocked
	store.byLogin["charlie"] = &userRecord{
		ID: 30, Username: "charlie", Name: "Charlie",
		Role: "viewer", DashboardAccess: "admin", TokenExpiry: 3600,
	}
	h3, _ := hashPassword("charliepass")
	store.byLogin["charlie"].PasswordHash = h3
	store.byID[30] = store.byLogin["charlie"]

	t.Run("alice_gets_tenant_a_admin", func(t *testing.T) {
		pair, err := svc.Login(context.Background(), LoginInput{Username: "alice", Password: "alicepass"})
		if err != nil {
			t.Fatalf("alice login: %v", err)
		}
		parts := splitJWT(pair.AccessToken)
		if parts == nil {
			t.Fatal("invalid JWT")
		}
		var claims struct {
			TenantID string `json:"tenant_id"`
			Role     string `json:"role"`
		}
		import_json_unmarshal(t, parts[1], &claims)
		if claims.TenantID != tenantA {
			t.Errorf("alice: want tenant_id=%q got %q", tenantA, claims.TenantID)
		}
		if claims.Role != "admin" {
			t.Errorf("alice: want role=admin got %q", claims.Role)
		}

		// /me must return JWT membership role, not global DB role
		me, err := svc.Me(context.Background(), pair.AccessToken)
		if err != nil {
			t.Fatalf("alice /me: %v", err)
		}
		if me.Role != "admin" {
			t.Errorf("alice /me role: want admin got %q", me.Role)
		}
		if me.TenantID != tenantA {
			t.Errorf("alice /me tenant_id: want %q got %q", tenantA, me.TenantID)
		}
	})

	t.Run("bob_gets_tenant_b_member", func(t *testing.T) {
		pair, err := svc.Login(context.Background(), LoginInput{Username: "bob", Password: "bobpass"})
		if err != nil {
			t.Fatalf("bob login: %v", err)
		}
		parts := splitJWT(pair.AccessToken)
		if parts == nil {
			t.Fatal("invalid JWT")
		}
		var claims struct {
			TenantID string `json:"tenant_id"`
			Role     string `json:"role"`
		}
		import_json_unmarshal(t, parts[1], &claims)
		if claims.TenantID != tenantB {
			t.Errorf("bob: want tenant_id=%q got %q", tenantB, claims.TenantID)
		}
		if claims.Role != "member" {
			t.Errorf("bob: want role=member got %q", claims.Role)
		}

		me, err := svc.Me(context.Background(), pair.AccessToken)
		if err != nil {
			t.Fatalf("bob /me: %v", err)
		}
		if me.Role != "member" {
			t.Errorf("bob /me role: want member got %q", me.Role)
		}
		if me.TenantID != tenantB {
			t.Errorf("bob /me tenant_id: want %q got %q", tenantB, me.TenantID)
		}
	})

	t.Run("charlie_no_membership_blocked", func(t *testing.T) {
		_, err := svc.Login(context.Background(), LoginInput{Username: "charlie", Password: "charliepass"})
		if err != ErrNoTenantMembership {
			t.Fatalf("want ErrNoTenantMembership, got %v", err)
		}
	})
}

// UM-14: TestTenantLogin_RefreshCarriesTenant — refresh issues a new access token
// that still carries the correct tenant_id for the user.
func TestTenantLogin_RefreshCarriesTenant(t *testing.T) {
	const tenantA = "aaaaaaaa-0000-0000-0000-000000000001"

	svc, store := newTwoTenantService(t)
	store.addMember(&userRecord{
		ID: 10, Username: "alice", Name: "Alice",
		Role: "developer", DashboardAccess: "admin", TokenExpiry: 3600,
	}, "alicepass", tenantA, "admin")

	pair, err := svc.Login(context.Background(), LoginInput{Username: "alice", Password: "alicepass"})
	if err != nil {
		t.Fatalf("initial login: %v", err)
	}

	newPair, err := svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	parts := splitJWT(newPair.AccessToken)
	if parts == nil {
		t.Fatal("invalid refreshed JWT")
	}
	var claims struct {
		TenantID string `json:"tenant_id"`
		Role     string `json:"role"`
	}
	import_json_unmarshal(t, parts[1], &claims)
	if claims.TenantID != tenantA {
		t.Errorf("refreshed token: want tenant_id=%q got %q", tenantA, claims.TenantID)
	}
	if claims.Role != "admin" {
		t.Errorf("refreshed token: want role=admin got %q", claims.Role)
	}
}
