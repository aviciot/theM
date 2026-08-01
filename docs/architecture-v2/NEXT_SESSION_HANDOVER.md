# Execution Lifecycle Unification — Partial — Handover

**Date:** 2026-08-01
**Branch:** main
**Phase:** Execution Lifecycle Unification (Phase 1 complete; Phase 2 pending)

---

## Current Objective

`internal/execution/` package is created and tested. A2A is migrated. WS and SSE handlers
still use the old duplicated pipeline and must be migrated next.

---

## Branch and HEAD

Branch: `main`
HEAD: run `git log --oneline -1` after commit.

---

## Commits Created This Session

1. `feat(execution): R-5 — shared execution lifecycle + A2A migration (WS/SSE pending)`

---

## Work Completed

### `internal/execution/` (new package)

- `errors.go`: `AdmitError`, `AdmitErrorKind` (7 kinds), `StartError`
- `request.go`: `ExecutionRequest`, `ExecutionHandle`, `ExecutionResult`
- `lifecycle.go`: `Lifecycle.Admit/Start/Release`, `RunCreator`, `NewLifecycle`, `NewLifecycleWithRecorder`
- `lifecycle_test.go`: 14 unit tests — all pass, 0 races

### `internal/a2a/server.go` (migrated)

- `Server` now holds `*execution.Lifecycle` instead of individual gate/session/epLoader/recorder/temporal deps
- `handleMessageSend` calls `lc.Admit → bus.Subscribe → lc.Start → wfRun.Get → lc.Release`
- `extractToken` replaces `tryAuthenticate` (cleaner 2-value return)
- `mapAdmitError` maps `*execution.AdmitError` to A2A HTTP+JSON-RPC responses
- 27 tests — all pass, 0 races

### `go/cmd/them/main.go`

- Added `execution.NewLifecycle(...)` → `execLifecycle`
- `a2a.NewServer(execLifecycle, bus, authenticator, instanceID, log)` — new 5-arg signature

### `go/TEST_INDEX.md`

- S1-14 (a2a): 25 → 27 tests; updated purpose description
- S1-35 (execution lifecycle): new, 14 tests
- S1 total: 474 → 490; `go test ./...` total: 502 → 518

---

## Deployed / Live State

- Go bridge: healthy (confirmed before session)
- Go workers (2x): polling `them-orchestration-go`
- A2A: unit tests pass; no live A2A EP in DB (live E2E not possible without one)
- WS/SSE: unchanged, healthy

---

## Tests Executed

```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings (pre-existing llm/provider_test.go cancel warning is not new)
go test ./...           → 33 packages, 0 failed
go test -race ./...     → 33 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```

---

## Architecture Decisions Made

1. **`RunCreator` interface**: Lifecycle uses interface (not concrete `*runrecorder.Recorder`) internally. `NewLifecycle` takes the concrete type for production; `NewLifecycleWithRecorder` takes the interface for tests.

2. **`bus.Subscribe` NOT in Lifecycle**: Caller subscribes between `Admit` and `Start`. This preserves the bootstrap ordering invariant without leaking the bus into the Lifecycle API.

3. **WS/SSE not migrated this session**: Both handlers have a protocol handshake step (WS upgrade or SSE headers) that occurs between `gate.Check` and `session.Register`. Migrating to `Lifecycle.Admit` requires reordering these steps. This is safe for SSE (HTTP errors before headers are cleaner) but needs care for WS. Deferred to avoid risk.

4. **A2A `extractToken` pattern**: Returns `(*auth.TokenInfo, string)`. The Lifecycle receives both and enforces based on EPConfig.AccessMode. Token present but invalid = `tokenInfo nil`, allowing Lifecycle to decide per EP policy.

---

## Temporary Compatibility Code Still in Place

- WS and SSE handlers still use the old individual-dep injection pattern (no `*execution.Lifecycle`). This is correct behavior, not a regression. It will be replaced in the next session.
- `newID()` in `sse/handler.go` (UUID v4 via `uuid.New().String()`) — correct, no change needed.

---

## Known Bugs and Blockers

- No `them.entry_points` rows of type `a2a` in DB — live A2A E2E not possible without one.
- WS handler pipeline duplication: ~200 lines identical to what Lifecycle now does. Non-critical (correct behavior), but must be migrated.
- SSE handler pipeline duplication: same issue.

---

## Files Most Relevant to the Next Task

- `go/internal/execution/lifecycle.go` — the shared pipeline; read before migrating handlers
- `go/internal/sse/handler.go` — next migration target (SSE reorder is cleaner than WS)
- `go/internal/ws/handler.go` — after SSE; needs upgrade-ordering analysis
- `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` — §6 (why WS/SSE deferred, migration options)
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

**WS/SSE Migration to Execution Lifecycle**

Migrate `internal/sse/handler.go` first (simpler — reorder SSE headers to after session.Register):

**SSE migration steps:**
1. Add `lc *execution.Lifecycle` to `Handler` struct
2. Update `NewHandler` to accept `*execution.Lifecycle` (or add `WithLifecycle` builder method)
3. Replace steps 1-9 in `ServeHTTP` with `lc.Admit(ctx, req)` → handle
4. Move `w.Header().Set(...)` + `w.WriteHeader(200)` AFTER `lc.Admit` succeeds (before bus.Subscribe)
5. Replace `bus.Subscribe` + `recorder.CreateRun` + `ExecuteWorkflow` with `lc.Start(ctx, h, input)` → wfRun
6. Replace `defer session.End + gate.Release` with `defer lc.Release(context.Background(), h)`
7. Update `NewHandler` in `cmd/them/main.go` to pass `execLifecycle`
8. Update `sse_handler_test.go` to inject a `*execution.Lifecycle` with fakes
9. Run `go test ./internal/sse/... ./...` — zero regressions
10. Run `go test -race ./...`

**WS migration** (after SSE, separate commit):
- Analyze whether WS upgrade can safely move after session.Register
- If yes: same migration pattern as SSE
- If no: consider `AdmitPre` / `AdmitPost` split, or keep WS with old pattern and document

**Before starting:**
- Read `EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` §6 — WS/SSE migration options
- Read `EXECUTION_LIFECYCLE_UNIFICATION_DESIGN.md` §7 — what stays in each handler
- Read `sse/handler.go` ServeHTTP body to confirm the header-ordering change is safe

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
cat go/internal/sse/handler.go
```

**First prompt for next session:**
> Continue the THEM Python-to-Go migration at `/home/avi/them`. Phase 1 of Execution Lifecycle Unification is complete: `internal/execution/` package built and tested, A2A handler migrated. The next task is migrating `internal/sse/handler.go` to use `execution.Lifecycle`, then `internal/ws/handler.go`. Read `docs/architecture-v2/EXECUTION_LIFECYCLE_UNIFICATION_REPORT.md` §6 before writing any code. SSE migration requires reordering SSE headers to after `session.Register` (so errors before headers = clean HTTP errors). Do not touch WS until SSE is complete and tested.
