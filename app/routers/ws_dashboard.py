"""
WebSocket endpoint: /ws/dashboard

Auth: JWT (admin dashboard users).
Multiplexed named channels — client subscribes to one or more.

Protocol:
  Client → Server (after connect):
    {"type": "subscribe", "channels": ["runs", "agents", "metrics"]}

  Server → Client (subscribed events):
    {"channel": "runs",    "event": {...}}
    {"channel": "agents",  "event": {...}}
    {"channel": "metrics", "event": {...}}

  Server → Client (control):
    {"type": "subscribed", "channels": [...]}
    {"type": "error",      "message": "..."}
    {"type": "ping"}                           — sent every 30s as keepalive
"""

import asyncio
import json
from typing import Optional

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

import app.database as db_module
from app.services.auth_client import validate_jwt
from app.utils.logger import logger

router = APIRouter()

_DASH_PREFIX = "odin:dash:"
_VALID_CHANNELS = {"runs", "agents", "metrics"}
_PING_INTERVAL = 30


def _parse_bearer(ws: WebSocket) -> Optional[str]:
    auth = ws.headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        return auth[7:].strip()
    return ws.query_params.get("token")


async def _listen_channels(
    websocket: WebSocket,
    channels: list[str],
) -> None:
    """Subscribe to Redis pub/sub channels and relay events to the WS client."""
    if db_module.redis_client is None:
        await websocket.send_json({"type": "error", "message": "Redis unavailable"})
        return

    pubsub = db_module.redis_client.pubsub()
    redis_channels = [f"{_DASH_PREFIX}{ch}" for ch in channels]
    await pubsub.subscribe(*redis_channels)

    ping_task = asyncio.create_task(_ping_loop(websocket))

    try:
        async for message in pubsub.listen():
            if message["type"] != "message":
                continue
            raw_channel = message["channel"]
            channel = raw_channel.removeprefix(_DASH_PREFIX)
            try:
                event = json.loads(message["data"])
            except Exception:
                continue
            try:
                await websocket.send_json({"channel": channel, "event": event})
            except WebSocketDisconnect:
                break
            except Exception:
                break
    except asyncio.CancelledError:
        pass
    finally:
        ping_task.cancel()
        await pubsub.unsubscribe(*redis_channels)
        try:
            await pubsub.aclose()
        except Exception:
            pass


async def _ping_loop(websocket: WebSocket) -> None:
    """Send a ping every 30s to keep the connection alive."""
    try:
        while True:
            await asyncio.sleep(_PING_INTERVAL)
            await websocket.send_json({"type": "ping"})
    except (WebSocketDisconnect, asyncio.CancelledError):
        pass
    except Exception:
        pass


@router.websocket("/ws/dashboard")
async def ws_dashboard(websocket: WebSocket):
    await websocket.accept()

    # ── Auth ──────────────────────────────────────────────────────────
    raw_token = _parse_bearer(websocket)
    if not raw_token:
        await websocket.send_json({"type": "error", "message": "Authorization required"})
        await websocket.close(code=4001)
        return

    user = await validate_jwt(raw_token)
    if user is None:
        await websocket.send_json({"type": "error", "message": "Invalid or expired JWT"})
        await websocket.close(code=4001)
        return

    logger.info("ws_dashboard connected", user_id=user.get("user_id"))

    # ── Wait for subscribe message ────────────────────────────────────
    try:
        raw = await asyncio.wait_for(websocket.receive_text(), timeout=10.0)
        msg = json.loads(raw)
    except asyncio.TimeoutError:
        await websocket.send_json({"type": "error", "message": "Subscribe timeout"})
        await websocket.close(code=4000)
        return
    except (WebSocketDisconnect, json.JSONDecodeError):
        await websocket.close()
        return

    if msg.get("type") != "subscribe":
        await websocket.send_json({"type": "error", "message": "Expected subscribe message"})
        await websocket.close(code=4000)
        return

    requested = set(msg.get("channels", []))
    channels = list(requested & _VALID_CHANNELS)
    if not channels:
        await websocket.send_json({"type": "error", "message": f"No valid channels. Valid: {sorted(_VALID_CHANNELS)}"})
        await websocket.close(code=4000)
        return

    await websocket.send_json({"type": "subscribed", "channels": channels})
    logger.info("ws_dashboard subscribed", user_id=user.get("user_id"), channels=channels)

    # ── Relay loop ────────────────────────────────────────────────────
    try:
        await _listen_channels(websocket, channels)
    except WebSocketDisconnect:
        pass
    except Exception as exc:
        logger.error("ws_dashboard error", error=str(exc))
    finally:
        try:
            await websocket.close()
        except Exception:
            pass
        logger.info("ws_dashboard disconnected", user_id=user.get("user_id"))
