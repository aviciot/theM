"""
WebSocket endpoint: /ws/orchestrate/{name}

Auth: opaque Bearer token (them.access_tokens) OR admin JWT.
Protocol:
  Server → Client: {"type": "ready",      "run_id": "...", "task_id": "..."}
  Client → Server: {"content": "user goal text"}
  Server → Client: {"type": "token",      "text": "..."}
  Server → Client: {"type": "tool_start", "tool": "...", "input": {...}}
  Server → Client: {"type": "tool_done",  "tool": "...", "latency_ms": N}
  Server → Client: {"type": "done",       "run_id": "...", "task_id": "...", "iterations": N}
  Server → Client: {"type": "error",      "message": "..."}

Phase 8.6: WS is now a thin shell over WebsocketEdge.
The orchestrator's edges list must include "websocket" or the connection is
rejected immediately after auth with a clear error message.
"""

import json
import uuid

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

import app.database as _db
from app.edges.registry import get_edge_class
from app.edges.websocket_edge import WebsocketEdge
from app.services.auth_client import validate_jwt
from app.services.task_runner import run as task_runner_run
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

router = APIRouter()


def _parse_bearer(websocket: WebSocket) -> str | None:
    auth = websocket.headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        return auth[7:].strip()
    return websocket.query_params.get("token")


async def _resolve_auth(raw_token: str, db) -> dict | None:
    payload = await validate_bearer_token(raw_token, db)
    if payload is not None:
        return payload
    jwt_payload = await validate_jwt(raw_token)
    if jwt_payload and jwt_payload.get("role") in ("admin", "super_admin"):
        return {
            "user_id": jwt_payload.get("user_id", 0),
            "orchestrator_id": None,
        }
    return None


def _orchestrator_allows_edge(orchestrator_name: str, edge_name: str) -> bool:
    """
    Check whether the named orchestrator has 'edge_name' in its edges list.
    Falls back to allowing 'websocket' when the column is absent (pre-8.6 rows).
    Loads from Redis cache / DB via a quick synchronous-style check inside the
    async context — we do this lazily after auth so the rejection is cheap.
    """
    # Delegate the actual check to task_runner's cache; handled inline in the
    # endpoint below where we have a DB session available.
    return True  # sentinel — real check done in the endpoint


@router.websocket("/ws/orchestrate/{name}")
async def ws_orchestrate(name: str, websocket: WebSocket):
    await websocket.accept()

    # ── Auth ──────────────────────────────────────────────────────────────
    raw_token = _parse_bearer(websocket)
    if not raw_token:
        await websocket.send_json({"type": "error", "message": "Authorization required"})
        await websocket.close(code=4001)
        return

    if _db.AsyncSessionLocal is None:
        await websocket.send_json({"type": "error", "message": "Service not ready"})
        await websocket.close(code=4003)
        return

    async with _db.AsyncSessionLocal() as db:
        token_payload = await _resolve_auth(raw_token, db)

    if token_payload is None:
        await websocket.send_json({"type": "error", "message": "Invalid or disabled token"})
        await websocket.close(code=4001)
        return

    user_id = token_payload["user_id"]
    logger.info("ws_orchestrate connected", orchestrator=name, user_id=user_id)

    # ── Edge guard — reject if orchestrator does not allow websocket ───────
    async with _db.AsyncSessionLocal() as db:
        from sqlalchemy import select
        from app.models import Orchestrator
        orch_result = await db.execute(
            select(Orchestrator.edges).where(
                Orchestrator.name == name,
                Orchestrator.enabled == True,
            )
        )
        edges_row = orch_result.scalar_one_or_none()

    # edges_row is None if orch not found (task_runner will give the proper error);
    # None also means the column doesn't exist yet (pre-8.6) — allow through.
    if edges_row is not None:
        allowed_edges = edges_row if isinstance(edges_row, list) else list(edges_row)
        if "websocket" not in allowed_edges:
            await websocket.send_json({
                "type": "error",
                "message": f"Orchestrator '{name}' does not allow the websocket edge",
            })
            await websocket.close(code=4003)
            return

    # ── Receive user message ──────────────────────────────────────────────
    try:
        raw = await websocket.receive_text()
    except WebSocketDisconnect:
        return

    try:
        msg = json.loads(raw)
        user_message = msg.get("content", "").strip()
        client_context_id = msg.get("context_id")
    except (json.JSONDecodeError, AttributeError):
        user_message = raw.strip()
        client_context_id = None

    if not user_message:
        await websocket.send_json({"type": "error", "message": "Empty message"})
        await websocket.close(code=4000)
        return

    session_id = uuid.uuid4()
    # Reuse context_id supplied by client so memory carries across turns
    try:
        context_id = uuid.UUID(client_context_id) if client_context_id else uuid.uuid4()
    except (ValueError, AttributeError):
        context_id = uuid.uuid4()

    # ── Instantiate the WebsocketEdge and relay runner events ─────────────
    edge = WebsocketEdge(websocket, orchestrator_name=name, user_id=user_id)

    try:
        async with _db.AsyncSessionLocal() as db:
            async for event in task_runner_run(
                orchestrator_name=name,
                user_message=user_message,
                user_id=user_id,
                token_payload=token_payload,
                db=db,
                session_id=session_id,
                context_id=context_id,
            ):
                try:
                    await edge.emit(event)
                except WebSocketDisconnect:
                    logger.info(
                        "ws_orchestrate client disconnected mid-run",
                        orchestrator=name,
                        user_id=user_id,
                    )
                    return

    except WebSocketDisconnect:
        logger.info("ws_orchestrate disconnected", orchestrator=name, user_id=user_id)
    except Exception as exc:
        logger.error("ws_orchestrate unhandled error", orchestrator=name, error=str(exc))
        try:
            await edge.emit({"type": "error", "message": "Internal server error"})
        except Exception:
            pass
    finally:
        await edge.close()
