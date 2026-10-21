package authserver

import (
	"context"
	"testing"
	"time"
)

// TestLogout_VerifiedToken verifies that Logout returns verified claims for a valid token.
func TestLogout_VerifiedToken(t *testing.T) {
	store := newFakeStore()
	cfg := &Config{JWTSecret: "test-secret", AccessTokenExpiry: 3600, RefreshTokenExpiry: 86400}
	svc := NewService(store, cfg, nil)

	// Issue a valid access token
	token, _, err := svc.signer.IssueAccessToken(123, "testuser", "Test User", "admin", "tenant-uuid", 0)
	if err != nil {
		t.Fatalf("IssueAccessToken failed: %v", err)
	}

	// Logout should return verified claims
	claims := svc.Logout(context.Background(), token)
	if claims == nil {
		t.Fatal("Logout returned nil claims for valid token")
	}
	if claims.UserID() != 123 {
		t.Errorf("Expected user_id=123, got %d", claims.UserID())
	}
	if claims.TenantID != "tenant-uuid" {
		t.Errorf("Expected tenant_id=tenant-uuid, got %s", claims.TenantID)
	}
	if claims.Username != "testuser" {
		t.Errorf("Expected username=testuser, got %s", claims.Username)
	}
}

// TestLogout_ExpiredToken verifies that Logout returns nil for expired tokens.
func TestLogout_ExpiredToken(t *testing.T) {
	store := newFakeStore()
	cfg := &Config{JWTSecret: "test-secret", AccessTokenExpiry: 3600, RefreshTokenExpiry: 86400}
	svc := NewService(store, cfg, nil)

	// Issue token with past expiry (1 second ago)
	oldNow := svc.signer.now
	svc.signer.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	token, _, err := svc.signer.IssueAccessToken(123, "testuser", "Test User", "admin", "tenant-uuid", 0)
	if err != nil {
		t.Fatalf("IssueAccessToken failed: %v", err)
	}
	svc.signer.now = oldNow // Restore to current time (token is now expired)

	// Logout should return nil for expired token
	claims := svc.Logout(context.Background(), token)
	if claims != nil {
		t.Error("Logout should return nil for expired token")
	}
}

// TestLogout_ForgedToken verifies that Logout returns nil for forged tokens.
func TestLogout_ForgedToken(t *testing.T) {
	store := newFakeStore()
	cfg := &Config{JWTSecret: "test-secret", AccessTokenExpiry: 3600, RefreshTokenExpiry: 86400}
	svc := NewService(store, cfg, nil)

	// Create a token signed with wrong secret
	forgeSigner := newTokenSigner([]byte("wrong-secret"), 3600, 86400)
	forgedToken, _, err := forgeSigner.IssueAccessToken(999, "hacker", "Hacker", "admin", "forged-tenant", 0)
	if err != nil {
		t.Fatalf("IssueAccessToken (forged) failed: %v", err)
	}

	// Logout should return nil for forged token
	claims := svc.Logout(context.Background(), forgedToken)
	if claims != nil {
		t.Error("Logout should return nil for forged token")
	}
}

// TestLogout_MalformedToken verifies that Logout returns nil for malformed tokens.
func TestLogout_MalformedToken(t *testing.T) {
	store := newFakeStore()
	cfg := &Config{JWTSecret: "test-secret", AccessTokenExpiry: 3600, RefreshTokenExpiry: 86400}
	svc := NewService(store, cfg, nil)

	// Test various malformed tokens
	malformedTokens := []string{
		"not.a.jwt",           // too few parts
		"not.enough",          // still too few
		"too.many.parts.here", // too many parts
		"invalid-base64!.payload.signature",
	}

	for _, token := range malformedTokens {
		claims := svc.Logout(context.Background(), token)
		if claims != nil {
			t.Errorf("Logout should return nil for malformed token %q", token)
		}
	}
}

// TestLogout_EmptyToken verifies that Logout returns nil for empty token.
func TestLogout_EmptyToken(t *testing.T) {
	store := newFakeStore()
	cfg := &Config{JWTSecret: "test-secret", AccessTokenExpiry: 3600, RefreshTokenExpiry: 86400}
	svc := NewService(store, cfg, nil)

	claims := svc.Logout(context.Background(), "")
	if claims != nil {
		t.Error("Logout should return nil for empty token")
	}
}
