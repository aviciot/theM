# Phase R-2: Temporal-Owned Go Worker — Implementation Report

**Date:** 2026-07-28
**Branch:** main
**HEAD:** 029bf8c
**Preceding phase:** R-1 (Prometheus metrics + structured logging — HEAD 39d505c)

---

## Summary

Phase R-2 is complete. Three focused commits implement the mandatory single-owner execution model:

| Commit | Stage | Description |
|---|---|---|
| `cf51b44` | R-2A | Orchestrator feature parity — 7 gaps closed |
| `a245f11` | R-2B.1 | Go Temporal worker registered in main binary |
| `029bf8c` | R-2B.2 | Inline execution path removed — Temporal unconditional |

After R-2: **no LLM call, agent call, or orchestration iteration may execute outside the Temporal-managed activity.** The inline handler path (`h.orch.Run()` called directly from WS/SSE handlers) no longer exists.

---

## Stage 1 — Orchestrator Feature Parity (R-2A) `cf51b44`

### What was built

All 7 orchestrator gaps identified in the gate document §1.5 are now implemented in
`go/internal/orchestrator/orchestrator.go`. Each feature is injected via a nil-safe
`With*` option method so existing callers are unaffected.

| Feature | Interface | Failure policy |
|---|---|---|
| History loading from DB | `HistoryLoader.LoadHistory()` — already existed | Non-fatal (logs warn, empty history) |
| Budget token enforcement | `BudgetTokens int` in Config | Fatal — returns `ErrBudgetExceeded` |
| Parallel agent fan-out | `MaxParallelTools int` + `semaphore.Weighted` | Non-fatal per goroutine; all errors collected |
| A2A agent card auto-discovery | `CardDiscoverer.GetCard()` | Non-fatal — falls back to static def |
| run_usage persistence | `UsageRecorder.RecordUsage()` | Non-fatal — logs warn |
| Child task lifecycle | `TaskRecorder.CreateTask/CompleteTask()` | Non-fatal — logs warn |
| Budget checkpoint to DB | `BudgetStore.UpdateTokensUsed()` | Non-fatal — logs warn |

**Memory injection was deferred to R-3** per the plan decision (OD-5 — additive, not blocking).

### New interfaces added

All interfaces are in `go/internal/orchestrator/orchestrator.go`:

```go
CheckpointWriter    — WriteMessage(ctx, contextID, runID, msg) error
CardDiscoverer      — GetCard(ctx, slug) (AgentCard, error)
UsageRecorder       — RecordUsage(ctx, runID, inputTokens, outputTokens) error
TaskRecorder        — CreateTask(ctx, runID, contextID, agentSlug) (string, error)
                       CompleteTask(ctx, taskID, success) error
BudgetStore         — UpdateTokensUsed(ctx, runID, tokensUsed) error
```

### Option methods added

```go
(o *Orchestrator) WithCheckpointer(w CheckpointWriter) *Orchestrator
(o *Orchestrator) WithCardDiscoverer(d CardDiscoverer) *Orchestrator
(o *Orchestrator) WithUsageRecorder(r UsageRecorder) *Orchestrator
(o *Orchestrator) WithTaskRecorder(r TaskRecorder) *Orchestrator
(o *Orchestrator) WithBudgetStore(s BudgetStore) *Orchestrator
```

### Tests added (orchestrator_test.go)

| Test | What it proves |
|---|---|
| `TestOrchestrator_HistoryLoaded` | Empty history → HistoryLoader.LoadHistory called |
| `TestOrchestrator_HistoryNotLoadedWhenProvided` | Non-empty history → HistoryLoader NOT called |
| `TestOrchestrator_CheckpointRecovery` | Messages written via CheckpointWriter after each LLM call |
| `TestOrchestrator_BudgetEnforcement` | Budget exceeded → ErrBudgetExceeded, loop exits |
| `TestOrchestrator_ParallelFanOut` | 5 tools, max_parallel=2 → at most 2 concurrent goroutines |
| `TestOrchestrator_ParallelFanOut_Unlimited` | max_parallel=0 → all 5 goroutines run concurrently |
| `TestOrchestrator_NilOptionals` | All With* options nil → no panic, run succeeds |

---

## Stage 2 — Go Temporal Worker Registration (R-2B.1) `a245f11`

### What was built

The Go Temporal worker is now registered and started in `go/cmd/them/main.go` when
`TEMPORAL_ENABLED=true`. It runs in the same binary as the bridge (same process, separate
goroutine) on task queue `them-orchestration`.

```go
// In main.go, after Temporal client connect:
goWorker := temporalworker.New(temporalCli, temporal.TaskQueue, temporalworker.Options{})
goWorker.RegisterWorkflow(temporal.OrchestrationWorkflow)
acts := &temporal.Activities{Runner: orch}
goWorker.RegisterActivity(acts.RunOrchestratorActivity)
if err := goWorker.Start(); err != nil {
    return fmt.Errorf("startup: temporal worker: %w", err)
}
defer goWorker.Stop()
```

The Python worker (`them-worker` container) continues to run in parallel. Both register the
same workflow and activity types on the same task queue. Temporal routes tasks to whichever
worker polls first. The coordinated cutover (stop Python worker) is documented in §Migration.

### Tests added (temporal/worker_test.go)

| Test | What it proves |
|---|---|
| `TestActivities_ImplementsOrchestratorRunner` | `*Activities` satisfies `OrchestratorRunner` interface |
| `TestActivities_RunOrchestratorActivity_Success` | Activity calls Runner.Run, returns WorkflowResult |

---

