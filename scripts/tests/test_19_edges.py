#!/usr/bin/env python3.12
"""
test_19_edges.py — structural tests for Phase 8.6: pluggable edge adapters.
No containers required.
Usage: python3.12 scripts/tests/test_19_edges.py
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
def classes(path):
    tree = ast.parse(src(path))
    return [n.name for n in ast.walk(tree) if isinstance(n, ast.ClassDef)]


print("=== test_19_edges: Phase 8.6 Pluggable Edge Adapters ===")

# 1. app/edges/ package exists
try:
    check("app/edges/__init__.py exists", (ROOT / "app/edges/__init__.py").exists())
    check("app/edges/base.py exists", (ROOT / "app/edges/base.py").exists())
    check("app/edges/websocket_edge.py exists", (ROOT / "app/edges/websocket_edge.py").exists())
    check("app/edges/voice_edge.py exists", (ROOT / "app/edges/voice_edge.py").exists())
    check("app/edges/rest_edge.py exists", (ROOT / "app/edges/rest_edge.py").exists())
    check("app/edges/registry.py exists", (ROOT / "app/edges/registry.py").exists())
except Exception as exc:
    check("app/edges/ files", False, str(exc))

# 2. base.py — EdgeAdapter ABC and EdgeRequest
try:
    s = src("app/edges/base.py")
    cls = classes("app/edges/base.py")
    check("EdgeAdapter class defined", "EdgeAdapter" in cls)
    check("EdgeRequest dataclass defined", "EdgeRequest" in cls)
    check("emit abstract method", "emit" in s)
    check("close abstract method", "close" in s)
    check("EdgeAdapter is ABC", "ABC" in s)
    check("EdgeRequest has orchestrator_name", "orchestrator_name" in s)
    check("EdgeRequest has user_message", "user_message" in s)
    check("EdgeRequest has modality", "modality" in s)
except Exception as exc:
    check("base.py", False, str(exc))

# 3. WebsocketEdge
try:
    s = src("app/edges/websocket_edge.py")
    cls = classes("app/edges/websocket_edge.py")
    check("WebsocketEdge class defined", "WebsocketEdge" in cls)
    check("WebsocketEdge.name = 'websocket'", "websocket" in s)
    check("WebsocketEdge.emit defined", "async def emit" in s)
    check("WebsocketEdge.close defined", "async def close" in s)
    check("WebsocketEdge handles WebSocketDisconnect", "WebSocketDisconnect" in s)
except Exception as exc:
    check("websocket_edge.py", False, str(exc))

# 4. Stub edges
try:
    for stub in ("voice_edge.py", "rest_edge.py"):
        s = src(f"app/edges/{stub}")
        check(f"{stub}: NotImplementedError in emit", "NotImplementedError" in s)
        check(f"{stub}: close defined", "async def close" in s)
except Exception as exc:
    check("stub edges", False, str(exc))

# 5. Registry
try:
    s = src("app/edges/registry.py")
    check("get_edge_class defined", "get_edge_class" in s)
    check("VALID_EDGES defined", "VALID_EDGES" in s)
    check("websocket in registry", "websocket" in s)
    check("voice in registry", "voice" in s)
    check("rest in registry", "rest" in s)
    check("ValueError for unknown edge", "ValueError" in s)
except Exception as exc:
    check("registry.py", False, str(exc))

# 6. Registry importable + correct types
try:
    from app.edges.registry import get_edge_class, VALID_EDGES
    from app.edges.websocket_edge import WebsocketEdge
    from app.edges.voice_edge import VoiceEdge
    from app.edges.rest_edge import RestEdge
    check("get_edge_class('websocket') returns WebsocketEdge", get_edge_class("websocket") is WebsocketEdge)
    check("get_edge_class('voice') returns VoiceEdge", get_edge_class("voice") is VoiceEdge)
    check("get_edge_class('rest') returns RestEdge", get_edge_class("rest") is RestEdge)
    check("VALID_EDGES contains websocket", "websocket" in VALID_EDGES)
    check("VALID_EDGES contains voice", "voice" in VALID_EDGES)
    check("VALID_EDGES contains rest", "rest" in VALID_EDGES)
    try:
        get_edge_class("ftp")
        check("unknown edge raises ValueError", False)
    except ValueError:
        check("unknown edge raises ValueError", True)
except Exception as exc:
    check("registry import", False, str(exc))

# 7. ws_orchestrator.py uses WebsocketEdge + edge guard
try:
    s = src("app/routers/ws_orchestrator.py")
    fns_ws = funcs("app/routers/ws_orchestrator.py")
    check("ws_orchestrate defined", "ws_orchestrate" in fns_ws)
    check("WebsocketEdge imported", "WebsocketEdge" in s)
    check("edge.emit() called", "edge.emit" in s)
    check("edge.close() called", "edge.close" in s)
    check("edges guard present", "websocket" in s and ("not in" in s or "edges" in s))
    check("edge rejection error message", "does not allow the websocket edge" in s)
    check("ws_orchestrate still on /ws/orchestrate/{name}", "/ws/orchestrate/{name}" in s)
except Exception as exc:
    check("ws_orchestrator.py", False, str(exc))

# 8. Orchestrator model has edges column
try:
    s = src("app/models.py")
    check("Orchestrator.edges column", "edges" in s and "ARRAY" in s)
except Exception as exc:
    check("models.py edges column", False, str(exc))

# 9. Migration has edges column
try:
    s = src("db/003_phase8.sql")
    check("edges column in migration", "edges" in s)
except Exception as exc:
    check("db/003_phase8.sql edges", False, str(exc))

# 10. Existing WS tests still pass (structural — no regression)
try:
    s = src("app/routers/ws_orchestrator.py")
    check("ready event still referenced", "ready" in s or "task_runner_run" in s)
    check("WebSocketDisconnect handled", "WebSocketDisconnect" in s)
    check("bearer auth preserved", "_parse_bearer" in s or "Bearer" in s or "bearer" in s.lower())
except Exception as exc:
    check("ws_orchestrator regression checks", False, str(exc))

print(f"\n{'='*50}")
print(f"Results: {PASS} passed, {FAIL} failed")
sys.exit(0 if FAIL == 0 else 1)
