"""
Admin — Orchestrators
CRUD for them.orchestrators. Publishes them:orchestrators:changed on any write.
"""

import uuid
from decimal import Decimal
from typing import List, Optional

import httpx
from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.database import get_db, get_redis
from app.models import Orchestrator
from app.utils.crypto import encrypt_value, decrypt_value, key_hint
from app.utils.logger import logger

router = APIRouter(prefix="/admin/orchestrators", tags=["admin-orchestrators"])

_CHANGE_CHANNEL = "them:orchestrators:changed"
_CACHE_PREFIX = "them:orchestrators:"
_TTL = 600


# ------------------------------------------------------------------ #
# Pydantic schemas                                                     #
# ------------------------------------------------------------------ #

class OrchestratorCreate(BaseModel):
    name: str = Field(..., description="Unique slug used in /ws/orchestrate/{name}")
    display_name: str
    system_prompt: str = ""
    allowed_agent_ids: List[uuid.UUID] = Field(default_factory=list)
    llm_provider: Optional[str] = None
    llm_model: Optional[str] = None
    llm_api_key: Optional[str] = None
    llm_base_url: Optional[str] = None
    max_iterations: int = 10
    max_parallel_tools: int = 4
    rate_limit_rpm: int = 30
    daily_budget_usd: Decimal = Decimal("0")
    enabled: bool = True
    voice_enabled: bool = False
    transcription_provider: Optional[str] = None
    transcription_model: Optional[str] = None
    transcription_api_key: Optional[str] = None
    tts_enabled: bool = False
    tts_provider: Optional[str] = None
    tts_voice: Optional[str] = None
    tts_api_key: Optional[str] = None
    memory_enabled: bool = False
    summarize_every_n_calls: int = 3
    memory_raw_fallback_n: int = 5
    summarizer_provider: Optional[str] = None
    summarizer_model: Optional[str] = None
    summarizer_api_key: Optional[str] = None
    history_window: int = 20
    budget_tokens: Optional[int] = None


class OrchestratorUpdate(BaseModel):
    display_name: Optional[str] = None
    system_prompt: Optional[str] = None
    allowed_agent_ids: Optional[List[uuid.UUID]] = None
    llm_provider: Optional[str] = None
    llm_model: Optional[str] = None
    llm_api_key: Optional[str] = None       # blank = keep existing
    llm_base_url: Optional[str] = None
    max_iterations: Optional[int] = None
    max_parallel_tools: Optional[int] = None
    rate_limit_rpm: Optional[int] = None
    daily_budget_usd: Optional[Decimal] = None
    enabled: Optional[bool] = None
    voice_enabled: Optional[bool] = None
    transcription_provider: Optional[str] = None
    transcription_model: Optional[str] = None
    transcription_api_key: Optional[str] = None
    tts_enabled: Optional[bool] = None
    tts_provider: Optional[str] = None
    tts_voice: Optional[str] = None
    tts_api_key: Optional[str] = None
    memory_enabled: Optional[bool] = None
    summarize_every_n_calls: Optional[int] = None
    memory_raw_fallback_n: Optional[int] = None
    summarizer_provider: Optional[str] = None
    summarizer_model: Optional[str] = None
    summarizer_api_key: Optional[str] = None
    history_window: Optional[int] = None
    budget_tokens: Optional[int] = None


class OrchestratorOut(BaseModel):
    id: uuid.UUID
    name: str
    display_name: str
    system_prompt: str
    allowed_agent_ids: List[uuid.UUID]
    llm_provider: Optional[str]
    llm_model: Optional[str]
    llm_api_key_hint: Optional[str]         # masked, e.g. sk-a••••••••1234
    llm_base_url: Optional[str]
    max_iterations: int
    max_parallel_tools: int
    rate_limit_rpm: int
    daily_budget_usd: Decimal
    enabled: bool
    voice_enabled: bool
    transcription_provider: Optional[str]
    transcription_model: Optional[str]
    transcription_api_key_hint: Optional[str]
    tts_enabled: bool
    tts_provider: Optional[str]
    tts_voice: Optional[str]
    tts_api_key_hint: Optional[str]
    memory_enabled: bool = False
    summarize_every_n_calls: int = 3
    memory_raw_fallback_n: int = 5
    summarizer_provider: Optional[str] = None
    summarizer_model: Optional[str] = None
    summarizer_api_key_hint: Optional[str] = None
    history_window: int = 20
    budget_tokens: Optional[int] = None

    class Config:
        from_attributes = True


