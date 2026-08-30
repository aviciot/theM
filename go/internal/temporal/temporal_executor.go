package temporal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/agentgen"
)

// TemporalExecutor implements agentgen.ExecutionBackend by submitting a
// CanvasAgentWorkflow to Temporal and blocking until it completes.
// Context cancellation sends a best-effort CancelWorkflow to Temporal.
type TemporalExecutor struct {
	client             client.Client
	workflowTimeout    time.Duration
	maxConcurrentTasks int
}

// NewTemporalExecutor creates a TemporalExecutor.
// workflowTimeout is applied as WorkflowExecutionTimeout (default 12 min if zero).
// maxConcurrentTasks caps per-workflow activity concurrency (0 → DefaultMaxConcurrentTasks).
func NewTemporalExecutor(c client.Client, workflowTimeout time.Duration, maxConcurrentTasks int) *TemporalExecutor {
	if workflowTimeout <= 0 {
		workflowTimeout = dagWorkflowTimeout
	}
	return &TemporalExecutor{
		client:             c,
		workflowTimeout:    workflowTimeout,
		maxConcurrentTasks: maxConcurrentTasks,
	}
}

// Execute submits a CanvasAgentWorkflow and blocks until it completes.
// Each call generates a unique workflow ID so parallel invocations on the same
// agent do not collide. When ctx is cancelled the workflow receives a
// CancelWorkflow call on a best-effort basis (5 s timeout).
func (e *TemporalExecutor) Execute(
	ctx context.Context,
	ic *agentgen.InvocationContext,
	plan *agentgen.ExecutionPlan,
	initial agentgen.PipelineVars,
) (*agentgen.ExecutionResult, error) {
	if plan == nil || len(plan.Nodes) == 0 {
		return nil, fmt.Errorf("TemporalExecutor: empty or nil plan")
	}

	workflowID := fmt.Sprintf("canvas:%s:%s", ic.AgentID, uuid.NewString())

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
		MaxConcurrentTasks: e.maxConcurrentTasks,
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
		// If the caller cancelled the context, send a best-effort cancel to Temporal.
		if ctx.Err() != nil {
			go func() {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = e.client.CancelWorkflow(cancelCtx, workflowID, run.GetRunID())
			}()
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
