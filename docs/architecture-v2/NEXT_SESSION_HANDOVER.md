# R-4e Complete — Handover to Execution Lifecycle Unification

**Date:** 2026-08-01
**Branch:** main
**Phase completed:** R-4e — A2A Inbound Execution Path Alignment

---

## Current Objective

R-4e is complete. All three inbound paths — WS, SSE, and A2A — now use the same
execution pipeline: EPConfig resolution → auth → access control → gate → session →
Temporal dispatch. TenantID and ApplicationID propagate from server-side EPConfig on
all paths.

The next session should implement **Execution Lifecycle Unification**: extracting the
shared handler pipeline into a common helper to eliminate the ~200-line duplication
between `ws/handler.go`, `sse/handler.go`, and `a2a/server.go`.

---

## Branch and HEAD

Branch: `main`
HEAD after this session: `git log --oneline -1` after commit.

---

## Commits Created This Session

1. `feat(a2a): R-4e — align inbound A2A with tenant-aware Temporal execution path`

---

## Work Completed

### R-4a through R-4d (complete — prior sessions)
See `docs/architecture-v2/R4D_IMPLEMENTATION_REPORT.md`.

### R-4e (complete — this session)

**`go/internal/epconfig/pgx.go`:**
- Added `COALESCE(a.tenant_id::text, '')` to `epConfigQuery`
- Updated `Scan` call to read `TenantID` into `EPConfigRow.TenantID`
- Fix: EPConfig was previously returning empty TenantID for all EPs even though
  the column existed in the DB since R-4a. This silently affected WS and SSE too.

**`go/internal/a2a/server.go`:**
- Complete rewrite. Removed `*orchestrator.Orchestrator` dependency.
- Added: `Authenticator`, `EPConfigLoader`, `GateStore`, `SessionStore`,
  `TemporalClientExecutor`, `instanceID` to `Server` struct.
- `NewServer` signature updated accordingly.
- `handleMessageSend` now runs the full execution pipeline:
  auth → EPConfig → access → gate → session → Temporal → result mapping
- Fixed wire format: `rpcPart{Kind,Text}` → `rpcTextPart{Text}` (A2A spec)
- Added `taskId` to `rpcResult`
- `writeHTTPError` helper for HTTP-level failures with JSON-RPC body
- `tryAuthenticate` mirrors WS/SSE implementation

**`go/cmd/them/main.go`:**
- A2A Server wiring updated to pass full dependency set.

**`go/internal/a2a/server_test.go`:**
- Complete rewrite: 25 tests covering all R-4e requirements from the architecture
  review (§15). Removed old 3-test suite; replaced with full coverage.

**`go/TEST_INDEX.md`:**
- S1-14 expanded from 3 to 25 tests; S1 total 452→474; overall 480→502.

---

## Deployed / Live State

- Go bridge: healthy (`{"status":"ok","checks":{"postgres":"ok","redis":"ok"}}`)
- Go workers (2x): polling `them-orchestration-go`
- A2A unknown slug → HTTP 404 ✓ (no entry points in DB; live A2A test requires one)
- Agent card endpoint → correct JSON ✓
- WS/SSE: unchanged, healthy

---

## Tests Executed

- `go test ./...`: **29 packages, 0 failed**
- `go test -race ./...`: **29 packages, 0 data races**
- Python sanity 01 02 03 04 15: **55 passed, 0 failed**
- Docker image build: **success** (tests run inside build)

---

## Architecture Decisions Made

1. **OrchestratorName = appSlug**: WS uses `appSlug` directly as `OrchestratorName`
   in `WorkflowInput`. A2A follows the same convention. EPConfig does not carry an
   `OrchestratorName` field — `app_slug` is the identifier.

2. **epConfigQuery TenantID fix**: Previously missing from SELECT, so `TenantID` was
   always empty string even after R-4a. Fixed in this session. WS and SSE also
   benefit from this correction.

