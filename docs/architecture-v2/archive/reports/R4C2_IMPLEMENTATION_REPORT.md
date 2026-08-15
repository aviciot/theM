# R-4c2 Implementation Report
# Trusted Tenant Middleware Wired to Admin/Control-Plane Routes
# Date: 2026-08-01

---

## Objective

Wire `BearerTenantMiddleware` to tenant-scoped admin/control-plane routes and remove the
`tenantIDFromCtxOrBootstrap` / `bootstrapTenantID` R-4c1 compatibility shim entirely.

After R-4c2, every admin route is classified as either:
- **Tenant-scoped control-plane** — requires JWT + RequireSuperAdmin + BearerTenantMiddleware
- **Platform-global control-plane** — requires JWT + RequireSuperAdmin only

TenantID is NEVER read from request headers, query parameters, path parameters, or body fields.

---

## Route Classification

### Tenant-scoped (BearerTenantMiddleware applied)
| Route prefix | Handler |
|---|---|
| `/admin/agents` | AgentsHandler |
| `/admin/orchestrators` | OrchestratorsHandler |
| `/admin/applications` | ApplicationsHandler |
| `/admin/tokens` | TokensHandler |
| `/runs` | RunsHandler |

### Platform-global (JWT + RequireSuperAdmin only)
| Route prefix | Handler |
|---|---|
| `/admin/llm-providers` | LLMProvidersHandler |
| `/admin/llm-routing` | LLMRoutingHandler |
| `/admin/monitoring-config` | MonitoringConfigHandler |
| `/admin/sessions` | SessionsHandler |

---

## Files Changed

### `go/internal/admin/router.go`
- Added `tokenCache *auth.Cache` parameter to `BuildRouter` signature
- Added `"github.com/aviciot/them/internal/auth"` import
- Restructured route groups:
  - Single admin group with JWT + RequireSuperAdmin
  - Within `/admin`: sub-group with `BearerTenantMiddleware` for tenant-scoped routes
  - Platform-global routes in parent `/admin` group (no BearerTenantMiddleware)
  - Separate `/runs` group with JWT + BearerTenantMiddleware (no RequireSuperAdmin)

### `go/cmd/them/main.go`
- Added `tokenCache` argument to `BuildRouter` call (line 298)

### `go/internal/admin/middleware.go`
- Removed `"context"` import
- Removed `"github.com/aviciot/them/internal/tenantctx"` import
- Removed `bootstrapTenantID` constant (`"00000000-0000-0000-0000-000000000001"`)
- Removed `tenantIDFromCtxOrBootstrap` function entirely

### `go/internal/admin/agents.go`, `orchestrators.go`, `applications.go`, `runs.go`, `tokens.go`
- Added `"github.com/aviciot/them/internal/tenantctx"` import to each
- Replaced all `tenantIDFromCtxOrBootstrap(r.Context())` calls with `tenantctx.MustTenantIDFromCtx(r.Context())`
- Total: 5 files × ~4 call sites = 23 replacements

### `go/internal/admin/admin_test.go`
- Added `"github.com/aviciot/them/internal/tenantctx"` import
- Added `testTenantID` constant
- Added `withTestTenant` helper middleware that injects `testTenantID` into context
- Applied `r.Use(withTestTenant)` to all chi routers in tenant-scoped handler tests

### `go/internal/admin/tenant_http_test.go` (new file)
- 12 HTTP-layer tests (TH-01 through TH-12)
- Uses real `auth.Cache` with in-memory `thTokenQuerier` and `thRedis`
- Tests cover: 401 on missing/invalid token, 403 on tenantless token, 200 on valid token,
  header/query-param injection resistance, platform-global route bypass, cross-resource coverage

---

## HTTP Behavior Enforced

| Scenario | Status |
|---|---|
| Missing Authorization header | 401 Unauthorized |
| Invalid/unknown bearer token | 401 Unauthorized |
| Valid token, empty TenantID (pre-R-4a) | 403 Forbidden |
| Valid token with TenantID | Handler reached |
| X-Tenant-ID header present | Ignored (TenantID from token only) |
| ?tenant_id query param present | Ignored (TenantID from token only) |
| Cross-tenant access | 404 Not Found (DAL-level enforcement) |
| Platform-global route, JWT only | 200 OK |

---

## Test Results

### Go unit tests (in Docker builder)
```
go test ./... — 480 passed, 0 failed (29 packages)
```
All new TH-01 through TH-12 tests pass. All pre-existing tests continue to pass.

### Python sanity tests
```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 — 55 passed, 0 failed
```

### Live validation
| Check | Result |
|---|---|
| Missing bearer token → 401 on /admin/agents | ✅ |
| Invalid bearer token → 401 on /admin/agents | ✅ |
| JWT-as-bearer (not in access_tokens) → 401 | ✅ |
| Platform-global /admin/llm-providers with JWT only → 200 | ✅ |
| Both Go bridges healthy (/health/live) | ✅ |

---

## Build Lesson

The Docker image build with `docker compose build` uses layer caching aggressively. When source
files in `go/` change but Docker's content hash for the COPY layer doesn't update (stale metadata),
the cached binary is used even though source changed. Fix: `docker compose build --no-cache`
forces a clean rebuild. Additionally, `docker restart` reuses the existing container image; use
`docker compose up --force-recreate` to pick up a newly built image.

Both lessons added to `docs/architecture-v2/lessons-learned.md`.

---

## Shim Removal Confirmed

Binary inspection after fresh `--no-cache` rebuild confirmed that
`github.com/aviciot/them/internal/admin.tenantIDFromCtxOrBootstrap` is NOT present in the
production binary (`strings /app/them | grep tenantIDFromCtxOrBootstrap` returns exit 1).

---

## What's Next (R-4d)

R-4d scope (do NOT start in this session):
- DAL-level cross-tenant enforcement (return 404 for cross-tenant reads via consistent filtering)
- Integration test coverage for multi-tenant scenarios against live PostgreSQL
- Token endpoint audit: verify `POST /admin/tokens` correctly scopes new token to requesting tenant
