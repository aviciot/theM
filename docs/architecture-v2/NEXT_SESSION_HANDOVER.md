# Execution Lifecycle Unification — Phase 2 Complete — Handover

**Date:** 2026-08-01
**Branch:** main
**Phase:** Execution Lifecycle Unification (Phase 2 complete; Phase 3/WS pending)

---

## Current Objective

`internal/execution/` package is created and tested. A2A and SSE are migrated to use
`Lifecycle.Admit/Start/Release`. WS handler still uses the old duplicated pipeline and must be
migrated next.

---

## Branch and HEAD

Branch: `main`
HEAD: run `git log --oneline -1` after commit.

---

## Commits Created This Session

1. `feat(sse): R-5 Phase 2 — migrate SSE handler to execution.Lifecycle`

---

## Work Completed

### Phase 1 — `internal/execution/` + A2A (previous session, commit d6b7a26)

- `errors.go`: `AdmitError`, `AdmitErrorKind` (7→8 kinds), `StartError`
- `request.go`: `ExecutionRequest`, `ExecutionHandle` (with `EventsTransport`), `ExecutionResult`
- `lifecycle.go`: `Lifecycle.Admit/Start/Release`, `RunCreator`, `NewLifecycle`, `NewLifecycleWithRecorder`
- `lifecycle_test.go`: 14 unit tests — all pass, 0 races
- `internal/a2a/server.go`: migrated to use `*execution.Lifecycle`
- `internal/a2a/server_test.go`: 27 tests

### Phase 2 — SSE migration (this session)

- `internal/execution/errors.go`: Added `AdmitErrNotImplemented` (voice EP check inside Lifecycle)
- `internal/execution/request.go`: `ExecutionHandle.EventsTransport string` added
- `internal/execution/lifecycle.go`: Voice EP check (step 3), `eventsTransportFromMode`, nil-safe logger
- `internal/sse/handler.go`: Complete rewrite — uses `*execution.Lifecycle`; SSE headers AFTER Admit
- `internal/sse/handler_test.go`: Rewritten — 22 tests via `execution.NewLifecycleWithRecorder` fakes
- `go/cmd/them/main.go`: `execLifecycle` moved to section 16 (before WS+SSE); SSE uses it
- `go/TEST_INDEX.md`: S1-13 (18→22), totals 490→494, 518→522

---

## Key Architecture Change: SSE Headers Now After Admit

In the old SSE handler, SSE headers (200 OK + `Content-Type: text/event-stream`) were written
AFTER `gate.Check` but BEFORE `session.Register`. This meant Register failures resulted in
SSE error events (client already connected as SSE).

In the migrated handler:
1. `lc.Admit` runs the full pipeline (auth → EPConfig → voice-check → access → gate → session → CreateRun)
2. SSE headers are written AFTER Admit succeeds
3. All pre-Admit errors are clean HTTP responses (not SSE events)
4. Errors after Start (temporal nil, stream unavailable) are SSE error events

This is better UX. Test 6 reflects this: `TestSSEGateRollbackOnRegisterFailure` now
asserts `http.StatusInternalServerError` instead of an SSE error event.

---

## Deployed / Live State

- Go bridge: healthy
- A2A: unit tests pass; no live A2A EP in DB (live E2E not possible without one)
- SSE: migrated and tested in unit tests; live behavior unchanged
- WS: unchanged, healthy

---

## Tests Executed

```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 33 packages, 0 failed
go test -race ./...     → 33 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```

---

## Architecture Decisions Made

1. **Voice EP check moved into Lifecycle.Admit**: Added `AdmitErrNotImplemented` (→ 501). The SSE
   handler maps this kind to the voice-specific message. Lifecycle is the authoritative place for
   EP type enforcement so WS and A2A inherit this automatically.

2. **`EventsTransport` in `ExecutionHandle`**: The SSE handler needs the derived transport value to
   pass to `runEvents()` (dispatcher selects pubsub vs. streams). Deriving it in Lifecycle avoids
   threading `RunEventsMode` through to the streaming path.

