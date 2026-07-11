"""
security-scanner — A2A v1.0 agent.

Receives a target agent's profile (endpoint_url, description, skills, agent_card)
and returns a structured security assessment: HTTP surface probes + LLM card/skill
risk analysis.

Input: typed data part (application/json) with fields:
  agent_id, slug, display_name, description, endpoint_url,
  agent_card, skills, supports_streaming, supports_push, has_auth_token

Output: A2A artifact with:
  - parts[0].text      = JSON-encoded ScanResult
  - parts[0].media_type = "application/json"
  - parts[0].filename   = "security-scan.json"
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

from scanner import run_scan

ANTHROPIC_API_KEY = os.getenv("ANTHROPIC_API_KEY", "")
PORT = int(os.getenv("PORT", "9500"))


def _extract_input(context: RequestContext) -> dict:
    """
    Extract scan payload from A2A message parts.
    Prefers typed data part (application/json). Falls back to parsing text as JSON.
    """
    if not context.message:
        return {}

    for part in context.message.parts:
        if part.HasField("data"):
            return json_format.MessageToDict(part.data.struct_value)
        if part.HasField("text"):
            try:
                return json.loads(part.text)
            except Exception:
                return {"description": part.text}

    return {}


class SecurityScannerExecutor(AgentExecutor):
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
            payload = _extract_input(context)
            result = await run_scan(payload, anthropic_api_key=ANTHROPIC_API_KEY)

            artifact = Artifact()
            artifact.artifact_id = "scan-result"
            artifact.name = "security-scan.json"
            artifact.description = f"Security scan for agent: {payload.get('display_name', payload.get('slug', '?'))}"
            part = artifact.parts.add()
            part.text = json.dumps(result)
            part.filename = "security-scan.json"
            part.media_type = "application/json"

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
            part.text = f"SecurityScanner error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "security-scanner"
    card.description = (
        "Analyzes a registered agent for security posture: HTTP surface probes "
        "(TLS, auth enforcement, reachability) and LLM analysis of the agent card "
        "and declared skills for over-broad scope, prompt-injection risk, and missing "
        "input guardrails. Returns a 0-100 score, risk level, summary, and actionable findings."
    )
    card.version = "1.0.0"
    iface = card.supported_interfaces.add()
    iface.url = f"http://them-security-agent:{PORT}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False

    skill = card.skills.add()
    skill.id = "scan_agent"
    skill.name = "Scan Agent"
    skill.description = (
        "Given a target agent's endpoint URL, agent card, and declared skills, "
        "run HTTP surface probes and an LLM card/skill risk analysis, returning a "
        "structured security assessment with score (0-100), risk level, summary, and findings."
    )
    skill.tags.append("security")
    skill.tags.append("audit")
    skill.tags.append("analysis")
    skill.input_modes.append("application/json")
    skill.output_modes.append("application/json")

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="security-scanner")
    card = make_agent_card()
    task_store = InMemoryTaskStore()
    executor = SecurityScannerExecutor()
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
