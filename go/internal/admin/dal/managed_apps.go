package dal

import (
	"context"
	"encoding/json"
	"time"
)

// ── Managed App types ─────────────────────────────────────────────────────────

// ManagedApp is a platform-owned application (app_type = 'managed').
type ManagedApp struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Version   string    `json:"version"`
	Changelog *string   `json:"changelog,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ManagedAppInput is the request body for creating a managed app.
type ManagedAppInput struct {
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Version   string  `json:"version,omitempty"`
	Changelog *string `json:"changelog,omitempty"`
}

// ManagedAppParam is one row from them.managed_app_params.
type ManagedAppParam struct {
	ID           string   `json:"id"`
	AppID        string   `json:"app_id"`
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Description  *string  `json:"description,omitempty"`
	ParamType    string   `json:"param_type"`
	EnumValues   []string `json:"enum_values,omitempty"`
	Required     bool     `json:"required"`
	DefaultValue *string  `json:"default_value,omitempty"`
	SortOrder    int      `json:"sort_order"`
}

// ManagedAppParamInput is the request body for setting a param in the manifest.
type ManagedAppParamInput struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Description  *string  `json:"description,omitempty"`
	ParamType    string   `json:"param_type"`
	EnumValues   []string `json:"enum_values,omitempty"`
	Required     bool     `json:"required"`
	DefaultValue *string  `json:"default_value,omitempty"`
	SortOrder    int      `json:"sort_order"`
}

// ManagedAppDetail extends ManagedApp with its parameter manifest.
type ManagedAppDetail struct {
	ManagedApp
	Params []ManagedAppParam `json:"params"`
}

// ManagedAppBinding is one row from them.managed_app_bindings.
type ManagedAppBinding struct {
	ID         string          `json:"id"`
	AppID      string          `json:"app_id"`
	TenantID   string          `json:"tenant_id"`
	Enabled    bool            `json:"enabled"`
	Config     json.RawMessage `json:"config"`
	AppVersion string          `json:"app_version"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// ManagedAppBindingInput is the request body for creating/updating a binding.
