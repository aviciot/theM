package dal

import "context"

// tenantSystemAgentConfigSelectCols is the column list shared by all
// tenant_system_agent_config queries.
const tenantSystemAgentConfigSelectCols = `
	SELECT tenant_id, role, mode, provider_name, key_id,
	       custom_provider, custom_model, custom_api_key_encrypted,
	       custom_base_url, custom_system_prompt
	FROM them.tenant_system_agent_config`

// scanTenantSystemAgentConfig scans one tenant_system_agent_config row from r.
func scanTenantSystemAgentConfig(r RowScanner) (TenantSystemAgentConfig, error) {
	var c TenantSystemAgentConfig
	if err := r.Scan(
		&c.TenantID, &c.Role, &c.Mode, &c.ProviderName, &c.KeyID,
		&c.CustomProvider, &c.CustomModel, &c.CustomAPIKeyEncrypted,
		&c.CustomBaseURL, &c.CustomSystemPrompt,
	); err != nil {
		return c, err
	}
	return c, nil
}

// GetTenantSystemAgentConfig returns the (tenant, role) config row.
// Returns pgx.ErrNoRows when no row exists yet (caller should treat as
// mode="custom" with no custom fields set — i.e. today's default behavior).
func (d *DB) GetTenantSystemAgentConfig(ctx context.Context, tenantID, role string) (TenantSystemAgentConfig, error) {
	row := d.q.QueryRow(ctx,
		tenantSystemAgentConfigSelectCols+" WHERE tenant_id=$1::uuid AND role=$2",
		tenantID, role)
	return scanTenantSystemAgentConfig(&singleToRow{s: row})
}

// UpsertTenantSystemAgentConfig creates or replaces the (tenant, role) config row.
func (d *DB) UpsertTenantSystemAgentConfig(ctx context.Context, in TenantSystemAgentConfigInput) (TenantSystemAgentConfig, error) {
	const q = `
		INSERT INTO them.tenant_system_agent_config
		  (tenant_id, role, mode, provider_name, key_id,
		   custom_provider, custom_model, custom_api_key_encrypted,
		   custom_base_url, custom_system_prompt)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (tenant_id, role) DO UPDATE SET
		  mode                     = EXCLUDED.mode,
		  provider_name            = EXCLUDED.provider_name,
		  key_id                   = EXCLUDED.key_id,
		  custom_provider          = EXCLUDED.custom_provider,
		  custom_model             = EXCLUDED.custom_model,
		  custom_api_key_encrypted = EXCLUDED.custom_api_key_encrypted,
		  custom_base_url          = EXCLUDED.custom_base_url,
		  custom_system_prompt     = EXCLUDED.custom_system_prompt,
		  updated_at               = now()
		RETURNING tenant_id, role, mode, provider_name, key_id,
		          custom_provider, custom_model, custom_api_key_encrypted,
		          custom_base_url, custom_system_prompt`

	row := d.q.ExecReturning(ctx, q,
		in.TenantID, in.Role, in.Mode, in.ProviderName, in.KeyID,
		in.CustomProvider, in.CustomModel, in.CustomAPIKeyEncrypted,
		in.CustomBaseURL, in.CustomSystemPrompt,
	)
	return scanTenantSystemAgentConfig(&singleToRow{s: row})
}

// ── tenant_system_agent_config types ──────────────────────────────────────────

// TenantSystemAgentConfig is the internal DB row representation of
// them.tenant_system_agent_config. CustomAPIKeyEncrypted holds the raw stored
// value (with "enc:" prefix) when set. Masking and decryption happen
// exclusively in the service layer.
type TenantSystemAgentConfig struct {
	TenantID              string
	Role                  string
	Mode                  string
	ProviderName          *string
	KeyID                 *int64
	CustomProvider        *string
	CustomModel           *string
	CustomAPIKeyEncrypted *string
	CustomBaseURL         *string
	CustomSystemPrompt    *string
}

// TenantSystemAgentConfigInput is used for upsert.
type TenantSystemAgentConfigInput struct {
	TenantID              string
	Role                  string
	Mode                  string
	ProviderName          *string
	KeyID                 *int64
	CustomProvider        *string
	CustomModel           *string
	CustomAPIKeyEncrypted *string
	CustomBaseURL         *string
	CustomSystemPrompt    *string
}
