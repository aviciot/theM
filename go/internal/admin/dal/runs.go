package dal

import (
	"context"
)

// runSelectCols is the column list shared by ListRuns and GetRun queries.
const runSelectCols = `
	id::text,
	COALESCE(orchestrator_id::text, ''), COALESCE(orchestrator_name, ''),
	COALESCE(entry_point_slug, ''), user_id, COALESCE(session_id::text, ''),
	COALESCE(goal, ''), status,
	COALESCE(final_output, ''), COALESCE(error, ''), COALESCE(parent_run_id::text, ''),
	iterations, total_tokens_in, total_tokens_out,
	COALESCE(total_cost_usd::text, '0'),
	started_at::text, COALESCE(ended_at::text, '')`

// scanRun scans one run row from s.
func scanRun(s SingleRowScanner) (Run, error) {
	var r Run
	if err := s.Scan(
		&r.ID, &r.OrchestratorID, &r.OrchestratorName,
		&r.EntryPointSlug, &r.UserID, &r.SessionID,
		&r.Goal, &r.Status,
		&r.FinalOutput, &r.Error, &r.ParentRunID,
		&r.Iterations, &r.TotalTokensIn, &r.TotalTokensOut,
		&r.TotalCostUSD,
		&r.StartedAt, &r.EndedAt,
	); err != nil {
		return r, err
	}
	r.TotalTokens = r.TotalTokensIn + r.TotalTokensOut
	return r, nil
}

// runRowToSingle adapts a RowScanner to SingleRowScanner for use inside the
// multi-row loop in ListRuns.
type runRowToSingle struct{ r RowScanner }

func (a *runRowToSingle) Scan(dest ...any) error { return a.r.Scan(dest...) }

// ListRuns returns the most recent runs up to limit. When contextID is
// non-empty only runs whose root task matches that context_id are returned.
func (d *DB) ListRuns(ctx context.Context, contextID string, limit int) ([]Run, error) {
	var (
		rows RowScanner
		err  error
	)

	if contextID != "" {
		q := "SELECT " + runSelectCols + `
			FROM them.runs r
			JOIN them.tasks t ON t.run_id = r.id AND t.kind = 'root'
			WHERE t.context_id = $1::uuid
			ORDER BY r.started_at DESC LIMIT $2`
		rows, err = d.q.Query(ctx, q, contextID, limit)
	} else {
		q := "SELECT " + runSelectCols + `
			FROM them.runs
			ORDER BY started_at DESC LIMIT $1`
		rows, err = d.q.Query(ctx, q, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]Run, 0)
	for rows.Next() {
		run, err := scanRun(&runRowToSingle{r: rows})
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// GetRun returns a single run by UUID id.
func (d *DB) GetRun(ctx context.Context, runID string) (Run, error) {
	q := "SELECT " + runSelectCols + " FROM them.runs WHERE id = $1::uuid"
	return scanRun(d.q.QueryRow(ctx, q, runID))
}

// GetRunContextID returns the context_id of the root task for a given run UUID.
// context_id lives on them.tasks (not them.runs); it is used to build the
// Temporal workflow ID ("ctx-{context_id}") for HITL signal routing.
func (d *DB) GetRunContextID(ctx context.Context, runID string) (string, error) {
	row := d.q.QueryRow(ctx,
		`SELECT context_id::text FROM them.tasks WHERE run_id = $1::uuid AND kind = 'root' LIMIT 1`,
		runID)
	var contextID string
	if err := row.Scan(&contextID); err != nil {
		return "", err
	}
	return contextID, nil
}
