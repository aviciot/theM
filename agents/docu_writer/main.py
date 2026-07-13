"""
docu-writer — A2A v1.0 documentation generation agent.

Receives structured content (from a code analysis agent) and a desired output
format, then uses Claude to render polished documentation as a file artifact.

Input message format (plain text, the orchestrator composes this):
  FORMAT: html|markdown|slides
  TITLE: <optional title>
  CONTENT:
  <the explanation / analysis text to render>

Output: A2A artifact with:
  - parts[0].text  = full rendered content (HTML string, Markdown, etc.)
  - parts[0].filename   = e.g. "documentation.html"
  - parts[0].media_type = e.g. "text/html"

Supported formats:
  html      → self-contained interactive HTML with Mermaid diagrams
  markdown  → clean Markdown with Mermaid fenced blocks
  slides    → Marp-compatible Markdown slide deck
"""

import os
import uvicorn
from fastapi import FastAPI

import anthropic
from google.protobuf import json_format

from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events.event_queue import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import add_a2a_routes_to_fastapi, create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCard,
    Artifact,
    Task,
    TaskArtifactUpdateEvent,
    TaskState,
    TaskStatusUpdateEvent,
)

ANTHROPIC_API_KEY = os.getenv("ANTHROPIC_API_KEY", "")
PORT = int(os.getenv("PORT", "9300"))

_FORMAT_META = {
    "html": {
        "filename": "documentation.html",
        "media_type": "text/html",
        "instruction": (
            "Render the content as a self-contained, interactive HTML file. "
            "Requirements:\n"
            "- Single HTML file, no external dependencies (inline all CSS/JS)\n"
            "- Include Mermaid.js (from CDN is fine) to render any diagrams\n"
            "- Clean, professional styling with a sidebar table of contents\n"
            "- Syntax-highlighted code blocks using highlight.js (CDN)\n"
            "- Collapsible sections for long content\n"
            "- Dark/light mode toggle\n"
            "- Output ONLY the raw HTML — no markdown fences, no explanation"
        ),
    },
    "markdown": {
        "filename": "documentation.md",
        "media_type": "text/markdown",
        "instruction": (
            "Render the content as clean, well-structured Markdown. "
            "Requirements:\n"
            "- Use ATX headings (# ## ###)\n"
            "- Include Mermaid diagrams in ```mermaid fenced blocks\n"
            "- Use tables for comparisons\n"
            "- Output ONLY the raw Markdown — no explanation around it"
        ),
    },
    "slides": {
        "filename": "slides.md",
        "media_type": "text/markdown",
        "instruction": (
            "Render the content as a Marp slide deck (Markdown). "
            "Requirements:\n"
            "- Start with YAML front matter: --- marp: true theme: default ---\n"
            "- Separate slides with ---\n"
            "- First slide: title + subtitle\n"
            "- One concept per slide, max 5 bullet points\n"
            "- Include Mermaid diagrams where helpful\n"
            "- Last slide: summary / key takeaways\n"
            "- Output ONLY the raw Marp Markdown — no explanation"
        ),
    },
}

_SYSTEM_PROMPT = (
    "You are a technical documentation specialist. You receive structured technical "
    "content (code analysis, architecture explanations, business logic descriptions) "
    "and render it into polished, professional documentation in the requested format. "
    "You produce complete, ready-to-use output — never partial, never with placeholders."
)


def _extract_input(context: "RequestContext") -> tuple[str, str, str]:
    """
    Extract (fmt, title, content) from the A2A message parts.

    Prefers a typed data part: {"format": "html", "title": "...", "content": "..."}.
    Falls back to the first text part as raw content with defaults.
    """
    fmt = "html"
    title = "Documentation"
    content = ""

    if not context.message:
        return fmt, title, content

    for part in context.message.parts:
        if part.HasField("data"):
            data = json_format.MessageToDict(part.data.struct_value)
            fmt = data.get("format", fmt).lower()
            title = data.get("title", title)
            content = data.get("content", content)
            break
        elif part.HasField("text") and not content:
            content = part.text

    if fmt not in _FORMAT_META:
        fmt = "html"

    return fmt, title, content


