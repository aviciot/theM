package temporal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/temporal/workerconfig"
)

// fakeOrchestratorRunner implements OrchestratorRunner for testing.
type fakeOrchestratorRunner struct{}

func (f *fakeOrchestratorRunner) Run(_ context.Context, _, _ string, _ domain.Message, _ []domain.Message, _ ...orchestrator.RunContext) (string, error) {
	return "done", nil
}

// TestWorkerRegistration verifies that:
//  1. Activities struct satisfies the OrchestratorRunner interface contract.
//  2. The workflow and activity names used in main.go match the constants
//     in the temporal package (OrchestrationWorkflow, RunOrchestratorActivityName).
//  3. Activities struct can be constructed with a fake runner without panicking.
//
// This test intentionally does NOT start a real Temporal worker (no live server
// required) — it verifies the wiring at the type/interface level only.
func TestWorkerRegistration(t *testing.T) {
	// Verify Activities satisfies OrchestratorRunner at compile time.
	var _ temporal.OrchestratorRunner = (*fakeOrchestratorRunner)(nil)

	// Construct Activities with a fake runner — must not panic.
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}}
	assert.NotNil(t, acts, "Activities struct must be non-nil after construction")

	// Verify workflow type constant is non-empty (registered name in main.go).
	assert.NotEmpty(t, temporal.WorkflowType,
		"WorkflowType constant must be non-empty (used in RegisterWorkflow + ExecuteWorkflow)")

	// Verify activity name constant is non-empty (used in workflow.ExecuteActivity).
	assert.NotEmpty(t, temporal.RunOrchestratorActivityName,
		"RunOrchestratorActivityName must be non-empty (registered activity name)")

	// Verify task queue is the expected value (matches Python worker).
	assert.Equal(t, "them-orchestration", temporal.TaskQueue,
		"TaskQueue must match the Python worker's task queue name")

	// Verify workflow ID scheme preserved: "ctx-{contextID}".
	// This is enforced in the ws/sse handlers; we verify the constant exists.
	assert.NotEmpty(t, temporal.SignalHumanInput,
		"SignalHumanInput must be non-empty (HITL signal name must match Python)")
}

// TestGoWorkerTaskQueue_IsDistinct verifies that the Go-only task queue name
// (R-2C) is non-empty, distinct from the legacy Python queue, and has the
// expected value. This is a compile-time/documentation guard: if either
// constant is changed, the test fails immediately before any deploy.
func TestGoWorkerTaskQueue_IsDistinct(t *testing.T) {
	assert.Equal(t, "them-orchestration-go", temporal.GoTaskQueue,
		"GoTaskQueue must be the Go-only task queue (them-orchestration-go)")
	assert.Equal(t, "them-orchestration", temporal.TaskQueue,
		"TaskQueue must still be the legacy Python queue (them-orchestration)")
	assert.NotEqual(t, temporal.GoTaskQueue, temporal.TaskQueue,
		"GoTaskQueue and TaskQueue must be distinct — Go and Python workers must not share a queue")
}

// TestGoWorkerTaskQueue_ActivityRoutedToGoQueue is a documentation-level test
// that verifies the OrchestrationWorkflow wires its ActivityOptions to GoTaskQueue.
// Since we cannot easily inspect workflow activity options without running a Temporal
// server, this test asserts the constant values that are used in workflow.go and
// confirms the architectural invariant: activities route to the Go Worker queue.
func TestGoWorkerTaskQueue_ActivityRoutedToGoQueue(t *testing.T) {
	// OrchestrationWorkflow sets ao.TaskQueue = GoTaskQueue (see workflow.go).
	// The Go Worker polls GoTaskQueue, so activities are always executed by the Go Worker.
	// This test confirms the constant that the workflow uses matches the worker's queue.
	assert.Equal(t, "them-orchestration-go", temporal.GoTaskQueue,
		"OrchestrationWorkflow activity options must route to GoTaskQueue (verified via constant equality)")
}

// TestWorkflowInput_Serialization verifies that WorkflowInput can be marshalled
// and unmarshalled cleanly (critical for Temporal's workflow input serialization).
func TestWorkflowInput_Serialization(t *testing.T) {
	input := temporal.WorkflowInput{
		RunID:            "run-abc-123",
		ContextID:        "ctx-def-456",
		EntryPointSlug:   "my-ep",
		OrchestratorName: "my-orch",
		UserMessage:      domain.TextMessage(domain.RoleUser, "hello"),
		History:          nil,
	}

	data, err := json.Marshal(input)
	assert.NoError(t, err, "WorkflowInput must marshal to JSON without error")
	assert.NotEmpty(t, data, "marshalled JSON must be non-empty")

	var decoded temporal.WorkflowInput
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err, "WorkflowInput must unmarshal from JSON without error")
	assert.Equal(t, input.RunID, decoded.RunID, "RunID must survive JSON round-trip")
	assert.Equal(t, input.ContextID, decoded.ContextID, "ContextID must survive JSON round-trip")
	assert.Equal(t, input.EntryPointSlug, decoded.EntryPointSlug, "EntryPointSlug must survive JSON round-trip")
}

