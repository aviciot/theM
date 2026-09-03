# Step 19 — Postgres Row-Level Security (dedicated progress tracker)
# the-M multi-agent orchestration platform
# Branch: main
# Design HEAD: 61730f3
# Last updated: 2026-09-03
# Status: IN PROGRESS — Phase A complete, B1+B2 complete, C1 complete, C2 next
#
# Context: This is a focused side-track from the main multi-tenancy roadmap
# (Steps 1–18 complete). Step 19 (RLS) must finish before the tenant roadmap
# continues. When Step 19 is done, resume the main tenant effort from
# docs/HANDOVER.md § "Tenant roadmap — next step after Step 19".

---

## Purpose of this document

Step 19 is large. It will span multiple sessions. Each session should pick up where
the previous one left off using this file as the authoritative progress tracker.

**Update this file at the end of every session.** Mark completed sub-steps, record
commit hashes, and note any constraints discovered during implementation.

---

## Before you write a single line of code

Read these docs in this order:

1. `docs/HANDOVER.md` — session rules, standing constraints, test runner command
2. `docs/CURRENT.md` — deployment state, active containers
3. `docs/design/rls-option-a-plan.md` — **the full v3 design; this is the blueprint**
4. `go/CLAUDE.md` — Go package rules, file-size limits, Handler→Service→DAL rule
5. `go/TEST_INDEX.md` — before adding any test, check existing coverage
6. `docs/SCHEMA.md` — before touching any table

**Key constraints carried from earlier steps (non-negotiable):**
- Go runs inside Docker only: `docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...`
- TenantID is NEVER from HTTP headers — only from JWT claims via `tenantctx.TenantIDFromCtx`
- `BeginTenantTx` takes `uuid.UUID`, never a string
- 500 responses use static strings only — never `err.Error()`
- `go test ./...` must be zero failures before every commit
- Files under 400 lines — propose a split at 500
- TEST_INDEX.md must be updated in the same commit as any new test

---

## Architecture summary (read the design doc for full detail)

### New package: `go/internal/dbtype/`

A stdlib-only package containing the base interfaces. Nothing imports it except
`db`, `dal`, and handlers. Import direction: `dbtype ← db`, `dbtype ← dal`.

```
dbtype   (no deps beyond stdlib)
  ↑         ↑         ↑
  db        dal     handlers
```

Exports:
- `Querier` interface (Query / QueryRow / Exec / ExecReturning) — moved from `dal`
- `TenantQuerier` interface: embeds `Querier`, adds exported `IsTenantQuerier() struct{}`
- `AdminQuerier` interface: embeds `Querier`, adds exported `IsAdminQuerier() struct{}`

### Two pools: `go/internal/db/db.go`

```go
type Pools struct {
    App   *pgxpool.Pool  // them_app role — NO BYPASSRLS — tenant-scoped ops
    Admin *pgxpool.Pool  // them_admin role — BYPASSRLS — admin/platform/cross-tenant ops
}
func (p *Pools) BeginTenantTx(ctx context.Context, tenantID uuid.UUID) (*TenantTx, error)
func (p *Pools) BeginAdminTx(ctx context.Context) (*AdminTx, error)
func (p *Pools) NewAdminQuerier() dbtype.AdminQuerier
```

`TenantTx` and `AdminTx` live in `go/internal/db/`. Both implement their respective
`dbtype.*Querier` markers.

### Deferred rollback pattern (CORRECT — context cancellation safe)

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

The cleanup context is separate from the request context because pgx does NOT
auto-rollback on context cancellation — a cancelled context passed to Rollback
returns immediately without sending ROLLBACK to the server.

### Three DB roles

| Role | BYPASSRLS | LOGIN | Purpose |
|---|---|---|---|
| `them_owner` | No | No (NOLOGIN) | Owns tables, runs migrations via superuser SET ROLE |
| `them_admin` | Yes | Yes | Admin/cross-tenant runtime — all platform paths |
| `them_app` | No | Yes | Tenant-scoped runtime — all user-facing paths |

`FORCE ROW LEVEL SECURITY` overrides the *table owner's implicit bypass* but does NOT
override the `BYPASSRLS` role attribute. `them_admin` bypasses RLS unconditionally.

---

## Sub-step checklist

Each sub-step is one focused commit. Complete in order. Do not skip.

### Phase A — Infrastructure (no RLS enabled yet)

