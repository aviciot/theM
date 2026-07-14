"""
task_runner — durable A2A-native orchestration engine.

Key properties:
- LLM context is rebuilt from them.task_messages + them.artifacts on every
  planning turn. The run survives WS disconnects.
- Each run creates a root them.tasks row. Agent invocations create child rows.
- The WS endpoint is a subscriber to Redis them:tasks:{id}:events; it does
  not own the loop's lifetime.
- Budget envelope enforced: tokens_used checked before each planning turn.

Entry point: run(root_task_id, publish_fn, db, redis)
  publish_fn(event: dict) → coroutine  — caller supplies the WS relay
"""

import asyncio
import json
import time
import uuid
from dataclasses import dataclass, field
from decimal import Decimal
from typing import AsyncGenerator, Callable, Optional

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.adapters.factory import get_adapter
from app.models import Agent, LLMProvider, Orchestrator, Task
from app.services import context_service, run_recorder, task_store
from app.services.providers import create_provider
from app.services.providers.base import (
    LLMProvider, LLMStreamEvent, NeutralTool, ToolCall, TokenUsage,
)
from app.utils.logger import logger

_ORCH_PREFIX = "them:orchestrators:"
_ORCH_TTL = 600
_DASH_RUN_PREFIX = "them:dash:run:"
_DASH_RUNS_CHANNEL = "them:dash:runs"
_CARD_TTL_SECONDS = 3600  # re-fetch A2A agent card at most once per hour


# ─────────────────────────────────────────────────────────────────────────────
# Internal helpers
# ─────────────────────────────────────────────────────────────────────────────

async def _publish_dash(run_id: uuid.UUID, event: dict) -> None:
    """Publish to dashboard pub/sub channels."""
    if db_module.redis_client is None:
        return
    try:
        data = json.dumps(event)
        await db_module.redis_client.publish(f"{_DASH_RUN_PREFIX}{run_id}", data)
        summary = {"run_id": str(run_id), **{k: v for k, v in event.items() if k != "input"}}
        await db_module.redis_client.publish(_DASH_RUNS_CHANNEL, json.dumps(summary))
    except Exception as exc:
        logger.warning("task_runner: dash publish failed", error=str(exc))


@dataclass
class _OrchestratorProxy:
    """Typed cache proxy — replaces the old _Proxy + setattr pattern."""
    id: uuid.UUID
    name: str
    display_name: str
    system_prompt: str
    allowed_agent_ids: list
    llm_provider: str
    llm_model: str
    llm_api_key_encrypted: Optional[str]
    llm_base_url: Optional[str]
    max_iterations: int
    max_parallel_tools: int
    rate_limit_rpm: int
    daily_budget_usd: Decimal
    a2a_exposed: bool = False
    memory_enabled: bool = False
    summarize_every_n_calls: int = 3
    memory_raw_fallback_n: int = 5
    summarizer_provider: Optional[str] = None
    summarizer_model: Optional[str] = None
    summarizer_api_key_encrypted: Optional[str] = None
    history_window: int = 20
    budget_tokens: Optional[int] = None


