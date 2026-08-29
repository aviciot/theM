# DAG Execution Plan — Final Implementation Design

**Status:** Design doc — not yet implemented.
**Last updated:** 2026-08-29

---

## 1. Current State

**Interpreter (`go/internal/agentgen/interpreter.go`):** Sequential loop. Follows `step.Next[0]`
only. `Next[1:]` is silently ignored. `nextStepOverride` is a mutable field on `Interpreter` — not
safe for concurrent use. No join concept exists.

**`StepParallel` node (`go/internal/agentgen/nodes.go:503`):** Registered with `Execute: nil` —
stub, not executable. `ParallelConfig { Branches [][]string; MergeVar string }` exists in spec.go
and is correct for fan-out.

**Compiler (`go/internal/agentgen/compiler.go`):** Emits `SkillSpec.Steps` with `Next []string`
already populated. Validates `MaxIn`/`MaxOut` per `EdgeRules`. Most node types have `MaxOut: 1` —
multi-successor edges are rejected at compile time.

**Temporal (`go/internal/temporal/workflow.go`):** `OrchestrationWorkflow` executes a single
`RunOrchestratorActivity` which runs the entire sequential interpreter. HITL is at workflow level
via signal. There is no per-step activity — the plan is opaque to Temporal.

**Canvas frontend:** `isPipeConnectionValid` reads `MaxOut` from server-supplied `EdgeRules`. If
server says `MaxOut: 1`, canvas blocks the second outgoing edge. Canvas does not need frontend-side
changes — it is fully data-driven from node registry.

**What's missing:**
- `ExecutionPlan` type with join annotations
- `LocalExecutor` with goroutine fan-out and join
- `TemporalExecutor` mapping per-step to Temporal activities
- `ExecutionBackend` selection mechanism
- Deep-copy of `PipelineVars` at fan-out points
- `StepParallel` Execute implementation

---

## 2. Target Architecture

```
Canvas JSON
    ↓ compiler (no change to canvas JSON)
AgentSpec / SkillSpec
    ↓ plan compiler (new pass)
ExecutionPlan (PlanNode[], join annotations)
    ↓
ExecutionBackend (interface)
    ├─ LocalExecutor    — goroutines, in-process, fast
    └─ TemporalExecutor — one activity per step, durable
```

The same `ExecutionPlan` runs on either backend. Backend selection is per-agent-definition row
(`execution_backend: "local" | "temporal"`). Default: `"local"`. Temporal is opt-in per skill.

---

## 3. ExecutionPlan Schema

```go
// go/internal/agentgen/spec.go — add these types

type JoinMode string
const (
    JoinNone    JoinMode = "none"      // no join — standard node
    JoinWaitAll JoinMode = "wait_all"  // block until all predecessors deliver vars
    JoinWaitAny JoinMode = "wait_any"  // first predecessor wins (reserved, not impl Phase 1)
)

// ExecutionPlan is compiled from SkillSpec once, stored alongside AgentSpec.
// Serialisable to JSON; loaded at runtime before first invocation.
type ExecutionPlan struct {
    SkillID string     `json:"skill_id"`
    StartID string     `json:"start_id"`  // ID of the entry step (type: input)
    Nodes   []PlanNode `json:"nodes"`
}

type PlanNode struct {
    StepID   string          `json:"step_id"`
    Type     StepType        `json:"type"`
    Next     []string        `json:"next"`             // len>1 = fan-out
    JoinOf   []string        `json:"join_of"`          // predecessor IDs that must finish
    JoinMode JoinMode        `json:"join_mode"`
    Branches []BranchArm     `json:"branches,omitempty"`
    Inputs   []VarRef        `json:"inputs,omitempty"`
    Outputs  []VarRef        `json:"outputs,omitempty"`
    Config   json.RawMessage `json:"config"`
}
```

**Compiler rule:** A node with `inDegree > 1` gets `JoinMode: wait_all` and `JoinOf` set to the
complete list of immediate predecessors. Linear nodes: `JoinOf: nil`, `JoinMode: none`.

---

## 4. Control Flow Semantics

### Fan-out
A node with `Next: ["A", "B"]` launches A and B concurrently. At the fork point, the executor
**deep-copies** `PipelineVars` for each branch. Branches share no mutable state.

### Fan-in / Join (`wait_all`)
A join node has `JoinOf: ["A", "B"]` and `JoinMode: wait_all`. The executor:
1. Allocates a counter `remaining = len(JoinOf)`.
2. Each arriving branch atomically decrements `remaining` and deposits its output `PipelineVars`.
3. The last branch to arrive merges all deposited vars (last-write-wins per key) and continues
   execution past the join node.
