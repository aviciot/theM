package authserver

import (
	"context"
	"errors"
	"time"
)

// ErrUserNotFound is returned when no active user matches the lookup criteria.
var ErrUserNotFound = errors.New("authserver: user not found")

// userRecord is the subset of auth_service.users (+ role name) the auth flows
// need. dashboardAccess drives the login gate (Python rejects 'none').
type userRecord struct {
	ID              int64
	Username        string
	Name            string
	Email           string
	Role            string
	PasswordHash    string
	DashboardAccess string
	TokenExpiry     int // roles.token_expiry, seconds; 0 → use config fallback
}

// Store abstracts all reads/writes against the auth_service schema so handlers
// and the service layer never embed SQL. The pgx implementation lives in pgx.go.
type Store interface {
	// GetUserByLogin resolves an active user by email OR username (Python tries
	// email first, then username). Returns ErrUserNotFound when absent.
	GetUserByLogin(ctx context.Context, login string) (*userRecord, error)
	// GetUserByAPIKeyHash resolves an active user by the SHA-256 hex of an API key.
	GetUserByAPIKeyHash(ctx context.Context, apiKeyHash string) (*userRecord, error)
	// GetUserByID resolves an active user by id.
	GetUserByID(ctx context.Context, id int64) (*userRecord, error)

	// TouchLastLogin sets users.last_login_at = now for the given user. Best
	// effort — errors are logged by the caller but do not fail login.
	TouchLastLogin(ctx context.Context, id int64) error

	// InsertSession records an issued access token hash + expiry. Best effort.
	InsertSession(ctx context.Context, userID int64, accessTokenHash string, expiresAt time.Time) error

	// IsBlacklisted reports whether tokenHash is present and not yet expired.
	IsBlacklisted(ctx context.Context, tokenHash string) (bool, error)
	// Blacklist inserts tokenHash with an expiry (idempotent).
	Blacklist(ctx context.Context, tokenHash string, expiresAt time.Time) error

	// Ping checks database reachability for readiness probes.
	Ping(ctx context.Context) error
}
