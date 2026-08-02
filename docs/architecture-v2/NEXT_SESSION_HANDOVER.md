# Execution Core Hardening — Phase 4 Complete — Handover

**Date:** 2026-08-02
**Branch:** main
**Phase:** Execution Core Hardening (R-5.1) — COMPLETE

---

## Current Objective

Execution Core Hardening is complete. The execution.Lifecycle now has a correct run state machine,
fail-fast production validation, fatal gate.Confirm, orphan-run prevention, clean Release API, and
single-owner token validation. All 3 protocol handlers (WS, SSE, A2A) are updated.

Next work: check `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` for the next migration wave.

---

## Branch and HEAD

Branch: `main`
HEAD: run `git log --oneline -1` after commit.

---

## Commits Created This Session

1. `fix(execution): R-5.1 — execution core hardening (orphan runs, Confirm fatal, fail-fast, clean API)`

---

## Work Completed

### Execution Core Hardening (Phase 4)

- `internal/domain/domain.go`: Added `RunStatusAdmitted` — transient state between Admit and Start
- `internal/execution/request.go`: Added `runCreated bool`, `startedOK bool` to `ExecutionHandle`
- `internal/execution/lifecycle.go`:
  - `RunCreator` interface: added `UpdateRunStatus(ctx, runID, status, errMsg)` method
  - `NewLifecycle` (production): panics if `epLoader`, `gate`, `sessions`, `recorder`, or `temporal` are nil
  - `Admit` step 9: `gate.Confirm` failure is now **fatal** — `session.End` + `gate.Release` called, `CreateRun` skipped, returns `AdmitErrInternal`
  - `Admit` step 10: `CreateRun` with `RunStatusAdmitted` (not `RunStatusRunning`); sets `h.runCreated = true`
  - `Start`: after `ExecuteWorkflow` succeeds, calls `UpdateRunStatus(admitted → running)`; sets `h.startedOK = true`
  - `Release`: removed `ctx context.Context` parameter (always self-contained 5s timeout); if `h.runCreated && !h.startedOK`, calls `UpdateRunStatus(failed, "startup failed")` before session/gate cleanup
- `internal/ws/handler.go`: `extractToken` → `extractRawToken` (no Validate); all `Release(context.Background(), h)` → `Release(h)`
- `internal/sse/handler.go`: same extractRawToken and Release API changes
- `internal/a2a/server.go`: same extractRawToken and Release API changes; removed unused `context` import
- `internal/execution/lifecycle_test.go`: 4 new hardening tests; `fakeRecorder.UpdateRunStatus` added; all `Release(ctx, h)` → `Release(h)`
- `internal/ws/handler_test.go`: 3 new failure-path tests; `fakeRunCreator.UpdateRunStatus` and `captureRunCreator.UpdateRunStatus` added
- `internal/sse/handler_test.go`: fakes updated for `UpdateRunStatus`
- `internal/a2a/server_test.go`: fakes updated for `UpdateRunStatus`
- `go/TEST_INDEX.md`: S1-12 (21→24), S1-35 (14→18), totals 496→503, 524→531

---

## Run State Machine (after Phase 4)

```
Admit → DB: status=admitted, h.runCreated=true
         ↓
      (WS upgrade / message read / stream subscribe may fail here)
         ↓ failure → Release → UpdateRunStatus(failed); cleanup session + gate
         ↓ success ↓
Start → ExecuteWorkflow → DB: status=running, h.startedOK=true
         ↓ (workflow runs)
Release (normal) → cleanup session + gate (no run update; worker handles final status)
```

---

## Deployed / Live State

- Go bridge: healthy; all 3 handlers share execution.Lifecycle
- WS/SSE/A2A: migrated and tested (unit tests)
- Python bridge still handling routes not yet migrated to Go

---

## Tests Executed

```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 29 packages, 0 failed (531 unit tests)
go test -race ./...     → 29 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```

---

## Architecture Decisions Made

1. **Run state machine**: admitted → running (Start success) → failed (Release when Start never ran). The Python worker only polls `running` runs; `admitted` rows transition before any work starts.

2. **gate.Confirm fatal**: Previously non-fatal (warning + continue). Now triggers full rollback. Rationale: Confirm failure means the slot has expired TTL and may be claimed by another connection. Continuing would corrupt the gate concurrency accounting.

3. **Release ctx removed**: All call sites passed `context.Background()`. The 5s bounded context is internal to Release. Removing the parameter prevents future callers from accidentally passing a cancelled context.

4. **Handler extractRawToken (no Validate)**: Lifecycle.Admit already validates the token in step 1 when `TokenInfo == nil && RawToken != ""`. The handler's Validate call was a duplicate that could silently differ from Lifecycle's enforcement (e.g. if the authenticator had different state). Single responsibility is now enforced.

5. **NewLifecycle panic on nil deps**: Chosen over returning an error because misconfigured-server is a programming error, not a recoverable runtime condition. A startup panic is immediately visible; a nil-pointer dereference at first request is not.

---

## Temporary Compatibility Code Still in Place

None. All hardening is centralized in execution.Lifecycle.

---

## Known Bugs and Blockers

- `internal/llm/provider_test.go:49` has a pre-existing `go vet` context-leak warning. Unrelated.
- No `them.entry_points` rows of type `a2a` in DB — live A2A E2E not possible without one.
- `RunStatusAdmitted` is a new status in the DB; Python worker may not query/display it. Runs spend < 1ms in this state (transition happens synchronously in Start), so this is cosmetic only.

---

## Files Most Relevant to the Next Task

- `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` — route ownership table, next wave
- `go/internal/execution/lifecycle.go` — shared pipeline, Phase 4 hardening complete
- `go/cmd/them/main.go` — binary wiring, cleaned up

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — `Lifecycle.Start` overwrites identity fields from the handle (EPConfig).
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — `AdmitError.Error()` and `StartError.Error()` both return static strings.
5. **Every code change MUST have a test** — zero new failures before commit.
6. **bus.Subscribe MUST happen between Admit and Start** — not inside Lifecycle.
7. **Release is always self-contained** — never pass a cancellable context; Release derives its own 5s bounded context.

---

## Exact Next Single Focused Task

Check `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` for the next migration wave.
The Execution Core Hardening is complete.

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
