#!/usr/bin/env python3
"""
run_tests.py — the-M cross-platform test runner (Windows + Linux)
Usage:
    python scripts/tests/run_tests.py
    python scripts/tests/run_tests.py --test 01 05 15     # run specific tests
    ADMIN_JWT=<token> python scripts/tests/run_tests.py   # enable E2E test

Calls Docker CLI via subprocess — works on any OS where `docker` is in PATH.
No WSL, no bash required.
"""

import io
import ast
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import time
import asyncio
import types

# Force UTF-8 output on Windows (avoids CP1252 UnicodeEncodeError)
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

ROOT = pathlib.Path(__file__).parent.parent.parent
PASS = 0
FAIL = 0
SKIP = 0

# ─── helpers ──────────────────────────────────────────────────────────────────

def _color(text, code): return f"\033[{code}m{text}\033[0m" if sys.stdout.isatty() else text
def green(t): return _color(t, "32")
def red(t):   return _color(t, "31")
def yellow(t): return _color(t, "33")
def bold(t):  return _color(t, "1")

def check(desc, ok, detail=""):
    global PASS, FAIL
    if ok:
        print(f"  {green('[PASS]')} {desc}"); PASS += 1
    else:
        msg = f"  ({detail})" if detail else ""
        print(f"  {red('[FAIL]')} {desc}{msg}"); FAIL += 1

def skip(desc):
    global SKIP
    print(f"  {yellow('[SKIP]')} {desc}"); SKIP += 1

def section(title):
    print(f"\n{bold('===')} {title} {bold('===')}")

def docker(*args, input=None):
    """Run a docker command, return stdout string (empty on error)."""
    try:
        r = subprocess.run(
            ["docker", *args],
            capture_output=True, text=True, input=input, timeout=15
        )
        return r.stdout.strip()
    except Exception:
        return ""

def dexec(container, *cmd):
    """docker exec <container> <cmd...> → stdout string."""
    return docker("exec", container, *cmd)

def dcurl(container, *curl_args):
    """docker exec <container> curl -s <args> → stdout string."""
    return dexec(container, "curl", "-s", *curl_args)

def http_status(container, path, port, method="GET", body=None, headers=None, host="localhost"):
    args = ["-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10",
            "-X", method, f"http://{host}:{port}{path}"]
    if body:
        args += ["-H", "Content-Type: application/json", "-d", body]
    for h in (headers or []):
        args += ["-H", h]
    return dcurl(container, *args)

def http_json(container, path, port, method="GET", body=None, headers=None, host="localhost"):
    args = ["--max-time", "10", "-X", method, f"http://{host}:{port}{path}"]
    if body:
        args += ["-H", "Content-Type: application/json", "-d", body]
    for h in (headers or []):
        args += ["-H", h]
    raw = dcurl(container, *args)
    try:
        return json.loads(raw)
    except Exception:
        return {}

def src(rel_path):
    return (ROOT / rel_path).read_text(encoding="utf-8")

def funcs_in(rel_path):
    tree = ast.parse(src(rel_path))
    return [n.name for n in ast.walk(tree)
            if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]

# ─── test 01: DB schema ───────────────────────────────────────────────────────

def test_01_db():
    section("test_01_db: Database & Schema")
    PG = "them-postgres"

    r = dexec(PG, "psql", "-U", "them", "-d", "them", "-tAc", "SELECT 1")
    check("DB connectivity", r.strip() == "1")

    r = dexec(PG, "psql", "-U", "them", "-d", "them", "-tAc",
              "SELECT count(*) FROM information_schema.schemata WHERE schema_name='them'")
    check("them schema exists", r.strip() == "1")

    for tbl in ("llm_providers","config","agents","orchestrators",
                "access_tokens","runs","run_steps","run_usage","audit_logs"):
        r = dexec(PG, "psql", "-U", "them", "-d", "them", "-tAc",
                  f"SELECT count(*) FROM information_schema.tables "
                  f"WHERE table_schema='them' AND table_name='{tbl}'")
        check(f"table them.{tbl} exists", r.strip() == "1")

# ─── test 02: Redis ───────────────────────────────────────────────────────────

def test_02_redis():
    section("test_02_redis: Redis Connectivity")
    R = "them-redis"
    r = dexec(R, "redis-cli", "-n", "0", "PING")
    check("Redis PING", r.strip() == "PONG")
    dexec(R, "redis-cli", "-n", "0", "SET", "them:test:canary", "ok", "EX", "10")
    r = dexec(R, "redis-cli", "-n", "0", "GET", "them:test:canary")
    check("Redis DB 0 read/write", r.strip() == "ok")
    dexec(R, "redis-cli", "-n", "0", "DEL", "them:test:canary")

# ─── test 03: auth service health ─────────────────────────────────────────────

def test_03_auth_service():
    section("test_03_auth_service: Auth Service Health")
    C, P = "them-auth-service", 8701
    for path in ("/health", "/health/live", "/health/ready"):
        s = http_status(C, path, P)
        check(f"GET {path} returns 200", s == "200", f"got {s}")
    d = http_json(C, "/health", P)
    check("health status=ok", d.get("status") == "ok", str(d))

# ─── test 04: bridge health ───────────────────────────────────────────────────

