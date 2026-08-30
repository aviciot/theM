# Loop Design Comparison — Opaque vs. Durable
# Date: 2026-08-30

## What we are choosing between

Two architectures for StepLoop execution. Both fix the four Phase 5-A audit defects. They
differ entirely in where loop iteration lives in Temporal history and how body nodes are
scheduled.

---

## Option A — Minimal Opaque Loop

The loop node remains a single Temporal Activity. Body nodes run inline, inside that one
activity call, on the Temporal worker's goroutine.

### How it works

1. The canvas gets two output ports: `loop-body` (body chain) and `loop-done` (post-loop
   step). The frontend serializer derives `body_steps` by walking from the `loop-body`
   handle.
2. The Go compiler reads `body_steps` from config as today. It also validates that the
   set of steps reachable from `loop-body` in the full graph equals `body_steps`, so the
   frontend is not the sole source of truth.
3. `execLoop` calls `LocalExecutor.execNodeWithPolicy` (extracted from `execNode`) for
   each body step, giving per-body-step retry and timeout within the single activity.
4. `CanvasAgentWorkflow` schedules the loop node as one `ExecuteStepActivity`. It retries
   the whole loop as a unit if the activity fails.
5. `accum_var` is fixed to snapshot only declared `node.Outputs` keys, not all pipeline
   vars.

### Temporal history shape

```
WorkflowStarted
  ActivityScheduled  loop_step_1   ← one entry for the whole loop
  ActivityStarted    loop_step_1
    (body: llm_step × N  — no Temporal history; run inside the activity)
  ActivityCompleted  loop_step_1   → {accum_var: [...]}
  ActivityScheduled  post_loop_step
  ...
```

### What this does NOT fix

- Body nodes have zero individual Temporal history entries. No per-iteration replay.
- A crash inside iteration 47 of 100 restarts from iteration 0 (the whole activity retries).
- `TimeoutSeconds` on a body step is enforced per-attempt by `execNodeWithPolicy`, but the
  outer `ExecuteStepActivity` `StartToCloseTimeout` still governs the loop as a whole.
- Branch inside a body chain works; Parallel fan-out still does not (no workflow
  goroutines available inside an activity).
- The Temporal UI cannot show which body step failed or how many iterations completed.

### Scope of change

| File | Change |
|---|---|
| `go/internal/agentgen/nodes.go` | Fix accum_var, add empty-body validation |
| `go/internal/agentgen/local_executor.go` | Extract `execNodeWithPolicy` as package-visible |
| `go/internal/agentgen/plan_compiler.go` | Add body-steps graph-validation |
| `frontend/.../useDefinitionLifecycle.ts` | Derive body_steps from loop-body edge |
| `frontend/.../nodeRegistry.ts` | Add loop-body / loop-done output handles |
| `go/internal/agentgen/nodes.go` (Validate) | Error on empty body_steps |
| Tests | Update EP-LOOP, CT-LOOP; add body-validation test |

---

## Option B — Durable Loop (recommended)

The loop node in `CanvasAgentWorkflow` does **not** call `ExecuteStepActivity` for itself.
Instead, the workflow directly iterates and schedules each body node as its own independent
`ExecuteStepActivity` — one activity per body step per iteration.

### How it works

1. **Canvas + compiler** (identical to Option A):
   - Two output ports: `loop-body` and `loop-done`.
   - Frontend serializer derives `body_steps` from `loop-body` edge walk.
   - Compiler validates body steps against graph, builds `SubPlan` as before.
   - `PlanNode.Type == StepLoop` is a signal to the workflow layer; the node itself
     never reaches `ExecuteStepActivity`.

2. **`CanvasAgentWorkflow` / `runBranch`**: When the current node is `StepLoop`, instead
   of calling `ExecuteStepActivity`, the workflow runs a dedicated `runLoopNode` function.
   `runLoopNode`:
   - Reads `items_var` from `localVars`.
   - Validates it is `[]any`; validates `len ≤ cfg.MaxIterations`.
   - For each item (up to cap):
     - Injects `cfg.ItemVar = item` into a fresh iteration-scoped var copy.
     - Calls `runBranch` on `SubPlan.StartID` using the iteration-scoped vars, with
       the same `launchBranch` / `cancelAll` / `errCh` plumbing. This schedules every
       body step through `ExecuteStepActivity` with its own per-node activity options.
     - Awaits the iteration's `doneCh` drain before starting the next iteration
       (sequential: one item at a time).
     - After the iteration completes, collects only declared `Outputs` keys from the
       body's final vars into one `accum_var` entry.
   - Writes `accum_var` to `localVars` and `state.vars`.
   - Continues `runBranch` to the post-loop step via the loop node's `Next[0]` (the
     `loop-done` target).

3. **`execLoop` (nodes.go)**: Becomes unreachable for the Temporal path. Retained for the
   `LocalExecutor` path (non-Temporal agents), but refactored to call
   `LocalExecutor.execNodeWithPolicy` per body step (same as Option A fix).

4. **`ExecuteStepActivity`**: No change needed. A `StepLoop` node is never passed to it in
   the Temporal path.

