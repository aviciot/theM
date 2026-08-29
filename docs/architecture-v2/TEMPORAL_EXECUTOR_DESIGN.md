# TemporalExecutor — Phase 4 Design (revised v2)
# Date: 2026-08-29
# Status: Phase 4-A implemented (node_executor.go + spec field + compiler wiring)

---

## 1. Current architecture (what exists today)

```
WS/SSE handler
  └── OrchestrationWorkflow  (Temporal, task queue: "them-orchestration-go")
        └── RunOrchestratorActivity  ← long-running, heartbeating activity
              └── orchestrator.Run()  ← multi-turn LLM tool loop
                    └── agentregistry.InvokeForRun()
                          └── HTTP POST → them-agent-runtime
                                └── LocalExecutor.Execute()
                                      └── goroutine fan-out over ExecutionPlan
```

Key constants in `go/internal/temporal/workflow.go`:
- `GoTaskQueue = "them-orchestration-go"` — Go worker polls this queue
- `TaskQueue   = "them-orchestration"`    — Python legacy queue (kept for compat)

`LocalExecutor` is in `go/internal/agentgen/local_executor.go`.
`ExecutionBackend` interface is in `go/internal/agentgen/executor.go`.
The wiring point is `go/cmd/agent-runtime/main.go:294–295`.

Temporal currently sees the **entire canvas agent run as one opaque HTTP call**.
It has no visibility into individual DAG nodes.

---

## 2. Corrected execution flow

```
RunOrchestratorActivity  (activity running on them-go-worker, unchanged)
  → HTTP POST → them-agent-runtime
       └── if execution_backend == "temporal":
             TemporalClient.ExecuteWorkflow("CanvasAgentWorkflow", plan, ic_safe)
             block on workflow result  (client.GetWorkflow(...).Get(ctx, &result))
             if HTTP ctx cancelled → client.CancelWorkflow(...)
             → return HTTP response to caller
       └── else:
             LocalExecutor.Execute(...)  (unchanged)

CanvasAgentWorkflow  (independent top-level workflow, task queue: "dag-nodes")
  ├── ExecuteStepActivity(node-A)
  ├── ExecuteStepActivity(node-B1) ─┐ parallel workflow.Go coroutines
  ├── ExecuteStepActivity(node-B2) ─┘
  └── ExecuteStepActivity(node-C)   ← join (workflow.Await)
```

---

## 3. Corrections to the original design

### 3.1 `CanvasAgentWorkflow` is a top-level workflow, not a child workflow

`them-agent-runtime` is an HTTP service, not a Temporal workflow. It holds a
`temporal.Client` and calls `client.ExecuteWorkflow(...)`, which starts an
**independent top-level workflow** named `CanvasAgentWorkflow`. There is no
parent–child relationship in the Temporal sense. The agent-runtime HTTP handler
blocks synchronously on the workflow result via `client.GetWorkflow(...).Get(ctx, &out)`.

The name `DAGWorkflow` from the original design is replaced throughout by
`CanvasAgentWorkflow` to make this explicit.

### 3.2 Option B is invalid — `RunOrchestratorActivity` cannot call `workflow.ExecuteChildWorkflow`

`workflow.ExecuteChildWorkflow` is only callable from workflow code that receives a
`workflow.Context`. `RunOrchestratorActivity` receives a plain `context.Context`
and runs inside an activity goroutine — it cannot schedule child workflows.
Option B is architecturally impossible without restructuring the orchestrator as
a workflow, which is incompatible with its LLM loop and HTTP calls.
**Option B is removed from this design.**

### 3.3 Cancellation does not propagate automatically

When `RunOrchestratorActivity`'s context is cancelled (Temporal timeout, workflow
cancel, pod crash), the HTTP call to `them-agent-runtime` is cancelled too. But
`CanvasAgentWorkflow` has already been registered with Temporal and **runs
independently** — it is not cancelled automatically.

`them-agent-runtime` must explicitly cancel the workflow when the HTTP request
context is cancelled:

```go
workflowRun, err := temporalClient.ExecuteWorkflow(ctx, opts, CanvasAgentWorkflow, input)
// ...
resultCh := make(chan result, 1)
go func() {
    var out CanvasAgentWorkflowOutput
    err := workflowRun.Get(ctx, &out)
    resultCh <- result{out, err}
}()

select {
case r := <-resultCh:
    // normal completion
case <-ctx.Done():
    // HTTP request cancelled — explicitly stop the workflow
    cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = temporalClient.CancelWorkflow(cancelCtx, workflowRun.GetID(), workflowRun.GetRunID())
    return nil, ctx.Err()
}
```

Inside `CanvasAgentWorkflow`, Temporal delivers the cancellation as a
`workflow.Context` cancellation. In-flight activities receive a cancellation
signal; the workflow returns `temporal.CanceledError`.

### 3.4 Correlation — parent run ID, Workflow ID, Memo, Search Attributes

Every `CanvasAgentWorkflow` execution is correlated to its parent orchestration run:

- **Workflow ID**: `"canvas-agent:{agentID}:{parentRunID}:{skillID}"` — deterministic,
  unique per invocation, searchable in Temporal UI.
- **Memo**: `{"parent_run_id": parentRunID, "tenant_id": tenantID, "agent_id": agentID}`
  — visible in Temporal UI without a Search Attribute index.
