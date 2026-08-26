package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// listAppQuery is shared by ListApplications and GetApplication.
// It returns: id, name, enabled, active_revision, active_status.
// app_orchestrators are fetched separately per-app to avoid N×M fanout.
// Note: them.applications has no slug column — slug lives on entry_points.
const listAppQuery = `
SELECT
    a.id::text,
    a.name,
    a.enabled,
    d.revision,
    d.status
FROM them.applications a
LEFT JOIN them.application_definitions d ON d.id = a.active_definition_id
WHERE a.tenant_id = $1::uuid`

// scanApplication scans one application row from listAppQuery.
func scanApplication(rows SingleRowScanner) (Application, error) {
	var a Application
	if err := rows.Scan(&a.ID, &a.Name, &a.Enabled, &a.ActiveRevision, &a.ActiveStatus); err != nil {
		return a, err
	}
	return a, nil
}

// listAppOrchSummaries returns lightweight orchestrator summaries for one app.
// app_orchestrators has no tenant_id — tenant safety is through application_id FK.
func (d *DB) listAppOrchSummaries(ctx context.Context, appID string) []AppOrchestratorSummary {
	const q = `
SELECT id::text, name, COALESCE(display_name,''), llm_provider, llm_model,
       COALESCE(mcp_servers, '[]'::jsonb)
FROM them.app_orchestrators
WHERE application_id = $1::uuid AND enabled = true
ORDER BY created_at`

	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []AppOrchestratorSummary
	for rows.Next() {
		var s AppOrchestratorSummary
		var mcpRaw []byte
		if err := rows.Scan(&s.ID, &s.Name, &s.DisplayName, &s.LLMProvider, &s.LLMModel, &mcpRaw); err != nil {
			break
		}
		if len(mcpRaw) > 0 && string(mcpRaw) != "null" {
			_ = json.Unmarshal(mcpRaw, &s.MCPServers)
		}
		if s.MCPServers == nil {
			s.MCPServers = []MCPServerAttachment{}
		}
		out = append(out, s)
	}
	if out == nil {
		out = []AppOrchestratorSummary{}
	}
	return out
}

// SetOrchestratorMCPServers writes the mcp_servers JSONB array for one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator belonging to another application.
func (d *DB) SetOrchestratorMCPServers(ctx context.Context, appID, orchID string, servers []MCPServerAttachment) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	const q = `
		UPDATE them.app_orchestrators
		SET mcp_servers = $3::jsonb, updated_at = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, orchID, appID, raw).Scan(&id)
}

// ListApplications returns all applications for the given tenant, ordered by creation date.
func (d *DB) ListApplications(ctx context.Context, tenantID string) ([]Application, error) {
	q := listAppQuery + ` ORDER BY a.created_at`

	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	apps := make([]Application, 0)
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		a.AppOrchestrators = d.listAppOrchSummaries(ctx, a.ID)
		a.EntryPoints = d.ListEntryPoints(ctx, a.ID)
		apps = append(apps, a)
	}
	return apps, nil
}

// GetApplication returns a single application by UUID id, scoped to the tenant.
// Returns pgx.ErrNoRows when not found or when it belongs to another tenant.
func (d *DB) GetApplication(ctx context.Context, tenantID, id string) (Application, error) {
	q := listAppQuery + ` AND a.id = $2::uuid`

	a, err := scanApplication(d.q.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		return Application{}, err
	}
	a.AppOrchestrators = d.listAppOrchSummaries(ctx, a.ID)
	a.EntryPoints = d.ListEntryPoints(ctx, a.ID)
	return a, nil
}

// CreateApplication inserts a new application row for the given tenant and returns the new UUID.
func (d *DB) CreateApplication(ctx context.Context, tenantID, name string, enabled bool) (string, error) {
	const q = `INSERT INTO them.applications (tenant_id, name, enabled) VALUES ($1::uuid, $2, $3) RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, tenantID, name, enabled)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateApplication modifies an existing application row, scoped to the tenant.
