"""
Voice edge adapter stub (Phase 8.6).

Not yet implemented. Registered so the edge registry knows the name.
Voice will reuse orchestrator.transcription_* / tts_* columns when shipped.
"""

from app.edges.base import EdgeAdapter


class VoiceEdge(EdgeAdapter):
    name = "voice"

    async def emit(self, event: dict) -> None:
        raise NotImplementedError("voice edge not yet implemented")

    async def close(self) -> None:
        pass
