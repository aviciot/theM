"""
Temporal Activities — all non-deterministic I/O for the orchestration workflow.

Each activity maps 1-to-1 with a section of the legacy task_runner.run() loop.
Activities run in them-worker processes; they can use asyncio, DB sessions,
Redis, and httpx freely.
"""

import asyncio
import json
import pathlib
import time
import uuid
from datetime import timedelta
from decimal import Decimal
from typing import Optional

from temporalio import activity

import app.database as db_module
from app.adapters.factory import get_adapter
from app.middleware import build_agent_pipeline, MiddlewareContext
from app.services import context_service, run_recorder, task_store
from app.temporal.loaders import (
    agent_to_config,
    build_provider_from_config,
    compose_tool_description,
    ensure_agent_skills,
    load_agents,
    load_model_pricing,
    load_orchestrator_row,
    orch_to_config,
)
from app.temporal.serde import build_tools_for_agents
from app.temporal.shared import (
    RecordToolResultsInput,
    AgentConfig,
    FinalizeRunInput,
    InitRunResult,
    InvokeAgentInput,
    InvokeAgentResult,
    LoadContextResult,
    OrchestratorConfig,
    OrchestrationInput,
    PlanTurnInput,
    PlanTurnResult,
    SummarizeContextInput,
)
from app.utils.logger import logger

_DASH_RUN_PREFIX = "them:dash:run:"
_DASH_RUNS_CHANNEL = "them:dash:runs"

# ─── Phase 11c-A: Redis Streams atomic dual-publish ───────────────────────────

# Terminal event types that trigger the final 24-hour TTL on the stream key.
# This is the single authoritative definition in the Python codebase.
# Must be kept in sync with phase-11c-design.md D10 and ADR-003 D10.
TERMINAL_EVENT_TYPES: frozenset = frozenset({
    "done",
    "error",
    "canceled",
    "terminated",
    "timed_out",
})

# Stream configuration constants
_STREAM_MAXLEN = 5000          # approximate trim target (MAXLEN ~5000)
_STREAM_SAFETY_TTL = 172800    # 48 hours — set on first event; prevents orphaned keys
_STREAM_FINAL_TTL = 86400      # 24 hours — set on terminal event; retention window

# Load the Lua script once at module init (re-loaded per redis_client instance at call time)
_LUA_SCRIPT_PATH = pathlib.Path(__file__).parent / "stream_publish.lua"
_LUA_SCRIPT_SOURCE: str = _LUA_SCRIPT_PATH.read_text(encoding="utf-8")

# Cache the registered script object per redis client instance to avoid re-registering
# on every call.  Key: id(redis_client), Value: registered script callable.
_registered_scripts: dict = {}


async def stream_publish(
    redis_client,
    run_id: str,
    payload_dict: dict,
    dual_publish: bool = True,
) -> str:
    """
    Atomically publish one run event to the Redis Stream (and optionally to the
    legacy Pub/Sub channel).

    Uses the Lua script in stream_publish.lua, which in a single atomic operation:
      1. XADDs the payload to them:dash:run:{run_id}:stream with MAXLEN ~5000
      2. PUBLISHes to them:dash:run:{run_id}:tokens (if dual_publish=True)
      3. Sets safety TTL (48h) on first event, or final TTL (24h) on terminal event

    Args:
        redis_client: async redis-py client (db_module.redis_client)
        run_id: run UUID string
        payload_dict: event dict (must contain 'type' key)
        dual_publish: if True, also PUBLISH to legacy Pub/Sub channel

    Returns:
        stream entry ID string (e.g. "1721234567890-0")

    Raises:
        Exception: propagates Redis errors — never silently swallowed.
    """
    stream_key = f"{_DASH_RUN_PREFIX}{run_id}:stream"
    pubsub_channel = f"{_DASH_RUN_PREFIX}{run_id}:tokens"
    payload_json = json.dumps(payload_dict)
    event_type = payload_dict.get("type", "")
    is_terminal = event_type in TERMINAL_EVENT_TYPES

    # Register the Lua script with this client instance (cached to avoid repeat calls)
    client_id = id(redis_client)
    if client_id not in _registered_scripts:
        _registered_scripts[client_id] = redis_client.register_script(_LUA_SCRIPT_SOURCE)
    script = _registered_scripts[client_id]

    entry_id = await script(
        keys=[stream_key, pubsub_channel],
        args=[
            str(_STREAM_MAXLEN),
            str(_STREAM_SAFETY_TTL),
            str(_STREAM_FINAL_TTL),
            "1" if is_terminal else "0",
            payload_json,
            "1" if dual_publish else "0",
        ],
    )

    # entry_id may be bytes from redis-py; decode for logging
    if isinstance(entry_id, bytes):
        entry_id = entry_id.decode()

    logger.debug(
        "stream_publish: event published",
        run_id=run_id,
        event_type=event_type,
        is_terminal=is_terminal,
        entry_id=entry_id,
        dual_publish=dual_publish,
    )
    return entry_id


