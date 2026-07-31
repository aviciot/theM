# Phase R-4c1 Complete — Handover to R-4c2

**Date:** 2026-07-31
**Branch:** main
**HEAD:** `09c5665` feat(r4c1): tenant-scoped DAL and service layers
**Prepared by:** Phase R-4c1 session

---

## Current Objective

Phase R-4c1 (Tenant-Scoped DAL and Service Layers) is **complete**. The next task is
**Phase R-4c2: Wire Tenant Middleware to Admin Routes** — connecting `BearerTenantMiddleware`
or `HS256TenantMiddleware` to admin routes in `cmd/them/main.go` and removing the
`tenantIDFromCtxOrBootstrap` compatibility shim.

---

## What Was Completed This Session (R-4c1)

1. **`go/internal/admin/dal/agents.go`** — All 5 methods tenant-scoped.
2. **`go/internal/admin/dal/orchestrators.go`** — All 5 methods tenant-scoped.
3. **`go/internal/admin/dal/applications.go`** — App CRUD (5 methods) tenant-scoped; entry point methods unchanged.
4. **`go/internal/admin/dal/runs.go`** — `ListRuns`, `GetRun`, `GetRunContextID` tenant-scoped.
5. **`go/internal/admin/dal/tokens.go`** — All 7 methods tenant-scoped (incl. `OrchestratorExists`).
6. **`go/internal/admin/service/service.go`** — `Dal` interface updated; all tenant-owned entity methods now take `tenantID string`.
7. **`go/internal/admin/service/agents.go`** — Service methods forwarding tenantID to DAL.
8. **`go/internal/admin/service/orchestrators.go`** — Same pattern.
9. **`go/internal/admin/service/applications.go`** — App CRUD methods take tenantID; entry point methods unchanged.
10. **`go/internal/admin/service/runs.go`** — `List`, `Get`, `Signal` take tenantID.
11. **`go/internal/admin/service/tokens.go`** — All CRUD methods take tenantID.
12. **`go/internal/admin/middleware.go`** — Compatibility shim `tenantIDFromCtxOrBootstrap`; `bootstrapTenantID` constant.
13. **`go/internal/admin/agents.go`** — Handler calls shim, passes tenantID to service.
14. **`go/internal/admin/orchestrators.go`** — Same pattern.
15. **`go/internal/admin/applications.go`** — Same pattern.
16. **`go/internal/admin/runs.go`** — Same pattern.
17. **`go/internal/admin/tokens.go`** — Same pattern.
18. **`go/internal/admin/service/service_test.go`** — `fakeDal` updated to match new interface.
19. **`go/internal/admin/service/tenant_isolation_test.go`** — New: 21 two-tenant isolation tests (S1-33).
20. **`go/TEST_INDEX.md`** — Added S1-33, updated trigger map, count 447 → 468.
21. **`docs/architecture-v2/R4C1_IMPLEMENTATION_REPORT.md`** — Created.

Full details: `docs/architecture-v2/R4C1_IMPLEMENTATION_REPORT.md`

---

## Stack State

| Container | Status |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-worker (Python) | Running |
| them-postgres | Healthy — R-4a migration applied |
| them-redis | Healthy |

**No DB migrations in R-4c1** (DB schema already has `tenant_id` columns from R-4a).

---

## Test State

```
go test ./...        →   468 passed, 0 failed (28 packages)
go test -race ./...  →   468 passed, 0 failed, 0 data races (28 packages)
```

New in R-4c1: 21 tests in `tenant_isolation_test.go` (S1-33).

---

## Bootstrap Tenant

| Field | Value |
|---|---|
| ID | `00000000-0000-0000-0000-000000000001` |
| Slug | `default` |
| Display name | Default Development Tenant |

---

## Hard Constraints — Carry Forward

- **Bootstrap tenant UUID `00000000-0000-0000-0000-000000000001` is immutable.**
- **Temporal is the single durable owner of every run.**
- **Never log token values, API keys, or secrets.**
- **All Go changes require `go test ./...` before commit.** Zero regressions allowed.
- **Workflow ID scheme `ctx-{contextID}`** must be preserved.
- **`llm_providers`, `config`, `middleware_defs` are platform-global** — no tenant_id ever.
- **DB name and schema: `them` only.** Never `odin`.
- **TenantID must never come from request headers or query parameters.**
- **The bootstrap tenant must NOT be silently assigned when authentication is absent/invalid.**
- **R-4c1 compatibility shim `tenantIDFromCtxOrBootstrap` must be removed in R-4c2**, not carried forward.
- **Entry point DAL methods have no `tenantID` param** — they are scoped through the parent app's FK. Do not add one.

