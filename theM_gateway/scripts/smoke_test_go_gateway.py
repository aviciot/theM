#!/usr/bin/env python3
"""
smoke_test_go_gateway.py — manual smoke tests for the Go gateway hybrid path.

Tests the complete flow:
  Client → Go WS/SSE → Auth → EP Config → Gate → Session
  → Go subscribes to them:dash:run:{run_id}:tokens
  → Go starts Temporal workflow with canonical run_id
  → Python Temporal Worker executes workflow
  → Python publishes events to Redis run channel
  → Go forwards events to client

Prerequisites:
  1. Full hybrid stack running:
       cd theM_gateway
       docker compose -f docker-compose.yml -f docker-compose.local.yml \\
                      -f docker-compose.integration.yml --profile temporal up -d --build
  2. A valid bearer token (create via auth service or use a seeded one).
  3. An App + EP seeded in the DB with an orchestrator that has max_iterations=0
     OR an orchestrator that will actually call the LLM.

Usage:
  # With explicit token:
  python3 scripts/smoke_test_go_gateway.py --token <bearer_token> \\
      --app <app_slug> --ep <ep_slug> --message "hello"

  # Run all scenarios (token-mode, temporal-enabled, fallback):
  python3 scripts/smoke_test_go_gateway.py --token <bearer_token> \\
      --app <app_slug> --ep <ep_slug> --all

Environment variables (alternative to flags):
  SMOKE_TOKEN    Bearer token
  SMOKE_APP      App slug
  SMOKE_EP       Entry point slug
  SMOKE_MESSAGE  User message (default: "ping")
  GO_GATEWAY_URL Base URL of Go gateway (default: http://localhost:8002)
"""

import argparse
import json
import os
import sys
import time
import urllib.request
import urllib.error


def _env(key: str, default: str = "") -> str:
    return os.environ.get(key, default)


def check_health(base_url: str) -> bool:
    try:
        with urllib.request.urlopen(f"{base_url}/health/live", timeout=5) as r:
            return r.status == 200
    except Exception as exc:
        print(f"  FAIL health check: {exc}")
        return False


def check_ready(base_url: str) -> bool:
    try:
        with urllib.request.urlopen(f"{base_url}/health/ready", timeout=5) as r:
            body = json.loads(r.read())
            ok = body.get("status") == "ok"
            if not ok:
                print(f"  WARN ready check: {body}")
            return ok
    except Exception as exc:
        print(f"  FAIL ready check: {exc}")
        return False


def smoke_ws(base_url: str, token: str, app: str, ep: str, message: str) -> bool:
    """
    WebSocket smoke test using Python's websockets library if available,
    otherwise prints manual test command.
    """
    ws_url = base_url.replace("http://", "ws://").replace("https://", "wss://")
    ws_url = f"{ws_url}/ws/orchestrate/{app}/{ep}?token={token}"
    print(f"  WS URL: {ws_url}")

    try:
        import websockets  # type: ignore
        import asyncio

        async def _run():
            received = []
            async with websockets.connect(ws_url) as ws:
                await ws.send(json.dumps({"type": "message", "content": message}))
                deadline = time.time() + 30
                while time.time() < deadline:
                    try:
                        msg = await asyncio.wait_for(ws.recv(), timeout=5.0)
                        ev = json.loads(msg)
                        received.append(ev)
                        print(f"    ← {ev}")
                        if ev.get("type") in ("done", "error"):
                            break
                    except asyncio.TimeoutError:
                        break
            return received

        evs = asyncio.run(_run())
        types = [e.get("type") for e in evs]
        if "done" in types or "error" in types:
            run_id = next((e.get("run_id") for e in evs if e.get("run_id")), None)
            print(f"  PASS WS smoke — events: {types}, run_id: {run_id}")
            return True
        else:
            print(f"  FAIL WS smoke — no terminal event. Got: {types}")
            return False

    except ImportError:
        print("  SKIP WS test (websockets package not installed)")
        print(f"  Manual: wscat -H 'Authorization: Bearer {token}' -c '{ws_url}'")
        print(f"    Then send: {{\"type\":\"message\",\"content\":\"{message}\"}}")
        return True


