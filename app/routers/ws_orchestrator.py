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

import asyncio
import json
import uuid

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

import app.database as _db
from app.edges.registry import get_edge_class
from app.edges.websocket_edge import WebsocketEdge
from app.services.auth_client import validate_jwt
from app.services.session_manager import end as session_end
from app.services.session_manager import register as session_register
from app.services.task_runner import run as task_runner_run
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

# Phase 7: always route through Temporal (TEMPORAL_ENABLED=true is now the default)
_TEMPORAL_ENABLED = True

from app.config import settings as _settings

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
            select(Orchestrator.edges, Orchestrator.history_window).where(
                Orchestrator.name == name,
                Orchestrator.enabled == True,
            )
        )
        orch_row = orch_result.one_or_none()
        edges_row = orch_row[0] if orch_row is not None else None
        history_window = int(orch_row[1]) if orch_row is not None and orch_row[1] is not None else 20

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

    await session_register(
        session_id=session_id,
        instance_id=_settings.instance_id,
        user_id=user_id,
        orchestrator_name=name,
        context_id=context_id,
        ep_slug=None,   # direct /ws/orchestrate — no entry point
        app_id=None,
    )
    try:
        await _run_temporal(
            name, user_message, user_id, token_payload,
            context_id, session_id, edge, websocket, history_window,
        )
    except WebSocketDisconnect:
        logger.info("ws_orchestrate disconnected", orchestrator=name, user_id=user_id)
    except Exception as exc:
        logger.error("ws_orchestrate unhandled error", orchestrator=name, error=str(exc))
        try:
            await edge.emit({"type": "error", "message": "Internal server error"})
        except Exception:
            pass
    finally:
        await session_end(session_id, ep_slug=None, app_id=None)
        await edge.close()


async def _run_temporal(
    name: str, user_message: str, user_id: int, token_payload: dict,
    context_id: uuid.UUID, session_id: uuid.UUID, edge, websocket: WebSocket,
    history_window: int = 20,
) -> None:
    """Temporal OrchestrationWorkflow path — used when TEMPORAL_ENABLED=true."""
    from app.temporal.bridge_client import (
        cancel_workflow,
        start_orchestration_workflow,
        stream_run_events,
    )

    try:
        workflow_handle, workflow_id, pubsub = await start_orchestration_workflow(
            orchestrator_name=name,
            user_message=user_message,
            user_id=user_id,
            token_payload=token_payload,
            context_id=context_id,
            session_id=session_id,
            history_window=history_window,
        )
    except Exception as exc:
        from app.temporal.bridge_client import DeadContextError
        is_dead = isinstance(exc, DeadContextError)
        await edge.emit({
            "type": "error",
            "message": str(exc) if is_dead else f"Failed to start workflow: {exc}",
            "context_id": None,
        })
        return

    cancel_event = asyncio.Event()
    active_run_id: list[str | None] = [None]

    async def _capture_run_id(event: dict) -> None:
        if event.get("type") == "ready" and event.get("run_id"):
            active_run_id[0] = event["run_id"]
        await edge.emit(event)

    async def _cancel_listener():
        while True:
            try:
                raw = await websocket.receive_text()
                msg = json.loads(raw)
                if msg.get("type") == "cancel":
                    cancel_event.set()
                    return True
            except (json.JSONDecodeError, AttributeError):
                continue
            except (WebSocketDisconnect, Exception):
                # Client disconnected without sending cancel — treat as implicit
                # cancel so the Temporal workflow doesn't keep burning tokens.
                # cancel_workflow() is idempotent and safe on completed workflows.
                cancel_event.set()
                return True

    stream_task = asyncio.ensure_future(
        stream_run_events(
            context_id=str(context_id),
            workflow_handle=workflow_handle,
            emit_fn=_capture_run_id,
            cancel_event=cancel_event,
            pubsub=pubsub,
        )
    )
    cancel_task = asyncio.ensure_future(_cancel_listener())

    done, pending = await asyncio.wait(
        [stream_task, cancel_task],
        return_when=asyncio.FIRST_COMPLETED,
    )

    for t in pending:
        t.cancel()
        try:
            await t
        except (asyncio.CancelledError, Exception):
            pass

    if cancel_task in done and cancel_task.result() is True:
        logger.info("ws_orchestrate (temporal) canceled by client", orchestrator=name, user_id=user_id)
        await cancel_workflow(workflow_id)
        try:
            await edge.emit({"type": "canceled"})
        except Exception:
            pass
