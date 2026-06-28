"""
Anthropic Claude provider.

Supports:
- Prompt caching (cache_control on system message)
- Native tool use (ToolUseBlock / ToolResultBlock)
- Async streaming via AsyncAnthropic
"""
from typing import Any, AsyncGenerator

import anthropic
from anthropic.types import TextBlock, ToolUseBlock

from app.utils.logger import logger
from .base import LLMIterationResult, LLMProvider, LLMStreamEvent, NeutralTool, ToolCall, TokenUsage


def _convert_tools(tools: list[NeutralTool]) -> list[dict]:
    """Convert neutral tool format → Anthropic format."""
    return [
        {
            "name": t["name"],
            "description": t["description"],
            "input_schema": t["schema"],
        }
        for t in tools
    ]


class AnthropicProvider(LLMProvider):
    name = "anthropic"
    supports_caching = True

    def __init__(self, api_key: str, model: str) -> None:
        self.model = model
        self._client = anthropic.Anthropic(api_key=api_key)
        self._async_client: anthropic.AsyncAnthropic | None = None
        try:
            self._async_client = anthropic.AsyncAnthropic(api_key=api_key)
        except Exception:
            pass
        logger.info("AnthropicProvider initialised", model=model)

    # ------------------------------------------------------------------ #
    # Message history helpers                                              #
    # ------------------------------------------------------------------ #

    def init_messages(self, user_message: str) -> list:
        return [{"role": "user", "content": user_message}]

    def append_assistant_response(self, messages: list, raw_response: Any) -> None:
        # raw_response is the full Anthropic Message object; .content is the list of blocks
        messages.append({"role": "assistant", "content": raw_response.content})

    def append_tool_results(
        self,
        messages: list,
        tool_calls: list[ToolCall],
        results: list[str],
    ) -> None:
        messages.append({
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": tc.id,
                    "content": result,
                }
                for tc, result in zip(tool_calls, results)
            ],
        })

    # ------------------------------------------------------------------ #
    # System prompt helper                                                 #
    # ------------------------------------------------------------------ #

    def _system_config(self, system: str) -> list:
        """Wrap system prompt with cache_control for prompt caching."""
        if not system:
            return []
        return [{"type": "text", "text": system, "cache_control": {"type": "ephemeral"}}]

    # ------------------------------------------------------------------ #
    # Non-streaming call                                                   #
    # ------------------------------------------------------------------ #

    async def call(
        self,
        system: str,
        messages: list,
        tools: list[NeutralTool],
        max_tokens: int,
    ) -> LLMIterationResult:
        response = self._client.messages.create(
            model=self.model,
            max_tokens=max_tokens,
            system=self._system_config(system),
            tools=_convert_tools(tools),
            messages=messages,
        )

        usage = TokenUsage(
            input_tokens=getattr(response.usage, "input_tokens", 0),
            output_tokens=getattr(response.usage, "output_tokens", 0),
            cached_tokens=getattr(response.usage, "cache_read_input_tokens", 0),
        )

        tool_use_blocks = [b for b in response.content if isinstance(b, ToolUseBlock)]

        if not tool_use_blocks:
            text = "\n".join(b.text for b in response.content if isinstance(b, TextBlock))
            return LLMIterationResult(text=text, raw_response=response, usage=usage)

        tool_calls = [
            ToolCall(id=b.id, name=b.name, input=b.input)
            for b in tool_use_blocks
        ]
        return LLMIterationResult(tool_calls=tool_calls, raw_response=response, usage=usage)

    # ------------------------------------------------------------------ #
    # Streaming call                                                       #
    # ------------------------------------------------------------------ #

    async def stream_call(
        self,
        system: str,
        messages: list,
        tools: list[NeutralTool],
        max_tokens: int,
    ) -> AsyncGenerator[LLMStreamEvent, None]:
        converted_tools = _convert_tools(tools)
        system_config = self._system_config(system)
        final_message = None

        try:
            if self._async_client is not None:
                async with self._async_client.messages.stream(
                    model=self.model,
                    max_tokens=max_tokens,
                    system=system_config,
                    tools=converted_tools,
                    messages=messages,
                ) as stream:
                    async for text in stream.text_stream:
                        if text:
                            yield LLMStreamEvent(type="token", text=text)
                    final_message = await stream.get_final_message()
            else:
                with self._client.messages.stream(
                    model=self.model,
                    max_tokens=max_tokens,
                    system=system_config,
                    tools=converted_tools,
                    messages=messages,
                ) as stream:
                    for text in stream.text_stream:
                        if text:
                            yield LLMStreamEvent(type="token", text=text)
                    final_message = stream.get_final_message()

        except Exception as e:
            yield LLMStreamEvent(type="error", error=str(e))
            return

        usage = TokenUsage(
            input_tokens=getattr(final_message.usage, "input_tokens", 0),
            output_tokens=getattr(final_message.usage, "output_tokens", 0),
            cached_tokens=getattr(final_message.usage, "cache_read_input_tokens", 0),
        )

        tool_use_blocks = [b for b in final_message.content if isinstance(b, ToolUseBlock)]

        if not tool_use_blocks:
            text = "\n".join(b.text for b in final_message.content if isinstance(b, TextBlock))
            yield LLMStreamEvent(
                type="done",
                result={"answer": text, "raw_response": final_message, "usage": usage},
                usage=usage,
            )
            return

        tool_calls = [ToolCall(id=b.id, name=b.name, input=b.input) for b in tool_use_blocks]
        for tc in tool_calls:
            mcp, tool = tc.name.split("__", 1)
            yield LLMStreamEvent(type="tool_call", mcp=mcp, tool=tool, parameters=tc.input)

        # Yield special event so the caller can update message history and loop
        yield LLMStreamEvent(
            type="tool_calls_ready",
            result={"tool_calls": tool_calls, "raw_response": final_message, "usage": usage},
        )
