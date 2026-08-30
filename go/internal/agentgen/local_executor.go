package agentgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// LocalExecutor implements ExecutionBackend using goroutines.
// Fan-out nodes launch each branch in a separate goroutine with a deep-copied
// PipelineVars. Join nodes block according to their JoinMode:
//   - JoinWaitAll: block until ALL predecessor branches arrive; merge in JoinOf order.
//   - JoinBranchMerge: first arrival wins (only one branch arm ever runs); continue immediately.
//
// Any branch error cancels all other in-flight branches. The causal error (not
// context.Canceled from siblings) is always returned.
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
		arrived: make(map[string]map[string]PipelineVars),
		count:   make(map[string]int),
	}
	// Pre-populate expected arrival counts from JoinOf lists.
	for _, n := range plan.Nodes {
		if n.JoinMode == JoinWaitAll {
			js.count[n.StepID] = len(n.JoinOf)
		}
		// JoinBranchMerge expects exactly 1 (first arrival wins).
		if n.JoinMode == JoinBranchMerge {
			js.count[n.StepID] = 1
		}
	}

	// Shared result — first response/stream_out step wins.
	res := &sharedResult{}

	// cancelable context — any branch error cancels all others.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Per-run concurrency semaphore: limits how many nodes execute simultaneously.
	// Acquired per attempt in execNode; released after the attempt completes.
	// Goroutines that fan out but can't acquire wait without blocking join logic —
	// joins do not hold the semaphore, so no deadlock.
	limit := ResolveMaxConcurrentTasks(ic.Policies.MaxConcurrentTasks)
	sem := make(chan struct{}, limit)

	// errCh carries the causal error from whichever branch first fails.
	// Buffered to len(plan.Nodes) so goroutines never block on send.
	errCh := make(chan error, len(plan.Nodes))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := e.runBranch(runCtx, ic, plan, nodeIdx, js, res, deepCopyVars(initial), plan.StartID, "", cancel, errCh, &wg, e.interp.clone(), sem); err != nil {
			sendErr(errCh, err)
		}
	}()

	wg.Wait()

	// Return the first causal error. Prefer non-Canceled errors over Canceled.
	if err := drainFirstCausalError(errCh); err != nil {
		return nil, err
	}

	result := res.get()
	if result == nil {
		return nil, fmt.Errorf("plan %q produced no result", plan.SkillID)
	}
	return result, nil
}

// drainFirstCausalError reads all errors from ch and returns the first
// non-context.Canceled one, or context.Canceled if that's all there is,
// or nil if the channel is empty.
func drainFirstCausalError(ch <-chan error) error {
	var first error
	for {
		select {
		case err := <-ch:
			if first == nil {
				first = err
			}
			// Replace a Canceled placeholder with a real error if we find one.
			if errors.Is(first, context.Canceled) && !errors.Is(err, context.Canceled) {
				first = err
			}
		default:
			if first == nil {
				return nil
			}
			// Final result: if we only collected a no-result sentinel, convert.
			return first
		}
	}
}

// sendErr sends err to ch without blocking; drops if full (already an error in flight).
func sendErr(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

// runBranch walks the plan from startID, running each node sequentially.
// fromID is the predecessor step ID that sent this branch here (used for join keying).
// At a fan-out node (len(Next)>1) it launches sibling goroutines.
// At a join node it deposits vars and either waits (not last) or merges (last).
// sem is the per-run concurrency semaphore threaded from Execute.
func (e *LocalExecutor) runBranch(
	ctx context.Context,
	ic *InvocationContext,
	plan *ExecutionPlan,
	nodeIdx map[string]*PlanNode,
	js *joinState,
	res *sharedResult,
	vars PipelineVars,
	startID string,
	fromID string, // which predecessor step launched this branch
	cancel context.CancelFunc,
	errCh chan<- error,
	wg *sync.WaitGroup,
	interp *Interpreter, // per-goroutine clone — owns nextStepOverride, never shared
	sem chan struct{},    // per-run concurrency semaphore; never nil
) error {
	currentID := startID
	prevID := fromID
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

		// ── Join: deposit vars; only the triggering arrival continues ──────────
		switch node.JoinMode {
		case JoinWaitAll:
			ready, merged, err := js.arrive(node.StepID, node.JoinOf, prevID, len(node.JoinOf), vars)
			if err != nil {
				return err
			}
			if !ready {
				return nil // not the last arrival — done
			}
			vars = merged

		case JoinBranchMerge:
			// Only one branch arm executes. First arrival wins and continues;
			// any subsequent arrivals (from arms that somehow ran) are silently
			// dropped. Use count=1 so arrive() returns ready on first call.
			ready, merged, err := js.arrive(node.StepID, node.JoinOf, prevID, 1, vars)
			if err != nil {
				return err
			}
			if !ready {
				return nil // a sibling arm already won
			}
			vars = merged
		}

		// ── Execute the node ───────────────────────────────────────────────────
		stepSpec := planNodeToStepSpec(node)
		nextOverride, err := e.execNode(ctx, ic, interp, stepSpec, node.Policy, vars, res, sem)
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
				curID := node.StepID          // capture as fromID for the sibling
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := e.runBranch(ctx, ic, plan, nodeIdx, js, res, sibVars, sibID, curID, cancel, errCh, wg, sibInterp, sem); err != nil {
						sendErr(errCh, err)
					}
				}()
			}
		}

		prevID = node.StepID
		currentID = nextIDs[0]
	}
	return nil
}

