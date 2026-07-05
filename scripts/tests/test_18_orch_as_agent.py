#!/usr/bin/env python3.12
"""
test_18_orch_as_agent.py — structural tests for Phase 8.5: orchestrator-as-agent
(durable inbound A2A, no in-memory _tasks dict).
No containers required.
Usage: python3.12 scripts/tests/test_18_orch_as_agent.py
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


def src(path): return (ROOT / path).read_text(encoding="utf-8")
def funcs(path):
    tree = ast.parse(src(path))
    return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]


print("=== test_18_orch_as_agent: Phase 8.5 Durable Inbound A2A ===")

# 1. _tasks dict deleted from a2a_server.py
try:
    s = src("app/routers/a2a_server.py")
    check("_tasks in-memory dict deleted", "_tasks: dict" not in s and "_tasks = {}" not in s)
    check("_tasks[task_id] assignment deleted", "_tasks[task_id]" not in s)
except Exception as exc:
    check("a2a_server.py _tasks removal", False, str(exc))

# 2. Required functions still present
try:
    fns = funcs("app/routers/a2a_server.py")
    check("_handle_send_message defined", "_handle_send_message" in fns)
    check("_handle_get_task defined", "_handle_get_task" in fns)
    check("_handle_cancel_task defined", "_handle_cancel_task" in fns)
    check("_run_and_finalize defined", "_run_and_finalize" in fns)
    check("agent_card defined", "agent_card" in fns)
    check("a2a_rpc defined", "a2a_rpc" in fns)
    check("a2a_push defined", "a2a_push" in fns)
    check("_dispatch_single defined", "_dispatch_single" in fns)
    check("_task_to_a2a defined", "_task_to_a2a" in fns)
except Exception as exc:
    check("a2a_server.py functions", False, str(exc))

# 3. Durable task_store used in send/get/cancel
try:
    s = src("app/routers/a2a_server.py")
    check("task_store.create_task called", "task_store.create_task" in s)
    check("task_store.get_task called", "task_store.get_task" in s)
    check("task_store.transition called", "task_store.transition" in s)
    check("task_store.get_context_artifacts called", "task_store.get_context_artifacts" in s)
    check("task_store imported", "from app.services import task_store" in s)
except Exception as exc:
    check("task_store usage", False, str(exc))

# 4. returnImmediately / async detach
try:
    s = src("app/routers/a2a_server.py")
    check("returnImmediately honored", "returnImmediately" in s)
    check("asyncio.create_task used for detach", "asyncio.create_task" in s)
    check("asyncio imported", "import asyncio" in s)
except Exception as exc:
    check("async detach", False, str(exc))

# 5. Recursion / fork-bomb guard
try:
    s = src("app/routers/a2a_server.py")
    check("_MAX_TASKS_PER_CONTEXT defined", "_MAX_TASKS_PER_CONTEXT" in s)
    check("task ceiling check present", "task ceiling" in s or "count_context_tasks" in s or "_MAX_TASKS_PER_CONTEXT" in s)
except Exception as exc:
    check("recursion guard", False, str(exc))

# 6. Agent card uses config bridge_url
try:
    s = src("app/routers/a2a_server.py")
    check("bridge_url from config", "bridge_url" in s)
    check("hardcoded localhost:8001 removed from card", 'url": "http://localhost:8001"' not in s)
except Exception as exc:
    check("agent card url", False, str(exc))

# 7. config.py has BRIDGE_URL
try:
    s = src("app/config.py")
    check("BRIDGE_URL in Settings", "BRIDGE_URL" in s)
    check("bridge_url in GlobalConfig", "bridge_url" in s)
except Exception as exc:
    check("config.py BRIDGE_URL", False, str(exc))

# 8. Idempotent push webhook still present
try:
    s = src("app/routers/a2a_server.py")
    check("/a2a/push/{task_id} route present", "/a2a/push/{task_id}" in s)
    check("terminal guard in push webhook", "_TERMINAL" in s)
    check("idempotent response on terminal", 'ok": True' in s)
except Exception as exc:
    check("push webhook", False, str(exc))

# 9. _TERMINAL constant defined (not just in push)
try:
    s = src("app/routers/a2a_server.py")
    check("_TERMINAL set defined at module level", '_TERMINAL = {' in s or "_TERMINAL=" in s)
except Exception as exc:
    check("_TERMINAL", False, str(exc))

# 10. Orchestrator model has a2a_exposed + budget_tokens
try:
    s = src("app/models.py")
    check("Orchestrator.a2a_exposed column", "a2a_exposed" in s)
    check("Orchestrator.budget_tokens column", "budget_tokens" in s)
except Exception as exc:
    check("models.py a2a_exposed/budget_tokens", False, str(exc))

# 11. db/003_phase8.sql has budget_tokens migration
try:
    s = src("db/003_phase8.sql")
    check("budget_tokens column in migration", "budget_tokens" in s)
except Exception as exc:
    check("db/003_phase8.sql budget_tokens", False, str(exc))

# 12. a2a_server is still wired in main.py
try:
    s = src("app/main.py")
    check("a2a_server imported in main.py", "a2a_server" in s)
    check("a2a_server.router included", "a2a_server.router" in s)
except Exception as exc:
    check("main.py wiring", False, str(exc))

print(f"\n{'='*50}")
print(f"Results: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
