package agentgen

import (
	"context"
	"fmt"
)

// NodeExecutionInput is the scoped input to ExecuteNodeForActivity.
// Vars contains only the keys declared in Node.Inputs (scoped projection).
// The caller is responsible for projecting the global PipelineVars down
// to the declared inputs before calling this function.
type NodeExecutionInput struct {
	Node PlanNode     // one PlanNode from the ExecutionPlan
	Vars PipelineVars // scoped: only keys declared in Node.Inputs
}

// NodeExecutionOutput is the result of executing one node.
// Vars contains only the keys declared in Node.Outputs (scoped projection).
// The caller merges these into the global PipelineVars accumulator.
type NodeExecutionOutput struct {
	Vars         PipelineVars // scoped: only keys declared in Node.Outputs; nil when empty
	NextOverride string       // non-empty when the node sets branch routing (e.g. StepBranch)
	ResultText   string       // non-empty when the node is a terminal step (StepResponse/StepStreamOut)
	ResultMT     string       // media type for ResultText; "text/plain" when unset
}

// ExecuteNodeForActivity executes one PlanNode using an isolated Interpreter clone.
//
// The caller MUST pass interp.clone() — not the shared template Interpreter — so
// that nextStepOverride and any other mutable Interpreter state is isolated per call.
// This matches the guarantee LocalExecutor provides: each branch goroutine holds its
// own clone.
//
// This function has no Temporal SDK dependency. It is the single point of dispatch
// from CanvasAgentActivities into the agentgen execution engine.
//
// Policy enforcement:
//   - Idempotency guard: when Node.Policy.RequiresIdempotencyKey=true and
//     MaxAttempts>1, the HTTP step config MUST contain a static Idempotency-Key
//     header; if absent → ErrIdempotencyKeyMissing. (Retries are managed by the
//     Temporal RetryPolicy set in activityOptionsForNode; this guard prevents
//     the activity from being registered at all without the safety header.)
//
// Scoping contract:
//   - input.Vars contains only the keys declared in input.Node.Inputs.
//     Nodes with empty Inputs receive the full projected map (backward-compat).
//   - Output.Vars contains only the keys declared in input.Node.Outputs.
//     Keys written by the node but not declared in Outputs are dropped.
//   - When input.Node.Inputs is empty (uncompiled spec), input.Vars is passed
//     through unchanged (same as LocalExecutor / interpreter.executeStep behaviour).
func ExecuteNodeForActivity(
	ctx context.Context,
	interp *Interpreter, // must be a fresh clone — never the shared template
	ic *InvocationContext,
	input NodeExecutionInput,
) (NodeExecutionOutput, error) {
	if interp == nil {
		return NodeExecutionOutput{}, fmt.Errorf("ExecuteNodeForActivity: interp must not be nil")
	}

	step := planNodeToStepSpec(&input.Node)
	p := input.Node.Policy

	// Idempotency guard: mirror the same check LocalExecutor performs so Temporal
	// activities are also protected against double-spend on mutating HTTP retries.
	maxAttempts := p.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if p.RequiresIdempotencyKey && maxAttempts > 1 {
		if step.Type == StepHTTP {
			if !httpConfigHasIdempotencyKey(step.Config) {
				return NodeExecutionOutput{}, &ErrIdempotencyKeyMissing{StepID: step.ID}
			}
		}
	}

	localResult := &ExecutionResult{MediaType: "text/plain"}

	interp.nextStepOverride = ""
	if err := interp.executeStep(ctx, ic, step, input.Vars, localResult); err != nil {
		return NodeExecutionOutput{}, err
	}
	nextOverride := interp.nextStepOverride
	interp.nextStepOverride = ""

	// Collect output vars: project global vars down to declared Outputs.
	// When Outputs is empty (uncompiled spec or no-output node), return nil vars.
	var outVars PipelineVars
	if len(input.Node.Outputs) > 0 {
		outVars = make(PipelineVars, len(input.Node.Outputs))
		for _, ref := range input.Node.Outputs {
			if v, ok := input.Vars[ref.Name]; ok {
				outVars[ref.Name] = v
			}
		}
	}

	// Capture result from terminal nodes (StepResponse, StepStreamOut).
	resultText := ""
	resultMT := ""
	if localResult.Text != "" || step.Type == StepResponse || step.Type == StepStreamOut {
		resultText = localResult.Text
		resultMT = localResult.MediaType
		if resultMT == "" {
			resultMT = "text/plain"
		}
	}

	return NodeExecutionOutput{
		Vars:         outVars,
		NextOverride: nextOverride,
		ResultText:   resultText,
		ResultMT:     resultMT,
	}, nil
}

// ActivityIC is the credential-safe subset of InvocationContext that may be
// serialized into Temporal workflow and activity history.
//
// Secrets (AppAPIKey, AgentParams, AppGlobalParams, NodeLLMOverrides) are NEVER
// included. The activity reconstructs the full InvocationContext by loading
// credentials from DB using these four IDs.
//
// All four IDs are required for the DB query so that binding lookup is scoped
// by tenant, application, agent, and binding — preventing cross-tenant leakage
// even in the presence of UUID collisions across tenants.
type ActivityIC struct {
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	AgentID       string `json:"agent_id"`
	BindingID     string `json:"binding_id"`
}

// Validate returns a non-nil error when any required field is missing.
func (ic ActivityIC) Validate() error {
	if ic.TenantID == "" {
		return fmt.Errorf("ActivityIC: TenantID is required")
	}
	if ic.ApplicationID == "" {
		return fmt.Errorf("ActivityIC: ApplicationID is required")
	}
	if ic.AgentID == "" {
		return fmt.Errorf("ActivityIC: AgentID is required")
	}
	// BindingID may be empty for anonymous/debug invocations; allow it.
	return nil
}

// ActivityICFromInvocationContext extracts the credential-safe fields from ic.
// The returned ActivityIC is safe to serialize into Temporal history.
func ActivityICFromInvocationContext(ic *InvocationContext) ActivityIC {
	return ActivityIC{
		TenantID:      ic.TenantID,
		ApplicationID: ic.ApplicationID,
		AgentID:       ic.AgentID,
		BindingID:     ic.BindingID,
	}
}
