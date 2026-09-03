# RLS Option A — Full Row-Level Security Design Plan
# the-M multi-agent orchestration platform
# Author: Avi Cohen / chaya.friedman@shift4.com
# Created: 2026-09-03
# Status: APPROVED FOR IMPLEMENTATION (design only — no code in this file)

---

## Executive Summary

This document is the complete implementation blueprint for enabling Postgres Row-Level Security
on all tenant-scoped tables in the `them` database. It is the authoritative source of truth for
Step 19 of the multi-tenancy roadmap. No implementation code exists yet; implementation begins
only after this document is reviewed and implementation is explicitly authorised.

**Core recommendation:** Create a dedicated `them_app` role without `BYPASSRLS`. All runtime
queries run as `them_app`. Every tenant-scoped DB operation is wrapped in an explicit transaction
that begins with `SELECT set_config('app.tenant_id', $1, true)`. RLS policies on each
tenant-scoped table enforce `tenant_id = current_setting('app.tenant_id')::uuid`. Platform/admin
paths use a separate `them_admin` role that bypasses RLS.

---

## 1. Transaction Architecture

### 1.1 Design choice: explicit `BeginTenantTx` API

Two approaches were evaluated:

| Approach | Description | Decision |
|---|---|---|
| **Callback-based** `WithinTenantTx(ctx, tenantID, func(tx Tx) error)` | Caller passes closure; begin/set/rollback is automatic | Rejected: hides the transaction boundary, makes testing harder, fights Go idiomatic style |
| **Explicit** `BeginTenantTx(ctx, tenantID) (TenantTx, error)` + `Commit`/`Rollback` | Caller owns lifecycle | **Chosen** |

### 1.2 New `TenantTx` interface

```go
// TenantTx is a live DB transaction with app.tenant_id already set via
// SELECT set_config('app.tenant_id', tenantID, true).
// The caller MUST call Commit or Rollback exactly once.
type TenantTx interface {
    dal.Querier          // Query / QueryRow / Exec / ExecReturning
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}
```

The concrete type wraps `pgx.Tx` and implements `dal.Querier` so all existing DAL functions
that accept a `Querier` work without change — they just receive a `TenantTx` instead of the
pool-backed `*dal.DB`.

### 1.3 Transaction lifecycle — exact rules

**Begin:**
1. `pool.Begin(ctx)` → obtains a `pgx.Tx` (a real DB connection, not a pgx PreparedStatement).
2. Immediately execute `SELECT set_config('app.tenant_id', $1, true)` with the tenant UUID.
   - Parameterised query — never string interpolation.
   - `true` = transaction-scoped; resets to empty on COMMIT or ROLLBACK.
3. If the `set_config` call fails, call `tx.Rollback` and return the error. The connection is
   returned clean to the pool.

**Commit:**
- Call `pgx.Tx.Commit`. The `app.tenant_id` GUC reverts to empty automatically.

**Rollback:**
- Call `pgx.Tx.Rollback` (idempotent in pgx — safe to call after Commit or after a prior error).
- On panic: caller must `defer tx.Rollback(ctx)` before any fallible operation. Rollback on a
  committed transaction is a pgx no-op (returns `pgx.ErrTxClosed`), so `defer Rollback` is
  always safe.

**Context cancellation:**
- pgx propagates context cancellation to the in-flight query. If the context is cancelled after
  Begin but before Commit, the next query (or Commit itself) returns a cancellation error.
- The deferred `Rollback` still fires and succeeds because pgx rolls back the connection
  server-side on error.
- No special handling needed: standard Go `defer tx.Rollback(ctx)` pattern.

**Connection reuse safety:**
- `SET LOCAL` / `set_config(..., true)` scopes the GUC to the current transaction.
- When the transaction ends (Commit or Rollback), PG resets `app.tenant_id` to the session
  default (empty string, since it is never set at the session level).
- A pooled connection that is reused for a new transaction always starts with `app.tenant_id = ''`.
- This is the core safety guarantee: no cross-tenant leakage through connection reuse.

### 1.4 Non-transactional admin/platform queries

Some queries are intentionally cross-tenant (admin CRUD for tenants, platform-global stats,
background jobs). These run through a separate `them_admin` Querier backed by a pool that
connects as the `them_admin` role (which holds `BYPASSRLS`). Details in Section 4.

---

## 2. Complete Database Access Inventory

### 2.1 `go/internal/admin/dal/` — all functions

All DAL functions currently accept a `dal.Querier`. Once RLS is live, callers pass a `TenantTx`
for tenant-scoped operations, and the `them_admin`-backed `Querier` for admin/platform operations.

**Legend:**
- `T` = tenant-scoped (must run inside `BeginTenantTx`)
- `X` = cross-tenant admin (must run via `them_admin` Querier)
- `A` = application-scoped (safety via FK → applications → tenant_id; still wrap in TenantTx so RLS passes)
- `P` = platform-global row (tenant_id IS NULL rows; must run via `them_admin` Querier)
- `⚠` = atomicity bug to fix in the same PR

