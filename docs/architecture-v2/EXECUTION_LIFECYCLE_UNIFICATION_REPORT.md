# Execution Lifecycle Unification — Implementation Report
# Status: COMPLETE (A2A + SSE + WS all migrated; Core Hardening applied)
# Date: 2026-08-02

---

## 1. Summary

This report covers three sessions of Execution Lifecycle Unification:

**Phase 1 (session 1):**
1. Created `internal/execution/` — the shared admission-and-run-start package
2. Migrated the A2A server to use `Lifecycle.Admit/Start/Release`

**Phase 2 (session 2):**
3. Migrated `internal/sse/handler.go` to use `Lifecycle.Admit/Start/Release`
4. Added `AdmitErrNotImplemented` (voice EP check now inside Lifecycle.Admit)
5. SSE headers moved to AFTER `Lifecycle.Admit` — pre-Admit errors are clean HTTP responses
6. Added `EventsTransport` to `ExecutionHandle`; `eventsTransportFromMode` in lifecycle.go

**Phase 3 (session 3):**
7. Migrated `internal/ws/handler.go` to use `Lifecycle.Admit/Start/Release`
8. `Lifecycle.Admit` now runs BEFORE `upgrader.Upgrade` — all pre-Admit errors are clean HTTP
9. On upgrade failure: `lc.Release` with bounded 5-second timeout cleans up gate/session/run
10. Fixed bounded cleanup timeout and logged cleanup failures in `lifecycle.go`
11. All three protocols (WS, SSE, A2A) now use the shared execution lifecycle

**Phase 4 — Execution Core Hardening (session 4):**
12. Run state machine corrected: `Admit` creates run as `RunStatusAdmitted` (not `running`)
13. `Start` transitions run `admitted → running` after `ExecuteWorkflow` succeeds
14. `Release` marks run `failed` when `runCreated && !startedOK` (orphan-run prevention)
15. `gate.Confirm` failure is now fatal: session.End + gate.Release called, CreateRun skipped
16. `NewLifecycle` panics on nil `epLoader`, `gate`, `sessions`, `recorder`, or `temporal`
17. `Release` API: `ctx context.Context` parameter removed — Release is always self-contained (5s internal timeout)
18. All 3 handlers: `extractToken` removed; `extractRawToken` returns raw string only — Lifecycle owns all validation
19. 7 new failure-path tests (3 WS + 4 lifecycle); 531 unit tests total, 0 data races

All existing tests pass; 7 new tests added (531 total, up from 524).

---

## 2. Files Changed

| File | Change |
|---|---|
| `go/internal/execution/errors.go` | New + updated: `AdmitError` (8 kinds incl. `AdmitErrNotImplemented`), `StartError` |
| `go/internal/execution/request.go` | New + updated: `ExecutionHandle` gained `EventsTransport string` |
| `go/internal/execution/lifecycle.go` | New + updated: voice EP check, `eventsTransportFromMode`, nil-safe logger, bounded Release timeout (5s), logged cleanup failures |
| `go/internal/execution/lifecycle_test.go` | New: 14 unit tests for Lifecycle methods |
| `go/internal/ws/handler.go` | Migrated: uses `*execution.Lifecycle`; Admit before upgrade; bounded cleanup on upgrade failure |
| `go/internal/ws/handler_test.go` | Rewritten: 21 tests via `execution.NewLifecycleWithRecorder` fakes (wsBuilder pattern) |
| `go/internal/sse/handler.go` | Migrated: uses `*execution.Lifecycle`; SSE headers after Admit; wire-format only |
| `go/internal/sse/handler_test.go` | Rewritten: 22 tests via `execution.NewLifecycleWithRecorder` fakes |
| `go/internal/a2a/server.go` | Migrated: `Server` now holds `*execution.Lifecycle` instead of individual deps |
| `go/internal/a2a/server_test.go` | Updated: 27 tests using `*execution.Lifecycle` with fakes |
| `go/cmd/them/main.go` | Updated: WS now uses `execLifecycle`; LLM provider and orchestrator removed (no longer needed) |
| `go/internal/domain/domain.go` | Added `RunStatusAdmitted` — transient state between Admit and Start |
| `go/internal/execution/request.go` | Added `runCreated bool`, `startedOK bool` to `ExecutionHandle` |
| `go/internal/execution/lifecycle.go` | Phase 4: `RunCreator.UpdateRunStatus` added; Admit creates as admitted; Start updates to running; Release marks failed; Confirm fatal; NewLifecycle panic on nil deps; Release ctx param removed |
| `go/internal/execution/lifecycle_test.go` | 4 new hardening tests (Release orphan, Confirm fatal, NewLifecycle panic) |
| `go/internal/ws/handler.go` | `extractToken` → `extractRawToken` (no Validate); Release(h) without ctx |
| `go/internal/ws/handler_test.go` | 3 new failure-path tests; fakes updated for UpdateRunStatus |
| `go/internal/sse/handler.go` | `extractToken` → `extractRawToken`; Release(h) without ctx |
| `go/internal/sse/handler_test.go` | Fakes updated for UpdateRunStatus |
| `go/internal/a2a/server.go` | `extractToken` → `extractRawToken`; Release(h) without ctx |
| `go/internal/a2a/server_test.go` | Fakes updated for UpdateRunStatus |
| `go/TEST_INDEX.md` | Updated: S1-12 (21→24), S1-35 (14→18), totals 496→503, 524→531 |
| `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` | This document |

