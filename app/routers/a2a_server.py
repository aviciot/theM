"""
A2A Server — the-M as an A2A agent.

Endpoints:
  GET  /.well-known/agent-card.json   → the-M's Agent Card
  POST /a2a                           → JSON-RPC 2.0 (SendMessage, GetTask, CancelTask)

Protocol: A2A v1.0 (https://google.github.io/A2A/)

Auth: Bearer token (existing them.access_tokens) — same as /ws/orchestrate.

Phase 1: wraps the existing orchestrator loop synchronously.
         Tasks are tracked in them.tasks (Phase 2 adds the full durable layer).
"""

import uuid
from datetime import datetime, timezone
from typing import Any

from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import JSONResponse

import app.database as db_module
from app.services.auth_client import validate_jwt
from app.services.task_runner import run as task_runner_run
from app.services.token_cache import validate_bearer_token
from app.utils.logger import logger

router = APIRouter(tags=["a2a"])

# ── In-memory task store (Phase 1 only — replaced in Phase 2 by them.tasks) ──
# Maps task_id → task dict. Survives only while the process is alive.
_tasks: dict[str, dict] = {}


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
    Skills are loaded dynamically from the DB.
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

    # Always include a default skill for the default orchestrator
    if not skills:
        skills.append({
            "id": "default",
            "name": "Default Orchestrator",
            "description": "Multi-agent orchestration via the-M platform.",
            "tags": ["orchestration"],
            "inputModes": ["text/plain"],
            "outputModes": ["text/plain"],
        })

    card = {
        "name": "the-M",
        "description": "Multi-agent orchestration platform. Routes goals to specialized AI agents.",
        "url": "http://localhost:8001",
        "version": "1.0.0",
        "capabilities": {
            "streaming": False,
            "pushNotifications": False,
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


def _make_task(
    task_id: str,
    state: str,
    context_id: str | None,
    input_text: str,
    output_text: str | None = None,
    error: str | None = None,
) -> dict:
    """Build an A2A-compliant Task object."""
    artifacts = []
    if output_text:
        artifacts.append({
            "artifactId": "result",
            "name": "result",
            "parts": [{"kind": "text", "text": output_text}],
        })

    status_message = None
    if error:
        status_message = {
            "role": "agent",
            "parts": [{"kind": "text", "text": error}],
            "messageId": str(uuid.uuid4()),
        }

    return {
        "id": task_id,
        "contextId": context_id or task_id,
        "status": {
            "state": state,
            "timestamp": datetime.now(timezone.utc).isoformat(),
            **({"message": status_message} if status_message else {}),
        },
        "artifacts": artifacts,
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

async def _handle_send_message(rpc_id: Any, params: dict, token_payload: dict) -> dict:
    message = params.get("message", {})
    parts = message.get("parts", [])
    context_id = message.get("contextId")
    task_id_hint = message.get("taskId")

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

    task_id = str(uuid.uuid4())
    ctx_id = context_id or task_id

    # Register as working
    _tasks[task_id] = _make_task(task_id, "working", ctx_id, user_text)

    # Run the orchestrator loop synchronously (Phase 1 — Phase 3 makes this durable)
    final_answer = ""
    run_error = None

    try:
        if db_module.AsyncSessionLocal is None:
            raise RuntimeError("Database not ready")

        async with db_module.AsyncSessionLocal() as db:
            async for event in task_runner_run(
                orchestrator_name=orchestrator_name,
                user_message=user_text,
                user_id=token_payload["user_id"],
                token_payload=token_payload,
                db=db,
                session_id=uuid.uuid4(),
                context_id=uuid.UUID(ctx_id) if _is_valid_uuid(ctx_id) else uuid.uuid4(),
            ):
                if event.get("type") == "token":
                    final_answer += event.get("text", "")
                elif event.get("type") == "done":
                    pass  # final_answer already assembled from tokens
                elif event.get("type") == "error":
                    run_error = event.get("message", "Unknown error")
                    break

    except Exception as exc:
        run_error = str(exc)
        logger.error("a2a SendMessage error", task_id=task_id, error=str(exc))

    if run_error:
        task = _make_task(task_id, "failed", ctx_id, user_text, error=run_error)
    else:
        task = _make_task(task_id, "completed", ctx_id, user_text, output_text=final_answer)

    _tasks[task_id] = task
    return _rpc_ok(rpc_id, task)


# ─────────────────────────────────────────────────────────────────────────────
# GetTask handler
# ─────────────────────────────────────────────────────────────────────────────

async def _handle_get_task(rpc_id: Any, params: dict) -> dict:
    task_id = params.get("id") or params.get("taskId")
    if not task_id:
        return _rpc_error(rpc_id, -32602, "id is required")

    task = _tasks.get(str(task_id))
    if task is None:
        return _rpc_error(rpc_id, -32001, f"Task {task_id} not found")

    return _rpc_ok(rpc_id, task)


# ─────────────────────────────────────────────────────────────────────────────
# CancelTask handler
# ─────────────────────────────────────────────────────────────────────────────

async def _handle_cancel_task(rpc_id: Any, params: dict) -> dict:
    task_id = params.get("id") or params.get("taskId")
    if not task_id:
        return _rpc_error(rpc_id, -32602, "id is required")

    task = _tasks.get(str(task_id))
    if task is None:
        return _rpc_error(rpc_id, -32001, f"Task {task_id} not found")

    # Can only cancel non-terminal tasks
    state = task.get("status", {}).get("state", "")
    if state in ("completed", "failed", "canceled", "rejected"):
        return _rpc_error(rpc_id, -32002, f"Task is already in terminal state: {state}")

    task["status"]["state"] = "canceled"
    task["status"]["timestamp"] = datetime.now(timezone.utc).isoformat()
    _tasks[str(task_id)] = task
    return _rpc_ok(rpc_id, task)


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

    # Support batch or single
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