- **Search Attributes** (optional, requires Temporal namespace config):
  `ParentRunID`, `TenantID`, `AgentID` as custom search attributes — enables
  server-side filtering. Add only if the Temporal namespace is configured for it.

### 3.5 Single source of truth for `execution_backend`

`execution_backend` is stored **only in `AgentSpec`** (the compiled JSON in
`agent_runtime_specs.spec`). No separate DB column. The value is set by the
compiler at publish time, persisted as part of `AgentSpec`, and read at invocation
time from the already-loaded spec. Adding a separate column would duplicate the
authoritative value and require keeping two stores in sync.

The `AgentSpec` field:
```go
ExecutionBackend string `json:"execution_backend,omitempty"` // "" or "local" → LocalExecutor; "temporal" → TemporalExecutor
```

### 3.6 Migration number

The next available migration number is **`048`** (046 = `mcp_probe_credential`,
047 = `ep_llm` already exist). No `agent_runtime_specs` column is added — see §3.5.
Migration 048 is reserved for any schema change that Phase 4 actually requires
(e.g. a `canvas_dag_runs` audit table if we add per-node run recording).
If Phase 4 needs no schema change, 048 is unused.

### 3.7 Actual task queue names

From `go/internal/temporal/workflow.go`:
- `GoTaskQueue = "them-orchestration-go"` — existing Go worker queue
- `TaskQueue   = "them-orchestration"`    — Python legacy queue

The new DAG node queue is `"canvas-dag-nodes"` (distinct from both). See §6.

### 3.8 What must NOT pass through Temporal history

Temporal persists every workflow and activity input/output in its history. This is
permanent and visible to operators.

**Never pass:**
- Full `InvocationContext` (contains `Credentials map[string]string`)
- Full cumulative `PipelineVars` at each node (may contain LLM output, PII)
- Raw API keys, bearer tokens, or any secret material

**Pass instead:**
- `ActivityIC` — credential-safe subset: `TenantID`, `ApplicationID`, `AgentID`, `BindingID` only
- **Scoped input delta** for each node: only the `VarRef` names declared in `node.Inputs`, not the full accumulated vars
- **Scoped output delta** from each node: only the vars declared in `node.Outputs`
- For large values (LLM output > 64KB threshold): store in Redis with a TTL key;
  pass the Redis key in vars. Activity reads and writes the key. This keeps history small.

The `CanvasAgentWorkflow` reconstructs global `PipelineVars` by merging output
deltas from each completed activity — it never passes the full merged map to the
next activity, only the projected scoped inputs.

### 3.9 Package placement — `internal/temporal`, not `agentgen`

Temporal workflow and activity code belongs in `go/internal/temporal/`, which
already owns all Temporal concerns (`workflow.go`, `activities.go`, `client.go`,
`signaler.go`). Placing Temporal-specific constructs inside `agentgen` would
couple the core execution engine to the Temporal SDK — a framework dependency that
does not belong in a package meant to be testable without Temporal.

The boundary:

- `go/internal/agentgen/` — exports one adapter function (see §4.2) and the
  existing `ExecutionBackend` interface. No Temporal SDK import.
- `go/internal/temporal/` — contains `CanvasAgentWorkflow`, `CanvasAgentActivities`,
  and `TemporalExecutor`. Imports `agentgen` for types and the adapter function.

### 3.10 Dedicated worker process vs. in-process worker in `agent-runtime`

`them-agent-runtime` is an HTTP service optimized for low-latency canvas agent
invocations. Adding a Temporal worker (goroutine pool, long-poll loop, activity
registration) to the same process:

- Increases baseline memory and goroutine count per replica
- Means the Temporal worker task queue competes with HTTP handler goroutines
- Tightly couples a stateless HTTP service to a stateful Temporal polling loop

**Recommended: separate dedicated worker process — `them-dag-worker`.**

A new binary `go/cmd/dag-worker/main.go` polls `"canvas-dag-nodes"`, registers
`CanvasAgentWorkflow` and `ExecuteStepActivity`, and holds the `Interpreter`,
`LLMFactory`, `MCPCaller` dependencies. `them-agent-runtime` retains only the
`temporal.Client` to start and wait on workflows.

Benefits:
- `them-agent-runtime` HTTP latency is unaffected by Temporal polling
- `them-dag-worker` can be scaled independently (more replicas = more concurrent node executions)
- Failures in the DAG worker do not crash the HTTP service

The `InvocationContext` dependencies (credential loading from DB, agent-params)
are shared via the same `internal/admin/dal` package already used by `them-go-bridge`.

---

## 3b. Additional corrections (v2)

### 3b.1 Workflow ID must include a stable invocation/tool-call ID; retries reattach

The previous workflow ID scheme `"canvas-agent:{agentID}:{parentRunID}:{skillID}"` is
insufficient when `RunOrchestratorActivity` is retried by Temporal: a new attempt would
start a **new** `CanvasAgentWorkflow` for the same logical invocation, producing duplicate
execution. The attempt number must not be part of the ID (that was already removed from v1).

Instead, the caller — `agentregistry.InvokeForRun()` — generates a **stable invocation ID**
(`invocationID`) that is unique per tool-call and stable across retries of the same activity.
The correct source is the A2A task ID or the run step ID, which is assigned before the
activity starts and does not change on retry.

**Workflow ID**: `"canvas:{agentID}:{invocationID}"` where `invocationID` is passed from
the orchestrator through to `them-agent-runtime` as a request header `X-Them-Invocation-Id`.

