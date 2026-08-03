# Next Session Bridge Handover
# Updated: 2026-08-03 (post Registry-Backed Component Model Review)

---

## Current State

**Branch:** `main`
**HEAD before this session's doc push:** `55ad66e docs(arch): Application Execution Architecture Review — three-layer model, source-of-truth decision, revised migration roadmap`
**HEAD after this session:** see `git log --oneline -1` (the Registry-Backed Component Model commit).
**Push status:** pushed to `origin/main`.

---

## This Session's Work

Wrote `docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md` — the definitive review
of the registry-backed component model proposal. It **extends** (does not replace) the Option C
three-layer model from `APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md` (55ad66e) with a Component
Registry: every reusable Canvas object gets a versioned Component Definition, and Application
Definitions store component **instances** that reference those definitions by portable identity +
version, carrying only per-instance config overrides and secret references. This adds the registry to
Wave 9. No code was changed. Wave 8 scope is **unchanged** (runtime + bulk-delete only).

**Prior session (55ad66e)** wrote `APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md` — the three-layer
model + Option C source-of-truth decision + Temporal-model confirmation + Waves 8–15 roadmap. That
review's decisions are all still in force; this review builds on them.

---

## Registry-Backed Component Model — Decisions This Review (2026-08-03)

Full document: `docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md`. Governs Wave 9.

1. **Proposal ACCEPTED with modifications.** Registry **extends** Option C; it is a fourth
   *design-time* concept beside Layer 1 (Application Definition). The three runtime layers and the
   Temporal model are unchanged. Registry is **never on the runtime hot path** — config is resolved
   and pinned at publish; the worker reads only the projection.

2. **Common Component Definition contract** (shared by all kinds): `id, kind, namespace, name,
   version, display_name, description, implementation_type, configuration_schema, default_config,
   capabilities (typed tags), input_schema, output_schema, credential_schema, scope, tenant_id,
   status, content_hash, enabled`. Instance-only fields (name/slug, config overrides, secret
   bindings, canvas position) stay OFF the definition.

3. **Storage = Option C: base `component_definitions` table + kind subtypes.** `them.agents` and
   `them.middleware_defs` **become** the `agent`/`middleware` subtypes with id-shared rows — nothing
   relocates, all FKs to `agents.id`/`middleware_defs.id` keep working. Fallback = Option D (a
   registry *view*) only if Wave 9 cannot ALTER `agents`.

4. **Table fates:** `agents` → agent Component Definition (keep+extend). `middleware_defs` →
   middleware Component Definition (absorb as subtype). `orchestrators` (global) → **DEPRECATED**,
   replaced by a builtin `llm-orchestrator` definition; its cluster-wide `name`-unique coupling dies
   with it; Temporal loader fallback to it is removed (not repointed) after backfill. `app_orchestrators`,
   `entry_points`, `middleware_wirings` → **kept as compiled projections** (shape unchanged + pin/stamp
   columns). `applications` → container.

5. **Application Definition v2 format:** `components[]` (instances with portable `definition_ref` +
   `config` + `secret_bindings`) + typed `connections[]` (`entry`/`delegation`/`tool`, `via[]` for
   middleware chains). Replaces the 55ad66e `bindings[]/agent_refs[]/delegation[]/tool_grants[]`.

6. **Entry points = instances of an IMPLICIT protocol definition** (the `entry_point_type` string).
   No EP-definition DB table (would risk the `epconfig(slug)` hot path for zero reuse payoff).
   Optional 5 builtin palette rows for Canvas form uniformity; projection never joins to them.

7. **Orchestrators = instances of the builtin `llm-orchestrator` definition** (option c). Tenant
   authored orchestrator templates available later for free (just another registry row). NOT the
   legacy `orchestrators` table.

8. **Version pinning = EXACT version at publish, all kinds.** No latest/range. Definition edits
   affect only new publishes; fleet update = explicit auditable re-pin+republish.

9. **Config resolution:** `default_config ⊕ tenant/env defaults ⊕ instance.config` (deep-merge,
   right-wins), validated vs `configuration_schema`, frozen into the projection at publish. Runtime
   reads only the projection.

10. **Secrets airtight:** references only in JSON (`secret://scope/name`); Fernet ciphertext only in
    projection; resolved+encrypted at publish; NEVER in JSONB, exports, logs, or Temporal history.

