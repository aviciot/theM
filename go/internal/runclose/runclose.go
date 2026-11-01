// Package runclose holds the SQL that closes what a finished run left open.
// Shared by the AppFlow finalize activity and the admin "cancel run" path; the
// reconciler (internal/reconciler/close.go) uses the same mapping inside a single
// statement that also updates the run row.
package runclose

// ChildrenQuery closes run_steps (pending/running), tasks (submitted/working/input-required)
// and hil_approvals (pending → timed_out) of one run. Idempotent.
// $1 run id, $2 run status, $3 failure reason (” = leave error untouched).
// An empty existing error counts as unset (the error columns default to ”).
// The caller must already have verified the run belongs to the right tenant.
const ChildrenQuery = `
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
