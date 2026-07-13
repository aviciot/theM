"""
agent_logic — A2A debater agent.

Produces the strongest logic-based argument using reasoning, principles, and deduction.
Accepts typed JSON input: {question, position, context (optional), round (1|2), opponent_arguments (round 2)}
Returns a structured argument artifact.
"""

import json
import os
import uvicorn
from fastapi import FastAPI
from google.protobuf import json_format

import anthropic

from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.events.event_queue import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.routes import add_a2a_routes_to_fastapi, create_agent_card_routes, create_jsonrpc_routes
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCard, Artifact, Task,
    TaskArtifactUpdateEvent, TaskState, TaskStatusUpdateEvent,
)

ANTHROPIC_API_KEY = os.getenv("ANTHROPIC_API_KEY", "")
MODEL = os.getenv("MODEL", "claude-haiku-4-5-20251001")
PORT = int(os.getenv("PORT", "9402"))


def _normalize_arg(a) -> dict:
    """Normalize an opponent_arguments entry to a dict regardless of whether the LLM passed a string or object."""
    if isinstance(a, dict):
        return a
    return {"agent": "opponent", "argument": str(a)}


def _parse_json(text: str) -> dict:
    t = text.strip()
    if t.startswith("```"):
        lines = t.splitlines()
        t = "\n".join(lines[1:-1] if lines[-1].strip() == "```" else lines[1:])
    t = t.strip()
    try:
        obj, _ = json.JSONDecoder().raw_decode(t)
        return obj
    except json.JSONDecodeError:
        return {"argument": text.strip(), "logical_chain": [], "confidence": 0.5,
                "main_point": text.strip()[:120], "approach": "logic-based"}

SYSTEM_PROMPT = """You are an expert logic-based debater. Your role is to construct the strongest possible argument using pure reasoning, first principles, logical deduction, and structured thinking.

Rules:
- Build arguments from first principles — show WHY something must be true by logical necessity
- Use deductive and inductive reasoning explicitly
- Identify and preemptively counter logical fallacies in opposing positions
- Structure arguments as: premise → reasoning → conclusion
- Do not rely on external data — your weapon is the strength of the reasoning chain itself
- In round 2, find the logical weaknesses in opponent arguments and exploit them

Output format — return ONLY valid JSON:
{
  "argument": "your full logic-based argument (2-4 paragraphs)",
  "logical_chain": ["premise 1", "reasoning step", "conclusion"],
  "confidence": 0.85,
  "main_point": "one sentence summary of your core logical claim",
  "approach": "logic-based"
}"""


def _emit_status(task_id: str, context_id: str, state: TaskState, message: dict | None = None):
    ev = TaskStatusUpdateEvent()
    ev.task_id = task_id
    ev.context_id = context_id
    ev.status.state = state
    if message:
        ev.status.message.role = 2
        ev.status.message.message_id = task_id + "-status"
        part = ev.status.message.parts.add()
        part.text = json.dumps(message)
    return ev


class LogicExecutor(AgentExecutor):
    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        task = Task()
        task.id = context.task_id
        task.context_id = context.context_id
        task.status.state = TaskState.TASK_STATE_SUBMITTED
        await event_queue.enqueue_event(task)

        data = {}
        for part in context.message.parts:
            if part.HasField("data"):
                data = json_format.MessageToDict(part.data.struct_value)
                break
            elif part.HasField("text"):
                try:
                    data = json.loads(part.text)
                except Exception:
                    data = {"question": part.text}

        question = data.get("question", "")
        position = data.get("position", "")
        round_num = data.get("round", 1)
        opponent_args = data.get("opponent_arguments", [])

        await event_queue.enqueue_event(_emit_status(
            context.task_id, context.context_id,
            TaskState.TASK_STATE_WORKING,
            {"current_task": f"Round {round_num} — structuring logical framework", "approach": "logic-based", "status": "reasoning"},
        ))

        try:
            user_content = f"Question: {question}\nPosition to argue: {position}"
            if round_num == 2 and opponent_args:
                user_content += "\n\nOpponent arguments to counter:\n" + "\n---\n".join(
                    f"{a.get('agent', 'opponent')}: {a.get('argument', '')[:500]}"
                    for a in (_normalize_arg(x) for x in opponent_args)
                )

            client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {"current_task": "Building deductive chain", "approach": "logic-based", "status": "working"},
            ))

            response = client.messages.create(
                model=MODEL,
                max_tokens=2048,
                system=SYSTEM_PROMPT,
                messages=[{"role": "user", "content": user_content}],
            )
            raw = response.content[0].text
            result = _parse_json(raw)

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {
                    "current_task": "Argument ready",
                    "approach": result.get("approach", "logic-based"),
                    "main_point": result.get("main_point", "")[:120],
                    "confidence": result.get("confidence", 0.8),
                    "status": "finalizing",
                },
            ))

            artifact = Artifact()
            artifact.artifact_id = "argument"
            artifact.name = "logic_argument.json"
            part = artifact.parts.add()
            part.text = json.dumps({**result, "agent": "agent_logic", "round": round_num})
            part.media_type = "application/json"

            art_ev = TaskArtifactUpdateEvent()
            art_ev.task_id = context.task_id
            art_ev.context_id = context.context_id
            art_ev.artifact.CopyFrom(artifact)
            art_ev.last_chunk = True
            await event_queue.enqueue_event(art_ev)

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id, TaskState.TASK_STATE_COMPLETED,
            ))

        except Exception as exc:
            err = TaskStatusUpdateEvent()
            err.task_id = context.task_id
            err.context_id = context.context_id
            err.status.state = TaskState.TASK_STATE_FAILED
            err.status.message.role = 2
            err.status.message.message_id = context.task_id + "-err"
            part = err.status.message.parts.add()
            part.text = f"agent_logic error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "agent-logic"
    card.description = (
        "Produces the strongest logic-based argument using reasoning, first principles, and deduction. "
        "Input: JSON with fields: question (string), position (string), round (1|2), "
        "opponent_arguments (array for round 2). "
        "Output: structured argument with logical chain, confidence score, and main point."
    )
    card.version = "1.0.0"
    card.icon_url = "psychology"
    iface = card.supported_interfaces.add()
    iface.url = f"http://agent-logic:{PORT}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False

    skill = card.skills.add()
    skill.id = "argue_logic"
    skill.name = "Logic-Based Argument"
    skill.description = (
        "Constructs the strongest possible argument using pure reasoning and logical deduction. "
        "Input: JSON {question, position, round (1|2), opponent_arguments (round 2 only)}. "
        "Output: JSON {argument, logical_chain, confidence, main_point, approach}."
    )
    skill.input_modes.append("application/json")
    skill.output_modes.append("application/json")
    skill.examples.append('{"question": "Is remote work better?", "position": "Yes", "round": 1}')

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="agent-logic")
    card = make_agent_card()
    handler = DefaultRequestHandler(
        agent_executor=LogicExecutor(),
        task_store=InMemoryTaskStore(),
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
