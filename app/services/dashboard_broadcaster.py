"""
Dashboard broadcaster — publishes events to Redis pub/sub them:dash:{channel}.
Subscribers (ws_dashboard.py) relay events to connected dashboard WS clients.

Channels:
  runs      — run lifecycle events (started, completed, failed, step)
  agents    — agent registry changes
  metrics   — periodic aggregate metrics
"""

import json
from typing import Any

import app.database as db_module
from app.utils.logger import logger

_DASH_PREFIX = "them:dash:"
_SCAN_STATE_PREFIX = "them:scan:state:"
_SCAN_STATE_TTL = 300  # 5 minutes


async def _set_scan_state(agent_id: str, state: dict[str, Any]) -> None:
    """Overwrite the scan state Hash for agent_id. Best-effort."""
    if db_module.redis_client is None:
        return
    try:
        key = f"{_SCAN_STATE_PREFIX}{agent_id}"
        await db_module.redis_client.hset(key, mapping={k: json.dumps(v) if not isinstance(v, str) else v for k, v in state.items()})
        await db_module.redis_client.expire(key, _SCAN_STATE_TTL)
    except Exception as exc:
        logger.warning("scan state write failed", agent_id=agent_id, error=str(exc))


async def get_scan_state(agent_id: str) -> dict[str, Any] | None:
    """Read current scan state Hash. Returns None if no active scan."""
    if db_module.redis_client is None:
        return None
    try:
        key = f"{_SCAN_STATE_PREFIX}{agent_id}"
        raw = await db_module.redis_client.hgetall(key)
        if not raw:
            return None
        return {k: _try_json(v) for k, v in raw.items()}
    except Exception:
        return None


def _try_json(v: str) -> Any:
    try:
        return json.loads(v)
    except Exception:
        return v


async def publish(channel: str, event: dict[str, Any]) -> None:
    """Publish an event dict to them:dash:{channel}."""
    if db_module.redis_client is None:
        return
    try:
        await db_module.redis_client.publish(
            f"{_DASH_PREFIX}{channel}", json.dumps(event)
        )
    except Exception as exc:
        logger.warning("dashboard_broadcaster publish failed",
                       channel=channel, error=str(exc))


async def publish_run_started(run_id: str, orchestrator: str, user_id: int, goal: str) -> None:
    await publish("runs", {
        "type": "run_started",
        "run_id": run_id,
        "orchestrator": orchestrator,
        "user_id": user_id,
        "goal": goal[:200],
    })


async def publish_run_completed(run_id: str, status: str, iterations: int, cost_usd: str) -> None:
    await publish("runs", {
        "type": "run_completed",
        "run_id": run_id,
        "status": status,
        "iterations": iterations,
        "cost_usd": cost_usd,
    })


async def publish_run_step(run_id: str, agent_slug: str, iteration: int, status: str) -> None:
    await publish("runs", {
        "type": "run_step",
        "run_id": run_id,
        "agent": agent_slug,
        "iteration": iteration,
        "status": status,
    })


async def publish_agents_changed() -> None:
    await publish("agents", {"type": "agents_changed"})


async def publish_scan_started(agent_id: str) -> None:
    event = {"type": "scan_started", "agent_id": agent_id}
    await _set_scan_state(agent_id, event)
    await publish(f"agent:{agent_id}", event)


async def publish_scan_step(agent_id: str, step: str) -> None:
    event = {"type": "scan_step", "agent_id": agent_id, "step": step}
    await _set_scan_state(agent_id, event)
    await publish(f"agent:{agent_id}", event)


async def publish_scan_complete(agent_id: str, result: dict) -> None:
    event = {
        "type": "scan_complete",
        "agent_id": agent_id,
        "score": result["score"],
        "risk": result["risk"],
        "summary": result["summary"],
        "findings": result["findings"],
        "http_probes": result.get("http_probes", {}),
        "scanned_at": result.get("scanned_at", ""),
    }
    await _set_scan_state(agent_id, event)
    await publish(f"agent:{agent_id}", event)
    # expire the state key shortly after completion — result is persisted in DB
    if db_module.redis_client is not None:
        try:
            await db_module.redis_client.expire(f"{_SCAN_STATE_PREFIX}{agent_id}", 30)
        except Exception:
            pass


async def publish_scan_failed(agent_id: str, error: str) -> None:
    event = {"type": "scan_failed", "agent_id": agent_id, "error": error}
    await _set_scan_state(agent_id, event)
    await publish(f"agent:{agent_id}", event)
    if db_module.redis_client is not None:
        try:
            await db_module.redis_client.expire(f"{_SCAN_STATE_PREFIX}{agent_id}", 30)
        except Exception:
            pass


_APP_STATUS_CACHE_KEY = "them:dash:app_status_cache"


async def publish_app_status(statuses: dict[str, dict]) -> None:
    """Publish liveness probe results for all apps to them:dash:apps.

    statuses: {slug: {"reachable": bool, "latency_ms": int | None}}
    Also caches the latest statuses in Redis so new WS subscribers get them immediately.
    """
    import json as _json
    import app.database as _db
    if _db.redis_client is not None:
        try:
            await _db.redis_client.set(_APP_STATUS_CACHE_KEY, _json.dumps(statuses), ex=120)
        except Exception:
            pass
    await publish("apps", {"type": "app_status", "statuses": statuses})


async def get_cached_app_status() -> dict | None:
    """Return last known app statuses from Redis cache, or None if not yet available."""
    import json as _json
    import app.database as _db
    if _db.redis_client is None:
        return None
    try:
        raw = await _db.redis_client.get(_APP_STATUS_CACHE_KEY)
        return _json.loads(raw) if raw else None
    except Exception:
        return None