async def _publish_dash(run_id: str, event: dict) -> None:
    if db_module.redis_client is None:
        return
    try:
        data = json.dumps(event)
        await db_module.redis_client.publish(f"{_DASH_RUN_PREFIX}{run_id}", data)
        summary = {"run_id": run_id, **{k: v for k, v in event.items() if k != "input"}}
        await db_module.redis_client.publish(_DASH_RUNS_CHANNEL, json.dumps(summary))
    except Exception as exc:
        logger.warning("activities: dash publish failed", error=str(exc))


# ─────────────────────────────────────────────────────────────────────────────
# Activity: load_orchestration_context
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="load_orchestration_context")
async def load_orchestration_context_activity(
    orchestrator_name: str,
    user_id: int,
    token_payload: dict,
    context_id: str,
    current_task_id: str,
    history_window: int = 20,
    entry_point_slug: Optional[str] = None,
) -> dict:
    """
    Load orchestrator config, agent list, tool definitions, and prior conversation history.
    Returns a plain dict (LoadContextResult fields) — serialized for Temporal.
    """
    async with db_module.AsyncSessionLocal() as db:  # type: ignore[union-attr]
        orch = await load_orchestrator_row(orchestrator_name, db)
        if orch is None:
            raise ValueError(f"Orchestrator '{orchestrator_name}' not found or disabled")

        # Token scope check — mirrors apps.py:264.
        # token.orchestrator_id is a them.orchestrators.id FK (Phase 12 will migrate to
        # app_orchestrators.id). Until then, scoped tokens are incompatible with app
        # orchestrators (their UUIDs differ). Unscoped tokens (orchestrator_id=None) pass.
        # The proxy carries the real orch.id so this works on both ORM rows and cache-hit proxies.
        scoped_orch_id = token_payload.get("orchestrator_id")
        if scoped_orch_id and str(orch.id) != scoped_orch_id:
            raise PermissionError("Token is not authorized for this orchestrator")

        agents = await load_agents(orch, db)
        if not agents:
            raise ValueError("No agents available for this orchestrator")

        await asyncio.gather(*[ensure_agent_skills(a, db) for a in agents])

        agent_configs = [agent_to_config(a) for a in agents]
        tools = build_tools_for_agents(agent_configs)

        price_in, price_out = await load_model_pricing(
            getattr(orch, "llm_provider", "anthropic") or "anthropic",
            orch.llm_model or "unknown",
            db,
        )
        orch_config = orch_to_config(orch, price_in, price_out)

        # Resolve application_id from entry_point_slug (needed for middleware chain)
        application_id: Optional[str] = None
        if entry_point_slug:
            from sqlalchemy import select as _select
            from app.models import EntryPoint
            ep_row = (await db.execute(
                _select(EntryPoint.application_id).where(EntryPoint.slug == entry_point_slug)
            )).scalar_one_or_none()
            if ep_row is not None:
                application_id = str(ep_row)

        # Load prior conversation history once (not per-iteration)
        prior_history: list = []
        try:
            from app.services.task_store import get_task
            from app.temporal.loaders import build_provider_from_config
            provider = build_provider_from_config(
                orch_config.llm_provider, orch_config.llm_model,
                orch_config.llm_api_key_encrypted, orch_config.llm_base_url,
            )
            prior_history = await _load_context_history(
                provider, context_id, current_task_id, db,
                history_window=orch_config.history_window,
            )
            from app.temporal.serde import serialize_messages
            prior_history = serialize_messages(prior_history)
        except Exception as exc:
            logger.warning("activities: prior history load failed", error=str(exc))
            prior_history = []

    return {
        "orch": _dataclass_to_dict(orch_config),
        "agents": [_dataclass_to_dict(a) for a in agent_configs],
        "tools": tools,
        "prior_history": prior_history,
        "application_id": application_id,
    }


