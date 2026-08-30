package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/agentgen"
	"go.temporal.io/sdk/client"
)

// humanWaitWorkflowTimeout is the WorkflowExecutionTimeout applied when the
// plan contains at least one human_wait node. Long enough for a human to respond
// in a typical working session. Override via NewTemporalExecutorWithHumanWait.
const humanWaitWorkflowTimeout = 24 * time.Hour

// TemporalExecutor implements agentgen.ExecutionBackend by submitting a
// CanvasAgentWorkflow to Temporal and blocking until it completes.
// Context cancellation sends a bounded synchronous CancelWorkflow to Temporal
// and logs any error via the embedded logger.
type TemporalExecutor struct {
	client              client.Client
	workflowTimeout     time.Duration
	humanWaitTimeout    time.Duration // timeout override when plan has human_wait nodes
	maxConcurrentTasks  int
	logger              *slog.Logger
}

// NewTemporalExecutor creates a TemporalExecutor.
// workflowTimeout is applied as WorkflowExecutionTimeout (default 12 min if zero).
// Plans containing human_wait nodes automatically use humanWaitWorkflowTimeout (24h)
// instead of workflowTimeout.
// maxConcurrentTasks is the struct-level default; ic.Policies.MaxConcurrentTasks
// takes precedence at invocation time (0 → DefaultMaxConcurrentTasks).
// logger must not be nil; pass slog.Default() if no dedicated logger is available.
func NewTemporalExecutor(c client.Client, workflowTimeout time.Duration, maxConcurrentTasks int, logger *slog.Logger) *TemporalExecutor {
	if workflowTimeout <= 0 {
		workflowTimeout = dagWorkflowTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TemporalExecutor{
		client:             c,
		workflowTimeout:    workflowTimeout,
		humanWaitTimeout:   humanWaitWorkflowTimeout,
		maxConcurrentTasks: maxConcurrentTasks,
		logger:             logger,
	}
}

// Execute submits a CanvasAgentWorkflow and blocks until it completes.
// The workflow ID is derived from ic.InvocationID so retries of the same logical
// request re-attach to the existing workflow. A uuid fallback is used when
// InvocationID is empty (defensive only — the request boundary always sets it).
// When ctx is cancelled the workflow receives a synchronous bounded CancelWorkflow;
// errors from that call are logged but do not shadow the original cancellation error.
//
// Timeout selection:
//   - Normal workflows: workflowTimeout (default 12 min)
//   - Plans with any human_wait node: humanWaitTimeout (default 24h)
func (e *TemporalExecutor) Execute(
	ctx context.Context,
	ic *agentgen.InvocationContext,
	plan *agentgen.ExecutionPlan,
	initial agentgen.PipelineVars,
) (*agentgen.ExecutionResult, error) {
	if plan == nil || len(plan.Nodes) == 0 {
		return nil, fmt.Errorf("TemporalExecutor: empty or nil plan")
	}

	// Stable workflow ID: use InvocationID set at the request boundary.
	// A random uuid fallback ensures we never collide even if the field is missing.
	invID := ic.InvocationID
	if invID == "" {
		invID = uuid.NewString()
	}
	workflowID := fmt.Sprintf("canvas:%s:%s", ic.AgentID, invID)

	// Per-invocation concurrency: policy value overrides the struct default.
	maxConcurrent := e.maxConcurrentTasks
	if ic.Policies.MaxConcurrentTasks > 0 {
		maxConcurrent = ic.Policies.MaxConcurrentTasks
	}

	// Choose timeout: long-running for plans with HITL, bounded for all others.
	wfTimeout := e.workflowTimeout
	if agentgen.PlanHasHumanWait(plan) {
		wfTimeout = e.humanWaitTimeout
	}

	// AllowDuplicateFailedOnly: if a prior run with this workflow ID completed
	// successfully, re-attach via GetWorkflow (idempotent); only allow a new
	// run when the prior run failed or was cancelled. This is the correct
	// policy for canvas agents where the stable InvocationID comes from the
	// A2A TaskID — retries of a successful invocation must not re-execute.
	opts := client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                CanvasDAGTaskQueue,
		WorkflowExecutionTimeout: wfTimeout,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
	}

	input := CanvasAgentWorkflowInput{
		Plan:               *plan,
		Initial:            initial,
		IC:                 agentgen.ActivityICFromInvocationContext(ic),
		MaxConcurrentTasks: maxConcurrent,
		HumanWaitTimeout:   int64(wfTimeout),
	}

	run, err := e.client.ExecuteWorkflow(ctx, opts, CanvasAgentWorkflow, input)
	if err != nil {
		// On AlreadyStarted, re-attach to the existing run.
		if temporalerr.IsWorkflowExecutionAlreadyStartedError(err) {
			run = e.client.GetWorkflow(ctx, workflowID, "")
		} else {
			return nil, fmt.Errorf("TemporalExecutor: start workflow: %w", err)
		}
	}

	var out CanvasAgentWorkflowOutput
	if err := run.Get(ctx, &out); err != nil {
		// Caller cancelled: send a synchronous bounded CancelWorkflow.
		if ctx.Err() != nil {
			cancelCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelFn()
			if cerr := e.client.CancelWorkflow(cancelCtx, workflowID, run.GetRunID()); cerr != nil {
				e.logger.Error("TemporalExecutor: CancelWorkflow failed",
					"workflow_id", workflowID,
					"run_id", run.GetRunID(),
					"err", cerr,
				)
			}
		}
		return nil, fmt.Errorf("TemporalExecutor: workflow %s: %w", workflowID, err)
	}

	return &agentgen.ExecutionResult{
		Text:      out.ResultText,
		MediaType: out.ResultMT,
	}, nil
}

