"""
docu-writer — A2A v1.0 documentation generation agent.

Receives structured content and a desired output format, then uses Claude
to render polished documentation as a file artifact.

Input message format (typed data part preferred):
  {"format": "html|markdown|slides|pdf", "title": "...", "content": "..."}

Output: A2A artifact with the rendered file.

Supported formats:
  html      → single-page self-contained HTML (print-ready)
  markdown  → clean Markdown with Mermaid fenced blocks
  slides    → Marp-compatible Markdown slide deck
  pdf       → PDF generated from Claude-rendered markdown via fpdf2
"""

import asyncio
import io
import os
import re
import unicodedata

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
MODEL = "claude-haiku-4-5-20251001"
MAX_TOKENS = 4096

_FORMAT_META = {
    "html": {
        "filename": "documentation.html",
        "media_type": "text/html",
        "instruction": (
            "Render the content as a single self-contained HTML page. "
            "Requirements:\n"
            "- Single HTML file, all CSS inline in <style> tags, no external dependencies\n"
            "- Include Mermaid.js via CDN script tag for diagrams\n"
            "- Clean professional styling: readable font, max-width 860px, centered\n"
            "- Syntax-highlighted code blocks (use <pre><code> with inline CSS)\n"
            "- Print-ready: @media print styles that remove nav/buttons\n"
            "- Dark/light mode toggle button\n"
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
    "pdf": {
        "filename": "documentation.pdf",
        "media_type": "application/pdf",
        "instruction": (
            "Render the content as clean, well-structured Markdown. "
            "Requirements:\n"
            "- Use ATX headings (# ## ###)\n"
            "- Use bullet points and numbered lists where appropriate\n"
            "- Use tables for comparisons (plain markdown table syntax)\n"
            "- Keep code blocks using triple backticks\n"
            "- Output ONLY the raw Markdown — no explanation around it"
        ),
    },
}

_SYSTEM_PROMPT = (
    "You are a technical documentation specialist. You receive structured technical "
    "content and render it into polished, professional documentation in the requested format. "
    "You produce complete, ready-to-use output — never partial, never with placeholders."
)


def _ascii_safe(text: str) -> str:
    """Normalize unicode to closest ASCII for fpdf2 latin-1 encoding."""
    return unicodedata.normalize("NFKD", text).encode("latin-1", errors="replace").decode("latin-1")


def _markdown_to_pdf(title: str, markdown: str) -> bytes:
    """Convert markdown text to a PDF using fpdf2 (pure Python, no system deps)."""
    from fpdf import FPDF

    pdf = FPDF()
    pdf.set_auto_page_break(auto=True, margin=15)
    pdf.add_page()
    pdf.set_margins(20, 20, 20)

    lines = markdown.splitlines()
    in_code = False
    code_buf: list[str] = []

    def flush_code(buf: list[str]) -> None:
        pdf.set_fill_color(240, 240, 240)
        pdf.set_font("Courier", size=8)
        for cl in buf:
            pdf.multi_cell(0, 4, _ascii_safe(cl), fill=True)
        pdf.ln(2)
        pdf.set_fill_color(255, 255, 255)

    for line in lines:
        if line.startswith("```"):
            if in_code:
                flush_code(code_buf)
                code_buf = []
                in_code = False
            else:
                in_code = True
            continue

        if in_code:
            code_buf.append(line)
            continue

        # Headings
        if line.startswith("### "):
            pdf.set_font("Helvetica", "B", 11)
            pdf.multi_cell(0, 6, _ascii_safe(line[4:]))
            pdf.ln(1)
        elif line.startswith("## "):
            pdf.set_font("Helvetica", "B", 13)
            pdf.multi_cell(0, 7, _ascii_safe(line[3:]))
            pdf.ln(2)
        elif line.startswith("# "):
            pdf.set_font("Helvetica", "B", 16)
            pdf.multi_cell(0, 9, _ascii_safe(line[2:]))
            pdf.ln(3)
        elif line.startswith(("- ", "* ", "+ ")):
            pdf.set_font("Helvetica", size=10)
            pdf.multi_cell(0, 5, "  • " + _ascii_safe(line[2:]))
        elif re.match(r"^\d+\. ", line):
            pdf.set_font("Helvetica", size=10)
            pdf.multi_cell(0, 5, "  " + _ascii_safe(line))
        elif line.strip() == "" or line.strip() == "---":
            pdf.ln(3)
        else:
            # Strip inline markdown (bold/italic/code) for plain rendering
            clean = re.sub(r"(\*\*|__)(.*?)\1", r"\2", line)
            clean = re.sub(r"(\*|_)(.*?)\1", r"\2", clean)
            clean = re.sub(r"`([^`]+)`", r"\1", clean)
            pdf.set_font("Helvetica", size=10)
            pdf.multi_cell(0, 5, _ascii_safe(clean))

    if in_code and code_buf:
        flush_code(code_buf)

    return pdf.output()


def _extract_input(context: "RequestContext") -> tuple[str, str, str]:
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
    def __init__(self) -> None:
        self._client = anthropic.AsyncAnthropic(api_key=ANTHROPIC_API_KEY)

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
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

            response = await self._client.messages.create(
                model=MODEL,
                max_tokens=MAX_TOKENS,
                system=_SYSTEM_PROMPT,
                messages=[{"role": "user", "content": _build_prompt(fmt, title, content)}],
            )
            rendered = response.content[0].text
            # Strip markdown fences the model sometimes wraps around its output.
            rendered = re.sub(r"^```[a-zA-Z]*\n?", "", rendered.strip())
            rendered = re.sub(r"\n?```$", "", rendered.strip())

            artifact = Artifact()
            artifact.artifact_id = "docu-output"
            artifact.name = meta["filename"]
            artifact.description = f"{fmt} documentation: {title}"
            part = artifact.parts.add()

            if fmt == "pdf":
                # Convert Claude's markdown output to binary PDF
                pdf_bytes = await asyncio.get_event_loop().run_in_executor(
                    None, _markdown_to_pdf, title, rendered
                )
                part.data = pdf_bytes
                part.filename = meta["filename"]
                part.media_type = meta["media_type"]
            else:
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
        "Accepts structured analysis text and a format (html, markdown, slides, pdf) "
        "and returns a ready-to-use file artifact."
    )
    card.version = "1.1.0"
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
            f"Input: JSON with fields: format ({'/'.join(_FORMAT_META)}), title (string), content (markdown string). "
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
