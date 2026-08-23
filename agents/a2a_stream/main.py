"""
a2a-stream — A2A v1.0 test agent.
Streams a response word by word via TaskArtifactUpdateEvent chunks,
then emits a small binary file artifact to test the streaming file path.
Advertises capabilities.streaming=True in its Agent Card.
"""

import asyncio
import io
import os
import zipfile
import uvicorn
from fastapi import FastAPI

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

STREAM_WORDS = "The quick brown fox jumps over the lazy dog. Streaming word by word via A2A artifacts.".split()
WORD_DELAY_S = float(os.getenv("WORD_DELAY_S", "0.1"))


def _make_dummy_zip() -> bytes:
    """Build a minimal valid zip containing one text file."""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("hello.txt", "Hello from a2a-stream!\nThis is a dummy zip artifact.\n")
    return buf.getvalue()


class StreamExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        # SDK v1.1: must enqueue Task object first
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        working_event = TaskStatusUpdateEvent()
        working_event.task_id = context.task_id
        working_event.context_id = context.context_id
        working_event.status.state = TaskState.TASK_STATE_WORKING
        await event_queue.enqueue_event(working_event)

        # Stream words as text artifact chunks
        for i, word in enumerate(STREAM_WORDS):
            is_last = (i == len(STREAM_WORDS) - 1)
            chunk_text = word + ("" if is_last else " ")

            artifact = Artifact()
            artifact.artifact_id = "stream-result"
            artifact.name = "Streamed Result"
            part = artifact.parts.add()
            part.text = chunk_text

            chunk_event = TaskArtifactUpdateEvent()
            chunk_event.task_id = context.task_id
            chunk_event.context_id = context.context_id
            chunk_event.artifact.CopyFrom(artifact)
            chunk_event.append = (i > 0)
            chunk_event.last_chunk = is_last
            await event_queue.enqueue_event(chunk_event)

            if not is_last:
                await asyncio.sleep(WORD_DELAY_S)

        # Emit a binary file artifact (dummy zip) to exercise the file streaming path
        await asyncio.sleep(0.1)
        zip_bytes = _make_dummy_zip()
        file_artifact = Artifact()
        file_artifact.artifact_id = "stream-file"
        file_artifact.name = "Stream Output"
        file_part = file_artifact.parts.add()
        file_part.raw = zip_bytes
        file_part.filename = "stream_output.zip"
        file_part.media_type = "application/zip"

        file_event = TaskArtifactUpdateEvent()
        file_event.task_id = context.task_id
        file_event.context_id = context.context_id
        file_event.artifact.CopyFrom(file_artifact)
        file_event.append = False
        file_event.last_chunk = True
        await event_queue.enqueue_event(file_event)

        done_event = TaskStatusUpdateEvent()
        done_event.task_id = context.task_id
        done_event.context_id = context.context_id
        done_event.status.state = TaskState.TASK_STATE_COMPLETED
        await event_queue.enqueue_event(done_event)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        cancel_event = TaskStatusUpdateEvent()
        cancel_event.task_id = context.task_id
        cancel_event.context_id = context.context_id
        cancel_event.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(cancel_event)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "a2a-stream"
    card.description = "Streams text word-by-word then emits a zip file artifact via A2A streaming."
    card.version = "1.1.0"
    iface = card.supported_interfaces.add()
    iface.url = f"http://a2a-stream:{os.getenv('PORT', '9202')}"
    card.capabilities.streaming = True
    card.capabilities.push_notifications = False
    skill = card.skills.add()
    skill.id = "stream_words"
    skill.name = "Stream Words + File"
    skill.description = "Streams text word by word, then emits a zip file artifact."
    skill.input_modes.append("text/plain")
    skill.output_modes.append("text/plain")
    skill.output_modes.append("application/zip")
    return card


def create_app() -> FastAPI:
    app = FastAPI(title="a2a-stream")
    card = make_agent_card()
    task_store = InMemoryTaskStore()
    executor = StreamExecutor()
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
    port = int(os.getenv("PORT", "9202"))
    uvicorn.run("main:app", host="0.0.0.0", port=port, log_level="info")