def _sanitize_history(messages: list) -> list:
    """
    Remove any assistant message whose tool_use IDs have no matching tool_result
    in the immediately following user message. Also drops the orphaned tool_result
    message that would follow a dropped assistant turn (to keep role alternation valid).
    Handles both mid-conversation gaps (pre-fix runs) and trailing orphans (failed runs).
    """
    if not messages:
        return messages

    # First pass: collect all tool_result IDs that exist anywhere in the history
    result_ids: set[str] = set()
    for msg in messages:
        if msg.get("role") in ("user", "tool"):
            content = msg.get("content", [])
            if isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get("type") == "tool_result":
                        result_ids.add(block.get("tool_use_id", ""))

    # Second pass: rebuild, skipping assistant messages with unmatched tool_use IDs
    # and skipping the immediately following user message if it only has tool_results
    # for the dropped assistant turn.
    sanitized: list = []
    skip_next_tool_result = False
    for msg in messages:
        role = msg.get("role")

        if skip_next_tool_result:
            skip_next_tool_result = False
            content = msg.get("content", [])
            if isinstance(content, list) and all(
                isinstance(b, dict) and b.get("type") == "tool_result" for b in content
            ):
                continue  # drop the orphaned tool_result message

        if role == "assistant":
            content = msg.get("content", [])
            if isinstance(content, list):
                tool_use_ids = [b["id"] for b in content if isinstance(b, dict) and b.get("type") == "tool_use"]
                if tool_use_ids and not all(tid in result_ids for tid in tool_use_ids):
                    skip_next_tool_result = True
                    continue  # drop this orphaned assistant message

        sanitized.append(msg)

    return sanitized


async def _load_context_history(provider, context_id: str, current_task_id: str, db, history_window: int = 20) -> list:
    """Port of task_runner._load_context_history."""
    from sqlalchemy import select as _select
    from app.models import Task, TaskMessage

    q = (
        _select(Task)
        .where(
            Task.context_id == uuid.UUID(context_id),
            Task.id != uuid.UUID(current_task_id),
            Task.kind == "root",
        )
        .order_by(Task.created_at)
    )
    prior_tasks_result = await db.execute(q)
    prior_tasks = list(prior_tasks_result.scalars().all())

    if history_window >= 0 and len(prior_tasks) > history_window:
        prior_tasks = prior_tasks[-history_window:]
    if not prior_tasks:
        return []

    all_rows = []
    for pt in prior_tasks:
        msgs_result = await db.execute(
            _select(TaskMessage)
            .where(TaskMessage.task_id == pt.id)
            .order_by(TaskMessage.seq)
        )
        rows = list(msgs_result.scalars().all())
        if rows:
            all_rows.extend(rows)
        else:
            input_text = ""
            for part in (pt.input_message or {}).get("parts", []):
                if "text" in part:
                    input_text = part["text"]
                    break
            if input_text:
                class _SyntheticRow:
                    role = "user"
                    parts: dict
                r = _SyntheticRow()
                r.parts = {"content": input_text}
                all_rows.append(r)  # type: ignore

    if not all_rows:
        return []
    history = provider.deserialize_history(all_rows)
    return _sanitize_history(history)


