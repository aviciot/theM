# Phase R-4b Implementation Report — Authenticated Tenant Identity Foundation
# Date: 2026-07-31
# Status: COMPLETE

---

## Summary

Phase R-4b adds the authenticated tenant identity foundation to the Go gateway. TenantID now
flows from trusted authentication data (JWT claims and DB-stored bearer token records) through
a typed context package into request context and session metadata. No DAL query filtering was
changed (that is R-4c). No runtime WS/SSE/artifact ownership enforcement was added.

---

## Scope Executed

**Implemented:**

1. `go/internal/tenantctx/` — new typed context package
2. `go/internal/auth/jwt.go` — `TenantID` field on `Claims` and `hs256RawClaims`
3. `go/internal/auth/token_cache.go` — `TenantID` field on `TokenInfo` and `TokenRow`
4. `go/internal/auth/pgx_querier.go` — DB query now fetches `tenant_id` from `access_tokens`
5. `go/internal/auth/middleware.go` — `BearerTenantMiddleware` and `HS256TenantMiddleware`
6. `go/internal/transport/transport.go` — `RuntimeIdentity` struct
7. `go/internal/tenantctx/tenantctx_test.go` — 8 context round-trip tests
8. `go/internal/auth/tenant_middleware_test.go` — 15 middleware + identity tests

**Not implemented (deferred to R-4c through R-4e):**
- DAL query `WHERE tenant_id = $n` clauses (R-4c)
- Admin route refactoring for tenant scoping (R-4c)
- Run recorder tenant_id parameter (R-4d/R-4e)
- Redis key tenant-prefix (Tier 2, deferred indefinitely)

---

## Identity Structures Changed

### `auth.Claims` (`go/internal/auth/jwt.go`)
Added field:
```go
TenantID string `json:"tenant_id,omitempty"`
```
Source: JWT payload `tenant_id` claim. Absent claim → empty string (not an error at validation time;
middleware layer enforces non-empty for tenant-scoped operations).

Both `ValidateJWT` (RS256) and `ValidateHS256JWT` (HS256) now propagate `TenantID` into `Claims`.

### `hs256RawClaims` (`go/internal/auth/jwt.go`)
Added field:
```go
TenantID string `json:"tenant_id,omitempty"`
```
The auth service issues HS256 tokens. When the auth service is updated to add `tenant_id` to its
JWT payload, this field will be populated automatically.

### `auth.TokenInfo` (`go/internal/auth/token_cache.go`)
Added field:
```go
TenantID string `json:"tenant_id,omitempty"`
```
Source: `them.access_tokens.tenant_id` (NOT NULL after R-4a migration). Populated at DB lookup;
flows through L2 (Redis) and L1 (sync.Map) cache layers.

### `auth.TokenRow` (`go/internal/auth/token_cache.go`)
Added field:
```go
TenantID string // UUID string; empty for pre-R-4a records
```
This is the raw DB scan target.

---

## Tenant Resolution Sources

| Path | Source | Where extracted |
|---|---|---|
| Bearer token (API clients) | `access_tokens.tenant_id` (DB row, NOT NULL) | `pgx_querier.go` → `TokenRow.TenantID` → `TokenInfo.TenantID` → `BearerTenantMiddleware` → `tenantctx` |
| HS256 JWT (auth service sessions) | JWT `tenant_id` claim | `ValidateHS256JWT` → `Claims.TenantID` → `HS256TenantMiddleware` → `tenantctx` |
| RS256 JWT (test/admin tokens) | JWT `tenant_id` claim | `ValidateJWT` → `Claims.TenantID` → (can be wrapped in a tenant middleware if needed) |
| Entry point slug (public EPs) | `applications.tenant_id` via `epconfig.EPConfig.TenantID` | WS/SSE handlers already populate `sessInfo.TenantID` from this path (unchanged) |

**TenantID is NEVER read from:**
- `X-Tenant-ID` header
- `tenant_id` query parameter
- Request body fields

---

## New Package: `tenantctx` (`go/internal/tenantctx/tenantctx.go`)

A small, dependency-free typed context package.

| Function | Purpose |
|---|---|
| `WithTenantID(ctx, id) ctx` | Store validated TenantID in context |
| `TenantIDFromCtx(ctx) (string, error)` | Retrieve TenantID; ErrNoTenant if absent; ErrInvalidTenant if empty |
| `MustTenantIDFromCtx(ctx) string` | Panics if absent — for handlers guarded by middleware |

**Sentinel errors:**
- `tenantctx.ErrNoTenant` — no TenantID in context (missing authentication)
- `tenantctx.ErrInvalidTenant` — TenantID present but empty string (data integrity guard)

**Context key:** unexported struct type `tenantKey{}` — not a string, prevents any stringly-typed
key collision.

---

## New Middleware Primitives (`go/internal/auth/middleware.go`)

### `BearerTenantMiddleware(cache *Cache)`
- Validates `Authorization: Bearer <token>` against the token cache
- Extracts `TenantID` from `TokenInfo.TenantID`
- **401** if token is missing or invalid
- **403** if `TokenInfo.TenantID` is empty (token has no tenant identity)
- On success: places both `*TokenInfo` and `TenantID` into request context

