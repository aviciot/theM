// Package runrecorder persists run lifecycle events to the database.
// It wraps DB calls behind an interface so tests can inject a fake.
package runrecorder

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/aviciot/them/internal/domain"
)

// DBQuerier is the database interface needed by the Recorder.
// The production implementation uses pgxpool; tests inject a fake.
type DBQuerier interface {
	// Exec executes a statement and discards the result.
	Exec(ctx context.Context, sql string, args ...any) error
	// QueryRow executes a query expected to return at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
}

// SingleRowScanner scans a single database row.
type SingleRowScanner interface {
	Scan(dest ...any) error
}

// eventsTransportStreams is the value written to the them.runs.events_transport
// column for every new run. The Go worker always writes run events to Redis
// Streams and the bridge always reads them from there, so "streams" is the only
// valid value. It must satisfy the CHECK constraint in db/025_events_transport.sql.
const eventsTransportStreams = "streams"

// ArtifactMaxBytes is the maximum size of a file artifact in bytes (1 MiB).
const ArtifactMaxBytes = 1 << 20 // 1 MiB

// ErrArtifactTooLarge is returned when the artifact data exceeds ArtifactMaxBytes.
var ErrArtifactTooLarge = errors.New("runrecorder: artifact exceeds 1 MiB limit")

// ErrMissingTenantID is returned by CreateRun when run.TenantID is empty.
// them.runs.tenant_id is UUID NOT NULL — a nil/empty value would fail at the
// DB level. Callers must supply TenantID from epconfig.EPConfig, never from
// client-supplied request data.
var ErrMissingTenantID = errors.New("runrecorder: TenantID must not be empty")

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
// The events_transport column is always set to "streams" unless
// run.EventsTransport is explicitly provided (non-empty), in which case that
// value is used verbatim.
//
// TenantID is required: them.runs.tenant_id is UUID NOT NULL. An empty TenantID
// returns ErrMissingTenantID immediately — no DB call is made. TenantID must come
// from epconfig.EPConfig at run-creation time, never from client-supplied data.
//
// Note: them.runs has no context_id or application_id columns. Those fields are
// tracked in domain.Run for in-memory routing only. Application linkage is
// recoverable at query time via entry_point_slug → entry_points.application_id.
func (r *Recorder) CreateRun(ctx context.Context, run domain.Run) error {
	if run.TenantID == "" {
		return ErrMissingTenantID
	}
	const q = `
		INSERT INTO them.runs (id, tenant_id, entry_point_slug, status, started_at, events_transport, goal, orchestrator_name)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, NULLIF($7,''), NULLIF($8,''))
		ON CONFLICT (id) DO NOTHING`
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	transport := run.EventsTransport
	if transport == "" {
		transport = eventsTransportStreams
	}

	err := r.db.Exec(ctx, q,
		run.ID, run.TenantID, run.EntryPointSlug,
		string(domain.RunRunning), startedAt, transport,
		run.Goal, run.OrchestratorName,
	)
	if err != nil {
		return fmt.Errorf("runrecorder: create run %s: %w", run.ID, err)
	}
	return nil
}

// UpdateRunGoal sets the goal column on an existing run row.
// Called after the first client message is parsed so the user's text is recorded.
func (r *Recorder) UpdateRunGoal(ctx context.Context, runID, goal string) error {
	const q = `UPDATE them.runs SET goal=NULLIF($2,'') WHERE id=$1::uuid`
	if err := r.db.Exec(ctx, q, runID, goal); err != nil {
		return fmt.Errorf("runrecorder: update goal for run %s: %w", runID, err)
	}
	return nil
}