5. **Iteration history cap**: `CanvasAgentWorkflow` enforces `cfg.MaxIterations` (default 100)
   and also a compile-time constant `MaxLoopBodyHistoryEvents = 5000` (body-steps ×
   iterations). If the resolved cap would exceed this, compilation fails with a validation
   error.

### Temporal history shape

```
WorkflowStarted
  ActivityScheduled  pre_loop_step
  ActivityCompleted  pre_loop_step  → {items: ["a","b","c"]}

  // Iteration 0 — item "a"
  ActivityScheduled  body_llm_step  (node.Policy from SubPlan.Nodes[0])
  ActivityStarted    body_llm_step
  ActivityCompleted  body_llm_step  → {processed_item: "done:a"}

  // Iteration 1 — item "b"
  ActivityScheduled  body_llm_step
  ActivityCompleted  body_llm_step  → {processed_item: "done:b"}

  // Iteration 2 — item "c"
  ActivityScheduled  body_llm_step
  ActivityCompleted  body_llm_step  → {processed_item: "done:c"}

  // Loop exits; loop node writes accum_var to state
  ActivityScheduled  post_loop_step
  ...
```

Every body step execution is a first-class Temporal history event with its own:
- Retry policy (from `SubPlan.Nodes[i].Policy`)
- `StartToCloseTimeout`
- Replay-safe deterministic scheduling
- Visibility in the Temporal UI

### What this fixes vs. Option A

| Property | Option A (Opaque) | Option B (Durable) |
|---|---|---|
| body_steps derived from canvas edges | ✅ | ✅ |
| Compiler validates body graph | ✅ | ✅ |
| Body step retry/timeout enforced | ✅ (in-process) | ✅ (Temporal activity) |
| Per-body-step Temporal history | ❌ | ✅ |
| Crash mid-iteration resumes from that iteration | ❌ | ✅ |
| Branch inside loop body works | ✅ | ✅ |
| Parallel fan-out inside loop body works | ❌ | ✅ (via runBranch) |
| Join inside loop body works | ❌ | ✅ (via handleJoin) |
| accum_var scoped to declared outputs | ✅ | ✅ |
| Empty body validation | ✅ | ✅ |
| MaxIterations + history budget cap | config only | compile-time + runtime |
| Architecture consistency (all nodes = activities) | ❌ | ✅ |

### Scope of change

| File | Change |
|---|---|
| `go/internal/agentgen/nodes.go` | Fix accum_var, add empty-body validation, add LocalExecutor body policy enforcement |
| `go/internal/agentgen/local_executor.go` | Extract `execNodeWithPolicy` as package-visible |
| `go/internal/agentgen/plan_compiler.go` | Add body-steps graph-validation, add history-budget compile check |
| `go/internal/temporal/canvas_workflow.go` | Add `runLoopNode`; add StepLoop branch in `runBranch` before activity dispatch |
| `go/internal/temporal/canvas_activities.go` | No change |
| `frontend/.../useDefinitionLifecycle.ts` | Derive body_steps from loop-body edge walk |
| `frontend/.../nodeRegistry.ts` | Add loop-body / loop-done output handles |
| `go/internal/agentgen/nodes.go` (Validate) | Error on empty body_steps |
| `go/internal/temporal/canvas_workflow_test.go` | Add CT-LOOP-DURABLE-1..N tests |
| `go/internal/agentgen/executor_test.go` | Update EP-LOOP tests for policy enforcement |
| `go/TEST_INDEX.md` | Add new tests |

---

## Recommendation: Option B — Durable Loop

### Rationale

The project's stated architecture principle (CLAUDE.md, canvas_activities.go comment,
canvas_workflow.go comment) is: **every executable node receives its own Temporal
Activity, policy, retry, and history entry.** StepLoop is an executable node. Having it
be the single exception undermines this principle and creates a two-tier execution model
(outer nodes = durable; body nodes = ephemeral).

Option B is not materially harder than Option A:
- `runLoopNode` in `canvas_workflow.go` is ~80 lines, leveraging the existing
  `runBranch` / `handleJoin` machinery already written and tested.
- The body sub-plan is already compiled and stored on `PlanNode.SubPlan`. The workflow
  just needs to iterate items and call `runBranch(subPlan.StartID, ...)`.
- No new data structures are needed. `LoopConfig` and `PlanNode` are unchanged.

Option A trades correctness for a smaller diff. In a Temporal-native platform it is the
wrong trade. A crash mid-loop in production with 100 items means restarting from item 0,
re-processing 47 already-processed items — a real cost for LLM or mutation steps.

Option B is the only design consistent with the rest of the platform.

### One constraint to call out

Option B requires that `runLoopNode` runs loop iterations **sequentially** by default.
Parallel iteration (process all items concurrently) requires N simultaneous `runBranch`
goroutines, which multiplies history events by N. This should be a future `parallel: true`
flag on `LoopConfig`, not default. The initial implementation is sequential-only.

---

## Exact files and tests

### Go changes