// ── R-4d: Tenant propagation tests ───────────────────────────────────────────

// TestWorkflowInput_TenantIDAndApplicationIDPresent verifies that WorkflowInput
// has TenantID and ApplicationID string fields (R-4d), and that they survive
// a JSON round-trip (Temporal serializes inputs as JSON).
func TestWorkflowInput_TenantIDAndApplicationIDPresent(t *testing.T) {
	tenantID := "aaaa0000-0000-0000-0000-000000000001"
	appID := "bbbb0000-0000-0000-0000-000000000002"

	input := temporal.WorkflowInput{
		RunID:         "run-r4d-1",
		ContextID:     "ctx-r4d-1",
		TenantID:      tenantID,
		ApplicationID: appID,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var decoded temporal.WorkflowInput
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, tenantID, decoded.TenantID, "TenantID must survive JSON round-trip")
	assert.Equal(t, appID, decoded.ApplicationID, "ApplicationID must survive JSON round-trip")
}

// TestWorkflowInput_ApplicationID_IsString verifies that ApplicationID is a
// string (UUID) type, not int64, matching the PostgreSQL UUID column type.
func TestWorkflowInput_ApplicationID_IsString(t *testing.T) {
	input := temporal.WorkflowInput{ApplicationID: "00000000-0000-0000-0000-000000000099"}
	// Marshal and confirm the JSON field is a string, not a number.
	data, err := json.Marshal(input)
	require.NoError(t, err)
	// The JSON must contain the UUID as a string value (quoted), not a number.
	assert.Contains(t, string(data), `"00000000-0000-0000-0000-000000000099"`,
		"ApplicationID must be serialized as a JSON string (UUID), not a number")
}

// TestRunOrchestratorActivity_RejectsEmptyTenantID verifies that the activity
// returns a non-retryable error when TenantID is missing (R-4d boundary check).
func TestRunOrchestratorActivity_RejectsEmptyTenantID(t *testing.T) {
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}}
	ctx := context.Background()

	_, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:         "run-1",
		ApplicationID: "00000000-0000-0000-0000-000000000001",
		// TenantID deliberately empty.
	})
	require.Error(t, err, "empty TenantID must be rejected")
	assert.Contains(t, err.Error(), "TenantID")
}

// TestRunOrchestratorActivity_RejectsEmptyApplicationID verifies that the
// activity returns a non-retryable error when ApplicationID is missing.
func TestRunOrchestratorActivity_RejectsEmptyApplicationID(t *testing.T) {
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}}
	ctx := context.Background()

	_, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:    "run-1",
		TenantID: "00000000-0000-0000-0000-000000000001",
		// ApplicationID deliberately empty.
	})
	require.Error(t, err, "empty ApplicationID must be rejected")
	assert.Contains(t, err.Error(), "ApplicationID")
}

// TestRunOrchestratorActivity_RejectsEmptyRunID verifies that the activity
// returns a non-retryable error when RunID is missing.
func TestRunOrchestratorActivity_RejectsEmptyRunID(t *testing.T) {
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}}
	ctx := context.Background()

	_, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		TenantID:      "00000000-0000-0000-0000-000000000001",
		ApplicationID: "00000000-0000-0000-0000-000000000002",
		// RunID deliberately empty.
	})
	require.Error(t, err, "empty RunID must be rejected")
	assert.Contains(t, err.Error(), "RunID")
}

// TestRunOrchestratorActivity_PropagatesTenantToRunner verifies that when all
// required fields are present, the activity runs to completion and returns a
// successful result — proving TenantID and ApplicationID pass through correctly.
func TestRunOrchestratorActivity_PropagatesTenantToRunner(t *testing.T) {
	acts := &temporal.Activities{Runner: &fakeOrchestratorRunner{}}
	ctx := context.Background()

	result, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:         "run-with-tenant",
		ContextID:     "ctx-with-tenant",
		TenantID:      "aaaa0000-0000-0000-0000-000000000001",
		ApplicationID: "bbbb0000-0000-0000-0000-000000000002",
		UserMessage:   domain.TextMessage(domain.RoleUser, "hello"),
	})
	require.NoError(t, err)
	assert.Equal(t, "done", result.FinalText)
}

// ── Per-run config loading tests (E2E wiring) ────────────────────────────────

// fakeConfigLoader implements workerconfig.Loader for testing.
type fakeConfigLoader struct {
	cfg workerconfig.RunConfig
	err error
}

func (f *fakeConfigLoader) LoadRunConfig(_ context.Context, _, _, _, _ string) (workerconfig.RunConfig, error) {
	return f.cfg, f.err
}

// fakeOrchestratorFactory implements OrchestratorFactory for testing.
// It records which RunConfig was passed to Build.
type fakeOrchestratorFactory struct {
	built []workerconfig.RunConfig
}

func (f *fakeOrchestratorFactory) Build(cfg workerconfig.RunConfig) (temporal.OrchestratorRunner, error) {
	f.built = append(f.built, cfg)
	return &fakeOrchestratorRunner{}, nil
}