// UpdateRunStatus sets the status and error for the given run.
// Sets ended_at when the status is a terminal state (completed/failed/canceled).
func (r *Recorder) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error {
	terminal := status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCanceled
	var q string
	if terminal {
		q = `UPDATE them.runs SET status=$2, error=$3, ended_at=now() WHERE id=$1`
	} else {
		q = `UPDATE them.runs SET status=$2, error=$3 WHERE id=$1`
	}
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

// RecordUsage appends a token usage row for a run and rolls up totals on them.runs.
// provider and model are recorded for cost breakdown; costUSD is the per-call cost.
func (r *Recorder) RecordUsage(ctx context.Context, runID, provider, model string, inputTokens, outputTokens int, costUSD float64) error {
	const insert = `
		INSERT INTO them.run_usage (run_id, provider, model, tokens_input, tokens_output, cost_usd)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)`
	if err := r.db.Exec(ctx, insert, runID, provider, model, inputTokens, outputTokens, costUSD); err != nil {
		return fmt.Errorf("runrecorder: record usage for run %s: %w", runID, err)
	}
	// Roll up totals onto the parent run row.
	const rollup = `
		UPDATE them.runs SET
			total_tokens_in  = COALESCE(total_tokens_in,  0) + $2,
			total_tokens_out = COALESCE(total_tokens_out, 0) + $3,
			total_cost_usd   = COALESCE(total_cost_usd,   0) + $4
		WHERE id = $1::uuid`
	if err := r.db.Exec(ctx, rollup, runID, inputTokens, outputTokens, costUSD); err != nil {
		return fmt.Errorf("runrecorder: rollup usage for run %s: %w", runID, err)
	}
	return nil
}

// RecordAgentStep inserts a run_steps row for one agent invocation.
func (r *Recorder) RecordAgentStep(ctx context.Context, runID, agentSlug string, iteration int, inputJSON []byte, output string, latencyMS int64, status, stepErr string) error {
	const q = `
		INSERT INTO them.run_steps
			(run_id, iteration, agent_slug, input, output, status, latency_ms, started_at, ended_at, error)
		VALUES
			($1::uuid, $2, $3, $4::jsonb, NULLIF($5,''), $6, $7::integer, now() - make_interval(secs => $7::float8 / 1000), now(), NULLIF($8,''))`
	if err := r.db.Exec(ctx, q, runID, iteration, agentSlug, string(inputJSON), output, status, latencyMS, stepErr); err != nil {
		return fmt.Errorf("runrecorder: record agent step for run %s: %w", runID, err)
	}
	return nil
}

// SetFinalOutput writes the final LLM answer text to them.runs.final_output.
func (r *Recorder) SetFinalOutput(ctx context.Context, runID, text string) error {
	const q = `UPDATE them.runs SET final_output = NULLIF($2, '') WHERE id = $1::uuid`
	if err := r.db.Exec(ctx, q, runID, text); err != nil {
		return fmt.Errorf("runrecorder: set final output for run %s: %w", runID, err)
	}
	return nil
}

// RecordStep is kept for backward compatibility but is a no-op in the new schema.
// Use RecordAgentStep instead.
func (r *Recorder) RecordStep(ctx context.Context, runID, stepType, content string) error {
	return nil
}

// ArtifactInput carries the fields needed to persist a file artifact.
type ArtifactInput struct {
	RunID         string
	ApplicationID string // may be empty
	SessionID     string // may be empty
	Filename      string
	ContentType   string
	Data          []byte
}

// ArtifactMeta carries the metadata and data for a retrieved artifact.
// SECURITY: Data is raw bytes loaded from the DB. Never include in log output.
type ArtifactMeta struct {
	ID          string
	RunID       string
	Filename    string
	ContentType string
	Size        int64
	Data        []byte
}

// RecordArtifact persists a file artifact to them.run_artifacts.
// Returns ErrArtifactTooLarge if len(data) > ArtifactMaxBytes.
// Returns the artifact UUID on success.
// SECURITY: Data must never appear in log output.
func (r *Recorder) RecordArtifact(ctx context.Context, in ArtifactInput) (string, error) {
	if len(in.Data) > ArtifactMaxBytes {
		return "", ErrArtifactTooLarge
	}
	ct := in.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	filename := sanitizeFilename(in.Filename)
	if filename == "" {
		filename = "artifact"
	}

	// Nullable UUID fields: empty string means NULL.
	var appID, sessionID *string
	if in.ApplicationID != "" {
		appID = &in.ApplicationID
	}
	if in.SessionID != "" {
		sessionID = &in.SessionID
	}

	const q = `
		INSERT INTO them.run_artifacts (run_id, application_id, session_id, filename, content_type, size, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text`
	row := r.db.QueryRow(ctx, q, in.RunID, appID, sessionID, filename, ct, int64(len(in.Data)), in.Data)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", fmt.Errorf("runrecorder: record artifact for run %s: %w", in.RunID, err)
	}
	return id, nil
}

// GetArtifactScanStatus returns the scan_status for an artifact.
// Returns ("disabled", nil) if the column is not present or the row is not found.
// This is a lightweight query (no BYTEA fetch) used for the download gate check.
func (r *Recorder) GetArtifactScanStatus(ctx context.Context, artifactID string) (string, error) {
	const q = `SELECT COALESCE(scan_status, 'disabled') FROM them.run_artifacts WHERE id = $1::uuid`
	var status string
	if err := r.db.QueryRow(ctx, q, artifactID).Scan(&status); err != nil {
		return "disabled", nil // row not found or column missing → treat as disabled
	}
	return status, nil
}

