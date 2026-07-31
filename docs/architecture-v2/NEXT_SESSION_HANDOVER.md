# Phase R-4b Complete — Handover to R-4c

**Date:** 2026-07-31
**Branch:** main
**HEAD:** `a95e859` feat(r4b): authenticated tenant identity foundation
**Prepared by:** Phase R-4b session

---

## Current Objective

Phase R-4b (Authenticated Tenant Identity Foundation) is **complete**. The next task is
**Phase R-4c: DAL Query Tenant Filtering** — adding `WHERE tenant_id = $n` to all DAL
queries on tenant-scoped tables, and wiring the tenant-aware middleware to admin and
runtime routes.

---

## What Was Completed This Session (R-4b)

1. **`go/internal/tenantctx/`** — new typed context package with `WithTenantID`, `TenantIDFromCtx`,
   `MustTenantIDFromCtx`, `ErrNoTenant`, `ErrInvalidTenant`. No stringly-typed key. No fallback to
   bootstrap tenant.

2. **`go/internal/auth/jwt.go`** — added `TenantID` to `Claims` and `hs256RawClaims`. Both
   `ValidateJWT` (RS256) and `ValidateHS256JWT` (HS256) now propagate `TenantID`.

3. **`go/internal/auth/token_cache.go`** — added `TenantID` to `TokenInfo` and `TokenRow`.
   `rowToTokenInfo` propagates it.

4. **`go/internal/auth/pgx_querier.go`** — query now fetches `tenant_id` from `access_tokens`.

5. **`go/internal/auth/middleware.go`** — added `BearerTenantMiddleware` and `HS256TenantMiddleware`
   (both return 401 for missing/invalid auth, 403 for absent TenantID). Added `writeForbidden`.

6. **`go/internal/transport/transport.go`** — added `RuntimeIdentity` struct (TenantID, AppID,
   UserID, SessionID, RunID).

7. **`go/internal/tenantctx/tenantctx_test.go`** — 8 tests (TC-01 through TC-08).

8. **`go/internal/auth/tenant_middleware_test.go`** — 15 tests (TM-01 through TM-15).

9. **Documentation** — R4B_IMPLEMENTATION_REPORT.md, updated implementation-status.md,
   lessons-learned.md, TEST_INDEX.md (S1-31, S1-32, trigger map).

Full details: `docs/architecture-v2/R4B_IMPLEMENTATION_REPORT.md`

---

## Stack State

| Container | Status |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-worker (Python) | Running |
| them-postgres | Healthy — R-4a migration applied |
| them-redis | Healthy |

**No DB migrations in R-4b** (DB-only changes were in R-4a).

---

## Test State

```
go test ./...        →   447 passed, 0 failed (28 packages)
go test -race ./internal/auth/... ./internal/tenantctx/...   → PASS (no data races)
Python sanity 01-04,15  →   55 passed, 0 failed
```

New in R-4b: 23 tests across 2 new test files.

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

---

## Known Issues and Blockers

| Issue | Severity | Notes |
|---|---|---|
| Auth service does not include `tenant_id` in JWT tokens | Medium | `HS256TenantMiddleware` returns 403 for all current JWTs — middleware NOT yet wired to any route. Unblocks when auth service is updated. |
| No DAL tenant filtering yet | Expected | R-4b is identity foundation only; R-4c adds WHERE clauses |
| Cross-tenant access returns 200/data (not 403) | Expected | Not enforced until R-4c complete |

---

## R-4c Scope — DAL Query Tenant Filtering

**Scope:**
1. Add `tenantID string` parameter to all DAL functions on tenant-scoped tables
   (`agents`, `orchestrators`, `access_tokens`, `applications`, `runs`, `audit_logs`,
   `app_orchestrators`)
2. Add `WHERE tenant_id = $n` clause to all affected queries
3. Wire `BearerTenantMiddleware` or `HS256TenantMiddleware` to admin routes
4. Wire tenant context extraction in WS/SSE handlers (from bearer token path)
5. Update all admin handler call sites to pass TenantID from context
6. Update tests for all changed DAL functions

**R-4c does NOT:**
- Change run recorder signature (R-4d)
- Add tenant provisioning UI or APIs
- Change session TTL or Redis key structure (Tier 2, deferred)

**Files most relevant to R-4c:**

| File | Why |
|---|---|
| `go/internal/admin/dal/agents.go` | Add tenantID param + WHERE clause |
| `go/internal/admin/dal/orchestrators.go` | Add tenantID param + WHERE clause |
| `go/internal/admin/dal/applications.go` | Add tenantID param + WHERE clause |
| `go/internal/admin/dal/runs.go` | Add tenantID param + WHERE clause |
| `go/internal/admin/dal/tokens.go` | Add tenantID param + WHERE clause |
| `go/internal/admin/` (handler files) | Extract TenantID from context, pass to DAL |
| `go/internal/auth/middleware.go` | `BearerTenantMiddleware`, `HS256TenantMiddleware` — wire to routes |
| `go/internal/tenantctx/tenantctx.go` | `TenantIDFromCtx` — call from handlers |
| `go/internal/admin/router.go` | Add tenant middleware to admin routes |

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

> Phase R-4b is complete (HEAD from git log, 447 Go tests passing). Start Phase R-4c: DAL
> Query Tenant Filtering. Read docs/architecture-v2/R4B_IMPLEMENTATION_REPORT.md and
> TENANT_FOUNDATION_DECISIONS.md §5 before writing any code. Add WHERE tenant_id = $n to
> all DAL functions on tenant-scoped tables; wire BearerTenantMiddleware to admin routes;
> extract TenantID from context in handlers. Use Sonnet.

---

## Commits This Session

- `a95e859` feat(r4b): authenticated tenant identity foundation

Push status: **pushed to origin/main** ✓

---

## R-4c through R-4e NOT started

- R-4c (DAL WHERE tenant_id): NOT started
- R-4d (session propagation): NOT started
- R-4e (run recorder): NOT started
