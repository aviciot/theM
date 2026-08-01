# Execution Lifecycle Unification — Phase 3 Complete — Handover

**Date:** 2026-08-01
**Branch:** main
**Phase:** Execution Lifecycle Unification — ALL PHASES COMPLETE

---

## Current Objective

Execution Lifecycle Unification is complete. WS, SSE, and A2A all use `execution.Lifecycle.Admit/Start/Release`.

Next work: determine next migration wave per `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md`.

---

## Branch and HEAD

Branch: `main`
HEAD: run `git log --oneline -1` after commit.

---

## Commits Created This Session

1. `feat(ws): R-5 Phase 3 — migrate WS handler to execution.Lifecycle`

---

## Work Completed

### Phase 3 — WS migration (this session)

- `internal/execution/lifecycle.go`: Bounded 5-second cleanup timeout in `Release`; logged `session.End`/`gate.Release` failures (previously silently discarded)
- `internal/ws/handler.go`: Complete rewrite — uses `*execution.Lifecycle`; `Lifecycle.Admit` runs BEFORE `upgrader.Upgrade`; on upgrade failure bounded `lc.Release` cleans up; handler retains only upgrade, frame I/O, metrics
- `internal/ws/handler_test.go`: Rewritten — 21 tests via `execution.NewLifecycleWithRecorder` fakes (wsBuilder pattern)
- `go/cmd/them/main.go`: WS wired to `execLifecycle`; in-process orchestrator and LLM provider removed (no longer needed by any handler)
- `go/TEST_INDEX.md`: S1-12 (19→21), totals 494→496, 522→524

### Previous phases

- Phase 1 (d6b7a26): `internal/execution/` package + A2A migration
- Phase 2 (67ec97b): SSE handler migration

---

## Key Architecture Change: WS Admit-Before-Upgrade

Old WS handler: `tryAuthenticate` → EPConfig → voice-check → gate.Check → **upgrade** → session.Register → gate.Confirm → CreateRun

New WS handler: `lc.Admit` (all 10 steps) → **upgrade** → bus.Subscribe → readClientMessage → lc.Start → streamEvents

On upgrade failure: `lc.Release` (bounded 5s timeout) cleans up gate/session/run. The gorilla upgrader writes its own HTTP error to the client.

**Behavioral changes vs. old WS handler:**
- Gate cap exceeded: was 503, now 429 (matching SSE and A2A via Lifecycle)
- Register failure: was WS error frame after upgrade, now HTTP 500 before upgrade (cleaner)
- Cleanup: was unbounded; now bounded 5-second timeout

---

## Deployed / Live State

- Go bridge: healthy
- WS: migrated, tested in unit tests; live behavior unchanged (Temporal path identical)
- SSE: migrated and tested (previous session)
- A2A: migrated and tested (previous session)

---

## Tests Executed

```
go build ./...          → 0 errors
go vet ./...            → 0 new warnings
go test ./...           → 29 packages, 0 failed
go test -race ./...     → 29 packages, 0 data races
Python sanity 01-04,15  → 55 passed, 0 failed
```

---

## Architecture Decisions Made

1. **Full reorder (Option A)**: `Lifecycle.Admit` before upgrade. No split Admit needed.
2. **Bounded Release timeout**: 5s inside `Release` itself — callers always pass `context.Background()`.
3. **Cleanup failures logged**: `session.End`/`gate.Release` errors now emit `Warn` log instead of `_ =`.
4. **Orchestrator removed from main.go**: In-process orchestrator was only used by old WS handler; now dead code, removed.
5. **Gate 503→429**: cap exceeded now 429 (Too Many Requests) via Lifecycle, consistent across all 3 protocols.

---

## Temporary Compatibility Code Still in Place

None. All three handlers (WS, SSE, A2A) are fully migrated. No duplicate pipeline code remains.

---

## Known Bugs and Blockers

- `internal/llm/provider_test.go:49` has a pre-existing `go vet` context-leak warning. Unrelated to this work.
- No `them.entry_points` rows of type `a2a` in DB — live A2A E2E not possible without one.

---

## Files Most Relevant to the Next Task

- `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` — route ownership table, next wave
- `go/internal/execution/lifecycle.go` — shared pipeline, now complete
- `go/cmd/them/main.go` — binary wiring, cleaned up

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — `Lifecycle.Start` overwrites identity fields from the handle (EPConfig).
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — `AdmitError.Error()` and `StartError.Error()` both return static strings.
5. **Every code change MUST have a test** — zero new failures before commit.
6. **bus.Subscribe MUST happen between Admit and Start** — not inside Lifecycle.

---

## Exact Next Single Focused Task

Check `docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md` for the next migration wave.
The Execution Lifecycle Unification is complete — all three protocol handlers share the same admission pipeline.

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