4. **Failure behavior:** if any branch returns an error, the join immediately cancels all other
   in-flight branches (via context cancellation) and returns the error. No partial merge.
5. **Future `wait_any`:** reserved in `JoinMode` enum; not implemented in Phase 1.

### Branch
`StepBranch` evaluates its Go-template expression and returns `NextOverride` (one of two step IDs).
In the plan, `Next: [true_next, false_next]`; executor activates only the returned arm. The pruned
arm is never launched — no goroutine, no activity.

### Parallel node
`StepParallel` with `ParallelConfig.Branches [][]string`: each inner slice is a chain. Compiler
emits one fan-out from the parallel node and detects the convergence point as a join node. The step
immediately after all chains is tagged `JoinMode: wait_all`. Results are merged into `MergeVar` as
a JSON array `[result_A, result_B, ...]`.

### Loop
`StepLoop` is opaque: the executor runs `BodySteps` sequentially in a sub-interpreter loop until
`Condition` is false or `MaxIterations` is reached. No structural fan-out inside a loop in Phase 1.
`AccumVar` is appended each iteration.

### HumanWait
Local: blocks on a `chan domain.Message` injected into `InvocationContext.HumanInputCh`. Not
production-grade — prototype only for local mode. Temporal: `workflow.GetSignalChannel(ctx,
SignalHumanInput).Receive(...)` already in `workflow.go`; moved to `HumanWaitActivity` level in
Phase 4.

### A2A and MCP calls
Both execute as normal single-step activities/goroutines. `A2ACallStepConfig.TimeoutSeconds` and
HTTP client timeout govern cancellation. Fan-in after A2A is handled identically to any other join.
No structural changes needed to their config types.

---

## 5. ExecutionBackend Interface

```go
// go/internal/agentgen/executor.go (new file)

type ExecutionBackend interface {
    // Execute runs the plan from StartID. initialVars is the seed PipelineVars.
    // Implementations must be safe for concurrent calls with distinct plans.
    Execute(
        ctx     context.Context,
        ic      *InvocationContext,
        plan    *ExecutionPlan,
        initial PipelineVars,
    ) (*ExecutionResult, error)
}
```

---

## 6. LocalExecutor Design

```go
// go/internal/agentgen/local_executor.go (new file)

type LocalExecutor struct{ interp *Interpreter }

type execState struct {
    plan    *ExecutionPlan
    ic      *InvocationContext
    interp  *Interpreter
    result  *ExecutionResult
    resMu   sync.Mutex

    // join tracking
    joinMu      sync.Mutex
    joinArrived map[string][]PipelineVars // joinID → vars from each arriving branch
    joinCount   map[string]int            // joinID → total expected arrivals
}
```

- **Fan-out:** when `len(node.Next) > 1`, first successor runs inline; remaining launch via
  `go state.run(ctx, ic, branchID, deepCopyVars(vars))`.
- **Join:** on arrival at a join node, `joinArrived[id] = append(...)`. When
  `len(joinArrived[id]) == joinCount[id]`, merge all var maps (deterministic order = `JoinOf`
  slice order) and continue past the join.
- **Error propagation:** cancel context on first error; goroutines check `ctx.Done()`.
- **`deepCopyVars`:** recursive copy for `map[string]any` and `[]any` values. Scalar types
  (string, int, float64, bool) are value-copied automatically.
- **`ExecutionResult`:** set by first `response`/`stream_out` step; subsequent calls are no-ops
  (mutex-protected).

---

## 7. TemporalExecutor Design

### Mapping

| Plan concept | Temporal primitive |
|---|---|
| Linear step | `workflow.ExecuteActivity(ctx, ExecuteStepActivityName, input).Get(ctx, &out)` |
| Fan-out (`len(Next) > 1`) | `workflow.Go(ctx, func(gCtx) { future = ExecuteActivity(...) })` |
| Join (`wait_all`) | loop over `[]workflow.Future`, call `.Get(ctx, &out)` on each |
| Branch | activity returns `NextOverride`; workflow routes |
| Loop | `for` loop in workflow code (Temporal-safe determinism) |
| HumanWait | `workflow.GetSignalChannel(ctx, SignalHumanInput).Receive(ctx, &msg)` |
| A2A / MCP | standard activity with per-type timeout |

### Activity definition