async def _load_orchestrator_row(name: str, db: AsyncSession) -> Optional[Orchestrator]:
    """Load orchestrator from Redis cache → DB."""
    # Try Redis cache first
    if db_module.redis_client is not None:
        try:
            cached = await db_module.redis_client.get(f"{_ORCH_PREFIX}{name}")
            if cached:
                data = json.loads(cached)
                return _OrchestratorProxy(
                    id=uuid.UUID(data["id"]),
                    name=data["name"],
                    display_name=data.get("display_name", ""),
                    system_prompt=data.get("system_prompt", ""),
                    allowed_agent_ids=[uuid.UUID(x) for x in data.get("allowed_agent_ids", [])],
                    llm_provider=data.get("llm_provider", "anthropic"),
                    llm_model=data.get("llm_model", ""),
                    llm_api_key_encrypted=data.get("llm_api_key_encrypted"),
                    llm_base_url=data.get("llm_base_url"),
                    max_iterations=data.get("max_iterations", 10),
                    max_parallel_tools=data.get("max_parallel_tools", 4),
                    rate_limit_rpm=data.get("rate_limit_rpm", 60),
                    daily_budget_usd=Decimal(str(data.get("daily_budget_usd", "0"))),
                    a2a_exposed=data.get("a2a_exposed", False),
                    memory_enabled=data.get("memory_enabled", False),
                    summarize_every_n_calls=data.get("summarize_every_n_calls", 3),
                    memory_raw_fallback_n=data.get("memory_raw_fallback_n", 5),
                    summarizer_provider=data.get("summarizer_provider"),
                    summarizer_model=data.get("summarizer_model"),
                    summarizer_api_key_encrypted=data.get("summarizer_api_key_encrypted"),
                    history_window=data.get("history_window", 20),
                    budget_tokens=data.get("budget_tokens"),
                )  # type: ignore[return-value]
        except Exception as exc:
            logger.warning("task_runner: orchestrator cache miss", name=name, error=str(exc))

    result = await db.execute(
        select(Orchestrator).where(Orchestrator.name == name, Orchestrator.enabled == True)
    )
    row = result.scalar_one_or_none()
    if row is None:
        return None

    # Write to cache
    if db_module.redis_client is not None:
        try:
            payload = {
                "id": str(row.id),
                "name": row.name,
                "display_name": row.display_name,
                "system_prompt": row.system_prompt or "",
                "allowed_agent_ids": [str(x) for x in (row.allowed_agent_ids or [])],
                "llm_provider": row.llm_provider or "anthropic",
                "llm_model": row.llm_model,
                "llm_api_key_encrypted": row.llm_api_key_encrypted,
                "llm_base_url": row.llm_base_url,
                "max_iterations": row.max_iterations,
                "max_parallel_tools": row.max_parallel_tools,
                "rate_limit_rpm": row.rate_limit_rpm,
                "daily_budget_usd": str(row.daily_budget_usd),
                "a2a_exposed": getattr(row, "a2a_exposed", False),
                "memory_enabled": getattr(row, "memory_enabled", False),
                "summarize_every_n_calls": getattr(row, "summarize_every_n_calls", 3),
                "memory_raw_fallback_n": getattr(row, "memory_raw_fallback_n", 5),
                "summarizer_provider": getattr(row, "summarizer_provider", None),
                "summarizer_model": getattr(row, "summarizer_model", None),
                "summarizer_api_key_encrypted": getattr(row, "summarizer_api_key_encrypted", None),
                "history_window": getattr(row, "history_window", 20),
            }
            await db_module.redis_client.setex(f"{_ORCH_PREFIX}{name}", _ORCH_TTL, json.dumps(payload))
        except Exception:
            pass

    return row


async def _load_agents(orch, db: AsyncSession) -> list[Agent]:
    q = select(Agent).where(Agent.enabled == True)
    if orch.allowed_agent_ids:
        q = q.where(Agent.id.in_(orch.allowed_agent_ids))
    result = await db.execute(q.order_by(Agent.slug))
    return list(result.scalars().all())


async def _ensure_agent_skills(agent: Agent, db: AsyncSession) -> None:
    """
    Lazily fetch the A2A agent card and populate agent.skills.

    Re-fetches when skills are missing or card_fetched_at is older than
    _CARD_TTL_SECONDS. Never raises — on any failure the run proceeds with
    whatever skills/description are already on the agent row.
    """
    from datetime import datetime, timezone
    import httpx
    from app.utils.crypto import decrypt_value

    now = datetime.now(timezone.utc)
    fetched_at = getattr(agent, "card_fetched_at", None)
    has_skills = bool(getattr(agent, "skills", None))
    if has_skills and fetched_at is not None:
        if (now - fetched_at).total_seconds() < _CARD_TTL_SECONDS:
            return

    if not agent.endpoint_url:
        return

    card_url = agent.endpoint_url.rstrip("/") + "/.well-known/agent-card.json"
    headers = {"A2A-Version": "1.0"}
    token = decrypt_value(agent.auth_token_encrypted) if agent.auth_token_encrypted else ""
    if token:
        headers["Authorization"] = f"Bearer {token}"

    try:
        async with httpx.AsyncClient(timeout=10) as client:
            resp = await client.get(card_url, headers=headers)
        resp.raise_for_status()
        card = resp.json()
    except Exception as exc:
        logger.warning(
            "task_runner: agent card fetch failed — using existing skills/description",
            agent=agent.slug, url=card_url, error=str(exc),
        )
        return

    raw_skills = card.get("skills", []) or []
    skills = [
        {
            "id": s.get("id", ""),
            "name": s.get("name", ""),
            "description": s.get("description", ""),
            "tags": s.get("tags", []),
            "input_modes": s.get("inputModes") or s.get("input_modes") or [],
            "output_modes": s.get("outputModes") or s.get("output_modes") or [],
        }
        for s in raw_skills
        if isinstance(s, dict)
    ]

    agent.skills = skills
    agent.agent_card = card
    agent.agent_card_url = card_url
    agent.card_fetched_at = now
    try:
        await db.commit()
        logger.info(
            "task_runner: agent skills auto-discovered",
            agent=agent.slug, skills=len(skills),
        )
    except Exception as exc:
        await db.rollback()
        logger.warning(
            "task_runner: failed to persist discovered skills",
            agent=agent.slug, error=str(exc),
        )


