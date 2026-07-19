package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier wraps a pgxpool.Pool to satisfy admin.DBQuerier.
// It translates pgx row types to the RowScanner / SingleRowScanner interfaces.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier wraps pool as a DBQuerier for admin handlers.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

// Query runs a SELECT and returns a RowScanner.
func (q *PgxQuerier) Query(ctx context.Context, sql string, args ...any) (RowScanner, error) {
	rows, err := q.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRowsWrapper{rows: rows}, nil
}

// QueryRow runs a SELECT and returns a single-row scanner.
func (q *PgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

// Exec executes a statement and discards the result.
func (q *PgxQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}

// ExecReturning executes a statement with a RETURNING clause and returns a scanner.
func (q *PgxQuerier) ExecReturning(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

// ── pgxRowsWrapper ────────────────────────────────────────────────────────────

// pgxRowsWrapper adapts pgx.Rows to admin.RowScanner.
type pgxRowsWrapper struct {
	rows pgx.Rows
}

func (w *pgxRowsWrapper) Next() bool  { return w.rows.Next() }
func (w *pgxRowsWrapper) Close() error { w.rows.Close(); return nil }
func (w *pgxRowsWrapper) Scan(dest ...any) error {
	return w.rows.Scan(dest...)
}

// ErrNoRows is returned by SingleRowScanner.Scan when no row was found.
// It aliases pgx.ErrNoRows so callers can use errors.Is.
var ErrNoRows = pgx.ErrNoRows

// IsNotFound returns true if err represents a "no rows" condition.
func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