# ─────────────────────────────────────────────────────────────────────────────
# Activity: init_run
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="init_run")
async def init_run_activity(
    orchestrator_name: str,
    orchestrator_id: str,
    user_message: str,
    user_id: int,
    session_id: str,
    context_id: str,
    run_id: str,
    root_task_id: str,
    budget_tokens: Optional[int],
    parent_run_id: Optional[str] = None,
    entry_point_slug: Optional[str] = None,
) -> dict:
    """
    Create them.runs + them.tasks rows and emit run_start dashboard event.
    run_id and root_task_id are pre-generated by the Workflow (via workflow.uuid4())
    so retries are idempotent via INSERT ... ON CONFLICT DO NOTHING (or duplicate key handling).
    Returns {run_id, root_task_id} for confirmation.
    """
    run_id_uuid = uuid.UUID(run_id)
    root_task_id_uuid = uuid.UUID(root_task_id)
    orch_id_uuid = uuid.UUID(orchestrator_id)
    context_id_uuid = uuid.UUID(context_id)
    session_id_uuid = uuid.UUID(session_id)

    parent_run_uuid = uuid.UUID(parent_run_id) if parent_run_id else None

    async with db_module.AsyncSessionLocal() as db:
        actual_run_id = await run_recorder.start_run(
            db,
            orchestrator_id=orch_id_uuid,
            orchestrator_name=orchestrator_name,
            user_id=user_id,
            session_id=session_id_uuid,
            goal=user_message,
            run_id=run_id_uuid,
            parent_run_id=parent_run_uuid,
            entry_point_slug=entry_point_slug,
        )

        root_task = await task_store.create_task(
            db,
            context_id=context_id_uuid,
            input_message={"parts": [{"kind": "text", "text": user_message}]},
            kind="root",
            run_id=actual_run_id,
            orchestrator_id=orch_id_uuid,
            budget_tokens=budget_tokens,
        )
        await task_store.transition(db, root_task.id, "working")
        await task_store.record_message(
            db,
            task_id=root_task.id,
            role="user",
            parts={"content": user_message},
            seq=0,
        )

    actual_run_id_str = str(actual_run_id)
    root_task_id_str = str(root_task.id)

    # Publish run_start to the canonical run channel.
    # The Go gateway subscribes to them:dash:run:{run_id}:tokens before calling
    # ExecuteWorkflow, so this event is guaranteed not to be missed.
    await _publish_dash(actual_run_id_str, {
        "type": "run_start",
        "run_id": actual_run_id_str,
        "orchestrator": orchestrator_name,
        "goal": user_message,
        "task_id": root_task_id_str,
        "context_id": context_id,
    })

    # Publish ready event to context channel so bridge_client.stream_run_events()
    # can extract run_id and subscribe to the run-specific token channel.
    if db_module.redis_client is not None:
        try:
            ctx_channel = f"{_DASH_RUN_PREFIX}{context_id}:ctx"
            ready_event = json.dumps({
                "type": "ready",
                "run_id": actual_run_id_str,
                "task_id": root_task_id_str,
                "context_id": context_id,
            })
            await db_module.redis_client.publish(ctx_channel, ready_event)
        except Exception as exc:
            logger.warning("init_run: context channel ready publish failed", error=str(exc))

    logger.info("init_run: run created", run_id=actual_run_id_str, root_task_id=root_task_id_str)
    return {"run_id": actual_run_id_str, "root_task_id": root_task_id_str}