def _compose_tool_description(agent: Agent) -> str:
    """
    Return agent.description as the tool description shown to the orchestrator LLM.

    Agent card skills describe the sub-agent's internal capabilities for routing
    decisions — they are NOT a menu of operations for the orchestrator to call
    individually. Exposing them here caused the orchestrator LLM to micro-manage
    each skill call instead of delegating a complete goal to the sub-agent.
    """
    return (agent.description or "").strip()


def _build_agent_tool_schema(agent: Agent) -> dict:
    """
    Build the JSON schema for this agent's tool as seen by the orchestrator LLM.

    A2A agents are autonomous: the orchestrator sends a goal as a text message and
    the sub-agent's own LLM handles the breakdown. The schema is always
    {message: string} so the orchestrator composes a rich goal, not individual
    skill calls.

    Exception: agents with an explicit non-empty input_schema (e.g. docu_writer)
    get their declared typed schema — they explicitly want structured input.
    """
    schema = agent.input_schema or {}
    if schema.get("properties"):
        return schema

    return {
        "type": "object",
        "properties": {"message": {"type": "string"}},
        "required": ["message"],
    }


_PROVIDER_DEFAULT_KEYS = {
    "openai": lambda s: (s.openai_api_key, s.openai_model),
    "anthropic": lambda s: (s.llm.api_key, s.llm.model),
}


def _build_provider(orch) -> LLMProvider:
    from app.config import settings
    from app.utils.crypto import decrypt_value
    provider_name = getattr(orch, "llm_provider", None) or "anthropic"
    if orch.llm_api_key_encrypted:
        api_key = decrypt_value(orch.llm_api_key_encrypted)
        model = orch.llm_model or _PROVIDER_DEFAULT_KEYS.get(provider_name, _PROVIDER_DEFAULT_KEYS["anthropic"])(settings)[1]
    else:
        default_key, default_model = _PROVIDER_DEFAULT_KEYS.get(
            provider_name, _PROVIDER_DEFAULT_KEYS["anthropic"]
        )(settings)
        api_key = default_key
        model = orch.llm_model or default_model
    return create_provider(provider_name, api_key=api_key, model=model)


# ─────────────────────────────────────────────────────────────────────────────
# Context reconstruction from task store
# ─────────────────────────────────────────────────────────────────────────────

async def _build_messages_from_store(
    provider: LLMProvider,
    task: Task,
    db: AsyncSession,
) -> list:
    """
    Reconstruct provider-native LLM message history from them.task_messages.
    Delegates format reconstruction to provider.deserialize_history() so the
    loop works with any provider (Anthropic, OpenAI, etc.).
    """
    from sqlalchemy import select as _select
    from app.models import TaskMessage
    result = await db.execute(
        _select(TaskMessage)
        .where(TaskMessage.task_id == task.id)
        .order_by(TaskMessage.seq)
    )
    rows = list(result.scalars().all())

    if not rows:
        input_text = ""
        input_msg = task.input_message or {}
        for part in input_msg.get("parts", []):
            if "text" in part:
                input_text = part["text"]
                break
        if not input_text:
            input_text = str(input_msg)
        return provider.init_messages(input_text)

    return provider.deserialize_history(rows)