| File | Function | Scope | Notes |
|---|---|---|---|
| `agents.go` | `ListAgents` | T | |
| `agents.go` | `GetAgent` | T | |
| `agents.go` | `CreateAgent` | T | |
| `agents.go` | `UpdateAgent` | T | |
| `agents.go` | `DeleteAgent` | T ⚠ | Second DELETE on `component_definitions` uses agent_id only, no tenant scope — add `AND tenant_id = $N` |
| `agents.go` | `AgentExists` | X | Intentional: cross-tenant uniqueness check |
| `agents.go` | `GetAgentBySlug` | X | Intentional: slug is globally unique |
| `agents.go` | `UpdateAgentScanResult` | X | Called by security scanner; no tenant context available |
| `agents.go` | `GetAgentByID` | X | Used by runtime path; tenantID asserted by caller |
| `agents.go` | `GetAgentTokenEncrypted` | X | Used by token introspection; cross-tenant by design |
| `orchestrators.go` | `ListOrchestrators` | T | |
| `orchestrators.go` | `GetOrchestrator` | T | |
| `orchestrators.go` | `CreateOrchestrator` | T | |
| `orchestrators.go` | `UpdateOrchestrator` | T | |
| `orchestrators.go` | `DeleteOrchestrator` | T | |
| `applications.go` | `ListApplications` | T | |
| `applications.go` | `GetApplication` | T | |
| `applications.go` | `CreateApplication` | T | |
| `applications.go` | `UpdateApplication` | T | |
| `applications.go` | `DeleteApplication` | T | |
| `applications.go` | `BulkDeleteApplications` | T | |
| `applications.go` | `UpdateRuntimeConfig` | T | |
| `applications.go` | `GetProviderKeys` | T | |
| `applications.go` | `SetProviderKey` | T | |
| `applications.go` | `DeleteProviderKey` | T | |
| `applications.go` | `GetAppParams` | T | |
| `applications.go` | `SetAppParam` | T | |
| `applications.go` | `DeleteAppParam` | T | |
| `applications.go` | `ListEntryPoints` | A | Scoped by applicationID; wrap in TenantTx |
| `applications.go` | `listAppOrchSummaries` | A | |
| `applications.go` | `SetOrchestratorLLM` | A | |
| `applications.go` | `SetOrchestratorVoice` | A | |
| `applications.go` | `SetOrchestratorMCPServers` | A | |
| `applications.go` | `CreateEntryPoint` | A | |
| `applications.go` | `UpdateEntryPoint` | A | |
| `applications.go` | `DeleteEntryPoint` | A | |
| `applications.go` | `GetAgentSummariesByIDs` | X | Intentional: cross-tenant for agent card synthesis |
| `runs.go` | `ListRuns` | T | |
| `runs.go` | `GetRunDetail` | T | Run scoped; child rows (run_steps, run_usage) scoped by run_id (safe via FK) |
| `runs.go` | `GetRunTasks` | T | EXISTS subquery checks run tenant |
| `runs.go` | `GetContextMessages` | T | Uses `tasks.tenant_id` — requires backfill before RLS on tasks |
| `runs.go` | `TailRunSteps` | T | |
| `runs.go` | `TailRunUsage` | T | |
| `tokens.go` | `ListTokens` | T | |
| `tokens.go` | `GetToken` | T | |
| `tokens.go` | `CreateToken` | T | |
| `tokens.go` | `UpdateToken` | T | |
| `tokens.go` | `DeleteToken` | T | |
| `tokens.go` | `LookupToken` | T | |
| `tenants.go` | `ListTenants` | X | Admin-only |
| `tenants.go` | `GetTenant` | X | Admin-only |
| `tenants.go` | `CreateTenant` | X | Admin-only |
| `tenants.go` | `PatchTenant` | X | Admin-only |
| `tenants.go` | `GetTenantByEmailDomain` | X | Used at login; no tenant context yet |
| `tenants.go` | `GetQuota` | T | scoped by tenantID param |
| `tenants.go` | `UpsertQuota` | T | |
| `tenants.go` | `ListMembers` | T | |
| `tenants.go` | `AddMember` | T | |
| `tenants.go` | `ListGroupMappings` | T | |
| `tenants.go` | `UpsertGroupMapping` | T | |
| `tenants.go` | `DeleteGroupMapping` | T | |
| `llm_providers.go` | `ListProvidersForTenant` | T | |
| `llm_providers.go` | `GetProviderByNameForTenant` | T | |
| `llm_providers.go` | `UpsertTenantProvider` | T | |
| `llm_providers.go` | `ListPlatformProviders` | P | tenant_id IS NULL rows; needs `them_admin` |
| `llm_providers.go` | `GetPlatformProvider` | P | |
| `llm_providers.go` | `UpsertPlatformProvider` | P | |
| `managed_apps.go` | `ListManagedApps` | X | Platform-global |
| `managed_apps.go` | `CreateManagedApp` | X | |
| `managed_apps.go` | `GetManagedApp` | X | |
| `managed_apps.go` | `ListManagedAppParams` | X | |
| `managed_apps.go` | `UpsertManagedAppParams` | X ⚠ | DELETE + INSERT loop without tx — atomicity bug; fix in same PR |
| `managed_apps.go` | `ListBindingsForTenant` | T | |
| `managed_apps.go` | `GetBinding` | T | |
| `managed_apps.go` | `UpsertBinding` | T | |
| `mcp_servers.go` | `ListMCPServers` | T | |
| `mcp_servers.go` | `GetMCPServer` | T | |
| `mcp_servers.go` | `CreateMCPServer` | T | |
| `mcp_servers.go` | `UpdateMCPServer` | T | |
| `mcp_servers.go` | `DeleteMCPServer` | T | |
| `mcp_servers.go` | `ListAppMCPCredentials` | A | scoped by applicationID |
| `mcp_servers.go` | `GetAppMCPCredential` | A | |
| `mcp_servers.go` | `UpsertAppMCPCredential` | A | |
| `mcp_servers.go` | `DeleteAppMCPCredential` | A | |
| `services_stats.go` | `GetSecurityScanStats` | X | Intentional: platform-wide aggregate; admin-only |
| `agent_definitions.go` | `ListDefinitionsForAgent` | T | |
| `agent_definitions.go` | `GetDefinition` | T | |
| `agent_definitions.go` | `CreateDefinition` | T | |
| `agent_definitions.go` | `UpdateDefinition` | T | |
| `agent_definitions.go` | `DeleteDefinition` | T | |
| `config.go` | `GetConfig` | X | Platform-global config table; no tenant_id column |
| `config.go` | `UpsertConfig` | X | |
| `agent_bindings.go` | `GetAgentParamsForBinding` | X | Platform-global spec lookup |
| `agent_bindings.go` | `GetRequiredParamsForAgent` | X | Platform-global spec lookup |
| `agent_bindings.go` | `(other binding fns)` | A | scoped by applicationID |
| `publish.go` | `PublishDefinition` | T ⚠ | Two sequential UPDATEs commented "may be a transaction" — must be atomically wrapped |
| `publish.go` | `UpsertAppOrchestrator` | A | |
| `publish.go` | `UpsertEntryPoint` | A | |
| `publish.go` | `DeactivateStaleOrchestrators` | T | |
| `publish.go` | `DeactivateStaleEntryPoints` | T | |

