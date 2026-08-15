package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

// ListRuns returns the most recent runs up to limit for the given tenant.
// When contextID is non-empty only runs whose root task matches that context_id are returned.
func (d *DB) ListRuns(ctx context.Context, tenantID, contextID string, limit int) ([]Run, error) {
	var (
		rows RowScanner
		err  error
	)

	if contextID != "" {
		q := "SELECT " + runSelectCols + `
			FROM them.runs r
			JOIN them.tasks t ON t.run_id = r.id AND t.kind = 'root'
			WHERE t.context_id = $1::uuid AND r.tenant_id = $2::uuid
			ORDER BY r.started_at DESC LIMIT $3`
		rows, err = d.q.Query(ctx, q, contextID, tenantID, limit)
	} else {
		q := "SELECT " + runSelectCols + `
			FROM them.runs
			WHERE tenant_id = $1::uuid
			ORDER BY started_at DESC LIMIT $2`
		rows, err = d.q.Query(ctx, q, tenantID, limit)
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

// GetRun returns a single run by UUID id, scoped to the tenant.
// Returns pgx.ErrNoRows when not found or when it belongs to another tenant.
func (d *DB) GetRun(ctx context.Context, tenantID, runID string) (Run, error) {
	q := "SELECT " + runSelectCols + " FROM them.runs WHERE id = $1::uuid AND tenant_id = $2::uuid"
	return scanRun(d.q.QueryRow(ctx, q, runID, tenantID))
}

// GetRunContextID returns the context_id of the root task for a given run UUID, scoped to the tenant.
// context_id lives on them.tasks (not them.runs); it is used to build the
// Temporal workflow ID ("ctx-{context_id}") for HITL signal routing.
func (d *DB) GetRunContextID(ctx context.Context, tenantID, runID string) (string, error) {
	row := d.q.QueryRow(ctx,
		`SELECT t.context_id::text
		 FROM them.tasks t
		 JOIN them.runs r ON r.id = t.run_id
		 WHERE t.run_id = $1::uuid AND t.kind = 'root' AND r.tenant_id = $2::uuid
		 LIMIT 1`,
		runID, tenantID)
	var contextID string
	if err := row.Scan(&contextID); err != nil {
		return "", err
	}
	return contextID, nil
}

// GetRunStats returns aggregate run counts and total cost for the tenant.
func (d *DB) GetRunStats(ctx context.Context, tenantID string) (RunStats, error) {
	q := `SELECT status, COUNT(*)::int, COALESCE(SUM(total_cost_usd), 0)::text
          FROM them.runs WHERE tenant_id = $1::uuid
          GROUP BY status`
	rows, err := d.q.Query(ctx, q, tenantID)
	if err != nil {
		return RunStats{}, err
	}
	defer rows.Close()

	stats := RunStats{
		ByStatus:     make(map[string]int),
		TotalCostUSD: "0",
	}
	var totalCost float64
	for rows.Next() {
		var status string
		var count int
		var costStr string
		if err := rows.Scan(&status, &count, &costStr); err != nil {
			return RunStats{}, err
		}
		stats.Total += count
		if count > 0 {
			stats.ByStatus[status] = count
		}
		// parse cost per status row and accumulate
		if c, err2 := strconv.ParseFloat(costStr, 64); err2 == nil {
			totalCost += c
		}
	}
	stats.TotalCostUSD = fmt.Sprintf("%.6f", totalCost)
	return stats, nil
}

// GetRunDetail returns a run with its steps, usage rows, and child runs.
func (d *DB) GetRunDetail(ctx context.Context, tenantID, runID string) (RunDetail, error) {
	run, err := d.GetRun(ctx, tenantID, runID)
	if err != nil {
		return RunDetail{}, err
	}

	detail := RunDetail{
		Run:      run,
		Steps:    []RunStep{},
		Usage:    []RunUsage{},
		Children: []Run{},
	}

	// Steps
	stepsQ := `SELECT id::text, iteration,
        COALESCE(agent_slug, ''), COALESCE(tool_call_id, ''),
        COALESCE(input::text, ''), COALESCE(output, ''),
        COALESCE(status, ''), COALESCE(error, ''),
        latency_ms, started_at::text, COALESCE(ended_at::text, '')
        FROM them.run_steps WHERE run_id = $1::uuid ORDER BY started_at`
	srows, err := d.q.Query(ctx, stepsQ, runID)
	if err != nil {
		return RunDetail{}, err
	}
	defer srows.Close()
	for srows.Next() {
		var s RunStep
		var inputStr string
		if err := srows.Scan(
			&s.ID, &s.Iteration,
			&s.AgentSlug, &s.ToolCallID,
			&inputStr, &s.Output,
			&s.Status, &s.Error,
			&s.LatencyMS, &s.StartedAt, &s.EndedAt,
		); err != nil {
			return RunDetail{}, err
		}
		if inputStr != "" {
			s.Input = json.RawMessage(inputStr)
		}
		detail.Steps = append(detail.Steps, s)
	}

	// Usage
	usageQ := `SELECT COALESCE(provider, ''), COALESCE(model, ''),
        tokens_input, tokens_output, COALESCE(cost_usd::text, '0')
        FROM them.run_usage WHERE run_id = $1::uuid ORDER BY created_at`
	urows, err := d.q.Query(ctx, usageQ, runID)
	if err != nil {
		return RunDetail{}, err
	}
	defer urows.Close()
	for urows.Next() {
		var u RunUsage
		if err := urows.Scan(&u.Provider, &u.Model, &u.TokensIn, &u.TokensOut, &u.CostUSD); err != nil {
			return RunDetail{}, err
		}
		detail.Usage = append(detail.Usage, u)
	}

	// Children (runs with parent_run_id = this run)
	childQ := "SELECT " + runSelectCols + ` FROM them.runs
        WHERE parent_run_id = $1::uuid AND tenant_id = $2::uuid
        ORDER BY started_at`
	crows, err := d.q.Query(ctx, childQ, runID, tenantID)
	if err != nil {
		return RunDetail{}, err
	}
	defer crows.Close()
	for crows.Next() {
		child, err := scanRun(&runRowToSingle{r: crows})
		if err != nil {
			return RunDetail{}, err
		}
		detail.Children = append(detail.Children, child)
	}

	return detail, nil
}

// GetRunTasks returns tasks belonging to a run, ordered by created_at.
func (d *DB) GetRunTasks(ctx context.Context, runID string) ([]Task, error) {
	q := `SELECT id::text, COALESCE(parent_task_id::text, ''),
        COALESCE(agent_id::text, ''), COALESCE(orchestrator_id::text, ''),
        COALESCE(context_id::text, ''), state, kind,
        COALESCE(remote_task_id, ''), budget_tokens, tokens_used,
        COALESCE(error, ''), created_at::text, updated_at::text
        FROM them.tasks WHERE run_id = $1::uuid ORDER BY created_at`
	rows, err := d.q.Query(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		var t Task
		if err := rows.Scan(
			&t.ID, &t.ParentTaskID,
			&t.AgentID, &t.OrchestratorID,
			&t.ContextID, &t.State, &t.Kind,
			&t.RemoteTaskID, &t.BudgetTokens, &t.TokensUsed,
			&t.Error, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// GetRunArtifacts returns artifacts for a run via their tasks, ordered by created_at.
func (d *DB) GetRunArtifacts(ctx context.Context, runID string) ([]Artifact, error) {
	q := `SELECT a.id::text, a.task_id::text,
        COALESCE(a.context_id::text, ''), COALESCE(a.artifact_id, ''),
        COALESCE(a.name, ''), COALESCE(a.parts::text, '[]'),
        COALESCE(a.append_index, 0), COALESCE(a.last_chunk, false),
        a.created_at::text
        FROM them.artifacts a
        JOIN them.tasks t ON t.id = a.task_id
        WHERE t.run_id = $1::uuid
        ORDER BY a.created_at`
	rows, err := d.q.Query(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := make([]Artifact, 0)
	for rows.Next() {
		var a Artifact
		var partsJSON string
		if err := rows.Scan(
			&a.ID, &a.TaskID,
			&a.ContextID, &a.ArtifactID,
			&a.Name, &partsJSON,
			&a.AppendIndex, &a.LastChunk,
			&a.CreatedAt,
		); err != nil {
			return nil, err
		}
		if partsJSON != "" && partsJSON != "[]" {
			_ = json.Unmarshal([]byte(partsJSON), &a.Parts)
		}
		if a.Parts == nil {
			a.Parts = []ArtifactPart{}
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, nil
}