// isNonRetryable returns true when err must not be retried.
//
// Detection order:
//  1. Context cancellation / deadline — always non-retryable.
//  2. NonRetryableError interface — any typed error that declares IsNonRetryable() bool.
//
// policy.NonRetryableErrors is intentionally NOT checked here. That list contains
// Temporal error type name strings consumed by Temporal's RetryPolicy.NonRetryableErrorTypes.
// In the LocalExecutor path all non-retryable conditions are expressed as typed Go errors
// implementing NonRetryableError, so string-matching is both unnecessary and fragile
// (e.g. a generic "invalid config: ..." message would false-positive on "InvalidConfig").
func isNonRetryable(err error, _ ExecutionPolicy) bool {
	if err == nil {
		return false
	}
	// Context cancellation and deadline: never retry — stop immediately.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Typed NonRetryableError interface — catches *ErrContractViolation,
	// *ErrIdempotencyKeyMissing, and any future error type that opts in.
	var nre NonRetryableError
	if errors.As(err, &nre) && nre.IsNonRetryable() {
		return true
	}
	return false
}

// execNode runs one plan node with full retry semantics.
//
// Thin wrapper around ExecNodeWithPolicy that passes the shared sharedResult.
func (e *LocalExecutor) execNode(
	ctx context.Context,
	ic *InvocationContext,
	interp *Interpreter,
	step *StepSpec,
	policy ExecutionPolicy,
	vars PipelineVars,
	res *sharedResult,
	sem chan struct{},
) (nextOverride string, err error) {
	return ExecNodeWithPolicy(ctx, ic, interp, step, policy, vars, res, sem)
}

// ExecNodeWithPolicy runs one step with full retry/timeout/backoff semantics.
//
// Exported so execLoop (nodes.go) can apply per-body-step policy in the LocalExecutor
// path without duplicating the retry machinery.
//
// interp is treated as a template: each attempt gets a fresh interp.clone() so that
// no mutable interpreter state (nextStepOverride or future fields) leaks between attempts.
// This mirrors the Temporal path where each activity invocation gets its own isolated clone.
//
// Retry semantics (matching Temporal's RetryPolicy):
//   - Up to policy.MaxAttempts total attempts (0 treated as 1 for backward compat).
//   - Non-retryable errors (NonRetryableError interface, context errors) stop immediately.
//   - Between attempts: exponential backoff with InitialIntervalSeconds, BackoffCoefficient,
//     MaxIntervalSeconds. Zero values use safe defaults (1s initial, 2.0 coeff, 30s max).
//   - policy.TimeoutSeconds, when non-zero, wraps each individual attempt (StartToCloseTimeout
//     per-attempt semantics, matching Temporal). A fresh deadline is created per attempt.
//   - policy.RequiresIdempotencyKey: when true AND MaxAttempts > 1, the HTTP step config
//     MUST contain a static Idempotency-Key header; if absent → ErrIdempotencyKeyMissing.
//   - vars is deep-copied before each attempt so a failed attempt cannot leak partial writes.
//
// res may be nil (loop body steps do not emit a terminal result).
func ExecNodeWithPolicy(
	ctx context.Context,
	ic *InvocationContext,
	interp *Interpreter,
	step *StepSpec,
	policy ExecutionPolicy,
	vars PipelineVars,
	res *sharedResult,
	sem chan struct{},
) (nextOverride string, err error) {
	maxAttempts := policy.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1 // backward compat: zero means "not set"
	}

	// Idempotency guard: before any retry, ensure the caller has set a static key.
	if policy.RequiresIdempotencyKey && maxAttempts > 1 {
		if step.Type == StepHTTP {
			if !httpConfigHasIdempotencyKey(step.Config) {
				return "", &ErrIdempotencyKeyMissing{StepID: step.ID}
			}
		}
	}

	// Backoff parameters — use safe defaults when unset.
	initialInterval := policy.InitialIntervalSeconds
	if initialInterval <= 0 {
		initialInterval = 1.0
	}
	backoffCoeff := policy.BackoffCoefficient
	if backoffCoeff < 1.0 {
		backoffCoeff = 2.0
	}
	maxInterval := float64(policy.MaxIntervalSeconds)
	if maxInterval <= 0 {
		maxInterval = 30.0
	}

	var lastErr error
	interval := initialInterval

	for attempt := int32(1); attempt <= maxAttempts; attempt++ {
		// Check cancellation before each attempt.
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return "", lastErr
			}
			return "", ctx.Err()
		default:
		}

		// Acquire the per-run concurrency semaphore before executing.
		// Goroutines that exceed the limit block here (not at goroutine launch),
		// so join logic is unaffected — joins wait via js.arrive() which does not
		// hold the semaphore. Release is deferred to after the attempt completes.
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return "", lastErr
			}
			return "", ctx.Err()
		case sem <- struct{}{}:
		}

		// Per-attempt timeout (StartToCloseTimeout semantics): each attempt gets its
		// own fresh deadline. This matches Temporal's behaviour where StartToCloseTimeout
		// is applied to each individual activity attempt, not the whole retry sequence.
		attemptCtx := ctx
		var attemptCancel context.CancelFunc
		if policy.TimeoutSeconds > 0 {
			attemptCtx, attemptCancel = context.WithTimeout(ctx, time.Duration(policy.TimeoutSeconds)*time.Second)
		}

		// Fresh interpreter clone per attempt: each attempt starts with clean mutable
		// state (nextStepOverride and any future Interpreter fields). interp is the
		// template; we never execute on it directly.
		attemptInterp := interp.clone()

		// Deep-copy vars before each attempt so a failed attempt cannot leak partial
		// writes into the next attempt's input.
		attemptVars := deepCopyVars(vars)

		localResult := &ExecutionResult{MediaType: "text/plain"}
		execErr := attemptInterp.executeStep(attemptCtx, ic, step, attemptVars, localResult)

		if attemptCancel != nil {
			attemptCancel()
		}
		<-sem // release semaphore slot

		if execErr == nil {
			override := attemptInterp.nextStepOverride

			// Merge successful attempt's var writes back to the caller's vars map
			// so that downstream steps can see the outputs of this step.
			for k, v := range attemptVars {
				vars[k] = v
			}

			// Promote result if this step produced one (nil res = body step, no terminal output).
			if res != nil && (localResult.Text != "" || step.Type == StepResponse || step.Type == StepStreamOut) {
				res.setIfEmpty(localResult)
			}
			return override, nil
		}

		lastErr = execErr

		// Non-retryable: stop immediately.
		if isNonRetryable(execErr, policy) {
			return "", execErr
		}

		// Last attempt: no sleep needed.
		if attempt >= maxAttempts {
			break
		}

		// Backoff sleep — respect context cancellation.
		sleep := time.Duration(interval * float64(time.Second))
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(sleep):
		}

		// Advance interval for next attempt.
		interval = min(interval*backoffCoeff, maxInterval)
	}

	return "", lastErr
}

