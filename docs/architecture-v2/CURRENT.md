# Current Session State — the-M
# Last updated: 2026-08-16
# Replaces: NEXT_SESSION_BRIDGE_HANDOVER.md, NEXT_SESSION_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `54a7dad` — feat(sec): EP resolution tenant-safe end-to-end (migration 028) (2026-08-16)

---

## Deployment state

**Active deployment: local Linux server** (moved from Hetzner 2026-08-15)

Stack: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`
UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is sole auth service — Python `them-auth-service` removed from compose
- `them-bridge` (Python) handles all non-auth API routes in default dev mode
- `them-go-bridge` is NOT started in default dev mode (requires `--profile go`)
- Without `--profile go`, Traefik has no routers for `/api/v1/`, `/health/`, `/ws/`, `/sse/`, `/apps/`
- To match Hetzner prod routing: add `--profile go` to startup command
- `docker-compose.dev.yml` is the local Linux overlay (replaces old `docker-compose.local.yml`)
- Named Docker volumes: `them-postgres-data`, `them-redis-data`, `them-logs` — `external: true`
- Project name: `them_gateway` — required for volume/network ownership consistency

All containers healthy. See `docs/STATUS.md` for full container list.

---

## Environment alignment done this session

- `docker-compose.dev.yml` fixed: `THE_M_AUTH_URL` → `them-auth-go:8703`, Dockerfile names, named volumes, external network
- `.dockerignore` updated: added `theM_gateway/` to prevent build-context permission errors
- `docs/STATUS.md` updated: HEAD, startup command, container map
- `docs/architecture-v2/LOCAL_DEV_PYTHON_OFF_AUDIT.md` created: Phase 10/11 route audit
- Local repo aligned to `origin/main` at `ca29acd` (saved local R-4 work to `local-r4-backup` branch)

---

## Current migration slice

**EP Resolution Tenant-Safe End-to-End — COMPLETE** (2026-08-16)

Migration 028: `entry_points.tenant_id` NOT NULL, `UNIQUE(tenant_id, slug)` — applied to live DB.

What was done:
- `db/028_entry_points_tenant_scoped_slug.sql`: adds `tenant_id` to `entry_points`, backfills from `applications.tenant_id`, drops global `UNIQUE(slug)`, adds `UNIQUE(tenant_id, slug)` + index.
- `epconfig.Loader.Load(ctx, tenantID, slug)` — SQL filters by `tenant_id`. Cache key: `"{tenantID}:{slug}"`.
- `epconfig.Loader.Invalidate(tenantID, slug)` — tenant-scoped.
- `epconfig.Loader.Subscribe` — handles `"{tenantID}:{slug}"` payload format; legacy bare slug as safety net.
- `dal.CreateEntryPoint` — backfills `tenant_id` from parent application via INSERT...SELECT.
- `dal.GetEntryPointTenantAndSlug`, `dal.ListEPTenantSlugsForApp` — new methods for tenant-scoped cache invalidation.
- `service.AppService.UpdateEntryPoint` — takes `tenantID` param as fallback when EP lookup returns empty.
- `service.AppService.publishEP` — payload format `"{tenantID}:{slug}"`.
- WS/SSE/A2A handlers: resolve `tenantID` from bearer token; fall back to `BootstrapTenantID` for public EPs.
- `tenantctx.BootstrapTenantID` constant: `"00000000-0000-0000-0000-000000000001"`.
- `execution.ExecutionRequest.TenantID` field added.
- `transport.EPConfigLoader.Load` signature updated to `Load(ctx, tenantID, slug)`.
- All test fakes updated; `go test ./...`: 30/30 packages, 0 failures. Go bridge rebuilt and healthy.

**SAFE TO DEVELOP WAVE 9: YES**
**SAFE FOR GO-ONLY MULTI-TENANT ENTRY-POINT ROUTING: YES**

---

**Tenant/Runtime Foundation — COMPLETE** (2026-08-16)

Architecture decision: **Python is permanently retired.** `them-bridge` and `them-worker` MUST remain OFF. No Python patches. No Python fallbacks. No compatibility shims.

What was done:
- `db/027_app_orchestrators_uniqueness.sql` — drops global `UNIQUE(name)` on `app_orchestrators`, adds `UNIQUE(application_id, name)`. Applied to live DB. Verified.
- **SEC-03:** Agent registry fully tenant-scoped. Redis key: `them:agents:registry:{tenant_id}`. L1 key: `"{tenantID}:{slug}"`. SQL: `QueryAgentsByTenant(ctx, tenantID)`. Invalidation: per-tenant only. 9 new isolation tests.
- **SEC-04:** `EPConfig` now carries `AppOrchestratorID` + `OrchestratorName` loaded via LEFT JOIN `app_orchestrators`. WS/SSE/A2A/Lifecycle all use `EPConfig.OrchestratorName`. NULL binding = hard error (503). `WorkflowInput.AppOrchestratorID` added for future Go worker.
- **SEC-01/SEC-02:** Documented as LEGACY PYTHON PATH — RETIRED. Not implementation blockers. Dead paths that will not be reactivated.
- R5 Application Readiness Review + R6 Tenant Architecture Review written and committed.
- `go test ./...`: 30/30 packages, 0 failures. Go bridge rebuilt and healthy.

Permanent architectural constraint (enforced, never waive):
> Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID. Never by name globally.

---

**Runs WRITE — COMPLETE** (2026-08-15)

What was done:
- New DAL methods: `CancelRun` (UPDATE...RETURNING), `DeleteRun` (DELETE...RETURNING), `BulkDeleteRuns` (DELETE...RETURNING with IN list)
- New service methods: `Cancel` (404/409 distinction via fallback GetRun), `Delete`, `BulkDelete` (max 500 IDs enforced)
- New handlers: `PATCH /runs/{run_id}/cancel`, `DELETE /runs/{run_id}`, `POST /runs/bulk-delete`
- `POST /runs/bulk-delete` registered as static route before `/{run_id}` to prevent wildcard shadowing
- Traefik Wave 2f: 3 new routers — `them-go-runs-cancel` (PATCH, priority 116), `them-go-runs-delete` (DELETE, priority 114), `them-go-runs-bulk-delete` (POST, priority 116)
- 6 new handler tests (RW-1 through RW-6) in `go/internal/admin/runs_test.go`
- `isolationFakeDal` and `fakeDal` in service tests updated to satisfy Dal interface
- All 30 Go packages pass `go test ./...`

**Runs READ/UI — COMPLETE** (cf953cf, 2026-08-15)

What was done:
- Auth fixed: runs routes moved from `BearerTenantMiddleware` to `JWT + RequireSuperAdmin + AdminTenantMiddleware` — session JWTs from auth-go now work
- New handlers: `GET /runs/stats`, `GET /runs/{id}` (now returns RunDetail with steps/usage/children), `GET /runs/{id}/tasks`, `GET /runs/{id}/artifacts`
- New DAL types: `RunStep`, `RunUsage`, `RunDetail`, `Task`, `ArtifactPart`, `Artifact`, `RunStats`
- New DAL methods: `GetRunStats`, `GetRunDetail`, `GetRunTasks`, `GetRunArtifacts`
- Static route `/runs/stats` registered before `/{run_id}` to prevent chi wildcard shadowing
- Traefik Wave 2e added: `them-go-runs-sub` rule captures `/{id}/tasks` and `/{id}/artifacts`
- `THE_M_API_URL` in dev overlay changed to `http://them-traefik:8088` — Next.js proxy now routes through Go
- 8 new tests (RS-1/2, RD-1, RT-1/2, RA-1/2, RO-1) in `go/internal/admin/runs_test.go`
- Python-OFF verified: all 5 GET endpoints return 200 with `them-bridge` stopped

