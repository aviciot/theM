#!/usr/bin/env python3.12
"""
test_13_dashboard_ws.py — structural tests for dashboard WS + broadcaster.
No containers required.
Usage: python3.12 scripts/tests/test_13_dashboard_ws.py
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


def src(path): return (ROOT / path).read_text()
def funcs(path):
    tree = ast.parse(src(path))
    return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]


print("=== test_13_dashboard_ws: Phase 6 Structure ===")

# 1. dashboard_broadcaster
try:
    fns = funcs("app/services/dashboard_broadcaster.py")
    check("publish defined", "publish" in fns)
    check("publish_run_started defined", "publish_run_started" in fns)
    check("publish_run_completed defined", "publish_run_completed" in fns)
    check("publish_run_step defined", "publish_run_step" in fns)
    check("publish_agents_changed defined", "publish_agents_changed" in fns)
    s = src("app/services/dashboard_broadcaster.py")
    check("odin:dash: prefix used", "odin:dash:" in s)
except Exception as exc:
    check("dashboard_broadcaster", False, str(exc))

# 2. ws_dashboard
try:
    s = src("app/routers/ws_dashboard.py")
    check("ws_dashboard route defined", "ws_dashboard" in s)
    check("/ws/dashboard path present", "/ws/dashboard" in s)
    check("subscribe message type handled", '"subscribe"' in s)
    check("valid channels defined", "_VALID_CHANNELS" in s)
    check("ping loop implemented", "_ping_loop" in s)
    check("JWT auth present", "validate_jwt" in s)
    check("WebSocketDisconnect handled", "WebSocketDisconnect" in s)
    check("channel relay to client", '"channel"' in s)
except Exception as exc:
    check("ws_dashboard", False, str(exc))

# 3. runs.py
try:
    fns = funcs("app/routers/runs.py")
    check("list_runs defined", "list_runs" in fns)
    check("get_run defined", "get_run" in fns)
    check("run_stats defined", "run_stats" in fns)
    check("delete_run defined", "delete_run" in fns)
    s = src("app/routers/runs.py")
    check("require_jwt used in runs", "require_jwt" in s)
    check("RunDetailOut includes steps+usage", "steps" in s and "usage" in s)
    check("admin role check in delete", "admin" in s)
except Exception as exc:
    check("runs.py", False, str(exc))

# 4. main.py wiring
try:
    s = src("app/main.py")
    check("ws_dashboard imported", "ws_dashboard" in s)
    check("runs imported", "from app.routers import runs" in s)
    check("ws_dashboard.router included", "ws_dashboard.router" in s)
    check("runs.router included", "runs.router" in s)
except Exception as exc:
    check("main.py wiring", False, str(exc))

# 5. Redis key prefix in broadcaster
try:
    s = src("app/services/dashboard_broadcaster.py")
    check("runs channel supported", '"runs"' in s or "'runs'" in s)
    check("agents channel supported", '"agents"' in s or "'agents'" in s)
except Exception as exc:
    check("broadcaster channels", False, str(exc))


print()
print(f"Result: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
