// Package runrecorder persists run lifecycle events to the database.
// It wraps DB calls behind an interface so tests can inject a fake.
package runrecorder

import (
	"context"
	"fmt"
	"time"

	"github.com/aviciot/them/internal/domain"
)

// DBQuerier is the database interface needed by the Recorder.
// The production implementation uses pgxpool; tests inject a fake.
type DBQuerier interface {
	// Exec executes a statement and discards the result.
	Exec(ctx context.Context, sql string, args ...any) error
}

// Recorder writes run lifecycle events to the database.
type Recorder struct {
	db DBQuerier
}

// New creates a Recorder backed by the given DBQuerier.
func New(db DBQuerier) *Recorder {
	return &Recorder{db: db}
}

// NewRecorder is an alias for New for backward compatibility.
func NewRecorder(db DBQuerier) *Recorder {
	return New(db)
}

// CreateRun inserts a new run row in them.runs with status "running".
func (r *Recorder) CreateRun(ctx context.Context, run domain.Run) error {
	const q = `
		INSERT INTO them.runs (id, context_id, application_id, entry_point_slug, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	err := r.db.Exec(ctx, q,
		run.ID, run.ContextID, run.ApplicationID, run.EntryPointSlug,
		string(domain.RunRunning), startedAt,
	)
	if err != nil {
		return fmt.Errorf("runrecorder: create run %s: %w", run.ID, err)
	}
	return nil
}

// UpdateRunStatus sets the status and error_message for the given run.
func (r *Recorder) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error {
	const q = `UPDATE them.runs SET status=$2, error_message=$3, updated_at=now() WHERE id=$1`
	err := r.db.Exec(ctx, q, runID, string(status), errMsg)
	if err != nil {
		return fmt.Errorf("runrecorder: update status for run %s: %w", runID, err)
	}
	return nil
}

// UpdateStatus is a compatibility wrapper over UpdateRunStatus with no error message.
func (r *Recorder) UpdateStatus(ctx context.Context, runID string, status domain.RunStatus) error {
	return r.UpdateRunStatus(ctx, runID, status, "")
}

// RecordUsage inserts or updates token usage for a run.
func (r *Recorder) RecordUsage(ctx context.Context, runID string, inputTokens, outputTokens int) error {
	const q = `
		INSERT INTO them.run_usage (run_id, input_tokens, output_tokens, recorded_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id) DO UPDATE
			SET input_tokens = excluded.input_tokens,
			    output_tokens = excluded.output_tokens,
			    recorded_at = excluded.recorded_at`
	err := r.db.Exec(ctx, q, runID, inputTokens, outputTokens, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("runrecorder: record usage for run %s: %w", runID, err)
	}
	return nil
}

// RecordStep inserts a step record for a run.
func (r *Recorder) RecordStep(ctx context.Context, runID, stepType, content string) error {
	const q = `INSERT INTO them.run_steps (run_id, step_type, content) VALUES ($1, $2, $3)`
	err := r.db.Exec(ctx, q, runID, stepType, content)
	if err != nil {
		return fmt.Errorf("runrecorder: record step for run %s: %w", runID, err)
	}
	return nil
}