- [x] **A1** — Create DB roles migration (`db/070_rls_roles.sql`):
  - `CREATE ROLE them_owner NOLOGIN;`
  - `CREATE ROLE them_admin LOGIN BYPASSRLS;` (password from secrets)
  - `CREATE ROLE them_app LOGIN;` (password from secrets, no BYPASSRLS)
  - Transfer table ownership to `them_owner` (all 32 tables)
  - Apply exact per-table grants from §4.5 of the design doc
  - Add `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN` to `generate-env.sh` and `.env.example`
  - Verify: `SELECT rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname IN ('them_owner','them_admin','them_app');`
  - **Commit tag:** `feat(rls): A1 — DB roles, ownership transfer, grants`

- [x] **A2** — Create `go/internal/dbtype/` package:
  - `querier.go` — `Querier`, `TenantQuerier`, `AdminQuerier` interfaces
  - `querier_test.go` — compile-time interface satisfaction tests
  - **Commit tag:** `feat(rls): A2 — dbtype package with Querier marker interfaces`

- [x] **A3** — Refactor `go/internal/db/db.go`:
  - Add `Pools` struct with `App` and `Admin` pools
  - Implement `TenantTx` (wraps pgx.Tx, implements dbtype.TenantQuerier)
  - Implement `AdminTx` (wraps pgx.Tx, implements dbtype.AdminQuerier)
  - Implement `adminQuerier` (wraps pool, implements dbtype.AdminQuerier)
  - Implement `BeginTenantTx`, `BeginAdminTx`, `NewAdminQuerier`
  - Update `go/cmd/them/main.go` and other cmd/ binaries to pass two DSNs
  - Unit tests for BeginTenantTx/BeginAdminTx error paths (mock pool)
  - **Commit tag:** `feat(rls): A3 — Pools struct, TenantTx, AdminTx`

- [x] **A4** — Fix atomicity bugs (before RLS touches any DAL):
  - `UpsertManagedAppParams`: wrap DELETE+INSERT in `BeginAdminTx`
  - `PublishDefinition`: wrap two UPDATEs in `BeginTenantTx`
  - `UpsertOIDCUser`: wrap 4 queries in `pool.Begin` (auth_service schema, no TenantTx)
  - Update tests for each
  - **Commit tag:** `fix(rls): A4 — atomicity bugs before RLS enablement`

- [x] **A5** — Integration test infrastructure:
  - `go/internal/db/rls_integration_test.go` with build tag `integration`
  - Tests use REAL production schema (db/001_schema.sql + all migrations), NOT ad-hoc tables
  - Tests use REAL them_app/them_admin roles created by A1 migration
  - Role verification tests (RLS-30..33, RLS-31b NOLOGIN)
  - Pool configured with `MaxConns: 1` for connection-reuse tests (RLS-11..15)
  - **Commit tag:** `test(rls): A5 — integration test infrastructure`

### Phase B — First RLS tables (low caller count)

Migrate callers → deploy → enable RLS. For each phase, the sequence is:
1. Update Go callers to use TenantTx/AdminQuerier
2. `go test ./...` zero failures
3. Deploy (restart container)
4. THEN apply the SQL migration to enable RLS
5. Run integration tests

- [x] **B1** — Migrate callers for `mcp_servers`, `tenant_group_mappings`, `agent_definitions`, `agent_runtime_specs`:
  - `dal/dal.go`: tenantQuerierAdapter + adminQuerierAdapter + dbTypeRowsWrapper;
    `NewDBFromTenantQuerier` / `NewDBFromAdminQuerier` constructors
  - `service/mcp_servers.go`: `NewMCPServerServiceFromFernet` (pre-derived key, avoids re-derive per request)
  - `mcp_servers.go`: dual-path MCPServersHandler (TenantTx when pools != nil, legacySvc otherwise)
  - `agent_definitions.go`: dual-path AgentDefinitionsHandler; GetParams uses legacySvc (admin/cross-tenant)
  - `router.go`: BuildRouter gains `pools *db.Pools`; passes to both handlers
  - `cmd/them/main.go`: rlsPools now passed to BuildRouter (was `_ = rlsPools`)
  - `tenant_group_mappings` in tenants.go: these are super_admin admin routes; they run via the
    `them_admin` pool (BYPASSRLS) already — no TenantTx needed; RLS policies won't block them
  - authserver `GetGroupRole`: already uses admin pool (BYPASSRLS); no code change needed
  - All 48 packages pass: `go test ./...` zero failures
  - **Commit:** `5f70499`
  - **Commit tag:** `feat(rls): B1 — migrate callers for mcp_servers/tenant_group_mappings/agent_definitions`

