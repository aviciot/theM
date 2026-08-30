#!/usr/bin/env python3
"""
Live smoke test for A2A message/send and message/stream endpoints.

Tests:
  1. message/send → HTTP 200, JSON result with text artifact
  2. message/stream → HTTP 200, text/event-stream, message-delta frames with text,
                      final task-status-update completed

Run from host:
  python3 scripts/test_a2a_stream_live.py

Requires:
  - them-go-bridge running (port 8088 via Traefik)
  - ep-a2a-1 entry point configured and enabled
  - Admin credentials (default: admin / admin123)
  - A valid access token for ep-a2a-1's application in the DB, OR
    the script will use the admin JWT directly if the EP is public
"""

import json
import sys
import os
import urllib.request
import urllib.error

BASE = os.environ.get("THEM_BASE", "http://localhost:8088")
AUTH_BASE = os.environ.get("THEM_AUTH", "http://localhost:8088")
EP_SLUG = os.environ.get("EP_SLUG", "ep-a2a-1")
ADMIN_USER = os.environ.get("ADMIN_USER", "admin")
ADMIN_PASS = os.environ.get("ADMIN_PASS", "admin123")
TOKEN = os.environ.get("A2A_TOKEN", "")  # override: supply a bearer token directly

PASS = 0
FAIL = 0


def ok(msg):
    global PASS
    print(f"  [PASS] {msg}")
    PASS += 1


def fail(msg, detail=""):
    global FAIL
    print(f"  [FAIL] {msg}" + (f" — {detail}" if detail else ""))
    FAIL += 1


def http_post(url, body, headers=None, timeout=30):
    data = json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.headers, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers, e.read()


def get_admin_token():
    """Login via Go auth service, return JWT."""
    status, _, body = http_post(
        f"{AUTH_BASE}/auth/api/v1/auth/login",
        {"username": ADMIN_USER, "password": ADMIN_PASS},
    )
    if status != 200:
        return None, f"login failed: HTTP {status}"
    data = json.loads(body)
    return data.get("access_token"), None


def get_app_token(admin_jwt, ep_slug):
    """Create a fresh access token via the admin API and return the plaintext value."""
    # Resolve the application_id for this EP slug
    req = urllib.request.Request(f"{BASE}/api/v1/admin/applications")
    req.add_header("Authorization", f"Bearer {admin_jwt}")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            apps = json.loads(resp.read())
    except Exception as e:
        return None, f"list apps failed: {e}"

    # Find the app that owns this EP slug
    app_id = None
    for app in apps:
        for ep in app.get("entry_points", []):
            if ep.get("slug") == ep_slug:
                app_id = app.get("id")
                break
        if app_id:
            break

    if not app_id:
        return None, f"no app found with EP slug={ep_slug}"

    # Create a fresh token — the plaintext is returned only at creation time
    status, _, body = http_post(
        f"{BASE}/api/v1/admin/tokens",
        {"label": "smoke-test-stream", "application_id": app_id},
        {"Authorization": f"Bearer {admin_jwt}"},
    )
    if status not in (200, 201):
        return None, f"create token failed: HTTP {status} {body.decode()[:100]}"

    data = json.loads(body)
    token = data.get("token") or data.get("plaintext") or data.get("value")
    if not token:
        return None, f"no token in response: {data}"
    return token, None


# ── Main ──────────────────────────────────────────────────────────────────────

print(f"=== A2A Stream Live Test — {BASE}/a2a/{EP_SLUG} ===\n")

# Step 1: Resolve bearer token
bearer = TOKEN
if not bearer:
    print("[auth] Logging in as admin...")
    jwt, err = get_admin_token()
    if err or not jwt:
        print(f"[SKIP] Could not get admin JWT: {err}")
        print("       Set A2A_TOKEN=<token> env var and retry.")
        sys.exit(0)
    print(f"[auth] Got JWT (len={len(jwt)})")

    # Try to get an application token; fall back to JWT for token-mode EPs
    app_token, err = get_app_token(jwt, EP_SLUG)
    if app_token:
        bearer = app_token
        print(f"[auth] Using app token")
    else:
        # Use JWT directly — works if auth middleware accepts JWTs on A2A EPs
        bearer = jwt
        print(f"[auth] No app token found ({err}), using admin JWT directly")