```go
// go/internal/temporal/plan_executor.go (new file)

const ExecuteStepActivityName = "ExecuteStepActivity"

type StepActivityInput struct {
    RunID  string          `json:"run_id"`
    StepID string          `json:"step_id"`
    Type   agentgen.StepType `json:"type"`
    Config json.RawMessage `json:"config"`
    Inputs agentgen.PipelineVars `json:"inputs"` // scoped snapshot only
    IC     agentgen.InvocationContext `json:"ic"`
}

type StepActivityOutput struct {
    Outputs      agentgen.PipelineVars `json:"outputs"`
    NextOverride string                `json:"next_override"` // branch only
}
```

### Retry policies per type

| Type | MaxAttempts | Backoff |
|---|---|---|
| LLM, HTTP, MCPCall | 3 | exponential 1s→10s |
| A2ACall | 2 | 5s fixed |
| Input, Transform, Response | 1 | — |
| HumanWait | — | signal-driven |

### Cancellation
`workflow.Context` cancel propagates to all `workflow.Go` goroutines. Activity cancellation
propagates via Go `context.Context` already wired in `internal/llm/anthropic.go`.

### Restart recovery
All `PipelineVars` flow through activity I/O — no in-process state. Temporal replays the workflow
from history; completed activities are not re-executed. Fan-out state is encoded in Temporal
workflow history as completed futures.

---

## 8. Backend Selection

```go
// go/internal/agentgen/invocation.go (extend InvocationContext)

type ExecutionBackendType string
const (
    BackendLocal    ExecutionBackendType = "local"
    BackendTemporal ExecutionBackendType = "temporal"
)

// InvocationContext — add:
Backend ExecutionBackendType
```

Set from `agent_definitions.execution_backend` column (new DB column, default `"local"`). The
`Interpreter.Execute` entry point checks `ic.Backend` and dispatches to the appropriate
`ExecutionBackend` implementation. No other call-site change needed.

---

## 9. Backward Compatibility

- All existing linear skills (`Next` has exactly one element, no join nodes) produce valid
  `ExecutionPlan`s where every node has `JoinMode: none`. The `LocalExecutor` runs them
  identically to the current loop — no behavioral change.
- `AgentSpec` / `SkillSpec` JSON format is unchanged. `ExecutionPlan` is a compiled derivative
  stored separately (e.g. in `agent_runtime_specs.plan JSONB`).
- `StepSpec.Inputs/Outputs` contracts (Stage 6) are preserved; deep-copy at fan-out ensures
  scoped vars are still respected.
- Temporal: `OrchestrationWorkflow` continues to function until Phase 4 replaces it. Both
  `RunOrchestratorActivity` (legacy) and `ExecuteStepActivity` (new) can coexist in the same
  worker binary registered under different names.

---

## 10. Phased Implementation Plan

### Phase 0 — ExecutionPlan compiler (no executor change, no canvas change)
- Add `ExecutionPlan`, `PlanNode`, `JoinMode` types to `spec.go`.
- Add compiler pass: `SkillSpec → ExecutionPlan` with join annotation.
- No `MaxOut` change. No canvas change. Interpreter unchanged.
- Tests: compile linear skill → plan (no joins); compile diamond pattern (two successors + join).
- **Risk:** zero — purely additive types and a new code path not yet called.

### Phase 1 — LocalExecutor with goroutine fan-out and wait_all join
- Implement `deepCopyVars`.
- Implement `LocalExecutor` in `local_executor.go`.
- Implement `StepParallel.Execute` using fan-out.
- Wire `Interpreter.Execute` to use `LocalExecutor` when plan is present.
- Tests: two HTTP nodes in parallel from one LLM source; join merges both outputs.
- Tests: branch prunes one arm; parallel collects both.

### Phase 2 — Integration tests + validation
- End-to-end tests: run a parallel skill locally with mock HTTP steps.
- Validate deep-copy correctness under race detector: `go test -race ./internal/agentgen/...`.
- Add join-failure test: one branch errors, other is cancelled.
- Confirm backward compatibility: all existing sequential skill tests still pass.

### Phase 3 — Canvas unlock (max_out relaxed, after LocalExecutor is green)
- Change `EdgeRules.MaxOut` from 1 to 0 for LLM, HTTP, Transform, MCPCall, A2ACall in `nodes.go`.
- Frontend `isPipeConnectionValid` is data-driven from server `EdgeRules` — no frontend change needed.
- Canvas now permits fan-out wiring. Compiler rejects malformed DAGs (cycles, orphaned join nodes).

### Phase 4 — TemporalExecutor
- Implement `ExecuteStepActivity` and `TemporalExecutor`.
- Add `execution_backend` column to `agent_definitions` (default `"local"`).
- Refactor `OrchestrationWorkflow` to use `TemporalExecutor` when backend is `"temporal"`.
- Retain `RunOrchestratorActivity` registered under its original name for any in-flight workflows.
- Tests: integration test with live Temporal; verify fan-out produces two activity executions.

