# Next Session Bridge Handover
# Updated: 2026-08-02 (post Application Execution Architecture Review)

---

## Current State

**Branch:** `main`
**HEAD before this session's doc push:** `fb212ae docs(arch): Application Model Architecture Review — pre-Wave 8 design decisions`
**HEAD after this session:** see `git log --oneline -1` (the Application Execution Architecture Review commit).
**Push status:** pushed to `origin/main`.

---

## This Session's Work

Wrote `docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md` — the definitive
three-layer architecture review that governs implementation for Waves 8–15. It re-opens and
**revises** the source-of-truth decision from the prior review, confirms the Temporal execution
model, and re-sequences the migration roadmap. No code was changed.

---

## Architecture Decisions Made This Review

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
| **9** | **Application Definition Layer** | `application_definitions` table + backfill; `CompileDefinition` (Go port of compile_graph); draft/publish/validate/activate; export/import/restore/clone/rollback; `runs.definition_id` | redesign-before-migrate (gates 10–14) |
| **10** | Runs read/audit tail | stats, contexts, tasks, artifacts, bulk-delete, delete | migrate |
| **11** | Run control (Temporal) | cancel + signal delivery | migrate |
| **12** | Apps runtime surface (product-critical) | `/apps`, `/apps/{slug}` REST, tasks | migrate |
| **13** | A2A server + agent card | agent-card, `/a2a`, `/a2a/push` | redesign (invoke /a2a skill) |
| **14** | Admin agent/orch/middleware/system-agent ops | test/discover/security-scan, middleware-defs, system-agents, middleware-wirings, per-AO test-llm/voice/tts | redesign (middleware now definition-driven) |
| **15** | Voice + legacy deprecation + Python removal | voice/webrtc; deprecate legacy `orchestrators/{name}` voice + `ws/orchestrate/{name}`; remove Python | migrate + deprecate |

Blocked-by-definition-layer: import, restore, clone, rollback, export, middleware-wirings writes.

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

**Deferred to Wave 9 (definition layer):** export, import, restore, clone, rollback, `CompileDefinition`,
`application_definitions` table + backfill, middleware-wirings writes, `runs.definition_id`.
**Deferred beyond v1 (no format break needed):** static `flows[]` DSL (deterministic sequence/
condition/loop), OR/quorum join policy engine change, revision diff UI + rollback UX, physical
rename of `app_orchestrators`.
**Deprecated (remove in Wave 15):** old lossy graph-export format; `POST /orchestrators/{name}/
transcribe|tts`; `GET(WS) /ws/orchestrate/{name}`.
**Known engine cleanups (later wave):** replace hardcoded `_MAX_SUB_ORCH_DEPTH`/`max_parallel`
defaults with `policies.*` from the definition; fix fragile `agent__orch__` double-prefix name
coupling with an explicit `ref_kind`/`transport` field.

---

## Files Most Relevant to the Next Task

| File | Relevance |
|---|---|
| `docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md` | This review — governs Waves 8–15 |
| `docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md` | Prior review — Wave 8 endpoint details, Traefik rule §4a |
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
  docs/architecture-v2/APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md   ← governing decisions
  docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md       ← Wave 8 endpoint detail + Traefik rule §4a
  docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md
  go/CLAUDE.md

Implement Wave 8 — Application Runtime Special-Ops — RUNTIME + BULK-DELETE ONLY.
Export is NOT in Wave 8 (moved to Wave 9 as a definition-based export).

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
Then prepare the Wave 9 handover (Application Definition Layer).
```

### Startup commands

```bash
cd /home/avi/them
git log --oneline -5
docker compose -f docker-compose.yml -f docker-compose.local.yml ps
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile go up -d them-go-bridge
cd go && go test ./...
```
