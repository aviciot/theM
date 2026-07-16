"""Shared STT and TTS helpers for both global orchestrators and app entry points."""
from typing import AsyncIterator
import io


async def transcribe(
    provider: str,
    model: str,
    api_key: str,
    audio_bytes: bytes,
    filename: str = "audio.webm",
    content_type: str = "audio/webm",
) -> str:
    """Convert audio bytes to text using the specified STT provider."""
    if provider == "openai":
        import openai as _openai
        client = _openai.AsyncOpenAI(api_key=api_key)
        audio_file = io.BytesIO(audio_bytes)
        audio_file.name = filename
        result = await client.audio.transcriptions.create(
            model=model or "whisper-1",
            file=audio_file,
        )
        return result.text
    elif provider == "groq":
        from groq import AsyncGroq
        client = AsyncGroq(api_key=api_key)
        audio_file = io.BytesIO(audio_bytes)
        audio_file.name = filename
        result = await client.audio.transcriptions.create(
            model=model or "whisper-large-v3",
            file=audio_file,
        )
        return result.text
    else:
        raise ValueError(f"Unsupported STT provider: {provider}")


async def stream_tts(
    provider: str,
    voice: str,
    api_key: str,
    text: str,
    model: str = "tts-1",
) -> AsyncIterator[bytes]:
    """Stream TTS audio bytes from the specified provider."""
    if provider == "openai":
        import openai as _openai
        client = _openai.AsyncOpenAI(api_key=api_key)
        async with client.audio.speech.with_streaming_response.create(
            model=model,
            voice=voice or "alloy",
            input=text,
            response_format="mp3",
        ) as response:
            async for chunk in response.iter_bytes(chunk_size=4096):
                yield chunk
    elif provider == "elevenlabs":
        # ElevenLabs streaming TTS
        import httpx
        url = f"https://api.elevenlabs.io/v1/text-to-speech/{voice}/stream"
        headers = {"xi-api-key": api_key, "Content-Type": "application/json"}
        payload = {"text": text, "model_id": "eleven_monolingual_v1"}
        async with httpx.AsyncClient() as client:
            async with client.stream("POST", url, json=payload, headers=headers) as response:
                async for chunk in response.aiter_bytes(chunk_size=4096):
                    yield chunk
    else:
        raise ValueError(f"Unsupported TTS provider: {provider}")
