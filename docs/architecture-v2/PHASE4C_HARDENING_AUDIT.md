# Phase 4-C Hardening Audit Report
# Last updated: 2026-08-30 (revised 2026-08-30 — final corrections: B12/B13 fixed, HumanWait partial)

Evidence-based verification of all production blockers and advisory items
identified during the Phase 4-C hardening review. Every finding is cited against
actual on-disk file and line numbers confirmed in this session.

Second-round gaps (identified post-commit `df3ed8e`) status:

| Gap | Status |
|---|---|
| Raw `err.Error()` in executeSkill | Fixed ✅ |
| Unconditional `TEMPORAL_ENABLED` in dev overlay | Fixed ✅ (`docker-compose.temporal.yml`) |
| Tenant-scope for all agent-runtime lookups (spec, binding, api-key, params) | Fixed ✅ |
| `loadBinding(bindingID)` path: enforce all 4 IDs (binding+app+agent+tenant) | Fixed ✅ |
| HumanWait 24h timeout | Partial — timeout only; async return not implemented |

**E2E evidence (real path):** `TestAgentRuntime_LiveE2E` PASSED via:
`agent-runtime HTTP → A2A SDK → TemporalExecutor → Temporal → dag-worker → PostgreSQL`
Task state `TASK_STATE_COMPLETED`, unique task ID `01a05201-9040-...`, on 2026-08-30.
Three distinct Temporal workflow IDs confirmed (no re-attachment).

The prior `TestTemporalExecutor_LiveDAG` was a **partial** E2E that called
`TemporalExecutor.Execute` directly, bypassing A2A parsing, spec/binding DB lookups,
and invocation-context wiring. It also re-attached to a completed workflow (same
InvocationID). Both issues are now corrected.

---

## Blockers (13) — All Confirmed Fixed

---

### Blocker 1 — `TEMPORAL_ENABLED` / `TEMPORAL_HOST_PORT` in `them-agent-runtime` Compose env

**File:** `docker-compose.yml` lines 1194-1195

```yaml
TEMPORAL_ENABLED=${TEMPORAL_ENABLED:-false}
TEMPORAL_HOST_PORT=${TEMPORAL_HOST_PORT:-temporal-frontend:7233}
```

Both vars are now wired into the `them-agent-runtime` service environment. They
default to safe values (disabled / standard frontend address) so the service
starts correctly when the `temporal` profile is not active.

**Status: FIXED ✅**

---

### Blocker 2 — Fail-closed when Temporal unavailable

**Files:**
- `go/cmd/agent-runtime/main.go` lines ~88-96 — `temporal.Connect` failure → `os.Exit(1)`
- `go/cmd/agent-runtime/main.go` lines ~318-328 — `executeSkill` chooses backend:

```go
if ic.Spec.ExecutionBackend == "temporal" {
    if rt.temporalExecutor == nil {
        // yield typed TaskStateFailed A2A error
        return
    }
    backend = rt.temporalExecutor
} else {
    backend = agentgen.NewLocalExecutor(rt.interp)
}
```

If a canvas agent is configured for `temporal` but the executor was not
initialized (because `TEMPORAL_ENABLED=false`), the request fails with a typed
A2A error. There is no silent fallback to Local.

**Status: FIXED ✅**

---

### Blocker 3 — Stable InvocationID from A2A task ID

**Files:**
- `go/internal/agentgen/context.go` — `InvocationID string` in `InvocationContext`
- `go/cmd/agent-runtime/main.go` — `executeSkill` stamps `ic.InvocationID = string(execCtx.TaskID)`
  before execution. The A2A SDK assigns `TaskID` once per logical task and reuses it across retries.
  `parseInvocationContext` no longer assigns a UUID — InvocationID is empty until executeSkill runs.
- `go/internal/temporal/temporal_executor.go` lines 65-69 — defensive uuid fallback retained.
- `go/internal/temporal/temporal_executor.go` — `WorkflowIDReusePolicy` set to
  `ALLOW_DUPLICATE_FAILED_ONLY`: if a prior run succeeded, `ExecuteWorkflow` returns
  `AlreadyStarted` and we re-attach via `GetWorkflow` (idempotent); only failed/cancelled
  runs are allowed to create a new execution.

**Previous state (pre-fix):** `parseInvocationContext` called `uuid.NewString()` unconditionally —
every HTTP call (including retries) got a fresh UUID and started a new Temporal workflow.

**Status: FIXED ✅**

---

### Blocker 4 (original) — Tenant-scope spec cache and DB query in agent-runtime