### 2.2 `go/internal/runrecorder/`

`recorder.go` writes to `them.runs`, `them.run_steps`, `them.run_usage`. Already passes
`tenant_id` in INSERT. Will need to be wrapped in `TenantTx` so RLS passes for INSERT.
`ErrMissingTenantID` guard already exists — preserve it.

### 2.3 `go/internal/authserver/` — uses `pool` directly

Both `pgx.go` and `oidc_store.go` hold a raw `*pgxpool.Pool`. They do **not** use `dal.Querier`.

- `pgx.go` queries: `auth_service.*` tables only (users, roles, tenant_memberships, user_sessions,
  blacklisted_tokens). These tables are **not** in `them` schema and are **not** subject to
  `them`-schema RLS. No change needed for RLS isolation.
- `oidc_store.go`:
  - `GetTenantIDPConfig` — reads `them.tenants` (cross-tenant; needs `them_admin` pool or
    explicit BYPASSRLS exception; this is a pre-auth lookup).
  - `GetGroupRole` — reads `them.tenant_group_mappings` (cross-tenant; same as above).
  - `UpsertOIDCUser` — writes `auth_service.*` only; no `them`-schema RLS impact. However,
    it runs 4+ sequential queries without a transaction (**existing race condition**). Fix in a
    separate PR before Step 19 implementation or as part of it.

**Decision:** authserver uses a dedicated `them_admin`-backed pool for `them.*` reads, and
continues using the main pool for `auth_service.*` reads. The authserver never needs to
enforce `app.tenant_id` because it runs before tenant context is established.

### 2.4 `go/internal/agentregistry/pgx_querier.go`

Uses `pool.Query`/`pool.QueryRow` directly. `GetBindingID` has no tenant scope (cross-tenant).
`QueryAgentsByTenant` is tenant-scoped by explicit WHERE clause. These are read-only, runtime-path
queries. Wrap `QueryAgentsByTenant` in TenantTx; `GetBindingID` routes through `them_admin` pool.

### 2.5 `go/internal/middleware/` (job queue)

`pgx.go` adapts pool to `Querier`. `job.go` functions:
- `EnqueueWithQuarantine` — scoped by applicationID (no direct tenant_id in query).
- `Claim` — cross-tenant; picks next unclaimed job from all tenants. Uses `them_admin` pool.
- `LoadSecurityConfig` — reads `them.applications` by applicationID; needs tenant context.
- `Complete` + `completeQuarantinePath` / `completeLegacyPath` — write `them.run_artifacts`,
  `them.quarantine_artifacts`. Need tenant context.

