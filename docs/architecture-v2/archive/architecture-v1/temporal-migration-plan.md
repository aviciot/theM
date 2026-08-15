# the-M — Temporal Migration Plan

**Platform**: the-M (codebase at `/opt/docker/odin`)  
**Document type**: Implementation plan (phase-by-phase)  
**Companion documents**:
- `current-orchestration-architecture.md` — As-Is reference
- `the-M_Temporal_Migration.md` — To-Be design decisions
- `temporal-implementation-reference.md` — Developer file/function reference

**Date**: 2026-07-11  
**Planned by**: Opus 4  
**Task queue**: `them-orchestration`

---

## Open Questions — Resolved

Before any code is written, the Opus planning pass resolved the following open questions from the To-Be design document:

| Question (§9 of To-Be doc) | Resolution |
|---|---|
| Workflow ID scheme: one per `context_id` vs one per `run_id` | **One per `context_id`**, using `signal-with-start` per turn. See rationale below. |
| Token-level streaming approach | **Confirmed**: `plan_turn_activity` publishes tokens to Redis `them:dash:run:{run_id}:tokens`; bridge subscribes. Terminal `done`/`error` comes from `workflow_handle.result()`, not the Redis stream. |
| `them.tasks`/`them.runs` — keep as projection or reshape? | **Keep as-is for reporting**. Activities own all Postgres writes. Temporal Event History is the execution authority. |
| Worker topology: single pool vs per-agent-type pools | **Single pool** on `them-orchestration`. Per-agent `max_concurrency` enforced inside each Activity or at Worker-level config. |

### Workflow ID Scheme — Rationale

One Workflow per `context_id`, ID format: `ctx-{context_id}`.

Each new user turn uses `signal-with-start`: if the Workflow for that context is still alive it receives the new turn as a Signal; if it completed, a new one starts and loads prior history once from the Postgres projection. This directly maps to the-M's existing `context_id` multi-turn model and eliminates the per-iteration `_build_messages_from_store()` DB re-query — conversation history lives in `self.messages` (Workflow state).

To prevent unbounded Event History growth on long conversations, the Workflow calls `continue_as_new` when iteration count or message list size crosses a threshold, carrying `self.messages` and `self.context_summary` forward.

Fallback (if signal-with-start proves complex for the debate stack): one Workflow per `run_id` (`run-{run_id}`), history reloaded from Postgres projection each turn — identical to current behavior, no regression.

---

## Phase 1 — Infrastructure

**Goal**: Stand up self-hosted Temporal (Postgres-backed), an empty worker container, and the Temporal UI. Zero changes to application request paths.

### Files to create

| File | Purpose |
|---|---|
| `postgres/init/009_temporal_databases.sql` | Creates `temporal` and `temporal_visibility` databases inside `them-postgres`. Runs automatically on a fresh data volume. For existing volumes: execute manually via `docker exec them-postgres psql -U them -c "CREATE DATABASE temporal; CREATE DATABASE temporal_visibility;"` |
| `app/temporal/__init__.py` | New package marker |
| `app/temporal/config.py` | `TemporalConfig` dataclass + `get_temporal_config()` reading env vars |
| `app/temporal/client.py` | `async def get_temporal_client() -> Client` — connect + module-level singleton |
| `app/temporal/worker.py` | Worker entrypoint: `async def main()` — connects client, constructs `Worker(...)`, `await worker.run()`. In Phase 1, workflows/activities lists are empty or contain a single smoke-test `PingWorkflow`. |
| `Dockerfile.worker` | Same base image as `Dockerfile`; entrypoint `python -m app.temporal.worker` |

### Files to modify

| File | Change |
|---|---|
| `requirements.txt` | Add `temporalio==1.9.0` |
| `app/config.py` | Add `TEMPORAL_ENABLED: bool = False`, `TEMPORAL_HOST: str = "temporal-frontend:7233"`, `TEMPORAL_NAMESPACE: str = "default"`, `TEMPORAL_TASK_QUEUE: str = "them-orchestration"` to `Settings` |
| `docker-compose.yml` | Add four services on `them-network`: `temporal-frontend` (image `temporalio/auto-setup:1.25.2`, Postgres-backed), `temporal-ui` (image `temporalio/ui:2.31.2`, bound to `127.0.0.1:3111:8080`), `temporal-admin-tools` (image `temporalio/admin-tools:1.25.2`, for CLI), `them-worker` (build `Dockerfile.worker`, same DB/Redis/Anthropic env as `them-bridge`, plus `TEMPORAL_HOST`, `TEMPORAL_TASK_QUEUE`, `TEMPORAL_ENABLED=true`, no Traefik labels) |