### Phase 5 — Loop, HumanWait, A2A in DAG context
- Move HumanWait signal handling to `HumanWaitActivity`.
- Loop: validate `MaxIterations` enforcement under concurrent outer branches.
- A2A: timeout propagation in fan-out context.
- ADK: remains optional — `ExecutionBackend` interface can wrap an ADK executor if needed without
  touching the plan compiler or canvas.

---

## 11. Files and Types That Must Change

| File | Change |
|---|---|
| `go/internal/agentgen/spec.go` | Add `ExecutionPlan`, `PlanNode`, `JoinMode` |
| `go/internal/agentgen/compiler.go` | Add plan-compiler pass; join annotation; Phase 3: relax MaxOut validation |
| `go/internal/agentgen/interpreter.go` | Accept `ExecutionBackend`; dispatch to it; remove `nextStepOverride` mutable field (move to executor state) |
| `go/internal/agentgen/nodes.go` | Phase 3: `MaxOut: 0` for LLM, HTTP, Transform, MCPCall, A2ACall; Phase 1: `Execute` for `StepParallel` |
| `go/internal/agentgen/executor.go` | **New** — `ExecutionBackend` interface |
| `go/internal/agentgen/local_executor.go` | **New** — `LocalExecutor`, `deepCopyVars`, `execState` |
| `go/internal/temporal/plan_executor.go` | **New** — `TemporalExecutor`, `ExecuteStepActivity` |
| `go/internal/temporal/workflow.go` | Phase 4: use `TemporalExecutor`; move HITL to HumanWaitActivity |
| `go/internal/domain/domain.go` | Phase 4: add `ExecutionBackendType` or use string constant |
| `db/` (new migration) | Phase 4: `agent_definitions.execution_backend` column (default `'local'`) |
| `go/TEST_INDEX.md` | Add rows per phase |

---

## 12. Tests Required

| Phase | Tests |
|---|---|
| 0 | `TestCompile_LinearPlan`, `TestCompile_FanOutJoin`, `TestCompile_BranchPlan` |
| 1 | `TestLocalExecutor_Linear`, `TestLocalExecutor_FanOut`, `TestLocalExecutor_Join_WaitAll`, `TestLocalExecutor_JoinFailure_CancelsOtherBranch`, `TestDeepCopyVars_NestedMap`, `TestLocalExecutor_Race` (run with `-race`) |
| 2 | `TestLocalExecutor_E2E_ParallelHTTP` (mock HTTP), `TestBackwardCompat_AllExistingSkills` |
| 3 | `TestCompiler_MaxOut0_AllowsFanOut`, `TestCanvas_FanOutEdgeAccepted` |
| 4 | `TestTemporalExecutor_LinearActivity`, `TestTemporalExecutor_FanOut_TwoActivities` (integration, live Temporal) |
| 5 | `TestLoop_MaxIterationsEnforced`, `TestHumanWait_SignalResumes` |

---

## 13. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Deep-copy misses nested slice-of-map | Recursive copy + `-race` test with nested mutation |
| Temporal history bloat (many small activities) | Batch sequential steps into one activity where branch/join does not split them |
| Join key collision (two branches write same var) | Compile-time warning when two branches declare same `Outputs` var; runtime: last-write-wins is documented |
| Canvas unlocked before executor is ready (Phase 3 before Phase 1/2) | **Gate:** Phase 3 only merges after Phase 2 integration tests are green |
| `OrchestrationWorkflow` in-flight during migration | Retain `RunOrchestratorActivity` under original name; Temporal versioning (`workflow.GetVersion`) if needed |
| Loop inside parallel branch: recursive goroutine depth | Loops are opaque sub-executors — they call a fresh sequential sub-interpreter, not the outer executor |

---

## 14. Smallest First Step

**Monday:** Implement `ExecutionPlan`, `PlanNode`, and `JoinMode` types in `spec.go` (pure type
definitions, ~30 lines). Write `CompileExecutionPlan(skill *SkillSpec) *ExecutionPlan` in a new
file `go/internal/agentgen/plan_compiler.go` that populates `Next`, detects `inDegree > 1` for
join annotation, and sets `StartID`. Add three table-driven tests: linear, diamond fan-out, branch.
Zero runtime change — the interpreter still runs the old sequential loop. This proves the compiler
handles multi-successor graphs and gives a foundation for Phase 1 without any risk to existing
agents or Temporal workflows.
