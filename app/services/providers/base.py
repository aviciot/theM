"""
Abstract LLM Provider interface.

Each provider implements:
  - call()           — single non-streaming LLM call
  - stream_call()    — streaming LLM call (async generator)
  - init_messages()  — create initial message history for a conversation
  - append_assistant_response() — add assistant turn to history
  - append_tool_results()       — add tool results to history

The LLMService owns the agentic loop and MCP tool execution; providers
only handle the LLM API interaction and message-format differences.
"""
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, AsyncGenerator, Optional


@dataclass
class ToolCall:
    """A single tool call requested by the LLM."""
    id: str
    name: str   # "mcp_name__tool_name"
    input: dict


@dataclass
class TokenUsage:
    input_tokens: int = 0
    output_tokens: int = 0
    cached_tokens: int = 0


@dataclass
class LLMIterationResult:
    """Result of a single LLM API call (one iteration of the agentic loop)."""
    text: Optional[str] = None                  # Set when LLM is done
    tool_calls: Optional[list[ToolCall]] = None  # Set when LLM wants tools
    raw_response: Any = None                     # Provider-specific; passed back to append_assistant_response
    usage: TokenUsage = field(default_factory=TokenUsage)


@dataclass
class LLMStreamEvent:
    """A single event from a streaming LLM call."""
    type: str  # "token" | "tool_call" | "done" | "error"
    text: Optional[str] = None
    mcp: Optional[str] = None
    tool: Optional[str] = None
    parameters: Optional[dict] = None
    result: Optional[dict] = None
    error: Optional[str] = None
    usage: Optional[TokenUsage] = None


# Neutral tool format passed from LLMService into providers.
# Matches MCP tool schema: {name, description, schema (JSON Schema object)}.
NeutralTool = dict  # {"name": str, "description": str, "schema": dict}


class LLMProvider(ABC):
    """Abstract base for all LLM providers."""

    #: Unique slug matching llm_providers.name ("anthropic", "openai", …)
    name: str = "unknown"

    #: Whether this provider supports prompt caching (Anthropic only for now)
    supports_caching: bool = False

    @abstractmethod
    async def call(
        self,
        system: str,
        messages: list,
        tools: list[NeutralTool],
        max_tokens: int,
    ) -> LLMIterationResult:
        """
        Single LLM API call (one agentic-loop iteration).

        Returns either a text response (done) or a list of tool calls.
        The raw_response field must be populated so the caller can pass it to
        append_assistant_response() before the next iteration.
        """
        ...

    @abstractmethod
    def init_messages(self, user_message: str) -> list:
        """Create the initial message list for a new conversation."""
        ...

    @abstractmethod
    def append_assistant_response(self, messages: list, raw_response: Any) -> None:
        """Append the assistant's raw response to the message history."""
        ...

    @abstractmethod
    def append_tool_results(
        self,
        messages: list,
        tool_calls: list[ToolCall],
        results: list[str],
    ) -> None:
        """
        Append tool execution results to the message history.

        tool_calls[i] corresponds to results[i].
        """
        ...

    @abstractmethod
    async def stream_call(
        self,
        system: str,
        messages: list,
        tools: list[NeutralTool],
        max_tokens: int,
    ) -> AsyncGenerator[LLMStreamEvent, None]:
        """
        Streaming LLM call.

        Yields LLMStreamEvent objects. The caller is responsible for executing
        any tool_call events and calling append_assistant_response /
        append_tool_results before the next iteration.

        The generator must NOT do multi-iteration looping — that belongs in
        LLMService.ask_stream().
        """
        ...
        yield  # make type checker happy
