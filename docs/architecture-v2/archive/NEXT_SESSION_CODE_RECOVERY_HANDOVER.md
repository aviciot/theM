# Next Session Code Recovery Handover
# Go Runtime Migration — Post-Refactor State
# Date: 2026-07-24

---

## Git State

**HEAD:** `8c49246f777458b2af019f26e46d8a7ff6887c44`
**Branch:** `main`
**Working tree:** clean (one untracked doc file: `docs/architecture-v2/GO_WAVE_REVIEW.md` — not committed, safe to add)
**Ahead of origin/main by:** 13 commits (push was blocked by HTTPS credential requirement — must be pushed manually)

### Commit stack (unpushed, newest first)

```
8c49246  refactor: extract shared transport interfaces (internal/transport/)
13dafba  refactor: extract admin DAL (internal/admin/dal/)
72824e0  fix: test_11 bearer token uses user_id=1 (admin), not non-existent 99
5b32995  fix: Go admin handlers — align schemas, add HS256 auth, fix all admin routes
1828e39  docs: update STATUS.md routing table and CLAUDE.md trigger map for Waves 1-4
123e04f  feat: Wave 4 — Go bridge takes WS and SSE traffic for app entry points
fd3fe9e  feat: Wave 3 — gate, agent registry, and pod heartbeat wired in Go main.go
7dae98e  feat: Wave 2 — Go bridge takes admin write routes + PATCH method aliases
0ceef8d  feat: Wave 1 — Go bridge takes /health/live, /health/ready and GET admin reads
f897d5a  test: Phase 3 — Go↔Python parity contract test (test 37)
077770f  fix: P3 — unify Go↔Python Redis pub/sub channel names
2738347  fix: P1 — Go↔Python Temporal seam: workflow ID, HITL signal name, signal target
609b1d8  docs: Go runtime migration inventory + implementation plan
```

---

## Test Totals (verified post-refactor)

| Suite | Result |
|---|---|
| Go unit tests (`go test ./...` in Docker builder) | 21 packages — all `ok`, 0 `FAIL` |
| Python test suite (`python3.12 scripts/tests/run_tests.py`) | 929 passed, 0 failed, 6 skipped |

The 6 Python skips are legitimate env gaps: `structlog`/`fastapi` missing on host (tests 07/19), `code_agent` unreachable (test 24), `ADMIN_JWT` not set (test 14). None are masked failures.

---

## What Changed — Refactor 1: Admin DAL (`13dafba`)

### Before
All SQL query strings and `pgx` row-scan logic lived inline inside the four admin HTTP handler files. Handlers were simultaneously controllers and data access objects.

### After
New package `go/internal/admin/dal/` owns all SQL:

| New file | Contents |
|---|---|
| `dal/dal.go` | `Querier` interface, `RowScanner`/`SingleRowScanner` interfaces, all domain types (`Agent`, `AgentInput`, `Orchestrator`, `OrchestratorInput`, `Application`, `EntryPoint`, `ApplicationInput`, `EntryPointInput`, `Run`, `SignalInput`) |
| `dal/agents.go` | `ListAgents`, `GetAgent`, `CreateAgent`, `UpdateAgent`, `DeleteAgent` + `agentSelectCols` const + `scanAgent` helper |
| `dal/orchestrators.go` | `ListOrchestrators`, `GetOrchestrator`, `CreateOrchestrator`, `UpdateOrchestrator`, `DeleteOrchestrator` + `orchSelectCols` const + `scanOrch` helper |
| `dal/applications.go` | Full application + entry point query functions + `ListEPSlugsForApp` |
| `dal/runs.go` | `ListRuns`, `GetRun`, `GetRunContextID` + `runSelectCols` const + `scanRun` helper |

Handler files (`agents.go`, `orchestrators.go`, `applications.go`, `runs.go`) now contain zero SQL strings — they call dal functions and translate results to HTTP responses.

