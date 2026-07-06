"""
End-to-end WebSocket orchestrate integration tests.

Uses Starlette TestClient (sync) for WS — it runs the app in a thread with
its own event loop. Auth is patched per-test so it doesn't touch the session
event loop. DB state is verified via a fresh asyncpg connection.
"""
import asyncio
import json
import time
import uuid
from unittest.mock import AsyncMock, MagicMock, patch

import jwt
import pytest
import pytest_asyncio
from httpx import AsyncClient
from starlette.testclient import TestClient

from app.main import app
from app.config import settings
from app.services.providers.base import LLMStreamEvent, ToolCall, TokenUsage
from tests.conftest import make_jwt, TEST_SECRET

# ── LLM mock helpers ──────────────────────────────────────────────────────────

def _make_final_answer_stream(text: str = "The capital of France is Paris."):
    async def _stream(*args, **kwargs):
        for word in text.split():
            yield LLMStreamEvent(type="token", text=word + " ")
        yield LLMStreamEvent(
            type="done",
            result={"answer": text, "raw_response": None, "usage": TokenUsage(10, 20)},
            usage=TokenUsage(10, 20),
        )
    return _stream


def _make_tool_call_stream(slug: str = "test_agent", then_answer: str = "Done."):
    call_count = 0

    async def _stream(system, messages, tools, max_tokens):
        nonlocal call_count
        call_count += 1
        if call_count == 1:
            tc = ToolCall(id="tc1", name=f"agent__{slug}", input={"message": "What is 2+2?"})
            yield LLMStreamEvent(
                type="tool_calls_ready",
                result={"tool_calls": [tc], "raw_response": None, "usage": TokenUsage(50, 10)},
            )
        else:
            yield LLMStreamEvent(type="token", text="The answer is 4.")
            yield LLMStreamEvent(
                type="done",
                result={"answer": "The answer is 4.", "raw_response": None, "usage": TokenUsage(60, 15)},
                usage=TokenUsage(60, 15),
            )

    return _stream


async def _fake_validate_jwt(token: str):
    """Drop-in async replacement for auth_client.validate_jwt in WS tests."""
    try:
        return jwt.decode(token, TEST_SECRET, algorithms=["HS256"])
    except Exception:
        return None


# ── Fixtures ──────────────────────────────────────────────────────────────────

@pytest_asyncio.fixture
async def ws_orchestrator(client: AsyncClient, admin_headers: dict, orchestrator: dict):
    token = make_jwt()
    return orchestrator, token


async def _fetch_runs(orchestrator_name: str) -> list:
    """Query odin.runs via asyncpg using the app's DB DSN."""
    import app.database as db_module
    from sqlalchemy import text
    async with db_module.AsyncSessionLocal() as session:
        result = await session.execute(
            text("SELECT status, iterations FROM odin.runs WHERE orchestrator_name = :name"),
            {"name": orchestrator_name},
        )
        return result.fetchall()


# ── Tests ─────────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_ws_rejects_no_token(orchestrator: dict):
    """WS without token must receive an error event."""
    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with TestClient(app) as tc:
            with tc.websocket_connect(f"/ws/orchestrate/{orchestrator['name']}") as ws:
                msg = ws.receive_json()
                assert msg["type"] == "error"


@pytest.mark.asyncio
async def test_ws_rejects_bad_token(orchestrator: dict):
    """WS with invalid JWT must receive an error event."""
    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with TestClient(app) as tc:
            with tc.websocket_connect(
                f"/ws/orchestrate/{orchestrator['name']}?token=bad-token"
            ) as ws:
                msg = ws.receive_json()
                assert msg["type"] == "error"


@pytest.mark.asyncio
async def test_ws_unknown_orchestrator():
    """Connecting to a non-existent orchestrator yields an error event."""
    token = make_jwt()
    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with TestClient(app) as tc:
            with tc.websocket_connect(
                f"/ws/orchestrate/does_not_exist?token={token}"
            ) as ws:
                ws.send_json({"type": "message", "content": "hello"})
                msgs = []
                for _ in range(5):
                    try:
                        msgs.append(ws.receive_json())
                    except Exception:
                        break
                types = [m["type"] for m in msgs]
                assert "error" in types