### Key env for `temporal-frontend` container

```yaml
environment:
  - DB=postgres12
  - DB_PORT=5432
  - POSTGRES_USER=${POSTGRES_USER}
  - POSTGRES_PWD=${POSTGRES_PASSWORD}
  - POSTGRES_SEEDS=them-postgres
  - DBNAME=temporal
  - VISIBILITY_DBNAME=temporal_visibility
  - DYNAMIC_CONFIG_FILE_PATH=config/dynamicconfig/development-sql.yaml
depends_on:
  them-postgres:
    condition: service_healthy
```

### Validation

- `docker compose up -d temporal-frontend temporal-ui them-worker` — all three start cleanly
- Temporal UI reachable at `http://localhost:3111`
- `them-worker` logs show it polling `them-orchestration` task queue
- Run the smoke-test `PingWorkflow` from inside the worker container; confirm `"pong"` result in Temporal UI
- `them-bridge` request paths are fully untouched — existing WS/SSE/API routes work as before

---

## Phase 2 — Port Core Loop to Workflow + Agent Call to Activity

**Goal**: Reproduce `task_runner.run()` as `OrchestrationWorkflow` and `_invoke_agent()` as `invoke_agent_activity`. Validate end-to-end against `a2a-echo`, including a deliberate mid-run worker kill to confirm resume.

### Files to create

| File | Purpose |
|---|---|
| `app/temporal/shared.py` | All serializable dataclasses crossing the Workflow/Activity boundary (no ORM objects, no `Decimal` on wire — use `str`) |
| `app/temporal/loaders.py` | Pure helpers moved from `task_runner.py`: `_load_orchestrator_row`, `_load_agents`, `_ensure_agent_skills`, `_build_provider`, `_OrchestratorProxy`, `_compose_tool_description`, `_build_agent_tool_schema` |
| `app/temporal/serde.py` | `ToolCall ↔ dict` conversion, `NeutralTool` list build, message history serialization, `build_agent_tool_input(agent_input_schema, tool_call_input, injected_context) -> dict` (ports the typed/text `__context__` injection logic from `_run_one`) |
| `app/temporal/activities.py` | All Activity definitions (see specifications below) |
| `app/temporal/workflows.py` | `OrchestrationWorkflow` |

### Files to modify

| File | Change |
|---|---|
| `app/temporal/worker.py` | Register `OrchestrationWorkflow` and all activities from `activities.py` |

### Dataclasses in `shared.py`

```
OrchestrationInput(
    orchestrator_name: str, user_message: str, user_id: int,
    token_payload: dict, session_id: str, context_id: str, run_id: str
)

PlanTurnInput(
    run_id: str, context_id: str, root_task_id: str,
    orchestrator_name: str, system_prompt: str,
    provider_name: str, model: str, api_key_encrypted: str | None, base_url: str | None,
    messages: list[dict], tools: list[dict],
    max_tokens: int, msg_seq: int,
    price_in: str, price_out: str
)

PlanTurnResult(
    tool_calls: list[dict], final_answer: str | None,
    serialized_turn: list[dict],
    input_tokens: int, output_tokens: int
)

InvokeAgentInput(
    run_id: str, context_id: str, root_task_id: str, iteration: int,
    agent_id: str, agent_slug: str, tool_call_id: str,
    tool_input: dict, timeout_seconds: int,
    injected_context: str | None
)

InvokeAgentResult(
    status: str, result_text: str, file_parts: list[dict],
    remote_task_id: str | None, latency_ms: int
)
```

### Activity Specifications

**`load_orchestration_context_activity(orchestrator_name, user_id, token_payload, context_id, current_task_id)`**
- Timeout: 30s, retry: 3
- Wraps: `_load_orchestrator_row`, `_load_agents`, `_ensure_agent_skills`, tool-list build, pricing load, `_load_context_history` (once — not per iteration)
- Returns: plain dict with orchestrator config, agents list, tools list, `price_in`/`price_out` as strings, `prior_history` as serialized provider-native message list

**`init_run_activity(OrchestrationInput, run_id, root_task_id)`**
- Timeout: 30s, retry: 3
- Pre-generated `run_id` and `root_task_id` passed in (generated by `workflow.uuid4()`) so retries are idempotent
- Wraps: `run_recorder.start_run`, `task_store.create_task(kind=root)`, `task_store.transition("working")`, `task_store.record_message(seq=0)`, `_publish_dash(run_start)`

