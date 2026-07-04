#!/usr/bin/env python3.12
"""
test_07_adapter_factory.py — unit tests for adapter factory + base contract.
Usage: python scripts/tests/test_07_adapter_factory.py
No containers required — pure Python.
"""

import sys
import os
import asyncio
import types
from unittest.mock import MagicMock

# Add project root to path so we can import app.*
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
    agent.slug = kwargs.get("slug", "test-agent")
    agent.endpoint_url = kwargs.get("endpoint_url", "ws://localhost:9999/ws")
    agent.auth_token_encrypted = kwargs.get("auth_token_encrypted", None)
    return agent


print("=== test_07_adapter_factory: Adapter Factory & Contract ===")

# 1. AdapterEvent dataclass
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

# 2-4. Factory tests — require pydantic (installed in container, not on host)
try:
    from app.adapters.factory import get_adapter
    from app.adapters.omni_ws_adapter import OmniWsAdapter
    from app.adapters.a2a_adapter import A2aAdapter

    agent = make_agent("omni_ws")
    adapter = get_adapter(agent)
    check("get_adapter('omni_ws') returns OmniWsAdapter", isinstance(adapter, OmniWsAdapter))

    agent = make_agent("a2a")
    adapter = get_adapter(agent)
    check("get_adapter('a2a') returns A2aAdapter", isinstance(adapter, A2aAdapter))

    agent = make_agent("ftp")
    raised = False
    try:
        get_adapter(agent)
    except ValueError:
        raised = True
    check("get_adapter(unknown) raises ValueError", raised)

except ImportError as exc:
    print(f"  [SKIP] factory tests — missing container deps ({exc})")
    print(f"         Run inside them-bridge container for full coverage.")

# 5. A2aAdapter yields error event on connection failure (real impl, not a stub)
async def _test_a2a():
    from app.adapters.a2a_adapter import A2aAdapter
    adapter = A2aAdapter(agent_slug="test", endpoint_url="http://localhost:19999", auth_token_encrypted=None)
    events = []
    async for ev in adapter.stream_invoke({"message": "hi"}, timeout=3):
        events.append(ev)
    return len(events) > 0 and events[-1].type == "error"

try:
    result = asyncio.run(_test_a2a())
    check("A2aAdapter yields error event on unreachable endpoint", result)
except Exception as exc:
    check("A2aAdapter error event", False, str(exc))

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
