package authserver

import (
	"context"
	"errors"
	"time"
)

// ErrUserNotFound is returned when no active user matches the lookup criteria.
var ErrUserNotFound = errors.New("authserver: user not found")

// ErrNoMembership is returned when a user has no row in tenant_memberships.
// This blocks login: every user must belong to at least one tenant.
var ErrNoMembership = errors.New("authserver: user has no tenant membership")

// ErrTenantDomainNotFound is returned when no enabled tenant claims the given email domain.
var ErrTenantDomainNotFound = errors.New("authserver: no tenant for email domain")

// TenantLookupResult is the public payload returned by the email-domain lookup endpoint.
type TenantLookupResult struct {
	Slug          string `json:"slug"`
	DisplayName   string `json:"display_name"`
	IDPConfigured bool   `json:"idp_configured"`
}

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

	// GetTenantMembership returns the tenant_id and role for the given user.
	// When tenantSlug is non-empty, the membership for that specific tenant is
	// returned; otherwise the first membership row (arbitrary order) is used.
	// Returns ErrNoMembership when no matching row exists.
	GetTenantMembership(ctx context.Context, userID int64, tenantSlug string) (tenantID, role string, err error)

	// TouchLastLogin sets users.last_login_at = now for the given user. Best
	// effort — errors are logged by the caller but do not fail login.
	TouchLastLogin(ctx context.Context, id int64) error

	// InsertSession records an issued access token hash + expiry. Best effort.
	InsertSession(ctx context.Context, userID int64, accessTokenHash string, expiresAt time.Time) error

	// IsBlacklisted reports whether tokenHash is present and not yet expired.
	IsBlacklisted(ctx context.Context, tokenHash string) (bool, error)
	// Blacklist inserts tokenHash with an expiry (idempotent).
	Blacklist(ctx context.Context, tokenHash string, expiresAt time.Time) error

	// GetPreferences returns the preferences JSON blob for the given user ID.
	// Returns an empty object when no preferences have been saved yet.
	GetPreferences(ctx context.Context, userID int64) ([]byte, error)
	// SetPreferences replaces the full preferences blob for the given user ID.
	SetPreferences(ctx context.Context, userID int64, prefs []byte) error

	// LookupTenantByEmailDomain finds an enabled tenant by its registered email domain.
	// The domain comparison is case-insensitive. Returns ErrTenantDomainNotFound when
	// no tenant claims the domain.
	LookupTenantByEmailDomain(ctx context.Context, domain string) (TenantLookupResult, error)

	// Ping checks database reachability for readiness probes.
	Ping(ctx context.Context) error
}
