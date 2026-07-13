"""
Pluggable application entry points — Phase 9 / Phase 10.

Routes:
  GET  /apps                          → list all enabled applications
  GET  /apps/{slug}                   → get one application by slug
  POST /apps/{slug}                   → fire-and-forget; returns task_id for polling
  GET  /apps/{slug}/tasks/{task_id}   → poll task state
  WS   /apps/{slug}/ws               → WebSocket streaming chat
  GET  /apps/{slug}/sse               → SSE streaming (text/event-stream)

Auth:
  - Bearer token (them.access_tokens) — same token validation as /ws/orchestrate
  - access_policy {"mode":"public"} → no auth required
  - access_policy {"mode":"token"}  → Bearer required for all methods

Design:
  Entry points are thin adapters. They load the Application row, verify auth,
  look up the bound orchestrator, then delegate to task_runner.run().
"""

import asyncio
import uuid
from datetime import datetime, timedelta, timezone
from typing import Optional

from fastapi import APIRouter, HTTPException, Request, WebSocket, WebSocketDisconnect
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

import app.database as db_module
from app.models import Application, Task
from app.services.auth_client import validate_jwt
from app.services import task_store
from app.services.task_runner import run as task_runner_run
from app.services.token_cache import validate_bearer_token
from app.edges.sse_edge import SSEEdge
from app.utils.logger import logger

# Temporal feature flag — read once at import time
def _temporal_enabled() -> bool:
    try:
        from app.config import Settings
        return Settings().TEMPORAL_ENABLED
    except Exception:
        return False

_TEMPORAL_ENABLED = _temporal_enabled()

router = APIRouter(tags=["apps"])

_DEFAULT_DEADLINE_MINUTES = 30


# ─────────────────────────────────────────────────────────────────────────────
# Auth helpers
# ─────────────────────────────────────────────────────────────────────────────

async def _resolve_bearer(request: Request) -> dict | None:
    auth = request.headers.get("authorization", "")
    if not auth.lower().startswith("bearer "):
        return None
    raw_token = auth[7:].strip()
    if db_module.AsyncSessionLocal is None:
        return None
    async with db_module.AsyncSessionLocal() as db:
        payload = await validate_bearer_token(raw_token, db)
        if payload is not None:
            expires_at_raw = payload.get("expires_at")
            if expires_at_raw:
                try:
                    if datetime.fromisoformat(expires_at_raw) < datetime.now(timezone.utc):
                        return None
                except (ValueError, TypeError):
                    pass
            return payload
        jwt_payload = await validate_jwt(raw_token)
        if jwt_payload and jwt_payload.get("role") in ("admin", "super_admin"):
            return {"user_id": jwt_payload.get("user_id", 0), "orchestrator_id": None, "expires_at": None}
    return None


async def _resolve_bearer_ws(websocket: WebSocket) -> dict | None:
    raw = websocket.query_params.get("token") or ""
    auth = websocket.headers.get("authorization", "")
    if not raw and auth.lower().startswith("bearer "):
        raw = auth[7:].strip()
    if not raw or db_module.AsyncSessionLocal is None:
        return None
    async with db_module.AsyncSessionLocal() as db:
        payload = await validate_bearer_token(raw, db)
        if payload is not None:
            expires_at_raw = payload.get("expires_at")
            if expires_at_raw:
                try:
                    if datetime.fromisoformat(expires_at_raw) < datetime.now(timezone.utc):
                        return None
                except (ValueError, TypeError):
                    pass
            return payload
        jwt_payload = await validate_jwt(raw)
        if jwt_payload and jwt_payload.get("role") in ("admin", "super_admin"):
            return {"user_id": jwt_payload.get("user_id", 0), "orchestrator_id": None, "expires_at": None}
    return None


# ─────────────────────────────────────────────────────────────────────────────
# Application loader
# ─────────────────────────────────────────────────────────────────────────────

async def _load_app(db: AsyncSession, slug: str) -> Application:
    result = await db.execute(
        select(Application).where(Application.slug == slug, Application.enabled == True)
    )
    app_row = result.scalar_one_or_none()
    if app_row is None:
        raise HTTPException(status_code=404, detail=f"Application '{slug}' not found")
    return app_row