# ─────────────────────────────────────────────────────────────────────────────
# Activity: plan_turn
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="plan_turn")
async def plan_turn_activity(inp: PlanTurnInput) -> PlanTurnResult:
    """
    One LLM planning turn: calls stream_call, publishes tokens to Redis,
    records usage and assistant turn to Postgres.
    """
    provider = build_provider_from_config(
        inp.provider_name, inp.model, inp.api_key_encrypted, inp.base_url
    )

    from app.temporal.serde import deserialize_messages, serialize_messages
    messages = deserialize_messages(inp.messages)

    tool_calls_raw = []
    raw_response = None
    final_answer = None
    input_tokens = 0
    output_tokens = 0
    text_buffer = ""

    from app.services.providers.base import TokenUsage
    iter_usage = TokenUsage()

    async for event in provider.stream_call(
        system=inp.system_prompt,
        messages=messages,
        tools=[{"name": t["name"], "description": t["description"], "schema": t["schema"]} for t in inp.tools],
        max_tokens=inp.max_tokens,
    ):
        if event.type == "token":
            text = event.text or ""
            text_buffer += text
            # Publish token to Redis Stream (+ legacy Pub/Sub in dual mode)
            if db_module.redis_client is not None:
                try:
                    await stream_publish(
                        db_module.redis_client,
                        inp.run_id,
                        {"type": "token", "text": text},
                    )
                except Exception as _exc:
                    logger.warning("stream_publish: token publish failed", error=str(_exc))
            # Heartbeat so Temporal knows the activity is alive
            activity.heartbeat({"phase": "token", "len": len(text_buffer)})

        elif event.type == "tool_calls_ready":
            result_data = event.result or {}
            tool_calls_raw = [
                {"id": tc.id, "name": tc.name, "input": tc.input}
                for tc in result_data.get("tool_calls", [])
            ]
            raw_response = result_data.get("raw_response")
            iter_usage = result_data.get("usage", TokenUsage())

        elif event.type == "done":
            result_data = event.result or {}
            final_answer = result_data.get("answer", text_buffer)
            raw_response = result_data.get("raw_response")
            iter_usage = result_data.get("usage", TokenUsage()) if event.usage is None else event.usage

        elif event.type == "error":
            raise RuntimeError(f"LLM error: {event.error}")

    input_tokens = iter_usage.input_tokens
    output_tokens = iter_usage.output_tokens
    iter_cost = (
        Decimal(input_tokens)  * Decimal(inp.price_in) +
        Decimal(output_tokens) * Decimal(inp.price_out)
    )

    # Persist usage + assistant turn
    msg_seq_after = inp.msg_seq
    serialized_turn = None

    async with db_module.AsyncSessionLocal() as db:
        from app.services.providers.base import TokenUsage as TU
        usage_obj = TU()
        usage_obj.input_tokens = input_tokens
        usage_obj.output_tokens = output_tokens

        await run_recorder.record_usage(
            db,
            run_id=uuid.UUID(inp.run_id),
            user_id=inp.user_id,
            provider=inp.llm_provider,
            model=inp.model,
            usage=usage_obj,
            cost_usd=iter_cost,
        )
        if input_tokens + output_tokens > 0:
            await task_store.add_tokens_used(
                db, uuid.UUID(inp.root_task_id),
                input_tokens + output_tokens,
            )

        if raw_response is not None:
            try:
                content = provider.serialize_turn(raw_response)
                await task_store.record_message(
                    db,
                    task_id=uuid.UUID(inp.root_task_id),
                    role="agent",
                    parts={"content": content},
                    seq=inp.msg_seq,
                )
                # JSON-encode as string so Temporal's converter handles it cleanly
                serialized_turn = json.dumps(content)
                msg_seq_after = inp.msg_seq + 1
            except Exception as exc:
                logger.warning("plan_turn: persist assistant turn failed", error=str(exc))

    await _publish_dash(inp.run_id, {
        "type": "usage",
        "iteration": inp.iteration,
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "cost_usd": float(iter_cost),
    })

    # Publish final_answer or tool_calls signal to the bridge stream
    if final_answer is not None:
        if db_module.redis_client is not None:
            try:
                await stream_publish(
                    db_module.redis_client,
                    inp.run_id,
                    {"type": "plan_done", "final_answer": final_answer},
                )
            except Exception as _exc:
                logger.warning("stream_publish: plan_done publish failed", error=str(_exc))
    elif tool_calls_raw:
        # Publish iteration_start with agent list to both channels so the UI can show
        # "Iteration N — calling agent_foo, agent_bar" before any agent responds
        agents_called = [tc["name"].removeprefix("agent__") for tc in tool_calls_raw]
        iteration_start_event = {
            "type": "iteration_start",
            "iteration": inp.iteration,
            "agents": agents_called,
        }
        await _publish_dash(inp.run_id, iteration_start_event)
        if db_module.redis_client is not None:
            try:
                await stream_publish(
                    db_module.redis_client,
                    inp.run_id,
                    iteration_start_event,
                )
            except Exception as _exc:
                logger.warning("stream_publish: iteration_start publish failed", error=str(_exc))

        if db_module.redis_client is not None:
            try:
                await stream_publish(
                    db_module.redis_client,
                    inp.run_id,
                    {"type": "tool_calls_ready", "tool_calls": tool_calls_raw},
                )
            except Exception as _exc:
                logger.warning("stream_publish: tool_calls_ready publish failed", error=str(_exc))

    return PlanTurnResult(
        tool_calls=tool_calls_raw,
        final_answer=final_answer,
        serialized_assistant_turn=serialized_turn,
        input_tokens=input_tokens,
        output_tokens=output_tokens,
        msg_seq_after=msg_seq_after,
    )


