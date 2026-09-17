// Per-node-kind execution for the larger, self-contained node protocols
// (Router classification, HIL approve/reject with timeout fallback).
//
// These are workflow code called from AppFlowWorkflow's dispatch loop and are
// subject to the same determinism rules as workflow.go: no I/O, no wall clock
// (workflow.NewTimer only), no randomness.
//
// The smaller cases (agent, llm, condition, fork/join, pass-throughs) stay
// inline in the dispatch loop where reading them next to the control flow is
// clearer than chasing a one-line helper.
package appflow

import (
	"encoding/json"
	"fmt"
	"time"

	temporalerr "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// execRouterNode runs the Router activity for node and returns the ID of the
// node its chosen intent label routes to.
//
// Fallback behaviour is deliberate and differs from Condition: when the
// classifier returns a label with no matching edge but the router has exactly
// one outgoing edge, that edge is taken. A router is a best-effort N-way
// classifier, so degrading to the only available path beats failing the run.
// Condition makes no such concession — see execConditionBranch's caller.
func execRouterNode(
	ctx workflow.Context,
	node *AppFlowNode,
	input AppFlowWorkflowInput,
	outEdges []AppFlowEdge,
	accumulated string,
) (string, error) {
	var cfg RouterConfig
	if len(node.Config) > 0 {
		_ = json.Unmarshal(node.Config, &cfg)
	}

	var routerOut RouterActivityOutput
	err := workflow.ExecuteActivity(ctx, AppFlowExecuteRouterActivityName, RouterActivityInput{
		RunID:            input.RunID,
		TenantID:         input.TenantID,
		ApplicationID:    input.ApplicationID,
		NodeID:           node.ID,
		UserMessage:      accumulated,
		Labels:           cfg.OutputLabels,
		ClassifierPrompt: cfg.ClassifierPrompt,
		LLMProviderName:  input.LLMProviderName,
		LLMProvider:      input.LLMProvider,
		LLMModel:         input.LLMModel,
	}).Get(ctx, &routerOut)
	if err != nil {
		return "", fmt.Errorf("router %q: %w", node.ID, err)
	}

	// Find the outgoing edge matching the chosen label.
	nextID := findEdgeByLabel(outEdges, routerOut.ChosenLabel)
	if nextID == "" {
		if len(outEdges) == 1 {
			return outEdges[0].Target, nil
		}
		return "", temporalerr.NewNonRetryableApplicationError(
			fmt.Sprintf("router %q: no outgoing edge matches label %q", node.ID, routerOut.ChosenLabel),
			"RouterNoMatch", nil,
		)
	}
	return nextID, nil
}

// execHILNode persists an approval request, then blocks the workflow until a
// human signals a decision (or the configured timeout applies the fallback).
//
// Returns approved=false when the gate was rejected; the caller must then end
// the run with status "rejected" rather than routing onward. The rejection
// comment is returned so the caller can surface why.
func execHILNode(
	ctx workflow.Context,
	node *AppFlowNode,
	input AppFlowWorkflowInput,
	shortAO workflow.ActivityOptions,
) (approved bool, comment string, err error) {
	var cfg HILConfig
	if len(node.Config) > 0 {
		_ = json.Unmarshal(node.Config, &cfg)
	}
	if cfg.FallbackAction == "" {
		cfg.FallbackAction = "reject"
	}
	if cfg.ApproverRole == "" {
		cfg.ApproverRole = "admin"
	}

	// The HIL activity persists the approval request and returns immediately;
	// the wait happens below on a signal, not inside the activity.
	var hilOut HILActivityOutput
	hilCtx := workflow.WithActivityOptions(ctx, shortAO)
	if aErr := workflow.ExecuteActivity(hilCtx, AppFlowExecuteHILActivityName, HILActivityInput{
		RunID:          input.RunID,
		TenantID:       input.TenantID,
		ApplicationID:  input.ApplicationID,
		NodeID:         node.ID,
		ApproverRole:   cfg.ApproverRole,
		Prompt:         cfg.Prompt,
		TimeoutSecs:    cfg.TimeoutSeconds,
		FallbackAction: cfg.FallbackAction,
	}).Get(hilCtx, &hilOut); aErr != nil {
		return false, "", fmt.Errorf("hil %q: persist: %w", node.ID, aErr)
	}

	// Wait for the approval signal, optionally racing a timeout.
	var approval HILApprovalPayload
	signalName := AppFlowSignalHILApproval + ":" + node.ID

	if cfg.TimeoutSeconds > 0 {
		timer := workflow.NewTimer(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
		sigCh := workflow.GetSignalChannel(ctx, signalName)
		workflow.NewSelector(ctx).
			AddFuture(timer, func(f workflow.Future) {
				// Timer fired — apply the configured fallback.
				switch cfg.FallbackAction {
				case "approve":
					approval.Approved = true
					approval.Comment = "timeout-auto-approved"
				default:
					approval.Approved = false
					approval.Comment = "timeout-rejected"
				}
			}).
			AddReceive(sigCh, func(ch workflow.ReceiveChannel, more bool) {
				ch.Receive(ctx, &approval)
			}).
			Select(ctx)
	} else {
		// Wait indefinitely.
		workflow.GetSignalChannel(ctx, signalName).Receive(ctx, &approval)
	}

	return approval.Approved, approval.Comment, nil
}
