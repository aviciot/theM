---
title: Multi-tenancy & RLS
type: architecture
status: current
owns:
  - go/internal/db/
  - go/internal/dbtype/
  - go/internal/tenantctx/
  - db/070_rls_roles.sql
  - db/071_rls_phase_b.sql
  - db/072_rls_phase_c.sql
  - db/073_rls_phase_d.sql
  - db/075_rls_phase_e.sql
  - db/076_rls_phase_f.sql
  - db/077_rls_phase_g.sql
  - db/078_rls_phase_h2.sql
  - db/110_platform_as_bootstrap_tenant.sql
last_verified: 2026-10-24   # code read + live read-only catalog queries on the running stack; integration tests NOT run
---

# Multi-tenancy & RLS

## Purpose
Keep one tenant's data invisible to another. Two layers: **app-level scoping** (every query filters by
tenant/application) and **Postgres Row-Level Security** as a safety net. This doc states what is
enforced where, based on the live database, not on migration comments.

## How it works

### Tenant identity
- Admin API: tenant comes **only** from the signed JWT `tenant_id` claim (`admin.AdminTenantMiddleware` → `tenantctx.WithTenantID`).
- Runtime (WS/SSE/A2A/gateway): from the URL slug `/{tenant}/...` via `tenantctx.PgxSlugResolver` (enabled tenants only, cached 5 min), or from the opaque token's DB row. Never from headers/body. Legacy `/ws|/sse/orchestrate/...` paths fall back to the token's tenant, then the bootstrap tenant `00000000-0000-0000-0000-000000000001`.
- The bootstrap tenant (`default`, `is_bootstrap=true`) **is the platform**: platform LLM providers/keys are normal rows owned by it (db/110). Live tenants today: `default`, `bank`, `payops_ai`, `rnd`.

