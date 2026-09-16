"""
test_39_appflow_hil — AppFlow HIL approval API E2E tests.

Tests the HIL approval endpoint:
  POST /api/v1/runs/{run_id}/hil/{node_id}/approve
  POST /api/v1/runs/{run_id}/hil/{node_id}/reject

Uses the live DB (via docker exec psql) to insert a pending hil_approvals row,
then verifies the API returns the expected HTTP status codes.

Run: python3 scripts/tests/test_39_appflow_hil.py
Requires: stack running, ADMIN_JWT env var (or auto-fetched via /auth/login).
"""

import json
import os
import subprocess
import sys
import uuid
import time
import urllib.request
import urllib.error

BASE_URL = "http://localhost:8088"
ADMIN_USER = "admin"
ADMIN_PASS = "admin123"


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


def api(method, path, token, body=None):
    """Make an API call to the bridge. Returns (status_code, parsed_body)."""
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
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, json.load(resp)
    except urllib.error.HTTPError as e:
        try:
            body_text = e.read().decode()
            return e.code, json.loads(body_text)
        except Exception:
            return e.code, {}


def psql(sql: str) -> str:
    """Run SQL via docker exec against them-postgres."""
    result = subprocess.run(
        ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
         "-t", "-c", sql],
        capture_output=True, text=True, timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError(f"psql error: {result.stderr.strip()}")
    return result.stdout.strip()


def get_tenant_id(token: str) -> str:
    """Fetch the caller's tenant_id from the JWT payload."""
    import base64
    parts = token.split(".")
    if len(parts) < 2:
        raise ValueError("invalid JWT")
    payload_b64 = parts[1] + "=="  # pad
    payload = json.loads(base64.urlsafe_b64decode(payload_b64))
    return payload.get("tenant_id", "")


def seed_hil_approval(tenant_id: str, run_id: str, node_id: str, app_id: str) -> None:
    """Insert a pending hil_approvals row directly into the DB."""
    psql(
        f"INSERT INTO them.hil_approvals "
        f"(tenant_id, application_id, run_id, node_id, approver_role, status) "
        f"VALUES ('{tenant_id}'::uuid, '{app_id}'::uuid, '{run_id}'::uuid, '{node_id}', 'admin', 'pending') "
        f"ON CONFLICT DO NOTHING"
    )


def cleanup_hil_approval(run_id: str) -> None:
    psql(f"DELETE FROM them.hil_approvals WHERE run_id = '{run_id}'::uuid")


def cleanup_run(run_id: str) -> None:
    psql(f"DELETE FROM them.runs WHERE id = '{run_id}'::uuid")


def get_app_id(token: str) -> str:
    """Get the first application ID for the tenant (or create a stub run row manually)."""
    status, data = api("GET", "/admin/applications", token)
    if status != 200 or not data:
        raise RuntimeError(f"could not list applications: {status} {data}")
    if not isinstance(data, list) or len(data) == 0:
        raise RuntimeError("no applications found — create an application first")
    return data[0]["id"]


def seed_run(tenant_id: str, app_id: str, run_id: str) -> None:
    """Insert a minimal them.runs row for testing."""
    psql(
        f"INSERT INTO them.runs (id, tenant_id, application_id, status, started_at) "
        f"VALUES ('{run_id}'::uuid, '{tenant_id}'::uuid, '{app_id}'::uuid, 'running', now()) "
        f"ON CONFLICT (id) DO NOTHING"
    )


def assert_eq(label, got, want):
    if got != want:
        print(f"  FAIL {label}: got {got!r}, want {want!r}")
        return False
    print(f"  OK   {label}")
    return True


def main():
    print("=== test_39_appflow_hil ===")
    ok = True

    token = fetch_token()
    tenant_id = get_tenant_id(token)
    if not tenant_id:
        print("SKIP: could not extract tenant_id from JWT")
        return 0

    # Use first app ID for the FK.
    try:
        app_id = get_app_id(token)
    except RuntimeError as e:
        print(f"SKIP: {e}")
        return 0

    # Unique IDs per test run.
    run_id = str(uuid.uuid4())
    node_id = "test-hil-node-1"

    try:
        # Seed a them.runs row (required FK for hil_approvals).
        seed_run(tenant_id, app_id, run_id)
        # Seed the HIL approval row.
        seed_hil_approval(tenant_id, run_id, node_id, app_id)
        time.sleep(0.1)  # allow write to propagate

        # T1: Approve a pending HIL gate → 200.
        print("\nT1: Approve pending HIL gate")
        status, body = api("POST", f"/runs/{run_id}/hil/{node_id}/approve", token, {"comment": "looks good"})
        ok &= assert_eq("status", status, 200)
        ok &= assert_eq("response.status", body.get("status"), "approved")
        ok &= assert_eq("response.run_id", body.get("run_id"), run_id)

        # T2: Re-approve the same node → 409 (already decided).
        print("\nT2: Re-approve already-decided HIL gate → 409")
        status, body = api("POST", f"/runs/{run_id}/hil/{node_id}/approve", token, {})
        ok &= assert_eq("status", status, 409)

        # Reset to pending for reject test.
        psql(f"UPDATE them.hil_approvals SET status='pending', decided_at=NULL WHERE run_id='{run_id}'::uuid AND node_id='{node_id}'")

        # T3: Reject a pending HIL gate → 200.
        print("\nT3: Reject pending HIL gate")
        status, body = api("POST", f"/runs/{run_id}/hil/{node_id}/reject", token, {"comment": "not safe"})
        ok &= assert_eq("status", status, 200)
        ok &= assert_eq("response.status", body.get("status"), "rejected")

        # T4: Non-existent run/node → 404.
        print("\nT4: Non-existent run/node → 404")
        fake_run = str(uuid.uuid4())
        status, body = api("POST", f"/runs/{fake_run}/hil/no-such-node/approve", token, {})
        ok &= assert_eq("status", status, 404)

    finally:
        cleanup_hil_approval(run_id)
        cleanup_run(run_id)

    print()
    if ok:
        print("PASS test_39_appflow_hil")
        return 0
    else:
        print("FAIL test_39_appflow_hil")
        return 1


if __name__ == "__main__":
    sys.exit(main())
