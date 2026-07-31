# Phase R-4a: Tenant Database Foundation — Implementation Plan
# Date: 2026-07-31
# Status: APPROVED — implement R-4a only (DB schema + bootstrap data + validation)

---

## Context

The-M is adding multi-tenant (Tier 0 logical isolation) support. This document covers **R-4a
only**: the PostgreSQL schema foundation. R-4b through R-4e (Go auth middleware, DAL queries,
session propagation, run recorder) are separate sessions.

**Mandate from TENANT_FOUNDATION_DECISIONS.md D-14:**
> The tenant migration will be a dedicated wave that adds `tenant_id` columns and backfills.
> It will not be mixed with any other migration wave.

---

## Current DB State (inspected 2026-07-31)

- No `them.tenants` table exists
- No `tenant_id` column on any table
- 29 migrations applied (latest: `025_events_transport`)
- Run artifacts table (`025_run_artifacts`) not yet applied to live DB

Row counts on tables receiving `tenant_id`:
```
agents:         8 rows  (need backfill)
orchestrators:  2 rows  (need backfill)
access_tokens:  0 rows
applications:   0 rows
runs:           0 rows
audit_logs:     0 rows
run_artifacts:  (table doesn't exist yet)
```

Existing uniqueness constraints that become tenant-scoped:
- `agents.slug` — UNIQUE globally → must become UNIQUE per tenant
- `orchestrators.name` — UNIQUE globally → must become UNIQUE per tenant
- `app_orchestrators.name` — UNIQUE globally → must become UNIQUE per (tenant, application)

Platform-global tables (NO tenant_id, unchanged):
- `llm_providers` — platform-global per D-01
- `config` — platform-global per D-02 / D-03
- `middleware_defs` — platform-global catalog
- `run_steps`, `run_usage`, `task_messages`, `tasks` — owned through run chain, no direct tenant_id needed
- `artifacts` — A2A task artifacts, owned through task chain

---

## Open Decisions Resolved

From TENANT_FOUNDATION_DECISIONS.md §9:

| # | Question | Resolution |
|---|---|---|
| O-01 | Agents/orchestrators: tenant-owned or platform-global? | **Tenant-owned** — slug/name uniqueness becomes per-tenant |
| O-02 | access_tokens: direct tenant_id or inferred? | **Direct tenant_id column** — simplest, enables bearer-token fast path |
| O-03 | Billing model drives run_usage tenant? | Runs carry tenant_id; run_usage inherits through run chain — no direct column needed now |
| O-04 | Auth service user multi-tenancy? | Deferred — auth_service tables not touched in R-4a |
| O-05 | Redis isolation strategy? | Tier 0 (row-level DB only) for R-4 — Redis key changes deferred |
| O-06 | Tenant Portal separate deployment? | Deferred — UI work out of scope for R-4a |
| O-07 | When required Tier 1? | Not required for R-4 |
| O-08 | LLM provider override approach? | Deferred — llm_providers stays platform-global |

---

## R-4a Migration Scope

### Migration files to create

| File | Purpose |
|---|---|
| `db/026_tenant_foundation.sql` | Core: tenants table, bootstrap tenant, tenant_id columns, backfill, constraints, indexes |

### Tables receiving `tenant_id UUID NOT NULL`

| Table | Notes |
|---|---|
| `them.agents` | 8 existing rows → backfill with bootstrap tenant |
| `them.orchestrators` | 2 existing rows → backfill |
| `them.access_tokens` | 0 rows → no backfill needed |
| `them.applications` | 0 rows → no backfill needed |
| `them.runs` | 0 rows → no backfill needed |
| `them.audit_logs` | 0 rows → no backfill needed |
| `them.run_artifacts` | Table created in same migration (025 not yet applied to live) |

### Bootstrap tenant

```sql
INSERT INTO them.tenants (id, slug, display_name, is_bootstrap)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  'default',
  'Default Development Tenant',
  true
);
```

UUID is deterministic (hardcoded) so the bootstrap tenant ID is known in Go/Python config
and can be referenced in tests without a DB lookup.

### Uniqueness constraint changes

| Old constraint | New constraint |
|---|---|
| `UNIQUE(agents.slug)` | `UNIQUE(tenant_id, agents.slug)` |
| `UNIQUE(orchestrators.name)` | `UNIQUE(tenant_id, orchestrators.name)` |
| `UNIQUE(app_orchestrators.name)` | `UNIQUE(tenant_id, app_orchestrators.name)` |

Note: `app_orchestrators.name` transitively scoped by `application_id` which carries
`tenant_id` — but adding direct tenant_id makes queries simpler.

### Foreign key from `app_orchestrators` to `tenants`

`app_orchestrators.tenant_id` → `them.tenants(id)` ON DELETE RESTRICT

---

## Migration Safety Approach

1. Create `them.tenants` table and insert bootstrap tenant
2. Add `tenant_id` columns as NULLABLE first (allows backfill without locking)
3. Backfill all existing rows with bootstrap tenant UUID
4. Validate: confirm no NULL values remain on affected tables
5. Add NOT NULL constraint (fast metadata change after full backfill)
6. Drop old global uniqueness constraints
7. Add tenant-scoped uniqueness constraints
8. Add foreign key constraints + indexes
9. Create `them.run_artifacts` table with `tenant_id` column included from the start
10. Record migration in `them.schema_migrations`

---

## Validation Script

A separate `db/validate_r4a.sql` script will verify:
- No NULL tenant_id on any tenant-owned table with rows
- No orphan records (agents/orchestrators pointing to non-existent tenant)
- Duplicate slug within same tenant rejected
- Same slug allowed across different tenants
- Bootstrap tenant exists and is the only tenant

---

## Idempotency

The migration uses `IF NOT EXISTS`, `IF EXISTS`, `ON CONFLICT DO NOTHING`, and conditional
column additions to be safely re-runnable. A re-run will detect existing state and skip.

---

## What R-4a Does NOT Do

- No Go code changes
- No Python code changes
- No auth middleware changes
- No DAL query changes
- No Redis key changes
- No session struct changes
- No run recorder changes

These are R-4b through R-4e, to be planned and implemented in subsequent sessions.

---

## Test Plan

1. Run migration against live VPS DB
2. Run `db/validate_r4a.sql` validation queries
3. Run Go test suite: `go test ./...` — must pass (422 tests, no new regressions)
4. Run Python sanity suite: `01 02 03 04 15` — must pass
5. Verify bridge and worker restart cleanly against migrated schema
6. Verify cross-tenant uniqueness: same slug in two tenants — both allowed
7. Verify intra-tenant uniqueness: same slug in same tenant — rejected

---

## Hard Constraints

- Bootstrap tenant UUID is `00000000-0000-0000-0000-000000000001` (deterministic, never changes)
- `llm_providers`, `config`, `middleware_defs` are NOT touched
- No Go or Python application code changes in R-4a
- Migration must be idempotent (safe to re-run)
- Never commit secrets or .env
