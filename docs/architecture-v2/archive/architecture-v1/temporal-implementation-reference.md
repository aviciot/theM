# the-M — Temporal Implementation Reference

**Purpose:** Developer reference for implementing the Temporal migration. Companion to `temporal-migration-plan.md`.  
**Date:** 2026-07-11

---

## 1. Code Migration Map

This table shows exactly where each piece of current code goes in the Temporal model.

| Current location | Current role | Moves to |
|---|---|---|
| `task_runner.run()` | Orchestration loop | `app/temporal/workflows.py::OrchestrationWorkflow.run()` |
| `task_runner._invoke_agent()` | Agent invocation | `app/temporal/activities.py::invoke_agent_activity()` |
| `task_runner._build_messages_from_store()` | LLM context rebuild from DB | Eliminated — `self.messages` in Workflow state |
| `task_runner._load_context_history()` | Multi-turn history load | `load_orchestration_context_activity()` (once per run, not per iteration) |
| `task_runner._persist_assistant_turn()` | Assistant turn persistence | Inside `plan_turn_activity()` |
| `task_runner._persist_tool_results()` | Tool result persistence | Inside `invoke_agent_activity()` (after all results) |
| `task_runner._load_orchestrator_row()` | Orchestrator config load | `app/temporal/loaders.py` + called from `load_orchestration_context_activity()` |
| `task_runner._load_agents()` | Agent list load | `app/temporal/loaders.py` + called from `load_orchestration_context_activity()` |
| `task_runner._ensure_agent_skills()` | Agent card fetch | `app/temporal/loaders.py` + called from `load_orchestration_context_activity()` |
| `task_runner._build_provider()` | LLM provider construction | `app/temporal/loaders.py` + called from `plan_turn_activity()` |
| `task_runner._OrchestratorProxy` | Typed cache proxy | `app/temporal/loaders.py` |
| `task_runner._compose_tool_description()` | Tool description build | `app/temporal/loaders.py` |
| `task_runner._build_agent_tool_schema()` | Tool schema build | `app/temporal/loaders.py` |
| `task_runner._publish_dash()` | Dashboard event publish | Inside each Activity that currently calls it |
| `task_runner.asyncio.gather()` | Parallel agent fan-out | `asyncio.gather(*[workflow.execute_activity(...) for tc in tool_calls])` |
| `task_runner._run_one()` typed/text input split | Context injection | `app/temporal/serde.py::build_agent_tool_input()` |
| `app/main.py::_reaper_loop()` | Task deadline enforcement | Removed — replaced by Activity `start_to_close_timeout` + `heartbeat_timeout` |
| `app/routers/ws_orchestrator.py` generator loop | WS event relay | `app/temporal/bridge_client.py::stream_run_events()` + Redis pub/sub subscription |
| `app/routers/a2a_server.py::asyncio.create_task(_run_and_finalize)` | Detached A2A inbound run | `bridge_client.start_orchestration_workflow()` |
| Traefik sticky sessions | Keep WS on same replica | Removed — Workflow state lives in Temporal, not in-process |
| `memory_service.summarize_context()` | Context summarization | `summarize_context_activity()` — result stored in `self.context_summary` (Workflow state) |
| `memory_service.get_injected_context()` (Redis read) | Context injection | Reads `self.context_summary` from Workflow state (no Redis TTL loss) |

---

## 2. New File Structure

```
app/temporal/
├── __init__.py
├── config.py           # TemporalConfig, get_temporal_config()
├── client.py           # get_temporal_client() singleton
├── worker.py           # Worker entrypoint: main()
├── shared.py           # Serializable dataclasses for Workflow/Activity boundary
├── workflows.py        # OrchestrationWorkflow
├── activities.py       # All Activity definitions
├── loaders.py          # Pure helpers moved from task_runner.py
├── serde.py            # ToolCall↔dict, NeutralTool build, message history, input construction
└── bridge_client.py    # start_orchestration_workflow(), stream_run_events()
```

---

## 3. Activity Specifications

### 3.1 `load_orchestration_context_activity`

**Timeout**: `start_to_close_timeout=timedelta(seconds=30)`  
**Retry**: `maximum_attempts=3`  
**Returns**: plain dict (no ORM objects)

