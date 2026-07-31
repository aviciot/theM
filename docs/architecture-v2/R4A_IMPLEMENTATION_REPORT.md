# Phase R-4a Implementation Report — Tenant Database Foundation
# Date: 2026-07-31
# Status: COMPLETE

---

## Summary

Phase R-4a adds the PostgreSQL schema foundation for multi-tenant isolation (Tier 0 logical
isolation). The `them.tenants` table now exists, a deterministic bootstrap development tenant
is present, and `tenant_id UUID NOT NULL` columns have been added to all tenant-owned tables
with existing data backfilled.

No Go or Python application code was changed in this phase.

---

## Scope Executed

**Implemented:**
- `db/026_tenant_foundation.sql` — single idempotent migration
- `db/validate_r4a.sql` — standalone validation script
- `docs/architecture-v2/R4_TENANT_IMPLEMENTATION_PLAN.md` — formal plan document

**Not implemented (deferred to R-4b through R-4e):**
- Go auth middleware tenant resolution
- DAL query `WHERE tenant_id = $n` clauses
- SessionInfo.TenantID population
- Run recorder tenant_id parameter
- Redis key tenant-prefix (Tier 2, deferred indefinitely)

---

## Pre-Migration DB Inspection

Confirmed **no tenant migrations had been applied manually** to the VPS database before this
session. DB state:
- No `them.tenants` table
- No `tenant_id` column on any table
- 29 migrations applied (latest: `025_events_transport`)
- `them.run_artifacts` table did not yet exist in live DB

---

## Migration: `db/026_tenant_foundation.sql`

### Steps executed (in order within a single transaction)

1. **Create `them.tenants` table** with slug uniqueness constraint
2. **Insert bootstrap tenant** (deterministic UUID `00000000-0000-0000-0000-000000000001`, slug `default`)
3. **Add `tenant_id` as NULLABLE** to: agents, orchestrators, access_tokens, applications, runs, audit_logs, app_orchestrators
4. **Backfill** all existing rows with bootstrap tenant UUID
5. **Validate no NULLs remain** (DO block raises EXCEPTION on any residual NULL)
6. **Set NOT NULL** on all tenant_id columns
7. **Drop old global uniqueness constraints:**
   - `agents.agents_slug_key` UNIQUE constraint
   - `agents.idx_agents_slug` explicit UNIQUE index
   - `orchestrators.orchestrators_name_key` UNIQUE constraint
   - `app_orchestrators` global name uniqueness (was already absent in live DB — DROP IF EXISTS)
8. **Create tenant-scoped uniqueness indexes:**
   - `uq_agents_tenant_slug` on `(tenant_id, slug)`
   - `uq_orchestrators_tenant_name` on `(tenant_id, name)`
   - `uq_app_orchestrators_tenant_name` on `(tenant_id, name)`
9. **Create tenant query indexes** on all 7 tenant-owned tables
10. **Create `them.run_artifacts`** with `tenant_id` column included from the start (025 was not yet applied to live DB; IF NOT EXISTS guard handles re-run)
11. **Add tenant_id FK** to run_artifacts; backfill + tighten to NOT NULL
12. **Verify run_artifacts tenant/run ownership consistency**
13. **Record migration** in `them.schema_migrations`

---

## Bootstrap Tenant

| Field | Value |
|---|---|
| ID | `00000000-0000-0000-0000-000000000001` |
| Slug | `default` |
| Display name | Default Development Tenant |
| is_bootstrap | true |

This UUID is deterministic and must never change after deployment. It can be hardcoded in
Go/Python config, tests, and seed scripts.

---

## Tables Modified

| Table | Change | Existing rows backfilled |
|---|---|---|
| `them.tenants` | Created (new table) | — |
| `them.agents` | Added `tenant_id UUID NOT NULL` | 8 rows |
| `them.orchestrators` | Added `tenant_id UUID NOT NULL` | 2 rows |
| `them.access_tokens` | Added `tenant_id UUID NOT NULL` | 0 rows |
| `them.applications` | Added `tenant_id UUID NOT NULL` | 0 rows |
| `them.runs` | Added `tenant_id UUID NOT NULL` | 0 rows |
| `them.audit_logs` | Added `tenant_id UUID NOT NULL` | 0 rows |
| `them.app_orchestrators` | Added `tenant_id UUID NOT NULL` | 0 rows |
| `them.run_artifacts` | Created with `tenant_id UUID NOT NULL` | — |

