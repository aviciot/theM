# R-4c1 Implementation Report
# Tenant-Scoped DAL and Service Layers
# Completed: 2026-07-31

---

## Summary

R-4c1 adds `tenant_id` scoping to every SQL operation on tenant-owned entities in the Go gateway
(`go/`). Every SELECT, INSERT, UPDATE, and DELETE on agents, orchestrators, applications,
access_tokens, and runs now includes `tenant_id` in the WHERE clause or INSERT column list.
Platform-global entities (`llm_providers`, `config`) are untouched.

A single, clearly-marked compatibility shim provides the bootstrap development tenant UUID to all
admin handlers until R-4c2 wires `BearerTenantMiddleware` or `HS256TenantMiddleware` to admin
routes. No tenant identity is accepted from headers, query parameters, or request bodies.

---

## Files Changed

### DAL layer — `go/internal/admin/dal/`

| File | Change |
|---|---|
| `agents.go` | All 5 methods: `tenantID string` added; WHERE/INSERT include `tenant_id = $N::uuid` |
| `orchestrators.go` | All 5 methods: same pattern |
| `applications.go` | All 5 app methods: same pattern; entry point methods unchanged (scoped through app FK) |
| `runs.go` | `ListRuns`, `GetRun`, `GetRunContextID`: tenant filter on both query branches |
| `tokens.go` | All 7 methods: `tenantID string` added; OrchestratorExists also scoped to tenant |

### Service interface — `go/internal/admin/service/service.go`

`Dal` interface updated: all tenant-owned entity methods now take `tenantID string` as second
argument after `ctx`. Platform-global methods (`GetConfig`, `UpsertConfig`, `ListProviders`, etc.)
are unchanged.

### Service implementations

| File | Change |
|---|---|
| `agents.go` | All methods accept `tenantID string`, forward to DAL |
| `orchestrators.go` | Same pattern |
| `applications.go` | App CRUD methods accept `tenantID string`; entry point methods unchanged |
| `runs.go` | `List`, `Get`, `Signal` accept `tenantID string` |
| `tokens.go` | All CRUD methods accept `tenantID string` |

### Compatibility shim — `go/internal/admin/middleware.go`

```go
const bootstrapTenantID = "00000000-0000-0000-0000-000000000001"

// R-4c1 COMPATIBILITY SHIM — remove once admin routes are wired to
// BearerTenantMiddleware or HS256TenantMiddleware in R-4c2.
func tenantIDFromCtxOrBootstrap(ctx context.Context) string {
    if id, err := tenantctx.TenantIDFromCtx(ctx); err == nil {
        return id
    }
    return bootstrapTenantID
}
```

This is the **only** place the bootstrap UUID is referenced. All five handler files call
`tenantIDFromCtxOrBootstrap(r.Context())` — never accept tenant from request data.

### Handler files — `go/internal/admin/`

All five handler files call `tenantIDFromCtxOrBootstrap(r.Context())` before every service
call that requires a tenant: `agents.go`, `orchestrators.go`, `applications.go`, `runs.go`,
`tokens.go`.

### Tests — `go/internal/admin/service/`

| File | Change |
|---|---|
| `service_test.go` | `fakeDal` method signatures updated (22 call sites: `_ string` for tenantID); all test function calls pass `"t1"` as tenantID |
| `tenant_isolation_test.go` | **New file** — 21 two-tenant isolation tests (see S1-33 in TEST_INDEX.md) |

---

## DAL Method Signatures (Tenant-Scoped)

```go
// Agents
ListAgents(ctx context.Context, tenantID string) ([]dal.Agent, error)
GetAgent(ctx context.Context, tenantID, id string) (dal.Agent, error)
CreateAgent(ctx context.Context, tenantID string, in dal.AgentInput, enabled bool) (string, error)
UpdateAgent(ctx context.Context, tenantID, id string, in dal.AgentInput, enabled bool) error
DeleteAgent(ctx context.Context, tenantID, id string) error

// Orchestrators
ListOrchestrators(ctx context.Context, tenantID string) ([]dal.Orchestrator, error)
GetOrchestrator(ctx context.Context, tenantID, name string) (dal.Orchestrator, error)
CreateOrchestrator(ctx context.Context, tenantID string, in dal.OrchestratorInput, enabled bool) (string, error)
UpdateOrchestrator(ctx context.Context, tenantID, name string, in dal.OrchestratorInput, enabled bool) error
DeleteOrchestrator(ctx context.Context, tenantID, name string) error

// Applications
ListApplications(ctx context.Context, tenantID string) ([]dal.Application, error)
GetApplication(ctx context.Context, tenantID, id string) (dal.Application, error)
CreateApplication(ctx context.Context, tenantID, name string, enabled bool) (string, error)
UpdateApplication(ctx context.Context, tenantID, id, name string, enabled bool) error
DeleteApplication(ctx context.Context, tenantID, id string) error
// Entry point methods: NO tenantID (scoped through parent app FK)

// Runs
ListRuns(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Run, error)
GetRun(ctx context.Context, tenantID, runID string) (dal.Run, error)
GetRunContextID(ctx context.Context, tenantID, runID string) (string, error)

// Tokens
ListTokens(ctx context.Context, tenantID string, userID *int64) ([]dal.Token, error)
GetToken(ctx context.Context, tenantID, id string) (dal.Token, error)
OrchestratorExists(ctx context.Context, tenantID, orchID string) (bool, error)
CreateToken(ctx context.Context, tenantID string, in dal.TokenCreateRow) (dal.Token, error)
UpdateToken(ctx context.Context, tenantID, id string, patch dal.TokenPatchRow) (hash string, out dal.Token, err error)
DeleteToken(ctx context.Context, tenantID, id string) (hash string, err error)
```

