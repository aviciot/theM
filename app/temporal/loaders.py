"""
loaders — pure DB/cache helpers shared by Activities and legacy task_runner.

Extracted from task_runner.py. No Temporal imports here — these are plain
async functions, safe to call from any context.
"""

import json
import uuid
from decimal import Decimal
from typing import Optional

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.models import Agent, LLMProvider, Orchestrator
from app.temporal.shared import AgentConfig, LoadContextResult, OrchestratorConfig
from app.utils.logger import logger

_ORCH_PREFIX = "them:orchestrators:"
_ORCH_TTL = 600
_CARD_TTL_SECONDS = 3600


# ─────────────────────────────────────────────────────────────────────────────
# Orchestrator loading (Redis cache → DB)
# ─────────────────────────────────────────────────────────────────────────────

async def load_orchestrator_row(name: str, db: AsyncSession):
    """Load orchestrator config from Redis cache or DB. Returns an ORM row or proxy."""
    if db_module.redis_client is not None:
        try:
            cached = await db_module.redis_client.get(f"{_ORCH_PREFIX}{name}")
            if cached:
                data = json.loads(cached)
                return _make_proxy(data)
        except Exception as exc:
            logger.warning("loaders: orchestrator cache miss", name=name, error=str(exc))

    result = await db.execute(
        select(Orchestrator).where(Orchestrator.name == name, Orchestrator.enabled == True)
    )
    row = result.scalar_one_or_none()
    if row is None:
        return None

    if db_module.redis_client is not None:
        try:
            payload = _orchestrator_to_cache_dict(row)
            await db_module.redis_client.setex(
                f"{_ORCH_PREFIX}{name}", _ORCH_TTL, json.dumps(payload)
            )
        except Exception:
            pass

    return row


def _make_proxy(data: dict):
    """Reconstruct a typed proxy from Redis cache dict."""
    from dataclasses import dataclass

    @dataclass
    class _OrchestratorProxy:
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
    )


def _orchestrator_to_cache_dict(row) -> dict:
    return {
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
        "budget_tokens": getattr(row, "budget_tokens", None),
    }


# ─────────────────────────────────────────────────────────────────────────────
# Agent loading
# ─────────────────────────────────────────────────────────────────────────────

async def load_agents(orch, db: AsyncSession) -> list:
    q = select(Agent).where(Agent.enabled == True)
    if orch.allowed_agent_ids:
        q = q.where(Agent.id.in_(orch.allowed_agent_ids))
    result = await db.execute(q.order_by(Agent.slug))
    return list(result.scalars().all())


async def ensure_agent_skills(agent, db: AsyncSession) -> None:
    """Lazily fetch A2A agent card and populate agent.skills. Never raises."""
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
            "loaders: agent card fetch failed",
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
        logger.info("loaders: agent skills auto-discovered", agent=agent.slug, skills=len(skills))
    except Exception as exc:
        await db.rollback()
        logger.warning("loaders: failed to persist discovered skills", agent=agent.slug, error=str(exc))


# ─────────────────────────────────────────────────────────────────────────────
# Tool schema builders
# ─────────────────────────────────────────────────────────────────────────────

def compose_tool_description(agent) -> str:
    return (agent.description or "").strip()


def build_agent_tool_schema(agent) -> dict:
    schema = agent.input_schema or {}
    if schema.get("properties"):
        return schema
    return {
        "type": "object",
        "properties": {"message": {"type": "string"}},
        "required": ["message"],
    }


# ─────────────────────────────────────────────────────────────────────────────
# LLM provider builder
# ─────────────────────────────────────────────────────────────────────────────

_PROVIDER_DEFAULT_KEYS = {
    "openai": lambda s: (s.openai_api_key, s.openai_model),
    "anthropic": lambda s: (s.llm.api_key, s.llm.model),
}


def build_provider(orch):
    from app.config import settings
    from app.services.providers import create_provider
    from app.utils.crypto import decrypt_value

    provider_name = getattr(orch, "llm_provider", None) or "anthropic"
    if orch.llm_api_key_encrypted:
        api_key = decrypt_value(orch.llm_api_key_encrypted)
        model = orch.llm_model or _PROVIDER_DEFAULT_KEYS.get(
            provider_name, _PROVIDER_DEFAULT_KEYS["anthropic"]
        )(settings)[1]
    else:
        default_key, default_model = _PROVIDER_DEFAULT_KEYS.get(
            provider_name, _PROVIDER_DEFAULT_KEYS["anthropic"]
        )(settings)
        api_key = default_key
        model = orch.llm_model or default_model
    return create_provider(provider_name, api_key=api_key, model=model)


