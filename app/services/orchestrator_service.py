"""
Orchestrator service — the agentic loop.

Lifecycle per WS connection:
  1. Load orchestrator config (Redis → DB)
  2. Build NeutralTool list from enabled agents
  3. Create LLM provider
  4. Stream agentic loop: LLM → tool calls → adapter fan-out → LLM → ...
  5. Stream all events to caller via async generator

Parallel tool execution: asyncio.gather() bounded by:
  - orchestrator.max_parallel_tools (global cap)
  - per-agent asyncio.Semaphore(agent.max_concurrency)
"""

import asyncio
import json
import time
import uuid
from decimal import Decimal
from typing import AsyncGenerator, Optional

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.adapters.factory import get_adapter
from app.models import Agent, Orchestrator
from app.services import run_recorder
from app.services.providers.base import (
    LLMStreamEvent, NeutralTool, ToolCall, TokenUsage,
)
from app.services.providers.anthropic import AnthropicProvider
from app.utils.logger import logger

_ORCH_PREFIX = "odin:orchestrators:"
_ORCH_TTL = 600


# ------------------------------------------------------------------ #
# Dataclass for a loaded orchestrator                                  #
# ------------------------------------------------------------------ #

class OrchestratorConfig:
    def __init__(self, row: Orchestrator):
        self.id = row.id
        self.name = row.name
        self.display_name = row.display_name
        self.system_prompt = row.system_prompt or ""
        self.allowed_agent_ids = list(row.allowed_agent_ids or [])
        self.llm_provider = row.llm_provider or "anthropic"
        self.llm_model = row.llm_model
        self.max_iterations = row.max_iterations
        self.max_parallel_tools = row.max_parallel_tools
        self.rate_limit_rpm = row.rate_limit_rpm
        self.daily_budget_usd = row.daily_budget_usd


# ------------------------------------------------------------------ #
# Load orchestrator (Redis → DB)                                       #
# ------------------------------------------------------------------ #

async def _load_orchestrator(name: str, db: AsyncSession) -> Optional[OrchestratorConfig]:
    # L2 Redis cache
    if db_module.redis_client is not None:
        try:
            cached = await db_module.redis_client.get(f"{_ORCH_PREFIX}{name}")
            if cached:
                data = json.loads(cached)
                # Rebuild a minimal proxy from cached dict
                class _Proxy:
                    pass
                p = _Proxy()
                for k, v in data.items():
                    setattr(p, k, v)
                p.id = uuid.UUID(data["id"])
                p.allowed_agent_ids = [uuid.UUID(x) for x in data.get("allowed_agent_ids", [])]
                p.daily_budget_usd = Decimal(str(data.get("daily_budget_usd", "0")))
                return OrchestratorConfig(p)
        except Exception as exc:
            logger.warning("orchestrator cache miss", name=name, error=str(exc))

    # DB
    result = await db.execute(
        select(Orchestrator).where(Orchestrator.name == name, Orchestrator.enabled == True)
    )
    row = result.scalar_one_or_none()
    if row is None:
        return None

    # Write to Redis
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
                "max_iterations": row.max_iterations,
                "max_parallel_tools": row.max_parallel_tools,
                "rate_limit_rpm": row.rate_limit_rpm,
                "daily_budget_usd": str(row.daily_budget_usd),
            }
            await db_module.redis_client.setex(
                f"{_ORCH_PREFIX}{name}", _ORCH_TTL, json.dumps(payload)
            )
        except Exception as exc:
            logger.warning("orchestrator cache write failed", error=str(exc))

    return OrchestratorConfig(row)


# ------------------------------------------------------------------ #
# Build tool list from agent rows                                      #
# ------------------------------------------------------------------ #

async def _build_tools(orch: OrchestratorConfig, db: AsyncSession) -> list[dict]:
    """Return (agent_row, semaphore) pairs and NeutralTool list."""
    q = select(Agent).where(Agent.enabled == True)
    if orch.allowed_agent_ids:
        q = q.where(Agent.id.in_(orch.allowed_agent_ids))
    result = await db.execute(q.order_by(Agent.slug))
    agents = result.scalars().all()
    return agents


# ------------------------------------------------------------------ #
# LLM provider factory                                                 #
# ------------------------------------------------------------------ #

def _build_provider(orch: OrchestratorConfig) -> AnthropicProvider:
    from app.config import settings
    from app.utils.crypto import decrypt_value
    # For now only Anthropic is implemented
    api_key = settings.llm.api_key
    model = orch.llm_model or settings.llm.model
    return AnthropicProvider(api_key=api_key, model=model)


# ------------------------------------------------------------------ #
# Single agent tool execution                                          #
# ------------------------------------------------------------------ #

async def _invoke_agent(
    agent: Agent,
    tool_call: ToolCall,
    semaphores: dict,
    run_id: uuid.UUID,
    iteration: int,
    db: AsyncSession,
) -> str:
    sem = semaphores.get(str(agent.id))
    t0 = time.monotonic()

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
            adapter = get_adapter(agent)
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
        logger.error("agent invocation error", agent=agent.slug, error=str(exc))

    latency_ms = int((time.monotonic() - t0) * 1000)
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


# ------------------------------------------------------------------ #
# WS event types                                                       #
# ------------------------------------------------------------------ #

def _ws_token(text: str) -> dict:
    return {"type": "token", "text": text}

def _ws_tool_start(name: str, input: dict) -> dict:
    return {"type": "tool_start", "tool": name, "input": input}

