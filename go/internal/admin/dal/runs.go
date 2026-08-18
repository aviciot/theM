package dal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
	started_at::text, COALESCE(ended_at::text, ''),
	CASE WHEN ended_at IS NOT NULL
	     THEN EXTRACT(EPOCH FROM (ended_at - started_at)) * 1000
	     ELSE NULL
	END`

// scanRun scans one run row from s.
func scanRun(s SingleRowScanner) (Run, error) {
	var r Run
	var durationMS *float64
	if err := s.Scan(
		&r.ID, &r.OrchestratorID, &r.OrchestratorName,
		&r.EntryPointSlug, &r.UserID, &r.SessionID,
		&r.Goal, &r.Status,
		&r.FinalOutput, &r.Error, &r.ParentRunID,
		&r.Iterations, &r.TotalTokensIn, &r.TotalTokensOut,
		&r.TotalCostUSD,
		&r.StartedAt, &r.EndedAt,
		&durationMS,
	); err != nil {
		return r, err
	}
	r.TotalTokens = r.TotalTokensIn + r.TotalTokensOut
	if durationMS != nil {
		ms := int64(*durationMS)
		r.DurationMS = &ms
	}
	// aliases for frontend compatibility
	r.CostUSD = r.TotalCostUSD
	r.UserMessage = r.Goal
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
// Agent name and slug are resolved via LEFT JOIN on them.agents.
// duration_ms is derived from (updated_at - created_at) for completed/failed tasks.
// tenantID is used to verify the run belongs to the caller's tenant.
func (d *DB) GetRunTasks(ctx context.Context, tenantID, runID string) ([]Task, error) {
	q := `SELECT t.id::text, COALESCE(t.parent_task_id::text, ''),
        COALESCE(t.agent_id::text, ''), COALESCE(t.orchestrator_id::text, ''),
        COALESCE(t.context_id::text, ''), t.state, t.kind,
        COALESCE(t.remote_task_id, ''), t.budget_tokens, t.tokens_used,
        COALESCE(t.error, ''), t.created_at::text, t.updated_at::text,
        COALESCE(a.display_name, ''), COALESCE(a.slug, ''),
        CASE WHEN t.state IN ('completed','failed','canceled')
             THEN EXTRACT(EPOCH FROM (t.updated_at - t.created_at)) * 1000
             ELSE NULL END
        FROM them.tasks t
        LEFT JOIN them.agents a ON a.id = t.agent_id
        WHERE t.run_id = $1::uuid
          AND EXISTS (SELECT 1 FROM them.runs r WHERE r.id = $1::uuid AND r.tenant_id = $2::uuid)
        ORDER BY t.created_at`
	rows, err := d.q.Query(ctx, q, runID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		var t Task
		var durMS *float64
		if err := rows.Scan(
			&t.ID, &t.ParentTaskID,
			&t.AgentID, &t.OrchestratorID,
			&t.ContextID, &t.State, &t.Kind,
			&t.RemoteTaskID, &t.BudgetTokens, &t.TokensUsed,
			&t.Error, &t.CreatedAt, &t.UpdatedAt,
			&t.AgentName, &t.AgentSlug,
			&durMS,
		); err != nil {
			return nil, err
		}
		if durMS != nil {
			ms := int64(*durMS)
			t.DurationMS = &ms
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// CancelRun sets a running run to "canceled" with ended_at=now() and a fixed error message.
// Returns pgx.ErrNoRows if the run does not exist or does not belong to the tenant.
// Returns ErrRunNotRunning if the run status is not "running".
func (d *DB) CancelRun(ctx context.Context, tenantID, runID string) (Run, error) {
	q := `UPDATE them.runs
	      SET status = 'canceled', ended_at = now(), error = 'Canceled by user'
	      WHERE id = $1::uuid AND tenant_id = $2::uuid AND status = 'running'
	      RETURNING ` + runSelectCols
	return scanRun(d.q.QueryRow(ctx, q, runID, tenantID))
}

// DeleteRun deletes a single run scoped to the tenant.
// Uses RETURNING id::text so that no matching row → pgx.ErrNoRows.
func (d *DB) DeleteRun(ctx context.Context, tenantID, runID string) error {
	var id string
	return d.q.ExecReturning(ctx,
		`DELETE FROM them.runs WHERE id = $1::uuid AND tenant_id = $2::uuid RETURNING id::text`,
		runID, tenantID,
	).Scan(&id)
}

// BulkDeleteRuns deletes up to 500 runs by ID, scoped to the tenant.
// Returns the number of rows deleted (via RETURNING).
func (d *DB) BulkDeleteRuns(ctx context.Context, tenantID string, runIDs []string) (int64, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}
	// Build $2, $3, … placeholders for the run ID list.
	args := make([]any, 0, len(runIDs)+1)
	args = append(args, tenantID)
	placeholders := make([]string, len(runIDs))
	for i, id := range runIDs {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+2)
	}
	q := fmt.Sprintf(
		`DELETE FROM them.runs WHERE tenant_id = $1::uuid AND id IN (%s) RETURNING id::text`,
		strings.Join(placeholders, ","),
	)
	rows, err := d.q.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// ContextSession is one row returned by ListContextSessions.
type ContextSession struct {
	ContextID       string `json:"context_id"`
	OrchestratorName string `json:"orchestrator_name"`
	TurnCount       int    `json:"turn_count"`
	Title           string `json:"title"`
	LastActive      string `json:"last_active"`
}

// ListContextSessions returns distinct conversation contexts for the given tenant.
// Each context is the unique context_id on tasks; we join runs to surface the
// orchestrator name and derive a title from the earliest run goal.
func (d *DB) ListContextSessions(ctx context.Context, tenantID, orchestrator string, limit int) ([]ContextSession, error) {
	q := `
		SELECT
			t.context_id::text,
			COALESCE(MAX(r.orchestrator_name), '') AS orchestrator_name,
			COUNT(DISTINCT r.id)                   AS turn_count,
			COALESCE(
				(SELECT COALESCE(rr.goal, '')
				 FROM them.runs rr
				 JOIN them.tasks tt ON tt.run_id = rr.id
				 WHERE tt.context_id = t.context_id
				   AND rr.tenant_id = $1::uuid
				 ORDER BY rr.started_at ASC LIMIT 1),
				''
			) AS title,
			MAX(r.started_at)::text AS last_active
		FROM them.tasks t
		JOIN them.runs r ON r.id = t.run_id
		WHERE r.tenant_id = $1::uuid
		  AND t.context_id IS NOT NULL
		  AND ($2 = '' OR r.orchestrator_name = $2)
		GROUP BY t.context_id
		ORDER BY MAX(r.started_at) DESC
		LIMIT $3`

	rows, err := d.q.Query(ctx, q, tenantID, orchestrator, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]ContextSession, 0)
	for rows.Next() {
		var s ContextSession
		if err := rows.Scan(&s.ContextID, &s.OrchestratorName, &s.TurnCount, &s.Title, &s.LastActive); err != nil {
			return nil, err
		}
		if s.Title == "" {
			s.Title = s.ContextID[:8]
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetContextArtifacts returns artifacts whose context_id matches the given contextID, tenant-scoped.
func (d *DB) GetContextArtifacts(ctx context.Context, tenantID, contextID string, limit int) ([]Artifact, error) {
	q := `SELECT a.id::text, a.task_id::text,
		COALESCE(a.context_id::text, ''), COALESCE(a.artifact_id, ''),
		COALESCE(a.name, ''), COALESCE(a.parts::text, '[]'),
		COALESCE(a.append_index, 0), COALESCE(a.last_chunk, false),
		a.created_at::text
		FROM them.artifacts a
		JOIN them.tasks t ON t.id = a.task_id
		LEFT JOIN them.runs r ON r.id = t.run_id
		WHERE a.context_id = $1::uuid
		  AND COALESCE(r.tenant_id, t.tenant_id) = $2::uuid
		ORDER BY a.created_at DESC
		LIMIT $3`
	rows, err := d.q.Query(ctx, q, contextID, tenantID, limit)
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

// GetRunArtifacts returns artifacts for a run via their tasks, ordered by created_at.
// tenantID is used to verify the run belongs to the caller's tenant.
func (d *DB) GetRunArtifacts(ctx context.Context, tenantID, runID string) ([]Artifact, error) {
	q := `SELECT a.id::text, a.task_id::text,
        COALESCE(a.context_id::text, ''), COALESCE(a.artifact_id, ''),
        COALESCE(a.name, ''), COALESCE(a.parts::text, '[]'),
        COALESCE(a.append_index, 0), COALESCE(a.last_chunk, false),
        a.created_at::text
        FROM them.artifacts a
        JOIN them.tasks t ON t.id = a.task_id
        WHERE t.run_id = $1::uuid
          AND EXISTS (SELECT 1 FROM them.runs r WHERE r.id = $1::uuid AND r.tenant_id = $2::uuid)
        ORDER BY a.created_at`
	rows, err := d.q.Query(ctx, q, runID, tenantID)
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

// ContextMessage is one chat turn from task_messages.
type ContextMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// GetContextMessages returns user/agent turns for the root task of a context,
// ordered by sequence. Only 'user' and 'agent' roles returned; summary messages
// and empty texts skipped. Parts are stored as JSONB envelope {canonical_role,
// summary?, parts: [...]}, so text is read from the envelope's parts array first;
// falls back to reading tm.parts directly for legacy rows.
func (d *DB) GetContextMessages(ctx context.Context, tenantID, contextID string, limit int) ([]ContextMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT tm.role, COALESCE(
		(SELECT p->>'text' FROM jsonb_array_elements(tm.parts->'parts') p WHERE p->>'type' = 'text' LIMIT 1),
		(SELECT p->>'text' FROM jsonb_array_elements(tm.parts) p WHERE p->>'type' = 'text' LIMIT 1),
		''
	)
	FROM them.task_messages tm
	JOIN them.tasks t ON t.id = tm.task_id
	WHERE t.context_id = $1::uuid AND t.kind = 'root'
	  AND t.tenant_id = $2::uuid
	  AND tm.role IN ('user', 'agent')
	  AND (tm.parts->>'summary')::boolean IS NOT TRUE
	ORDER BY tm.seq
	LIMIT $3`
	rows, err := d.q.Query(ctx, q, contextID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := make([]ContextMessage, 0)
	for rows.Next() {
		var m ContextMessage
		if err := rows.Scan(&m.Role, &m.Text); err != nil {
			return nil, err
		}
		if m.Text != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}
