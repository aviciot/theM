package dal

import (
	"context"
	"time"
)

// ── Row types ──────────────────────────────────────────────────────────────

// RoleRow is a tenant_roles row.
type RoleRow struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GrantRow is a tenant_role_grants row.
type GrantRow struct {
	ID            string    `json:"id"`
	RoleID        string    `json:"role_id"`
	ApplicationID string    `json:"application_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// MappingRow is a tenant_role_mappings row.
type MappingRow struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Source    string    `json:"source"`
	Field     string    `json:"field"`
	Value     string    `json:"value"`
	RoleID    string    `json:"role_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ── Role CRUD ──────────────────────────────────────────────────────────────

func (d *DB) ListRoles(ctx context.Context, tenantID string) ([]RoleRow, error) {
	rows, err := d.q.Query(ctx, `
		SELECT id::text, tenant_id::text, name, display_name, description, created_at, updated_at
		FROM them.tenant_roles
		WHERE tenant_id = $1
		ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleRow
	for rows.Next() {
		var r RoleRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	if out == nil {
		out = []RoleRow{}
	}
	return out, nil
}

func (d *DB) CreateRole(ctx context.Context, tenantID, name, displayName, description string) (RoleRow, error) {
	var r RoleRow
	err := d.q.QueryRow(ctx, `
		INSERT INTO them.tenant_roles (tenant_id, name, display_name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, tenant_id::text, name, display_name, description, created_at, updated_at
	`, tenantID, name, displayName, description).
		Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (d *DB) GetRole(ctx context.Context, tenantID, roleID string) (RoleRow, error) {
	var r RoleRow
	err := d.q.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, name, display_name, description, created_at, updated_at
		FROM them.tenant_roles
		WHERE tenant_id = $1 AND id = $2::uuid
	`, tenantID, roleID).
		Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (d *DB) UpdateRole(ctx context.Context, tenantID, roleID, name, displayName, description string) (RoleRow, error) {
	var r RoleRow
	err := d.q.QueryRow(ctx, `
		UPDATE them.tenant_roles
		SET name=$3, display_name=$4, description=$5, updated_at=now()
		WHERE tenant_id=$1 AND id=$2::uuid
		RETURNING id::text, tenant_id::text, name, display_name, description, created_at, updated_at
	`, tenantID, roleID, name, displayName, description).
		Scan(&r.ID, &r.TenantID, &r.Name, &r.DisplayName, &r.Description, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (d *DB) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	return d.q.Exec(ctx, `
		DELETE FROM them.tenant_roles
		WHERE tenant_id = $1 AND id = $2::uuid
	`, tenantID, roleID)
}

// ── Grants ─────────────────────────────────────────────────────────────────

func (d *DB) ListGrants(ctx context.Context, tenantID, roleID string) ([]GrantRow, error) {
	rows, err := d.q.Query(ctx, `
		SELECT g.id::text, g.role_id::text, g.application_id::text, g.created_at
		FROM them.tenant_role_grants g
		JOIN them.tenant_roles r ON r.id = g.role_id
		WHERE r.tenant_id = $1 AND g.role_id = $2::uuid
		ORDER BY g.created_at
	`, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GrantRow
	for rows.Next() {
		var g GrantRow
		if err := rows.Scan(&g.ID, &g.RoleID, &g.ApplicationID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	rows.Close()
	if out == nil {
		out = []GrantRow{}
	}
	return out, nil
}

func (d *DB) AddGrant(ctx context.Context, tenantID, roleID, applicationID string) (GrantRow, error) {
	var g GrantRow
	err := d.q.QueryRow(ctx, `
		INSERT INTO them.tenant_role_grants (role_id, application_id)
		SELECT $2::uuid, $3::uuid
		FROM them.tenant_roles
		WHERE id = $2::uuid AND tenant_id = $1
		RETURNING id::text, role_id::text, application_id::text, created_at
	`, tenantID, roleID, applicationID).
		Scan(&g.ID, &g.RoleID, &g.ApplicationID, &g.CreatedAt)
	return g, err
}

func (d *DB) DeleteGrant(ctx context.Context, tenantID, roleID, grantID string) error {
	return d.q.Exec(ctx, `
		DELETE FROM them.tenant_role_grants g
		USING them.tenant_roles r
		WHERE g.id = $3::uuid AND g.role_id = $2::uuid AND r.id = g.role_id AND r.tenant_id = $1
	`, tenantID, roleID, grantID)
}

// ── Mappings ───────────────────────────────────────────────────────────────

func (d *DB) ListRoleMappings(ctx context.Context, tenantID, roleID string) ([]MappingRow, error) {
	rows, err := d.q.Query(ctx, `
		SELECT id::text, tenant_id::text, source, field, value, role_id::text, created_at
		FROM them.tenant_role_mappings
		WHERE tenant_id = $1 AND role_id = $2::uuid
		ORDER BY created_at
	`, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MappingRow
	for rows.Next() {
		var m MappingRow
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Source, &m.Field, &m.Value, &m.RoleID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if out == nil {
		out = []MappingRow{}
	}
	return out, nil
}

func (d *DB) AddRoleMapping(ctx context.Context, tenantID, roleID, source, field, value string) (MappingRow, error) {
	var m MappingRow
	err := d.q.QueryRow(ctx, `
		INSERT INTO them.tenant_role_mappings (tenant_id, source, field, value, role_id)
		SELECT $1, $3, $4, $5, $2::uuid
		FROM them.tenant_roles
		WHERE id = $2::uuid AND tenant_id = $1
		RETURNING id::text, tenant_id::text, source, field, value, role_id::text, created_at
	`, tenantID, roleID, source, field, value).
		Scan(&m.ID, &m.TenantID, &m.Source, &m.Field, &m.Value, &m.RoleID, &m.CreatedAt)
	return m, err
}

func (d *DB) DeleteRoleMapping(ctx context.Context, tenantID, roleID, mappingID string) error {
	return d.q.Exec(ctx, `
		DELETE FROM them.tenant_role_mappings
		WHERE id = $3::uuid AND role_id = $2::uuid AND tenant_id = $1
	`, tenantID, roleID, mappingID)
}
