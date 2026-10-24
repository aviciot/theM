# RLS Option A — Full Row-Level Security Design Plan
# the-M multi-agent orchestration platform
# Author: Avi Cohen / chaya.friedman@shift4.com
# Created: 2026-09-03
# Revised: 2026-09-03 (v3 — fixes import cycle, component_definitions policies,
#           role/grant accuracy, middleware enqueue scope, caller deployment matrix,
#           cancellation handling, test design, PgBouncer prepared-statement facts)
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
migrations; it has `NOLOGIN` and `NOBYPASSRLS` and is never used as an application DSN.

RLS policies enforce `tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid`.
Child tables without a direct `tenant_id` get explicit EXISTS-based policies — FK relationships
provide no implicit RLS protection.

Type-level pool separation is achieved via a thin `go/internal/dbtype/` package that breaks the
import cycle between `db` and `dal`.

---

## 1. Transaction Architecture

### 1.1 Package dependency graph — resolving the import cycle

v2 placed `TenantQuerier` and `AdminQuerier` in `go/internal/db/`, which embeds `dal.Querier`
from `go/internal/admin/dal/`. DAL functions accepting `TenantQuerier` would then import `db`,
creating a cycle: `db → dal → db`. Additionally, unexported marker methods cannot be
implemented by structs in other packages.

**Solution: introduce `go/internal/dbtype/`**

This package contains only interfaces. It imports nothing beyond the standard library.

```
dbtype   ← stdlib only
db       → dbtype          (creates Pools; TenantTx and adminQuerier implement dbtype interfaces)
dal      → dbtype          (DAL functions accept dbtype.TenantQuerier / dbtype.AdminQuerier)
handlers → db, dal, dbtype (obtains *TenantTx from db.Pools.BeginTenantTx; passes to dal)
```

`go/internal/dbtype/dbtype.go` exports:

```go
package dbtype

import (
    "context"
    pgx "github.com/jackc/pgx/v5"
)

// Querier is the base query-execution interface. Implemented by pgxpool.Pool,
// pgx.Tx, and any test fake. All DAL functions accept a subtype of this.
type Querier interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// TenantQuerier marks a Querier that has had app.tenant_id set for the
// current transaction via BeginTenantTx. Only db.Pools.BeginTenantTx produces one.
// DAL functions for tenant-scoped operations accept this type.
type TenantQuerier interface {
    Querier
    IsTenantQuerier() struct{} // exported marker — prevents AdminQuerier from satisfying this
}

// AdminQuerier marks a Querier backed by the BYPASSRLS admin pool.
// Only db.Pools.NewAdminQuerier and db.Pools.BeginAdminTx produce one.
// DAL functions for cross-tenant/admin operations accept this type.
type AdminQuerier interface {
    Querier
    IsAdminQuerier() struct{} // exported marker — prevents TenantQuerier from satisfying this
}
```

The exported marker methods (`IsTenantQuerier`, `IsAdminQuerier`) return `struct{}` and serve
only as compile-time tags. Any package can implement them, but the goal is not unforgeability
— it is that `TenantQuerier` and `AdminQuerier` are distinct types at compile time so the
compiler rejects wrong-pool wiring.

`go/internal/admin/dal/dal.go` imports `dbtype` and removes its own `Querier` definition.
Existing DAL functions that previously accepted `dal.Querier` are updated to accept
`dbtype.TenantQuerier` (tenant-scoped) or `dbtype.AdminQuerier` (admin-scoped).

### 1.2 Concrete types in `go/internal/db/`

```go
// TenantTx wraps pgx.Tx. Produced only by Pools.BeginTenantTx (App pool).
type TenantTx struct{ tx pgx.Tx }
func (t *TenantTx) IsTenantQuerier() struct{} { return struct{}{} }
func (t *TenantTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *TenantTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
// Query / QueryRow / Exec delegate to t.tx

// AdminTx wraps pgx.Tx from the Admin pool. Produced only by Pools.BeginAdminTx.
type AdminTx struct{ tx pgx.Tx }
func (a *AdminTx) IsAdminQuerier() struct{} { return struct{}{} }
func (a *AdminTx) Commit(ctx context.Context) error   { return a.tx.Commit(ctx) }
func (a *AdminTx) Rollback(ctx context.Context) error { return a.tx.Rollback(ctx) }
// Query / QueryRow / Exec delegate to a.tx

// adminQuerier wraps the Admin pool for non-transactional admin queries.
type adminQuerier struct{ pool *pgxpool.Pool }
func (a *adminQuerier) IsAdminQuerier() struct{} { return struct{}{} }
// Query / QueryRow / Exec delegate to a.pool
```

### 1.3 `Pools` struct

```go
type Pools struct {
    App   *pgxpool.Pool  // them_app — no BYPASSRLS; tenant-scoped ops
    Admin *pgxpool.Pool  // them_admin — BYPASSRLS; admin/platform/cross-tenant ops
}

// BeginTenantTx acquires a connection from the App pool and sets app.tenant_id
// for the duration of the transaction. tenantID comes from JWT claims only —
// callers must not pass values derived from HTTP headers or query parameters.
func (p *Pools) BeginTenantTx(ctx context.Context, tenantID uuid.UUID) (*TenantTx, error)

// BeginAdminTx acquires a connection from the Admin pool and begins a transaction.
// No app.tenant_id is set. Use for atomic admin operations (e.g. DELETE+INSERT pairs).
func (p *Pools) BeginAdminTx(ctx context.Context) (*AdminTx, error)

// NewAdminQuerier returns a non-transactional AdminQuerier backed by the Admin pool.
// Use for single-statement admin reads or writes that do not need atomicity.
func (p *Pools) NewAdminQuerier() dbtype.AdminQuerier
```

### 1.4 Transaction lifecycle

**BeginTenantTx:**
1. `p.App.Begin(ctx)` — acquires from the App pool (no BYPASSRLS).
2. Execute `SELECT set_config('app.tenant_id', $1, true)` with `tenantID.String()` as the
   parameterised argument. Third argument `true` = transaction-local; resets on COMMIT/ROLLBACK.
3. If the set_config call fails: call `tx.Rollback` with a cleanup context (not the caller's
   ctx — see §1.5), return the error.