```python
{
  "orchestrator": {
    "id": str, "name": str, "display_name": str,
    "system_prompt": str, "max_iterations": int,
    "max_parallel_tools": int, "budget_tokens": int | None,
    "memory_enabled": bool, "summarize_every_n_calls": int,
    "history_window": int,
    "llm_provider": str, "llm_model": str,
    "llm_api_key_encrypted": str | None, "llm_base_url": str | None,
    "summarizer_provider": str | None, "summarizer_model": str | None,
    "summarizer_api_key_encrypted": str | None,
  },
  "agents": [
    {
      "id": str, "slug": str, "endpoint_url": str,
      "auth_token_encrypted": str | None, "input_schema": dict,
      "skills": list, "supports_streaming": bool,
      "max_concurrency": int, "timeout_seconds": int,
      "description": str,
    }, ...
  ],
  "tools": [
    {"name": str, "description": str, "schema": dict}, ...
  ],
  "price_in": str,   # Decimal as string — e.g. "0.000003"
  "price_out": str,
  "prior_history": list[dict],  # Provider-native serialized prior turns
}
```

### 3.2 `init_run_activity`

**Timeout**: `start_to_close_timeout=timedelta(seconds=30)`  
**Retry**: `maximum_attempts=3`, idempotent via pre-generated IDs

Receives `run_id` and `root_task_id` generated by `workflow.uuid4()`. Uses `INSERT ... ON CONFLICT DO NOTHING` pattern (or equivalent) so retries are safe.

### 3.3 `plan_turn_activity`

**Timeout**: `start_to_close_timeout=timedelta(minutes=5)`  
**Retry**: `maximum_attempts=3`

Token side-channel: publishes to `them:dash:run:{run_id}:tokens`.

On retry, tokens are re-published. The bridge treats the token stream as best-effort. The bridge always derives the authoritative final text from `PlanTurnResult.final_answer` (the Activity return value), not from the Redis stream.

`msg_seq` is passed in from the Workflow (Workflow tracks `self.msg_seq` counter, increments after each persist call) so retries write to the same `seq` position (upsert semantics or skip-if-exists on `them.task_messages`).

### 3.4 `invoke_agent_activity`

**Timeout**: `start_to_close_timeout=timedelta(seconds=agent.timeout_seconds)`  
**Heartbeat timeout**: `heartbeat_timeout=timedelta(seconds=30)`  
**Retry**: `maximum_attempts=2, non_retryable_error_types=[]`

Heartbeat payload (emitted on each `status` or `remote_task_id` event from the adapter):

```python
activity.heartbeat({"state": event.state, "remote_task_id": remote_task_id})
```

This makes the `remote_task_id` available in Temporal Event History. On retry, the activity can inspect `activity.info().heartbeat_details` to potentially re-attach to the existing remote task (future optimization — not required for Phase 2).

Per-agent `max_concurrency` enforcement: configure `Worker(max_concurrent_activities=N)` or use a per-agent semaphore inside the activity (importing the existing `asyncio.Semaphore` pattern) if strict per-agent limits are needed. Document the chosen approach when implementing.

### 3.5 `summarize_context_activity`

**Timeout**: `start_to_close_timeout=timedelta(seconds=60)`  
**Retry**: `maximum_attempts=2`

Returns `str | None`. The Workflow stores the result in `self.context_summary`. No Redis write — context summary durability comes from Workflow state (Temporal Event History), not Redis TTL.

### 3.6 `finalize_run_activity`

**Timeout**: `start_to_close_timeout=timedelta(seconds=30)`  
**Retry**: `maximum_attempts=5` (must succeed — projection must finalize)  
Run in a `workflow.CancellationScope(shield=True)` block so it executes even when the Workflow is being canceled.

---

## 4. Workflow State

All fields in `OrchestrationWorkflow` must be JSON-serializable. Temporal checkpoints this state in Event History.

```python
self.messages: list[dict]        # Full LLM conversation history (provider-native serialized)
self.context_summary: str | None # Latest context memory summary (replaces Redis key)
self.human_response: dict | None # Set by submit_human_response Signal (Phase 5)
self.iteration: int              # Current loop iteration
self.tokens_used: int            # Running token count (for budget enforcement)
self.msg_seq: int                # Next seq number for task_messages inserts
self.agent_calls_since_summary: int  # Counter for memory threshold
```

### What is NOT in Workflow state (lives in Postgres projection only)

- `run_id`, `root_task_id` (generated once via `workflow.uuid4()`, passed to Activities as inputs)
- Artifact content (in `them.artifacts`)
- Run step details (in `them.run_steps`)
- Per-iteration token usage (in `them.run_usage`)

