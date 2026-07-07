#!/usr/bin/env python3
"""
Multi-turn conversation test.

Tests that the LLM sees prior conversation turns across separate WS connections
using the same context_id. Each turn opens a fresh WebSocket (simulating a
browser reconnect) but carries the same context_id — proving history is
reconstructed from Postgres, not held in memory.

Usage:
    python scripts/test_multiturn.py

Requires: websockets (pip install websockets)
Stack must be running with a2a_echo agent enabled.
"""

import asyncio
import json
import sys
import uuid

try:
    import websockets
except ImportError:
    print("FAIL: pip install websockets")
    sys.exit(1)

# When run from host: WS goes through Traefik on 8088.
# When run from inside bridge container: WS goes direct on 8001.
import os
_IN_DOCKER = os.path.exists("/.dockerenv")
BASE_URL = "ws://localhost:8001" if _IN_DOCKER else "ws://localhost:8088"
AUTH_URL = (
    "http://them-auth-service:8701/api/v1/auth/login"
    if _IN_DOCKER
    else "http://localhost:8088/api/v1/auth/login"
)
ORCH = "echo_test"

# ── Redis cache bust ─────────────────────────────────────────────────────────

def _bust_cache(orch_name: str) -> None:
    """Delete the orchestrator Redis cache key so the next request reloads from DB."""
    redis_host = "them-redis" if _IN_DOCKER else "localhost"
    try:
        import socket, struct
        # Minimal inline Redis DEL via raw socket — avoids needing redis-py or subprocess
        key = f"them:orchestrators:{orch_name}".encode()
        cmd = (
            f"*2\r\n$3\r\nDEL\r\n${len(key)}\r\n".encode() + key + b"\r\n"
        )
        with socket.create_connection((redis_host, 6379), timeout=3) as s:
            s.sendall(cmd)
            s.recv(64)
    except Exception:
        pass  # best-effort; test will still work on next TTL expiry


# ── Auth ──────────────────────────────────────────────────────────────────────

