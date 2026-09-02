# Handover — Multi-Tenancy Step 3
**Date:** 2026-09-02
**Commits:** Step 2: 97c9d71 | Step 3: 98ccf03
**Branch:** main

---

## What was completed (Step 2 — Redis key hardening)

### Goal
All tenant-scoped Redis keys must include `{tenant_id}` in the key to prevent cross-tenant data leakage when two tenants have the same slug or agent ID.

### Changes shipped

| File | What changed |
|---|---|
| `go/internal/mcp/registry.go` | Removed global `manifestKeyPrefix`/`healthKeyPrefix` constants. Added `manifestKey(tenantID, slug)` and `healthKey(tenantID, slug)` helpers. Updated `CacheManifest`, `GetCachedManifest`, `CacheHealth` to accept `tenantID` param. Keys: `them:{tenant_id}:mcp:manifest:{slug}` / `them:{tenant_id}:mcp:health:{slug}`. |
| `go/internal/mcp/health.go` | Updated `setStatus` and `setManifest` to pass `w.server.TenantID` to the registry methods. |
| `go/internal/admin/scanjob.go` | `scanStateHashKey(agentID)` → `scanStateHashKey(tenantID, agentID)`. `runScanJob` gains `tenantID string` as first param. Key: `them:{tenant_id}:scan:state:{agent_id}`. |
| `go/internal/admin/agents.go` | Updated `runScanJob` call site to pass `tenantID` (already extracted from JWT context). |
| `go/internal/gate/gate.go` | Added `TenantID string` to `Config`. `rlKey()` now produces `rl:them:{tenant_id}:token:{hash}:{minute}`. |
| `go/internal/execution/lifecycle.go` | Wired `TenantID: resolvedCfg.TenantID` into `gateCfg`. |
| `go/internal/ratelimit/limiter.go` | `CheckToken(ctx, tokenHash, limit)` → `CheckToken(ctx, tenantID, tokenHash, limit)`. Key: `rl:them:{tenant_id}:token:{hash}:{minute}`. |
| `go/internal/ratelimit/limiter_test.go` | Updated `TestCheckTokenAllowed` and `TestCheckTokenDenied` to pass `testTenantID`; updated hard-coded key in `TestCheckTokenDenied`. |
| `go/internal/dashboard/handler.go` | Removed `scanStatePrefix` constant. `ServeHTTP` now captures `tenantID` from JWT claims (no longer discards them). `sendSnapshots` threads `tenantID` to `sendAgentSnapshot` and `sendScanSnapshot`. Scan state keys are now `them:{tenant_id}:scan:state:{...}`. |
| `go/internal/dashboard/handler_test.go` | Added `testTenantID` constant. `makeHS256JWT` includes `tenant_id` claim. Updated `TestDashboard_AgentSnapshot` and `TestDashboard_ScanSnapshot` to use tenant-scoped keys. |
| `docs/REDIS.md` | Updated key patterns for MCP manifest, MCP health, scan state, and rate limit. Added new `rl:them:{tenant_id}:token:...` row. |

### Tests
- `go test ./...` — zero failures across all 46 packages
- All gate, ratelimit, dashboard, mcp, admin, authserver packages pass

### Coverage added
- `TestCheckTokenAllowed` — verifies tenant-scoped rate limit key allows first request
- `TestCheckTokenDenied` — verifies correct key format for denial (pre-fills tenant-scoped key)
- `TestDashboard_AgentSnapshot` — verifies snapshot is read from tenant-scoped scan state hash
- `TestDashboard_ScanSnapshot` — verifies scan snapshot is read from tenant-scoped key

---

## Completed steps so far

| Step | Description | Status | Commit |
|---|---|---|---|
| Step 1 | JWT + tenant membership foundation | Complete | 4ccb4c4 |
| Step 2 | Redis key hardening | Complete | 97c9d71 |
| Step 3 | Temporal workflow ID prefix with `{tenant_id}:` | Complete | 98ccf03 |
| Step 4 | Tenant CRUD API + provisioning | Not started | — |
| Step 5 | OIDC login flow | Not started | — |
| Step 6 | Managed Apps foundation | Not started | — |

---

## Known constraints and surprises

1. **Go 1.25 is only available inside Docker** — the host has no `go` binary. Always run `go test ./...` and `go build ./...` via `docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go ...`

2. **`ratelimit.Limiter.CheckToken` is defined but currently unused** — main.go assigns `_ = limiter`. The signature was updated to be correct, but nothing calls it yet. It will be wired in when per-token rate limiting is moved out of the Lua gate script.

3. **Dashboard `sendScanSnapshot`** — The `scan:{artifactID}` channel and `them:{tenant_id}:scan:state:{artifactID}` are actually distinct from the agent scan flow (which uses `agent:{agentID}` channel and `them:{tenant_id}:scan:state:{agentID}` hash). Both now use tenant-scoped keys, but they remain separate flows.

4. **MCP server `TenantID` field** — `w.server.TenantID` flows from the DB query in the supervisor. Verify this field is populated in `go/internal/mcp/dal.go` scan query if you add a new MCP server.

---

## Next recommended task — Step 4: Tenant CRUD API + provisioning

**Goal:** Allow new tenants to be created via an admin API. This is the foundation for real multi-tenant onboarding.

**What to build:**
- `POST /admin/tenants` — create a tenant (name, slug, optional config)
- `GET /admin/tenants` — list tenants (super_admin only)
- `GET /admin/tenants/{id}` — get tenant by ID
- DB: a `them.tenants` table (`id UUID PK, slug TEXT UNIQUE, name TEXT, created_at, config JSONB`)
- Migration: `db/054_tenants.sql`
- Wire the handler in `go/cmd/them/main.go`

**Files to read before starting:**
- `docs/architecture/MULTI_TENANCY_DESIGN.md` §4 (Tenant Model) and §5 (Identity)
- `go/internal/admin/` — follow Handler → Service → DAL pattern
- `db/001_schema.sql` — understand the existing schema
- `go/CLAUDE.md` — file size and testing rules

**Test requirement:** Add handler tests following the pattern in `go/internal/admin/tenant_http_test.go`.

⚠️ **Session boundary recommendation:** Step 4 is a larger feature (new DB table, migration, handler, service, DAL, tests). This is a good point to start a fresh session to get maximum context for the implementation.

---

## Startup commands for next session

```bash
cd /opt/docker/them

# Verify stack is healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Read before starting
cat docs/HANDOVER.md
cat docs/architecture/MULTI_TENANCY_DESIGN.md
cat go/CLAUDE.md

# Run tests before making any changes
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 4: Tenant CRUD API + provisioning.

Current state: Steps 1–3 are complete and merged.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (commit 4ccb4c4)
- Step 2: All Redis keys tenant-scoped (commit 97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (commit 98ccf03)
All 46 Go packages pass (go test ./...).

Read docs/HANDOVER.md and docs/architecture/MULTI_TENANCY_DESIGN.md §4–5 before starting.

Step 4 scope only:
1. Create db/054_tenants.sql migration: them.tenants (id UUID PK, slug TEXT UNIQUE, name TEXT, created_at TIMESTAMPTZ, config JSONB DEFAULT '{}')
2. Apply migration to live DB
3. Build Go CRUD: POST /admin/tenants, GET /admin/tenants, GET /admin/tenants/{id}
4. Follow Handler → Service → DAL pattern in go/internal/admin/
5. Super_admin only — use RequireSuperAdmin middleware
6. Tests required; follow tenant_http_test.go pattern

After each change: run go test ./... (zero failures required before commit).
Update docs/HANDOVER.md at the end.
```
