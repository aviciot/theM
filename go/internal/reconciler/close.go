package reconciler

import (
	"fmt"

	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/temporal"
)

// stuckRun is one eligible "running" row plus what is needed to find its workflow.
type stuckRun struct {
	ID         string
	TenantID   string   // "" when runs.tenant_id is NULL
	ContextIDs []string // tasks.context_id of the run's tasks (orchestration workflow ID input)
	PastGrace  bool     // older than Config.NotFoundGrace
}

// workflowIDs lists the Temporal workflow IDs this run can have, most likely first:
// canvas/AppFlow run, orchestration session (one per context), and the bare run ID
// (legacy contract). Without a tenant only the bare run ID can be tried.
func (s stuckRun) workflowIDs() []string {
	var ids []string
	if s.TenantID != "" {
		ids = append(ids, appflow.WorkflowIDForRun(s.TenantID, s.ID))
		for _, c := range s.ContextIDs {
			ids = append(ids, temporal.WorkflowIDForContext(s.TenantID, c))
		}
	}
	return append(ids, s.ID)
}

// closeRunQuery moves one run to a terminal status and closes everything it left open,
// atomically in a single statement. $1 run id, $2 run status, $3 failure reason
// (” for completed). The WHERE status='running' guard makes it idempotent: if another
// writer already finalized the run, nothing (including the children) is touched.
// Children: run_steps (pending/running), tasks (submitted/working/input-required),
// hil_approvals (pending → timed_out).
const closeRunQuery = `
WITH r AS (
	UPDATE them.runs
	SET status = $2::text, ended_at = now(),
	    error = CASE WHEN NULLIF($3::text, '') IS NULL THEN error ELSE COALESCE(NULLIF(error, ''), $3::text) END
	WHERE id = $1::uuid AND status = 'running'
	RETURNING id
), s AS (
	UPDATE them.run_steps st
	SET status = CASE WHEN $2::text = 'completed' THEN 'completed' ELSE 'failed' END,
	    ended_at = now(),
	    error = CASE WHEN NULLIF($3::text, '') IS NULL THEN st.error ELSE COALESCE(NULLIF(st.error, ''), $3::text) END
	FROM r WHERE st.run_id = r.id AND st.status IN ('pending', 'running')
	RETURNING st.id
), t AS (
	UPDATE them.tasks tk
	SET state = CASE WHEN $2::text = 'completed' THEN 'completed'
	                 WHEN $2::text IN ('canceled', 'cancelled', 'stopped') THEN 'canceled'
	                 ELSE 'failed' END,
	    updated_at = now(),
	    error = CASE WHEN NULLIF($3::text, '') IS NULL THEN tk.error ELSE COALESCE(NULLIF(tk.error, ''), $3::text) END
	FROM r WHERE tk.run_id = r.id AND tk.state IN ('submitted', 'working', 'input-required')
	RETURNING tk.id
), h AS (
	UPDATE them.hil_approvals ha
	SET status = 'timed_out', decided_at = now(), updated_at = now()
	FROM r WHERE ha.run_id = r.id AND ha.status = 'pending'
	RETURNING ha.id
)
SELECT id FROM r`

// orphanReason is stored in runs.error when no workflow exists for an old running run.
const orphanReason = "reconciler: no Temporal workflow found for this run (orphaned)"

func temporalReason(status string) string {
	return fmt.Sprintf("reconciler: Temporal workflow %s", status)
}