4. Return `*TenantTx`.

**BeginAdminTx:**
1. `p.Admin.Begin(ctx)` — acquires from the Admin pool (BYPASSRLS).
2. No set_config call. Return `*AdminTx`.

**Call-site pattern (TenantTx):**
```go
tx, err := pools.BeginTenantTx(ctx, tenantID)
if err != nil { return err }
defer func() {
    cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    tx.Rollback(cleanupCtx) // no-op after Commit
}()

result, err := dal.ListAgents(ctx, tx)
if err != nil { return err }

return tx.Commit(ctx)
```

**Call-site pattern (AdminTx):**
```go
tx, err := pools.BeginAdminTx(ctx)
if err != nil { return err }
defer func() {
    cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    tx.Rollback(cleanupCtx)
}()

if err := dal.DeleteManagedAppParams(ctx, tx, appID); err != nil { return err }
if err := dal.InsertManagedAppParams(ctx, tx, appID, params); err != nil { return err }
return tx.Commit(ctx)
```

### 1.5 Context cancellation — corrected

v2 stated "the deferred Rollback still fires and succeeds because pgx rolls back the server
side on error." This is incorrect.

pgx does NOT automatically roll back the server-side transaction on context cancellation. When
the request context is cancelled, the in-flight query returns an error, but the server
transaction remains open on the backend connection. pgxpool marks the connection unhealthy when
it detects the cancel and destroys it on return — which causes an implicit rollback at the
server — but this is not guaranteed to complete before the `defer` fires.

More critically: `defer tx.Rollback(ctx)` with a cancelled `ctx` fails immediately because the
context is already done. pgx checks the context before issuing the network call.

**Rule:** Always use a separate cleanup context for the deferred rollback:
```go
defer func() {
    cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    tx.Rollback(cleanupCtx)
}()
```

After a cancelled context, the caller should expect `context.Canceled` or
`context.DeadlineExceeded` — not a silent zero-row result.

### 1.6 Trusted tenant-ID sources

`BeginTenantTx` accepts `uuid.UUID`, not `string`. The `.String()` conversion for the GUC
happens inside `BeginTenantTx` only. This prevents accidental string injection.

| Call context | Source of tenantID | Type |
|---|---|---|
| HTTP request handler | `tenantctx.TenantIDFromCtx(ctx)` — populated by `auth.Middleware` from JWT claims | `uuid.UUID` |
| Temporal activity | `InvocationContext.TenantID` — derived from JWT at workflow initiation | `uuid.UUID` |
| Background workers | Cross-tenant by design; must use AdminQuerier — no tenantID parameter | — |

TenantID is **never** read from HTTP headers, query parameters, or environment variables.

### 1.7 Transaction boundary rule

One transaction per handler operation, not per DAL call. A handler that calls multiple DAL
functions opens one `TenantTx` and passes it to all of them. This is the correct atomicity and
RLS correctness boundary.

---

## 2. Complete Database Access Inventory

**Scope legend:**
- `T` — tenant-scoped: `BeginTenantTx` via `Pools.App`
- `X` — cross-tenant/admin: `AdminQuerier` or `AdminTx` via `Pools.Admin`
- `A` — application-scoped (no direct `tenant_id`): `TenantTx` (parent EXISTS policy provides isolation)
- `P` — platform-global (tenant_id IS NULL): `AdminQuerier`
- `G` — global (no tenant dimension): `AdminQuerier`

### 2.1 `go/internal/admin/dal/`

| Package | Function | Scope |
|---|---|---|
| agents.go | ListAgents, GetAgent, CreateAgent, UpdateAgent, DeleteAgent | T |
| agents.go | AgentExists, GetAgentBySlug, UpdateAgentScanResult, GetAgentByID, GetAgentTokenEncrypted | X |
| orchestrators.go | All CRUD | T |
| applications.go | ListApplications…DeleteProviderKey, GetAppParams…DeleteAppParam | T |
| applications.go | ListEntryPoints, CreateEntryPoint, UpdateEntryPoint, DeleteEntryPoint, listAppOrchSummaries, SetOrchestratorLLM, SetOrchestratorVoice, SetOrchestratorMCPServers | A |
| applications.go | GetAgentSummariesByIDs | X |
| runs.go | All run/step/usage reads and writes | T |
| tokens.go | All CRUD | T |
| tenants.go | ListTenants…GetTenantByEmailDomain, GetQuota, UpsertQuota, ListMembers, AddMember | X |
| tenants.go | ListGroupMappings, UpsertGroupMapping, DeleteGroupMapping | T |
| llm_providers.go | ListProvidersForTenant, GetProviderByNameForTenant, UpsertTenantProvider | T |
| llm_providers.go | ListProviders (platform), GetProviderByNamePlatform, CreateProvider, UpdateProvider, DeleteProvider | P |
| managed_apps.go | ListManagedApps, CreateManagedApp, GetManagedApp, ListManagedAppParams, UpsertManagedAppParams ⚠ | X (AdminTx for upsert) |
| managed_apps.go | ListBindingsForTenant, GetBinding, UpsertBinding | T |
| managed_apps.go | ListBindingsByTenant (path-param), UpsertBindingByTenant (path-param) | X |
| mcp_servers.go | ListMCPServers…DeleteMCPServer | T |
| mcp_servers.go | ListAppMCPCredentials…DeleteAppMCPCredential | A |
| agent_definitions.go | All CRUD | T |
| publish.go | PublishDefinition ⚠ (two UPDATEs, needs TenantTx) | T |
| publish.go | UpsertAppOrchestrator, UpsertEntryPoint, DeactivateStale* | A/T |
| services_stats.go | GetSecurityScanStats | X |
| agent_bindings.go | GetAgentParamsForBinding, GetRequiredParamsForAgent | X |
| agent_bindings.go | Binding CRUD | A |
| config.go | GetConfig, UpsertConfig | G |

### 2.2 `go/internal/runrecorder/recorder.go`

Writes to `them.runs`, `them.run_steps`, `them.run_usage`. Must use `TenantTx` after Step 19.
Constructor must accept `dbtype.TenantQuerier`.

### 2.3 `go/internal/authserver/` (pgx.go, oidc_store.go)