**`go/internal/agentgen/nodes.go`**
- `execLoop`: replace full-vars accum with declared-outputs-only snapshot
- `execLoop`: call `execNodeWithPolicy` per body step (LocalExecutor path only)
- `StepLoop.Validate`: return error when `len(cfg.BodySteps) == 0`

**`go/internal/agentgen/local_executor.go`**
- Extract retry/timeout/backoff loop from `execNode` into
  `execNodeWithPolicy(ctx, ic, interp, step, policy, vars, sem) (override string, err error)`
- `execNode` becomes a thin wrapper calling `execNodeWithPolicy`
- `execNodeWithPolicy` exported (upper-case) so `nodes.go` can call it

**`go/internal/agentgen/plan_compiler.go`**
- After building `SubPlan`, validate that all `BodySteps` IDs exist in `stepByID`
- Validate that the `loop-done` output port target is not itself a body step
- Add `MaxLoopHistoryBudget` const = 5000; refuse compilation when
  `len(SubPlan.Nodes) × cfg.MaxIterations > MaxLoopHistoryBudget` with a descriptive error

**`go/internal/temporal/canvas_workflow.go`**
- `runBranch`: before the `ExecuteActivity` call, add:
  ```go
  if node.Type == agentgen.StepLoop {
      runLoopNode(ctx, state, node, localVars, launchBranch, cancelAll, errCh)
      // advance currentID to node.Next[0] (loop-done target) and continue
  }
  ```
- Add `runLoopNode(ctx, state, node, localVars, launchBranch, cancelAll, errCh)`:
  - Reads `LoopConfig` from `node.Config`
  - Reads `items []any` from `localVars[cfg.ItemsVar]`
  - Iterates sequentially: per item, creates iteration vars, calls internal
    `runSubPlan(ctx, state, node.SubPlan, iterVars, launchBranch, cancelAll, errCh)`
  - `runSubPlan` is a thin wrapper: sets up a local `pending` counter, calls
    `launchBranch(subPlan.StartID, ...)` with a sub-done channel, drains it
  - After each iteration, builds `accum_var` entry from declared body `Outputs`
  - Writes `accum_var` to `localVars` and `state.vars`

**`go/internal/temporal/canvas_activities.go`** — no change

### Frontend changes

**`frontend/src/lib/nodeRegistry.ts`** (or equivalent handle config)
- Add two named output handles to the loop node definition:
  - `loop-body` — routes to the first body step
  - `loop-done` — routes to the post-loop step
- Remove the single unnamed output handle from the loop node

**`frontend/src/app/admin/agents/builder/hooks/useDefinitionLifecycle.ts`**
- In `buildDefinitionDoc()`, when serializing a `loop` step:
  - Walk the `loop-body` outgoing edge to collect all reachable step IDs
    (BFS until a step has a `loop-done` edge or no outgoing edge)
  - Inject `body_steps: [...]` into `config` before writing the step
  - Set the loop step's `next` to the target of the `loop-done` outgoing edge

**`frontend/src/app/admin/agents/builder/components/properties/StepConfigSection.tsx`**
- Remove `body_steps` from any visible config field (it is computed, not user-typed)
- Keep `items_var`, `item_var`, `accum_var`, `max_iterations`, `condition`
- Add a read-only "Body steps" display that shows the derived body step count

### New tests

**`go/internal/temporal/canvas_workflow_test.go`**
- `TestCanvasAgentWorkflow_Loop_DurableIteration` — 3-item list, body has one LLM step;
  assert activity called 3 times, `accum_var` has 3 entries, each with only declared output key
- `TestCanvasAgentWorkflow_Loop_BodyStepRetry` — body step fails on attempt 1, succeeds
  on attempt 2 (MaxAttempts=2); assert final result correct, Temporal retry triggered
- `TestCanvasAgentWorkflow_Loop_EmptyList` — items_var is empty list; assert no body
  activity scheduled, post-loop step runs
- `TestCanvasAgentWorkflow_Loop_MaxIterationsCap` — list has 200 items; assert only 100
  processed (default cap)
- `TestCanvasAgentWorkflow_Loop_BodyBranchInside` — body has a branch node; assert
  correct arm taken per item

**`go/internal/agentgen/executor_test.go`** (LocalExecutor path)
- Update `EP-LOOP-1` to verify `accum_var` entries contain only declared Outputs keys
- Add `EP-LOOP-POLICY-1` — body step has `MaxAttempts=2`; step fails once then succeeds;
  assert result correct and call count = 2

**`go/internal/agentgen/plan_compiler_test.go`**
- Add `PC-LOOP-BUDGET-1` — body_steps × max_iterations > 5000; assert compile error
- Add `PC-LOOP-UNKNOWN-BODY-1` — body_steps contains unknown step ID; assert compile error

---

## What does NOT change

- `LoopConfig` struct fields — unchanged
- `PlanNode.SubPlan` — still holds the compiled body; workflow reads it directly
- `ExecuteStepActivity` — unchanged; StepLoop nodes never reach it in the Temporal path
- `compileLoopBodyPlan` logic — minor addition of validation only, no structural change
- The `LocalExecutor` path for non-Temporal agents — still runs `execLoop`; gets
  `execNodeWithPolicy` per body step but otherwise unchanged