**Re-attach on retry**: when `ExecuteWorkflow` returns an error because the workflow ID
already exists (`temporal.IsWorkflowExecutionAlreadyStartedError`), call
`client.GetWorkflow(ctx, workflowID, "")` to attach to the already-running execution.
This is the standard Temporal idempotent-start pattern.

```go
wfOpts := client.StartWorkflowOptions{
    ID:                    "canvas:" + agentID + ":" + invocationID,
    TaskQueue:             cfg.DAGWorkerTaskQueue,
    WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
}
run, err := temporalClient.ExecuteWorkflow(ctx, wfOpts, CanvasAgentWorkflow, input)
if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
    run = temporalClient.GetWorkflow(ctx, wfOpts.ID, "")
}
```

### 3b.2 Cancellation — correct pattern without goroutine race

The previous design used a goroutine + select + channel to wait for the workflow result,
which creates a race: the goroutine calling `Get` may continue after the `select` arm
fires and reference stack variables that are no longer valid.

**Correct pattern**: use `ctx` directly with `workflowRun.Get`. When `ctx` is cancelled,
`Get` returns immediately with `ctx.Err()`. Then explicitly cancel the workflow using a
fresh `context.Background()` with a short timeout.

```go
var out CanvasAgentWorkflowOutput
err := run.Get(ctx, &out)   // returns as soon as ctx is cancelled OR workflow completes
if err != nil && ctx.Err() != nil {
    // HTTP ctx cancelled — stop the workflow (best-effort, non-blocking path)
    cancelCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelFn()
    _ = temporalClient.CancelWorkflow(cancelCtx, run.GetID(), run.GetRunID())
    return nil, ctx.Err()
}
```

`workflowRun.Get(ctx, &out)` is already context-aware in the Temporal Go SDK — no goroutine
or channel needed.

### 3b.3 HumanWait handled by CanvasAgentWorkflow via signal, never inside an Activity

`ExecuteStepActivity` for a `human_wait` node must **not** block indefinitely inside the
activity. Temporal activities have a `ScheduleToClose` timeout; blocking for hours would
exhaust it. Instead:

- `ExecuteStepActivity` for `human_wait` returns immediately with a sentinel output
  `{WaitingForHuman: true}`.
- `CanvasAgentWorkflow` detects this and calls `workflow.GetSignalChannel(ctx, "human_input:{nodeID}")`.
- The workflow coroutine blocks on `workflow.Await` until the signal arrives or the
  workflow execution timeout fires.
- When the signal arrives, the workflow resumes, injects the reply into `PipelineVars`,
  and calls `ExecuteStepActivity` for the next node.
- The existing HITL signaler (`go/internal/temporal/signaler.go`) must be extended to
  target `CanvasAgentWorkflow` by its workflow ID (passed through run metadata).

### 3b.4 Timeout alignment

All timeouts must be consistent so no inner timer can fire after the outer has expired:

| Boundary | Timeout | Config |
|---|---|---|
| `RunOrchestratorActivity` `StartToClose` | 10 min | `internal/temporal/workflow.go:activityStartToClose` |
| `CanvasAgentWorkflow` `WorkflowExecutionTimeout` | 12 min | `DAG_WORKFLOW_TIMEOUT` env, default `"12m"` |
| `ExecuteStepActivity` `StartToClose` (LLM/HTTP/A2A/MCP) | 5 min | Per-activity options in `canvas_workflow.go` (`activityOptionsForNode`) |
| `ExecuteStepActivity` (HumanWait) | Returns immediately; signal wait in workflow | No activity timeout needed |
| Redis large-value TTL | 30 min | `DAGVarStore` constant `dagVarTTL` |

Rule: `activity StartToClose` < `CanvasAgentWorkflow` execution timeout
< `RunOrchestratorActivity` StartToClose + some slack. This ensures the DAG workflow
always terminates before the parent activity times out.

### 3b.5 ExecuteStepActivity must use a fresh cloned Interpreter per activity

Each `ExecuteStepActivity` call must have its own `*Interpreter` instance. `Interpreter`
carries mutable state (`nextStepOverride string`) which must not be shared across
concurrent activity goroutines. `them-dag-worker` holds a single `*Interpreter` template;
each activity call calls `interp.clone()` to get an isolated copy — the same pattern
`LocalExecutor` uses per branch goroutine.

`ExecuteNodeForActivity` (the Phase 4-A adapter already implemented) takes an `*Interpreter`
parameter. The caller (`ExecuteStepActivity`) passes `sharedInterp.clone()`.

### 3b.6 Retry policy — explicit idempotency classification per node type

Retrying a non-idempotent operation (LLM call, HTTP POST, A2A task dispatch) produces
duplicate side-effects. The retry policy must match the idempotency of each node type.

| Node type | Idempotent? | Retry policy | Rationale |
|---|---|---|---|
| `input`, `transform`, `response`, `branch`, `parallel` | Yes (pure, no I/O) | `MaxAttempts: 1` | Deterministic; retry adds no value |
| `http` GET | Yes | `MaxAttempts: 3`, backoff 2s→30s | Safe to retry reads |
| `http` POST/PUT/DELETE | **No** | `MaxAttempts: 1` | Side-effectful; caller must add idempotency key if retry needed |
| `llm` | **No** (each call may produce different output) | `MaxAttempts: 2` | Transient provider failures; accept 1 retry |
| `a2a_call` | **No** | `MaxAttempts: 1` | A2A tasks have their own retry in the target agent |
| `mcp_call` | Tool-dependent | `MaxAttempts: 2` for read-like tools, `MaxAttempts: 1` for mutating | MCP tool manifest has no idempotency annotation yet; default safe |
| `human_wait` | N/A | Activity returns immediately; no retry needed | Signal-wait is in the workflow, not the activity |
| `loop` (per iteration body) | Depends on body | Inherit body node policy | Each iteration schedules its own activities |