### 2.6 `go/internal/temporal/workerconfig/loader.go`

Uses `pool` directly. All queries pass explicit `applicationID` and `tenantID` params in WHERE
clauses. Once RLS is live, these queries must run inside `BeginTenantTx` for tenant-scoped tables.
Platform-global lookups (ManagedAppParams, LLM provider platform defaults) use `them_admin` pool.

### 2.7 `go/cmd/agent-runtime/` (spec.go, llm.go)

Uses `pool` directly. All queries include explicit `tenant_id = $N::uuid` predicates in WHERE.
Already correctly tenant-scoped at the query level. After RLS, must also run inside `TenantTx`
so the RLS policy passes. The `tenant_id` value comes from the JWT-derived tenantctx.

### 2.8 `go/cmd/dag-worker/main.go`

Uses `pool` directly. Queries join `them.applications` and filter by `applicationID`.
Must be migrated to use `TenantTx` where tenant-scoped or `them_admin` pool for cross-tenant.

### 2.9 No raw SQL found in these locations

- `go/internal/orchestrator/orchestrator.go` — no direct pool calls; delegates to runrecorder.
- `go/internal/quota/enforcer.go` — Redis only; no DB.
- `go/internal/gate/gate.go` — Redis only; no DB.
- `go/internal/session/session.go` — Redis only; no DB.
- `go/internal/llm/` — HTTP only; no DB.
- `go/internal/ws/`, `go/internal/sse/`, `go/internal/a2a/` — delegate to other packages.
- `go/cmd/them/main.go` — wiring only; no SQL.

---

## 3. Tenant Context Setup

### 3.1 The `set_config` call

```sql
SELECT set_config('app.tenant_id', $1, true)
```

- `$1` is the tenant UUID as a string (validated before this call).
- Third argument `true` = transaction-local; the GUC resets to its session default on
  COMMIT or ROLLBACK.
- Session default is the empty string `''` (never set at session level).
- This is a parameterised query — `$1` is bound by the pgx driver, never interpolated into SQL.
  String injection into a GUC value is therefore impossible.

### 3.2 Fail-closed guarantee

When `app.tenant_id` is empty:
```sql
current_setting('app.tenant_id', true)::uuid
```
`current_setting` with `missing_ok = true` returns `NULL` when the GUC is not set.
`NULL::uuid` never equals any `tenant_id` UUID → no rows returned.

This is the fail-closed default: a query that forgot to set the tenant context returns empty
results rather than all rows. Confirm during implementation that `missing_ok = true` is used
in every RLS policy expression (see Section 5).

### 3.3 Source of tenant context

TenantID is extracted only from JWT claims via the `tenantctx` typed context key. It is never
read from request headers, query params, or any other source. This constraint predates Step 19
and is already enforced by the codebase.

### 3.4 Connection reuse safety proof

1. Connection checked out from pool.
2. `BEGIN` issued by pgx.
3. `SELECT set_config('app.tenant_id', $1, true)` — GUC is now tenant-A.
4. All tenant-A queries execute; RLS filters to tenant-A rows.
5. `COMMIT` (or `ROLLBACK`).
6. PG server resets GUC to session default (empty string) automatically.
7. Connection returned to pool.
8. Next checkout: GUC is `''` — RLS returns zero rows until a new `set_config` call.

There is no scenario where a pooled connection carries a stale `tenant_id` from a previous
request because `set_config(..., true)` is strictly transaction-scoped.

---

## 4. Database Roles and Ownership

### 4.1 Current state

```
them | rolbypassrls = t | rolsuper = f
```

The `them` role both owns the tables and is the runtime application role. BYPASSRLS means all
current app queries skip RLS even if policies exist.

### 4.2 Target state after Step 19

| Role | Purpose | BYPASSRLS | Connection string |
|---|---|---|---|
| `them_owner` | Owns tables, runs migrations | Yes (needs it) | Migration tool only; never used at runtime |
| `them_admin` | Platform/admin runtime queries | Yes | `them-go-bridge` admin paths; authserver `them.*` reads |
| `them_app` | Tenant-scoped runtime queries | No | `them-go-bridge` tenant paths; agent-runtime; dag-worker |

### 4.3 Migration path for the `them` role

The existing `them` role will be renamed to `them_owner` (or a new `them_owner` role created
and table ownership transferred). The application DSN is split into:
- `THEM_DB_URL_APP` — connects as `them_app` (no BYPASSRLS).
- `THEM_DB_URL_ADMIN` — connects as `them_admin` (BYPASSRLS).

Both DSNs are derived from `secrets.local` via `generate-env.sh`. They are never committed.

### 4.4 `FORCE ROW LEVEL SECURITY`

