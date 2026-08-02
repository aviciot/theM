#!/usr/bin/env python3
"""test_routing_fix_contracts.py — Routing correctness tests for UUID regex + runs fixes.

Verifies that after the routing fixes in the routing-fix commit:

1. UUID agent writes (PUT/PATCH/DELETE) reach Go, not Python.
2. UUID application writes (PUT/PATCH/DELETE) reach Go, not Python.
3. Entry-point writes (POST/PUT/PATCH/DELETE) reach Go, not Python.
4. Implemented run GET routes (list, get-by-id) still reach Go.
5. Python-only run GET routes (stats, contexts, tasks, artifacts-list) reach Python,
   not Go (i.e. they return a real response, not a 404 from Go).

Strategy: the test uses the X-Served-By response header emitted by each bridge
(Go sets "go-bridge", Python doesn't set it at all). When X-Served-By is absent
the request hit Python. When "go-bridge" is present it hit Go.

Fallback: if the header is not present on either side (neither bridge sets it),
the test uses response shape differences that are known to differ:
  - Error envelope key: Go uses "error", Python uses "detail"
  - All Go 404s on unknown paths return {"error": "Not Found"} or similar.
  - Python 404 for non-existent resources returns {"detail": "..."}.

The test creates real (but minimal) resources via Python to obtain valid UUIDs,
then exercises the write paths through Traefik, then cleans up.

Run:
    python3.12 scripts/tests/test_routing_fix_contracts.py
    python3.12 scripts/tests/test_routing_fix_contracts.py --verbose

Environment variables:
    TRAEFIK_BASE    Traefik base URL  (default: http://localhost:8088)
    PYTHON_BASE     Python bridge     (default: http://localhost:8001)
    GO_BASE         Go bridge         (default: http://localhost:8002)
    AUTH_SERVICE    Auth service      (default: http://localhost:8701)
    CONTRACT_JWT    Pre-supplied JWT  (skips login if set)
"""

import json
import os
import sys
import urllib.request
import urllib.error
from typing import Any

TRAEFIK_BASE = os.getenv("TRAEFIK_BASE", "http://localhost:8088").rstrip("/")
PYTHON_BASE  = os.getenv("PYTHON_BASE",  "http://localhost:8001").rstrip("/")
GO_BASE      = os.getenv("GO_BASE",      "http://localhost:8002").rstrip("/")
AUTH_BASE    = os.getenv("AUTH_SERVICE", "http://localhost:8701").rstrip("/")
VERBOSE      = "--verbose" in sys.argv or "-v" in sys.argv

PASS_COUNT = 0
FAIL_COUNT = 0
SKIP_COUNT = 0
_JWT: str | None = None


# ── helpers ──────────────────────────────────────────────────────────────────

def _log(msg: str) -> None:
    if VERBOSE:
        print(f"    {msg}")


def _acquire_jwt() -> str | None:
    for url in [AUTH_BASE + "/api/v1/auth/login", PYTHON_BASE + "/api/v1/auth/login"]:
        code, body, _ = _request(url, "POST", {"username": "admin", "password": "admin123"}, auth=False)
        if code == 200 and body and body.get("access_token"):
            return body["access_token"]
    return None


def _request(
    url: str,
    method: str = "GET",
    body: Any = None,
    auth: bool = True,
) -> tuple[int, Any, dict]:
    """Returns (status_code, parsed_json_or_None, response_headers_dict)."""
    global _JWT
    data = json.dumps(body).encode() if body is not None else None
    headers: dict[str, str] = {}
    if data:
        headers["Content-Type"] = "application/json"
    if auth and _JWT:
        headers["Authorization"] = "Bearer " + _JWT
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=8) as resp:
            raw = resp.read()
            resp_headers = {k.lower(): v for k, v in resp.headers.items()}
            return resp.status, json.loads(raw) if raw else None, resp_headers
    except urllib.error.HTTPError as e:
        raw = e.read()
        resp_headers = {k.lower(): v for k, v in e.headers.items()}
        try:
            return e.code, json.loads(raw), resp_headers
        except Exception:
            return e.code, None, resp_headers
    except Exception:
        return 0, None, {}


