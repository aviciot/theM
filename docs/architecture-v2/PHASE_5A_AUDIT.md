# Phase 5-A Audit — StepLoop Architecture Review
# Date: 2026-08-30

## Summary

Phase 5-A (StepLoop) is **functionally broken** for canvas agent use. Four critical defects
were found. Unit tests pass because they construct `PlanNode.SubPlan` and `LoopConfig.BodySteps`
manually; those fields are never populated via the real canvas authoring and publish pipeline.

---

## Finding 1 — CRITICAL: body_steps is never written by the frontend

### What the code expects

`LoopConfig.body_steps []string` is the list of step IDs that form the loop body. The Go
compiler reads it from the loop node's `config` JSON (via `json.Unmarshal(s.Config, &lc)`
in `plan_compiler.go:173–176`), then uses it to:
- Exclude body steps from the outer plan
- Compile them into `PlanNode.SubPlan`
- Remap loop.Next to the first post-body step

### What the frontend actually does

`useDefinitionLifecycle.ts` serializes canvas steps at lines 124–151 and 300–330. For each
step node it emits:
```typescript
{
  id: stepd.step_id,
  type: stepd.step_type,
  config: stepd.config ?? {},   // ← raw user-edited config object from StepData
  next,                          // ← control edges from this step
  ...
}
```

`StepData.config` is exactly the object the user typed into `StepConfigSection.tsx`. The
loop config panel exposes `items_var`, `item_var`, `accum_var`, `max_iterations`,
`condition` — but **not `body_steps`**. There is no UI control for it. There is no
serializer that derives it from canvas edges. The field is absent in `api.ts`
`AgentStepDoc.config`. The string `body_steps` does not appear anywhere in the frontend
source tree.

### Impact

A published loop agent has `body_steps: []`. The compiler sees an empty body list:
- `compileLoopBodyPlan` returns `&ExecutionPlan{SkillID: ..., Nodes: nil}` — an empty sub-plan
- `execLoop` returns immediately (line 857–860: `if step.SubPlan == nil || len(step.SubPlan.Nodes) == 0 { return nil }`)
- `resolveLoopOuterNext` with empty BodySteps falls back to `loopStep.Next` unchanged

**Result: the loop node is a silent no-op. No body steps run. No error is raised.**

### Root cause

The design conflates a topology concept (which nodes are the loop body, derived from graph
edges) with a config field (a user-typed list of step IDs). Canvas tools typically derive
this from connectivity, not a manual list.

---

## Finding 2 — CRITICAL: body steps bypass per-node policy (retry, timeout)

### What execLoop does

`execLoop` (nodes.go:888, 928–932) runs each body step by calling:
```go
bodySpec := planNodeToStepSpec(node)
bodyInterp.nextStepOverride = ""
if err := bodyInterp.executeStep(ctx, ic, bodySpec, iterVars, iterResult); err != nil {
    return fmt.Errorf(...)
}
```

`executeStep` executes the node type's `Execute` function once. It applies Stage-6 input
scoping, but it has **no retry loop, no per-attempt timeout, and no backoff**.

### What LocalExecutor does for outer nodes

`LocalExecutor` runs each outer `PlanNode` through `execNode` (local_executor.go:300–420):
- Reads `policy.MaxAttempts`, `policy.TimeoutSeconds`, `policy.InitialIntervalSeconds`
- Wraps each attempt in `context.WithTimeout(ctx, policy.TimeoutSeconds)`
- Retries up to `MaxAttempts` with exponential backoff
- Short-circuits on non-retryable errors
- Acquires/releases the per-run concurrency semaphore

`compileLoopBodyPlan` does compute `policy = resolvePolicy(nd, s.Config, s.PolicyOverride)`
for each body step — the policy is on the `PlanNode` — but `execLoop` ignores it entirely.

### Impact

- An LLM body step with `max_attempts: 2` gets exactly 1 attempt
- A `timeout_seconds: 30` body step may run for 300 s (the outer plan policy)
- An HTTP body step with `requires_idempotency_key` is not checked
- A transient LLM error inside the body immediately fails the entire loop, even if the
  step would have succeeded on retry

### What the Temporal path does

In the Temporal path (`ExecuteStepActivity`), the loop node falls through to
`ExecuteNodeForActivity` → `executeStep` → `execLoop` — the same codepath. Body steps
run in the same activity as the outer loop node. Temporal retry policy applies to the
**loop activity as a whole** (the outer `PlanNode.Policy`), not to individual body steps.
Body steps get zero individual Temporal history entries, zero individual retries, and
cannot be inspected or replayed in the Temporal UI.