class LLMTestRequest(BaseModel):
    provider: str
    model: str
    api_key: Optional[str] = None           # plaintext; if omitted use stored key
    base_url: Optional[str] = None


class LLMTestResult(BaseModel):
    ok: bool
    latency_ms: Optional[int] = None
    error: Optional[str] = None


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _row_to_out(row: Orchestrator) -> OrchestratorOut:
    return OrchestratorOut(
        id=row.id,
        name=row.name,
        display_name=row.display_name,
        system_prompt=row.system_prompt or "",
        allowed_agent_ids=list(row.allowed_agent_ids or []),
        llm_provider=row.llm_provider,
        llm_model=row.llm_model,
        llm_api_key_hint=key_hint(row.llm_api_key_encrypted) if row.llm_api_key_encrypted else None,
        llm_base_url=row.llm_base_url,
        max_iterations=row.max_iterations,
        max_parallel_tools=row.max_parallel_tools,
        rate_limit_rpm=row.rate_limit_rpm,
        daily_budget_usd=row.daily_budget_usd,
        enabled=row.enabled,
        voice_enabled=row.voice_enabled,
        transcription_provider=row.transcription_provider,
        transcription_model=row.transcription_model,
        transcription_api_key_hint=key_hint(row.transcription_api_key_encrypted) if row.transcription_api_key_encrypted else None,
        tts_enabled=row.tts_enabled,
        tts_provider=row.tts_provider,
        tts_voice=row.tts_voice,
        tts_api_key_hint=key_hint(row.tts_api_key_encrypted) if row.tts_api_key_encrypted else None,
        memory_enabled=getattr(row, "memory_enabled", False),
        summarize_every_n_calls=getattr(row, "summarize_every_n_calls", 3),
        memory_raw_fallback_n=getattr(row, "memory_raw_fallback_n", 5),
        summarizer_provider=getattr(row, "summarizer_provider", None),
        summarizer_model=getattr(row, "summarizer_model", None),
        summarizer_api_key_hint=key_hint(row.summarizer_api_key_encrypted) if getattr(row, "summarizer_api_key_encrypted", None) else None,
        history_window=getattr(row, "history_window", 20),
        budget_tokens=getattr(row, "budget_tokens", None),
    )


async def _get_or_404(db: AsyncSession, orch_id: uuid.UUID) -> Orchestrator:
    row = await db.get(Orchestrator, orch_id)
    if row is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Orchestrator not found")
    return row


async def _invalidate(name: str) -> None:
    try:
        redis = await get_redis()
        await redis.delete(f"{_CACHE_PREFIX}{name}")
        await redis.publish(_CHANGE_CHANNEL, name)
    except Exception as exc:
        logger.warning("orchestrator: failed to invalidate cache", error=str(exc))