@pytest.mark.asyncio
async def test_ws_direct_answer(orchestrator: dict):
    """LLM returns a direct answer — verifies: ready → token → done."""
    token = make_jwt()
    mock_provider = MagicMock()
    mock_provider.init_messages = MagicMock(return_value=[])
    mock_provider.append_assistant_response = MagicMock()
    mock_provider.append_tool_results = MagicMock()
    mock_provider.stream_call = _make_final_answer_stream("Paris is the capital of France.")

    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with patch("app.services.task_runner._build_provider", return_value=mock_provider):
            with TestClient(app) as tc:
                with tc.websocket_connect(
                    f"/ws/orchestrate/{orchestrator['name']}?token={token}"
                ) as ws:
                    ws.send_json({"type": "message", "content": "What is the capital of France?"})
                    events = []
                    for _ in range(30):
                        try:
                            events.append(ws.receive_json())
                        except Exception:
                            break

    types = [e["type"] for e in events]
    assert "ready" in types, f"got: {types}"
    assert "token" in types, f"got: {types}"
    assert "done" in types, f"got: {types}"
    assert "tool_start" not in types


@pytest.mark.asyncio
async def test_ws_tool_call_and_answer(orchestrator: dict, agent: dict):
    """LLM calls a tool then answers — verifies: ready → tool_start → tool_done → done."""
    token = make_jwt()
    mock_provider = MagicMock()
    mock_provider.init_messages = MagicMock(return_value=[])
    mock_provider.append_assistant_response = MagicMock()
    mock_provider.append_tool_results = MagicMock()
    mock_provider.stream_call = _make_tool_call_stream(slug="test_agent")

    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with patch("app.services.task_runner._build_provider", return_value=mock_provider):
            with TestClient(app) as tc:
                with tc.websocket_connect(
                    f"/ws/orchestrate/{orchestrator['name']}?token={token}"
                ) as ws:
                    ws.send_json({"type": "message", "content": "What is 2+2?"})
                    events = []
                    for _ in range(40):
                        try:
                            events.append(ws.receive_json())
                        except Exception:
                            break

    types = [e["type"] for e in events]
    assert "ready" in types, f"got: {types}"
    assert "tool_start" in types, f"got: {types}"
    assert "tool_done" in types, f"got: {types}"
    assert "done" in types, f"got: {types}"
    tool_starts = [e for e in events if e["type"] == "tool_start"]
    assert any("test_agent" in e.get("tool", "") for e in tool_starts)


@pytest.mark.asyncio
async def test_ws_run_saved_to_db(orchestrator: dict):
    """After a completed run, odin.runs must have a completed row."""
    token = make_jwt()
    mock_provider = MagicMock()
    mock_provider.init_messages = MagicMock(return_value=[])
    mock_provider.append_assistant_response = MagicMock()
    mock_provider.stream_call = _make_final_answer_stream("Paris.")

    with patch("app.routers.ws_orchestrator.validate_jwt", new=_fake_validate_jwt):
        with patch("app.services.task_runner._build_provider", return_value=mock_provider):
            with TestClient(app) as tc:
                with tc.websocket_connect(
                    f"/ws/orchestrate/{orchestrator['name']}?token={token}"
                ) as ws:
                    ws.send_json({"type": "message", "content": "Capital of France?"})
                    for _ in range(20):
                        try:
                            msg = ws.receive_json()
                            if msg["type"] == "done":
                                break
                        except Exception:
                            break

    await asyncio.sleep(0.3)
    rows = await _fetch_runs(orchestrator["name"])
    assert len(rows) == 1, f"Expected 1 run row, got {len(rows)}: {rows}"
    assert rows[0].status == "completed"
