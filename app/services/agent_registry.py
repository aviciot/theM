"""
Agent registry with two-level cache:
  L1: in-process dict (per replica, cleared on pub/sub signal)
  L2: Redis them:agents:registry (TTL 600s, shared across replicas)

Pub/sub channel them:agents:changed triggers invalidation on all replicas.
"""

import asyncio
import json
from typing import Optional

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_redis
from app.models import Agent
from app.utils.logger import logger

_REGISTRY_KEY = "them:agents:registry"
_CHANGE_CHANNEL = "them:agents:changed"
_TTL = 600

# L1 in-process cache: None means "not loaded yet"
_l1_cache: Optional[list[dict]] = None
_l1_lock = asyncio.Lock()


def _agent_to_dict(agent: Agent) -> dict:
    return {
        "id": str(agent.id),
        "slug": agent.slug,
        "display_name": agent.display_name,
        "description": agent.description,
        "transport": agent.transport,
        "endpoint_url": agent.endpoint_url,
        "auth_token_encrypted": agent.auth_token_encrypted,
        "input_schema": agent.input_schema or {},
        "timeout_seconds": agent.timeout_seconds,
        "max_concurrency": agent.max_concurrency,
        "enabled": agent.enabled,
        "tags": list(agent.tags or []),
        "skills": list(agent.skills or []),
        "supports_streaming": agent.supports_streaming,
        "supports_push": agent.supports_push,
    }


async def _load_from_db(db: AsyncSession) -> list[dict]:
    result = await db.execute(
        select(Agent).where(Agent.enabled == True).order_by(Agent.slug)
    )
    agents = result.scalars().all()
    return [_agent_to_dict(a) for a in agents]


async def get_enabled_agents(db: AsyncSession) -> list[dict]:
    """Return enabled agents from L1 → L2 → DB."""
    global _l1_cache

    if _l1_cache is not None:
        return _l1_cache

    async with _l1_lock:
        if _l1_cache is not None:
            return _l1_cache

        try:
            redis = await get_redis()
            cached = await redis.get(_REGISTRY_KEY)
            if cached:
                _l1_cache = json.loads(cached)
                logger.debug("agent_registry: loaded from Redis L2")
                return _l1_cache
        except Exception as exc:
            logger.warning("agent_registry: Redis L2 miss", error=str(exc))

        agents = await _load_from_db(db)
        _l1_cache = agents

        try:
            redis = await get_redis()
            await redis.setex(_REGISTRY_KEY, _TTL, json.dumps(agents))
        except Exception as exc:
            logger.warning("agent_registry: failed to write Redis L2", error=str(exc))

        logger.info("agent_registry: loaded from DB", count=len(agents))
        return _l1_cache


async def invalidate_registry() -> None:
    """Clear L1 and L2 caches. Called after any agent write."""
    global _l1_cache
    _l1_cache = None
    try:
        redis = await get_redis()
        await redis.delete(_REGISTRY_KEY)
        await redis.publish(_CHANGE_CHANNEL, "changed")
    except Exception as exc:
        logger.warning("agent_registry: failed to invalidate Redis", error=str(exc))
    logger.info("agent_registry: cache invalidated")


async def start_change_listener() -> None:
    """Background task: subscribe to them:agents:changed and clear L1 on signal."""
    global _l1_cache
    try:
        redis = await get_redis()
        pubsub = redis.pubsub()
        await pubsub.subscribe(_CHANGE_CHANNEL)
        async for message in pubsub.listen():
            if message["type"] == "message":
                _l1_cache = None
                logger.info("agent_registry: L1 cleared via pub/sub")
    except asyncio.CancelledError:
        pass
    except Exception as exc:
        logger.error("agent_registry: change listener error", error=str(exc))