func (d *DB) UpdateApplication(ctx context.Context, tenantID, id, name string, enabled bool) error {
	const q = `UPDATE them.applications SET name=$3, enabled=$4, updated_at=now() WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, id, tenantID, name, enabled)
}

// DeleteApplication soft-deletes an application by setting enabled=false, scoped to the tenant.
func (d *DB) DeleteApplication(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM them.applications WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, id, tenantID)
}

// ListEntryPoints returns all entry points for a given application UUID.
// Returns an empty (non-nil) slice on DB error so callers can safely range over it.
func (d *DB) ListEntryPoints(ctx context.Context, appID string) []EntryPoint {
	const q = `
		SELECT id::text, application_id::text, app_orchestrator_id::text,
		       slug, entry_point_type, enabled,
		       COALESCE(memory_enabled, false),
		       COALESCE(summarize_every_n_calls, 10),
		       COALESCE(memory_raw_fallback_n, 3),
		       summarizer_provider, summarizer_model
		FROM them.entry_points WHERE application_id=$1::uuid ORDER BY created_at`

	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return make([]EntryPoint, 0)
	}
	defer rows.Close()

	eps := make([]EntryPoint, 0)
	for rows.Next() {
		var ep EntryPoint
		if err := rows.Scan(
			&ep.ID, &ep.ApplicationID, &ep.AppOrchestratorID,
			&ep.Slug, &ep.EntryPointType, &ep.Enabled,
			&ep.MemoryEnabled, &ep.SummarizeEveryNCalls, &ep.MemoryRawFallbackN,
			&ep.SummarizerProvider, &ep.SummarizerModel,
		); err != nil {
			break
		}
		eps = append(eps, ep)
	}
	return eps
}

// SetEntryPointSummarizer updates summarizer settings on one entry_points row.
func (d *DB) SetEntryPointSummarizer(ctx context.Context, appID, epID string, enabled bool, everyN, fallbackN int, provider, model *string) error {
	const q = `
		UPDATE them.entry_points
		SET memory_enabled          = $3,
		    summarize_every_n_calls = $4,
		    memory_raw_fallback_n   = $5,
		    summarizer_provider     = $6,
		    summarizer_model        = $7,
		    updated_at              = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, epID, appID, enabled, everyN, fallbackN, provider, model).Scan(&id)
}

