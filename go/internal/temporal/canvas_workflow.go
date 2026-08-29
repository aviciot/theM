package temporal

import (
	"fmt"
	"time"

	temporalerr "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/aviciot/them/internal/agentgen"
)

const (
	// CanvasDAGTaskQueue is the Temporal task queue for CanvasAgentWorkflow and
	// ExecuteStepActivity. Polled exclusively by them-dag-worker.
	CanvasDAGTaskQueue = "canvas-dag-nodes"

	// CanvasAgentWorkflowType is the registered workflow type name.
	CanvasAgentWorkflowType = "CanvasAgentWorkflow"

	// SignalHumanInputPrefix is the prefix for per-node human-input signals.
	// Full signal name: SignalHumanInputPrefix + nodeID.
	SignalHumanInputPrefix = "human_input:"

	// dagWorkflowTimeout is the default maximum lifetime for CanvasAgentWorkflow.
	dagWorkflowTimeout = 12 * time.Minute

	// stepActivityTimeout is the ScheduleToClose for non-human-wait activities.
	stepActivityTimeout = 5 * time.Minute
)

// workflowState is workflow-local mutable state shared across coroutines.
// Safe because the Temporal coroutine scheduler is single-threaded.
type workflowState struct {
	nodeIdx     map[string]*agentgen.PlanNode
	joinArrived map[string]map[string]agentgen.PipelineVars // joinID → fromID → vars
	joinMerged  map[string]agentgen.PipelineVars             // joinID → merged vars (after join fires)
	joinFired   map[string]bool                              // joinID → true once the winning coroutine merged and moved on
	vars        agentgen.PipelineVars                        // global accumulator
	result      *CanvasAgentWorkflowOutput                   // first terminal result
	ic          agentgen.ActivityIC
}

// CanvasAgentWorkflow is an independent top-level Temporal workflow that runs one
// canvas agent DAG execution. NOT a child workflow of OrchestrationWorkflow.
//
// Execution model:
//   - Each PlanNode is executed as an ExecuteStepActivity.
//   - Fan-out (len(Next)>1) launches parallel workflow.Go coroutines.
//   - Join nodes wait via workflow.Await until all expected predecessors arrive.
//   - Branch convergence (JoinBranchMerge): first arrival wins.
//   - HumanWait: activity returns WaitingForHuman=true; workflow awaits a signal.
//   - Any activity error cancels all in-flight coroutines; the causal error is returned.
func CanvasAgentWorkflow(ctx workflow.Context, input CanvasAgentWorkflowInput) (CanvasAgentWorkflowOutput, error) {
	if len(input.Plan.Nodes) == 0 {
		return CanvasAgentWorkflowOutput{}, temporalerr.NewNonRetryableApplicationError(
			"CanvasAgentWorkflow: empty or nil plan",
			"InvalidInput", nil,
		)
	}
	if err := input.IC.Validate(); err != nil {
		return CanvasAgentWorkflowOutput{}, temporalerr.NewNonRetryableApplicationError(
			"CanvasAgentWorkflow: invalid ActivityIC: "+err.Error(),
			"InvalidInput", nil,
		)
	}

	// Build node index.
	nodeIdx := make(map[string]*agentgen.PlanNode, len(input.Plan.Nodes))
	for i := range input.Plan.Nodes {
		n := input.Plan.Nodes[i]
		nodeIdx[n.StepID] = n
	}

	state := &workflowState{
		nodeIdx:     nodeIdx,
		joinArrived: make(map[string]map[string]agentgen.PipelineVars),
		joinMerged:  make(map[string]agentgen.PipelineVars),
		joinFired:   make(map[string]bool),
		vars:        cloneVars(input.Initial),
		ic:          input.IC,
	}

	// cancelCtx cancels all in-flight coroutines when an error occurs.
	cancelCtx, cancelAll := workflow.WithCancel(ctx)

	// errCh carries errors from coroutines. Buffered to len(nodes) so senders never block.
	errCh := workflow.NewBufferedChannel(ctx, len(input.Plan.Nodes)+1)

	// pending tracks how many coroutines are still running.
	// Mutated only from within workflow.Go callbacks — safe (single-threaded scheduler).
	pending := 0
	doneCh := workflow.NewChannel(ctx)

	var launchBranch func(startID, fromID string, branchVars agentgen.PipelineVars)
	launchBranch = func(startID, fromID string, branchVars agentgen.PipelineVars) {
		pending++
		workflow.Go(cancelCtx, func(gCtx workflow.Context) {
			defer func() { doneCh.Send(gCtx, struct{}{}) }()
			runBranch(gCtx, state, startID, fromID, cloneVars(branchVars), &launchBranch, cancelAll, errCh)
		})
	}

	launchBranch(input.Plan.StartID, "", cloneVars(state.vars))

	// Drain completed coroutines.
	for pending > 0 {
		doneCh.Receive(ctx, nil)
		pending--
	}

	// Collect first causal error (prefer non-canceled).
	firstErr := drainWorkflowErrors(errCh)
	if firstErr != nil {
		return CanvasAgentWorkflowOutput{}, firstErr
	}

	if state.result == nil {
		return CanvasAgentWorkflowOutput{}, temporalerr.NewNonRetryableApplicationError(
			"CanvasAgentWorkflow: plan produced no result",
			"NoResult", nil,
		)
	}
	return *state.result, nil
}

