"""
Session Manager — tracks active WS sessions in Redis.

Each session is stored as a Redis Hash keyed by session_id.
Two index Sets let you enumerate sessions per entry-point and per application.

Redis keys (all under them: namespace):
  them:sess:{session_id}          Hash  TTL 90s (heartbeat-refreshed)
  them:ep:{ep_slug}:sessions      Set   (no TTL — managed by register/end)
  them:app:{app_id}:sessions      Set   (no TTL — managed by register/end)
  them:pod:{pod_id}               Hash  TTL 30s (written by heartbeat loop in main.py)
  them:pods                       Set   (no TTL — pod membership)

Extension points (add here when needed):
  - Concurrency limits: check SCARD before SADD in register()
  - Idle timeout: compare last_active in a sweep loop
  - Cross-replica disconnect: subscribe them:sess:control pub/sub channel
  - Metrics: SCARD queries against the index sets
"""

import json
import uuid
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Optional

import app.database as db_module
from app.utils.logger import logger

_SESS_PREFIX  = "them:sess:"
_EP_PREFIX    = "them:ep:"
_APP_PREFIX   = "them:app:"
_POD_PREFIX   = "them:pod:"
_PODS_KEY     = "them:pods"

_SESS_TTL     = 90   # seconds — refreshed by touch(); expires if pod dies
_POD_TTL      = 30   # seconds — refreshed by heartbeat loop every 15s


@dataclass
class SessionInfo:
    session_id: str
    instance_id: str
    user_id: int
    orchestrator_name: str
    ep_slug: Optional[str]
    app_id: Optional[str]
    context_id: str
    started_at: str  # ISO8601


# ── Public API ────────────────────────────────────────────────────────────────

async def register(
    session_id: uuid.UUID,
    instance_id: str,
    user_id: int,
    orchestrator_name: str,
    context_id: uuid.UUID,
    ep_slug: Optional[str] = None,
    app_id: Optional[str] = None,
) -> None:
    """Register a new session. Best-effort — never raises."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        info = SessionInfo(
            session_id=str(session_id),
            instance_id=instance_id,
            user_id=user_id,
            orchestrator_name=orchestrator_name,
            ep_slug=ep_slug,
            app_id=app_id,
            context_id=str(context_id),
            started_at=datetime.now(timezone.utc).isoformat(),
        )
        sess_key = f"{_SESS_PREFIX}{session_id}"
        await redis.hset(sess_key, mapping={k: json.dumps(v) if not isinstance(v, str) else v
                                            for k, v in asdict(info).items()
                                            if v is not None})
        await redis.expire(sess_key, _SESS_TTL)

        if ep_slug:
            await redis.sadd(f"{_EP_PREFIX}{ep_slug}:sessions", str(session_id))
        if app_id:
            await redis.sadd(f"{_APP_PREFIX}{app_id}:sessions", str(session_id))

        logger.info("session registered", session_id=str(session_id),
                    ep_slug=ep_slug, app_id=app_id, user_id=user_id)

        if app_id:
            from app.services.dashboard_broadcaster import publish_session_event
            await publish_session_event(
                app_id, ep_slug or "", "session_start", str(session_id), asdict(info)
            )
    except Exception as exc:
        logger.warning("session_manager.register failed", session_id=str(session_id), error=str(exc))


async def end(
    session_id: uuid.UUID,
    ep_slug: Optional[str] = None,
    app_id: Optional[str] = None,
) -> None:
    """Deregister a session. Best-effort — never raises."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        await redis.delete(f"{_SESS_PREFIX}{session_id}")
        if ep_slug:
            await redis.srem(f"{_EP_PREFIX}{ep_slug}:sessions", str(session_id))
        if app_id:
            await redis.srem(f"{_APP_PREFIX}{app_id}:sessions", str(session_id))

        logger.info("session ended", session_id=str(session_id))

        if app_id:
            from app.services.dashboard_broadcaster import publish_session_event
            await publish_session_event(
                app_id, ep_slug or "", "session_end", str(session_id), {}
            )
    except Exception as exc:
        logger.warning("session_manager.end failed", session_id=str(session_id), error=str(exc))