async def _check_conversation_budget(
    db: AsyncSession,
    app_row: Application,
    context_id: uuid.UUID,
) -> None:
    """Raise 429 if this context has already consumed the application's conversation_token_limit."""
    limit = getattr(app_row, "conversation_token_limit", None)
    if not limit:
        return
    from sqlalchemy import func
    result = await db.execute(
        select(func.coalesce(func.sum(Task.tokens_used), 0)).where(
            Task.context_id == context_id
        )
    )
    tokens_used = result.scalar() or 0
    if tokens_used >= limit:
        logger.warning(
            "conversation budget exceeded",
            slug=app_row.slug,
            context_id=str(context_id),
            tokens_used=tokens_used,
            limit=limit,
        )
        raise HTTPException(
            status_code=429,
            detail=f"Conversation token limit reached ({tokens_used}/{limit}). Start a new conversation.",
        )


# ─────────────────────────────────────────────────────────────────────────────
# Public catalogue
# ─────────────────────────────────────────────────────────────────────────────

class AppInfo(BaseModel):
    slug: str
    name: str
    entry_point_type: str
    access_policy: dict
    presentation: dict


@router.get("/apps", response_model=list[AppInfo], tags=["apps"])
async def list_apps():
    """List all enabled applications (public catalogue)."""
    if db_module.AsyncSessionLocal is None:
        return []
    async with db_module.AsyncSessionLocal() as db:
        result = await db.execute(
            select(Application).where(Application.enabled == True).order_by(Application.name)
        )
        rows = result.scalars().all()
    return [
        AppInfo(
            slug=r.slug,
            name=r.name,
            entry_point_type=r.entry_point_type,
            access_policy=r.access_policy or {},
            presentation=r.presentation or {},
        )
        for r in rows
    ]


@router.get("/apps/{slug}", response_model=AppInfo, tags=["apps"])
async def get_app(slug: str):
    """Get a single application by slug."""
    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Service not ready")
    async with db_module.AsyncSessionLocal() as db:
        app_row = await _load_app(db, slug)
    return AppInfo(
        slug=app_row.slug,
        name=app_row.name,
        entry_point_type=app_row.entry_point_type,
        access_policy=app_row.access_policy or {},
        presentation=app_row.presentation or {},
    )


# ─────────────────────────────────────────────────────────────────────────────
# REST fire-and-forget — POST /apps/{slug}
# ─────────────────────────────────────────────────────────────────────────────

class RestRequest(BaseModel):
    message: str
    context_id: Optional[str] = None


class RestResponse(BaseModel):
    task_id: str
    context_id: str
    state: str
    poll_url: str


