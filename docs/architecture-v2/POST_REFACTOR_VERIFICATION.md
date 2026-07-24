# Post-Refactor Verification — Waves 1–4
# Go Runtime Migration — Admin DAL + Transport Interfaces
# Verified: 2026-07-24 against commits 13dafba and 8c49246

---

## Verdict Summary

| Check | Result |
|---|---|
| All admin SQL moved to DAL | PASS — zero SQL strings in any handler file |
| Handlers no longer call pgx directly | PASS — zero pgx imports in handlers |
| WS/SSE use internal/transport types | PASS — all five interfaces consumed as aliases |
| Duplicate interfaces removed | PASS — declared once in transport, aliased everywhere |
| TokenHash duplication | PARTIAL — one private copy survives in auth/jwt.go |
| Remaining compat/migration shims | 13 identified instances, all load-bearing for now |
| Business logic in handlers | YES — medium accumulation, specific list below |
| Service layer required before Wave 5 | NO — proceed with explicit scoped exception for tokens |

**Wave 5 safe to start:** YES, with one prerequisite cleanup noted below.

---

## 1. Admin SQL Migration

**All SQL is in the DAL. Zero SQL strings remain in handler files.**

Handler files confirm (grep `INSERT|UPDATE|SELECT|FROM them\.` returns zero hits):

- `internal/admin/agents.go` — calls `h.dal.ListAgents`, `GetAgent`, `CreateAgent`, `UpdateAgent`, `DeleteAgent`
- `internal/admin/orchestrators.go` — calls `h.dal.ListOrchestrators`, `GetOrchestrator`, `CreateOrchestrator`, `UpdateOrchestrator`, `DeleteOrchestrator`
- `internal/admin/applications.go` — calls `h.dal.ListApplications`, `GetApplication`, `CreateApplication`, `UpdateApplication`, `DeleteApplication`, `ListEntryPoints`, `CreateEntryPoint`, `GetEntryPointSlug`, `UpdateEntryPoint`, `DeleteEntryPoint`, `ListEPSlugsForApp`
- `internal/admin/runs.go` — calls `h.dal.ListRuns`, `GetRun`, `GetRunContextID`

All SQL is confined to:
- `internal/admin/dal/agents.go` — `agentSelectCols` const + five query functions
- `internal/admin/dal/orchestrators.go` — `orchSelectCols` const + five query functions
- `internal/admin/dal/applications.go` — eight query functions
- `internal/admin/dal/runs.go` — `runSelectCols` const + three query functions

The pgx pool is wrapped by `internal/admin/pgx.go`, which implements `dal.Querier`. The DAL operates against the interface only — no pgx imports in `dal/`.

---

## 2. Direct pgx Usage in Handlers

**NONE.** Zero pgx imports in any of the four handler files, middleware.go, or router.go.

pgx appears only in:
- `internal/admin/pgx.go` — correct placement, the pool adapter
- No pgx in `internal/ws/handler.go` or `internal/sse/handler.go`

---

## 3. Business Logic Remaining in Handlers

This is a medium-risk accumulation point. All items below are in handler code, not a service layer.

**`internal/admin/agents.go`**
- Lines 56–57: Required-field validation (`slug`, `display_name` non-empty)
- Lines 56–70 (Create): Default injection — `Transport` → `"a2a_async"`, `MaxConcurrency` → 5, `MaxRetries` → 2, `TimeoutSeconds` → 30
- Lines 114–115 (Update): `MaxConcurrency` default re-applied on every update
- Lines 59–61, 117–119: `enabled` bool derivation from nullable pointer

**`internal/admin/orchestrators.go`**
- Lines 53–54: Required-field validation (`name` non-empty)
- Lines 60–64 (Create): Default injection — `MaxIterations` → 10, `HistoryWindow` → 20
- Lines 57–59, 100–102: `enabled` bool derivation

