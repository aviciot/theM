"""
REST edge adapter stub (Phase 8.6).

Not yet implemented. Registered so the edge registry knows the name.
"""

from app.edges.base import EdgeAdapter


class RestEdge(EdgeAdapter):
    name = "rest"

    async def emit(self, event: dict) -> None:
        raise NotImplementedError("rest edge not yet implemented")

    async def close(self) -> None:
        pass
