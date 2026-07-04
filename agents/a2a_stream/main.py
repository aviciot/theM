"""
a2a-stream — A2A v1.0 test agent.
Streams a response word by word via TaskArtifactUpdateEvent chunks.
Advertises capabilities.streaming=True in its Agent Card.
"""

import asyncio
import os
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

        # Stream words as artifact chunks
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
    card.description = "Streams a response word by word via A2A artifact chunks."
    card.version = "1.0.0"
    iface = card.supported_interfaces.add()
    iface.url = f"http://a2a-stream:{os.getenv('PORT', '9202')}"
    card.capabilities.streaming = True
    card.capabilities.push_notifications = False
    skill = card.skills.add()
    skill.id = "stream_words"
    skill.name = "Stream Words"
    skill.description = "Streams a response word by word."
    skill.input_modes.append("text/plain")
    skill.output_modes.append("text/plain")
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
