# RLS Option A — Full Row-Level Security Design Plan
# the-M multi-agent orchestration platform
# Author: Avi Cohen / chaya.friedman@shift4.com
# Created: 2026-09-03
# Revised: 2026-09-03 (v2 — corrects child-table analysis, role design, policy expressions,
#           deployment ordering, type-level pool separation, and PgBouncer assessment)
# Status: DESIGN ONLY — no implementation code exists in this file

---

## Executive Summary

This document is the complete implementation blueprint for enabling Postgres Row-Level Security
on all tenant-scoped tables in the `them` database. It is the authoritative design reference
for Step 19 of the multi-tenancy roadmap.

**Core recommendation:** Create a dedicated `them_app` role without `BYPASSRLS`. All runtime
tenant-scoped queries run through that role, inside explicit transactions that begin with
`SELECT set_config('app.tenant_id', $1, true)`. A separate `them_admin` role retains
`BYPASSRLS` for admin and cross-tenant paths. A `them_owner` role owns tables and runs
migrations; it has `NOLOGIN` and is never used as an application DSN. RLS policies on each
tenant-scoped table enforce
`tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`.
Child tables that have no direct `tenant_id` column are protected by one of three strategies
chosen per table, not assumed to inherit protection through a foreign key.

---

## 1. Transaction Architecture

### 1.1 Design choice: explicit `BeginTenantTx` API

Two approaches were evaluated:

| Approach | Description | Decision |
|---|---|---|
| **Callback-based** `WithinTenantTx(ctx, tenantID, func(tx Tx) error) error` | Caller passes closure; begin / set / commit / rollback are automatic | Rejected: harder to test, fights Go idiomatic style, makes multi-step operations across service boundaries harder to express |
| **Explicit** `BeginTenantTx(ctx, tenantID) (TenantTx, error)` with `Commit` / `Rollback` | Caller owns lifecycle | **Chosen** |

The explicit API preserves `dal.Querier` as the single query-execution interface implemented by
both `pgxpool.Pool` (for non-transactional admin paths) and `pgx.Tx` (for transactional tenant
paths). No existing DAL function signatures change; callers are updated to pass a `TenantTx`
instead of a pool-backed `Querier`.

### 1.2 Type-level separation: `TenantQuerier` vs `AdminQuerier`

A central requirement is that the Go type system prevents an admin pool from being accidentally
passed into a tenant-scoped DAL function, and prevents a tenant transaction from being passed
into an admin DAL function.

```go
// TenantQuerier is a Querier that has had app.tenant_id set for the current
// transaction. Only BeginTenantTx (using Pools.App) can produce one.
// All tenant-scoped DAL functions accept TenantQuerier, not Querier directly.
type TenantQuerier interface {
    dal.Querier
    tenantTxMarker() // unexported: only our package implements this
}

// AdminQuerier is a Querier backed by the Pools.Admin connection (BYPASSRLS).
// Only NewAdminQuerier (using Pools.Admin) can produce one.
// All cross-tenant/admin DAL functions accept AdminQuerier.
type AdminQuerier interface {
    dal.Querier
    adminQuerierMarker() // unexported: only our package implements this
}
```

`dal.Querier` remains the base interface (Query / QueryRow / Exec / ExecReturning). Neither
marker type is exported for construction outside the `db` package.

**Consequence:** A DAL function declared as `func ListAgents(ctx, q TenantQuerier)` cannot
receive a raw `pgxpool.Pool` or an `AdminQuerier` — compile error. A DAL function declared as
`func ListTenants(ctx, q AdminQuerier)` cannot receive a `TenantTx` — compile error.

DAL functions that legitimately accept either (e.g. test helpers) continue to accept
`dal.Querier` directly, but no production handler or service is allowed to do this.

### 1.3 `TenantTx` concrete type

```go
// TenantTx wraps pgx.Tx and marks itself as a TenantQuerier.
// Obtained only from Pools.BeginTenantTx.
type TenantTx struct {
    tx pgx.Tx
}
func (t *TenantTx) tenantTxMarker() {}
func (t *TenantTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *TenantTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
// Query / QueryRow / Exec / ExecReturning delegate to t.tx
```

### 1.4 Transaction lifecycle — exact rules

**Begin:**
1. `Pools.App.Begin(ctx)` → obtains a `pgx.Tx` from the app pool (no BYPASSRLS).
2. Immediately execute:
   ```sql
   SELECT set_config('app.tenant_id', $1, true)
   ```
   where `$1` is the tenant UUID as a string. This is a parameterised query — the tenant ID
   is never interpolated into SQL.
   - `true` = transaction-local; the GUC resets automatically on COMMIT or ROLLBACK.
3. If the `set_config` call fails, call `tx.Rollback(ctx)` and return the error. The connection
   is returned clean to the pool.
4. Return a `*TenantTx` wrapping the transaction.

**Pattern at call site:**
```go
tx, err := pools.BeginTenantTx(ctx, tenantID)
if err != nil { return err }
defer tx.Rollback(ctx) // safe no-op after Commit

result, err := dal.ListAgents(ctx, tx, tenantID)
if err != nil { return err }

return tx.Commit(ctx)
```

**Commit:** `pgx.Tx.Commit`. The `app.tenant_id` GUC reverts to the session default (empty
string) automatically.

**Rollback:** `pgx.Tx.Rollback` is idempotent in pgx — safe to call after Commit (returns
`pgx.ErrTxClosed`, which is silently ignored in the deferred pattern). The deferred `Rollback`
is therefore always safe regardless of whether Commit succeeded or failed.

**Panic:** the caller must `defer tx.Rollback(ctx)` before any fallible operation. If a panic
occurs after `Begin` but before `Commit`, the deferred `Rollback` fires during the stack
unwind. pgx closes the underlying connection on error, so the pool recycles it clean.

**Context cancellation:** pgx propagates context cancellation to the in-flight query. If the
context is cancelled after `Begin` but before `Commit`, the next operation returns a
cancellation error. The deferred `Rollback` still fires and succeeds because pgx rolls back the
server side on error. No special handling is needed beyond the standard `defer tx.Rollback(ctx)`
pattern.

**Connection reuse safety** (the core guarantee):
`set_config(..., true)` scopes the GUC strictly to the current transaction. When the
transaction ends (commit or rollback), Postgres resets `app.tenant_id` to the session default.
The session default is never set by the application — it is always empty string. A pooled
connection that is checked out for a new `BeginTenantTx` call always starts with
`app.tenant_id = ''`. There is no mechanism by which a tenant ID from transaction A can survive
into transaction B on the same physical connection. This is enforced by Postgres, not by the
application.

**Nested transactions:** pgx supports savepoints via `pgx.Tx.Begin`. The design does not use
them. If an existing transaction is detected, `BeginTenantTx` returns an error rather than
attempting a savepoint nest. This prevents accidental re-entrance.

### 1.5 AdminQuerier concrete type

```go
// adminQuerier wraps a Querier backed by Pools.Admin and marks itself AdminQuerier.
// Obtained only from pools.NewAdminQuerier().
type adminQuerier struct{ q dal.Querier }
func (a *adminQuerier) adminQuerierMarker() {}
// Query / QueryRow / Exec / ExecReturning delegate to a.q
```

Admin DAL functions receive this type. The admin pool connects as `them_admin` (BYPASSRLS) so
no `set_config` call is needed or made.

### 1.6 Transaction boundary is at the service/handler operation, not per-DAL-call

A single handler that calls multiple DAL functions (e.g. `PublishDefinition` which performs two
sequential UPDATEs) opens **one** transaction and passes it to all DAL calls. The transaction
commits or rolls back as a unit. This is the correct boundary for atomicity and for RLS
correctness (the `set_config` call needs to be issued only once per transaction, not once per
query).

---

## 2. Complete Database Access Inventory

