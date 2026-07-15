"""
LLM probe — send a minimal request to a provider to validate connectivity and API key.
Extracted from admin_orchestrators so other services (e.g. system_agents) can reuse it.
"""

import time
from typing import Optional

import httpx
from pydantic import BaseModel


class LLMProbeResult(BaseModel):
    ok: bool
    latency_ms: Optional[int] = None
    error: Optional[str] = None
    response_text: Optional[str] = None


async def probe_llm(
    provider: str,
    model: str,
    api_key: str,
    base_url: Optional[str] = None,
    prompt: str = "Say hello.",
    max_tokens: int = 5,
) -> LLMProbeResult:
    """Send a minimal request to the provider to validate the key. Returns LLMProbeResult."""
    start = time.monotonic()
    try:
        if provider == "anthropic":
            url = (base_url or "https://api.anthropic.com") + "/v1/messages"
            headers = {
                "x-api-key": api_key,
                "anthropic-version": "2023-06-01",
                "content-type": "application/json",
            }
            payload = {
                "model": model,
                "max_tokens": max_tokens,
                "messages": [{"role": "user", "content": prompt}],
            }
            async with httpx.AsyncClient(timeout=15) as c:
                r = await c.post(url, headers=headers, json=payload)

        elif provider == "openai":
            url = (base_url or "https://api.openai.com") + "/v1/chat/completions"
            headers = {"Authorization": f"Bearer {api_key}", "content-type": "application/json"}
            payload = {
                "model": model,
                "max_tokens": max_tokens,
                "messages": [{"role": "user", "content": prompt}],
            }
            async with httpx.AsyncClient(timeout=15) as c:
                r = await c.post(url, headers=headers, json=payload)

        elif provider == "groq":
            url = (base_url or "https://api.groq.com") + "/openai/v1/chat/completions"
            headers = {"Authorization": f"Bearer {api_key}", "content-type": "application/json"}
            payload = {
                "model": model,
                "max_tokens": max_tokens,
                "messages": [{"role": "user", "content": prompt}],
            }
            async with httpx.AsyncClient(timeout=15) as c:
                r = await c.post(url, headers=headers, json=payload)

        elif provider == "gemini":
            url = f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={api_key}"
            payload = {
                "contents": [{"parts": [{"text": prompt}]}],
                "generationConfig": {"maxOutputTokens": max_tokens},
            }
            async with httpx.AsyncClient(timeout=15) as c:
                r = await c.post(url, json=payload)

        else:
            return LLMProbeResult(ok=False, error=f"Unknown provider: {provider}")

        ms = int((time.monotonic() - start) * 1000)
        if r.status_code in (200, 201):
            response_text = _extract_text(provider, r)
            return LLMProbeResult(ok=True, latency_ms=ms, response_text=response_text)

        body = r.json()
        msg = body.get("error", {}).get("message") or body.get("error") or str(r.status_code)
        return LLMProbeResult(ok=False, error=str(msg), latency_ms=ms)

    except Exception as exc:
        ms = int((time.monotonic() - start) * 1000)
        return LLMProbeResult(ok=False, error=str(exc), latency_ms=ms)


def _extract_text(provider: str, response: httpx.Response) -> Optional[str]:
    """Pull the assistant reply text out of a successful provider response."""
    try:
        body = response.json()
        if provider == "anthropic":
            return body.get("content", [{}])[0].get("text")
        elif provider in ("openai", "groq"):
            return body.get("choices", [{}])[0].get("message", {}).get("content")
        elif provider == "gemini":
            return (
                body.get("candidates", [{}])[0]
                .get("content", {})
                .get("parts", [{}])[0]
                .get("text")
            )
    except Exception:
        pass
    return None
