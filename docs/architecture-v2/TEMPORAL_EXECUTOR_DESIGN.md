# TemporalExecutor — Phase 4 Design
# Date: 2026-08-29
# Status: design only — no code changed

---

## 1. Current architecture (what exists today)

```
WS/SSE handler
  └── OrchestrationWorkflow (Temporal)
        └── RunOrchestratorActivity  ← one long-running heartbeating activity
              └── orchestrator.Run()  ← multi-turn LLM tool loop
                    └── agentregistry.InvokeForRun()
                          └── HTTP POST → them-agent-runtime
                                └── LocalExecutor.Execute()
                                      └── goroutine fan-out over ExecutionPlan
```

`LocalExecutor` is in `go/internal/agentgen/local_executor.go`.
`ExecutionBackend` interface is in `go/internal/agentgen/executor.go`.
The wiring point is `go/cmd/agent-runtime/main.go:294–295`.

Temporal currently sees the **entire canvas agent run as one opaque HTTP call**.
It has no visibility into individual DAG nodes.

---

## 2. Workflow boundary decision — the central question

**Option A — Child workflow started by agent-runtime (recommended)**

When `them-agent-runtime` receives an invocation, instead of running `LocalExecutor`, it signals Temporal to start a child DAG workflow. The child workflow executes the `ExecutionPlan` node by node using `ExecuteStepActivity` activities. The HTTP handler on `agent-runtime` blocks (polls Redis or a Temporal `QueryWorkflow`) until the child completes, then returns the result to the caller.

```
RunOrchestratorActivity  (parent, unchanged)
  → HTTP POST → them-agent-runtime
       └── TemporalClient.ExecuteWorkflow(DAGWorkflow, plan, ic)
             └── DAGWorkflow (child)
                   ├── ExecuteStepActivity(node-A)
                   ├── ExecuteStepActivity(node-B1) ─┐ parallel
                   ├── ExecuteStepActivity(node-B2) ─┘
                   └── ExecuteStepActivity(node-C)   ← join
```

**Option B — Inline in RunOrchestratorActivity as Temporal child workflow**

When the orchestrator decides to call a canvas agent, instead of posting to `agent-runtime`, `RunOrchestratorActivity` directly schedules a child Temporal workflow via `workflow.ExecuteChildWorkflow`. No HTTP hop.

**Option C — TemporalExecutor replaces LocalExecutor in agent-runtime, no child workflow**

`agent-runtime` uses `TemporalExecutor` which calls `workflow.ExecuteActivity` on each node inside what would need to be its own workflow context — but `agent-runtime` is not itself a Temporal workflow, so this is not valid without restructuring.

### Decision: Option A

**Reasons:**

1. **Preserves the existing call boundary exactly.** `agentregistry.InvokeForRun()` already makes an HTTP POST to `them-agent-runtime`. No changes to the orchestrator, the LLM loop, or `RunOrchestratorActivity`.

2. **`them-agent-runtime` is already the canvas execution boundary.** It owns `ExecutionPlan` compilation, `InvocationContext` parsing, and skill dispatch. Adding a Temporal client there is additive.

3. **Config lives in `them-agent-runtime`.** The `execution_backend` field on `AgentSpec` is read there. The switch from `LocalExecutor` to `TemporalExecutor` is a one-line branch in `main.go`.

4. **No Temporal workflow determinism constraints on the LLM loop.** `RunOrchestratorActivity` uses real `context.Context`, makes HTTP calls, reads random — none of that is valid inside a Temporal workflow function. Option B would require the LLM loop to be a workflow, which is architecturally incompatible.

5. **Child workflow gives per-node Temporal visibility.** Each `ExecuteStepActivity` call appears in the Temporal UI under the child workflow. Retry counts, timeouts, and failure reasons are all visible per node.

**Trade-off of Option A:** The `agent-runtime` HTTP handler must wait for the child workflow to complete before returning to the caller. This is synchronous blocking — acceptable because `RunOrchestratorActivity` already heartbeats independently and can tolerate a long-running canvas agent.

---

## 3. Component map — files to create or change

### New files