This section catalogs every location in the codebase that touches Postgres, the scope of each
operation, and its required pool and transaction context after Step 19 is complete.

**Scope legend:**
- `T` — tenant-scoped: must run inside `BeginTenantTx` via `Pools.App`
- `X` — cross-tenant/admin: must run via `AdminQuerier` from `Pools.Admin`
- `A` — application-scoped: no direct `tenant_id` column, but scoped via FK to `applications`; must run inside `TenantTx` so the parent-table RLS policy (or subquery policy on the child) is satisfied
- `P` — platform-global: rows with `tenant_id IS NULL`; must run via `AdminQuerier`
- `G` — global with no tenant dimension: platform config, schema_migrations, etc.; `AdminQuerier`

### 2.1 `go/internal/admin/dal/agents.go`

| Function | Scope | Notes |
|---|---|---|
| `ListAgents` | T | |
| `GetAgent` | T | |
| `CreateAgent` | T | |
| `UpdateAgent` | T | |
| `DeleteAgent` | T | Second DELETE on `component_definitions` uses `agent_id` only — must add `AND tenant_id = $N` or verify cascade |
| `AgentExists` | X | Cross-tenant uniqueness check by slug |
| `GetAgentBySlug` | X | Slug is cross-tenant unique |
| `UpdateAgentScanResult` | X | Called by security scanner; no tenant context at call site |
| `GetAgentByID` | X | Runtime path; caller asserts ownership |
| `GetAgentTokenEncrypted` | X | Token introspection; cross-tenant by design |

### 2.2 `go/internal/admin/dal/orchestrators.go`

| Function | Scope | Notes |
|---|---|---|
| `ListOrchestrators` | T | |
| `GetOrchestrator` | T | |
| `CreateOrchestrator` | T | |
| `UpdateOrchestrator` | T | |
| `DeleteOrchestrator` | T | |

### 2.3 `go/internal/admin/dal/applications.go`

| Function | Scope | Notes |
|---|---|---|
| `ListApplications` | T | |
| `GetApplication` | T | |
| `CreateApplication` | T | |
| `UpdateApplication` | T | |
| `DeleteApplication` | T | |
| `BulkDeleteApplications` | T | |
| `UpdateRuntimeConfig` | T | |
| `GetProviderKeys` | T | |
| `SetProviderKey` | T | |
| `DeleteProviderKey` | T | |
| `GetAppParams` | T | |
| `SetAppParam` | T | |
| `DeleteAppParam` | T | |
| `ListEntryPoints` | A | Scoped by `application_id`; pass inside TenantTx so entry_points subquery RLS passes |
| `listAppOrchSummaries` | A | |
| `SetOrchestratorLLM` | A | |
| `SetOrchestratorVoice` | A | |
| `SetOrchestratorMCPServers` | A | |
| `CreateEntryPoint` | A | |
| `UpdateEntryPoint` | A | |
| `DeleteEntryPoint` | A | |
| `GetAgentSummariesByIDs` | X | Cross-tenant agent card synthesis |

### 2.4 `go/internal/admin/dal/runs.go`

| Function | Scope | Notes |
|---|---|---|
| `ListRuns` | T | |
| `GetRun` | T | |
| `GetRunContextID` | T | |
| `GetRunStats` | T | |
| `CountActiveRuns` | T | Called from `tenantQuotaAdapter` in `cmd/them/main.go`; must use TenantTx |
| `GetRunDetail` | T | run_steps / run_usage scanned via run_id FK inside same tx |
| `GetRunTasks` | T | EXISTS subquery on runs.tenant_id |
| `GetContextMessages` | T | Uses `tasks.tenant_id`; requires tasks backfill first |
| `TailRunSteps` | T | |
| `TailRunUsage` | T | |

### 2.5 `go/internal/admin/dal/tokens.go`

| Function | Scope | Notes |
|---|---|---|
| `ListTokens` | T | |
| `GetToken` | T | |
| `CreateToken` | T | |
| `UpdateToken` | T | |
| `DeleteToken` | T | |
| `LookupToken` | T | |

### 2.6 `go/internal/admin/dal/tenants.go`

| Function | Scope | Notes |
|---|---|---|
| `ListTenants` | X | Platform admin |
| `GetTenant` | X | |
| `CreateTenant` | X | |
| `PatchTenant` | X | |
| `GetTenantByEmailDomain` | X | Pre-auth lookup; no tenant context established yet |
| `GetQuota` | X | Reads `tenant_quotas` by tenantID param; no RLS needed on this table (see §5) |
| `UpsertQuota` | X | |
| `ListMembers` | X | Reads `auth_service.tenant_memberships`; no `them`-schema RLS |
| `AddMember` | X | |
| `ListGroupMappings` | T | `tenant_group_mappings` is RLS-protected |
| `UpsertGroupMapping` | T | |
| `DeleteGroupMapping` | T | |

### 2.7 `go/internal/admin/dal/llm_providers.go`

| Function | Scope | Notes |
|---|---|---|
| `ListProvidersForTenant` | T | Reads both platform + tenant rows; split policy in §5.3 |
| `GetProviderByNameForTenant` | T | |
| `UpsertTenantProvider` | T | |
| `ListProviders` (platform) | P | `WHERE tenant_id IS NULL`; uses AdminQuerier |
| `GetProviderByNamePlatform` | P | |
| `CreateProvider` | P | |
| `UpdateProvider` | P | |
| `DeleteProvider` | P | |

### 2.8 `go/internal/admin/dal/managed_apps.go`

| Function | Scope | Notes |
|---|---|---|
| `ListManagedApps` | X | Platform-global |
| `CreateManagedApp` | X | |
| `GetManagedApp` | X | |
| `ListManagedAppParams` | X | |
| `UpsertManagedAppParams` | X ⚠ | DELETE + INSERT without transaction — atomicity bug; fix before Step 19 |
| `ListBindingsForTenant` | T | |
| `GetBinding` | T | |
| `UpsertBinding` | T | |
| `ListBindingsByTenant` (path-param variant) | X | Platform admin path; uses tenantID from path, not JWT context |
| `UpsertBindingByTenant` (path-param variant) | X | Same |

### 2.9 `go/internal/admin/dal/mcp_servers.go`

| Function | Scope | Notes |
|---|---|---|
| `ListMCPServers` | T | |
| `GetMCPServer` | T | |
| `CreateMCPServer` | T | |
| `UpdateMCPServer` | T | |
| `DeleteMCPServer` | T | |
| `ListAppMCPCredentials` | A | Scoped by `application_id`; run inside TenantTx |
| `GetAppMCPCredential` | A | |
| `UpsertAppMCPCredential` | A | |
| `DeleteAppMCPCredential` | A | |

### 2.10 `go/internal/admin/dal/agent_definitions.go`

| Function | Scope | Notes |
|---|---|---|
| `ListDefinitionsForAgent` | T | |
| `GetDefinition` | T | |
| `CreateDefinition` | T | |
| `UpdateDefinition` | T | |
| `DeleteDefinition` | T | |

### 2.11 `go/internal/admin/dal/publish.go`

| Function | Scope | Notes |
|---|---|---|
| `PublishDefinition` | T ⚠ | Two sequential UPDATEs currently without an explicit transaction; must be atomically wrapped before Step 19 |
| `UpsertAppOrchestrator` | A | |
| `UpsertEntryPoint` | A | |
| `DeactivateStaleOrchestrators` | T | |
| `DeactivateStaleEntryPoints` | T | |

### 2.12 `go/internal/admin/dal/services_stats.go`

| Function | Scope | Notes |
|---|---|---|
| `GetSecurityScanStats` | X | Platform-wide aggregate; admin-only |

### 2.13 `go/internal/admin/dal/agent_bindings.go`

