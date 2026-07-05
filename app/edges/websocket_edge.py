"""
WebSocket edge adapter (Phase 8.6).

Wraps a FastAPI WebSocket. emit() sends JSON. close() closes gracefully.
"""

from fastapi import WebSocket, WebSocketDisconnect

from app.edges.base import EdgeAdapter
from app.utils.logger import logger


class WebsocketEdge(EdgeAdapter):
    name = "websocket"

    def __init__(self, websocket: WebSocket, *, orchestrator_name: str = "", user_id: int = 0):
        self._ws = websocket
        self._orchestrator_name = orchestrator_name
        self._user_id = user_id

    async def emit(self, event: dict) -> None:
        try:
            await self._ws.send_json(event)
        except WebSocketDisconnect:
            logger.info(
                "ws_edge: client disconnected mid-run",
                orchestrator=self._orchestrator_name,
                user_id=self._user_id,
            )
            raise  # propagate so the caller can stop iterating

    async def close(self) -> None:
        try:
            await self._ws.close()
        except Exception:
            pass