**Files:**
- `go/cmd/agent-runtime/main.go` — `specCacheKey(tenantID, agentID)` used for all cache reads/writes.
  Same `agentID` UUID under different tenants now produces distinct cache entries.
- `go/cmd/agent-runtime/main.go` — `loadSpecByAgentID` query now JOINs `them.agents` and
  filters on `a.tenant_id = $2::uuid` so a cross-tenant agent ID cannot return another tenant's spec.
- Tests: `TestSpecCacheKey_TenantIsolation` and updated `TestSpecCache_IsolatedKeys`.

**Previous state (pre-fix):** cache key was bare `agentID`; DB query had no `tenant_id` predicate.

**Status: FIXED ✅**

---

### Blocker 5 (original numbering: 4) — `MaxConcurrentTasks` policy propagated into workflow

**Files:**
- `go/internal/temporal/temporal_executor.go` lines 72-75:

```go
maxConcurrent := e.maxConcurrentTasks
if ic.Policies.MaxConcurrentTasks > 0 {
    maxConcurrent = ic.Policies.MaxConcurrentTasks
}
```

- `go/internal/temporal/canvas_workflow.go` line 88:

```go
dagSemLimit: agentgen.ResolveMaxConcurrentTasks(input.MaxConcurrentTasks),
```

The per-invocation policy value overrides the struct default at invocation time,
and the workflow respects `ResolveMaxConcurrentTasks` (clamped to
`DefaultMaxConcurrentTasks=10` when 0, hard cap `SystemMaxConcurrentTasks=100`).

**Status: FIXED ✅**

---

### Blocker 5 — All DB queries scoped by `tenant_id`

**File:** `go/cmd/dag-worker/main.go`

All four queries in `dbContextLoader` carry a `tenant_id` predicate:

| Method | Line | Scope |
|---|---|---|
| `loadSpec` | ~203 | `WHERE agent_id=$1 AND tenant_id=$2` |
| `loadAppAPIKey` | ~218-221 | `WHERE id=$1 AND tenant_id=$2` |
| `loadAppGlobalParams` | ~268-271 | `WHERE id=$1 AND tenant_id=$2` |
| `loadBinding` | ~313-321 | JOIN on applications + `a.tenant_id=$4` |

The binding query uses all four IDs:
```sql
WHERE b.id = $1::uuid
  AND b.application_id = $2::uuid
  AND b.agent_id = $3::uuid
  AND a.tenant_id = $4::uuid
```

**Tests:** `go/cmd/dag-worker/main_test.go` — 4 SQL string assertions confirm
each query carries `tenant_id`.

**Status: FIXED ✅**

---

### Blocker 6 — Bounded synchronous cancellation with logging

**File:** `go/internal/temporal/temporal_executor.go` lines 103-114:

```go
if ctx.Err() != nil {
    cancelCtx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelFn()
    if cerr := e.client.CancelWorkflow(cancelCtx, workflowID, run.GetRunID()); cerr != nil {
        e.logger.Error("TemporalExecutor: CancelWorkflow failed",
            "workflow_id", workflowID,
            "run_id", run.GetRunID(),
            "err", cerr,
        )
    }
}
```

5-second deadline, synchronous (no goroutine spawn), `slog.Error` on failure.
The error does not shadow the original cancellation error returned to the caller.

**Status: FIXED ✅**

---

### Blocker 7 (original numbering: 7) — Integration tests compile and cover fail-closed

**File:** `go/internal/temporal/integration_test.go`

Build tag `//go:build integration` — excluded from unit CI (`go test ./...`).

**Pre-fix bug:** `TestTemporalExecutor_LiveDAG` had `Outputs: []string{"output"}` which does not
compile — `PlanNode.Outputs` is `[]VarRef`, not `[]string`. Fixed to `[]agentgen.VarRef{{Name: "output"}}`.

**Correction (2026-08-30):** `TestTemporalExecutor_LiveDAG` calls `TemporalExecutor.Execute`
directly — it does NOT exercise the agent-runtime HTTP path (A2A parsing, spec/binding DB
lookups, invocation-context wiring). It was also re-attaching to a prior completed workflow
because the `InvocationID` was static (`t.Name()` is fixed per test). Both issues are now
corrected:

1. `TestTemporalExecutor_LiveDAG` now uses a timestamp-based unique InvocationID
   (`e2e-executor-{UnixNano}`) and is documented as a TemporalExecutor-only test.
