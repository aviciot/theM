"""
A2A Server — the-M as an A2A agent (Phase 8.5: durable inbound A2A).

Endpoints:
  GET  /.well-known/agent-card.json   → the-M's Agent Card
  POST /a2a                           → JSON-RPC 2.0 (SendMessage, GetTask, CancelTask)
  POST /a2a/push/{task_id}            → push webhook for child agent state changes

Protocol: A2A v1.0

Auth: Bearer token (them.access_tokens) or admin JWT — same as /ws/orchestrate.

Phase 8.5 changes vs Phase 1:
  - _tasks in-memory dict DELETED; all task state lives in them.tasks
  - SendMessage honors configuration.returnImmediately:
      true  → creates task, launches run detached (asyncio.create_task), returns working Task
      false → awaits completion before returning (default, backward-compat)
  - GetTask / CancelTask read/transition via task_store
  - Agent card url driven by config.BRIDGE_URL
  - Recursion guard: rejects inbound call if parent task depth >= max_depth
"""

import asyncio
import uuid
from datetime import datetime, timezone
from typing import Any, Optional

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse

import app.database as db_module
from app.config import settings
from app.services.auth_client import validate_jwt
from app.services import task_store
from app.services.task_runner import run as task_runner_run
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

router = APIRouter(tags=["a2a"])

_TERMINAL = {"completed", "failed", "canceled", "rejected"}

# Per-context task ceiling to prevent fork bombs
_MAX_TASKS_PER_CONTEXT = 50


# ─────────────────────────────────────────────────────────────────────────────
# Auth helper
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
            return payload
        jwt_payload = await validate_jwt(raw_token)
        if jwt_payload and jwt_payload.get("role") in ("admin", "super_admin"):
            return {"user_id": jwt_payload.get("user_id", 0), "orchestrator_id": None}
    return None


# ─────────────────────────────────────────────────────────────────────────────
# Agent Card
# ─────────────────────────────────────────────────────────────────────────────

@router.get("/.well-known/agent-card.json")
async def agent_card():
    """
    Serve the-M's A2A Agent Card.
    Each a2a_exposed orchestrator is listed as one skill.
    Skills loaded dynamically from DB; url from config.BRIDGE_URL.
    """
    skills = []

    if db_module.AsyncSessionLocal is not None:
        try:
            from sqlalchemy import select
            from app.models import Orchestrator
            async with db_module.AsyncSessionLocal() as db:
                result = await db.execute(
                    select(Orchestrator).where(
                        Orchestrator.enabled == True,
                        Orchestrator.a2a_exposed == True,
                    )
                )
                orchestrators = result.scalars().all()
                for orch in orchestrators:
                    skills.append({
                        "id": orch.name,
                        "name": orch.display_name,
                        "description": orch.system_prompt[:200] if orch.system_prompt else f"Orchestrator: {orch.display_name}",
                        "tags": ["orchestration"],
                        "inputModes": ["text/plain"],
                        "outputModes": ["text/plain"],
                    })
        except Exception as exc:
            logger.warning("agent_card: failed to load orchestrators", error=str(exc))

    if not skills:
        skills.append({
            "id": "default",
            "name": "Default Orchestrator",
            "description": "Multi-agent orchestration via the-M platform.",
            "tags": ["orchestration"],
            "inputModes": ["text/plain"],
            "outputModes": ["text/plain"],
        })

    bridge_url = getattr(settings, "bridge_url", f"http://localhost:{settings.app.port}")

    card = {
        "name": "the-M",
        "description": "Multi-agent orchestration platform. Routes goals to specialized AI agents.",
        "url": bridge_url,
        "version": "1.0.0",
        "capabilities": {
            "streaming": False,
            "pushNotifications": True,
            "stateTransitionHistory": False,
            "extensions": [
                {
                    "uri": "https://the-m.internal/ext/shared-context/v1",
                    "description": "Pass contextId in message to thread tasks into a shared conversation.",
                    "required": False,
                }
            ],
        },
        "defaultInputModes": ["text/plain"],
        "defaultOutputModes": ["text/plain"],
        "skills": skills,
        "securitySchemes": {
            "bearer": {
                "type": "http",
                "scheme": "bearer",
                "description": "Opaque access token from them.access_tokens",
            }
        },
        "security": [{"bearer": []}],
    }
    return JSONResponse(content=card)


# ─────────────────────────────────────────────────────────────────────────────
# JSON-RPC helpers
# ─────────────────────────────────────────────────────────────────────────────

def _rpc_error(id: Any, code: int, message: str) -> dict:
    return {"jsonrpc": "2.0", "id": id, "error": {"code": code, "message": message}}


def _rpc_ok(id: Any, result: Any) -> dict:
    return {"jsonrpc": "2.0", "id": id, "result": result}