// CreateEntryPoint inserts a new entry point row and returns the new UUID.
// tenant_id is backfilled from the parent application row (migration 028).
func (d *DB) CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled bool) (string, error) {
	const q = `
		INSERT INTO them.entry_points (application_id, tenant_id, slug, entry_point_type, enabled)
		SELECT $1::uuid, tenant_id, $2, $3, $4
		  FROM them.applications WHERE id = $1::uuid
		RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, appID, slug, epType, enabled)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetEntryPointSlug returns the slug of an entry point by its UUID and parent appID.
// Used for cache invalidation before rename.
func (d *DB) GetEntryPointSlug(ctx context.Context, epID, appID string) (string, error) {
	row := d.q.QueryRow(ctx,
		`SELECT slug FROM them.entry_points WHERE id=$1::uuid AND application_id=$2::uuid`, epID, appID)
	var slug string
	if err := row.Scan(&slug); err != nil {
		return "", err
	}
	return slug, nil
}

// EPTenantSlug is a (tenant_id, slug) pair for cache invalidation.
type EPTenantSlug struct {
	TenantID string
	Slug     string
}

// GetEntryPointTenantAndSlug returns the tenant_id and slug for cache invalidation.
// Returns empty strings on error (caller skips invalidation).
func (d *DB) GetEntryPointTenantAndSlug(ctx context.Context, epID, appID string) EPTenantSlug {
	row := d.q.QueryRow(ctx,
		`SELECT tenant_id::text, slug FROM them.entry_points WHERE id=$1::uuid AND application_id=$2::uuid`,
		epID, appID)
	var ts EPTenantSlug
	_ = row.Scan(&ts.TenantID, &ts.Slug)
	return ts
}

// ListEPTenantSlugsForApp returns all (tenant_id, slug) pairs for a given application UUID.
// Used by the cache invalidation helper when an application is modified/deleted.
func (d *DB) ListEPTenantSlugsForApp(ctx context.Context, appID string) []EPTenantSlug {
	const q = `SELECT tenant_id::text, slug FROM them.entry_points WHERE application_id = $1::uuid`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []EPTenantSlug
	for rows.Next() {
		var ts EPTenantSlug
		if err := rows.Scan(&ts.TenantID, &ts.Slug); err != nil {
			break
		}
		result = append(result, ts)
	}
	return result
}

// UpdateEntryPoint modifies an existing entry point row.
func (d *DB) UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled bool) error {
	const q = `
		UPDATE them.entry_points
		SET slug=$3, entry_point_type=$4, enabled=$5, updated_at=now()
		WHERE id=$1::uuid AND application_id=$2::uuid`
	return d.q.Exec(ctx, q, epID, appID, slug, epType, enabled)
}

// DeleteEntryPoint soft-deletes an entry point by setting enabled=false.
func (d *DB) DeleteEntryPoint(ctx context.Context, epID, appID string) error {
	const q = `UPDATE them.entry_points SET enabled=false, updated_at=now() WHERE id=$1::uuid AND application_id=$2::uuid`
	return d.q.Exec(ctx, q, epID, appID)
}

// ListEPSlugsForApp returns all EP slugs for a given application UUID.
// Used by the cache invalidation helper when an application is modified/deleted.
func (d *DB) ListEPSlugsForApp(ctx context.Context, appID string) []string {
	const q = `SELECT slug FROM them.entry_points WHERE application_id = $1::uuid`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			break
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

// UpdateRuntimeConfig persists a JSON runtime config blob for the application,
// scoped to the tenant. Returns pgx.ErrNoRows if the application does not exist
// or belongs to a different tenant.
// Uses RETURNING id::text so that no rows updated → pgx.ErrNoRows (dal.IsNoRows detects it).
func (d *DB) UpdateRuntimeConfig(ctx context.Context, tenantID, appID string, configJSON []byte) error {
	const q = `UPDATE them.applications SET runtime_config=$3, updated_at=now()
               WHERE id=$1::uuid AND tenant_id=$2::uuid RETURNING id::text`
	var id string
	return d.q.ExecReturning(ctx, q, appID, tenantID, configJSON).Scan(&id)
}

// ListAppOrchestratorNames returns the names of all app_orchestrators for the given application.
// Used by cache flush before a bulk delete.
func (d *DB) ListAppOrchestratorNames(ctx context.Context, appID string) ([]string, error) {
	const q = `SELECT name FROM them.app_orchestrators WHERE application_id=$1::uuid`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, nil
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

// SetOrchestratorLLM updates llm_provider and llm_model on one app_orchestrators row.
// Scoped to appID so a caller cannot modify an orchestrator belonging to another application.
func (d *DB) SetOrchestratorLLM(ctx context.Context, appID, orchID, provider, model string) error {
	const q = `
		UPDATE them.app_orchestrators
		SET llm_provider = $3, llm_model = $4, updated_at = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, orchID, appID, provider, model).Scan(&id)
}

// BulkDeleteApplications hard-deletes applications matching the provided UUID list,
// scoped to the tenant. Returns the number of rows actually deleted via RETURNING.
// CASCADE on the FK to app_orchestrators, entry_points, and middleware_wirings handles child rows.
// Runs are NOT deleted — they reference app_orchestrators, not applications.
func (d *DB) BulkDeleteApplications(ctx context.Context, tenantID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// Build parameterized query. $1=tenantID, $2..$N = app IDs as UUID casts.
	args := make([]any, 0, len(ids)+1)
	args = append(args, tenantID)
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+2)
	}
	q := fmt.Sprintf(
		`DELETE FROM them.applications WHERE tenant_id=$1::uuid AND id IN (%s) RETURNING id::text`,
		strings.Join(placeholders, ","),
	)
	rows, err := d.q.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
