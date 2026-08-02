# Execution Lifecycle Hardening — R-5.2 Complete — Handover

**Date:** 2026-08-02
**Branch:** main
**HEAD:** 42b182a
**Phase:** R-5.2 — SSE Subscribe Order, Centralized Cleanup, UpdateRunStatus Retry — COMPLETE

---

## Current Objective

R-5.2 is complete. The execution.Lifecycle now has:
- Correct SSE subscribe-before-Start ordering (bootstrap invariant enforced for all 3 protocols)
- Centralized `admitCleanup` covering all Admit failure paths
- Retry loop (3 attempts, 100ms backoff) + Prometheus counter for UpdateRunStatus failures after ExecuteWorkflow

Next work: check `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` for the next migration wave.

---

## Branch and HEAD

Branch: `main`
HEAD: `42b182a`

---

## Commits Created This Session

1. `fix(execution): R-5.2 — SSE subscribe order, centralized cleanup, UpdateRunStatus retry` (42b182a)

---

## Work Completed

### R-5.2 Execution Lifecycle Improvements

- `go/internal/sse/handler.go`:
  - Steps 7 and 8 swapped: `runEvents` (subscribe) now at step 7, `lc.Start` at step 8
  - Package comment updated to reflect correct ordering
  - SSE now has the same bootstrap ordering as WS (subscribe → Start)

- `go/internal/execution/lifecycle.go`:
  - Added `"github.com/aviciot/them/internal/metrics"` import
  - Extracted `admitCleanup` closure after session.Register succeeds — replaces 2 separate inline session.End+gate.Release blocks (Confirm-fail path + CreateRun-fail path)
  - `Start`: retry `UpdateRunStatus` up to 3 attempts with 100ms backoff; on exhaustion increment `metrics.RunStatusUpdateFailed` and set `startedOK=true`

- `go/internal/execution/request.go`:
  - Added `IsStartedOK() bool` accessor on `ExecutionHandle` for test verification

- `go/internal/metrics/metrics.go`:
  - Added `RunStatusUpdateFailed prometheus.Counter` with name `them_run_status_update_failed_total`
  - Registered in `init()`

- `go/TEST_INDEX.md`: S1-13 (22→23), S1-35 (18→21), S1-total 503→507, go test total 531→547

- 3 new tests:
  - `sse/handler_test.go`: `TestSSE_RunStreamSubscribedBeforeStart`
  - `execution/lifecycle_test.go`: `TestLifecycle_AdmitCleanup_BothFailPathsCleanUp`
  - `execution/lifecycle_test.go`: `TestLifecycle_Start_UpdateRunStatus_AllRetriesExhausted_StartedOKSet`

---

## Run State Machine (complete, after R-5.1 + R-5.2)

```
Admit → DB: status=admitted, h.runCreated=true
         ↓
      [SSE: runEvents subscribed here — BEFORE Start]
      [WS: bus.Subscribe + first-message wait here — BEFORE Start]
      [A2A: bus.Subscribe here — BEFORE Start]
         ↓ any failure → Release → UpdateRunStatus(failed); cleanup session + gate
         ↓ success ↓
Start → ExecuteWorkflow → UpdateRunStatus(admitted→running, retry 3×) → h.startedOK=true
         ↓ (workflow runs)
Release (normal) → cleanup session + gate (no run update; worker handles final status)
```

---

## Deployed / Live State

- Go bridge: healthy; all 3 handlers share execution.Lifecycle
- SSE bootstrap ordering: FIXED (subscribe before Start)
- UpdateRunStatus failure: retry 3×, Prometheus counter, startedOK=true
- Python bridge still handling routes not yet migrated to Go

---

## Tests Executed

```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 29 packages, 0 failed (547 unit tests)
go test -race ./...     → 29 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```

---

## Architecture Decisions Made

1. **SSE subscribe before Start (R-5.2)**: The run-stream subscriber is now opened BEFORE `lc.Start` calls `ExecuteWorkflow`. This matches WS ordering and closes the race window where events emitted immediately after workflow launch could be lost before the SSE handler reached `runEvents`.

2. **admitCleanup closure (R-5.2)**: Instead of a method, a closure is used because it captures `sessionID`, `req.EPSlug`, `resolvedCfg.AppID`, and `gateAdmitted` from the local scope. This avoids adding parameters to a helper while keeping the cleanup logic DRY. The closure is created only after `session.Register` succeeds (the point at which cleanup is needed).

3. **UpdateRunStatus retry → metric (R-5.2)**: 3 attempts, 100ms backoff. After exhaustion: `startedOK=true` (the workflow IS executing), increment `them_run_status_update_failed_total`. The reconciler currently only scans `WHERE status = 'running'`; a future extension must also scan `WHERE status = 'admitted' AND started_at < now() - interval '5 minutes'` to repair these stuck rows. The metric is the signal.

4. **IsStartedOK() accessor (R-5.2)**: Added only to support test verification that `startedOK` is set correctly even when `UpdateRunStatus` fails. Not needed by any production code path.

---

## Temporary Compatibility Code Still in Place

None.

---

## Known Bugs and Blockers

- `internal/llm/provider_test.go:49` has a pre-existing `go vet` context-leak warning. Unrelated.
- No `them.entry_points` rows of type `a2a` in DB — live A2A E2E not possible without one.
- `RunStatusAdmitted` rows may get stuck if `UpdateRunStatus` fails all 3 retries (metric fires, reconciler will need to be extended to scan admitted rows).

---

## Files Most Relevant to the Next Task

- `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` — route ownership table, next wave
- `go/internal/execution/lifecycle.go` — shared pipeline, R-5.1+R-5.2 hardening complete
- `go/internal/sse/handler.go` — subscribe-before-Start ordering now correct
- `go/internal/metrics/metrics.go` — new `them_run_status_update_failed_total` counter

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — `Lifecycle.Start` overwrites identity fields from the handle (EPConfig).
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — `AdmitError.Error()` and `StartError.Error()` both return static strings.
5. **Every code change MUST have a test** — zero new failures before commit.
6. **bus.Subscribe / runEvents MUST happen between Admit and Start** — not inside Lifecycle.
7. **Release is always self-contained** — never pass a cancellable context; Release derives its own 5s bounded context.

---

## Exact Next Single Focused Task

Check `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` for the next migration wave.
R-5.1 and R-5.2 (Execution Core Hardening) are both complete.

---

## Commands for Next Session

```bash
# Verify state
git log --oneline -3
git status

# Sanity before touching anything
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
docker run --rm -v /home/avi/them/go:/go_src -w /go_src golang:1.23 sh -c "go test ./..."

# Read before coding
cat docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md
cat docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md
```
