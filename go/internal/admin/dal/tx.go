package dal

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// txQuerier adapts pgx.Tx to dal.Querier for internal transactional use.
type txQuerier struct{ tx pgx.Tx }

func (t *txQuerier) Query(ctx context.Context, sql string, args ...any) (RowScanner, error) {
	rows, err := t.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &txRowsWrapper{rows: rows}, nil
}
func (t *txQuerier) QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return t.tx.QueryRow(ctx, sql, args...)
}
func (t *txQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.tx.Exec(ctx, sql, args...)
	return err
}
func (t *txQuerier) ExecReturning(ctx context.Context, sql string, args ...any) SingleRowScanner {
	return t.tx.QueryRow(ctx, sql, args...)
}

// txRowsWrapper adapts pgx.Rows to dal.RowScanner.
type txRowsWrapper struct{ rows pgx.Rows }

func (w *txRowsWrapper) Next() bool         { return w.rows.Next() }
func (w *txRowsWrapper) Scan(dest ...any) error { return w.rows.Scan(dest...) }
func (w *txRowsWrapper) Close() error       { w.rows.Close(); return nil }

// runInTx executes fn inside a new transaction from pool.
// Commits on success; rolls back on fn error using a separate cleanup context
// so a cancelled request context does not prevent the ROLLBACK from reaching the server.
func runInTx(ctx context.Context, pool *pgxpool.Pool, fn func(q Querier) error) error {
	pgTx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = pgTx.Rollback(cleanupCtx) }()

	if err := fn(&txQuerier{tx: pgTx}); err != nil {
		return err
	}
	return pgTx.Commit(ctx)
}
