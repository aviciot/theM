package temporal

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
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
	// dagSem is the per-run activity concurrency semaphore. It counts the number
	// of in-flight ExecuteActivity calls. A coroutine acquires by incrementing
	// dagSemInFlight (checking < limit) and releases by decrementing.
	// Since the Temporal scheduler is single-threaded, no mutex is needed.
	dagSemLimit   int // max concurrent activities for this run
	dagSemInFlight int // current count of activities in flight
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
		nodeIdx:      nodeIdx,
		joinArrived:  make(map[string]map[string]agentgen.PipelineVars),
		joinMerged:   make(map[string]agentgen.PipelineVars),
		joinFired:    make(map[string]bool),
		vars:         cloneVars(input.Initial),
		ic:           input.IC,
		dagSemLimit:  agentgen.ResolveMaxConcurrentTasks(input.MaxConcurrentTasks),
		dagSemInFlight: 0,
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

		// ── Loop: orchestrate body iterations as individual activities ────────
		if node.Type == agentgen.StepLoop {
			outVars, loopErr := runLoopNode(ctx, state, node, localVars)
			if loopErr != nil {
				errCh.Send(ctx, fmt.Errorf("step %q (loop): %w", node.StepID, loopErr))
				cancelAll()
				return
			}
			for k, v := range outVars {
				localVars[k] = v
				state.vars[k] = v
			}
			prevID = node.StepID
			if len(node.Next) > 0 {
				currentID = node.Next[0]
			} else {
				currentID = ""
			}
			continue
		}

		// ── Execute node (guarded by per-run activity semaphore) ──────────────
		// Acquire: wait until in-flight count is below the per-run limit.
		// The semaphore is applied here, around the individual ExecuteActivity call,
		// so goroutines that fan out but are waiting do NOT block join logic.
		// Joins wait via workflow.Await(state.joinArrived check) which is independent.
		workflow.Await(ctx, func() bool {
			return ctx.Err() != nil || state.dagSemInFlight < state.dagSemLimit
		})
		if ctx.Err() != nil {
			return
		}
		state.dagSemInFlight++

		ao := activityOptionsForNode(ctx, node)
		var stepOut StepActivityOutput
		err := workflow.ExecuteActivity(ao, CanvasExecuteStepActivityName, StepActivityInput{
			Node: *node,
			Vars: projectInputs(node, localVars),
			IC:   state.ic,
		}).Get(ctx, &stepOut)

		// Release immediately after activity completes (success or failure).
		state.dagSemInFlight--

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
		// Check ResultMT too — a Response node may set only ResultMT (e.g. JSON with empty text).
		if (stepOut.ResultText != "" || stepOut.ResultMT != "") && state.result == nil {
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

// activityOptionsForNode returns ActivityOptions driven entirely by node.Policy.
// Both timeout and retry policy come from the resolved ExecutionPolicy so that
// LocalExecutor and Temporal always use the same per-node settings.
func activityOptionsForNode(ctx workflow.Context, node *agentgen.PlanNode) workflow.Context {
	p := node.Policy

	// Guard: zero MaxAttempts (unresolved policy) → treat as 1.
	maxAttempts := p.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}

	timeout := stepActivityTimeout
	if p.TimeoutSeconds > 0 {
		timeout = time.Duration(p.TimeoutSeconds) * time.Second
	}

	nonRetryable := p.NonRetryableErrors
	if len(nonRetryable) == 0 {
		nonRetryable = []string{"ContractViolation", "InvalidConfig", "PermissionDenied"}
	}

	opts := workflow.ActivityOptions{
		TaskQueue:           CanvasDAGTaskQueue,
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporalerr.RetryPolicy{
			MaximumAttempts:        maxAttempts,
			InitialInterval:        time.Duration(p.InitialIntervalSeconds * float64(time.Second)),
			BackoffCoefficient:     p.BackoffCoefficient,
			MaximumInterval:        time.Duration(p.MaxIntervalSeconds) * time.Second,
			NonRetryableErrorTypes: nonRetryable,
		},
	}
	return workflow.WithActivityOptions(ctx, opts)
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

// runLoopNode executes a StepLoop node in the Temporal path by iterating items
// sequentially and scheduling every body step as its own ExecuteStepActivity.
//
// Each body step gets its own activity with the policy from SubPlan.Nodes[i].Policy,
// giving it independent retry, timeout, and Temporal history entry.
//
// Body iterations are sequential (one item at a time). Parallel iteration is a future
// enhancement controlled by a LoopConfig.Parallel flag; not implemented here.
//
// Returns outVars containing accum_var (if configured) and any vars written during
// the last body iteration. The caller merges outVars into localVars and state.vars.
func runLoopNode(
	ctx workflow.Context,
	state *workflowState,
	node *agentgen.PlanNode,
	localVars agentgen.PipelineVars,
) (agentgen.PipelineVars, error) {
	var cfg agentgen.LoopConfig
	if len(node.Config) > 0 {
		if err := json.Unmarshal(node.Config, &cfg); err != nil {
			return nil, fmt.Errorf("invalid loop config: %w", err)
		}
	}
	if cfg.ItemsVar == "" {
		return nil, fmt.Errorf("items_var is required")
	}
	if node.SubPlan == nil || len(node.SubPlan.Nodes) == 0 {
		// Empty body — no-op; return vars unchanged.
		return agentgen.PipelineVars{}, nil
	}

	itemVar := cfg.ItemVar
	if itemVar == "" {
		itemVar = "item"
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 100
	}

	raw, ok := localVars[cfg.ItemsVar]
	if !ok {
		return agentgen.PipelineVars{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%q must be a list, got %T", cfg.ItemsVar, raw)
	}

	// Build body node index and collect declared output keys.
	bodyIdx := make(map[string]*agentgen.PlanNode, len(node.SubPlan.Nodes))
	bodyOutputKeys := make(map[string]bool)
	for _, bn := range node.SubPlan.Nodes {
		bodyIdx[bn.StepID] = bn
		for _, ref := range bn.Outputs {
			bodyOutputKeys[ref.Name] = true
		}
	}

	var accumulated []any
	outVars := agentgen.PipelineVars{} // collects accum_var + last-iteration body outputs

	for i, item := range items {
		if i >= maxIter {
			break
		}

		// Build per-iteration vars starting from the outer localVars.
		iterVars := cloneVars(localVars)
		iterVars[itemVar] = item

		// Apply optional condition filter (deterministic template rendering).
		if cfg.Condition != "" {
			tmpl, err := template.New("loop_cond").Option("missingkey=zero").Parse(cfg.Condition)
			if err != nil {
				return nil, fmt.Errorf("iteration %d: condition parse error: %w", i, err)
			}
			var buf strings.Builder
			if err := tmpl.Execute(&buf, iterVars); err != nil {
				return nil, fmt.Errorf("iteration %d: condition execute error: %w", i, err)
			}
			if strings.TrimSpace(buf.String()) != "true" {
				continue
			}
		}

		// Walk the body sequentially, scheduling each step as its own activity.
		currentBodyID := node.SubPlan.StartID
		for currentBodyID != "" {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			bodyNode, found := bodyIdx[currentBodyID]
			if !found {
				return nil, fmt.Errorf("iteration %d: body step %q not found in sub-plan", i, currentBodyID)
			}

			// Acquire outer semaphore before scheduling the body activity.
			workflow.Await(ctx, func() bool {
				return ctx.Err() != nil || state.dagSemInFlight < state.dagSemLimit
			})
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			state.dagSemInFlight++

			ao := activityOptionsForNode(ctx, bodyNode)
			var bodyOut StepActivityOutput
			actErr := workflow.ExecuteActivity(ao, CanvasExecuteStepActivityName, StepActivityInput{
				Node: *bodyNode,
				Vars: projectInputs(bodyNode, iterVars),
				IC:   state.ic,
			}).Get(ctx, &bodyOut)

			state.dagSemInFlight--

			if actErr != nil {
				return nil, fmt.Errorf("iteration %d body step %q: %w", i, currentBodyID, actErr)
			}

			// Merge body step outputs into iteration vars.
			for k, v := range bodyOut.Vars {
				iterVars[k] = v
			}

			// Advance to next body step (branch override or node.Next[0]).
			if bodyOut.NextOverride != "" {
				currentBodyID = bodyOut.NextOverride
			} else if len(bodyNode.Next) > 0 {
				currentBodyID = bodyNode.Next[0]
			} else {
				currentBodyID = ""
			}
		}

		// Merge last-iteration body outputs back into outer localVars.
		for k, v := range iterVars {
			localVars[k] = v
			outVars[k] = v
		}

		// Accumulate only declared body output keys (or all if none declared).
		if cfg.AccumVar != "" {
			snapshot := make(agentgen.PipelineVars)
			if len(bodyOutputKeys) > 0 {
				for k := range bodyOutputKeys {
					if v, exists := iterVars[k]; exists {
						snapshot[k] = v
					}
				}
			} else {
				for k, v := range iterVars {
					snapshot[k] = v
				}
			}
			accumulated = append(accumulated, snapshot)
		}
	}

	if cfg.AccumVar != "" {
		outVars[cfg.AccumVar] = accumulated
	}
	return outVars, nil
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
