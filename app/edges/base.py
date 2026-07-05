"""
Edge adapter abstraction (Phase 8.6).

An EdgeAdapter translates a client-native transport (WebSocket, REST, voice)
into a normalized EdgeRequest, and relays task_runner events back to the
client in that transport's encoding.

Every edge declared in orchestrator.edges must have a registered EdgeAdapter.
"""

import uuid
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any


@dataclass
class EdgeRequest:
    """Normalized inbound request, transport-agnostic."""
    orchestrator_name: str
    user_message: str
    user_id: int
    token_payload: dict
    session_id: uuid.UUID
    context_id: uuid.UUID
    modality: str = "text"          # "text" | "audio"
    extra: dict = field(default_factory=dict)


class EdgeAdapter(ABC):
    """ABC for all client transport adapters."""

    name: str  # Must match an entry in orchestrator.edges

    @abstractmethod
    async def emit(self, event: dict) -> None:
        """Send one task_runner event to the client."""
        ...

    @abstractmethod
    async def close(self) -> None:
        """Cleanly close the transport."""
        ...