| File | Purpose |
|---|---|
| `go/internal/agentgen/temporal_executor.go` | `TemporalExecutor` — implements `ExecutionBackend`; uses a Temporal client to start `DAGWorkflow` and wait for its result |
| `go/internal/agentgen/dag_workflow.go` | `DAGWorkflow` — Temporal workflow function; runs the `ExecutionPlan` using `workflow.Go`, `workflow.ExecuteActivity`, and `workflow.Await` |
| `go/internal/agentgen/dag_activities.go` | `ExecuteStepActivity` — Temporal activity; calls `Interpreter.executeStep` for one node; stateless, registered on a dedicated task queue |
| `go/internal/agentgen/dag_worker.go` | Worker registration helper — registers `DAGWorkflow` + `ExecuteStepActivity` on `"dag-nodes"` task queue; called from `cmd/agent-runtime/main.go` |
| `go/internal/agentgen/temporal_executor_test.go` | Conformance tests (shared test suite run against both executors) |

### Changed files

| File | Change |
|---|---|
| `go/cmd/agent-runtime/main.go` | Add Temporal client init; add `execution_backend` branch; wire `TemporalExecutor` when spec says `"temporal"` |
| `go/internal/agentgen/spec.go` | Add `ExecutionBackend string` field to `AgentSpec` (`"local"` default, `"temporal"` for Temporal) |
| `go/internal/admin/service/agent_definitions_publish.go` | Copy `execution_backend` from canvas JSON into compiled `AgentSpec` |
| `go/internal/config/config.go` | Add `DAGWorkerTaskQueue`, `DAGWorkerConcurrency` env vars |
| `db/046_agent_execution_backend.sql` | Add `execution_backend TEXT NOT NULL DEFAULT 'local' CHECK (execution_backend IN ('local','temporal'))` to `agent_runtime_specs` |
| `go/TEST_INDEX.md` | New S1-75 section |

---

## 4. Data flow and semantics

### 4.1 ExecutionPlan serialization

`ExecutionPlan` is already JSON-serializable (`json` tags on every field). It is the only input to both executors. The Temporal workflow receives it as its `WorkflowInput` — Temporal serializes it through its data converter (JSON by default).

`PipelineVars` is `map[string]any` — also JSON-serializable. Each activity input carries the node's scoped input vars; each activity output carries the node's scoped output vars.

### 4.2 InvocationContext serialization

`InvocationContext` holds `TenantID`, `ApplicationID`, `AgentID`, `BindingID`, `Credentials`, `AgentParams`, `AppGlobalParams`, `NodeLLMOverrides`. All are strings or `map[string]string` — safe for JSON serialization. **Credentials must never appear in Temporal history.** Pass credentials as an opaque encrypted blob; `TemporalExecutor` decrypts after the activity starts (using the same per-request decryption already done in `agent-runtime`). Alternatively, pass only the `BindingID` and re-decrypt in the activity from DB — same pattern as `TaskState` today.

### 4.3 JoinMode → Temporal primitives

| LocalExecutor | DAGWorkflow equivalent |
|---|---|
| `sync.WaitGroup` fan-out | `workflow.Go` coroutines per branch |
| `joinState.arrive()` mutex | `workflow.Await()` over a shared counter (deterministic) |
| `JoinWaitAll` — wait for all | `workflow.Await(func() bool { return arrivals == len(JoinOf) })` |
| `JoinBranchMerge` — first wins | `workflow.Await(func() bool { return arrivals >= 1 })` |
| `cancel()` on error | `workflow.GetChildWorkflowExecution().Cancel()` or ctx cancel on sibling coroutines |
| `sharedResult.setIfEmpty` | Workflow-local var; first coroutine to set it wins |

All of this is deterministic: Temporal replays the workflow from history using the same coroutine scheduling. `workflow.Go` and `workflow.Await` are safe in Temporal workflow functions.

### 4.4 nextStepOverride (branch routing)

`execNode` returns `nextOverride` set by `Interpreter.executeStep` → branch Execute logic sets `interp.nextStepOverride`. In the Temporal path, `ExecuteStepActivity` returns `{OutputVars map[string]any, NextOverride string}`. The workflow reads `NextOverride` to determine which arm to follow — identical semantics, different delivery mechanism.

### 4.5 Error and cancellation

- Activity failure → workflow coroutine gets the error → workflow cancels sibling coroutines via `ctx` cancel → workflow returns the causal error to the caller.
- Parent `RunOrchestratorActivity` context cancelled → Temporal propagates cancellation to the child `DAGWorkflow` → workflow cancels all in-flight activities.
- `ErrContractViolation` → returned as a non-retryable `temporal.ApplicationError("ContractViolation")` so retries do not repeat a deterministically failing node.

---

## 5. ExecuteStepActivity — the generic dispatcher