Even if `them_owner` (who owns the tables) is somehow used at runtime, RLS should still fire:
```sql
ALTER TABLE them.agents FORCE ROW LEVEL SECURITY;
```
`FORCE ROW LEVEL SECURITY` makes RLS apply even to the table owner. Apply to all tenant-scoped
tables listed in Section 5.

### 4.5 Grants (minimal)

```sql
-- them_app: DML on tenant-scoped tables only
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agents TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.orchestrators TO them_app;
-- ... (all tenant-scoped tables listed in Section 5)

-- them_admin: all tables in both schemas
GRANT ALL ON ALL TABLES IN SCHEMA them TO them_admin;
GRANT ALL ON ALL TABLES IN SCHEMA auth_service TO them_admin;

-- them_app must NOT be granted on config, tenants (admin-only)
-- them_app read-only access to reference tables (llm_providers for platform defaults):
GRANT SELECT ON them.llm_providers TO them_app;
GRANT SELECT ON them.tenants TO them_app;  -- needed for membership lookups
```

### 4.6 Connection pool split in Go

`go/internal/db/db.go` currently creates one pool. After Step 19 it will create two:

```go
type Pools struct {
    App   *pgxpool.Pool  // them_app role — used for all tenant-scoped operations
    Admin *pgxpool.Pool  // them_admin role — used for admin/platform/cross-tenant ops
}
```

`BeginTenantTx` always uses `Pools.App`.
Admin DAL functions receive `Pools.Admin` as their Querier.

---

## 5. RLS Policies

### 5.1 Table classification

| Table | Scope | RLS | Policy type |
|---|---|---|---|
| `them.agents` | tenant | Yes | USING + WITH CHECK |
| `them.orchestrators` | tenant | Yes | USING + WITH CHECK |
| `them.applications` | tenant | Yes | USING + WITH CHECK |
| `them.entry_points` | tenant (via app FK) | Yes | USING via JOIN on applications |
| `them.access_tokens` | tenant | Yes | USING + WITH CHECK |
| `them.runs` | tenant | Yes | USING + WITH CHECK |
| `them.run_steps` | scoped by run_id FK | No (protected by run RLS) | — |
| `them.run_usage` | scoped by run_id FK | No (protected by run RLS) | — |
| `them.run_artifacts` | scoped by run_id FK | No (protected by run RLS) | — |
| `them.tasks` | tenant (nullable — backfill first) | Yes — **after backfill** | USING + WITH CHECK |
| `them.task_messages` | scoped by task_id FK | No (protected by tasks RLS) | — |
| `them.artifacts` | scoped by run_id FK | No (protected by run RLS) | — |
| `them.tenant_group_mappings` | tenant | Yes | USING + WITH CHECK |
| `them.mcp_servers` | tenant | Yes | USING + WITH CHECK |
| `them.llm_providers` | mixed (tenant OR NULL) | Partial | USING (tenant_id = ... OR tenant_id IS NULL for reads) — see 5.3 |
| `them.managed_app_bindings` | tenant | Yes | USING + WITH CHECK |
| `them.quarantine_artifacts` | scoped by run_id FK | No | — |
| `them.middleware_jobs` | scoped by application_id | No direct RLS | Protected via application FK |
| `them.tenants` | admin-only | No RLS (them_app has SELECT, policies unnecessary) | — |
| `them.config` | platform-global | No RLS | them_app has no grants on config |
| `them.managed_apps` | platform-global | No RLS | them_app has no grants |
| `auth_service.*` | no tenant_id | No RLS | Separate schema; them_app has no grants |

### 5.2 Standard policy template

```sql
ALTER TABLE them.<table> ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.<table> FORCE ROW LEVEL SECURITY;

CREATE POLICY <table>_tenant_isolation ON them.<table>
    USING      (tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
```

The `true` (missing_ok) argument to `current_setting` means a missing GUC returns `NULL`,
which never matches any tenant_id — fail-closed.

### 5.3 Special case: `them.llm_providers` (platform + tenant rows)

`llm_providers` has rows where `tenant_id IS NULL` (platform defaults) and rows where
`tenant_id = <uuid>` (tenant overrides). The `them_app` role needs to read both:

```sql
-- Read policy: tenant's own rows OR platform defaults
CREATE POLICY llm_providers_read ON them.llm_providers
    FOR SELECT
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid
           OR tenant_id IS NULL);

-- Write policy: tenant may only write their own rows
CREATE POLICY llm_providers_write ON them.llm_providers
    FOR INSERT WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE POLICY llm_providers_update ON them.llm_providers
    FOR UPDATE USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
               WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE POLICY llm_providers_delete ON them.llm_providers
    FOR DELETE USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
```

Platform default rows (tenant_id IS NULL) are managed only by `them_admin`.

### 5.4 Special case: `them.entry_points` (no direct tenant_id column)

`entry_points` has `application_id` but no `tenant_id` column. Enforce via subquery:

