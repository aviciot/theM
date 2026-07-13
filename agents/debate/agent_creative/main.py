"""
agent_creative — A2A creative agent with two skills:

1. argue_creative — produces a surprising argument from an unexpected field.
   Input: {question, position, round (1|2), fields (optional), opponent_arguments (round 2)}

2. suggest_topic — generates a thought-provoking debate question from a random field.
   Input: {fields (optional list), theme (optional hint)}
   Output: {question, rationale, field, alternatives (list of 2 other options)}
"""

import json
import os
import random
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
PORT = int(os.getenv("PORT", "9403"))


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
        return {"argument": text.strip(), "field": "unknown", "insight": "", "confidence": 0.5,
                "main_point": text.strip()[:120], "approach": "creative/unknown"}

DEFAULT_FIELDS = [
    "evolutionary biology", "behavioral economics", "ancient history",
    "cognitive psychology", "game theory", "anthropology",
    "neuroscience", "philosophy of mind", "complexity theory",
    "systems thinking", "sociology", "information theory",
]

SUGGEST_TOPIC_PROMPT = """You are a creative thinker who generates surprising, thought-provoking debate questions.

Your goal: pick one field from the list given (or choose your own if none given), and generate a debate question that:
- Is genuinely debatable (smart people can argue both sides)
- Has a surprising or non-obvious angle — not "is AI good or bad?"
- Is specific enough to debate concretely, not vague philosophy
- Would make someone think "I've never thought about it that way"

Output ONLY valid JSON:
{
  "question": "the debate question",
  "rationale": "one sentence on why this question is surprising or interesting",
  "field": "the field that inspired it",
  "alternatives": ["alternative question 1", "alternative question 2"]
}"""

