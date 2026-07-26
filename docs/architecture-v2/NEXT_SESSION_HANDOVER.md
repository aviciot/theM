# Next Session Handover — Critical Runtime Architecture Gate Complete

**Prepared:** 2026-07-26  
**Status:** Architecture gate and all blocking decisions resolved. Phase R-0 implementation authorized. No runtime implementation has started.

---

## Branch and HEAD

```
branch: main
HEAD:   8200a9f  docs(architecture): Critical Runtime Architecture Gate + Blocking Decisions
```

---

## Push Status

Push attempted — HTTPS credentials not available in this session.

**Push with:**
```bash
git push origin main
```

4 commits ahead of last known pushed state (e5bbca5 was the most recent pushed commit
before this session began; this session added 8200a9f on top of local commits
ac587af, dd78cec that were also unpushed).

Verify with:
```bash
git log --oneline origin/main..HEAD
```

---

## Commits Created This Session

1. `8200a9f` — docs(architecture): Critical Runtime Architecture Gate + Blocking Decisions

---

## Work Completed This Session

### Architecture Gate (planning only — no code implemented)

1. **`docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md`** — 1700+ line full gate
   document covering: corrected tenant boundary, session model, orchestration state model
   (selected: in-memory + durable checkpoints), streaming/backpressure, Temporal boundary,
   performance targets, code classification (25 reusable, 7 redesign), delivery plan
   (Phases R-0 through R-5), and resolved open decisions table.

2. **`docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md`** — concise resolution
   of all 8 open decisions with rationale, failure behaviors, required tests, and Phase R-0
   implementation checklist.

No runtime implementation was started. No code files under `go/` or `app/` were modified.
No DB schema, Redis keys, Temporal, Docker, Traefik, or UI was changed.

---

## Corrected Tenant Execution Boundary

**Tenant is the security and execution boundary. Application is a tenant-owned resource.**

The previous gate document incorrectly stated "the tenant boundary is the application."
This is corrected throughout the gate document (§2).

**Five-element RuntimeIdentity:**
```go
type RuntimeIdentity struct {
    TenantID      string // UUID string
    ApplicationID string // UUID string
    UserID        int64  // 0 for anonymous
    SessionID     string // UUID string
    RunID         string // UUID string; empty until CreateRun
}
```

**Application ownership verification — 5-step gate (must all pass before any DB resource is allocated):**

1. Authenticate the request (JWT or bearer token) → extract `tenant_id`
2. Resolve entry point slug from request path → look up `entry_points` row
3. Load the parent `applications` row for that entry point
4. Assert `applications.tenant_id == authenticated_tenant_id` — **mismatch = 403, never 404**
5. Assert the entry point belongs to that application (FK constraint + explicit check)

Only after all 5 steps pass may session registration, gate slot, or run creation begin.

---

## Selected Orchestration State Model

**In-memory accumulation with durable per-iteration checkpoints.**

The Python per-iteration DB rebuild is a workaround for Python's async model and must
not be carried into Go. Selected model:

```
Run start:       Load history from DB (HistoryLoader with LIMIT)
                 Initialize in-memory messages slice

Each iteration:  Append user message to in-memory slice
                 Call Provider.Stream() with in-memory slice
                 CHECKPOINT: write assistant turn to task_messages
                 Fan out tool calls (parallel goroutines)
                 CHECKPOINT: write tool results to task_messages
                 Append results to in-memory slice
                 CHECKPOINT: update tasks.tokens_used

Crash recovery:  Temporal re-executes the activity
                 HistoryLoader reads task_messages to last checkpoint
                 Run continues from last complete iteration
```

Zero per-iteration DB reads during normal execution. One DB write per iteration checkpoint.

---

## Phase R-0 Blocking Decisions — Resolved

| # | Decision | Resolution |
|---|---|---|
| OD-1 | Terminal event must-deliver | Separate `termCh chan event.Event` (buffer 1) alongside `evCh` |
| OD-2 | AppID + TenantID in SessionInfo | Populate both from `resolvedCfg` in WS/SSE handlers; no Redis migration needed |
| OD-7 | Shutdown drain timeout | Increase from 5s to 30s; configurable via `SHUTDOWN_DRAIN_SECONDS` |
| OD-8 | Agent HTTP context propagation | **CLOSED** — verified clean by code inspection; no change required |

OD-8 details: `invokeA2A` line 236 and `invokeHTTP` line 293 in
`internal/agentregistry/registry.go` both use `http.NewRequestWithContext(ctx, ...)`.
Context propagation is already correct.

---

## Phase R-1 Through R-5 — FROZEN

The gate for Phase R-1 through R-5 is **closed** until Phase R-0 is complete and
`go test -race ./...` passes.

| Phase R-1 blocker | Resolution |
|---|---|
| OD-3 | In-memory + checkpoints (not DB rebuild) |
| OD-4 | Orchestrator owns goroutines via WaitGroup + dual semaphore |
| OD-5 | Memory injection inline in orchestrator loop |

Phase R-2 blocker OD-6: DB BYTEA with 1MB limit; object store deferred.

---

## Exact Phase R-0 Scope

Single focused commit set — implement all 5 items together:

