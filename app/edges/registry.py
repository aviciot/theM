"""
Edge registry (Phase 8.6).

Maps edge name → EdgeAdapter class.
Raises ValueError for unknown names so orchestrator validation fails loudly.
"""

from typing import Type

from app.edges.base import EdgeAdapter
from app.edges.websocket_edge import WebsocketEdge
from app.edges.voice_edge import VoiceEdge
from app.edges.rest_edge import RestEdge

_REGISTRY: dict[str, Type[EdgeAdapter]] = {
    WebsocketEdge.name: WebsocketEdge,
    VoiceEdge.name:     VoiceEdge,
    RestEdge.name:      RestEdge,
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