3. **`nil` logger guard in `newLifecycle`**: Tests pass `nil` logger; falls back to `slog.Default()`.
   Prevents panic on Register failure log path in tests.

4. **`execLifecycle` constructed before WS+SSE in main.go**: Moved from section 17 to 16 so both
   SSE and (future) WS can share it without forward-reference errors.

---

## Temporary Compatibility Code Still in Place

- WS handler still uses the old individual-dep injection pattern (no `*execution.Lifecycle`).
  This is correct behavior, not a regression. Will be replaced in the next session.

---

## Known Bugs and Blockers

- No `them.entry_points` rows of type `a2a` in DB — live A2A E2E not possible without one.
- WS handler pipeline duplication: ~200 lines identical to what Lifecycle now does. Non-critical.

---

## Files Most Relevant to the Next Task

- `go/internal/execution/lifecycle.go` — the shared pipeline; understand before migrating WS
- `go/internal/ws/handler.go` — next migration target; contains the upgrade-between-gate-session issue
- `go/internal/ws/handler_test.go` — 19 tests to update
- `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` — §6 (WS options)
- `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_DESIGN.md` — §7 (what stays in each handler)

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — `Lifecycle.Start` overwrites identity fields from the handle (EPConfig).
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — `AdmitError.Error()` and `StartError.Error()` both return static strings.
5. **Every code change MUST have a test** — zero new failures before commit.
6. **bus.Subscribe MUST happen between Admit and Start** — do not move it into Lifecycle.

---

## Exact Next Single Focused Task

**WS Handler Migration to Execution Lifecycle**

The WS handler has `upgrader.Upgrade()` between `gate.Check` and `session.Register`. After
upgrade succeeds, errors must be WS close frames (not HTTP). Two options:

**Option A — Full reorder**: Move upgrade AFTER `session.Register`. Gate reservation TTL (10s)
provides safety net if upgrade fails. Simpler code, but upgrade failure after session.Register
requires explicit cleanup.

**Option B — Split Admit**: Add `AdmitToGate` (runs steps 1-7: auth→EP→check→gate.Check) and
keep `Admit` for the full pipeline. After `AdmitToGate`, do WS upgrade, then call remaining
steps (session.Register → Confirm → CreateRun). This avoids releasing a registered session on
upgrade failure but adds API surface.

**Recommendation**: Read `ws/handler.go` carefully. If upgrade rarely fails in practice,
Option A is simpler. If upgrade can fail for valid reasons (bad headers, etc.), Option B is safer.

**WS migration steps (after analysis):**
1. Read `internal/ws/handler.go` fully — understand all steps and their order
2. Choose Option A or B
3. Update `Handler` struct to hold `*execution.Lifecycle`
4. Rewrite `ServeHTTP` to use `lc.Admit` (or `AdmitToGate` + upgrade + rest)
5. Keep WS-specific: upgrader, ws.Conn read/write loop, WS close frames
6. Update `ws/handler_test.go` to use `execution.NewLifecycleWithRecorder` fakes
7. Update `cmd/them/main.go` — WS handler already has `execLifecycle` available
8. `go test ./internal/ws/... ./...` → zero regressions
9. `go test -race ./...`

---

## Commands for Next Session

```bash
# Verify state
git log --oneline -3
git status
docker ps --format "{{.Names}}\t{{.Status}}" | grep -E "go-bridge|go-worker"

# Sanity before touching anything
python3.12 scripts/tests/run_tests.py 01 02 03 04 15

# Read before coding
cat docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md
cat go/internal/execution/lifecycle.go
cat go/internal/ws/handler.go
```

**First prompt for next session:**
> Continue the THEM Python-to-Go migration at `/home/avi/them`. Phases 1 and 2 of Execution Lifecycle Unification are complete: `internal/execution/` package built and tested, A2A and SSE handlers migrated. The next task is migrating `internal/ws/handler.go` to use `execution.Lifecycle`. Read `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` §6 before writing any code. WS has an `upgrader.Upgrade()` call between gate.Check and session.Register — analyze whether to reorder or split Admit. Start by reading `go/internal/ws/handler.go` fully, then design the migration approach before writing any code.
