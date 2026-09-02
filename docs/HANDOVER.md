# Handover — Multi-Tenancy Step 2
**Date:** 2026-09-02
**Commit:** TBD (pending)
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
| Step 2 | Redis key hardening | Complete | TBD |
| Step 3 | Temporal workflow ID prefix with `{tenant_id}-` | Not started | — |
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

## Next recommended task — Step 3: Temporal workflow ID prefix

**Goal:** Prevent Temporal workflow ID collisions between tenants when two tenants run the same orchestrator or agent workflow.

**What to change:**
- In `go/internal/temporal/workflow.go` or `go/internal/execution/lifecycle.go`, prefix the Temporal workflow ID with `{tenant_id}-`.
- Current pattern: `run-{run_id}` or `{orchestrator_slug}-{run_id}`
- New pattern: `{tenant_id}-{run_id}` (or similar — check actual pattern in code first)

**Files to read before starting:**
- `go/internal/temporal/workflow.go`
- `go/internal/execution/lifecycle.go`
- `go/cmd/dag-worker/main.go`
- `docs/CURRENT.md`

**Test requirement:** After changes, restart `them-dag-worker` and `them-go-worker`, then run `go test ./...`.

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
Continue multi-tenancy implementation — Step 3: Temporal workflow ID prefix.

Current state: Steps 1 and 2 are complete and merged.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (commit 4ccb4c4)
- Step 2: All Redis keys now include tenant_id; 46 packages pass (see docs/HANDOVER.md)

Read docs/HANDOVER.md for full context before starting.

Step 3 scope only: Prefix Temporal workflow IDs with {tenant_id}- to prevent
cross-tenant workflow ID collisions.

Files to check:
- go/internal/temporal/workflow.go
- go/internal/execution/lifecycle.go
- go/cmd/dag-worker/main.go

After changes: run go test ./... (zero failures required).
Restart them-dag-worker and them-go-worker after any workflow.go changes.
Update docs/HANDOVER.md at the end.
```