def test_04_bridge_health():
    section("test_04_bridge_health: Bridge Health")
    C, P = "them-bridge", 8001
    for path in ("/health", "/health/live", "/health/ready"):
        s = http_status(C, path, P)
        check(f"GET {path} returns 200", s == "200", f"got {s}")
    d = http_json(C, "/health", P)
    check("health status=ok", d.get("status") == "ok", str(d))

# ─── test 05: agents API CRUD ─────────────────────────────────────────────────

def test_05_agents_api():
    section("test_05_agents_api: Agents CRUD")
    C, P = "them-bridge", 8001
    BASE = "/api/v1/admin/agents"

    s = http_status(C, BASE, P)
    check("GET /admin/agents returns 200", s == "200", f"got {s}")

    body = json.dumps({
        "slug": "test_smoke_agent", "display_name": "Smoke Test Agent",
        "description": "Temp agent for test_05", "transport": "a2a_async",
        "endpoint_url": "http://localhost:9999/", "auth_token": "test-token-abc123",
        "timeout_seconds": 60, "max_concurrency": 2, "tags": ["test", "smoke"],
    })
    d = http_json(C, BASE, P, method="POST", body=body)
    agent_id = d.get("id", "")
    check("POST creates agent", d.get("slug") == "test_smoke_agent", str(d))
    check("auth_token_set=True", d.get("auth_token_set") is True, str(d))

    if not agent_id:
        check("agent ID present (skipping remaining)", False); return

    s = http_status(C, f"{BASE}/{agent_id}", P)
    check("GET /admin/agents/{id} returns 200", s == "200", f"got {s}")

    d = http_json(C, f"{BASE}/{agent_id}", P, method="PATCH",
                  body='{"display_name":"Smoke Test Agent (updated)"}')
    check("PATCH updates display_name",
          d.get("display_name") == "Smoke Test Agent (updated)", str(d))

    s = http_status(C, BASE, P, method="POST", body=body)
    check("POST duplicate slug returns 409", s == "409", f"got {s}")

    s = http_status(C, BASE, P, method="POST",
                    body='{"slug":"x","display_name":"x","description":"x","transport":"invalid","endpoint_url":"ws://x"}')
    check("POST invalid transport returns 422", s == "422", f"got {s}")

    s = http_status(C, f"{BASE}/{agent_id}", P, method="DELETE")
    check("DELETE returns 204", s == "204", f"got {s}")

    s = http_status(C, f"{BASE}/{agent_id}", P)
    check("GET deleted agent returns 404", s == "404", f"got {s}")

# ─── test 06: orchestrators API CRUD ─────────────────────────────────────────

def test_06_orchestrators_api():
    section("test_06_orchestrators_api: Orchestrators CRUD")
    C, P = "them-bridge", 8001
    BASE = "/api/v1/admin/orchestrators"

    s = http_status(C, BASE, P)
    check("GET /admin/orchestrators returns 200", s == "200", f"got {s}")

    body = json.dumps({
        "name": "test_smoke_orch", "display_name": "Smoke Test Orchestrator",
        "system_prompt": "You are a smoke test.", "allowed_agent_ids": [],
        "max_iterations": 5, "max_parallel_tools": 2,
        "rate_limit_rpm": 10, "daily_budget_usd": "1.00",
    })
    d = http_json(C, BASE, P, method="POST", body=body)
    orch_id = d.get("id", "")
    check("POST creates orchestrator", d.get("name") == "test_smoke_orch", str(d))

    if not orch_id:
        check("orchestrator ID present (skipping remaining)", False); return

    s = http_status(C, f"{BASE}/{orch_id}", P)
    check("GET /admin/orchestrators/{id} returns 200", s == "200", f"got {s}")

    d = http_json(C, f"{BASE}/{orch_id}", P, method="PATCH",
                  body='{"display_name":"Smoke Orch (updated)","max_iterations":8}')
    check("PATCH updates max_iterations", d.get("max_iterations") == 8, str(d))

    s = http_status(C, BASE, P, method="POST", body=body)
    check("POST duplicate name returns 409", s == "409", f"got {s}")

    s = http_status(C, f"{BASE}/{orch_id}", P, method="DELETE")
    check("DELETE returns 204", s == "204", f"got {s}")

    s = http_status(C, f"{BASE}/{orch_id}", P)
    check("GET deleted orchestrator returns 404", s == "404", f"got {s}")

# ─── test 07: adapter factory (structural) ────────────────────────────────────