- `auth_service.*` queries: no RLS needed (separate schema, them_app has no grants there).
- `GetTenantIDPConfig` (reads `them.tenants`), `GetGroupRole` (reads `them.tenant_group_mappings`):
  pre-auth path; must use `Pools.Admin` after Step 19.
- `UpsertOIDCUser` ⚠: 4+ sequential queries without a transaction; wrap in standard `pool.Begin`
  (not TenantTx — auth_service schema only).

### 2.4 `go/internal/agentregistry/pgx_querier.go`

- `GetBindingID` (reads `app_agent_bindings` by `application_id`): `TenantTx`.
- `QueryAgentsByTenant` (reads `agents WHERE tenant_id = $1`): `TenantTx`.

### 2.5 `go/internal/middleware/`

| Function | Context | Required pool |
|---|---|---|
| `EnqueueWithQuarantine` / `Enqueue` | Gateway (has tenant context) | `TenantTx` via App pool |
| `Claim` | Worker — cross-tenant | `AdminQuerier` |
| `LoadSecurityConfig` (job.go) | Worker — has application_id, no JWT tenant | `AdminQuerier` |
| `LoadFileBytes` | Worker | `AdminQuerier` |
| `Complete` / `completeQuarantinePath` / `completeLegacyPath` | Worker | `AdminTx` |
| `WriteAudit` | Worker | `AdminQuerier` |
| `LoadSecurityConfig` (gate.go) | Gateway — has tenant context | `TenantTx` |

### 2.6 `go/internal/temporal/workerconfig/loader.go`

- Tenant-scoped lookups (app runtime config, binding params, entry point config): `TenantTx`.
- Platform-global lookups (LLM provider defaults, ManagedApp catalog): `AdminQuerier`.

### 2.7 `go/cmd/agent-runtime/` (spec.go, llm.go, runtime.go)

All queries include `tenant_id` predicates. Must use `TenantTx`. `tenantID` comes from
`InvocationContext.TenantID` (JWT-derived at workflow start).

### 2.8 `go/cmd/dag-worker/main.go`

Mixed: tenant-scoped spec loads → `TenantTx`; cross-tenant activity claims → `AdminQuerier`.

### 2.9 `go/internal/appliveness/liveness.go`

`listEnabledEPSlugs`: cross-tenant health probe. Must use `AdminQuerier`.

### 2.10 `go/internal/reconciler/reconciler.go`

Cross-tenant stale-run sweep. Must use `AdminQuerier`. Uses `pg_try_advisory_lock` (session-
scoped) — see §7.4.

---

## 3. Tenant Context Setup

### 3.1 The `set_config` call

```sql
SELECT set_config('app.tenant_id', $1, true)
```

- `$1` is `tenantID.String()` — a validated UUID string, never user-controlled input.
- Third argument `true` = transaction-local. Resets to `''` on COMMIT or ROLLBACK.
- Session default is never set by the application; it is always `''`.

### 3.2 Empty-string-safe policy expression

All RLS policies use:
```sql
NULLIF(current_setting('app.tenant_id', true), '')::uuid
```

| State of `app.tenant_id` | Result | Policy outcome |
|---|---|---|
| GUC never initialized (missing_ok) | `NULL` | No rows match — fail-closed |
| `''` (reset after tx end, or fresh connection) | `NULL` | No rows match — fail-closed |
| Valid UUID string | UUID value | Rows for that tenant |
| Invalid UUID string (unreachable in practice) | Cast error | Query error |

The invalid-UUID case is unreachable: `BeginTenantTx` receives a `uuid.UUID` whose `.String()`
always produces a valid UUID string.

### 3.3 Connection reuse safety

`set_config(..., true)` is strictly transaction-scoped at the Postgres server. When a
transaction ends, the server resets `app.tenant_id` to `''`. A pooled connection checked out
for a new `BeginTenantTx` call starts with `app.tenant_id = ''` → fail-closed until the new
`set_config` call executes. Tenant-A's GUC value cannot leak into tenant-B's transaction.

---

## 4. Database Roles and Ownership

### 4.1 Current state

The live `them` role is a SUPERUSER with BYPASSRLS and LOGIN. It owns all tables and is the
sole application DSN. This must be split before Step 19.

### 4.2 Target roles

| Role | Purpose | LOGIN | BYPASSRLS | SUPERUSER | Owns tables |
|---|---|---|---|---|---|
| `them_owner` | Table owner; runs migrations | **No** (NOLOGIN) | **No** | No | Yes |
| `them_admin` | Admin / cross-tenant runtime | Yes | **Yes** (explicit) | No | No |
| `them_app` | Tenant-scoped runtime | Yes | **No** | No | No |

#### them_owner: NOLOGIN and NOBYPASSRLS

`them_owner` owns the tables. Table owners bypass RLS when only `ENABLE ROW LEVEL SECURITY` is
set (not FORCE). Since `them_owner` is `NOLOGIN`, it can never be used as an application DSN —
this is the primary safety guarantee, not the BYPASSRLS attribute. `them_owner` does NOT need
and does NOT have the BYPASSRLS attribute.

`FORCE ROW LEVEL SECURITY` is applied to all tenant-scoped tables as defense in depth. It
overrides the table-owner bypass but does NOT override the BYPASSRLS role attribute.

#### FORCE ROW LEVEL SECURITY and BYPASSRLS — correct interaction

| Mechanism | What it overrides | What it does NOT override |
|---|---|---|
| `ENABLE ROW LEVEL SECURITY` | Normal table access by non-owners | Table owner implicit bypass; BYPASSRLS roles |
| `FORCE ROW LEVEL SECURITY` | Table owner implicit bypass | BYPASSRLS role attribute |

Roles with `BYPASSRLS` (like `them_admin`) skip all RLS unconditionally, regardless of
`FORCE ROW LEVEL SECURITY`. This is intentional — `them_admin` is the designated bypass role.

### 4.3 Migration path

1. Create `them_owner` (NOLOGIN, NOBYPASSRLS, NOCREATEDB, NOCREATEROLE).
2. Create `them_admin` (LOGIN, BYPASSRLS, NOCREATEDB, NOCREATEROLE, NOSUPERUSER).
3. Create `them_app` (LOGIN, NOBYPASSRLS, NOCREATEDB, NOCREATEROLE, NOSUPERUSER).
4. Transfer ownership of all `them.*` tables to `them_owner`.
5. Apply minimal grants (§4.4).
6. Revoke SUPERUSER from the `them` role; retire it after DSN migration.
7. Add `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` to `generate-env.sh`. Never commit.