async def _load_context_history(
    provider: LLMProvider,
    context_id: uuid.UUID,
    current_task_id: uuid.UUID,
    db: AsyncSession,
    history_window: int = 20,
) -> list:
    """
    Load prior root task messages for this context_id (excluding current task).
    history_window: max number of prior turns to include (-1 = unlimited).
    Returns a flat provider-native message list for prepending before the current turn.
    """
    from sqlalchemy import select as _select
    from app.models import TaskMessage

    q = (
        _select(Task)
        .where(
            Task.context_id == context_id,
            Task.id != current_task_id,
            Task.kind == "root",
        )
        .order_by(Task.created_at)
    )
    prior_tasks_result = await db.execute(q)
    prior_tasks = list(prior_tasks_result.scalars().all())

    # Apply window — keep only the most recent N turns
    if history_window >= 0 and len(prior_tasks) > history_window:
        prior_tasks = prior_tasks[-history_window:]
    if not prior_tasks:
        return []

    all_rows: list[TaskMessage] = []
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
            # task_messages not saved (old runs before multi-turn) — synthesize from input_message
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
                all_rows.append(r)  # type: ignore[arg-type]

    if not all_rows:
        return []
    return provider.deserialize_history(all_rows)


# ─────────────────────────────────────────────────────────────────────────────
# Agent invocation (creates child task row)
# ─────────────────────────────────────────────────────────────────────────────

async def _invoke_agent(
    agent: Agent,
    tool_call: ToolCall,
    semaphores: dict,
    run_id: uuid.UUID,
    root_task: Task,
    iteration: int,
    db: AsyncSession,  # kept for signature compat — parallel calls open their own sessions
    status_queue: asyncio.Queue = None,
) -> tuple[str, list[dict]]:
    """Returns (result_text, file_parts) where file_parts are A2A artifact parts with filename/media_type."""
    sem = semaphores.get(str(agent.id))
    t0 = time.monotonic()

    result_text = ""
    status = "completed"
    error_msg = None

    # Each parallel invocation opens its own DB session to avoid concurrent
    # access on the shared outer session (SQLAlchemy async sessions are not
    # safe for concurrent use within a single transaction).
    async with db_module.AsyncSessionLocal() as own_db:
        # Create child task row
        child_task = await task_store.create_task(
            own_db,
            context_id=root_task.context_id,
            input_message={"parts": [{"kind": "text", "text": tool_call.input.get("message", "")}]},
            kind="delegated",
            run_id=run_id,
            parent_task_id=root_task.id,
            agent_id=agent.id,
        )
        await task_store.transition(own_db, child_task.id, "working")

        # Legacy run_step record (billing/analytics log kept intact)
        step_id = await run_recorder.record_step(
            own_db,
            run_id=run_id,
            iteration=iteration,
            agent_id=agent.id,
            agent_slug=agent.slug,
            tool_call_id=tool_call.id,
            input=tool_call.input,
        )

        file_parts: list[dict] = []  # file parts from artifact events (filename + media_type)

        try:
            async with sem:
                adapter = get_adapter(agent, context_id=str(root_task.context_id))
                async for event in adapter.stream_invoke(
                    input=tool_call.input,
                    timeout=float(agent.timeout_seconds),
                ):
                    if event.type == "token":
                        result_text += event.text or ""
                    elif event.type == "status" and event.state:
                        if status_queue is not None:
                            elapsed = int((time.monotonic() - t0) * 1000)
                            await status_queue.put({
                                "type": "agent_status",
                                "agent": agent.slug,
                                "state": event.state,
                                "elapsed_ms": elapsed,
                            })
                    elif event.type == "artifact":
                        # Collect file parts (parts with filename/media_type) from A2A artifacts.
                        # Normalize camelCase mediaType → media_type so DB and WS are consistent.
                        # JSON data parts (application/json, no filename) feed result_text so the
                        # LLM can read the structured output as a tool result.
                        for p in (event.artifact or {}).get("parts", []):
                            media_type = p.get("media_type") or p.get("mediaType")
                            filename = p.get("filename")
                            is_json_data = media_type == "application/json" and not filename
                            if is_json_data:
                                # Data artifact — expose text to LLM, don't treat as file download
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
        except Exception as exc:
            error_msg = str(exc)
            status = "failed"
            logger.error("task_runner: agent invocation error", agent=agent.slug, error=str(exc))

        latency_ms = int((time.monotonic() - t0) * 1000)

        # Update child task — persist file parts if present, otherwise plain text
        child_state = "completed" if status == "completed" else "failed"
        if child_state == "completed" and (result_text or file_parts):
            parts = file_parts if file_parts else [{"kind": "text", "text": result_text}]
            await context_service.record_and_cache_artifact(
                task_id=child_task.id,
                context_id=root_task.context_id,
                artifact_id=f"{agent.slug}-{tool_call.id}",
                parts=parts,
                name=f"{agent.slug} result",
                db=own_db,
            )
        await task_store.transition(
            own_db, child_task.id, child_state,
            error=error_msg,
        )

        await run_recorder.complete_step(
            own_db,
            step_id=step_id,
            output=result_text if status == "completed" else None,
            status=status,
            error=error_msg,
            latency_ms=latency_ms,
        )

    if status == "failed":
        return f"[Agent {agent.slug} error: {error_msg}]", []
    return result_text, file_parts