| Item | Finding | File(s) |
|---|---|---|
| Add `termCh chan event.Event` (buffer 1) to each subscriber; route `done`/`error` events to it; add `termCh` drain case to `streamEvents` select | L-1 / OD-1 | `internal/event/bus.go`, `internal/ws/handler.go`, `internal/sse/handler.go` |
| Populate `sessInfo.AppID` and `sessInfo.TenantID` from `resolvedCfg` in WS and SSE handlers | T-1, T-2 / OD-2 | `internal/ws/handler.go`, `internal/sse/handler.go`, `internal/session/session.go` (SessionInfo struct) |
| Increase `httpServer.Shutdown` context timeout to 30s; add `SHUTDOWN_DRAIN_SECONDS` env var (default 30, min 5) | OD-7 | `internal/server/server.go`, `internal/config/config.go` |
| Fix heartbeat goroutine to use a derived context, not `context.Background()` | L-2 | `internal/health/health.go` |
| Stop Subscribe goroutines before Redis close | L-3 | `internal/auth/token_cache.go` |

**No other files should be touched in Phase R-0.**

---

## Required Tests for Phase R-0

All must pass before commit:

| Test | What it proves |
|---|---|
| `TestBus_TerminalEventDeliveredOnFullBuffer` | `termCh` delivers `done` even when `evCh` is full |
| `TestSession_AppIDAndTenantIDPopulated` | Redis Hash contains `app_id` and `tenant_id` after registration |
| `TestServer_GracefulShutdownWith30sDrain` | 25s mock LLM response completes before server exits after SIGTERM |
| `go test ./...` | Zero regressions across all 212+ unit tests |
| `go test -race ./...` | No data races (requires Linux with GCC — run in Docker or CI) |
| Python sanity `01 02 03 04 15` | Zero Python regressions |

Update `go/TEST_INDEX.md` in the same commit as the code changes.

---

## Deployed / Live State

| Container | Status |
|---|---|
| `them-traefik` | Up — routing Wave 7 LLM provider CRUD to Go |
| `them-go-bridge` | Up — serving Go-owned routes |
| `them-bridge` | Up — Python fallback for all non-migrated routes |
| `them-postgres` | Up (healthy) |
| `them-redis` | Up (healthy) |
| `them-auth-service` | Up (healthy) |

Wave 7 complete: LLM provider CRUD served by Go. 5 active Go Traefik routers.

---

## Hard Constraints That Must Remain in Force

- No plaintext API key in any log, response, or error — `crypto.Decrypt` output zeroed after use
- `api_key_encrypted` never in JSON output structs
- 500 responses use static strings — never `err.Error()` from service/DAL layers
- Handler must NOT log request body (may contain plaintext api_key)
- `THE_M_SECRET_KEY` must never be logged; `SafeString()` must omit it
- `ErrConflict` → 409 with static message (MF-1 — already fixed, must not regress)
- Tenant boundary: 403 on tenant mismatch, never 404
- Application ownership verification must complete all 5 steps before any DB resource allocation
- Do not use DB name `odin` or schema `odin` — everything is `them`
- Never query `auth_service.*` tables directly — use `internal/auth/` from Go

---

## Files Most Relevant to Phase R-0

| File | Why |
|---|---|
| `go/internal/event/bus.go` | Add `termCh` to subscriber record; route terminal events |
| `go/internal/ws/handler.go` | Add `termCh` drain case; populate AppID + TenantID |
| `go/internal/sse/handler.go` | Same as ws/handler.go |
| `go/internal/session/session.go` | Add `AppID` and `TenantID` fields to `SessionInfo` |
| `go/internal/server/server.go` | Increase shutdown drain timeout |
| `go/internal/config/config.go` | Add `SHUTDOWN_DRAIN_SECONDS` env var |
| `go/internal/health/health.go` | Fix heartbeat goroutine context |
| `go/internal/auth/token_cache.go` | Stop Subscribe goroutines before Redis close |
| `go/TEST_INDEX.md` | Must be updated in the same commit |
| `docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md` | Full rationale + checklist |

---

## Exact Commands for Starting the Next Session

```bash
cd /opt/docker/them

# Verify working tree
git fetch origin
git log --oneline -5
git status

# Push if not yet pushed
git push origin main

# Verify stack is healthy
python3.12 scripts/tests/run_tests.py 01 02 03 04 15

# Verify Go tests pass
cd go && go test ./... && cd ..
```

---

## Exact First Prompt for the Next Session

> We are on branch main at HEAD 8200a9f. The Critical Runtime Architecture Gate is complete
> and all Phase R-0 blocking decisions are resolved. No implementation has started.
>
> Read these two documents before writing any code:
> - `docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md` — Phase R-0 checklist and all resolved decisions
> - `go/TEST_INDEX.md` — test index to update
>
> Then implement Phase R-0 as a single focused commit set. The 5 items are:
> 1. `termCh chan event.Event` (buffer 1) in `internal/event/bus.go` — route `done`/`error` events there; add `termCh` drain case in `ws/handler.go` and `sse/handler.go`
> 2. Populate `sessInfo.AppID` and `sessInfo.TenantID` from `resolvedCfg` in both `ws/handler.go` and `sse/handler.go`
> 3. Increase shutdown drain from 5s to 30s; add `SHUTDOWN_DRAIN_SECONDS` env var in `internal/config/config.go` and `internal/server/server.go`
> 4. Fix heartbeat goroutine to use a derived context (not `context.Background()`) in `internal/health/health.go`
> 5. Stop Subscribe goroutines before Redis close in `internal/auth/token_cache.go`
>
> Required tests before commit: `TestBus_TerminalEventDeliveredOnFullBuffer`, `TestSession_AppIDAndTenantIDPopulated`, `TestServer_GracefulShutdownWith30sDrain`, `go test ./...`, `go test -race ./...`, Python sanity `01 02 03 04 15`. Update `go/TEST_INDEX.md` in the same commit.
>
> Do not start Phase R-1. Stop after Phase R-0 commit and push.
> Use Sonnet for implementation.
