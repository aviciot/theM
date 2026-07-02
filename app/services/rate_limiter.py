"""
Rate limiter — Redis INCR fixed-window per user per hour slot.
Key: rl:odin:{user_id}:{hour_slot}  TTL 7200s
Replica-safe: all replicas share the same Redis counter.
"""

import time
from typing import Optional

import app.database as db_module
from app.utils.logger import logger

_KEY_PREFIX = "rl:odin:"
_WINDOW_SECONDS = 3600
_KEY_TTL = 7200  # 2x window so key outlives the slot


def _slot() -> int:
    """Current hour slot (unix epoch // 3600)."""
    return int(time.time()) // _WINDOW_SECONDS


async def check_rate_limit(user_id: int, limit_rpm: int) -> tuple[bool, int]:
    """
    Check and increment rate limit counter.
    Returns (allowed: bool, current_count: int).
    limit_rpm is per-orchestrator requests-per-minute converted to per-hour internally.
    Pass limit_rpm=0 to disable rate limiting.
    """
    if limit_rpm <= 0:
        return True, 0

    limit_per_hour = limit_rpm * 60

    if db_module.redis_client is None:
        logger.warning("rate_limiter: Redis not available — allowing request")
        return True, 0

    key = f"{_KEY_PREFIX}{user_id}:{_slot()}"
    try:
        count = await db_module.redis_client.incr(key)
        if count == 1:
            await db_module.redis_client.expire(key, _KEY_TTL)
        allowed = count <= limit_per_hour
        if not allowed:
            logger.warning(
                "rate_limit_exceeded",
                user_id=user_id,
                count=count,
                limit=limit_per_hour,
            )
        return allowed, count
    except Exception as exc:
        logger.error("rate_limiter: Redis error — allowing request", error=str(exc))
        return True, 0


async def get_current_count(user_id: int) -> int:
    """Return current request count for this user in the current hour slot."""
    if db_module.redis_client is None:
        return 0
    key = f"{_KEY_PREFIX}{user_id}:{_slot()}"
    try:
        val = await db_module.redis_client.get(key)
        return int(val) if val else 0
    except Exception:
        return 0
