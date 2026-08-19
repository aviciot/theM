# Current Session State — the-M
# Last updated: 2026-08-18
# Replaces: NEXT_SESSION_HANDOVER.md, NEXT_SESSION_BRIDGE_HANDOVER.md

---

## HEAD

Branch: `main`
Commit: `148235b` — fix(runrecorder): correct schema column names and wire step/usage/final-output recording

---

## Deployment state

**Active deployment: local Linux server**

Stack startup command:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
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

Verified clean restart with `--profile temporal` (go-bridge and go-worker are now default services):
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

## Per-EP History Config + Tenant Isolation + Playground JWT fix — COMPLETE (50c9d71, 2026-08-18)

**Per-EP memory configuration (3882a9c):**
- `db/032_ep_memory_config.sql` — 6 new columns on `entry_points`: `memory_enabled`, `history_window`, `summarize_every_n_calls`, `memory_raw_fallback_n`, `summarizer_provider`, `summarizer_model`. Also adds `tasks.tenant_id` with index.
- `go/internal/history/pgx.go` — all methods gained `tenantID string` parameter; `LoadHistory` filters by `AND ($2 = '' OR t.tenant_id = $2::uuid)`. `resolveRootTaskID` inserts `tenant_id` column.
- `go/internal/orchestrator/` — `HistoryLoader`/`CheckpointWriter`/`SummaryStore` interfaces all carry `tenantID`; call sites pass `rctx.TenantID`
- `go/internal/temporal/workerconfig/loader.go` — `LoadRunConfig` takes 4 args (+ `entryPointID`); EP query reads memory config from `entry_points` when `entryPointID != ""`
- `go/internal/temporal/workflow.go` — `WorkflowInput.EntryPointID string` added
- `go/internal/ws/handler.go` + `go/internal/sse/handler.go` — `EntryPointID: handle.EPConfig.EPID` wired into `WorkflowInput`
- `go/internal/admin/dal/publish.go` — `EntryPointRow` gets memory fields; `UpsertEntryPoint` writes them
- `go/internal/admin/service/publish.go` — extracts `ep_memory[ep.instance_id]` from orchestrator canvas config during compile
- `frontend/src/app/admin/applications/page.tsx` — Memory section inside each EP block in orchestrator properties panel; fields: enable, history_window, summarize_every, raw_fallback, provider+model; history_window removed from Loop Tuning

**Playground JWT fix (50c9d71):**
- `go/cmd/them/main.go` — `jwtFallbackAuthenticator` chain: try opaque bearer token cache first, then `auth.ValidateHS256JWT` so session JWTs work as `?token=` param on token-mode EPs

## Multi-turn History + Summarizer — COMPLETE (3862a43, 2026-08-17)

New packages:
- `go/internal/history/pgx.go` — `HistoryLoader` + `CheckpointWriter` + `SummaryStore` backed by pgxpool; JOIN task_messages→tasks on context_id; JSONB envelope with canonical_role for lossless role round-trip; `resolveRootTaskID` creates tasks row on first message
- `go/internal/summarizer/summarizer.go` — in-process LLM call; drains text_delta; max 1024 tokens; no microservice dependency
- `go/internal/orchestrator/summary.go` — `maybeSummarize` trigger (len(history) > HistoryWindow); older/recent split; summary persisted as system message; `SummaryConfig` struct