---

## 3. What Was Built

### 3.1 `internal/execution` package

**`errors.go`:**
- `AdmitErrorKind` enum with 7 values: NotFound, Unauthorized, Forbidden, CapExceeded, RateLimited, QueueFull, DBUnavailable, Internal
- `AdmitError` with `Kind` and `HTTPStatus` — `Error()` returns static client-safe string, never raw internals
- `StartError` — same pattern; internal cause accessible only via `Cause()` for logging

**`request.go`:**
- `ExecutionRequest` — per-call inputs: EPSlug, RawToken, TokenInfo, UserMessage, ContextID, RunEventsMode, InstanceID
- `ExecutionHandle` — admission ticket: RunID, ContextID, SessionID, EPConfig; plus unexported gate fields used by Release
- `ExecutionResult` — for synchronous callers (A2A); streaming callers iterate `wfRun.Get` themselves

**`lifecycle.go`:**
- `RunCreator` interface — minimal recorder subset; enables test fakes without a live DB
- `Lifecycle.Admit(ctx, req)` — 9-step pipeline:
  1. tryAuthenticate (if auth != nil and token present)
  2. epLoader.Load → ErrNotFound | DBUnavailable on failure
  3. Access mode enforcement (token EP + no/invalid token → Unauthorized)
  4. epconfig.CheckAccess (disabled / block-list → Forbidden)
  5. Generate RunID, ContextID (from req or generated), SessionID — all UUID v4
  6. gate.Check → CapExceeded | RateLimited | QueueFull
  7. session.Register → Rollback gate on failure
  8. gate.Confirm (non-fatal on error)
  9. recorder.CreateRun → Release session+gate on failure
- `Lifecycle.Start(ctx, h, input)` — overwrites identity fields (RunID, ContextID, TenantID, ApplicationID, EntryPointSlug) from handle; calls ExecuteWorkflow on GoTaskQueue
- `Lifecycle.Release(ctx, h)` — nil-safe no-op; calls session.End + gate.Release using context.Background()
- `NewLifecycle` — production constructor (takes `*runrecorder.Recorder`)
- `NewLifecycleWithRecorder` — test constructor (takes `RunCreator` interface)

### 3.2 A2A Server Migration

`internal/a2a/server.go` now holds:
- `lc *execution.Lifecycle` — replaces individual gate, session, epLoader, recorder, temporal deps
- `bus event.Bus` — retained (server subscribes between Admit and Start)
- `authenticator Authenticator` — retained (extracts token before Admit)
- `instanceID string`, `logger *slog.Logger`

`handleMessageSend` pipeline after migration:
1. `extractToken(r)` → `(tokenInfo, rawToken)`
2. Parse params → `userText`, `contextID`
3. `lc.Admit(ctx, admitReq)` → handle or typed error
4. `defer lc.Release(context.Background(), h)`
5. `bus.Subscribe(ctx, h.ContextID, 256)` — before Start (bootstrap ordering)
6. `lc.Start(ctx, h, input)` → wfRun
7. `wfRun.Get(ctx, &wfResult)` — synchronous block
8. Map result → JSON-RPC response