async def get_jwt() -> str:
    import urllib.request
    body = json.dumps({"username": "admin", "password": "admin123"}).encode()
    req = urllib.request.Request(
        AUTH_URL,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.loads(resp.read())
    token = data.get("access_token")
    if not token:
        raise RuntimeError(f"No access_token in response: {data}")
    return token


# ── WS turn ──────────────────────────────────────────────────────────────────

async def ws_turn(jwt: str, message: str, context_id: str) -> dict:
    """
    Opens a fresh WS connection, sends one message, collects all events until
    done/error, closes the connection. Returns a summary dict.
    """
    url = f"{BASE_URL}/ws/orchestrate/{ORCH}?token={jwt}"
    tokens = []
    events = []

    async with websockets.connect(url, open_timeout=10) as ws:
        payload = {"type": "message", "content": message, "context_id": context_id}
        await ws.send(json.dumps(payload))

        async for raw in ws:
            ev = json.loads(raw)
            events.append(ev)
            if ev.get("type") == "token":
                tokens.append(ev.get("text", ""))
            if ev.get("type") in ("done", "error"):
                break

    return {
        "answer": "".join(tokens),
        "events": events,
        "context_id": context_id,
    }


# ── Checks ───────────────────────────────────────────────────────────────────

def check(label: str, cond: bool, detail: str = "") -> bool:
    status = "PASS" if cond else "FAIL"
    suffix = f"  ({detail})" if detail and not cond else ""
    print(f"  [{status}] {label}{suffix}")
    return cond


def section(title: str):
    print(f"\n=== {title} ===")


# ── Main ─────────────────────────────────────────────────────────────────────

async def main():
    print("=" * 50)
    print("  Multi-Turn Conversation Test")
    print("=" * 50)

    passed = failed = 0

    def c(label, cond, detail=""):
        nonlocal passed, failed
        ok = check(label, cond, detail)
        if ok:
            passed += 1
        else:
            failed += 1
        return ok

    # ── Auth ──────────────────────────────────────────────────────────────
    section("Auth")
    try:
        jwt = await get_jwt()
        c("Got JWT from auth service", bool(jwt))
    except Exception as exc:
        c("Got JWT from auth service", False, str(exc))
        print("\nCannot proceed without JWT.")
        sys.exit(1)

    context_id = str(uuid.uuid4())
    print(f"\n  context_id: {context_id}")

    # ── Turn 1 ────────────────────────────────────────────────────────────
    section("Turn 1 — introduce a number")
    turn1_msg = "My lucky number is 42. Please echo that back to me."
    try:
        t1 = await ws_turn(jwt, turn1_msg, context_id)
        c("Turn 1 completed (got events)", len(t1["events"]) > 0)
        c("Turn 1 got done event", any(e.get("type") == "done" for e in t1["events"]))
        c("Turn 1 has an answer", len(t1["answer"]) > 0)
        print(f"\n  Answer: {t1['answer'][:200]}")
    except Exception as exc:
        c("Turn 1 completed", False, str(exc))
        print("\nCannot proceed after turn 1 failure.")
        sys.exit(1)

    # ── Turn 2 — same context_id, new WS connection ───────────────────────
    section("Turn 2 — recall from prior turn (new WS connection)")
    turn2_msg = "What was the lucky number I mentioned? Do not use any agents, just answer from our conversation."
    try:
        t2 = await ws_turn(jwt, turn2_msg, context_id)
        c("Turn 2 completed (got events)", len(t2["events"]) > 0)
        c("Turn 2 got done event", any(e.get("type") == "done" for e in t2["events"]))
        c("Turn 2 has an answer", len(t2["answer"]) > 0)

        # The LLM should recall "42" from the prior turn
        answer_lower = t2["answer"].lower()
        c("Answer contains '42' (prior turn recalled)", "42" in answer_lower,
          f"got: {t2['answer'][:200]}")
        print(f"\n  Answer: {t2['answer'][:300]}")
    except Exception as exc:
        c("Turn 2 completed", False, str(exc))

    # ── Turn 3 — build on turn 2 ──────────────────────────────────────────
    section("Turn 3 — build on accumulating context (new WS connection)")
    turn3_msg = "Now double that lucky number and tell me the result."
    try:
        t3 = await ws_turn(jwt, turn3_msg, context_id)
        c("Turn 3 completed (got events)", len(t3["events"]) > 0)
        c("Turn 3 got done event", any(e.get("type") == "done" for e in t3["events"]))
        c("Turn 3 has an answer", len(t3["answer"]) > 0)

        # 42 * 2 = 84
        answer_lower = t3["answer"].lower()
        c("Answer contains '84' (42 doubled)", "84" in answer_lower,
          f"got: {t3['answer'][:200]}")
        print(f"\n  Answer: {t3['answer'][:300]}")
    except Exception as exc:
        c("Turn 3 completed", False, str(exc))

    # ── Window test ───────────────────────────────────────────────────────
    # Set history_window=1, run 3 turns. Turn 3 should recall turn 2 but NOT turn 1.
    section("Window test — history_window=1 (only last turn visible)")
    try:
        # Find orchestrator ID
        api_base = "http://localhost:8001" if _IN_DOCKER else "http://localhost:8088"
        import urllib.request as _ur
        req = _ur.Request(
            f"{api_base}/api/v1/admin/orchestrators",
            headers={"Authorization": f"Bearer {jwt}"},
        )
        with _ur.urlopen(req, timeout=10) as r:
            orchs = json.loads(r.read())
        orch_row = next((o for o in orchs if o["name"] == ORCH), None)
        c("Found orchestrator via API", orch_row is not None)

        if orch_row:
            orch_id = orch_row["id"]
            # Set history_window=1
            patch_body = json.dumps({"history_window": 1}).encode()
            req = _ur.Request(
                f"{api_base}/api/v1/admin/orchestrators/{orch_id}",
                data=patch_body,
                headers={"Authorization": f"Bearer {jwt}", "Content-Type": "application/json"},
                method="PATCH",
            )
            with _ur.urlopen(req, timeout=10) as r:
                updated = json.loads(r.read())
            c("history_window set to 1", updated.get("history_window") == 1)

            # Bust Redis cache
            _bust_cache(ORCH)

            win_ctx = str(uuid.uuid4())
            # Turn A: introduce colour
            tA = await ws_turn(jwt, "My favourite colour is blue.", win_ctx)
            c("Window turn A completed", any(e.get("type") == "done" for e in tA["events"]))
            print(f"\n  Turn A answer: {tA['answer'][:120]}")

            # Turn B: introduce food (this is the turn that should stay in window)
            tB = await ws_turn(jwt, "My favourite food is pizza.", win_ctx)
            c("Window turn B completed", any(e.get("type") == "done" for e in tB["events"]))
            print(f"\n  Turn B answer: {tB['answer'][:120]}")

            # Turn C: ask about both — with window=1 only turn B is in context
            tC = await ws_turn(
                jwt,
                "What is my favourite colour and my favourite food? Answer only from our conversation, do not guess.",
                win_ctx,
            )
            c("Window turn C completed", any(e.get("type") == "done" for e in tC["events"]))
            ans = tC["answer"].lower()
            print(f"\n  Turn C answer: {tC['answer'][:300]}")
            c("Turn C knows food (pizza — in window)", "pizza" in ans, f"got: {ans[:200]}")
            # LLM should say it doesn't know the colour — look for explicit "cannot" / "don't know" / "not mentioned"
            colour_forgotten = (
                "blue" not in ans
                or "cannot" in ans
                or "don't know" in ans
                or "not mentioned" in ans
                or "didn't mention" in ans
                or "haven't mentioned" in ans
            )
            c("Turn C does NOT recall colour (outside window)", colour_forgotten, f"got: {ans[:200]}")

            # Restore history_window=20
            patch_body = json.dumps({"history_window": 20}).encode()
            req = _ur.Request(
                f"{api_base}/api/v1/admin/orchestrators/{orch_id}",
                data=patch_body,
                headers={"Authorization": f"Bearer {jwt}", "Content-Type": "application/json"},
                method="PATCH",
            )
            with _ur.urlopen(req, timeout=10) as r:
                restored = json.loads(r.read())
            c("history_window restored to 20", restored.get("history_window") == 20)
            _bust_cache(ORCH)

    except Exception as exc:
        c("Window test", False, str(exc))

    # ── DB verification ───────────────────────────────────────────────────
    section("DB — task_messages saved for context")
    try:
        if _IN_DOCKER:
            # Inside bridge: use psycopg2 or sqlalchemy directly via env
            import subprocess as _sp
            db_url = os.environ.get("DATABASE_URL", "")
            if not db_url:
                # Parse from bridge env vars
                db_url = "postgresql://them:%(pw)s@them-postgres:5432/them" % {
                    "pw": os.environ.get("POSTGRES_PASSWORD", "them")
                }
            # Run psql via docker exec on the postgres container hostname isn't
            # available here — use Python psycopg2 if present
            try:
                import psycopg2
                conn = psycopg2.connect(db_url)
                cur = conn.cursor()
                cur.execute(
                    "SELECT t.kind, t.state, count(tm.id) AS msg_count "
                    "FROM them.tasks t "
                    "LEFT JOIN them.task_messages tm ON tm.task_id = t.id "
                    "WHERE t.context_id = %s AND t.kind = 'root' "
                    "GROUP BY t.id, t.kind, t.state ORDER BY t.created_at",
                    (context_id,)
                )
                rows = cur.fetchall()
                conn.close()
                print(f"\n  Rows: {rows}")
                c("3 root tasks created for context", len(rows) == 3, f"found {len(rows)}")
                c("Each root task has task_messages", all(r[2] > 0 for r in rows) if rows else False,
                  f"counts: {[r[2] for r in rows]}")
            except ImportError:
                c("DB check skipped (psycopg2 not available)", True)
        else:
            result = __import__("subprocess").run(
                ["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them",
                 "-c",
                 f"SELECT t.kind, t.state, count(tm.id) AS msg_count "
                 f"FROM them.tasks t "
                 f"LEFT JOIN them.task_messages tm ON tm.task_id = t.id "
                 f"WHERE t.context_id = '{context_id}' AND t.kind = 'root' "
                 f"GROUP BY t.id, t.kind, t.state ORDER BY t.created_at;"],
                capture_output=True, text=True,
            )
            output = result.stdout.strip()
            print(f"\n  {output}")
            lines = [l for l in output.splitlines() if "root" in l]
            c("3 root tasks created for context", len(lines) == 3, f"found {len(lines)}")
            c("Each root task has task_messages",
              all(l.split("|")[-1].strip() != "0" for l in lines) if lines else False)
    except Exception as exc:
        c("DB verification", False, str(exc))

    # ── Summary ───────────────────────────────────────────────────────────
    print(f"\n{'=' * 50}")
    print(f"  {passed} passed, {failed} failed")
    print(f"{'=' * 50}")
    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    asyncio.run(main())
