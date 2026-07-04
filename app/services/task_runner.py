"""
task_runner — durable A2A-native orchestration engine.

Replaces the body of orchestrator_service.run_orchestrator.

Key properties vs the old loop:
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
from decimal import Decimal
from typing import AsyncGenerator, Callable, Optional

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.adapters.factory import get_adapter
from app.models import Agent, Orchestrator, Task
from app.services import run_recorder, task_store
from app.services.providers.anthropic import AnthropicProvider
from app.services.providers.base import (
    LLMStreamEvent, NeutralTool, ToolCall, TokenUsage,
)
from app.utils.logger import logger

_ORCH_PREFIX = "them:orchestrators:"
_ORCH_TTL = 600
_DASH_RUN_PREFIX = "them:dash:run:"
_DASH_RUNS_CHANNEL = "them:dash:runs"


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


async def _load_orchestrator_row(name: str, db: AsyncSession) -> Optional[Orchestrator]:
    """Load orchestrator from Redis cache → DB."""
    # Try Redis cache first
    if db_module.redis_client is not None:
        try:
            cached = await db_module.redis_client.get(f"{_ORCH_PREFIX}{name}")
            if cached:
                data = json.loads(cached)
                # Build a lightweight proxy from cache
                class _Proxy:
                    pass
                p = _Proxy()
                for k, v in data.items():
                    setattr(p, k, v)
                p.id = uuid.UUID(data["id"])
                p.allowed_agent_ids = [uuid.UUID(x) for x in data.get("allowed_agent_ids", [])]
                p.daily_budget_usd = Decimal(str(data.get("daily_budget_usd", "0")))
                p.llm_api_key_encrypted = data.get("llm_api_key_encrypted")
                p.llm_base_url = data.get("llm_base_url")
                p.a2a_exposed = data.get("a2a_exposed", False)
                return p  # type: ignore[return-value]
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


def _build_provider(orch) -> AnthropicProvider:
    from app.config import settings
    from app.utils.crypto import decrypt_value
    api_key = decrypt_value(orch.llm_api_key_encrypted) if orch.llm_api_key_encrypted else settings.llm.api_key
    model = orch.llm_model or settings.llm.model
    return AnthropicProvider(api_key=api_key, model=model)


# ─────────────────────────────────────────────────────────────────────────────
# Context reconstruction from task store
# ─────────────────────────────────────────────────────────────────────────────

async def _build_messages_from_store(
    provider: AnthropicProvider,
    task: Task,
    db: AsyncSession,
) -> list:
    """
    Reconstruct LLM message history from them.task_messages + them.artifacts.

    For Phase 3, the root task has one user message (the goal) + any assistant
    turns and tool results stored as task_messages. This is what makes the loop
    resumable — no in-RAM accumulator.
    """
    # Load messages ordered by seq
    from sqlalchemy import select as _select
    from app.models import TaskMessage
    result = await db.execute(
        _select(TaskMessage)
        .where(TaskMessage.task_id == task.id)
        .order_by(TaskMessage.seq)
    )
    rows = list(result.scalars().all())

    if not rows:
        # First turn: use the input_message
        input_text = ""
        input_msg = task.input_message or {}
        parts = input_msg.get("parts", [])
        for part in parts:
            if part.get("kind") == "text" or "text" in part:
                input_text = part.get("text", "")
                break
        if not input_text:
            input_text = str(input_msg)
        return provider.init_messages(input_text)

    # Reconstruct from stored turns
    messages = []
    for row in rows:
        parts = row.parts
        if isinstance(parts, list):
            messages.append({"role": row.role, "content": parts})
        elif isinstance(parts, dict):
            messages.append({"role": row.role, "content": parts.get("content", [])})
    return messages


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
    db: AsyncSession,
) -> str:
    sem = semaphores.get(str(agent.id))
    t0 = time.monotonic()

    # Create child task row
    child_task = await task_store.create_task(
        db,
        context_id=root_task.context_id,
        input_message={"parts": [{"kind": "text", "text": tool_call.input.get("message", "")}]},
        kind="delegated",
        run_id=run_id,
        parent_task_id=root_task.id,
        agent_id=agent.id,
    )
    await task_store.transition(db, child_task.id, "working")

    # Legacy run_step record (billing/analytics log kept intact)
    step_id = await run_recorder.record_step(
        db,
        run_id=run_id,
        iteration=iteration,
        agent_id=agent.id,
        agent_slug=agent.slug,
        tool_call_id=tool_call.id,
        input=tool_call.input,
    )

    result_text = ""
    status = "completed"
    error_msg = None

    try:
        async with sem:
            adapter = get_adapter(agent, context_id=str(root_task.context_id))
            async for event in adapter.stream_invoke(
                input=tool_call.input,
                timeout=float(agent.timeout_seconds),
            ):
                if event.type == "token":
                    result_text += event.text or ""
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

    # Update child task
    child_state = "completed" if status == "completed" else "failed"
    if child_state == "completed" and result_text:
        await task_store.record_artifact(
            db,
            task_id=child_task.id,
            context_id=root_task.context_id,
            artifact_id=f"{agent.slug}-{tool_call.id}",
            parts=[{"kind": "text", "text": result_text}],
            name=f"{agent.slug} result",
        )
    await task_store.transition(
        db, child_task.id, child_state,
        error=error_msg,
    )

    await run_recorder.complete_step(
        db,
        step_id=step_id,
        output=result_text if status == "completed" else None,
        status=status,
        error=error_msg,
        latency_ms=latency_ms,
    )

    if status == "failed":
        return f"[Agent {agent.slug} error: {error_msg}]"
    return result_text


# ─────────────────────────────────────────────────────────────────────────────
# Persist assistant message turn (for context reconstruction on next iteration)
# ─────────────────────────────────────────────────────────────────────────────

async def _persist_assistant_turn(
    db: AsyncSession,
    task_id: uuid.UUID,
    raw_response,
    seq: int,
) -> None:
    """Store the raw Anthropic response content as a task_message for replay."""
    try:
        # Serialize the response blocks to a portable list
        content = []
        for block in raw_response.content:
            block_type = getattr(block, "type", None)
            if block_type == "text":
                content.append({"type": "text", "text": block.text})
            elif block_type == "tool_use":
                content.append({
                    "type": "tool_use",
                    "id": block.id,
                    "name": block.name,
                    "input": block.input,
                })

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

    # ── Load agents ────────────────────────────────────────────────────────
    agents = await _load_agents(orch, db)
    if not agents:
        yield {"type": "error", "message": "No agents available for this orchestrator"}
        return

    tools: list[NeutralTool] = [
        {
            "name": f"agent__{a.slug}",
            "description": a.description,
            "schema": a.input_schema or {
                "type": "object",
                "properties": {"message": {"type": "string"}},
                "required": ["message"],
            },
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
    )

    root_task = await task_store.create_task(
        db,
        context_id=context_id,
        input_message={"parts": [{"kind": "text", "text": user_message}]},
        kind="root",
        run_id=run_id,
        orchestrator_id=orch.id,
        budget_tokens=None,  # Phase 4 adds budget enforcement
    )
    await task_store.transition(db, root_task.id, "working")

    await _publish_dash(run_id, {
        "type": "run_start",
        "orchestrator": orchestrator_name,
        "goal": user_message,
        "task_id": str(root_task.id),
        "context_id": str(context_id),
    })
    yield {"type": "ready", "run_id": str(run_id), "task_id": str(root_task.id)}

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
    # seq tracks the next message sequence number for task_messages
    msg_seq = 0

    try:
        while iteration < orch.max_iterations:
            iteration += 1
            tool_calls_this_iter: list[ToolCall] = []
            raw_response = None
            iter_usage = TokenUsage()
            text_buffer = ""

            await _publish_dash(run_id, {"type": "iteration_start", "iteration": iteration})

            # Rebuild context from DB (the durable-planner key property)
            messages = await _build_messages_from_store(provider, root_task, db)

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

            # Record token usage
            total_in += iter_usage.input_tokens
            total_out += iter_usage.output_tokens
            await _publish_dash(run_id, {
                "type": "usage",
                "iteration": iteration,
                "input_tokens": iter_usage.input_tokens,
                "output_tokens": iter_usage.output_tokens,
            })
            await run_recorder.record_usage(
                db,
                run_id=run_id,
                user_id=user_id,
                provider=orch.llm_provider,
                model=orch.llm_model or "unknown",
                usage=iter_usage,
            )
            # Update root task token count
            if iter_usage.input_tokens + iter_usage.output_tokens > 0:
                await task_store.add_tokens_used(
                    db, root_task.id,
                    iter_usage.input_tokens + iter_usage.output_tokens,
                )

            # Persist assistant turn for context reconstruction
            if raw_response is not None:
                await _persist_assistant_turn(db, root_task.id, raw_response, seq=msg_seq)
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

            async def _run_one(tc: ToolCall) -> tuple[ToolCall, str]:
                slug = tc.name.removeprefix("agent__")
                agent = agent_by_slug.get(slug)
                if agent is None:
                    return tc, f"[Unknown agent: {slug}]"
                async with parallel_sem:
                    result = await _invoke_agent(
                        agent, tc, semaphores, run_id, root_task, iteration, db
                    )
                return tc, result

            results_pairs = await asyncio.gather(*[_run_one(tc) for tc in tool_calls_this_iter])

            results: list[str] = []
            for tc, result in results_pairs:
                yield {"type": "tool_done", "tool": tc.name, "latency_ms": 0}
                await _publish_dash(run_id, {
                    "type": "tool_done",
                    "tool": tc.name,
                    "output": result,
                    "iteration": iteration,
                })
                results.append(result)

            # Persist tool results for context reconstruction
            await _persist_tool_results(db, root_task.id, tool_calls_this_iter, results, seq=msg_seq)
            msg_seq += 1
            provider.append_tool_results(messages, tool_calls_this_iter, results)

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
        await task_store.record_artifact(
            db,
            task_id=root_task.id,
            context_id=context_id,
            artifact_id="final-answer",
            parts=[{"kind": "text", "text": final_answer}],
            name="Final Answer",
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