```go
// go/internal/agentgen/dag_activities.go

type StepActivityInput struct {
    Node    PlanNode        `json:"node"`
    Vars    PipelineVars    `json:"vars"`    // scoped inputs for this node
    IC      ActivityIC      `json:"ic"`      // credential-safe InvocationContext subset
}

type StepActivityOutput struct {
    Vars         PipelineVars `json:"vars"`          // scoped outputs written by node
    NextOverride string       `json:"next_override"`  // non-empty if branch sets routing
    ResultText   string       `json:"result_text"`    // non-empty if node produced a final result
    ResultMT     string       `json:"result_mt"`
}

// ActivityIC carries only the non-secret fields over Temporal history.
// Secrets are re-loaded from DB by binding_id in the activity.
type ActivityIC struct {
    TenantID      string `json:"tenant_id"`
    ApplicationID string `json:"application_id"`
    AgentID       string `json:"agent_id"`
    BindingID     string `json:"binding_id"`
}
```

The activity:
1. Reconstructs `InvocationContext` by loading credentials from DB using `BindingID` (same as today).
2. Calls `interp.executeStep(ctx, ic, stepSpec, vars, &result)`.
3. Returns `StepActivityOutput`.

No node logic is duplicated. All 12 node types dispatch through the existing registry.

---

## 6. DAGWorkflow — workflow function sketch

```go
// go/internal/agentgen/dag_workflow.go

type DAGWorkflowInput struct {
    Plan    ExecutionPlan `json:"plan"`
    Initial PipelineVars  `json:"initial"`
    IC      ActivityIC    `json:"ic"`
}

type DAGWorkflowOutput struct {
    ResultText string `json:"result_text"`
    ResultMT   string `json:"result_mt"`
}

func DAGWorkflow(ctx workflow.Context, input DAGWorkflowInput) (DAGWorkflowOutput, error) {
    // Build node index
    // Initialize joinCounters (atomic in workflow-local state, deterministic)
    // Launch runBranch coroutine from plan.StartID
    // workflow.Await final result or first error
    // Return DAGWorkflowOutput
}
```

Each `runBranch` coroutine (a `workflow.Go` goroutine):
1. Checks join state — `workflow.Await` if not ready.
2. Calls `workflow.ExecuteActivity(ctx, ExecuteStepActivity, input, activityOptions)`.
3. Reads `NextOverride` from output to determine next step.
4. At fan-out: calls `workflow.Go` for each sibling branch.
5. At terminal node: sets shared result, signals completion.

---

## 7. Retry and timeout policy

| Level | Policy |
|---|---|
| `ExecuteStepActivity` — LLM, HTTP, A2ACall | `MaxAttempts: 3`, `InitialInterval: 2s`, `BackoffCoefficient: 2.0`, `MaxInterval: 30s`, `NonRetryableErrors: ["ContractViolation", "InvalidConfig"]` |
| `ExecuteStepActivity` — Input, Transform, Response, Branch | `MaxAttempts: 1` (deterministic — retry is pointless) |
| `ExecuteStepActivity` — HumanWait | `MaxAttempts: 1`, `ScheduleToCloseTimeout: 48h` (waits for signal) |
| `ExecuteStepActivity` — MCP | `MaxAttempts: 3`, same backoff as HTTP |
| `DAGWorkflow` itself | `WorkflowExecutionTimeout: 10m` (configurable via env `DAG_WORKFLOW_TIMEOUT`) |

---

## 8. Task queues

| Queue | Workers | Purpose |
|---|---|---|
| `"orchestration"` (existing) | `them-go-worker` | `OrchestrationWorkflow` + `RunOrchestratorActivity` — unchanged |
| `"dag-nodes"` (new) | `them-agent-runtime` (both replicas) | `DAGWorkflow` + `ExecuteStepActivity` |

`them-agent-runtime` already holds `Interpreter`, `LLMFactory`, `MCPCaller` — all the execution dependencies. Registering a Temporal worker there is the natural fit. The worker runs in the same process as the existing HTTP handler, on the existing 2 replicas — giving the Temporal scheduler two execution slots for DAG activities.

`DAGWorkflow` must also be registered on the `"dag-nodes"` queue so Temporal can replay it there. If the workflow is replayed on a worker that lacks an `Interpreter` (e.g. `them-go-worker`), it will fail. Keeping both on the same queue avoids this.

---

## 9. DB and config changes

### DB migration — `db/046_agent_execution_backend.sql`

```sql
ALTER TABLE agent_runtime_specs
  ADD COLUMN IF NOT EXISTS execution_backend TEXT NOT NULL DEFAULT 'local'
    CHECK (execution_backend IN ('local', 'temporal'));
```