# ─────────────────────────────────────────────────────────────────────────────
# Persist assistant message turn (for context reconstruction on next iteration)
# ─────────────────────────────────────────────────────────────────────────────

async def _persist_assistant_turn(
    db: AsyncSession,
    task_id: uuid.UUID,
    raw_response,
    seq: int,
    provider: LLMProvider,
) -> None:
    """Serialize and store the provider's raw response as a task_message for replay."""
    try:
        content = provider.serialize_turn(raw_response)
        await task_store.record_message(
            db,
            task_id=task_id,
            role="agent",
            parts={"content": content},
            seq=seq,
        )
    except Exception as exc:
        logger.warning("task_runner: persist_assistant_turn failed", error=str(exc))


async def _persist_tool_results(
    db: AsyncSession,
    task_id: uuid.UUID,
    tool_calls: list[ToolCall],
    results: list[str],
    seq: int,
) -> None:
    """Store tool results as a user-role task_message for context replay."""
    try:
        content = [
            {"type": "tool_result", "tool_use_id": tc.id, "content": result}
            for tc, result in zip(tool_calls, results)
        ]
        await task_store.record_message(
            db,
            task_id=task_id,
            role="user",
            parts={"content": content},
            seq=seq,
        )
    except Exception as exc:
        logger.warning("task_runner: persist_tool_results failed", error=str(exc))


# ─────────────────────────────────────────────────────────────────────────────
# Main run — async generator yielding WS events
# ─────────────────────────────────────────────────────────────────────────────

