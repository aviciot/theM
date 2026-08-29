package agentgen

import (
	"context"
	"fmt"
	"sync"
)

// LocalExecutor implements ExecutionBackend using goroutines.
// Fan-out nodes launch each branch in a separate goroutine with a deep-copied
// PipelineVars. Join nodes (JoinWaitAll) block until all predecessor branches
// arrive then merge their vars (JoinOf order, last-write-wins per key).
// Any branch error cancels all other in-flight branches immediately.
type LocalExecutor struct {
	interp *Interpreter
}

// NewLocalExecutor creates a LocalExecutor backed by interp.
func NewLocalExecutor(interp *Interpreter) *LocalExecutor {
	return &LocalExecutor{interp: interp}
}

// Execute runs the plan from plan.StartID.
// initial is the seed PipelineVars (e.g. {"input": userText}).
func (e *LocalExecutor) Execute(
	ctx context.Context,
	ic *InvocationContext,
	plan *ExecutionPlan,
	initial PipelineVars,
) (*ExecutionResult, error) {
	if plan == nil || len(plan.Nodes) == 0 {
		return nil, fmt.Errorf("LocalExecutor: empty or nil plan")
	}

	// Build node index for O(1) lookup.
	nodeIdx := make(map[string]*PlanNode, len(plan.Nodes))
	for _, n := range plan.Nodes {
		nodeIdx[n.StepID] = n
	}

	// joinState tracks arrivals at join nodes.
	js := &joinState{
		arrived: make(map[string][]PipelineVars),
		count:   make(map[string]int),
	}
	// Pre-populate expected arrival counts from JoinOf lists.
	for _, n := range plan.Nodes {
		if n.JoinMode == JoinWaitAll {
			js.count[n.StepID] = len(n.JoinOf)
		}
	}

	// Shared result — first response/stream_out step wins.
	res := &sharedResult{}

	// cancelable context — any branch error cancels all others.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel for branch errors — buffered so goroutines never block.
	errCh := make(chan error, len(plan.Nodes))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := e.runBranch(runCtx, ic, plan, nodeIdx, js, res, deepCopyVars(initial), plan.StartID, cancel, errCh, &wg, e.interp.clone()); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	wg.Wait()

	// Collect first error if any.
	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	result := res.get()
	if result == nil {
		return nil, fmt.Errorf("plan %q produced no result", plan.SkillID)
	}
	return result, nil
}

// runBranch walks the plan from startID, running each node sequentially.
// At a fan-out node (len(Next)>1) it launches sibling goroutines.
// At a join node it deposits vars and either waits (not last) or merges (last).
func (e *LocalExecutor) runBranch(
	ctx context.Context,
	ic *InvocationContext,
	plan *ExecutionPlan,
	nodeIdx map[string]*PlanNode,
	js *joinState,
	res *sharedResult,
	vars PipelineVars,
	startID string,
	cancel context.CancelFunc,
	errCh chan<- error,
	wg *sync.WaitGroup,
	interp *Interpreter, // per-goroutine clone — owns nextStepOverride, never shared
) error {
	currentID := startID
	visited := make(map[string]bool)

	for currentID != "" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if visited[currentID] {
			return fmt.Errorf("cycle detected at step %q", currentID)
		}
		visited[currentID] = true

		node, ok := nodeIdx[currentID]
		if !ok {
			return fmt.Errorf("step %q not found in plan %q", currentID, plan.SkillID)
		}

		// ── Join: deposit vars; only last arrival continues ────────────────────
		if node.JoinMode == JoinWaitAll {
			ready, merged, err := js.arrive(node.StepID, len(node.JoinOf), vars)
			if err != nil {
				return err
			}
			if !ready {
				// Not the last arrival — this goroutine's work is done.
				return nil
			}
			// Last arrival: continue with merged vars.
			vars = merged
		}

		// ── Execute the node ───────────────────────────────────────────────────
		stepSpec := planNodeToStepSpec(node)
		nextOverride, err := e.execNode(ctx, ic, interp, stepSpec, vars, res)
		if err != nil {
			cancel()
			return fmt.Errorf("step %q (%s): %w", node.StepID, node.Type, err)
		}

		// ── Determine next step ────────────────────────────────────────────────
		var nextIDs []string
		if nextOverride != "" {
			nextIDs = []string{nextOverride}
		} else {
			nextIDs = node.Next
		}

		if len(nextIDs) == 0 {
			return nil
		}

		// ── Fan-out: launch siblings, continue inline with first ───────────────
		if len(nextIDs) > 1 {
			for _, sibID := range nextIDs[1:] {
				sibID := sibID // capture
				sibVars := deepCopyVars(vars)
				sibInterp := e.interp.clone() // each goroutine owns its nextStepOverride
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := e.runBranch(ctx, ic, plan, nodeIdx, js, res, sibVars, sibID, cancel, errCh, wg, sibInterp); err != nil {
						select {
						case errCh <- err:
						default:
						}
					}
				}()
			}
		}

		currentID = nextIDs[0]
	}
	return nil
}