### `HS256TenantMiddleware(secret []byte)`
- Validates `Authorization: Bearer <token>` as an HS256 JWT
- Extracts `TenantID` from `Claims.TenantID`
- **401** if token is missing or invalid
- **403** if `Claims.TenantID` is empty (JWT issued without `tenant_id` claim)
- On success: places both `*Claims` and `TenantID` into request context

Both middlewares use `writeForbidden` (new helper, returns `{"error": "<msg>"}` JSON at 403).

**These middlewares are NOT yet wired into any route.** Wiring admin and runtime routes to
tenant-scoped middleware is R-4c.

---

## RuntimeIdentity (`go/internal/transport/transport.go`)

```go
type RuntimeIdentity struct {
    TenantID  string
    AppID     string
    UserID    int64
    SessionID string
    RunID     string  // empty until a workflow is started
}
```

This type defines the five-element runtime principal. It is not yet used programmatically; it
documents the intended shape for R-4c/R-4d when run creation and session wiring require it.

---

## Legacy Compatibility

### Bearer tokens (`access_tokens`)
R-4a migration backfilled `tenant_id = '00000000-0000-0000-0000-000000000001'` (bootstrap tenant)
for all pre-existing records (8 agents, 2 orchestrators, all 0 access_tokens at migration time).

**No compatibility fallback was needed in the query layer** — the NOT NULL column guarantees a
non-empty UUID for every row. `pgx_querier.go` reads it directly.

### JWT tokens issued by auth service
The current auth service does NOT yet include a `tenant_id` claim in its HS256 tokens. This means
`Claims.TenantID` will be empty for all current production JWT tokens.

**Compatibility strategy:**
- The existing `HS256Middleware` (no tenant enforcement) continues to work as before — it is not
  replaced, only supplemented by `HS256TenantMiddleware`.
- `HS256TenantMiddleware` is NOT wired to any route yet (R-4c will make that decision).
- When the auth service is updated to include `tenant_id` in its tokens, `HS256TenantMiddleware`
  will start working without any gateway changes.
- **The bootstrap tenant is NOT silently assigned to JWT tokens without a `tenant_id` claim.**
  A token without `tenant_id` gets 403 when `HS256TenantMiddleware` is applied — this is correct
  behavior that will motivate the auth service upgrade.

**This compatibility strategy is temporary** and marked for resolution when auth service adds
the `tenant_id` claim (tracked in docs/STATUS.md).

---

## Test Results

| Suite | Before R-4b | After R-4b |
|---|---|---|
| `go test ./...` | 424 tests, 27 packages | 447 tests, 28 packages |
| `go test -race ./internal/auth/... ./internal/tenantctx/...` | n/a | PASS (no data races) |
| Python sanity (01 02 03 04 15) | 55 passed | 55 passed |

**23 new tests** across 2 new test files:
- `go/internal/tenantctx/tenantctx_test.go` (8 tests, new package)
- `go/internal/auth/tenant_middleware_test.go` (15 tests)

---

## Files Changed

| File | Change |
|---|---|
| `go/internal/tenantctx/tenantctx.go` | **New** — typed context package |
| `go/internal/tenantctx/tenantctx_test.go` | **New** — 8 tests |
| `go/internal/auth/jwt.go` | Added `TenantID` to `Claims`, `hs256RawClaims`; propagated in `ValidateHS256JWT` |
| `go/internal/auth/token_cache.go` | Added `TenantID` to `TokenInfo`, `TokenRow`; propagated in `rowToTokenInfo` |
| `go/internal/auth/pgx_querier.go` | Query now selects `tenant_id` from `access_tokens` |
| `go/internal/auth/middleware.go` | Added `BearerTenantMiddleware`, `HS256TenantMiddleware`, `writeForbidden` |
| `go/internal/auth/tenant_middleware_test.go` | **New** — 15 tests |
| `go/internal/transport/transport.go` | Added `RuntimeIdentity` struct |
| `docs/architecture-v2/R4B_IMPLEMENTATION_REPORT.md` | **New** — this report |
| `docs/architecture-v2/implementation-status.md` | Updated |
| `docs/architecture-v2/lessons-learned.md` | Updated |
| `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` | Updated for R-4c |
| `go/TEST_INDEX.md` | Updated |

---

## What R-4b Does NOT Change

- No DAL queries were modified (no `WHERE tenant_id = $n`)
- No admin routes were refactored for tenant context
- No runtime WS/SSE/artifact ownership enforcement was added
- No tenant provisioning UI or APIs were added
- No session struct or run recorder changes
- R-4c, R-4d, and R-4e were NOT started

---

## Remaining Risks

| Risk | Severity | Notes |
|---|---|---|
| Auth service does not yet issue `tenant_id` in JWT | Medium | `HS256TenantMiddleware` returns 403 for all current JWT tokens — middleware is NOT yet wired to any route |
| Legacy access_tokens pre-R-4a bootstrap backfill | Low | All existing records were backfilled with bootstrap tenant UUID; no orphan records |
| `writeForbidden` response message not yet standardised across codebase | Low | Other places that return 403 use `http.Error` with a string body; will be unified in R-4c |

---

## Next: Phase R-4c

R-4c adds `WHERE tenant_id = $n` to all DAL queries on tenant-scoped tables and wires
the tenant-aware middleware to admin and runtime routes.

Read before starting:
- `docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md` §5 (propagation chain)
- `go/internal/admin/dal/` — all files
- `go/internal/auth/middleware.go` — the new tenant middleware constructors