// runBranch walks the plan sequentially from startID, launching fan-out coroutines
// for siblings when len(Next)>1. It is called from within a workflow.Go coroutine.
func runBranch(
	ctx workflow.Context,
	state *workflowState,
	startID, fromID string,
	localVars agentgen.PipelineVars,
	launchBranch *func(string, string, agentgen.PipelineVars),
	cancelAll func(),
	errCh workflow.Channel,
) {
	currentID := startID
	prevID := fromID

	for currentID != "" {
		if ctx.Err() != nil {
			return
		}

		node, ok := state.nodeIdx[currentID]
		if !ok {
			errCh.Send(ctx, fmt.Errorf("step %q not found in plan", currentID))
			cancelAll()
			return
		}

		// ── Join: deposit vars and wait ────────────────────────────────────────
		if node.JoinMode == agentgen.JoinWaitAll || node.JoinMode == agentgen.JoinBranchMerge {
			if !handleJoin(ctx, state, node, prevID, localVars) {
				return // not the winning coroutine
			}
			// Retrieve merged vars produced by handleJoin.
			localVars = state.joinMerged[node.StepID]
			delete(state.joinMerged, node.StepID)
		}

		// ── Execute node ───────────────────────────────────────────────────────
		ao := activityOptionsForNode(ctx, node)
		var stepOut StepActivityOutput
		err := workflow.ExecuteActivity(ao, CanvasExecuteStepActivityName, StepActivityInput{
			Node: *node,
			Vars: projectInputs(node, localVars),
			IC:   state.ic,
		}).Get(ctx, &stepOut)
		if err != nil {
			errCh.Send(ctx, fmt.Errorf("step %q (%s): %w", node.StepID, node.Type, err))
			cancelAll()
			return
		}

		// ── HumanWait: receive signal then continue ────────────────────────────
		if stepOut.WaitingForHuman {
			sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+node.StepID)
			var humanVars agentgen.PipelineVars
			sigCh.Receive(ctx, &humanVars)
			if ctx.Err() != nil {
				return
			}
			for k, v := range humanVars {
				localVars[k] = v
			}
			// Continue to next node.
			prevID = node.StepID
			if len(node.Next) > 0 {
				currentID = node.Next[0]
			} else {
				currentID = ""
			}
			continue
		}

		// ── Merge output vars into local + global accumulators ─────────────────
		for k, v := range stepOut.Vars {
			localVars[k] = v
			state.vars[k] = v
		}

		// Capture terminal result (first wins).
		if stepOut.ResultText != "" && state.result == nil {
			state.result = &CanvasAgentWorkflowOutput{
				ResultText: stepOut.ResultText,
				ResultMT:   stepOut.ResultMT,
			}
		}

		// ── Determine next step(s) ─────────────────────────────────────────────
		var nextIDs []string
		if stepOut.NextOverride != "" {
			nextIDs = []string{stepOut.NextOverride}
		} else {
			nextIDs = node.Next
		}

		if len(nextIDs) == 0 {
			return
		}

		// Fan-out: launch sibling coroutines for nextIDs[1:].
		for _, sibID := range nextIDs[1:] {
			(*launchBranch)(sibID, node.StepID, cloneVars(localVars))
		}

		prevID = node.StepID
		currentID = nextIDs[0]
	}
}

