package dal

import (
	"context"
	"encoding/json"
)

// agentSelectCols is the column list shared by ListAgents and GetAgent queries.
const agentSelectCols = `
	SELECT id::text, slug, display_name, description, transport,
	       COALESCE(endpoint_url, ''),
	       auth_token_encrypted IS NOT NULL AND auth_token_encrypted <> '',
	       input_schema, timeout_seconds, max_concurrency, max_retries,
	       enabled, COALESCE(tags, '{}'), agent_card, agent_card_url,
	       skills, supports_streaming, supports_push, icon, category,
	       card_fetched_at::text, last_scan_at::text, last_scan_result
	FROM them.agents`

// scanAgent scans one agent row from r into an Agent value.
// r must have been positioned by a preceding Next() call (multi-row) or
// wrapped via singleRowScan (single-row QueryRow result).
func scanAgent(r RowScanner) (Agent, error) {
	var a Agent
	var tagsArr []string
	var inputSchema, agentCard, skills, lastScanResult []byte
	var cardFetchedAt, lastScanAt *string
	if err := r.Scan(
		&a.ID, &a.Slug, &a.DisplayName, &a.Description, &a.Transport,
		&a.EndpointURL, &a.AuthTokenSet,
		&inputSchema, &a.TimeoutSeconds, &a.MaxConcurrency, &a.MaxRetries,
		&a.Enabled, &tagsArr, &agentCard, &a.AgentCardURL,
		&skills, &a.SupportsStreaming, &a.SupportsPush, &a.Icon, &a.Category,
		&cardFetchedAt, &lastScanAt, &lastScanResult,
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
	rows, err := d.q.Query(ctx, agentSelectCols+" WHERE tenant_id = $1::uuid ORDER BY created_at", tenantID)
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
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE id = $1::uuid AND tenant_id = $2::uuid", id, tenantID)
	return scanAgent(&singleToRow{s: row})
}

// CreateAgent inserts a new agent row for the given tenant and returns the new UUID.
func (d *DB) CreateAgent(ctx context.Context, tenantID string, in AgentInput, enabled bool) (string, error) {
	const q = `
		INSERT INTO them.agents
		  (tenant_id, slug, display_name, description, transport, endpoint_url,
		   max_concurrency, max_retries, timeout_seconds, enabled,
		   supports_streaming, supports_push, icon, category)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id::text`

	row := d.q.ExecReturning(ctx, q,
		tenantID,
		in.Slug, in.DisplayName, in.Description, in.Transport,
		in.EndpointURL, in.MaxConcurrency, in.MaxRetries,
		in.TimeoutSeconds, enabled,
		in.SupportsStreaming, in.SupportsPush,
		in.Icon, in.Category,
	)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateAgent modifies an existing agent row identified by UUID id, scoped to the tenant.
// A row belonging to another tenant is silently treated as not found (0 rows affected).
func (d *DB) UpdateAgent(ctx context.Context, tenantID, id string, in AgentInput, enabled bool) error {
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

// DeleteAgent soft-deletes an agent by setting enabled=false, scoped to the tenant.
func (d *DB) DeleteAgent(ctx context.Context, tenantID, id string) error {
	return d.q.Exec(ctx,
		`UPDATE them.agents SET enabled=false, updated_at=now() WHERE id=$1::uuid AND tenant_id=$2::uuid`,
		id, tenantID)
}

// GetAgentBySlug returns a single agent by slug (platform-global, not tenant-scoped).
// Used for discovering the security_scanner agent which is a platform-level resource.
// Returns pgx.ErrNoRows when the agent does not exist.
func (d *DB) GetAgentBySlug(ctx context.Context, slug string) (Agent, error) {
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE slug = $1 AND enabled = true LIMIT 1", slug)
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
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE id = $1::uuid", id)
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