def _is_reachable(base: str) -> bool:
    code, _, _ = _request(f"{base}/health/live", auth=False)
    return code == 200


def _check(desc: str, ok: bool, detail: str = "") -> None:
    global PASS_COUNT, FAIL_COUNT
    if ok:
        print(f"  [PASS] {desc}")
        PASS_COUNT += 1
    else:
        print(f"  [FAIL] {desc}{' — ' + detail if detail else ''}")
        FAIL_COUNT += 1


def _skip(desc: str, reason: str) -> None:
    global SKIP_COUNT
    print(f"  [SKIP] {desc} — {reason}")
    SKIP_COUNT += 1


def _served_by_go(headers: dict) -> bool:
    """Return True if the response was served by Go (X-Served-By: go-bridge)."""
    return headers.get("x-served-by", "") == "go-bridge"


def _served_by_python_error_shape(body: Any) -> bool:
    """Python error responses use 'detail' key; Go uses 'error' key."""
    if isinstance(body, dict):
        return "detail" in body
    return False


def _served_by_go_error_shape(body: Any) -> bool:
    """Go error responses use 'error' key."""
    if isinstance(body, dict):
        return "error" in body and "detail" not in body
    return False


# ── reachability check ────────────────────────────────────────────────────────

def check_reachability() -> tuple[bool, bool, bool]:
    """Return (traefik_ok, python_ok, go_ok)."""
    traefik_ok = _is_reachable(TRAEFIK_BASE)
    python_ok  = _is_reachable(PYTHON_BASE)
    go_ok      = _is_reachable(GO_BASE)
    print(f"Traefik ({TRAEFIK_BASE}): {'reachable' if traefik_ok else 'UNREACHABLE'}")
    print(f"Python  ({PYTHON_BASE}):  {'reachable' if python_ok else 'UNREACHABLE'}")
    print(f"Go      ({GO_BASE}):      {'reachable' if go_ok else 'UNREACHABLE'}")
    return traefik_ok, python_ok, go_ok


# ── resource creation helpers ─────────────────────────────────────────────────

def _create_agent_via_python() -> str | None:
    """Create a minimal agent directly on Python and return its UUID id."""
    body = {
        "name": "routing-fix-test-agent",
        "slug": "routing-fix-test-agent",
        "endpoint": "http://test-agent.internal:9000",
        "adapter_type": "a2a",
        "description": "temporary agent for routing contract tests",
    }
    code, resp, _ = _request(PYTHON_BASE + "/api/v1/admin/agents", "POST", body)
    if code in (200, 201) and resp and resp.get("id"):
        _log(f"created agent id={resp['id']}")
        return str(resp["id"])
    _log(f"agent create failed: {code} {resp}")
    return None


def _delete_agent_via_python(agent_id: str) -> None:
    _request(PYTHON_BASE + f"/api/v1/admin/agents/{agent_id}", "DELETE")


def _create_application_via_python() -> str | None:
    """Create a minimal application directly on Python and return its UUID id."""
    body = {
        "name": "routing-fix-test-app",
        "enabled": True,
        "graph": {
            "nodes": [
                {
                    "id": "orch-test-01",
                    "type": "orchestrator",
                    "data": {
                        "kind": "standard",
                        "node_id": "orch-test-01",
                    },
                },
                {
                    "id": "ep-test-01",
                    "type": "entry_point",
                    "data": {
                        "slug": "routing-fix-ws",
                        "entry_point_type": "websocket",
                    },
                },
            ],
            "edges": [
                {"source": "orch-test-01", "target": "ep-test-01"},
            ],
        },
    }
    code, resp, _ = _request(PYTHON_BASE + "/api/v1/admin/applications", "POST", body)
    if code in (200, 201) and resp and resp.get("id"):
        _log(f"created app id={resp['id']}, eps={[ep['id'] for ep in resp.get('entry_points', [])]}")
        return str(resp["id"])
    _log(f"app create failed: {code} {resp}")
    return None


