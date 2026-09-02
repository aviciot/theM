package dal

import (
	"context"
	"time"
)

// Tenant is a row from them.tenants.
type Tenant struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TenantInput is the request body for creating a tenant.
type TenantInput struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

// ListTenants returns all tenants ordered by created_at ascending.
func (d *DB) ListTenants(ctx context.Context) ([]Tenant, error) {
	const q = `
		SELECT id::text, slug, display_name, enabled, created_at, updated_at
		FROM them.tenants
		ORDER BY created_at ASC`
	rows, err := d.q.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []Tenant{}
	}
	return out, nil
}

// GetTenant returns a single tenant by ID, or pgx.ErrNoRows if not found.
func (d *DB) GetTenant(ctx context.Context, id string) (Tenant, error) {
	const q = `
		SELECT id::text, slug, display_name, enabled, created_at, updated_at
		FROM them.tenants
		WHERE id = $1::uuid`
	var t Tenant
	err := d.q.QueryRow(ctx, q, id).Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Enabled, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// CreateTenant inserts a new tenant and returns the created row.
// Returns a unique-violation error if slug already exists.
func (d *DB) CreateTenant(ctx context.Context, in TenantInput) (Tenant, error) {
	const q = `
		INSERT INTO them.tenants (slug, display_name)
		VALUES ($1, $2)
		RETURNING id::text, slug, display_name, enabled, created_at, updated_at`
	var t Tenant
	err := d.q.ExecReturning(ctx, q, in.Slug, in.DisplayName).
		Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Enabled, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}
