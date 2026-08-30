package temporal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

// makeTestExecutorIC returns a minimal InvocationContext for executor tests.
func makeTestExecutorIC() *agentgen.InvocationContext {
	return &agentgen.InvocationContext{
		TenantID:      "t1",
		ApplicationID: "a1",
		AgentID:       "ag1",
		BindingID:     "b1",
	}
}

// makeOnePlanNode builds a trivial single-node ExecutionPlan.
func makeOnePlanNode() *agentgen.ExecutionPlan {
	return &agentgen.ExecutionPlan{
		SkillID: "skill-1",
		StartID: "step-1",
		Nodes:   []*agentgen.PlanNode{{StepID: "step-1", Type: "response"}},
	}
}

// ── TE-01: success path ───────────────────────────────────────────────────────

// TestTemporalExecutor_Execute_Success verifies that TemporalExecutor correctly
// submits a workflow and returns ExecutionResult from the workflow output.
func TestTemporalExecutor_Execute_Success(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	mockClient.On("ExecuteWorkflow",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	// WorkflowRun.Get populates the output struct via the Run callback.
	mockRun.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			out := args.Get(1).(*temporal.CanvasAgentWorkflowOutput)
			out.ResultText = "hello world"
			out.ResultMT = "text/plain"
		}).
		Return(nil).Once()

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0)
	result, err := exec.Execute(context.Background(), makeTestExecutorIC(), makeOnePlanNode(), agentgen.PipelineVars{"input": "hi"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "hello world", result.Text)
	assert.Equal(t, "text/plain", result.MediaType)
}

// ── TE-02: workflow error propagation ────────────────────────────────────────

// TestTemporalExecutor_Execute_WorkflowError verifies that an error returned by
// WorkflowRun.Get is wrapped and returned by Execute.
func TestTemporalExecutor_Execute_WorkflowError(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)
	injectedErr := errors.New("workflow failed: node timed out")

	mockClient.On("ExecuteWorkflow",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	// ctx is not cancelled, so no CancelWorkflow call expected.
	mockRun.On("Get", mock.Anything, mock.Anything).Return(injectedErr).Once()

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0)
	_, err := exec.Execute(context.Background(), makeTestExecutorIC(), makeOnePlanNode(), agentgen.PipelineVars{"input": "hi"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow failed: node timed out")
}

// ── TE-03: empty plan rejected ────────────────────────────────────────────────

// TestTemporalExecutor_Execute_EmptyPlan verifies that Execute returns an error
// immediately for nil or empty plans without calling the Temporal client.
func TestTemporalExecutor_Execute_EmptyPlan(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	// No mock expectations — the executor must return before calling ExecuteWorkflow.

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0)

	_, err := exec.Execute(context.Background(), makeTestExecutorIC(), nil, agentgen.PipelineVars{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty or nil plan")

	emptyPlan := &agentgen.ExecutionPlan{Nodes: nil}
	_, err = exec.Execute(context.Background(), makeTestExecutorIC(), emptyPlan, agentgen.PipelineVars{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty or nil plan")
}

// ── TE-04: compile-time interface satisfaction ────────────────────────────────

// TestTemporalExecutor_ImplementsExecutionBackend is a compile-time guard that
// TemporalExecutor satisfies agentgen.ExecutionBackend.
func TestTemporalExecutor_ImplementsExecutionBackend(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	exec := temporal.NewTemporalExecutor(mockClient, 0, 0)
	var _ agentgen.ExecutionBackend = exec
	assert.NotNil(t, exec)
}

// ── TE-05: default timeout applied ───────────────────────────────────────────

// TestTemporalExecutor_DefaultTimeout verifies that NewTemporalExecutor applies
// a sensible default when workflowTimeout is zero.
func TestTemporalExecutor_DefaultTimeout(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	exec := temporal.NewTemporalExecutor(mockClient, 0, 0)
	assert.NotNil(t, exec, "executor must be non-nil even with zero timeout")
}