// SubmitResult holds the Temporal workflow coordinates returned by Submit.
type SubmitResult struct {
	WorkflowID string
	RunID      string
}

// Submit starts a CanvasAgentWorkflow and returns immediately without waiting for it.
// It is used for HITL plans where the HTTP connection must be released before the
// workflow completes. The returned SubmitResult can be stored so a signal endpoint
// can route human responses back to the correct workflow run.
//
// ctx should be a background context with a generous timeout (e.g. 24h) — NOT the
// HTTP request context. Using the request context would cancel the workflow on
// client disconnect.
func (e *TemporalExecutor) Submit(
	ctx context.Context,
	ic *agentgen.InvocationContext,
	plan *agentgen.ExecutionPlan,
	initial agentgen.PipelineVars,
) (SubmitResult, error) {
	if plan == nil || len(plan.Nodes) == 0 {
		return SubmitResult{}, fmt.Errorf("TemporalExecutor: empty or nil plan")
	}

	invID := ic.InvocationID
	if invID == "" {
		invID = uuid.NewString()
	}
	workflowID := fmt.Sprintf("canvas:%s:%s", ic.AgentID, invID)

	maxConcurrent := e.maxConcurrentTasks
	if ic.Policies.MaxConcurrentTasks > 0 {
		maxConcurrent = ic.Policies.MaxConcurrentTasks
	}

	wfTimeout := e.workflowTimeout
	if agentgen.PlanHasHumanWait(plan) {
		wfTimeout = e.humanWaitTimeout
	}

	opts := client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                CanvasDAGTaskQueue,
		WorkflowExecutionTimeout: wfTimeout,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
	}

	input := CanvasAgentWorkflowInput{
		Plan:               *plan,
		Initial:            initial,
		IC:                 agentgen.ActivityICFromInvocationContext(ic),
		MaxConcurrentTasks: maxConcurrent,
		HumanWaitTimeout:   int64(wfTimeout),
	}

	run, err := e.client.ExecuteWorkflow(ctx, opts, CanvasAgentWorkflow, input)
	if err != nil {
		if temporalerr.IsWorkflowExecutionAlreadyStartedError(err) {
			run = e.client.GetWorkflow(ctx, workflowID, "")
		} else {
			return SubmitResult{}, fmt.Errorf("TemporalExecutor: submit workflow: %w", err)
		}
	}

	return SubmitResult{WorkflowID: workflowID, RunID: run.GetRunID()}, nil
}

