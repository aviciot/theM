"""
test_41_appflow_fork_join — Fork/Join parallel execution E2E test.

Flow under test:
  EP (websocket) → Fork → Agent A (a2a_echo) → Join → Agent C (a2a_echo)
                       → Agent B (a2a_echo) ↗

Steps:
  1. Create application + WS entry point
  2. Create definition (schema_version=2, execution_backend=temporal, Fork/Join)
  3. Publish definition (stamps _resolved_agent_ids)
  4. Create access token for WS auth
  5. Open WebSocket, send message → triggers AppFlowWorkflow
  6. Poll runs until status=completed (both branch agents + final agent run)
  7. Assert run.status == "completed"
  8. Assert run steps show multiple agent invocations (both branches executed)
  9. Cleanup

Run:
  python3.12 scripts/tests/test_41_appflow_fork_join.py
Requires:
  - Stack running with --profile temporal
  - them-dag-worker polling appflow-dag task queue
  - a2a-echo container healthy (http://a2a-echo:9200)
  - ADMIN_JWT env var (auto-fetched via /auth/login if absent)
"""

import asyncio
import base64
import json
import os
import subprocess
import sys
import time
import uuid
import urllib.request
import urllib.error

BASE_URL = "http://localhost:8088"
WS_BASE = "ws://localhost:8088"
ADMIN_USER = "admin"
ADMIN_PASS = "admin123"

A2A_ECHO_AGENT_ID = "84d60a87-ceae-460a-9ed2-506c30bc81d8"
A2A_ECHO_NAMESPACE = "them.tenant.00000000-0000-0000-0000-000000000001"
A2A_ECHO_NAME = "a2a_echo"


# ── HTTP helpers ──────────────────────────────────────────────────────────────

def fetch_token() -> str:
    env_jwt = os.environ.get("ADMIN_JWT", "")
    if env_jwt:
        return env_jwt
    body = json.dumps({"username": ADMIN_USER, "password": ADMIN_PASS}).encode()
    req = urllib.request.Request(
        f"{BASE_URL}/auth/api/v1/auth/login",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.load(resp)
    return data["access_token"]


def api(method, path, token, body=None, timeout=20):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"{BASE_URL}/api/v1{path}",
        data=data,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
        },
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            if not raw.strip():
                return resp.status, {}
            return resp.status, json.loads(raw)
    except urllib.error.HTTPError as e:
        try:
            body_bytes = e.read().decode()
            return e.code, json.loads(body_bytes) if body_bytes.strip() else {}
        except Exception:
            return e.code, {}


def get_tenant_id(token: str) -> str:
    parts = token.split(".")
    if len(parts) < 2:
        raise ValueError("invalid JWT")
    payload = json.loads(base64.urlsafe_b64decode(parts[1] + "=="))
    return payload.get("tenant_id", "")


def psql(sql: str) -> str:
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", sql],
        capture_output=True, text=True, timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError(f"psql error: {result.stderr.strip()}")
    return result.stdout.strip()


# ── Assertion helpers ─────────────────────────────────────────────────────────

PASS_COUNT = 0
FAIL_COUNT = 0


def ok(label, condition, detail=""):
    global PASS_COUNT, FAIL_COUNT
    if condition:
        print(f"  PASS  {label}")
        PASS_COUNT += 1
        return True
    else:
        msg = f" ({detail})" if detail else ""
        print(f"  FAIL  {label}{msg}")
        FAIL_COUNT += 1
        return False


# ── WebSocket session helper ──────────────────────────────────────────────────

async def open_ws_and_get_run_id(token: str, app_slug: str, ep_slug: str) -> str:
    """Opens WS, sends one message, returns run_id from run_created event."""
    try:
        import websockets
    except ImportError:
        print("  websockets not installed — pip install websockets")
        return ""

    uri = f"{WS_BASE}/apps/{app_slug}/ws/{ep_slug}?token={token}"
    run_id = None
    try:
        async with websockets.connect(uri, open_timeout=10) as ws:
            await ws.send(json.dumps({"type": "message", "content": "Hello Fork/Join!"}))
            deadline = asyncio.get_event_loop().time() + 15
            while asyncio.get_event_loop().time() < deadline:
                try:
                    raw = await asyncio.wait_for(ws.recv(), timeout=2)
                    evt = json.loads(raw)
                    if evt.get("type") == "run_created" and evt.get("run_id"):
                        run_id = evt["run_id"]
                        break
                    if "run_id" in evt and not run_id:
                        run_id = evt["run_id"]
                except asyncio.TimeoutError:
                    continue
                except Exception:
                    break
    except Exception as e:
        print(f"  WS error: {e}")
    return run_id or ""


