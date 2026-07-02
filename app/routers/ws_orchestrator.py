"""
WebSocket endpoint: /ws/orchestrate/{name}

Auth: opaque Bearer token (odin.access_tokens) validated via token_cache.
Protocol:
  Server → Client: {"type": "ready",      "run_id": "..."}
  Client → Server: {"content": "user goal text"}
  Server → Client: {"type": "token",      "text": "..."}          streaming LLM tokens
  Server → Client: {"type": "tool_start", "tool": "...", "input": {...}}
  Server → Client: {"type": "tool_done",  "tool": "...", "latency_ms": N}
  Server → Client: {"type": "done",       "run_id": "...", "iterations": N}
  Server → Client: {"type": "error",      "message": "..."}
"""

import json
import uuid

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db, AsyncSessionLocal
from app.services.orchestrator_service import run_orchestrator
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

router = APIRouter()


def _parse_bearer(websocket: WebSocket) -> str | None:
    """Extract Bearer token from Authorization header."""
    auth = websocket.headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        return auth[7:].strip()
    # Also check query param for clients that can't set headers
    return websocket.query_params.get("token")


@router.websocket("/ws/orchestrate/{name}")
async def ws_orchestrate(name: str, websocket: WebSocket):
    await websocket.accept()

    # ── Auth ──────────────────────────────────────────────────────────
    raw_token = _parse_bearer(websocket)
    if not raw_token:
        await websocket.send_json({"type": "error", "message": "Authorization required"})
        await websocket.close(code=4001)
        return

    if AsyncSessionLocal is None:
        await websocket.send_json({"type": "error", "message": "Service not ready"})
        await websocket.close(code=4003)
        return

    async with AsyncSessionLocal() as db:
        token_payload = await validate_bearer_token(raw_token, db)

    if token_payload is None:
        await websocket.send_json({"type": "error", "message": "Invalid or disabled token"})
        await websocket.close(code=4001)
        return

    user_id = token_payload["user_id"]
    logger.info("ws_orchestrate connected", orchestrator=name, user_id=user_id)

    # ── Wait for user message ─────────────────────────────────────────
    try:
        raw = await websocket.receive_text()
    except WebSocketDisconnect:
        return

    try:
        msg = json.loads(raw)
        user_message = msg.get("content", "").strip()
    except (json.JSONDecodeError, AttributeError):
        user_message = raw.strip()

    if not user_message:
        await websocket.send_json({"type": "error", "message": "Empty message"})
        await websocket.close(code=4000)
        return

    session_id = uuid.uuid4()

    # ── Agentic loop ──────────────────────────────────────────────────
    try:
        async with AsyncSessionLocal() as db:
            async for event in run_orchestrator(
                orchestrator_name=name,
                user_message=user_message,
                user_id=user_id,
                token_payload=token_payload,
                db=db,
                session_id=session_id,
            ):
                try:
                    await websocket.send_json(event)
                except WebSocketDisconnect:
                    logger.info("ws_orchestrate client disconnected mid-run",
                                orchestrator=name, user_id=user_id)
                    return

    except WebSocketDisconnect:
        logger.info("ws_orchestrate disconnected", orchestrator=name, user_id=user_id)
    except Exception as exc:
        logger.error("ws_orchestrate unhandled error", orchestrator=name, error=str(exc))
        try:
            await websocket.send_json({"type": "error", "message": "Internal server error"})
        except Exception:
            pass
    finally:
        try:
            await websocket.close()
        except Exception:
            pass
