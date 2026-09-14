package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// listAppQuery is shared by ListApplications and GetApplication.
// It returns: id, name, slug, tenant_slug, enabled, active_revision, active_status.
// app_orchestrators are fetched separately per-app to avoid N×M fanout.
const listAppQuery = `
SELECT
    a.id::text,
    a.name,
    COALESCE(a.slug, ''),
    COALESCE(t.slug, ''),
    a.enabled,
    d.revision,
    d.status
FROM them.applications a
JOIN  them.tenants t ON t.id = a.tenant_id
LEFT JOIN them.application_definitions d ON d.id = a.active_definition_id
WHERE a.tenant_id = $1::uuid`

// scanApplication scans one application row from listAppQuery.
func scanApplication(rows SingleRowScanner) (Application, error) {
	var a Application
	if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.TenantSlug, &a.Enabled, &a.ActiveRevision, &a.ActiveStatus); err != nil {
		return a, err
	}
	return a, nil
}

// listAppOrchSummaries returns lightweight orchestrator summaries for one app.
// app_orchestrators has no tenant_id — tenant safety is through application_id FK.
func (d *DB) listAppOrchSummaries(ctx context.Context, appID string) []AppOrchestratorSummary {
	const q = `
SELECT id::text, name, COALESCE(display_name,''), llm_provider, llm_model,
       COALESCE(mcp_servers, '[]'::jsonb),
       transcription_provider, transcription_model,
       tts_provider, tts_voice,
       COALESCE(voice_enabled, false), COALESCE(tts_enabled, false),
       COALESCE(allowed_agent_ids, '{}')
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
		if err := rows.Scan(
			&s.ID, &s.Name, &s.DisplayName, &s.LLMProvider, &s.LLMModel, &mcpRaw,
			&s.TranscriptionProvider, &s.TranscriptionModel,
			&s.TTSProvider, &s.TTSVoice,
			&s.VoiceEnabled, &s.TTSEnabled,
			&s.AllowedAgentIDs,
		); err != nil {
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

// ListApplications returns all applications for the given tenant, ordered by creation date.
func (d *DB) ListApplications(ctx context.Context, tenantID string) ([]Application, error) {
	q := listAppQuery + ` ORDER BY a.created_at`

	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}

	// Scan all apps first to close the cursor before running sub-queries.
	// pgx transactions do not support interleaved result sets on the same connection.
	apps := make([]Application, 0)
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		apps = append(apps, a)
	}
	rows.Close()

	// Enrich each app with orchestrators and entry points after the main cursor is closed.
	for i := range apps {
		apps[i].AppOrchestrators = d.listAppOrchSummaries(ctx, apps[i].ID)
		apps[i].EntryPoints = d.ListEntryPoints(ctx, apps[i].ID)
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
func (d *DB) CreateApplication(ctx context.Context, tenantID, name, slug string, enabled bool) (string, error) {
	const q = `INSERT INTO them.applications (tenant_id, name, slug, enabled) VALUES ($1::uuid, $2, $3, $4) RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, tenantID, name, slug, enabled)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateApplication modifies an existing application row, scoped to the tenant.
func (d *DB) UpdateApplication(ctx context.Context, tenantID, id, name, slug string, enabled bool) error {
	const q = `UPDATE them.applications SET name=$3, slug=$4, enabled=$5, updated_at=now() WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, id, tenantID, name, slug, enabled)
}

// CountApplications returns the number of applications belonging to tenantID.
// Used by quota enforcement to check max_apps before creating a new application.
func (d *DB) CountApplications(ctx context.Context, tenantID string) (int, error) {
	const q = `SELECT COUNT(*)::int FROM them.applications WHERE tenant_id = $1::uuid`
	var n int
	err := d.q.QueryRow(ctx, q, tenantID).Scan(&n)
	return n, err
}

// DeleteApplication hard-deletes an application row, scoped to the tenant.
func (d *DB) DeleteApplication(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM them.applications WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, id, tenantID)
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