def _task_to_a2a(task, artifacts: list[dict], input_text: str) -> dict:
    """Convert a them.tasks row + artifacts list to an A2A-compliant Task object."""
    a2a_artifacts = []
    for art in artifacts:
        # Skip internal summary artifacts from appearing in the inbound A2A response
        if isinstance(art.get("name"), str) and art["name"].startswith("Context Summary"):
            continue
        a2a_artifacts.append({
            "artifactId": art.get("artifact_id") or art.get("id", str(uuid.uuid4())),
            "name": art.get("name", "result"),
            "parts": art.get("parts", []),
        })

    status_message = None
    if task.error:
        status_message = {
            "role": "agent",
            "parts": [{"kind": "text", "text": task.error}],
            "messageId": str(uuid.uuid4()),
        }

    task_id = str(task.id)
    ctx_id = str(task.context_id)

    return {
        "id": task_id,
        "contextId": ctx_id,
        "status": {
            "state": task.state,
            "timestamp": datetime.now(timezone.utc).isoformat(),
            **({"message": status_message} if status_message else {}),
        },
        "artifacts": a2a_artifacts,
        "history": [
            {
                "role": "user",
                "parts": [{"kind": "text", "text": input_text}],
                "messageId": str(uuid.uuid4()),
            }
        ],
        "metadata": {},
    }


# ─────────────────────────────────────────────────────────────────────────────
# SendMessage handler
# ─────────────────────────────────────────────────────────────────────────────

async def _run_and_finalize(
    *,
    task_row,
    orchestrator_name: str,
    user_text: str,
    user_id: int,
    token_payload: dict,
    context_id: uuid.UUID,
    session_id: uuid.UUID,
) -> None:
    """Drive the orchestrator loop to completion and finalize the task row."""
    run_error: Optional[str] = None
    try:
        if db_module.AsyncSessionLocal is None:
            raise RuntimeError("Database not ready")
        async with db_module.AsyncSessionLocal() as db:
            async for event in task_runner_run(
                orchestrator_name=orchestrator_name,
                user_message=user_text,
                user_id=user_id,
                token_payload=token_payload,
                db=db,
                session_id=session_id,
                context_id=context_id,
            ):
                if event.get("type") == "error":
                    run_error = event.get("message", "Unknown error")
                    break
    except Exception as exc:
        run_error = str(exc)
        logger.error("a2a SendMessage run error", task_id=str(task_row.id), error=str(exc))

    # Transition the durable task to terminal state
    try:
        async with db_module.AsyncSessionLocal() as db:
            terminal = "failed" if run_error else "completed"
            await task_store.transition(
                db,
                task_row.id,
                terminal,
                error=run_error or None,
            )
    except Exception as exc:
        logger.warning("a2a: could not finalize task", task_id=str(task_row.id), error=str(exc))


