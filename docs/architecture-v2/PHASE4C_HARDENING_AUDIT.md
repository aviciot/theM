# Phase 4-C Hardening Audit Report
# Last updated: 2026-08-30 (revised 2026-08-30 — items 1/2/3/4/5/6 re-verified and fixed)

Evidence-based verification of all 7 production blockers and 5 advisory items
identified during the Phase 4-C hardening review. Every finding is cited against
actual on-disk file and line numbers confirmed in this session.

---

## Blockers (7) — All Confirmed Fixed

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

Three tests (all compile clean under `go build -tags=integration`):

| Test | What it covers |
|---|---|
| `TestTemporalConnect_Unavailable` | `Connect` to port 19999 returns error — fail-closed without live Temporal |
| `TestTemporalExecutor_EmptyPlan_Integration` | nil-plan guard fires before any RPC with nil client |
| `TestTemporalExecutor_LiveDAG` | Full E2E; gated by `THEM_TEMPORAL_E2E=true` — skipped in unit CI |

Run integration tests:
```bash
THEM_TEMPORAL_E2E=true TEMPORAL_HOST_PORT=localhost:7233 \
  go test -tags=integration -v ./internal/temporal/...
```

**Status: FIXED ✅**

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

### Advisory E — 12-minute workflow timeout vs HumanWait nodes ⚠️

**Location:** `go/internal/temporal/canvas_workflow.go` line 26:

```go
dagWorkflowTimeout = 12 * time.Minute
```

`canvas_workflow.go` line 204-205:
```go
sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+node.StepID)
var humanVars agentgen.PipelineVars
sigCh.Receive(ctx, &humanVars) // blocks until signal arrives
```

**The gap:** A canvas agent containing a `human_wait` node will be terminated by
Temporal with `WorkflowExecutionTimedOut` after 12 minutes if the human does not
respond. This is a correctness issue — HITL is silently broken if the user takes
longer than 12 minutes.

**Recommended fix:** `TemporalExecutor.Execute` should inspect the plan for
`human_wait` nodes before submitting. If any are found, use a longer timeout
(e.g., `24h` or `7d`). Or expose `workflow_timeout_seconds` in `AgentSpec` /
`ExecutionBackend` config so the agent author can declare the expected wait.

Example in `temporal_executor.go`:
```go
timeout := e.workflowTimeout
for _, node := range plan.Nodes {
    if node.Type == agentgen.StepHumanWait {
        timeout = 24 * time.Hour
        break
    }
}
opts := client.StartWorkflowOptions{
    WorkflowExecutionTimeout: timeout,
    ...
}
```

**Urgency: HIGH** — must fix before shipping any canvas agent with a HumanWait
node to production. Current behavior silently drops the HITL session at 12 min.

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
| B10 | `docker-compose.dev.yml` — agent-runtime gets `TEMPORAL_ENABLED=true` with temporal profile | Fixed ✅ |
| A | N+1 DB queries per node | Advisory — low urgency |
| B | Temporal payload/history growth | Advisory — monitor |
| C | DB pool < activity concurrency | Advisory — medium urgency |
| D | No health endpoint in dag-worker | Advisory — required before prod |
| E | 12-min timeout breaks HumanWait | **Advisory — HIGH, fix before HITL ship** |