**Agents Store — COMPLETE** (888861b)

- `POST /agents/discover` → Go
- `POST /agents/{id}/test` → Go
- `POST /agents/{id}/security-scan` → Go
- `AdminTenantMiddleware` for UI admin routes
- Go auth service cutover; Python auth container removed

---

## Next recommended task

**Wave 9 — Multi-tenant runtime enablement.** All pre-requisites are now complete.

Items 1 and 2 from the original Wave 9 plan are **done** (EP tenant_id column + epconfig tenant-scoped resolution).

Remaining Wave 9 scope:
3. Session and rate-limit keys: ensure all runtime keys include tenant scope
4. Tenant provisioning: admin endpoints to create/manage tenants
5. Auth: tenant_id claim in JWT and bearer token validation (multi-tenant token issuance)
6. Live two-tenant verification: create two test tenants/apps with same EP slug, verify correct routing, no cross-tenant cache leakage

Before starting Wave 9: read `docs/architecture-v2/R6_TENANT_ARCHITECTURE_REVIEW.md` Section 15 (Implementation Plan).

Lower-priority backlog:
- `GET /runs/context/{ctx}/artifacts` — no Traefik rule, no Go handler (not used by admin UI)
- Applications export/import/restore routes — still Python
- Middleware-wiring admin routes — still Python