# ─────────────────────────────────────────────────────────────────────────────
# Activity: invoke_agent
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="invoke_agent")
async def invoke_agent_activity(inp: InvokeAgentInput) -> InvokeAgentResult:
    """
    Call a downstream agent via its adapter and persist the result.
    Heartbeats on every status event so Temporal can detect stuck agents.
    """
    t0 = time.monotonic()

    # Reconstruct a minimal agent proxy for get_adapter
    class _AgentProxy:
        def __init__(self, cfg: InvokeAgentInput):
            self.id = uuid.UUID(cfg.agent_id)
            self.slug = cfg.agent_slug
            self.name = cfg.agent_name
            self.transport = cfg.transport
            self.endpoint_url = cfg.endpoint_url
            self.auth_token_encrypted = cfg.auth_token_encrypted
            self.timeout_seconds = cfg.timeout_seconds
            self.max_concurrency = 1
            self.input_schema = cfg.input_schema

    agent_proxy = _AgentProxy(inp)

    result_text = ""
    file_parts: list[dict] = []
    status = "completed"
    error_msg = None

    async with db_module.AsyncSessionLocal() as own_db:
        child_task = await task_store.create_task(
            own_db,
            context_id=uuid.UUID(inp.context_id),
            input_message={"parts": [{"kind": "text", "text": inp.tool_input.get("message", "")}]},
            kind="delegated",
            run_id=uuid.UUID(inp.run_id),
            parent_task_id=uuid.UUID(inp.root_task_id),
            agent_id=uuid.UUID(inp.agent_id),
        )
        await task_store.transition(own_db, child_task.id, "working")

        step_id = await run_recorder.record_step(
            own_db,
            run_id=uuid.UUID(inp.run_id),
            iteration=inp.iteration,
            agent_id=uuid.UUID(inp.agent_id),
            agent_slug=inp.agent_slug,
            tool_call_id=inp.tool_call_id,
            input=inp.tool_input,
        )

        try:
            from app.temporal.serde import build_agent_tool_input
            effective_input = build_agent_tool_input(
                inp.tool_input, inp.input_schema, inp.injected_context
            )
            # Publish tool_start to both channels:
            # :stream — durable stream that bridge forwards to WS client (Phase 11c+)
            # plain run channel — dashboard WS trace tab
            tool_start_event = {
                "type": "tool_start",
                "tool": inp.tool_call_name,
                "input": inp.tool_input,
            }
            await _publish_dash(inp.run_id, tool_start_event)
            if db_module.redis_client is not None:
                try:
                    await stream_publish(
                        db_module.redis_client,
                        inp.run_id,
                        tool_start_event,
                    )
                except Exception as _exc:
                    logger.warning("stream_publish: tool_start publish failed", error=str(_exc))
            mw_ctx = MiddlewareContext(
                run_id=inp.run_id,
                context_id=inp.context_id,
                agent_id=inp.agent_id,
                agent_slug=inp.agent_slug,
                user_id=inp.user_id_str,
                session_id=inp.session_id_str,
                application_id=inp.application_id,
                tool_call_id=inp.tool_call_id,
                timeout=float(inp.timeout_seconds),
                redis=db_module.redis_client,
                db_session_factory=db_module.AsyncSessionLocal,
            )
            adapter = await build_agent_pipeline(
                agent_proxy,
                db=own_db,
                redis=db_module.redis_client,
                ctx=mw_ctx,
                context_id=inp.context_id,
            )
            if inp.session_id_str and inp.application_id:
                from app.services.session_manager import set_active_agent, clear_active_agent
                _sess_uuid = uuid.UUID(inp.session_id_str)
                await set_active_agent(_sess_uuid, inp.agent_slug, inp.application_id)
            else:
                _sess_uuid = None
            try:
                async for event in adapter.stream_invoke(
                    input=effective_input,
                    timeout=float(inp.timeout_seconds),
                ):
                    if event.type == "task_created":
                        # Heartbeat immediately after submit — the POST can take many seconds
                        activity.heartbeat({"phase": "submitted", "agent": inp.agent_slug})

                    elif event.type == "token":
                        result_text += event.text or ""

                    elif event.type == "status" and event.state:
                        # Heartbeat with remote task state for Temporal Event History
                        activity.heartbeat({"state": event.state, "agent": inp.agent_slug})
                        elapsed = int((time.monotonic() - t0) * 1000)
                        if event.input_required:
                            # Surface input-required to the workflow so it can pause
                            status = "input-required"
                            if db_module.redis_client is not None:
                                try:
                                    await stream_publish(
                                        db_module.redis_client,
                                        inp.run_id,
                                        {
                                            "type": "input_required",
                                            "agent": inp.agent_slug,
                                            "tool_call_id": inp.tool_call_id,
                                            "elapsed_ms": elapsed,
                                        },
                                    )
                                except Exception as _exc:
                                    logger.warning("stream_publish: input_required publish failed", error=str(_exc))
                            break
                        # Publish regular status to bridge stream
                        if db_module.redis_client is not None:
                            try:
                                await stream_publish(
                                    db_module.redis_client,
                                    inp.run_id,
                                    {
                                        "type": "agent_status",
                                        "agent": inp.agent_slug,
                                        "state": event.state,
                                        "elapsed_ms": elapsed,
                                    },
                                )
                            except Exception as _exc:
                                logger.warning("stream_publish: agent_status publish failed", error=str(_exc))

                    elif event.type == "artifact":
                        for p in (event.artifact or {}).get("parts", []):
                            media_type = p.get("media_type") or p.get("mediaType")
                            filename = p.get("filename")
                            is_json_data = media_type == "application/json" and not filename
                            if is_json_data:
                                result_text += p.get("text", "")
                            elif filename or media_type:
                                normalized = {**p}
                                if media_type:
                                    normalized["media_type"] = media_type
                                file_parts.append(normalized)
                            elif p.get("text") and not file_parts:
                                result_text += p.get("text", "")

                    elif event.type == "done":
                        result_text = event.result or result_text
                        break

                    elif event.type == "error":
                        error_msg = event.error
                        status = "failed"
                        break

            finally:
                if _sess_uuid is not None:
                    await clear_active_agent(_sess_uuid, inp.application_id, inp.agent_slug)

        except asyncio.CancelledError:
            error_msg = "activity cancelled"
            status = "failed"
            raise
        except Exception as exc:
            error_msg = str(exc)
            status = "failed"
            logger.error("invoke_agent: error", agent=inp.agent_slug, error=str(exc))

        latency_ms = int((time.monotonic() - t0) * 1000)

        child_state = "completed" if status == "completed" else "failed"
        if child_state == "completed" and (result_text or file_parts):
            parts = file_parts if file_parts else [{"kind": "text", "text": result_text}]
            await context_service.record_and_cache_artifact(
                task_id=child_task.id,
                context_id=uuid.UUID(inp.context_id),
                artifact_id=f"{inp.agent_slug}-{inp.tool_call_id}",
                parts=parts,
                name=f"{inp.agent_slug} result",
                db=own_db,
            )
        await task_store.transition(own_db, child_task.id, child_state, error=error_msg)

        await run_recorder.complete_step(
            own_db,
            step_id=step_id,
            output=result_text if status == "completed" else None,
            status=status,
            error=error_msg,
            latency_ms=latency_ms,
        )

    # Publish tool_done to both channels (trace tab + streaming side-channel)
    tool_done_event = {
        "type": "tool_done",
        "tool": inp.tool_call_name,
        "latency_ms": latency_ms,
    }
    await _publish_dash(inp.run_id, tool_done_event)
    if db_module.redis_client is not None:
        try:
            await stream_publish(
                db_module.redis_client,
                inp.run_id,
                tool_done_event,
            )
            for part in file_parts:
                if part.get("filename") and part.get("media_type"):
                    await stream_publish(
                        db_module.redis_client,
                        inp.run_id,
                        {
                            "type": "file",
                            "filename": part["filename"],
                            "media_type": part["media_type"],
                            "text": part.get("text", ""),
                        },
                    )
        except Exception as _exc:
            logger.warning("stream_publish: tool_done publish failed", error=str(_exc))

    if status == "input-required":
        return InvokeAgentResult(
            status="input-required",
            result_text="",
            file_parts=[],
            latency_ms=latency_ms,
        )
    if status == "failed":
        return InvokeAgentResult(
            status="failed",
            result_text=f"[Agent {inp.agent_slug} error: {error_msg}]",
            file_parts=[],
            latency_ms=latency_ms,
            error=error_msg,
        )
    return InvokeAgentResult(
        status="completed",
        result_text=result_text,
        file_parts=file_parts,
        latency_ms=latency_ms,
    )