@router.post("/apps/{slug}", response_model=RestResponse, tags=["apps"])
async def rest_entry(slug: str, body: RestRequest, request: Request):
    """
    Fire-and-forget entry point. Returns task_id immediately; caller polls
    GET /apps/{slug}/tasks/{task_id} for the result.
    Auth: Bearer required unless access_policy.mode == 'public'.
    """
    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Service not ready")

    async with db_module.AsyncSessionLocal() as db:
        app_row = await _load_app(db, slug)

        policy = app_row.access_policy or {}
        if policy.get("mode") != "public":
            token_payload = await _resolve_bearer(request)
            if token_payload is None:
                raise HTTPException(status_code=401, detail="Authorization required")
        else:
            token_payload = {"user_id": 0, "orchestrator_id": None, "expires_at": None}

        user_id = token_payload.get("user_id", 0)

        from app.models import Orchestrator
        orch = await db.get(Orchestrator, app_row.orchestrator_id)
        if orch is None or not orch.enabled:
            raise HTTPException(status_code=503, detail="Bound orchestrator unavailable")

        token_orch_id = token_payload.get("orchestrator_id")
        if token_orch_id and str(orch.id) != token_orch_id:
            raise HTTPException(status_code=403, detail="Token not authorized for this application")

        context_id = (
            uuid.UUID(body.context_id)
            if body.context_id and _is_valid_uuid(body.context_id)
            else uuid.uuid4()
        )
        await _check_conversation_budget(db, app_row, context_id)
        deadline = datetime.now(timezone.utc) + timedelta(minutes=_DEFAULT_DEADLINE_MINUTES)

        task_row = await task_store.create_task(
            db,
            context_id=context_id,
            input_message={"parts": [{"kind": "text", "text": body.message}]},
            kind="root",
            orchestrator_id=orch.id,
            deadline=deadline,
            user_id=user_id,
        )
        await task_store.transition(db, task_row.id, "working")

    orch_name_for_task = orch.name

    async def _run():
        run_error: Optional[str] = None
        try:
            if _TEMPORAL_ENABLED:
                from app.temporal.bridge_client import start_orchestration_workflow, stream_run_events
                handle, _ = await start_orchestration_workflow(
                    orchestrator_name=orch_name_for_task,
                    user_message=body.message,
                    user_id=user_id,
                    token_payload=token_payload,
                    context_id=context_id,
                    session_id=uuid.uuid4(),
                )
                result = await handle.result()
                if result.get("status") != "completed":
                    run_error = result.get("error")
            else:
                async with db_module.AsyncSessionLocal() as run_db:
                    async for event in task_runner_run(
                        orchestrator_name=orch_name_for_task,
                        user_message=body.message,
                        user_id=user_id,
                        token_payload=token_payload,
                        db=run_db,
                        context_id=context_id,
                    ):
                        if event.get("type") == "error":
                            run_error = event.get("message")
                            break
        except Exception as exc:
            run_error = str(exc)
        try:
            async with db_module.AsyncSessionLocal() as fin_db:
                await task_store.transition(
                    fin_db, task_row.id,
                    "failed" if run_error else "completed",
                    error=run_error,
                )
        except Exception:
            pass

    asyncio.create_task(_run())

    return RestResponse(
        task_id=str(task_row.id),
        context_id=str(context_id),
        state="working",
        poll_url=f"/apps/{slug}/tasks/{task_row.id}",
    )


# ─────────────────────────────────────────────────────────────────────────────
# Task poll — GET /apps/{slug}/tasks/{task_id}
# ─────────────────────────────────────────────────────────────────────────────

class TaskPollResponse(BaseModel):
    task_id: str
    context_id: str
    state: str
    result: Optional[str] = None
    error: Optional[str] = None


@router.get("/apps/{slug}/tasks/{task_id}", response_model=TaskPollResponse, tags=["apps"])
async def poll_task(slug: str, task_id: str, request: Request):
    """Poll the state of a task created via the REST entry point."""
    if not _is_valid_uuid(task_id):
        raise HTTPException(status_code=400, detail="Invalid task_id")

    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Service not ready")

    async with db_module.AsyncSessionLocal() as db:
        app_row = await _load_app(db, slug)

        policy = app_row.access_policy or {}
        token_payload: Optional[dict] = None
        if policy.get("mode") != "public":
            token_payload = await _resolve_bearer(request)
            if token_payload is None:
                raise HTTPException(status_code=401, detail="Authorization required")

        task = await task_store.get_task(db, uuid.UUID(task_id))
        if task is None:
            raise HTTPException(status_code=404, detail="Task not found")

        if token_payload and not task_store.owns_task(task, token_payload.get("user_id")):
            raise HTTPException(status_code=404, detail="Task not found")

        result_text: Optional[str] = None
        if task.state == "completed":
            artifacts = await task_store.get_context_artifacts(db, task.context_id)
            for a in artifacts:
                for part in (a.parts or []):
                    if "text" in part:
                        result_text = part["text"]
                        break
                if result_text:
                    break

    return TaskPollResponse(
        task_id=task_id,
        context_id=str(task.context_id),
        state=task.state,
        result=result_text,
        error=task.error,
    )


# ─────────────────────────────────────────────────────────────────────────────
# SSE streaming — GET /apps/{slug}/sse
# ─────────────────────────────────────────────────────────────────────────────