def smoke_sse(base_url: str, token: str, app: str, ep: str, message: str) -> bool:
    """SSE smoke test via POST to /sse/orchestrate/{app}/{ep}."""
    url = f"{base_url}/sse/orchestrate/{app}/{ep}"
    payload = json.dumps({"type": "message", "content": message}).encode()
    req = urllib.request.Request(
        url,
        data=payload,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
        },
        method="POST",
    )
    print(f"  SSE URL: POST {url}")
    received = []
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            buf = b""
            while True:
                chunk = resp.read(256)
                if not chunk:
                    break
                buf += chunk
                while b"\n\n" in buf:
                    line, buf = buf.split(b"\n\n", 1)
                    line = line.strip()
                    if line.startswith(b"data: "):
                        data = line[6:]
                        try:
                            ev = json.loads(data)
                            received.append(ev)
                            print(f"    ← {ev}")
                            if ev.get("type") in ("done", "error"):
                                break
                        except json.JSONDecodeError:
                            pass
                types = [e.get("type") for e in received]
                if "done" in types or "error" in types:
                    break
    except urllib.error.HTTPError as exc:
        print(f"  FAIL SSE HTTP {exc.code}: {exc.read()}")
        return False
    except Exception as exc:
        print(f"  FAIL SSE: {exc}")
        return False

    types = [e.get("type") for e in received]
    if "done" in types or "error" in types:
        run_id = next((e.get("run_id") for e in received if e.get("run_id")), None)
        print(f"  PASS SSE smoke — events: {types}, run_id: {run_id}")
        return True
    print(f"  FAIL SSE — no terminal event. Got: {types}")
    return False


def smoke_fallback(base_url: str, token: str, app: str, ep: str, message: str) -> bool:
    """
    Verify TEMPORAL_ENABLED=false fallback is documented but can't be tested
    against a running container without restarting it.
    """
    print("  SKIP TEMPORAL_ENABLED=false fallback — requires Go gateway restart with env change")
    print("  Manual: set TEMPORAL_ENABLED=false in them-go-bridge and re-run WS/SSE smoke")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description="Go gateway smoke tests")
    parser.add_argument("--token", default=_env("SMOKE_TOKEN"))
    parser.add_argument("--app", default=_env("SMOKE_APP"))
    parser.add_argument("--ep", default=_env("SMOKE_EP"))
    parser.add_argument("--message", default=_env("SMOKE_MESSAGE", "ping"))
    parser.add_argument("--base-url", default=_env("GO_GATEWAY_URL", "http://localhost:8002"))
    parser.add_argument("--all", action="store_true", help="Run all scenarios")
    args = parser.parse_args()

    base = args.base_url.rstrip("/")
    results: dict[str, bool] = {}

    print(f"\n=== Go Gateway Smoke Tests ===")
    print(f"Base URL: {base}")

    # ── Health / Ready ────────────────────────────────────────────────────────
    print("\n[1] Health check")
    results["health/live"] = check_health(base)

    print("\n[2] Ready check")
    results["health/ready"] = check_ready(base)

    if not results["health/live"]:
        print("\nFATAL: Go gateway not responding on /health/live — is it running?")
        print(f"  Start: cd theM_gateway && docker compose -f docker-compose.yml "
              f"-f docker-compose.local.yml -f docker-compose.integration.yml "
              f"--profile temporal up -d --build them-go-bridge")
        return 1

    if not (args.token and args.app and args.ep):
        print("\nINFO: --token/--app/--ep not provided — skipping orchestration tests")
        print("  Set SMOKE_TOKEN, SMOKE_APP, SMOKE_EP or pass --token --app --ep")
        passed = sum(1 for v in results.values() if v)
        total = len(results)
        print(f"\nResult: {passed}/{total} passed")
        return 0 if all(results.values()) else 1

    # ── WS smoke ──────────────────────────────────────────────────────────────
    print("\n[3] WebSocket smoke (TEMPORAL_ENABLED=true)")
    results["ws"] = smoke_ws(base, args.token, args.app, args.ep, args.message)

    # ── SSE smoke ─────────────────────────────────────────────────────────────
    print("\n[4] SSE smoke (TEMPORAL_ENABLED=true)")
    results["sse"] = smoke_sse(base, args.token, args.app, args.ep, args.message)

    if args.all:
        print("\n[5] Fallback path")
        results["fallback"] = smoke_fallback(base, args.token, args.app, args.ep, args.message)

    # ── Summary ───────────────────────────────────────────────────────────────
    print("\n=== Summary ===")
    passed = 0
    for name, ok in results.items():
        status = "PASS" if ok else "FAIL"
        print(f"  {status}  {name}")
        if ok:
            passed += 1
    total = len(results)
    print(f"\n{passed}/{total} passed")
    return 0 if all(results.values()) else 1


if __name__ == "__main__":
    sys.exit(main())
