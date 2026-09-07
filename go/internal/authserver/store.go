package authserver

import (
	"context"
	"errors"
	"time"
)

// ErrUserNotFound is returned when no active user matches the lookup criteria.
var ErrUserNotFound = errors.New("authserver: user not found")

// ErrUserConflict is returned when a username or email is already taken.
var ErrUserConflict = errors.New("authserver: username or email already exists")

// ErrEWrongTokenType sentinel re-used for admin context (token not access type).
// Existing ErrWrongTokenType in jwt.go covers this case.

// ErrNoMembership is returned when a user has no row in tenant_memberships.
// This blocks login: every user must belong to at least one tenant.
var ErrNoMembership = errors.New("authserver: user has no tenant membership")

// ErrTenantDomainNotFound is returned when no enabled tenant claims the given email domain.
var ErrTenantDomainNotFound = errors.New("authserver: no tenant for email domain")

// ErrInvalidRole is returned when an unsupported membership role is provided.
var ErrInvalidRole = errors.New("authserver: invalid membership role")

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

	// GetTenantMembershipByID returns the tenant_id and role for a specific tenant UUID.
	// Used by Refresh to re-validate the tenant selected at login.
	// Returns ErrNoMembership when the membership has been revoked.
	GetTenantMembershipByID(ctx context.Context, userID int64, tenantID string) (tenantID2, role string, err error)

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

	// ── User management (super_admin only) ───────────────────────────────────

	// ListUsers returns all users with their current tenant membership (if any).
	// Results are ordered by id ascending.
	ListUsers(ctx context.Context) ([]ManagedUser, error)
	// GetManagedUser returns a single user by id.
	GetManagedUser(ctx context.Context, id int64) (*ManagedUser, error)
	// CreateUser inserts a new user and optionally assigns a tenant membership.
	// The password is expected to already be bcrypt-hashed.
	CreateUser(ctx context.Context, in UserCreateInput) (*ManagedUser, error)
	// UpdateUser patches mutable fields (name, email, active) for the given user id.
	UpdateUser(ctx context.Context, id int64, in UserUpdateInput) (*ManagedUser, error)
	// DeleteUser removes the user (hard delete — also cascades sessions/memberships).
	DeleteUser(ctx context.Context, id int64) error
	// ResetPassword replaces the bcrypt hash for the given user.
	// The new hash is expected to already be bcrypt-hashed.
	ResetPassword(ctx context.Context, id int64, newHash string) error
	// ListTenants returns id+slug+display_name for all tenants (for the assignment UI).
	ListTenants(ctx context.Context) ([]TenantSummary, error)
}

// ManagedUser is the public view of a user returned by admin endpoints.
// PasswordHash is NEVER included.
type ManagedUser struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email,omitempty"`
	Role        string     `json:"role"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	TenantID    string     `json:"tenant_id,omitempty"`
	TenantSlug  string     `json:"tenant_slug,omitempty"`
	TenantRole  string     `json:"tenant_role,omitempty"`
}

// UserCreateInput carries validated fields for creating a new user.
// Password must be bcrypt-hashed by the caller before passing here.
type UserCreateInput struct {
	Username     string
	Name         string
	Email        string
	PasswordHash string
	RoleName     string // e.g. "super_admin", "viewer" — looked up by name
	TenantID     string // UUID; empty = no membership row
	TenantRole   string // e.g. "admin", "member"
}

// UserUpdateInput carries the patchable fields. nil = leave unchanged.
type UserUpdateInput struct {
	Name       *string
	Email      *string
	Active     *bool
	TenantRole *string // membership role (admin/member/viewer); updates tenant_memberships
}

// TenantSummary is a lightweight tenant descriptor for dropdown UIs.
type TenantSummary struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}