// SignalCanvasStep delivers a human_input signal to a specific step in a
// CanvasAgentWorkflow. signalName must be SignalHumanInputPrefix+stepID.
// payload is merged into the workflow's pipeline vars at the waiting node.
func (e *TemporalExecutor) SignalCanvasStep(ctx context.Context, workflowID, runID, signalName string, payload agentgen.PipelineVars) error {
	if err := e.client.SignalWorkflow(ctx, workflowID, runID, signalName, payload); err != nil {
		return fmt.Errorf("TemporalExecutor: signal canvas step: %w", err)
	}
	return nil
}

// AwaitResult blocks until the CanvasAgentWorkflow identified by workflowID/runID
// completes and returns its output. Use after a HITL workflow has been signalled
// to collect the final result without re-starting the workflow.
func (e *TemporalExecutor) AwaitResult(ctx context.Context, workflowID, runID string) (*agentgen.ExecutionResult, error) {
	run := e.client.GetWorkflow(ctx, workflowID, runID)
	var out CanvasAgentWorkflowOutput
	if err := run.Get(ctx, &out); err != nil {
		return nil, fmt.Errorf("TemporalExecutor: await result: %w", err)
	}
	return &agentgen.ExecutionResult{Text: out.ResultText, MediaType: out.ResultMT}, nil
}

// CancelWorkflow requests cancellation of the running workflow.
func (e *TemporalExecutor) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
	if err := e.client.CancelWorkflow(ctx, workflowID, runID); err != nil {
		return fmt.Errorf("TemporalExecutor: cancel workflow: %w", err)
	}
	return nil
}

// QueryHITLStatus queries the hitl_status query handler on a running workflow.
// Returns ErrHITLWorkflowNotRunning when the workflow is not in a queryable state.
func (e *TemporalExecutor) QueryHITLStatus(ctx context.Context, workflowID, runID string) (HITLQueryStatus, error) {
	resp, err := e.client.QueryWorkflow(ctx, workflowID, runID, "hitl_status")
	if err != nil {
		return HITLQueryStatus{}, fmt.Errorf("TemporalExecutor: query hitl_status: %w", err)
	}
	var status HITLQueryStatus
	if err := resp.Get(&status); err != nil {
		return HITLQueryStatus{}, fmt.Errorf("TemporalExecutor: decode hitl_status: %w", err)
	}
	return status, nil
}

// CanvasSignaler can deliver a human_input signal to a running CanvasAgentWorkflow.
// Implemented by TemporalExecutor; nil when Temporal is disabled.
type CanvasSignaler interface {
	SignalCanvasStep(ctx context.Context, workflowID, runID, signalName string, payload agentgen.PipelineVars) error
}

// CanvasSubmitter can start a CanvasAgentWorkflow without blocking.
// Implemented by TemporalExecutor; nil when Temporal is disabled.
type CanvasSubmitter interface {
	Submit(ctx context.Context, ic *agentgen.InvocationContext, plan *agentgen.ExecutionPlan, initial agentgen.PipelineVars) (SubmitResult, error)
}

// CanvasAwaiter can block until a running CanvasAgentWorkflow completes.
// Implemented by TemporalExecutor; nil when Temporal is disabled.
type CanvasAwaiter interface {
	AwaitResult(ctx context.Context, workflowID, runID string) (*agentgen.ExecutionResult, error)
}

// CanvasCanceler can request cancellation of a running CanvasAgentWorkflow.
// Implemented by TemporalExecutor; nil when Temporal is disabled.
type CanvasCanceler interface {
	CancelWorkflow(ctx context.Context, workflowID, runID string) error
}

// CanvasHITLQuerier can query the hitl_status handler on a running CanvasAgentWorkflow.
// Implemented by TemporalExecutor; nil when Temporal is disabled.
type CanvasHITLQuerier interface {
	QueryHITLStatus(ctx context.Context, workflowID, runID string) (HITLQueryStatus, error)
}

// compile-time interface satisfaction checks.
var _ agentgen.ExecutionBackend = (*TemporalExecutor)(nil)
var _ CanvasSignaler = (*TemporalExecutor)(nil)
var _ CanvasSubmitter = (*TemporalExecutor)(nil)
var _ CanvasAwaiter = (*TemporalExecutor)(nil)
var _ CanvasCanceler = (*TemporalExecutor)(nil)
var _ CanvasHITLQuerier = (*TemporalExecutor)(nil)