// GetArtifact retrieves a file artifact from them.run_artifacts.
// The query enforces that artifact.run_id == runID so cross-run access is denied.
// SECURITY: Returned Data must never appear in log output.
func (r *Recorder) GetArtifact(ctx context.Context, runID, artifactID string) (ArtifactMeta, error) {
	const q = `
		SELECT id::text, run_id::text, filename, content_type, size, data
		FROM them.run_artifacts
		WHERE id = $1::uuid AND run_id = $2::uuid`
	row := r.db.QueryRow(ctx, q, artifactID, runID)
	var a ArtifactMeta
	if err := row.Scan(&a.ID, &a.RunID, &a.Filename, &a.ContentType, &a.Size, &a.Data); err != nil {
		return a, fmt.Errorf("runrecorder: get artifact %s: %w", artifactID, err)
	}
	return a, nil
}

// CreateTask inserts a child task row for an agent invocation.
// It looks up the agent UUID by slug+tenantID and writes a 'delegated'/'working' row.
// Returns the new task UUID string. Non-fatal: callers should log but continue on error.
func (r *Recorder) CreateTask(ctx context.Context, tenantID, runID, contextID, agentSlug string) (string, error) {
	const q = `
		WITH parent AS (
			SELECT id FROM them.tasks
			WHERE run_id = $2::uuid AND kind = 'root'
			LIMIT 1
		),
		ag AS (
			SELECT id FROM them.agents
			WHERE slug = $4 AND tenant_id = $1::uuid AND enabled = true
			LIMIT 1
		)
		INSERT INTO them.tasks (tenant_id, run_id, context_id, parent_task_id, agent_id, state, kind)
		SELECT $1::uuid, $2::uuid, $3::uuid, parent.id, ag.id, 'working', 'delegated'
		FROM parent CROSS JOIN ag
		RETURNING id::text`
	row := r.db.QueryRow(ctx, q, tenantID, runID, contextID, agentSlug)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", fmt.Errorf("runrecorder: create task (slug=%s): %w", agentSlug, err)
	}
	return id, nil
}

// CompleteTask marks a child task as completed or failed.
func (r *Recorder) CompleteTask(ctx context.Context, taskID string, success bool) error {
	state := "completed"
	if !success {
		state = "failed"
	}
	const q = `UPDATE them.tasks SET state=$2, updated_at=now() WHERE id=$1::uuid`
	if err := r.db.Exec(ctx, q, taskID, state); err != nil {
		return fmt.Errorf("runrecorder: complete task %s: %w", taskID, err)
	}
	return nil
}

// CompleteRootTask marks the root task for a run as completed or failed.
// Non-fatal: callers should log but continue on error.
func (r *Recorder) CompleteRootTask(ctx context.Context, runID string, success bool) error {
	state := "completed"
	if !success {
		state = "failed"
	}
	const q = `UPDATE them.tasks SET state=$2, updated_at=now() WHERE run_id=$1::uuid AND kind='root'`
	if err := r.db.Exec(ctx, q, runID, state); err != nil {
		return fmt.Errorf("runrecorder: complete root task for run %s: %w", runID, err)
	}
	return nil
}

// CreateRootTask ensures a root task row exists for (contextID, runID).
// Called before writing task_messages so the FK constraint is satisfied.
// Idempotent — ON CONFLICT DO NOTHING means repeated calls are safe.
func (r *Recorder) CreateRootTask(ctx context.Context, contextID, runID string) error {
	const q = `
INSERT INTO them.tasks (context_id, run_id, state, kind)
VALUES ($1::uuid, NULLIF($2, '')::uuid, 'working', 'root')
ON CONFLICT DO NOTHING`
	if err := r.db.Exec(ctx, q, contextID, runID); err != nil {
		return fmt.Errorf("runrecorder: create root task: %w", err)
	}
	return nil
}

// sanitizeFilename strips directory components and control characters from a
// filename to prevent path traversal attacks and unsafe filenames.
func sanitizeFilename(name string) string {
	// Strip any directory components (path traversal prevention).
	name = filepath.Base(name)
	// Replace null bytes and control characters.
	var sb strings.Builder
	for _, ru := range name {
		if ru >= 32 && ru != 127 && ru != '/' && ru != '\\' {
			sb.WriteRune(ru)
		}
	}
	result := strings.TrimSpace(sb.String())
	// Prevent hidden files from being served directly.
	if strings.HasPrefix(result, ".") {
		result = "file" + result
	}
	return result
}
