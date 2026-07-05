#!/usr/bin/env python3.12
"""
test_07_adapter_factory.py — unit tests for adapter factory + base contract.
Phase 8.1: only a2a_async transport remains.
Usage: python scripts/tests/run_tests.py 07
No containers required — pure Python.
"""

import sys
import os
import asyncio
from unittest.mock import MagicMock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../.."))

PASS = 0
FAIL = 0


def check(desc: str, ok: bool, detail: str = ""):
    global PASS, FAIL
    if ok:
        print(f"  [PASS] {desc}")
        PASS += 1
    else:
        print(f"  [FAIL] {desc}" + (f"  ({detail})" if detail else ""))
        FAIL += 1


def make_agent(transport: str, **kwargs) -> MagicMock:
    agent = MagicMock()
    agent.transport = transport
    agent.slug = kwargs.get("slug", "test_agent")
    agent.endpoint_url = kwargs.get("endpoint_url", "http://localhost:9999/")
    agent.auth_token_encrypted = kwargs.get("auth_token_encrypted", None)
    agent.supports_streaming = kwargs.get("supports_streaming", False)
    return agent


print("=== test_07_adapter_factory: Adapter Factory & Contract ===")

# 1. AdapterEvent — core types
try:
    from app.adapters.base import AdapterEvent, AgentAdapter
    e = AdapterEvent(type="token", text="hello")
    check("AdapterEvent(type='token') created", e.type == "token" and e.text == "hello")

    e2 = AdapterEvent(type="done", result="full output")
    check("AdapterEvent(type='done') created", e2.type == "done" and e2.result == "full output")

    e3 = AdapterEvent(type="error", error="something broke")
    check("AdapterEvent(type='error') created", e3.type == "error" and e3.error == "something broke")
except Exception as exc:
    check("AdapterEvent import", False, str(exc))

# 2. AdapterEvent — A2A event types
try:
    from app.adapters.base import AdapterEvent
    e4 = AdapterEvent(type="task_created", remote_task_id="abc-123")
    check("AdapterEvent(type='task_created') has remote_task_id", e4.remote_task_id == "abc-123")

    e5 = AdapterEvent(type="status", state="TASK_STATE_WORKING")
    check("AdapterEvent(type='status') has state", e5.state == "TASK_STATE_WORKING")

    e6 = AdapterEvent(type="artifact", artifact={"artifactId": "x", "parts": [{"text": "hi"}]})
    check("AdapterEvent(type='artifact') has artifact dict", isinstance(e6.artifact, dict))

    e7 = AdapterEvent(type="status", state="TASK_STATE_INPUT_REQUIRED", input_required=True)
    check("AdapterEvent input_required flag", e7.input_required is True)
except Exception as exc:
    check("AdapterEvent A2A types", False, str(exc))

# 3. Factory — only a2a_async survives (Phase 8.1)
try:
    from app.adapters.factory import get_adapter
    from app.adapters.a2a_async_adapter import A2aAsyncAdapter

    check("get_adapter('a2a_async') returns A2aAsyncAdapter",
          isinstance(get_adapter(make_agent("a2a_async")), A2aAsyncAdapter))

    check("get_adapter('a2a_async') with streaming flag",
          isinstance(get_adapter(make_agent("a2a_async", supports_streaming=True)), A2aAsyncAdapter))

    for dead_transport in ("omni_ws", "a2a", "ftp", "grpc"):
        raised = False
        try:
            get_adapter(make_agent(dead_transport))
        except ValueError:
            raised = True
        check(f"get_adapter('{dead_transport}') raises ValueError", raised)

except ImportError as exc:
    print(f"  [SKIP] factory tests — missing deps ({exc})")

# 4. Legacy adapter files are deleted
check("omni_ws_adapter.py deleted",
      not os.path.exists(os.path.join(os.path.dirname(__file__), "../../app/adapters/omni_ws_adapter.py")))
check("a2a_adapter.py deleted",
      not os.path.exists(os.path.join(os.path.dirname(__file__), "../../app/adapters/a2a_adapter.py")))

# 5. A2aAsyncAdapter yields error event on connection failure
async def _test_a2a_async():
    from app.adapters.a2a_async_adapter import A2aAsyncAdapter
    adapter = A2aAsyncAdapter(
        agent_slug="test",
        endpoint_url="http://localhost:19999",
        auth_token_encrypted=None,
        max_poll_seconds=3,
    )
    events = []
    async for ev in adapter.stream_invoke({"message": "hi"}, timeout=3):
        events.append(ev)
    return len(events) > 0 and events[-1].type == "error"

try:
    result = asyncio.run(_test_a2a_async())
    check("A2aAsyncAdapter yields error event on unreachable endpoint", result)
except Exception as exc:
    check("A2aAsyncAdapter error event", False, str(exc))

# 6. AgentAdapter is abstract
try:
    from app.adapters.base import AgentAdapter
    raised = False
    try:
        AgentAdapter()
    except TypeError:
        raised = True
    check("AgentAdapter is abstract (cannot instantiate)", raised)
except Exception as exc:
    check("AgentAdapter abstractness", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