One column on `agent_runtime_specs`. No index needed — read once at invocation time.

### AgentSpec field — `go/internal/agentgen/spec.go`

```go
type AgentSpec struct {
    // ... existing fields ...
    ExecutionBackend string `json:"execution_backend,omitempty"` // "local" (default) or "temporal"
}
```

Empty string and `"local"` both mean `LocalExecutor`. The compiler sets this from the canvas publish payload.

### Config — `go/internal/config/config.go`

```go
DAGWorkerTaskQueue   string // env: DAG_WORKER_TASK_QUEUE, default "dag-nodes"
DAGWorkerConcurrency int    // env: DAG_WORKER_CONCURRENCY, default 20
DAGWorkflowTimeout   string // env: DAG_WORKFLOW_TIMEOUT, default "10m"
```

### docker-compose additions

```yaml
them-agent-runtime:
  environment:
    - TEMPORAL_ENABLED=true
    - TEMPORAL_HOST_PORT=temporal:7233
    - DAG_WORKER_TASK_QUEUE=dag-nodes
    - DAG_WORKER_CONCURRENCY=20
```

---

## 10. Conformance test suite

`go/internal/agentgen/temporal_executor_test.go` runs the same table of scenarios against both executors. The test harness uses the Temporal `testsuite.WorkflowTestSuite` (in-process, no external Temporal required).

### Test scenarios (must pass on both executors)

| ID | Scenario | Assertion |
|---|---|---|
| CT-1 | Linear chain A→B→C | Result from C; A/B/C all execute in order |
| CT-2 | Parallel fan-out A→{B,C}→D (JoinWaitAll) | Both B and C execute; D receives merged vars |
| CT-3 | Branch true path A→branch→B→D (JoinBranchMerge) | B executes, C does not; D receives B's vars |
| CT-4 | Branch false path A→branch→C→D | C executes, B does not |
| CT-5 | Node error propagates | Error from B cancels siblings; caller receives causal error |
| CT-6 | Context cancellation | Cancel before DAG completes; all in-flight nodes stop |
| CT-7 | JoinBranchMerge: second arm dropped | Even if C somehow executes, D continues with first arrival only |
| CT-8 | Empty plan | Returns error immediately |
| CT-9 | VarRef contract violation | Returns `ErrContractViolation`; non-retryable |
| CT-10 | Result from correct node | `StepResponse` output var propagated to `ExecutionResult.Text` |

The test harness uses `MockInterpreter` (already used in existing tests) so no real LLM/HTTP calls are needed.

---

## 11. Phased implementation plan

### Phase 4-A — Serialization and wiring (no workflow yet)

**Scope:** Prove the data model survives a Temporal round-trip.

1. Add `ExecutionBackend` to `AgentSpec` and `agent_runtime_specs` (DB migration + compiler + DAL).
2. Add `ActivityIC` type (credential-safe IC subset).
3. Add `StepActivityInput` / `StepActivityOutput` types.
4. Implement `ExecuteStepActivity` (calls `interp.executeStep` — same as `execNode` in local executor).
5. Write unit tests for activity serialization (CT-9, CT-10 input/output shapes).

**Files:** `spec.go`, `db/046_*.sql`, `dag_activities.go`, `dag_activities_test.go`.
**Risk:** Low. No workflow code yet; existing tests unaffected.

### Phase 4-B — DAGWorkflow

**Scope:** Implement the workflow using `testsuite`.

1. Implement `DAGWorkflow` in `dag_workflow.go`.
2. Implement `dag_worker.go` (registration helper).
3. Write conformance tests CT-1 through CT-10 against `WorkflowTestSuite`.
4. Run `go test -race ./internal/agentgen/...` — must be green.

**Files:** `dag_workflow.go`, `dag_worker.go`, `temporal_executor_test.go`.
**Risk:** Medium. `workflow.Await` in Temporal must be used carefully — deadlock possible if the join counter is never incremented. Use deterministic Temporal timer as a safety timeout inside `workflow.Await`.

### Phase 4-C — TemporalExecutor

**Scope:** Implement `TemporalExecutor` and wire it into `agent-runtime`.

1. Implement `TemporalExecutor` in `temporal_executor.go`: starts `DAGWorkflow` as a child workflow (or via `temporal.Client.ExecuteWorkflow` depending on context), blocks until result, returns `ExecutionResult`.
2. Wire Temporal worker registration into `cmd/agent-runtime/main.go`.
3. Add `execution_backend` branch: `if spec.ExecutionBackend == "temporal" { ... }` at line 294–295.
4. Run conformance tests CT-1..CT-10 against real Temporal dev server in CI (`docker compose --profile temporal`).