```sql
CREATE POLICY entry_points_tenant_isolation ON them.entry_points
    USING (application_id IN (
        SELECT id FROM them.applications
        WHERE tenant_id = current_setting('app.tenant_id', true)::uuid
    ))
    WITH CHECK (application_id IN (
        SELECT id FROM them.applications
        WHERE tenant_id = current_setting('app.tenant_id', true)::uuid
    ));
```

Index `them.applications(tenant_id)` (already exists from Step 3) ensures this subquery is fast.

### 5.5 Behavior on missing/invalid tenant context

| Scenario | Result |
|---|---|
| `app.tenant_id` not set (GUC is empty string) | `current_setting` returns `NULL` with `missing_ok=true`; `NULL::uuid` never matches; zero rows returned for SELECT; WITH CHECK rejects INSERT/UPDATE |
| `app.tenant_id` set to invalid UUID string | `::uuid` cast fails; query errors with `invalid input syntax for type uuid` |
| `app.tenant_id` set to valid UUID that matches no rows | Zero rows returned (correct behavior) |
| Running as `them_admin` (BYPASSRLS) | RLS not applied; all rows visible |

---

## 6. Test Plan

### 6.1 Unit tests (no live DB)

**Location:** `go/internal/db/` and `go/internal/admin/dal/` test files.

- Test that `BeginTenantTx` returns an error when `set_config` fails (mock `pool.Begin`).
- Test that `TenantTx.Rollback` is a no-op after `Commit` (ensures deferred Rollback is safe).
- Test that `TenantTx` implements `dal.Querier` interface (compile-time, no live DB needed).
- Test that `NewAdminQuerier` returns a Querier backed by the admin pool (mock pool).

### 6.2 Integration tests — real Postgres required (build tag `integration`)

**Location:** new file `go/internal/db/rls_integration_test.go`.

**Prerequisites:** The test setup must:
1. Create `them_app` and `them_admin` roles in the test DB.
2. Create a minimal `them.agents` table with RLS enabled and the standard policy.
3. Insert two rows: one for tenant-A, one for tenant-B.

**Test cases:**

| Test | Description | Expected result |
|---|---|---|
| `TestRLS_TenantA_SeesOwnRows` | Begin TenantTx as tenant-A; SELECT agents | Returns only tenant-A's row |
| `TestRLS_TenantB_SeesOwnRows` | Begin TenantTx as tenant-B; SELECT agents | Returns only tenant-B's row |
| `TestRLS_CrossTenantIsolation` | Begin TenantTx as tenant-A; attempt to SELECT tenant-B row by ID | Returns 0 rows (not error) |
| `TestRLS_CrossTenantInsertBlocked` | Begin TenantTx as tenant-A; INSERT row with tenant_id=tenant-B | WITH CHECK fails; error returned |
| `TestRLS_MissingContext_FailClosed` | Execute SELECT without beginning a TenantTx (GUC not set) | Returns 0 rows |
| `TestRLS_AdminBypasses` | Execute SELECT as them_admin with no GUC set | Returns all rows |
| `TestRLS_ConnectionReuse` | Acquire same pool connection twice; verify tenant isolation resets between transactions | No cross-tenant leakage |
| `TestRLS_RollbackClearsContext` | Begin TenantTx as tenant-A; Rollback; immediately begin TenantTx as tenant-B on same connection; SELECT | Returns only tenant-B rows |
| `TestRLS_PanicRollback` | Begin TenantTx; panic (recovered); deferred Rollback; new TenantTx on same connection | New transaction isolated correctly |
| `TestRLS_ContextCancellation` | Begin TenantTx; cancel context mid-query; deferred Rollback | No panic, connection returned clean |
| `TestRLS_EntryPoints_ViaSubquery` | entry_points isolation test with subquery-based policy | Only tenant's EPs visible |
| `TestRLS_LLMProviders_PlatformRowsVisible` | SELECT llm_providers as them_app with tenant-A context | Tenant-A rows AND platform (NULL) rows visible |
| `TestRLS_LLMProviders_NoCrossTenantWrite` | INSERT llm_provider with tenant_id=NULL as them_app | Blocked by WITH CHECK |
| `TestRLS_Tasks_AfterBackfill` | Verify tasks RLS after NULL rows are backfilled | Correct isolation |
| `TestRLS_UpsertManagedAppParams_Atomic` | UpsertManagedAppParams wrapped in tx; simulate failure mid-loop | No partial state (rollback verified) |
| `TestRLS_PublishDefinition_Atomic` | PublishDefinition wrapped in tx; simulate second UPDATE failure | No partial state (first UPDATE rolled back) |

### 6.3 Two-tenant isolation proof test