| Function | Scope | Notes |
|---|---|---|
| `GetAgentParamsForBinding` | X | Platform-global spec lookup |
| `GetRequiredParamsForAgent` | X | |
| `(binding CRUD fns)` | A | Scoped by `application_id` |

### 2.14 `go/internal/admin/dal/config.go`

| Function | Scope | Notes |
|---|---|---|
| `GetConfig` | G | No `tenant_id` column; platform-global |
| `UpsertConfig` | G | |

### 2.15 `go/internal/runrecorder/recorder.go`

Uses its own `DBQuerier` interface backed by `PgxPoolQuerier`. Writes to `them.runs`,
`them.run_steps`, `them.run_usage`. Already passes `tenant_id` on INSERT for runs.
After Step 19 must use a `TenantTx` so the RLS WITH CHECK passes on INSERT.

A `TenantRecorder` wrapper or a recorder constructor that accepts a `TenantQuerier` is needed.

### 2.16 `go/internal/authserver/pgx.go` and `oidc_store.go`

Hold a raw `*pgxpool.Pool`. Queries split into two groups:

- `auth_service.*` tables only (`GetUser`, `GetRole`, `UpsertUser`, `GetTenantMembership`,
  `UpsertOIDCUser`, `GetUserSessions`, `BlacklistToken`, `LookupTenantByEmailDomain`): these
  are in the `auth_service` schema which has no RLS policies and which `them_app` has no
  grants on. No change needed for RLS isolation.
- `them.*` reads in `oidc_store.go`:
  - `GetTenantIDPConfig` — reads `them.tenants` (cross-tenant; pre-auth path)
  - `GetGroupRole` — reads `them.tenant_group_mappings` (cross-tenant; pre-auth path)

  These two must use the `Pools.Admin` pool (BYPASSRLS) after Step 19. Currently they use
  the main pool which will be the app pool (no BYPASSRLS) once the role is split.

- `UpsertOIDCUser` ⚠ — 4+ sequential queries without a transaction (existing race condition).
  Fix independently before Step 19: wrap in `pool.Begin(ctx)` (standard transaction, not
  TenantTx — no `app.tenant_id` needed for auth_service schema writes).

### 2.17 `go/internal/agentregistry/pgx_querier.go`

Direct `pgxpool.Pool` usage:
- `GetBindingID` — reads `them.app_agent_bindings` by `(application_id, agent_id)`. No tenant
  column; scoped via `application_id` → `applications.tenant_id`. This is a runtime-path lookup
  used by the canvas agent executor; it needs a TenantTx and an EXISTS-based subquery policy on
  `app_agent_bindings` (see §5).
- `QueryAgentsByTenant` — reads `them.agents WHERE tenant_id = $1`. Needs TenantTx.

### 2.18 `go/internal/middleware/` (gate.go, job.go, pgx.go)

`PgxQuerier` in `pgx.go` wraps `pgxpool.Pool`. Operations split:
- `EnqueueWithQuarantine` / `Enqueue` — writes `them.middleware_jobs`. No direct tenant_id
  (scoped via `application_id`). Called from the gateway (has tenant context) → use TenantTx.
- `Claim` — reads `them.middleware_jobs` cross-tenant (picks next unclaimed job regardless of
  tenant). Called from middleware worker loop. Must use AdminQuerier / `Pools.Admin`.
- `LoadSecurityConfig` / `LoadFileBytes` — reads `them.applications`, `them.quarantine_artifacts`.
  The middleware worker has application context but not a JWT-derived tenant ID. Strategy: use
  AdminQuerier, and verify ownership via `application_id` explicitly (safe because the worker
  only processes jobs it claimed from its own queue).
- `Complete` / `completeQuarantinePath` / `completeLegacyPath` — writes `them.run_artifacts`,
  `them.quarantine_artifacts`. Same context situation. Use AdminQuerier for the worker path.
- `WriteAudit` — writes `them.middleware_audit`. Use AdminQuerier for worker path.
- `LoadSecurityConfig` in `gate.go` — reads `them.applications` by app_id. Called from gateway
  with tenant context → TenantTx.

### 2.19 `go/internal/temporal/workerconfig/loader.go`

Direct `pgxpool.Pool` usage. All queries pass explicit `applicationID` and `tenantID` WHERE
clauses already. After Step 19:
- Tenant-scoped lookups (app runtime config, binding params, entry point config) → TenantTx
- Platform-global lookups (LLM provider platform defaults, ManagedApp catalog) → AdminQuerier

### 2.20 `go/cmd/agent-runtime/` (spec.go, llm.go, runtime.go)

Direct `pgxpool.Pool` usage. All queries include `tenant_id = $N::uuid` predicates. After Step
19, must wrap in TenantTx. The `tenantID` comes from `InvocationContext.TenantID` which is
already derived from JWT claims.

### 2.21 `go/cmd/dag-worker/main.go`

Direct `pgxpool.Pool` usage. Queries join `them.applications` and filter by `applicationID`.
Mix of tenant-scoped (spec loads) and cross-tenant (activity claims) queries. Must be split:
tenant-scoped → TenantTx; cross-tenant → AdminQuerier.

### 2.22 `go/internal/appliveness/liveness.go`

`listEnabledEPSlugs` queries `them.entry_points JOIN them.applications` without a tenant filter
— intentionally cross-tenant (health probe of all enabled EPs). Must use AdminQuerier.

### 2.23 `go/internal/reconciler/reconciler.go`

Queries `them.runs` to detect stale runs and update their status — intentionally cross-tenant
(platform-level health sweep). Must use AdminQuerier.

Also uses `pg_try_advisory_lock` / `pg_advisory_unlock` via session-scoped calls on the pool
(not inside a transaction). Advisory locks are session-scoped in Postgres and incompatible with
PgBouncer transaction mode. See §7.

### 2.24 No direct DB access

The following packages have no direct DB calls and require no changes for RLS:
`internal/quota/`, `internal/gate/`, `internal/session/`, `internal/llm/`, `internal/ws/`,
`internal/sse/`, `internal/a2a/`, `internal/orchestrator/` (delegates to runrecorder),
`cmd/them/main.go` (wiring only).

---

## 3. Tenant Context Setup

### 3.1 The `set_config` call

```sql
SELECT set_config('app.tenant_id', $1, true)
```

- `$1` is the tenant UUID as a string, bound by the pgx driver.
- The string is never interpolated into SQL text — injection into a GUC value is impossible.
- Third argument `true` = transaction-local. The GUC resets to its session default (empty
  string — never set at session level) on COMMIT or ROLLBACK.

### 3.2 Empty-string-safe policy expression

The original document used `current_setting('app.tenant_id', true)::uuid`. This is not
empty-string-safe: when `app.tenant_id` is set (not missing) but has value `''`, the cast to
`uuid` raises `invalid input syntax for type uuid`, causing a query error rather than an
empty result set.

All RLS policy expressions in this design use:

```sql
NULLIF(current_setting('app.tenant_id', true), '')::uuid
```

Behavior table:

| State of `app.tenant_id` | `current_setting(..., true)` | `NULLIF(..., '')` | `::uuid` cast | Policy result |
|---|---|---|---|---|
| GUC not set (never initialized) | `NULL` (missing_ok) | `NULL` | `NULL` | No rows match (fail-closed) |
| GUC set to `''` (fresh connection after reset) | `''` | `NULL` | `NULL` | No rows match (fail-closed) |
| GUC set to valid UUID string | `'<uuid>'` | `'<uuid>'` | valid UUID | Rows for that tenant |
| GUC set to invalid string | `'bad'` | `'bad'` | cast error | Query error |

The last case (invalid UUID string) is not reachable in practice: `BeginTenantTx` receives the
tenant ID from `tenantctx.TenantIDFromCtx`, which is populated exclusively from JWT claims
validated by `internal/auth/`. However, the policy expression must not fail on an empty string
because fresh pool connections have the GUC at its session default value of `''` (set_config
with the `true` flag resets to `''`, not to uninitialized).