@router.get("/apps/{slug}/sse", tags=["apps"])
async def sse_entry(slug: str, request: Request, message: str, context_id: Optional[str] = None):
    """
    SSE streaming entry point.

    Client sends message as query param; receives a text/event-stream response.

    Stream format:
      data: <token text>\n\n          — LLM token (one per frame)
      event: tool_start\ndata: {...}  — tool invocation started
      event: tool_done\ndata: {...}   — tool invocation finished
      event: error\ndata: {...}       — run failed
      event: done\ndata: {}          — stream complete

    Auth: Bearer token in Authorization header (or ?token= query param).
    access_policy.mode=public skips auth.

    Ideal for TTS pipelines: pipe token frames directly to a TTS engine as
    they arrive.
    """
    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Service not ready")

    if not message.strip():
        raise HTTPException(status_code=400, detail="message is required")

    async with db_module.AsyncSessionLocal() as db:
        app_row = await _load_app(db, slug)

        policy = app_row.access_policy or {}
        if policy.get("mode") != "public":
            token_payload = await _resolve_bearer(request)
            if token_payload is None:
                raise HTTPException(status_code=401, detail="Authorization required")
        else:
            token_payload = {"user_id": 0, "orchestrator_id": None, "expires_at": None}

        user_id = token_payload.get("user_id", 0)

        from app.models import Orchestrator
        orch = await db.get(Orchestrator, app_row.orchestrator_id)
        if orch is None or not orch.enabled:
            raise HTTPException(status_code=503, detail="Bound orchestrator unavailable")

        token_orch_id = token_payload.get("orchestrator_id")
        if token_orch_id and str(orch.id) != token_orch_id:
            raise HTTPException(status_code=403, detail="Token not authorized for this application")

    ctx_id = (
        uuid.UUID(context_id)
        if context_id and _is_valid_uuid(context_id)
        else uuid.uuid4()
    )
    await _check_conversation_budget(db, app_row, ctx_id)
    session_id = uuid.uuid4()
    edge = SSEEdge()
    orch_name = orch.name

    async def _run_and_stream():
        try:
            if _TEMPORAL_ENABLED:
                from app.temporal.bridge_client import start_orchestration_workflow, stream_run_events
                handle, _ = await start_orchestration_workflow(
                    orchestrator_name=orch_name,
                    user_message=message,
                    user_id=user_id,
                    token_payload=token_payload,
                    context_id=ctx_id,
                    session_id=session_id,
                )
                await stream_run_events(
                    context_id=str(ctx_id),
                    workflow_handle=handle,
                    emit_fn=edge.emit,
                )
            else:
                async with db_module.AsyncSessionLocal() as run_db:
                    async for event in task_runner_run(
                        orchestrator_name=orch_name,
                        user_message=message,
                        user_id=user_id,
                        token_payload=token_payload,
                        db=run_db,
                        session_id=session_id,
                        context_id=ctx_id,
                    ):
                        await edge.emit(event)
        except Exception as exc:
            logger.error("apps sse_entry error", slug=slug, error=str(exc))
            await edge.emit({"type": "error", "message": str(exc)})
        finally:
            await edge.close()

    asyncio.create_task(_run_and_stream())

    return StreamingResponse(
        edge.stream(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "X-Accel-Buffering": "no",  # disable Nginx/Traefik response buffering
        },
    )


# ─────────────────────────────────────────────────────────────────────────────
# WebSocket — WS /apps/{slug}/ws
# ─────────────────────────────────────────────────────────────────────────────

