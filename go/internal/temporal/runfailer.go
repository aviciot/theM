package temporal

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
)

// RunFailureReporter reports a run that failed outside orchestrator.Run (which reports its own
// failures): marks the run failed, closes its root task and publishes an `error` event so the
// WS/SSE client stops waiting. Optional on Activities (nil = no reporting).
type RunFailureReporter interface {
	FailRun(ctx context.Context, runID, contextID, message string)
}

// RunStatusWriter updates a run's terminal status (satisfied by *runrecorder.Recorder; the recorder
// never overwrites an already-finished run).
type RunStatusWriter interface {
	UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error
}

// RootTaskCloser closes a run's root task (satisfied by *runrecorder.Recorder).
type RootTaskCloser interface {
	CompleteRootTask(ctx context.Context, runID string, success bool) error
}

// RunFailer implements RunFailureReporter. Every step is best-effort and independent, so one
// failing dependency cannot stop the client from learning the run failed.
type RunFailer struct {
	status RunStatusWriter
	tasks  RootTaskCloser // may be nil
	bus    event.Bus
}

// NewRunFailer builds a RunFailer. tasks may be nil.
func NewRunFailer(status RunStatusWriter, tasks RootTaskCloser, bus event.Bus) *RunFailer {
	return &RunFailer{status: status, tasks: tasks, bus: bus}
}

// FailRun marks the run failed, closes its root task and publishes the error event.
func (f *RunFailer) FailRun(ctx context.Context, runID, contextID, message string) {
	if f.status != nil {
		if err := f.status.UpdateRunStatus(ctx, runID, domain.RunStatusFailed, message); err != nil {
			slog.Warn("temporal: mark run failed", "run_id", runID, "error", err)
		}
	}
	if f.tasks != nil {
		if err := f.tasks.CompleteRootTask(ctx, runID, false); err != nil {
			slog.Warn("temporal: close root task of failed run", "run_id", runID, "error", err)
		}
	}
	if f.bus != nil {
		raw, _ := json.Marshal(map[string]string{"run_id": runID, "message": message})
		f.bus.Publish(ctx, event.Event{
			Topic: contextID, Type: "error", RunID: runID, ContextID: contextID,
			Payload: raw, Timestamp: time.Now(),
		})
	}
}