Modified:
- `go/internal/orchestrator/orchestrator.go` — `MemoryEnabled`/`SummarizeEveryNCalls`/`MemoryRawFallbackN` config fields; user message now checkpointed before LLM call (was missing)
- `go/internal/temporal/workerconfig/loader.go` — loads `memory_enabled`, `summarize_every_n_calls`, `memory_raw_fallback_n`, `summarizer_provider`, `summarizer_model` from DB; fixed `loadProviderKey` to decrypt via `crypto.DecryptStored` (was returning raw ciphertext — latent bug)
- `go/cmd/worker/main.go` — wires `historyStore`, `WithHistoryLoader`, `WithCheckpointer`; conditional `WithSummarizer` when `MemoryEnabled && SummarizerProvider != ""`
- `go/internal/temporal/activities.go` — restore `ConfigLoadError` non-retryable Temporal error type (was broken by merge)
- `go/internal/llm/anthropic.go` — map `RoleTool→RoleUser` for Anthropic API compatibility (fixes 400 on second message)
- `go/internal/ws/handler.go` — 15s ping/pong keepalive (fixes WS drop during Temporal schedule-to-start); `wfRun.Get(context.Background(), nil)` (fixes context-canceled on WS disconnect propagating to workflow monitor)
- `frontend/src/app/admin/playground/page.tsx` — fix token field: `msg.content || msg.text || ''`

Test state: `go test ./...` — all packages pass, Docker build clean.

---

## Tenant isolation fixes — COMPLETE (ec66b8a, 2026-08-18)

Four data-isolation bugs in Go runtime fixed:

1. **GetRunTasks**: Added `tenantID` param + `EXISTS` subquery on `them.runs` to verify run ownership before returning tasks. Cross-tenant run_id now returns empty result.
2. **GetRunArtifacts**: Same pattern as GetRunTasks.
3. **GetContextMessages**: Added `tenantID` param + `t.tenant_id = $2::uuid` filter. Fixed JSONB parsing: reads from `tm.parts->'parts'` envelope first (correct format written by `history.WriteMessage`), falls back to direct `jsonb_array_elements(tm.parts)` for legacy rows. Excludes summary messages (`summary IS NOT TRUE`).
4. **resolveRootTaskID**: `findQ` now includes `AND ($3 = '' OR tenant_id = $3::uuid)`. Both initial find and post-ON-CONFLICT re-query pass `tenantID`. Prevents context_id from tenant A reusing a root task row owned by tenant B.

Propagated through handler→service→Dal interface for all three methods. All three fake implementations updated. Three new isolation tests added.

`go test ./...` — **36 packages, 0 failures**

---

## Phase 1 — A2A Agent Runtime (`them-agent-runtime`) — COMPLETE (2026-08-18)

Design: `docs/architecture-v2/CANVAS_A2A_AGENT_GENERATION_FULL.md`

### What was built

**Go 1.25 + a2a-go/v2 SDK:**
- `go/go.mod`: bumped `go 1.23` → `go 1.25.0`; added `github.com/a2aproject/a2a-go/v2 v2.5.0`
- `Dockerfile.go`, `Dockerfile.go-worker`, `Dockerfile.auth-go`: `golang:1.23-alpine` → `golang:1.25-alpine`

**`go/internal/agentgen/` package:**
- `spec.go` — `AgentSpec` (compiled, no secrets, slot names only), `SkillSpec`, `StepSpec`, `StepType` constants (input/llm/http/transform/response/branch/loop/parallel/a2a_call/human_wait/stream_out), all step config types
- `context.go` — `InvocationContext` (holds decrypted credentials in-memory only; `String()` never logs them), `InvocationPolicies`
- `binding.go` — `AppAgentBinding` (EncryptedCredentials = Fernet ciphertext only), `ResolveCredentials` method
- `redistaskstore.go` — `RedisTaskStore` (ownership check: mismatch → `ErrTaskNotFound`, not 403; key `them:agent:task:{task_id}`, TTL 24h), `TaskState` struct (no credentials)
- `interpreter.go` — `Interpreter.Execute` pipeline walker; Phase 1: `input`, `llm`, `http`, `transform`, `response` implemented; others return "not implemented in Phase 1"
- `agentgen_test.go` — 8 tests (S1-48): credential redaction, per-binding isolation, cross-tenant ownership, interpreter steps

