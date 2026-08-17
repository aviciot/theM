# Current Session State — the-M
# Last updated: 2026-08-16
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `TBD` — feat(admin): Application Definition CRUD (Phase B)

---

## Deployment state

**Active deployment: local Linux server**

Stack startup command:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile go --profile temporal up -d
```

UI: `http://<server-ip>:8088`

Key facts:
- `them-auth-go` is sole auth service
- **`them-bridge` (Python) is permanently retired** — behind `profiles: [legacy]`; does NOT start in default, `--profile go`, or `--profile temporal` mode
- **`them-worker` (Python) is behind `profiles: [temporal]`** and the go-worker replaces it; Python worker will be moved to `[legacy]` once Go worker is sole owner
- `them-go-bridge` is the active API gateway — started with `--profile go`
- Frontend `depends_on` changed from `them-bridge` → `them-auth-go` (Python removed from dependency chain)
- Frontend `THE_M_API_URL` points to `http://them-traefik:8088` (not them-bridge directly)
- `docker-compose.dev.yml` is the local Linux overlay
- Named Docker volumes: `them-postgres-data`, `them-redis-data`, `them-logs` — `external: true`
- Project name: `them_gateway` — required for volume/network ownership consistency

All containers healthy as of 2026-08-16. See `docs/STATUS.md` for full container map.

---

## Python permanently locked out via compose profiles

Both Python runtimes are now behind `profiles: [legacy]`:

| Service | Profile (before) | Profile (now) | Status |
|---|---|---|---|
| `them-bridge` | _(default — no profile)_ | `[legacy]` | Permanently retired |
| `them-bridge-2` | `[replica]` | `[replica]` | Unchanged — also effectively dead |
| `them-worker` | `[temporal]` | `[legacy]` | Permanently retired |

**`--profile temporal` now starts Temporal infrastructure only:**
- `temporal-frontend`, `temporal-ui`, `temporal-admin-tools`
- Does NOT start `them-worker` (Python) — that is behind `[legacy]`

**Go Temporal worker** (`them-go-worker` in `docker-compose.dev.yml`) is defined behind `profiles: [go-worker]` and is future work — not yet the active orchestration path.

Verified clean restart with `--profile go --profile temporal`:
```
PRESENT:  them-go-bridge, temporal-frontend, temporal-ui, temporal-admin-tools,
          them-auth-go, them-frontend, them-postgres, them-redis, them-traefik
ABSENT:   them-bridge (Python), them-worker (Python)
```

---

## Current migration slice

**Phase A — Application v2 Component Registry: COMPLETE** (b93dff4, 2026-08-16)

Goal: DB foundation + Go resolver. No UI, no compiler, no Python. Scope-locked.

### DB migrations applied (live DB)

- `db/028_entry_points_tenant_scoped_slug.sql` — EP tenant isolation (prior session)
- `db/029_component_registry_foundation.sql` — `component_definitions`, `application_definitions`, `active_definition_id` on applications, `definition_id` on runs
- `db/030_component_subtype_adoption.sql` — namespace/version/scope/status columns on `agents` + `middleware_defs`; inserts matching `component_definitions` base rows (same UUIDs, FK constraints); seeds builtin `llm-orchestrator` + 5 EP palette rows

Total rows in `component_definitions`: 20 (as of b93dff4)

### Go packages added

`go/internal/registry/` — Component Registry resolver:
- `types.go` — `ComponentKind`, `ComponentScope`, `ComponentStatus`, `DefinitionRef`, `ComponentDefinition`, `CredentialSlot`
- `pgx.go` — `PgxQuerier` (ResolveByRef + ResolveByID), `ErrNotFound`, `ErrDisabled`, `ErrDeprecated`
- `resolver.go` — `Resolver.Resolve` (tenant access check, UUID fast-path, ref fallback) + `ResolveForPublish` (blocks deprecated)
- `resolver_test.go` — 12 tests (S1-41 in TEST_INDEX.md)

### Architecture decisions (permanent)

- **Portable identity**: `{kind, namespace, name, version}` tuple — stable across environments; UUID is fast-path cache
- **Builtin scope**: `scope=builtin` → all-tenant access; `scope=tenant` → owner-only access
- **No deprecated in publish**: `ResolveForPublish` returns `ErrDeprecated` for deprecated components
- **No secrets in Definition JSONB**: only secret references; secrets never in logs, exports, or Temporal history
- **Exact version pinning**: integer revision pinned at publish; no "latest" or floating ranges
- **`BootstrapTenantID`**: `"00000000-0000-0000-0000-000000000001"` for single-tenant/public EPs

### Test state