### 4.4 Minimal grants for `them_app`

Exact privileges per table. No blanket `ALL` or unnecessary operations.

```sql
-- Full tenant-scoped DML:
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agents            TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.orchestrators     TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.applications      TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.entry_points      TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.access_tokens     TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.runs              TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_steps         TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_usage         TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_artifacts     TO them_app;
GRANT SELECT, INSERT, UPDATE    ON them.tasks                  TO them_app;  -- no DELETE
GRANT SELECT, INSERT, UPDATE, DELETE ON them.task_messages     TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.artifacts         TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.mcp_servers       TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_definitions TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_runtime_specs TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.application_definitions TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.managed_app_bindings TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_group_mappings TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.component_definitions TO them_app; -- RLS enforces per-cmd
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_agent_bindings   TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_orchestrators    TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_mcp_credentials  TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.middleware_wirings    TO them_app;

-- Restricted operations:
GRANT SELECT, INSERT        ON them.quarantine_artifacts TO them_app; -- no UPDATE/DELETE
GRANT INSERT                ON them.audit_logs           TO them_app; -- write-only
GRANT INSERT                ON them.middleware_jobs      TO them_app; -- gateway enqueue only

-- Read-only reference:
GRANT SELECT ON them.llm_providers  TO them_app; -- split RLS policy (§5.3)
GRANT SELECT ON them.middleware_defs TO them_app; -- builtins only; no RLS policy needed

-- them_app has NO access to:
--   them.tenants (resolved at login via admin path; not needed during request)
--   them.tenant_quotas (admin-only)
--   them.config (platform-global)
--   them.schema_migrations (internal)
--   them.managed_apps / managed_app_params (platform-global)

-- Sequences:
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO them_app;

-- Schema:
GRANT USAGE ON SCHEMA them TO them_app;
GRANT USAGE ON SCHEMA them TO them_admin;
GRANT USAGE ON SCHEMA auth_service TO them_admin;

-- them_admin: full DML on everything (BYPASSRLS; policies don't apply):
GRANT ALL ON ALL TABLES IN SCHEMA them TO them_admin;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO them_admin;
GRANT ALL ON ALL TABLES IN SCHEMA auth_service TO them_admin;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth_service TO them_admin;
```

### 4.5 Verification queries

```sql
SELECT rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname = 'them_app';
-- Expected: f, t

SELECT rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname = 'them_owner';
-- Expected: f, f

SELECT rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname = 'them_admin';
-- Expected: t, t

SELECT tableowner FROM pg_tables WHERE schemaname = 'them' AND tablename = 'agents';
-- Expected: them_owner
```

---

## 5. RLS Policies

### 5.1 Principle: FK provides no implicit RLS protection

A foreign key from `run_steps.run_id → runs.id` does not cause Postgres to consult `runs`
policies when querying `run_steps`. Each table's rows are filtered by policies on **that table
only**. Every child table in this design gets an explicit RLS policy or has them_app access
revoked entirely.

### 5.2 Full table classification

| Table | tenant_id | RLS strategy | See |
|---|---|---|---|
| `agents` | NOT NULL | Standard direct policy | §5.4 |
| `orchestrators` | NOT NULL | Standard | §5.4 |
| `applications` | NOT NULL | Standard | §5.4 |
| `entry_points` | NOT NULL | Standard (direct preferred over subquery) | §5.4 |
| `access_tokens` | NOT NULL | Standard | §5.4 |
| `runs` | NOT NULL | Standard | §5.4 |
| `run_artifacts` | NOT NULL | Standard | §5.4 |
| `tasks` | Nullable → backfill required | Standard after backfill | §5.4, Appendix B |
| `quarantine_artifacts` | NOT NULL | Standard | §5.4 |
| `agent_definitions` | NOT NULL | Standard | §5.4 |
| `agent_runtime_specs` | NOT NULL | Standard | §5.4 |
| `application_definitions` | NOT NULL | Standard | §5.4 |
| `managed_app_bindings` | NOT NULL | Standard | §5.4 |
| `audit_logs` | Nullable (NULL = platform event) | Standard; NULL rows visible only to admin | §5.4 |
| `mcp_servers` | NOT NULL | Standard | §5.4 |
| `tenant_group_mappings` | NOT NULL | Standard | §5.4 |
| `llm_providers` | Nullable (NULL = platform) | Split by command | §5.5 |
| `component_definitions` | Nullable (NULL = builtin) | Per-command split | §5.6 |
| `run_steps` | None (→ run_id → runs) | EXISTS via runs | §5.7 |
| `run_usage` | None (→ run_id → runs) | EXISTS via runs | §5.7 |
| `artifacts` | None (→ task_id → tasks) | EXISTS via tasks | §5.7 |
| `task_messages` | None (→ task_id → tasks) | EXISTS via tasks | §5.7 |
| `middleware_audit` | None (→ artifact_id → run_artifacts) | EXISTS via run_artifacts | §5.7 |
| `app_agent_bindings` | None (→ application_id → applications) | EXISTS via applications | §5.7 |
| `app_orchestrators` | None (→ application_id) | EXISTS via applications | §5.7 |
| `app_mcp_credentials` | None (→ application_id) | EXISTS via applications | §5.7 |
| `middleware_wirings` | None (→ application_id) | EXISTS via applications | §5.7 |
| `middleware_jobs` | None (→ application_id) | INSERT-only policy for them_app | §5.8 |
| `managed_app_params` | None (→ managed app) | No them_app grants | §4.4 |
| `tenants` | N/A | No RLS; them_app has no grants | §4.4 |
| `tenant_quotas` | PK=tenant_id | No RLS; them_app has no grants | §4.4 |
| `config` | None | No RLS; them_app has no grants | §4.4 |
| `schema_migrations` | None | No RLS; them_app has no grants | §4.4 |
| `middleware_defs` | None | No RLS; them_app has SELECT only | §4.4 |

### 5.3 FORCE ROW LEVEL SECURITY

