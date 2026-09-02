package authserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoIDPConfig is returned when a tenant has no idp_config set.
var ErrNoIDPConfig = errors.New("authserver: tenant has no OIDC IdP configuration")

// ErrTenantNotFound is returned when no tenant matches the given slug.
var ErrTenantNotFound = errors.New("authserver: tenant not found")

// IDPConfig holds per-tenant OIDC provider settings stored in them.tenants.idp_config.
type IDPConfig struct {
	// DiscoveryURL is the OIDC provider's well-known discovery document base URL
	// (without /.well-known/openid-configuration suffix).
	DiscoveryURL string `json:"discovery_url"`
	// ClientID is the OAuth2 client ID registered with the IdP.
	ClientID string `json:"client_id"`
	// ClientSecret is the OAuth2 client secret. Never log this value.
	ClientSecret string `json:"client_secret"`
	// RedirectURI is the registered callback URI (must match what is registered at the IdP).
	RedirectURI string `json:"redirect_uri"`
}

// OIDCStore extends the auth Store with OIDC-specific operations.
// It is a separate interface so the mock in tests can implement only what is needed.
type OIDCStore interface {
	// GetTenantIDPConfig returns the IdP config for the given tenant slug.
	// Returns ErrTenantNotFound when no tenant matches, ErrNoIDPConfig when the
	// tenant exists but has no idp_config set.
	GetTenantIDPConfig(ctx context.Context, slug string) (tenantID string, cfg *IDPConfig, err error)

	// UpsertOIDCUser finds an existing user by email within a tenant, or creates a
	// new auth_service.users row and a tenant_memberships row, then returns the user
	// record. This is best-effort idempotent: if the user already exists the
	// existing record is returned unchanged.
	UpsertOIDCUser(ctx context.Context, tenantID, email, name string) (*userRecord, error)
}

// pgxOIDCStore is the PostgreSQL-backed OIDCStore.
type pgxOIDCStore struct {
	pool *pgxpool.Pool
}

// NewPgxOIDCStore builds an OIDCStore over the given pgx pool.
func NewPgxOIDCStore(pool *pgxpool.Pool) OIDCStore {
	return &pgxOIDCStore{pool: pool}
}

func (s *pgxOIDCStore) GetTenantIDPConfig(ctx context.Context, slug string) (string, *IDPConfig, error) {
	const q = `
		SELECT id::text, idp_config
		FROM   them.tenants
		WHERE  slug = $1 AND enabled = true
		LIMIT  1`
	var tenantID string
	var raw []byte
	err := s.pool.QueryRow(ctx, q, slug).Scan(&tenantID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrTenantNotFound
	}
	if err != nil {
		return "", nil, err
	}
	if raw == nil {
		return "", nil, ErrNoIDPConfig
	}
	var cfg IDPConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", nil, ErrNoIDPConfig
	}
	if cfg.DiscoveryURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return "", nil, ErrNoIDPConfig
	}
	return tenantID, &cfg, nil
}

func (s *pgxOIDCStore) UpsertOIDCUser(ctx context.Context, tenantID, email, name string) (*userRecord, error) {
	// Use the same role for all OIDC-provisioned users. This can be refined later
	// with per-tenant role mapping stored in idp_config.
	const defaultRole = "viewer"

	// Look up the default role ID.
	var roleID int
	var roleDashboard string
	err := s.pool.QueryRow(ctx,
		`SELECT id, COALESCE(dashboard_access,'none') FROM auth_service.roles WHERE name = $1 LIMIT 1`,
		defaultRole,
	).Scan(&roleID, &roleDashboard)
	if errors.Is(err, pgx.ErrNoRows) {
		// Fallback: any role will do — pick the first one.
		if err2 := s.pool.QueryRow(ctx,
			`SELECT id, COALESCE(dashboard_access,'none') FROM auth_service.roles ORDER BY id LIMIT 1`,
		).Scan(&roleID, &roleDashboard); err2 != nil {
			return nil, err2
		}
	} else if err != nil {
		return nil, err
	}

	// Upsert the user row. email is the external identity anchor.
	// Username defaults to the email (truncated to 150 chars to stay within any UI limits).
	username := email
	if len(username) > 150 {
		username = username[:150]
	}
	if name == "" {
		name = email
	}

	var userID int64
	var username2, name2, roleStr, dashAccess string
	// ON CONFLICT (email) handles idempotent upsert. The username unique constraint
	// is satisfied on first insert; subsequent logins match via email.
	err = s.pool.QueryRow(ctx, `
		INSERT INTO auth_service.users (username, name, email, role_id, active)
		VALUES ($1, $2, $3, $4, true)
		ON CONFLICT (email) DO UPDATE
		    SET name       = EXCLUDED.name,
		        active     = true,
		        updated_at = CURRENT_TIMESTAMP
		RETURNING id, username, name, role_id`,
		username, name, email, roleID,
	).Scan(&userID, &username2, &name2, &roleID)
	if err != nil {
		return nil, err
	}

	// Re-read the role details (dashboard_access may differ from what we had).
	if err := s.pool.QueryRow(ctx,
		`SELECT name, COALESCE(dashboard_access,'none') FROM auth_service.roles WHERE id = $1`,
		roleID,
	).Scan(&roleStr, &dashAccess); err != nil {
		roleStr, dashAccess = defaultRole, "none"
	}

	// Upsert tenant membership. Role stored in the membership is the canonical
	// per-tenant role going forward.
	var memberRole string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
		VALUES ($1, $2::uuid, $3)
		ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING role`,
		userID, tenantID, roleStr,
	).Scan(&memberRole)
	if err != nil {
		return nil, err
	}

	u := &userRecord{
		ID:              userID,
		Username:        username2,
		Name:            name2,
		Email:           email,
		Role:            memberRole,
		DashboardAccess: dashAccess,
	}
	return u, nil
}