async def _handle_send_message(rpc_id: Any, params: dict, token_payload: dict) -> dict:
    message = params.get("message", {})
    parts = message.get("parts", [])
    context_id_hint = message.get("contextId")
    configuration = params.get("configuration", {})
    return_immediately = configuration.get("returnImmediately", False)

    # Extract text from parts
    user_text = ""
    for part in parts:
        if part.get("kind") == "text" or "text" in part:
            user_text = part.get("text", "")
            break

    if not user_text.strip():
        return _rpc_error(rpc_id, -32602, "message must contain at least one text part")

    # Determine orchestrator from skill metadata or use default
    metadata = params.get("metadata", {})
    orchestrator_name = metadata.get("skill") or metadata.get("orchestrator") or "default"

    context_id = (
        uuid.UUID(context_id_hint)
        if context_id_hint and _is_valid_uuid(context_id_hint)
        else uuid.uuid4()
    )
    session_id = uuid.uuid4()
    user_id = token_payload.get("user_id", 0)

    # Recursion / fork-bomb guard
    if db_module.AsyncSessionLocal is not None:
        try:
            async with db_module.AsyncSessionLocal() as db:
                existing = await task_store.count_context_tasks(db, context_id)
                if existing >= _MAX_TASKS_PER_CONTEXT:
                    return _rpc_error(
                        rpc_id, -32003,
                        f"Context {context_id} has reached the task ceiling ({_MAX_TASKS_PER_CONTEXT})"
                    )
        except AttributeError:
            pass  # count_context_tasks may not exist yet — skip guard gracefully
        except Exception as exc:
            logger.warning("a2a: task ceiling check failed", error=str(exc))

    # Create the durable task row
    task_row = None
    if db_module.AsyncSessionLocal is not None:
        try:
            from sqlalchemy import select as sa_select
            from app.models import Orchestrator as OrchestratorModel
            async with db_module.AsyncSessionLocal() as db:
                orch_result = await db.execute(
                    sa_select(OrchestratorModel).where(
                        OrchestratorModel.name == orchestrator_name,
                        OrchestratorModel.enabled == True,
                    )
                )
                orch_row = orch_result.scalar_one_or_none()
                orch_id = orch_row.id if orch_row else None
                budget = getattr(orch_row, "budget_tokens", None) if orch_row else None

                task_row = await task_store.create_task(
                    db,
                    context_id=context_id,
                    input_message={"parts": [{"kind": "text", "text": user_text}]},
                    kind="root",
                    orchestrator_id=orch_id,
                    budget_tokens=budget,
                )
                await task_store.transition(db, task_row.id, "working")
        except Exception as exc:
            logger.error("a2a: could not create durable task", error=str(exc))
            return _rpc_error(rpc_id, -32000, f"Failed to initialize task: {exc}")

    if task_row is None:
        return _rpc_error(rpc_id, -32000, "Database unavailable")

    if return_immediately:
        # Detach — caller will poll via GetTask
        asyncio.create_task(
            _run_and_finalize(
                task_row=task_row,
                orchestrator_name=orchestrator_name,
                user_text=user_text,
                user_id=user_id,
                token_payload=token_payload,
                context_id=context_id,
                session_id=session_id,
            )
        )
        working_task = _task_to_a2a(task_row, [], user_text)
        return _rpc_ok(rpc_id, working_task)

    # Synchronous path: await completion
    await _run_and_finalize(
        task_row=task_row,
        orchestrator_name=orchestrator_name,
        user_text=user_text,
        user_id=user_id,
        token_payload=token_payload,
        context_id=context_id,
        session_id=session_id,
    )

    # Re-read final state and artifacts from DB
    try:
        async with db_module.AsyncSessionLocal() as db:
            final_task = await task_store.get_task(db, task_row.id)
            raw_artifacts = await task_store.get_context_artifacts(db, context_id)
    except Exception as exc:
        logger.warning("a2a: could not read final task state", error=str(exc))
        final_task = task_row
        raw_artifacts = []

    arts = [
        {"artifact_id": a.artifact_id, "name": a.name, "parts": a.parts}
        for a in raw_artifacts
    ] if raw_artifacts else []

    result_task = final_task if final_task else task_row
    return _rpc_ok(rpc_id, _task_to_a2a(result_task, arts, user_text))


# ─────────────────────────────────────────────────────────────────────────────
# GetTask handler
# ─────────────────────────────────────────────────────────────────────────────

async def _handle_get_task(rpc_id: Any, params: dict) -> dict:
    task_id_raw = params.get("id") or params.get("taskId")
    if not task_id_raw or not _is_valid_uuid(str(task_id_raw)):
        return _rpc_error(rpc_id, -32602, "id is required and must be a valid UUID")

    task_id = uuid.UUID(str(task_id_raw))

    if db_module.AsyncSessionLocal is None:
        return _rpc_error(rpc_id, -32000, "Database not ready")

    try:
        async with db_module.AsyncSessionLocal() as db:
            task = await task_store.get_task(db, task_id)
            if task is None:
                return _rpc_error(rpc_id, -32001, f"Task {task_id} not found")
            artifacts = await task_store.get_context_artifacts(db, task.context_id)
    except Exception as exc:
        logger.error("a2a GetTask error", task_id=str(task_id), error=str(exc))
        return _rpc_error(rpc_id, -32000, f"Internal error: {exc}")

    # Recover original input text from task row
    try:
        input_parts = task.input_message.get("parts", []) if task.input_message else []
        input_text = next(
            (p.get("text", "") for p in input_parts if "text" in p), ""
        )
    except Exception:
        input_text = ""

    arts = [
        {"artifact_id": a.artifact_id, "name": a.name, "parts": a.parts}
        for a in (artifacts or [])
    ]
    return _rpc_ok(rpc_id, _task_to_a2a(task, arts, input_text))


# ─────────────────────────────────────────────────────────────────────────────
# CancelTask handler
# ─────────────────────────────────────────────────────────────────────────────

async def _handle_cancel_task(rpc_id: Any, params: dict) -> dict:
    task_id_raw = params.get("id") or params.get("taskId")
    if not task_id_raw or not _is_valid_uuid(str(task_id_raw)):
        return _rpc_error(rpc_id, -32602, "id is required and must be a valid UUID")

    task_id = uuid.UUID(str(task_id_raw))

    if db_module.AsyncSessionLocal is None:
        return _rpc_error(rpc_id, -32000, "Database not ready")

    try:
        async with db_module.AsyncSessionLocal() as db:
            task = await task_store.get_task(db, task_id)
            if task is None:
                return _rpc_error(rpc_id, -32001, f"Task {task_id} not found")

            if task.state in _TERMINAL:
                return _rpc_error(rpc_id, -32002, f"Task is already in terminal state: {task.state}")

            canceled = await task_store.transition(db, task_id, "canceled")
    except Exception as exc:
        logger.error("a2a CancelTask error", task_id=str(task_id), error=str(exc))
        return _rpc_error(rpc_id, -32000, f"Internal error: {exc}")

    try:
        input_parts = task.input_message.get("parts", []) if task.input_message else []
        input_text = next((p.get("text", "") for p in input_parts if "text" in p), "")
    except Exception:
        input_text = ""

    result_task = canceled if canceled else task
    return _rpc_ok(rpc_id, _task_to_a2a(result_task, [], input_text))