Business logic that stayed in handlers (intentionally not moved — belongs in a future service layer):
- Default value injection on Create (agents: transport, concurrency, retries, timeout)
- Soft-delete semantics (Delete sets `enabled=false`)
- EP type validation (`isValidEPType`)
- Pre-update slug fetch for cache invalidation (applications UpdateEntryPoint)
- Temporal workflow ID construction (`"ctx-" + contextID`) in runs Signal

`admin/middleware.go` re-exports dal types as type aliases (`type DBQuerier = dal.Querier`) so `admin_test.go` compiled without changes.

---

## What Changed — Refactor 2: Shared Transport (`8c49246`)

### Before
Five interfaces and the `tokenHash` function were declared identically in both `internal/ws/handler.go` and `internal/sse/handler.go`. Any change to these contracts required updating two files.

### After
New package `go/internal/transport/transport.go` is the single source:

| Extracted | Type |
|---|---|
| `Authenticator` | interface |
| `SessionStore` | interface |
| `GateStore` | interface |
| `EPConfigLoader` | interface |
| `TemporalClientExecutor` | interface |
| `TokenHash` | `func(token string) string` (SHA-256, exported) |

`ws/handler.go` and `sse/handler.go` now use type aliases:
```go
type Authenticator = transport.Authenticator
var tokenHash = transport.TokenHash
// (etc.)
```

No concrete implementations changed. No external contracts changed.

---

## Current Package/Layer Map

```
request
  → chi router (internal/server/server.go)
    → admin handler (internal/admin/*.go)       ← thin HTTP: parse, validate, call dal, respond
        → internal/admin/dal/*.go               ← all SQL + scan logic
          → pgxpool.Pool (direct)
        → admin.AdminCache (Redis, direct)       ← cache invalidation still in handler
        → internal/temporal/signaler.go          ← HITL signal only

request
  → internal/ws/handler.go or internal/sse/handler.go
      ← shared interfaces from internal/transport/transport.go
      ↓ auth: transport.Authenticator (impl: auth.Cache)
      ↓ gate: transport.GateStore (impl: gate.Gate)
      ↓ session: transport.SessionStore (impl: session.Store)
      ↓ EP config: transport.EPConfigLoader (impl: epconfig.Loader)
      ↓ run recording inline (calls internal/runrecorder)
      → internal/orchestrator/ (agentic loop)
        → internal/llm/ (Anthropic provider)
        → internal/agentregistry/ (agent dispatch + Redis cache)
      → internal/temporal/ (workflow start, HITL signal)
      → internal/runstream/ (event fan-out: Pub/Sub + Streams)
```

### Remaining deviations from clean layering

| Deviation | Location | Risk |
|---|---|---|
| Cache invalidation logic in handlers | `admin/applications.go:312–316` | Low — isolated, load-bearing during migration |
| Business logic (defaults, soft-delete, EP validation) in handlers | `admin/*.go` | Medium — grows with each new route |
| WS/SSE handlers own session + gate + auth + recording | `ws/handler.go`, `sse/handler.go` | High — largest spaghetti risk |
| `main.go:run()` is 260-line wiring monolith with `buildAppsHandler` routing logic | `cmd/them/main.go:281–311` | Medium — readability, not correctness |
| No shared DB schema contract (Go structs hand-written against Python models) | all admin dal files | High — root cause of Waves 1-4 schema mismatches |

---

## Temporary Compatibility Code (load-bearing — do not remove until Python decommissioned)