def _ws_tool_done(name: str, latency_ms: int) -> dict:
    return {"type": "tool_done", "tool": name, "latency_ms": latency_ms}

def _ws_done(run_id: str, iterations: int) -> dict:
    return {"type": "done", "run_id": run_id, "iterations": iterations}

def _ws_error(message: str) -> dict:
    return {"type": "error", "message": message}

def _ws_ready(run_id: str) -> dict:
    return {"type": "ready", "run_id": run_id}


# ------------------------------------------------------------------ #
# Main orchestrator run                                                #
# ------------------------------------------------------------------ #

async def run_orchestrator(
    *,
    orchestrator_name: str,
    user_message: str,
    user_id: int,
    token_payload: dict,
    db: AsyncSession,
    session_id: Optional[uuid.UUID] = None,
) -> AsyncGenerator[dict, None]:
    """
    Full agentic loop. Yields WS-ready dicts to stream to the client.
    """
    if session_id is None:
        session_id = uuid.uuid4()

    # Load orchestrator
    orch = await _load_orchestrator(orchestrator_name, db)
    if orch is None:
        yield _ws_error(f"Orchestrator '{orchestrator_name}' not found or disabled")
        return

    # Validate token is scoped correctly
    scoped_orch_id = token_payload.get("orchestrator_id")
    if scoped_orch_id and str(orch.id) != scoped_orch_id:
        yield _ws_error("Token is not authorized for this orchestrator")
        return

    # Build agent list
    agents = await _build_tools(orch, db)
    if not agents:
        yield _ws_error("No agents available for this orchestrator")
        return

    # Build NeutralTools and per-agent semaphores
    tools: list[NeutralTool] = [
        {
            "name": f"agent__{a.slug}",
            "description": a.description,
            "schema": a.input_schema or {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]},
        }
        for a in agents
    ]
    agent_by_slug = {a.slug: a for a in agents}
    semaphores = {str(a.id): asyncio.Semaphore(a.max_concurrency) for a in agents}
    parallel_sem = asyncio.Semaphore(orch.max_parallel_tools)

    # Start run record
    run_id = await run_recorder.start_run(
        db,
        orchestrator_id=orch.id,
        orchestrator_name=orch.name,
        user_id=user_id,
        session_id=session_id,
        goal=user_message,
    )

    yield _ws_ready(str(run_id))

    # Build provider
    try:
        provider = _build_provider(orch)
    except Exception as exc:
        yield _ws_error(f"LLM provider error: {exc}")
        await run_recorder.complete_run(db, run_id=run_id, status="failed", error=str(exc))
        return

    # Agentic loop
    messages = provider.init_messages(user_message)
    total_in = total_out = 0
    total_cost = Decimal("0")
    iteration = 0
    final_answer = ""
    run_status = "completed"
    run_error = None

    try:
        while iteration < orch.max_iterations:
            iteration += 1
            tool_calls_this_iter: list[ToolCall] = []
            raw_response = None
            iter_usage = TokenUsage()
            text_buffer = ""

            # Stream LLM call
            async for event in provider.stream_call(
                system=orch.system_prompt,
                messages=messages,
                tools=tools,
                max_tokens=4096,
            ):
                if event.type == "token":
                    text_buffer += event.text or ""
                    yield _ws_token(event.text or "")

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
                    yield _ws_error(event.error or "LLM error")
                    break

            if run_status == "failed":
                break

            # Record usage
            total_in += iter_usage.input_tokens
            total_out += iter_usage.output_tokens
            await run_recorder.record_usage(
                db,
                run_id=run_id,
                user_id=user_id,
                provider=orch.llm_provider,
                model=orch.llm_model or "unknown",
                usage=iter_usage,
            )

            # Append assistant response to history
            if raw_response is not None:
                provider.append_assistant_response(messages, raw_response)

            # No tool calls → done
            if not tool_calls_this_iter:
                break

            # Fan out tool calls in parallel
            async def _run_one(tc: ToolCall) -> tuple[ToolCall, str]:
                slug = tc.name.removeprefix("agent__")
                agent = agent_by_slug.get(slug)
                if agent is None:
                    return tc, f"[Unknown agent: {slug}]"
                t0 = time.monotonic()
                yield_start = _ws_tool_start(tc.name, tc.input)
                async with parallel_sem:
                    result = await _invoke_agent(
                        agent, tc, semaphores, run_id, iteration, db
                    )
                latency = int((time.monotonic() - t0) * 1000)
                return tc, result

            # Collect starts, run, collect results
            for tc in tool_calls_this_iter:
                yield _ws_tool_start(tc.name, tc.input)

            tasks = [_run_one(tc) for tc in tool_calls_this_iter]
            results_pairs = await asyncio.gather(*tasks)

            results: list[str] = []
            for tc, result in results_pairs:
                slug = tc.name.removeprefix("agent__")
                latency = 0  # approximate
                yield _ws_tool_done(tc.name, latency)
                results.append(result)

            provider.append_tool_results(messages, tool_calls_this_iter, results)

        else:
            # Hit max_iterations
            run_status = "stopped"
            run_error = f"Reached max iterations ({orch.max_iterations})"
            yield _ws_error(run_error)

    except Exception as exc:
        run_status = "failed"
        run_error = str(exc)
        logger.error("orchestrator loop error", run_id=str(run_id), error=str(exc))
        yield _ws_error(f"Internal error: {exc}")

    # Complete run
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

    if run_status == "completed":
        yield _ws_done(str(run_id), iteration)