**`plan_turn_activity(PlanTurnInput) -> PlanTurnResult`**
- Timeout: 5 min, retry: 3
- Builds provider from passed config (not ORM), calls `provider.stream_call()`
- `token` events → publish to Redis `them:dash:run:{run_id}:tokens`
- Terminal `tool_calls_ready`/`done` → captures `tool_calls`, `raw_response`, `usage`, `final_answer`
- Writes projection: `run_recorder.record_usage`, `task_store.add_tokens_used`, `task_store.record_message(assistant turn, seq=msg_seq)`, `_publish_dash(usage)`
- Returns `PlanTurnResult`

**`invoke_agent_activity(InvokeAgentInput) -> InvokeAgentResult`**
- Timeout: `agent.timeout_seconds`, heartbeat timeout: 30s, retry: 2
- Body is the port of `_invoke_agent()` — nearly identical logic
- Key difference: emits `activity.heartbeat({"state": ..., "remote_task_id": ...})` on each adapter status/task_created event instead of the old `status_queue` — puts `remote_task_id` in Temporal Event History (resolves pain point #6)
- Publishes `agent_status`, `tool_done`, `file` events to Redis `them:dash:run:{run_id}:tokens`
- Writes projection: delegated child task create/transition, `run_recorder.record_step/complete_step`, `context_service.record_and_cache_artifact`, `task_store.record_message(tool results)`

**`summarize_context_activity(context_id, root_task_id, orch_config) -> str | None`**
- Timeout: 60s, retry: 2
- Wraps `memory_service.summarize_context()`
- Returns summary text; Workflow stores it in `self.context_summary` (no Redis write needed)

**`finalize_run_activity(run_id, root_task_id, status, final_answer, iterations, total_in, total_out, total_cost, error)`**
- Timeout: 30s, retry: 5 (must succeed)
- Run inside `workflow.CancellationScope(shield=True)` so it executes even during cancel
- Wraps: `run_recorder.complete_run`, final-answer artifact, `task_store.transition(root, terminal)`, `_publish_dash(run_end)`

### Workflow Structure (`OrchestrationWorkflow.run`)

```
1. Generate run_id, root_task_id via workflow.uuid4()
2. execute_activity(load_orchestration_context_activity)
   → seeds self.messages with prior_history + user message
3. execute_activity(init_run_activity)
   → creates them.runs + them.tasks rows; yields ready event via Redis
4. while self.iteration < max_iterations:
   a. Budget check (self.tokens_used vs budget_tokens)
   b. execute_activity(plan_turn_activity, messages=self.messages)
      → appends serialized_turn to self.messages
      → if final_answer: break
   c. asyncio.gather(*[execute_activity(invoke_agent_activity, ...) for tc in tool_calls])
      → bounded by max_parallel_tools (Workflow-level asyncio.Semaphore is allowed — pure Python)
   d. Append tool results to self.messages
   e. self.agent_calls_since_summary += len(tool_calls)
   f. if memory_enabled and threshold reached:
        summary = execute_activity(summarize_context_activity)
        self.context_summary = summary
        self.agent_calls_since_summary = 0
5. execute_activity(finalize_run_activity)
```

**Cancellation handling**: wrap the loop body in `try/except asyncio.CancelledError`; on cancel, call `finalize_run_activity` inside a shielded scope with `status="canceled"`.

### Validation (Phase 2)

- Start `OrchestrationWorkflow` directly via a standalone client script inside `them-worker` (not through the bridge) against an `a2a-echo` orchestrator
- Confirm in Temporal UI: all 5 activity steps appear in the correct order
- Confirm `them.runs`, `them.tasks`, `them.run_steps`, `them.artifacts` rows match what the legacy generator produced for identical input
- **Resume test**: `docker kill them-worker` mid-run (while `invoke_agent_activity` is executing); restart worker; confirm Workflow replays and completes — this validates pain point #3
- Diff projection rows from a Temporal run vs a legacy run for same input and goal

---

## Phase 3 — Token Streaming Side-Channel + Bridge Integration

**Goal**: Wire `them-bridge` WS/SSE edges to start Workflows and stream live events from the Redis side-channel, behind the `TEMPORAL_ENABLED` feature flag. Validated with `a2a-echo`.

### Files to create

| File | Purpose |
|---|---|
| `app/temporal/bridge_client.py` | `start_orchestration_workflow()` and `stream_run_events()` — the bridge's interface to Temporal |

### Key functions in `bridge_client.py`

**`async def start_orchestration_workflow(*, orchestrator_name, user_message, user_id, token_payload, context_id, session_id) -> tuple[WorkflowHandle, str]`**
- Computes `workflow_id = f"ctx-{context_id}"`
- Calls `client.start_workflow(OrchestrationWorkflow, input, id=workflow_id, task_queue="them-orchestration")` or `signal_with_start` for an already-running context
- Returns `(workflow_handle, run_id)` — `run_id` comes from the Workflow's first output event on the Redis channel or from a Workflow Query

**`async def stream_run_events(run_id, workflow_handle, emit_fn)`**
- Subscribes to Redis `them:dash:run:{run_id}:tokens` via `redis_client.pubsub()`
- Forwards each decoded event to `emit_fn` (the WS/SSE send function)
- Terminates when it receives a terminal event (`done`/`error`) **or** when `await workflow_handle.result()` resolves — whichever comes first
- Always emits the final `done`/`error` from `workflow_handle.result()` to guarantee client sees completion even if the last Redis publish is dropped

### Files to modify

| File | Change |
|---|---|
| `app/routers/ws_orchestrator.py` | When `settings.TEMPORAL_ENABLED`: replace the `task_runner.run()` generator with `start_orchestration_workflow()` + `stream_run_events()`. Map client `cancel` message → `workflow_handle.cancel()` (real propagation — pain point #2). Keep legacy path behind `not TEMPORAL_ENABLED`. |
| `app/routers/apps.py` | Same swap in `_run_and_stream` (SSE) and `ws_entry`; REST fire-and-forget becomes `start_orchestration_workflow` without streaming |

### Validation (Phase 3)

- WS client against `a2a-echo` orchestrator sees identical event sequence: `ready` → `token`… → `tool_start` → `agent_status` → `tool_done` → `done`
- Disconnect the WS client mid-run; confirm the Workflow continues to completion in Temporal UI; projection rows finalize (pain point #4)
- Reconnect with the same `context_id`; confirm re-attach streams subsequent events
- Token streaming spike: confirm latency is acceptable under 10 concurrent runs before proceeding to Phase 4

---

## Phase 4 — Remaining Agents

**Goal**: Bring vision-agent, docu-writer, and the debate stack (4 agents) onto the Workflow path.

### Files to modify

| File | Change |
|---|---|
| `app/temporal/serde.py::build_agent_tool_input` | Ensure typed-input agents (docu-writer, debate agents with `input_schema.properties`) correctly receive `__context__` as a separate key; text agents get it prepended to `message`. Port the branching logic from `task_runner._run_one` lines 852–859. |

No new files. No changes to agent containers.

### Validation (Phase 4)

- Run each agent's representative orchestrator through the Workflow path
- **Debate stack**: confirms concurrent Activity fan-out at 3+ agents per iteration (the `asyncio.gather` equivalent)
- **docu-writer**: confirms typed-input (`data` part) + file artifact → `file` events reach the client
- **vision-agent**: confirms long-timeout agent (custom A2A, pre-SDK) works within `invoke_agent_activity` timeout config

---

## Phase 5 — Human-in-the-Loop

**Goal**: Replace the dead `input-required` state with durable Signal-based pause/resume.

### Files to create

| File | Purpose |
|---|---|
| `app/routers/admin_signals.py` (or extend `runs.py`) | `POST /api/v1/runs/{workflow_id}/signal` — sends `submit_human_response` Signal to a running Workflow. Auth: JWT + admin role. |

### Files to modify

| File | Change |
|---|---|
| `app/temporal/workflows.py::OrchestrationWorkflow` | Add `self.human_response: dict \| None = None`. Add `@workflow.signal def submit_human_response(self, payload: dict)`. Add `@workflow.query def get_status(self) -> dict`. After `invoke_agent_activity` returns `InvokeAgentResult(status="input-required")`: call `await workflow.wait_condition(lambda: self.human_response is not None, timeout=<business_timeout>)`; feed response back into `self.messages`; clear `self.human_response`. |
| `app/temporal/activities.py::invoke_agent_activity` | Surface `input_required` from `InvokeAgentResult`. When the adapter emits an `input_required` event (currently ignored), set `status="input-required"` and return early — do not mark as failed. |
| `app/services/task_store.py` | `task_store.transition(root, "input-required")` called from an Activity so the dashboard projection reflects the paused state. |

### Validation (Phase 5)

- Drive a scenario where an agent returns `input-required` (can use a modified `a2a-slow` that returns this state)
- Confirm in Temporal UI: Workflow shows as Running but paused at `wait_condition`
- Kill and restart `them-worker`; confirm Workflow is still suspended (durably) after restart
- Send Signal via `POST /api/v1/runs/{workflow_id}/signal`; confirm Workflow resumes from exactly that point

---

## Phase 6 — Cutover

**Goal**: Route all new runs through Workflows exclusively. Remove the reaper loop and Traefik sticky sessions.

### Files to modify

| File | Change |
|---|---|
| `docker-compose.yml` | Remove `loadbalancer.sticky.cookie*` labels from `them-bridge` and `them-bridge-2`. Add `them-worker` to the default `up` set. Document scaling via `--scale them-worker=N`. |
| `app/main.py` | Remove `_reaper_loop()` function and its `asyncio.create_task` call. Activity `start_to_close_timeout` + `heartbeat_timeout` now enforce all deadlines. |
| `app/routers/a2a_server.py` | Replace `asyncio.create_task(_run_and_finalize(...))` with `bridge_client.start_orchestration_workflow(...)`. `_handle_cancel_task` → `workflow_handle.cancel()`. `_handle_get_task` continues reading the Postgres projection (unchanged). |
| `app/config.py` / `docker-compose.yml` env | Flip `TEMPORAL_ENABLED=true` as the default. Remove legacy branch guards once validation passes. |

### Validation (Phase 6)

- Full regression across all entry points: WS, SSE, REST, inbound A2A
- Kill a `them-bridge` replica mid-run; confirm reconnect works without sticky sessions (Workflow continues on any available worker)
- Kill `them-worker` mid-run; restart; confirm Workflow resumes
- Deliberately exceed a short `start_to_close_timeout` on a test agent; confirm Activity is marked failed and the Workflow handles it correctly (retry → final error)
- Run `python scripts/tests/run_tests.py` — zero new failures

---

## Phase 7 — Cleanup

**Goal**: Remove now-dead execution-control code from `task_runner.py` and `ws_orchestrator.py`. Keep all projection-writing code (it moved to Activities).

### Files to modify

| File | Change |
|---|---|
| `app/services/task_runner.py` | Delete: `run()`, `_invoke_agent()`, `_build_messages_from_store()`, `_load_context_history()`, `_persist_assistant_turn()`, `_persist_tool_results()`, `_run_one()`, `_publish_dash()` (now in Activities). Move remaining pure helpers to `app/temporal/loaders.py` if not already there. File may become empty or be deleted. |
| `app/services/task_store.py` | Remove `status_queue` plumbing if any leaked in. `_TRANSITIONS`, `_publish`, and all projection write functions remain. |
| `app/routers/ws_orchestrator.py` | Remove legacy `task_runner.run()` branch (the `not TEMPORAL_ENABLED` path). |
| `app/routers/apps.py` | Same — remove legacy branch. |

### Validation (Phase 7)

- `grep -r "task_runner.run\|_invoke_agent\|_reaper_loop" app/` returns no hits
- Full test suite green
- Run history, billing, and dashboard APIs return identical data to pre-migration

---

## Test Coverage Requirements

Each phase must produce or update tests before the phase is considered complete.

| Phase | Tests to add/update |
|---|---|
| 1 | Test 15 (compose health) — add `temporal-frontend` and `them-worker` to healthy container list |
| 2 | New test: Workflow execution against `a2a-echo` via direct client call; resume-after-kill assertion |
| 3 | Test 11 (WS orchestrate) — update to work with `TEMPORAL_ENABLED=true` path |
| 4 | Existing agent tests (debate, docu-writer) — run against Workflow path |
| 5 | New test: Signal-based pause/resume |
| 6 | Full suite regression; update test 20 (Traefik) — sticky session labels removed |
| 7 | Confirm dead-code grep returns clean; full suite |

---

## Dependency Order

```
Phase 1 (infra)
    └─► Phase 2 (core loop)
            └─► Phase 3 (bridge integration)
                    └─► Phase 4 (remaining agents)
                            └─► Phase 5 (human-in-the-loop)  ← can parallel with 4
                            └─► Phase 6 (cutover)             ← after 4+5
                                    └─► Phase 7 (cleanup)
```

Phases 4 and 5 can run in parallel (different files, no shared state).

---

## What Does Not Change

The following are explicitly out of scope and must not be touched during this migration:

- `app/adapters/a2a_async_adapter.py` — called from inside Activities, identical interface
- `app/services/providers/` — all LLM provider implementations
- `app/services/task_store.py` — projection writes (used by Activities)
- `app/services/run_recorder.py` — projection writes (used by Activities)
- `app/services/context_service.py` — artifact storage (used by Activities)
- `app/routers/admin_*.py` — all admin APIs
- `app/routers/runs.py` — run history API reads Postgres projection
- `auth_service/` — unchanged
- `agents/` — all downstream A2A agents unchanged
- `db/` schema files — no new migrations required for this migration (Temporal manages its own schema in the `temporal` database)

---

*Plan produced from Opus 4 architectural analysis of `/opt/docker/odin` source as of 2026-07-11. Each phase is independently executable and testable.*