**Non-retryable error types** (always `MaxAttempts: 1` regardless of node type):
- `ContractViolation` — deterministic; retrying will always fail
- `InvalidConfig` — canvas misconfiguration; retrying will always fail
- `PermissionDenied` — binding policy violation; not transient

The `NodeTypeIdempotency` value is declared alongside `NodeDef` in the registry so
the activity options can be computed from the node type without hardcoding a switch.

### 3b.7 Binding reload scoped by all four IDs

When `ExecuteStepActivity` loads credentials from DB using `BindingID`, the query
must be scoped by all four identity dimensions to prevent cross-tenant or cross-agent
credential leakage:

```go
// Correct — all four IDs required
binding, err := dal.GetAgentBinding(ctx, tenantID, applicationID, agentID, bindingID)
```

`ActivityIC` must carry all four: `TenantID`, `ApplicationID`, `AgentID`, `BindingID`.
The DAL query must include all four in the `WHERE` clause — not just `binding_id` alone,
since binding IDs are UUIDs that could theoretically collide across tenants if a UUID
were reused (defense in depth).

### 3b.8 Large-value Redis TTL must cover maximum workflow duration

The Redis TTL for large var values (`them:dag:var:{workflowID}:{varName}`) was previously
set to 1 hour. A `CanvasAgentWorkflow` with a `HumanWait` node may pause for up to the
`WorkflowExecutionTimeout` (12 min by default). But if a long workflow contains multiple
HumanWait nodes and the timeout is extended, or if the key is written early and read by
a later node, the TTL must exceed the maximum possible elapsed time.

**Policy**: TTL = `max(30 min, 2 × DAGWorkflowTimeout)`. With default 12-min workflow
timeout → TTL = 30 min (safe). If `DAGWorkflowTimeout` is extended to, say, 2 hours
(for complex HumanWait agents), TTL must be extended to 4 hours to match.

`DAGVarStore` reads `DAGWorkflowTimeout` from config at startup and computes the TTL
dynamically: `ttl = max(30*time.Minute, 2*workflowTimeout)`.

---

## 4. Component map — files to create or change

### 4.1 New files

| File | Purpose |
|---|---|
| `go/internal/temporal/canvas_workflow.go` | `CanvasAgentWorkflow` — top-level Temporal workflow; orchestrates DAG fan-out/join using `workflow.Go` + `workflow.Await` |
| `go/internal/temporal/canvas_activities.go` | `CanvasAgentActivities` struct + `ExecuteStepActivity` — calls `agentgen.ExecuteNodeForActivity` per node; loads credentials from DB |
| `go/internal/temporal/temporal_executor.go` | `TemporalExecutor` — implements `agentgen.ExecutionBackend`; starts `CanvasAgentWorkflow`, blocks on result, cancels on ctx done |
| `go/cmd/dag-worker/main.go` | `them-dag-worker` binary — connects Temporal, registers `CanvasAgentWorkflow` + `ExecuteStepActivity` on `"canvas-dag-nodes"`, wires dependencies |
| `go/internal/agentgen/node_executor.go` | `ExecuteNodeForActivity(ctx, ic, node, scopedVars) (outputVars, nextOverride, resultText, error)` — narrow exported adapter; no Temporal SDK import |
| `go/internal/temporal/canvas_workflow_test.go` | Conformance tests CT-1..CT-10 using `testsuite.WorkflowTestSuite` |

### 4.2 Narrow agentgen adapter

```go
// go/internal/agentgen/node_executor.go
// No Temporal SDK import. Called by CanvasAgentActivities.

type NodeExecutionInput struct {
    Node     PlanNode     // the node to execute
    Vars     PipelineVars // scoped inputs (only declared node.Inputs keys)
}

type NodeExecutionOutput struct {
    Vars         PipelineVars // scoped outputs (only declared node.Outputs keys)
    NextOverride string       // branch routing override; empty if none
    ResultText   string       // non-empty if node is a terminal step
    ResultMT     string
}

func ExecuteNodeForActivity(
    ctx      context.Context,
    interp   *Interpreter,
    ic       *InvocationContext,
    input    NodeExecutionInput,
) (NodeExecutionOutput, error)
```

`ExecuteNodeForActivity` calls `interp.executeStep` exactly as `LocalExecutor.execNode`
does — no logic is duplicated.

### 4.3 Changed files

| File | Change |
|---|---|
| `go/internal/agentgen/spec.go` | Add `ExecutionBackend string` to `AgentSpec` |
| `go/internal/agentgen/executor.go` | No change to interface |
| `go/cmd/agent-runtime/main.go` | Add Temporal client init; add `execution_backend` branch; wire `TemporalExecutor`; add cancel-on-ctx-done |
| `go/internal/admin/service/agent_definitions_publish.go` | Copy `execution_backend` from canvas definition into `AgentSpec` |
| `go/internal/config/config.go` | Add `DAGWorkerTaskQueue` (default `"canvas-dag-nodes"`), `DAGWorkerConcurrency`, `DAGWorkflowTimeout` |
| `go/TEST_INDEX.md` | New S1-75 section for canvas_workflow_test.go |
| `docker-compose.yml` | New `them-dag-worker` service |
| `Dockerfile.dag-worker` | New — mirrors `Dockerfile.agent-runtime` structure |