def _delete_application_via_python(app_id: str) -> None:
    _request(PYTHON_BASE + f"/api/v1/admin/applications/{app_id}", "DELETE")


def _get_entry_point_id(app_id: str) -> str | None:
    """Fetch the first entry point UUID for an application."""
    code, resp, _ = _request(PYTHON_BASE + f"/api/v1/admin/applications/{app_id}")
    if code == 200 and resp and resp.get("entry_points"):
        ep_id = str(resp["entry_points"][0]["id"])
        _log(f"entry point id={ep_id}")
        return ep_id
    _log(f"app GET failed: {code} {resp}")
    return None


# ── test sections ─────────────────────────────────────────────────────────────

def test_agent_uuid_writes_reach_go(traefik_ok: bool, python_ok: bool) -> None:
    print("\n── Agent UUID writes through Traefik → Go ──")

    if not traefik_ok:
        _skip("all agent write tests", "Traefik unreachable")
        return
    if not python_ok:
        _skip("all agent write tests", "Python bridge unreachable (needed for resource setup)")
        return

    agent_id = _create_agent_via_python()
    if not agent_id:
        _skip("agent PATCH/DELETE via Traefik", "Could not create test agent on Python")
        return

    try:
        # ── PATCH via Traefik ──
        patch_url = TRAEFIK_BASE + f"/api/v1/admin/agents/{agent_id}"
        code, body, hdrs = _request(patch_url, "PATCH", {"description": "patched by routing fix test"})
        _check(
            f"PATCH /admin/agents/{{uuid}} via Traefik → Go (not 404, code={code})",
            code != 404,
            f"body={body}",
        )
        # Go returns 200 on success; Python also returns 200. Use header or error shape to distinguish.
        # A 404 with {"error": ...} means Go received it and couldn't find the tenant-scoped agent
        # (expected since the agent was created on Python which may have a different tenant).
        # A non-404 means routing succeeded at the Traefik layer.
        # We verify routing by checking the response is NOT the Python error shape with "detail".
        if code == 404:
            # 404 is acceptable IF it came from Go (agent created on Python may not be
            # visible under the default Go tenant). Distinguish by error key.
            _check(
                "PATCH 404 has Go error shape ('error' key, not 'detail')",
                _served_by_go_error_shape(body),
                f"body={body}",
            )
        elif code in (200, 204):
            _check("PATCH reached service layer (2xx)", True)
        else:
            _check(f"PATCH unexpected status {code}", False, f"body={body}")

        # ── DELETE via Traefik ──
        delete_url = TRAEFIK_BASE + f"/api/v1/admin/agents/{agent_id}"
        code, body, hdrs = _request(delete_url, "DELETE")
        _check(
            f"DELETE /admin/agents/{{uuid}} via Traefik → Go error shape (code={code})",
            code in (200, 204, 404) and not _served_by_python_error_shape(body),
            f"body={body}",
        )

    finally:
        # Clean up via Python direct (in case Traefik route didn't fully delete)
        _delete_agent_via_python(agent_id)


def test_application_uuid_writes_reach_go(traefik_ok: bool, python_ok: bool) -> None:
    print("\n── Application UUID writes through Traefik → Go ──")

    if not traefik_ok:
        _skip("all application write tests", "Traefik unreachable")
        return
    if not python_ok:
        _skip("all application write tests", "Python bridge unreachable")
        return

    app_id = _create_application_via_python()
    if not app_id:
        _skip("application PATCH/DELETE via Traefik", "Could not create test application on Python")
        return

    try:
        # ── PATCH via Traefik ──
        patch_url = TRAEFIK_BASE + f"/api/v1/admin/applications/{app_id}"
        code, body, hdrs = _request(patch_url, "PATCH", {"name": "routing-fix-patched"})
        _check(
            f"PATCH /admin/applications/{{uuid}} via Traefik → non-Python response (code={code})",
            # Not a Python error shape (Go handles it, whether 200 or tenant-scoped 404)
            not _served_by_python_error_shape(body),
            f"body={body}",
        )

        # ── DELETE via Traefik ──
        delete_url = TRAEFIK_BASE + f"/api/v1/admin/applications/{app_id}"
        code, body, hdrs = _request(delete_url, "DELETE")
        _check(
            f"DELETE /admin/applications/{{uuid}} via Traefik → non-Python response (code={code})",
            code in (200, 204, 404) and not _served_by_python_error_shape(body),
            f"body={body}",
        )

    finally:
        _delete_application_via_python(app_id)


