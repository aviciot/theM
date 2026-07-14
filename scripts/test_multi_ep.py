"""
Multi-entry-point integration test.
Creates an app with 2 WS entry points, runs 2 parallel WS sessions on each
(4 total), verifies entry_point_slug is set correctly in them.runs.
"""
import asyncio
import json
import uuid
import httpx
import websockets
import sys

AUTH_URL  = "http://them-auth-service:8701/api/v1/auth/login"
BRIDGE    = "http://localhost:8001"
WS_BASE   = "ws://localhost:8001"
ORCH_ID   = "3f2d0d96-1150-4895-88a7-26a668e24cf0"   # echo_test orchestrator
ORCH_NAME = "echo_test"

SLUG_A = f"meptest-door-a-{uuid.uuid4().hex[:6]}"
SLUG_B = f"meptest-door-b-{uuid.uuid4().hex[:6]}"

results: dict = {}


async def get_token(client: httpx.AsyncClient) -> str:
    r = await client.post(AUTH_URL, json={"username": "admin", "password": "admin123"})
    r.raise_for_status()
    return r.json()["access_token"]


async def create_app(client: httpx.AsyncClient, token: str) -> dict:
    r = await client.post(
        f"{BRIDGE}/api/v1/admin/applications",
        headers={"Authorization": f"Bearer {token}"},
        json={
            "name": "Multi-EP Integration Test",
            "orchestrator_id": ORCH_ID,
            "enabled": True,
            "entry_points": [
                {"slug": SLUG_A, "entry_point_type": "websocket",
                 "access_policy": {"mode": "public"}, "conversation_token_limit": None, "enabled": True},
                {"slug": SLUG_B, "entry_point_type": "websocket",
                 "access_policy": {"mode": "public"}, "conversation_token_limit": None, "enabled": True},
            ],
        },
    )
    if r.status_code != 201:
        print(f"[FAIL] create app: {r.status_code} {r.text}")
        sys.exit(1)
    app = r.json()
    print(f"[OK] Created app id={app['id'][:8]} name={app['name']!r}")
    for ep in app["entry_points"]:
        print(f"     EP: {ep['slug']} [{ep['entry_point_type']}]")
    return app


async def run_ws_session(slug: str, message: str, label: str) -> str | None:
    """Open a WS, send one message, collect tokens until done, return run_id."""
    url = f"{WS_BASE}/apps/{slug}/ws"
    run_id = None
    output_tokens = []
    try:
        async with websockets.connect(url, open_timeout=10) as ws:
            await ws.send(json.dumps({"type": "message", "content": message}))
            async for raw in ws:
                msg = json.loads(raw)
                t = msg.get("type")
                if t == "ready":
                    run_id = msg.get("run_id")
                    print(f"  [{label}] ready run_id={str(run_id)[:8] if run_id else '?'}")
                elif t == "token":
                    output_tokens.append(msg.get("text", ""))
                elif t == "done":
                    print(f"  [{label}] done  run_id={str(run_id)[:8] if run_id else '?'} tokens={len(output_tokens)}")
                    break
                elif t == "error":
                    print(f"  [{label}] ERROR: {msg.get('message')}")
                    break
    except Exception as e:
        print(f"  [{label}] EXCEPTION: {e}")
    return run_id


async def verify_runs(client: httpx.AsyncClient, token: str, run_ids: list[str], expected_slugs: dict[str, str]):
    """Query each run and check entry_point_slug."""
    headers = {"Authorization": f"Bearer {token}"}
    print("\n── Run verification ──────────────────────────────────────")
    all_ok = True
    for run_id in run_ids:
        if not run_id:
            print(f"  [SKIP] no run_id")
            continue
        r = await client.get(f"{BRIDGE}/api/v1/runs/{run_id}", headers=headers)
        if r.status_code != 200:
            print(f"  [FAIL] GET /runs/{run_id[:8]}: {r.status_code}")
            all_ok = False
            continue
        run = r.json()
        ep_slug = run.get("entry_point_slug")
        expected = expected_slugs.get(run_id)
        ok = ep_slug == expected
        mark = "[OK]" if ok else "[FAIL]"
        print(f"  {mark} run={run_id[:8]} entry_point_slug={ep_slug!r} (expected {expected!r})")
        if not ok:
            all_ok = False
    return all_ok


async def delete_app(client: httpx.AsyncClient, token: str, app_id: str):
    r = await client.delete(
        f"{BRIDGE}/api/v1/admin/applications/{app_id}",
        headers={"Authorization": f"Bearer {token}"},
    )
    if r.status_code == 204:
        print(f"\n[OK] Deleted test app {app_id[:8]}")
    else:
        print(f"\n[WARN] Delete returned {r.status_code}")


async def main():
    async with httpx.AsyncClient(timeout=30) as client:
        token = await get_token(client)
        app   = await create_app(client, token)
        app_id = app["id"]

        print(f"\n── Running 4 parallel WS sessions ────────────────────────")
        print(f"   Door A slug: {SLUG_A}")
        print(f"   Door B slug: {SLUG_B}")

        # 2 sessions on door-a, 2 on door-b — all in parallel
        tasks = await asyncio.gather(
            run_ws_session(SLUG_A, "Say the word ALPHA and nothing else.", f"A1/{SLUG_A}"),
            run_ws_session(SLUG_A, "Say the word ALPHA and nothing else.", f"A2/{SLUG_A}"),
            run_ws_session(SLUG_B, "Say the word BETA and nothing else.",  f"B1/{SLUG_B}"),
            run_ws_session(SLUG_B, "Say the word BETA and nothing else.",  f"B2/{SLUG_B}"),
        )
        run_id_a1, run_id_a2, run_id_b1, run_id_b2 = tasks

        expected = {}
        for rid in [run_id_a1, run_id_a2]:
            if rid: expected[rid] = SLUG_A
        for rid in [run_id_b1, run_id_b2]:
            if rid: expected[rid] = SLUG_B

        all_run_ids = [r for r in [run_id_a1, run_id_a2, run_id_b1, run_id_b2] if r]
        ok = await verify_runs(client, token, all_run_ids, expected)

        await delete_app(client, token, app_id)

        print(f"\n{'='*50}")
        if ok and len(all_run_ids) == 4:
            print("RESULT: ALL PASS")
        else:
            print(f"RESULT: ISSUES FOUND (got {len(all_run_ids)}/4 run_ids, verify={'ok' if ok else 'FAIL'})")


asyncio.run(main())