def test_07_adapter_factory():
    section("test_07_adapter_factory: Adapter Factory & Contract")
    sys.path.insert(0, str(ROOT))

    # AdapterEvent — base types
    try:
        from app.adapters.base import AdapterEvent, AgentAdapter
        e = AdapterEvent(type="token", text="hello")
        check("AdapterEvent(type='token') created", e.type == "token" and e.text == "hello")
        e2 = AdapterEvent(type="done", result="out")
        check("AdapterEvent(type='done') created", e2.type == "done")
        e3 = AdapterEvent(type="error", error="boom")
        check("AdapterEvent(type='error') created", e3.type == "error")
    except Exception as exc:
        check("AdapterEvent import", False, str(exc)); return

    # AdapterEvent — Phase 4 extended types
    try:
        from app.adapters.base import AdapterEvent
        e4 = AdapterEvent(type="task_created", remote_task_id="abc-123")
        check("AdapterEvent(type='task_created') has remote_task_id", e4.remote_task_id == "abc-123")
        e5 = AdapterEvent(type="status", state="working")
        check("AdapterEvent(type='status') has state", e5.state == "working")
        e6 = AdapterEvent(type="artifact", artifact={"parts": []})
        check("AdapterEvent(type='artifact') has artifact dict", isinstance(e6.artifact, dict))
        e7 = AdapterEvent(type="status", state="input-required", input_required=True)
        check("AdapterEvent input_required flag", e7.input_required is True)
    except Exception as exc:
        check("AdapterEvent Phase 4 types", False, str(exc))

    # Factory — only a2a_async (Phase 8.1)
    try:
        import os as _os
        from app.adapters.factory import get_adapter
        from app.adapters.a2a_async_adapter import A2aAsyncAdapter
        from unittest.mock import MagicMock

        def make(transport, url="http://localhost:9999/"):
            a = MagicMock()
            a.transport = transport
            a.slug = "t"
            a.endpoint_url = url
            a.auth_token_encrypted = None
            a.supports_streaming = False
            return a

        check("get_adapter('a2a_async') returns A2aAsyncAdapter",
              isinstance(get_adapter(make("a2a_async")), A2aAsyncAdapter))

        for dead in ("omni_ws", "a2a", "ftp"):
            raised = False
            try: get_adapter(make(dead))
            except ValueError: raised = True
            check(f"get_adapter('{dead}') raises ValueError", raised)

        check("omni_ws_adapter.py deleted",
              not _os.path.exists(_os.path.join(_os.path.dirname(__file__), "../../app/adapters/omni_ws_adapter.py")))
        check("a2a_adapter.py deleted",
              not _os.path.exists(_os.path.join(_os.path.dirname(__file__), "../../app/adapters/a2a_adapter.py")))

    except ImportError as exc:
        skip(f"factory tests — missing deps: {exc}")

    # A2aAsyncAdapter error on unreachable
    async def _test_a2a_async():
        from app.adapters.a2a_async_adapter import A2aAsyncAdapter
        adapter = A2aAsyncAdapter(agent_slug="test", endpoint_url="http://localhost:19999",
                                  auth_token_encrypted=None, max_poll_seconds=3)
        events = []
        async for ev in adapter.stream_invoke({"message": "hi"}, timeout=3):
            events.append(ev)
        return len(events) > 0 and events[-1].type == "error"

    try:
        result = asyncio.run(_test_a2a_async())
        check("A2aAsyncAdapter yields error event on unreachable endpoint", result)
    except Exception as exc:
        check("A2aAsyncAdapter error event", False, str(exc))

    try:
        from app.adapters.base import AgentAdapter
        import inspect
        check("AgentAdapter is abstract", inspect.isabstract(AgentAdapter))
    except Exception as exc:
        check("AgentAdapter abstractness", False, str(exc))

# ─── test 08: tokens API CRUD ─────────────────────────────────────────────────

def test_08_tokens_api():
    section("test_08_tokens_api: Access Tokens CRUD")
    C, P = "them-bridge", 8001
    BASE = "/api/v1/admin/tokens"

    s = http_status(C, BASE, P)
    check("GET /admin/tokens returns 200", s == "200", f"got {s}")

    d = http_json(C, BASE, P, method="POST",
                  body='{"label":"smoke-test-token","user_id":1}')
    token_id = d.get("id", "")
    token_val = d.get("token", "")
    check("POST creates token", d.get("label") == "smoke-test-token", str(d))
    check("token plaintext returned (len > 20)", len(token_val) > 20, token_val[:10])
    check("token enabled=True", d.get("enabled") is True, str(d))

    if not token_id:
        check("token ID present (skipping remaining)", False); return

    s = http_status(C, f"{BASE}/{token_id}", P)
    check("GET /admin/tokens/{id} returns 200", s == "200", f"got {s}")

    d = http_json(C, f"{BASE}/{token_id}", P, method="PATCH", body='{"enabled":false}')
    check("PATCH disables token", d.get("enabled") is False, str(d))

    d = http_json(C, f"{BASE}/{token_id}", P, method="PATCH", body='{"enabled":true}')
    check("PATCH re-enables token", d.get("enabled") is True, str(d))

    s = http_status(C, f"{BASE}/{token_id}", P, method="DELETE")
    check("DELETE returns 204", s == "204", f"got {s}")

    s = http_status(C, f"{BASE}/{token_id}", P)
    check("GET deleted token returns 404", s == "404", f"got {s}")

# ─── test 09: rate limiter (structural) ───────────────────────────────────────