### 3.3 Source of tenant context

`TenantID` is extracted only from JWT claims via the `tenantctx` typed context key (`tenantctx.TenantIDFromCtx`). It is never read from request headers, query parameters, or any
application-layer setting. This constraint was established in Step 1 and is unchanged.

### 3.4 Connection reuse safety proof

1. Connection checked out from `Pools.App`.
2. `BEGIN` issued by pgx.
3. `SELECT set_config('app.tenant_id', $1, true)` — GUC is now tenant-A's UUID.
4. All tenant-A queries execute; RLS filters to tenant-A rows.
5. `COMMIT` (or `ROLLBACK`).
6. Postgres server resets `app.tenant_id` to the session default (`''`) automatically.
7. Connection returned to pool.
8. Next checkout: `NULLIF('', '')::uuid = NULL` — no rows match. Fail-closed until a new
   `set_config` call.

There is no scenario where a pooled connection carries a stale tenant ID into a subsequent
request, because `set_config(..., true)` is strictly transaction-scoped on the Postgres server.

### 3.5 Missing tenant context fails closed

When `BeginTenantTx` is called but the `set_config` query fails (DB error), the function rolls
back and returns an error — no queries execute. When code accidentally runs a query outside a
`TenantTx` via the app pool (bug scenario), `app.tenant_id` is `''`, the policy expression
evaluates to `NULL`, and the WHERE clause `tenant_id = NULL` matches nothing — zero rows
returned for SELECT, `WITH CHECK` rejects INSERT/UPDATE.

---

## 4. Database Roles and Ownership

### 4.1 Current state (requires correction)

The live `them` role is a SUPERUSER with BYPASSRLS, LOGIN, CREATEROLE, and CREATEDB. It owns
all tables and is the sole application DSN. This must be corrected before Step 19.

### 4.2 Target state

| Role | Purpose | BYPASSRLS | SUPERUSER | LOGIN | Own tables |
|---|---|---|---|---|---|
| `them_owner` | Owns tables, runs migrations | Yes (table owner bypasses RLS) | No | **No** (NOLOGIN) | Yes |
| `them_admin` | Admin / cross-tenant runtime queries | Yes (explicit) | No | Yes | No |
| `them_app` | Tenant-scoped runtime queries | **No** | No | Yes | No |

#### Why `them_owner` must be NOLOGIN

`them_owner` owns the tables. Postgres normally allows table owners to bypass RLS even when
`FORCE ROW LEVEL SECURITY` is set, unless `BYPASSRLS` is explicitly revoked and `FORCE ROW
LEVEL SECURITY` is applied. Rather than rely on this nuance, `them_owner` must be `NOLOGIN` so
it can never be used as an application DSN. Migrations run via a superuser connection
(`postgres` or equivalent) that sets the role: `SET ROLE them_owner` for DDL operations.

`FORCE ROW LEVEL SECURITY` is still applied to all protected tables (see §5) as defense in
depth, but the primary guarantee is that `them_owner` cannot be used by the running application.

#### FORCE ROW LEVEL SECURITY and BYPASSRLS interaction

The Postgres documentation states clearly:

> Superusers and roles with the `BYPASSRLS` attribute always bypass the row security system,
> including both `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`.

`FORCE ROW LEVEL SECURITY` overrides the owner bypass, **but it does not override `BYPASSRLS`.**
This means `them_admin` (which has BYPASSRLS) will always bypass RLS regardless of FORCE. This
is intentional: `them_admin` is the designated bypass role. `them_app` has no BYPASSRLS, so
FORCE is irrelevant to it — it is subject to RLS unconditionally.

`FORCE ROW LEVEL SECURITY` is applied anyway to protect against accidental direct connections
using `them_owner` credentials (e.g. a misconfigured migration tool, a DBA testing something).

### 4.3 Migration path for the current `them` role

1. Create `them_owner` with NOLOGIN, grant it ownership of all tables.
2. Create `them_admin` with LOGIN, BYPASSRLS, no SUPERUSER.
3. Create `them_app` with LOGIN, no BYPASSRLS, no SUPERUSER.
4. Transfer table ownership: `ALTER TABLE them.<table> OWNER TO them_owner;` (all 32 tables).
5. Grant DML to `them_app` on tenant-scoped tables only (§4.5).
6. Grant ALL on all tables to `them_admin`.
7. Grant USAGE on schema `them` and `auth_service` to both `them_app` and `them_admin`.
8. Revoke SUPERUSER from `them` and eventually retire it (after DSN migration).
9. Add `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` secrets. Never commit these.

### 4.4 FORCE ROW LEVEL SECURITY

```sql
-- Applied to all tenant-scoped tables listed in §5.1:
ALTER TABLE them.<table> FORCE ROW LEVEL SECURITY;
```

This ensures that even if table DDL must be modified by `them_owner` during maintenance, an
accidental `them_owner` connection cannot read or write rows from all tenants without explicit
awareness.

### 4.5 Minimal grants for `them_app`

```sql
-- Tenant-scoped tables: SELECT, INSERT, UPDATE, DELETE
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agents TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.orchestrators TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.applications TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.entry_points TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.access_tokens TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.runs TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_steps TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_usage TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_artifacts TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tasks TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.task_messages TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.artifacts TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.mcp_servers TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.access_tokens TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_definitions TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_runtime_specs TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.application_definitions TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.managed_app_bindings TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_group_mappings TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.audit_logs TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.quarantine_artifacts TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.middleware_jobs TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.middleware_audit TO them_app;
-- Application-child tables (no direct tenant_id, protected via parent):
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_agent_bindings TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_orchestrators TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_mcp_credentials TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.middleware_wirings TO them_app;
-- Read-only reference tables needed by app runtime:
GRANT SELECT ON them.llm_providers TO them_app;   -- platform rows visible via split policy
GRANT SELECT ON them.tenants TO them_app;         -- membership lookups at login
-- them_app must NOT have access to: config, schema_migrations, managed_apps, managed_app_params,
-- component_definitions (builtin), middleware_defs (builtin), tenant_quotas (admin-only)

-- Sequences for SERIAL/BIGSERIAL columns:
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO them_app;

-- Schema access:
GRANT USAGE ON SCHEMA them TO them_app;
GRANT USAGE ON SCHEMA them TO them_admin;
GRANT USAGE ON SCHEMA auth_service TO them_admin;  -- authserver needs auth_service reads
```

### 4.6 Verification queries (run after role setup, before enabling RLS)

```sql
-- Confirm them_app has no BYPASSRLS:
SELECT rolbypassrls FROM pg_roles WHERE rolname = 'them_app';
-- Expected: f

-- Confirm them_owner has NOLOGIN:
SELECT rolcanlogin FROM pg_roles WHERE rolname = 'them_owner';
-- Expected: f

-- Confirm table ownership transferred:
SELECT tableowner FROM pg_tables WHERE tablename = 'agents' AND schemaname = 'them';
-- Expected: them_owner

-- Confirm them_app can connect but not bypass:
-- (Connect as them_app, try SELECT FROM them.agents without set_config)
-- Expected: 0 rows (once RLS is enabled)
```

### 4.7 Connection pool split in Go

`go/internal/db/db.go` currently creates one pool. After Step 19 it creates two:

```go
type Pools struct {
    App   *pgxpool.Pool  // them_app role — tenant-scoped ops
    Admin *pgxpool.Pool  // them_admin role — admin/platform/cross-tenant ops
}

func (p *Pools) BeginTenantTx(ctx context.Context, tenantID string) (*TenantTx, error)
func (p *Pools) NewAdminQuerier() AdminQuerier
```

`BeginTenantTx` always uses `Pools.App`.
`NewAdminQuerier` always uses `Pools.Admin`.

