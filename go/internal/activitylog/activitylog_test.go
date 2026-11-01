package activitylog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

func okActivity(ctx context.Context) error        { return nil }
func failingActivity(ctx context.Context) error   { return errors.New("boom: node exploded") }
func quietActivity(ctx context.Context) error     { return nil }
func cancelledActivity(ctx context.Context) error { return context.Canceled }

func run(t *testing.T, level slog.Level, fn interface{}, quiet ...string) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{New(log, quiet...)}})
	env.RegisterActivity(fn)
	_, _ = env.ExecuteActivity(fn) // errors are the activity's own result, asserted via the log
	return buf.String()
}

func TestSuccessIsLoggedAtInfoWithNameAndDuration(t *testing.T) {
	out := run(t, slog.LevelInfo, okActivity)
	for _, want := range []string{"activity done", "activity=", "okActivity", "workflow_id=", "attempt=1", "duration_ms="} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

func TestFailureIsLoggedAsWarnWithError(t *testing.T) {
	out := run(t, slog.LevelInfo, failingActivity)
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "activity failed") || !strings.Contains(out, "boom: node exploded") {
		t.Errorf("a failure must be a WARN carrying the error:\n%s", out)
	}
}

// A cancelled activity is not an error condition worth a warning.
func TestCancellationIsNotAWarning(t *testing.T) {
	out := run(t, slog.LevelInfo, cancelledActivity)
	if strings.Contains(out, "level=WARN") || !strings.Contains(out, "activity canceled") {
		t.Errorf("cancellation must be logged at info as canceled:\n%s", out)
	}
}

// High-volume activities (per-node trace events) stay out of the info log but remain available at debug.
func TestQuietActivitiesAreDebugOnly(t *testing.T) {
	if out := run(t, slog.LevelInfo, quietActivity, "quietActivity"); strings.Contains(out, "activity done") {
		t.Errorf("a quiet activity must not log at info:\n%s", out)
	}
	if out := run(t, slog.LevelDebug, quietActivity, "quietActivity"); !strings.Contains(out, "activity done") {
		t.Errorf("a quiet activity must still log at debug:\n%s", out)
	}
	if out := run(t, slog.LevelInfo, okActivity, "quietActivity"); !strings.Contains(out, "activity done") {
		t.Errorf("other activities are unaffected by the quiet list:\n%s", out)
	}
}