def test_09_rate_limiter():
    section("test_09_rate_limiter: Rate Limiter & Token Cache")
    sys.path.insert(0, str(ROOT))

    try:
        fake_db = types.ModuleType("app.database")
        fake_db.redis_client = None
        sys.modules.setdefault("app.database", fake_db)
        log_mod = types.ModuleType("app.utils.logger")
        log_mod.logger = type("L", (), {
            "warning": lambda s, *a, **k: None,
            "error":   lambda s, *a, **k: None,
            "info":    lambda s, *a, **k: None,
        })()
        sys.modules.setdefault("app.utils.logger", log_mod)

        from app.services.rate_limiter import _slot, check_rate_limit, get_current_count

        slot = _slot()
        check("_slot() returns int", isinstance(slot, int))
        check("_slot() is current hour", slot == int(time.time()) // 3600)

        result = asyncio.run(check_rate_limit(user_id=1, limit_rpm=10))
        check("check_rate_limit allows when Redis=None", result[0] is True)

        result = asyncio.run(check_rate_limit(user_id=1, limit_rpm=0))
        check("check_rate_limit allows when limit=0 (disabled)", result[0] is True)

        count = asyncio.run(get_current_count(user_id=1))
        check("get_current_count returns 0 when Redis=None", count == 0)

    except ImportError as exc:
        skip(f"rate_limiter — missing deps: {exc}")
    except Exception as exc:
        check("rate_limiter", False, str(exc))

    token = "test-token-abc123"
    h = hashlib.sha256(token.encode()).hexdigest()
    check("sha256 hash is 64 chars", len(h) == 64)
    check("sha256 hash is deterministic", h == hashlib.sha256(token.encode()).hexdigest())
    check("different tokens produce different hashes",
          hashlib.sha256(b"a").hexdigest() != hashlib.sha256(b"b").hexdigest())

    try:
        s = src("app/_deps.py")
        tree = ast.parse(s)
        fns = [n.name for n in ast.walk(tree) if isinstance(n, ast.AsyncFunctionDef)]
        check("require_jwt defined in _deps.py", "require_jwt" in fns)
        check("require_admin defined in _deps.py", "require_admin" in fns)
        check("require_bearer defined in _deps.py", "require_bearer" in fns)
    except Exception as exc:
        check("_deps.py structure", False, str(exc))

# ─── test 10: run recorder + task_runner + task_store (structural) ────────────

def test_10_run_recorder():
    section("test_10_run_recorder: Run Recorder & Task Runner Structure")

    # 1. run_recorder
    try:
        fns = funcs_in("app/services/run_recorder.py")
        for fn in ("start_run","record_step","complete_step","record_usage","complete_run"):
            check(f"{fn} defined", fn in fns)
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
        s = src("app/routers/ws_orchestrator.py")
        check("ws_orchestrate route defined", "ws_orchestrate" in s)
        check("WebSocket imported", "WebSocket" in s)
        check("Bearer token parsing present", "_parse_bearer" in s)
        check("task_runner imported", "task_runner" in s)
        check("WebSocketDisconnect handled", "WebSocketDisconnect" in s)
    except Exception as exc:
        check("ws_orchestrator structure", False, str(exc))

    # 5. task_runner yields correct WS event types
    try:
        s = src("app/services/task_runner.py")
        for ev in ("ready","done","token","error"):
            check(f"'type':'{ev}' event present", f'"ready"' in s if ev == "ready" else f'"{ev}"' in s)
        check("'type':'tool_start' event present", "tool_start" in s)
        check("'type':'tool_done' event present", "tool_done" in s)
        check("task_id in run events", "task_id" in s)
        check("context_id in run events", "context_id" in s)
    except Exception as exc:
        check("WS event types", False, str(exc))

    # 6. main.py wires ws_orchestrator and a2a_server
    try:
        s = src("app/main.py")
        check("ws_orchestrator imported in main.py", "ws_orchestrator" in s)
        check("ws_orchestrator.router included", "ws_orchestrator.router" in s)
        check("a2a_server imported in main.py", "a2a_server" in s)
        check("a2a_server.router included", "a2a_server.router" in s)
    except Exception as exc:
        check("main.py wiring", False, str(exc))

# ─── test 11: WS orchestrator endpoint ───────────────────────────────────────

def test_11_ws_orchestrate():
    section("test_11_ws_orchestrate: WS Orchestrator Endpoint")
    C, P = "them-bridge", 8001

    s = http_status(C, "/ws/orchestrate/test", P)
    check("WS route responds (not 500)",
          s in ("403","404","400","426"), f"got {s}")

    d = http_json(C, "/api/v1/admin/tokens", P, method="POST",
                  body='{"label":"ws-test-token","user_id":99}')
    token_id = d.get("id", "")
    check("Can create bearer token for WS auth", bool(token_id), str(d))

    if token_id:
        http_status(C, f"/api/v1/admin/tokens/{token_id}", P, method="DELETE")

    s = http_status(C, "/health/live", P)
    check("Bridge healthy after ws_orchestrator mount", s == "200", f"got {s}")

# ─── test 12: runs API ────────────────────────────────────────────────────────

def test_12_runs_api():
    section("test_12_runs_api: Runs API Auth")
    C, P = "them-bridge", 8001

    for path in ("/api/v1/runs", "/api/v1/runs/stats",
                 "/api/v1/runs/00000000-0000-0000-0000-000000000000"):
        s = http_status(C, path, P)
        check(f"GET {path} without auth → 401/403",
              s in ("401","403"), f"got {s}")

    s = http_status(C, "/api/v1/runs", P,
                    headers=["Authorization: Bearer bad-token"])
    check("GET /runs with bad JWT returns 401", s == "401", f"got {s}")

    s = http_status(C, "/health/live", P)
    check("Bridge healthy after runs router mount", s == "200", f"got {s}")

# ─── test 13: dashboard WS (structural) ───────────────────────────────────────

def test_13_dashboard_ws():
    section("test_13_dashboard_ws: Phase 6 Structure")

    try:
        fns = funcs_in("app/services/dashboard_broadcaster.py")
        for fn in ("publish","publish_run_started","publish_run_completed",
                   "publish_run_step","publish_agents_changed"):
            check(f"{fn} defined", fn in fns)
        s = src("app/services/dashboard_broadcaster.py")
        check("them:dash: prefix used", "them:dash:" in s)
    except Exception as exc:
        check("dashboard_broadcaster", False, str(exc))

    try:
        s = src("app/routers/ws_dashboard.py")
        check("ws_dashboard route defined", "ws_dashboard" in s)
        check("/ws/dashboard path present", "/ws/dashboard" in s)
        check("subscribe message type handled", '"subscribe"' in s)
        check("valid channels defined", "_STATIC_CHANNELS" in s or "_VALID_CHANNELS" in s)
        check("ping loop implemented", "_ping_loop" in s)
        check("JWT auth present", "validate_jwt" in s)
        check("WebSocketDisconnect handled", "WebSocketDisconnect" in s)
        check("channel relay to client", '"channel"' in s)
    except Exception as exc:
        check("ws_dashboard", False, str(exc))

    try:
        fns = funcs_in("app/routers/runs.py")
        for fn in ("list_runs","get_run","run_stats","delete_run"):
            check(f"{fn} defined", fn in fns)
        s = src("app/routers/runs.py")
        check("require_jwt used in runs", "require_jwt" in s)
        check("RunDetailOut includes steps+usage", "steps" in s and "usage" in s)
        check("admin role check in delete", "admin" in s)
    except Exception as exc:
        check("runs.py", False, str(exc))

    try:
        s = src("app/main.py")
        check("ws_dashboard imported", "ws_dashboard" in s)
        check("runs imported", "from app.routers import runs" in s or "app.routers.runs" in s)
        check("ws_dashboard.router included", "ws_dashboard.router" in s)
        check("runs.router included", "runs.router" in s)
    except Exception as exc:
        check("main.py wiring", False, str(exc))

    try:
        s = src("app/services/dashboard_broadcaster.py")
        check("runs channel supported", '"runs"' in s or "'runs'" in s)
        check("agents channel supported", '"agents"' in s or "'agents'" in s)
    except Exception as exc:
        check("broadcaster channels", False, str(exc))

# ─── test 14: E2E orchestrate ─────────────────────────────────────────────────

def test_14_e2e_orchestrate():
    section("test_14_e2e_orchestrate: Live E2E Orchestration")
    admin_jwt = os.environ.get("ADMIN_JWT", "")
    if not admin_jwt:
        skip("ADMIN_JWT not set — get one via POST /auth/login then re-run with ADMIN_JWT=<token>")
        return

    C, P = "them-bridge", 8001
    auth = [f"Authorization: Bearer {admin_jwt}"]

    # 1. Create access token
    d = http_json(C, "/api/v1/admin/tokens", P, method="POST",
                  body='{"label":"e2e-test-token","user_id":1}',
                  headers=auth)
    bearer = d.get("token", "")
    token_id = d.get("id", "")
    check("Access token created", bool(bearer), str(d))

    # 2. Create agent + orchestrator
    agent_body = json.dumps({
        "slug": "e2e_echo_agent", "display_name": "E2E Echo Agent",
        "description": "Echoes back the input for testing", "transport": "a2a_async",
        "endpoint_url": "http://localhost:9999/",
        "timeout_seconds": 5, "max_concurrency": 1,
    })
    d = http_json(C, "/api/v1/admin/agents", P, method="POST",
                  body=agent_body, headers=auth)
    agent_id = d.get("id", "")
    check("Agent created", bool(agent_id), str(d))

    orch_body = json.dumps({
        "name": "e2e_test_orch", "display_name": "E2E Test Orchestrator",
        "system_prompt": "You are a helpful assistant.",
        "allowed_agent_ids": [agent_id] if agent_id else [],
        "max_iterations": 2, "max_parallel_tools": 1,
    })
    d = http_json(C, "/api/v1/admin/orchestrators", P, method="POST",
                  body=orch_body, headers=auth)
    orch_id = d.get("id", "")
    check("Orchestrator created", bool(orch_id), str(d))

    # 3. WS route reachable
    s = http_status(C, "/ws/orchestrate/e2e_test_orch", P, headers=auth)
    check("WS route reachable (not 500)", s != "500", f"got {s}")

    # 4. Runs API
    d = http_json(C, "/api/v1/runs", P, headers=auth)
    check("Runs list returns list/items", isinstance(d, list) or "items" in d, str(d)[:80])
    d = http_json(C, "/api/v1/runs/stats", P, headers=auth)
    check("Runs stats returns total field", "total" in d, str(d)[:80])

    # 5. Cleanup
    for path, label in (
        (f"/api/v1/admin/orchestrators/{orch_id}", "Orchestrator deleted"),
        (f"/api/v1/admin/agents/{agent_id}",       "Agent deleted"),
        (f"/api/v1/admin/tokens/{token_id}",        "Token deleted"),
    ):
        if path.endswith("/"):
            continue
        s = http_status(C, path, P, method="DELETE", headers=auth)
        check(label, s in ("200","204"), f"got {s}")

# ─── test 15: compose health ─────────────────────────────────────────────────

def test_15_compose_health():
    section("test_15_compose_health: Container Health Check")

    core = ["them-postgres","them-redis","them-auth-service","them-bridge"]

    for name in core:
        status = docker("inspect", "--format={{.State.Status}}", name).strip()
        check(f"Container {name} running", status == "running", f"got '{status}'")

    for name in core:
        health = docker("inspect",
            "--format={{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}",
            name).strip()
        check(f"{name} healthcheck healthy",
              health in ("healthy","no-healthcheck"), f"got '{health}'")

    print()
    print("── HTTP health endpoints")
    s = http_status("them-auth-service", "/health/live", 8701)
    check("Auth service /health/live = 200", s == "200", f"got {s}")

    for path in ("/health/live", "/health/ready"):
        s = http_status("them-bridge", path, 8001)
        check(f"Bridge {path} = 200", s == "200", f"got {s}")

    d = http_json("them-bridge", "/health", 8001)
    check("Bridge /health status=ok", d.get("status") == "ok", str(d))

    print()
    print("── Network connectivity")

    pg = dexec("them-bridge", "python3", "-c",
               "import socket; s=socket.create_connection(('them-postgres',5432),3); s.close(); print('ok')")
    check("Bridge → them-postgres (TCP)", pg.strip() == "ok")

    rd = dexec("them-bridge", "python3", "-c",
               "import socket; s=socket.create_connection(('them-redis',6379),3); s.close(); print('ok')")
    check("Bridge → them-redis (TCP)", rd.strip() == "ok")

    s = http_status("them-bridge", "/health/live", 8701, host="them-auth-service")
    check("Bridge → them-auth-service HTTP", s == "200", f"got {s}")

# ─── test 16: A2A agent structure ────────────────────────────────────────────

def test_16_a2a_agents():
    section("test_16_a2a_agents: A2A Test Agents Structure")
    sys.path.insert(0, str(ROOT))

    agents = ("a2a_echo", "a2a_slow", "a2a_stream")

    # File existence
    for agent in agents:
        path = ROOT / f"agents/{agent}"
        check(f"agents/{agent}/ exists", path.is_dir())
        check(f"agents/{agent}/main.py exists", (path / "main.py").exists())
        check(f"agents/{agent}/Dockerfile exists", (path / "Dockerfile").exists())
        check(f"agents/{agent}/requirements.txt exists", (path / "requirements.txt").exists())

    # Structure of each main.py
    for agent in agents:
        try:
            agent_src = (ROOT / f"agents/{agent}/main.py").read_text(encoding="utf-8")
            tree = ast.parse(agent_src)
            fns = [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]
            check(f"{agent}: execute defined", "execute" in fns)
            check(f"{agent}: cancel defined", "cancel" in fns)
            check(f"{agent}: make_agent_card defined", "make_agent_card" in fns)
            check(f"{agent}: create_app defined", "create_app" in fns)
            check(f"{agent}: AgentExecutor used", "AgentExecutor" in agent_src)
            check(f"{agent}: add_a2a_routes_to_fastapi used", "add_a2a_routes_to_fastapi" in agent_src)
            check(f"{agent}: capabilities in agent card", "capabilities" in agent_src)
        except Exception as exc:
            check(f"{agent} structure parse", False, str(exc))

    # a2a-stream must advertise streaming=True
    try:
        stream_src = (ROOT / "agents/a2a_stream/main.py").read_text(encoding="utf-8")
        check("a2a-stream: capabilities.streaming = True", ".streaming = True" in stream_src)
    except Exception as exc:
        check("a2a-stream streaming flag", False, str(exc))

    # docker-compose.yml has test-agents profile
    try:
        compose_src = (ROOT / "docker-compose.yml").read_text(encoding="utf-8")
        check("docker-compose: a2a-echo service", "a2a-echo" in compose_src)
        check("docker-compose: a2a-slow service", "a2a-slow" in compose_src)
        check("docker-compose: a2a-stream service", "a2a-stream" in compose_src)
        check("docker-compose: test-agents profile", "test-agents" in compose_src)
    except Exception as exc:
        check("docker-compose structure", False, str(exc))

    # db/002_seed.sql has a2a agent seeds
    try:
        seed_src = (ROOT / "db/002_seed.sql").read_text(encoding="utf-8")
        check("seed: a2a_echo row", "'a2a_echo'" in seed_src)
        check("seed: a2a_slow row", "'a2a_slow'" in seed_src)
        check("seed: a2a_stream row", "'a2a_stream'" in seed_src)
        check("seed: transport a2a_async", "a2a_async" in seed_src)
        check("seed: supports_streaming column", "supports_streaming" in seed_src)
    except Exception as exc:
        check("002_seed.sql structure", False, str(exc))

    # A2aAsyncAdapter still importable (no regression)
    try:
        from app.adapters.a2a_async_adapter import A2aAsyncAdapter
        check("A2aAsyncAdapter importable", True)
    except Exception as exc:
        check("A2aAsyncAdapter import", False, str(exc))

    # requirements.txt lists a2a-sdk for each agent
    for agent in agents:
        try:
            req = (ROOT / f"agents/{agent}/requirements.txt").read_text(encoding="utf-8")
            check(f"{agent}: requirements include a2a-sdk", "a2a-sdk" in req)
        except Exception as exc:
            check(f"{agent} requirements.txt", False, str(exc))

def test_17_memory():
    section("test_17_memory: Phase 8.4 Context Summarization Memory")
    sys.path.insert(0, str(ROOT))

    def src(path): return (ROOT / path).read_text(encoding="utf-8")
    def fns(path):
        tree = ast.parse(src(path))
        return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]

    # memory_service.py
    try:
        s = src("app/services/memory_service.py")
        fn_names = fns("app/services/memory_service.py")
        check("memory_service.py exists", True)
        check("get_injected_context defined", "get_injected_context" in fn_names)
        check("summarize_context defined", "summarize_context" in fn_names)
        check("resolve_summarizer defined", "resolve_summarizer" in fn_names)
        check("Redis key prefix them:ctx:", "them:ctx:" in s)
        check("summary TTL defined", "_SUMMARY_TTL" in s)
        check("never raises in summarize_context", "return None" in s)
        check("raw artifacts preserved (no DB delete)", ".delete(" not in s and "DELETE FROM" not in s)
    except Exception as exc:
        check("memory_service.py", False, str(exc))

    # models.py
    try:
        s = src("app/models.py")
        check("memory_enabled column", "memory_enabled" in s)
        check("summarize_every_n_calls column", "summarize_every_n_calls" in s)
        check("memory_raw_fallback_n column", "memory_raw_fallback_n" in s)
        check("summarizer_provider column", "summarizer_provider" in s)
        check("summarizer_model column", "summarizer_model" in s)
        check("summarizer_api_key_encrypted column", "summarizer_api_key_encrypted" in s)
    except Exception as exc:
        check("models.py memory columns", False, str(exc))

    # admin_orchestrators.py
    try:
        s = src("app/routers/admin_orchestrators.py")
        check("OrchestratorCreate has memory_enabled", "memory_enabled" in s)
        check("OrchestratorOut has memory_enabled", s.count("memory_enabled") >= 2)
        check("summarize_every_n_calls in router", "summarize_every_n_calls" in s)
        check("summarizer_provider in router", "summarizer_provider" in s)
    except Exception as exc:
        check("admin_orchestrators.py memory fields", False, str(exc))

    # task_runner.py
    try:
        s = src("app/services/task_runner.py")
        check("memory_service imported", "memory_service" in s)
        check("get_injected_context called", "get_injected_context" in s)
        check("summarize_context called", "summarize_context" in s)
        check("agent_calls_since_summary tracked", "agent_calls_since_summary" in s)
        check("memory_enabled checked", "memory_enabled" in s)
        check("summarize_every_n_calls checked", "summarize_every_n_calls" in s)
        check("injected context prepended", "_injected_ctx" in s)
    except Exception as exc:
        check("task_runner.py memory integration", False, str(exc))

    # REDIS.md
    try:
        s = src("docs/REDIS.md")
        check("them:ctx: summary key documented", "them:ctx:" in s and ":summary" in s)
        check("memory_service.py listed as owner", "memory_service" in s)
    except Exception as exc:
        check("REDIS.md", False, str(exc))

    # db/003_phase8.sql
    try:
        s = src("db/003_phase8.sql")
        check("003_phase8.sql has memory_enabled", "memory_enabled" in s)
        check("003_phase8.sql has summarize_every_n_calls", "summarize_every_n_calls" in s)
        check("003_phase8.sql has summarizer_provider", "summarizer_provider" in s)
    except Exception as exc:
        check("db/003_phase8.sql", False, str(exc))

    # frontend types
    try:
        s = src("frontend/src/lib/api.ts")
        check("OrchestratorFull has memory_enabled", "memory_enabled" in s)
        check("OrchestratorFull has summarize_every_n_calls", "summarize_every_n_calls" in s)
        check("OrchestratorFull has summarizer_provider", "summarizer_provider" in s)
    except Exception as exc:
        check("frontend/src/lib/api.ts memory fields", False, str(exc))