`mapAdmitError` maps `*execution.AdmitError` kinds to HTTP status + JSON-RPC error body.

### 3.3 main.go Wiring

```go
execLifecycle := execution.NewLifecycle(
    authenticator, epLoader, admissionGate, sessionStore,
    recorder, temporalCli, log,
)
a2aServer := a2a.NewServer(execLifecycle, bus, authenticator, cfg.InstanceID, log)
```

The `execLifecycle` is constructed once and shared. WS and SSE will receive it in the next session.

---

## 4. Security Invariants Preserved

| Invariant | How enforced |
|---|---|
| TenantID never from request | `Lifecycle.Start` overwrites `input.TenantID` from `h.EPConfig.TenantID` |
| ApplicationID never from request | Same — `input.ApplicationID` overwritten from `h.EPConfig.AppID` |
| No raw err.Error() to client | `AdmitError.Error()` returns static strings; `StartError.Error()` = "internal error" |
| Gate rollback on session.Register failure | Lifecycle.Admit step 7 — explicit `gate.Rollback` before return |
| Session + gate cleanup on CreateRun failure | Lifecycle.Admit step 9 — explicit End + Release before return |
| Release exactly once | Protocol handlers call `defer lc.Release(context.Background(), h)` after Admit success |
| bus.Subscribe before ExecuteWorkflow | A2A: subscribe at step 5, Start at step 6 |
| All IDs are UUID v4 | `newRunID()` uses `uuid.New().String()` — Python worker requires `uuid.UUID()` parsing |

---

## 5. Test Results

### Phase 1 (A2A migration)
```
go test ./...           → 33 packages, 0 failed
go test -race ./...     → 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```
New tests: 14 (execution) + 2 (a2a, net new) = 16. Total: 518.

### Phase 2 (SSE migration)
```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 33 packages, 0 failed
go test -race ./...     → 33 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```
New tests: 4 (sse, net new: EventsTransport, LifecycleCallSequence, MissingMessage, IDsAreUUIDv4).
Total: 522 (up from 518).

### Phase 3 (WS migration)
```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings (pre-existing llm/provider_test.go warning unchanged)
go test ./...           → 29 packages, 0 failed
go test -race ./...     → 29 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```
New tests: 2 (ws, net new: IDsAreUUIDv4, AdmitBeforeUpgrade_EPNotFound). Total: 524 (up from 522).

### Phase 4 (Execution Core Hardening)
```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 29 packages, 0 failed
go test -race ./...     → 29 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```
New tests: 7 (3 ws failure-path + 4 lifecycle hardening). Total: 531 (up from 524).

---

## 6. WS Migration — Complete

All three protocol handlers are now migrated.

| Handler | Status | Key behavior |
|---|---|---|
| A2A | ✅ Phase 1 | No protocol handshake between gate and session — clean migration |
| SSE | ✅ Phase 2 | SSE headers after `Lifecycle.Admit`; pre-Admit errors = clean HTTP |
| WS | ✅ Phase 3 | `Lifecycle.Admit` before `upgrader.Upgrade`; upgrade failure → bounded lc.Release |

**WS ordering decision (Option A — Full reorder):**
`Lifecycle.Admit` (auth→EP→voice→access→gate→session→CreateRun) runs before `upgrader.Upgrade`.
The gorilla upgrader writes its own HTTP error if upgrade fails, so the handler only needs to call
`lc.Release` (bounded 5s timeout) to clean up gate/session/run state.

**Behavioral changes vs. old WS handler:**
- Gate cap exceeded: was 503, now 429 (matching SSE and A2A via Lifecycle HTTP mapping)
- Register failure: was WS error frame after upgrade, now HTTP 500 before upgrade (cleaner)
- Cleanup: was `context.Background()` with no timeout; now bounded 5-second timeout

---

## 7. Known Gaps After Phase 4

