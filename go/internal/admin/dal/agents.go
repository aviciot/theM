package dal

import (
	"context"
	"encoding/json"
)

// agentSelectCols is the column list shared by ListAgents and GetAgent queries.
// Note: no JOIN to auth_service.users — them_app (RLS pool) lacks USAGE on that schema.
// CreatedByUsername is not populated; the field is omitempty in the API response.
const agentSelectCols = `
	SELECT a.id::text, a.slug, a.display_name, a.description, a.transport,
	       COALESCE(a.endpoint_url, ''),
	       a.auth_token_encrypted IS NOT NULL AND a.auth_token_encrypted <> '',
	       a.input_schema, a.timeout_seconds, a.max_concurrency, a.max_retries,
	       a.enabled, COALESCE(a.tags, '{}'), a.agent_card, a.agent_card_url,
	       a.skills, a.supports_streaming, a.supports_push, a.icon, a.category,
	       a.card_fetched_at::text, a.last_scan_at::text, a.last_scan_result,
	       ars.definition_id::text,
	       a.created_by,
	       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM them.agents a
	LEFT JOIN them.agent_runtime_specs ars ON ars.agent_id = a.id`

// scanAgent scans one agent row from r into an Agent value.
// r must have been positioned by a preceding Next() call (multi-row) or
// wrapped via singleRowScan (single-row QueryRow result).
func scanAgent(r RowScanner) (Agent, error) {
	var a Agent
	var tagsArr []string
	var inputSchema, agentCard, skills, lastScanResult []byte
	var cardFetchedAt, lastScanAt *string
	var definitionID *string
	if err := r.Scan(
		&a.ID, &a.Slug, &a.DisplayName, &a.Description, &a.Transport,
		&a.EndpointURL, &a.AuthTokenSet,
		&inputSchema, &a.TimeoutSeconds, &a.MaxConcurrency, &a.MaxRetries,
		&a.Enabled, &tagsArr, &agentCard, &a.AgentCardURL,
		&skills, &a.SupportsStreaming, &a.SupportsPush, &a.Icon, &a.Category,
		&cardFetchedAt, &lastScanAt, &lastScanResult,
		&definitionID,
		&a.CreatedBy, &a.CreatedAt,
	); err != nil {
		return a, err
	}
	a.Tags = tagsArr
	if len(inputSchema) > 0 {
		_ = json.Unmarshal(inputSchema, &a.InputSchema)
	} else {
		a.InputSchema = map[string]any{}
	}
	if len(agentCard) > 0 {
		_ = json.Unmarshal(agentCard, &a.AgentCard)
	}
	if len(skills) > 0 {
		_ = json.Unmarshal(skills, &a.Skills)
	} else {
		a.Skills = []any{}
	}
	if len(lastScanResult) > 0 {
		_ = json.Unmarshal(lastScanResult, &a.LastScanResult)
	}
	a.CardFetchedAt = cardFetchedAt
	a.LastScanAt = lastScanAt
	a.RuntimeDefinitionID = definitionID
	return a, nil
}

// singleToRow wraps a SingleRowScanner as a RowScanner so scanAgent can be
// called uniformly for both multi-row (Query) and single-row (QueryRow) results.
type singleToRow struct{ s SingleRowScanner }

func (a *singleToRow) Next() bool          { return true }
func (a *singleToRow) Close() error         { return nil }
func (a *singleToRow) Scan(dest ...any) error { return a.s.Scan(dest...) }