**`go/cmd/agent-runtime/main.go`:** stateless chi HTTP server on port 9300
- Routes: `GET /healthz`, `GET /agents/{slug}/.well-known/agent-card.json`, `POST /agents/{slug}`
- `parseInvocationContext` reads `X-Them-Tenant-Id/Application-Id/Agent-Id/Binding-Id` headers
- Spec cache: `sync.Map`, TTL 60s, invalidated by Redis pub/sub `them:agents:registry:{tenant_id}`
- Three invariants enforced: definition_id pinned at publish (409 if mismatch), slug cross-check vs JWT agent_id (403 if mismatch), Redis task ownership (ErrTaskNotFound = 404 not 403)
- `anthropicProviderAdapter.Complete` wraps `llm.AnthropicProvider.Stream` + drains `text_delta` events
- Pool: `db.New(ctx, cfg.DSN())`, Redis: `cache.New(...)`, crypto key: `crypto.DeriveKey(cfg.SecretKey)`

**`go/internal/agentregistry/registry.go`:** `InvocationMeta` struct + `InvokeWithMeta` method
- Phase 1: injects `X-Them-Tenant-Id`, `X-Them-Application-Id`, `X-Them-Agent-Id`, `X-Them-Binding-Id` headers
- If `meta == nil`, delegates to existing `Invoke` (backwards-compatible)
- Phase 3 upgrade path: replace headers with signed JWT (THE_M_INVOCATION_JWT_KEY) — no other callers to update

**`Dockerfile.agent-runtime`:** golang:1.25-alpine builder → alpine:3.21 runtime; port 9300; healthcheck via wget

**`docker-compose.yml`:** `them-agent-runtime` service added
- `profiles: [agents]`, `deploy: replicas: 2` (no `container_name`), internal Docker network only
- Env: `THE_M_CRYPTO_KEY`, `THE_M_INVOCATION_JWT_KEY`, `ANTHROPIC_API_KEY_PLATFORM`
- NOT exposed through Traefik — internal calls only (from `them-go-bridge` via `InvokeWithMeta`)

**`go/TEST_INDEX.md`:** S1-48 added (8 tests); total 638 S1 / 641 overall

### Three invariants (must remain in force)
1. `binding.DefinitionID != nil && *binding.DefinitionID != spec.DefinitionID` → 409 (version pinning)
2. `spec.Slug != slug` (URL vs spec) → 403 (slug cross-check)
3. `ts.TenantID != tenantID || ts.ApplicationID != applicationID` → `ErrTaskNotFound` (ownership, 404 not 403)

### Security constraints (always in force)
- Credentials decrypted per-request, held only in `InvocationContext.Credentials`, never logged/persisted
- `InvocationContext.String()` is explicitly redacted (slot count only)
- `TaskState` has no credentials; credentials re-decrypted from binding on each resume
- Port 9300 NOT in Traefik — internal Docker network only

### Test state
`go test ./...` — **36 packages, 0 failures** (includes new S1-48)

---

## Run History Stabilization — COMPLETE (148235b, 2026-08-19)

Four root causes for empty Flow/Steps/Answer tabs fixed:

1. **`RecordUsage` wrong columns**: `input_tokens`/`output_tokens`/`recorded_at` → `tokens_input`/`tokens_output`/`created_at`; added required `provider`/`model`/`cost_usd` params; added `UPDATE them.runs` rollup for `total_tokens_in/out/cost_usd`
2. **`RecordStep` broken**: Replaced with `RecordAgentStep` writing correct columns (`agent_slug`, `iteration`, `input` jsonb, `output`, `status`, `latency_ms`, `started_at`, `ended_at`). Old `RecordStep` made a no-op.
3. **`SetFinalOutput`**: New method writes `them.runs.final_output` at completion → Answer tab now populated
4. **Orchestrator wiring**: `StepRecorder` interface added; `executeTools` now calls `RecordAgentStep` per invocation with timing; `WithUsageRecorder`+`WithStepRecorder` wired in `cmd/worker/main.go`