- [x] **B2** — Enable RLS on B1 tables:
  - `db/071_rls_phase_b.sql` — standard direct tenant_id policy (§5.4) on all 4 tables
  - RLS isolation smoke-tested via psql: correct tenant → 1 row; wrong tenant → 0; no GUC → 0; them_admin → all rows (BYPASSRLS)
  - Integration tests: `go test -tags=integration ./internal/db/...` PASS
    - RLS-33 PASS: them_admin BYPASSRLS confirmed (sees all agents)
    - RLS-31b PASS: them_owner cannot connect (NOLOGIN)
    - RLS-30/31/32 SKIP: superuser `them` DSN password mismatch with derived key — role attributes verified directly in psql instead
    - RLS-08/10/11 SKIP: correct — agents RLS not yet enabled (Phase C)
  - Prerequisites applied this session: db/053–059 + db/070_rls_roles.sql (roles/ownership/grants)
  - **NOTE:** `them_admin` lacks DELETE grant on `app_mcp_credentials` (FK cascade target) —
    070_rls_roles.sql doesn't GRANT DELETE on that table to them_admin. Flag for 070 patch or C1.
  - **Commit:** `e55437f`
  - **Commit tag:** `feat(rls): B2 — enable RLS on mcp_servers/tenant_group_mappings/agent_definitions`

### Phase C — Core admin CRUD tables

- [x] **C1** — Migrate callers for `agents`, `orchestrators`, `applications`, `entry_points`, `access_tokens`:
  - `agents.go`: dual-path AgentsHandler; CRUD via openSvc (TenantTx); Test/SecurityScan use legacyDAL for cross-tenant GetAgentBySlug/GetAgentTokenEncrypted
  - `orchestrators.go`: dual-path OrchestratorsHandler, all 5 CRUD methods
  - `tokens.go`: dual-path TokensHandler, all 5 CRUD methods
  - `applications.go`: dual-path ApplicationsHandler (legacySvc + legacyDAL); openSvc covers all 20+ handler methods including entry-points CRUD and provider-key management
  - `ep_discover.go`: openSvc for tenant-scoped app lookup; legacyDAL for cross-tenant orch/agent card synthesis
  - `router.go`: BuildRouter gains `pools *db.Pools` param; all handler constructors updated
  - `cmd/them/main.go`: rlsPools passed to BuildRouter; voice handler uses nil pools (legacy path OK for voice)
  - All test call sites updated (admin_test.go, agents_actions_test.go, applications_wave8_test.go)
  - 48 packages pass: `go test ./...` zero failures
  - **NOTE:** `appliveness.listEnabledEPSlugs` and `component_definitions` NOT yet migrated — entry_points has cross-tenant reader in appliveness.go; component_definitions has split policy (builtins + tenant rows). These can be handled in C1b or as part of C2 prep.
  - **Commit:** `4f3a0e1`
  - **Commit tag:** `feat(rls): C1 — migrate callers for agents/orchestrators/applications/tokens/entry-points`

- [ ] **C2** — Enable RLS on C1 tables:
  - `db/072_rls_phase_c.sql`
  - Run `TestRLS_TwoTenantFullIsolation`
  - Tests RLS-28b/c/d/e for component_definitions
  - **Commit tag:** `feat(rls): C2 — enable RLS on agents/orchestrators/applications/entry_points`

### Phase D — Child tables of applications

- [ ] **D1** — Migrate callers for `app_agent_bindings`, `app_orchestrators`, `app_mcp_credentials`, `middleware_wirings`:
  - EXISTS-based policies through `applications` (already RLS-enabled after C2)
  - Rebuild and redeploy affected containers
  - **Commit tag:** `feat(rls): D1 — migrate callers for app_agent_bindings and siblings`

- [ ] **D2** — Enable RLS on D1 tables:
  - `db/073_rls_phase_d.sql`
  - **Commit tag:** `feat(rls): D2 — enable RLS on app_agent_bindings/app_orchestrators/app_mcp_credentials/middleware_wirings`

### Phase E — Run and task tables