// httpConfigHasIdempotencyKey returns true when the HTTP step config contains
// a static "Idempotency-Key" header (case-insensitive key match).
func httpConfigHasIdempotencyKey(cfg json.RawMessage) bool {
	if len(cfg) == 0 {
		return false
	}
	var c struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		return false
	}
	for k := range c.Headers {
		if strings.EqualFold(k, "Idempotency-Key") {
			return true
		}
	}
	return false
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
		SubPlan:  n.SubPlan,
	}
}

// ── joinState ─────────────────────────────────────────────────────────────────

// joinState tracks branch arrivals at join nodes.
// All methods are safe for concurrent use.
type joinState struct {
	mu      sync.Mutex
	arrived map[string]map[string]PipelineVars // joinID → (fromID → vars)
	count   map[string]int                     // joinID → expected total arrivals
}

// arrive deposits vars from predecessor fromID for joinID.
// joinOf is the ordered list of predecessor IDs used for deterministic merge.
// expected is the number of arrivals needed before the join fires.
// Returns (ready=true, merged) when enough branches have arrived.
// Returns (false, nil) if more are pending.
func (js *joinState) arrive(joinID string, joinOf []string, fromID string, expected int, vars PipelineVars) (bool, PipelineVars, error) {
	js.mu.Lock()
	defer js.mu.Unlock()

	if js.arrived[joinID] == nil {
		js.arrived[joinID] = make(map[string]PipelineVars)
	}
	js.arrived[joinID][fromID] = vars
	got := len(js.arrived[joinID])

	if got < expected {
		return false, nil, nil
	}

	// Merge in joinOf order (deterministic: JoinOf defines precedence).
	// For keys present in multiple branches, later entries in JoinOf win.
	// For JoinBranchMerge (expected=1, joinOf may be longer), only the
	// one arrived branch contributes — still deterministic since there's one.
	merged := make(PipelineVars)
	for _, predID := range joinOf {
		if bv, ok := js.arrived[joinID][predID]; ok {
			for k, v := range bv {
				merged[k] = v
			}
		}
	}
	// Handle branches whose predID wasn't in joinOf (shouldn't happen in
	// well-formed plans, but be safe: append them last).
	for predID, bv := range js.arrived[joinID] {
		inJoinOf := false
		for _, jid := range joinOf {
			if jid == predID {
				inJoinOf = true
				break
			}
		}
		if !inJoinOf {
			for k, v := range bv {
				merged[k] = v
			}
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
