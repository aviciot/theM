package authserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxStore is the PostgreSQL-backed Store implementation. All SQL lives here;
// the service and handler layers never embed queries. It reads the auth_service
// schema in the them database — the same tables the Python auth service owns.
type pgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore builds a Store over the given pgx pool.
func NewPgxStore(pool *pgxpool.Pool) Store {
	return &pgxStore{pool: pool}
}

// userSelect is the shared projection joining users → roles. dashboard_access and
// token_expiry come from the role. COALESCE guards nullable columns so scans into
// non-pointer Go strings/ints never fail.
const userSelect = `
	SELECT u.id,
	       u.username,
	       u.name,
	       COALESCE(u.email, '')            AS email,
	       r.name                            AS role,
	       COALESCE(u.password_hash, '')     AS password_hash,
	       COALESCE(r.dashboard_access, 'none') AS dashboard_access,
	       COALESCE(r.token_expiry, 0)       AS token_expiry
	FROM auth_service.users u
	JOIN auth_service.roles r ON u.role_id = r.id
`

func scanUser(row pgx.Row) (*userRecord, error) {
	var u userRecord
	err := row.Scan(
		&u.ID, &u.Username, &u.Name, &u.Email, &u.Role,
		&u.PasswordHash, &u.DashboardAccess, &u.TokenExpiry,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *pgxStore) GetUserByLogin(ctx context.Context, login string) (*userRecord, error) {
	// Python tries email first, then username. A single query with OR + a
	// deterministic preference (exact email match first) reproduces that.
	const q = userSelect + `
		WHERE u.active = true AND (u.email = $1 OR u.username = $1)
		ORDER BY (u.email = $1) DESC
		LIMIT 1`
	return scanUser(s.pool.QueryRow(ctx, q, login))
}

func (s *pgxStore) GetUserByAPIKeyHash(ctx context.Context, apiKeyHash string) (*userRecord, error) {
	const q = userSelect + `WHERE u.active = true AND u.api_key_hash = $1 LIMIT 1`
	return scanUser(s.pool.QueryRow(ctx, q, apiKeyHash))
}

func (s *pgxStore) GetUserByID(ctx context.Context, id int64) (*userRecord, error) {
	const q = userSelect + `WHERE u.active = true AND u.id = $1 LIMIT 1`
	return scanUser(s.pool.QueryRow(ctx, q, id))
}

func (s *pgxStore) GetTenantMembership(ctx context.Context, userID int64, tenantSlug string) (string, string, error) {
	var tenantID, role string
	var err error
	if tenantSlug != "" {
		const q = `
			SELECT tm.tenant_id::text, tm.role
			FROM   auth_service.tenant_memberships tm
			JOIN   them.tenants t ON t.id = tm.tenant_id
			WHERE  tm.user_id = $1 AND t.slug = $2 AND t.enabled = true
			LIMIT  1`
		err = s.pool.QueryRow(ctx, q, userID, tenantSlug).Scan(&tenantID, &role)
	} else {
		const q = `
			SELECT tenant_id::text, role
			FROM   auth_service.tenant_memberships
			WHERE  user_id = $1
			LIMIT  1`
		err = s.pool.QueryRow(ctx, q, userID).Scan(&tenantID, &role)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNoMembership
	}
	if err != nil {
		return "", "", err
	}
	return tenantID, role, nil
}

func (s *pgxStore) GetTenantMembershipByID(ctx context.Context, userID int64, tenantID string) (string, string, error) {
	const q = `
		SELECT tenant_id::text, role
		FROM   auth_service.tenant_memberships
		WHERE  user_id = $1 AND tenant_id = $2::uuid
		LIMIT  1`
	var tid, role string
	err := s.pool.QueryRow(ctx, q, userID, tenantID).Scan(&tid, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNoMembership
	}
	if err != nil {
		return "", "", err
	}
	return tid, role, nil
}

func (s *pgxStore) TouchLastLogin(ctx context.Context, id int64) error {
	const q = `UPDATE auth_service.users SET last_login_at = CURRENT_TIMESTAMP WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id)
	return err
}

// InsertSession records the issued access+refresh token hashes. Both columns are
// NOT NULL in the schema, so both hashes are supplied at login time. Best effort:
// the caller treats failure as non-fatal (matches Python behaviour).
func (s *pgxStore) InsertSession(ctx context.Context, userID int64, accessTokenHash string, expiresAt time.Time) error {
	// refresh_token_hash is required NOT NULL/UNIQUE; when only the access hash is
	// known we still need a unique value. Prefix-namespace it to avoid collisions
	// with real refresh hashes while satisfying the constraint.
	const q = `
		INSERT INTO auth_service.user_sessions (user_id, access_token_hash, refresh_token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`
	_, err := s.pool.Exec(ctx, q, userID, accessTokenHash, "pending:"+accessTokenHash, expiresAt)
	return err
}

func (s *pgxStore) IsBlacklisted(ctx context.Context, tokenHash string) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1 FROM auth_service.blacklisted_tokens
			WHERE token_hash = $1 AND expires_at > CURRENT_TIMESTAMP
		)`
	var exists bool
	if err := s.pool.QueryRow(ctx, q, tokenHash).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *pgxStore) Blacklist(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO auth_service.blacklisted_tokens (token_hash, expires_at)
		VALUES ($1, $2)
		ON CONFLICT (token_hash) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, tokenHash, expiresAt)
	return err
}

func (s *pgxStore) GetPreferences(ctx context.Context, userID int64) ([]byte, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(preferences, '{}')::text FROM auth_service.users WHERE id = $1`,
		userID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *pgxStore) SetPreferences(ctx context.Context, userID int64, prefs []byte) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE auth_service.users SET preferences = $2::jsonb, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		userID, string(prefs),
	)
	return err
}

func (s *pgxStore) LookupTenantByEmailDomain(ctx context.Context, domain string) (TenantLookupResult, error) {
	const q = `
		SELECT slug, display_name, idp_config IS NOT NULL AS idp_configured
		FROM them.tenants
		WHERE email_domain = lower($1) AND enabled = true
		LIMIT 1`
	var r TenantLookupResult
	err := s.pool.QueryRow(ctx, q, domain).Scan(&r.Slug, &r.DisplayName, &r.IDPConfigured)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantLookupResult{}, ErrTenantDomainNotFound
	}
	if err != nil {
		return TenantLookupResult{}, err
	}
	return r, nil
}

func (s *pgxStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// ── User management queries ───────────────────────────────────────────────────

const managedUserSelect = `
	SELECT u.id, u.username, u.name, COALESCE(u.email, '') AS email,
	       r.name AS role, u.active, u.created_at, u.last_login_at,
	       COALESCE(tm.tenant_id::text, '') AS tenant_id,
	       COALESCE(t.slug, '') AS tenant_slug,
	       COALESCE(tm.role, '') AS tenant_role
	FROM auth_service.users u
	JOIN auth_service.roles r ON u.role_id = r.id
	LEFT JOIN auth_service.tenant_memberships tm ON tm.user_id = u.id
	LEFT JOIN them.tenants t ON t.id = tm.tenant_id
`

func scanManagedUser(row pgx.Row) (*ManagedUser, error) {
	var u ManagedUser
	var lastLogin *time.Time
	err := row.Scan(
		&u.ID, &u.Username, &u.Name, &u.Email, &u.Role, &u.Active,
		&u.CreatedAt, &lastLogin, &u.TenantID, &u.TenantSlug, &u.TenantRole,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	u.LastLoginAt = lastLogin
	return &u, nil
}

func (s *pgxStore) ListUsers(ctx context.Context) ([]ManagedUser, error) {
	const q = managedUserSelect + `ORDER BY u.id ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedUser
	for rows.Next() {
		var u ManagedUser
		var lastLogin *time.Time
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Name, &u.Email, &u.Role, &u.Active,
			&u.CreatedAt, &lastLogin, &u.TenantID, &u.TenantSlug, &u.TenantRole,
		); err != nil {
			return nil, err
		}
		u.LastLoginAt = lastLogin
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ManagedUser{}
	}
	return out, nil
}

