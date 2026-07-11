# the-M — Temporal Migration Progress

**Started**: 2026-07-11  
**Plan**: `temporal-migration-plan.md`  
**Reference**: `temporal-implementation-reference.md`

---

## Status Overview

| Phase | Description | Status | Notes |
|---|---|---|---|
| 1 | Infrastructure — Temporal server, worker, UI | ✅ Complete | Containers in `temporal` profile |
| 2 | Port core loop to Workflow + Activity | ✅ Complete | UnsandboxedWorkflowRunner; DB init at startup |
| 3 | Token streaming + bridge integration | ✅ Complete | Dual-channel Redis (ctx + run), TEMPORAL_ENABLED flag |
| 4 | Remaining agents (vision, docu-writer, debate) | 🔄 Pending | |
| 5 | Human-in-the-loop Signal-based pause/resume | 🔄 Pending | |
| 6 | Cutover — remove reaper, sticky sessions | 🔄 Pending | |
| 7 | Cleanup — remove dead code | 🔄 Pending | |

---

## Phase 1 — Infrastructure ✅

**Completed**: 2026-07-11

### What was done

- `postgres/init/009_temporal_databases.sql` — creates `temporal` and `temporal_visibility` DBs on first boot
- `app/temporal/__init__.py` — package marker
- `app/temporal/config.py` — `TemporalConfig` dataclass + `get_temporal_config()`
- `app/temporal/client.py` — `get_temporal_client()` singleton (mirrors redis_client pattern)
- `app/temporal/worker.py` — worker entrypoint with empty workflow/activity lists (Phase 1 smoke-test only)
- `Dockerfile.worker` — worker container image (same base as bridge)
- `requirements.txt` — added `temporalio==1.9.0`
- `app/config.py` — added `TEMPORAL_ENABLED`, `TEMPORAL_HOST`, `TEMPORAL_NAMESPACE`, `TEMPORAL_TASK_QUEUE`
- `docker-compose.yml` — added `temporal-frontend`, `temporal-ui`, `temporal-admin-tools`, `them-worker` under `profiles: [temporal]`
- `scripts/tests/run_tests.py` — test_15 updated: checks Temporal containers if running, skips gracefully if not

### How to start Temporal

```bash
# Create Temporal DBs in existing Postgres (one-time for existing volumes)
docker exec them-postgres psql -U them -c "CREATE DATABASE temporal;"
docker exec them-postgres psql -U them -c "CREATE DATABASE temporal_visibility;"

# Start Temporal stack
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile temporal up -d

# Verify
docker compose --profile temporal ps
# temporal-frontend, temporal-ui, temporal-admin-tools, them-worker should all be running

# Temporal UI
open http://localhost:3111
```

### Tests run

```
python scripts/tests/run_tests.py 15
```

Result: PASS (Temporal containers checked if running, skipped if not)

### Decisions made

- All Temporal containers in `profiles: [temporal]` — opt-in, no impact on default stack
- Temporal UI bound to `127.0.0.1:3111` (not proxied through Traefik — operator tool only)
- `them-worker` has no Traefik labels (not an HTTP service)
- `temporalio==1.9.0` pinned for reproducibility

---

## Phase 2 — Core Loop to Workflow + Activity ✅

**Completed**: 2026-07-11

### What was done

- `app/temporal/shared.py` — all serializable dataclasses for Workflow ↔ Activity boundary
- `app/temporal/loaders.py` — pure DB/cache helpers extracted from `task_runner.py`
- `app/temporal/serde.py` — ToolCall↔dict conversion, tool list builder, context injection
- `app/temporal/activities.py` — 6 activities: `load_orchestration_context`, `init_run`, `plan_turn`, `invoke_agent`, `summarize_context`, `finalize_run`
- `app/temporal/workflows.py` — `OrchestrationWorkflow`: full agentic loop, signal/query handlers, `continue_as_new` threshold
- `app/temporal/worker.py` — updated to register `OrchestrationWorkflow` + `ALL_ACTIVITIES`, `UnsandboxedWorkflowRunner`, DB init at startup
- `scripts/test_temporal_workflow.py` — Phase 2 validation script
- `scripts/test_temporal_resume.py` — Resume-after-kill validation script