Test state: `go test ./...` — **36 packages, 0 failures** (3 new tests S1-09 extended: TestRecordAgentStep, TestSetFinalOutput, TestRecordStep_isNoop; count 22→25)

**Effect**: New runs will populate `run_steps` rows (→ Flow tree + Steps tab) and `final_output` (→ Answer tab). Old runs (before this commit) remain empty.

---

## Phase 2 — Canvas Agent Builder CRUD — COMPLETE (2026-08-19)

All backend + frontend pieces were built in the previous session and the DB migration was applied this session.

What was built:
- `db/035_agent_definitions.sql` — `them.agent_definitions` table (tenant_id, agent_slug, revision, definition JSONB, status, UNIQUE constraint). Applied to live DB.
- `go/internal/admin/dal/agent_definitions.go` — DAL: GetNextAgentRevision, CreateAgentDefinition, GetAgentDefinition, ListAgentDefinitions, UpdateDraftAgentDefinition, DeleteDraftAgentDefinition
- `go/internal/admin/service/agent_definitions.go` — service: validateAgentDefinition (rejects secret values, validates credential slots + skills + step IDs), CreateDraft, GetDefinition, ListDefinitions, UpdateDraft, DeleteDraft
- `go/internal/admin/agent_definitions.go` — HTTP handler: POST/GET/GET/{id}/PUT/{id}/DELETE/{id}
- `go/internal/admin/router.go` — AgentDefinitionsHandler wired under JWT + RequireSuperAdmin + AdminTenantMiddleware
- `docker-compose.yml` — Traefik router `them-go-agent-defs` at priority 120 for PathPrefix(`/api/v1/admin/agent-definitions`)
- `frontend/src/lib/api.ts` — listAgentDefinitions, getAgentDefinition, createAgentDefinition, updateAgentDefinition, deleteAgentDefinition
- `frontend/src/app/admin/agents/builder/page.tsx` — 739-line ReactFlow canvas with all 11 node types (Agent Root, Skill, Input, LLM, HTTP, Transform, Response, Branch, Loop, Parallel, A2A Call, Human Wait, Stream Out), credential-slot picker, properties panels, save/load/list
- `frontend/src/app/admin/agents/page.tsx` — "Build Visually" button navigates to `/admin/agents/builder`
- `go/internal/admin/service/agent_definitions_test.go` — 21 tests (S1-49); `go/TEST_INDEX.md` updated
- `go/test ./...` — 36 packages, 0 failures

Live verified (2026-08-19):
- `POST /api/v1/admin/agent-definitions` → 201 `{"id":"...","revision":1}` ✓
- `GET /api/v1/admin/agent-definitions` → `[{status:"draft",...}]` ✓
- `GET /api/v1/admin/agent-definitions/{id}` → full row ✓

---

## Phase 3 — Compile + Publish Pipeline + Application Binding UI — COMPLETE (2026-08-19)

### What was built

**DB (applied to live DB):**
- `db/036_canvas_a2a_runtime.sql` — `them.agent_runtime_specs` + `them.app_agent_bindings`
  - `agent_runtime_specs`: `(agent_id PK FK→agents, definition_id FK→agent_definitions, spec_hash, credential_schema JSONB, agent_card_json JSONB, published_at)`
  - `app_agent_bindings`: `(id UUID PK, application_id FK, agent_id FK, definition_id FK, credential_bindings JSONB AES-GCM ciphertext, config_overrides JSONB, policies JSONB, UNIQUE(application_id, agent_id))`
- `db/037_agents_transport_canvas.sql` — extends `agents_transport_check` to `ANY ('a2a_async', 'canvas_a2a')`. **Critical for publish**: `PublishCanvasAgent` CTE inserts `transport='canvas_a2a'`; the original CHECK only allowed `'a2a_async'` which caused the publish endpoint to return 500.