11. **Portable refs = `{kind, namespace, name, version}`**; UUID is a resolution cache; portable tuple
    is the stable cross-environment key; version = integer revision; resolve UUID-first then tuple.

12. **Hard constraints honored:** `app_orchestrators.name` stays immutable on the *instance* (Temporal
    key); `entry_points.slug` stays on the *instance* (epconfig key); both projections + read paths
    unchanged.

---

## Architecture Decisions Carried In From 55ad66e (still in force)

1. **Source of truth → Option C (Hybrid).** JSON **Application Definition** is the canonical
   *design* source of truth; the existing relational rows (`app_orchestrators`, `entry_points`,
   `middleware_wirings`) are demoted to the **compiled runtime projection**. This overturns the
   prior review's "relational rows are the source of truth" for the design artifact, while keeping
   it true for the runtime. Runtime readers (epconfig, Temporal loaders, rate limiter) are
   unchanged — the definition layer is purely additive.

2. **Drift prevention:** projection is written *only* by the compile step (write monopoly),
   stamped with `definition_id` + `definition_hash`, and runs are pinned to the `definition_id`
   they started under (mid-run publish can never corrupt an in-flight conversation).

3. **Save vs Publish split.** Canvas edits a *draft* definition — cheap, no compile, no cache
   flush. **Publish** validates + compiles the projection + stamps the hash + flushes caches. This
   removes today's problem where every PATCH recompiles and flushes even for a layout nudge.

4. **Execution model CONFIRMED (already correct).** One `OrchestrationWorkflow` per `context_id`;
   leaf agent call → Temporal **Activity** (`invoke_agent_activity`); orchestrator→orchestrator
   delegation → **Child Workflow** (`execute_child_workflow`, depth cap `_MAX_SUB_ORCH_DEPTH=3`);
   parallel via `asyncio.gather` bounded by `Semaphore(max_parallel_tools)`; clarification via
   `wait_condition` + `submit_human_response` signal; native `handle.cancel()`. The parallel-
   clarify-nested-delegate example scenario is already expressible. Go has a parallel worker on the
   `them-orchestration-go` queue.

5. **Versioning added NOW** (columns, not full UX). New `application_definitions` table +
   `applications.active_definition_id` + `applications.source_definition_hash` +
   `runs.definition_id`, with a revision-1 backfill for existing apps. Diff UI / rollback UX
   deferred but not blocked.

6. **Export/import/restore REPLACED** with the lossless Application Definition v1 object. Old
   graph-reconstruction export is lossy (drops middleware→agent edges, keys) and is deprecated;
   importer accepts both forms for one migration window.

7. **`app_orchestrators` kept**, reframed as a compiled *binding* projection, renamed only
   conceptually/in-docs (no physical table rename in Waves 8–11).

8. **ADK compatibility:** an ADK agent is a catalog UUID + the `InvokeAgentResult` invoke
   contract; internals opaque. No schema change to the definition or projection.