**Platform-global tables unchanged (no tenant_id):**
`llm_providers`, `config`, `middleware_defs`, `run_steps`, `run_usage`, `tasks`,
`task_messages`, `artifacts`, `entry_points`, `middleware_wirings`

---

## Uniqueness Constraint Changes

| Old | New |
|---|---|
| `UNIQUE(agents.slug)` — global | `UNIQUE(tenant_id, slug)` — per-tenant |
| `UNIQUE(orchestrators.name)` — global | `UNIQUE(tenant_id, name)` — per-tenant |
| `UNIQUE(app_orchestrators.name)` — global | `UNIQUE(tenant_id, name)` — per-tenant |

---

## Validation Results (live DB)

All 9 validation checks passed:

```
1. Bootstrap tenant UUID   → PASS: correct UUID
2. NULL tenant_id checks:
   agents                  → PASS (0 nulls)
   orchestrators           → PASS (0 nulls)
   access_tokens           → PASS (0 nulls)
   applications            → PASS (0 nulls)
   runs                    → PASS (0 nulls)
   audit_logs              → PASS (0 nulls)
   app_orchestrators       → PASS (0 nulls)
   run_artifacts           → PASS (0 nulls)
3. Orphan FK checks         → PASS (all 0)
4. Cross-tenant same slug   → PASS: allowed
5. Intra-tenant dup slug    → PASS: rejected
6. run_artifacts tenant_id  → PASS: NOT NULL
7. Required indexes         → PASS: all 9 present
8. Old constraints removed  → PASS: none remain
9. Migration recorded       → PASS
```

---

## Test Results

| Suite | Before migration | After migration |
|---|---|---|
| `go test ./...` | 422 passed, 0 failed | 422 passed, 0 failed |
| Python sanity (01 02 03 04 15) | 55 passed | 55 passed |

Zero regressions.

---

## Container Health After Migration

All containers healthy (verified with `docker ps`):
- `them-bridge` — healthy (Python queries confirmed working)
- `them-go-bridge` — healthy
- `them-go-bridge-2` — healthy
- `them-postgres` — healthy
- `them-redis` — healthy
- `them-auth-service` — healthy
- `them-worker` — running

---

## Files Created

| File | Purpose |
|---|---|
| `db/026_tenant_foundation.sql` | Migration (idempotent, transactional) |
| `db/validate_r4a.sql` | Standalone validation script |
| `docs/architecture-v2/R4_TENANT_IMPLEMENTATION_PLAN.md` | Implementation plan |
| `docs/architecture-v2/R4A_IMPLEMENTATION_REPORT.md` | This report |

---

## Open Decisions Resolved

All O-01 through O-08 from TENANT_FOUNDATION_DECISIONS.md were resolved:
- O-01: Agents/orchestrators are tenant-owned (slug/name per-tenant) ✓
- O-02: access_tokens gets direct tenant_id column ✓
- O-03 through O-08: Deferred as planned (billing model, auth service, Redis isolation,
  Tenant Portal UI, Tier 1 queues, LLM provider overrides)

---

## Hard Constraints Carried Forward

- Bootstrap tenant UUID `00000000-0000-0000-0000-000000000001` is immutable
- `llm_providers`, `config`, `middleware_defs` have no tenant_id (platform-global, D-01/D-02/D-03)
- No Go or Python application code was changed in R-4a
- R-4b (Go auth middleware), R-4c (DAL queries), R-4d (session propagation), R-4e (run recorder) are NOT started

---

## What R-4a Does NOT Enforce at Runtime

R-4a adds the schema columns but **no runtime enforcement exists yet**:
- DAL queries do NOT yet filter `WHERE tenant_id = $n`
- Auth middleware does NOT yet resolve TenantID from JWT/token
- Cross-tenant access returns 200/data, not 403 (not enforced until R-4b/R-4c)

This is correct and expected. R-4a is the foundation layer only. Runtime enforcement is R-4b through R-4e.

---

## Next: Phase R-4b

Read `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §2 and
`TENANT_FOUNDATION_DECISIONS.md` §5 before starting R-4b.

R-4b scope: Go auth middleware resolves TenantID from JWT claim or bearer token lookup, and
propagates it through context. SessionInfo gains TenantID field.
