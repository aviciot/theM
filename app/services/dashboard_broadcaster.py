"""
Dashboard broadcaster — publishes events to Redis pub/sub odin:dash:{channel}.
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

_DASH_PREFIX = "odin:dash:"


async def publish(channel: str, event: dict[str, Any]) -> None:
    """Publish an event dict to odin:dash:{channel}."""
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