// ListAgents returns all agents for the given tenant, ordered by creation date.
func (d *DB) ListAgents(ctx context.Context, tenantID string) ([]Agent, error) {
	rows, err := d.q.Query(ctx, agentSelectCols+" WHERE a.tenant_id = $1::uuid ORDER BY a.created_at", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// GetAgent returns a single agent by UUID id, scoped to the tenant.
// Returns pgx.ErrNoRows when the agent does not exist or belongs to another tenant.
func (d *DB) GetAgent(ctx context.Context, tenantID, id string) (Agent, error) {
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE a.id = $1::uuid AND a.tenant_id = $2::uuid", id, tenantID)
	return scanAgent(&singleToRow{s: row})
}

// CreateAgent atomically inserts both the component_definitions and agents rows using
// a single CTE, so a failure on either statement rolls back both — no orphaned CD rows.
func (d *DB) CreateAgent(ctx context.Context, tenantID string, in AgentInput, enabled bool) (string, error) {
	namespace := "them.tenant." + tenantID

	const q = `
WITH cd AS (
    INSERT INTO them.component_definitions
      (kind, namespace, name, version, display_name, description,
       implementation_type, scope, tenant_id, status, content_hash)
    VALUES ('agent', $1, $2, 1, $3, $4, $5, 'tenant', $6::uuid, 'published', '')
    RETURNING id
)
INSERT INTO them.agents
  (id, tenant_id, slug, display_name, description, transport, endpoint_url,
   max_concurrency, max_retries, timeout_seconds, enabled,
   supports_streaming, supports_push, icon, category,
   namespace, created_by)
SELECT
    (SELECT id FROM cd), $6::uuid,
    $2, $3, $4, $5, $7,
    $8, $9, $10, $11, $12, $13, $14, $15,
    $1, NULLIF($16, 0)
RETURNING id::text`

	var id string
	if err := d.q.ExecReturning(ctx, q,
		namespace,          // $1 — namespace (also used for cd.namespace and agents.namespace)
		in.Slug,            // $2 — name in CD, slug in agents
		in.DisplayName,     // $3
		in.Description,     // $4
		in.Transport,       // $5 — implementation_type in CD, transport in agents
		tenantID,           // $6
		in.EndpointURL,     // $7
		in.MaxConcurrency,  // $8
		in.MaxRetries,      // $9
		in.TimeoutSeconds,  // $10
		enabled,            // $11
		in.SupportsStreaming, // $12
		in.SupportsPush,    // $13
		in.Icon,            // $14
		in.Category,        // $15
		in.CreatedBy,       // $16
	).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateAgent modifies an existing agent row identified by UUID id, scoped to the tenant.
// A row belonging to another tenant is silently treated as not found (0 rows affected).
// When AgentInput.AgentCard is non-nil, agent_card, agent_card_url, skills, and
// card_fetched_at are also updated (populated by the Discover + Apply flow).
func (d *DB) UpdateAgent(ctx context.Context, tenantID, id string, in AgentInput, enabled bool) error {
	cardJSON, _ := json.Marshal(in.AgentCard)
	skillsJSON, _ := json.Marshal(in.Skills)

	// Use CASE to update card fields only when a new card is provided.
	// An empty AgentCard (nil or {}) is encoded as "null" — treat that as no update.
	updateCard := in.AgentCard != nil && string(cardJSON) != "null"

	if updateCard {
		const q = `
			UPDATE them.agents
			SET display_name=$3, description=$4, transport=$5,
			    endpoint_url=COALESCE(NULLIF($6, ''), endpoint_url), max_concurrency=$7, max_retries=$8,
			    timeout_seconds=$9, enabled=$10,
			    supports_streaming=$11, supports_push=$12,
			    icon=$13, category=$14,
			    agent_card=$15::jsonb, agent_card_url=$16, skills=$17::jsonb,
			    card_fetched_at=now(), updated_at=now()
			WHERE id=$1::uuid AND tenant_id=$2::uuid`
		return d.q.Exec(ctx, q,
			id, tenantID,
			in.DisplayName, in.Description, in.Transport,
			in.EndpointURL, in.MaxConcurrency, in.MaxRetries,
			in.TimeoutSeconds, enabled,
			in.SupportsStreaming, in.SupportsPush,
			in.Icon, in.Category,
			string(cardJSON), in.AgentCardURL, string(skillsJSON),
		)
	}

	const q = `
		UPDATE them.agents
		SET display_name=$3, description=$4, transport=$5,
		    endpoint_url=COALESCE(NULLIF($6, ''), endpoint_url), max_concurrency=$7, max_retries=$8,
		    timeout_seconds=$9, enabled=$10,
		    supports_streaming=$11, supports_push=$12,
		    icon=$13, category=$14, updated_at=now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q,
		id, tenantID,
		in.DisplayName, in.Description, in.Transport,
		in.EndpointURL, in.MaxConcurrency, in.MaxRetries,
		in.TimeoutSeconds, enabled,
		in.SupportsStreaming, in.SupportsPush,
		in.Icon, in.Category,
	)
}

// DeleteAgent hard-deletes an agent and its component_definitions row, scoped to the tenant.
// component_definitions has no CASCADE on the agents FK, so we delete both explicitly.
func (d *DB) DeleteAgent(ctx context.Context, tenantID, id string) error {
	if err := d.q.Exec(ctx,
		`DELETE FROM them.agents WHERE id=$1::uuid AND tenant_id=$2::uuid`,
		id, tenantID); err != nil {
		return err
	}
	// component_definitions is not cascade-deleted — clean it up so the slug can be reused.
	return d.q.Exec(ctx,
		`DELETE FROM them.component_definitions WHERE id=$1::uuid`,
		id)
}

// CountAgents returns the number of agents belonging to tenantID.
// Used by quota enforcement to check max_agents before creating a new agent.
func (d *DB) CountAgents(ctx context.Context, tenantID string) (int, error) {
	const q = `SELECT COUNT(*)::int FROM them.agents WHERE tenant_id = $1::uuid`
	var n int
	err := d.q.QueryRow(ctx, q, tenantID).Scan(&n)
	return n, err
}

// AgentExists reports whether an agents row with the given id exists.
// Used by publish.go to give a clear error before attempting a binding that would
// fail with a FK violation.
func (d *DB) AgentExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := d.q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM them.agents WHERE id = $1::uuid)`, id,
	).Scan(&exists)
	return exists, err
}

// GetAgentBySlug returns a single agent by slug (platform-global, not tenant-scoped).
// Used for discovering the security_scanner agent which is a platform-level resource.
// Returns pgx.ErrNoRows when the agent does not exist.
func (d *DB) GetAgentBySlug(ctx context.Context, slug string) (Agent, error) {
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE a.slug = $1 AND a.enabled = true LIMIT 1", slug)
	return scanAgent(&singleToRow{s: row})
}

// UpdateAgentScanResult writes the last security scan result and timestamp for
// the given agent id (platform-global, no tenant scoping needed for the scan job).
func (d *DB) UpdateAgentScanResult(ctx context.Context, agentID string, result []byte) error {
	const q = `UPDATE them.agents
		SET last_scan_result=$2::jsonb, last_scan_at=now(), updated_at=now()
		WHERE id=$1::uuid`
	return d.q.Exec(ctx, q, agentID, string(result))
}

// GetAgentByID returns a single agent by UUID id (platform-global, not tenant-scoped).
// Used by the Discover handler when agent_id is provided to resolve auth tokens.
// Returns pgx.ErrNoRows when the agent does not exist.
func (d *DB) GetAgentByID(ctx context.Context, id string) (Agent, error) {
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE a.id = $1::uuid", id)
	return scanAgent(&singleToRow{s: row})
}

// GetAgentTokenEncrypted returns the raw auth_token_encrypted value for an agent.
// Returns ("", pgx.ErrNoRows) if the agent does not exist.
// Returns ("", nil) if the agent exists but has no token.
func (d *DB) GetAgentTokenEncrypted(ctx context.Context, id string) (string, error) {
	var enc *string
	err := d.q.QueryRow(ctx,
		`SELECT auth_token_encrypted FROM them.agents WHERE id = $1::uuid`, id,
	).Scan(&enc)
	if err != nil {
		return "", err
	}
	if enc == nil {
		return "", nil
	}
	return *enc, nil
}

// GetAgentTokenEncryptedForTenant returns the encrypted auth token for an agent
// that belongs to the given tenant. Returns ("", pgx.ErrNoRows) if the agent
// does not exist in that tenant (prevents cross-tenant token extraction).
func (d *DB) GetAgentTokenEncryptedForTenant(ctx context.Context, id, tenantID string) (string, error) {
	var enc *string
	err := d.q.QueryRow(ctx,
		`SELECT auth_token_encrypted FROM them.agents WHERE id = $1::uuid AND tenant_id = $2::uuid`, id, tenantID,
	).Scan(&enc)
	if err != nil {
		return "", err
	}
	if enc == nil {
		return "", nil
	}
	return *enc, nil
}
