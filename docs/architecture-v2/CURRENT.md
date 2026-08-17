# Current Session State — the-M
# Last updated: 2026-08-17
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `c0fdb1a` — feat(worker): E2E per-run orchestrator resolution from DB

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

**Phase B — Application Definition CRUD — COMPLETE** (bcb2943)

Routes added (all under `/api/v1/admin/applications/{id}/definitions`):
- `POST   .../definitions` → 201 `{"id":"...","revision":N}`
- `GET    .../definitions` → 200 `[AppDefinition,...]`
- `GET    .../definitions/{def_id}` → 200 `AppDefinition`
- `PUT    .../definitions/{def_id}` → 200 `{"id":"...","updated":true}`
- `DELETE .../definitions/{def_id}` → 204

**Phase D — Application Definition UI — COMPLETE** (HEAD)

Routes added:
- `GET /api/v1/admin/component-definitions` → 200 `[ComponentDefinitionSummary,...]`

Files added/modified:
- `go/internal/admin/dal/registry.go` — `ListComponentDefinitions` DAL (published+enabled, builtins+tenant)
- `go/internal/admin/registry.go` — `RegistryHandler` + `GET /admin/component-definitions`
- `go/internal/admin/registry_test.go` — S1-45 handler test (633 total)
- `go/internal/admin/router.go` — wire RegistryHandler; add `them-go-component-defs` Traefik label
- `go/internal/admin/service/service.go` — `ListComponentDefinitions` added to Dal interface
- `go/TEST_INDEX.md` — S1-45 added
- `frontend/src/lib/api.ts` — 10 types + 7 API methods (listComponentDefinitions, listDefinitions, createDefinition, updateDefinition, deleteDefinition, validateDefinition, publishDefinition)
- `frontend/src/app/admin/applications/page.tsx` — BuilderView (ReactFlow) retired; DefinitionView added (component palette + form editor + properties panel); view state `'builder'` → `'definition'`; AppCard "Open Builder" → "Definition"
- `docker-compose.yml` — Traefik router `them-go-component-defs` at priority 120

Live verified: `GET /api/v1/admin/component-definitions` through Traefik returns 15 component definitions. TypeScript compiles clean.

**Phase C — Application Definition Validate + Publish + Compile — COMPLETE** (e625fbc)

Routes added:
- `POST .../definitions/{def_id}/validate` → 200 `{"valid":true|false,"errors":[...]}`
- `POST .../definitions/{def_id}/publish` → 200 `{"definition_id":"...","revision":N,"definition_hash":"sha256:..."}`

DB migration applied (live DB):
- `db/031_phase_c_compiler_pins.sql` — `source_definition_id` (FK) + `source_definition_hash` on `app_orchestrators` and `entry_points`

Packages added/modified:
- `go/internal/admin/dal/publish.go` — 5 DAL methods: `PublishDefinition` (mark published + update active_definition_id), `UpsertAppOrchestrator` (INSERT ON CONFLICT by application_id+name), `UpsertEntryPoint` (INSERT ON CONFLICT by tenant_id+slug), `DeactivateStaleOrchestrators`, `DeactivateStaleEntryPoints`
- `go/internal/admin/service/publish.go` — `RegistryResolver` interface, `ValidationReport`/`ValidationError` types, `ValidateDefinition` (structural + registry resolution + connection integrity), `PublishDefinition` (resolve → upsert orchestrators → upsert entry_points → deactivate stale → atomic publish)
- `go/internal/admin/service/definitions.go` — `registry` field + `NewDefinitionServiceWithRegistry`
- `go/internal/admin/service/service.go` — Dal interface extended with 5 new methods
- `go/internal/admin/definitions.go` — `Validate`/`Publish` HTTP handlers + updated constructor
- `go/internal/admin/router.go` — registry resolver wired via `registryQuerierAdapter`
- `go/internal/admin/service/definitions_publish_test.go` — 22 new tests (S1-43, S1-44)
- `go/TEST_INDEX.md` — S1-43 + S1-44 documented; total 632 tests