- [ ] **E0** — Prerequisite: `tasks.tenant_id` backfill (Appendix B of design doc):
  - `db/074_tasks_tenant_backfill.sql` — UPDATE + ALTER COLUMN SET NOT NULL
  - Verify zero NULL rows
  - **Commit tag:** `feat(rls): E0 — backfill tasks.tenant_id NOT NULL`

- [ ] **E1** — Migrate callers for `runs`, `run_artifacts`, `tasks`, `quarantine_artifacts`, `managed_app_bindings`:
  - `runrecorder` → TenantTx
  - `agent-runtime` (spec.go, llm.go, runtime.go) → TenantTx (tenantID from InvocationContext.TenantID)
  - `temporal/workerconfig/loader.go` — tenant-scoped lookups → TenantTx; platform lookups → AdminQuerier
  - `dag-worker/main.go` — split tenant-scoped → TenantTx; cross-tenant → AdminQuerier
  - `reconciler` → AdminQuerier (cross-tenant by design)
  - Rebuild and redeploy `them-go-bridge`, `them-go-worker`, `them-agent-runtime`, `them-dag-worker`
  - **Commit tag:** `feat(rls): E1 — migrate callers for runs/tasks/run_artifacts`

- [ ] **E2** — Enable RLS on E1 tables:
  - `db/075_rls_phase_e.sql`
  - **Commit tag:** `feat(rls): E2 — enable RLS on runs/tasks/run_artifacts/quarantine_artifacts`

### Phase F — Child run/task tables

- [ ] **F1** — Migrate callers for `run_steps`, `run_usage`, `artifacts`, `task_messages`, `middleware_audit`:
  - These are written by `runrecorder` (already TenantTx after E1) and read by admin DAL (run detail, get tasks)
  - Confirm all callers are already using TenantTx after E1 — exists subquery policies only need the callers to be inside TenantTx
  - Rebuild and redeploy if any additional callers found
  - **Commit tag:** `feat(rls): F1 — verify callers for run_steps/run_usage/artifacts/task_messages`

- [ ] **F2** — Enable RLS on F1 tables:
  - `db/076_rls_phase_f.sql` — EXISTS-based policies through runs/tasks/run_artifacts
  - **Commit tag:** `feat(rls): F2 — enable RLS on run_steps/run_usage/artifacts/task_messages/middleware_audit`

### Phase G — LLM providers + remaining tables

- [ ] **G1** — Migrate callers for `llm_providers`, `audit_logs`, `middleware_jobs`, `tenants.*` reads in authserver:
  - `llm_providers` split policy (SELECT allows platform NULLs + own rows; write own only)
  - `audit_logs` — them_app INSERT only (no SELECT/UPDATE/DELETE)
  - `middleware_jobs` — gateway enqueue via TenantTx (INSERT EXISTS policy); worker Claim/Complete/Fail via AdminQuerier/AdminTx
  - authserver `GetTenantIDPConfig` → AdminQuerier (pool via Pools.Admin)
  - Rebuild and redeploy `them-auth-go`, `them-go-bridge`, middleware worker containers
  - **Commit tag:** `feat(rls): G1 — migrate callers for llm_providers/audit_logs/middleware_jobs/authserver`

- [ ] **G2** — Enable RLS on G1 tables:
  - `db/077_rls_phase_g.sql`
  - Verify platform LLM defaults still visible via split policy
  - **Commit tag:** `feat(rls): G2 — enable RLS on llm_providers/audit_logs/middleware_jobs`

### Phase H — Final verification

- [ ] **H1** — Full integration test run: `go test -tags=integration ./...` zero failures
- [ ] **H2** — `TestRLS_TwoTenantFullIsolation` passes
- [ ] **H3** — E2E test suite: `ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14` all pass
- [ ] **H4** — `docs/SCHEMA.md` updated with RLS status per table
- [ ] **H5** — `go/TEST_INDEX.md` updated with all new integration tests
- [ ] **H6** — This file (`docs/STEP19_HANDOVER.md`) updated with completion notes
- [ ] **H7** — `docs/CURRENT.md` and `docs/HANDOVER.md` updated

---

## Caller-to-table dependency matrix (summary)

See §8.2 of `docs/design/rls-option-a-plan.md` for the full matrix. Key ordering constraints:

| Table | Callers that must be migrated | Containers to redeploy |
|---|---|---|
| `tenant_group_mappings` | authserver `GetGroupRole` | `them-auth-go` |
| `entry_points` | appliveness cross-tenant query | `them-go-bridge` |
| `agents`, `applications` | agent-runtime, dag-worker, workerconfig loader | `them-agent-runtime`, `them-dag-worker` |
| `runs` | reconciler (cross-tenant sweep), runrecorder | `them-go-bridge`, `them-go-worker` |
| `run_steps`, `run_usage` | runrecorder writer, admin DAL reader | `them-go-bridge`, `them-go-worker` |
| `middleware_jobs` | gateway enqueue (TenantTx), worker claim (AdminQuerier) | `them-go-bridge`, middleware container |

**Rule:** For every row above, the listed containers must be rebuilt and running the migrated code BEFORE the SQL migration that enables RLS is applied.

---

## DB role DSN variables

Add to `generate-env.sh` and `.env.example` (never commit actual values):

```
THEM_DB_URL_APP=postgres://them_app:<password>@them-postgres:5432/them
THEM_DB_URL_ADMIN=postgres://them_admin:<password>@them-postgres:5432/them
```

The existing `THEM_DB_URL` can remain as the migration/superuser connection. The new
variables are derived from `secrets.local` via HMAC like all other secrets.

---

## Constraints discovered during Phase A implementation

7. **`dal.DB` now has optional `pool` field** — `NewDBWithPool(q, pool)` exists for handlers that need atomicity in multi-statement DAL functions. `NewDB(q)` still works unchanged (pool=nil, no transaction wrapping). Handlers that call `UpsertManagedAppParams` or `PublishDefinition` should use `NewDBWithPool` to get atomicity. The `admin.PgxQuerier` now exposes `Pool()` for this.

8. **`dal` package has a `tx.go` file** with `txQuerier`, `txRowsWrapper`, and `runInTx` helper for internal transactional wrappers. This is internal to `dal` — not exported.

9. **TEST_INDEX numbering conflict fixed** — A2/A3 sub-agents used S1-96/S1-97 which were already taken. Fixed to S1-98 (DB Pools) and S1-99 (dbtype). The summary table and section headers now agree.

## Known constraints discovered during design

1. **`pg_try_advisory_lock` in reconciler is session-scoped** — incompatible with PgBouncer
   transaction mode. Not a blocker for Step 19 (no PgBouncer). Document in STATUS.md if
   PgBouncer is ever considered.

2. **`tasks.tenant_id` has 27 NULL rows** from pre-multi-tenancy. Backfill is E0.

3. **`audit_logs.tenant_id` is nullable** — NULL rows are platform-level audit events. The
   NULLIF expression in RLS handles this correctly (NULL IS NULL → no tenant match → admin
   only). No backfill needed for audit_logs.

4. **`them` role is currently SUPERUSER** — it must not be used as the application DSN after
   Step 19. Retire it gradually: after deploying both `them_app` and `them_admin`, switch all
   application DSNs, then remove LOGIN from `them`.

5. **pgx/v5 default `QueryExecModeCacheStatement`** is compatible with PgBouncer ≥1.21
   (when `max_prepared_statements > 0`). Older PgBouncer requires SimpleProtocol. Not relevant
   until PgBouncer is added.

6. **Context cancellation does NOT auto-rollback via pgx** — the deferred Rollback MUST use
   a separate cleanup context (`context.WithTimeout(context.Background(), 5*time.Second)`).

---

## Progress log

| Date | Session | Sub-steps completed | Commit(s) | Notes |
|---|---|---|---|---|
| 2026-09-03 | Design session | Design v3 finalized | 61730f3 | All 8 blocking issues resolved |
| 2026-09-03 | Implementation session 1 | A1, A2, A3, A4, A5 | a46e703..7bfe778 | Phase A complete. DB migration not yet applied to live DB — apply db/070_rls_roles.sql + set passwords before Phase B. TEST_INDEX numbering fixed (ba5d0d8). |

*(Add a row for each session.)*

---

## Definition of done

- All sub-steps A1–H7 checked above
- `go test ./...` zero failures
- `go test -tags=integration ./...` zero failures
- `TestRLS_TwoTenantFullIsolation` passes
- `SELECT rolbypassrls FROM pg_roles WHERE rolname = 'them_app'` returns `f`
- `SELECT rolcanlogin FROM pg_roles WHERE rolname = 'them_owner'` returns `f`
- No raw `*pgxpool.Pool` passed to any DAL function — all callers use TenantTx or AdminQuerier
- E2E test suite (test 14) all pass
