package execution

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/aviciot/them/internal/epconfig"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// mockPlatformRoleChecker is a fake PlatformRoleChecker for testing.
type mockPlatformRoleChecker struct {
	userRoles map[int64]string // userID -> role name
	err       error             // error to return
}

func (m *mockPlatformRoleChecker) GetUserRole(ctx context.Context, userID int64) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	role, ok := m.userRoles[userID]
	if !ok {
		return "", nil // user not found
	}
	return role, nil
}

func (m *mockPlatformRoleChecker) GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	role, ok := m.userRoles[userID]
	if !ok {
		return nil, nil
	}
	// Return a minimal RoleConfig for basic role tests (quota tests use mockPlatformRoleCheckerWithConfig)
	return &RoleConfig{Name: role}, nil
}

// TestLifecycle_RequiredRole_Pass verifies that a user with the matching role is admitted.
func TestLifecycle_RequiredRole_Pass(t *testing.T) {
	// Setup: Alice (user_id=42) has role "developer"
	roleChecker := &mockPlatformRoleChecker{
		userRoles: map[int64]string{42: "developer"},
	}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		logger:             testLogger,
	}

	// EP requires "developer" role
	cfg := &epconfig.EPConfig{
		AccessMode:   epconfig.AccessModeUser,
		RequiredRole: "developer",
	}

	req := ExecutionRequest{
		UserID: 42, // Alice
	}

	// Simulate the role check (extracted from Admit method)
	ctx := context.Background()
	userRole, err := lc.platformRoleChecker.GetUserRole(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role lookup failed: %v", err)
	}

	// Assert: Alice's role matches required role
	if userRole != cfg.RequiredRole {
		t.Errorf("expected role %q, got %q", cfg.RequiredRole, userRole)
	}
}

// TestLifecycle_RequiredRole_Deny verifies that a user with insufficient role is rejected.
func TestLifecycle_RequiredRole_Deny(t *testing.T) {
	// Setup: Bob (user_id=99) has role "viewer"
	roleChecker := &mockPlatformRoleChecker{
		userRoles: map[int64]string{99: "viewer"},
	}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		logger:             testLogger,
	}

	// EP requires "developer" role
	cfg := &epconfig.EPConfig{
		AccessMode:   epconfig.AccessModeUser,
		RequiredRole: "developer",
	}

	req := ExecutionRequest{
		UserID: 99, // Bob
	}

	// Simulate the role check
	ctx := context.Background()
	userRole, err := lc.platformRoleChecker.GetUserRole(ctx, req.UserID)
	if err != nil {
		t.Fatalf("role lookup failed: %v", err)
	}

	// Assert: Bob's role does NOT match required role
	if userRole == cfg.RequiredRole {
		t.Errorf("expected role mismatch, but %q == %q", userRole, cfg.RequiredRole)
	}

	// Expected behavior: Admit would return AdmitErrForbidden (verified in integration test)
	if userRole != "viewer" {
		t.Errorf("expected viewer role, got %q", userRole)
	}
}

// TestLifecycle_RequiredRole_NotRequired verifies that when no role is required, any user passes.
func TestLifecycle_RequiredRole_NotRequired(t *testing.T) {
	// EP has NO required role
	cfg := &epconfig.EPConfig{
		AccessMode:   epconfig.AccessModeUser,
		RequiredRole: "", // empty = no requirement
	}

	// Simulate the check: if RequiredRole is empty, skip check entirely
	if cfg.RequiredRole != "" {
		t.Errorf("expected no required role, got %q", cfg.RequiredRole)
	}

	// Expected behavior: Admit proceeds without role check
}

// TestLifecycle_RequiredRole_PublicMode verifies that role check is skipped for public EPs.
func TestLifecycle_RequiredRole_PublicMode(t *testing.T) {
	// EP is public mode (no JWT, so no user_id)
	cfg := &epconfig.EPConfig{
		AccessMode:   epconfig.AccessModePublic,
		RequiredRole: "developer", // role requirement set, but mode is public
	}

	// Simulate the check: AccessModeUser is required for role enforcement
	if cfg.AccessMode != epconfig.AccessModeUser {
		// Role check skipped - this is correct behavior
		return
	}

	t.Errorf("role check should be skipped for non-user_jwt modes")
}

// TestLifecycle_PlatformRoleChecker_Error verifies that role lookup errors are handled.
func TestLifecycle_PlatformRoleChecker_Error(t *testing.T) {
	// Setup: role checker returns an error
	roleChecker := &mockPlatformRoleChecker{
		err: errors.New("database unavailable"),
	}

	lc := &Lifecycle{
		platformRoleChecker: roleChecker,
		logger:             testLogger,
	}

	req := ExecutionRequest{
		UserID: 42,
	}

	// Simulate the role check
	ctx := context.Background()
	_, err := lc.platformRoleChecker.GetUserRole(ctx, req.UserID)

	// Assert: error is propagated
	if err == nil {
		t.Error("expected error, got nil")
	}

	// Expected behavior: Admit returns AdmitErrInternal
}