Full route inventory: `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`

---

## Python-OFF baseline (2026-08-15, verified with a6b9953 + live smoke tests at cddc30a)

**Confirmed working with Python OFF, Go active (them-bridge + them-worker stopped):**
- All admin routes (Waves 1-8): agents CRUD+discover+test+security-scan, orchestrators, applications, tokens, sessions, LLM providers, monitoring-config ✓
- Runs READ: `GET /runs`, `/runs/stats`, `/runs/{id}`, `/runs/{id}/tasks`, `/runs/{id}/artifacts` → all 200 ✓
- Runs WRITE: `PATCH /runs/{id}/cancel` → 200 (canceled), 409 (not running) ✓
- Runs WRITE: `DELETE /runs/{id}` → 204, 404 (not found) ✓
- Runs WRITE: `POST /runs/bulk-delete` → 200 `{"deleted":N}` ✓
- Auth (login, me, refresh) → auth-go 200 ✓
- `/health/live`, `/health/ready` → Go 200 ✓

**Still broken with Python OFF:**
- `GET /runs/context/{ctx}/artifacts` → 404 (no Traefik rule, no Go handler; not used by admin UI)
- `GET /apps`, `GET /apps/{slug}` → 404 (Traefik only captures WS/SSE paths for apps)
- `GET /health` (bare) → 404 (no Traefik router)
- Applications export/import/restore/middleware-wirings → Python only

---

## Known blockers

1. Auth admin CRUD (users/roles/teams) — not exposed since Python auth removed. Needs Go port.
2. Go Temporal worker is not yet sole owner of orchestration. Python worker is permanently retired (must remain OFF). Go worker must become sole owner before any second tenant is provisioned.
3. A2A server (`/a2a/*`) still on Python — not yet migrated to Go.
4. ~~`entry_points` has no `tenant_id` column~~ — DONE (migration 028).
5. Wave 9 items 3–6 (session/rate-limit tenant scope, tenant provisioning, multi-tenant auth) remain open.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `app/services/auth_client.py`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`
- **Python is permanently retired.** `them-bridge` and `them-worker` MUST remain OFF. Do NOT patch Python for compatibility. Do NOT plan for Python bridge/worker to return.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** (from `WorkflowInput.AppOrchestratorID`). Never resolve orchestrators globally by name.
- **SEC-01/SEC-02 are dead paths.** Legacy Python globally-namespaced orchestrator Redis keys (`them:orch:loc:{name}`, `them:orch:tmpl:{name}`) will not be written again. Do not reactivate.
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** The old global key `them:agents:registry` must not be written or read.
- **EP cache key is `"{tenantID}:{slug}"`.** Cache invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{slug}"`. Global slug-only keys must not be written.
- **`entry_points.tenant_id` is NOT NULL.** All new EPs inherit tenant from parent application. `UNIQUE(tenant_id, slug)` enforced at DB level (migration 028).
- No secrets in Application Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history. Only secret references.

---

## Documentation rules (forward)

1. One source of truth per subject.
2. Completed plans/reports → `docs/architecture-v2/archive/`.
3. Update this file (CURRENT.md) at session end — do NOT create new NEXT_SESSION_*.md files.
4. ADRs are permanent — never archive them.
5. STATUS.md describes now, not history.
6. ARCHITECTURE.md describes current design, not migration chronology.
7. REMAINING_ROUTE_OWNERSHIP_INVENTORY.md is temporary — remove when Python is gone.
8. Documentation changes ship in same commit as the code changes they describe.
9. Never create another competing active architecture directory.
10. Code is final truth; stale canonical docs are a bug.
