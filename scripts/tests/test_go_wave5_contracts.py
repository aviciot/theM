#!/usr/bin/env python3
"""test_go_wave5_contracts.py — Python↔Go contract tests for Wave 5 routes.

Tests: /api/v1/admin/tokens and /api/v1/admin/sessions

For each operation this test:
1. Calls the Python bridge (port 8001) and records the response shape.
2. Calls the Go bridge (port 8002) with the same inputs.
3. Asserts both responses share the same JSON keys and semantics.

Run:
    python3.12 scripts/tests/test_go_wave5_contracts.py
    python3.12 scripts/tests/test_go_wave5_contracts.py --verbose

Environment:
    PYTHON_BRIDGE   base URL for Python bridge (default: http://localhost:8001)
    GO_BRIDGE       base URL for Go bridge    (default: http://localhost:8002)

Skip conditions:
    - Go bridge not reachable → all Go-parity tests skip (Python-only still run)
    - Python bridge not reachable → all tests skip
"""

import json
import os
import sys
import urllib.request
import urllib.error
from dataclasses import dataclass, field
from typing import Any

PYTHON_BASE = os.getenv("PYTHON_BRIDGE", "http://localhost:8001").rstrip("/")
GO_BASE = os.getenv("GO_BRIDGE", "http://localhost:8002").rstrip("/")
VERBOSE = "--verbose" in sys.argv or "-v" in sys.argv

PASS_COUNT = 0
FAIL_COUNT = 0
SKIP_COUNT = 0

# ── helpers ─────────────────────────────────────────────────────────────────

def _log(msg: str) -> None:
    if VERBOSE:
        print(f"    {msg}")


def _request(url: str, method: str = "GET", body: Any = None,
             raise_on_error: bool = False) -> tuple[int, Any]:
    """HTTP request helper. Returns (status_code, parsed_json_or_None)."""
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"} if data else {}
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            raw = resp.read()
            return resp.status, json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, None
    except Exception as exc:
        if raise_on_error:
            raise
        return 0, None


def _is_reachable(base: str) -> bool:
    code, _ = _request(f"{base}/health/live")
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


# ── token contract tests ─────────────────────────────────────────────────────

