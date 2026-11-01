package appflow

import (
	"context"
	"log/slog"

	"github.com/aviciot/them/internal/domain"
)

// closeChildrenQuery closes what a finished run left open: run_steps (pending/running),
// tasks (submitted/working/input-required) and hil_approvals (pending → timed_out).
// $1 run id, $2 run status, $3 failure reason (” = leave error untouched). Idempotent.
// An empty existing error counts as unset (runs.error and friends default to ”).
// Same mapping as the reconciler's closeRunQuery (internal/reconciler/close.go), which
// covers runs whose workflow ended without running this activity.
const closeChildrenQuery = `
WITH s AS (
	UPDATE them.run_steps
	SET status = CASE WHEN $2::text = 'completed' THEN 'completed' ELSE 'failed' END,
	    ended_at = now(),
	    error = CASE WHEN NULLIF($3::text, '') IS NULL THEN error ELSE COALESCE(NULLIF(error, ''), $3::text) END
	WHERE run_id = $1::uuid AND status IN ('pending', 'running')
	RETURNING id
), t AS (
	UPDATE them.tasks
	SET state = CASE WHEN $2::text = 'completed' THEN 'completed'
	                 WHEN $2::text IN ('canceled', 'cancelled', 'stopped') THEN 'canceled'
	                 ELSE 'failed' END,
	    updated_at = now(),
	    error = CASE WHEN NULLIF($3::text, '') IS NULL THEN error ELSE COALESCE(NULLIF(error, ''), $3::text) END
	WHERE run_id = $1::uuid AND state IN ('submitted', 'working', 'input-required')
	RETURNING id
), h AS (
	UPDATE them.hil_approvals
	SET status = 'timed_out', decided_at = now(), updated_at = now()
	WHERE run_id = $1::uuid AND status = 'pending'
	RETURNING id
)
SELECT (SELECT count(*) FROM s) + (SELECT count(*) FROM t) + (SELECT count(*) FROM h)`

// closeRunChildren is best-effort: a failure is logged, never returned, so it cannot
// block the terminal status update or the done/error event the client is waiting for.
func (a *AppFlowActivities) closeRunChildren(ctx context.Context, runID string, status domain.RunStatus, reason string) {
	if a.DB == nil || runID == "" {
		return
	}
	var closed int64
	if err := a.DB.QueryRow(ctx, closeChildrenQuery, runID, string(status), reason).Scan(&closed); err != nil {
		slog.Error("appflow: closing run children failed — steps/tasks/approvals may stay open",
			"run_id", runID, "status", string(status), "error", err)
		return
	}
	if closed > 0 {
		slog.Info("appflow: closed open children of finished run", "run_id", runID, "status", string(status), "closed", closed)
	}
}