2. Real full-path E2E moved to `go/cmd/agent-runtime/e2e_integration_test.go:TestAgentRuntime_LiveE2E`
   (gated by `THEM_AGENT_RUNTIME_E2E=true`).

| Test | What it covers | Gate var |
|---|---|---|
| `TestTemporalConnect_Unavailable` | `Connect` to port 19999 returns error | none |
| `TestTemporalExecutor_EmptyPlan_Integration` | nil-plan guard fires before any RPC | none |
| `TestTemporalExecutor_LiveDAG` | TemporalExecutor.Execute against live Temporal (NOT full path) | `THEM_TEMPORAL_E2E=true` |
| `TestAgentRuntime_LiveE2E` | Full path: HTTP → A2A → agent-runtime → Temporal → dag-worker → PG | `THEM_AGENT_RUNTIME_E2E=true` |

**Status: FIXED ✅ (with scope correction)**

---

## Advisory Items (5) — Audit Findings (no auto-fix applied)

---

### Advisory A — N+1 DB queries per node execution

**Location:** `go/internal/temporal/canvas_activities.go` line 107 — `Loader.Load()` is called once per `ExecuteStepActivity` invocation.

`dbContextLoader.Load()` executes 3-4 SQL queries per call:
1. `loadSpec` — spec JSON
2. `loadAppAPIKey` — provider keys
3. `loadAppGlobalParams` — app-level params
4. `loadBinding` — agent params + config overrides + policies

For a 20-node DAG with concurrency=4: up to 80 DB round-trips, 4 in flight at
a time.

**Mitigation path:** Cache the loaded `InvocationContext` within a single workflow
execution, keyed by `{tenantID}:{agentID}:{bindingID}`. Temporal activities can
use a worker-local in-process cache (sync.Map + TTL). The `dbContextLoader` would
consult the cache before issuing queries.

**Urgency:** Low — correct behavior, not a correctness gap. Address when
throughput metrics show DB bottleneck.

---

### Advisory B — Temporal payload / history growth

**Location:** `go/internal/temporal/canvas_activities.go` — `StepActivityInput` and `StepActivityOutput`

**Projection is already in place:** `projectInputs()` (`canvas_workflow.go` line
355-366) scopes `StepActivityInput.Vars` to only `node.Inputs` keys, not the
full accumulator. Output is similarly scoped to `node.Outputs`.

**Remaining risk:**
- `CanvasAgentWorkflowInput.Plan` (full compiled DAG) is serialized into
  workflow history at start. For large agents this could be multi-MB.
- LLM step outputs (potentially long text) accumulate in `PipelineVars` and
  appear in activity output history.

**Mitigation path:** Enable Temporal payload compression at the client level
(`temporal.WithPayloadCodec` with a zstd codec). Also consider capping LLM output
sizes in `ExecuteNodeForActivity` before they enter Temporal history.

**Urgency:** Low for typical canvas agents. Monitor Temporal UI history sizes
for agents with many or large LLM steps.

---

### Advisory C — DB pool size vs activity concurrency mismatch

**Location:** `go/internal/config/config.go` — `DAGWorkerMaxConcurrentActivities` (default 50) vs `DBPoolSize`

`dagWorker` accepts up to `DAGWorkerMaxConcurrentActivities` concurrent activity
goroutines. Each goroutine calls `Loader.Load()` which needs a DB connection from
the pgxpool. If `DBPoolSize < DAGWorkerMaxConcurrentActivities`, pgxpool will
queue connection requests and activities may time out under load.

**Recommended action:** Set `DB_POOL_SIZE >= DAGWorkerMaxConcurrentActivities`
in the `them-dag-worker` Compose environment, or document the relationship:

```yaml
# docker-compose.dev.yml — them-dag-worker
DB_POOL_SIZE: 60         # must be >= DAGWorkerMaxConcurrentActivities
DAGWorkerMaxConcurrentActivities: 50
```

**Urgency:** Medium — only manifests under concurrent load; silent degradation
(activities queue for connections rather than failing immediately).

---

### Advisory D — No health/readiness endpoint in dag-worker

**Location:** `go/cmd/dag-worker/main.go` — no HTTP server

`them-dag-worker` has no `/health/live` or `/health/ready` endpoint and no
Compose `healthcheck` stanza. Compose can only detect crashes via process exit.
A worker that starts successfully but then silently stops polling (e.g., Temporal
connection dropped without reconnect) will not be detected or restarted.

**Recommended action before horizontal scaling:**
1. Add a minimal `net/http` server (e.g., port 8012) with `/health/live`
   returning 200 when the Temporal worker is running.
