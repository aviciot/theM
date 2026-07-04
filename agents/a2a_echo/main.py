"""
a2a-echo — A2A v1.0 test agent.
Echoes the input message verbatim. Completes synchronously.
"""

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


class EchoExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        user_text = ""
        if context.message:
            for part in context.message.parts:
                if part.HasField("text"):
                    user_text += part.text

        # SDK v1.1: must enqueue Task object first
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        # Transition to working
        working_event = TaskStatusUpdateEvent()
        working_event.task_id = context.task_id
        working_event.context_id = context.context_id
        working_event.status.state = TaskState.TASK_STATE_WORKING
        await event_queue.enqueue_event(working_event)

        # Build result artifact
        artifact = Artifact()
        artifact.artifact_id = "echo-result"
        artifact.name = "Echo Result"
        part = artifact.parts.add()
        part.text = user_text or "(empty)"

        artifact_event = TaskArtifactUpdateEvent()
        artifact_event.task_id = context.task_id
        artifact_event.context_id = context.context_id
        artifact_event.artifact.CopyFrom(artifact)
        artifact_event.last_chunk = True
        await event_queue.enqueue_event(artifact_event)

        # Complete
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
    card.name = "a2a-echo"
    card.description = "Echoes the input message verbatim. A2A v1.0 test agent."
    card.version = "1.0.0"
    iface = card.supported_interfaces.add()
    iface.url = f"http://a2a-echo:{os.getenv('PORT', '9200')}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False
    skill = card.skills.add()
    skill.id = "echo"
    skill.name = "Echo"
    skill.description = "Returns the input message unchanged."
    skill.input_modes.append("text/plain")
    skill.output_modes.append("text/plain")
    return card


def create_app() -> FastAPI:
    app = FastAPI(title="a2a-echo")
    card = make_agent_card()
    task_store = InMemoryTaskStore()
    executor = EchoExecutor()
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
    port = int(os.getenv("PORT", "9200"))
    uvicorn.run("main:app", host="0.0.0.0", port=port, log_level="info")