9. **Wave 8 re-scoped:** export moves OUT of Wave 8 into Wave 9 (export must serialize a
   *definition*, which doesn't exist until Wave 9). Wave 8 = runtime + bulk-delete only.

Full document: `docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md`.

---

## Remaining Migration Overview (from this review, Section 17)

| Wave | Domain | Scope | Category |
|---|---|---|---|
| **8** | App runtime special-ops (pure) | `PUT /{id}/runtime`, `POST /bulk-delete` | migrate |
| **9** | **Application Definition + Component Registry Layer** | `component_definitions` base + agent/mw subtype adoption + backfill; `application_definitions` + backfill; portable-ref resolver + `configuration_schema` validator; `CompileDefinition` (v2 `components[]`/`connections[]`); draft/publish/validate/activate; export/import/restore/clone/rollback; component-definition CRUD; `runs.definition_id` | redesign-before-migrate (gates 10–14) |
| **10** | Runs read/audit tail | stats, contexts, tasks, artifacts, bulk-delete, delete | migrate |
| **11** | Run control (Temporal) | cancel + signal delivery | migrate |
| **12** | Apps runtime surface (product-critical) | `/apps`, `/apps/{slug}` REST, tasks | migrate |
| **13** | A2A server + agent card | agent-card, `/a2a`, `/a2a/push` | redesign (invoke /a2a skill) |
| **14** | Admin agent/orch/middleware/system-agent ops | test/discover/security-scan; middleware-defs CRUD now = component-def CRUD; system-agents; middleware-wirings write (definition-driven); per-AO test-llm/voice/tts; begin `orchestrators` legacy deprecation | redesign (registry-driven) |
| **15** | Voice + legacy deprecation + Python removal | voice/webrtc; deprecate legacy `orchestrators/{name}` voice + `ws/orchestrate/{name}`; **drop legacy `orchestrators` table** + dead `edges`/`orchestrator_id` columns; remove Python | migrate + deprecate |

Blocked-by-Wave-9: import, restore, clone, rollback, export, middleware-wirings writes, component
authoring, portable references, config resolution, secret resolution.

---

## Approved Next Task — Wave 8 (re-scoped: runtime + bulk-delete ONLY)

Two endpoints in Go:
1. `PUT /api/v1/admin/applications/{id}/runtime` — JSONB `runtime_config` UPDATE + cache flush.
2. `POST /api/v1/admin/applications/bulk-delete` — hard-delete + cache flush.

**Export is NO LONGER in Wave 8** — it moves to Wave 9 as a definition-based export.

Steps (detail in review Section 18):
1. Traefik: add `them-go-apps-subroutes` rule (exact rule in `APPLICATION_MODEL_ARCHITECTURE_REVIEW.md`
   §4a) to `docker-compose.yml` and `docker-compose.traefik.yml`. Do not otherwise touch Traefik.
2. Go cache: add `FlushApplicationOrchCaches(ctx, appID, orchNames)` —
   `DEL them:app:{app_id}:orch:{name}`, `DEL them:orch:loc:{name}`, `DEL them:agents:registry`,
   `PUBLISH them:ep:config:changed {app_id}`.
3. DAL (`go/internal/admin/dal/applications.go`): `UpdateRuntimeConfig`, `ListAppOrchestratorNames`,
   `BulkDeleteApplications` (tenant-scoped, `RETURNING id`).
4. Handlers (`go/internal/admin/applications.go`): `PutRuntime`, `BulkDelete`. Runtime struct =
   five fields, pointer types for nullable ints; `session_timeout_minutes` accepted+persisted,
   not enforced. Bulk-delete: pre-fetch orch names → hard-delete → flush after delete.
5. Wire routes in `go/internal/admin/router.go`.
6. Go tests per handler (mock DAL, assert flush call order); update `go/TEST_INDEX.md`.
7. `go test ./...` — zero new failures.
8. Deploy + smoke through Traefik; run `scripts/tests/test_routing_fix_contracts.py` inside
   `them-bridge`; app-write routing tests must PASS not skip.
9. Commit (staged file-by-file), push, update `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`.

Do NOT port `compile_graph`/`export_graph` or any definition-layer code in Wave 8.

---

## Deferred / Deprecated Areas

**Deferred to Wave 9 (definition + registry layer):** export, import, restore, clone, rollback,
`CompileDefinition`, `application_definitions` table + backfill, **`component_definitions` base table
+ agent/middleware subtype adoption + backfill**, portable-reference resolver, `configuration_schema`
validator, component-definition CRUD, middleware-wirings writes, `runs.definition_id`.
**Deferred beyond v1/v2 (no format break needed):** static `flows[]` DSL (deterministic sequence/
condition/loop), OR/quorum join policy engine change, revision diff UI + rollback UX, physical
rename of `app_orchestrators`, **tenant-authored orchestrator template definitions, fleet re-pin
UX, semver version constraints, Option-D→Option-C physicalization if the view fallback is used**.
**Deprecated (remove in Wave 15):** old lossy graph-export format; `POST /orchestrators/{name}/
transcribe|tts`; `GET(WS) /ws/orchestrate/{name}`.
**Known engine cleanups (later wave):** replace hardcoded `_MAX_SUB_ORCH_DEPTH`/`max_parallel`
defaults with `policies.*` from the definition; fix fragile `agent__orch__` double-prefix name
coupling with an explicit `ref_kind`/`transport` field.

---

## Files Most Relevant to the Next Task

| File | Relevance |
|---|---|
| `docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md` | **This review — governs Wave 9 component registry + v2 definition format** |
| `docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md` | Governs Waves 8–15 — Option C three-layer model, Temporal confirmation |
| `docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md` | Earliest review — Wave 8 endpoint details, Traefik rule §4a |
| `app/routers/admin_applications.py` | Python handlers for runtime/bulk-delete (lines ~617–791) + `_flush_orch_caches` |
| `app/services/app_compiler.py` | `compile_graph`/`export_graph`/`validate_graph` (Wave 9 port source) |
| `go/internal/admin/applications.go` | Go app handler (add PutRuntime, BulkDelete) |
| `go/internal/admin/dal/applications.go` | Go DAL (add the three methods) |
| `go/internal/admin/router.go` | Route wiring |
| `go/internal/epconfig/epconfig.go` + `pgx.go` | runtime_config reader + `them:ep:config:changed` pub/sub |
| `go/internal/crypto/fernet.go` | Fernet (needed Wave 9, not Wave 8) |
| `go/internal/temporal/` | Go worker/workflow (delegation parity for Python removal) |
| `docker-compose.yml`, `docker-compose.traefik.yml` | Traefik rule additions |

---

## Hard Constraints for Next Session

1. Wave 8 = runtime + bulk-delete ONLY. Do NOT port `compile_graph`/`export_graph`.
2. Do NOT build the definition layer in Wave 8 — that is Wave 9.
3. Do NOT use `git add .`/`-A` — stage only Wave 8 files.
4. Run `go test ./...` before every commit — zero new failures.
5. Update `go/TEST_INDEX.md` in the same commit as any new Go test.
6. Update `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` after cutover.
7. Secrets: never commit `.env`/`secrets.local`. DB name `them`, never `odin`.
8. Do NOT change Traefik beyond adding `them-go-apps-subroutes`.

---

## Exact First Prompt for Next Session

```
Continue from main (latest commit). Read these first:
  docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md        ← Option C three-layer model
  docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md      ← Wave 9 registry model (context for the Wave 9 handover; NOT needed to code Wave 8)
  docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md            ← Wave 8 endpoint detail + Traefik rule §4a
  docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md
  go/CLAUDE.md

Implement Wave 8 — Application Runtime Special-Ops — RUNTIME + BULK-DELETE ONLY.
Export is NOT in Wave 8 (moved to Wave 9 as a definition-based export).
The registry-backed component model is Wave 9 context only — do NOT build any of it in Wave 8.

Step 1: Add Traefik rule `them-go-apps-subroutes` (exact rule in APPLICATION_MODEL_ARCHITECTURE_REVIEW.md
        §4a) to docker-compose.yml and docker-compose.traefik.yml. Do not otherwise touch Traefik.
Step 2: Add FlushApplicationOrchCaches(ctx, appID, orchNames) to the Go cache layer:
        DEL them:app:{app_id}:orch:{name}, DEL them:orch:loc:{name}, DEL them:agents:registry,
        PUBLISH them:ep:config:changed {app_id}.
Step 3: DAL in go/internal/admin/dal/applications.go: UpdateRuntimeConfig, ListAppOrchestratorNames,
        BulkDeleteApplications (tenant-scoped, RETURNING id).
Step 4: Handlers in go/internal/admin/applications.go: PutRuntime, BulkDelete.
        Runtime struct = 5 fields, pointer nullable ints, session_timeout_minutes accept+persist not enforce.
        Bulk-delete: pre-fetch orch names, hard-delete, flush AFTER delete.
Step 5: Wire routes in go/internal/admin/router.go.
Step 6: Go tests for both handlers (mock DAL, assert flush order). Update go/TEST_INDEX.md.
Step 7: go test ./... — zero new failures.
Step 8: Deploy with --profile go, smoke through Traefik, run scripts/tests/test_routing_fix_contracts.py
        inside them-bridge — app-write routing tests must PASS not skip.
Step 9: Commit (staged file-by-file), push, update REMAINING_ROUTE_OWNERSHIP_INVENTORY.md.

Do NOT implement export/import/restore or compile_graph. Stop after step 9.
Then prepare the Wave 9 handover (Application Definition + Component Registry Layer) — it MUST
incorporate docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md: the
component_definitions base table, agent/middleware subtype adoption, v2 components[]/connections[]
format, portable references, exact version pinning, and the secret-references-only model.
```

### Startup commands

```bash
cd /home/avi/them
git log --oneline -5
docker compose -f docker-compose.yml -f docker-compose.local.yml ps
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile go up -d them-go-bridge
cd go && go test ./...
```