### Key fixes during implementation

- `GlobalConfig` doesn't expose Temporal settings directly → read from `Settings()` in temporal/config.py
- Temporal Python SDK requires ≥1 activity — added `_noop_activity` placeholder in Phase 1 (removed in Phase 2)
- Worker must call `init_db()` at startup once — not per-activity — to share DB connection pool
- `Agent` ORM model has `display_name`, not `name` → fixed in `agent_to_config()`
- `create_provider()` doesn't accept `base_url` → dropped from call
- `workflow.CancellationScope` doesn't exist in temporalio 1.9.0 → use plain `await` in finally
- `UnsandboxedWorkflowRunner` required to avoid `os.stat` sandbox restriction from structlog
- `PlanTurnResult.serialized_assistant_turn` must be JSON string (not dict/list) to pass Temporal's type converter

### Validation

- Workflow completed end-to-end against `echo_test` orchestrator + `a2a_echo` agent
- DB projection confirmed: `them.runs` completed, `them.tasks` root + delegated both completed, `them.run_steps` step recorded
- Tests 10, 11, 12, 15 — 69 passed, 0 failures

### How to validate manually

```bash
docker cp scripts/test_temporal_workflow.py them-worker:/tmp/test_temporal_workflow.py
docker exec them-worker python3 /tmp/test_temporal_workflow.py
```

### Issues log

| Issue | Resolution |
|---|---|
| `UnsandboxedWorkflowRunner` needed | structlog accesses `os.stat` which is blocked by default Temporal sandbox |
| `PlanTurnResult` type conversion failure | Changed `serialized_assistant_turn` from `Optional[dict]` to `Optional[str]` (JSON-encoded) |
| `workflow.CancellationScope` missing | Removed shielded scope; finalize runs in normal flow |

---

## Phase 3 — Bridge Integration ✅

**Completed**: 2026-07-11

### What was done

- `app/temporal/bridge_client.py` — two functions: `start_orchestration_workflow()` (starts or attaches to existing workflow), `stream_run_events()` (dual-phase Redis subscription: context channel → run-specific channel), `cancel_workflow()`
- `app/routers/ws_orchestrator.py` — `TEMPORAL_ENABLED` flag, `_run_temporal()` and `_run_legacy()` helpers, cancel propagates to `workflow_handle.cancel()`
- `app/routers/apps.py` — same flag in REST fire-and-forget, SSE, and WS entry points

### Key design decisions

- Bridge subscribes to `them:dash:run:{context_id}:ctx` first (known upfront) to receive `ready` event with `run_id`
- Then switches to `them:dash:run:{run_id}:tokens` for all subsequent events (tokens, tool_start, tool_done, done)
- `init_run_activity` publishes `ready` event to both channels so the switch is seamless
- `TEMPORAL_ENABLED=false` (default) leaves all existing paths 100% unchanged — legacy branch is unmodified code

### Validation

- Tests 10, 11, 15: 64 passed, 0 failures
- Bridge imports compile cleanly; `_TEMPORAL_ENABLED=False` by default so no behavior change

---

## Phase 4 — Remaining Agents 🔄

*Not started*

---

## Phase 5 — Human-in-the-Loop 🔄

*Not started*

---

## Phase 6 — Cutover 🔄

*Not started*

---

## Phase 7 — Cleanup 🔄

*Not started*

---

## Issues Log

| Date | Phase | Issue | Resolution |
|---|---|---|---|
| — | — | — | — |

---

## Key Decisions Log

| Date | Decision | Rationale |
|---|---|---|
| 2026-07-11 | Workflow ID = `ctx-{context_id}` | Maps to existing multi-turn model; eliminates per-iteration DB rebuild |
| 2026-07-11 | Token streaming via Redis `them:dash:run:{run_id}:tokens` | Keeps streaming UX identical; Activity return value is the deterministic result |
| 2026-07-11 | `them.tasks`/`them.runs` kept as reporting projection | Dashboard, billing, run history APIs unchanged |
| 2026-07-11 | Single worker pool on `them-orchestration` | Per-agent concurrency enforced inside Activities; simpler ops |
| 2026-07-11 | All Temporal services in `temporal` profile | Zero impact on default stack during migration |