**Go — `go/internal/agentgen/compiler.go` (NEW):**
- `Compile(agentID, tenantID, definitionID, agentSlug, rawJSON) (*AgentSpec, []CompileError)`
- Error codes: INVALID_JSON, MISSING_FIELD, DUPLICATE_SLOT, DUPLICATE_SKILL, DUPLICATE_STEP, UNKNOWN_STEP_TYPE, UNDECLARED_SLOT, DANGLING_NEXT, DANGLING_BRANCH, CYCLE_DETECTED, INVALID_SLUG
- DFS cycle detection + topological sort via `topoSort()`; slug sanitizer (hyphens→underscores, truncate to 48)
- 15 tests in `compiler_test.go` (S1-50)

**Go — `go/internal/admin/dal/agent_definitions_publish.go` (NEW):**
- `PublishCanvasAgent` — 3-table atomic CTE: `component_definitions` → `agents` → `agent_runtime_specs` (ON CONFLICT DO UPDATE for republish)
- `MarkAgentDefinitionPublished` — updates status to 'published'
- `namespace = tenantID` for tenant-scoped agents

**Go — `go/internal/admin/dal/agent_bindings.go` (NEW):**
- `UpsertAgentBinding`, `GetAgentBindingStatus`, `ListAgentBindings`, `DeleteAgentBinding`
- All responses return `credential_set: map[string]bool` only — never ciphertext or plaintext

**Go — `go/internal/admin/service/agent_definitions_publish.go` (NEW):**
- `ValidateAgentDefinition` (compile without persisting), `PublishAgentDefinition` (compile + 3-table write + mark published + Redis signal)
- `UpsertBinding`, `GetBindingStatus`, `ListBindings`, `DeleteBinding`
- AES-256-GCM: `encryptAESGCM(key, plaintext)` → `base64url(nonce||ct||tag)`; `DecryptAESGCM` exported for runtime layer
- `ErrEncryptionKeyMissing` sentinel; `AgentCompileError` type (NOT ValidationError — collision with Phase C type)
- 11 tests in `agent_definitions_publish_test.go` (S1-51)

**Go — `go/internal/admin/agent_definitions.go` (MODIFIED):**
- Routes: `POST /agent-definitions/{id}/validate`, `POST /agent-definitions/{id}/publish`
- Constructor: `NewAgentDefinitionsHandler(db, cache, fernetKey []byte)`
- `Svc()` accessor for `AgentBindingsHandler` reuse

**Go — `go/internal/admin/agent_bindings.go` (NEW):**
- `AgentBindingsHandler` with routes under `/admin/applications/{app_id}`:
  - `GET/POST/PUT/DELETE /agent-bindings`, `GET/POST/PUT/DELETE /agent-bindings/{agent_id}`

**Go — `go/internal/admin/router.go` (MODIFIED):**
- `NewAgentDefinitionsHandler(db, cache, fernetKey)` wired; bindings routes under `tenantScoped.Route("/applications/{app_id}", ...)`

**`docker-compose.yml` (MODIFIED):**
- Traefik router `them-go-agent-bindings` at priority 121 for `PathRegexp(^/api/v1/admin/applications/[^/]+/agent-bindings(/[^/]+)?)` → `them-go-bridge-svc`

**Frontend — `frontend/src/lib/api.ts` (MODIFIED):**
- 5 new types: `AgentCompileError`, `AgentValidationResult`, `AgentPublishResult`, `AgentBindingSlotStatus`, `AgentBindingUpsertBody`
- 6 new API methods: `validateAgentDefinition`, `publishAgentDefinition`, `listAgentBindings`, `getAgentBinding`, `upsertAgentBinding`, `deleteAgentBinding`

**Frontend — `frontend/src/app/admin/agents/builder/page.tsx` (MODIFIED):**
- State: `validating`, `publishing`, `publishError`, `publishedRevision`
- `handleValidate` + `handlePublish` handlers
- Toolbar buttons: Validate (green) + Publish (cyan) between Delete and Save

