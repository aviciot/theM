// Graph traversal helpers shared by AppFlowWorkflow and its fork branches.
//
// These run as workflow code (walkBranch executes activities), so the same
// determinism rules as workflow.go apply: no I/O, no clocks, no randomness.
package appflow

import (
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// findJoinNode walks the outgoing branches of a fork to locate the first join node
// reachable from any branch. All branches in a well-formed canvas converge on the same join.
func findJoinNode(branches []AppFlowEdge, nodeByID map[string]*AppFlowNode, outEdges map[string][]AppFlowEdge) string {
	for _, branch := range branches {
		cur := branch.Target
		visited := make(map[string]bool)
		for cur != "" && !visited[cur] {
			visited[cur] = true
			n, ok := nodeByID[cur]
			if !ok {
				break
			}
			if n.Kind == "join" {
				return cur
			}
			cur = firstEdgeTarget(outEdges[cur])
		}
	}
	return ""
}

// walkBranch executes nodes along a single fork branch starting at startID,
// stopping when it reaches stopID (the join node) or a dead end.
// Returns the final accumulated text for this branch.
func walkBranch(
	ctx workflow.Context,
	startID, stopID string,
	nodeByID map[string]*AppFlowNode,
	outEdges map[string][]AppFlowEdge,
	input AppFlowWorkflowInput,
	initialMsg string,
	ao, shortAO workflow.ActivityOptions,
) (string, error) {
	accumulated := initialMsg
	curID := startID
	for curID != "" && curID != stopID {
		node, ok := nodeByID[curID]
		if !ok {
			return accumulated, fmt.Errorf("branch: node %q not found", curID)
		}
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
			}).Get(agentCtx, &agentOut)
			if err != nil {
				return accumulated, fmt.Errorf("branch agent %q: %w", node.ID, err)
			}
			if agentOut.ResponseText != "" {
				accumulated = agentOut.ResponseText
			}
			curID = firstEdgeTarget(outEdges[node.ID])
		case "orchestrator", "middleware":
			curID = firstEdgeTarget(outEdges[node.ID])
		case "join":
			// Reached join — stop this branch.
			return accumulated, nil
		default:
			return accumulated, fmt.Errorf("branch: unsupported node kind %q at %q", node.Kind, node.ID)
		}
	}
	return accumulated, nil
}

// mergeBranchResults concatenates non-empty branch results with a newline separator.
func mergeBranchResults(results []string) string {
	var parts []string
	for _, r := range results {
		if r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, "\n")
}

// findEdgeByLabel returns the Target of the first edge whose Label matches label
// (case-insensitive). Returns "" if no match.
func findEdgeByLabel(edges []AppFlowEdge, label string) string {
	for _, e := range edges {
		if strings.EqualFold(e.Label, label) {
			return e.Target
		}
	}
	return ""
}

// firstEdgeTarget returns the Target of the first edge, or "" if there are none.
func firstEdgeTarget(edges []AppFlowEdge) string {
	if len(edges) == 0 {
		return ""
	}
	return edges[0].Target
}