# ─────────────────────────────────────────────────────────────────────────────
# Activity: summarize_context
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="summarize_context")
async def summarize_context_activity(inp: SummarizeContextInput) -> Optional[str]:
    """Summarize the conversation context for memory injection. Returns summary text or None."""
    if not inp.memory_enabled:
        return None
    try:
        async with db_module.AsyncSessionLocal() as db:
            from app.services import context_service as _cs
            from app.services.memory_service import summarize_context

            # Build a minimal orch proxy for summarize_context
            class _OrcProxy:
                memory_enabled = inp.memory_enabled
                summarize_every_n_calls = inp.summarize_every_n_calls
                memory_raw_fallback_n = inp.memory_raw_fallback_n
                summarizer_provider = inp.summarizer_provider
                summarizer_model = inp.summarizer_model
                summarizer_api_key_encrypted = inp.summarizer_api_key_encrypted
                llm_provider = inp.llm_provider
                llm_model = inp.llm_model
                llm_api_key_encrypted = inp.llm_api_key_encrypted

            artifacts = await _cs.get_context_artifacts(
                uuid.UUID(inp.context_id), db, limit=20
            )
            await summarize_context(
                context_id=uuid.UUID(inp.context_id),
                orch=_OrcProxy(),
                artifacts=artifacts,
                root_task_id=uuid.UUID(inp.root_task_id),
                db=db,
            )
            # The summary is stored in Redis by memory_service; also return it
            from app.services.memory_service import get_injected_context
            return await get_injected_context(uuid.UUID(inp.context_id))
    except Exception as exc:
        logger.warning("summarize_context: failed", error=str(exc))
        return None


