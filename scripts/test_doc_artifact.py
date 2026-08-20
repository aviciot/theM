#!/usr/bin/env python3.12
"""
test_doc_artifact.py — end-to-end smoke test for the doc-artifact-test app.

Flow tested:
  1. Login → JWT
  2. POST /apps/doc-artifact-test-sse/sse  → stream ALL SSE events until done/error
  3. Assert run_id was received and run completed (status=completed in DB)
  4. Assert a2a_echo + docu_writer were called (tasks endpoint or iterations proxy)
  5. GET /api/v1/runs/{run_id}/artifacts → assert HTML artifact present

NOTE: SSE connection must stay open for the full run duration — closing early
cancels the Temporal workflow context and marks the run as failed.

Usage:
  python3.12 scripts/test_doc_artifact.py
  THEM_BASE=http://localhost:8088 python3.12 scripts/test_doc_artifact.py
"""

import json
import os
import sys
import time
import urllib.request
import urllib.error

BASE       = os.getenv("THEM_BASE", "http://localhost:8088")
EP_SLUG    = "doc-artifact-test-sse"
USER_MSG   = "Explain the benefits of multi-agent systems in one paragraph"
RUN_TIMEOUT = 300   # seconds to wait for SSE stream to finish
LOGIN_PATH = "/auth/api/v1/auth/login"


# ── helpers ──────────────────────────────────────────────────────────────────

def req(method: str, path: str, body=None, token: str | None = None,
        timeout: int = 30) -> tuple[int, any]:
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            raw = resp.read()
            try:
                return resp.status, json.loads(raw)
            except Exception:
                return resp.status, raw.decode(errors="replace")
    except urllib.error.HTTPError as e:
        body_text = e.read().decode(errors="replace")[:400]
        try:
            return e.code, json.loads(body_text)
        except Exception:
            return e.code, {"error": body_text}


def ok(label: str, condition: bool, detail: str = "") -> None:
    mark = "PASS" if condition else "FAIL"
    print(f"  [{mark}] {label}" + (f": {detail}" if detail else ""))
    if not condition:
        sys.exit(1)


def run_via_sse(token: str) -> str:
    """
    POST to SSE entry point, read events until stream closes, return run_id.
    The Temporal workflow continues even after the SSE connection closes —
    use poll_run() afterward to wait for completion.
    """
    url = BASE + f"/apps/{EP_SLUG}/sse"
    data = json.dumps({"message": USER_MSG}).encode()
    headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
        "Authorization": f"Bearer {token}",
    }
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")

    run_id      = ""
    event_count = 0
    start       = time.time()

    try:
        with urllib.request.urlopen(request, timeout=RUN_TIMEOUT + 60) as resp:
            for raw_line in resp:
                elapsed = int(time.time() - start)
                line = raw_line.decode(errors="replace").strip()
                if not line.startswith("data:"):
                    continue
                payload = line[5:].strip()
                try:
                    ev = json.loads(payload)
                except Exception:
                    continue

                event_count += 1
                if not run_id and ev.get("run_id"):
                    run_id = ev["run_id"]
                    print(f"    [{elapsed:3d}s] run_id: {run_id}")

                ev_type = ev.get("type", "")
                print(f"    [{elapsed:3d}s] event={ev_type:<20} "
                      f"run={run_id[:8] if run_id else '?'}", end="\r", flush=True)

                if ev_type in ("done", "error"):
                    print(f"\n    stream closed by server: type={ev_type}")
                    break

    except Exception as e:
        print(f"\n    SSE stream closed: {e}")

    print(f"\n    total SSE events: {event_count}")
    return run_id


def poll_run(run_id: str, token: str) -> dict:
    """Poll GET /api/v1/runs/{run_id} until terminal status, up to TIMEOUT seconds."""
    deadline = time.time() + RUN_TIMEOUT
    while time.time() < deadline:
        code, body = req("GET", f"/api/v1/runs/{run_id}", token=token)
        if code != 200:
            print(f"    poll {code}: {body}")
            time.sleep(3)
            continue
        status  = body.get("status", "")
        iters   = body.get("iterations", 0)
        elapsed = int(RUN_TIMEOUT - (deadline - time.time()))
        print(f"    [{elapsed:3d}s] status={status:<12} iterations={iters}", end="\r", flush=True)
        if status not in ("running", "pending", ""):
            print()
            return body
        time.sleep(4)
    print()
    return {}