3. **HTTP + JSON-RPC error response**: `writeHTTPError` writes both the HTTP status
   code (for client-side routing) and a JSON-RPC error body (for A2A protocol
   compliance). This is the correct pattern for A2A pre-workflow failures.

4. **Wire format**: `{"kind":"text","text":"..."}` → `{"text":"..."}` per A2A spec.
   No known production callers, so not a breaking change.

---

## Temporary Compatibility Code Still in Place

None introduced in R-4e.

---

## Known Bugs and Blockers

- No `them.entry_points` rows with `entry_point_type = 'a2a'` exist in the DB.
  Live A2A end-to-end test requires creating one via admin API. All correctness
  verified by unit tests.
- Go bridge containers were manually recreated in this session (not via Compose).
  The Compose definition should be used for next startup to restore proper labeling.

---

## Files Most Relevant to the Next Task

- `go/internal/ws/handler.go` — source of truth for the pipeline; unification target
- `go/internal/sse/handler.go` — duplicate pipeline; unification target
- `go/internal/a2a/server.go` — R-4e implementation; unification target
- `go/internal/transport/transport.go` — shared interfaces; home for shared types
- `docs/architecture-v2/R4E_A2A_ARCHITECTURE_REVIEW.md` — §16 exclusions relevant
  to what unification should NOT bundle

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — only
   from server-resolved EPConfig or auth token cache lookup.
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — never `err.Error()` from service/DAL.
5. **Secrets never appear in log output** — use `cfg.SafeString()`.
6. **Every code change MUST have a test** — zero new failures before commit.
7. **Do NOT start the Execution Lifecycle Unification in the same session as R-4e.**

---

## Exact Next Single Focused Task

**Execution Lifecycle Unification (post-R-4e)**

WS, SSE, and A2A share ~200 lines of identical pipeline logic:
```
tryAuthenticate → epLoader.Load → access check → gate.Check →
session.Register → gate.Confirm → recorder.CreateRun →
bus.Subscribe → ExecuteWorkflow → block → cleanup
```

The unification should:
1. Extract this into an `ExecutionPipeline` struct (or a free function) in
   `internal/transport/` or a new `internal/execution/` package.
2. Update WS, SSE, and A2A handlers to call the shared code.
3. Keep handler-specific concerns (WS upgrade, SSE stream, A2A JSON-RPC framing)
   in each handler.

**Before starting:**
- Read `R4E_A2A_ARCHITECTURE_REVIEW.md` §16 (exclusions: do NOT bundle async A2A,
  HITL, streaming, or other features into this refactor)
- Read `R4E_IMPLEMENTATION_REPORT.md` §9 (next phase description)
- Read `ws/handler.go`, `sse/handler.go`, `a2a/server.go` side-by-side to identify
  the exact duplication boundary
- Plan the refactor before writing code; confirm scope with a brief architecture note

---

## Commands for Next Session

```bash
# Verify state
git log --oneline -3
git status
docker ps --format "{{.Names}}\t{{.Status}}" | grep -E "go-bridge|go-worker"

# Read before touching execution pipeline
cat docs/architecture-v2/R4E_IMPLEMENTATION_REPORT.md
cat go/internal/ws/handler.go
cat go/internal/sse/handler.go
cat go/internal/a2a/server.go

# Run sanity before any change
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

**First prompt for next session:**
> Continue the THEM Python-to-Go migration at `/home/avi/them`. R-4e is complete
> (A2A inbound path aligned with WS/SSE — auth, EPConfig, gate, session, Temporal).
> The next task is Execution Lifecycle Unification: extract the shared execution
> pipeline (auth → EPConfig → gate → session → Temporal) into a common helper shared
> by WS, SSE, and A2A, eliminating ~200 lines of duplication. Read
> `docs/architecture-v2/R4E_IMPLEMENTATION_REPORT.md` and
> `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` before writing any code. Plan
> the refactor with an architecture note before implementing. Do NOT bundle async
> A2A, HITL, streaming, or other features into this refactor.