---

## Finding 3 — MODERATE: Branch/Parallel/Join inside a loop body are unsupported

### What execLoop does

The body walk (nodes.go:916–942) is a simple sequential `for currentID != "" { ... }` loop.
It follows `node.Next[0]` or `bodyInterp.nextStepOverride` — exactly what the sequential
interpreter does. It does **not** use `LocalExecutor` and has no fan-out semantics.

### Implications

- A `branch` step inside a loop body: the branch node's Execute sets `nextStepOverride` to
  either the true or false successor — this works correctly for the sequential walker.
  **Branch is supported**, but only when both arms converge back to a step also in the
  body's sequential chain. If branch arms have divergent lengths or if a join step is
  needed, the body walker will silently lose one arm or hang.
- A `parallel` step inside a loop body: `execParallel` sets `nextStepOverride` to the
  parallel merge step after launching goroutines on the **outer executor** (which doesn't
  exist in the body context). **Parallel is broken** — it requires `LocalExecutor`-level
  goroutine fan-out.
- A `join` step inside a loop body: `JoinMode` from `compileLoopBodyPlan` is always
  `JoinNone` (line 326). A join node that is meant to be `JoinWaitAll` will execute as an
  ordinary step. **Join semantics are broken** in body steps.

### What the compiler does

`compileLoopBodyPlan` hardcodes `JoinMode: JoinNone` for all body nodes (line 326). Body
nodes are not put through the `classifyJoin` step that outer nodes receive. So even if
the body were run by `LocalExecutor`, join classification would be wrong.

---

## Finding 4 — MODERATE: accum_var stores all pipeline vars, not just body outputs

### What execLoop does (lines 949–956)

```go
if cfg.AccumVar != "" {
    snapshot := make(PipelineVars, len(iterVars))
    for k, v := range iterVars {
        snapshot[k] = v
    }
    accumulated = append(accumulated, snapshot)
}
```

`iterVars` at this point is the full deep-copy of the global pipeline vars with the item
injected, then merged with all of the body step writes. It includes:
- `input` (the original user text)
- `item` (or cfg.ItemVar) — the current list item
- `items_var` (the original list — the entire input array)
- Every var written by any upstream outer step
- Every var written by any body step in this iteration

A 100-item list with 10 outer pipeline vars will accumulate 100 snapshots each containing
all those vars. This is O(N × |pipeline_vars|) memory and Temporal history size.

### Expected semantics

`accum_var` should contain **only the variables written by body steps**, so the caller can
iterate `accum_var` and get one entry per processed item. The canonical shape is:
```
accum_var = [ {output_of_body_step: "..."}, {output_of_body_step: "..."}, ... ]
```
Not a snapshot of the entire pipeline state at each iteration.

---

## What the tests cover vs. what is broken

| Scenario | Tests | Status |
|---|---|---|
| Manual SubPlan + BodySteps construction | EP-LOOP-1..5, CT-LOOP-1..3 | ✅ Passes (tests bypass the broken path) |
| Canvas publish → body_steps populated | None | ❌ No test; field is never set |
| Body step policy enforcement | None | ❌ No test; policy ignored |
| Branch inside body | None | ❌ Works only trivially |
| Parallel inside body | None | ❌ Broken, no test |
| accum_var content | Asserts length=3 | ⚠️ Length correct, content not verified |

---

## Proposed corrected architecture

### Fix 1 — Derive body_steps from canvas edges, not user config

**Problem:** `body_steps` is a topology concept. It must be computed from the canvas graph.

**Proposed approach:** A loop node's body is defined as the set of steps reachable from the
loop's output control edge that do NOT route to a step outside the loop. The canvas
serializer should derive `body_steps` by walking the graph from the loop node's output
handle and collecting all reachable step IDs until a step connects back to a non-body node.

**Concretely:**
1. Remove `body_steps` from `LoopConfig` as a user-editable field. Keep it as a computed,
   serialized field (`json:"body_steps"`) that the frontend writes during serialization.
2. In `useDefinitionLifecycle.ts`, when serializing a `loop` step, walk the outgoing
   control edges from the loop node to identify body steps, then inject `body_steps` into
   the config before writing the definition.