def build_provider_from_config(provider_name: str, model: str, api_key_encrypted: Optional[str], base_url: Optional[str] = None):
    """Build provider from serialized config (used inside Activities — no ORM dependency)."""
    from app.config import Settings
    from app.services.providers import create_provider
    from app.utils.crypto import decrypt_value

    env = Settings()

    if api_key_encrypted:
        api_key = decrypt_value(api_key_encrypted)
    else:
        defaults = _PROVIDER_DEFAULT_KEYS.get(provider_name, _PROVIDER_DEFAULT_KEYS["anthropic"])
        api_key = defaults(env)[0]

    return create_provider(provider_name, api_key=api_key, model=model)


# ─────────────────────────────────────────────────────────────────────────────
# Pricing loader
# ─────────────────────────────────────────────────────────────────────────────

async def load_model_pricing(provider_name: str, model_name: str, db: AsyncSession) -> tuple[str, str]:
    """Returns (price_in_str, price_out_str) — per-token USD as strings."""
    try:
        row = await db.execute(
            select(LLMProvider).where(LLMProvider.name == provider_name)
        )
        row = row.scalar_one_or_none()
        if row and row.model_pricing:
            pricing = row.model_pricing.get(model_name, {})
            price_in  = Decimal(str(pricing.get("input",  0))) / Decimal("1000000")
            price_out = Decimal(str(pricing.get("output", 0))) / Decimal("1000000")
            return str(price_in), str(price_out)
    except Exception:
        pass
    return "0", "0"


# ─────────────────────────────────────────────────────────────────────────────
# Conversion: ORM agent → AgentConfig
# ─────────────────────────────────────────────────────────────────────────────

def agent_to_config(agent) -> AgentConfig:
    return AgentConfig(
        id=str(agent.id),
        slug=agent.slug,
        name=agent.display_name or agent.slug,
        description=agent.description or "",
        transport=agent.transport or "omni_ws",
        endpoint_url=getattr(agent, "endpoint_url", None),
        auth_token_encrypted=getattr(agent, "auth_token_encrypted", None),
        timeout_seconds=int(agent.timeout_seconds or 30),
        max_concurrency=int(agent.max_concurrency or 1),
        max_retries=max(1, int(getattr(agent, "max_retries", 2) or 2)),
        input_schema=agent.input_schema,
        skills=agent.skills,
    )


def orch_to_config(orch, price_in: str, price_out: str) -> OrchestratorConfig:
    return OrchestratorConfig(
        id=str(orch.id),
        name=orch.name,
        display_name=getattr(orch, "display_name", ""),
        system_prompt=getattr(orch, "system_prompt", "") or "",
        llm_provider=getattr(orch, "llm_provider", "anthropic") or "anthropic",
        llm_model=orch.llm_model or "",
        llm_api_key_encrypted=orch.llm_api_key_encrypted,
        llm_base_url=orch.llm_base_url,
        max_iterations=orch.max_iterations,
        max_parallel_tools=orch.max_parallel_tools,
        rate_limit_rpm=orch.rate_limit_rpm,
        daily_budget_usd=str(getattr(orch, "daily_budget_usd", "0")),
        a2a_exposed=getattr(orch, "a2a_exposed", False),
        memory_enabled=getattr(orch, "memory_enabled", False),
        summarize_every_n_calls=getattr(orch, "summarize_every_n_calls", 3),
        memory_raw_fallback_n=getattr(orch, "memory_raw_fallback_n", 5),
        summarizer_provider=getattr(orch, "summarizer_provider", None),
        summarizer_model=getattr(orch, "summarizer_model", None),
        summarizer_api_key_encrypted=getattr(orch, "summarizer_api_key_encrypted", None),
        history_window=getattr(orch, "history_window", 20),
        budget_tokens=getattr(orch, "budget_tokens", None),
        price_in=price_in,
        price_out=price_out,
    )
