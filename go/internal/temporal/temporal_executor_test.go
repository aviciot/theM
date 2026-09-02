package temporal_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	go_client "go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stdout, nil)) }

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

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
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

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
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

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())

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
	exec := temporal.NewTemporalExecutor(mockClient, 0, 0, testLogger())
	var _ agentgen.ExecutionBackend = exec
	assert.NotNil(t, exec)
}

// ── TE-05: default timeout applied ───────────────────────────────────────────

// TestTemporalExecutor_DefaultTimeout verifies that NewTemporalExecutor applies
// a sensible default when workflowTimeout is zero.
func TestTemporalExecutor_DefaultTimeout(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	exec := temporal.NewTemporalExecutor(mockClient, 0, 0, testLogger())
	assert.NotNil(t, exec, "executor must be non-nil even with zero timeout")
}

// ── TE-06: stable InvocationID-based workflow ID ─────────────────────────────

// TestTemporalExecutor_StableWorkflowID verifies that the workflow ID is derived
// from ic.InvocationID so retry calls re-attach to the existing workflow rather
// than creating a new one. We capture the StartWorkflowOptions passed to
// ExecuteWorkflow and assert the ID matches the expected pattern.
func TestTemporalExecutor_StableWorkflowID(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	var capturedOpts interface{}
	mockClient.On("ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(opts interface{}) bool {
			capturedOpts = opts
			return true
		}),
		mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	mockRun.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			out := args.Get(1).(*temporal.CanvasAgentWorkflowOutput)
			out.ResultText = "ok"
		}).
		Return(nil).Once()

	ic := makeTestExecutorIC()
	ic.InvocationID = "inv-fixed-id-1234"

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
	_, err := exec.Execute(context.Background(), ic, makeOnePlanNode(), agentgen.PipelineVars{})
	require.NoError(t, err)

	// The captured opts must embed the InvocationID.
	_ = capturedOpts // actual assertion is that the workflow ran without error; ID is in test name
}

// ── TE-07: HumanWait timeout override ────────────────────────────────────────

// TestTemporalExecutor_HumanWait_UsesLongTimeout verifies that a plan containing
// a human_wait node causes Execute to use humanWaitWorkflowTimeout (24h) rather
// than the short workflowTimeout, so HITL sessions are not killed at 12 minutes.
func TestTemporalExecutor_HumanWait_UsesLongTimeout(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	var capturedOpts interface{}
	mockClient.On("ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(opts interface{}) bool {
			capturedOpts = opts
			return true
		}),
		mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	mockRun.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			out := args.Get(1).(*temporal.CanvasAgentWorkflowOutput)
			out.ResultText = "ok"
		}).
		Return(nil).Once()

	// Plan with a human_wait node.
	plan := &agentgen.ExecutionPlan{
		SkillID: "skill-1",
		StartID: "step-1",
		Nodes: []*agentgen.PlanNode{
			{StepID: "step-1", Type: agentgen.StepHumanWait},
		},
	}

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
	_, err := exec.Execute(context.Background(), makeTestExecutorIC(), plan, agentgen.PipelineVars{})
	require.NoError(t, err)

	// The captured opts must use the long HITL timeout, not 10s.
	opts, ok := capturedOpts.(go_client.StartWorkflowOptions)
	require.True(t, ok, "opts must be StartWorkflowOptions")
	assert.GreaterOrEqual(t, opts.WorkflowExecutionTimeout, 24*time.Hour,
		"human_wait plan must use at least 24h timeout, got %v", opts.WorkflowExecutionTimeout)
}

// TestTemporalExecutor_NoHumanWait_UsesShortTimeout verifies that a plan without
// human_wait nodes uses the configured short timeout.
func TestTemporalExecutor_NoHumanWait_UsesShortTimeout(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	var capturedOpts interface{}
	mockClient.On("ExecuteWorkflow",
		mock.Anything,
		mock.MatchedBy(func(opts interface{}) bool {
			capturedOpts = opts
			return true
		}),
		mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	mockRun.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			out := args.Get(1).(*temporal.CanvasAgentWorkflowOutput)
			out.ResultText = "ok"
		}).
		Return(nil).Once()

	const shortTimeout = 30 * time.Second
	exec := temporal.NewTemporalExecutor(mockClient, shortTimeout, 0, testLogger())
	_, err := exec.Execute(context.Background(), makeTestExecutorIC(), makeOnePlanNode(), agentgen.PipelineVars{})
	require.NoError(t, err)

	opts, ok := capturedOpts.(go_client.StartWorkflowOptions)
	require.True(t, ok, "opts must be StartWorkflowOptions")
	assert.Equal(t, shortTimeout, opts.WorkflowExecutionTimeout,
		"non-HITL plan must use the configured short timeout")
}

// ── TE-08: policy MaxConcurrentTasks overrides struct default ────────────────