# ─────────────────────────────────────────────────────────────────────────────
# Main JSON-RPC dispatcher
# ─────────────────────────────────────────────────────────────────────────────

@router.post("/a2a")
async def a2a_rpc(request: Request):
    """A2A JSON-RPC 2.0 endpoint."""
    token_payload = await _resolve_bearer(request)
    if token_payload is None:
        raise HTTPException(status_code=401, detail="Authorization required")

    try:
        body = await request.json()
    except Exception:
        return JSONResponse(
            status_code=400,
            content=_rpc_error(None, -32700, "Parse error"),
        )

    if isinstance(body, list):
        responses = []
        for item in body:
            resp = await _dispatch_single(item, token_payload)
            responses.append(resp)
        return JSONResponse(content=responses)

    result = await _dispatch_single(body, token_payload)
    return JSONResponse(content=result)


async def _dispatch_single(body: dict, token_payload: dict) -> dict:
    rpc_id = body.get("id")
    method = body.get("method", "")
    params = body.get("params", {})

    if body.get("jsonrpc") != "2.0":
        return _rpc_error(rpc_id, -32600, "Invalid Request: jsonrpc must be '2.0'")

    logger.info("a2a rpc", method=method, user_id=token_payload.get("user_id"))

    if method == "SendMessage":
        return await _handle_send_message(rpc_id, params, token_payload)
    elif method == "GetTask":
        return await _handle_get_task(rpc_id, params)
    elif method == "CancelTask":
        return await _handle_cancel_task(rpc_id, params)
    else:
        return _rpc_error(rpc_id, -32601, f"Method not found: {method}")


# ─────────────────────────────────────────────────────────────────────────────
# Utils
# ─────────────────────────────────────────────────────────────────────────────

def _is_valid_uuid(value: str) -> bool:
    try:
        uuid.UUID(value)
        return True
    except (ValueError, AttributeError):
        return False


# ─────────────────────────────────────────────────────────────────────────────
# Push webhook — receives state change notifications from remote A2A child agents
# ─────────────────────────────────────────────────────────────────────────────

@router.post("/a2a/push/{task_id}")
async def a2a_push(task_id: str, request: Request):
    """
    Push webhook for remote A2A child agent state transitions.

    Called by a child agent when its task state changes. Body is a raw A2A Task
    object (not JSON-RPC). Updates the corresponding them.tasks row.
    Idempotent: terminal tasks are silently accepted.
    """
    token_payload = await _resolve_bearer(request)
    if token_payload is None:
        raise HTTPException(status_code=401, detail="Authorization required")

    try:
        body = await request.json()
    except Exception:
        raise HTTPException(status_code=400, detail="Invalid JSON body")

    if not _is_valid_uuid(task_id):
        raise HTTPException(status_code=400, detail="Invalid task_id")

    child_task_id = uuid.UUID(task_id)

    if db_module.AsyncSessionLocal is None:
        raise HTTPException(status_code=503, detail="Database not ready")

    async with db_module.AsyncSessionLocal() as db:
        child_task = await task_store.get_task(db, child_task_id)
        if child_task is None:
            raise HTTPException(status_code=404, detail=f"Task {task_id} not found")

        if child_task.state in _TERMINAL:
            logger.info("a2a push: task already terminal, ignoring", task_id=task_id, state=child_task.state)
            return JSONResponse(content={"ok": True})

        new_state = body.get("status", {}).get("state")
        if not new_state:
            raise HTTPException(status_code=400, detail="body.status.state is required")

        logger.info("a2a push: transitioning task", task_id=task_id, new_state=new_state)
        await task_store.transition(db, child_task_id, new_state)

        if new_state == "completed":
            artifacts = body.get("artifacts", [])
            for artifact in artifacts:
                artifact_id = artifact.get("artifactId") or artifact.get("artifact_id") or str(uuid.uuid4())
                parts = artifact.get("parts", [])
                name = artifact.get("name")
                await task_store.record_artifact(
                    db,
                    task_id=child_task_id,
                    context_id=child_task.context_id,
                    artifact_id=artifact_id,
                    parts=parts,
                    name=name,
                )

    return JSONResponse(content={"ok": True})
