package temporal

import (
	"context"
	"errors"
	"fmt"

	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/agentgen"
)

// ── Canvas DAG activity types ─────────────────────────────────────────────────

// CanvasAgentWorkflowInput is the input to CanvasAgentWorkflow. It must be
// safe to store in Temporal history: no secrets, no credentials.
type CanvasAgentWorkflowInput struct {
	Plan               agentgen.ExecutionPlan `json:"plan"`                 // compiled DAG — no secrets
	Initial            agentgen.PipelineVars  `json:"initial"`              // seed vars: {"input": userText}
	IC                 agentgen.ActivityIC    `json:"ic"`                   // credential-safe 4-ID subset
	MaxConcurrentTasks int                    `json:"max_concurrent_tasks"` // 0 → DefaultMaxConcurrentTasks (10)
}

// CanvasAgentWorkflowOutput is the result returned by CanvasAgentWorkflow.
type CanvasAgentWorkflowOutput struct {
	ResultText string `json:"result_text"`
	ResultMT   string `json:"result_mt"`
}

// StepActivityInput is the per-node activity input. Vars contains only the
// keys declared in Node.Inputs (scoped projection). The full PipelineVars
// accumulator is never passed through Temporal history.
type StepActivityInput struct {
	Node agentgen.PlanNode    `json:"node"`
	Vars agentgen.PipelineVars `json:"vars"` // scoped: only node.Inputs keys
	IC   agentgen.ActivityIC   `json:"ic"`
}

// StepActivityOutput is the per-node activity output. Vars contains only the
// keys declared in Node.Outputs. The workflow merges these into its accumulator.
type StepActivityOutput struct {
	Vars            agentgen.PipelineVars `json:"vars"`             // scoped: only node.Outputs keys
	NextOverride    string                `json:"next_override"`    // branch routing; empty if none
	ResultText      string                `json:"result_text"`      // non-empty for terminal steps
	ResultMT        string                `json:"result_mt"`
	WaitingForHuman bool                  `json:"waiting_for_human"` // true for human_wait nodes
}

// ── ContextLoader interface ───────────────────────────────────────────────────

// ContextLoader reconstructs a full InvocationContext from the credential-safe
// ActivityIC by loading secrets from the DB. Implemented by the dag-worker in
// Phase 4-C; tests inject a fake.
type ContextLoader interface {
	Load(ctx context.Context, ic agentgen.ActivityIC) (*agentgen.InvocationContext, error)
}

// ── CanvasAgentActivities ─────────────────────────────────────────────────────

// CanvasAgentActivities holds dependencies for DAG node activities.
// InterpTemplate is the zero-value Interpreter used as a source for Clone()
// calls — it must never be used directly across concurrent activity invocations.
type CanvasAgentActivities struct {
	InterpTemplate *agentgen.Interpreter
	Loader         ContextLoader
}

const CanvasExecuteStepActivityName = "ExecuteStepActivity"

// ExecuteStepActivity executes one PlanNode in a Temporal activity.
//
// It reconstructs the full InvocationContext from the credential-safe IC via
// ContextLoader.Load, then calls ExecuteNodeForActivity with a fresh clone of
// the shared Interpreter template.
//
// Contract:
//   - input.Vars must contain only the keys declared in input.Node.Inputs.
//   - Output.Vars contains only the keys declared in input.Node.Outputs.
//   - human_wait nodes return immediately with WaitingForHuman=true — the
//     caller (CanvasAgentWorkflow) handles the signal-wait in workflow code.
func (a *CanvasAgentActivities) ExecuteStepActivity(ctx context.Context, input StepActivityInput) (StepActivityOutput, error) {
	if a.InterpTemplate == nil {
		return StepActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"ExecuteStepActivity: InterpTemplate must not be nil",
			"InvalidConfig", nil,
		)
	}
	if a.Loader == nil {
		return StepActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"ExecuteStepActivity: ContextLoader must not be nil",
			"InvalidConfig", nil,
		)
	}
	if err := input.IC.Validate(); err != nil {
		return StepActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
			"ExecuteStepActivity: invalid ActivityIC: "+err.Error(),
			"InvalidInput", nil,
		)
	}

	// human_wait: return immediately; signal-wait is handled in the workflow.
	if input.Node.Type == agentgen.StepHumanWait {
		return StepActivityOutput{WaitingForHuman: true}, nil
	}

	// Load full InvocationContext (credentials) from DB.
	ic, err := a.Loader.Load(ctx, input.IC)
	if err != nil {
		return StepActivityOutput{}, fmt.Errorf("ExecuteStepActivity: load context: %w", err)
	}

	// Execute with an isolated interpreter clone.
	nodeOut, err := agentgen.ExecuteNodeForActivity(ctx, a.InterpTemplate.Clone(), ic, agentgen.NodeExecutionInput{
		Node: input.Node,
		Vars: input.Vars,
	})
	if err != nil {
		// Wrap ErrContractViolation as non-retryable — deterministic failure.
		if isContractViolation(err) {
			return StepActivityOutput{}, temporalerr.NewNonRetryableApplicationError(
				"ContractViolation: "+err.Error(),
				"ContractViolation", err,
			)
		}
		return StepActivityOutput{}, err
	}

	return StepActivityOutput{
		Vars:         nodeOut.Vars,
		NextOverride: nodeOut.NextOverride,
		ResultText:   nodeOut.ResultText,
		ResultMT:     nodeOut.ResultMT,
	}, nil
}

// isContractViolation checks if err wraps *agentgen.ErrContractViolation.
func isContractViolation(err error) bool {
	var cv *agentgen.ErrContractViolation
	return errors.As(err, &cv)
}
