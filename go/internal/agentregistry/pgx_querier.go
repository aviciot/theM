package agentregistry

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier implements DBReader against a PostgreSQL connection pool.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier creates a PgxQuerier backed by pool.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

// GetBindingID returns the app_agent_bindings.id for the given application + agent pair.
// Returns ("", nil) when no binding row exists (unboundd canvas agent).
func (q *PgxQuerier) GetBindingID(ctx context.Context, applicationID, agentID string) (string, error) {
	const sql = `
		SELECT id::text
		FROM them.app_agent_bindings
		WHERE application_id = $1::uuid
		  AND agent_id = $2::uuid
		LIMIT 1`
	var id string
	err := q.pool.QueryRow(ctx, sql, applicationID, agentID).Scan(&id)
	if err != nil {
		// pgx returns pgx.ErrNoRows when no binding exists — treat as missing, not an error.
		return "", nil
	}
	return id, nil
}

// QueryAgentsByTenant loads all enabled agents belonging to the given tenant.
// tenantID is the server-resolved UUID string from the auth context.
// Scoped to tenant_id so agents from different tenants are never mixed (SEC-03).
func (q *PgxQuerier) QueryAgentsByTenant(ctx context.Context, tenantID string) ([]*AgentConfig, error) {
	const sql = `
		SELECT id::text, slug, display_name, description,
		       transport, COALESCE(endpoint_url, ''),
		       COALESCE(auth_token_encrypted, ''),
		       max_concurrency, supports_streaming
		FROM them.agents
		WHERE enabled = true
		  AND tenant_id = $1::uuid
		ORDER BY id`

	rows, err := q.pool.Query(ctx, sql, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*AgentConfig
	for rows.Next() {
		a := &AgentConfig{}
		if err := rows.Scan(
			&a.ID, &a.Slug, &a.Name, &a.Description,
			&a.AdapterType, &a.EndpointURL, &a.AuthToken,
			&a.MaxConcurrency, &a.SupportsStreaming,
		); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}
