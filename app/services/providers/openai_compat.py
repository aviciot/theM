"""
OpenAI-compatible provider.

Works for:
  - OpenAI        (api.openai.com)
  - Groq          (api.groq.com/openai/v1)
  - Google Gemini (generativelanguage.googleapis.com/v1beta/openai)

All three expose the same OpenAI Chat Completions API shape, so a single
implementation covers all three — just swap base_url and api_key.
"""
import json
from typing import Any, AsyncGenerator

from app.utils.logger import logger
from .base import LLMIterationResult, LLMProvider, LLMStreamEvent, NeutralTool, ToolCall, TokenUsage

# Lazy import so missing openai package only errors when a provider is actually used
def _get_openai():
    try:
        import openai
        return openai
    except ImportError as e:
        raise ImportError(
            "openai package is required for OpenAI/Groq/Gemini providers. "
            "Install it with: pip install openai"
        ) from e


# Base URLs per provider slug
_BASE_URLS: dict[str, str] = {
    "openai": "https://api.openai.com/v1",
    "groq":   "https://api.groq.com/openai/v1",
    "gemini": "https://generativelanguage.googleapis.com/v1beta/openai",
}


def _convert_tools(tools: list[NeutralTool]) -> list[dict]:
    """Convert neutral tool format → OpenAI function-calling format."""
    return [
        {
            "type": "function",
            "function": {
                "name": t["name"],
                "description": t["description"],
                "parameters": t["schema"],
            },
        }
        for t in tools
    ]


