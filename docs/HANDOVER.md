# Handover — Multi-Tenancy Step 1
**Date:** 2026-09-02
**Commit:** 4ccb4c4
**Branch:** main

---

## What was completed (Step 1)

### Goal
Make tenant isolation real. JWT must carry `tenant_id`. Bootstrap fallback removed.

### Changes shipped

| File | What changed |
|---|---|
| `db/053_tenant_memberships.sql` | New migration: creates `auth_service.tenant_memberships`; backfills 2 existing users to bootstrap tenant |
| `auth_service/SCHEMA.sql` | Added `tenant_memberships` table definition as canonical doc |
| `go/internal/authserver/store.go` | Added `GetTenantMembership()` to Store interface; added `ErrNoMembership` |
| `go/internal/authserver/pgx.go` | Implemented `GetTenantMembership` (SELECT from tenant_memberships) |
| `go/internal/authserver/jwt.go` | `accessClaims` gains `TenantID string json:"tenant_id"` field; `IssueAccessToken` gains `tenantID` param |
| `go/internal/authserver/service.go` | `issuePair()` looks up membership, uses membership role, fails with `ErrNoTenantMembership` if no row; added sentinel |
| `go/internal/authserver/handlers.go` | Maps `ErrNoTenantMembership` → 403 |
| `go/internal/admin/middleware.go` | **Bootstrap fallback removed.** `AdminTenantMiddleware` now returns 403 if `claims.TenantID == ""` |

### Tests
- `go test ./...` — zero failures across all 40 packages
- `TestLoginEmbedsTenantID` — verifies JWT carries bootstrap tenant_id
- `TestLoginNoMembershipBlocked` — verifies login blocked when no membership row exists
- `TestBridgeCompatibility` — verifies bridge's `ValidateHS256JWT` reads `tenant_id` from new JWT format
- `TestTenantHTTP_TokenWithoutTenant_Agents_403` (TH-03) — verifies JWT without tenant_id → 403 (new `tenantAdminRouterNoTenant` helper)
- All existing TH-01 through TH-12 tests updated to new contract

### Live smoke test (verified)
- `admin` user logs in → JWT contains `tenant_id: 00000000-0000-0000-0000-000000000001`
- `GET /api/v1/admin/agents` with new JWT → 200, returns 21 agents
- Migration 053 applied to running DB; 2 users backfilled

---

## Current state of the DB

```sql
auth_service.tenant_memberships:
  admin  → 00000000-0000-0000-0000-000000000001 | super_admin
  avi    → 00000000-0000-0000-0000-000000000001 | super_admin
```

All other DB state unchanged. No schema changes to `auth_service.users` (decided to keep it simple — user-level `tenant_id` not needed since membership table provides the join).

---

## What is NOT done yet (Step 2–6)

| Step | What | Status |
|---|---|---|
| Step 2 | Redis key fixes (MCP manifest/health, rate-limit, scan state) | Not started |
| Step 3 | Temporal workflow ID prefix with `{tenant_id}-` | Not started |
| Step 4 | Tenant CRUD API + provisioning | Not started |
| Step 5 | OIDC login flow | Not started |
| Step 6 | Managed Apps foundation | Not started |

---

## Next recommended task — Step 2: Redis key hardening

Four key patterns to fix (all string changes in known files):

| Current key | Fixed key | File to change |
|---|---|---|
| `them:mcp:manifest:{slug}` | `them:{tenant_id}:mcp:manifest:{slug}` | `go/internal/mcp/` |
| `them:mcp:health:{slug}` | `them:{tenant_id}:mcp:health:{slug}` | `go/internal/mcp/` |
| `them:scan:state:{agent_id}` | `them:{tenant_id}:scan:state:{agent_id}` | `go/internal/middleware/av/` or similar |
| `rl:them:token:{hash}:{min}` | `rl:them:{tenant_id}:token:{hash}:{min}` | `go/internal/ratelimit/limiter.go` |

Estimated effort: 2–3 days.

Start by reading `go/internal/mcp/`, `go/internal/ratelimit/`, and `docs/REDIS.md` before touching anything.

---

## Known constraints and surprises from Step 1

1. **Go 1.25 is only available inside Docker** — the host has no `go` binary. Always run `go test ./...` and `go build ./...` via `docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go ...`

2. **`auth_service.users.role_id` is an INTEGER FK** — do not try to read `role` from `users` directly. Always JOIN `auth_service.roles`. The membership table stores the role name (text) directly, which is correct.

3. **Login path uses membership role, not user's global role** — `issuePair()` now reads role from `tenant_memberships.role`, not `userRecord.Role`. For existing users they are the same (backfilled from roles.name). This is intentional — membership role becomes the authority for future multi-tenant RBAC.

4. **fakeStore in tests must implement GetTenantMembership** — any new test that creates a `fakeStore` and adds a user via `addUser()` automatically gets a bootstrap tenant membership (added in `addUser`). If you need to test "no membership" specifically, `delete(store.memberships, userID)` after `addUser`.

5. **The `tenant_http_test.go` tests now require tenant_id in the JWT** — all auto-injected JWTs in `tenantAdminRouter` include `tenant_id = bootstrapTenantID`. Tests that need a tenantless JWT must use `tenantAdminRouterNoTenant`.

---

## Startup commands for next session

```bash
cd /opt/docker/them

# Verify stack is healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Read before starting
cat docs/CURRENT.md
cat docs/architecture/MULTI_TENANCY_DESIGN.md  # §10 Redis Isolation
cat go/CLAUDE.md
cat docs/REDIS.md

# Run tests before making any changes
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 2: Redis key hardening.

Current state: Step 1 is complete and merged (commit 4ccb4c4).
JWT now carries tenant_id. Bootstrap fallback removed. All 40 Go packages pass.

Read docs/HANDOVER.md for full context before starting.
Read docs/REDIS.md, go/internal/mcp/, go/internal/ratelimit/limiter.go.

Step 2 scope only — do not touch JWT, middleware, or Temporal.

Fix these four Redis key patterns to add tenant_id prefix:
1. them:mcp:manifest:{slug}      → them:{tenant_id}:mcp:manifest:{slug}
2. them:mcp:health:{slug}        → them:{tenant_id}:mcp:health:{slug}
3. them:scan:state:{agent_id}    → them:{tenant_id}:scan:state:{agent_id}
4. rl:them:token:{hash}:{min}    → rl:them:{tenant_id}:token:{hash}:{min}

After each change: run go test ./... (zero failures required before commit).
Update docs/REDIS.md with the new key patterns.
Update docs/HANDOVER.md at the end.
```
