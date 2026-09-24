package dal

import (
	"context"
	"encoding/json"

	"github.com/aviciot/them/internal/tenantctx"
)

// llmProviderSelectCols is the column list shared by all provider queries.
// tenant_id is nullable (NULL = platform default, UUID = tenant override).
const llmProviderSelectCols = `
	SELECT id, name, display_name, api_key_encrypted, base_url,
	       default_model, model_pricing, enabled, tenant_id, allowed_models
	FROM them.llm_providers`

// scanProvider scans one llm_providers row from r into an LLMProvider value.
func scanProvider(r RowScanner) (LLMProvider, error) {
	var p LLMProvider
	var modelPricing []byte
	var allowedModels []byte
	if err := r.Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.APIKeyEncrypted, &p.BaseURL,
		&p.DefaultModel, &modelPricing, &p.Enabled, &p.TenantID, &allowedModels,
	); err != nil {
		return p, err
	}
	p.ModelPricingRaw = modelPricing
	p.AllowedModelsRaw = allowedModels
	return p, nil
}

// ListProviders returns the bootstrap tenant's own LLM providers — the-M's own
// "platform-default" rows post-Platform-as-Tenant (see docs/PLATFORM_AS_TENANT_PLAN.md).
// Ordered by id ASC.
func (d *DB) ListProviders(ctx context.Context) ([]LLMProvider, error) {
	rows, err := d.q.Query(ctx, llmProviderSelectCols+" WHERE tenant_id = $1::uuid ORDER BY id ASC", tenantctx.BootstrapTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	providers := make([]LLMProvider, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}

// ListProvidersForTenant returns the merged view for a tenant:
// tenant overrides for this tenantID, plus the bootstrap tenant's provider
// defaults not overridden (Platform-as-Tenant Phase 1 moved what used to be
// NULL-tenant "platform default" rows to the bootstrap tenant's real
// tenant_id — see docs/PLATFORM_AS_TENANT_PLAN.md). Tenant rows win when name
// matches. Results are ordered by name ASC.
func (d *DB) ListProvidersForTenant(ctx context.Context, tenantID string) ([]LLMProvider, error) {
	const q = `
		SELECT id, name, display_name, api_key_encrypted, base_url,
		       default_model, model_pricing, enabled, tenant_id, allowed_models
		FROM them.llm_providers
		WHERE tenant_id = $1::uuid
		   OR (tenant_id = $2::uuid AND name NOT IN (
		           SELECT name FROM them.llm_providers WHERE tenant_id = $1::uuid
		       ))
		ORDER BY name ASC`
	rows, err := d.q.Query(ctx, q, tenantID, tenantctx.BootstrapTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	providers := make([]LLMProvider, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}

// GetProvider returns a single LLM provider by id. Returns pgx.ErrNoRows when not found.
func (d *DB) GetProvider(ctx context.Context, id int64) (LLMProvider, error) {
	row := d.q.QueryRow(ctx, llmProviderSelectCols+" WHERE id = $1", id)
	return scanProvider(&singleToRow{s: row})
}

// GetProviderByNameForTenant returns a tenant override row for (name, tenantID).
// Returns pgx.ErrNoRows when not found.
func (d *DB) GetProviderByNameForTenant(ctx context.Context, name, tenantID string) (LLMProvider, error) {
	row := d.q.QueryRow(ctx, llmProviderSelectCols+" WHERE name=$1 AND tenant_id=$2::uuid", name, tenantID)
	return scanProvider(&singleToRow{s: row})
}

// CreateProvider inserts a new LLM provider row owned by the bootstrap tenant
// — the-M's own "platform-default" provider post-Platform-as-Tenant (see
// docs/PLATFORM_AS_TENANT_PLAN.md). Returns a unique-violation error
// (SQLSTATE 23505) when a bootstrap-tenant row with this name already exists.
func (d *DB) CreateProvider(ctx context.Context, in LLMProviderInput) (LLMProvider, error) {
	const q = `
		INSERT INTO them.llm_providers
		  (name, display_name, api_key_encrypted, base_url, default_model, model_pricing, enabled, tenant_id, allowed_models)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid, $9)
		RETURNING id, name, display_name, api_key_encrypted, base_url,
		          default_model, model_pricing, enabled, tenant_id, allowed_models`

	modelPricingJSON := in.ModelPricingRaw
	if modelPricingJSON == nil {
		modelPricingJSON = []byte("{}")
	}
	allowedModelsJSON := in.AllowedModelsRaw
	if allowedModelsJSON == nil {
		allowedModelsJSON = []byte("[]")
	}

	row := d.q.ExecReturning(ctx, q,
		in.Name, in.DisplayName, in.APIKeyEncrypted, in.BaseURL,
		in.DefaultModel, modelPricingJSON, in.Enabled, tenantctx.BootstrapTenantID, allowedModelsJSON,
	)
	return scanProvider(&singleToRow{s: row})
}

// UpsertTenantProvider inserts or replaces a tenant-scoped LLM provider override.
// On conflict (name, tenant_id) it updates all mutable fields.
// Returns the resulting row.
func (d *DB) UpsertTenantProvider(ctx context.Context, tenantID string, in LLMProviderInput) (LLMProvider, error) {
	const q = `
		INSERT INTO them.llm_providers
		  (name, display_name, api_key_encrypted, base_url, default_model, model_pricing, enabled, tenant_id, allowed_models)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid, $9)
		ON CONFLICT (name, tenant_id) WHERE tenant_id IS NOT NULL
		DO UPDATE SET
		  display_name     = EXCLUDED.display_name,
		  api_key_encrypted= EXCLUDED.api_key_encrypted,
		  base_url         = EXCLUDED.base_url,
		  default_model    = EXCLUDED.default_model,
		  model_pricing    = EXCLUDED.model_pricing,
		  enabled          = EXCLUDED.enabled,
		  allowed_models   = EXCLUDED.allowed_models,
		  updated_at       = now()
		RETURNING id, name, display_name, api_key_encrypted, base_url,
		          default_model, model_pricing, enabled, tenant_id, allowed_models`

	modelPricingJSON := in.ModelPricingRaw
	if modelPricingJSON == nil {
		modelPricingJSON = []byte("{}")
	}
	allowedModelsJSON := in.AllowedModelsRaw
	if allowedModelsJSON == nil {
		allowedModelsJSON = []byte("[]")
	}

	row := d.q.ExecReturning(ctx, q,
		in.Name, in.DisplayName, in.APIKeyEncrypted, in.BaseURL,
		in.DefaultModel, modelPricingJSON, in.Enabled, tenantID, allowedModelsJSON,
	)
	return scanProvider(&singleToRow{s: row})
}

// UpdateProvider applies a full replacement UPDATE to the provider row identified by id.
// The caller is responsible for merging patch fields before calling (fetch-then-modify pattern).
// Returns pgx.ErrNoRows when the provider does not exist.
func (d *DB) UpdateProvider(ctx context.Context, id int64, in LLMProviderInput) (LLMProvider, error) {
	const q = `
		UPDATE them.llm_providers
		SET name=$2, display_name=$3, api_key_encrypted=$4, base_url=$5,
		    default_model=$6, model_pricing=$7, enabled=$8, allowed_models=$9, updated_at=now()
		WHERE id=$1
		RETURNING id, name, display_name, api_key_encrypted, base_url,
		          default_model, model_pricing, enabled, tenant_id, allowed_models`

	modelPricingJSON := in.ModelPricingRaw
	if modelPricingJSON == nil {
		modelPricingJSON = []byte("{}")
	}
	allowedModelsJSON := in.AllowedModelsRaw
	if allowedModelsJSON == nil {
		allowedModelsJSON = []byte("[]")
	}

	row := d.q.ExecReturning(ctx, q,
		id, in.Name, in.DisplayName, in.APIKeyEncrypted, in.BaseURL,
		in.DefaultModel, modelPricingJSON, in.Enabled, allowedModelsJSON,
	)
	return scanProvider(&singleToRow{s: row})
}

// DeleteProvider hard-deletes a provider by id.
// Returns pgx.ErrNoRows when the provider does not exist.
func (d *DB) DeleteProvider(ctx context.Context, id int64) error {
	const q = `DELETE FROM them.llm_providers WHERE id=$1 RETURNING id`
	row := d.q.ExecReturning(ctx, q, id)
	var deleted int64
	return row.Scan(&deleted)
}

// ── LLM provider types ────────────────────────────────────────────────────────

// LLMProvider is the internal DB row representation of them.llm_providers.
// api_key_encrypted holds the raw stored value (with "enc:" prefix when set).
// Masking and decryption happen exclusively in the service layer.
// TenantID is nil for platform-default rows; non-nil for tenant overrides.
type LLMProvider struct {
	ID               int64
	Name             string
	DisplayName      string
	APIKeyEncrypted  *string // nil when no key is set
	BaseURL          *string
	DefaultModel     string
	ModelPricingRaw  []byte  // raw JSONB bytes; may be nil or "{}"
	Enabled          bool
	TenantID         *string // nil = platform default; non-nil = tenant UUID
	AllowedModelsRaw []byte  // raw JSONB array bytes; may be nil or "[]"
}

// LLMProviderInput is used for both CREATE and the full-UPDATE (fetch-then-modify).
// The api_key_encrypted field must be pre-encrypted by the service layer.
type LLMProviderInput struct {
	Name             string
	DisplayName      string
	APIKeyEncrypted  *string // nil = no key; non-nil = "enc:..." value
	BaseURL          *string
	DefaultModel     string
	ModelPricingRaw  []byte // raw JSONB; nil treated as "{}"
	Enabled          bool
	AllowedModelsRaw []byte // raw JSONB array; nil treated as "[]"
}

// LLMProviderToInput converts an LLMProvider row to an LLMProviderInput for update.
func LLMProviderToInput(p LLMProvider) LLMProviderInput {
	return LLMProviderInput{
		Name:             p.Name,
		DisplayName:      p.DisplayName,
		APIKeyEncrypted:  p.APIKeyEncrypted,
		BaseURL:          p.BaseURL,
		DefaultModel:     p.DefaultModel,
		ModelPricingRaw:  p.ModelPricingRaw,
		Enabled:          p.Enabled,
		AllowedModelsRaw: p.AllowedModelsRaw,
	}
}

// ModelPricingOrEmpty unmarshals raw JSONB into a map, returning {} on failure.
func ModelPricingOrEmpty(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// AllowedModelsOrEmpty unmarshals raw JSONB into a string slice, returning []
// (non-nil, empty) on failure so JSON responses serialize as [] not null.
func AllowedModelsOrEmpty(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var s []string
	if err := json.Unmarshal(raw, &s); err != nil {
		return []string{}
	}
	return s
}
