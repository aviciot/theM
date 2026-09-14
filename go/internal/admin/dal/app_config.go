package dal

import "context"

// AppGlobalParam is one entry in applications.app_params JSONB as returned to callers.
// For secrets, Value is always empty and ValueHint holds the last 4 chars of the plaintext.
// For non-secrets, Value holds the plaintext and ValueHint is empty.
type AppGlobalParam struct {
	Name      string `json:"name"`
	Type      string `json:"type"`                  // "secret" | "string" | "url" | "int" | "bool"
	IsSet     bool   `json:"is_set"`
	ValueHint string `json:"value_hint,omitempty"` // last 4 chars; secrets only
	Value     string `json:"value,omitempty"`       // non-secrets only
}

// GetProviderKeys returns the provider_keys JSONB blob for the application.
// Returns an empty JSON object when the field is null or not set.
func (d *DB) GetProviderKeys(ctx context.Context, tenantID, appID string) ([]byte, error) {
	const q = `SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id=$1::uuid AND tenant_id=$2::uuid`
	var raw []byte
	if err := d.q.QueryRow(ctx, q, appID, tenantID).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SetProviderKey stores one encrypted API key for the given provider on the application.
// Uses jsonb_set so other provider keys are preserved.
func (d *DB) SetProviderKey(ctx context.Context, tenantID, appID, provider string, encryptedKey []byte) error {
	const q = `
		UPDATE them.applications
		SET provider_keys = jsonb_set(COALESCE(provider_keys,'{}'), $3::text[], $4::jsonb, true),
		    updated_at = now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, appID, tenantID, "{"+provider+"}", encryptedKey)
}

// DeleteProviderKey removes the key for a single provider from the application's provider_keys.
func (d *DB) DeleteProviderKey(ctx context.Context, tenantID, appID, provider string) error {
	const q = `
		UPDATE them.applications
		SET provider_keys = provider_keys - $3,
		    updated_at = now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, appID, tenantID, provider)
}

// UpsertProviderBaseURL upserts a tenant-scoped row in them.llm_providers for the
// given provider, setting only the base_url. The row is created with a default
// model name equal to the provider name when inserting; existing rows have only
// base_url updated. This allows the tenant-scoped base_url to override the
// platform default without touching the API key stored per-app.
func (d *DB) UpsertProviderBaseURL(ctx context.Context, tenantID, provider, baseURL string) error {
	const q = `
		INSERT INTO them.llm_providers (name, display_name, base_url, default_model, tenant_id, enabled)
		VALUES ($1, $1, $2, $1, $3::uuid, true)
		ON CONFLICT ON CONSTRAINT llm_providers_name_tenant_uq
		DO UPDATE SET base_url = EXCLUDED.base_url, updated_at = now()`
	return d.q.Exec(ctx, q, provider, baseURL, tenantID)
}

// GetProviderBaseURLs returns a map of provider name → base_url for the given
// tenant. Prefers the tenant-scoped row; falls back to platform default (tenant_id IS NULL).
func (d *DB) GetProviderBaseURLs(ctx context.Context, tenantID string) (map[string]string, error) {
	const q = `
		SELECT name, base_url
		FROM them.llm_providers
		WHERE (tenant_id = $1::uuid OR tenant_id IS NULL)
		  AND base_url IS NOT NULL
		  AND enabled = true
		ORDER BY tenant_id NULLS LAST`
	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]string{}
	for rows.Next() {
		var name string
		var url *string
		if err := rows.Scan(&name, &url); err != nil {
			continue
		}
		if url != nil && *url != "" {
			// tenant-scoped rows come first (ORDER BY tenant_id NULLS LAST);
			// don't overwrite a tenant row with the platform default.
			if _, exists := out[name]; !exists {
				out[name] = *url
			}
		}
	}
	return out, nil
}

// GetAppParams returns the app_params JSONB blob for the application.
// Returns an empty JSON object when the field is null or not set.
func (d *DB) GetAppParams(ctx context.Context, tenantID, appID string) ([]byte, error) {
	const q = `SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id=$1::uuid AND tenant_id=$2::uuid`
	var raw []byte
	if err := d.q.QueryRow(ctx, q, appID, tenantID).Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SetAppParam stores one named app param in the app_params JSONB column.
// Uses jsonb_set so other params are preserved.
func (d *DB) SetAppParam(ctx context.Context, tenantID, appID, name string, valueJSON []byte) error {
	const q = `
		UPDATE them.applications
		SET app_params = jsonb_set(COALESCE(app_params,'{}'), $3::text[], $4::jsonb, true),
		    updated_at = now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, appID, tenantID, "{"+name+"}", valueJSON)
}

// DeleteAppParam removes one named param from app_params.
func (d *DB) DeleteAppParam(ctx context.Context, tenantID, appID, name string) error {
	const q = `
		UPDATE them.applications
		SET app_params = app_params - $3,
		    updated_at = now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, appID, tenantID, name)
}
