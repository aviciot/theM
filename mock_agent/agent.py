"""
Mock Odin Agent — speaks the Omni WS protocol.

Env vars:
  AGENT_NAME        display name, default "Mock Agent"
  AGENT_PERSONA     short persona description used in every reply
  AGENT_DELAY       seconds between token chunks, default 0.05
  PORT              WS port, default 9000
  AUTH_TOKEN        optional bearer token to require
"""

import asyncio
import json
import logging
import os
import time

import websockets

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("mock-agent")

AGENT_NAME    = os.getenv("AGENT_NAME", "Mock Agent")
AGENT_PERSONA = os.getenv("AGENT_PERSONA", "a helpful assistant")
AGENT_DELAY   = float(os.getenv("AGENT_DELAY", "0.05"))
PORT          = int(os.getenv("PORT", "9000"))
AUTH_TOKEN    = os.getenv("AUTH_TOKEN", "")


def build_reply(message: str) -> str:
    return (
        f"Hi! I'm {AGENT_NAME} — {AGENT_PERSONA}.\n\n"
        f"You asked: \"{message}\"\n\n"
        f"Here's my response: I've processed your request and this is a streamed "
        f"mock reply to demonstrate the Odin orchestration pipeline working end-to-end. "
        f"In production I'd be a real LLM-backed agent, but for now I'm confirming "
        f"that the WS protocol, adapter, and orchestrator loop all work correctly.\n\n"
        f"[agent={AGENT_NAME}, ts={int(time.time())}]"
    )


async def handle(ws) -> None:
    client = ws.remote_address
    log.info(f"Connection from {client}")

    # Optional auth check
    if AUTH_TOKEN:
        auth_header = ws.request_headers.get("Authorization", "")
        token = auth_header.removeprefix("Bearer ").strip()
        if token != AUTH_TOKEN:
            log.warning(f"Auth failed from {client}")
            await ws.close(1008, "Unauthorized")
            return

    try:
        async for raw in ws:
            try:
                msg = json.loads(raw)
            except json.JSONDecodeError:
                continue

            if msg.get("type") != "message":
                continue

            content = msg.get("content", "")
            log.info(f"Message from {client}: {content[:80]}")

            reply = build_reply(content)

            # Stream token by token (word chunks)
            words = reply.split(" ")
            for i, word in enumerate(words):
                chunk = word + (" " if i < len(words) - 1 else "")
                await ws.send(json.dumps({"type": "token", "text": chunk}))
                await asyncio.sleep(AGENT_DELAY)

            await ws.send(json.dumps({"type": "done", "result": reply}))
            log.info(f"Done streaming to {client}")

    except websockets.exceptions.ConnectionClosed:
        log.info(f"Connection closed: {client}")


async def main() -> None:
    log.info(f"Starting {AGENT_NAME} on ws://0.0.0.0:{PORT}")
    log.info(f"Persona: {AGENT_PERSONA}")
    async with websockets.serve(handle, "0.0.0.0", PORT):
        await asyncio.Future()  # run forever


if __name__ == "__main__":
    asyncio.run(main())