def run_token_contracts(go_ok: bool) -> None:
    print("\n── Token CRUD contract tests ──")

    TOKEN_PATH = "/api/v1/admin/tokens"
    py_base = PYTHON_BASE + TOKEN_PATH
    go_base = GO_BASE + TOKEN_PATH

    # --- Create ---
    create_body = {"label": "contract-test-token", "user_id": 1}
    py_code, py_created = _request(py_base, "POST", create_body)
    _check("Python POST /tokens → 201", py_code == 201)

    if py_code != 201 or not py_created:
        _skip("Go POST /tokens contract", "Python create failed")
        return

    # Verify Python response shape
    _check("Python create: 'id' present", bool(py_created.get("id")))
    _check("Python create: 'token' (plaintext) present",
           bool(py_created.get("token")), f"keys={list(py_created.keys())}")
    _check("Python create: 'label' correct",
           py_created.get("label") == "contract-test-token")
    _check("Python create: 'enabled'=True", py_created.get("enabled") is True)
    _check("Python create: 'created_at' is RFC3339-ish",
           "T" in (py_created.get("created_at") or ""),
           f"got: {py_created.get('created_at')}")

    py_token_id = py_created["id"]
    _log(f"Python token id: {py_token_id}")

    # Cleanup Python token regardless of later failures
    def cleanup_py():
        _request(py_base + "/" + py_token_id, "DELETE")

    try:
        if not go_ok:
            _skip("Go POST /tokens contract", "Go bridge unreachable")
        else:
            go_code, go_created = _request(go_base, "POST", create_body)
            _check("Go POST /tokens → 201", go_code == 201,
                   f"got {go_code}: {go_created}")

            if go_code == 201 and go_created:
                go_token_id = go_created.get("id")
                _log(f"Go token id: {go_token_id}")

                # Field parity check
                py_keys = set(py_created.keys()) - {"token"}  # plaintext keys match
                go_keys = set(go_created.keys()) - {"token"}
                extra_in_go = go_keys - py_keys
                missing_in_go = py_keys - go_keys
                _check("Go create: no extra fields vs Python",
                       not extra_in_go, f"extra: {extra_in_go}")
                _check("Go create: no missing fields vs Python",
                       not missing_in_go, f"missing: {missing_in_go}")
                _check("Go create: 'token' plaintext present",
                       bool(go_created.get("token")))
                _check("Go create: 'enabled'=True",
                       go_created.get("enabled") is True)
                _check("Go create: 'created_at' is RFC3339-ish",
                       "T" in (go_created.get("created_at") or ""),
                       f"got: {go_created.get('created_at')}")

                # --- GET ---
                go_get_code, go_got = _request(go_base + "/" + go_token_id)
                _check("Go GET /tokens/{id} → 200", go_get_code == 200)
                if go_got:
                    _check("Go GET: plaintext absent",
                           "token" not in go_got,
                           f"found token key in: {list(go_got.keys())}")

                # --- PATCH ---
                patch_body = {"enabled": False}
                go_patch_code, go_patched = _request(
                    go_base + "/" + go_token_id, "PATCH", patch_body)
                _check("Go PATCH /tokens/{id} → 200", go_patch_code == 200)
                if go_patched:
                    _check("Go PATCH: enabled=False",
                           go_patched.get("enabled") is False)

                # --- DELETE → 204, then 404 ---
                go_del_code, _ = _request(go_base + "/" + go_token_id, "DELETE")
                _check("Go DELETE /tokens/{id} → 204", go_del_code == 204,
                       f"got {go_del_code}")
                go_del2_code, _ = _request(go_base + "/" + go_token_id, "DELETE")
                _check("Go DELETE /tokens/{id} repeat → 404",
                       go_del2_code == 404, f"got {go_del2_code}")

        # --- Python DELETE ---
        py_del_code, _ = _request(py_base + "/" + py_token_id, "DELETE")
        _check("Python DELETE /tokens/{id} → 204", py_del_code == 204,
               f"got {py_del_code}")
        py_del2_code, _ = _request(py_base + "/" + py_token_id, "DELETE")
        _check("Python DELETE repeat → 404", py_del2_code == 404,
               f"got {py_del2_code}")

    finally:
        cleanup_py()

    # --- error shape parity ---
    print("\n  -- Error shape tests --")

    # Missing label → 400
    bad_body = {"user_id": 1}
    py_bad_code, py_bad = _request(py_base, "POST", bad_body)
    _check("Python POST missing label → 400", py_bad_code == 400,
           f"got {py_bad_code}")
    if py_bad:
        _check("Python 400: 'error' key present",
               "error" in (py_bad or {}), f"body: {py_bad}")

    if go_ok:
        go_bad_code, go_bad = _request(go_base, "POST", bad_body)
        _check("Go POST missing label → 400", go_bad_code == 400,
               f"got {go_bad_code}")
        if go_bad and py_bad:
            go_err_keys = set(go_bad.keys())
            py_err_keys = set(py_bad.keys())
            _check("Error envelope: Go uses same keys as Python",
                   go_err_keys == py_err_keys,
                   f"Go={go_err_keys}, Python={py_err_keys}")

    # GET unknown UUID → 404
    zero_uuid = "00000000-0000-0000-0000-000000000000"
    py_404_code, _ = _request(py_base + "/" + zero_uuid)
    _check("Python GET zero-uuid → 404", py_404_code == 404)

    if go_ok:
        go_404_code, _ = _request(go_base + "/" + zero_uuid)
        _check("Go GET zero-uuid → 404", go_404_code == 404)


# ── session contract tests ────────────────────────────────────────────────────

