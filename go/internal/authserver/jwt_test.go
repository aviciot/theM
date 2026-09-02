package authserver

import (
	"strings"
	"testing"
	"time"

	auth "github.com/aviciot/them/internal/auth"
)

const testSecret = "test-secret-key-256-bit-abcdefgh"

func newTestSigner() *tokenSigner {
	return newTokenSigner([]byte(testSecret), 3600, 604800)
}

const testTenantID = "00000000-0000-0000-0000-000000000001"

func TestIssueAndVerifyAccessToken(t *testing.T) {
	s := newTestSigner()
	tok, expiresIn, err := s.IssueAccessToken(42, "admin", "Admin User", "super_admin", testTenantID, 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if expiresIn != 3600 {
		t.Fatalf("expiresIn = %d, want 3600", expiresIn)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Sub != "42" || claims.Username != "admin" || claims.Role != "super_admin" || claims.Type != "access" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.UserID() != 42 {
		t.Fatalf("UserID = %d, want 42", claims.UserID())
	}
}

func TestRoleExpiryOverride(t *testing.T) {
	s := newTestSigner()
	_, expiresIn, err := s.IssueAccessToken(1, "u", "n", "r", testTenantID, 7200)
	if err != nil {
		t.Fatal(err)
	}
	if expiresIn != 7200 {
		t.Fatalf("expiresIn = %d, want 7200 (role override)", expiresIn)
	}
}

func TestRefreshTokenType(t *testing.T) {
	s := newTestSigner()
	tok, err := s.IssueRefreshToken(7)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Type != "refresh" || claims.Sub != "7" {
		t.Fatalf("unexpected refresh claims: %+v", claims)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	s := newTestSigner()
	tok, _, _ := s.IssueAccessToken(1, "u", "n", "r", testTenantID, 0)
	other := newTokenSigner([]byte("a-completely-different-secret-000"), 3600, 604800)
	if _, err := other.Verify(tok); err != ErrTokenSignature {
		t.Fatalf("want ErrTokenSignature, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s := newTestSigner()
	// Freeze "now" in the past so the issued token is already expired.
	s.now = func() time.Time { return time.Unix(1000, 0) }
	tok, _, _ := s.IssueAccessToken(1, "u", "n", "r", testTenantID, 1)
	s.now = func() time.Time { return time.Unix(1_000_000, 0) }
	if _, err := s.Verify(tok); err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	s := newTestSigner()
	for _, bad := range []string{"", "a.b", "a.b.c.d", "not-a-token"} {
		if _, err := s.Verify(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

// TestBridgeCompatibility is the critical cross-package check: a token issued by
// the auth server MUST validate under the Go bridge's ValidateHS256JWT with the
// same secret and expose the same identity fields.
func TestBridgeCompatibility(t *testing.T) {
	s := newTestSigner()
	tok, _, err := s.IssueAccessToken(99, "alice", "Alice A", "developer", testTenantID, 0)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateHS256JWT(tok, []byte(testSecret))
	if err != nil {
		t.Fatalf("bridge ValidateHS256JWT rejected auth-server token: %v", err)
	}
	if claims.UserID != 99 {
		t.Fatalf("bridge UserID = %d, want 99", claims.UserID)
	}
	if claims.Username != "alice" {
		t.Fatalf("bridge Username = %q, want alice", claims.Username)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "developer" {
		t.Fatalf("bridge Roles = %v, want [developer]", claims.Roles)
	}
	if claims.TenantID != testTenantID {
		t.Fatalf("bridge TenantID = %q, want %q", claims.TenantID, testTenantID)
	}
}

func TestHashTokenIsHexSHA256(t *testing.T) {
	h := hashToken("hello")
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64", len(h))
	}
	if strings.ToLower(h) != h {
		t.Fatalf("hash should be lowercase hex")
	}
	// Known SHA-256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if h != want {
		t.Fatalf("hashToken(hello) = %s, want %s", h, want)
	}
}