async def _test_llm(provider: str, model: str, api_key: str, base_url: Optional[str]) -> LLMTestResult:
    """Send a minimal request to the provider to validate the key."""
    import time
    start = time.monotonic()
    try:
        if provider == "anthropic":
            url = (base_url or "https://api.anthropic.com") + "/v1/messages"
            headers = {"x-api-key": api_key, "anthropic-version": "2023-06-01", "content-type": "application/json"}
            payload = {"model": model, "max_tokens": 5, "messages": [{"role": "user", "content": "hi"}]}
            async with httpx.AsyncClient(timeout=10) as c:
                r = await c.post(url, headers=headers, json=payload)
        elif provider == "openai":
            url = (base_url or "https://api.openai.com") + "/v1/chat/completions"
            headers = {"Authorization": f"Bearer {api_key}", "content-type": "application/json"}
            payload = {"model": model, "max_tokens": 5, "messages": [{"role": "user", "content": "hi"}]}
            async with httpx.AsyncClient(timeout=10) as c:
                r = await c.post(url, headers=headers, json=payload)
        elif provider == "groq":
            url = (base_url or "https://api.groq.com") + "/openai/v1/chat/completions"
            headers = {"Authorization": f"Bearer {api_key}", "content-type": "application/json"}
            payload = {"model": model, "max_tokens": 5, "messages": [{"role": "user", "content": "hi"}]}
            async with httpx.AsyncClient(timeout=10) as c:
                r = await c.post(url, headers=headers, json=payload)
        elif provider == "gemini":
            url = f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={api_key}"
            payload = {"contents": [{"parts": [{"text": "hi"}]}], "generationConfig": {"maxOutputTokens": 5}}
            async with httpx.AsyncClient(timeout=10) as c:
                r = await c.post(url, json=payload)
        else:
            return LLMTestResult(ok=False, error=f"Unknown provider: {provider}")

        ms = int((time.monotonic() - start) * 1000)
        if r.status_code in (200, 201):
            return LLMTestResult(ok=True, latency_ms=ms)
        body = r.json()
        msg = body.get("error", {}).get("message") or body.get("error") or str(r.status_code)
        return LLMTestResult(ok=False, error=str(msg), latency_ms=ms)
    except Exception as exc:
        ms = int((time.monotonic() - start) * 1000)
        return LLMTestResult(ok=False, error=str(exc), latency_ms=ms)


# ------------------------------------------------------------------ #
# Routes                                                               #
# ------------------------------------------------------------------ #

@router.get("", response_model=List[OrchestratorOut])
async def list_orchestrators(enabled_only: bool = False, db: AsyncSession = Depends(get_db)):
    q = select(Orchestrator).order_by(Orchestrator.name)
    if enabled_only:
        q = q.where(Orchestrator.enabled == True)
    result = await db.execute(q)
    return [_row_to_out(r) for r in result.scalars()]


@router.post("", response_model=OrchestratorOut, status_code=status.HTTP_201_CREATED)
async def create_orchestrator(body: OrchestratorCreate, db: AsyncSession = Depends(get_db)):
    existing = await db.execute(select(Orchestrator).where(Orchestrator.name == body.name))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f"Orchestrator '{body.name}' already exists")

    row = Orchestrator(
        name=body.name,
        display_name=body.display_name,
        system_prompt=body.system_prompt,
        allowed_agent_ids=[str(i) for i in body.allowed_agent_ids],
        llm_provider=body.llm_provider or None,
        llm_model=body.llm_model or None,
        llm_api_key_encrypted=encrypt_value(body.llm_api_key) if body.llm_api_key else None,
        llm_base_url=body.llm_base_url or None,
        max_iterations=body.max_iterations,
        max_parallel_tools=body.max_parallel_tools,
        rate_limit_rpm=body.rate_limit_rpm,
        daily_budget_usd=body.daily_budget_usd,
        enabled=body.enabled,
        voice_enabled=body.voice_enabled,
        transcription_provider=body.transcription_provider or None,
        transcription_model=body.transcription_model or None,
        transcription_api_key_encrypted=encrypt_value(body.transcription_api_key) if body.transcription_api_key else None,
        tts_enabled=body.tts_enabled,
        tts_provider=body.tts_provider or None,
        tts_voice=body.tts_voice or None,
        tts_api_key_encrypted=encrypt_value(body.tts_api_key) if body.tts_api_key else None,
        memory_enabled=body.memory_enabled,
        summarize_every_n_calls=body.summarize_every_n_calls,
        memory_raw_fallback_n=body.memory_raw_fallback_n,
        summarizer_provider=body.summarizer_provider or None,
        summarizer_model=body.summarizer_model or None,
        summarizer_api_key_encrypted=encrypt_value(body.summarizer_api_key) if body.summarizer_api_key else None,
        history_window=body.history_window,
        budget_tokens=body.budget_tokens,
    )
    db.add(row)
    await db.commit()
    await db.refresh(row)
    await _invalidate(body.name)
    logger.info("orchestrator created", name=body.name)
    return _row_to_out(row)