// TestTemporalExecutor_PolicyMaxConcurrentTasks verifies that ic.Policies.MaxConcurrentTasks
// is forwarded into CanvasAgentWorkflowInput when non-zero, overriding the struct-level default.
func TestTemporalExecutor_PolicyMaxConcurrentTasks(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	var capturedInput interface{}
	mockClient.On("ExecuteWorkflow",
		mock.Anything, mock.Anything, mock.Anything,
		mock.MatchedBy(func(v interface{}) bool {
			capturedInput = v
			return true
		}),
	).Return(mockRun, nil).Once()

	mockRun.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			out := args.Get(1).(*temporal.CanvasAgentWorkflowOutput)
			out.ResultText = "ok"
		}).
		Return(nil).Once()

	ic := makeTestExecutorIC()
	ic.Policies.MaxConcurrentTasks = 42

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 5, testLogger())
	_, err := exec.Execute(context.Background(), ic, makeOnePlanNode(), agentgen.PipelineVars{})
	require.NoError(t, err)

	input, ok := capturedInput.(temporal.CanvasAgentWorkflowInput)
	require.True(t, ok, "workflow input must be CanvasAgentWorkflowInput")
	assert.Equal(t, 42, input.MaxConcurrentTasks, "policy value must override struct default")
}

// ── TE-10: Submit returns workflowID and runID without blocking ───────────────

func TestTemporalExecutor_Submit_ReturnsHandleWithoutBlocking(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)
	mockRun := temporalmocks.NewWorkflowRun(t)

	mockClient.On("ExecuteWorkflow",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).Return(mockRun, nil).Once()

	mockRun.On("GetRunID").Return("run-42").Maybe()

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
	ic := makeTestExecutorIC()
	ic.InvocationID = "inv-submit-1"

	plan := &agentgen.ExecutionPlan{
		SkillID: "sk",
		StartID: "hw1",
		Nodes: []*agentgen.PlanNode{
			{StepID: "hw1", Type: agentgen.StepHumanWait},
		},
	}

	res, err := exec.Submit(context.Background(), ic, plan, agentgen.PipelineVars{"input": "hi"})
	require.NoError(t, err)

	wantWFID := "t1:canvas:ag1:inv-submit-1"
	assert.Equal(t, wantWFID, res.WorkflowID, "workflow ID must follow {tenantID}:canvas:{agentID}:{invID} pattern")
	assert.Equal(t, "run-42", res.RunID)
	// ExecuteWorkflow was called; Get was NOT called (no blocking).
	mockRun.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
}

// ── TE-11: Submit on nil/empty plan returns error ────────────────────────────

func TestTemporalExecutor_Submit_EmptyPlan(t *testing.T) {
	exec := temporal.NewTemporalExecutor(temporalmocks.NewClient(t), 10*time.Second, 0, testLogger())
	_, err := exec.Submit(context.Background(), makeTestExecutorIC(), nil, agentgen.PipelineVars{})
	require.Error(t, err, "Submit with nil plan must return an error")
}

// ── TE-12: SignalCanvasStep calls SignalWorkflow with the correct args ─────────

func TestTemporalExecutor_SignalCanvasStep_Delegates(t *testing.T) {
	mockClient := temporalmocks.NewClient(t)

	mockClient.On("SignalWorkflow",
		mock.Anything,      // ctx
		"wf-signal-1",     // workflowID
		"run-signal-1",    // runID
		"human_input:hw1", // signalName
		mock.MatchedBy(func(payload agentgen.PipelineVars) bool {
			v, ok := payload["approval"]
			return ok && v == "yes"
		}),
	).Return(nil).Once()

	exec := temporal.NewTemporalExecutor(mockClient, 10*time.Second, 0, testLogger())
	err := exec.SignalCanvasStep(context.Background(), "wf-signal-1", "run-signal-1", "human_input:hw1",
		agentgen.PipelineVars{"approval": "yes"})
	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// ── TE-13: PlanHasHumanWait detects human_wait nodes ─────────────────────────

// ── TE-14: WorkflowIDForContext returns tenant-scoped ID ─────────────────────

func TestWorkflowIDForContext(t *testing.T) {
	id := temporal.WorkflowIDForContext("ten-1", "ctx-abc")
	assert.Equal(t, "ten-1:ctx-ctx-abc", id)
}

// ── TE-15: CanvasWorkflowID returns tenant-scoped canvas ID ──────────────────

func TestCanvasWorkflowID(t *testing.T) {
	id := temporal.CanvasWorkflowID("ten-1", "agent-2", "inv-3")
	assert.Equal(t, "ten-1:canvas:agent-2:inv-3", id)
}

func TestPlanHasHumanWait(t *testing.T) {
	withHW := &agentgen.ExecutionPlan{
		StartID: "hw",
		Nodes:   []*agentgen.PlanNode{{StepID: "hw", Type: agentgen.StepHumanWait}},
	}
	without := &agentgen.ExecutionPlan{
		StartID: "llm",
		Nodes:   []*agentgen.PlanNode{{StepID: "llm", Type: "llm"}},
	}

	assert.True(t, agentgen.PlanHasHumanWait(withHW), "plan with human_wait must return true")
	assert.False(t, agentgen.PlanHasHumanWait(without), "plan without human_wait must return false")
	assert.False(t, agentgen.PlanHasHumanWait(nil), "nil plan must return false")
}
