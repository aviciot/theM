#!/usr/bin/env python3.12
"""
test_10_run_recorder.py — structural tests for run_recorder, task_runner, and ws_orchestrator.
No live DB/containers needed — checks module structure and logic units.
Usage: python scripts/tests/run_tests.py 10
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


print("=== test_10_run_recorder: Run Recorder & Task Runner Structure ===")

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

# 2. task_runner (Phase 3 durable loop)
try:
    fns = funcs_in("app/services/task_runner.py")
    check("task_runner.run defined", "run" in fns)
    check("_load_orchestrator_row defined", "_load_orchestrator_row" in fns)
    check("_load_agents defined", "_load_agents" in fns)
    check("_invoke_agent defined", "_invoke_agent" in fns)
    check("_build_messages_from_store defined", "_build_messages_from_store" in fns)
    check("_persist_assistant_turn defined", "_persist_assistant_turn" in fns)
    check("_persist_tool_results defined", "_persist_tool_results" in fns)
except Exception as exc:
    check("task_runner structure", False, str(exc))

# 3. task_store (Phase 2)
try:
    fns = funcs_in("app/services/task_store.py")
    check("task_store.create_task defined", "create_task" in fns)
    check("task_store.transition defined", "transition" in fns)
    check("task_store.get_task defined", "get_task" in fns)
    check("task_store.record_artifact defined", "record_artifact" in fns)
    check("task_store.record_message defined", "record_message" in fns)
    check("task_store.get_context_artifacts defined", "get_context_artifacts" in fns)
    check("task_store.add_tokens_used defined", "add_tokens_used" in fns)
except Exception as exc:
    check("task_store structure", False, str(exc))

# 4. ws_orchestrator uses task_runner
try:
    src = (ROOT / "app/routers/ws_orchestrator.py").read_text()
    check("ws_orchestrate route defined", "ws_orchestrate" in src)
    check("WebSocket imported", "WebSocket" in src)
    check("Bearer token parsing present", "_parse_bearer" in src)
    check("task_runner imported", "task_runner" in src)
    check("WebSocketDisconnect handled", "WebSocketDisconnect" in src)
except Exception as exc:
    check("ws_orchestrator structure", False, str(exc))

# 5. task_runner yields correct WS event types
try:
    src = (ROOT / "app/services/task_runner.py").read_text()
    check("'type': 'ready' event present", '"ready"' in src)
    check("'type': 'done' event present", '"done"' in src)
    check("'type': 'token' event present", '"token"' in src)
    check("'type': 'error' event present", '"error"' in src)
    check("'type': 'tool_start' event present", "tool_start" in src)
    check("'type': 'tool_done' event present", "tool_done" in src)
    check("task_id in ready event", "task_id" in src)
    check("context_id in run events", "context_id" in src)
except Exception as exc:
    check("WS event types", False, str(exc))

# 6. main.py wires both ws_orchestrator and a2a_server
try:
    src = (ROOT / "app/main.py").read_text()
    check("ws_orchestrator imported in main.py", "ws_orchestrator" in src)
    check("ws_orchestrator.router included", "ws_orchestrator.router" in src)
    check("a2a_server imported in main.py", "a2a_server" in src)
    check("a2a_server.router included", "a2a_server.router" in src)
except Exception as exc:
    check("main.py wiring", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
