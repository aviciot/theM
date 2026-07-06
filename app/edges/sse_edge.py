"""
SSE (Server-Sent Events) edge adapter.

Wraps an asyncio Queue. The route handler reads from the queue and writes
SSE frames to the HTTP response. emit() enqueues events; close() sends the
terminal sentinel so the response loop exits cleanly.

Token events become `data: <text>\n\n` SSE frames.
All other events are sent as `event: <type>\ndata: <json>\n\n`.
A `done` sentinel frame closes the stream.
"""

import asyncio
import json

from app.edges.base import EdgeAdapter


_SENTINEL = object()


class SSEEdge(EdgeAdapter):
    name = "sse"

    def __init__(self) -> None:
        self._queue: asyncio.Queue = asyncio.Queue()

    async def emit(self, event: dict) -> None:
        await self._queue.put(event)

    async def close(self) -> None:
        await self._queue.put(_SENTINEL)

    async def stream(self):
        """Async generator yielding raw SSE-formatted byte strings."""
        while True:
            item = await self._queue.get()
            if item is _SENTINEL:
                yield b"event: done\ndata: {}\n\n"
                return
            event_type = item.get("type", "event")
            if event_type == "token":
                text = item.get("text", "")
                safe = text.replace("\n", "\\n")
                yield f"data: {safe}\n\n".encode()
            else:
                payload = json.dumps(item)
                yield f"event: {event_type}\ndata: {payload}\n\n".encode()
