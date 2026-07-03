"""
Transcription endpoint — per-orchestrator voice-to-text.

POST /api/v1/orchestrators/{name}/transcribe
  - Auth: require_jwt
  - Multipart field: audio (webm, wav, mp4, m4a)
  - Returns: {"text": "...", "provider": "openai", "model": "whisper-1"}
"""

import os
from typing import Optional

from fastapi import APIRouter, Depends, File, HTTPException, UploadFile, status
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app._deps import require_jwt
from app.database import get_db
from app.models import Orchestrator
from app.utils.crypto import decrypt_value
from app.utils.logger import logger

router = APIRouter(prefix="/orchestrators", tags=["transcription"])


class TranscriptionOut(BaseModel):
    text: str
    provider: str
    model: str


class TranscriptionError(BaseModel):
    error: str
    detail: Optional[str] = None


async def _get_orchestrator_by_name(db: AsyncSession, name: str) -> Orchestrator:
    result = await db.execute(select(Orchestrator).where(Orchestrator.name == name))
    row = result.scalar_one_or_none()
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f"Orchestrator '{name}' not found")
    return row


@router.post("/{name}/transcribe", response_model=TranscriptionOut)
async def transcribe_audio(
    name: str,
    audio: UploadFile = File(..., description="Audio file (webm, wav, mp4, m4a)"),
    user: dict = Depends(require_jwt),
    db: AsyncSession = Depends(get_db),
):
    """Transcribe uploaded audio using the orchestrator's configured provider."""
    orch = await _get_orchestrator_by_name(db, name)

    if not orch.voice_enabled:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Voice transcription is not enabled for this orchestrator",
        )

    provider = orch.transcription_provider
    model = orch.transcription_model

    if not provider:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="No transcription provider configured for this orchestrator",
        )
    if not model:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="No transcription model configured for this orchestrator",
        )

    # Resolve API key: stored encrypted key → env var fallback
    api_key: Optional[str] = None
    if orch.transcription_api_key_encrypted:
        try:
            api_key = decrypt_value(orch.transcription_api_key_encrypted)
        except Exception as exc:
            logger.warning("transcription: failed to decrypt api key", name=name, error=str(exc))

    if not api_key:
        env_var = "OPENAI_API_KEY" if provider == "openai" else "GROQ_API_KEY"
        api_key = os.environ.get(env_var)

    if not api_key:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=f"No API key available for provider '{provider}'",
        )

    audio_bytes = await audio.read()
    content_type = audio.content_type or "audio/webm"
    filename = audio.filename or "audio.webm"

    logger.info(
        "transcription: starting",
        name=name,
        provider=provider,
        model=model,
        user_id=user.get("sub"),
        bytes=len(audio_bytes),
    )

    try:
        if provider == "openai":
            import openai
            client = openai.AsyncOpenAI(api_key=api_key)
            transcript = await client.audio.transcriptions.create(
                model=model,
                file=(filename, audio_bytes, content_type),
            )
            text = transcript.text

        elif provider == "groq":
            from groq import AsyncGroq
            client = AsyncGroq(api_key=api_key)
            transcript = await client.audio.transcriptions.create(
                model=model,
                file=(filename, audio_bytes, content_type),
            )
            text = transcript.text

        else:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail=f"Unsupported transcription provider: '{provider}'",
            )

    except HTTPException:
        raise
    except Exception as exc:
        logger.error("transcription: provider error", name=name, provider=provider, error=str(exc))
        raise HTTPException(
            status_code=status.HTTP_502_BAD_GATEWAY,
            detail=f"Transcription provider error: {exc}",
        )

    logger.info("transcription: complete", name=name, provider=provider, chars=len(text))
    return TranscriptionOut(text=text, provider=provider, model=model)
