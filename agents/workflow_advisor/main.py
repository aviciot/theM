"""
workflow-advisor — A2A v1.0 agent.

Receives a serialized workflow graph (nodes, edges, orchestrator configs,
agent descriptions) and streams an advisory: what's missing, what's broken,
what could be improved.  Supports multi-turn follow-up via conversation history
embedded in the message context by the orchestrator.

Input: text part containing the user message.  On the first turn the
orchestrator embeds the serialized workflow JSON in the message.  On follow-up
turns, the orchestrator's history_window mechanism passes prior context.

Output: streaming text parts (the advisory, token by token).
"""

import json
import os

import uvicorn
from fastapi import FastAPI
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

from advisor import stream_analysis

ANTHROPIC_API_KEY = os.getenv("ANTHROPIC_API_KEY", "")
PORT = int(os.getenv("PORT", "9600"))


def _extract_message(context: RequestContext) -> str:
    """Extract plain text from the A2A message parts."""
    if not context.message:
        return ""
    for part in context.message.parts:
        if part.HasField("text"):
            return part.text
        if part.HasField("data"):
            return json.dumps(json_format.MessageToDict(part.data.struct_value))
    return ""


def _extract_workflow(context: RequestContext) -> dict:
    """
    Try to parse the workflow JSON from the message.  The orchestrator embeds
    it as a JSON block prefixed with 'Analyze this workflow:'.  Falls back to
    empty dict if not found (follow-up turns won't have it).
    """
    text = _extract_message(context)
    marker = "Analyze this workflow:"
    if marker in text:
        json_part = text[text.index(marker) + len(marker):].strip()
        try:
            return json.loads(json_part)
        except Exception:
            pass
    return {}


def _extract_history(context: RequestContext) -> list[dict]:
    """
    Extract conversation history injected by the orchestrator into prior parts.
    The orchestrator's task_messages history is surfaced as a JSON data part
    with key 'conversation_history' if the orchestrator passes it.
    For now we rely on the orchestrator's own history_window to re-send prior
    turns, so we return an empty list — the full turn context arrives via the
    orchestrator's messages array to the LLM, not to the agent directly.
    """
    return []


class WorkflowAdvisorExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        # Signal: submitted
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        # Signal: working
        working = TaskStatusUpdateEvent()
        working.task_id = context.task_id
        working.context_id = context.context_id
        working.status.state = TaskState.TASK_STATE_WORKING
        await event_queue.enqueue_event(working)

        try:
            user_message = _extract_message(context)
            workflow = _extract_workflow(context)
            history = _extract_history(context)

            # Stream the response — collect all chunks then emit as one artifact
            # (A2A streaming via TaskArtifactUpdateEvent with last_chunk flags)
            full_text = []
            chunk_index = 0

            async for chunk in stream_analysis(
                workflow=workflow,
                conversation_history=history,
                user_message=user_message,
                anthropic_api_key=ANTHROPIC_API_KEY,
            ):
                full_text.append(chunk)

                art_event = TaskArtifactUpdateEvent()
                art_event.task_id = context.task_id
                art_event.context_id = context.context_id
                art_event.append_index = chunk_index
                art_event.last_chunk = False

                artifact = art_event.artifact
                artifact.artifact_id = "advisor-response"
                artifact.name = "workflow-advisory"
                part = artifact.parts.add()
                part.text = chunk
                part.media_type = "text/plain"

                await event_queue.enqueue_event(art_event)
                chunk_index += 1

            # Final chunk marker
            final_event = TaskArtifactUpdateEvent()
            final_event.task_id = context.task_id
            final_event.context_id = context.context_id
            final_event.append_index = chunk_index
            final_event.last_chunk = True

            final_artifact = final_event.artifact
            final_artifact.artifact_id = "advisor-response"
            final_artifact.name = "workflow-advisory"
            final_part = final_artifact.parts.add()
            final_part.text = ""
            final_part.media_type = "text/plain"

            await event_queue.enqueue_event(final_event)

            # Signal: completed
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
            part.text = f"WorkflowAdvisor error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "workflow-advisor"
    card.description = (
        "Analyzes a the-M workflow canvas (orchestrators, agents, entry points, "
        "connections) and provides actionable advisory: missing configuration, "
        "routing logic gaps, orchestrator prompt quality, agent description clarity, "
        "structural issues, and security considerations. Supports multi-turn "
        "follow-up for prompt suggestions and detailed explanations."
    )
    card.version = "1.0.0"
    iface = card.supported_interfaces.add()
    iface.url = f"http://them-workflow-advisor:{PORT}"
    card.capabilities.streaming = True
    card.capabilities.push_notifications = False

    skill = card.skills.add()
    skill.id = "advise_workflow"
    skill.name = "Advise Workflow"
    skill.description = (
        "Given a serialized workflow graph (nodes, edges, orchestrator system prompts, "
        "agent descriptions, entry point config), analyze it for completeness, routing "
        "logic, prompt quality, structural issues, and security posture. Returns a "
        "streamed advisory with issues, warnings, and actionable suggestions."
    )
    skill.tags.extend(["advisor", "analysis", "workflow", "orchestration"])
    skill.input_modes.append("text/plain")
    skill.output_modes.append("text/plain")

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="workflow-advisor")
    card = make_agent_card()
    task_store = InMemoryTaskStore()
    executor = WorkflowAdvisorExecutor()
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