The two DSNs are read from separate environment variables and derived from `secrets.local`
via `generate-env.sh`. They are never committed.

---

## 5. RLS Policies

### 5.1 Full table classification

Every table in the `them` schema is classified. The classification is the authoritative record
— the previous document's claim that FK relationships provide implicit protection is incorrect
and is retracted here.

#### Child tables: FK does NOT provide implicit RLS protection

A foreign key from `run_steps.run_id → runs.id` does not cause Postgres to apply the RLS
policy on `runs` when querying `run_steps`. Each table's rows are visible according to
policies on **that table**, not its parents. A query `SELECT * FROM them.run_steps` by `them_app`
with no `app.tenant_id` set returns all rows — the runs RLS policy is not consulted.

For each child table (no direct `tenant_id`), the design explicitly chooses one of:
1. **Direct `tenant_id` + RLS policy** — add the column if missing, enable a direct policy.
2. **EXISTS-based RLS policy through parent** — policy uses a subquery join.
3. **Revoke direct `them_app` access** — `them_app` has no grants on this table; it can only
   reach the rows via a parent query that already applies RLS.

#### Table classification table

| Table | `tenant_id` | Direct RLS | Protection strategy | Notes |
|---|---|---|---|---|
| `them.agents` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.orchestrators` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.applications` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.entry_points` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | Has both `tenant_id` AND `application_id`; direct policy preferred over subquery |
| `them.access_tokens` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.runs` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.run_artifacts` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.tasks` | YES (nullable → backfill required) | YES — after backfill | Direct `tenant_id` | NULL rows must be backfilled before policy is created |
| `them.quarantine_artifacts` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.agent_definitions` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.agent_runtime_specs` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.application_definitions` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.component_definitions` | YES (nullable; NULL = builtin) | Partial — tenant rows only | EXISTS policy for tenant rows; builtin rows excluded | See §5.4 |
| `them.managed_app_bindings` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.audit_logs` | YES (nullable) | YES — after backfill if needed | Direct `tenant_id` (nullable OK with NULLIF expression) | NULLs = platform audit; policies pass NULL through safely |
| `them.mcp_servers` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.llm_providers` | YES (nullable; NULL = platform) | Partial — split policy | Read: own rows OR NULL rows; Write: own rows only | See §5.3 |
| `them.tenant_group_mappings` | YES (NOT NULL) | YES — standard policy | Direct `tenant_id` | |
| `them.run_steps` | NO (scoped via `run_id`) | YES — EXISTS via runs | EXISTS subquery | See §5.5 |
| `them.run_usage` | NO (scoped via `run_id`) | YES — EXISTS via runs | EXISTS subquery | See §5.5 |
| `them.artifacts` | NO (scoped via `task_id`) | YES — EXISTS via tasks | EXISTS subquery | See §5.5 |
| `them.task_messages` | NO (scoped via `task_id`) | YES — EXISTS via tasks | EXISTS subquery | See §5.5 |
| `them.middleware_audit` | NO (scoped via `artifact_id`) | YES — EXISTS via run_artifacts | EXISTS subquery | See §5.5 |
| `them.app_agent_bindings` | NO (scoped via `application_id`) | YES — EXISTS via applications | EXISTS subquery | See §5.5 |
| `them.app_orchestrators` | NO (scoped via `application_id`) | YES — EXISTS via applications | EXISTS subquery | See §5.5 |
| `them.app_mcp_credentials` | NO (scoped via `application_id`) | YES — EXISTS via applications | EXISTS subquery | See §5.5 |
| `them.middleware_wirings` | NO (scoped via `application_id`) | YES — EXISTS via applications | EXISTS subquery | See §5.5 |
| `them.middleware_jobs` | NO (scoped via `application_id`) | **Revoke them_app + AdminQuerier** | See §5.6 | Worker path is cross-tenant by design |
| `them.managed_app_params` | NO (FK to managed app) | **Revoke them_app** | them_app has no grants | Managed apps are platform-global; only them_admin writes params |
| `them.tenants` | N/A (IS the tenant table) | No RLS | No policy needed | them_app has SELECT only for reference lookups |
| `them.tenant_quotas` | YES (PK = tenant_id) | No RLS needed | them_app has no grants | Admin-only management; enforcement via Redis/service layer |
| `them.config` | NO | No RLS | them_app has no grants | Platform-global |
| `them.schema_migrations` | NO | No RLS | them_app has no grants | Internal |
| `them.middleware_defs` | NO (builtin only in practice) | No RLS | them_app has SELECT only | Builtin definitions; no tenant-specific rows today |

### 5.2 Standard policy template

```sql
ALTER TABLE them.<table> ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.<table> FORCE ROW LEVEL SECURITY;

CREATE POLICY <table>_tenant_isolation ON them.<table>
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

The `TO them_app` clause limits the policy to the runtime role. `them_admin` (BYPASSRLS) is
never subject to this policy. No policy is needed for `them_admin` — BYPASSRLS skips all
policies unconditionally.

### 5.3 Split policy: `them.llm_providers`

`llm_providers` has rows where `tenant_id IS NULL` (platform defaults) and rows where
`tenant_id = <uuid>` (tenant overrides). `them_app` needs to read both, but may only write
its own rows:

```sql
ALTER TABLE them.llm_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.llm_providers FORCE ROW LEVEL SECURITY;

-- Read: tenant's own rows OR platform defaults (NULL tenant_id)
CREATE POLICY llm_providers_read ON them.llm_providers
    AS PERMISSIVE
    FOR SELECT
    TO them_app
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR tenant_id IS NULL
    );

-- Write: tenant may only write its own rows
CREATE POLICY llm_providers_write ON them.llm_providers
    AS PERMISSIVE
    FOR INSERT
    TO them_app
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY llm_providers_update ON them.llm_providers
    AS PERMISSIVE
    FOR UPDATE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY llm_providers_delete ON them.llm_providers
    AS PERMISSIVE
    FOR DELETE
    TO them_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

Platform default rows (NULL tenant_id) are created and deleted only by `them_admin`.

### 5.4 Partial policy: `them.component_definitions`

`component_definitions` has `scope = 'builtin'` (NULL tenant_id) and `scope = 'tenant'`
rows (tenant_id NOT NULL). `them_app` needs to read builtin definitions but may only
read/write its own tenant definitions:

```sql
ALTER TABLE them.component_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.component_definitions FORCE ROW LEVEL SECURITY;

CREATE POLICY component_definitions_access ON them.component_definitions
    AS PERMISSIVE
    TO them_app
    USING (
        tenant_id IS NULL  -- builtins always visible
        OR tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    )
    WITH CHECK (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    );
```

Builtin rows (tenant_id IS NULL) cannot be written by `them_app` (WITH CHECK rejects NULL).

### 5.5 EXISTS-based policies for child tables

For child tables with no direct `tenant_id`, the policy uses an EXISTS subquery. This is
checked once per row and indexed on the parent's `tenant_id` column.

**`them.run_steps` (parent: `them.runs`)**
```sql
ALTER TABLE them.run_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.run_steps FORCE ROW LEVEL SECURITY;

CREATE POLICY run_steps_tenant_isolation ON them.run_steps
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_steps.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_steps.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));
```

**`them.run_usage` (parent: `them.runs`)** — identical pattern, `run_usage.run_id → runs.id`.

**`them.artifacts` (parent: `them.tasks`)**
```sql
CREATE POLICY artifacts_tenant_isolation ON them.artifacts
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = artifacts.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = artifacts.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));
```

**`them.task_messages`** — identical pattern, `task_messages.task_id → tasks.id`.

