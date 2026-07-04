#!/usr/bin/env python3.12
"""
test_16_a2a_agents.py — structural tests for A2A test agents (Phase 7).
Validates that agent files exist and are importable, and that docker-compose
defines the expected profiles.
Usage: python scripts/tests/run_tests.py 16
No containers required — pure filesystem/import checks.
"""

import sys
import os
import ast
import pathlib

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../.."))

PASS = 0
FAIL = 0
ROOT = pathlib.Path(os.path.join(os.path.dirname(__file__), "../.."))


def check(desc, ok, detail=""):
    global PASS, FAIL
    if ok:
        print(f"  [PASS] {desc}"); PASS += 1
    else:
        print(f"  [FAIL] {desc}" + (f"  ({detail})" if detail else "")); FAIL += 1


def funcs_in(path):
    src = (ROOT / path).read_text(encoding="utf-8")
    tree = ast.parse(src)
    return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]


print("=== test_16_a2a_agents: A2A Test Agents Structure ===")

# 1. Agent files exist
for agent in ("a2a_echo", "a2a_slow", "a2a_stream"):
    path = ROOT / f"agents/{agent}"
    check(f"agents/{agent}/ directory exists", path.is_dir())
    check(f"agents/{agent}/main.py exists", (path / "main.py").exists())
    check(f"agents/{agent}/Dockerfile exists", (path / "Dockerfile").exists())
    check(f"agents/{agent}/requirements.txt exists", (path / "requirements.txt").exists())

# 2. Each agent has correct structure
for agent in ("a2a_echo", "a2a_slow", "a2a_stream"):
    try:
        fns = funcs_in(f"agents/{agent}/main.py")
        check(f"{agent}: execute defined", "execute" in fns)
        check(f"{agent}: cancel defined", "cancel" in fns)
        check(f"{agent}: make_agent_card defined", "make_agent_card" in fns)
        check(f"{agent}: create_app defined", "create_app" in fns)

        src = (ROOT / f"agents/{agent}/main.py").read_text(encoding="utf-8")
        check(f"{agent}: uses a2a-sdk AgentExecutor", "AgentExecutor" in src)
        check(f"{agent}: uses add_a2a_routes_to_fastapi", "add_a2a_routes_to_fastapi" in src)
        check(f"{agent}: has agent card capabilities", "capabilities" in src)
    except Exception as exc:
        check(f"{agent} structure", False, str(exc))

# 3. a2a-stream advertises streaming
try:
    src = (ROOT / "agents/a2a_stream/main.py").read_text(encoding="utf-8")
    check("a2a-stream: streaming=True in agent card", "streaming = True" in src or "capabilities.streaming = True" in src or ".streaming = True" in src)
except Exception as exc:
    check("a2a-stream streaming capability", False, str(exc))

# 4. docker-compose.yml has test-agents profile
try:
    src = (ROOT / "docker-compose.yml").read_text(encoding="utf-8")
    check("docker-compose: a2a-echo service defined", "a2a-echo" in src)
    check("docker-compose: a2a-slow service defined", "a2a-slow" in src)
    check("docker-compose: a2a-stream service defined", "a2a-stream" in src)
    check("docker-compose: test-agents profile present", "test-agents" in src)
except Exception as exc:
    check("docker-compose structure", False, str(exc))

# 5. seed SQL has a2a agents
try:
    src = (ROOT / "db/002_seed.sql").read_text(encoding="utf-8")
    check("seed: a2a_echo seeded", "'a2a_echo'" in src)
    check("seed: a2a_slow seeded", "'a2a_slow'" in src)
    check("seed: a2a_stream seeded", "'a2a_stream'" in src)
    check("seed: transport=a2a_async used", "a2a_async" in src)
    check("seed: supports_streaming seeded", "supports_streaming" in src)
except Exception as exc:
    check("seed SQL structure", False, str(exc))

# 6. A2aAsyncAdapter still importable (no regression)
try:
    from app.adapters.a2a_async_adapter import A2aAsyncAdapter
    from app.adapters.factory import get_adapter
    check("A2aAsyncAdapter importable after Phase 7 changes", True)
except Exception as exc:
    check("A2aAsyncAdapter import", False, str(exc))

# 7. requirements.txt lists correct SDK version
for agent in ("a2a_echo", "a2a_slow", "a2a_stream"):
    try:
        req = (ROOT / f"agents/{agent}/requirements.txt").read_text(encoding="utf-8")
        check(f"{agent}: requirements list a2a-sdk", "a2a-sdk" in req)
    except Exception as exc:
        check(f"{agent} requirements", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
