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

// QueryAgents loads all enabled agents from the them.agents table.
func (q *PgxQuerier) QueryAgents(ctx context.Context) ([]*AgentConfig, error) {
	const sql = `
		SELECT id, slug, name, description,
		       adapter_type, COALESCE(endpoint_url, ''),
		       COALESCE(auth_token_encrypted, ''),
		       max_concurrency
		FROM them.agents
		WHERE enabled = true
		ORDER BY id`

	rows, err := q.pool.Query(ctx, sql)
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