def test_18_orch_as_agent():
    section("test_18_orch_as_agent: Phase 8.5 Durable Inbound A2A")
    sys.path.insert(0, str(ROOT))

    def src(path): return (ROOT / path).read_text(encoding="utf-8")
    def fns(path):
        tree = ast.parse(src(path))
        return [n.name for n in ast.walk(tree) if isinstance(n, (ast.AsyncFunctionDef, ast.FunctionDef))]

    # _tasks dict deleted
    try:
        s = src("app/routers/a2a_server.py")
        check("_tasks in-memory dict deleted", "_tasks: dict" not in s and "_tasks = {}" not in s)
        check("_tasks[task_id] assignment deleted", "_tasks[task_id]" not in s)
    except Exception as exc:
        check("a2a_server.py _tasks removal", False, str(exc))

    # Required functions
    try:
        fn_names = fns("app/routers/a2a_server.py")
        check("_handle_send_message defined", "_handle_send_message" in fn_names)
        check("_handle_get_task defined", "_handle_get_task" in fn_names)
        check("_handle_cancel_task defined", "_handle_cancel_task" in fn_names)
        check("_run_and_finalize defined", "_run_and_finalize" in fn_names)
        check("agent_card defined", "agent_card" in fn_names)
        check("a2a_rpc defined", "a2a_rpc" in fn_names)
        check("a2a_push defined", "a2a_push" in fn_names)
        check("_task_to_a2a defined", "_task_to_a2a" in fn_names)
    except Exception as exc:
        check("a2a_server.py functions", False, str(exc))

    # Durable task_store usage
    try:
        s = src("app/routers/a2a_server.py")
        check("task_store.create_task called", "task_store.create_task" in s)
        check("task_store.get_task called", "task_store.get_task" in s)
        check("task_store.transition called", "task_store.transition" in s)
        check("task_store.get_context_artifacts called", "task_store.get_context_artifacts" in s)
    except Exception as exc:
        check("task_store usage", False, str(exc))

    # Async detach / returnImmediately
    try:
        s = src("app/routers/a2a_server.py")
        check("returnImmediately honored", "returnImmediately" in s)
        check("asyncio.create_task used for detach", "asyncio.create_task" in s)
    except Exception as exc:
        check("async detach", False, str(exc))

    # Recursion guard
    try:
        s = src("app/routers/a2a_server.py")
        check("_MAX_TASKS_PER_CONTEXT defined", "_MAX_TASKS_PER_CONTEXT" in s)
    except Exception as exc:
        check("recursion guard", False, str(exc))

    # Bridge URL from config
    try:
        s = src("app/routers/a2a_server.py")
        check("bridge_url from config", "bridge_url" in s)
        check("hardcoded localhost:8001 removed from card", 'url": "http://localhost:8001"' not in s)
        s2 = src("app/config.py")
        check("BRIDGE_URL in Settings", "BRIDGE_URL" in s2)
        check("bridge_url in GlobalConfig", "bridge_url" in s2)
    except Exception as exc:
        check("config BRIDGE_URL", False, str(exc))

    # Push webhook
    try:
        s = src("app/routers/a2a_server.py")
        check("/a2a/push/{task_id} route present", "/a2a/push/{task_id}" in s)
        check("terminal guard in push webhook", "_TERMINAL" in s)
    except Exception as exc:
        check("push webhook", False, str(exc))

    # Models
    try:
        s = src("app/models.py")
        check("Orchestrator.a2a_exposed column", "a2a_exposed" in s)
        check("Orchestrator.budget_tokens column", "budget_tokens" in s)
    except Exception as exc:
        check("models.py", False, str(exc))

    # Migration
    try:
        s = src("db/003_phase8.sql")
        check("budget_tokens in migration", "budget_tokens" in s)
    except Exception as exc:
        check("db/003_phase8.sql", False, str(exc))

    # main.py wiring
    try:
        s = src("app/main.py")
        check("a2a_server imported in main.py", "a2a_server" in s)
        check("a2a_server.router included", "a2a_server.router" in s)
    except Exception as exc:
        check("main.py wiring", False, str(exc))