**`internal/admin/applications.go`**
- Lines 16–28: `validEPTypes` map + `isValidEPType` — enforces allowed EP types; comment states "Must stay in sync with the Python platform's `_VALID_EP_TYPES` list"
- Lines 190–197 (`CreateEntryPoint`): EP type validation + 422 branch
- Lines 228–231 (`UpdateEntryPoint`): EP type re-validation
- Lines 239: Pre-update slug fetch (`h.dal.GetEntryPointSlug`) for cache invalidation side-effect — two-step read-then-write pattern
- Lines 70–86: `invalidateEP` and `invalidateAppEPs` — cache invalidation side-effects on the handler struct

**`internal/admin/middleware.go`**
- Lines 47–52: `CacheInvalidator` interface declared in the admin handler package (not in infrastructure)
- Lines 54–57: `TemporalSignaler` interface declared in the admin handler package

**`internal/admin/runs.go`**
- Lines 33–40: Query parameter parsing and limit defaulting (default 50)
- Line 98: `workflowID := "ctx-" + contextID` — Temporal workflow ID construction; naming convention must match Python

**`internal/ws/handler.go` line 464** and **`internal/sse/handler.go` line 476**: same `"ctx-"` prefix — three independent sites for the same convention.

---

## 4. WS and SSE Transport Usage

**Confirmed. Both handlers consume all five interfaces as aliases from `internal/transport`.**

`internal/transport/transport.go` declares (lines 24, 30, 37, 46, 53):
- `Authenticator`
- `SessionStore`
- `GateStore`
- `EPConfigLoader`
- `TemporalClientExecutor`
- `TokenHash` — exported function, SHA-256 hex (line 61)

`internal/ws/handler.go` (lines 76–93, 666):
```go
type Authenticator = transport.Authenticator
type SessionStore = transport.SessionStore
type GateStore = transport.GateStore
type EPConfigLoader = transport.EPConfigLoader
type TemporalClientExecutor = transport.TemporalClientExecutor
var tokenHash = transport.TokenHash
```

`internal/sse/handler.go` (lines 62–83):
```go
var tokenHash = transport.TokenHash
type Authenticator = transport.Authenticator
type SessionStore = transport.SessionStore
type GateStore = transport.GateStore
type EPConfigLoader = transport.EPConfigLoader
type TemporalClientExecutor = transport.TemporalClientExecutor
```

Zero `interface {` declarations in ws/ or sse/ — confirmed by grep.

---

## 5. Duplicated Interfaces and TokenHash

**Interfaces: FULLY removed.** Before the refactor, five interfaces were declared identically in both ws and sse. Now declared once in transport, consumed as aliases. No drift.

**TokenHash: PARTIAL.** `transport.TokenHash` is the canonical exported function. Both ws and sse alias it. However, a private `tokenHash` function survives in `internal/auth/jwt.go` (lines 287–291):

```go
func tokenHash(rawToken string) string {
    h := sha256.Sum256([]byte(rawToken))
    return fmt.Sprintf("%x", h)
}
```

Used internally by `token_cache.go` (lines 123, 186). Functionally identical to `transport.TokenHash` (same `%x` format). No correctness bug today — the risk is silent divergence if either is changed independently. The auth package does not import transport; unifying requires a dependency decision.

---

## 6. Remaining Duplication and Migration Compatibility Code

All 13 instances below are load-bearing for the Python→Go migration period.

| Instance | File | Lines | What it is |
|---|---|---|---|
| PATCH alias (agents) | `internal/admin/agents.go` | 31 | Python frontend sends PATCH; Go accepts PUT and PATCH |
| PATCH alias (orchestrators) | `internal/admin/orchestrators.go` | 31 | Same |
| PATCH alias (applications) | `internal/admin/applications.go` | 50 | Same |
| PATCH alias (entry points) | `internal/admin/applications.go` | 55 | Same |
| `"ctx-"` prefix | `internal/admin/runs.go` | 98 | Temporal workflow ID must match Python registration |
| `"ctx-"` prefix | `internal/ws/handler.go` | 464 | Same convention, independent site |
| `"ctx-"` prefix | `internal/sse/handler.go` | 476 | Same convention, independent site |
| `validEPTypes` sync comment | `internal/admin/applications.go` | 17 | Must match Python's `_VALID_EP_TYPES`; no test enforces this |
| `NewRecorder` alias | `internal/runrecorder/recorder.go` | 44–45 | Backward-compat alias for `New` |
| `UpdateStatus` wrapper | `internal/runrecorder/recorder.go` | 104–105 | Compat wrapper over `UpdateRunStatus` |
| `invalidateChannel` string | `internal/agentregistry/registry.go` | 19 | `"them:agents:changed"` must match Python Redis pub/sub key |
| `buildAppsHandler` inline routing | `cmd/them/main.go` | 284–310 | Duplicates logic already in `ws.AppsWSRoute()` + `sse.AppsSSERoute()`; 27 lines of wiring code |
| Private `tokenHash` in auth | `internal/auth/jwt.go` | 287–291 | SHA-256 impl independent of `transport.TokenHash` |

