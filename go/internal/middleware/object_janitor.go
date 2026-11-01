package middleware

import (
	"context"
	"log/slog"
	"time"
)

// ArtifactDeleter deletes one stored artifact object. Implemented by *storage.Client.
type ArtifactDeleter interface {
	DeleteArtifact(ctx context.Context, key string) error
}

// janitorMaxAttempts is how many times a queued deletion is retried before it is left in the
// table for an operator (last_error says why) instead of being retried every sweep forever.
const janitorMaxAttempts = 20

// ObjectJanitor deletes stored files whose run_artifacts row is gone. A trigger on
// them.run_artifacts (db/127) queues each deleted row's storage_key in them.storage_deletions;
// this sweep deletes the object and then the queue row. Runs inside them-middleware-worker
// (which owns the storage client) next to the quarantine Reaper.
type ObjectJanitor struct {
	q     ReaperQuerier
	store ArtifactDeleter // may be nil: nothing is deleted, rows stay queued
	log   *slog.Logger
	batch int
}

// NewObjectJanitor creates an ObjectJanitor.
func NewObjectJanitor(q ReaperQuerier, store ArtifactDeleter, log *slog.Logger) *ObjectJanitor {
	return &ObjectJanitor{q: q, store: store, log: log, batch: 100}
}

// Run sweeps every interval until ctx is cancelled.
func (j *ObjectJanitor) Run(ctx context.Context, interval time.Duration) {
	if j.log != nil {
		j.log.Info("object janitor: started", "interval", interval)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.SweepOnce(ctx)
		}
	}
}

type queuedDeletion struct {
	id  int64
	key string
}

// SweepOnce processes one batch of queued deletions.
func (j *ObjectJanitor) SweepOnce(ctx context.Context) {
	if j.store == nil {
		return
	}
	rows, err := j.q.Query(ctx, `
SELECT id, storage_key
FROM   them.storage_deletions
WHERE  bucket = 'artifacts' AND attempts < $1
ORDER  BY id
LIMIT  $2`, janitorMaxAttempts, j.batch)
	if err != nil {
		j.warn("object janitor: query failed", "err", err)
		return
	}
	var queue []queuedDeletion
	for rows.Next() {
		var d queuedDeletion
		if err := rows.Scan(&d.id, &d.key); err != nil {
			j.warn("object janitor: scan failed", "err", err)
			continue
		}
		queue = append(queue, d)
	}
	_ = rows.Close()

	deleted := 0
	for _, d := range queue {
		if err := j.store.DeleteArtifact(ctx, d.key); err != nil {
			j.warn("object janitor: object delete failed", "id", d.id, "key", d.key, "err", err)
			if uerr := j.q.Exec(ctx, `UPDATE them.storage_deletions SET attempts = attempts + 1, last_error = $2 WHERE id = $1`, d.id, err.Error()); uerr != nil {
				j.warn("object janitor: could not record failure", "id", d.id, "err", uerr)
			}
			continue
		}
		if err := j.q.Exec(ctx, `DELETE FROM them.storage_deletions WHERE id = $1`, d.id); err != nil {
			j.warn("object janitor: could not remove queue row", "id", d.id, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 && j.log != nil {
		j.log.Info("object janitor: deleted stored files of removed artifacts", "deleted", deleted)
	}
}

func (j *ObjectJanitor) warn(msg string, args ...any) {
	if j.log != nil {
		j.log.Warn(msg, args...)
	}
}