def run_session_contracts(go_ok: bool) -> None:
    print("\n── Session admin contract tests ──")

    SESSION_PATH = "/api/v1/admin/sessions"

    # Both bridges must reject GET /sessions with no params → 400
    py_code, py_body = _request(PYTHON_BASE + SESSION_PATH)
    _check("Python GET /sessions (no params) → 400", py_code == 400,
           f"got {py_code}")
    if py_body:
        _check("Python 400: 'error' key present",
               "error" in py_body, f"body: {py_body}")

    if go_ok:
        go_code, go_body = _request(GO_BASE + SESSION_PATH)
        _check("Go GET /sessions (no params) → 400", go_code == 400,
               f"got {go_code}")
        if go_body and py_body:
            _check("Error envelope parity (sessions)",
                   set(go_body.keys()) == set(py_body.keys()),
                   f"Go={set(go_body.keys())}, Python={set(py_body.keys())}")

    # Both must reject GET /sessions?app_id=x&ep_slug=y → 400
    both_url = SESSION_PATH + "?app_id=abc&ep_slug=def"
    py_code2, _ = _request(PYTHON_BASE + both_url)
    _check("Python GET /sessions both params → 400", py_code2 == 400,
           f"got {py_code2}")

    if go_ok:
        go_code2, _ = _request(GO_BASE + both_url)
        _check("Go GET /sessions both params → 400", go_code2 == 400,
               f"got {go_code2}")

    # Valid app_id (non-existent → empty list, not 404)
    fake_url = SESSION_PATH + "?app_id=00000000-0000-0000-0000-000000000000"
    py_code3, py_list = _request(PYTHON_BASE + fake_url)
    _check("Python GET /sessions?app_id={zero} → 200", py_code3 == 200,
           f"got {py_code3}")
    if py_list:
        _check("Python sessions list: 'sessions' key", "sessions" in py_list)
        _check("Python sessions list: 'count' key", "count" in py_list)
        _check("Python sessions list: empty is []",
               py_list.get("sessions") == [])

    if go_ok:
        go_code3, go_list = _request(GO_BASE + fake_url)
        _check("Go GET /sessions?app_id={zero} → 200", go_code3 == 200,
               f"got {go_code3}")
        if go_list and py_list:
            _check("Go sessions: same top-level keys as Python",
                   set(go_list.keys()) == set(py_list.keys()),
                   f"Go={set(go_list.keys())}, Python={set(py_list.keys())}")
            _check("Go sessions: 'sessions' is list",
                   isinstance(go_list.get("sessions"), list))
            _check("Go sessions: count=0",
                   go_list.get("count") == 0)

    # Disconnect non-existent session → 404
    disc_url = SESSION_PATH + "/no-such-session-xyz/disconnect"
    py_disc_code, _ = _request(PYTHON_BASE + disc_url, "POST")
    _check("Python POST /sessions/{bad}/disconnect → 404", py_disc_code == 404,
           f"got {py_disc_code}")

    if go_ok:
        go_disc_code, _ = _request(GO_BASE + disc_url, "POST")
        _check("Go POST /sessions/{bad}/disconnect → 404", go_disc_code == 404,
               f"got {go_disc_code}")


# ── entrypoint ────────────────────────────────────────────────────────────────

def main() -> None:
    print("=== test_go_wave5_contracts: Python↔Go Wave 5 contract tests ===")
    print(f"Python bridge: {PYTHON_BASE}")
    print(f"Go bridge:     {GO_BASE}")

    # Check Python bridge
    if not _is_reachable(PYTHON_BASE):
        print(f"\n[SKIP] Python bridge not reachable at {PYTHON_BASE} — "
              "all tests skipped")
        sys.exit(0)
    print("  Python bridge reachable ✓")

    go_ok = _is_reachable(GO_BASE)
    if go_ok:
        print("  Go bridge reachable ✓")
    else:
        print(f"  Go bridge NOT reachable at {GO_BASE} — "
              "Go-parity assertions will be skipped")

    run_token_contracts(go_ok)
    run_session_contracts(go_ok)

    print(f"\nResult: {PASS_COUNT} passed, {FAIL_COUNT} failed, "
          f"{SKIP_COUNT} skipped")
    sys.exit(1 if FAIL_COUNT > 0 else 0)


if __name__ == "__main__":
    main()
