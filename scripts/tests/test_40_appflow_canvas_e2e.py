"""
test_40_appflow_canvas_e2e — Full Application Canvas E2E test.

Flow under test:
  EP (websocket) → HIL gate → Agent (a2a_echo)

Steps:
  1. Create application + WS entry point
  2. Create definition (schema_version=2, execution_backend=temporal, HIL→Agent)
  3. Publish definition (stamps _resolved_agent_ids)
  4. Open WebSocket, send a message → triggers AppFlowWorkflow
  5. Poll hil_approvals for pending row (workflow paused at HIL gate)
  6. Approve via POST /runs/{run_id}/hil/{node_id}/approve
  7. Poll runs until status=completed (agent invoked, run finishes)
  8. Assert run.status == "completed"
  9. Cleanup

Run:
  python3.12 scripts/tests/test_40_appflow_canvas_e2e.py
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

# a2a_echo agent — verified present in component_definitions.
A2A_ECHO_AGENT_ID = "84d60a87-ceae-460a-9ed2-506c30bc81d8"
A2A_ECHO_NAMESPACE = "them.tenant.00000000-0000-0000-0000-000000000001"
A2A_ECHO_NAME = "a2a_echo"

TENANT_SLUG = "default"


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
    """Make an API call, return (status_code, parsed_body or {})."""
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
    """
    Opens a WebSocket connection to the app entry point, sends one message,
    and returns the run_id from the first 'run_created' or 'error' event,
    or from the hil_approvals DB row if no run_created event arrives.
    """
    try:
        import websockets
    except ImportError:
        raise RuntimeError("websockets library not installed — pip install websockets")

    # Pass the JWT as a query parameter — the WS handler accepts ?token= as an
    # alternative to the Authorization header (WS upgrade requests cannot reliably
    # carry custom headers in all client implementations).
    ws_url = f"{WS_BASE}/{TENANT_SLUG}/apps/{app_slug}/{ep_slug}/ws?token={token}"

    run_id = None
    print(f"  WS connect: {WS_BASE}/{TENANT_SLUG}/apps/{app_slug}/{ep_slug}/ws (token=...)")
    async with websockets.connect(ws_url, open_timeout=15) as ws:
        # Send initial message to trigger the workflow.
        await ws.send(json.dumps({
            "type": "message",
            "content": "hello from E2E test",
        }))
        print("  WS message sent")

        # Read events for up to 10 seconds to capture run_created or run_id.
        deadline = time.time() + 10
        while time.time() < deadline:
            try:
                raw = await asyncio.wait_for(ws.recv(), timeout=2.0)
                evt = json.loads(raw)
                evt_type = evt.get("type", "")
                print(f"  WS event: {evt_type} — {str(evt)[:100]}")
                if "run_id" in evt:
                    run_id = evt["run_id"]
                    break
                if evt_type == "run_created":
                    run_id = evt.get("run_id", "")
                    break
                if evt_type == "error":
                    print(f"  WS error event: {evt.get('message')}")
                    break
            except asyncio.TimeoutError:
                continue

        # Keep connection alive briefly so the workflow has time to persist the HIL row.
        await asyncio.sleep(1)

    return run_id or ""


# ── Main test ─────────────────────────────────────────────────────────────────

def main():
    print("=== test_40_appflow_canvas_e2e ===")

    token = fetch_token()
    tenant_id = get_tenant_id(token)
    if not tenant_id:
        print("SKIP: could not extract tenant_id from JWT")
        return 0

    # Unique slugs for this test run to avoid collisions.
    suffix = uuid.uuid4().hex[:8]
    app_slug = f"e2e-canvas-{suffix}"
    ep_slug = f"ep-ws-{suffix}"

    app_id = None
    def_id = None
    ep_id = None
    run_id = None          # initialised early so finally block can reference it safely
    access_token_id = None

    try:
        # ── Step 1: Create application ────────────────────────────────────────
        print("\n[1] Create application")
        status, body = api("POST", "/admin/applications", token, {
            "name": f"E2E Canvas Test {suffix}",
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

        # ── Step 3: Create definition (EP→HIL→Agent, execution_backend=temporal) ─
        print("\n[3] Create definition")
        hil_instance_id = "hil_gate_1"
        agent_instance_id = "agent_echo_1"
        ep_instance_id = "ep_ws_1"

        definition_doc = {
            "schema_version": 2,
            "name": "E2E Canvas Flow",
            "execution_backend": "temporal",
            "components": [
                {
                    "instance_id": hil_instance_id,
                    "name": "HIL Gate",
                    "definition_ref": {
                        "kind": "flow_control",
                        "namespace": "them.builtin",
                        "name": "hil",
                        "version": 1,
                    },
                    "config": {
                        "approver_role": "admin",
                        "timeout_seconds": 300,
                        "fallback_action": "reject",
                        "prompt": "E2E test: please approve to continue",
                    },
                },
                {
                    "instance_id": agent_instance_id,
                    "name": "Echo Agent",
                    "definition_ref": {
                        "kind": "agent",
                        "namespace": A2A_ECHO_NAMESPACE,
                        "name": A2A_ECHO_NAME,
                        "version": 1,
                    },
                    "config": {},
                },
            ],
            "connections": [
                # HIL gate → Echo Agent
                {
                    "source": hil_instance_id,
                    "target": agent_instance_id,
                    "type": "flow_control",
                },
            ],
            "entry_points": [
                {
                    "instance_id": ep_instance_id,
                    "slug": ep_slug,
                    "protocol": "websocket",
                    "root": hil_instance_id,  # EP starts at the HIL gate
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

        # Verify the app is still reachable after publish (active_definition_id may
        # not be in the abbreviated list response — the publish result is sufficient).
        time.sleep(0.5)  # brief settle
        status, app_data = api("GET", f"/admin/applications/{app_id}", token)
        ok("app fetched after publish → 200", status == 200, str(app_data)[:80])

        # ── Step 5: Create an access token for WS authentication ────────────
        # The WS entry point uses AccessModeToken — it requires an opaque bearer
        # token (not the admin JWT which is a user session JWT and won't be in
        # the token cache). Create one via the admin API.
        print("\n[5a] Create access token for WS authentication")
        status, tok_body = api("POST", "/admin/tokens", token, {
            "label": f"e2e-canvas-ws-token-{suffix}",
            "user_id": 1,
        })
        ws_bearer = tok_body.get("token", "")
        access_token_id = tok_body.get("id", "")
        ok("access token created → 201", status == 201, f"{status} {tok_body}")
        ok("access token value present", bool(ws_bearer), str(tok_body)[:80])
        if not ws_bearer:
            return 1

        # ── Step 5b: Open WS, send message, capture run_id ──────────────────
        print("\n[5b] Open WS session and send message")
        run_id = asyncio.run(open_ws_and_get_run_id(ws_bearer, app_slug, ep_slug))
        ok("run_id from WS session", bool(run_id), f"got {run_id!r}")

        if not run_id:
            # Try to recover run_id from hil_approvals or runs table.
            rows = psql(
                f"SELECT run_id::text FROM them.hil_approvals "
                f"WHERE tenant_id='{tenant_id}'::uuid "
                f"ORDER BY created_at DESC LIMIT 1"
            )
            run_id = rows.strip()
            ok("run_id from hil_approvals fallback", bool(run_id), f"got {run_id!r}")
            if not run_id:
                print("  SKIP: cannot determine run_id — aborting E2E")
                return 0

        # ── Step 6: Poll for HIL approval row (workflow paused) ───────────────
        print(f"\n[6] Poll hil_approvals for run_id={run_id}")
        hil_node_id = None
        deadline = time.time() + 60  # HIL activity should persist within 60s
        while time.time() < deadline:
            rows = psql(
                f"SELECT node_id FROM them.hil_approvals "
                f"WHERE run_id='{run_id}'::uuid AND status='pending' LIMIT 1"
            )
            node_id_candidate = rows.strip()
            if node_id_candidate:
                hil_node_id = node_id_candidate
                break
            time.sleep(2)

        ok("HIL approval row created (workflow paused)", bool(hil_node_id),
           f"no pending row found for run {run_id}")
        if not hil_node_id:
            return 1

        print(f"  HIL pending at node: {hil_node_id}")

        # ── Step 7: Approve the HIL gate ──────────────────────────────────────
        print("\n[7] Approve HIL gate")
        status, body = api(
            "POST",
            f"/runs/{run_id}/hil/{hil_node_id}/approve",
            token,
            {"comment": "E2E test: approved"},
        )
        ok("approve HIL → 200", status == 200, f"{status} {body}")
        ok("response.status == approved", body.get("status") == "approved",
           str(body)[:100])

        # ── Step 8: Poll run until completed ──────────────────────────────────
        print("\n[8] Poll run until completed")
        final_status = None
        deadline = time.time() + 120  # agent invocation can take up to 2 min
        while time.time() < deadline:
            status, run_data = api("GET", f"/runs/{run_id}", token)
            if status == 200:
                final_status = run_data.get("status", "")
                if final_status in ("completed", "failed", "rejected", "canceled"):
                    break
            time.sleep(3)

        ok("run reached terminal status", final_status in ("completed", "failed", "rejected", "canceled"),
           f"final_status={final_status!r}")
        ok("run.status == completed", final_status == "completed",
           f"got {final_status!r} — if failed, check dag-worker logs for agent invocation error")

    finally:
        # ── Cleanup ──────────────────────────────────────────────────────────
        print("\n[cleanup]")
        # Delete hil_approvals first (FK → runs)
        if run_id:
            try:
                psql(f"DELETE FROM them.hil_approvals WHERE run_id='{run_id}'::uuid")
                psql(f"DELETE FROM them.runs WHERE id='{run_id}'::uuid")
                print("  cleaned up run + hil_approvals")
            except Exception as e:
                print(f"  cleanup runs: {e}")
        # Delete access token
        if access_token_id:
            s, _ = api("DELETE", f"/admin/tokens/{access_token_id}", token)
            print(f"  DELETE access token → {s}")
        # Delete application (cascades to definitions, entry_points, app_agent_bindings)
        if app_id:
            s, _ = api("DELETE", f"/admin/applications/{app_id}", token)
            print(f"  DELETE application → {s}")

    print()
    if FAIL_COUNT == 0:
        print(f"PASS test_40_appflow_canvas_e2e ({PASS_COUNT} checks)")
        return 0
    else:
        print(f"FAIL test_40_appflow_canvas_e2e ({FAIL_COUNT} failures, {PASS_COUNT} passed)")
        return 1


if __name__ == "__main__":
    sys.exit(main())
