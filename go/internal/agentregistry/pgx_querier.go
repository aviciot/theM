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

// QueryAgentsByTenant loads all enabled agents belonging to the given tenant.
// tenantID is the server-resolved UUID string from the auth context.
// Scoped to tenant_id so agents from different tenants are never mixed (SEC-03).
func (q *PgxQuerier) QueryAgentsByTenant(ctx context.Context, tenantID string) ([]*AgentConfig, error) {
	const sql = `
		SELECT id, slug, display_name, description,
		       transport, COALESCE(endpoint_url, ''),
		       COALESCE(auth_token_encrypted, ''),
		       max_concurrency
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
			&a.MaxConcurrency,
		); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}