---

## 5. Data model through Temporal history

### 5.1 CanvasAgentWorkflow input

```go
type CanvasAgentWorkflowInput struct {
    Plan    agentgen.ExecutionPlan `json:"plan"`    // compiled DAG — no secrets
    Initial agentgen.PipelineVars  `json:"initial"` // seed vars: {"input": userText} only
    IC      ActivityIC             `json:"ic"`      // credential-safe
}

// ActivityIC — never contains secrets
type ActivityIC struct {
    TenantID      string `json:"tenant_id"`
    ApplicationID string `json:"application_id"`
    AgentID       string `json:"agent_id"`
    BindingID     string `json:"binding_id"`
}
```

### 5.2 ExecuteStepActivity input and output

```go
type StepActivityInput struct {
    Node  agentgen.PlanNode    `json:"node"`  // one node from the plan
    Vars  agentgen.PipelineVars `json:"vars"` // scoped: only keys in node.Inputs
    IC    ActivityIC            `json:"ic"`
}

type StepActivityOutput struct {
    Vars         agentgen.PipelineVars `json:"vars"`          // scoped: only keys in node.Outputs
    NextOverride string                `json:"next_override"` // branch routing; empty if none
    ResultText   string                `json:"result_text"`   // non-empty if terminal step
    ResultMT     string                `json:"result_mt"`
}
```

