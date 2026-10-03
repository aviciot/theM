// Wait-for-Input node: pauses an AppFlowWorkflow until the user sends another
// WS/SSE message on the same run, then stores the reply in a named flow var.
//
// Mechanism:
//  1. PendingWaitSetActivity writes them:wait:{runID} = nodeID to Redis (TTL 4h).
//     This tells the WS/SSE handler "next message on this run should signal the
//     workflow, not start a new one."
//  2. execWaitForInputNode blocks on workflow.GetSignalChannel for
//     AppFlowSignalUserInput+":"+nodeID. One Receive = one user reply.
//  3. On receive: reply stored in vars[outputVar] and accumulated; flow continues.
//
// The Redis key is cleared by the WS/SSE handler (after signalling) or by TTL.
package appflow

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// PendingWaitSetInput is the input to AppFlowPendingWaitSetActivity.
// Writes them:wait:{RunID} = NodeID to Redis with a 4-hour TTL.
// The WS/SSE handler reads this key to route subsequent user messages as
// Temporal signals rather than ignoring them.
type PendingWaitSetInput struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id"`
}

// PendingWaitKey returns the Redis key for a run's pending wait state.
func PendingWaitKey(runID string) string {
	return "them:wait:" + runID
}

// PendingWaitStore is the interface used by PendingWaitSetActivity to write/delete
// the pending-wait Redis key. Implemented by *cache.RedisClient (rueidis wrapper)
// in dag-worker/main.go.
type PendingWaitStore interface {
	// SetWait stores nodeID under PendingWaitKey(runID) with a 4-hour TTL.
	SetWait(ctx context.Context, runID, nodeID string) error
	// DelWait removes PendingWaitKey(runID).
	DelWait(ctx context.Context, runID string) error
}

// execWaitForInputNode implements the wait_for_input node dispatch.
// Called from AppFlowWorkflow when node.Kind == "wait_for_input".
// Returns the updated accumulated text and vars after the user replies.
//
// WARNING: this function runs as Temporal workflow code. It MUST be
// deterministic across replays. The signal Receive is deterministic —
// Temporal replays it from history. Do not add any I/O here.
func execWaitForInputNode(
	ctx workflow.Context,
	node *AppFlowNode,
	input AppFlowWorkflowInput,
	accumulated string,
	vars FlowVars,
	shortAO workflow.ActivityOptions,
) (string, FlowVars, error) {
	var cfg WaitForInputConfig
	if len(node.Config) > 0 {
		_ = json.Unmarshal(node.Config, &cfg)
	}
	outputVar := cfg.OutputVar
	if outputVar == "" {
		outputVar = "input"
	}

	traceNode(ctx, input.RunID, node.ID, node.Kind, "node_start", "waiting for user reply", input.LogVerbosity)

	// Write the pending-wait Redis key via activity so the WS handler routes
	// the next message as a signal rather than discarding it.
	waitCtx := workflow.WithActivityOptions(ctx, shortAO)
	err := workflow.ExecuteActivity(waitCtx, AppFlowPendingWaitSetActivityName, PendingWaitSetInput{
		RunID:  input.RunID,
		NodeID: node.ID,
	}).Get(waitCtx, nil)
	if err != nil {
		traceNode(ctx, input.RunID, node.ID, node.Kind, "node_error", err.Error(), input.LogVerbosity)
		return accumulated, vars, fmt.Errorf("wait_for_input %q: set pending wait: %w", node.ID, err)
	}

	// Block until the WS/SSE handler sends the user's reply as a Temporal signal.
	sigName := AppFlowSignalUserInput + ":" + node.ID
	sigCh := workflow.GetSignalChannel(ctx, sigName)
	var reply string
	sigCh.Receive(ctx, &reply)

	// Store the reply in the requested flow var.
	vars[outputVar] = reply
	// Also advance accumulated when outputVar is "input" so downstream nodes
	// that read accumulated (e.g. the next LLM node's default user_prompt) see
	// the fresh user text, not the original first message.
	if outputVar == "input" {
		accumulated = reply
	}

	traceNode(ctx, input.RunID, node.ID, node.Kind, "node_done",
		fmt.Sprintf("reply stored in %s", outputVar), input.LogVerbosity)

	return accumulated, vars, nil
}
