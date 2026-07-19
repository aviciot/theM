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
	// TaskQueue is the Temporal task queue name for THEM orchestration.
	TaskQueue = "them-orchestration"

	// WorkflowType is the registered workflow type name.
	WorkflowType = "OrchestrationWorkflow"

	// SignalHumanInput is the signal name for HITL human responses.
	SignalHumanInput = "human_input"

	activityStartToClose = 10 * time.Minute
	heartbeatInterval    = 5 * time.Second
)

// WorkflowInput is the input to OrchestrationWorkflow.
type WorkflowInput struct {
	RunID          string
	ContextID      string
	ApplicationID  int64
	EntryPointSlug string
	UserMessage    domain.Message
	// History is pre-loaded by the caller (DB-level LIMIT applied).
	History []domain.Message
	// OrchestratorName identifies which orchestrator config to load.
	OrchestratorName string
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
		TaskQueue:           TaskQueue,
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