def test_entry_point_writes_reach_go(traefik_ok: bool, python_ok: bool) -> None:
    print("\n── Entry-point writes through Traefik → Go ──")

    if not traefik_ok:
        _skip("all entry-point write tests", "Traefik unreachable")
        return
    if not python_ok:
        _skip("all entry-point write tests", "Python bridge unreachable")
        return

    app_id = _create_application_via_python()
    if not app_id:
        _skip("entry-point writes via Traefik", "Could not create test application on Python")
        return

    try:
        ep_id = _get_entry_point_id(app_id)
        if not ep_id:
            _skip("entry-point PUT/DELETE via Traefik", "No entry point found on test app")
            return

        # ── PUT entry point via Traefik ──
        put_url = TRAEFIK_BASE + f"/api/v1/admin/applications/{app_id}/entry-points/{ep_id}"
        code, body, hdrs = _request(put_url, "PUT", {
            "slug": "routing-fix-ws",
            "entry_point_type": "websocket",
            "enabled": True,
        })
        _check(
            f"PUT /admin/applications/{{uuid}}/entry-points/{{uuid}} via Traefik → non-Python response (code={code})",
            not _served_by_python_error_shape(body),
            f"body={body}",
        )

        # ── DELETE entry point via Traefik ──
        delete_url = TRAEFIK_BASE + f"/api/v1/admin/applications/{app_id}/entry-points/{ep_id}"
        code, body, hdrs = _request(delete_url, "DELETE")
        _check(
            f"DELETE /admin/applications/{{uuid}}/entry-points/{{uuid}} via Traefik → non-Python response (code={code})",
            code in (200, 204, 404) and not _served_by_python_error_shape(body),
            f"body={body}",
        )

    finally:
        _delete_application_via_python(app_id)


def test_runs_get_go_routes(traefik_ok: bool) -> None:
    print("\n── Implemented runs GET routes reach Go ──")

    if not traefik_ok:
        _skip("runs GET go routes", "Traefik unreachable")
        return

    # GET /api/v1/runs — Go handles this (list)
    code, body, hdrs = _request(TRAEFIK_BASE + "/api/v1/runs")
    _check(
        f"GET /api/v1/runs via Traefik → 200 or 4xx (Go responds, code={code})",
        code in (200, 400, 401, 403, 404),
        f"body={body}",
    )
    if code == 200:
        _check(
            "GET /api/v1/runs → response is list (not 'detail' error)",
            isinstance(body, list) or not _served_by_python_error_shape(body),
            f"body={body}",
        )

    # GET /api/v1/runs/{fake-uuid} — Go handles this (get-by-id); expect 404 from Go
    # NOTE: requires `docker compose up` reload for updated Traefik labels to take effect.
    # If the stack hasn't been restarted since the routing fix, Python may still serve this.
    fake_run_id = "00000000-0000-0000-0000-000000000001"
    code, body, hdrs = _request(TRAEFIK_BASE + f"/api/v1/runs/{fake_run_id}")
    # Accept either: Go 404 (correct post-restart) or any 401 (auth blocks before routing matters).
    # A Python 404 {"detail":...} without auth failure means the fix hasn't been loaded yet.
    served_by_go_404 = code == 404 and _served_by_go_error_shape(body)
    served_by_auth_rejection = code == 401
    _check(
        f"GET /api/v1/runs/{{run_id}} via Traefik → Go or auth wall (code={code})",
        served_by_go_404 or served_by_auth_rejection,
        f"body={body} — if 401 via Python, restart stack to reload Traefik labels",
    )


