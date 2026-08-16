// Package temporal implements the durable Temporal workflow that wraps the
// orchestration loop. It enables HITL (human-in-the-loop) by pausing the
// workflow when the orchestrator returns TaskInputRequired and resuming on a
// Signal.
package temporal

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/aviciot/them/internal/domain"
)

const (
	// TaskQueue is the Temporal task queue name for the legacy Python worker.
	// The Python Temporal worker polls this queue for PythonOrchestrationInput workflows.
	// Kept for backward-compatibility; Go Worker uses GoTaskQueue instead.
	TaskQueue = "them-orchestration"

	// GoTaskQueue is the Temporal task queue name for the Go worker (R-2C).
	// The Go worker polls this queue exclusively; the Bridge sends OrchestrationWorkflow
	// executions here. Python worker continues to poll TaskQueue independently.
	GoTaskQueue = "them-orchestration-go"

	// WorkflowType is the registered workflow type name.
	WorkflowType = "OrchestrationWorkflow"

	// SignalHumanInput is the signal name for HITL human responses.
	// Must match the Python workflow's signal name exactly.
	SignalHumanInput = "submit_human_response"

	activityStartToClose = 10 * time.Minute
	heartbeatInterval    = 5 * time.Second
)

// WorkflowInput is the input to OrchestrationWorkflow.
type WorkflowInput struct {
	RunID     string
	ContextID string
	// TenantID is the UUID of the owning tenant (R-4d). Never sourced from
	// client request data — always from epconfig.EPConfig.TenantID.
	TenantID string
	// ApplicationID is the UUID string of the owning application (R-4d).
	// Type is string UUID to match the PostgreSQL column and EPConfig.AppID.
	ApplicationID    string
	EntryPointSlug   string
	UserMessage      domain.Message
	// History is pre-loaded by the caller (DB-level LIMIT applied).
	History []domain.Message
	// OrchestratorName identifies which orchestrator config to load.
	// Resolved from entry_points.app_orchestrator_id → app_orchestrators.name (SEC-04).
	// Never set to an EP slug — always the actual orchestrator name from the DB row.
	OrchestratorName string

	// AppOrchestratorID is the UUID of the bound app_orchestrators row (SEC-04).
	// The future Go Temporal worker MUST use this UUID for orchestrator resolution
	// rather than performing a global name lookup. This field is the authoritative
	// identity; OrchestratorName is kept for current Python worker compatibility only.
	// LEGACY NOTE: Python worker (permanently retired) ignored this field and
	// resolved orchestrators by name only — that global lookup path is dead.
	AppOrchestratorID string
}

// WorkflowResult is returned by OrchestrationWorkflow on completion.
type WorkflowResult struct {
	FinalText string
	Status    domain.RunStatus
}

// ErrTaskInputRequired signals the workflow must pause for human input.
type ErrTaskInputRequired struct {
	Prompt string
}

func (e *ErrTaskInputRequired) Error() string { return "input_required: " + e.Prompt }

// OrchestrationWorkflow is the durable Temporal workflow.
//
//  1. Execute RunOrchestratorActivity
//  2. If activity returns ErrTaskInputRequired:
//     - Update run status to "input_required"
//     - GetSignalChannel(SignalHumanInput).Receive — wait for human response
//     - Re-execute activity with the response appended
//  3. On completion, return WorkflowResult
func OrchestrationWorkflow(ctx workflow.Context, input WorkflowInput) (WorkflowResult, error) {
	ao := workflow.ActivityOptions{
		TaskQueue:           GoTaskQueue,
		StartToCloseTimeout: activityStartToClose,
		HeartbeatTimeout:    heartbeatInterval * 3,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1, // orchestrator handles retries internally
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for {
		var result WorkflowResult
		err := workflow.ExecuteActivity(ctx, RunOrchestratorActivityName, input).Get(ctx, &result)
		if err == nil {
			return result, nil
		}

		// Check for HITL pause.
		var appErr *temporal.ApplicationError
		if !isTemporalAppErr(err, &appErr) || appErr.Type() != "TaskInputRequired" {
			return WorkflowResult{Status: domain.RunStatusFailed}, err
		}

		// Signal channel — block until human response arrives.
		var humanResponse domain.Message
		workflow.GetSignalChannel(ctx, SignalHumanInput).Receive(ctx, &humanResponse)

		// Append human response to the history and re-run.
		input.History = append(input.History, input.UserMessage, humanResponse)
		input.UserMessage = humanResponse
	}
}

// isTemporalAppErr unwraps a Temporal ApplicationError if present.
func isTemporalAppErr(err error, out **temporal.ApplicationError) bool {
	var ae *temporal.ApplicationError
	if errors.As(err, &ae) {
		*out = ae
		return true
	}
	return false
}

// RunOrchestratorActivityName is the registered name for the activity.
const RunOrchestratorActivityName = "RunOrchestratorActivity"