3. The loop node gets **two output ports**: `loop-body` (connects to the first body step)
   and `loop-done` (connects to the next outer step after the loop). The serializer uses
   these two ports to determine which steps are body and which is the post-loop successor.
4. The Go compiler continues to read `body_steps` from config as today.

### Fix 2 — Run body steps through LocalExecutor (not raw executeStep)

**Problem:** `execLoop` bypasses policy, retry, timeout, and fan-out semantics.

**Proposed approach:** Run each body iteration by calling `LocalExecutor.Execute` on the
`SubPlan` with `iterVars` as the initial vars. But this has the constraint that
`LocalExecutor.Execute` requires a terminal node (StepResponse or StepStreamOut) — which
body sub-plans don't have.

**Two viable options:**

**Option A — Keep inline body execution but add policy enforcement**
In `execLoop`, replace the raw `bodyInterp.executeStep` call with `execNode` (extracted
from LocalExecutor and made package-visible as `ExecNodeWithPolicy`), passing the body
node's `Policy`. This gives retry, timeout, and idempotency check without requiring a
terminal node. Branch/Parallel/Join remain unsupported (document this as a constraint).

**Option B — Use a modified LocalExecutor for body execution**
Create `LocalExecutor.ExecuteBody(ctx, ic, subPlan, vars)` that does NOT require a
terminal node. It runs the body plan through the normal fan-out engine, returns output
vars, and supports Branch/Parallel/Join. This is significantly more complex but correct
for all node types.

**Recommendation: Option A for now.** Option B requires non-trivial changes to
LocalExecutor and a new "body plan" execution mode. Option A closes the retry/timeout gap
and keeps complexity bounded. Document the Branch/Parallel/Join limitation explicitly.

### Fix 3 — Constrain accum_var to declared body outputs only

**Problem:** accum_var snapshots all pipeline vars per iteration.

**Proposed approach:** After each iteration, collect only the keys declared in `Outputs`
for the body steps — specifically the union of `node.Outputs[*].Name` across all body
`PlanNode`s. If a body node has no declared outputs, fall back to all vars written during
that iteration (by diffing against the pre-iteration state).

**Concretely:** In `execLoop`, after running the body, compute:
```go
snapshot := make(PipelineVars)
for _, node := range step.SubPlan.Nodes {
    for _, ref := range node.Outputs {
        if v, ok := iterVars[ref.Name]; ok {
            snapshot[ref.Name] = v
        }
    }
}
accumulated = append(accumulated, snapshot)
```
Fall back to the full `iterVars` only when no body node declares Outputs.

### Fix 4 — Add a loop-specific validation issue for empty body_steps

In the `Validate` function for `StepLoop`, add an error-severity issue when
`len(cfg.BodySteps) == 0` (after the existing `items_var` check). This ensures the
builder shows an actionable error before publish. The current "not yet supported" UX hint
says "connect steps from this node's output port" — this needs to become a validation
error.

---

## What to fix before next commit

| Priority | Fix | Scope |
|---|---|---|
| P0 | Derive body_steps from canvas control edges in the frontend serializer | `useDefinitionLifecycle.ts` |
| P0 | Add loop-body output port (`loop-body`) and post-loop port (`loop-done`) to loop node | `nodes.go`, `nodeRegistry.ts`, `StepNode.tsx` |
| P1 | Apply policy (retry/timeout) to body steps via ExecNodeWithPolicy | `nodes.go` (execLoop) |
| P1 | Constrain accum_var to declared body outputs | `nodes.go` (execLoop) |
| P2 | Add empty body_steps validation error | `nodes.go` (Validate) |
| P2 | Document Branch/Parallel/Join unsupported inside loop body | code comment + LESSONS.md |

---

## Files to change

| File | Change |
|---|---|
| `go/internal/agentgen/nodes.go` | execLoop: add policy enforcement, fix accum_var scoping, add validation for empty body_steps |
| `go/internal/agentgen/spec.go` | LoopConfig: no field changes; document body_steps as computed-by-serializer |
| `go/internal/agentgen/local_executor.go` | Extract execNodeWithPolicy as package-visible helper (or keep in nodes.go) |
| `frontend/src/app/admin/agents/builder/hooks/useDefinitionLifecycle.ts` | Derive body_steps from loop node's outgoing control edges when serializing |
| `frontend/src/lib/nodeRegistry.ts` | Add loop summary function; add two named output ports for loop node |
| `go/internal/agentgen/compiler.go` | No change needed; reads body_steps from config.body_steps as raw JSON |
