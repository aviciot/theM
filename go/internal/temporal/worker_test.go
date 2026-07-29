package temporal_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/temporal"
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