**`them.middleware_audit` (parent: `them.run_artifacts`)**
```sql
CREATE POLICY middleware_audit_tenant_isolation ON them.middleware_audit
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.run_artifacts ra
        WHERE ra.id = middleware_audit.artifact_id
          AND ra.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.run_artifacts ra
        WHERE ra.id = middleware_audit.artifact_id
          AND ra.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));
```

**`them.app_agent_bindings` (parent: `them.applications`)**
```sql
CREATE POLICY app_agent_bindings_tenant_isolation ON them.app_agent_bindings
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_agent_bindings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_agent_bindings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));
```

**`them.app_orchestrators`**, **`them.app_mcp_credentials`**, **`them.middleware_wirings`**
— identical pattern through `application_id → applications.tenant_id`.

Performance note: all EXISTS subqueries join on a single-column FK that is indexed (`application_id`,
`run_id`, `task_id` all have explicit indexes). The planner uses an index scan on the parent table
rather than a seq scan. Verify with `EXPLAIN` after enablement.

### 5.6 `them.middleware_jobs` — revoke direct `them_app` access

`middleware_jobs` is accessed in two contexts:
- **Gateway** (when a file is intercepted): inserts one row via `EnqueueWithQuarantine`. This
  happens inside a tenant-aware request context, but the middleware worker is the primary
  consumer and it is cross-tenant.
- **Middleware worker** (Claim, Complete, Fail): reads the next pending job from any tenant —
  intentionally cross-tenant.

To avoid a split policy that is hard to reason about, `middleware_jobs` uses the AdminQuerier
path exclusively. `them_app` **is not granted** DML on `middleware_jobs`. The gateway's enqueue
call is moved to use `Pools.Admin` via `AdminQuerier`. No RLS policy is created for this table.
This is safe because all writes include `application_id` and `run_id`, and the middleware worker
is a trusted internal component.

### 5.7 Cross-tenant access — preferred approach

The design avoids `SECURITY DEFINER` functions as the primary bypass mechanism. The reasons:
- SECURITY DEFINER functions run with the definer's privileges, not the caller's — they are
  hard to audit and can silently elevate privilege in unexpected ways.
- The `them_admin` role with BYPASSRLS on a separate pool is simpler, auditable (all admin
  queries go through one pool), and equally secure.

The `them_admin` pool is the designated mechanism for all legitimate cross-tenant operations.
No SECURITY DEFINER functions are introduced in Step 19.

### 5.8 Behavior when `app.tenant_id` is missing or invalid

| Scenario | SELECT | INSERT/UPDATE | Notes |
|---|---|---|---|
| GUC not set (fresh connection) | 0 rows | Rejected by WITH CHECK | Fail-closed |
| GUC is `''` (reset after tx end) | 0 rows | Rejected by WITH CHECK | Fail-closed |
| GUC is valid UUID, no matching rows | 0 rows | Allowed (correct tenant) | Normal |
| GUC is valid UUID, mismatched tenant | 0 rows | Rejected | Cross-tenant attempt |
| GUC is invalid UUID string | Query error | Query error | Bug — UUID validation in BeginTenantTx prevents this |
| them_admin role (BYPASSRLS) | All rows | Allowed | Intentional bypass |

---

## 6. Test Plan

### 6.1 Separation of unit tests and integration tests

**Unit tests** (no live DB) can verify:
- `TenantTx` and `adminQuerier` implement the correct marker interfaces.
- `BeginTenantTx` returns an error when `pool.Begin` fails (mock).
- `BeginTenantTx` rolls back and returns error when `set_config` fails (mock).
- Deferred `Rollback` after `Commit` does not panic (pgx.ErrTxClosed is ignored).
- DAL functions that accept `TenantQuerier` do not compile when given a raw pool or `AdminQuerier`.
- DAL functions that accept `AdminQuerier` do not compile when given a `TenantTx`.

**Unit tests cannot verify:**
- That RLS policies actually filter rows.
- That `FORCE ROW LEVEL SECURITY` applies to `them_owner`.
- That connection reuse is safe.
- Any behavior that depends on real Postgres RLS semantics.

All RLS correctness tests must run against a live Postgres instance using the actual restricted
roles.

### 6.2 Integration tests (build tag `integration`)

Location: `go/internal/db/rls_integration_test.go`

Test setup: creates `them_app` and `them_admin` roles; creates a minimal schema with one
tenant-scoped table and one child table; inserts rows for two tenants.

#### Direct-tenant-id table tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-01 | TenantTx for tenant-A: SELECT agents | Only tenant-A rows returned |
| RLS-02 | TenantTx for tenant-B: SELECT agents | Only tenant-B rows returned |
| RLS-03 | TenantTx for tenant-A: SELECT by ID where row is tenant-B's | 0 rows (not error) |
| RLS-04 | TenantTx for tenant-A: INSERT row with tenant_id = tenant-B | WITH CHECK error |
| RLS-05 | TenantTx for tenant-A: UPDATE row owned by tenant-B | 0 rows updated |
| RLS-06 | TenantTx for tenant-A: DELETE row owned by tenant-B | 0 rows deleted |
| RLS-07 | AdminQuerier: SELECT all agents (no app.tenant_id set) | All rows returned |
| RLS-08 | Missing context: raw pool.Query (no BeginTenantTx) | 0 rows (fail-closed) |
| RLS-09 | Empty string safe: app.tenant_id = '' explicitly set | 0 rows (fail-closed) |

#### Fresh and reused connection tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-10 | Fresh connection from pool with no prior GUC: SELECT | 0 rows without BeginTenantTx |
| RLS-11 | After tx-A commits (tenant-A): new query on same physical connection | app.tenant_id is '' — 0 rows without new BeginTenantTx |
| RLS-12 | Back-to-back tx-A (tenant-A) → tx-B (tenant-B) on same connection | tx-B sees only tenant-B rows |
| RLS-13 | Rollback: tx opened for tenant-A, rolled back; tx-B on same connection | tx-B sees only tenant-B rows |
| RLS-14 | Panic + recover: deferred Rollback fires; next tx on same connection | Correct isolation |
| RLS-15 | Context cancelled mid-query: deferred Rollback fires cleanly | No error, no leaked GUC |

#### Child table tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-16 | TenantTx for tenant-A: SELECT run_steps WHERE run_id = tenant-A run | Own run_steps visible |
| RLS-17 | TenantTx for tenant-A: SELECT run_steps WHERE run_id = tenant-B run | 0 rows |
| RLS-18 | TenantTx for tenant-A: INSERT run_steps with tenant-B run_id | WITH CHECK error (exists subquery blocks) |
| RLS-19 | TenantTx for tenant-A: SELECT run_usage for tenant-B run | 0 rows |
| RLS-20 | TenantTx for tenant-A: SELECT artifacts for tenant-B task | 0 rows |
| RLS-21 | TenantTx for tenant-A: SELECT task_messages for tenant-B task | 0 rows |
| RLS-22 | TenantTx for tenant-A: SELECT middleware_audit for tenant-B artifact | 0 rows |
| RLS-23 | TenantTx for tenant-A: SELECT app_agent_bindings for tenant-B application | 0 rows |
| RLS-24 | TenantTx for tenant-A: SELECT app_orchestrators for tenant-B application | 0 rows |

#### Split-policy and special-case tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-25 | llm_providers: tenant-A reads own rows AND platform (NULL) rows | Both visible |
| RLS-26 | llm_providers: tenant-A cannot INSERT row with tenant_id = NULL | WITH CHECK error |
| RLS-27 | llm_providers: tenant-A cannot read tenant-B rows | 0 rows |
| RLS-28 | component_definitions: tenant-A reads own + builtin rows | Both visible |
| RLS-29 | component_definitions: tenant-A cannot INSERT builtin (NULL tenant_id) | WITH CHECK error |