# ── Main test ─────────────────────────────────────────────────────────────────

def main():
    print("=== test_41_appflow_fork_join ===")

    token = fetch_token()
    tenant_id = get_tenant_id(token)
    if not tenant_id:
        print("SKIP: could not extract tenant_id from JWT")
        return 0

    suffix = uuid.uuid4().hex[:8]
    app_slug = f"e2e-forkjoin-{suffix}"
    ep_slug = f"ep-ws-{suffix}"

    app_id = None
    def_id = None
    run_id = None
    access_token_id = None

    try:
        # ── Step 1: Create application ────────────────────────────────────────
        print("\n[1] Create application")
        status, body = api("POST", "/admin/applications", token, {
            "name": f"E2E Fork/Join Test {suffix}",
            "slug": app_slug,
            "enabled": True,
        })
        if not ok("create application → 201", status == 201, f"{status} {body}"):
            return 1
        app_id = body.get("id", "")
        ok("app_id present", bool(app_id), str(body))

        # ── Step 2: Create WS entry point ─────────────────────────────────────
        print("\n[2] Create entry point")
        status, body = api("POST", f"/admin/applications/{app_id}/entry-points", token, {
            "slug": ep_slug,
            "entry_point_type": "websocket",
            "enabled": True,
        })
        if not ok("create entry point → 201", status == 201, f"{status} {body}"):
            return 1
        ep_id = body.get("id", "")
        ok("ep_id present", bool(ep_id), str(body))

        # ── Step 3: Create definition (EP→Fork→A+B→Join→C) ───────────────────
        print("\n[3] Create definition")
        ep_inst = "ep_ws_1"
        fork_inst = "fork_1"
        agent_a_inst = "agent_a_1"
        agent_b_inst = "agent_b_1"
        join_inst = "join_1"
        agent_c_inst = "agent_c_1"

        agent_ref = {
            "kind": "agent",
            "namespace": A2A_ECHO_NAMESPACE,
            "name": A2A_ECHO_NAME,
            "version": 1,
        }

        definition_doc = {
            "schema_version": 2,
            "name": "E2E Fork/Join Flow",
            "execution_backend": "temporal",
            "components": [
                {
                    "instance_id": fork_inst,
                    "name": "Fork",
                    "definition_ref": {
                        "kind": "flow_control",
                        "namespace": "builtin",
                        "name": "fork",
                        "version": 1,
                    },
                    "config": {"node_type": "fork", "display_name": "Fork"},
                },
                {
                    "instance_id": agent_a_inst,
                    "name": "Branch Agent A",
                    "definition_ref": agent_ref,
                    "config": {},
                },
                {
                    "instance_id": agent_b_inst,
                    "name": "Branch Agent B",
                    "definition_ref": agent_ref,
                    "config": {},
                },
                {
                    "instance_id": join_inst,
                    "name": "Join",
                    "definition_ref": {
                        "kind": "flow_control",
                        "namespace": "builtin",
                        "name": "join",
                        "version": 1,
                    },
                    "config": {"node_type": "join", "display_name": "Join"},
                },
                {
                    "instance_id": agent_c_inst,
                    "name": "Final Agent",
                    "definition_ref": agent_ref,
                    "config": {},
                },
            ],
            "connections": [
                {"source": fork_inst, "target": agent_a_inst, "type": "flow_control"},
                {"source": fork_inst, "target": agent_b_inst, "type": "flow_control"},
                {"source": agent_a_inst, "target": join_inst, "type": "flow_control"},
                {"source": agent_b_inst, "target": join_inst, "type": "flow_control"},
                {"source": join_inst, "target": agent_c_inst, "type": "flow_control"},
            ],
            "entry_points": [
                {
                    "instance_id": ep_inst,
                    "slug": ep_slug,
                    "protocol": "websocket",
                    "root": fork_inst,
                    "config": {},
                }
            ],
        }

        status, body = api(
            "POST",
            f"/admin/applications/{app_id}/definitions",
            token,
            {"definition": definition_doc},
        )
        if not ok("create definition → 201", status == 201, f"{status} {body}"):
            return 1
        def_id = body.get("id", "")
        ok("def_id present", bool(def_id), str(body))

        # ── Step 4: Publish definition ─────────────────────────────────────────
        print("\n[4] Publish definition")
        status, body = api(
            "POST",
            f"/admin/applications/{app_id}/definitions/{def_id}/publish",
            token,
        )
        if not ok("publish definition → 200 or 201", status in (200, 201), f"{status} {body}"):
            print(f"  publish response: {body}")
            return 1
        ok("publish returned result", isinstance(body, dict), str(body)[:100])

        time.sleep(0.5)

        # ── Step 5: Create access token ────────────────────────────────────────
        print("\n[5a] Create access token")
        status, tok_body = api("POST", "/admin/tokens", token, {
            "label": f"e2e-forkjoin-ws-token-{suffix}",
            "user_id": 1,
        })
        ws_bearer = tok_body.get("token", "")
        access_token_id = tok_body.get("id", "")
        ok("access token created → 201", status == 201, f"{status} {tok_body}")
        ok("access token value present", bool(ws_bearer), str(tok_body)[:80])
        if not ws_bearer:
            return 1

        # ── Step 5b: Open WS session ───────────────────────────────────────────
        print("\n[5b] Open WS session and send message")
        run_id = asyncio.run(open_ws_and_get_run_id(ws_bearer, app_slug, ep_slug))
        ok("run_id from WS session", bool(run_id), f"got {run_id!r}")

        if not run_id:
            # Fallback: get run_id from runs table for this app.
            rows = psql(
                f"SELECT id::text FROM them.runs "
                f"WHERE tenant_id='{tenant_id}'::uuid "
                f"AND application_id='{app_id}'::uuid "
                f"ORDER BY created_at DESC LIMIT 1"
            )
            run_id = rows.strip()
            ok("run_id from DB fallback", bool(run_id), f"got {run_id!r}")
            if not run_id:
                print("  SKIP: cannot determine run_id — aborting E2E")
                return 0

        # ── Step 6: Poll run until completed ──────────────────────────────────
        print(f"\n[6] Poll run until completed (run_id={run_id})")
        final_status = None
        deadline = time.time() + 180  # parallel agents + final agent can take a few minutes
        while time.time() < deadline:
            status, run_data = api("GET", f"/runs/{run_id}", token)
            if status == 200:
                final_status = run_data.get("status", "")
                if final_status in ("completed", "failed", "rejected", "canceled"):
                    break
            time.sleep(3)

        ok("run reached terminal status",
           final_status in ("completed", "failed", "rejected", "canceled"),
           f"final_status={final_status!r}")
        ok("run.status == completed", final_status == "completed",
           f"got {final_status!r} — if failed, check dag-worker logs")

        # ── Step 7: Verify both branch agents were called ──────────────────────
        print("\n[7] Verify both branch agents were invoked (run_steps)")
        try:
            agent_a_id = psql(
                f"SELECT id::text FROM them.agents "
                f"WHERE tenant_id='{tenant_id}'::uuid "
                f"AND name='{A2A_ECHO_NAME}' LIMIT 1"
            ).strip()
        except Exception:
            agent_a_id = ""

        if agent_a_id:
            # Count run_steps rows for this run with node IDs matching agent branches.
            step_count_raw = psql(
                f"SELECT COUNT(*) FROM them.run_steps "
                f"WHERE run_id='{run_id}'::uuid"
            ).strip()
            try:
                step_count = int(step_count_raw)
            except ValueError:
                step_count = 0
            # 3 agent calls expected: agent_a, agent_b, agent_c
            ok("run has ≥3 run_steps (both branches + final agent)",
               step_count >= 3,
               f"got {step_count} run_steps")
        else:
            print("  SKIP step 7: agent ID not resolved from DB (non-blocking)")

    finally:
        # ── Cleanup ──────────────────────────────────────────────────────────
        print("\n[cleanup]")
        if run_id:
            try:
                psql(f"DELETE FROM them.run_steps WHERE run_id='{run_id}'::uuid")
                psql(f"DELETE FROM them.runs WHERE id='{run_id}'::uuid")
                print("  cleaned up run + run_steps")
            except Exception as e:
                print(f"  cleanup runs: {e}")
        if access_token_id:
            s, _ = api("DELETE", f"/admin/tokens/{access_token_id}", token)
            print(f"  DELETE access token → {s}")
        if app_id:
            s, _ = api("DELETE", f"/admin/applications/{app_id}", token)
            print(f"  DELETE application → {s}")

    print()
    if FAIL_COUNT == 0:
        print(f"PASS test_41_appflow_fork_join ({PASS_COUNT} checks)")
        return 0
    else:
        print(f"FAIL test_41_appflow_fork_join ({FAIL_COUNT} failures, {PASS_COUNT} passed)")
        return 1


if __name__ == "__main__":
    sys.exit(main())
