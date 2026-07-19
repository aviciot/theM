// Package runrecorder writes run lifecycle events to the them.runs,
// them.run_steps, and them.run_usage tables in PostgreSQL.
// It uses pgx/v5 directly — no ORM.
//
// All SQL interactions go through the DBQuerier interface so tests can inject a
// mock without a live database.
package runrecorder

import (
	"context"
	"fmt"
	"time"

	"github.com/aviciot/them/internal/domain"
)

// DBQuerier is the minimal database interface required by Recorder.
// Both pgx.Conn and pgxpool.Pool satisfy this interface.
type DBQuerier interface {
	// Exec executes a query that returns no rows (INSERT, UPDATE, DELETE).
	Exec(ctx context.Context, sql string, args ...any) error
}

// Recorder writes run state to PostgreSQL.
type Recorder struct {
	db DBQuerier
}

// New creates a Recorder backed by db.
func New(db DBQuerier) *Recorder {
	return &Recorder{db: db}
}

// CreateRun inserts a new run record into them.runs. If a run with the same ID
// already exists the insert is silently ignored (ON CONFLICT DO NOTHING).
func (r *Recorder) CreateRun(ctx context.Context, run domain.Run) error {
	const sql = `
INSERT INTO them.runs
	(id, context_id, application_id, entry_point_slug, status, started_at)
VALUES
	($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING`

	if err := r.db.Exec(ctx, sql,
		run.ID,
		run.ContextID,
		run.ApplicationID,
		run.EntryPointSlug,
		string(run.Status),
		run.StartedAt,
	); err != nil {
		return fmt.Errorf("runrecorder: CreateRun %s: %w", run.ID, err)
	}
	return nil
}

// UpdateRunStatus marks a run as finished with the given status. ended_at is
// set to NOW() and error_message is recorded (pass empty string for success).
func (r *Recorder) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error {
	const sql = `
UPDATE them.runs
SET    status        = $2,
       ended_at      = NOW(),
       error_message = $3
WHERE  id = $1`

	if err := r.db.Exec(ctx, sql, runID, string(status), errMsg); err != nil {
		return fmt.Errorf("runrecorder: UpdateRunStatus %s: %w", runID, err)
	}
	return nil
}

// RecordUsage upserts token usage for a run. If a row for run_id already
// exists (e.g. from a partial update) it is overwritten.
func (r *Recorder) RecordUsage(ctx context.Context, runID string, inputTokens, outputTokens int) error {
	const sql = `
INSERT INTO them.run_usage
	(run_id, input_tokens, output_tokens, recorded_at)
VALUES
	($1, $2, $3, $4)
ON CONFLICT (run_id) DO UPDATE
	SET input_tokens  = EXCLUDED.input_tokens,
	    output_tokens = EXCLUDED.output_tokens,
	    recorded_at   = EXCLUDED.recorded_at`

	if err := r.db.Exec(ctx, sql, runID, inputTokens, outputTokens, time.Now()); err != nil {
		return fmt.Errorf("runrecorder: RecordUsage %s: %w", runID, err)
	}
	return nil
}

// RecordStep appends a step record to them.run_steps. stepType is a short
// label such as "llm_response", "tool_call", or "final_answer". content is
// the serialised step payload.
func (r *Recorder) RecordStep(ctx context.Context, runID, stepType, content string) error {
	const sql = `
INSERT INTO them.run_steps
	(run_id, step_type, content, created_at)
VALUES
	($1, $2, $3, NOW())`

	if err := r.db.Exec(ctx, sql, runID, stepType, content); err != nil {
		return fmt.Errorf("runrecorder: RecordStep %s/%s: %w", runID, stepType, err)
	}
	return nil
}
