package execution

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlatformRoleChecker loads a user's platform role (from auth_service.roles).
// This is separate from the tenant-level RoleChecker interface which handles
// tenant_roles and app access grants.
type PlatformRoleChecker interface {
	// GetUserRole returns the role name for the given user_id.
	// Returns empty string if user not found or no role assigned.
	GetUserRole(ctx context.Context, userID int64) (string, error)

	// GetRoleConfig returns the full role configuration for the given user.
	// Returns nil if user not found or no role assigned.
	GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error)
}

// RoleConfig contains platform role limits and permissions.
type RoleConfig struct {
	Name             string
	RateLimit        int     // requests per minute (0 = unlimited)
	CostLimitDaily   float64 // USD per day (0 = unlimited)
	DashboardAccess  string  // "admin" | "view" | "none"
}

// pgxPlatformRoleChecker queries auth_service.users JOIN roles.
type pgxPlatformRoleChecker struct {
	pool *pgxpool.Pool
}

// NewPgxPlatformRoleChecker creates a PlatformRoleChecker that queries the database.
func NewPgxPlatformRoleChecker(pool *pgxpool.Pool) PlatformRoleChecker {
	return &pgxPlatformRoleChecker{pool: pool}
}

// GetUserRole loads the user's role name from auth_service.users → roles.
// Returns empty string if user not found or has no role assigned.
func (rc *pgxPlatformRoleChecker) GetUserRole(ctx context.Context, userID int64) (string, error) {
	const q = `
		SELECT r.name
		FROM auth_service.users u
		JOIN auth_service.roles r ON u.role_id = r.id
		WHERE u.id = $1
	`
	var roleName string
	err := rc.pool.QueryRow(ctx, q, userID).Scan(&roleName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// User exists but no role (shouldn't happen with FK, but safe to return empty)
			return "", nil
		}
		return "", fmt.Errorf("role query failed: %w", err)
	}
	return roleName, nil
}

// GetRoleConfig loads the user's full role configuration.
// Returns nil if user not found or has no role assigned.
func (rc *pgxPlatformRoleChecker) GetRoleConfig(ctx context.Context, userID int64) (*RoleConfig, error) {
	const q = `
		SELECT r.name, r.rate_limit, r.cost_limit_daily, r.dashboard_access
		FROM auth_service.users u
		JOIN auth_service.roles r ON u.role_id = r.id
		WHERE u.id = $1
	`
	var cfg RoleConfig
	err := rc.pool.QueryRow(ctx, q, userID).Scan(
		&cfg.Name, &cfg.RateLimit, &cfg.CostLimitDaily, &cfg.DashboardAccess,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("role config query failed: %w", err)
	}
	return &cfg, nil
}