# ── main ─────────────────────────────────────────────────────────────────────

def main() -> None:
    print(f"\n{'='*60}")
    print(f"  doc-artifact-test  end-to-end smoke test")
    print(f"  base={BASE}   ep={EP_SLUG}")
    print(f"{'='*60}\n")

    # 1. Login
    print("1. Login")
    code, body = req("POST", LOGIN_PATH, {"username": "admin", "password": "admin123"})
    ok("login 200", code == 200, f"got {code}: {str(body)[:120]}")
    token = body.get("access_token", "")
    ok("got access_token", bool(token))

    # 2. Start run via SSE — get run_id from stream
    print(f"\n2. Start run via SSE  (POST /apps/{EP_SLUG}/sse)")
    run_id = run_via_sse(token)
    ok("got run_id from SSE stream", bool(run_id), run_id)

    # 3. Poll DB until run reaches terminal state (workflow continues after SSE closes)
    print(f"\n3. Poll run until completed  (timeout={RUN_TIMEOUT}s)")
    run = poll_run(run_id, token)
    ok("run status=completed", run.get("status") == "completed",
       f"status={run.get('status')}  error={str(run.get('error',''))[:120]}")
    print(f"  iterations={run.get('iterations')}  "
          f"final_output[:80]={str(run.get('final_output',''))[:80]}")

    # Allow async artifact writes (tool calls run concurrently with run completion)
    print("  waiting 10s for async artifact writes to land...")
    time.sleep(10)

    # 4. Verify agent calls
    print("\n4. Verify agent calls")
    code, tasks = req("GET", f"/api/v1/runs/{run_id}/tasks", token=token)
    if code == 200 and isinstance(tasks, list):
        slugs = [t.get("agent_slug") or t.get("agent_name", "") for t in tasks]
        print(f"  task agent slugs: {slugs}")
        echo_called = any("echo" in (s or "").lower() for s in slugs)
        docu_called = any("docu" in (s or "").lower() for s in slugs)
    else:
        iters = run.get("iterations", 0)
        print(f"  tasks endpoint: {code} — using iterations={iters} as proxy")
        echo_called = iters >= 1
        docu_called = iters >= 2

    ok("a2a_echo was called",    echo_called)
    ok("docu_writer was called", docu_called)

    # 5. Fetch artifacts
    print("\n5. Fetch artifacts")
    code, artifacts = req("GET", f"/api/v1/runs/{run_id}/artifacts", token=token)
    ok("artifacts endpoint 200", code == 200, f"got {code}: {str(artifacts)[:120]}")
    ok("at least one artifact",
       isinstance(artifacts, list) and len(artifacts) > 0,
       f"got {len(artifacts) if isinstance(artifacts, list) else 0} artifact(s)")

    # 6. Validate artifact content
    print("\n6. Validate artifact")
    art = artifacts[0]
    print(f"  id={art.get('artifact_id')}  name={art.get('name')}")
    parts     = art.get("parts", [])
    file_part = next((p for p in parts if p.get("filename")), None)
    ok("artifact has file part with filename", file_part is not None,
       f"part keys: {[list(p.keys()) for p in parts]}")

    if file_part:
        fname = file_part.get("filename", "")
        mime  = file_part.get("mediaType") or file_part.get("media_type", "")
        html  = file_part.get("text", "")
        ok("filename=documentation.html", fname == "documentation.html", fname)
        ok("mediaType=text/html",         mime  == "text/html",          mime)
        ok("HTML content >100 chars",     len(html) > 100,               f"{len(html)} chars")
        ok("HTML contains <html or DOCTYPE",
           "<!DOCTYPE" in html or "<html" in html, html[:80])
        print(f"  HTML size : {len(html):,} chars")
        print(f"  HTML start: {html[:160].strip()}")

    print(f"\n{'='*60}")
    print("  ALL CHECKS PASSED")
    print(f"{'='*60}\n")


if __name__ == "__main__":
    main()