---

## Known Issues and Blockers

| Issue | Severity | Notes |
|---|---|---|
| `tenantIDFromCtxOrBootstrap` shim in place | Expected | All admin routes fall back to bootstrap tenant; fixes in R-4c2 |
| Auth service does not include `tenant_id` in JWT tokens | Medium | `HS256TenantMiddleware` returns 403 for all current JWTs until auth service is updated |
| Cross-tenant access on admin routes not enforced at HTTP level | Expected | DAL rejects cross-tenant queries (returns not-found), but HTTP callers all share bootstrap tenant until R-4c2 |

---

## R-4c2 Scope — Wire Tenant Middleware to Admin Routes

**Goal:** Remove the compatibility shim and enforce tenant identity on every admin request.

**Exact tasks:**

1. Wire `BearerTenantMiddleware` (not `HS256TenantMiddleware`) to admin router in `cmd/them/main.go`
   - All routes under the admin chi group must go through `BearerTenantMiddleware`
   - `BearerTenantMiddleware` is already implemented in `go/internal/auth/middleware.go`
   - It requires a valid bearer token with a non-empty `TenantID` in the token info

2. Remove from `go/internal/admin/middleware.go`:
   - `bootstrapTenantID` constant
   - `tenantIDFromCtxOrBootstrap` function

3. Update all 5 handler files (`agents.go`, `orchestrators.go`, `applications.go`, `runs.go`, `tokens.go`):
   - Replace `tenantIDFromCtxOrBootstrap(r.Context())` with `tenantctx.MustTenantIDFromCtx(r.Context())`
   - (This is safe because the middleware guarantees tenant is in context before handlers run)

4. Add handler-level tests:
   - Request with no bearer token → 401 (middleware rejects before handler)
   - Request with bearer token missing tenant → 403 (middleware rejects)
   - Request with valid token + tenant → handler runs, tenantID from context used

5. Update TEST_INDEX.md.

**Files most relevant to R-4c2:**

| File | Why |
|---|---|
| `go/cmd/them/main.go` | Wire `BearerTenantMiddleware` to admin chi group |
| `go/internal/admin/middleware.go` | Remove shim |
| `go/internal/admin/agents.go` | Replace shim call |
| `go/internal/admin/orchestrators.go` | Replace shim call |
| `go/internal/admin/applications.go` | Replace shim call |
| `go/internal/admin/runs.go` | Replace shim call |
| `go/internal/admin/tokens.go` | Replace shim call |
| `go/internal/auth/middleware.go` | `BearerTenantMiddleware` — already implemented |
| `go/internal/admin/admin_test.go` | Add middleware enforcement tests |

**Important:** The `RequireSuperAdmin` middleware remains in place. Admin routes need BOTH
`RequireSuperAdmin` (validates JWT role) AND `BearerTenantMiddleware` (extracts tenant from
bearer token). Check the current route registration order in `cmd/them/main.go` before wiring.

---

## Starting the Next Session

```bash
# Confirm HEAD
git log --oneline -5

# Run Go tests to confirm clean baseline
docker run --rm -v /home/avi/them/go:/workspace -w /workspace golang:1.24-alpine go test ./...

# Python sanity
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

**First prompt for the next session:**

> Phase R-4c1 is complete (468 Go tests passing). Start Phase R-4c2: Wire tenant middleware
> to admin routes. Read docs/architecture-v2/R4C1_IMPLEMENTATION_REPORT.md and
> NEXT_SESSION_HANDOVER.md before writing code. Goal: remove tenantIDFromCtxOrBootstrap shim
> and wire BearerTenantMiddleware to all admin routes in cmd/them/main.go. Add
> handler-level tests verifying 401/403 enforcement. Use Sonnet.

---

## R-4c through R-4e Status

- R-4c1 (DAL + service tenant scoping): **COMPLETE** ✓
- R-4c2 (wire middleware to admin routes): NOT started
- R-4d (session propagation): NOT started
- R-4e (run recorder): NOT started
