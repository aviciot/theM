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

	"github.com/aviciot/them/internal/config"
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

// eventsTransportPubSub / eventsTransportStreams are the two valid values of the
// them.runs.events_transport column (Phase 11c). They must match the CHECK
// constraint in db/025_events_transport.sql.
const (
	eventsTransportPubSub  = "pubsub"
	eventsTransportStreams = "streams"
)

// ArtifactMaxBytes is the maximum size of a file artifact in bytes (1 MiB).
const ArtifactMaxBytes = 1 << 20 // 1 MiB

// ErrArtifactTooLarge is returned when the artifact data exceeds ArtifactMaxBytes.
var ErrArtifactTooLarge = errors.New("runrecorder: artifact exceeds 1 MiB limit")

// Recorder writes run lifecycle events to the database.
type Recorder struct {
	db DBQuerier
	// runEventsMode decides the events_transport value written on new runs.
	// pubsub → "pubsub"; dual/streams → "streams". Injected at construction so
	// the value is decided once at startup, not threaded through call sites.
	runEventsMode config.RunEventsMode
}

// New creates a Recorder backed by the given DBQuerier. The events transport
// mode defaults to pubsub; use WithRunEventsMode to override at startup.
func New(db DBQuerier) *Recorder {
	return &Recorder{db: db, runEventsMode: config.RunEventsModePublish}
}

// NewRecorder is an alias for New for backward compatibility.
func NewRecorder(db DBQuerier) *Recorder {
	return New(db)
}

// WithRunEventsMode sets the run-events mode used to derive the events_transport
// column on new runs. Call once at startup in main.go. Returns the receiver for
// chaining.
func (r *Recorder) WithRunEventsMode(mode config.RunEventsMode) *Recorder {
	r.runEventsMode = mode
	return r
}

// eventsTransport returns the events_transport value to store on a new run row,
// based on the configured mode. pubsub mode → "pubsub" (Go reads Pub/Sub);
// dual/streams mode → "streams" (Python Lua publishes to the stream; Go reads it).
func (r *Recorder) eventsTransport() string {
	if r.runEventsMode == config.RunEventsModeDual || r.runEventsMode == config.RunEventsModeStreams {
		return eventsTransportStreams
	}
	return eventsTransportPubSub
}

// CreateRun inserts a new run row in them.runs with status "running".
// The events_transport column is set from the configured RunEventsMode unless
// run.EventsTransport is explicitly provided (non-empty), in which case that
// value is used verbatim.
func (r *Recorder) CreateRun(ctx context.Context, run domain.Run) error {
	const q = `
		INSERT INTO them.runs (id, context_id, application_id, entry_point_slug, status, started_at, events_transport)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO NOTHING`
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	transport := run.EventsTransport
	if transport == "" {
		transport = r.eventsTransport()
	}
	err := r.db.Exec(ctx, q,
		run.ID, run.ContextID, run.ApplicationID, run.EntryPointSlug,
		string(domain.RunRunning), startedAt, transport,
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
