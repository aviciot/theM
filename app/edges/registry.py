"""
Edge registry (Phase 8.6, updated Phase 10).

Maps edge name → EdgeAdapter class.
Raises ValueError for unknown names so orchestrator validation fails loudly.

Registered edges:
  websocket — WebsocketEdge (bidirectional JSON over WS)
  sse       — SSEEdge (streaming HTTP Server-Sent Events)
  webrtc    — future (not yet implemented)
"""

from typing import Type

from app.edges.base import EdgeAdapter
from app.edges.websocket_edge import WebsocketEdge
from app.edges.sse_edge import SSEEdge

_REGISTRY: dict[str, Type[EdgeAdapter]] = {
    WebsocketEdge.name: WebsocketEdge,
    SSEEdge.name:       SSEEdge,
}

VALID_EDGES = frozenset(_REGISTRY.keys())


def get_edge_class(name: str) -> Type[EdgeAdapter]:
    """Return the EdgeAdapter class for the given edge name."""
    cls = _REGISTRY.get(name)
    if cls is None:
        raise ValueError(
            f"Unknown edge {name!r}. Valid edges: {sorted(VALID_EDGES)}"
        )
    return cls
