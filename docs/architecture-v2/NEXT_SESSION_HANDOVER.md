# Phase R-4a Complete — Handover to R-4b

**Date:** 2026-07-31
**Branch:** main
**HEAD:** (R-4a commit — see below after git push)
**Prepared by:** Phase R-4a session

---

## Current Objective

Phase R-4a (Tenant Database Foundation) is **complete**. The next task is **Phase R-4b:
Go Auth Middleware Tenant Resolution** — resolving TenantID from JWT claims and bearer token
lookup, propagating it through context into SessionInfo.

---

## What Was Completed This Session (R-4a)

1. `docs/architecture-v2/R4_TENANT_IMPLEMENTATION_PLAN.md` — formal plan resolving O-01..O-08
2. `db/026_tenant_foundation.sql` — single idempotent migration (transactional):
   - Creates `them.tenants` table
   - Inserts bootstrap tenant (`00000000-0000-0000-0000-000000000001`, slug `default`)
   - Adds `tenant_id UUID NOT NULL` to: agents (8 rows backfilled), orchestrators (2 rows),
     access_tokens, applications, runs, audit_logs, app_orchestrators
   - Drops global uniqueness constraints, replaces with tenant-scoped indexes
   - Creates `them.run_artifacts` with `tenant_id` included from the start
3. `db/validate_r4a.sql` — standalone validation script (all 9 checks pass)
4. `docs/architecture-v2/R4A_IMPLEMENTATION_REPORT.md` — full implementation report
5. `docs/architecture-v2/implementation-status.md` — updated with R-4a state
6. `docs/architecture-v2/lessons-learned.md` — R-4a lessons appended

Full details: `docs/architecture-v2/R4A_IMPLEMENTATION_REPORT.md`

---

## Stack State

| Container | Status |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-worker (Python) | Running |
| them-postgres | Healthy — migration applied |
| them-redis | Healthy |

**Migration applied:** `db/026_tenant_foundation.sql` applied to live VPS DB ✓
**Validation:** all 9 checks passed on live DB ✓

Note: `db/025_run_artifacts.sql` (standalone file) was NOT applied to the live DB before R-4a;
`them.run_artifacts` was created by `026_tenant_foundation.sql` instead, with tenant_id
included from the start. The standalone `025` file should be marked as superseded.

---

## Test State

```
go test ./...        →   422 passed, 0 failed (27 packages)
Python sanity 01-04,15  →   55 passed, 0 failed
```

No Go or Python application code was changed in R-4a.

---

## Bootstrap Tenant

| Field | Value |
|---|---|
| ID | `00000000-0000-0000-0000-000000000001` |
| Slug | `default` |
| Display name | Default Development Tenant |

This UUID is deterministic and immutable. Future Go/Python code that needs to resolve the
development tenant can hardcode this UUID.

---

## DB Schema Changes

New constraint names (for reference in DAL tests):
- `uq_agents_tenant_slug` — `(tenant_id, slug)` on `them.agents`
- `uq_orchestrators_tenant_name` — `(tenant_id, name)` on `them.orchestrators`
- `uq_app_orchestrators_tenant_name` — `(tenant_id, name)` on `them.app_orchestrators`

New indexes:
- `idx_agents_tenant`, `idx_orchestrators_tenant`, `idx_access_tokens_tenant`,
  `idx_applications_tenant`, `idx_runs_tenant`, `idx_audit_logs_tenant`,
  `idx_app_orchestrators_tenant`, `idx_run_artifacts_tenant`

---

## Hard Constraints — Carry Forward

- **Bootstrap tenant UUID `00000000-0000-0000-0000-000000000001` is immutable** — never change
- **Temporal is the single durable owner of every run.**
- **Never log token values, API keys, or secrets** at any log level.
- **All Go changes require `go test ./...` before commit.** Zero regressions allowed.
- **Workflow ID scheme `ctx-{contextID}`** must be preserved.
- **`llm_providers`, `config`, `middleware_defs` are platform-global** — no tenant_id ever
- **DB name and schema: `them` only.** Never `odin`.
- **`RUN_EVENTS_MODE`**: Worker must be `streams`; Bridge must be `dual`. Do not change.

---

## Known Issues and Blockers