---

## Service Method Signatures (Tenant-Scoped)

Same pattern: every agent/orch/app/run/token service method takes `(ctx context.Context, tenantID string, ...)`.

---

## Compatibility Helper

**Name:** `tenantIDFromCtxOrBootstrap(ctx context.Context) string`
**Location:** `go/internal/admin/middleware.go`
**Used by:** all 5 handler files in `go/internal/admin/`
**Behaviour:** extracts tenant from `tenantctx`; falls back to `bootstrapTenantID` if not set
**Remove in:** R-4c2 (when admin routes are wired to `BearerTenantMiddleware`/`HS256TenantMiddleware`)

---

## Test Results

### `go test ./...`

All 28 packages passed, 0 failed.

### `go test -race ./...`

All 28 packages passed, 0 failed, 0 data races.

### Tenant isolation tests (S1-33 — 21 tests)

| Contract | Entity | Test | Result |
|---|---|---|---|
| TC-OWN | Agent | OwnRecordSucceeds | PASS |
| TC-OTHER | Agent | OtherTenantCannotRead | PASS |
| TC-OTHER | Agent | OtherTenantCannotUpdate | PASS |
| TC-OTHER | Agent | OtherTenantCannotDelete | PASS |
| TC-SLUG | Agent | SameSlugAcrossTenantsAllowed | PASS |
| TC-DUP | Agent | DuplicateSlugSameTenantReturnsError | PASS |
| TC-LIST | Agent | ListReturnsOwnTenantOnly | PASS |
| TC-OWN | Orchestrator | OwnRecordSucceeds | PASS |
| TC-OTHER | Orchestrator | OtherTenantCannotRead | PASS |
| TC-SLUG | Orchestrator | SameNameAcrossTenantsAllowed | PASS |
| TC-DUP | Orchestrator | DuplicateNameSameTenantReturnsError | PASS |
| TC-OWN | Application | OwnRecordSucceeds | PASS |
| TC-OTHER | Application | OtherTenantCannotRead | PASS |
| TC-SLUG | Application | SameNameAcrossTenantsAllowed | PASS |
| TC-OWN | Run | OwnRecordSucceeds | PASS |
| TC-OTHER | Run | OtherTenantCannotRead | PASS |
| TC-LIST | Run | ListReturnsOwnTenantOnly | PASS |
| TC-OWN | Token | OwnRecordSucceeds | PASS |
| TC-OTHER | Token | OtherTenantCannotRead | PASS |
| TC-OTHER | Token | OtherTenantCannotDelete | PASS |
| TC-LIST | Token | ListReturnsOwnTenantOnly | PASS |

---

## What Was NOT Changed

Per the R-4c1 scope constraints:
- Router or middleware wiring — unchanged
- Admin authentication — unchanged
- WS/SSE/WebRTC/A2A handlers — unchanged
- Temporal or Redis — unchanged
- Runtime session propagation — unchanged
- Artifacts — unchanged
- Platform-global entities (`llm_providers`, `config`) — unchanged
- Entry point DAL methods (scoped through app FK, no `tenant_id` needed)
- R-4d and R-4e — not started

---

## Remaining Work for R-4c2

R-4c2 must:
1. Wire `BearerTenantMiddleware` (or `HS256TenantMiddleware`) to admin routes in `cmd/them/main.go`
2. All admin routes will then have a verified `tenantID` in context
3. Remove the `tenantIDFromCtxOrBootstrap` shim from `go/internal/admin/middleware.go`
4. Remove the `bootstrapTenantID` constant from `go/internal/admin/middleware.go`
5. Add handler-level tests verifying that admin requests with no tenant are rejected (once middleware is wired)

The DAL and service layers are fully ready for R-4c2 — no DAL or service changes required.
