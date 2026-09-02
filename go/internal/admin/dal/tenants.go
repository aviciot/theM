package dal

import (
	"context"
	"encoding/json"
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

// ── Tenant PATCH types ────────────────────────────────────────────────────────

// TenantIDPConfig holds per-tenant OIDC provider settings stored in them.tenants.idp_config.
// ClientSecret is write-only — it is never returned in API responses.
type TenantIDPConfig struct {
	DiscoveryURL string `json:"discovery_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURI  string `json:"redirect_uri"`
}

// TenantPatch carries optional fields for PATCH /admin/tenants/{id}.
// SetIDP is true when the "idp_config" key was present in the JSON request body,
// allowing callers to distinguish "absent" from "explicit null" (which clears the config).
type TenantPatch struct {
	DisplayName *string         `json:"display_name,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	SetIDP      bool            `json:"-"`
	IDPConfig   *TenantIDPConfig `json:"idp_config"`
}

// UnmarshalJSON implements custom decoding so SetIDP is set whenever the
// "idp_config" key is present in the JSON body (even as null).
func (p *TenantPatch) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["display_name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			p.DisplayName = &s
		}
	}
	if v, ok := raw["enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err == nil {
			p.Enabled = &b
		}
	}
	if v, ok := raw["idp_config"]; ok {
		p.SetIDP = true
		if string(v) != "null" {
			var cfg TenantIDPConfig
			if err := json.Unmarshal(v, &cfg); err != nil {
				return err
			}
			p.IDPConfig = &cfg
		}
		// string(v) == "null" → SetIDP=true, IDPConfig=nil → clears the config
	}
	return nil
}

// TenantDetail extends Tenant with IdP configuration status.
type TenantDetail struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	DisplayName   string    `json:"display_name"`
	Enabled       bool      `json:"enabled"`
	IDPConfigured bool      `json:"idp_configured"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PatchTenant updates a tenant's display_name, enabled, and/or idp_config.
// When patch.SetIDP is false the idp_config column is left unchanged.
// When patch.SetIDP is true and patch.IDPConfig is nil the idp_config is set to NULL.
// Returns TenantDetail with IDPConfigured = true when idp_config IS NOT NULL.
func (d *DB) PatchTenant(ctx context.Context, id string, patch TenantPatch) (TenantDetail, error) {
	var idpJSON []byte
	if patch.SetIDP && patch.IDPConfig != nil {
		var err error
		idpJSON, err = json.Marshal(patch.IDPConfig)
		if err != nil {
			return TenantDetail{}, err
		}
	}
	const q = `
		UPDATE them.tenants
		SET
			display_name = COALESCE($2::text, display_name),
			enabled      = COALESCE($3::boolean, enabled),
			idp_config   = CASE WHEN $4 THEN $5::jsonb ELSE idp_config END,
			updated_at   = now()
		WHERE id = $1::uuid
		RETURNING id::text, slug, display_name, enabled,
		          idp_config IS NOT NULL AS idp_configured,
		          created_at, updated_at`
	var t TenantDetail
	var idpJSONArg interface{}
	if idpJSON != nil {
		idpJSONArg = string(idpJSON)
	}
	err := d.q.ExecReturning(ctx, q, id, patch.DisplayName, patch.Enabled, patch.SetIDP, idpJSONArg).
		Scan(&t.ID, &t.Slug, &t.DisplayName, &t.Enabled, &t.IDPConfigured, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}