# ─── runner ───────────────────────────────────────────────────────────────────

ALL_TESTS = [
    ("01", test_01_db),
    ("02", test_02_redis),
    ("03", test_03_auth_service),
    ("04", test_04_bridge_health),
    ("05", test_05_agents_api),
    ("06", test_06_orchestrators_api),
    ("07", test_07_adapter_factory),
    ("08", test_08_tokens_api),
    ("09", test_09_rate_limiter),
    ("10", test_10_run_recorder),
    ("11", test_11_ws_orchestrate),
    ("12", test_12_runs_api),
    ("13", test_13_dashboard_ws),
    ("14", test_14_e2e_orchestrate),
    ("15", test_15_compose_health),
    ("16", test_16_a2a_agents),
    ("17", test_17_memory),
    ("18", test_18_orch_as_agent),
]

if __name__ == "__main__":
    # Filter to specific tests if requested
    filter_ids = set(sys.argv[1:]) if len(sys.argv) > 1 else None
    # Strip leading -- in case user passes --test flags
    filter_ids = {a.lstrip("-") for a in filter_ids} if filter_ids else None

    print(bold("========================================"))
    print(bold("  the-M Test Suite (cross-platform)"))
    print(bold("========================================"))

    for tid, fn in ALL_TESTS:
        if filter_ids and tid not in filter_ids:
            continue
        fn()

    print(f"\n{bold('========================================')}")
    summary = f"  Total: {green(str(PASS))} passed"
    if FAIL:  summary += f", {red(str(FAIL))} failed"
    if SKIP:  summary += f", {yellow(str(SKIP))} skipped"
    print(bold("========================================"))
    print(summary)
    print(bold("========================================"))
    sys.exit(0 if FAIL == 0 else 1)