# ─────────────────────────────────────────────────────────────────────────────
# Activity: record_tool_results
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="record_tool_results")
async def record_tool_results_activity(inp: RecordToolResultsInput) -> None:
    """Persist the tool_result (user-role) message so history loads correctly."""
    async with db_module.AsyncSessionLocal() as db:
        try:
            content = [
                {"type": "tool_result", "tool_use_id": r["tool_use_id"], "content": r["content"]}
                for r in inp.tool_results
            ]
            await task_store.record_message(
                db,
                task_id=uuid.UUID(inp.root_task_id),
                role="user",
                parts={"content": content},
                seq=inp.msg_seq,
            )
        except Exception as exc:
            logger.warning("record_tool_results: persist failed", error=str(exc))


# ─────────────────────────────────────────────────────────────────────────────
# Activity: finalize_run
# ─────────────────────────────────────────────────────────────────────────────

@activity.defn(name="finalize_run")
async def finalize_run_activity(inp: FinalizeRunInput) -> None:
    """
    Complete the run: update them.runs, write final artifact, transition root task.
    Runs inside a shielded cancellation scope so it executes even on Workflow cancel.
    """
    run_id = uuid.UUID(inp.run_id)
    root_task_id = uuid.UUID(inp.root_task_id)
    context_id = uuid.UUID(inp.context_id)

    async with db_module.AsyncSessionLocal() as db:
        await run_recorder.complete_run(
            db,
            run_id=run_id,
            status=inp.status,
            final_output=inp.final_answer or None,
            error=inp.error,
            iterations=inp.iterations,
            total_tokens_in=inp.total_tokens_in,
            total_tokens_out=inp.total_tokens_out,
            total_cost_usd=Decimal(inp.total_cost_usd),
        )

        if inp.final_answer:
            await context_service.record_and_cache_artifact(
                task_id=root_task_id,
                context_id=context_id,
                artifact_id="final-answer",
                parts=[{"kind": "text", "text": inp.final_answer}],
                name="Final Answer",
                db=db,
            )

        final_task_state = "completed" if inp.status == "completed" else "failed"
        await task_store.transition(db, root_task_id, final_task_state, error=inp.error)

    await _publish_dash(inp.run_id, {
        "type": "run_end",
        "status": inp.status,
        "iterations": inp.iterations,
        "total_tokens_in": inp.total_tokens_in,
        "total_tokens_out": inp.total_tokens_out,
        "task_id": inp.root_task_id,
        "error": inp.error,
    })

    # Publish terminal event to bridge stream so it stops listening.
    # stream_publish applies the final 24h TTL atomically via Lua when is_terminal=True.
    if db_module.redis_client is not None:
        try:
            terminal_event = (
                {"type": "done", "run_id": inp.run_id, "task_id": inp.root_task_id, "iterations": inp.iterations}
                if inp.status == "completed"
                else {"type": "error", "message": inp.error or "Run failed"}
            )
            await stream_publish(
                db_module.redis_client,
                inp.run_id,
                terminal_event,
            )
        except Exception as _exc:
            logger.warning("stream_publish: terminal event publish failed", error=str(_exc))

    logger.info("finalize_run: done", run_id=inp.run_id, status=inp.status)


# ─────────────────────────────────────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────────────────────────────────────

def _dataclass_to_dict(obj) -> dict:
    """Convert a dataclass to a plain dict (for JSON serialization)."""
    import dataclasses
    return dataclasses.asdict(obj)


# Registry — imported by worker.py
ALL_ACTIVITIES = [
    load_orchestration_context_activity,
    init_run_activity,
    plan_turn_activity,
    invoke_agent_activity,
    record_tool_results_activity,
    summarize_context_activity,
    finalize_run_activity,
]