func (s *pgxStore) GetManagedUser(ctx context.Context, id int64) (*ManagedUser, error) {
	const q = managedUserSelect + `WHERE u.id = $1`
	return scanManagedUser(s.pool.QueryRow(ctx, q, id))
}

func (s *pgxStore) CreateUser(ctx context.Context, in UserCreateInput) (*ManagedUser, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const insertUser = `
		INSERT INTO auth_service.users (username, name, email, role_id, password_hash, active)
		SELECT $1, $2, NULLIF($3,''), r.id, $4, true
		FROM auth_service.roles r WHERE r.name = $5
		RETURNING id`
	var newID int64
	err = tx.QueryRow(ctx, insertUser, in.Username, in.Name, in.Email, in.PasswordHash, in.RoleName).Scan(&newID)
	if err != nil {
		// Unique constraint violation → conflict.
		if isUniqueViolation(err) {
			return nil, ErrUserConflict
		}
		return nil, err
	}

	if in.TenantID != "" && in.TenantRole != "" {
		const insertMembership = `
			INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
			VALUES ($1, $2::uuid, $3)
			ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = EXCLUDED.role`
		if _, err := tx.Exec(ctx, insertMembership, newID, in.TenantID, in.TenantRole); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetManagedUser(ctx, newID)
}

func (s *pgxStore) UpdateUser(ctx context.Context, id int64, in UserUpdateInput) (*ManagedUser, error) {
	if in.Name == nil && in.Email == nil && in.Active == nil {
		return s.GetManagedUser(ctx, id)
	}
	const q = `
		UPDATE auth_service.users
		SET name         = COALESCE($2, name),
		    email        = CASE WHEN $3::text IS NULL THEN email ELSE NULLIF($3,'') END,
		    active       = COALESCE($4, active),
		    updated_at   = CURRENT_TIMESTAMP
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, in.Name, in.Email, in.Active)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserConflict
		}
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrUserNotFound
	}
	return s.GetManagedUser(ctx, id)
}

func (s *pgxStore) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM auth_service.users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *pgxStore) ResetPassword(ctx context.Context, id int64, newHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE auth_service.users SET password_hash=$2, updated_at=CURRENT_TIMESTAMP WHERE id=$1`,
		id, newHash,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *pgxStore) ListTenants(ctx context.Context) ([]TenantSummary, error) {
	const q = `SELECT id::text, slug, display_name FROM them.tenants WHERE enabled=true ORDER BY slug ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TenantSummary
	for rows.Next() {
		var t TenantSummary
		if err := rows.Scan(&t.ID, &t.Slug, &t.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []TenantSummary{}
	}
	return out, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique_violation (23505).
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "23505") ||
		strings.Contains(err.Error(), "unique_violation") ||
		strings.Contains(err.Error(), "duplicate key")
}