**Files:** `temporal_executor.go`, `cmd/agent-runtime/main.go`, `config.go`.
**Risk:** Medium. Real Temporal round-trip adds ~50–200ms per activity. Fine for correctness; measure before enabling on LLM-heavy agents.

### Phase 4-D — Frontend and publish UI

**Scope:** Surface `execution_backend` in the publish panel.

1. Add `execution_backend` field to `AgentDefinitionDoc` in `api.ts`.
2. Add a toggle (Local / Temporal) in the `BuilderTopBar` publish panel.
3. Round-trip through `handlePublish` → `agent_definitions` → compiler → `AgentSpec`.

**Files:** `frontend/src/lib/api.ts`, `frontend/src/app/admin/agents/builder/components/BuilderTopBar.tsx`.
**Risk:** Low. UI change only; no execution path changes.

---

## 12. Open decisions

| # | Question | Options | Recommendation |
|---|---|---|---|
| OD-1 | How to pass credentials to `ExecuteStepActivity`? | (a) Pass encrypted blob; activity decrypts. (b) Pass only `BindingID`; activity re-loads from DB. | **Option (b)** — consistent with how `TaskState` handles credentials today; nothing secret in Temporal history. |
| OD-2 | Where does `DAGWorkflow` live — child workflow or separate top-level? | (a) Child of nothing — started by `agent-runtime` via `temporal.Client.ExecuteWorkflow`. (b) Child of `OrchestrationWorkflow`. | **Option (a)** — keeps the existing `RunOrchestratorActivity` boundary intact; child of (b) would require changes to `OrchestrationWorkflow` and coupling. |
| OD-3 | How does `agent-runtime` HTTP handler wait for the child workflow result? | (a) Temporal `client.GetWorkflow(ctx, workflowID, "").Get(ctx, &result)` — blocks. (b) Polling Redis. | **Option (a)** — Temporal long-poll is the right primitive; no extra Redis key needed. |
| OD-4 | `HumanWait` in DAG Temporal — how does the signal arrive? | Signal must be sent to the `DAGWorkflow` run, not the parent `OrchestrationWorkflow`. | Add `DAGWorkflowID` to the run's metadata so the HITL signal handler can target the correct workflow. Design separately in Phase 4-B. |
| OD-5 | `StepLoop` in Temporal — how to handle `MaxIterations` without a loop in the workflow? | Temporal workflow can use a `for` loop — deterministic because each iteration produces activity calls that appear in history. | Implement as a `for` loop in the workflow; MaxIterations enforced by the workflow, not the activity. |
| OD-6 | Default `execution_backend` for new agents? | `"local"` (safe, existing behavior) or `"temporal"` (new). | **`"local"`** until Phase 4-C is live-verified. |

---

## 13. What does NOT change

- `LocalExecutor` is preserved as-is. All existing canvas agents continue to use it.
- `OrchestrationWorkflow` and `RunOrchestratorActivity` are unchanged.
- `InvokeForRun` in `agentregistry` is unchanged — it still posts to `them-agent-runtime`.
- All 12 node `Execute` functions in `nodes.go` are unchanged — `ExecuteStepActivity` calls them through the existing interpreter dispatch.
- All existing tests (S1-54, S1-72, S1-73, S1-74) continue to pass.

---

## 14. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Temporal history size grows with many nodes | Low-Medium | Each activity input/output is bounded by `PipelineVars` size; large LLM responses → store in Redis, pass reference in vars |
| Activity timeout on slow LLM calls | Medium | `ScheduleToCloseTimeout: 5m` on LLM activities; matches existing HTTP client timeout in interpreter |
| Deadlock in `workflow.Await` if join counter never increments | Medium | Add `workflow.WithTimeout` inside `workflow.Await` as a safety valve; test with CT-5 (error path) |
| Credential leakage into Temporal history | High if mishandled | Use OD-1 option (b) — only `BindingID` in history; re-load from DB in activity |
| `them-agent-runtime` now needs a Temporal worker — adds startup dependency | Low | Temporal worker startup failure is non-fatal if `execution_backend == "local"` (default); guard with `if cfg.TemporalEnabled` |
| `DAGWorkflow` replay requires same worker registration | Medium | Keep `DAGWorkflow` and `ExecuteStepActivity` on same task queue `"dag-nodes"`; never register one without the other |
