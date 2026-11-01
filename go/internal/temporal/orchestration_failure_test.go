package temporal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	temporalactivity "go.temporal.io/sdk/activity"
	temporalerr "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/temporal/workerconfig"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

type failCall struct{ runID, contextID, message string }

type fakeReporter struct{ calls []failCall }

func (f *fakeReporter) FailRun(_ context.Context, runID, contextID, message string) {
	f.calls = append(f.calls, failCall{runID, contextID, message})
}

type fakeStatusWriter struct {
	calls []string
	err   error
}

func (f *fakeStatusWriter) UpdateRunStatus(_ context.Context, runID string, st domain.RunStatus, msg string) error {
	f.calls = append(f.calls, fmt.Sprintf("%s|%s|%s", runID, st, msg))
	return f.err
}

type fakeTaskCloser struct {
	calls []string
	err   error
}

func (f *fakeTaskCloser) CompleteRootTask(_ context.Context, runID string, ok bool) error {
	f.calls = append(f.calls, fmt.Sprintf("%s|%v", runID, ok))
	return f.err
}

type fakeBus struct{ events []event.Event }

func (b *fakeBus) Subscribe(context.Context, string, int) (<-chan event.Event, <-chan event.Event, func()) {
	return nil, nil, func() {}
}
func (b *fakeBus) Publish(_ context.Context, e event.Event) { b.events = append(b.events, e) }

type failingFactory struct{}

func (failingFactory) Build(workerconfig.RunConfig) (temporal.OrchestratorRunner, error) {
	return nil, errors.New("no API key for provider")
}

// ── RunFailer ─────────────────────────────────────────────────────────────────

func TestRunFailer_MarksFailedClosesTaskPublishesError(t *testing.T) {
	st, tk, bus := &fakeStatusWriter{}, &fakeTaskCloser{}, &fakeBus{}
	temporal.NewRunFailer(st, tk, bus).FailRun(context.Background(), "run-1", "ctx-1", "boom")

	assert.Equal(t, []string{"run-1|failed|boom"}, st.calls)
	assert.Equal(t, []string{"run-1|false"}, tk.calls)
	require.Len(t, bus.events, 1)
	e := bus.events[0]
	assert.Equal(t, "error", e.Type)
	assert.Equal(t, "ctx-1", e.Topic)
	assert.Equal(t, "run-1", e.RunID)
	var payload map[string]string
	require.NoError(t, json.Unmarshal(e.Payload, &payload))
	assert.Equal(t, map[string]string{"run_id": "run-1", "message": "boom"}, payload)
}

// One failing dependency must not stop the client from learning the run failed.
func TestRunFailer_StepsAreIndependent(t *testing.T) {
	st, tk, bus := &fakeStatusWriter{err: errors.New("db down")}, &fakeTaskCloser{err: errors.New("db down")}, &fakeBus{}
	temporal.NewRunFailer(st, tk, bus).FailRun(context.Background(), "run-2", "ctx-2", "boom")

	assert.Len(t, bus.events, 1, "the error event is published even when the DB writes fail")
	assert.Len(t, tk.calls, 1, "the task is still attempted after the status update failed")
}

func TestRunFailer_NilTaskCloser(t *testing.T) {
	st, bus := &fakeStatusWriter{}, &fakeBus{}
	temporal.NewRunFailer(st, nil, bus).FailRun(context.Background(), "run-3", "ctx-3", "boom")
	assert.Len(t, st.calls, 1)
	assert.Len(t, bus.events, 1)
}

// ── Activity: failures before orchestrator.Run are reported ───────────────────

func preRunInput() temporal.WorkflowInput {
	return temporal.WorkflowInput{
		RunID: "run-pre", ContextID: "ctx-pre", TenantID: "t1", ApplicationID: "a1", AppOrchestratorID: "orch-1",
	}
}

func TestRunOrchestratorActivity_ConfigLoadError_ReportsFailure(t *testing.T) {
	rep := &fakeReporter{}
	acts := &temporal.Activities{
		ConfigLoader: &fakeConfigLoader{err: fmt.Errorf("db timeout host=10.0.0.5")},
		Factory:      &fakeOrchestratorFactory{},
		RunFailer:    rep,
	}
	_, err := acts.RunOrchestratorActivity(context.Background(), preRunInput())
	require.Error(t, err)
	require.Len(t, rep.calls, 1)
	assert.Equal(t, "run-pre", rep.calls[0].runID)
	assert.Equal(t, "ctx-pre", rep.calls[0].contextID)
	assert.Contains(t, rep.calls[0].message, "failed to load orchestrator config")
	assert.NotContains(t, rep.calls[0].message, "10.0.0.5", "internal error detail must not reach the client")
}

func TestRunOrchestratorActivity_BuildError_ReportsFailure(t *testing.T) {
	rep := &fakeReporter{}
	acts := &temporal.Activities{
		ConfigLoader: &fakeConfigLoader{cfg: workerconfig.RunConfig{}},
		Factory:      failingFactory{},
		RunFailer:    rep,
	}
	_, err := acts.RunOrchestratorActivity(context.Background(), preRunInput())
	require.Error(t, err)
	require.Len(t, rep.calls, 1)
	assert.Contains(t, rep.calls[0].message, "no API key for provider", "build errors are user-actionable (missing key)")
}

