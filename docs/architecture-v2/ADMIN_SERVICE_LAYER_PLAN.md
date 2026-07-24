# Admin Service Layer — Implementation Plan
# Go Runtime Migration — Refactor 3
# Author: Go architect session, 2026-07-24
# Scope: introduce `internal/admin/service/` between HTTP handlers and the DAL

---

## PROCEED

Reviewed against every hard constraint (see §14 below). No constraint is violated. This
refactor is a pure internal restructuring: no DB schema change, no API contract change, no
route change, no new framework, no orchestration redesign, no destructive operation, no Wave 5
work, and no token CRUD implementation. The token boundary is designed as a seam only.

Proceed to implementation in the commit order defined in §11.

---

## 0. Current State (verified from source, not docs)

Three layers exist today; the middle "service" role is squatting inside the handler files:

```
HTTP handler (internal/admin/*.go)   ← parse + validate + defaults + soft-delete + cache + temporal + respond
    → dal.DB (internal/admin/dal/*.go) ← SQL + scan
        → dal.Querier (pgx adapter in internal/admin/pgx.go)
```

Each handler constructs its own `dal.DB` internally:

- `agents.go:22`      → `dal: dal.NewDB(db)`
- `orchestrators.go:22` → `dal: dal.NewDB(db)`
- `applications.go:41` → `dal: dal.NewDB(db)`
- `runs.go:22`        → `dal: dal.NewDB(db)`

Handlers hold `db DBQuerier`, `cache CacheInvalidator`, `temporal TemporalSignaler`, and a
private `*dal.DB`. Business logic (defaults, soft-delete intent, EP type validation, cache
invalidation orchestration, Temporal workflow-ID construction) is interleaved with HTTP concerns.

`CacheInvalidator` (middleware.go:47-52) and `TemporalSignaler` (middleware.go:54-57) are declared
in the **handler** package today.

---

## 1. Package Structure

New package: `go/internal/admin/service/`

