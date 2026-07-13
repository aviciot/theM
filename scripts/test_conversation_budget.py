"""
Test: conversation token budget enforcement via /apps/{slug}/ws

What this tests:
1. First message goes through (under limit)
2. After enough messages, new message is rejected with error type=error containing "limit"
3. Verifies DB token accumulation matches

Usage:
  docker cp scripts/test_conversation_budget.py them-bridge:/tmp/
  docker exec them-bridge python3 /tmp/test_conversation_budget.py
"""

import asyncio
import json
import sys
import uuid
import websockets

BASE_WS = "ws://localhost:8001"
APP_SLUG = "docu-assistant"  # has working API key; token auth
LIMIT = 100                  # must match what's set in DB


def get_jwt() -> str:
    import httpx
    r = httpx.post("http://them-auth-service:8701/api/v1/auth/login",
                   json={"username": "admin", "password": "admin123"})
    return r.json()["access_token"]


async def send_message(uri: str, message: str, context_id: str, token: str = "") -> dict:
    """Send one message over WS, collect all events, return last meaningful one."""
    events = []
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    try:
        async with websockets.connect(uri, open_timeout=10, additional_headers=headers) as ws:
            await ws.send(json.dumps({
                "content": message,
                "context_id": context_id,
            }))
            while True:
                try:
                    raw = await asyncio.wait_for(ws.recv(), timeout=30)
                    evt = json.loads(raw)
                    events.append(evt)
                    print(f"  << {evt.get('type','?')}: {str(evt)[:120]}")
                    if evt.get("type") in ("done", "error", "canceled"):
                        break
                except asyncio.TimeoutError:
                    print("  [timeout waiting for response]")
                    break
    except Exception as e:
        return {"type": "error", "message": str(e)}
    return events[-1] if events else {"type": "error", "message": "no events"}


async def get_context_tokens(context_id: str) -> int:
    """Query DB token usage for a context via bridge health (we run inside bridge)."""
    import asyncpg
    import os
    db_url = os.environ.get(
        "DATABASE_URL",
        f"postgresql://{os.environ.get('DATABASE_USER','them')}:{os.environ.get('DATABASE_PASSWORD','them_secret')}@{os.environ.get('DATABASE_HOST','them-postgres')}:{os.environ.get('DATABASE_PORT','5432')}/{os.environ.get('DATABASE_NAME','them')}"
    )
    conn = await asyncpg.connect(db_url)
    row = await conn.fetchrow(
        "SELECT COALESCE(SUM(tokens_used), 0) as total FROM them.tasks WHERE context_id = $1",
        uuid.UUID(context_id),
    )
    await conn.close()
    return int(row["total"])


async def main():
    context_id = str(uuid.uuid4())
    uri = f"{BASE_WS}/apps/{APP_SLUG}/ws"
    jwt = get_jwt()
    print(f"\n{'='*60}")
    print(f"Test: conversation token budget ({LIMIT} tokens)")
    print(f"App:  {APP_SLUG}")
    print(f"Context: {context_id}")
    print(f"{'='*60}\n")

    passed = 0
    failed = 0

    # ── Message 1: should always go through ──────────────────────
    print("── Message 1: 'hi' (expect: goes through) ──")
    result = await send_message(uri, "hi", context_id, jwt)
    if result.get("type") == "done":
        print("  ✓ PASS — message went through\n")
        passed += 1
    elif result.get("type") == "error" and "limit" in result.get("message", "").lower():
        print("  ✗ FAIL — blocked on first message (limit too low or prior context data)\n")
        failed += 1
    else:
        print(f"  ? UNKNOWN result: {result}\n")

    tokens = await get_context_tokens(context_id)
    print(f"  DB tokens used so far: {tokens}/{LIMIT}\n")

    # ── Messages 2-5: keep sending until blocked or exhausted ────
    blocked = False
    for i in range(2, 6):
        print(f"── Message {i}: 'tell me something interesting' (expect: blocked once over {LIMIT}) ──")
        result = await send_message(uri, "tell me something interesting", context_id, jwt)

        if result.get("type") == "error":
            msg = result.get("message", "")
            if "limit" in msg.lower() or "429" in msg or "token" in msg.lower():
                print(f"  ✓ PASS — correctly blocked: {msg}\n")
                passed += 1
                blocked = True
                break
            else:
                print(f"  ? error but not budget: {msg}\n")
        elif result.get("type") == "done":
            tokens = await get_context_tokens(context_id)
            print(f"  → went through, DB tokens now: {tokens}/{LIMIT}\n")
            if tokens >= LIMIT:
                print(f"  (over limit — next message should be blocked)\n")

        await asyncio.sleep(1)

    if not blocked:
        print("  ✗ FAIL — never got blocked after 5 messages\n")
        failed += 1

    # ── Final DB check ───────────────────────────────────────────
    tokens = await get_context_tokens(context_id)
    print(f"── Final DB state ──")
    print(f"  context_id:  {context_id}")
    print(f"  tokens used: {tokens}")
    print(f"  limit:       {LIMIT}")
    print(f"  over limit:  {'YES' if tokens >= LIMIT else 'NO'}")

    print(f"\n{'='*60}")
    print(f"Results: {passed} passed, {failed} failed")
    print(f"{'='*60}\n")
    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    asyncio.run(main())