// handleJoin deposits localVars for prevID at node's join point.
//
// Returns true if this coroutine should continue past the join node.
// Exactly ONE coroutine returns true per join point:
//   - JoinWaitAll: whichever coroutine arrives last (after all expected branches deposit).
//   - JoinBranchMerge: the first coroutine to arrive.
//
// All others return false. On true, merged vars are in state.joinMerged[node.StepID].
func handleJoin(
	ctx workflow.Context,
	state *workflowState,
	node *agentgen.PlanNode,
	prevID string,
	localVars agentgen.PipelineVars,
) bool {
	// Join already fired — a winner already merged and moved on. Drop this branch.
	if state.joinFired[node.StepID] {
		return false
	}

	expected := len(node.JoinOf)
	if node.JoinMode == agentgen.JoinBranchMerge {
		expected = 1
	}

	// Deposit this branch's vars.
	if state.joinArrived[node.StepID] == nil {
		state.joinArrived[node.StepID] = make(map[string]agentgen.PipelineVars)
	}
	state.joinArrived[node.StepID][prevID] = localVars

	// Wait until enough predecessors have arrived OR join already fired.
	workflow.Await(ctx, func() bool {
		return ctx.Err() != nil ||
			state.joinFired[node.StepID] ||
			len(state.joinArrived[node.StepID]) >= expected
	})
	if ctx.Err() != nil || state.joinFired[node.StepID] {
		return false
	}

	// I am the winner — mark fired so no other coroutine proceeds.
	state.joinFired[node.StepID] = true

	// Merge in JoinOf order (later entries override on key collision).
	merged := make(agentgen.PipelineVars)
	for _, predID := range node.JoinOf {
		if bv, ok := state.joinArrived[node.StepID][predID]; ok {
			for k, v := range bv {
				merged[k] = v
			}
		}
	}

	state.joinMerged[node.StepID] = merged
	return true
}

// activityOptionsForNode returns ActivityOptions appropriate for the node type.
func activityOptionsForNode(ctx workflow.Context, node *agentgen.PlanNode) workflow.Context {
	base := workflow.ActivityOptions{
		TaskQueue:            CanvasDAGTaskQueue,
		StartToCloseTimeout: stepActivityTimeout,
	}

	switch node.Type {
	case agentgen.StepLLM, agentgen.StepHTTP, agentgen.StepA2ACall, agentgen.StepMCPCall:
		base.RetryPolicy = &temporalerr.RetryPolicy{
			MaximumAttempts:        2,
			InitialInterval:        2 * time.Second,
			BackoffCoefficient:     2.0,
			MaximumInterval:        30 * time.Second,
			NonRetryableErrorTypes: []string{"ContractViolation", "InvalidConfig", "PermissionDenied"},
		}
	default:
		base.RetryPolicy = &temporalerr.RetryPolicy{
			MaximumAttempts:        1,
			NonRetryableErrorTypes: []string{"ContractViolation", "InvalidConfig", "PermissionDenied"},
		}
	}

	return workflow.WithActivityOptions(ctx, base)
}

// projectInputs returns a scoped copy of vars containing only the keys declared
// in node.Inputs. When Inputs is empty, returns the full vars (backward-compat).
func projectInputs(node *agentgen.PlanNode, vars agentgen.PipelineVars) agentgen.PipelineVars {
	if len(node.Inputs) == 0 {
		return cloneVars(vars)
	}
	scoped := make(agentgen.PipelineVars, len(node.Inputs))
	for _, ref := range node.Inputs {
		if v, ok := vars[ref.Name]; ok {
			scoped[ref.Name] = v
		}
	}
	return scoped
}

// cloneVars returns a shallow copy of src.
func cloneVars(src agentgen.PipelineVars) agentgen.PipelineVars {
	if src == nil {
		return agentgen.PipelineVars{}
	}
	dst := make(agentgen.PipelineVars, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// drainWorkflowErrors reads all errors from a workflow.Channel and returns the
// first causal (non-canceled) error, or context.Canceled if that's all there is.
func drainWorkflowErrors(ch workflow.Channel) error {
	var first error
	for {
		var e error
		if !ch.ReceiveAsync(&e) {
			break
		}
		if first == nil {
			first = e
		}
		// Prefer a real error over a Canceled placeholder.
		if temporalerr.IsCanceledError(first) && !temporalerr.IsCanceledError(e) {
			first = e
		}
	}
	return first
}