@router.get("/{orch_id}", response_model=OrchestratorOut)
async def get_orchestrator(orch_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    return _row_to_out(await _get_or_404(db, orch_id))


@router.patch("/{orch_id}", response_model=OrchestratorOut)
async def update_orchestrator(orch_id: uuid.UUID, body: OrchestratorUpdate, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, orch_id)

    if body.display_name is not None:
        row.display_name = body.display_name
    if body.system_prompt is not None:
        row.system_prompt = body.system_prompt
    if body.allowed_agent_ids is not None:
        row.allowed_agent_ids = [str(i) for i in body.allowed_agent_ids]
    if body.llm_provider is not None:
        row.llm_provider = body.llm_provider or None
    if body.llm_model is not None:
        row.llm_model = body.llm_model or None
    if body.llm_api_key:                        # non-blank = update; blank = preserve
        row.llm_api_key_encrypted = encrypt_value(body.llm_api_key)
    if body.llm_base_url is not None:
        row.llm_base_url = body.llm_base_url or None
    if body.max_iterations is not None:
        row.max_iterations = body.max_iterations
    if body.max_parallel_tools is not None:
        row.max_parallel_tools = body.max_parallel_tools
    if body.rate_limit_rpm is not None:
        row.rate_limit_rpm = body.rate_limit_rpm
    if body.daily_budget_usd is not None:
        row.daily_budget_usd = body.daily_budget_usd
    if body.enabled is not None:
        row.enabled = body.enabled
    if body.voice_enabled is not None:
        row.voice_enabled = body.voice_enabled
    if body.transcription_provider is not None:
        row.transcription_provider = body.transcription_provider or None
    if body.transcription_model is not None:
        row.transcription_model = body.transcription_model or None
    if body.transcription_api_key:
        row.transcription_api_key_encrypted = encrypt_value(body.transcription_api_key)
    if body.tts_enabled is not None:
        row.tts_enabled = body.tts_enabled
    if body.tts_provider is not None:
        row.tts_provider = body.tts_provider or None
    if body.tts_voice is not None:
        row.tts_voice = body.tts_voice or None
    if body.tts_api_key:
        row.tts_api_key_encrypted = encrypt_value(body.tts_api_key)
    if body.memory_enabled is not None:
        row.memory_enabled = body.memory_enabled
    if body.summarize_every_n_calls is not None:
        row.summarize_every_n_calls = body.summarize_every_n_calls
    if body.memory_raw_fallback_n is not None:
        row.memory_raw_fallback_n = body.memory_raw_fallback_n
    if body.summarizer_provider is not None:
        row.summarizer_provider = body.summarizer_provider or None
    if body.summarizer_model is not None:
        row.summarizer_model = body.summarizer_model or None
    if body.summarizer_api_key:
        row.summarizer_api_key_encrypted = encrypt_value(body.summarizer_api_key)
    if body.history_window is not None:
        row.history_window = body.history_window
    if body.budget_tokens is not None:
        row.budget_tokens = body.budget_tokens

    name = row.name
    await db.commit()
    await db.refresh(row)
    await _invalidate(name)
    logger.info("orchestrator updated", orch_id=str(orch_id), name=name)
    return _row_to_out(row)


@router.delete("/{orch_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_orchestrator(orch_id: uuid.UUID, db: AsyncSession = Depends(get_db)):
    row = await _get_or_404(db, orch_id)
    name = row.name
    await db.delete(row)
    await db.commit()
    await _invalidate(name)
    logger.info("orchestrator deleted", orch_id=str(orch_id), name=name)


@router.post("/{orch_id}/test-llm", response_model=LLMTestResult)
async def test_llm(orch_id: uuid.UUID, body: LLMTestRequest, db: AsyncSession = Depends(get_db)):
    """Validate an LLM API key by sending a minimal request to the provider."""
    api_key = body.api_key
    if not api_key:
        row = await _get_or_404(db, orch_id)
        if not row.llm_api_key_encrypted:
            raise HTTPException(status_code=400, detail="No API key stored and none provided")
        api_key = decrypt_value(row.llm_api_key_encrypted)
    return await _test_llm(body.provider, body.model, api_key, body.base_url)


class VoiceTestRequest(BaseModel):
    provider: str
    model: str
    api_key: Optional[str] = None


class TTSTestRequest(BaseModel):
    provider: str
    voice: str
    api_key: Optional[str] = None


@router.post("/{orch_id}/test-voice", response_model=LLMTestResult)
async def test_voice(orch_id: uuid.UUID, body: VoiceTestRequest, db: AsyncSession = Depends(get_db)):
    """Validate a transcription API key by listing available models."""
    import time
    api_key = body.api_key
    if not api_key:
        row = await _get_or_404(db, orch_id)
        if not row.transcription_api_key_encrypted:
            raise HTTPException(status_code=400, detail="No API key stored and none provided")
        api_key = decrypt_value(row.transcription_api_key_encrypted)

    start = time.monotonic()
    try:
        if body.provider == "openai":
            import openai
            client = openai.AsyncOpenAI(api_key=api_key)
            models = await client.models.list()
            ms = int((time.monotonic() - start) * 1000)
            whisper_models = [m.id for m in models.data if "whisper" in m.id or "transcribe" in m.id]
            return LLMTestResult(ok=True, latency_ms=ms, error=f"Available: {', '.join(whisper_models) or 'none found'}")
        elif body.provider == "groq":
            from groq import AsyncGroq
            client = AsyncGroq(api_key=api_key)
            models = await client.models.list()
            ms = int((time.monotonic() - start) * 1000)
            audio_models = [m.id for m in models.data if "whisper" in m.id]
            return LLMTestResult(ok=True, latency_ms=ms, error=f"Available: {', '.join(audio_models) or 'none found'}")
        else:
            return LLMTestResult(ok=False, error=f"Unknown provider: {body.provider}")
    except Exception as exc:
        ms = int((time.monotonic() - start) * 1000)
        return LLMTestResult(ok=False, error=str(exc), latency_ms=ms)


@router.post("/{orch_id}/test-tts", response_model=LLMTestResult)
async def test_tts(orch_id: uuid.UUID, body: TTSTestRequest, db: AsyncSession = Depends(get_db)):
    """Validate a TTS API key with a minimal synthesis request."""
    import time
    api_key = body.api_key
    if not api_key:
        row = await _get_or_404(db, orch_id)
        if not row.tts_api_key_encrypted:
            raise HTTPException(status_code=400, detail="No TTS API key stored and none provided")
        api_key = decrypt_value(row.tts_api_key_encrypted)

    start = time.monotonic()
    try:
        if body.provider == "openai":
            import openai
            client = openai.AsyncOpenAI(api_key=api_key)
            resp = await client.audio.speech.create(
                model="tts-1", voice=body.voice, input="Hello", response_format="mp3"
            )
            await resp.aread()
            ms = int((time.monotonic() - start) * 1000)
            return LLMTestResult(ok=True, latency_ms=ms)
        else:
            return LLMTestResult(ok=False, error=f"Unknown TTS provider: {body.provider}")
    except Exception as exc:
        ms = int((time.monotonic() - start) * 1000)
        return LLMTestResult(ok=False, error=str(exc), latency_ms=ms)