class OpenAICompatProvider(LLMProvider):
    """Provider for any OpenAI-compatible API endpoint."""

    supports_caching = False

    def __init__(self, provider_name: str, api_key: str, model: str) -> None:
        self.name = provider_name
        self.model = model
        self._base_url = _BASE_URLS.get(provider_name, "https://api.openai.com/v1")
        self._api_key = api_key

        openai = _get_openai()
        self._client = openai.OpenAI(api_key=api_key, base_url=self._base_url)
        self._async_client = openai.AsyncOpenAI(api_key=api_key, base_url=self._base_url)
        logger.info("OpenAICompatProvider initialised", provider=provider_name, model=model)

    # ------------------------------------------------------------------ #
    # Message history helpers                                              #
    # ------------------------------------------------------------------ #

    def init_messages(self, user_message: str) -> list:
        # System goes in as first message at call() time; only user message here
        return [{"role": "user", "content": user_message}]

    def append_assistant_response(self, messages: list, raw_response: Any) -> None:
        # raw_response is the ChatCompletion object; grab the first choice message
        msg = raw_response.choices[0].message
        entry: dict = {"role": "assistant", "content": msg.content}
        if msg.tool_calls:
            entry["tool_calls"] = [
                {
                    "id": tc.id,
                    "type": "function",
                    "function": {
                        "name": tc.function.name,
                        "arguments": tc.function.arguments,
                    },
                }
                for tc in msg.tool_calls
            ]
        messages.append(entry)

    def append_tool_results(
        self,
        messages: list,
        tool_calls: list[ToolCall],
        results: list[str],
    ) -> None:
        for tc, result in zip(tool_calls, results):
            messages.append({
                "role": "tool",
                "tool_call_id": tc.id,
                "content": result,
            })

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
        all_messages = [{"role": "system", "content": system}] + messages

        response = await self._async_client.chat.completions.create(
            model=self.model,
            max_tokens=max_tokens,
            messages=all_messages,
            tools=_convert_tools(tools) if tools else None,
        )

        usage = TokenUsage(
            input_tokens=getattr(response.usage, "prompt_tokens", 0),
            output_tokens=getattr(response.usage, "completion_tokens", 0),
        )

        msg = response.choices[0].message

        if not msg.tool_calls:
            return LLMIterationResult(
                text=msg.content or "",
                raw_response=response,
                usage=usage,
            )

        tool_calls = [
            ToolCall(
                id=tc.id,
                name=tc.function.name,
                input=json.loads(tc.function.arguments),
            )
            for tc in msg.tool_calls
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
        all_messages = [{"role": "system", "content": system}] + messages
        converted_tools = _convert_tools(tools) if tools else None

        # Accumulate tool call fragments across stream chunks
        accumulated_tool_calls: dict[int, dict] = {}
        accumulated_text = ""
        final_usage = TokenUsage()

        try:
            async with await self._async_client.chat.completions.create(
                model=self.model,
                max_tokens=max_tokens,
                messages=all_messages,
                tools=converted_tools,
                stream=True,
                stream_options={"include_usage": True},
            ) as stream:
                async for chunk in stream:
                    if chunk.usage:
                        final_usage = TokenUsage(
                            input_tokens=chunk.usage.prompt_tokens or 0,
                            output_tokens=chunk.usage.completion_tokens or 0,
                        )

                    if not chunk.choices:
                        continue
                    delta = chunk.choices[0].delta

                    # Text token
                    if delta.content:
                        accumulated_text += delta.content
                        yield LLMStreamEvent(type="token", text=delta.content)

                    # Tool call fragments
                    if delta.tool_calls:
                        for tc_chunk in delta.tool_calls:
                            idx = tc_chunk.index
                            if idx not in accumulated_tool_calls:
                                accumulated_tool_calls[idx] = {
                                    "id": "",
                                    "name": "",
                                    "arguments": "",
                                }
                            if tc_chunk.id:
                                accumulated_tool_calls[idx]["id"] += tc_chunk.id
                            if tc_chunk.function:
                                if tc_chunk.function.name:
                                    accumulated_tool_calls[idx]["name"] += tc_chunk.function.name
                                if tc_chunk.function.arguments:
                                    accumulated_tool_calls[idx]["arguments"] += tc_chunk.function.arguments

        except Exception as e:
            yield LLMStreamEvent(type="error", error=str(e))
            return

        if not accumulated_tool_calls:
            yield LLMStreamEvent(
                type="done",
                result={"answer": accumulated_text, "usage": final_usage},
                usage=final_usage,
            )
            return

        # Build ToolCall objects and emit tool_call events
        tool_calls = []
        for _idx, tc_data in sorted(accumulated_tool_calls.items()):
            try:
                input_data = json.loads(tc_data["arguments"]) if tc_data["arguments"] else {}
            except json.JSONDecodeError:
                input_data = {}
            tc = ToolCall(id=tc_data["id"], name=tc_data["name"], input=input_data)
            tool_calls.append(tc)
            mcp, tool = tc.name.split("__", 1)
            yield LLMStreamEvent(type="tool_call", mcp=mcp, tool=tool, parameters=tc.input)

        # Build a synthetic raw_response so the caller can call append_assistant_response
        yield LLMStreamEvent(
            type="tool_calls_ready",
            result={
                "tool_calls": tool_calls,
                "raw_response": _SyntheticResponse(tool_calls),
                "usage": final_usage,
            },
        )


class _SyntheticResponse:
    """
    Minimal stand-in for an OpenAI ChatCompletion when building message
    history after streaming tool calls. We only need what
    append_assistant_response() reads.
    """

    def __init__(self, tool_calls: list[ToolCall]) -> None:
        import json as _json

        class _Fn:
            def __init__(self, tc: ToolCall) -> None:
                self.name = tc.name
                self.arguments = _json.dumps(tc.input)

        class _TC:
            def __init__(self, tc: ToolCall) -> None:
                self.id = tc.id
                self.type = "function"
                self.function = _Fn(tc)

        class _Msg:
            def __init__(self, tcs: list[ToolCall]) -> None:
                self.content = None
                self.tool_calls = [_TC(tc) for tc in tcs]

        class _Choice:
            def __init__(self, tcs: list[ToolCall]) -> None:
                self.message = _Msg(tcs)

        self.choices = [_Choice(tool_calls)]