Key behaviors:
- **Validation** returns `{"valid":false,"errors":[...]}` for missing/disabled/deprecated components, dangling connections, duplicate instance_ids, invalid protocols — never 4xx unless definition not found
- **Registry resolution** per `registry.ResolveForPublish`: tenant-safe, blocks disabled + deprecated definitions, no cross-tenant lookup
- **Compiler** maps orchestrator components → `app_orchestrators` (UPSERT on application_id+name), entry_points → `entry_points` (UPSERT on tenant_id+slug); stale rows deactivated
- **Component pins**: `component_definition_id` + `component_version` set on projection rows
- **Source tracking**: `source_definition_id` + `source_definition_hash` on projection rows
- **AllowedAgentIDs**: populated from "tool" connections using resolved component definition UUIDs (= agents.id via Option C FK)
- **Published immutability**: re-publish → 409; update/delete published → 409
- **Second revision**: new draft required; second publish reconciles stale rows
- **Transaction**: `PublishDefinition` DAL atomically marks published + sets active_definition_id; upserts are idempotent before the gate

Live verification (Python OFF, all scenarios):
- CREATE draft 201 ✓, VALIDATE valid=true 200 ✓, PUBLISH 200 ✓
- active_definition_id set ✓, status='published' ✓
- app_orchestrators row created with component_definition_id+component_version+source_definition_id ✓
- entry_points row created with app_orchestrator_id bound ✓
- Re-publish same def → 409 ✓
- Second revision: new draft/validate/publish ✓, active_definition_id updated ✓
- Stale projection rows deactivated ✓
- Dangling connection → validate returns valid=false, publish → 422 ✓
- Active_definition_id unchanged after failed publish ✓

Test state: `go test ./...` — **33 packages, 0 failures, 22 new tests (S1-43, S1-44)** (Phase C)
E2E wiring: `go test ./...` — **33 packages, 0 failures, 6 new tests (S1-29 extended, S1-46)** (c0fdb1a)

**Runs READ/UI — COMPLETE** (cf953cf)

- `GET /runs/stats`, `GET /runs/{id}` (RunDetail), `GET /runs/{id}/tasks`, `GET /runs/{id}/artifacts`
- Auth fixed: moved to `JWT + RequireSuperAdmin + AdminTenantMiddleware`
- Python-OFF verified for all 5 GET endpoints

**Agents Store — COMPLETE** (888861b)

- `POST /agents/discover`, `POST /agents/{id}/test`, `POST /agents/{id}/security-scan`
- `AdminTenantMiddleware` for admin routes
- Go auth service cutover; Python auth container removed

---

## E2E Run Wiring — COMPLETE (c0fdb1a, 2026-08-17)

The Go Temporal worker now loads per-run orchestrator config from DB on every activity execution:

- `go/internal/temporal/workerconfig/loader.go` — `PgxLoader.LoadRunConfig`: queries `app_orchestrators` JOIN `applications`, resolves agent UUIDs to slugs, reads provider key from `applications.provider_keys`
- `go/internal/temporal/activities.go` — `OrchestratorFactory` interface + optional `ConfigLoader`/`Factory` fields on `Activities`; `RunOrchestratorActivity` uses per-run config when `AppOrchestratorID` is set
- `go/cmd/worker/main.go` — fixed `cache.NewAuthRedisClient` for agentregistry; wires `PgxLoader`, `runOrchestratorFactory`, and per-run `Activities`
- 6 new tests (S1-29 extended, S1-46 added); 33 packages pass

Worker is running and polling `them-orchestration-go`. Next: create an application, publish it, and trigger a real run to verify E2E.

---

## Next recommended task

**End-to-end run test** — Create a published application with an orchestrator, publish it, trigger a run via the WS/SSE/A2A endpoint, and verify the Go Temporal worker picks it up, loads config from DB, and returns a real LLM response.

Then: **Wave 9 — Multi-tenant runtime enablement** (session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims)
- Read `docs/architecture-v2/R6_TENANT_ARCHITECTURE_REVIEW.md` Section 15 before starting Wave 9.

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
2. Go Temporal worker (`them-go-worker`) is running and wired for per-run config loading. E2E run test not yet performed — a published application with a real LLM call has not been verified end-to-end against the live stack.
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