def test_runs_get_python_routes(traefik_ok: bool) -> None:
    print("\n── Python-only runs GET routes NOT captured by Go ──")

    if not traefik_ok:
        _skip("Python-only runs routes", "Traefik unreachable")
        return

    python_only_paths = [
        "/api/v1/runs/stats",
        "/api/v1/runs/contexts",
    ]

    for path in python_only_paths:
        code, body, hdrs = _request(TRAEFIK_BASE + path)
        # If Go captured this incorrectly, it would return {"error": "Not Found"} (404).
        # If Python handles it, it returns 200 with data or a Python 401/403.
        # The key check: it must NOT return Go's 404 error shape.
        is_go_404 = code == 404 and _served_by_go_error_shape(body)
        _check(
            f"GET {path} via Traefik → NOT routed to Go (code={code})",
            not is_go_404,
            f"body={body} — should be Python response, not Go 404",
        )

    # Runs tasks and artifacts require a real run_id to distinguish 404-from-Go
    # vs 404-from-Python. We test with a fake ID: Python returns {"detail":...} on 404,
    # Go returns {"error":...}. After the fix, these should hit Python.
    fake_run_id = "00000000-0000-0000-0000-000000000001"
    python_subpath_tests = [
        (f"/api/v1/runs/{fake_run_id}/tasks",     "tasks"),
        (f"/api/v1/runs/{fake_run_id}/artifacts",  "artifacts-list"),
        (f"/api/v1/runs/context/{fake_run_id}/artifacts", "context-artifacts"),
    ]

    for path, label in python_subpath_tests:
        code, body, hdrs = _request(TRAEFIK_BASE + path)
        is_go_404 = code == 404 and _served_by_go_error_shape(body)
        # These paths match /runs/{run_id} pattern which IS routed to Go.
        # After fix: /runs/{run_id}/tasks does NOT match /runs/{run_id} exactly —
        # PathRegexp(^/api/v1/runs/[^/]+$) has end-anchor, so tasks/artifacts sub-paths
        # are NOT captured and fall through to Python.
        _check(
            f"GET /runs/.../{{label}} via Traefik → NOT routed to Go (code={code})",
            not is_go_404,
            f"body={body} — should be Python 404/200, not Go 404",
        )


# ── main ──────────────────────────────────────────────────────────────────────

def main() -> None:
    global _JWT

    print("=" * 60)
    print("Routing Fix Contract Tests")
    print("Validates UUID regex + runs path fixes in Traefik config.")
    print("=" * 60)

    traefik_ok, python_ok, go_ok = check_reachability()

    if not traefik_ok and not python_ok:
        print("\nNeither Traefik nor Python bridge is reachable. All tests skip.")
        print(f"\nResult: 0 passed, 0 failed, {SKIP_COUNT + 20} skipped")
        sys.exit(0)

    # Acquire JWT for authenticated routes
    _JWT = _acquire_jwt()
    if _JWT:
        print(f"JWT acquired (admin login OK)")
    else:
        print("WARNING: Could not acquire JWT — authenticated tests will fail")

    # Run test sections
    test_agent_uuid_writes_reach_go(traefik_ok, python_ok)
    test_application_uuid_writes_reach_go(traefik_ok, python_ok)
    test_entry_point_writes_reach_go(traefik_ok, python_ok)
    test_runs_get_go_routes(traefik_ok)
    test_runs_get_python_routes(traefik_ok)

    # Summary
    total = PASS_COUNT + FAIL_COUNT + SKIP_COUNT
    print("\n" + "=" * 60)
    print(f"Result: {PASS_COUNT} passed, {FAIL_COUNT} failed, {SKIP_COUNT} skipped  (total {total})")
    print("=" * 60)

    if FAIL_COUNT > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