func TestRunOrchestratorActivity_InvalidInput_ReportsWhenRunKnown(t *testing.T) {
	rep := &fakeReporter{}
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}, RunFailer: rep}
	_, err := acts.RunOrchestratorActivity(context.Background(), temporal.WorkflowInput{RunID: "run-x", ContextID: "ctx-x", ApplicationID: "a1"}) // no tenant
	require.Error(t, err)
	require.Len(t, rep.calls, 1)
	assert.Equal(t, "run-x", rep.calls[0].runID)

	rep2 := &fakeReporter{}
	acts2 := &temporal.Activities{Runner: &fakeOrchestratorRunner{}, RunFailer: rep2}
	_, err = acts2.RunOrchestratorActivity(context.Background(), temporal.WorkflowInput{TenantID: "t1", ApplicationID: "a1"}) // no run id
	require.Error(t, err)
	assert.Empty(t, rep2.calls, "nothing to report without a run id")
}

func TestReportRunFailureActivity_Delegates(t *testing.T) {
	rep := &fakeReporter{}
	acts := &temporal.Activities{RunFailer: rep}
	require.NoError(t, acts.ReportRunFailureActivity(context.Background(), temporal.ReportRunFailureInput{RunID: "r", ContextID: "c", Message: "m"}))
	assert.Equal(t, []failCall{{"r", "c", "m"}}, rep.calls)

	assert.NoError(t, (&temporal.Activities{}).ReportRunFailureActivity(context.Background(), temporal.ReportRunFailureInput{RunID: "r"}), "no reporter = no-op")
}

// ── Workflow: abnormal activity endings are reported ──────────────────────────

type orchWorkflowEnv struct {
	env      *testsuite.TestWorkflowEnvironment
	reported *[]temporal.ReportRunFailureInput
}

func newOrchWorkflowEnv(t *testing.T) orchWorkflowEnv {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	acts := &temporal.Activities{}
	env.RegisterWorkflow(temporal.OrchestrationWorkflow)
	env.RegisterActivityWithOptions(acts.RunOrchestratorActivity, temporalactivity.RegisterOptions{Name: temporal.RunOrchestratorActivityName})
	env.RegisterActivityWithOptions(acts.ReportRunFailureActivity, temporalactivity.RegisterOptions{Name: temporal.ReportRunFailureActivityName})
	var reported []temporal.ReportRunFailureInput
	env.OnActivity(temporal.ReportRunFailureActivityName, mock.Anything, mock.Anything).Return(nil).Maybe().
		Run(func(args mock.Arguments) { reported = append(reported, args.Get(1).(temporal.ReportRunFailureInput)) })
	return orchWorkflowEnv{env: env, reported: &reported}
}

var orchInput = temporal.WorkflowInput{RunID: "run-w", ContextID: "ctx-w", TenantID: "t1", ApplicationID: "a1"}

func TestOrchestrationWorkflow_ActivityTimeout_ReportsRunFailure(t *testing.T) {
	w := newOrchWorkflowEnv(t)
	// What the SDK hands the workflow when the activity exceeds StartToClose (or loses its worker).
	w.env.OnActivity(temporal.RunOrchestratorActivityName, mock.Anything, mock.Anything).
		Return(temporal.WorkflowResult{}, temporalerr.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil))

	w.env.ExecuteWorkflow(temporal.OrchestrationWorkflow, orchInput)

	require.True(t, w.env.IsWorkflowCompleted())
	require.Error(t, w.env.GetWorkflowError())
	require.Len(t, *w.reported, 1)
	assert.Equal(t, temporal.ReportRunFailureInput{RunID: "run-w", ContextID: "ctx-w", Message: "run timed out"}, (*w.reported)[0])
}

func TestOrchestrationWorkflow_Cancelled_ReportsRunFailure(t *testing.T) {
	w := newOrchWorkflowEnv(t)
	w.env.OnActivity(temporal.RunOrchestratorActivityName, mock.Anything, mock.Anything).
		After(5*time.Minute).Return(temporal.WorkflowResult{Status: domain.RunStatusCompleted}, nil)
	w.env.RegisterDelayedCallback(func() { w.env.CancelWorkflow() }, time.Minute)

	w.env.ExecuteWorkflow(temporal.OrchestrationWorkflow, orchInput)

	require.True(t, w.env.IsWorkflowCompleted())
	require.Error(t, w.env.GetWorkflowError())
	require.Len(t, *w.reported, 1, "a cancelled run must be reported (disconnected context)")
	assert.Equal(t, "run canceled", (*w.reported)[0].Message)
}

// Errors the activity itself raised were already reported where they happened: no second report.
func TestOrchestrationWorkflow_ActivityReportedItself_NotReportedAgain(t *testing.T) {
	w := newOrchWorkflowEnv(t)
	w.env.OnActivity(temporal.RunOrchestratorActivityName, mock.Anything, mock.Anything).
		Return(temporal.WorkflowResult{Status: domain.RunStatusFailed},
			temporalerr.NewNonRetryableApplicationError("RunOrchestratorActivity: failed to load orchestrator config", "ConfigLoadError", nil))

	w.env.ExecuteWorkflow(temporal.OrchestrationWorkflow, orchInput)

	require.True(t, w.env.IsWorkflowCompleted())
	require.Error(t, w.env.GetWorkflowError())
	assert.Empty(t, *w.reported)
}

func TestOrchestrationWorkflow_Success_NoReport(t *testing.T) {
	w := newOrchWorkflowEnv(t)
	w.env.OnActivity(temporal.RunOrchestratorActivityName, mock.Anything, mock.Anything).
		Return(temporal.WorkflowResult{FinalText: "hi", Status: domain.RunStatusCompleted}, nil)

	w.env.ExecuteWorkflow(temporal.OrchestrationWorkflow, orchInput)

	require.True(t, w.env.IsWorkflowCompleted())
	require.NoError(t, w.env.GetWorkflowError())
	assert.Empty(t, *w.reported)
}