#### FORCE ROW LEVEL SECURITY and role tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-30 | them_app role exists, has no BYPASSRLS | `rolbypassrls = false` |
| RLS-31 | them_owner role has NOLOGIN | `rolcanlogin = false` |
| RLS-32 | them_admin role has BYPASSRLS | `rolbypassrls = true` |
| RLS-33 | them_admin SELECT with no app.tenant_id set: all rows returned | BYPASSRLS confirmed |

#### Wrong-pool wiring tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-34 | Passing AdminQuerier to a TenantQuerier-accepting DAL function | Compile error (type mismatch) |
| RLS-35 | Passing TenantTx to an AdminQuerier-accepting DAL function | Compile error (type mismatch) |
| RLS-36 | Test fake that accepts TenantQuerier panics when called with raw pool | Runtime panic on misuse |

#### Atomicity and pre-condition tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-37 | UpsertManagedAppParams: simulate crash between DELETE and INSERT | After rollback: original params still present (no partial state) |
| RLS-38 | PublishDefinition: simulate second UPDATE failure | After rollback: first UPDATE not committed |

#### Rollout compatibility tests

| Test ID | Description | Assertion |
|---|---|---|
| RLS-39 | RLS enabled on agents; old code path without TenantTx queries agents | 0 rows (not crash) — rollout safe |
| RLS-40 | RLS disabled on table; code using TenantTx queries it | All matching rows returned (not filtered) — backward compatible |

### 6.3 Two-tenant full isolation regression test

`TestRLS_TwoTenantFullIsolation` — permanent member of the integration suite:
1. Creates tenants A and B with agents, orchestrators, applications, runs, run_steps, tasks,
   artifacts, task_messages, app_agent_bindings, app_orchestrators, mcp_servers.
2. Reads every tenant-scoped table as tenant-A; asserts zero tenant-B rows.
3. Reads every tenant-scoped table as tenant-B; asserts zero tenant-A rows.
4. Attempts cross-tenant INSERT on every RLS-protected table; asserts all are blocked.

This test is the permanent regression gate. It must pass on every PR that touches the DB layer.

### 6.4 Fake / mock updates

All existing fake `Querier` implementations in test files must be updated:
- Fakes that currently accept `dal.Querier` in tenant-scoped functions must be updated to
  accept `TenantQuerier`. If a test passes a raw fake struct (not obtained from
  `BeginTenantTx`), it must fail loudly (panic or compile error) — not silently succeed.
- A `FakeTenantTx` test helper is introduced that implements `TenantQuerier` using an in-memory
  fake, for unit tests that do not need a real DB.
- A `FakeAdminQuerier` test helper similarly for admin DAL unit tests.

---

## 7. PgBouncer Assessment

### 7.1 Is PgBouncer justified independently of RLS?

A connection capacity problem would justify PgBouncer regardless of RLS. There is currently no
measured connection exhaustion: the stack has one `them-go-bridge`, one `them-go-worker`, one
`them-agent-runtime`, one `them-dag-worker`, one `them-middleware-worker`, and one
`them-auth-go`, each with a default `pgxpool` of up to 4 connections. The database limit is the
Postgres default of 100. Current utilization is well below that. There is no independently
measured connection-capacity problem.

**Decision: PgBouncer is out of scope for Step 19 and should be deferred as a separate
infrastructure project when connection exhaustion is actually measured.**

### 7.2 pgx/v5 prepared statement behavior (corrected)

pgx/v5 uses `QueryExecModeCacheStatement` as its default query execution mode. In this mode,
pgx automatically prepares and caches named statements using the PostgreSQL extended query
protocol. Named prepared statements are **session-scoped** in Postgres.

PgBouncer in **transaction mode** (the mode that actually saves connections by multiplexing
many app connections onto few server connections) does not support session-scoped prepared
statements. Each transaction can land on a different server connection, so a prepared statement
created in one transaction is not guaranteed to be present in the next.

The consequence: if PgBouncer in transaction mode is ever added, pgx must be configured with:
```go
cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
// or
cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
```

`SimpleProtocol` uses the simple query protocol (no prepared statements), which is compatible
with PgBouncer transaction mode. `DescribeExec` describes each query once per pool connection
but does not hold a named statement — also compatible.

This is relevant only if PgBouncer is added in the future. The current direct-connection setup
with `QueryExecModeCacheStatement` is fully correct and has no incompatibility.

### 7.3 `SET LOCAL` / `set_config` compatibility

`SET LOCAL` (and `set_config(..., true)`) scopes the GUC strictly to the current transaction.
It resets on COMMIT or ROLLBACK regardless of connection pooling. Compatibility:

| Pooling mode | Compatible | Notes |
|---|---|---|
| No pooler (current) | Yes | |
| PgBouncer transaction mode | Yes | Each transaction gets a server connection for its duration; GUC resets between |
| PgBouncer session mode | Yes | Session is persistent; GUC still resets per transaction |
| PgBouncer statement mode | No | Transactions not supported; incompatible with BEGIN/COMMIT |

If PgBouncer is added, transaction mode is required. Statement mode must never be used with
this codebase.

### 7.4 Advisory locks

The reconciler uses `pg_try_advisory_lock` / `pg_advisory_unlock` via session-scoped calls on
a pool connection (not inside a transaction). Session-scoped advisory locks are incompatible
with PgBouncer transaction mode because the lock is held on a specific server connection, but
subsequent calls in the same "session" may land on a different server connection.

If PgBouncer is added, the reconciler must migrate to `pg_advisory_xact_lock` (transaction-
scoped) or use a different distributed lock mechanism. This is a blocker for PgBouncer adoption
but does not affect Step 19.

### 7.5 `set_config` and the RLS design are PgBouncer-ready

The RLS design using `set_config('app.tenant_id', $1, true)` inside explicit transactions is
compatible with a future PgBouncer addition in transaction mode, provided (a) pgx is
reconfigured to use `SimpleProtocol` or `DescribeExec`, and (b) the reconciler migrates its
advisory lock. Both are deferred changes; neither blocks Step 19.

---

## 8. Execution Plan and Rollback

### 8.1 Pre-conditions (must be true before any schema change)

- [ ] All existing `go test ./...` pass at HEAD
- [ ] `tasks.tenant_id` backfilled: zero NULL rows (Appendix B)
- [ ] `audit_logs.tenant_id` reviewed: NULL rows are platform-level events; document whether
      to backfill or leave nullable (policy expression handles both via NULLIF)
- [ ] `UpsertOIDCUser` race fix: 4 queries wrapped in a single transaction
- [ ] `UpsertManagedAppParams` atomicity fix: DELETE+INSERT in a transaction
- [ ] `PublishDefinition` atomicity fix: two UPDATEs in a single transaction
- [ ] `them_owner`, `them_admin`, `them_app` roles created with correct attributes
- [ ] All table ownership transferred to `them_owner`
- [ ] Minimal grants applied (§4.5)
- [ ] `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` added to `generate-env.sh` and `.env`
- [ ] `Pools` struct created in `go/internal/db/db.go`
- [ ] `BeginTenantTx` and `NewAdminQuerier` implemented and unit-tested
- [ ] `TenantQuerier` and `AdminQuerier` marker interfaces implemented
- [ ] Integration test infrastructure exists and passes against the two new roles

### 8.2 Deployment ordering rule

**Every caller of a table must be migrated to use the correct pool/transaction before RLS is
enabled on that table.** Enabling RLS with `them_app` before migrating callers causes those
callers to return 0 rows (not an error), which is a live production regression.

The safe sequence for each table group:
1. Migrate all Go callers to use `TenantTx` or `AdminQuerier` as appropriate.
2. Deploy new application binary (both pools wired, all callers correct).
3. Verify deployment is healthy (smoke test, logs, existing tests pass).
4. Apply migration to enable RLS on the table group.
5. Verify RLS is enforcing correctly (run integration tests against live DB).

### 8.3 Execution phases