The workflow reconstructs `PipelineVars` by merging `StepActivityOutput.Vars` into
a workflow-local accumulator after each node completes. It passes only the projected
subset of that accumulator (filtered by the next node's declared `Inputs`) to the
next `ExecuteStepActivity` call.

Large values (LLM response > 64 KB): store in Redis under
`them:dag:var:{workflowID}:{varName}` with TTL = 30 min (dynamic: `max(30m, 2×DAGWorkflowTimeout)`
per §3b.8); pass the key string as the var value; activities read/write via a `DAGVarStore`
interface (injectable, so tests do not need Redis).

### 5.3 CanvasAgentWorkflow output

```go
type CanvasAgentWorkflowOutput struct {
    ResultText string `json:"result_text"`
    ResultMT   string `json:"result_mt"`
}
```

---

## 6. Task queues

| Queue | Workers | Purpose |
|---|---|---|
| `"them-orchestration-go"` (existing) | `them-go-worker` | `OrchestrationWorkflow` + `RunOrchestratorActivity` — unchanged |
| `"them-orchestration"` (existing) | Python legacy | Legacy Python orchestration — unchanged |
| `"canvas-dag-nodes"` (new) | `them-dag-worker` | `CanvasAgentWorkflow` + `ExecuteStepActivity` |

`them-agent-runtime` does **not** poll any task queue. It only holds a
`temporal.Client` to start workflows and query their results.

---

## 7. Cancellation and failure behavior (exact)

### 7.1 Normal cancellation path

1. `RunOrchestratorActivity` context is cancelled (workflow cancel or timeout).
2. The HTTP call from `agentregistry.InvokeForRun()` to `them-agent-runtime` is cancelled.
3. `them-agent-runtime` HTTP handler detects `ctx.Done()` while blocked in `workflowRun.Get(ctx, &out)`.
4. Handler calls `client.CancelWorkflow(background5s, workflowID, runID)`.
5. Temporal delivers a cancel request to `CanvasAgentWorkflow`.
6. In-flight `ExecuteStepActivity` calls receive context cancellation from Temporal.
7. `CanvasAgentWorkflow` returns `temporal.CanceledError`.
8. Handler returns the original `ctx.Err()` to the caller.

### 7.2 Activity failure path

1. `ExecuteStepActivity` returns an error.
2. Temporal retries per the retry policy (see §8).
3. On final retry exhaustion: Temporal marks the activity as failed; the workflow
   coroutine that called it gets the error.
4. The workflow cancels all sibling `workflow.Go` coroutines via a shared
   `workflow.Channel` error signal (same semantics as `cancel()` in `LocalExecutor`).
5. `CanvasAgentWorkflow` returns the causal error (first non-canceled error, matching
   `LocalExecutor.drainFirstCausalError` semantics).
6. `workflowRun.Get(...)` in `them-agent-runtime` returns the error.
7. HTTP handler returns 500 to `agentregistry.InvokeForRun()`.
8. Orchestrator records a failed run step.

### 7.3 `them-dag-worker` crash

Temporal detects the missing heartbeat and reschedules in-flight activities on
another available `them-dag-worker` replica. No data is lost — Temporal history is
the source of truth. `CanvasAgentWorkflow` resumes from the last completed activity.

### 7.4 `them-agent-runtime` crash mid-wait

The `CanvasAgentWorkflow` continues running independently. When `them-agent-runtime`
restarts and `RunOrchestratorActivity` retries (Temporal retries the activity),
the new invocation uses the stable `"canvas:{agentID}:{invocationID}"` Workflow ID
from §3b.1. If the workflow is still running, `ExecuteWorkflow` returns
`AlreadyStarted` and the caller re-attaches via `client.GetWorkflow(ctx, workflowID, "")`.
The orphaned previous workflow (if any) eventually times out (`WorkflowExecutionTimeout`,
default 12 min). **Never include the activity attempt number in the Workflow ID** — that
would start a new workflow on each retry instead of re-attaching to the running one.

### 7.5 `ErrContractViolation`

Returned as `temporal.NewNonRetryableApplicationError("ContractViolation", ...)`.
Temporal does not retry; the workflow receives the error immediately and fails.

---

## 8. Retry and timeout policy

| Node type | MaxAttempts | InitialInterval | BackoffCoeff | MaxInterval | NonRetryable |
|---|---|---|---|---|---|
| `llm`, `http`, `a2a_call`, `mcp_call` | 2 | 2s | 2.0 | 30s | `ContractViolation`, `InvalidConfig`, `PermissionDenied` |
| `input`, `transform`, `response`, `branch` | 1 | — | — | — | all (deterministic) |
| `human_wait` | N/A | — | — | — | Activity returns immediately (WaitingForHuman=true); signal-wait in workflow — no activity timeout |
| `parallel` | 1 | — | — | — | all (no-op coordinator) |
| `loop` | 1 per iteration | — | — | — | each iteration body activity has its own policy |

`CanvasAgentWorkflow` `WorkflowExecutionTimeout`: configurable via `DAG_WORKFLOW_TIMEOUT`
env (default `"12m"`). Set low to prevent orphan workflows consuming Temporal quota.

---

## 9. JoinMode → Temporal workflow primitives

| `LocalExecutor` | `CanvasAgentWorkflow` equivalent |
|---|---|
| `sync.WaitGroup` fan-out | `workflow.Go` coroutine per branch |
| `joinState.arrive()` mutex | workflow-local `map[joinID]map[predID]PipelineVars`; no mutex needed (single-threaded workflow coroutine scheduler) |
| `JoinWaitAll` | `workflow.Await(ctx, func() bool { return len(arrived[id]) == len(node.JoinOf) })` |
| `JoinBranchMerge` | `workflow.Await(ctx, func() bool { return len(arrived[id]) >= 1 })`; subsequent arrivals update map but workflow already continued |
| `sharedResult.setIfEmpty` | workflow-local `*CanvasAgentWorkflowOutput`; first coroutine to set it wins |
| `cancel()` on error | shared `workflow.Channel`; any coroutine sends error; all others select on it and return |
| `drainFirstCausalError` | workflow-local `firstErr`; set once; prefer non-CanceledError |
| `nextStepOverride` | returned as `StepActivityOutput.NextOverride`; read by the workflow coroutine after activity completes |

All `workflow.Await` calls are deterministic — Temporal replays them exactly.

---

## 10. DB and config changes

### No new DB column for `execution_backend`

`execution_backend` lives only in `AgentSpec.ExecutionBackend` (the `spec` JSONB
column in `agent_runtime_specs`). This is the single source of truth. It is set by
the compiler at publish time and read by `them-agent-runtime` at invocation time
from the already-loaded `AgentSpec`. No separate column is added.

### Migration 048 — reserved

Migration `048` is reserved. It will be used only if Phase 4 introduces a new table
(e.g. `canvas_dag_runs` for per-node audit records). If no schema change is needed,
048 is left unused until the next unrelated migration.

### Config additions — `go/internal/config/config.go`

```go
DAGWorkerTaskQueue   string // env: DAG_WORKER_TASK_QUEUE,   default "canvas-dag-nodes"
DAGWorkerConcurrency int    // env: DAG_WORKER_CONCURRENCY,  default 20
DAGWorkflowTimeout   string // env: DAG_WORKFLOW_TIMEOUT,    default "12m"
```

### docker-compose additions

```yaml
them-dag-worker:
  build:
    context: .
    dockerfile: Dockerfile.dag-worker
  environment:
    - TEMPORAL_ENABLED=true
    - TEMPORAL_HOST_PORT=temporal:7233
    - DAG_WORKER_TASK_QUEUE=canvas-dag-nodes
    - DAG_WORKER_CONCURRENCY=20
    - DATABASE_URL=${DATABASE_URL}
    - REDIS_URL=${REDIS_URL}
  depends_on:
    - them-postgres
    - them-redis
    - temporal
  profiles: [temporal]
```

`them-agent-runtime` additions:
```yaml
environment:
  - TEMPORAL_ENABLED=true
  - TEMPORAL_HOST_PORT=temporal:7233
  - DAG_WORKER_TASK_QUEUE=canvas-dag-nodes  # only used to start/cancel workflows
```

---

## 11. Conformance test suite

`go/internal/temporal/canvas_workflow_test.go` uses `testsuite.WorkflowTestSuite`
(in-process, no external Temporal required). The same test table runs against both
`LocalExecutor` and `CanvasAgentWorkflow` via a shared interface:

```go
type conformanceExecutor interface {
    Execute(ctx context.Context, ic *agentgen.InvocationContext, plan *agentgen.ExecutionPlan, initial agentgen.PipelineVars) (*agentgen.ExecutionResult, error)
}
```

`LocalExecutor` already implements `agentgen.ExecutionBackend` which matches this
shape. `TemporalExecutor` implements the same interface. Tests instantiate both and
run the same assertions.

### Test scenarios (CT-1..CT-10)

| ID | Scenario | Assertion |
|---|---|---|
| CT-1 | Linear chain A→B→C | Result from C; A/B/C execute in order |
| CT-2 | Parallel fan-out A→{B,C}→D (`JoinWaitAll`) | Both B and C execute; D receives merged vars |
| CT-3 | Branch true path A→branch→B→D (`JoinBranchMerge`) | B executes; C does not; D receives B's vars |
| CT-4 | Branch false path A→branch→C→D | C executes; B does not |
| CT-5 | Node error propagates and cancels siblings | Causal error returned; sibling cancelled |
| CT-6 | Context cancellation mid-DAG | All in-flight nodes stop; `ctx.Err()` returned |
| CT-7 | `JoinBranchMerge` — second arm dropped | D continues with first arrival vars only |
| CT-8 | Empty plan | Returns error immediately |
| CT-9 | `ErrContractViolation` — non-retryable | Error returned; non-retryable in Temporal path |
| CT-10 | `StepResponse` result propagation | `ExecutionResult.Text` matches `from_var` value |

---

## 12. Phased implementation plan

### Phase 4-A — Adapter and serialization

**Scope:** The `agentgen` adapter and data types only. No Temporal SDK dependency in `agentgen`.

1. Add `ExecutionBackend string` to `AgentSpec` in `spec.go`.
2. Add `ActivityIC`, `StepActivityInput`, `StepActivityOutput`, `NodeExecutionInput`, `NodeExecutionOutput` types.
3. Implement `ExecuteNodeForActivity` in `go/internal/agentgen/node_executor.go`.
4. Update compiler (`agent_definitions_publish.go`) to copy `execution_backend` from canvas doc to `AgentSpec`.
5. Unit-test `ExecuteNodeForActivity` for each node type with `MockInterpreter`.
6. Run `go test ./internal/agentgen/...` — must be green.

**Files:** `spec.go`, `node_executor.go`, `agent_definitions_publish.go`.
**Risk:** Low. No Temporal SDK; no workflow code.

### Phase 4-B — `CanvasAgentWorkflow` with conformance tests

**Scope:** Implement the workflow and activities in `internal/temporal/`.

1. Implement `ExecuteStepActivity` in `canvas_activities.go` (calls `ExecuteNodeForActivity`; loads credentials from DB via `BindingID`).
2. Implement `CanvasAgentWorkflow` in `canvas_workflow.go` (fan-out via `workflow.Go`, join via `workflow.Await`, error propagation via shared channel).
3. Write conformance tests CT-1..CT-10 in `canvas_workflow_test.go` using `WorkflowTestSuite`.
4. Run `go test -race ./internal/temporal/...` — must be green.

**Files:** `canvas_activities.go`, `canvas_workflow.go`, `canvas_workflow_test.go`.
**Risk:** Medium. `workflow.Await` deadlock possible if join counter never increments — guard with `workflow.WithTimeout` inside the await. Test CT-5 (error path) specifically exercises this.

### Phase 4-C — `TemporalExecutor` and `them-dag-worker`

**Scope:** Wire everything end-to-end.

1. Implement `TemporalExecutor` in `internal/temporal/temporal_executor.go` (start workflow, block, cancel on ctx done — exact code from §3.3).
2. Implement `go/cmd/dag-worker/main.go` (registers `CanvasAgentWorkflow` + `ExecuteStepActivity` on `"canvas-dag-nodes"`).
3. Add Temporal client init to `cmd/agent-runtime/main.go`; add `execution_backend` branch.
4. Add `Dockerfile.dag-worker` and `them-dag-worker` service to `docker-compose.yml`.
5. Add config vars to `config.go`.
6. Run conformance tests against real Temporal dev server (`docker compose --profile temporal`).
7. Live smoke test: publish one canvas agent with `execution_backend: "temporal"`, invoke it, verify Temporal UI shows node-level activity history.

**Files:** `temporal_executor.go`, `cmd/dag-worker/main.go`, `cmd/agent-runtime/main.go`, `Dockerfile.dag-worker`, `docker-compose.yml`, `config.go`.
**Risk:** Medium. Temporal round-trip adds ~50–200 ms per activity. LLM nodes dominate latency so this overhead is acceptable. Measure before enabling on latency-sensitive paths.

### Phase 4-D — Frontend publish toggle

**Scope:** Expose `execution_backend` in the canvas builder publish panel.

1. Add `execution_backend` field to `AgentDefinitionDoc` in `frontend/src/lib/api.ts`.
2. Add a "Execution backend" toggle (Local / Temporal) in `BuilderTopBar.tsx`.
3. Round-trip through `handlePublish` → compiler → `AgentSpec`.
4. Default: `"local"` — no behavior change for existing agents.

**Files:** `frontend/src/lib/api.ts`, `BuilderTopBar.tsx`.
**Risk:** Low. UI only; no execution path changes.

---

## 13. Open decisions

| # | Question | Recommendation |
|---|---|---|
| OD-1 | Credential loading in activity | Activity receives only `BindingID`; re-loads credentials from DB. Matches `TaskState` today. Nothing secret in Temporal history. |
| OD-2 | Large var values (LLM output > 64 KB) | Store in Redis `them:dag:var:{workflowID}:{varName}` TTL 1h; pass Redis key as var value. Inject `DAGVarStore` interface (Redis impl + in-memory test impl). |
| OD-3 | `HumanWait` in `CanvasAgentWorkflow` | `ExecuteStepActivity` for `human_wait` blocks on a Temporal `workflow.Channel` signal sent by the existing HITL signaler. The signal channel name must include `workflowID` so the signaler targets the correct workflow run. Design separately in Phase 4-B. |
| OD-4 | `StepLoop` in workflow | Implement as a `for` loop in the workflow coroutine. Each iteration body schedules activities. `MaxIterations` enforced by the workflow. Each iteration is a deterministic Temporal replay step. |
| OD-5 | Default `execution_backend` for new agents | `"local"` (empty string treated as local) until Phase 4-C is live-verified. |
| OD-6 | Search Attributes for `CanvasAgentWorkflow` | Add `ParentRunID`, `TenantID`, `AgentID` as custom Search Attributes only if the Temporal namespace is pre-configured. Otherwise Memo is sufficient for the initial deployment. |
| OD-7 | Orphan workflow from `agent-runtime` crash | Workflow ID includes activity attempt number. Short `WorkflowExecutionTimeout` (15 min default) ensures orphans self-terminate. |

---

## 14. What does NOT change

- `LocalExecutor` is preserved unchanged. All existing canvas agents use it.
- `OrchestrationWorkflow` and `RunOrchestratorActivity` are unchanged.
- `agentregistry.InvokeForRun()` is unchanged — still posts to `them-agent-runtime`.
- All 12 node `Execute` functions in `nodes.go` are unchanged — `ExecuteNodeForActivity` calls them through the existing interpreter dispatch.
- Existing tests S1-54, S1-72, S1-73, S1-74 continue to pass.
- `them-agent-runtime` HTTP API is unchanged — callers see no difference.

---

## 15. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Temporal history grows large with many nodes or large LLM outputs | Medium | Scoped input/output deltas + Redis var store for large values (OD-2) |
| `workflow.Await` deadlock if join counter never increments | Medium | Safety timeout inside `workflow.Await`; CT-5 (error path) tests this |
| Orphan workflows from `agent-runtime` crash | Low-Medium | Short `WorkflowExecutionTimeout` (12 min); re-attach via stable Workflow ID on retry (§3b.1) |
| `them-dag-worker` crash mid-activity | Low | Temporal reschedules on next available replica; history is the source of truth |
| Per-node Temporal overhead adds latency | Low | ~50–200ms per activity; LLM nodes dominate; measure in Phase 4-C before enabling |
| `HumanWait` signal routing — must target `CanvasAgentWorkflow`, not parent | High if unhandled | Signal channel name includes `workflowID`; design explicitly in Phase 4-B (OD-3) |
| Credentials re-loaded from DB per activity — DB load increases | Low | Same pattern as today; credentials are cached in `them-go-bridge` token cache |

---

## 16. Implementation notes — confirmed invariants (Phase 4-B)

The following design invariants were verified during Phase 4-B implementation
(`canvas_workflow.go`, `canvas_activities.go`, `canvas_workflow_test.go`, commit 68da87c).

### 16.1 Every node runs through ExecuteNodeForActivity → Interpreter dispatch

`ExecuteStepActivity` calls `agentgen.ExecuteNodeForActivity(ctx, interp.Clone(), ic, ...)`
for every non-human_wait node. `ExecuteNodeForActivity` calls `interp.executeStep(ctx, ic, node, vars)`,
which dispatches to the same switch-on-type that `LocalExecutor` uses. No node bypasses the
`Interpreter`. **Future Go registry nodes registered in the same switch require no new Activity** —
they are automatically dispatched via the existing `executeStep` path.

### 16.2 Runtime secrets are loaded by the worker, never stored in Temporal history

`CanvasAgentWorkflowInput.IC` carries only `{TenantID, ApplicationID, AgentID, BindingID}` —
no credentials. The `dag-worker` (Phase 4-C) implements `ContextLoader.Load(ctx, ic)` which
queries the DB (scoped by all four IDs per §3b.7) to retrieve the full `InvocationContext` at
activity execution time. The `InvocationContext` — including `Credentials` — exists only in
activity memory and is never written to Temporal history.

### 16.3 Future Go registry nodes need no new Activity

The `Interpreter.executeStep` switch dispatches on `node.Type`. Adding a new Go node type to
the registry adds a new case to that switch. `ExecuteStepActivity` calls `ExecuteNodeForActivity`
which calls `executeStep` — so the new node type is automatically available under
`ExecuteStepActivity` without registering a new Temporal Activity. The only required update is
`activityOptionsForNode`: add the new node type to the appropriate `case` to assign the correct
retry policy (idempotent → `MaxAttempts:1`; side-effectful → `MaxAttempts:2`).

### 16.4 Authoritative timeout and retry values (ground truth: code constants in canvas_workflow.go)

| Constant | Value | Source |
|---|---|---|
| `dagWorkflowTimeout` | 12 min | `canvas_workflow.go:27` |
| `stepActivityTimeout` (`StartToCloseTimeout`) | 5 min | `canvas_workflow.go:30` |
| Redis large-value TTL | 30 min min (`max(30m, 2×dagWorkflowTimeout)`) | §3b.8 policy |
| `MaximumAttempts` — LLM/HTTP/A2A/MCP | 2 | `activityOptionsForNode` (`canvas_workflow.go`) |
| `MaximumAttempts` — pure nodes (input/transform/response/branch/parallel) | 1 | `activityOptionsForNode` |
| Non-retryable error types | `ContractViolation`, `InvalidConfig`, `PermissionDenied` | `activityOptionsForNode` |