### Database roles (live)
| Role | Super | BYPASSRLS | Login | Used for |
|---|---|---|---|---|
| `them` | **yes** | yes | yes | Postgres superuser; `DATABASE_USER` in several services (see gaps) |
| `them_owner` | no | no | no | Owns tables (NOLOGIN, can't be a DSN) |
| `them_admin` | no | **yes** | yes | `Pools.Admin`: platform / cross-tenant / worker paths |
| `them_app` | no | no | yes | `Pools.App`: tenant-scoped paths; subject to RLS |

### Pools (`internal/db/db.go`)
`NewPools(appDSN, adminDSN)` needs `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` (startup fails without them). 
- `Pools.BeginTenantTx(ctx, tenantUUID)` → App pool transaction with `SELECT set_config('app.tenant_id', $1, true)` (transaction-local). Returns `TenantTx`, a distinct type (`dbtype.TenantQuerier`) so DAL functions can demand a tenant-scoped connection at compile time.
- `Pools.BeginAdminTx` / `NewAdminQuerier` → Admin pool (`dbtype.AdminQuerier`), no GUC.
- Separately the bridge still opens a legacy `db.New(cfg.DSN())` pool as the `them` superuser (health check, `appliveness`).

### RLS policies (live catalog)
All policies target role `them_app` only. Two shapes:
1. **Direct**: `tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid` — with no GUC set the comparison is NULL, so **zero rows (fail-closed)**.
2. **Via parent** for tables without their own `tenant_id`: `EXISTS (SELECT 1 FROM applications|runs|tasks … WHERE parent tenant matches)` — e.g. `app_agent_bindings`, `app_orchestrators`, `middleware_*`, `app_flow_http_params`, `run_steps`, `run_usage`, `task_messages`, `artifacts`.
Exceptions: `component_definitions_read` also allows `tenant_id IS NULL` (global catalog); `llm_providers_read` also allows the bootstrap tenant's rows (platform providers visible to everyone).

Coverage today: 38 tables have RLS+FORCE. `tenant_roles`, `tenant_role_mappings`, `tenant_role_grants` have RLS but **not FORCE**. No RLS: `tenants`, `tenant_quotas`, `config`, `middleware_defs`, `schema_migrations` (platform-global) and `hil_approvals`, `app_debug_config`, `app_flow_llm_overrides`, `app_temporal_config`, `gateway_profile_steps` (see gaps). The `auth_service` schema has **no RLS**; `them_app` has no grants on it at all (privileges, not RLS, protect users/memberships).

### Where RLS actually applies
Only code that uses `BeginTenantTx` runs under RLS: admin handlers for agents, orchestrators, applications, tokens, MCP servers, agent definitions, tenant runtime IdP (`admin/*.go`). Everything else uses the **Admin pool (BYPASSRLS, ~36 call sites)** or the superuser: epconfig load, run recorder, gate/session, workers (`dag-worker`, `worker`), gateway, reconciler. For those, isolation depends on explicit `WHERE tenant_id = …` in SQL, not on RLS.

### Application-level scoping rules (root `CLAUDE.md`)
Every session/run/rate-limit/gate is scoped to an application; queries include `application_id`/`entry_point_id`; per-app Redis keys include app ID/slug; routes under `/{tenant}/apps/{slug}/` inherit the slug.

### Migrations
Plain SQL files in `db/` applied by hand with `psql -f` (README steps), no runner. `them.schema_migrations` is advisory only (52 rows vs 125 files; latest recorded `111_…`).

## Key invariants
- **A new tenant-scoped table needs, in its own migration:** `tenant_id` (or a parent FK), `ENABLE` + `FORCE ROW LEVEL SECURITY`, a `them_app` policy, `GRANT`s to **both** `them_app` and `them_admin` (BYPASSRLS does not replace GRANT), and an explicit **sequence GRANT** for any SERIAL/BIGSERIAL (db/070's grant was one-time).
- Queries on a FORCE-RLS table from `them_app` must run inside `BeginTenantTx`, otherwise they silently return 0 rows.
- GUC is transaction-local (`set_config(..., true)`), so a pooled connection never leaks a tenant to the next request.
- Tenant ID is never taken from request data; managed-app runs bill the caller's tenant but execute against the owner's config.
- New per-app settings must be exportable: live inside the definition JSON or be registered in `admin/dal/app_deploy.go` (root `CLAUDE.md`).

## Failure modes
| What fails | Effect |
|---|---|
| `THEM_DB_URL_APP/ADMIN` missing | Bridge refuses to start |
| Handler uses plain querier on FORCE-RLS table | 0 rows / confusing 500 (no error from Postgres) |
| Missing GRANT or sequence GRANT | `42501 permission denied` at runtime, only when that code path runs |
| Slug of a disabled tenant | Still resolves for up to 5 min (resolver cache) |
| Admin-pool query without `tenant_id` filter | Cross-tenant data leak — RLS will **not** catch it |

## Known gaps (found while writing, 2026-10-24; code + live catalog; integration tests not run)
See `docs/plans/code-findings-backlog.md` F-31…F-41. Highlights:
1. **Gate keys are not tenant-scoped.** `them:ep:{slug}:sessions|shadow` and `them:ep:gate:queue:{slug}` use only the EP slug, so two tenants with an EP named e.g. `chat` share one concurrency cap and one queue (noisy neighbour / DoS across tenants).
2. **Superuser credentials in services.** `DATABASE_USER=them` (superuser) is used by `them-auth-go`, `them-mcp-service`, `them-middleware-worker`, `agent-runtime`, and the bridge's legacy pool; superuser ignores RLS and `FORCE`.
3. **Most data paths bypass RLS** (36 Admin-pool sites). RLS protects only the handful of admin handlers that use `BeginTenantTx`.
4. **Tables with tenant data but no RLS while `them_app` has DML:** `hil_approvals` (has `tenant_id`), `app_debug_config`, `app_flow_llm_overrides`, `app_temporal_config`, `gateway_profile_steps`; `them_app` can also `SELECT` all of `tenants` and `tenant_quotas`.
5. **`db/001_schema.sql` is not the source of truth.** It lacks gateway_*, governance_policies, appflow_debug_presets, tenant_runtime_config, hil_approvals, app_flow_http_params, … A fresh install following `db/README.md` does not produce the live schema. `db/README.md` is Python-era.
6. **No migration runner/tracking**; `schema_migrations` is incomplete.
7. `tenant_roles`, `tenant_role_mappings`, `tenant_role_grants` are RLS-enabled but not FORCE; `rls_integration_test.go` `CV-02` expects all RLS tables FORCE (likely failing — not run). Several tables are owned by `them` instead of `them_owner`.
8. Tenant slug resolver cache (5 min) lets a just-disabled tenant keep serving runtime traffic.

## Where to change things
| To… | Edit |
|---|---|
| Add a tenant-scoped table | new `db/NNN_*.sql` (RLS + FORCE + policy + both GRANTs + sequence GRANT), `docs/reference/schema.md`, `app_deploy.go` if per-app |
| Query a tenant table from a handler | use `pools.BeginTenantTx` (see `admin/tenant_runtime_idp.go` `withRIDPTx` as the pattern) |
| Change pool wiring | `internal/db/db.go`, `cmd/them/main.go` |
| Change tenant resolution | `internal/tenantctx/`, `admin/middleware.go`, `ws|sse` `AppsWSRoute` |

Tests: `go test ./internal/db/...`; with a live DB: `go test -tags=integration ./internal/db/...` (two-tenant isolation, catalog checks) — **creates data; run against a test DB**.

## Related
- `auth-and-roles.md` — where the tenant claim comes from
- `run-lifecycle.md` — gate/session keys (gap 1)
- `platform-overview.md` — containers and the three pools
- `docs/reference/schema.md`, `docs/plans/code-findings-backlog.md`