| Gap | Severity | Notes |
|---|---|---|
| No live A2A entry point in DB | Low | Unit tests verify correctness. Live E2E requires creating a2a-type EP via admin API. |
| `internal/llm/provider_test.go` vet warning | Low | Pre-existing; unrelated to lifecycle migration. Context leak warning on line 49. |
| `RunStatusAdmitted` not handled by Python worker | Low | Worker polls for `running` runs only. `admitted` rows are invisible to the worker; they transition to `running` via `UpdateRunStatus` in `Start` before workflow execution begins. No behavioral impact. |

---

## 8. Architecture Decisions

1. **`RunCreator` interface in `execution` package**: `Lifecycle.recorder` is typed as the interface, not `*runrecorder.Recorder`. This enables test fakes without a DB. Production uses `NewLifecycle` which takes the concrete recorder; tests use `NewLifecycleWithRecorder`.

2. **`bus.Subscribe` not in `Admit`**: The caller must subscribe between `Admit` and `Start`. This preserves the bootstrap ordering invariant (events emitted immediately after `ExecuteWorkflow` starts are not missed).

3. **WS: Full reorder (Option A) chosen**: `Lifecycle.Admit` runs before `upgrader.Upgrade`. The gorilla upgrader writes its own HTTP error on failure, so no HTTP error needs to be written manually. On failure: `lc.Release` cleans up gate/session/run state with a bounded 5-second timeout. This is simpler than split Admit and avoids adding `AdmitPre/AdmitPost` API surface.

4. **Bounded cleanup in Release**: `Release` now derives a fresh `context.WithTimeout(context.Background(), 5s)` internally. All callers may safely pass `context.Background()` — the timeout is always applied. This prevents cleanup from hanging indefinitely if Redis is slow.

5. **Logged cleanup failures**: `session.End` and `gate.Release` failures in `Release` are now logged as `Warn` instead of silently discarded (`_ =`). This surfaces Redis availability issues in prod without changing the cleanup semantics.

6. **Gate cap-exceeded HTTP status change**: Old WS handler returned 503 for `gate.ErrCapExceeded`. New path via Lifecycle returns 429 (consistent with SSE, A2A, and the HTTP semantics of "too many requests"). Tests updated accordingly.

7. **Orchestrator removed from main.go**: The in-process `orchestrator.Orchestrator` was only wired to the WS handler (as a non-Temporal fallback). Now that WS uses Lifecycle exclusively, the orchestrator and LLM provider construction were removed from `cmd/them/main.go`. This reduces startup dependencies.

8. **Run state machine: admitted → running → failed/completed** (Phase 4): Runs are now created in DB as `admitted` (not `running`). `Start` transitions to `running` only after `ExecuteWorkflow` succeeds. `Release` transitions to `failed` when `runCreated && !startedOK`. This eliminates the class of orphan/stuck runs that were previously possible when WS upgrade, first-message, run-stream subscribe, or Temporal failures left a run permanently `running`.

9. **gate.Confirm is now fatal** (Phase 4): Previously a warning-only guard. Now triggers full rollback (session.End + gate.Release) and returns `AdmitErrInternal`. This ensures no session consumes gate capacity with an expired reservation.

10. **Release ctx removed** (Phase 4): `Release(ctx, h)` → `Release(h)`. The `ctx` parameter was always `context.Background()` at every call site (the request context is always cancelled by the time Release fires). The 5-second bounded context is derived internally. This prevents future callers from passing a cancelled context and breaking cleanup.

11. **Handler token validation ownership** (Phase 4): All 3 handlers had `extractToken` that called `authenticator.Validate` and returned `*auth.TokenInfo`. These are replaced with `extractRawToken` returning only the raw string. `Lifecycle.Admit` step 1 owns all validation (and already re-validates when `TokenInfo == nil && RawToken != ""`). This removes the duplicate validation path and the risk of handler-level enforcement diverging from Lifecycle enforcement.

12. **NewLifecycle fail-fast validation** (Phase 4): `NewLifecycle` panics if `epLoader`, `gate`, `sessions`, `recorder`, or `temporal` are nil. Previously these nil deps caused obscure runtime failures under load. The panic is immediate and obvious at server startup, preventing misconfigured deployments from accepting traffic.