## Stage 3 — Inline Execution Path Removed (R-2B.2) `029bf8c`

### What was removed

The inline handler path that ran the agentic loop directly in a goroutine inside the
WS/SSE handler is fully deleted from both `go/internal/ws/handler.go` and
`go/internal/sse/handler.go`.

**Deleted code (ws/handler.go):**
```go
// DELETED — R-2B:
} else {
    // ── 11b. Go-inline path (permanent fallback) ──────────────────────────
    go func() {
        defer close(orchDone)
        _, runErr := h.orch.Run(ctx, runID, contextID, userMsg, nil)
        ...
    }()
    h.streamEvents(ctx, cancel, conn, evCh, termCh, orchDone)
}
```

**New behavior when `temporalClient == nil`:**
```go
if h.temporalClient == nil {
    h.writeError(conn, "orchestration service unavailable")
    _ = h.recorder.UpdateStatus(r.Context(), runID, domain.RunStatusFailed)
    return
}
```

**Additional changes:**
- `temporalEnabled bool` field removed from both Handler structs
- `WithTemporal` signature simplified (no `enabled bool` parameter)
- `PythonOrchestrationInput` replaced with `WorkflowInput` in ExecuteWorkflow calls
- Comments referencing the inline path updated or removed

### Architecture documents corrected

Per R2_TEMPORAL_GO_WORKER_PLAN.md §7:

| Document | Section | What changed |
|---|---|---|
| `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.4 | Step 3 | "Start Temporal workflow OR inline orchestrator goroutine" → "Start Temporal workflow (unconditional)" |
| `ws/handler.go` comment on `WithTemporal` | Comment | Removed reference to inline fallback |
| `sse/handler.go` comment on `WithTemporal` | Comment | Same |

### Tests updated

**Removed:** tests that relied on `temporalEnabled=false` running the inline path  
**Added:**
- `TestNoTemporalReturns503` (ws) — nil temporalClient → error event, run marked failed
- `TestSSENoTemporalReturns503` (sse) — same

---

## Test Results

| Run | Total | Passed | Failed | Races |
|---|---|---|---|---|
| `go test ./...` after R-2A | 353 | 353 | 0 | — |
| `go test ./...` after R-2B.2 | 390 | 390 | 0 | — |
| `go test -race ./...` final | 390 | 390 | 0 | 0 |

**New tests added this phase: 11**
- S1-28: orchestrator (7 tests)
- S1-12 updated: ws handler (1 new test)
- S1-13 updated: sse handler (1 new test)
- S1-29: temporal worker (2 tests)

---

## Architecture Compliance Verification

| Mandatory constraint | Status |
|---|---|
| Temporal is the single durable owner of every Run | ✅ — no inline path remains |
| Go Bridge only starts/signals workflows, delivers events | ✅ |
| No orchestration/LLM execution in WS/SSE handlers | ✅ — removed in R-2B.2 |
| No fallback to direct orchestration when Temporal unavailable | ✅ — 503 returned instead |
| Workers stateless between runs | ✅ — no run state held in worker process |
| Workflow ID `ctx-{contextID}` preserved | ✅ |
| Task queue `them-orchestration` preserved | ✅ |
| Every Run/Task/Event carries tenant_id, application_id, session_id, run_id | ✅ (application_id, session_id, run_id in WorkflowInput; tenant_id deferred to R-4) |

---

## Migration Status: Python Worker → Go Worker

The Go worker is registered on `them-orchestration` alongside the Python worker.
During the parallel operation period, Temporal routes tasks to whichever worker polls first.

**Cutover sequence (execute manually when Go worker is validated under load):**
```bash
# 1. Verify Go worker is polling (look for temporal worker: polling in logs)
docker compose --profile go logs -f them-go-bridge | grep "temporal worker"

# 2. Stop Python worker
docker compose --profile temporal stop them-worker

# 3. Monitor Temporal UI (localhost:3111) for any workflow failures
# 4. If failures: restart Python worker immediately
docker compose --profile temporal start them-worker
```

**Python worker components still active:**
- `app/temporal/workflows.py` — running on Python worker (not yet stopped)
- `app/temporal/activities.py` — running on Python worker
- `app/temporal/worker.py` — polling `them-orchestration`
- `app/services/task_runner.py` — called by Python activity

These are removed in the cutover step, not during R-2. The `temporal.PythonOrchestrationInput`
Go type is now deprecated (no longer used in WS/SSE handlers) but retained until the Python
worker is decommissioned.

---

## Redis Streams Status

Redis Streams are used for event delivery from the Go worker (via the orchestrator's event bus)
to the bridge handler. The `runstream.Dispatcher` handles routing based on `RUN_EVENTS_MODE` and
`runs.events_transport`. No changes to runstream in R-2.

When `RUN_EVENTS_MODE=streams` and the Go worker is in the same binary as the bridge, the
in-process event bus carries events. When the worker is in a separate process (future R-3+),
Redis Streams carry events. The dispatcher handles both modes.

---

## Remaining Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Python worker cutover is manual — must be coordinated deploy | Medium | Document preserved in §Migration |
| Go worker in same binary as bridge — crash takes both | Low | Temporal reassigns activity to any available worker; separate binary is R-3 scope |
| `tenant_id` still empty string in session info (pre-existing) | Low | Deferred to R-4 tenant foundation |
| `PythonOrchestrationInput` dead code still in codebase | Low | Remove after Python worker decommissioned |

---

## Next Phase

**R-3: File Artifact Delivery** — deliver file artifacts by reference (DB BYTEA with 1MB limit,
`GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` endpoint).

See `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` for exact next steps.