`go test ./...`: **31 packages, 0 failures** (as of b93dff4)

---

**EP Resolution Tenant-Safe End-to-End — COMPLETE** (54a7dad, 2026-08-16)

- `db/028_entry_points_tenant_scoped_slug.sql`: `entry_points.tenant_id` NOT NULL; `UNIQUE(tenant_id, slug)`
- `epconfig.Loader.Load(ctx, tenantID, slug)` — SQL filters by `tenant_id`
- Cache key: `"{tenantID}:{slug}"` — invalidation payload: `"{tenantID}:{slug}"`
- WS/SSE/A2A handlers: resolve `tenantID` from bearer token; fallback to `BootstrapTenantID`
- `tenantctx.BootstrapTenantID` = `"00000000-0000-0000-0000-000000000001"`
- All fakes updated; 30→31 packages pass

**SAFE TO DEVELOP WAVE 9: YES**
**SAFE FOR GO-ONLY MULTI-TENANT ENTRY-POINT ROUTING: YES**

---

**Tenant/Runtime Foundation — COMPLETE** (efeb1ec, 2026-08-16)

- P-08: `db/027` — `UNIQUE(application_id, name)` on `app_orchestrators`. Live DB.
- SEC-03: Agent registry Redis key: `them:agents:registry:{tenant_id}`.
- SEC-04: `EPConfig` carries `AppOrchestratorID` + `OrchestratorName` via LEFT JOIN. NULL = 503.
- SEC-01/SEC-02: Dead legacy Python paths — retired, will not be reactivated.

---

**Runs WRITE — COMPLETE** (prior session)

- `PATCH /runs/{run_id}/cancel`, `DELETE /runs/{run_id}`, `POST /runs/bulk-delete`
- Traefik Wave 2f: 3 new routers

**Phase B — Application Definition CRUD — COMPLETE** (HEAD)

Routes added (all under `/api/v1/admin/applications/{id}/definitions`):
- `POST   .../definitions` → 201 `{"id":"...","revision":N}`
- `GET    .../definitions` → 200 `[AppDefinition,...]`
- `GET    .../definitions/{def_id}` → 200 `AppDefinition`
- `PUT    .../definitions/{def_id}` → 200 `{"id":"...","updated":true}`
- `DELETE .../definitions/{def_id}` → 204

Packages added/modified:
- `go/internal/admin/dal/definitions.go` — 6 DAL methods (GetNextRevision, CreateDefinition, GetDefinition, ListDefinitions, UpdateDraftDefinition, DeleteDraftDefinition)
- `go/internal/admin/service/definitions.go` — DefinitionService with validation, hash, published-immutability
- `go/internal/admin/definitions.go` — DefinitionsHandler with 5 HTTP handlers
- `go/internal/admin/definitions_test.go` — 12 tests (S1-42 to S1-53)
- `go/internal/admin/service/service.go` — Dal interface extended with 6 new methods
- `go/internal/admin/router.go` — defs.Routes() wired in tenant-scoped group
- `go/internal/admin/service/service_test.go` — fakeDal stubs for new Dal methods
- `go/internal/admin/service/tenant_isolation_test.go` — isolationFakeDal stubs

Key behaviors:
- Tenant isolation: every DAL query includes `AND tenant_id=$N::uuid`; cross-tenant access silently returns 404
- Insert via sub-SELECT: INSERT...SELECT FROM applications WHERE id=$1 AND tenant_id=$2 — zero rows = ErrNotFound
- Published immutability: UPDATE/DELETE require `AND status='draft'`; ErrNoRows → lookup to distinguish 404 vs 409
- Secret key rejection: rejects `"secret_value"` keys and `"enc:"` prefixed values with secret/key in key name at any nesting depth
- Canonical hash: unmarshal→marshal→sha256→`"sha256:"` prefix
- No `updated_at` on `application_definitions` table — SET clause omits it

Live verification (Python OFF, all 9 scenarios):
- CREATE 201 ✓, GET 200 ✓, LIST 200 ✓, UPDATE 200 ✓
- CROSS-TENANT 404 ✓, WRONG APP 404 ✓, DUP INSTANCE_ID 422 ✓, SECRET KEY 400 ✓, DELETE 204 ✓

Test state: `go test ./...` — **31 packages, 0 failures, 12 new tests**

**Runs READ/UI — COMPLETE** (cf953cf)

- `GET /runs/stats`, `GET /runs/{id}` (RunDetail), `GET /runs/{id}/tasks`, `GET /runs/{id}/artifacts`
- Auth fixed: moved to `JWT + RequireSuperAdmin + AdminTenantMiddleware`
- Python-OFF verified for all 5 GET endpoints

