package appflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// cycleHILRejectedError is returned by execCycleNode when a HIL node inside the
// cycle body was rejected and has no body-level "rejected" edge to follow.
// The outer workflow checks for this type to follow the cycle node's own outer
// "rejected" edge rather than failing the run.
type cycleHILRejectedError struct{ comment string }

func (e cycleHILRejectedError) Error() string {
	return "hil rejected: " + e.comment
}

// evalBreakCondition reports whether the loop should exit given the current vars.
// op: "eq" (default when blank), "neq", "truthy"
func evalBreakCondition(vars FlowVars, varName, op, wantVal string) bool {
	if varName == "" {
		return false
	}
	got := vars[varName]
	switch strings.ToLower(op) {
	case "neq":
		return got != wantVal
	case "truthy":
		return isTruthy(got)
	default: // "eq" or empty
		return got == wantVal
	}
}

// execCycleNode runs the Cycle body repeatedly until the exit condition is met
// or max iterations are exhausted. Returns updated accumulated text and vars.
func execCycleNode(
	ctx workflow.Context,
	node *AppFlowNode,
	input AppFlowWorkflowInput,
	accumulated string,
	vars FlowVars,
	ao, shortAO workflow.ActivityOptions,
	tick *stepTick,
	seedGen int,
) (string, FlowVars, error) {
	var cfg CycleConfig
	if len(node.Config) > 0 {
		_ = json.Unmarshal(node.Config, &cfg)
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	bodyNodeByID := make(map[string]*AppFlowNode, len(cfg.BodyNodes))
	for i := range cfg.BodyNodes {
		n := &cfg.BodyNodes[i]
		bodyNodeByID[n.ID] = n
	}
	bodyEdgesBySource := make(map[string][]AppFlowEdge)
	for _, e := range cfg.BodyEdges {
		bodyEdgesBySource[e.Source] = append(bodyEdgesBySource[e.Source], e)
	}

	traceNode(ctx, input.RunID, node.ID, "cycle", "node_start",
		fmt.Sprintf("max=%d break_when=%s %s %s", maxIter, cfg.BreakWhenVar, cfg.BreakWhenOp, cfg.BreakWhenVal),
		input.LogVerbosity)

	for i := 0; i < maxIter; i++ {
		var bodyErr error
		accumulated, vars, bodyErr = walkCycleBody(ctx, cfg.EntryNodeID, node.ID, bodyNodeByID, bodyEdgesBySource, input, accumulated, vars, ao, shortAO, tick, seedGen)
		if bodyErr != nil {
			// Propagate HIL-rejected sentinel as-is so the outer workflow can
			// follow the cycle node's own "rejected" edge.
			if _, isRej := bodyErr.(cycleHILRejectedError); isRej {
				return accumulated, vars, bodyErr
			}
			return accumulated, vars, fmt.Errorf("cycle %q iteration %d: %w", node.ID, i+1, bodyErr)
		}
		if evalBreakCondition(vars, cfg.BreakWhenVar, cfg.BreakWhenOp, cfg.BreakWhenVal) {
			traceNode(ctx, input.RunID, node.ID, "cycle", "node_done",
				fmt.Sprintf("broke after %d iteration(s)", i+1), input.LogVerbosity)
			return accumulated, vars, nil
		}
	}
	traceNode(ctx, input.RunID, node.ID, "cycle", "node_done",
		fmt.Sprintf("max iterations (%d) reached", maxIter), input.LogVerbosity)
	return accumulated, vars, nil
}

// walkCycleBody executes the cycle body sub-graph from entryID, mirroring
// walkBranch's dispatch pattern. Returns updated accumulated text and vars.
// cycleID is the containing cycle node's ID — an edge targeting it via OUT ▶
// is treated as an explicit body exit (same as no outgoing edge).
func walkCycleBody(
	ctx workflow.Context,
	entryID string,
	cycleID string,
	nodeByID map[string]*AppFlowNode,
	outEdges map[string][]AppFlowEdge,
	input AppFlowWorkflowInput,
	accumulated string,
	vars FlowVars,
	ao, shortAO workflow.ActivityOptions,
	tick *stepTick,
	seedGen int,
) (string, FlowVars, error) {
	lastSeenGen := seedGen
	exitBody := func(next string) string {
		if next == cycleID {
			return ""
		}
		return next
	}
	curID := entryID
	for curID != "" {
		node, ok := nodeByID[curID]
		if !ok {
			break
		}
		stepGate(ctx, tick, &lastSeenGen, input, input.RunID, node.ID, node.Kind)

		switch node.Kind {
		case "agent":
			var agentOut AgentInvokeActivityOutput
			agentCtx := workflow.WithActivityOptions(ctx, ao)
			err := workflow.ExecuteActivity(agentCtx, AppFlowInvokeAgentActivityName, AgentInvokeActivityInput{
				RunID:         input.RunID,
				TenantID:      input.TenantID,
				ApplicationID: input.ApplicationID,
				NodeID:        node.ID,
				AgentID:       node.AgentID,
				UserMessage:   accumulated,
				Verbosity:     input.LogVerbosity,
			}).Get(agentCtx, &agentOut)
			if err != nil {
				return accumulated, vars, fmt.Errorf("cycle agent %q: %w", node.ID, err)
			}
			if agentOut.ResponseText != "" {
				accumulated = agentOut.ResponseText
			}
			curID = exitBody(firstEdgeTarget(outEdges[node.ID]))

		case "llm":
			var cfg InlineLLMConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			provider, model := cfg.Provider, cfg.Model
			if provider == "" {
				provider = input.LLMProviderName
			}
			if model == "" {
				model = input.LLMModel
			}
			vars["input"] = accumulated
			var llmOut InlineLLMActivityOutput
			llmCtx := workflow.WithActivityOptions(ctx, ao)
			err := workflow.ExecuteActivity(llmCtx, AppFlowInlineLLMActivityName, InlineLLMActivityInput{
				RunID:         input.RunID,
				TenantID:      input.TenantID,
				ApplicationID: input.ApplicationID,
				NodeID:        node.ID,
				SystemPrompt:  cfg.SystemPrompt,
				UserPrompt:    cfg.UserPrompt,
				Vars:          vars,
				Provider:      provider,
				Model:         model,
				MaxTokens:     cfg.MaxTokens,
				Temperature:   cfg.Temperature,
				OutputVar:     cfg.OutputVar,
				Stream:        true,
				Verbosity:     input.LogVerbosity,
				Debug:         input.Debug,
			}).Get(llmCtx, &llmOut)
			if err != nil {
				return accumulated, vars, fmt.Errorf("cycle llm %q: %w", node.ID, err)
			}
			outVar := llmOut.OutputVar
			if outVar == "" {
				outVar = "output"
			}
			vars[outVar] = llmOut.ResponseText
			if llmOut.ResponseText != "" {
				accumulated = llmOut.ResponseText
			}
			curID = exitBody(firstEdgeTarget(outEdges[node.ID]))

		case "condition":
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_start", "", input.LogVerbosity)
			var cfg InlineConditionConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &cfg)
			}
			vars["input"] = accumulated
			rendered, rErr := renderFlowTemplate(cfg.Expression, vars)
			if rErr != nil {
				traceNode(ctx, input.RunID, node.ID, node.Kind, "node_error", rErr.Error(), input.LogVerbosity)
				return accumulated, vars, fmt.Errorf("cycle condition %q: %w", node.ID, rErr)
			}
			branch := "false"
			if isTruthy(rendered) {
				branch = "true"
			}
			nextID := findEdgeByLabel(outEdges[node.ID], branch)
			if nextID == "" {
				return accumulated, vars, fmt.Errorf("cycle condition %q: no edge labelled %q", node.ID, branch)
			}
			traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done", "branch="+branch, input.LogVerbosity)
			curID = exitBody(nextID)

		case "wait_for_input":
			var waitErr error
			accumulated, vars, waitErr = execWaitForInputNode(ctx, node, input, accumulated, vars, shortAO)
			if waitErr != nil {
				return accumulated, vars, fmt.Errorf("cycle body wait_for_input %q: %w", node.ID, waitErr)
			}
			curID = exitBody(firstEdgeTarget(outEdges[node.ID]))

		case "hil":
			approved, comment, hErr := execHILNode(ctx, node, input, shortAO, vars)
			if hErr != nil {
				return accumulated, vars, fmt.Errorf("cycle body hil %q: %w", node.ID, hErr)
			}
			vars["hil_comment"] = comment
			vars[node.ID+"_comment"] = comment
			decision := "rejected"
			if approved {
				decision = "approved"
			}
			vars["hil_decision"] = decision
			vars[node.ID+"_decision"] = decision
			if !approved {
				// Follow "rejected" edge if wired, otherwise exit cycle early.
				if rejID := findEdgeByLabel(outEdges[node.ID], "rejected"); rejID != "" {
					curID = rejID
				} else {
					// No rejection branch inside the cycle body — bubble up so the
					// outer workflow can follow the cycle node's own "rejected" edge.
					return accumulated, vars, cycleHILRejectedError{comment: comment}
				}
			} else {
				curID = exitBody(firstEdgeTarget(outEdges[node.ID]))
			}

		case "transform":
			var transformErr error
			vars, transformErr = execTransformNode(ctx, node, input, vars, shortAO)
			if transformErr != nil {
				return accumulated, vars, fmt.Errorf("cycle body transform %q: %w", node.ID, transformErr)
			}
			curID = exitBody(firstEdgeTarget(outEdges[node.ID]))

		case "orchestrator", "middleware":
			curID = exitBody(firstEdgeTarget(outEdges[node.ID]))

		default:
			return accumulated, vars, fmt.Errorf("cycle body: unsupported node kind %q at %q", node.Kind, node.ID)
		}
	}
	return accumulated, vars, nil
}
