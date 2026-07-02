#!/usr/bin/env python3.12
"""
test_10_run_recorder.py — structural tests for run_recorder and orchestrator_service.
No live DB/containers needed — checks module structure and logic units.
Usage: python3.12 scripts/tests/test_10_run_recorder.py
"""

import sys, os, ast, pathlib

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
    src = (ROOT / path).read_text()
    tree = ast.parse(src)
    return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]


print("=== test_10_run_recorder: Phase 5 Structure ===")

# 1. run_recorder functions
try:
    fns = funcs_in("app/services/run_recorder.py")
    check("start_run defined", "start_run" in fns)
    check("record_step defined", "record_step" in fns)
    check("complete_step defined", "complete_step" in fns)
    check("record_usage defined", "record_usage" in fns)
    check("complete_run defined", "complete_run" in fns)
except Exception as exc:
    check("run_recorder structure", False, str(exc))

# 2. orchestrator_service functions
try:
    fns = funcs_in("app/services/orchestrator_service.py")
    check("run_orchestrator defined", "run_orchestrator" in fns)
    check("_load_orchestrator defined", "_load_orchestrator" in fns)
    check("_build_tools defined", "_build_tools" in fns)
    check("_invoke_agent defined", "_invoke_agent" in fns)
    check("_ws_ready helper defined", "_ws_ready" in fns)
    check("_ws_done helper defined", "_ws_done" in fns)
    check("_ws_error helper defined", "_ws_error" in fns)
except Exception as exc:
    check("orchestrator_service structure", False, str(exc))

# 3. ws_orchestrator structure
try:
    src = (ROOT / "app/routers/ws_orchestrator.py").read_text()
    check("ws_orchestrate route defined", "ws_orchestrate" in src)
    check("WebSocket imported", "WebSocket" in src)
    check("Bearer token parsing present", "_parse_bearer" in src)
    check("run_orchestrator imported", "run_orchestrator" in src)
    check("WebSocketDisconnect handled", "WebSocketDisconnect" in src)
except Exception as exc:
    check("ws_orchestrator structure", False, str(exc))

# 4. run_recorder WS events are dicts with 'type' key
try:
    src = (ROOT / "app/services/orchestrator_service.py").read_text()
    check("'type': 'ready' event present", '"type": "ready"' in src or "'type': 'ready'" in src)
    check("'type': 'done' event present", '"type": "done"' in src or "'type': 'done'" in src)
    check("'type': 'token' event present", '"type": "token"' in src or "'type': 'token'" in src)
    check("'type': 'error' event present", '"type": "error"' in src or "'type': 'error'" in src)
    check("'type': 'tool_start' event present", "tool_start" in src)
    check("'type': 'tool_done' event present", "tool_done" in src)
except Exception as exc:
    check("WS event types", False, str(exc))

# 5. main.py wires ws_orchestrator
try:
    src = (ROOT / "app/main.py").read_text()
    check("ws_orchestrator imported in main.py", "ws_orchestrator" in src)
    check("ws_orchestrator.router included", "ws_orchestrator.router" in src)
except Exception as exc:
    check("main.py wiring", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