// execNode runs one plan node using the caller's per-goroutine interp clone.
// interp.nextStepOverride is safe to read/write because each goroutine has its own clone.
func (e *LocalExecutor) execNode(
	ctx context.Context,
	ic *InvocationContext,
	interp *Interpreter,
	step *StepSpec,
	vars PipelineVars,
	res *sharedResult,
) (nextOverride string, err error) {
	localResult := &ExecutionResult{MediaType: "text/plain"}

	interp.nextStepOverride = ""
	if execErr := interp.executeStep(ctx, ic, step, vars, localResult); execErr != nil {
		return "", execErr
	}
	override := interp.nextStepOverride
	interp.nextStepOverride = ""

	// Promote result if this step produced one.
	if localResult.Text != "" || step.Type == StepResponse || step.Type == StepStreamOut {
		res.setIfEmpty(localResult)
	}

	return override, nil
}

// planNodeToStepSpec converts a PlanNode back to a StepSpec for executeStep.
func planNodeToStepSpec(n *PlanNode) *StepSpec {
	return &StepSpec{
		ID:       n.StepID,
		Type:     n.Type,
		Config:   n.Config,
		Next:     n.Next,
		Branches: n.Branches,
		Inputs:   n.Inputs,
		Outputs:  n.Outputs,
	}
}

// ── joinState ─────────────────────────────────────────────────────────────────

// joinState tracks branch arrivals at join nodes.
// All methods are safe for concurrent use.
type joinState struct {
	mu      sync.Mutex
	arrived map[string][]PipelineVars // stepID → arriving branch vars
	count   map[string]int            // stepID → expected total arrivals
}

// arrive deposits vars for joinID. Returns (ready=true, merged) when all
// expected branches have arrived. Returns (false, nil) if more are pending.
func (js *joinState) arrive(joinID string, expected int, vars PipelineVars) (bool, PipelineVars, error) {
	js.mu.Lock()
	defer js.mu.Unlock()

	js.arrived[joinID] = append(js.arrived[joinID], vars)
	got := len(js.arrived[joinID])

	if got < expected {
		return false, nil, nil
	}

	// All branches arrived — merge in JoinOf order (last-write-wins per key).
	merged := make(PipelineVars)
	for _, bv := range js.arrived[joinID] {
		for k, v := range bv {
			merged[k] = v
		}
	}
	delete(js.arrived, joinID) // free memory
	return true, merged, nil
}

// ── sharedResult ──────────────────────────────────────────────────────────────

// sharedResult holds the first ExecutionResult produced by any branch.
type sharedResult struct {
	mu  sync.Mutex
	val *ExecutionResult
}

func (r *sharedResult) setIfEmpty(v *ExecutionResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.val == nil {
		r.val = v
	}
}

func (r *sharedResult) get() *ExecutionResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.val
}

// ── deepCopyVars ──────────────────────────────────────────────────────────────

// deepCopyVars returns a deep copy of vars so concurrent branches cannot
// interfere with each other's mutations on map and slice values.
func deepCopyVars(src PipelineVars) PipelineVars {
	if src == nil {
		return PipelineVars{}
	}
	dst := make(PipelineVars, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, inner := range val {
			cp[k] = deepCopyValue(inner)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, inner := range val {
			cp[i] = deepCopyValue(inner)
		}
		return cp
	default:
		// string, int, float64, bool, nil — value types, safe to copy directly.
		return v
	}
}
