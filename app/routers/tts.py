"""
TTS endpoint — per-orchestrator text-to-speech.

POST /api/v1/orchestrators/{name}/tts
  - Auth: require_jwt
  - Body: {"text": "..."}
  - Returns: audio/mpeg stream
"""

import os
from typing import Optional

from fastapi import APIRouter, Depends, HTTPException, status
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app._deps import require_jwt
from app.database import get_db
from app.models import Orchestrator
from app.utils.crypto import decrypt_value
from app.utils.logger import logger

router = APIRouter(prefix="/orchestrators", tags=["tts"])


class TTSRequest(BaseModel):
    text: str


async def _get_orchestrator_by_name(db: AsyncSession, name: str) -> Orchestrator:
    result = await db.execute(select(Orchestrator).where(Orchestrator.name == name))
    row = result.scalar_one_or_none()
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f"Orchestrator '{name}' not found")
    return row


@router.post("/{name}/tts")
async def text_to_speech(
    name: str,
    body: TTSRequest,
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    orch = await _get_orchestrator_by_name(db, name)

    if not orch.tts_enabled:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="TTS not enabled for this orchestrator")

    provider = orch.tts_provider or "openai"
    voice = orch.tts_voice or "nova"

    api_key: Optional[str] = None
    if orch.tts_api_key_encrypted:
        try:
            api_key = decrypt_value(orch.tts_api_key_encrypted)
        except Exception:
            pass
    if not api_key:
        api_key = os.environ.get("OPENAI_API_KEY")
    if not api_key:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="No TTS API key available")

    text = body.text.strip()
    if not text:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="Empty text")

    # Truncate to avoid huge TTS bills on runaway responses
    if len(text) > 4000:
        text = text[:4000]

    logger.info("tts: synthesizing", name=name, provider=provider, voice=voice, chars=len(text))

    try:
        if provider == "openai":
            import openai
            client = openai.AsyncOpenAI(api_key=api_key)
            response = await client.audio.speech.create(
                model="tts-1",
                voice=voice,
                input=text,
                response_format="mp3",
            )
            audio_bytes = await response.aread()

            async def audio_stream():
                yield audio_bytes

            return StreamingResponse(audio_stream(), media_type="audio/mpeg")
        else:
            raise HTTPException(status_code=400, detail=f"Unsupported TTS provider: {provider}")

    except HTTPException:
        raise
    except Exception as exc:
        logger.error("tts: provider error", name=name, error=str(exc))
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=f"TTS error: {exc}")