async def run(
    *,
    orchestrator_name: str,
    user_message: str,
    user_id: int,
    token_payload: dict,
    db: AsyncSession,
    session_id: Optional[uuid.UUID] = None,
    context_id: Optional[uuid.UUID] = None,
    entry_point_slug: Optional[str] = None,
) -> AsyncGenerator[dict, None]:
    """
    Durable agentic loop. Yields WS-ready event dicts.

    Every iteration persists assistant turns and tool results to them.task_messages
    so the LLM context can be rebuilt from the DB on resume.
    """
    if session_id is None:
        session_id = uuid.uuid4()
    if context_id is None:
        context_id = uuid.uuid4()

    # ── Load orchestrator ──────────────────────────────────────────────────
    orch = await _load_orchestrator_row(orchestrator_name, db)
    if orch is None:
        yield {"type": "error", "message": f"Orchestrator '{orchestrator_name}' not found or disabled"}
        return

    # ── Token scope check ──────────────────────────────────────────────────
    scoped_orch_id = token_payload.get("orchestrator_id")
    if scoped_orch_id and str(orch.id) != scoped_orch_id:
        yield {"type": "error", "message": "Token is not authorized for this orchestrator"}
        return

    # ── Load agents + auto-discover skills from A2A agent cards ───────────
    agents = await _load_agents(orch, db)
    if not agents:
        yield {"type": "error", "message": "No agents available for this orchestrator"}
        return

    for a in agents:
        await _ensure_agent_skills(a, db)

    tools: list[NeutralTool] = [
        {
            "name": f"agent__{a.slug}",
            "description": _compose_tool_description(a),
            "schema": _build_agent_tool_schema(a),
        }
        for a in agents
    ]
    agent_by_slug = {a.slug: a for a in agents}
    semaphores = {str(a.id): asyncio.Semaphore(a.max_concurrency) for a in agents}
    parallel_sem = asyncio.Semaphore(orch.max_parallel_tools)

    # ── Create run + root task ─────────────────────────────────────────────
    run_id = await run_recorder.start_run(
        db,
        orchestrator_id=orch.id,
        orchestrator_name=orch.name,
        user_id=user_id,
        session_id=session_id,
        goal=user_message,
        entry_point_slug=entry_point_slug,
    )

    root_task = await task_store.create_task(
        db,
        context_id=context_id,
        input_message={"parts": [{"kind": "text", "text": user_message}]},
        kind="root",
        run_id=run_id,
        orchestrator_id=orch.id,
        budget_tokens=getattr(orch, "budget_tokens", None),
    )
    await task_store.transition(db, root_task.id, "working")

    # Save user message as the first task_message so multi-turn history replay works.
    await task_store.record_message(
        db,
        task_id=root_task.id,
        role="user",
        parts={"content": user_message},
        seq=0,
    )

    await _publish_dash(run_id, {
        "type": "run_start",
        "orchestrator": orchestrator_name,
        "goal": user_message,
        "task_id": str(root_task.id),
        "context_id": str(context_id),
    })
    yield {"type": "ready", "run_id": str(run_id), "task_id": str(root_task.id), "context_id": str(context_id)}

    # ── Load model pricing ─────────────────────────────────────────────────
    provider_name = getattr(orch, "llm_provider", None) or "anthropic"
    model_name = orch.llm_model or "unknown"
    _pricing: dict = {}
    try:
        _llm_row = await db.execute(
            select(LLMProvider).where(LLMProvider.name == provider_name)
        )
        _llm_row = _llm_row.scalar_one_or_none()
        if _llm_row and _llm_row.model_pricing:
            _pricing = _llm_row.model_pricing.get(model_name, {})
    except Exception:
        pass
    _price_in  = Decimal(str(_pricing.get("input",  0))) / Decimal("1000000")
    _price_out = Decimal(str(_pricing.get("output", 0))) / Decimal("1000000")

    # ── Build LLM provider ─────────────────────────────────────────────────
    try:
        provider = _build_provider(orch)
    except Exception as exc:
        await _publish_dash(run_id, {"type": "error", "message": str(exc)})
        yield {"type": "error", "message": f"LLM provider error: {exc}"}
        await run_recorder.complete_run(db, run_id=run_id, status="failed", error=str(exc))
        await task_store.transition(db, root_task.id, "failed", error=str(exc))
        return

    # ── Agentic loop ───────────────────────────────────────────────────────
    total_in = total_out = 0
    total_cost = Decimal("0")
    iteration = 0
    final_answer = ""
    run_status = "completed"
    run_error = None
    msg_seq = 1  # seq=0 is the user message saved above
    agent_calls_since_summary = 0  # memory: incremented per agent call batch

    # Load prior turns from this context for multi-turn conversation history.
    prior_history: list = []
    try:
        prior_history = await _load_context_history(
            provider, context_id, root_task.id, db,
            history_window=getattr(orch, "history_window", 20),
        )
    except Exception as exc:
        logger.warning("task_runner: failed to load context history", context_id=str(context_id), error=str(exc))

    try:
        while iteration < orch.max_iterations:
            iteration += 1
            tool_calls_this_iter: list[ToolCall] = []
            raw_response = None
            iter_usage = TokenUsage()
            text_buffer = ""

            await _publish_dash(run_id, {"type": "iteration_start", "iteration": iteration})

            # Check budget before each planning turn
            if root_task.budget_tokens is not None:
                refreshed = await task_store.get_task(db, root_task.id)
                if refreshed and refreshed.tokens_used >= root_task.budget_tokens:
                    run_error = f"Budget exceeded: {refreshed.tokens_used} tokens used (limit: {root_task.budget_tokens})"
                    run_status = "failed"
                    yield {"type": "error", "message": run_error}
                    break

            # Rebuild current-task context from DB (durable-planner key property).
            # Prepend prior turns so the LLM sees the full multi-turn conversation.
            current_messages = await _build_messages_from_store(provider, root_task, db)
            messages = prior_history + current_messages

            # Stream LLM call
            async for event in provider.stream_call(
                system=orch.system_prompt,
                messages=messages,
                tools=tools,
                max_tokens=4096,
            ):
                if event.type == "token":
                    text_buffer += event.text or ""
                    yield {"type": "token", "text": event.text or ""}

                elif event.type == "tool_calls_ready":
                    result_data = event.result or {}
                    tool_calls_this_iter = result_data.get("tool_calls", [])
                    raw_response = result_data.get("raw_response")
                    iter_usage = result_data.get("usage", TokenUsage())

                elif event.type == "done":
                    result_data = event.result or {}
                    final_answer = result_data.get("answer", text_buffer)
                    raw_response = result_data.get("raw_response")
                    iter_usage = result_data.get("usage", TokenUsage()) if event.usage is None else event.usage

                elif event.type == "error":
                    run_error = event.error
                    run_status = "failed"
                    yield {"type": "error", "message": event.error or "LLM error"}
                    break

            if run_status == "failed":
                break

            # Record token usage + cost
            total_in += iter_usage.input_tokens
            total_out += iter_usage.output_tokens
            iter_cost = (
                Decimal(iter_usage.input_tokens)  * _price_in +
                Decimal(iter_usage.output_tokens) * _price_out
            )
            total_cost += iter_cost
            await _publish_dash(run_id, {
                "type": "usage",
                "iteration": iteration,
                "input_tokens": iter_usage.input_tokens,
                "output_tokens": iter_usage.output_tokens,
                "cost_usd": float(iter_cost),
            })
            await run_recorder.record_usage(
                db,
                run_id=run_id,
                user_id=user_id,
                provider=orch.llm_provider,
                model=orch.llm_model or "unknown",
                usage=iter_usage,
                cost_usd=iter_cost,
            )
            # Update root task token count
            if iter_usage.input_tokens + iter_usage.output_tokens > 0:
                await task_store.add_tokens_used(
                    db, root_task.id,
                    iter_usage.input_tokens + iter_usage.output_tokens,
                )

            # Persist assistant turn for context reconstruction
            if raw_response is not None:
                await _persist_assistant_turn(db, root_task.id, raw_response, seq=msg_seq, provider=provider)
                msg_seq += 1
                provider.append_assistant_response(messages, raw_response)

            # No tool calls → done
            if not tool_calls_this_iter:
                break

            # Fan out tool calls in parallel
            for tc in tool_calls_this_iter:
                yield {"type": "tool_start", "tool": tc.name, "input": tc.input}
                await _publish_dash(run_id, {
                    "type": "tool_start",
                    "tool": tc.name,
                    "input": tc.input,
                    "iteration": iteration,
                })

            # Inject memory context into each agent call when available
            _injected_ctx: Optional[str] = None
            if getattr(orch, "memory_enabled", False):
                from app.services.memory_service import get_injected_context
                _injected_ctx = await get_injected_context(context_id)

            # Queue for agent_status events emitted by parallel _invoke_agent calls.
            # We run each invocation as a Task and poll the queue between awaits so
            # status events stream to the client live instead of batching after gather.
            _status_q: asyncio.Queue = asyncio.Queue()

            async def _run_one(tc: ToolCall) -> tuple[ToolCall, str, list[dict], int]:
                slug = tc.name.removeprefix("agent__")
                agent = agent_by_slug.get(slug)
                if agent is None:
                    return tc, f"[Unknown agent: {slug}]", [], 0
                tc_input = dict(tc.input)
                # Typed agents (explicit input_schema with properties) get context as a
                # separate __context__ key so the adapter sends it as a text part alongside
                # the data part. Text-only agents get it prepended to the message string.
                is_typed = bool((agent.input_schema or {}).get("properties"))
                if is_typed:
                    if _injected_ctx:
                        tc_input["__context__"] = _injected_ctx
                else:
                    if _injected_ctx and "message" in tc_input:
                        tc_input["message"] = f"[Context summary]\n{_injected_ctx}\n\n{tc_input['message']}"
                tc = ToolCall(id=tc.id, name=tc.name, input=tc_input)
                t_start = time.monotonic()
                async with parallel_sem:
                    result_text, file_parts = await _invoke_agent(
                        agent, tc, semaphores, run_id, root_task, iteration, db,
                        status_queue=_status_q,
                    )
                elapsed_ms = int((time.monotonic() - t_start) * 1000)
                return tc, result_text, file_parts, elapsed_ms

            results_pairs = await asyncio.gather(*[_run_one(tc) for tc in tool_calls_this_iter])

            # Drain status events collected during agent execution
            while not _status_q.empty():
                yield _status_q.get_nowait()

            results: list[str] = []
            for tc, result, fp, elapsed_ms in results_pairs:
                yield {"type": "tool_done", "tool": tc.name, "latency_ms": elapsed_ms}
                await _publish_dash(run_id, {
                    "type": "tool_done",
                    "tool": tc.name,
                    "output": result,
                    "iteration": iteration,
                })
                # Emit file events over WS for each A2A file artifact part
                for part in fp:
                    if part.get("filename") and part.get("media_type"):
                        yield {
                            "type": "file",
                            "filename": part["filename"],
                            "media_type": part["media_type"],
                            "text": part.get("text", ""),
                        }
                results.append(result)

            # Persist tool results for context reconstruction
            await _persist_tool_results(db, root_task.id, tool_calls_this_iter, results, seq=msg_seq)
            msg_seq += 1
            provider.append_tool_results(messages, tool_calls_this_iter, results)

            # Memory: summarize after N agent calls
            agent_calls_since_summary += len(tool_calls_this_iter)
            if getattr(orch, "memory_enabled", False):
                threshold = getattr(orch, "summarize_every_n_calls", 3)
                if agent_calls_since_summary >= threshold:
                    from app.services import context_service as _cs
                    from app.services.memory_service import summarize_context
                    artifacts = await _cs.get_context_artifacts(context_id, db, limit=20)
                    await summarize_context(
                        context_id=context_id,
                        orch=orch,
                        artifacts=artifacts,
                        root_task_id=root_task.id,
                        db=db,
                    )
                    agent_calls_since_summary = 0

        else:
            run_status = "stopped"
            run_error = f"Reached max iterations ({orch.max_iterations})"
            yield {"type": "error", "message": run_error}

    except Exception as exc:
        run_status = "failed"
        run_error = str(exc)
        logger.error("task_runner: loop error", run_id=str(run_id), error=str(exc))
        yield {"type": "error", "message": f"Internal error: {exc}"}

    # ── Complete run + root task ───────────────────────────────────────────
    await run_recorder.complete_run(
        db,
        run_id=run_id,
        status=run_status,
        final_output=final_answer or None,
        error=run_error,
        iterations=iteration,
        total_tokens_in=total_in,
        total_tokens_out=total_out,
        total_cost_usd=total_cost,
    )

    # Record final answer as root task artifact
    if final_answer:
        await context_service.record_and_cache_artifact(
            task_id=root_task.id,
            context_id=context_id,
            artifact_id="final-answer",
            parts=[{"kind": "text", "text": final_answer}],
            name="Final Answer",
            db=db,
        )

    final_task_state = "completed" if run_status == "completed" else "failed"
    await task_store.transition(
        db, root_task.id, final_task_state,
        error=run_error,
    )

    await _publish_dash(run_id, {
        "type": "run_end",
        "status": run_status,
        "iterations": iteration,
        "total_tokens_in": total_in,
        "total_tokens_out": total_out,
        "task_id": str(root_task.id),
        "error": run_error,
    })

    if run_status == "completed":
        yield {"type": "done", "run_id": str(run_id), "task_id": str(root_task.id), "iterations": iteration}