| Issue | Severity | Notes |
|---|---|---|
| No runtime tenant enforcement | Expected | R-4a is DB-only; R-4b through R-4e add enforcement |
| Cross-tenant access returns 200/data (not 403) | Expected | Not enforced until R-4b/R-4c complete |
| `db/025_run_artifacts.sql` superseded | Low | The `025` file still exists in repo but was not applied to live DB; `026` creates `run_artifacts` instead. Can be kept for documentation but should never be applied to a DB that has `026`. |

---

## Next Task: Phase R-4b — Go Auth Middleware Tenant Resolution

**Scope:** Resolve TenantID from JWT claims or bearer token lookup; propagate through context.

This is a **DB-only** foundation for app code. R-4b adds Go code changes.

**Read before starting R-4b:**
- `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §2 (tenant boundary, steps 1–2)
- `docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md` §1.2 (tenant context resolution chain)
- `go/internal/auth/token_cache.go` — add `TenantID` to `TokenInfo`
- `go/internal/transport/transport.go` — add `TenantID` to `SessionInfo`
- `go/internal/auth/middleware.go` — add tenant extraction from JWT claims
- `go/internal/ws/handler.go` and `go/internal/sse/handler.go` — populate TenantID in sessInfo
- `go/internal/epconfig/epconfig.go` — confirm EPConfig carries TenantID (may already be there)

**R-4b scope:**
1. Add `TenantID string` to `auth.TokenInfo` struct; populate from `them.access_tokens.tenant_id`
2. Add `TenantID string` to `transport.SessionInfo` struct
3. In WS + SSE handlers: set `sessInfo.TenantID` from `tokenInfo.TenantID` (authenticated) or `resolvedCfg.TenantID` (public EP)
4. Verify `EPConfig.TenantID` is populated; add if not
5. Write tests: `TestSession_TenantIDFromToken`, `TestSession_TenantIDFromPublicEP`
6. Run `go test ./...` — must pass

**R-4b does NOT:**
- Add `WHERE tenant_id = $n` to any DAL query (that is R-4c)
- Enforce cross-tenant 403 (that is R-4c after DAL changes)
- Change `runs.CreateRun` signature (that is R-4d)

**Files most relevant to R-4b:**

| File | Why |
|---|---|
| `go/internal/auth/token_cache.go` | Add TenantID to TokenInfo + DB query |
| `go/internal/transport/transport.go` | Add TenantID to SessionInfo |
| `go/internal/auth/middleware.go` | Extract TenantID from JWT claims |
| `go/internal/ws/handler.go` | Populate sessInfo.TenantID |
| `go/internal/sse/handler.go` | Populate sessInfo.TenantID |
| `go/internal/epconfig/epconfig.go` | Confirm/add EPConfig.TenantID |
| `db/026_tenant_foundation.sql` | Schema reference — tenant_id columns are NOT NULL now |

---

## Starting the Next Session

```bash
# Confirm HEAD and migration state
git log --oneline -5
docker exec them-postgres psql -U them -d them -c "SELECT id, slug FROM them.tenants;"
docker exec them-postgres psql -U them -d them -c "SELECT version, description FROM them.schema_migrations ORDER BY applied_at DESC LIMIT 3;"

# Run Go tests to confirm clean baseline
docker run --rm -v /home/avi/them/go:/workspace -w /workspace golang:1.24-alpine go test ./...

# Python sanity
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

**First prompt for the next session:**

> Phase R-4a is complete (HEAD from git log, 422 Go tests passing, tenant DB foundation
> applied to live DB). Start Phase R-4b: Go Auth Middleware Tenant Resolution. Read
> docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md §2 and
> TENANT_FOUNDATION_DECISIONS.md §1.2 before writing any code. Add TenantID to TokenInfo
> and SessionInfo; populate in WS/SSE handlers. Use Sonnet.

---

## Commits This Session

- R-4a commit (to be added after git commit/push)

Push status: **will push after commit**

---

## R-4b through R-4e NOT started

- R-4b (Go auth middleware tenant resolution): NOT started
- R-4c (DAL WHERE tenant_id): NOT started
- R-4d (session propagation): NOT started
- R-4e (run recorder): NOT started
