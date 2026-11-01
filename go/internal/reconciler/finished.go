package reconciler

import (
	"context"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/runclose"
)

// finishedChildrenQuery finds runs that already reached a terminal status but still have open
// steps, tasks or approvals (e.g. the best-effort close in FinalizeRunActivity failed, or the
// run was ended by a path that does not close children). $1 grace seconds, $2 limit.
// The marker comment lets tests tell this query apart from the stuck-run query.
const finishedChildrenQuery = `
	-- finished-children
	SELECT r.id::text, r.status
	FROM them.runs r
	WHERE r.status <> 'running'
	  AND COALESCE(r.ended_at, r.started_at) < now() - ($1 * interval '1 second')
	  AND (EXISTS (SELECT 1 FROM them.run_steps s WHERE s.run_id = r.id AND s.status IN ('pending', 'running'))
	    OR EXISTS (SELECT 1 FROM them.tasks t WHERE t.run_id = r.id AND t.state IN ('submitted', 'working', 'input-required'))
	    OR EXISTS (SELECT 1 FROM them.hil_approvals h WHERE h.run_id = r.id AND h.status = 'pending'))
	ORDER BY COALESCE(r.ended_at, r.started_at)
	LIMIT $2`

const finishedReason = "reconciler: run finished with open steps/tasks/approvals"

type finishedRun struct {
	ID     string
	Status string
}

// closeFinishedRunChildren closes the open children of already-finished runs. Runs younger than
// Config.ChildrenGrace are skipped so it never races a finalize that is still in flight.
func (r *Reconciler) closeFinishedRunChildren(ctx context.Context) {
	rows, err := r.db.Query(ctx, finishedChildrenQuery, int(r.cfg.ChildrenGrace.Seconds()), r.cfg.BatchSize)
	if err != nil {
		r.logger.Error("reconciler: failed to query finished runs with open children", "error", err)
		metricErrors.Inc()
		return
	}
	var runs []finishedRun
	for rows.Next() {
		var fr finishedRun
		if err := rows.Scan(&fr.ID, &fr.Status); err != nil {
			r.logger.Warn("reconciler: scan error (finished runs)", "error", err)
			continue
		}
		runs = append(runs, fr)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		r.logger.Error("reconciler: finished-runs rows iteration error", "error", err)
		metricErrors.Inc()
		return
	}

	for _, fr := range runs {
		if r.cfg.DryRun {
			r.logger.Info("reconciler: [dry-run] would close children of finished run", "run_id", fr.ID, "run_status", fr.Status)
			metricDryRun.Inc()
			continue
		}
		reason := ""
		if fr.Status != string(domain.RunCompleted) {
			reason = finishedReason
		}
		if _, err := r.db.Exec(ctx, runclose.ChildrenQuery, fr.ID, fr.Status, reason); err != nil {
			r.logger.Error("reconciler: failed to close children of finished run", "run_id", fr.ID, "error", err)
			metricErrors.Inc()
			continue
		}
		r.logger.Info("reconciler: closed open children of finished run", "run_id", fr.ID, "run_status", fr.Status)
		metricChildrenClosed.Inc()
	}
}