Applied to every table where them_app has grants and a policy exists:
```sql
ALTER TABLE them.<table> ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.<table> FORCE ROW LEVEL SECURITY;
```

FORCE overrides the table-owner bypass (them_owner). It does NOT affect them_admin (BYPASSRLS).

### 5.4 Standard direct policy

```sql
ALTER TABLE them.<table> ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.<table> FORCE ROW LEVEL SECURITY;

CREATE POLICY <table>_tenant_isolation ON them.<table>
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

`TO them_app` limits the policy to the app role. them_admin (BYPASSRLS) is never subject to it.

### 5.5 Split policy: `them.llm_providers`

```sql
ALTER TABLE them.llm_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.llm_providers FORCE ROW LEVEL SECURITY;

-- SELECT: tenant's own rows OR platform defaults
CREATE POLICY llm_providers_select ON them.llm_providers AS PERMISSIVE FOR SELECT TO them_app
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR tenant_id IS NULL
    );

-- INSERT: tenant rows only (no platform rows)
CREATE POLICY llm_providers_insert ON them.llm_providers AS PERMISSIVE FOR INSERT TO them_app
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- UPDATE: tenant's own rows only
CREATE POLICY llm_providers_update ON them.llm_providers AS PERMISSIVE FOR UPDATE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- DELETE: tenant's own rows only
CREATE POLICY llm_providers_delete ON them.llm_providers AS PERMISSIVE FOR DELETE TO them_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

### 5.6 Per-command policies: `them.component_definitions`

A single combined policy is insufficient here. `USING` in a combined policy matches rows for
the command's scope — builtins (tenant_id IS NULL) would be visible to UPDATE and DELETE via
USING, allowing modification of non-key columns or deletion unless blocked by WITH CHECK.
WITH CHECK does not apply to DELETE. Four separate policies are required:

```sql
ALTER TABLE them.component_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.component_definitions FORCE ROW LEVEL SECURITY;

-- SELECT: tenant's own rows + builtins
CREATE POLICY component_definitions_select ON them.component_definitions
    AS PERMISSIVE FOR SELECT TO them_app
    USING (
        tenant_id IS NULL
        OR tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    );

-- INSERT: tenant rows only (never builtins)
CREATE POLICY component_definitions_insert ON them.component_definitions
    AS PERMISSIVE FOR INSERT TO them_app
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- UPDATE: only tenant's own rows; result must remain tenant's own rows
CREATE POLICY component_definitions_update ON them.component_definitions
    AS PERMISSIVE FOR UPDATE TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- DELETE: only tenant's own rows (builtins protected via USING)
CREATE POLICY component_definitions_delete ON them.component_definitions
    AS PERMISSIVE FOR DELETE TO them_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

### 5.7 EXISTS-based policies for child tables

```sql
-- run_steps (parent: runs)
ALTER TABLE them.run_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.run_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY run_steps_tenant_isolation ON them.run_steps AS PERMISSIVE TO them_app
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

-- run_usage: identical pattern (run_id → runs)
-- artifacts: task_id → tasks
-- task_messages: task_id → tasks
-- middleware_audit: artifact_id → run_artifacts
-- app_agent_bindings, app_orchestrators, app_mcp_credentials, middleware_wirings:
--   application_id → applications
```

Full SQL for each follows the same template. Verify with `EXPLAIN` after enablement — the
planner should use an index scan on the parent FK column, not a seq scan.

### 5.8 `them.middleware_jobs` — INSERT-only for them_app, full admin access for worker

The gateway enqueues jobs in a tenant-aware request context → uses `TenantTx` (them_app).
The worker claims and processes jobs cross-tenant → uses `AdminQuerier`/`AdminTx` (them_admin).

```sql
ALTER TABLE them.middleware_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.middleware_jobs FORCE ROW LEVEL SECURITY;

