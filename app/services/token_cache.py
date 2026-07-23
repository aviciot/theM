"""
Access token cache — two levels:
  L1: in-process dict per replica (fast, not shared)
  L2: Redis them:session:token:{sha256(token)} TTL 300s (shared across replicas)

On miss: DB lookup via SQLAlchemy, then populate both caches.
On revoke: delete from DB + Redis; L1 expires naturally within TTL.

User-active check: on DB miss, verify user is still active in auth-service.
Result cached alongside token payload — deactivated users are rejected even
if their token is otherwise valid.
"""

import hashlib
import json
import time
from typing import Optional

from sqlalchemy import select, update
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.models import AccessToken
from app.utils.logger import logger

# Separate prefix for user-active cache to allow independent invalidation
_USER_ACTIVE_PREFIX = "them:session:user_active:"
_USER_ACTIVE_TTL = 60  # re-check auth-service every 60s

_TOKEN_PREFIX = "them:session:token:"
_TTL = 300

# L1: {token_hash: (payload_dict, expires_at_monotonic)}
_l1: dict[str, tuple[dict, float]] = {}


def _hash(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def _l1_get(token_hash: str) -> Optional[dict]:
    entry = _l1.get(token_hash)
    if entry and entry[1] > time.monotonic():
        return entry[0]
    _l1.pop(token_hash, None)
    return None


def _l1_set(token_hash: str, payload: dict) -> None:
    _l1[token_hash] = (payload, time.monotonic() + _TTL)


def _l1_delete(token_hash: str) -> None:
    _l1.pop(token_hash, None)


def _row_to_payload(row: AccessToken) -> dict:
    return {
        "token_id": str(row.id),
        "user_id": row.user_id,
        "label": row.label,
        "orchestrator_id": str(row.orchestrator_id) if row.orchestrator_id else None,
        "enabled": row.enabled,
        "expires_at": row.expires_at.isoformat() if row.expires_at else None,
    }


async def _l2_get(token_hash: str) -> Optional[dict]:
    try:
        if db_module.redis_client is None:
            return None
        raw = await db_module.redis_client.get(f"{_TOKEN_PREFIX}{token_hash}")
        if raw:
            return json.loads(raw)
    except Exception as exc:
        logger.warning("token_cache: L2 get failed", error=str(exc))
    return None


async def _l2_set(token_hash: str, payload: dict) -> None:
    try:
        if db_module.redis_client is None:
            return
        await db_module.redis_client.setex(
            f"{_TOKEN_PREFIX}{token_hash}", _TTL, json.dumps(payload)
        )
    except Exception as exc:
        logger.warning("token_cache: L2 set failed", error=str(exc))


async def _l2_delete(token_hash: str) -> None:
    try:
        if db_module.redis_client is None:
            return
        await db_module.redis_client.delete(f"{_TOKEN_PREFIX}{token_hash}")
    except Exception as exc:
        logger.warning("token_cache: L2 delete failed", error=str(exc))


async def _is_user_active(user_id: int) -> bool:
    """
    Check auth-service that user_id is still active.
    Result cached in Redis for _USER_ACTIVE_TTL seconds.
    Fails open (returns True) if auth-service is unreachable.
    """
    cache_key = f"{_USER_ACTIVE_PREFIX}{user_id}"
    try:
        if db_module.redis_client is not None:
            cached = await db_module.redis_client.get(cache_key)
            if cached is not None:
                return cached == b"1"
    except Exception as exc:
        logger.warning("token_cache: user_active cache get failed", error=str(exc))

    # Call auth-service
    try:
        from app.services.auth_client import get_user
        user = await get_user(user_id)
        if user is None:
            # Auth-service returned non-200 (404, 500, wrong URL, etc.) — fail open
            logger.warning("token_cache: get_user returned None — failing open", user_id=user_id)
            return True
        active = bool(user.get("active", False))
    except Exception as exc:
        logger.warning("token_cache: auth_client.get_user failed — failing open", error=str(exc))
        return True  # fail open: don't block if auth-service is down

    try:
        if db_module.redis_client is not None:
            await db_module.redis_client.setex(cache_key, _USER_ACTIVE_TTL, b"1" if active else b"0")
    except Exception as exc:
        logger.warning("token_cache: user_active cache set failed", error=str(exc))

    return active


async def validate_bearer_token(token: str, db: AsyncSession) -> Optional[dict]:
    """
    Validate a bearer token. Returns user/token payload or None if invalid.
    L1 → L2 → DB. On DB hit, also checks user is still active in auth-service.
    Updates last_used_at on DB hit.
    """
    token_hash = _hash(token)

    # L1
    payload = _l1_get(token_hash)
    if payload:
        return payload if payload["enabled"] else None

    # L2
    payload = await _l2_get(token_hash)
    if payload:
        _l1_set(token_hash, payload)
        return payload if payload["enabled"] else None

    # DB
    result = await db.execute(
        select(AccessToken).where(AccessToken.token_hash == token_hash)
    )
    row = result.scalar_one_or_none()
    if row is None or not row.enabled:
        return None

    # Verify user is still active in auth-service
    if not await _is_user_active(row.user_id):
        logger.warning("token_cache: token rejected — user deactivated", user_id=row.user_id)
        return None

    # Update last_used_at (fire-and-forget style — don't block on it)
    try:
        from sqlalchemy import func
        await db.execute(
            update(AccessToken)
            .where(AccessToken.id == row.id)
            .values(last_used_at=func.now())
        )
        await db.commit()
    except Exception as exc:
        logger.warning("token_cache: last_used_at update failed", error=str(exc))

    payload = _row_to_payload(row)
    _l1_set(token_hash, payload)
    await _l2_set(token_hash, payload)
    return payload


async def invalidate_token(token_hash: str) -> None:
    """Remove token from both caches. Call after DB delete/disable."""
    _l1_delete(token_hash)
    await _l2_delete(token_hash)
    # Publish cross-pod eviction so Go replicas drop their L1 entry immediately.
    try:
        if db_module.redis_client is not None:
            await db_module.redis_client.publish("them:token:revoked", token_hash)
    except Exception as exc:
        logger.warning("token_cache: revocation publish failed", error=str(exc))


async def invalidate_user_active(user_id: int) -> None:
    """Remove user-active cache entry. Call when a user is deactivated."""
    try:
        if db_module.redis_client is not None:
            await db_module.redis_client.delete(f"{_USER_ACTIVE_PREFIX}{user_id}")
    except Exception as exc:
        logger.warning("token_cache: user_active invalidate failed", error=str(exc))