**Frontend — `frontend/src/app/admin/applications/page.tsx` (MODIFIED):**
- `AgentCredentialPanel` component: fetches binding status + definition slots, renders masked password inputs per slot, Save/Remove buttons
- Shown in agent node Properties panel when `transport === 'a2a'` and `definition_id` is present

**Docs updated in same commit:** `docs/SCHEMA.md`, `docs/REDIS.md`, `go/TEST_INDEX.md`

### Security constraints (always in force)
- Credential values **NEVER** in JSONB, logs, exports, Temporal history, or HTTP responses
- Only AES-256-GCM ciphertext in `app_agent_bindings.credential_bindings`; decrypted per-request only
- HTTP responses: `credential_set: {slot_name: bool}` only
- Bindings API requires `ErrEncryptionKeyMissing` guard when `fernetKey` not configured

### Test state
`go test ./...` — **37 packages, 0 failures** (S1-50: 15 tests, S1-51: 11 tests)
TypeScript: **clean** (0 errors)

### Live end-to-end verification (2026-08-19)
- Validate → 200 `{"valid":true}` ✓
- Publish → 200 `{"agent_id":"...","revision":1,"spec_hash":"fcc0b..."}` ✓
- `component_definitions` row: status='published' ✓
- `agents` row: transport='canvas_a2a', status='published' ✓
- `agent_runtime_specs` row: spec_hash matches ✓
- `agent_definitions` status: 'published' ✓

### Key bug fixed in this session
- **Root cause of publish 500**: `agents_transport_check` only allowed `'a2a_async'`; canvas agents use `'canvas_a2a'`. Fixed by `db/037_agents_transport_canvas.sql`.

---

## Next recommended task

**Phase 4 — Agent Runtime DB Wiring**

Goals:
1. Wire `them-agent-runtime` to load compiled specs from `agent_runtime_specs` table — `loadSpecByAgentID` is currently a stub returning empty spec. Implement the real pgx query: `SELECT spec FROM them.agent_runtime_specs WHERE agent_id=$1`.
2. Smoke test the full invocation path: Publish agent → invoke via agentregistry → `them-agent-runtime` executes steps.
3. Optionally add Phase 4 step types to `interpreter.go`: `branch`, `loop`, `parallel`, `a2a_call`, `human_wait`, `stream_out`.

Do NOT begin Wave 10 (auth admin CRUD Go port) in the same session as Phase 4.

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
2. E2E run test not yet performed — multi-turn history round-trip with real LLM not verified on live stack.
3. `db/032_ep_memory_config.sql` applied live — verify `entry_points` and `tasks` have new columns before the next session: `\d them.entry_points` and `\d them.tasks`.
4. A2A server (`/a2a/*`) still on Python — not yet migrated to Go.
5. Wave 9 items 3–6 (session/rate-limit tenant scope, tenant provisioning, multi-tenant JWT claims, live two-tenant verification) remain open.
6. `them-go-bridge` container startup: must use `--project-name them_gateway` when starting via compose to share the `them-network` network with the `them_gateway` project. Without it, postgres/redis conflict. Command: `docker compose -p them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge`.
7. ~~`agent_runtime_specs` and `app_agent_bindings` tables not yet created~~ — **RESOLVED**: `db/036_canvas_a2a_runtime.sql` applied, both tables exist.
8. `them-agent-runtime` reads from `agent_runtime_specs` (`loadSpecByAgentID` stub) — **partially resolved**: table exists; real pgx query still needs implementation (Phase 4 item).
9. ~~Publish endpoint returning 500~~ — **RESOLVED**: `agents_transport_check` extended to include `'canvas_a2a'` via `db/037_agents_transport_canvas.sql`. Publish end-to-end verified.

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