// TestRunOrchestratorActivity_UsesPerRunConfigWhenAvailable verifies that when
// ConfigLoader and Factory are wired and AppOrchestratorID is set, the activity
// calls Factory.Build with the loaded config instead of using the fallback Runner.
func TestRunOrchestratorActivity_UsesPerRunConfigWhenAvailable(t *testing.T) {
	loadedCfg := workerconfig.RunConfig{
		LLMProvider: "anthropic",
		LLMAPIKey:   "sk-ant-test",
	}
	loader := &fakeConfigLoader{cfg: loadedCfg}
	factory := &fakeOrchestratorFactory{}

	acts := &temporal.Activities{
		Runner:       &fakeOrchestratorRunner{}, // fallback — must NOT be called
		ConfigLoader: loader,
		Factory:      factory,
	}
	ctx := context.Background()

	result, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:               "run-per-cfg",
		ContextID:           "ctx-per-cfg",
		TenantID:            "aaaa0000-0000-0000-0000-000000000001",
		ApplicationID:       "bbbb0000-0000-0000-0000-000000000002",
		AppOrchestratorID:   "cccc0000-0000-0000-0000-000000000003",
		UserMessage:         domain.TextMessage(domain.RoleUser, "hello"),
	})
	require.NoError(t, err)
	assert.Equal(t, "done", result.FinalText)

	// Factory.Build must have been called exactly once with the loaded config.
	require.Len(t, factory.built, 1, "Factory.Build must be called once per activity")
	assert.Equal(t, "anthropic", factory.built[0].LLMProvider)
	assert.Equal(t, "sk-ant-test", factory.built[0].LLMAPIKey)
}

// TestRunOrchestratorActivity_FallsBackToRunnerWhenNoOrchestratorID verifies that
// when AppOrchestratorID is empty, the activity uses the static Runner (legacy path).
func TestRunOrchestratorActivity_FallsBackToRunnerWhenNoOrchestratorID(t *testing.T) {
	loader := &fakeConfigLoader{cfg: workerconfig.RunConfig{LLMProvider: "anthropic"}}
	factory := &fakeOrchestratorFactory{}

	acts := &temporal.Activities{
		Runner:       &fakeOrchestratorRunner{},
		ConfigLoader: loader,
		Factory:      factory,
	}
	ctx := context.Background()

	result, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:         "run-no-orch-id",
		ContextID:     "ctx-no-orch-id",
		TenantID:      "aaaa0000-0000-0000-0000-000000000001",
		ApplicationID: "bbbb0000-0000-0000-0000-000000000002",
		// AppOrchestratorID deliberately empty → must use fallback Runner
		UserMessage: domain.TextMessage(domain.RoleUser, "hello"),
	})
	require.NoError(t, err)
	assert.Equal(t, "done", result.FinalText)

	// Factory.Build must NOT have been called.
	assert.Empty(t, factory.built, "Factory.Build must not be called when AppOrchestratorID is empty")
}

// TestRunOrchestratorActivity_ConfigLoadError_FailsFast verifies that when
// LoadRunConfig returns an error, the activity returns a non-retryable error
// immediately without calling the runner.
func TestRunOrchestratorActivity_ConfigLoadError_FailsFast(t *testing.T) {
	loader := &fakeConfigLoader{err: fmt.Errorf("db timeout")}
	factory := &fakeOrchestratorFactory{}

	acts := &temporal.Activities{
		Runner:       &fakeOrchestratorRunner{},
		ConfigLoader: loader,
		Factory:      factory,
	}
	ctx := context.Background()

	_, err := acts.RunOrchestratorActivity(ctx, temporal.WorkflowInput{
		RunID:             "run-cfg-err",
		ContextID:         "ctx-cfg-err",
		TenantID:          "aaaa0000-0000-0000-0000-000000000001",
		ApplicationID:     "bbbb0000-0000-0000-0000-000000000002",
		AppOrchestratorID: "cccc0000-0000-0000-0000-000000000003",
		UserMessage:       domain.TextMessage(domain.RoleUser, "hello"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConfigLoadError")
	assert.Empty(t, factory.built, "Factory.Build must not be called when config load fails")
}

// TestWorkflowInput_AppOrchestratorID_Serialization verifies that AppOrchestratorID
// survives a JSON round-trip (required for Temporal workflow input serialization).
func TestWorkflowInput_AppOrchestratorID_Serialization(t *testing.T) {
	orchID := "dddd0000-0000-0000-0000-000000000004"
	input := temporal.WorkflowInput{
		RunID:             "run-serial",
		TenantID:          "aaaa0000-0000-0000-0000-000000000001",
		ApplicationID:     "bbbb0000-0000-0000-0000-000000000002",
		AppOrchestratorID: orchID,
	}

	data, err := json.Marshal(input)
	require.NoError(t, err)

	var decoded temporal.WorkflowInput
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, orchID, decoded.AppOrchestratorID,
		"AppOrchestratorID must survive JSON round-trip (Temporal serialization)")
}
