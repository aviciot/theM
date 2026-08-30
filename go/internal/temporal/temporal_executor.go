package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/agentgen"
	"go.temporal.io/sdk/client"
)

// TemporalExecutor implements agentgen.ExecutionBackend by submitting a
// CanvasAgentWorkflow to Temporal and blocking until it completes.
// Context cancellation sends a bounded synchronous CancelWorkflow to Temporal
// and logs any error via the embedded logger.
type TemporalExecutor struct {
	client             client.Client
	workflowTimeout    time.Duration
	maxConcurrentTasks int
	logger             *slog.Logger
}

// NewTemporalExecutor creates a TemporalExecutor.
// workflowTimeout is applied as WorkflowExecutionTimeout (default 12 min if zero).
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

	// WorkflowIDReusePolicy defaults to AllowDuplicate — no need to set it explicitly.
	opts := client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                CanvasDAGTaskQueue,
		WorkflowExecutionTimeout: e.workflowTimeout,
	}

	input := CanvasAgentWorkflowInput{
		Plan:               *plan,
		Initial:            initial,
		IC:                 agentgen.ActivityICFromInvocationContext(ic),
		MaxConcurrentTasks: maxConcurrent,
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

// compile-time interface satisfaction check.
var _ agentgen.ExecutionBackend = (*TemporalExecutor)(nil)