**Agents Store — COMPLETE** (888861b)

- `POST /agents/discover`, `POST /agents/{id}/test`, `POST /agents/{id}/security-scan`
- `AdminTenantMiddleware` for admin routes
- Go auth service cutover; Python auth container removed

---

## Next recommended task

**Phase C — Application Definition Validate + Publish + Compile**

Pre-reading: `docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md` Section 5–8

Phase C scope (implement in a fresh session):
1. `POST /api/v1/admin/applications/{id}/definitions/{def_id}/validate` — resolve all component refs via `registry.Resolver`; return validation report (missing/deprecated/cross-tenant blocked)
2. `POST /api/v1/admin/applications/{id}/definitions/{def_id}/publish` — call ResolveForPublish, set `applications.active_definition_id`, set `application_definitions.status='published'`, set `published_at`
3. New DAL method: `PublishDefinition(ctx, tenantID, appID, defID string) error` — UPDATE definitions + UPDATE applications in same TX
4. New service: `ValidateDefinition`, `PublishDefinition` on `DefinitionService`
5. Tests: validate happy, validate missing component, validate deprecated component, publish happy, publish cross-tenant blocked, publish sets active_definition_id
6. Traefik: no new labels needed (routes already under `/admin/applications/` priority 112)
7. No compiler, no UI, no Python

Alternative next task: **Wave 9 — Multi-tenant runtime enablement** (session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims)

Read `docs/architecture-v2/R6_TENANT_ARCHITECTURE_REVIEW.md` Section 15 before starting Wave 9.

---

## Python-OFF baseline (verified 2026-08-16, all with them-bridge locked to profiles: [legacy])

**Confirmed working:**
- All admin routes (Waves 1-8): agents CRUD+discover+test+security-scan, orchestrators, applications, tokens, sessions, LLM providers, monitoring-config ✓
- Runs READ: `GET /runs`, `/runs/stats`, `/runs/{id}`, `/runs/{id}/tasks`, `/runs/{id}/artifacts` ✓
- Runs WRITE: cancel, delete, bulk-delete ✓
- Auth (login, me, refresh) → auth-go 200 ✓
- `/health/live`, `/health/ready` ✓

**Still not covered by Go:**
- `GET /runs/context/{ctx}/artifacts` → no Traefik rule, no Go handler (not used by admin UI)
- `GET /apps`, `GET /apps/{slug}` → Traefik only captures WS/SSE paths for apps
- `GET /health` (bare) → no Traefik router
- Applications export/import/restore/middleware-wirings → Python-only endpoints, not yet migrated

---

## Known blockers

1. Auth admin CRUD (users/roles/teams) — not exposed since Python auth removed. Needs Go port.
2. Go Temporal worker is not yet implemented as the active orchestration path. `them-worker` (Python) is locked to `profiles: [legacy]` and must NOT be started. The Go worker (`them-go-worker`) is defined in `docker-compose.dev.yml` behind `profiles: [go-worker]` but is not yet production-ready. No orchestration runs until this is complete.
3. A2A server (`/a2a/*`) still on Python — not yet migrated to Go.
4. Wave 9 items 3–6 (session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims, live two-tenant verification) remain open.

---

## Hard constraints (always in force)

- DB name: `them`, never `odin`
- Never query `auth_service.*` from bridge — use `go/internal/auth/` or `app/services/auth_client.py`
- Bootstrap tenant ID: `00000000-0000-0000-0000-000000000001`
- `go test ./...` must pass before every commit
- `go/TEST_INDEX.md` updated in same commit as new Go tests
- Secrets never in logs — use `cfg.SafeString()`
- Never `git add .` or `git add -A`
- **Python is permanently retired.** `them-bridge` MUST remain behind `profiles: [legacy]`. Do NOT move it back to default profile. Do NOT patch Python for compatibility.
- **Go Temporal worker MUST resolve orchestrators by `AppOrchestratorID` UUID** — never globally by name.
- **SEC-01/SEC-02 are dead paths.** Legacy Python globally-namespaced orchestrator Redis keys will not be written again.
- **Agent registry Redis key is `them:agents:registry:{tenant_id}`.** The old global key must not be written or read.
- **EP cache key is `"{tenantID}:{slug}"`.** Cache invalidation payload on `them:ep:config:changed` is always `"{tenantID}:{slug}"`.
- **`entry_points.tenant_id` is NOT NULL.** All new EPs inherit tenant from parent application. `UNIQUE(tenant_id, slug)` enforced at DB level.
- **No secrets in Definition JSONB, Component Definition JSONB, export files, logs, or Temporal history. Only secret references.**

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
