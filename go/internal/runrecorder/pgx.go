package runrecorder

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPoolQuerier wraps a pgxpool.Pool to implement DBQuerier.
type PgxPoolQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxPoolQuerier wraps the given pool as a DBQuerier.
func NewPgxPoolQuerier(pool *pgxpool.Pool) *PgxPoolQuerier {
	return &PgxPoolQuerier{pool: pool}
}

// Exec executes sql on the pool, discarding the result.
func (q *PgxPoolQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}

// QueryRow executes sql on the pool and returns a single-row scanner.
// pgxpool.Pool.QueryRow already satisfies SingleRowScanner (it returns pgx.Row).
func (q *PgxPoolQuerier) QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}