A single integration test `TestRLS_TwoTenantFullIsolation` that:
1. Creates two tenants (A and B) with separate applications, agents, runs, orchestrators.
2. Reads every tenant-scoped table as tenant-A; asserts zero tenant-B rows in any result.
3. Reads every tenant-scoped table as tenant-B; asserts zero tenant-A rows in any result.
4. Attempts cross-tenant writes (INSERT with wrong tenant_id); asserts all are blocked.

This test serves as the regression gate for the full RLS implementation.

---

## 7. PgBouncer Assessment

**Current state:** No PgBouncer in the stack. `them-go-bridge` connects directly to
`them-postgres` via pgxpool.

### 7.1 Compatibility with `SET LOCAL` / `set_config(..., true)`

`SET LOCAL` (and its equivalent `set_config(..., true)`) scopes the GUC to the current
transaction. It resets automatically on COMMIT or ROLLBACK. This means:

| Connection pooling mode | Compatible with SET LOCAL? | Notes |
|---|---|---|
| No pooler (current state) | ✅ Yes | Transactions are connection-scoped; reset is guaranteed |
| PgBouncer **transaction mode** | ✅ Yes | Each transaction gets its own server connection; GUC resets between transactions |
| PgBouncer **session mode** | ✅ Yes | But you lose the main benefit of pgbouncer (connection reuse) |
| PgBouncer **statement mode** | ❌ No | Transactions are not supported; SET LOCAL is meaningless |

**Conclusion:** If PgBouncer is added in the future, it must be configured in **transaction mode**
(the default for most PgBouncer deployments). Statement mode is incompatible. The current
direct-connection setup is fully compatible.

### 7.2 pgx prepared statements

PgBouncer in transaction mode does **not** support protocol-level prepared statements (they are
session-scoped in Postgres). pgx/v5 uses the extended query protocol with prepared statements
by default. If PgBouncer is added:
- Set `prefer_simple_protocol: true` in pgxpool config, OR
- Use pgBouncer's `server_reset_query = DISCARD ALL` to clean up prepared statements.

The `set_config` call itself uses a simple parameterised query; it does not use prepared
statements by name. No issue with the RLS implementation itself.

### 7.3 Advisory locks

`pg_advisory_lock` is session-scoped. It is incompatible with PgBouncer transaction mode.
Currently no advisory locks are used in the codebase. If added in the future, they must be
`pg_advisory_xact_lock` (transaction-scoped) to be pgBouncer-compatible.

### 7.4 LISTEN / NOTIFY

LISTEN is session-scoped and incompatible with PgBouncer transaction mode. Currently not used
in the main application pool. The token revocation channel uses Redis pub/sub, not Postgres
LISTEN/NOTIFY. No issue.

### 7.5 PgBouncer recommendation

PgBouncer is orthogonal to Step 19 (RLS). Adding it is a separate operational decision. The
RLS design is compatible with a future PgBouncer addition provided transaction mode is used.
Do not block Step 19 on a PgBouncer decision.

---

## 8. Execution Plan and Rollback

### 8.1 Pre-conditions (must be true before any schema change)

- [ ] `tasks.tenant_id` backfill complete: zero NULL rows in `them.tasks`
- [ ] `UpsertOIDCUser` race condition fixed (4 queries wrapped in a single transaction)
- [ ] `UpsertManagedAppParams` atomicity bug fixed (DELETE+INSERT in a transaction)
- [ ] `PublishDefinition` atomicity bug fixed (two UPDATEs in a single transaction)
- [ ] All existing `go test ./...` passing at HEAD
- [ ] `them_app` and `them_admin` roles exist in the DB with correct grants

### 8.2 Execution order (table-by-table, one PR per group)

The safe execution order is to enable RLS on tables with the lowest blast radius first,
verify isolation, then proceed to higher-impact tables.

**Phase A — Infrastructure (no table changes yet)**
1. Create `them_app` role (no BYPASSRLS).
2. Create `them_admin` role (BYPASSRLS).
3. Grant minimal permissions to each role (Section 4.5).
4. Add `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` to `.env` / `generate-env.sh`.
5. Add `Pools` struct to `go/internal/db/db.go`; wire both pools in `cmd/them/main.go`.
6. Add `BeginTenantTx` to `go/internal/db/db.go` (uses `Pools.App`).
7. Add `NewAdminQuerier` helper (uses `Pools.Admin`).
8. Verify all existing tests still pass. Deploy and smoke-test.

**Phase B — Enable RLS on low-risk tables**
Enable RLS on `them.mcp_servers`, `them.access_tokens`, `them.tenant_group_mappings`.
These are accessed by a small number of DAL functions with clear tenant scope.
Update the corresponding DAL functions to use `TenantTx`. Test. Deploy.

**Phase C — Enable RLS on core tables**
Enable RLS on `them.agents`, `them.orchestrators`, `them.applications`.
These are the highest-volume admin CRUD tables. Update all corresponding DAL callers.
Run `TestRLS_TwoTenantFullIsolation`. Deploy.

