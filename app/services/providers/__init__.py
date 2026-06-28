"""
Provider factory.

Usage:
    from app.services.providers import create_provider

    provider = create_provider("anthropic", api_key="sk-...", model="claude-sonnet-4-6")
"""
from .base import (
    LLMIterationResult,
    LLMProvider,
    LLMStreamEvent,
    NeutralTool,
    ToolCall,
    TokenUsage,
)

SUPPORTED_PROVIDERS = ("anthropic", "openai", "groq", "gemini")


def create_provider(name: str, api_key: str, model: str) -> LLMProvider:
    """Instantiate the correct LLMProvider for the given provider name."""
    if name == "anthropic":
        from .anthropic import AnthropicProvider
        return AnthropicProvider(api_key=api_key, model=model)

    if name in ("openai", "groq", "gemini"):
        from .openai_compat import OpenAICompatProvider
        return OpenAICompatProvider(provider_name=name, api_key=api_key, model=model)

    raise ValueError(
        f"Unknown provider '{name}'. Supported: {', '.join(SUPPORTED_PROVIDERS)}"
    )


__all__ = [
    "create_provider",
    "SUPPORTED_PROVIDERS",
    "LLMProvider",
    "LLMIterationResult",
    "LLMStreamEvent",
    "NeutralTool",
    "ToolCall",
    "TokenUsage",
]