A2A_URL = f"{BASE}/a2a/{EP_SLUG}"
RPC_SEND = {
    "jsonrpc": "2.0",
    "id": "test-send-1",
    "method": "message/send",
    "params": {
        "message": {
            "role": "user",
            "parts": [{"text": "Reply with exactly: STREAM_TEST_OK"}],
        }
    },
}
RPC_STREAM = {
    "jsonrpc": "2.0",
    "id": "test-stream-1",
    "method": "message/stream",
    "params": {
        "message": {
            "role": "user",
            "parts": [{"text": "Reply with exactly: STREAM_TEST_OK"}],
        }
    },
}

# ── Test 1: message/send ──────────────────────────────────────────────────────
print("\n[1] message/send")
status, headers, body = http_post(
    A2A_URL,
    RPC_SEND,
    {"Authorization": f"Bearer {bearer}"},
    timeout=60,
)
ct = headers.get("Content-Type", "")

if status == 200:
    ok(f"HTTP 200")
else:
    fail(f"HTTP {status}", body.decode()[:200])

try:
    resp = json.loads(body)
    if resp.get("error"):
        fail("no RPC error", f"got error: {resp['error']}")
    else:
        ok("no RPC error in response")

    result = resp.get("result", {})
    artifacts = result.get("artifacts", [])
    text = ""
    if artifacts:
        parts = artifacts[0].get("parts", [])
        text = parts[0].get("text", "") if parts else ""

    if text:
        ok(f"artifact text present: {text[:80]!r}")
    else:
        fail("artifact text missing", f"result={result}")

    state = result.get("status", {}).get("state", "")
    if state == "completed":
        ok("status.state = completed")
    else:
        fail(f"status.state = {state!r}")
except Exception as e:
    fail("parse response", str(e))

# ── Test 2: message/stream ────────────────────────────────────────────────────
print("\n[2] message/stream")

data = json.dumps(RPC_STREAM).encode()
req = urllib.request.Request(A2A_URL, data=data, method="POST")
req.add_header("Content-Type", "application/json")
req.add_header("Accept", "text/event-stream")
req.add_header("Authorization", f"Bearer {bearer}")

import socket
raw = ""
stream_status = 0
stream_ct = ""
try:
    with urllib.request.urlopen(req, timeout=90) as resp:
        stream_status = resp.status
        stream_ct = resp.headers.get("Content-Type", "")

        if stream_status == 200:
            ok("HTTP 200")
        else:
            fail(f"HTTP {stream_status}")

        if "text/event-stream" in stream_ct:
            ok("Content-Type: text/event-stream")
        else:
            fail("Content-Type wrong", stream_ct)

        # Read SSE incrementally — stop when we see the terminal completed/failed frame
        # or after 64 KiB, whichever comes first.
        chunks = []
        while True:
            try:
                chunk = resp.read(4096)
            except socket.timeout:
                break
            if not chunk:
                break
            chunks.append(chunk.decode(errors="replace"))
            combined = "".join(chunks)
            # Stop as soon as we have a terminal frame
            if '"completed"' in combined or '"failed"' in combined:
                break
            if len(combined) > 1 << 16:
                break
        raw = "".join(chunks)

except urllib.error.HTTPError as e:
    stream_status = e.code
    raw = e.read().decode(errors="replace")
    fail(f"HTTP {stream_status}", raw[:200])
    raw = ""
except Exception as e:
    fail("stream connect", str(e))
    raw = ""

# Parse SSE frames
delta_texts = []
completed = False
failed = False

for line in raw.splitlines():
    line = line.strip()
    if not line.startswith("data: "):
        continue
    try:
        ev = json.loads(line[6:])
        params = ev.get("params", {})
        event = params.get("event", {})
        kind = event.get("kind", "")

        if kind == "message-delta":
            parts = event.get("parts", [])
            for p in parts:
                t = p.get("text", "")
                if t:
                    delta_texts.append(t)

        elif kind == "task-status-update":
            state = (event.get("status") or {}).get("state", "")
            if state == "completed":
                completed = True
            elif state == "failed":
                failed = True
    except Exception:
        pass

assembled = "".join(delta_texts)

if delta_texts:
    ok(f"message-delta frames received ({len(delta_texts)} chunks): {assembled[:80]!r}")
else:
    fail("no message-delta frames — text was not streamed", f"raw={raw[:300]!r}")

if completed:
    ok("task-status-update completed received")
elif failed:
    fail("task-status-update = failed")
else:
    fail("no terminal task-status-update received", f"raw={raw[:300]!r}")

# ── Summary ───────────────────────────────────────────────────────────────────
print(f"\n{'='*50}")
print(f"Results: {PASS} passed, {FAIL} failed")
if FAIL:
    sys.exit(1)
