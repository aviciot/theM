package authserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/idpcrypto"
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
	// GroupsClaim is the OIDC claim name that carries group values. Defaults to "groups"
	// when empty. Must be present in the validated ID token payload.
	GroupsClaim string `json:"groups_claim,omitempty"`
	// UnmatchedAction controls login behaviour when no group mapping matches.
	// "viewer" (default when empty) — proceed with viewer role.
	// "deny"   — reject the login with 403; no session or tokens are issued.
	UnmatchedAction string `json:"unmatched_action,omitempty"`
}

// groupsClaimName returns the effective claim name for group values, defaulting to "groups".
func (c *IDPConfig) groupsClaimName() string {
	if c.GroupsClaim != "" {
		return c.GroupsClaim
	}
	return "groups"
}

// unmatchedDeny returns true when the tenant requires a group match to log in.
func (c *IDPConfig) unmatchedDeny() bool {
	return c.UnmatchedAction == "deny"
}

// OIDCDebugRecord stores the sanitised outcome of the most recent SSO login for
// a given (tenantID, email) pair. Written at callback time, TTL 24h, read by
// tenant admins via GET /tenant/oidc-debug?email=... Diagnostic failures never
// block login.
type OIDCDebugRecord struct {
	Email         string    `json:"email"`
	GroupsReceived []string `json:"groups_received"`
	// MatchedGroup is the group_claim value that produced the role (empty when none matched).
	MatchedGroup string `json:"matched_group,omitempty"`
	// MatchedRole is the role the matching produced (empty = unmatched / default viewer / denied).
	MatchedRole string `json:"matched_role,omitempty"`
	// Outcome is one of: "matched", "unmatched_viewer", "unmatched_denied", "lookup_error".
	Outcome string `json:"outcome"`
	LoginAt time.Time `json:"login_at"`
}

const oidcDebugTTL = 24 * time.Hour

func oidcDebugKey(tenantID, email string) string {
	return "them:oidc:last_login:" + tenantID + ":" + email
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
	UpsertOIDCUser(ctx context.Context, tenantID, email, name, role string) (*userRecord, error)

	// GetGroupRole returns the tenant role mapped to the highest-priority group
	// (lowest priority integer wins) that appears in the groups slice.
	// Returns ("", false, nil) when no mapping matches — caller should use the
	// default role. Returns an error only on DB failure.
	GetGroupRole(ctx context.Context, tenantID string, groups []string) (role string, found bool, err error)

	// WriteOIDCDebug writes a sanitised login outcome record to Redis (24h TTL).
	// Failure is non-fatal and must never block login.
	WriteOIDCDebug(ctx context.Context, tenantID string, rec OIDCDebugRecord) error

	// GetOIDCDebug reads the stored login outcome for (tenantID, email).
	// Returns nil when no record exists (key expired or never written).
	GetOIDCDebug(ctx context.Context, tenantID, email string) (*OIDCDebugRecord, error)
}

// pgxOIDCStore is the PostgreSQL + Redis-backed OIDCStore.
type pgxOIDCStore struct {
	pool   *pgxpool.Pool
	idpKey []byte       // AES-256 key for client_secret decryption; nil = pass-through
	redis  rueidis.Client // nil when Redis unavailable (debug writes are skipped)
}

// NewPgxOIDCStore builds an OIDCStore over the given pgx pool.
// Call WithIDPKey to enable client_secret decryption.
func NewPgxOIDCStore(pool *pgxpool.Pool) OIDCStore {
	return &pgxOIDCStore{pool: pool}
}

// NewPgxOIDCStoreWithKey builds an OIDCStore that decrypts client_secret values
// written by PatchTenant when IDP_ENCRYPTION_KEY was set.
func NewPgxOIDCStoreWithKey(pool *pgxpool.Pool, key []byte) OIDCStore {
	return &pgxOIDCStore{pool: pool, idpKey: key}
}

// NewPgxOIDCStoreWithKeyAndRedis builds an OIDCStore with both client_secret
// decryption and Redis-backed debug log support.
func NewPgxOIDCStoreWithKeyAndRedis(pool *pgxpool.Pool, key []byte, rc rueidis.Client) OIDCStore {
	return &pgxOIDCStore{pool: pool, idpKey: key, redis: rc}
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
	// Decrypt client_secret when an encryption key is configured. Legacy plaintext
	// values (no "enc:" prefix) pass through unchanged so existing configs keep working.
	if s.idpKey != nil {
		dec, err := idpcrypto.Decrypt(s.idpKey, cfg.ClientSecret)
		if err != nil {
			return "", nil, ErrNoIDPConfig
		}
		cfg.ClientSecret = dec
	}
	return tenantID, &cfg, nil
}