**Phase D — Enable RLS on run/task tables (after tasks backfill)**
Enable RLS on `them.runs`. Enable RLS on `them.tasks` (only after NULL rows backfilled).
`run_steps`, `run_usage`, `run_artifacts`, `task_messages` are protected via FK to runs/tasks —
no separate RLS policy needed.

**Phase E — Enable RLS on llm_providers**
Apply the split read/write policy (Section 5.3). Update LLM provider DAL functions.
Verify platform defaults are still readable by `them_app`.

**Phase F — Migrate non-DAL pool users**
Migrate `agent-runtime`, `dag-worker`, `temporal/workerconfig`, `middleware/job` to use the
appropriate pool (`App` or `Admin`) and wrap tenant-scoped queries in `TenantTx`.

**Phase G — Migrate authserver**
Migrate `oidc_store.go` `them.*` reads to use the `them_admin` pool. No RLS change needed;
this is a connection string change only.

### 8.3 Verification steps at each phase

After each phase:
1. Run `go test ./...` — zero failures.
2. Run `go test -tags=integration ./...` — zero failures.
3. Run `TestRLS_TwoTenantFullIsolation` against the live test DB.
4. Check `docker logs them-go-bridge` for unexpected 500s after deploy.
5. Run the E2E test suite (scripts/tests/run_tests.py 14) with a valid JWT.

### 8.4 Rollback plan

Rollback does NOT restore `BYPASSRLS` to the `them` role. Restoring BYPASSRLS would
silently re-disable isolation enforcement without any visible signal. Instead:

**Per-table rollback** (if a specific phase fails):
```sql
-- Disable RLS on the failing table only:
ALTER TABLE them.<table> DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS <table>_tenant_isolation ON them.<table>;
```
The application continues running with `them_app` (no BYPASSRLS) on the non-RLS tables,
and the queries use their existing WHERE clauses for isolation (as they do today).

**Full rollback** (if Phase A infrastructure must be reverted):
1. Revert DSN split — point both `App` and `Admin` pools at the same `them_owner` connection.
2. Drop `them_app` and `them_admin` roles.
3. Table ownership and grants remain with `them_owner` (which has BYPASSRLS if needed for
   migration work).

In no scenario should BYPASSRLS be re-added to the runtime application role.

### 8.5 Definition of done for Step 19

- [ ] All tables in Section 5.1 marked "Yes" have RLS enabled and FORCE ROW LEVEL SECURITY set
- [ ] `them_app` role is the runtime connection; `them_admin` is admin/platform
- [ ] All tests in Section 6 pass (unit + integration + two-tenant isolation proof)
- [ ] `TestRLS_TwoTenantFullIsolation` is a permanent member of the integration test suite
- [ ] `docs/SCHEMA.md` updated with RLS status for each table
- [ ] `docs/REDIS.md` unchanged (RLS is DB-only)
- [ ] `go/TEST_INDEX.md` updated with new integration tests
- [ ] `docs/CURRENT.md` updated with Step 19 status
- [ ] Zero new test failures in `go test ./...`

---

## Appendix A — Atomicity Bugs to Fix Before/During Step 19

### A.1 `UpsertManagedAppParams` (managed_apps.go)

**Current:** DELETE then INSERT in a loop, no transaction. A crash between delete and
insert leaves a window with no params.

**Fix:** Wrap the entire operation in a `BeginTx` / `Commit`. Since this is a platform-global
operation, use the `them_admin` Querier with a tx from `Pools.Admin`.

### A.2 `PublishDefinition` (publish.go)

**Current:** Two sequential UPDATEs with comment "may be a transaction." If the second UPDATE
fails, the first is committed — partial publish state.

**Fix:** Wrap both UPDATEs in an explicit transaction. This is a tenant-scoped operation — use
`BeginTenantTx`.

### A.3 `UpsertOIDCUser` (authserver/oidc_store.go)

**Current:** 4 sequential queries (role lookup × 3 + user upsert + tenant membership upsert)
without a transaction. Concurrent OIDC logins for the same email can create duplicate users or
inconsistent membership rows.

**Fix:** Wrap all queries in a single transaction. Since this touches only `auth_service.*`
tables, use a standard `pool.Begin()` (not TenantTx — no `app.tenant_id` needed here).

---

## Appendix B — Tables That Need `tasks.tenant_id` Backfill

**Current state:** `them.tasks.tenant_id` is nullable. There are 27 NULL rows from before
multi-tenancy was introduced. These rows belong to the bootstrap tenant
(`00000000-0000-0000-0000-000000000001`).

**Backfill SQL:**
```sql
UPDATE them.tasks
SET    tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE  tenant_id IS NULL;
```

**After backfill:**
```sql
ALTER TABLE them.tasks ALTER COLUMN tenant_id SET NOT NULL;
```

This migration must be numbered and applied before the `them.tasks` RLS policy is created.
Add it as a numbered migration in `db/` (e.g. `db/053_tasks_tenant_backfill.sql`).

---

*End of document.*
