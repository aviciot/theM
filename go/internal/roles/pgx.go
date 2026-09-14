package roles

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier implements Querier against a pgxpool.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier creates a PgxQuerier backed by the given pool.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

// ListMappings returns all tenant_role_mappings rows for the tenant.
func (q *PgxQuerier) ListMappings(ctx context.Context, tenantID string) ([]Mapping, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT source, field, value, role_id::text
		FROM them.tenant_role_mappings
		WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Mapping
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.Source, &m.Field, &m.Value, &m.RoleID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppHasGrants returns true when tenant_role_grants has at least one row for the app.
func (q *PgxQuerier) AppHasGrants(ctx context.Context, applicationID string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM them.tenant_role_grants
			WHERE application_id = $1
		)
	`, applicationID).Scan(&exists)
	return exists, err
}

// RoleHasGrant returns true when the role has a grant for the application.
func (q *PgxQuerier) RoleHasGrant(ctx context.Context, roleID, applicationID string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM them.tenant_role_grants
			WHERE role_id = $1 AND application_id = $2
		)
	`, roleID, applicationID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}