type ManagedAppBindingInput struct {
	Config     json.RawMessage `json:"config"`
	AppVersion string          `json:"app_version,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
}

// ── DAL methods ───────────────────────────────────────────────────────────────

// ListManagedApps returns all platform-owned apps ordered by created_at.
func (d *DB) ListManagedApps(ctx context.Context) ([]ManagedApp, error) {
	const q = `
		SELECT id::text, name, COALESCE(slug,''), COALESCE(version,'1.0.0'),
		       changelog, enabled, created_at, updated_at
		FROM them.applications
		WHERE app_type = 'managed'
		ORDER BY created_at ASC`
	rows, err := d.q.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedApp
	for rows.Next() {
		var a ManagedApp
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.Version, &a.Changelog,
			&a.Enabled, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []ManagedApp{}
	}
	return out, nil
}

// CreateManagedApp inserts a new platform-owned application (app_type='managed').
func (d *DB) CreateManagedApp(ctx context.Context, in ManagedAppInput) (ManagedApp, error) {
	ver := in.Version
	if ver == "" {
		ver = "1.0.0"
	}
	const q = `
		INSERT INTO them.applications (name, slug, app_type, version, changelog, enabled)
		VALUES ($1, $2, 'managed', $3, $4, true)
		RETURNING id::text, name, COALESCE(slug,''), COALESCE(version,'1.0.0'),
		          changelog, enabled, created_at, updated_at`
	var a ManagedApp
	err := d.q.ExecReturning(ctx, q, in.Name, in.Slug, ver, in.Changelog).
		Scan(&a.ID, &a.Name, &a.Slug, &a.Version, &a.Changelog, &a.Enabled, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// GetManagedApp returns a single managed app with its param manifest.
func (d *DB) GetManagedApp(ctx context.Context, id string) (ManagedAppDetail, error) {
	const q = `
		SELECT id::text, name, COALESCE(slug,''), COALESCE(version,'1.0.0'),
		       changelog, enabled, created_at, updated_at
		FROM them.applications
		WHERE id = $1::uuid AND app_type = 'managed'`
	var detail ManagedAppDetail
	err := d.q.QueryRow(ctx, q, id).Scan(
		&detail.ID, &detail.Name, &detail.Slug, &detail.Version,
		&detail.Changelog, &detail.Enabled, &detail.CreatedAt, &detail.UpdatedAt,
	)
	if err != nil {
		return detail, err
	}
	params, err := d.ListManagedAppParams(ctx, id)
	if err != nil {
		return detail, err
	}
	detail.Params = params
	return detail, nil
}

// ListManagedAppParams returns the parameter manifest for one managed app.
func (d *DB) ListManagedAppParams(ctx context.Context, appID string) ([]ManagedAppParam, error) {
	const q = `
		SELECT id::text, app_id::text, key, label, description,
		       param_type, COALESCE(enum_values, '{}'), required, default_value, sort_order
		FROM them.managed_app_params
		WHERE app_id = $1::uuid
		ORDER BY sort_order ASC, key ASC`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedAppParam
	for rows.Next() {
		var p ManagedAppParam
		var enumArr []string
		if err := rows.Scan(&p.ID, &p.AppID, &p.Key, &p.Label, &p.Description,
			&p.ParamType, &enumArr, &p.Required, &p.DefaultValue, &p.SortOrder); err != nil {
			return nil, err
		}
		p.EnumValues = enumArr
		out = append(out, p)
	}
	if out == nil {
		out = []ManagedAppParam{}
	}
	return out, nil
}

// UpsertManagedAppParams replaces all params for a managed app atomically.
// When d.pool is set (production), the DELETE+INSERT runs inside a transaction
// so a partial failure never leaves params in an inconsistent state.
func (d *DB) UpsertManagedAppParams(ctx context.Context, appID string, params []ManagedAppParamInput) error {
	if d.pool != nil {
		return runInTx(ctx, d.pool, func(q Querier) error {
			return upsertManagedAppParamsWithQ(ctx, q, appID, params)
		})
	}
	return upsertManagedAppParamsWithQ(ctx, d.q, appID, params)
}

func upsertManagedAppParamsWithQ(ctx context.Context, q Querier, appID string, params []ManagedAppParamInput) error {
	if err := q.Exec(ctx, `DELETE FROM them.managed_app_params WHERE app_id = $1::uuid`, appID); err != nil {
		return err
	}
	const ins = `
		INSERT INTO them.managed_app_params
		  (app_id, key, label, description, param_type, enum_values, required, default_value, sort_order)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::text[], $7, $8, $9)`
	for _, p := range params {
		if err := q.Exec(ctx, ins,
			appID, p.Key, p.Label, p.Description, p.ParamType,
			p.EnumValues, p.Required, p.DefaultValue, p.SortOrder,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListBindingsForTenant returns all managed-app bindings for a tenant.
func (d *DB) ListBindingsForTenant(ctx context.Context, tenantID string) ([]ManagedAppBinding, error) {
	const q = `
		SELECT id::text, app_id::text, tenant_id::text, enabled, config, app_version, created_at, updated_at
		FROM them.managed_app_bindings
		WHERE tenant_id = $1::uuid
		ORDER BY created_at ASC`
	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedAppBinding
	for rows.Next() {
		var b ManagedAppBinding
		var configRaw []byte
		if err := rows.Scan(&b.ID, &b.AppID, &b.TenantID, &b.Enabled,
			&configRaw, &b.AppVersion, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		if len(configRaw) > 0 {
			b.Config = json.RawMessage(configRaw)
		} else {
			b.Config = json.RawMessage("{}")
		}
		out = append(out, b)
	}
	if out == nil {
		out = []ManagedAppBinding{}
	}
	return out, nil
}

// GetBinding returns a single binding by app_id + tenant_id.
func (d *DB) GetBinding(ctx context.Context, appID, tenantID string) (ManagedAppBinding, error) {
	const q = `
		SELECT id::text, app_id::text, tenant_id::text, enabled, config, app_version, created_at, updated_at
		FROM them.managed_app_bindings
		WHERE app_id = $1::uuid AND tenant_id = $2::uuid`
	var b ManagedAppBinding
	var configRaw []byte
	err := d.q.QueryRow(ctx, q, appID, tenantID).Scan(
		&b.ID, &b.AppID, &b.TenantID, &b.Enabled, &configRaw, &b.AppVersion, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return b, err
	}
	if len(configRaw) > 0 {
		b.Config = json.RawMessage(configRaw)
	} else {
		b.Config = json.RawMessage("{}")
	}
	return b, nil
}

// UpsertBinding creates or updates a tenant binding for a managed app.
func (d *DB) UpsertBinding(ctx context.Context, appID, tenantID string, in ManagedAppBindingInput) (ManagedAppBinding, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	appVer := in.AppVersion
	if appVer == "" {
		appVer = "latest"
	}
	cfg := in.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO them.managed_app_bindings (app_id, tenant_id, enabled, config, app_version)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (app_id, tenant_id) DO UPDATE
		  SET enabled = EXCLUDED.enabled,
		      config  = EXCLUDED.config,
		      app_version = EXCLUDED.app_version,
		      updated_at  = NOW()
		RETURNING id::text, app_id::text, tenant_id::text, enabled, config, app_version, created_at, updated_at`
	var b ManagedAppBinding
	var configRaw []byte
	err := d.q.ExecReturning(ctx, q, appID, tenantID, enabled, []byte(cfg), appVer).
		Scan(&b.ID, &b.AppID, &b.TenantID, &b.Enabled, &configRaw, &b.AppVersion, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return b, err
	}
	if len(configRaw) > 0 {
		b.Config = json.RawMessage(configRaw)
	} else {
		b.Config = json.RawMessage("{}")
	}
	return b, nil
}