// validMemberRoles is the set of allowed tenant membership roles for OIDC users.
// super_admin is intentionally excluded — OIDC group mappings must not elevate
// a user to platform super_admin via UpsertOIDCUser.
var validMemberRoles = map[string]bool{
	"admin":  true,
	"member": true,
	"viewer": true,
}

func (s *pgxOIDCStore) UpsertOIDCUser(ctx context.Context, tenantID, email, name, role string) (*userRecord, error) {
	// Validate and default the membership role. super_admin or unknown → viewer.
	if !validMemberRoles[role] {
		role = "viewer"
	}

	// Platform role for OIDC users is always "viewer" — group mappings only
	// control the tenant membership role, never the platform role.
	const platformRole = "viewer"

	// Wrap all queries in a single transaction for atomicity.
	// A concurrent OIDC login for the same email could otherwise interleave
	// user upsert and membership upsert, producing an orphaned membership row.
	pgTx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = pgTx.Rollback(cleanupCtx) }()

	// Look up the role ID for the platform role ("viewer" — never the tenant role).
	var roleID int
	var roleDashboard string
	err = pgTx.QueryRow(ctx,
		`SELECT id, COALESCE(dashboard_access,'none') FROM auth_service.roles WHERE name = $1 LIMIT 1`,
		platformRole,
	).Scan(&roleID, &roleDashboard)
	if errors.Is(err, pgx.ErrNoRows) {
		// Last-resort: any role.
		if err2 := pgTx.QueryRow(ctx,
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
	var username2, name2 string
	// ON CONFLICT (email) handles idempotent upsert. The username unique constraint
	// is satisfied on first insert; subsequent logins match via email.
	err = pgTx.QueryRow(ctx, `
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

	// Re-read the platform role name + dashboard_access (we looked up by ID above).
	var roleStr, dashAccess string
	if err := pgTx.QueryRow(ctx,
		`SELECT name, COALESCE(dashboard_access,'none') FROM auth_service.roles WHERE id = $1`,
		roleID,
	).Scan(&roleStr, &dashAccess); err != nil {
		roleStr, dashAccess = platformRole, "none"
	}

	// Upsert tenant membership using the validated tenant membership role (not the
	// platform role). This is the canonical per-tenant role going forward.
	var memberRole string
	err = pgTx.QueryRow(ctx, `
		INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
		VALUES ($1, $2::uuid, $3)
		ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = EXCLUDED.role
		RETURNING role`,
		userID, tenantID, role,
	).Scan(&memberRole)
	if err != nil {
		return nil, err
	}

	if err := pgTx.Commit(ctx); err != nil {
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

// GetGroupRole returns the role from the highest-priority tenant group mapping
// that matches any of the provided group claim values.
// Priority is ascending (0 = highest priority). When multiple groups match at
// the same priority, the one with the lowest group_claim (alphabetically) wins.
// Returns ("", false, nil) when no mapping matches any of the groups.
func (s *pgxOIDCStore) GetGroupRole(ctx context.Context, tenantID string, groups []string) (string, bool, error) {
	if len(groups) == 0 {
		return "", false, nil
	}
	const q = `
		SELECT role
		FROM them.tenant_group_mappings
		WHERE tenant_id = $1::uuid
		  AND group_claim = ANY($2)
		ORDER BY priority ASC, group_claim ASC
		LIMIT 1`
	var role string
	err := s.pool.QueryRow(ctx, q, tenantID, groups).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

// WriteOIDCDebug stores a sanitised login outcome in Redis (24h TTL).
// Silently skips when Redis is not configured.
func (s *pgxOIDCStore) WriteOIDCDebug(ctx context.Context, tenantID string, rec OIDCDebugRecord) error {
	if s.redis == nil {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := oidcDebugKey(tenantID, rec.Email)
	cmd := s.redis.B().Set().Key(key).Value(string(data)).Ex(oidcDebugTTL).Build()
	return s.redis.Do(ctx, cmd).Error()
}

// GetOIDCDebug retrieves the stored login outcome for (tenantID, email).
// Returns nil when no record exists.
func (s *pgxOIDCStore) GetOIDCDebug(ctx context.Context, tenantID, email string) (*OIDCDebugRecord, error) {
	if s.redis == nil {
		return nil, nil
	}
	key := oidcDebugKey(tenantID, email)
	cmd := s.redis.B().Get().Key(key).Build()
	res := s.redis.Do(ctx, cmd)
	if err := res.Error(); err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	raw, err := res.AsBytes()
	if err != nil {
		return nil, err
	}
	var rec OIDCDebugRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