**Phase A — Infrastructure (no RLS enabled yet)**
1. Create roles, transfer ownership, apply grants (§4.3).
2. Add DSN secrets, update `generate-env.sh`.
3. Add `Pools` struct, `BeginTenantTx`, `NewAdminQuerier`, marker interfaces to `go/internal/db/`.
4. Fix pre-condition atomicity bugs (UpsertManagedAppParams, PublishDefinition, UpsertOIDCUser).
5. Run full test suite. Deploy. Verify pool connectivity for both roles.

**Phase B — Low-blast-radius tables**
Tables: `mcp_servers`, `tenant_group_mappings`, `agent_definitions`, `agent_runtime_specs`.
These have a small number of DAL callers and no child table complications.
1. Migrate callers (DAL functions + their call sites in handlers/services).
2. Deploy. Smoke test.
3. Enable RLS. Run integration tests.

**Phase C — Core admin CRUD tables**
Tables: `agents`, `orchestrators`, `applications`, `entry_points`, `access_tokens`,
`application_definitions`, `component_definitions`.
1. Migrate callers. Note: `entry_points` uses `tenant_id` directly despite also having
   `application_id` — use the direct policy.
2. Deploy. Smoke test admin CRUD flows.
3. Enable RLS. Run `TestRLS_TwoTenantFullIsolation`.

**Phase D — Child tables for core admin (after Phase C)**
Tables: `app_agent_bindings`, `app_orchestrators`, `app_mcp_credentials`, `middleware_wirings`.
These join through `applications`, which already has RLS from Phase C.
1. Migrate callers to use TenantTx.
2. Deploy. Enable RLS via EXISTS subquery policies.

**Phase E — Run and task tables**
Tables: `runs`, `run_artifacts`, `tasks` (after backfill), `quarantine_artifacts`,
`managed_app_bindings`.
1. Tasks backfill must be complete before this phase.
2. Migrate `runrecorder` callers to use TenantTx.
3. Migrate agent-runtime, workerconfig, and dag-worker tenant-scoped queries.
4. Deploy. Enable RLS.

**Phase F — Child run/task tables**
Tables: `run_steps`, `run_usage`, `artifacts`, `task_messages`, `middleware_audit`.
1. These are protected via EXISTS subquery through their parents (now RLS-enabled).
2. Enable RLS policies. Deploy. Verify.

**Phase G — LLM providers, split policy**
Table: `llm_providers`.
1. Migrate callers to use TenantTx for tenant paths, AdminQuerier for platform paths.
2. Enable split RLS policy (§5.3). Verify platform defaults still visible.

**Phase H — Remaining**
Tables: `llm_providers` (full rollout), `audit_logs`, `managed_app_bindings`.
Migrate `middleware_jobs` gateway path to AdminQuerier (§5.6).
Migrate authserver `them.*` reads to `Pools.Admin` (§2.16).
Migrate appliveness and reconciler to AdminQuerier (§2.22, §2.23).

### 8.4 Verification steps after each phase

1. `go test ./...` — zero failures.
2. `go test -tags=integration ./...` — zero failures.
3. `TestRLS_TwoTenantFullIsolation` against live DB.
4. `docker logs them-go-bridge` — no unexpected 500s.
5. E2E test suite: `ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14` — all pass.
6. Confirm via `psql` as `them_app` that rows outside current tenant are inaccessible.

### 8.5 Rollback plan

**Important invariant:** The rollback plan never re-adds `BYPASSRLS` to `them_app`. Doing so
would silently disable the isolation guarantee without any visible signal. Once `them_app` is
created without BYPASSRLS, that attribute must not be added.

**Per-table rollback (if one phase fails after RLS is enabled):**
```sql
ALTER TABLE them.<table> DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS <table>_tenant_isolation ON them.<table>;
-- FORCE RLS is harmless without a policy; can be left or removed:
ALTER TABLE them.<table> NO FORCE ROW LEVEL SECURITY;
```
The application continues running. `them_app` still has no BYPASSRLS, but without an active RLS
policy, it sees all rows it has table-level grants on — same as today's behavior (application-
layer WHERE clauses provide isolation).

**Full rollback (revert Phase A before any RLS was enabled):**
1. Revert DSN split: point `Pools.App` and `Pools.Admin` to the same `them` (original) DSN.
2. Drop `them_app` and `them_admin` roles if they have no active connections.
3. Table ownership remains with `them_owner` (NOLOGIN — safe).
4. Revert Go code changes (one commit to revert).

Notably: the `them_owner` NOLOGIN change is non-reversible in normal operation — it should
not need to be reversed, and reversing it would require explicit `ALTER ROLE them_owner LOGIN`
by a superuser.

### 8.6 Definition of done for Step 19

- [ ] All tables in §5.1 marked "YES" for direct RLS have `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY` set
- [ ] All child tables have EXISTS-based policies enabled
- [ ] `them_app` role has no BYPASSRLS; confirmed by `SELECT rolbypassrls FROM pg_roles WHERE rolname = 'them_app'` = `f`
- [ ] `them_owner` has NOLOGIN; confirmed by `SELECT rolcanlogin FROM pg_roles WHERE rolname = 'them_owner'` = `f`
- [ ] All callers use `TenantTx` or `AdminQuerier` — no raw pool passed to DAL
- [ ] All integration tests RLS-01 through RLS-40 pass
- [ ] `TestRLS_TwoTenantFullIsolation` is a permanent member of the integration suite
- [ ] `docs/SCHEMA.md` updated with RLS status per table
- [ ] `go/TEST_INDEX.md` updated with new integration tests
- [ ] `docs/CURRENT.md` updated with Step 19 status
- [ ] `docs/HANDOVER.md` updated for next session
- [ ] Zero new test failures in `go test ./...`

---

## Appendix A — Atomicity Bugs to Fix Before Step 19

### A.1 `UpsertManagedAppParams` (`go/internal/admin/dal/managed_apps.go`)

**Current:** DELETE then INSERT in a loop with no transaction wrapper. A crash between the
DELETE and the last INSERT leaves the table in a partially-updated state.
**Fix:** Wrap the entire operation in a transaction from `Pools.Admin` (it is a platform-global
operation). Use a standard `pool.Begin(ctx)` from the admin pool, not a TenantTx.

### A.2 `PublishDefinition` (`go/internal/admin/dal/publish.go`)

**Current:** Two sequential UPDATEs with a comment noting "may be a transaction." If the second
UPDATE fails, the first is committed — partial publish state.
**Fix:** Wrap both UPDATEs in an explicit `BeginTenantTx`. This is a tenant-scoped operation.

### A.3 `UpsertOIDCUser` (`go/internal/authserver/oidc_store.go`)

**Current:** 4+ sequential queries (role lookups, user upsert, membership upsert) without a
transaction. Concurrent OIDC logins for the same email can produce duplicate users or
inconsistent membership rows.
**Fix:** Wrap all queries in a single `pool.Begin(ctx)` from the main (or admin) pool. These
queries touch only `auth_service.*` tables — no TenantTx or `app.tenant_id` needed.

---

## Appendix B — `tasks.tenant_id` Backfill

**Current state:** `them.tasks.tenant_id` is nullable. There are currently 27 NULL rows from
before multi-tenancy was introduced. These rows belong to the bootstrap tenant
(`00000000-0000-0000-0000-000000000001`).

**Migration file:** `db/060_tasks_tenant_backfill.sql`

```sql
-- Step 1: backfill
UPDATE them.tasks
SET    tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE  tenant_id IS NULL;

-- Step 2: add NOT NULL constraint
ALTER TABLE them.tasks ALTER COLUMN tenant_id SET NOT NULL;
```

Apply before Phase E. Verify with:
```sql
SELECT COUNT(*) FROM them.tasks WHERE tenant_id IS NULL;
-- Expected: 0
```

---

*End of document. v2.*