-- them_app may only insert jobs for its own applications
CREATE POLICY middleware_jobs_enqueue ON them.middleware_jobs
    AS PERMISSIVE FOR INSERT TO them_app
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_jobs.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- No SELECT/UPDATE/DELETE policy for them_app. Worker uses them_admin (BYPASSRLS).
```

them_app has INSERT only on middleware_jobs (§4.4). them_admin has full DML (BYPASSRLS).

### 5.9 Fail-closed behavior summary

| State | SELECT | INSERT/UPDATE | Notes |
|---|---|---|---|
| GUC not set / empty string | 0 rows | Rejected | Fail-closed |
| Valid UUID, wrong tenant | 0 rows | Rejected | Cross-tenant blocked |
| Valid UUID, correct tenant | Matching rows | Allowed | Normal |
| them_admin (BYPASSRLS) | All rows | Allowed | Intentional bypass |

---

## 6. Test Plan

### 6.1 Test infrastructure requirements

**Integration tests use real production schema.** Tests run against a live Postgres instance
that has been initialized with the actual `db/001_schema.sql` + all numbered migrations + the
production migration that creates `them_owner`, `them_admin`, `them_app` and applies the grants
from §4.4. Tests must NOT create their own ad-hoc tables or roles — the test schema must match
production exactly.

**Build tag:** `integration`. Location: `go/internal/db/rls_integration_test.go`.

**Unit tests** (no live DB) cover:
- `TenantTx` and `adminQuerier` implement the correct dbtype interfaces.
- `BeginTenantTx` returns error and rolls back when set_config fails (mock pool).
- Deferred Rollback with cancelled context uses cleanup context (does not hang).
- DAL function type mismatches fail at compile time (verified by `go build ./...`).

### 6.2 Integration test table

#### Direct-tenant-id table tests

| ID | Description | Assertion |
|---|---|---|
| RLS-01 | TenantTx for tenant-A: SELECT agents | Only tenant-A rows |
| RLS-02 | TenantTx for tenant-B: SELECT agents | Only tenant-B rows |
| RLS-03 | TenantTx for tenant-A: SELECT agent owned by tenant-B | 0 rows |
| RLS-04 | TenantTx for tenant-A: INSERT agent with tenant_id = tenant-B | WITH CHECK error |
| RLS-05 | TenantTx for tenant-A: UPDATE agent owned by tenant-B | 0 rows updated |
| RLS-06 | TenantTx for tenant-A: DELETE agent owned by tenant-B | 0 rows deleted |
| RLS-07 | AdminQuerier: SELECT all agents (no set_config) | All rows returned |
| RLS-08 | them_app raw pool query without BeginTenantTx | 0 rows (fail-closed) |
| RLS-09 | them_app with app.tenant_id explicitly set to '' | 0 rows (fail-closed) |

#### Connection reuse tests — MaxConns=1 pool required

Tests RLS-10 through RLS-15 must use a pgxpool configured with `MaxConns: 1` to guarantee
physical connection reuse. Without this, a test may happen to get a fresh connection, pass
accidentally, and fail to verify actual GUC isolation.

| ID | Description | Assertion |
|---|---|---|
| RLS-10 | Fresh connection, no set_config: SELECT | 0 rows (fail-closed) |
| RLS-11 | tx-A (tenant-A) commits; raw query on same connection | 0 rows (GUC reset to '') |
| RLS-12 | tx-A (tenant-A) then tx-B (tenant-B) on same connection | tx-B sees only tenant-B rows |
| RLS-13 | tx-A rolled back; tx-B on same connection | tx-B sees only tenant-B rows |
| RLS-14 | Panic + recover inside tx-A; deferred Rollback fires; tx-B on same connection | Correct isolation |
| RLS-15 | Context cancelled mid-query; expect context.Canceled from query; new ctx tx-B sees no leaked GUC | GUC not leaked; tx-B isolated |

RLS-15 specifically: cancel context, confirm the query returns `context.Canceled` (or
`context.DeadlineExceeded`), then open a new transaction with a fresh context and verify
the previous tenant's ID is not visible.

#### Child table tests

| ID | Description | Assertion |
|---|---|---|
| RLS-16 | TenantTx-A: SELECT run_steps for tenant-A run | Own steps visible |
| RLS-17 | TenantTx-A: SELECT run_steps for tenant-B run | 0 rows |
| RLS-18 | TenantTx-A: INSERT run_steps with tenant-B run_id | EXISTS subquery blocks — error |
| RLS-19 | TenantTx-A: run_usage for tenant-B run | 0 rows |
| RLS-20 | TenantTx-A: artifacts for tenant-B task | 0 rows |
| RLS-21 | TenantTx-A: task_messages for tenant-B task | 0 rows |
| RLS-22 | TenantTx-A: middleware_audit for tenant-B artifact | 0 rows |
| RLS-23 | TenantTx-A: app_agent_bindings for tenant-B application | 0 rows |
| RLS-24 | TenantTx-A: app_orchestrators for tenant-B application | 0 rows |

#### Split and per-command policy tests

| ID | Description | Assertion |
|---|---|---|
| RLS-25 | llm_providers: tenant-A reads own + platform (NULL) rows | Both visible |
| RLS-26 | llm_providers: tenant-A INSERT row with tenant_id = NULL | WITH CHECK error |
| RLS-27 | llm_providers: tenant-A cannot read tenant-B rows | 0 rows |
| RLS-28 | component_definitions: tenant-A reads own + builtin rows | Both visible |
| RLS-28b | component_definitions: tenant-A UPDATE a builtin row | 0 rows affected (USING blocks) |
| RLS-28c | component_definitions: tenant-A DELETE a builtin row | 0 rows deleted (USING blocks) |
| RLS-28d | component_definitions: tenant-A UPDATE own row to set tenant_id = NULL | WITH CHECK error |
| RLS-28e | component_definitions: tenant-A INSERT builtin (NULL tenant_id) | WITH CHECK error |
| RLS-29 | middleware_jobs: tenant-A INSERT job for own application | Succeeds |
| RLS-29b | middleware_jobs: tenant-A INSERT job for tenant-B application | EXISTS check error |
| RLS-29c | middleware_jobs: tenant-A SELECT middleware_jobs | 0 rows (no SELECT grant) |

#### Role attribute tests

| ID | Description | Assertion |
|---|---|---|
| RLS-30 | them_app: rolbypassrls | false |
| RLS-31 | them_owner: rolcanlogin | false |
| RLS-31b | Direct connect attempt as them_owner | Authentication error (NOLOGIN) |
| RLS-32 | them_admin: rolbypassrls | true |
| RLS-33 | them_admin SELECT with no set_config | All rows returned (BYPASSRLS confirmed) |

#### Wrong-pool wiring (compile-time)

| ID | Description | Assertion |
|---|---|---|
| RLS-34 | Pass `AdminQuerier` to tenant-DAL function | Compile error |
| RLS-35 | Pass `*TenantTx` to admin-DAL function | Compile error |
| RLS-36 | Pass `*pgxpool.Pool` to either typed DAL function | Compile error |

These are verified by `go build ./...` — no runtime test needed.

#### Atomicity tests

| ID | Description | Assertion |
|---|---|---|
| RLS-37 | UpsertManagedAppParams via AdminTx: crash between DELETE and INSERT | Original params intact after rollback |
| RLS-38 | PublishDefinition via TenantTx: second UPDATE fails | First UPDATE rolled back |

#### Deployment ordering / regression detection

| ID | Description | Assertion |
|---|---|---|
| RLS-39 | **Deploy ordering violation detector**: old-path query (no TenantTx) after RLS enabled on that table | Returns 0 rows — this is a **BUG**, not "rollout safe." If this test passes, it means RLS was enabled before the caller was migrated. Fix: migrate and redeploy the caller before enabling RLS. |
| RLS-40 | TenantTx query on table where RLS is NOT YET enabled | Returns all matching rows — expected during migration window |

RLS-39 exists to detect the failure mode: if it triggers (returns 0 rows from an old path),
the deployment ordering rule was violated. It is not a pass condition.

### 6.3 Two-tenant full isolation regression test

`TestRLS_TwoTenantFullIsolation` is a permanent member of the integration suite:
1. Inserts rows for two tenants across every RLS-protected table.
2. As tenant-A, reads every table — asserts zero tenant-B rows in all.
3. As tenant-B, reads every table — asserts zero tenant-A rows in all.
4. Attempts cross-tenant INSERT on every protected table — asserts all rejected.

Must pass on every PR that touches the DB layer.

### 6.4 Fake helpers

- `dbtype.FakeTenantQuerier`: implements `dbtype.TenantQuerier` with in-memory row storage.
  Used in unit tests that don't need a real DB.
- `dbtype.FakeAdminQuerier`: same for admin DAL unit tests.
- Both live in `go/internal/dbtype/fake_test.go` (test-only file).

---

## 7. PgBouncer Assessment

### 7.1 Is PgBouncer justified?

No independent measurement of connection exhaustion exists. The stack has ~6 services, each
with a pgxpool of up to 4 connections — well below the Postgres default of 100. **PgBouncer
is out of scope for Step 19.**

### 7.2 pgx/v5 prepared statements and PgBouncer — current facts

pgx/v5 defaults to `QueryExecModeCacheStatement` (extended query protocol, automatic named
prepared statement caching). The compatibility with PgBouncer depends on version:

| PgBouncer version | Transaction mode + pgx default | Notes |
|---|---|---|
| < 1.21 | Incompatible | Named prepared statements are session-scoped; transaction mode doesn't guarantee same server connection per named statement |
| ≥ 1.21 | Compatible when `max_prepared_statements > 0` | PgBouncer 1.21+ added protocol-level prepared statement tracking — it proxies Parse/Bind/Execute and maintains a per-server PS cache |

If PgBouncer < 1.21 is ever used, configure pgx with:
```go
cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
// or pgx.QueryExecModeDescribeExec
```

Current direct-connection setup has no incompatibility. This section is informational only.

### 7.3 `set_config` compatibility

`set_config('app.tenant_id', $1, true)` is transaction-scoped. It is compatible with PgBouncer
transaction mode (each transaction holds the server connection for its duration; GUC resets on
commit/rollback). Incompatible only with PgBouncer statement mode (no transaction support).

### 7.4 Advisory locks — PgBouncer blocker

`go/internal/reconciler/reconciler.go` uses `pg_try_advisory_lock` / `pg_advisory_unlock` via
session-scoped calls. Session advisory locks are incompatible with PgBouncer transaction mode
(the lock is held on a specific server connection; subsequent calls may route to a different
connection). If PgBouncer is added: migrate to `pg_advisory_xact_lock` or a Redis-based lock.
This does not affect Step 19.

---

## 8. Deployment Ordering

### 8.1 The invariant

**Every caller of a table must be migrated to use the correct pool/transaction AND deployed
before RLS is enabled on that table.** Enabling RLS before migrating a caller causes silent
data loss (0 rows instead of an error) in that caller's path — a live regression.

### 8.2 Caller-to-table dependency matrix

For each table group, every caller must use the stated pool/tx before the migration step runs.

| Table(s) | Callers | Required pool/tx | Containers to deploy first |
|---|---|---|---|
| `tenant_group_mappings` | admin DAL (TenantTx); authserver `GetGroupRole` (AdminQuerier) | TenantTx + AdminQuerier | **them-go-bridge** + **them-auth-go** |
| `mcp_servers` | admin DAL | TenantTx | them-go-bridge |
| `agent_definitions`, `agent_runtime_specs`, `application_definitions` | admin DAL | TenantTx | them-go-bridge |
| `agents` | admin DAL (TenantTx); agent-runtime (TenantTx); workerconfig (TenantTx); dag-worker (TenantTx) | TenantTx | **them-go-bridge** + **them-agent-runtime** + **them-dag-worker** |
| `orchestrators` | admin DAL | TenantTx | them-go-bridge |
| `applications` | admin DAL (TenantTx); agent-runtime (TenantTx); workerconfig (TenantTx); dag-worker (TenantTx); appliveness (AdminQuerier); middleware gate.go (TenantTx); middleware job.go (AdminQuerier) | TenantTx + AdminQuerier | **them-go-bridge** + **them-agent-runtime** + **them-dag-worker** + **them-middleware-worker** |
| `entry_points` | admin DAL (TenantTx); appliveness (AdminQuerier) | TenantTx + AdminQuerier | **them-go-bridge** |
| `access_tokens` | admin DAL | TenantTx | them-go-bridge |
| `runs` | admin DAL (TenantTx); runrecorder (TenantTx); reconciler (AdminQuerier) | TenantTx + AdminQuerier | **them-go-bridge** (contains reconciler + runrecorder) |
| `run_artifacts` | admin DAL (TenantTx); runrecorder (TenantTx); middleware worker (AdminTx) | TenantTx + AdminTx | them-go-bridge + them-middleware-worker |
| `tasks` | admin DAL (TenantTx); runrecorder (TenantTx) | TenantTx | them-go-bridge |
| `quarantine_artifacts` | admin DAL (TenantTx); middleware worker (AdminTx) | TenantTx + AdminTx | them-go-bridge + them-middleware-worker |
| `managed_app_bindings` | admin DAL | TenantTx | them-go-bridge |
| `run_steps`, `run_usage` | admin DAL (TenantTx); runrecorder (TenantTx); agent-runtime (TenantTx) | TenantTx | **them-go-bridge** + **them-agent-runtime** |
| `artifacts`, `task_messages` | admin DAL (TenantTx) | TenantTx | them-go-bridge |
| `middleware_audit` | middleware worker (AdminQuerier) | AdminQuerier | them-middleware-worker |
| `app_agent_bindings`, `app_orchestrators`, `app_mcp_credentials`, `middleware_wirings` | admin DAL (TenantTx); agentregistry (TenantTx) | TenantTx | them-go-bridge |
| `middleware_jobs` | middleware gateway enqueue (TenantTx); worker Claim/Complete (AdminQuerier/AdminTx) | TenantTx + AdminQuerier | them-go-bridge + them-middleware-worker |
| `llm_providers` | admin DAL (T+P split); workerconfig (TenantTx + AdminQuerier) | TenantTx + AdminQuerier | them-go-bridge + them-dag-worker |
| `audit_logs` | admin DAL (INSERT via TenantTx) | TenantTx | them-go-bridge |

### 8.3 Derived execution phases

Each phase follows the rule: **Deploy all containers listed → then enable RLS**.

**Phase A — Infrastructure (no RLS enabled)**
1. Create roles, transfer ownership, apply grants (§4.3, §4.4).
2. Add DSN secrets; update `generate-env.sh`.
3. Implement `dbtype` package, `Pools` struct, `BeginTenantTx`, `BeginAdminTx`, `NewAdminQuerier`.
4. Fix atomicity bugs: UpsertManagedAppParams (AdminTx), PublishDefinition (TenantTx), UpsertOIDCUser (pool.Begin).
5. Run `go test ./...`. Deploy. Verify both pool DSNs connect.

**Phase B — Auth-go tables**
Tables: `tenant_group_mappings`.
Deploy: `them-go-bridge` + `them-auth-go` (authserver GetGroupRole → AdminQuerier).
Then: enable RLS.

**Phase C — Low-complexity tables**
Tables: `mcp_servers`, `agent_definitions`, `agent_runtime_specs`, `application_definitions`,
`orchestrators`, `access_tokens`.
Deploy: `them-go-bridge`.
Then: enable RLS.

**Phase D — Core tenant tables**
Tables: `agents`, `applications`, `entry_points`.
All four containers have callers. Deploy: `them-go-bridge` + `them-agent-runtime` +
`them-dag-worker`.
Wait for all containers healthy.
Then: enable RLS. Run `TestRLS_TwoTenantFullIsolation`.

**Phase E — Application-child tables**
Tables: `app_agent_bindings`, `app_orchestrators`, `app_mcp_credentials`, `middleware_wirings`.
Parent (`applications`) already has RLS from Phase D.
Deploy: `them-go-bridge` (agentregistry migration).
Then: enable EXISTS policies.

**Phase F — Run and task tables**
Pre-condition: tasks backfill complete (Appendix B).
Tables: `runs`, `run_artifacts`, `tasks`, `quarantine_artifacts`, `managed_app_bindings`.
Deploy: `them-go-bridge` (reconciler → AdminQuerier; runrecorder → TenantTx) +
`them-middleware-worker` (AdminTx for run_artifacts writes).
Then: enable RLS.

**Phase G — Child run/task tables**
Tables: `run_steps`, `run_usage`, `artifacts`, `task_messages`, `middleware_audit`.
Deploy: `them-go-bridge` + `them-agent-runtime` (run_steps/run_usage writes → TenantTx).
Then: enable EXISTS policies.

**Phase H — Middleware jobs and remaining**
Tables: `middleware_jobs`, `llm_providers`, `audit_logs`.
Deploy: `them-go-bridge` (gateway enqueue → TenantTx; llm_providers split → TenantTx+AdminQuerier) +
`them-middleware-worker` (Claim/Complete → AdminQuerier/AdminTx; workerconfig → correct split) +
`them-dag-worker` (workerconfig → correct split).
Then: enable RLS.

### 8.4 Verification after each phase

1. `go test ./...` — zero failures.
2. `go test -tags=integration ./...` — zero failures including `TestRLS_TwoTenantFullIsolation`.
3. `docker logs them-go-bridge` — no unexpected 500s.
4. E2E: `ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14` — all pass.
5. `psql` as `them_app` — confirm rows outside current tenant are inaccessible on enabled tables.

### 8.5 Rollback

**Per-table rollback** (after RLS is enabled, a phase fails):
```sql
ALTER TABLE them.<table> DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS <table>_tenant_isolation ON them.<table>;
ALTER TABLE them.<table> NO FORCE ROW LEVEL SECURITY;
```

The application continues running with application-layer WHERE clauses for isolation (same as
pre-Step-19 behavior). them_app still has no BYPASSRLS.

**Invariant:** Never add BYPASSRLS to them_app. Doing so silently disables the isolation
guarantee. Once them_app is created without BYPASSRLS, that attribute must never be added.

**Full rollback** (revert Phase A before any RLS enabled):
1. Point both Pools.App and Pools.Admin to the original `them` DSN.
2. Drop them_app, them_admin if no active connections.
3. Table ownership remains with them_owner (NOLOGIN — safe).
4. Revert Go code (one commit revert).

### 8.6 Definition of done for Step 19

- [ ] All tables in §5.2 with "Standard" strategy have ENABLE RLS + FORCE RLS + policy
- [ ] All child tables have EXISTS-based policies enabled
- [ ] `them_app`: `rolbypassrls = f`, `rolcanlogin = t`
- [ ] `them_owner`: `rolbypassrls = f`, `rolcanlogin = f`
- [ ] `them_admin`: `rolbypassrls = t`, `rolcanlogin = t`
- [ ] All callers use TenantTx, AdminQuerier, or AdminTx — no raw pool in DAL
- [ ] `dbtype` package exists; import cycle is absent (`go build ./...` succeeds)
- [ ] All integration tests RLS-01 through RLS-40 pass
- [ ] `TestRLS_TwoTenantFullIsolation` is in the permanent integration suite
- [ ] `docs/SCHEMA.md` updated with RLS status per table
- [ ] `go/TEST_INDEX.md` updated with new integration tests
- [ ] `docs/CURRENT.md` and `docs/HANDOVER.md` updated
- [ ] `go test ./...` zero failures

---

## Appendix A — Atomicity Bugs to Fix Before Step 19

### A.1 `UpsertManagedAppParams` (`go/internal/admin/dal/managed_apps.go`)

Current: DELETE then INSERT in a loop, no transaction.
Fix: wrap in `BeginAdminTx` (platform-global operation).

### A.2 `PublishDefinition` (`go/internal/admin/dal/publish.go`)

Current: two sequential UPDATEs, no transaction.
Fix: wrap in `BeginTenantTx`.

### A.3 `UpsertOIDCUser` (`go/internal/authserver/oidc_store.go`)

Current: 4+ sequential queries, no transaction — race condition on concurrent OIDC logins.
Fix: wrap in `pool.Begin(ctx)` (admin pool, not TenantTx — auth_service schema only).

---

## Appendix B — `tasks.tenant_id` Backfill

27 NULL rows from before multi-tenancy. All belong to the bootstrap tenant.

**Migration file:** `db/060_tasks_tenant_backfill.sql`

```sql
UPDATE them.tasks
SET    tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE  tenant_id IS NULL;

ALTER TABLE them.tasks ALTER COLUMN tenant_id SET NOT NULL;
```

Apply before Phase F. Verify:
```sql
SELECT COUNT(*) FROM them.tasks WHERE tenant_id IS NULL;
-- Expected: 0
```

---

*End of document. v3.*