def _build_prompt(fmt: str, title: str, content: str) -> str:
    meta = _FORMAT_META[fmt]
    return (
        f"Title: {title}\n\n"
        f"Output format instructions:\n{meta['instruction']}\n\n"
        f"Content to render:\n{content}"
    )


class DocuWriterExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        # Enqueue initial task state
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        working = TaskStatusUpdateEvent()
        working.task_id = context.task_id
        working.context_id = context.context_id
        working.status.state = TaskState.TASK_STATE_WORKING
        await event_queue.enqueue_event(working)

        try:
            fmt, title, content = _extract_input(context)
            meta = _FORMAT_META[fmt]

            if not ANTHROPIC_API_KEY:
                raise RuntimeError("ANTHROPIC_API_KEY is not set")

            client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)
            response = client.messages.create(
                model="claude-sonnet-4-6",
                max_tokens=8192,
                system=_SYSTEM_PROMPT,
                messages=[{"role": "user", "content": _build_prompt(fmt, title, content)}],
            )
            rendered = response.content[0].text

            artifact = Artifact()
            artifact.artifact_id = "docu-output"
            artifact.name = meta["filename"]
            artifact.description = f"{fmt} documentation: {title}"
            part = artifact.parts.add()
            part.text = rendered
            part.filename = meta["filename"]
            part.media_type = meta["media_type"]

            art_event = TaskArtifactUpdateEvent()
            art_event.task_id = context.task_id
            art_event.context_id = context.context_id
            art_event.artifact.CopyFrom(artifact)
            art_event.last_chunk = True
            await event_queue.enqueue_event(art_event)

            done = TaskStatusUpdateEvent()
            done.task_id = context.task_id
            done.context_id = context.context_id
            done.status.state = TaskState.TASK_STATE_COMPLETED
            await event_queue.enqueue_event(done)

        except Exception as exc:
            err = TaskStatusUpdateEvent()
            err.task_id = context.task_id
            err.context_id = context.context_id
            err.status.state = TaskState.TASK_STATE_FAILED
            err.status.message.role = 2  # ROLE_AGENT
            err.status.message.message_id = context.task_id + "-err"
            part = err.status.message.parts.add()
            part.text = f"DocuWriter error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "docu-writer"
    card.description = (
        "Renders technical content into polished documentation files. "
        "Accepts structured analysis text and a format (html, markdown, slides) "
        "and returns a ready-to-use file artifact with Mermaid diagrams and syntax highlighting."
    )
    card.version = "1.0.0"
    card.icon_url = "description"
    iface = card.supported_interfaces.add()
    iface.url = f"http://docu-writer:{PORT}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False

    for fmt, meta in _FORMAT_META.items():
        skill = card.skills.add()
        skill.id = f"render_{fmt}"
        skill.name = f"Render {fmt.capitalize()}"
        skill.description = (
            f"Renders technical analysis into a {meta['filename']} file. "
            f"Input: JSON with fields: format (html|markdown|slides), title (string), content (markdown string). "
            f"Output: complete {fmt} file artifact."
        )
        skill.input_modes.append("application/json")
        skill.input_modes.append("text/plain")
        skill.output_modes.append(meta["media_type"])

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="docu-writer")
    card = make_agent_card()
    task_store = InMemoryTaskStore()
    executor = DocuWriterExecutor()
    handler = DefaultRequestHandler(
        agent_executor=executor,
        task_store=task_store,
        agent_card=card,
    )
    add_a2a_routes_to_fastapi(
        app,
        agent_card_routes=create_agent_card_routes(card),
        jsonrpc_routes=create_jsonrpc_routes(handler, rpc_url="/"),
    )
    return app


app = create_app()

if __name__ == "__main__":
    uvicorn.run("main:app", host="0.0.0.0", port=PORT, log_level="info")