| Location | What it is |
|---|---|
| `admin/agents.go:83` | `r.Patch("/agents/{id}", h.Update)` — PATCH alias for Python frontend |
| `admin/orchestrators.go:80` | `r.Patch("/orchestrators/{name}", h.Update)` — same |
| `admin/applications.go:83,88` | Two PATCH aliases for app + EP update |
| `admin/runs.go:182` | Hardcoded `"ctx-"` Temporal workflow ID prefix matching Python |
| `admin/applications.go:15` | `validEPTypes` — "Must stay in sync with Python's _VALID_EP_TYPES list" |
| `ws/handler.go`, `sse/handler.go` | `tokenHash` via `transport.TokenHash` — must match Python's SHA-256 format |
| `runrecorder/recorder.go:44–46` | `NewRecorder` — backward-compat alias for `New` |
| `runrecorder/recorder.go:104–107` | `UpdateStatus` — compat wrapper over `UpdateRunStatus` |
| `agentregistry/registry.go:19` | `invalidateChannel = "them:agents:changed"` — must match Python publisher |
| `cmd/them/main.go:281–311` | `buildAppsHandler` — URL param rewriting (`slug` → `entry_point_slug`) |

---

## Remaining Architecture Risks

**1. WS/SSE handlers as mini-services (highest risk)**
`ws/handler.go` and `sse/handler.go` each own session lifecycle, gate admission, auth, run recording, and Temporal workflow management. The shared interface extraction (Refactor 2) removes duplication but does not reduce the handler size or complexity. Adding a third transport (voice, WebRTC) will require duplicating the entire session/gate/Temporal orchestration again unless a shared `SessionRuntime` or `TransportBase` is extracted first.

**2. No DB schema contract enforcement**
Go structs in `dal/` are hand-written against `db/001_schema.sql`. The Waves 1-4 cutover found 40+ column mismatches. Nothing prevents this from recurring when schema migrations add columns. The fix is integration tests against the live DB, not unit tests with fakes.

**3. Admin business logic accumulation**
Default value injection, soft-delete, EP type validation, and cache invalidation side-effects all live in `admin/*.go` handlers. The DAL refactor moved SQL out but left these in place. Each new admin route adds more business logic to the handler layer unless a service layer is introduced.

---

## Wave 5 Status

**Wave 5 has not started.**

The migration plan calls for Wave 5 to take additional Python-owned routes (tokens CRUD, sessions, dashboard WS) into Go. None of those routes have been migrated, no Traefik labels have been added for them, and no Go handler code has been written for them.

Current Traefik route ownership summary:

| Route group | Owner |
|---|---|
| `/health/live`, `/health/ready` | Go (priority 130) |
| `GET /api/v1/admin/agents*` | Go (priority 110) |
| `GET /api/v1/admin/orchestrators*` | Go (priority 110) |
| `GET /api/v1/admin/applications*` | Go (priority 110) |
| `GET /api/v1/runs*` | Go (priority 110) |
| `POST/PUT/PATCH/DELETE /api/v1/admin/agents*` | Go (priority 115) |
| `POST/PUT/PATCH/DELETE /api/v1/admin/orchestrators*` | Go (priority 115) |
| `POST/PUT/PATCH/DELETE /api/v1/admin/applications*` | Go (priority 115) |
| `/apps/{slug}/ws` | Go (priority 120) |
| `/apps/{slug}/sse` | Go (priority 120) |
| All other routes | Python (priority 100) |

---

## Recommended Next Focused Task

**Do not start Wave 5 immediately.**

The next session must first:

1. **Verify the completed refactor is structurally sound** — read `internal/admin/dal/` and `internal/transport/` in full, confirm no SQL leaked back into handlers, confirm type aliases in `ws`/`sse` are not masking interface drift, run `go test -race ./...` to surface any data races introduced by the structural move.

2. **Assess whether a service layer is needed before Wave 5** — the DAL refactor moved SQL out of handlers but left business logic (default value injection, soft-delete, EP type validation, cache invalidation side-effects) in the handler layer. Decide explicitly: introduce `internal/admin/service/` before adding more routes, or accept handler-level business logic for the duration of the migration and clean up post-decommission.

3. **Only then decide on Wave 5 scope** — if the service layer assessment concludes it is not needed yet, proceed to Wave 5 (`/api/v1/admin/tokens` CRUD). If it is needed, do that refactor first as a third bounded step before any new route migrations.