| File | Contents |
|---|---|
| `service/service.go` | `Dal` interface (service's view of the DAL), `Cache` interface, `Temporal` interface, shared constructor helpers, the shared `enabledOrDefault(*bool) bool` helper, and package doc |
| `service/errors.go` | Typed error values/types: `ErrValidation`, `ErrNotFound`, `ErrConflict`, plus `ValidationError` struct and constructors |
| `service/agents.go` | `AgentService` struct + `List/Get/Create/Update/Delete`. Owns agent defaults + required-field validation + soft-delete + agents-registry cache invalidation |
| `service/orchestrators.go` | `OrchService` struct + `List/Get/Create/Update/Delete`. Owns orchestrator defaults + required-field validation + soft-delete + per-name cache invalidation |
| `service/applications.go` | `AppService` struct + application + entry-point methods. Owns EP type validation, EP required-field validation, the read-then-write slug fetch for rename, and all EP cache-invalidation orchestration. Owns `validEPTypes` / `isValidEPType` and `epConfigChannel` |
| `service/tokens.go` | **Seam only** — `TokenGenerator` interface + `TokenService` skeleton with a documented `// Wave 5` stub. No token CRUD, no DAL calls, no route. Compiles, does nothing callable from a route. (See §7.) |
| `service/service_test.go` | Unit tests for all service logic, no HTTP, using a `fakeDal` + `fakeCache` + `fakeTemporal` local to the service package |

No other new packages. The `dal` package is unchanged. `pgx.go` is unchanged.

---

## 2. Interfaces

All interfaces below live in `service/service.go` unless noted. The service depends on **narrow
interfaces it declares itself** (consumer-defined interfaces), not on the concrete `*dal.DB`. The
real `*dal.DB` already has every method these interfaces name, so `*dal.DB` satisfies `Dal`
structurally with zero DAL changes.

### 2.1 `Dal` — the service's view of the data layer

```go
// Dal is the data-access surface the admin services depend on.
// The concrete *dal.DB satisfies this interface structurally; the service
// package never imports pgx and never sees SQL.
type Dal interface {
	// Agents
	ListAgents(ctx context.Context) ([]dal.Agent, error)
	GetAgent(ctx context.Context, id string) (dal.Agent, error)
	CreateAgent(ctx context.Context, in dal.AgentInput, enabled bool) (string, error)
	UpdateAgent(ctx context.Context, id string, in dal.AgentInput, enabled bool) error
	DeleteAgent(ctx context.Context, id string) error

	// Orchestrators
	ListOrchestrators(ctx context.Context) ([]dal.Orchestrator, error)
	GetOrchestrator(ctx context.Context, name string) (dal.Orchestrator, error)
	CreateOrchestrator(ctx context.Context, in dal.OrchestratorInput, enabled bool) (string, error)
	UpdateOrchestrator(ctx context.Context, name string, in dal.OrchestratorInput, enabled bool) error
	DeleteOrchestrator(ctx context.Context, name string) error

	// Applications + entry points
	ListApplications(ctx context.Context) ([]dal.Application, error)
	GetApplication(ctx context.Context, id string) (dal.Application, error)
	CreateApplication(ctx context.Context, name string, enabled bool) (string, error)
	UpdateApplication(ctx context.Context, id, name string, enabled bool) error
	DeleteApplication(ctx context.Context, id string) error
	ListEntryPoints(ctx context.Context, appID string) []dal.EntryPoint
	CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled bool) (string, error)
	GetEntryPointSlug(ctx context.Context, epID, appID string) (string, error)
	UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled bool) error
	DeleteEntryPoint(ctx context.Context, epID, appID string) error
	ListEPSlugsForApp(ctx context.Context, appID string) []string

	// Runs
	ListRuns(ctx context.Context, contextID string, limit int) ([]dal.Run, error)
	GetRun(ctx context.Context, runID string) (dal.Run, error)
	GetRunContextID(ctx context.Context, runID string) (string, error)
}
```

Note: this is one wide interface intentionally. An alternative is four narrow interfaces
(`AgentDal`, `OrchDal`, `AppDal`, `RunDal`). Chosen single interface because (a) `*dal.DB`
implements all of them anyway, (b) the router wires one `*dal.DB` into all four services, and
(c) it keeps the fake in tests to one type. If a future maintainer wants tighter coupling, the
narrow split is a mechanical follow-up — not required now.

### 2.2 `Cache` — cache invalidation surface

Moved out of the handler package. Identical method set to today's `admin.CacheInvalidator`.

```go
// Cache invalidates Redis caches on mutations. Nil is tolerated by every
// service method (no-op) so tests and dev mode can pass nil.
type Cache interface {
	Del(ctx context.Context, key string) error
	Publish(ctx context.Context, channel, message string) error
}
```

### 2.3 `Temporal` — HITL signal surface

Moved out of the handler package. Identical method set to today's `admin.TemporalSignaler`.

```go
// Temporal sends HITL signals to workflows.
type Temporal interface {
	SignalRun(ctx context.Context, workflowID string, payload []byte) error
}
```

### 2.4 `TokenGenerator` — future seam (in `service/tokens.go`)

Declared now, implemented in Wave 5. See §7.

```go
// TokenGenerator produces an opaque bearer token and its storage hash.
// Wave 5 will supply a crypto/rand + SHA-256 implementation. Declared now
// so the service package's dependency surface is stable before token CRUD lands.
type TokenGenerator interface {
	// Generate returns (plaintextToken, storageHash). plaintext is shown to the
	// caller once; storageHash is what the DAL persists.
	Generate() (plaintext string, hash string, err error)
}
```

---

## 3. Concrete Types

All in the `service` package.

```go
// service/agents.go
type AgentService struct {
	dal   Dal
	cache Cache
}
func NewAgentService(d Dal, c Cache) *AgentService { return &AgentService{dal: d, cache: c} }

// service/orchestrators.go
type OrchService struct {
	dal   Dal
	cache Cache
}
func NewOrchService(d Dal, c Cache) *OrchService { return &OrchService{dal: d, cache: c} }

// service/applications.go
type AppService struct {
	dal   Dal
	cache Cache
}
func NewAppService(d Dal, c Cache) *AppService { return &AppService{dal: d, cache: c} }

// service/runs.go
type RunService struct {
	dal      Dal
	temporal Temporal
}
func NewRunService(d Dal, t Temporal) *RunService { return &RunService{dal: d, temporal: t} }

// service/tokens.go  (seam only)
type TokenService struct {
	dal Dal
	gen TokenGenerator
	// Wave 5: cache Cache for token revocation pub/sub
}
func NewTokenService(d Dal, g TokenGenerator) *TokenService { return &TokenService{dal: d, gen: g} }
```

Each service constructor takes its dependencies explicitly — no globals, no package-level state.

### Service method signatures (return domain values + typed errors, never touch `http`)

```go
// AgentService
func (s *AgentService) List(ctx context.Context) ([]dal.Agent, error)
func (s *AgentService) Get(ctx context.Context, id string) (dal.Agent, error)
func (s *AgentService) Create(ctx context.Context, in dal.AgentInput) (id string, err error)
func (s *AgentService) Update(ctx context.Context, id string, in dal.AgentInput) error
func (s *AgentService) Delete(ctx context.Context, id string) error

// OrchService
func (s *OrchService) List(ctx context.Context) ([]dal.Orchestrator, error)
func (s *OrchService) Get(ctx context.Context, name string) (dal.Orchestrator, error)
func (s *OrchService) Create(ctx context.Context, in dal.OrchestratorInput) (id string, err error)
func (s *OrchService) Update(ctx context.Context, name string, in dal.OrchestratorInput) error
func (s *OrchService) Delete(ctx context.Context, name string) error

// AppService
func (s *AppService) List(ctx context.Context) ([]dal.Application, error)
func (s *AppService) Get(ctx context.Context, id string) (dal.Application, error)   // includes EntryPoints
func (s *AppService) Create(ctx context.Context, in dal.ApplicationInput) (id string, err error)
func (s *AppService) Update(ctx context.Context, id string, in dal.ApplicationInput) error
func (s *AppService) Delete(ctx context.Context, id string) error
func (s *AppService) CreateEntryPoint(ctx context.Context, appID string, in dal.EntryPointInput) (id string, err error)
func (s *AppService) UpdateEntryPoint(ctx context.Context, appID, epID string, in dal.EntryPointInput) error
func (s *AppService) DeleteEntryPoint(ctx context.Context, appID, epID string) error

// RunService
func (s *RunService) List(ctx context.Context, contextID string, limit int) ([]dal.Run, error)
func (s *RunService) Get(ctx context.Context, runID string) (dal.Run, error)
func (s *RunService) Signal(ctx context.Context, runID string, payload json.RawMessage) error
```

---

## 4. Dependency Direction

```
                internal/server (router wiring)
                          │  constructs services, injects dal + cache + temporal
                          ▼
   HTTP handler ─────────────────────────────► admin service ───────► DAL ───────► pgx pool
 internal/admin/*.go                        internal/admin/service    internal/admin/dal   internal/admin/pgx.go
      │                                              │                        │
      │ imports: service, dal (types),               │ imports: dal (types    │ imports: nothing internal
      │          chi, encoding/json, net/http        │  + method surface)     │  (only pgx, encoding/json, context)
      │                                              │ imports: NOT net/http, NOT chi, NOT pgx
      ▼
  auth (middleware only, unchanged)
```

Import rules, enforced by review:

- `admin` (handlers) imports `admin/service` and `admin/dal` (for the shared struct types passed
  as request bodies / returned as responses) — allowed, no cycle.
- `admin/service` imports `admin/dal` (types + it declares the `Dal` interface those methods live on).
  `admin/service` must **not** import `net/http`, `chi`, or `pgx`.
- `admin/dal` imports nothing internal (unchanged: `context`, `encoding/json` only).
- No package imports `admin` back → no cycle. `service` never imports `admin`.

The `*dal.DB` value satisfies `service.Dal` structurally, so `service` depending on `dal` for the
**concrete return/argument types** (`dal.Agent`, `dal.AgentInput`, …) is the only `dal` dependency —
there is no reverse edge.

---

## 5. Logic Moved From Each Handler → Service

Line numbers cite the current handler files as read this session.

### 5.1 `agents.go` → `service/agents.go`

| Source lines | What | Destination |
|---|---|---|
| 52-54 | Required-field validation (`slug`, `display_name` non-empty) → return `ErrValidation` | `AgentService.Create` |
| 56-58 | Default `Transport` → `"a2a_async"` | `AgentService.Create` |
| 59-62 | `enabled` derivation from `*bool` (default true) | `AgentService.Create` (via `enabledOrDefault`) |
| 63-65 | Default `MaxConcurrency` → 5 | `AgentService.Create` |
| 66-68 | Default `MaxRetries` → 2 | `AgentService.Create` |
| 69-71 | Default `TimeoutSeconds` → 30 | `AgentService.Create` |
| 73 | `dal.CreateAgent` call | `AgentService.Create` |
| 79 / 150-154 | `invalidateCache` → `Del("them:agents:registry")` | `AgentService.Create/Update/Delete` (private `s.invalidate(ctx)`) |
| 114-116 | Default `MaxConcurrency` → 5 on update | `AgentService.Update` |
| 117-120 | `enabled` derivation on update | `AgentService.Update` (via `enabledOrDefault`) |
| 122 | `dal.UpdateAgent` call | `AgentService.Update` |
| 140 | `dal.DeleteAgent` (soft-delete intent lives in DAL SQL; service just calls it + invalidates) | `AgentService.Delete` |

### 5.2 `orchestrators.go` → `service/orchestrators.go`

| Source lines | What | Destination |
|---|---|---|
| 52-54 | Required-field validation (`name` non-empty) → `ErrValidation` | `OrchService.Create` |
| 56-59 | `enabled` derivation | `OrchService.Create` (via `enabledOrDefault`) |
| 60-62 | Default `MaxIterations` → 10 | `OrchService.Create` |
| 63-65 | Default `HistoryWindow` → 20 | `OrchService.Create` |
| 67 | `dal.CreateOrchestrator` call | `OrchService.Create` |
| 73 / 129-134 | `invalidateCache(name)` → `Del("them:orchestrators:{name}")` | `OrchService.Create/Update/Delete` (private `s.invalidate(ctx, name)`) |
| 100-103 | `enabled` derivation on update | `OrchService.Update` (via `enabledOrDefault`) |
| 105 | `dal.UpdateOrchestrator` call | `OrchService.Update` |
| 119 | `dal.DeleteOrchestrator` call | `OrchService.Delete` |

Note: `Create` returns id; the service returns `(id string, err error)`. The `name` used for the
`Location` header and cache key is `input.Name`, still available to the handler after the call.

### 5.3 `applications.go` → `service/applications.go`

| Source lines | What | Destination |
|---|---|---|
| 14 | `epConfigChannel` const | `service/applications.go` |
| 16-24 | `validEPTypes` map | `service/applications.go` |
| 26-30 | `isValidEPType` | `service/applications.go` (unexported) |
| 69-76 | `invalidateEP` helper | `AppService` private method `invalidateEP(ctx, slug)` |
| 78-87 | `invalidateAppEPs` helper (calls `ListEPSlugsForApp`) | `AppService` private method `invalidateAppEPs(ctx, appID)` |
| 96-99 | Required-field validation (`name` non-empty) | `AppService.Create` |
| 100-103 | `enabled` derivation | `AppService.Create` (via `enabledOrDefault`) |
| 105 | `dal.CreateApplication` call | `AppService.Create` |
| 129 | `ListEntryPoints` composition into `a.EntryPoints` | `AppService.Get` |
| 146-149 | `enabled` derivation on update | `AppService.Update` |
| 151 | `dal.UpdateApplication` call | `AppService.Update` |
| 156 | `invalidateAppEPs` on update | `AppService.Update` |
| 168 | `dal.DeleteApplication` call | `AppService.Delete` |
| 173 | `invalidateAppEPs` on delete | `AppService.Delete` |
| 190-193 | EP required-field validation (`slug`, `entry_point_type`) → `ErrValidation` | `AppService.CreateEntryPoint` |
| 194-198 | EP type validation → `ErrUnprocessable` (422 seam) | `AppService.CreateEntryPoint` |
| 199-202 | `enabled` derivation | `AppService.CreateEntryPoint` |
| 204 | `dal.CreateEntryPoint` call | `AppService.CreateEntryPoint` |
| 228-232 | EP type re-validation (only when non-empty) → `ErrUnprocessable` | `AppService.UpdateEntryPoint` |
| 233-236 | `enabled` derivation | `AppService.UpdateEntryPoint` |
| 239 | Pre-update slug fetch (`GetEntryPointSlug`) for rename invalidation | `AppService.UpdateEntryPoint` |
| 241 | `dal.UpdateEntryPoint` call | `AppService.UpdateEntryPoint` |
| 246-247 | Publish old + new slug (order preserved: old first, new second) | `AppService.UpdateEntryPoint` |
| 260 | Pre-delete slug fetch | `AppService.DeleteEntryPoint` |
| 262 | `dal.DeleteEntryPoint` call | `AppService.DeleteEntryPoint` |
| 267 | Publish deleted slug | `AppService.DeleteEntryPoint` |

**Ordering contract that MUST be preserved** (test `TestUpdateEntryPoint_SlugRename_OldSlugPublishedFirst`):
old slug published first, new slug published second, exactly two messages on a rename. When the
old-slug lookup fails, only the new slug is published (test `..._OldSlugLookupFails_OnlyNewSlugPublished`).
`CreateEntryPoint` publishes nothing (test `TestCreateEntryPoint_DoesNotPublish`).

### 5.4 `runs.go` → `service/runs.go`

| Source lines | What | Destination |
|---|---|---|
| 43 | `dal.ListRuns` call | `RunService.List` |
| 59 | `dal.GetRun` call | `RunService.Get` |
| 82-85 | `temporal == nil` check | `RunService.Signal` → returns typed `ErrTemporalUnavailable` |
| 89 | `GetRunContextID` call | `RunService.Signal` |
| 90-97 | not-found vs db-error branch on context lookup | `RunService.Signal` → `ErrNotFound` vs wrapped generic error |
| 98 | `workflowID := "ctx-" + contextID` (Temporal ID construction) | `RunService.Signal` (single owner; see §8) |
| 106 | `temporal.SignalRun` call | `RunService.Signal` |

**Left in the runs handler:** query-param parsing + limit default (lines 33-41), `json.Marshal` of
the payload (line 100-104) stays as HTTP decoding, and the `run_id` empty check. See §6.

---

## 6. Logic Intentionally Left In Handlers

| Handler concern | Lines | Why it stays |
|---|---|---|
| JSON body decode (`json.NewDecoder(r.Body).Decode`) | all Create/Update | HTTP parsing — handler's job |
| URL param extraction (`chi.URLParam`) | all | HTTP parsing — handler's job |
| Empty-id / empty-name URL-param guards (e.g. agents.go:88-91, runs.go:54-57) | all | These validate the **HTTP request shape** (missing path segment → 400), not domain rules. Cheap, HTTP-local, keep as 400 before calling the service. |
| `Location` header construction | agents:81, orchestrators:75, applications:111,210 | HTTP response formatting — handler's job. Uses the id/name returned by the service. |
| `writeJSON` / `writeError` response writing | all | HTTP response writing — handler's job |
| Status-code mapping (service error → HTTP code) | all | Handler's job per §7. New: handler switches on typed errors. |
| Runs query-param parse + `limit` default (50) | runs.go:33-41 | `limit`/`context_id` are **query-string parsing**, not domain rules. The default-50 is a request-parsing default, not a persisted-entity default. Keep in handler; pass parsed `limit` into `RunService.List`. |
| Signal payload `json.Marshal` (runs.go:100-104) | runs.go | Turning the decoded `SignalInput.Payload` into `[]byte` is serialization at the HTTP boundary. Handler marshals, passes `json.RawMessage`/`[]byte` to the service. (Service already receives `json.RawMessage`; it does not re-parse.) |
| PATCH route aliases | agents:31, orchestrators:31, applications:50,55 | Routing — handler's job. Load-bearing migration compat. Unchanged. |
| `RequireSuperAdmin` middleware | middleware.go:61 | Auth middleware integration — handler package's job. Unchanged. |

---

## 7. Error Type Design

Defined in `service/errors.go`. The service returns typed errors; handlers map them to codes. This
replaces today's implicit mapping (handlers return 400/404/422/500 inline).

```go
package service

import (
	"errors"
	"fmt"
)

// Sentinel errors. Handlers use errors.Is to map to HTTP status codes.
var (
	// ErrValidation → 400 Bad Request. A required field is missing/empty.
	ErrValidation = errors.New("validation error")

	// ErrUnprocessable → 422 Unprocessable Entity. The value is syntactically
	// present but semantically invalid (e.g. entry_point_type not in the allow-list).
	ErrUnprocessable = errors.New("unprocessable entity")

	// ErrNotFound → 404 Not Found. The addressed resource does not exist.
	ErrNotFound = errors.New("not found")

	// ErrTemporalUnavailable → 503 Service Unavailable. Temporal not configured.
	ErrTemporalUnavailable = errors.New("temporal not configured")
)

// FieldError wraps ErrValidation/ErrUnprocessable with a specific field and
// message so handlers can surface the same error strings the current API returns.
type FieldError struct {
	Kind    error  // ErrValidation or ErrUnprocessable
	Field   string // e.g. "slug", "entry_point_type"
	Message string // human-readable, matches current handler wording
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error { return e.Kind }

// Constructors keep call sites terse and messages centralized.
func validation(field, msg string) error {
	return &FieldError{Kind: ErrValidation, Field: field, Message: msg}
}
func unprocessable(field, msg string) error {
	return &FieldError{Kind: ErrUnprocessable, Field: field, Message: msg}
}
```

For not-found, the service wraps the DAL's `pgx.ErrNoRows`. The service must not import pgx, so it
detects "no rows" via the existing `admin.IsNotFound` helper — but that helper is in the `admin`
(handler) package and importing it would create a cycle. **Resolution:** move the not-found
detection into the DAL boundary. The DAL already aliases `ErrNoRows` in `admin/pgx.go:62`. Add a
tiny helper in the `dal` package:

```go
// dal/dal.go
import "github.com/jackc/pgx/v5"
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
```

`dal` already indirectly depends on pgx-produced errors at runtime but does not import pgx today.
Adding this one import to `dal` is acceptable (dal is the data layer; knowing "no rows" is a data
concept). The service then calls `dal.IsNoRows(err)` and returns `service.ErrNotFound`.

Alternative that avoids adding pgx to dal: keep the current behavior where `Get`/`GetRun` map **any**
DAL error to 404 (that is what handlers do today — agents.go:94-97 returns 404 on any error). To
preserve the **exact current contract with zero behavior change**, the service `Get`/`GetOrchestrator`/
`GetApplication`/`GetRun` methods return `ErrNotFound` on **any** DAL error, matching today's blanket
404. Only `RunService.Signal` distinguishes not-found from db-error, because runs.go:90-97 already
does via `IsNotFound`. For Signal, use `dal.IsNoRows` (the one added helper). This keeps `dal` as
the sole owner of pgx knowledge.

### Handler → HTTP mapping

Each handler gets a small local mapper. Example for a mutation handler:

```go
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrUnprocessable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrTemporalUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
```

This is placed in `middleware.go` (handler package) next to `writeError`. It is the single point
where service errors become HTTP codes.

**Message-text preservation:** the `FieldError.Message` strings must be byte-identical to the current
handler strings so no API consumer sees a changed error body:
- agents: `"slug and display_name are required"`
- orchestrators: `"name is required"`
- applications: `"name is required"`, `"slug and entry_point_type are required"`,
  `"invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a"`
- runs: `"temporal not configured"`, `"run not found or no root task"`

The generic 500 wording differs slightly per handler today (`"create agent: "`, `"db error: "` etc.).
To preserve exact bodies, the handler prepends its current prefix when the error is the default
(non-typed) branch: `writeError(w, 500, "create agent: "+err.Error())`. So the handler keeps its
prefix string for the default case and only delegates typed cases to `writeServiceError`. Simplest
faithful approach: handler does its own switch inline (not a shared helper) so each keeps its exact
500-prefix. This is spelled out per-handler in the implementation, prioritizing zero body drift over
DRY.

---

## 8. CacheInvalidator / TemporalSignaler Placement — Decision

**Decision: move both interface *definitions* into `internal/admin/service` (renamed `Cache` and
`Temporal`), and delete them from `middleware.go`. Keep backward-compat type aliases in the `admin`
package so `admin_test.go` and the router compile unchanged during migration.**

Rationale:

1. These interfaces describe **what the service needs**, not what the HTTP layer needs. Consumer-defined
   interfaces belong with the consumer. After the refactor the handlers no longer call `cache.Del`
   or `temporal.SignalRun` directly — the service does. So the service is the true consumer.
2. Keeping them in `middleware.go` (handler package) would force the service package to import the
   handler package to name its own dependencies → import cycle (`admin → service → admin`). Forbidden
   by requirement 4.
3. The concrete implementations (Redis cache wrapper, `temporal.Signaler`) live in `cmd/them/main.go`
   wiring and are injected. They already satisfy the method sets; moving the interface does not touch
   them.

Compatibility shim (temporary, load-bearing until Wave 5 completes and `admin_test.go` is updated):

```go
// middleware.go  (handler package) — keep as aliases, remove the interface bodies
type CacheInvalidator = service.Cache
type TemporalSignaler  = service.Temporal
```

The existing `admin_test.go` fakes (`fakeCache`, `fakeTemporal`) satisfy the aliased interfaces
without change. `BuildRouter`'s parameters keep the alias types, so `cmd/them/main.go` wiring is
untouched.

**Temporal workflow-ID construction (`"ctx-" + contextID`)** moves into `RunService.Signal`
(runs.go:98). This makes `RunService` the single owner of the admin-side convention. The other two
sites (`ws/handler.go:464`, `sse/handler.go:476`) are **out of scope** — they are transport handlers,
not admin, and touching them is Wave-5-adjacent WS/SSE work explicitly excluded. Document in
`lessons-learned.md` that the convention now has one admin owner (`RunService`) and two transport
sites still duplicated, to be unified only when a shared workflow-id helper is extracted.

---

## 9. Test Strategy

### 9.1 New: `service/service_test.go` (package `service_test`, unit, no HTTP)

Uses a local `fakeDal` implementing `service.Dal`, a `fakeCache` implementing `service.Cache`, and a
`fakeTemporal` implementing `service.Temporal`. These mirror the existing `admin_test.go` fakes but
operate at the method level (record calls, return canned values) rather than at the SQL level — no
`fakeRows`/`scanInto` machinery needed, because the service talks to the DAL by method, not by SQL.

New unit tests (one behavior each):

| Test | Asserts |
|---|---|
| `TestAgentService_Create_Defaults` | Missing transport→`a2a_async`, MaxConcurrency→5, MaxRetries→2, TimeoutSeconds→30 passed to `CreateAgent`; enabled defaults true |
| `TestAgentService_Create_MissingSlug_Validation` | Empty slug → `errors.Is(err, ErrValidation)`, no DAL call |
| `TestAgentService_Create_MissingDisplayName_Validation` | Empty display_name → `ErrValidation` |
| `TestAgentService_Create_EnabledFalse_Respected` | `enabled:false` passed through as false |
| `TestAgentService_Update_ReappliesMaxConcurrencyDefault` | MaxConcurrency<=0 → 5 on update |
| `TestAgentService_Create_InvalidatesRegistry` | `Del("them:agents:registry")` called once |
| `TestAgentService_NilCache_NoPanic` | cache nil → no panic |
| `TestOrchService_Create_Defaults` | MaxIterations→10, HistoryWindow→20 |
| `TestOrchService_Create_MissingName_Validation` | Empty name → `ErrValidation` |
| `TestOrchService_Create_InvalidatesName` | `Del("them:orchestrators:{name}")` |
| `TestAppService_Get_ComposesEntryPoints` | Result carries `ListEntryPoints` output |
| `TestAppService_CreateEP_InvalidType_Unprocessable` | `entry_point_type:"grpc"` → `errors.Is(err, ErrUnprocessable)`, no DAL create |
| `TestAppService_CreateEP_MissingFields_Validation` | empty slug/type → `ErrValidation` |
| `TestAppService_CreateEP_ValidTypes` | all of websocket/sse/voice/webrtc/a2a accepted |
| `TestAppService_CreateEP_DoesNotPublish` | no `Publish` on create |
| `TestAppService_UpdateEP_EmptyType_Allowed` | empty type on update → no error |
| `TestAppService_UpdateEP_InvalidType_Unprocessable` | invalid type on update → `ErrUnprocessable`, no DAL update, no publish |
| `TestAppService_UpdateEP_Rename_PublishesOldThenNew` | exactly two publishes, old first, new second |
| `TestAppService_UpdateEP_OldSlugLookupFails_OnlyNewPublished` | GetEntryPointSlug err → only new slug published |
| `TestAppService_UpdateApp_PublishesAllEPSlugs` | publishes each slug from `ListEPSlugsForApp` |
| `TestAppService_DeleteEP_PublishesSlug` | fetches then publishes deleted slug |
| `TestRunService_Signal_BuildsWorkflowID` | `SignalRun` called with `"ctx-"+contextID` |
| `TestRunService_Signal_TemporalNil_Unavailable` | temporal nil → `ErrTemporalUnavailable` |
| `TestRunService_Signal_ContextNotFound_NotFound` | GetRunContextID no-rows → `ErrNotFound` |
| `TestRunService_List_PassesContextAndLimit` | forwards context_id + limit to DAL |

### 9.2 Existing: `admin_test.go` — keep, mostly unchanged

The existing HTTP-level tests remain valid **end-to-end through HTTP** because routes, payloads, and
status codes are unchanged. Constructors change name (see §10), so the handler constructors that
`admin_test.go` calls (`admin.NewAgentsHandler(db, nil)` etc.) must keep the same signature. Two
options:

- **Option A (chosen):** keep `admin.NewAgentsHandler(db DBQuerier, cache CacheInvalidator)` signature
  identical. Internally it now builds `dal.NewDB(db)` and wraps it in `service.NewAgentService(...)`.
  The test file compiles with **zero changes**. Constructors still accept the raw `DBQuerier` and
  build the dal + service inside. This is the lowest-risk path and satisfies requirement 7 (existing
  tests keep testing handlers end-to-end).

Only if a later cleanup switches constructors to accept a `*service.AgentService` directly would
`admin_test.go` need edits — that is explicitly **not** done in this refactor.

`TEST_INDEX.md` must gain rows for every new `service_test.go` test and update the count, in the same
commit as `service/service_test.go` (Go CLAUDE.md rule 2).

### 9.3 Verification commands

```bash
go build ./...
go test ./internal/admin/...        # both admin and admin/service packages
go test -race ./internal/admin/...
go test ./...                        # full suite, zero new failures
```

---

## 10. Constructor Adaptation (keeps API + tests stable)

Handlers keep their current public constructor signatures; internally they now delegate to a service.

```go
// agents.go — signature UNCHANGED, body rewired
type AgentsHandler struct {
	svc *service.AgentService
}
func NewAgentsHandler(db DBQuerier, cache CacheInvalidator) *AgentsHandler {
	return &AgentsHandler{svc: service.NewAgentService(dal.NewDB(db), cache)}
}
```

`db DBQuerier` (== `dal.Querier`) is wrapped by `dal.NewDB` → `*dal.DB` → satisfies `service.Dal`.
`cache CacheInvalidator` (== `service.Cache` after §8) is passed straight through.

`RunsHandler`:

```go
type RunsHandler struct {
	svc *service.RunService
}
func NewRunsHandler(db DBQuerier, temporal TemporalSignaler) *RunsHandler {
	return &RunsHandler{svc: service.NewRunService(dal.NewDB(db), temporal)}
}
```

The handler struct no longer holds `db`, `cache`, `temporal`, or `*dal.DB` directly — only `svc`.
`BuildRouter` (router.go:29-41) is unchanged: it still receives `db, cache, temporal` and passes them
to the same constructors.

---

## 11. Migration Order (lowest risk first) + Commit Boundaries

Migrate one resource per commit. After each commit: `go build ./... && go test ./internal/admin/...`
must pass before the next. Order chosen by blast radius: **runs (smallest, no cache) → agents →
orchestrators → applications (largest, cache-ordering-sensitive)**.

### Commit 1 — scaffolding, no behavior change

Files:
- `internal/admin/service/service.go` (Dal/Cache/Temporal interfaces, constructors' shared helper)
- `internal/admin/service/errors.go` (typed errors)
- `internal/admin/dal/dal.go` (+`IsNoRows` helper, +pgx import)
- `internal/admin/middleware.go` (replace interface bodies with aliases to `service.Cache`/`service.Temporal`)

Verify: `go build ./...`, `go test ./internal/admin/...` (green — nothing calls the service yet).

### Commit 2 — runs (lowest risk: no cache, one Temporal call, isolated tests)

Files:
- `internal/admin/service/runs.go` (RunService)
- `internal/admin/runs.go` (rewire to `svc`, add error mapping)
- `internal/admin/service/service_test.go` (RunService tests)
- `TEST_INDEX.md`

Verify: `go test ./internal/admin/...` incl. `TestSignalRun`, `TestListRunsContextIDFilter`.

### Commit 3 — agents

Files:
- `internal/admin/service/agents.go`
- `internal/admin/agents.go` (rewire)
- `internal/admin/service/service_test.go` (+agent tests)
- `TEST_INDEX.md`

Verify: incl. `TestCreateAgent`, `TestGetNonexistentAgent`, `TestListAgentsEmptyArray`, `TestPatchAgentAliasesUpdate`.

### Commit 4 — orchestrators

Files:
- `internal/admin/service/orchestrators.go`
- `internal/admin/orchestrators.go` (rewire)
- `internal/admin/service/service_test.go` (+orch tests)
- `TEST_INDEX.md`

Verify: incl. `TestPatchOrchestratorAliasesUpdate`.

### Commit 5 — applications (highest risk: cache-ordering + EP validation + read-then-write)

Files:
- `internal/admin/service/applications.go` (AppService, `validEPTypes`, `epConfigChannel`)
- `internal/admin/applications.go` (rewire; delete `validEPTypes`/`isValidEPType`/`invalidateEP`/`invalidateAppEPs`/`epConfigChannel` from handler)
- `internal/admin/service/service_test.go` (+app/EP tests)
- `TEST_INDEX.md`

Verify: ALL AI-1..AI-6, EPT-1..EPT-4, PATCH-app/EP tests green. Then full suite + `-race`.

### Commit 6 — tokens seam (documentation-level, compiles, no route)

Files:
- `internal/admin/service/tokens.go` (`TokenGenerator` interface + `TokenService` skeleton, no routed methods)
- `internal/admin/service/service_test.go` (one compile/constructor smoke test asserting `NewTokenService` builds)
- `TEST_INDEX.md`

Verify: `go build ./...`, `go vet ./internal/admin/...` (must not report the seam as unused — the
constructor + a smoke test reference it). Full suite green.

Six commits total; each independently green and revertable.

---

## 12. Rollback Approach

- **Per-commit revert:** each commit is self-contained and leaves the suite green, so `git revert <sha>`
  of any single commit restores the previous working state without touching the others. Because
  handler public constructor signatures never change (§10), reverting a service commit only reverts
  the internal delegation for that one resource.
- **Full rollback:** `git revert` commits 6→1 in reverse order, or `git reset --hard <sha before commit 1>`
  on a throwaway branch. The branch this lands on must not be `main` directly if not yet reviewed
  (Go CLAUDE.md / repo workflow: branch first when on default branch).
- **Safety net:** no schema migration, no data mutation of a new shape, no Redis key change — a revert
  has zero persistent side effects. The only runtime-visible surface is HTTP behavior, which is held
  identical by the unchanged `admin_test.go` end-to-end tests. If those pass, behavior is preserved.
- **Detection before merge:** run `go test -race ./...` and the Python parity contract test (test 37,
  referenced in the handover) to confirm Go↔Python admin behavior still matches.

---

## 13. Risks

| Risk | Likelihood | Detection | Mitigation |
|---|---|---|---|
| Import cycle `admin → service → admin` (e.g. service accidentally references `admin.IsNotFound`) | Medium | `go build ./...` fails immediately | Not-found detection lives in `dal.IsNoRows`; service never imports admin. Enforced in commit 1. |
| EP publish ordering drift (old-before-new) breaks `TestUpdateEntryPoint_SlugRename_OldSlugPublishedFirst` | Medium | Existing HTTP test + new `TestAppService_UpdateEP_Rename_PublishesOldThenNew` | Preserve exact statement order in `AppService.UpdateEntryPoint`; test asserts `publishedMsgs[0]`/`[1]`. |
| Error-body text drift (client sees different message/500 prefix) | Medium | `admin_test.go` HTTP tests + manual diff of message strings (§7) | Keep `FieldError.Message` byte-identical; keep per-handler 500 prefixes inline. |
| Blanket-404 vs typed-404 divergence on `Get` (today any DAL error → 404) | Low | `TestGetNonexistentAgent` | Service `Get*` maps any DAL error to `ErrNotFound` to match current contract exactly; only `Signal` distinguishes. |
| `enabled` default flips (nullable `*bool` → true) mis-handled | Low | New `..._EnabledFalse_Respected` test | Single shared `enabledOrDefault(*bool) bool` helper used everywhere; unit-tested. |
| Constructor signature change breaks `admin_test.go` / `BuildRouter` / main.go wiring | Low | `go test ./internal/admin/...`, `go build ./cmd/them/` | §10: public constructor signatures held identical; only internals change. |
| Adding pgx import to `dal` introduces a build/dep surprise | Low | `go build ./...`, `go mod tidy` no-op | pgx is already a dep (used by `pgx.go`); `dal` importing it for `errors.Is(err, pgx.ErrNoRows)` adds no new module. |
| `TokenService` seam flagged as dead/unused code by linters | Low | `go vet`, CI lint | Reference it from `NewTokenService` + a smoke test; document `// Wave 5` intent. Do not add a route. |
| Scope creep into WS/SSE `"ctx-"` sites or token CRUD | Medium (human) | Diff review against §14 constraints | Hard stop: those are explicitly excluded; only the admin `runs` site moves. |
| Python parity: admin behavior must still match Python during migration | Low | Parity contract test 37 (handover) + `-race` full suite | No behavior change intended; run parity test before merge. |

---

## 14. Constraint Review (hard constraints)

| Constraint | Status | Evidence |
|---|---|---|
| No DB schema changes | PASS | No file under `db/` touched; DAL SQL strings unchanged. Only a Go `IsNoRows` helper added. |
| No API contract changes | PASS | Same payloads/status codes/error strings; §7 preserves message text; `admin_test.go` unchanged and still green. |
| No route changes | PASS | `router.go` and all `Routes()` methods unchanged; PATCH aliases retained. |
| No generic framework | PASS | Four concrete services + plain typed errors; no reflection, no registry, no generics-based CRUD engine. |
| No major orchestration redesign | PASS | Temporal call path unchanged; only the `"ctx-"` string construction relocates into `RunService`. |
| No destructive operations | PASS | Soft-delete semantics unchanged (still `enabled=false` in DAL SQL); no data deletion, no key deletion beyond existing cache invalidation. |
| No Wave 5 work | PASS | No tokens/sessions/dashboard routes; Wave 5 route ownership table untouched. |
| No token CRUD implementation | PASS | `tokens.go` is a seam: `TokenGenerator` interface + empty `TokenService`, no routed methods, no DAL token calls. |
| Package `internal/admin/service/`, one file per resource | PASS | agents.go / orchestrators.go / applications.go / runs.go (+ service.go, errors.go, tokens.go seam). |
| `Dal` interface in service package | PASS | §2.1; `*dal.DB` satisfies it structurally. |
| Typed errors for 422/400/404 | PASS | §7: `ErrValidation`(400), `ErrUnprocessable`(422), `ErrNotFound`(404), `ErrTemporalUnavailable`(503). |
| No circular imports (handler→service→dal→nothing) | PASS | §4 import rules; not-found detection kept in dal to avoid `service→admin`. |
| DI via constructor, no globals | PASS | §3: every service takes deps as constructor args. |
| Cache/Temporal placement decided | PASS | §8: moved to service, aliased in admin for compat. |
| Service tested directly (unit, no HTTP) | PASS | §9.1: `service_test.go` calls service methods with fakes, no `httptest`. |

No blockers. Proceed.
