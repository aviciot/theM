package dal

import "context"

// EPTenantSlug is a (tenant_id, slug) pair for cache invalidation.
type EPTenantSlug struct {
	TenantID string
	Slug     string
}

// ListEntryPoints returns all entry points for a given application UUID,
// including the tenant_slug from the parent tenant (for URL construction).
// Returns an empty (non-nil) slice on DB error so callers can safely range over it.
func (d *DB) ListEntryPoints(ctx context.Context, appID string) []EntryPoint {
	const q = `
		SELECT ep.id::text, ep.application_id::text, ep.app_orchestrator_id::text,
		       ep.slug, ep.entry_point_type, ep.enabled,
		       COALESCE(ep.memory_enabled, false),
		       COALESCE(ep.summarize_every_n_calls, 10),
		       COALESCE(ep.memory_raw_fallback_n, 3),
		       COALESCE(ep.history_window, 20),
		       ep.summarizer_provider, ep.summarizer_model,
		       ep.llm_provider, ep.llm_model,
		       COALESCE(t.slug, ''),
		       COALESCE(ep.allowed_principals, 'internal')
		FROM them.entry_points ep
		JOIN them.applications a ON a.id = ep.application_id
		JOIN them.tenants t      ON t.id = a.tenant_id
		WHERE ep.application_id=$1::uuid ORDER BY ep.created_at`

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
			&ep.MemoryEnabled, &ep.SummarizeEveryNCalls, &ep.MemoryRawFallbackN, &ep.HistoryWindow,
			&ep.SummarizerProvider, &ep.SummarizerModel,
			&ep.LLMProvider, &ep.LLMModel,
			&ep.TenantSlug,
			&ep.AllowedPrincipals,
		); err != nil {
			break
		}
		eps = append(eps, ep)
	}
	return eps
}

// SetEntryPointLLM updates llm_provider and llm_model on one entry_points row.
func (d *DB) SetEntryPointLLM(ctx context.Context, appID, epID string, provider, model *string) error {
	const q = `
		UPDATE them.entry_points
		SET llm_provider = $3,
		    llm_model    = $4,
		    updated_at   = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, epID, appID, provider, model).Scan(&id)
}

// SetEntryPointSummarizer updates summarizer and history settings on one entry_points row.
func (d *DB) SetEntryPointSummarizer(ctx context.Context, appID, epID string, enabled bool, everyN, fallbackN, historyWindow int, provider, model *string) error {
	const q = `
		UPDATE them.entry_points
		SET memory_enabled          = $3,
		    summarize_every_n_calls = $4,
		    memory_raw_fallback_n   = $5,
		    history_window          = $6,
		    summarizer_provider     = $7,
		    summarizer_model        = $8,
		    updated_at              = now()
		WHERE id = $1::uuid AND application_id = $2::uuid
		RETURNING id`
	var id string
	return d.q.ExecReturning(ctx, q, epID, appID, enabled, everyN, fallbackN, historyWindow, provider, model).Scan(&id)
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

// SetEntryPointEnabled updates only the enabled column. Slug and type are untouched.
func (d *DB) SetEntryPointEnabled(ctx context.Context, epID, appID string, enabled bool) error {
	const q = `
		UPDATE them.entry_points
		SET enabled=$3, updated_at=now()
		WHERE id=$1::uuid AND application_id=$2::uuid`
	return d.q.Exec(ctx, q, epID, appID, enabled)
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