async def touch(session_id: uuid.UUID) -> None:
    """Refresh the session TTL. Call periodically to keep alive."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        await redis.expire(f"{_SESS_PREFIX}{session_id}", _SESS_TTL)
    except Exception:
        pass


async def get(session_id: uuid.UUID) -> Optional[dict]:
    """Return session metadata dict, or None if not found."""
    redis = db_module.redis_client
    if redis is None:
        return None
    try:
        raw = await redis.hgetall(f"{_SESS_PREFIX}{session_id}")
        if not raw:
            return None
        return {k: _try_json(v) for k, v in raw.items()}
    except Exception:
        return None


async def list_ep_sessions(ep_slug: str) -> list[str]:
    """Return active session_ids for an entry point."""
    redis = db_module.redis_client
    if redis is None:
        return []
    try:
        members = await redis.smembers(f"{_EP_PREFIX}{ep_slug}:sessions")
        return [m.decode() if isinstance(m, bytes) else m for m in members]
    except Exception:
        return []


async def list_app_sessions(app_id: str) -> list[str]:
    """Return active session_ids for an application."""
    redis = db_module.redis_client
    if redis is None:
        return []
    try:
        members = await redis.smembers(f"{_APP_PREFIX}{app_id}:sessions")
        return [m.decode() if isinstance(m, bytes) else m for m in members]
    except Exception:
        return []


async def count_ep_sessions(ep_slug: str) -> int:
    """Return active session count for an entry point."""
    redis = db_module.redis_client
    if redis is None:
        return 0
    try:
        return await redis.scard(f"{_EP_PREFIX}{ep_slug}:sessions")
    except Exception:
        return 0


async def count_app_sessions(app_id: str) -> int:
    """Return active session count for an application."""
    redis = db_module.redis_client
    if redis is None:
        return 0
    try:
        return await redis.scard(f"{_APP_PREFIX}{app_id}:sessions")
    except Exception:
        return 0


_ACTIVE_AGENTS_SUFFIX = ":active"  # them:sess:{session_id}:active  — Redis Set, TTL matches session


async def set_active_agent(
    session_id: uuid.UUID,
    agent_slug: str,
    app_id: Optional[str],
) -> None:
    """Add agent_slug to the session's active-agents set. Best-effort."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        active_key = f"{_SESS_PREFIX}{session_id}{_ACTIVE_AGENTS_SUFFIX}"
        await redis.sadd(active_key, agent_slug)
        await redis.expire(active_key, _SESS_TTL)
        if app_id:
            slugs = await redis.smembers(active_key)
            active_list = [s.decode() if isinstance(s, bytes) else s for s in slugs]
            from app.services.dashboard_broadcaster import publish_session_event
            await publish_session_event(
                app_id, "", "session_update", str(session_id),
                {"active_agents": active_list},
            )
    except Exception:
        pass


async def clear_active_agent(
    session_id: uuid.UUID,
    app_id: Optional[str],
    agent_slug: Optional[str] = None,
) -> None:
    """Remove agent_slug from the session's active-agents set (or clear all). Best-effort."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        active_key = f"{_SESS_PREFIX}{session_id}{_ACTIVE_AGENTS_SUFFIX}"
        if agent_slug:
            await redis.srem(active_key, agent_slug)
        else:
            await redis.delete(active_key)
        if app_id:
            slugs = await redis.smembers(active_key)
            active_list = [s.decode() if isinstance(s, bytes) else s for s in slugs]
            from app.services.dashboard_broadcaster import publish_session_event
            await publish_session_event(
                app_id, "", "session_update", str(session_id),
                {"active_agents": active_list},
            )
    except Exception:
        pass


# ── Pod heartbeat (called from main.py lifespan loop) ─────────────────────────

async def write_pod_heartbeat(instance_id: str, session_count: int) -> None:
    """Write bridge pod liveness + load to Redis. Called every 15s."""
    redis = db_module.redis_client
    if redis is None:
        return
    try:
        await redis.hset(f"{_POD_PREFIX}{instance_id}", mapping={
            "instance_id": instance_id,
            "sessions": str(session_count),
            "ts": datetime.now(timezone.utc).isoformat(),
        })
        await redis.expire(f"{_POD_PREFIX}{instance_id}", _POD_TTL)
        await redis.sadd(_PODS_KEY, instance_id)
    except Exception as exc:
        logger.warning("session_manager.write_pod_heartbeat failed", error=str(exc))


# ── Internal ──────────────────────────────────────────────────────────────────

def _try_json(v):
    try:
        return json.loads(v)
    except Exception:
        return v