---

## 5. Determinism Rules for Workflow Code

Temporal replays Workflow code from Event History on worker restart. Any non-deterministic operation in Workflow code will cause replay divergence (a fatal error).

**Forbidden in Workflow code** (must go in Activities):
- Any DB or Redis I/O
- Any HTTP call
- `datetime.now()` — use `workflow.now()` instead
- `uuid.uuid4()` — use `workflow.uuid4()` instead
- `random.*`
- `asyncio.sleep()` — use `await workflow.sleep()` instead
- Importing modules with side effects

**Allowed in Workflow code**:
- `await workflow.execute_activity(...)`
- `await asyncio.gather(*[workflow.execute_activity(...) for ...])` — this is the parallel fan-out pattern
- `await workflow.wait_condition(...)` — for Signal-based pause (Phase 5)
- Pure Python logic, list operations, arithmetic

---

## 6. Redis Channel Map (new channels added)

| Channel | Publisher | Subscriber | Content |
|---|---|---|---|
| `them:dash:run:{run_id}:tokens` | `plan_turn_activity` (token events) + `invoke_agent_activity` (status/file/tool_done events) | `bridge_client.stream_run_events()` in `them-bridge` | All real-time events previously yielded by `task_runner.run()` generator |

Existing channels (`them:dash:run:{run_id}`, `them:dash:runs`, `them:tasks:{id}:events`) remain unchanged — written by Activities via the same `_publish_dash` and `task_store._publish` calls as today.

---

## 7. Environment Variables (new)

Add to `app/config.py::Settings` and to the `them-bridge` + `them-worker` env blocks in `docker-compose.yml`:

| Variable | Default | Description |
|---|---|---|
| `TEMPORAL_ENABLED` | `false` | Feature flag; `true` routes runs through Workflows |
| `TEMPORAL_HOST` | `temporal-frontend:7233` | Temporal frontend gRPC address |
| `TEMPORAL_NAMESPACE` | `default` | Temporal namespace |
| `TEMPORAL_TASK_QUEUE` | `them-orchestration` | Task queue name |

---

## 8. Temporal Container Versions (pinned)

| Image | Version | Purpose |
|---|---|---|
| `temporalio/auto-setup` | `1.25.2` | Temporal server (auto-initializes Postgres schema) |
| `temporalio/ui` | `2.31.2` | Temporal Web UI |
| `temporalio/admin-tools` | `1.25.2` | CLI tools (`temporal workflow`, `tctl`) |
| `temporalio` (Python SDK) | `1.9.0` | Python SDK for worker + client |

---

## 9. Pain Points Resolved (traceability)

| Pain Point (As-Is §16) | Resolution |
|---|---|
| #1 No retry in `a2a_async_adapter.py` | `invoke_agent_activity` `RetryPolicy(maximum_attempts=2)` |
| #2 Cancellation does not propagate | `workflow_handle.cancel()` from bridge; `CancellationScope` in Workflow; Activity cancellation |
| #3 No resume after restart | Temporal Workflow replay from Event History |
| #4 In-flight tasks after WS disconnect | Workflow lifecycle decoupled from client connection |
| #5 `input-required` is dead | `workflow.wait_condition()` + `submit_human_response` Signal (Phase 5) |
| #6 Remote task ID not persisted | `activity.heartbeat({"remote_task_id": ...})` in `invoke_agent_activity` |
| #7 Context memory Redis-primary | `self.context_summary` in Workflow state — no TTL |

---

## 10. What Stays Identical (no changes required)

- `app/adapters/a2a_async_adapter.py` — Activities call `stream_invoke()` the same way
- `app/services/task_store.py` — all projection writers used by Activities
- `app/services/run_recorder.py` — all projection writers used by Activities
- `app/services/context_service.py` — `record_and_cache_artifact` used in Activities
- `app/services/providers/` — all provider implementations
- `app/services/agent_registry.py` — used in `load_orchestration_context_activity`
- `app/services/token_cache.py` — unchanged (bridge-level auth)
- `app/services/rate_limiter.py` — unchanged
- `app/services/auth_client.py` — unchanged
- `app/routers/admin_*.py` — all admin APIs unchanged
- `app/routers/runs.py` — reads from Postgres projection (unchanged)
- `auth_service/` — unchanged
- `agents/` — all downstream agents unchanged

---

*Reference document for the-M Temporal migration. All file paths and function names verified against `/opt/docker/odin` source as of 2026-07-11.*