@router.websocket("/apps/{slug}/ws")
async def ws_entry(slug: str, websocket: WebSocket):
    """
    WebSocket chat entry point for a named application.
    Protocol mirrors /ws/orchestrate/{name}:
      Client → { "content": "user message", "context_id": "<uuid>" }
      Server → { "type": "ready", "task_id": "...", "context_id": "..." }
      Server → { "type": "token", "text": "..." }
      Server → { "type": "tool_start", "tool": "...", "iteration": N }
      Server → { "type": "tool_done",  "tool": "...", "duration_ms": N }
      Server → { "type": "done", "task_id": "...", "total_tokens": N }
      Server → { "type": "error", "message": "..." }
    """
    await websocket.accept()

    if db_module.AsyncSessionLocal is None:
        await websocket.send_json({"type": "error", "message": "Service not ready"})
        await websocket.close(code=4003)
        return

    try:
        async with db_module.AsyncSessionLocal() as db:
            app_row = await _load_app(db, slug)
            policy = app_row.access_policy or {}
            from app.models import Orchestrator
            orch = await db.get(Orchestrator, app_row.orchestrator_id)
            if orch is None or not orch.enabled:
                await websocket.send_json({"type": "error", "message": "Bound orchestrator unavailable"})
                await websocket.close(code=4003)
                return
            orchestrator_name = orch.name
            orch_id = orch.id
    except HTTPException as exc:
        await websocket.send_json({"type": "error", "message": exc.detail})
        await websocket.close(code=4004)
        return

    if policy.get("mode") != "public":
        token_payload = await _resolve_bearer_ws(websocket)
        if token_payload is None:
            await websocket.send_json({"type": "error", "message": "Authorization required"})
            await websocket.close(code=4001)
            return
        token_orch_id = token_payload.get("orchestrator_id")
        if token_orch_id and str(orch_id) != token_orch_id:
            await websocket.send_json({"type": "error", "message": "Token not authorized for this application"})
            await websocket.close(code=4001)
            return
    else:
        token_payload = {"user_id": 0, "orchestrator_id": None, "expires_at": None}

    user_id = token_payload.get("user_id", 0)

    try:
        raw = await websocket.receive_json()
    except WebSocketDisconnect:
        return
    except Exception:
        await websocket.send_json({"type": "error", "message": "Invalid message format"})
        await websocket.close(code=4000)
        return

    user_message = raw.get("content", "").strip()
    if not user_message:
        await websocket.send_json({"type": "error", "message": "content is required"})
        await websocket.close(code=4000)
        return

    context_id_raw = raw.get("context_id")
    context_id = (
        uuid.UUID(context_id_raw)
        if context_id_raw and _is_valid_uuid(context_id_raw)
        else uuid.uuid4()
    )
    async with db_module.AsyncSessionLocal() as budget_db:
        try:
            await _check_conversation_budget(budget_db, app_row, context_id)
        except HTTPException as exc:
            await websocket.send_json({"type": "error", "message": exc.detail})
            await websocket.close(code=4029)
            return
    session_id = uuid.uuid4()

    try:
        if _TEMPORAL_ENABLED:
            from app.temporal.bridge_client import (
                cancel_workflow,
                start_orchestration_workflow,
                stream_run_events,
            )
            handle, workflow_id = await start_orchestration_workflow(
                orchestrator_name=orchestrator_name,
                user_message=user_message,
                user_id=user_id,
                token_payload=token_payload,
                context_id=context_id,
                session_id=session_id,
            )
            cancel_evt = asyncio.Event()

            async def _ws_emit(event: dict) -> None:
                try:
                    await websocket.send_json(event)
                except Exception:
                    cancel_evt.set()

            async def _ws_cancel_listener():
                while True:
                    try:
                        raw_cancel = await websocket.receive_json()
                        if raw_cancel.get("type") == "cancel":
                            cancel_evt.set()
                            return
                    except (WebSocketDisconnect, Exception):
                        return

            stream_t = asyncio.ensure_future(
                stream_run_events(
                    context_id=str(context_id),
                    workflow_handle=handle,
                    emit_fn=_ws_emit,
                    cancel_event=cancel_evt,
                )
            )
            cancel_t = asyncio.ensure_future(_ws_cancel_listener())
            done, pending = await asyncio.wait([stream_t, cancel_t], return_when=asyncio.FIRST_COMPLETED)
            for t in pending:
                t.cancel()
                try:
                    await t
                except (asyncio.CancelledError, Exception):
                    pass
            if cancel_evt.is_set():
                await cancel_workflow(workflow_id)
        else:
            async with db_module.AsyncSessionLocal() as db:
                async for event in task_runner_run(
                    orchestrator_name=orchestrator_name,
                    user_message=user_message,
                    user_id=user_id,
                    token_payload=token_payload,
                    db=db,
                    session_id=session_id,
                    context_id=context_id,
                ):
                    try:
                        await websocket.send_json(event)
                    except WebSocketDisconnect:
                        return
                    except Exception:
                        return
    except WebSocketDisconnect:
        pass
    except Exception as exc:
        logger.error("apps ws_entry error", slug=slug, error=str(exc))
        try:
            await websocket.send_json({"type": "error", "message": str(exc)})
        except Exception:
            pass


# ─────────────────────────────────────────────────────────────────────────────
# Util
# ─────────────────────────────────────────────────────────────────────────────

def _is_valid_uuid(value: str) -> bool:
    try:
        uuid.UUID(value)
        return True
    except (ValueError, AttributeError):
        return False
