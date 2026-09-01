package middleware

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier adapts pgxpool.Pool to the Querier and GateQuerier interfaces.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier creates a PgxQuerier backed by pool.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

func (q *PgxQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}

func (q *PgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

func (q *PgxQuerier) Query(ctx context.Context, sql string, args ...any) (RowScanner, error) {
	rows, err := q.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsWrapper{rows: rows}, nil
}

type pgxRowsWrapper struct {
	rows interface {
		Next() bool
		Scan(dest ...any) error
		Close()
		Err() error
	}
}

func (r *pgxRowsWrapper) Next() bool          { return r.rows.Next() }
func (r *pgxRowsWrapper) Scan(dst ...any) error { return r.rows.Scan(dst...) }
func (r *pgxRowsWrapper) Close() error          { r.rows.Close(); return r.rows.Err() }
