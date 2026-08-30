package temporal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
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

// HITLQueryStatus is the payload returned by the "hitl_status" workflow query.
// The agent-runtime polls this to synchronize the HITLStore without needing a
// reverse push from the workflow. Never logged or written to Temporal history;
// it is returned only as a query response.
type HITLQueryStatus struct {
	State     string `json:"state"`      // "submitted" | "waiting" | "signalled" | "done"
	WaitToken string `json:"wait_token"` // deterministic token for the current wait occurrence
	StepID    string `json:"step_id"`    // canvas node ID currently waiting for signal
}

// hitlStatusState is workflow-local HITL tracking state.
type hitlStatusState struct {
	status  HITLQueryStatus
	counter int // occurrence counter for deterministic wait_token generation
}

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
	hitl        hitlStatusState // HITL query state (polled by agent-runtime)
	// dagSem is the per-run activity concurrency semaphore. It counts the number
	// of in-flight ExecuteActivity calls. A coroutine acquires by incrementing
	// dagSemInFlight (checking < limit) and releases by decrementing.
	// Since the Temporal scheduler is single-threaded, no mutex is needed.
	dagSemLimit    int // max concurrent activities for this run
	dagSemInFlight int // current count of activities in flight
}

// hitlWaitToken generates a deterministic wait token for a given human_wait
// occurrence. It must be deterministic so Temporal replay produces the same token.
// Never call uuid.New() inside workflow code — it is non-deterministic.
func hitlWaitToken(runID, stepID string, counter int) string {
	sum := sha256.Sum256([]byte(runID + ":" + stepID + ":" + strconv.Itoa(counter)))
	return fmt.Sprintf("%x", sum[:8]) // 16-char hex token
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
		nodeIdx:        nodeIdx,
		joinArrived:    make(map[string]map[string]agentgen.PipelineVars),
		joinMerged:     make(map[string]agentgen.PipelineVars),
		joinFired:      make(map[string]bool),
		vars:           cloneVars(input.Initial),
		ic:             input.IC,
		dagSemLimit:    agentgen.ResolveMaxConcurrentTasks(input.MaxConcurrentTasks),
		dagSemInFlight: 0,
		hitl: hitlStatusState{
			status: HITLQueryStatus{State: agentgen.HITLStateSubmitted},
		},
	}

	// Register hitl_status query handler. The agent-runtime polls this to
	// synchronize HITLStore state without a reverse push from the workflow.
	// The closure captures state.hitl directly — safe because the Temporal
	// scheduler is single-threaded (query handlers run between coroutine steps).
	if err := workflow.SetQueryHandler(ctx, "hitl_status", func() (HITLQueryStatus, error) {
		return state.hitl.status, nil
	}); err != nil {
		return CanvasAgentWorkflowOutput{}, fmt.Errorf("CanvasAgentWorkflow: register query handler: %w", err)
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

		// ── HumanWait: update HITL state, receive signal then continue ───────────
		if stepOut.WaitingForHuman {
			// Generate a deterministic wait token for this occurrence.
			// counter increments per wait so repeated waits (loop bodies) get unique tokens.
			state.hitl.counter++
			tok := hitlWaitToken(workflow.GetInfo(ctx).WorkflowExecution.RunID, node.StepID, state.hitl.counter)
			state.hitl.status = HITLQueryStatus{
				State:     agentgen.HITLStateWaiting,
				WaitToken: tok,
				StepID:    node.StepID,
			}

			sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+node.StepID)

			// Parse per-step timeout from the node config.
			var hwCfg agentgen.HumanWaitConfig
			if len(node.Config) > 0 {
				_ = json.Unmarshal(node.Config, &hwCfg)
			}

			var humanVars agentgen.PipelineVars
			received := false
			if hwCfg.TimeoutSeconds > 0 {
				// Use workflow.Select + a timer so the workflow can time out per step.
				timer := workflow.NewTimer(ctx, time.Duration(hwCfg.TimeoutSeconds)*time.Second)
				sel := workflow.NewSelector(ctx)
				sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, _ bool) {
					c.Receive(ctx, &humanVars)
					received = true
				})
				sel.AddFuture(timer, func(_ workflow.Future) {
					// Timeout: cancel the coroutine path.
				})
				sel.Select(ctx)
			} else {
				sigCh.Receive(ctx, &humanVars)
				received = true
			}

			if ctx.Err() != nil {
				return
			}
			if !received {
				// Per-step timeout expired — treat as workflow-level cancellation.
				errCh.Send(ctx, temporalerr.NewNonRetryableApplicationError(
					fmt.Sprintf("human_wait step %q timed out after %ds", node.StepID, hwCfg.TimeoutSeconds),
					"HumanWaitTimeout", nil,
				))
				cancelAll()
				return
			}

			state.hitl.status = HITLQueryStatus{
				State:     agentgen.HITLStateSignalled,
				WaitToken: tok,
				StepID:    node.StepID,
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

// bodyIterState holds per-iteration join state so iterations don't bleed into each other.
type bodyIterState struct {
	nodeIdx     map[string]*agentgen.PlanNode
	joinArrived map[string]map[string]agentgen.PipelineVars
	joinMerged  map[string]agentgen.PipelineVars
	joinFired   map[string]bool
}

func newBodyIterState(bodyNodes []*agentgen.PlanNode) *bodyIterState {
	idx := make(map[string]*agentgen.PlanNode, len(bodyNodes))
	for i := range bodyNodes {
		n := bodyNodes[i]
		idx[n.StepID] = n
	}
	return &bodyIterState{
		nodeIdx:     idx,
		joinArrived: make(map[string]map[string]agentgen.PipelineVars),
		joinMerged:  make(map[string]agentgen.PipelineVars),
		joinFired:   make(map[string]bool),
	}
}

// runLoopNode executes a StepLoop node in the Temporal path.
//
// Each body step is its own ExecuteStepActivity with its own policy/retry/timeout.
// Branch/Parallel/Join inside the body are handled with per-iteration isolated join
// state and workflow.Go coroutines so that fan-out works correctly.
// Iteration state is fully isolated — no join arrival bleeds between iterations.
//
// Only declared body output keys are merged back to outer vars and accumulated.
// No fallback to all vars is applied; body nodes must declare their Outputs.
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

	// Collect declared output keys across ALL body nodes.
	bodyOutputKeys := make(map[string]bool)
	for _, bn := range node.SubPlan.Nodes {
		for _, ref := range bn.Outputs {
			bodyOutputKeys[ref.Name] = true
		}
	}
	// Only enforce declared outputs when there are items to iterate.
	if len(items) > 0 && len(bodyOutputKeys) == 0 {
		return nil, fmt.Errorf("loop body steps must declare at least one output (add Outputs to body nodes)")
	}

	var accumulated []any
	outVars := agentgen.PipelineVars{}

	for i, item := range items {
		if i >= maxIter {
			break
		}

		iterVars := cloneVars(localVars)
		iterVars[itemVar] = item

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

		// Run the body sub-plan for this iteration with isolated join state.
		postVars, err := runBodyIteration(ctx, state, node.SubPlan, iterVars, i)
		if err != nil {
			return nil, fmt.Errorf("iteration %d: %w", i, err)
		}

		// Merge only declared body output keys back into outer scope.
		for k := range bodyOutputKeys {
			if v, exists := postVars[k]; exists {
				localVars[k] = v
				outVars[k] = v
			}
		}

		if cfg.AccumVar != "" {
			snapshot := make(agentgen.PipelineVars, len(bodyOutputKeys))
			for k := range bodyOutputKeys {
				if v, exists := postVars[k]; exists {
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

// runBodyIteration executes one iteration of the loop body sub-plan.
// It creates an isolated bodyIterState so join arrivals from this iteration
// do not contaminate other iterations. Fan-out/Join inside the body is handled
// with workflow.Go coroutines, mirroring the outer runBranch structure.
//
// Returns the merged post-execution vars from all terminal branches.
func runBodyIteration(
	ctx workflow.Context,
	state *workflowState,
	plan *agentgen.ExecutionPlan,
	iterVars agentgen.PipelineVars,
	iterIdx int,
) (agentgen.PipelineVars, error) {
	bis := newBodyIterState(plan.Nodes)

	// errCh carries errors from body coroutines. Buffered to plan size.
	errCh := workflow.NewChannel(ctx)

	// terminalVarsCh captures vars from each terminal branch.
	terminalVarsCh := workflow.NewChannel(ctx)

	// cancelCtx + cancel for body-wide cancellation on error.
	bodyCtx, cancelBody := workflow.WithCancel(ctx)
	_ = cancelBody // called on first error

	var inFlight int

	var launchBodyBranch func(startID, fromID string, branchVars agentgen.PipelineVars)
	launchBodyBranch = func(startID, fromID string, branchVars agentgen.PipelineVars) {
		inFlight++
		workflow.Go(bodyCtx, func(gCtx workflow.Context) {
			defer func() { inFlight-- }()
			runBodyBranch(gCtx, state, bis, plan, startID, fromID, cloneVars(branchVars),
				&launchBodyBranch, cancelBody, errCh, terminalVarsCh)
		})
	}

	launchBodyBranch(plan.StartID, "", cloneVars(iterVars))

	// Collect results: wait until all in-flight coroutines finish.
	mergedTerminal := make(agentgen.PipelineVars)
	var firstErr error

	workflow.Await(bodyCtx, func() bool {
		// Drain errCh.
		var e error
		for errCh.ReceiveAsync(&e) {
			if firstErr == nil {
				firstErr = e
			}
			cancelBody()
		}
		// Drain terminalVarsCh.
		var tv agentgen.PipelineVars
		for terminalVarsCh.ReceiveAsync(&tv) {
			for k, v := range tv {
				mergedTerminal[k] = v
			}
		}
		return inFlight == 0 || firstErr != nil || bodyCtx.Err() != nil
	})

	// Final drain after Await.
	var e error
	for errCh.ReceiveAsync(&e) {
		if firstErr == nil {
			firstErr = e
		}
	}
	var tv agentgen.PipelineVars
	for terminalVarsCh.ReceiveAsync(&tv) {
		for k, v := range tv {
			mergedTerminal[k] = v
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	_ = iterIdx
	return mergedTerminal, nil
}

// runBodyBranch walks one branch of the body sub-plan, scheduling each step
// as its own ExecuteStepActivity. Fan-out launches sibling coroutines. Join nodes
// use the per-iteration bodyIterState (never the outer workflowState join maps).
func runBodyBranch(
	ctx workflow.Context,
	state *workflowState,
	bis *bodyIterState,
	plan *agentgen.ExecutionPlan,
	startID, fromID string,
	localVars agentgen.PipelineVars,
	launchBranch *func(string, string, agentgen.PipelineVars),
	cancelAll func(),
	errCh workflow.Channel,
	terminalCh workflow.Channel,
) {
	currentID := startID
	prevID := fromID

	for currentID != "" {
		if ctx.Err() != nil {
			return
		}

		bodyNode, found := bis.nodeIdx[currentID]
		if !found {
			errCh.Send(ctx, fmt.Errorf("body step %q not found in sub-plan", currentID))
			cancelAll()
			return
		}

		// ── Join: use per-iteration bis join state ──────────────────────────
		if bodyNode.JoinMode == agentgen.JoinWaitAll || bodyNode.JoinMode == agentgen.JoinBranchMerge {
			if !handleBodyJoin(ctx, bis, bodyNode, prevID, localVars) {
				return // not the winning coroutine for this join
			}
			localVars = bis.joinMerged[bodyNode.StepID]
			delete(bis.joinMerged, bodyNode.StepID)
		}

		// ── Execute as its own activity ─────────────────────────────────────
		workflow.Await(ctx, func() bool {
			return ctx.Err() != nil || state.dagSemInFlight < state.dagSemLimit
		})
		if ctx.Err() != nil {
			return
		}
		state.dagSemInFlight++

		ao := activityOptionsForNode(ctx, bodyNode)
		var bodyOut StepActivityOutput
		actErr := workflow.ExecuteActivity(ao, CanvasExecuteStepActivityName, StepActivityInput{
			Node: *bodyNode,
			Vars: projectInputs(bodyNode, localVars),
			IC:   state.ic,
		}).Get(ctx, &bodyOut)

		state.dagSemInFlight--

		if actErr != nil {
			errCh.Send(ctx, fmt.Errorf("body step %q: %w", currentID, actErr))
			cancelAll()
			return
		}

		// ── HumanWait inside loop body ──────────────────────────────────────
		if bodyOut.WaitingForHuman {
			state.hitl.counter++
			tok := hitlWaitToken(workflow.GetInfo(ctx).WorkflowExecution.RunID, bodyNode.StepID, state.hitl.counter)
			state.hitl.status = HITLQueryStatus{
				State:     agentgen.HITLStateWaiting,
				WaitToken: tok,
				StepID:    bodyNode.StepID,
			}

			sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+bodyNode.StepID)

			var hwCfg agentgen.HumanWaitConfig
			if len(bodyNode.Config) > 0 {
				_ = json.Unmarshal(bodyNode.Config, &hwCfg)
			}

			var humanVars agentgen.PipelineVars
			received := false
			if hwCfg.TimeoutSeconds > 0 {
				timer := workflow.NewTimer(ctx, time.Duration(hwCfg.TimeoutSeconds)*time.Second)
				sel := workflow.NewSelector(ctx)
				sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, _ bool) {
					c.Receive(ctx, &humanVars)
					received = true
				})
				sel.AddFuture(timer, func(_ workflow.Future) {})
				sel.Select(ctx)
			} else {
				sigCh.Receive(ctx, &humanVars)
				received = true
			}

			if ctx.Err() != nil {
				return
			}
			if !received {
				errCh.Send(ctx, temporalerr.NewNonRetryableApplicationError(
					fmt.Sprintf("human_wait body step %q timed out after %ds", bodyNode.StepID, hwCfg.TimeoutSeconds),
					"HumanWaitTimeout", nil,
				))
				cancelAll()
				return
			}

			state.hitl.status = HITLQueryStatus{
				State:     agentgen.HITLStateSignalled,
				WaitToken: tok,
				StepID:    bodyNode.StepID,
			}

			for k, v := range humanVars {
				localVars[k] = v
			}
			prevID = bodyNode.StepID
			if len(bodyNode.Next) > 0 {
				currentID = bodyNode.Next[0]
			} else {
				terminalCh.Send(ctx, localVars)
				return
			}
			continue
		}

		for k, v := range bodyOut.Vars {
			localVars[k] = v
		}

		// ── Determine next ──────────────────────────────────────────────────
		var nextIDs []string
		if bodyOut.NextOverride != "" {
			nextIDs = []string{bodyOut.NextOverride}
		} else {
			nextIDs = bodyNode.Next
		}

		if len(nextIDs) == 0 {
			// Terminal body node — send final vars to collector.
			terminalCh.Send(ctx, localVars)
			return
		}

		// Fan-out sibling branches.
		if len(nextIDs) > 1 {
			for _, sibID := range nextIDs[1:] {
				sibID := sibID
				(*launchBranch)(sibID, bodyNode.StepID, cloneVars(localVars))
			}
		}

		prevID = bodyNode.StepID
		currentID = nextIDs[0]
	}
}

// handleBodyJoin is the per-iteration equivalent of handleJoin but uses
// bodyIterState instead of workflowState. This keeps join arrivals per-iteration.
func handleBodyJoin(
	ctx workflow.Context,
	bis *bodyIterState,
	node *agentgen.PlanNode,
	prevID string,
	localVars agentgen.PipelineVars,
) bool {
	if bis.joinFired[node.StepID] {
		return false
	}

	expected := len(node.JoinOf)
	if node.JoinMode == agentgen.JoinBranchMerge {
		expected = 1
	}

	if bis.joinArrived[node.StepID] == nil {
		bis.joinArrived[node.StepID] = make(map[string]agentgen.PipelineVars)
	}
	bis.joinArrived[node.StepID][prevID] = localVars

	workflow.Await(ctx, func() bool {
		return ctx.Err() != nil ||
			bis.joinFired[node.StepID] ||
			len(bis.joinArrived[node.StepID]) >= expected
	})
	if ctx.Err() != nil || bis.joinFired[node.StepID] {
		return false
	}

	bis.joinFired[node.StepID] = true

	merged := make(agentgen.PipelineVars)
	for _, predID := range node.JoinOf {
		if bv, ok := bis.joinArrived[node.StepID][predID]; ok {
			for k, v := range bv {
				merged[k] = v
			}
		}
	}
	bis.joinMerged[node.StepID] = merged
	return true
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
