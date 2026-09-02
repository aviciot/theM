package middleware

import (
	"context"
	"log/slog"
	"time"
)

// ExpiredQuarantineRow is one row returned by ScanExpired.
type ExpiredQuarantineRow struct {
	ID         string
	StorageKey string // may be empty if bytes were already deleted
}

// ReaperQuerier is the DB interface the Reaper needs.
type ReaperQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (RowScanner, error)
	Exec(ctx context.Context, sql string, args ...any) error
}

// Reaper deletes expired quarantine objects from MinIO and the DB.
// It runs as a background goroutine inside them-middleware-worker.
type Reaper struct {
	q     ReaperQuerier
	store ObjectStore // may be nil — skip MinIO delete if unavailable
	log   *slog.Logger
}

// NewReaper creates a Reaper.
func NewReaper(q ReaperQuerier, store ObjectStore, log *slog.Logger) *Reaper {
	return &Reaper{q: q, store: store, log: log}
}

// Run loops forever, waking every interval to reap expired quarantine rows.
// Returns when ctx is cancelled.
func (r *Reaper) Run(ctx context.Context, interval time.Duration) {
	if r.log != nil {
		r.log.Info("quarantine reaper: started", "interval", interval)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if r.log != nil {
				r.log.Info("quarantine reaper: stopped")
			}
			return
		case <-ticker.C:
			r.ReapOnce(ctx)
		}
	}
}

// ReapOnce performs a single reap cycle. Exported for testing.
func (r *Reaper) ReapOnce(ctx context.Context) {
	r.reap(ctx)
}

func (r *Reaper) reap(ctx context.Context) {
	rows, err := r.scanExpired(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Error("quarantine reaper: scan failed", "err", err)
		}
		return
	}
	if len(rows) == 0 {
		return
	}
	if r.log != nil {
		r.log.Info("quarantine reaper: found expired rows", "count", len(rows))
	}

	deleted := 0
	for _, row := range rows {
		if err := r.deleteOne(ctx, row); err != nil {
			if r.log != nil {
				r.log.Warn("quarantine reaper: failed to delete row", "id", row.ID, "err", err)
			}
			continue
		}
		deleted++
	}
	if deleted > 0 && r.log != nil {
		r.log.Info("quarantine reaper: reaped", "deleted", deleted)
	}
}

func (r *Reaper) scanExpired(ctx context.Context) ([]ExpiredQuarantineRow, error) {
	const q = `
SELECT id::text, COALESCE(storage_key, '')
FROM   them.quarantine_artifacts
WHERE  expires_at < now()`

	rows, err := r.q.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []ExpiredQuarantineRow
	for rows.Next() {
		var row ExpiredQuarantineRow
		if err := rows.Scan(&row.ID, &row.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Close()
}

func (r *Reaper) deleteOne(ctx context.Context, row ExpiredQuarantineRow) error {
	// Delete MinIO bytes first — if the DB delete succeeds but MinIO fails,
	// we still want to retry next tick (row still in DB), so MinIO goes first.
	if r.store != nil && row.StorageKey != "" {
		if err := r.store.DeleteQuarantine(ctx, row.StorageKey); err != nil {
			// Log but don't abort — bytes may already be gone via MinIO TTL policy.
			if r.log != nil {
				r.log.Warn("quarantine reaper: MinIO delete failed", "id", row.ID, "key", row.StorageKey, "err", err)
			}
		}
	}

	const q = `DELETE FROM them.quarantine_artifacts WHERE id = $1::uuid`
	return r.q.Exec(ctx, q, row.ID)
}