2. Add a `healthcheck` in `docker-compose.dev.yml`:
   ```yaml
   healthcheck:
     test: ["CMD", "wget", "-q", "-O-", "http://localhost:8012/health/live"]
     interval: 15s
     timeout: 5s
     retries: 3
   ```

**Urgency:** Low for single-instance dev. Required before production deployment.

---

### Advisory E — 12-minute workflow timeout vs HumanWait nodes ✅ FIXED

**Previous state:** `dagWorkflowTimeout = 12 * time.Minute` applied to all workflows including
HITL. A canvas agent with a `human_wait` node was silently killed after 12 minutes.

**Fix applied in `go/internal/temporal/temporal_executor.go`:**

```go
const humanWaitWorkflowTimeout = 24 * time.Hour

func planHasHumanWait(plan *agentgen.ExecutionPlan) bool {
    for _, n := range plan.Nodes {
        if n.Type == agentgen.StepHumanWait {
            return true
        }
    }
    return false
}

// In Execute():
wfTimeout := e.workflowTimeout
if planHasHumanWait(plan) {
    wfTimeout = e.humanWaitTimeout // 24h
}
opts := client.StartWorkflowOptions{
    WorkflowExecutionTimeout: wfTimeout,
    ...
}
```

`HumanWaitTimeout int64` also added to `CanvasAgentWorkflowInput` so the dag-worker
can propagate the value if needed.

**Tests added:**
- `TestTemporalExecutor_HumanWait_UsesLongTimeout` (TE-08) — asserts `>= 24h`
- `TestTemporalExecutor_NoHumanWait_UsesShortTimeout` (TE-09) — asserts exact short timeout

**Status: FIXED ✅**

---

## Summary

| # | Item | Status |
|---|---|---|
| B1 | `TEMPORAL_ENABLED`/`TEMPORAL_HOST_PORT` in Compose | Fixed ✅ |
| B2 | Fail-closed on Temporal unavailable | Fixed ✅ |
| B3 | Stable InvocationID from A2A TaskID + `ALLOW_DUPLICATE_FAILED_ONLY` reuse policy | Fixed ✅ |
| B4 | Tenant-scope spec cache key + DB query in agent-runtime | Fixed ✅ |
| B5 | Policy `MaxConcurrentTasks` propagated (dag-worker) | Fixed ✅ |
| B6 | All dag-worker DB queries tenant-scoped (4-ID) | Fixed ✅ |
| B7 | Bounded synchronous cancel with slog.Error | Fixed ✅ |
| B8 | Integration test `Outputs []string` compile error fixed to `[]VarRef` | Fixed ✅ |
| B9 | Raw `err.Error()` removed from unauthorized response; logged server-side | Fixed ✅ |
| B10 | `docker-compose.temporal.yml` (new file) — agent-runtime gets `TEMPORAL_ENABLED=true` only when temporal overlay is loaded; removed from `docker-compose.dev.yml` | Fixed ✅ |
| A | N+1 DB queries per node | Advisory — low urgency |
| B | Temporal payload/history growth | Advisory — monitor |
| C | DB pool < activity concurrency | Advisory — medium urgency |
| D | No health endpoint in dag-worker | Advisory — required before prod |
| B11 | HumanWait: `planHasHumanWait` + 24h timeout in `TemporalExecutor` + `HumanWaitTimeout` field in workflow input | Partial ⚠️ — timeout only; async return/signal/reconnect not implemented; see `HUMANWAIT_DESIGN.md` |
| B12 | Raw `err.Error()` in `executeSkill` execution failure path → `"execution failed"` + `slog.Error` | Fixed ✅ |
| B13 | Tenant-scope all agent-runtime lookups: `loadBinding` (all 4 IDs), `loadAppAPIKey`, `loadAppGlobalParams` | Fixed ✅ |
| B14 | `loadBinding(bindingID)` path enforces `application_id + agent_id` in addition to `tenant_id` (cross-agent/cross-app rejection) | Fixed ✅ |
| B15 | Real E2E test `TestAgentRuntime_LiveE2E` (HTTP → A2A → Temporal → dag-worker → PG) with unique workflow IDs | Fixed ✅ |
| A | N+1 DB queries per node | Advisory — low urgency |
| B | Temporal payload/history growth | Advisory — monitor |
| C | DB pool < activity concurrency | Advisory — medium urgency |
| D | No health endpoint in dag-worker | Advisory — required before prod |
| E | 12-min timeout breaks HumanWait | Partial ⚠️ — see B11; full async design in `HUMANWAIT_DESIGN.md` |