**The `buildAppsHandler` duplication (main.go:284–310) is the only non-load-bearing instance.** The `AppsWSRoute` and `AppsSSERoute` methods in ws/handler.go (208–218) and sse/handler.go (199–216) are correct but unused — `main.go` reinvents the same URL-remapping inline. This is dead code in the handler files and live duplication in main.

---

## 7. Admin Service Layer — Required Before Wave 5?

**Decision: NOT required. Proceed to Wave 5 with one scoped exception.**

**Case for proceeding without:**
- Wave 5 target is `/api/v1/admin/tokens` CRUD — a single bounded resource.
- The established DAL pattern supports adding `dal/tokens.go` directly.
- Existing handler business logic (validation, defaults, soft-delete) is self-contained per resource.
- The four current handlers follow consistent structure — a tokens handler fits the pattern.

**Case for a service layer first:**
- Token creation requires `crypto/rand` generation + SHA-256 hashing — algorithmic business logic that does not belong in an HTTP handler.
- Cache invalidation side-effects (`invalidateEP`, `invalidateAppEPs`) are already misplaced in handlers.
- The Temporal workflow ID construction (`"ctx-"` prefix) is a cross-cutting naming contract duplicated in three files.
- A service layer would own all three; without one, tokens adds a fourth misplaced item.

**Compromise:** Add tokens handler following the existing pattern but extract token generation (random generation + hashing) into `internal/admin/tokengen/` or a small unexported helper package. Do not wait for a full `internal/admin/service/` layer. Set an explicit checkpoint before Wave 6 to reassess whether the service layer is required before continuing.

---

## 8. Single Next Focused Task

**Remove the duplicate `buildAppsHandler` in `cmd/them/main.go` (lines 284–310) by wiring the already-correct `ws.Handler.AppsWSRoute()` and `sse.Handler.AppsSSERoute()` methods that exist but are unused.**

This is the only non-load-bearing duplication, it has no behavioral change, it cleans up `main.go` before Wave 5 adds another handler mount there, and it eliminates the dead `AppsWSRoute`/`AppsSSERoute` code in the handler files. The fix is ~5 lines. It should be done as a standalone commit before Wave 5 begins so the Wave 5 diff is not mixed with pre-existing duplication cleanup.

---

## Appendix — File Map Verified

```
go/
  cmd/them/main.go                          — wiring; buildAppsHandler duplication at 284–310
  internal/transport/transport.go           — five interfaces + TokenHash (canonical)
  internal/admin/
    agents.go                               — handler, zero SQL
    orchestrators.go                        — handler, zero SQL
    applications.go                         — handler + validEPTypes business rule
    runs.go                                 — handler, zero SQL
    middleware.go                           — CacheInvalidator + TemporalSignaler interfaces
    router.go                               — route registration
    pgx.go                                  — pool adapter implementing dal.Querier
    dal/
      agents.go                             — all SQL for agents
      orchestrators.go                      — all SQL for orchestrators
      applications.go                       — all SQL for applications
      runs.go                               — all SQL for runs
  internal/ws/handler.go                    — WS handler; all interfaces aliased from transport
  internal/sse/handler.go                   — SSE handler; all interfaces aliased from transport
  internal/auth/jwt.go                      — private tokenHash at 287–291 (duplication risk)
  internal/runrecorder/recorder.go          — NewRecorder + UpdateStatus compat wrappers
  internal/agentregistry/registry.go        — invalidateChannel string must match Python
```
