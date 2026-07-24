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

// ListAgents returns all agents ordered by creation date.
func (d *DB) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := d.q.Query(ctx, agentSelectCols+" ORDER BY created_at")
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

// GetAgent returns a single agent by UUID id. Returns an error if not found.
func (d *DB) GetAgent(ctx context.Context, id string) (Agent, error) {
	row := d.q.QueryRow(ctx, agentSelectCols+" WHERE id = $1::uuid", id)
	return scanAgent(&singleToRow{s: row})
}

// CreateAgent inserts a new agent row and returns the new UUID.
func (d *DB) CreateAgent(ctx context.Context, in AgentInput, enabled bool) (string, error) {
	const q = `
		INSERT INTO them.agents
		  (slug, display_name, description, transport, endpoint_url,
		   max_concurrency, max_retries, timeout_seconds, enabled,
		   supports_streaming, supports_push, icon, category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id::text`

	row := d.q.ExecReturning(ctx, q,
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

// UpdateAgent modifies an existing agent row identified by UUID id.
func (d *DB) UpdateAgent(ctx context.Context, id string, in AgentInput, enabled bool) error {
	const q = `
		UPDATE them.agents
		SET display_name=$2, description=$3, transport=$4,
		    endpoint_url=NULLIF($5, ''), max_concurrency=$6, max_retries=$7,
		    timeout_seconds=$8, enabled=$9,
		    supports_streaming=$10, supports_push=$11,
		    icon=$12, category=$13, updated_at=now()
		WHERE id=$1::uuid`

	return d.q.Exec(ctx, q,
		id, in.DisplayName, in.Description, in.Transport,
		in.EndpointURL, in.MaxConcurrency, in.MaxRetries,
		in.TimeoutSeconds, enabled,
		in.SupportsStreaming, in.SupportsPush,
		in.Icon, in.Category,
	)
}

// DeleteAgent soft-deletes an agent by setting enabled=false.
func (d *DB) DeleteAgent(ctx context.Context, id string) error {
	return d.q.Exec(ctx,
		`UPDATE them.agents SET enabled=false, updated_at=now() WHERE id=$1::uuid`,
		id)
}
