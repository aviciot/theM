# R-4c2 Complete — Handover to R-4d

**Date:** 2026-08-01
**Branch:** main
**HEAD:** cf1fd5c (pre-R-4c2 commit; R-4c2 changes are uncommitted at handover time)
**Prepared by:** R-4c2 implementation session

---

## Deployment State

| Fact | Value |
|---|---|
| Active project | `them_gateway` |
| Compose working dir | `/home/avi/them` |
| Production command | `bash scripts/deploy.sh up` |
| Go bridges | `them-go-bridge`, `them-go-bridge-2` — running R-4c2 binary |
| Go workers | `them-go-worker`, `them-go-worker-2` — running, healthy |
| All other services | Running |

---

## Current Objective

R-4c2 is **complete**. The next task is **Phase R-4d**.

---

## R-4c2 Work Completed

### What was done
1. **Route classification** in `go/internal/admin/router.go`:
   - Tenant-scoped: agents, orchestrators, applications, tokens, runs — `BearerTenantMiddleware` applied
   - Platform-global: llm-providers, monitoring-config, llm-routing, sessions — JWT + RequireSuperAdmin only

2. **Shim removed**: `tenantIDFromCtxOrBootstrap` and `bootstrapTenantID` deleted from `middleware.go`

3. **All handlers updated**: agents.go, orchestrators.go, applications.go, runs.go, tokens.go — all 23 call sites replaced with `tenantctx.MustTenantIDFromCtx(r.Context())`

4. **BuildRouter signature**: added `tokenCache *auth.Cache` parameter; wired from `main.go`

5. **Tests added**:
   - `admin_test.go`: added `withTestTenant` middleware helper; applied to all tenant-handler tests
   - `tenant_http_test.go`: 12 new HTTP-layer tests (TH-01 through TH-12) in S1-34

6. **TEST_INDEX.md**: added S1-34 section; updated S1-15 count (34→46); total 468→480

7. **Docs created**: `R4C2_IMPLEMENTATION_REPORT.md`

### Test results
- `go test ./...` in Docker builder: **480 passed, 0 failed** (29 packages)
- Python sanity 01 02 03 04 15: **55 passed, 0 failed**
- Live smoke tests: 401 on missing token, 401 on invalid token, 401 on JWT-not-in-access-tokens, 200 on platform-global with JWT

### Build lesson (critical for future sessions)
- `docker compose build` uses layer cache aggressively — always use `--no-cache` when Go source changes
- `docker restart` reuses existing container image — use `docker compose up --force-recreate` to pick up new image
- `strings /app/them | grep tenantIDFromCtxOrBootstrap` confirmed old shim is NOT in the binary

---

## Files Changed in R-4c2

| File | Change |
|---|---|
| `go/internal/admin/router.go` | Restructured route groups; added tokenCache param; BearerTenantMiddleware wired |
| `go/internal/admin/middleware.go` | Removed bootstrapTenantID, tenantIDFromCtxOrBootstrap |
| `go/internal/admin/agents.go` | Replaced shim calls with MustTenantIDFromCtx |
| `go/internal/admin/orchestrators.go` | Replaced shim calls with MustTenantIDFromCtx |
| `go/internal/admin/applications.go` | Replaced shim calls with MustTenantIDFromCtx |
| `go/internal/admin/runs.go` | Replaced shim calls with MustTenantIDFromCtx |
| `go/internal/admin/tokens.go` | Replaced shim calls with MustTenantIDFromCtx |
| `go/internal/admin/admin_test.go` | Added withTestTenant helper; applied to tenant-scoped tests |
| `go/internal/admin/tenant_http_test.go` | New file — 12 HTTP-layer tenant tests |
| `go/cmd/them/main.go` | Added tokenCache argument to BuildRouter call |
| `go/TEST_INDEX.md` | S1-34 added; count 468→480 |
| `docs/architecture-v2/R4C2_IMPLEMENTATION_REPORT.md` | New report |
| `docs/architecture-v2/implementation-status.md` | R-4c2 status added |
| `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` | This file |

---

## Working Tree State

R-4c2 changes are uncommitted (the session summary was produced before a final commit+push).
The changes have been verified in the live container via force-rebuild with `--no-cache`.

**Before starting R-4d, the next session must:**
1. `git status` — verify the R-4c2 files listed above are the only modified files
2. Commit them: `git add <files> && git commit -m "feat(tenant): R-4c2 — wire BearerTenantMiddleware to admin routes, remove bootstrap shim"`
3. Push: `git push origin main`

---

## Known Constraints

- TenantID is NEVER accepted from request headers, query params, or body — only from `auth.Cache` lookup of the bearer token
- Bootstrap tenant UUID `00000000-0000-0000-0000-000000000001` is no longer used in any Go handler
- Platform-global routes (llm-providers, monitoring-config, llm-routing, sessions) do NOT use BearerTenantMiddleware by design — they are multi-tenant administrative resources
- The `/runs` group uses JWT + BearerTenantMiddleware but NOT RequireSuperAdmin — run access is tenanted but open to any authenticated identity with a valid bearer token
- Admin routes require BOTH a valid super_admin JWT and a bearer token with tenant — they cannot share the same Authorization header in a real request without a header-swap mechanism

---

## Next Task: R-4d

**Goal:** DAL-level cross-tenant enforcement + integration test coverage.

Scope:
1. Verify all DAL queries use `AND tenant_id = $N::uuid` in WHERE clauses (agents, orchestrators, applications, runs, tokens)
2. Add integration tests that verify cross-tenant reads return 404 (not data from wrong tenant)
3. Token endpoint audit: `POST /admin/tokens` must scope the created token to the requesting tenant
4. Entry point queries: verify `entry_points` table queries include tenant scoping

**Do NOT start R-4d without first committing and pushing R-4c2.**

---

## Startup Commands for Next Session

```bash
# Read first:
cat docs/architecture-v2/NEXT_SESSION_HANDOVER.md
cat go/CLAUDE.md
cat CLAUDE.md

# Verify stack:
bash scripts/deploy.sh status

# Commit R-4c2 if not yet committed:
git status
git log --oneline -3
```

First prompt for next session:
> The THEM repo is at `/home/avi/them`. R-4c2 is complete but may not be committed yet — check `git status` and commit if needed, then start R-4d. Read `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` before writing any code.