SYSTEM_PROMPT = """You are a creative lateral thinker who argues from unexpected angles using insights from a specific field of knowledge.

Rules:
- Draw ONLY from the specified field — this is your constraint and your power
- Find the most surprising, non-obvious insight from that field that supports the position
- Your argument should make the listener think "I never considered it from THAT angle"
- Be concrete — use specific concepts, phenomena, or findings from the field
- Do not use generic arguments — find the angle nobody else would find
- In round 2, go even deeper into the field to find a counter to opponent weaknesses

Output format — return ONLY valid JSON:
{
  "argument": "your full creative argument (2-4 paragraphs)",
  "field": "the field you drew from",
  "insight": "the specific insight or phenomenon from that field you leveraged",
  "confidence": 0.80,
  "main_point": "one sentence summary of your creative angle",
  "approach": "creative/<field name>"
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


class CreativeExecutor(AgentExecutor):
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

        skill = data.get("skill", "argue_creative")
        # Infer skill from payload shape: no question+position → suggest_topic
        if not data.get("question") and not data.get("position"):
            skill = "suggest_topic"

        fields = data.get("fields") or DEFAULT_FIELDS
        chosen_field = random.choice(fields) if isinstance(fields, list) else fields

        try:
            client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)

            # ── suggest_topic ────────────────────────────────────────────────
            if skill == "suggest_topic":
                await event_queue.enqueue_event(_emit_status(
                    context.task_id, context.context_id,
                    TaskState.TASK_STATE_WORKING,
                    {"current_task": f"Exploring {chosen_field} for debate ideas", "status": "thinking"},
                ))

                theme = data.get("theme", "")
                user_content = f"Field to draw from: {chosen_field}"
                if theme:
                    user_content += f"\nTheme hint: {theme}"

                response = client.messages.create(
                    model=MODEL,
                    max_tokens=1024,
                    system=SUGGEST_TOPIC_PROMPT,
                    messages=[{"role": "user", "content": user_content}],
                )
                result = _parse_json(response.content[0].text)

                await event_queue.enqueue_event(_emit_status(
                    context.task_id, context.context_id,
                    TaskState.TASK_STATE_WORKING,
                    {"current_task": "Topic ready", "question": result.get("question", "")[:120], "status": "finalizing"},
                ))

                artifact = Artifact()
                artifact.artifact_id = "topic"
                artifact.name = "suggested_topic.json"
                part = artifact.parts.add()
                part.text = json.dumps({**result, "field": chosen_field})
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
                return

            # ── argue_creative ───────────────────────────────────────────────
            question = data.get("question", "")
            position = data.get("position", "")
            round_num = data.get("round", 1)
            opponent_args = data.get("opponent_arguments", [])

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {"current_task": f"Round {round_num} — exploring {chosen_field}", "approach": f"creative/{chosen_field}", "status": "searching for angle"},
            ))

            user_content = (
                f"Question: {question}\n"
                f"Position to argue: {position}\n"
                f"Field to draw from: {chosen_field}"
            )
            if round_num == 2 and opponent_args:
                user_content += "\n\nOpponent arguments to counter:\n" + "\n---\n".join(
                    f"{a.get('agent', 'opponent')}: {a.get('argument', '')[:500]}"
                    for a in (_normalize_arg(x) for x in opponent_args)
                )

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {"current_task": f"Drawing insight from {chosen_field}", "approach": f"creative/{chosen_field}", "status": "working"},
            ))

            response = client.messages.create(
                model=MODEL,
                max_tokens=2048,
                system=SYSTEM_PROMPT,
                messages=[{"role": "user", "content": user_content}],
            )
            result = _parse_json(response.content[0].text)

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {
                    "current_task": "Creative angle found",
                    "approach": result.get("approach", f"creative/{chosen_field}"),
                    "main_point": result.get("main_point", "")[:120],
                    "confidence": result.get("confidence", 0.8),
                    "status": "finalizing",
                },
            ))

            artifact = Artifact()
            artifact.artifact_id = "argument"
            artifact.name = "creative_argument.json"
            part = artifact.parts.add()
            part.text = json.dumps({**result, "agent": "agent_creative", "round": round_num, "field": chosen_field})
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
            part.text = f"agent_creative error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "agent-creative"
    card.description = (
        "Creative agent with two skills: "
        "(1) argue_creative — produces a surprising argument from an unexpected field of knowledge; "
        "(2) suggest_topic — generates a thought-provoking debate question when the user has no topic. "
        "Call suggest_topic with JSON {fields (optional), theme (optional)} to get {question, rationale, field, alternatives}. "
        "Call argue_creative with JSON {question, position, round, fields (optional), opponent_arguments (round 2)} to argue."
    )
    card.version = "1.0.0"
    card.icon_url = "auto_awesome"
    iface = card.supported_interfaces.add()
    iface.url = f"http://agent-creative:{PORT}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False

    skill = card.skills.add()
    skill.id = "argue_creative"
    skill.name = "Creative Lateral Argument"
    skill.description = (
        "Finds the most surprising non-obvious argument by drawing from an unexpected field of knowledge. "
        "Input: JSON {question, position, round (1|2), fields (optional list), opponent_arguments (round 2 only)}. "
        "Output: JSON {argument, field, insight, confidence, main_point, approach}."
    )
    skill.input_modes.append("application/json")
    skill.output_modes.append("application/json")
    skill.examples.append('{"question": "Is remote work better?", "position": "Yes", "round": 1, "fields": ["evolutionary biology", "game theory"]}')

    skill2 = card.skills.add()
    skill2.id = "suggest_topic"
    skill2.name = "Suggest Debate Topic"
    skill2.description = (
        "Generates a surprising, thought-provoking debate question drawn from an unexpected field. "
        "Call this when the user has no topic or says 'surprise me'. "
        "Input: JSON {fields (optional list of fields), theme (optional hint string)}. "
        "Output: JSON {question, rationale, field, alternatives (list of 2 other questions)}. "
        "Present the question to the user for approval before starting the debate."
    )
    skill2.input_modes.append("application/json")
    skill2.output_modes.append("application/json")
    skill2.examples.append('{"fields": ["game theory", "neuroscience"]}')

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="agent-creative")
    card = make_agent_card()
    handler = DefaultRequestHandler(
        agent_executor=CreativeExecutor(),
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
