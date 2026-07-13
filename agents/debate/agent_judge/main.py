"""
agent_judge — A2A judge agent.

Receives all debater arguments, scores each on 5 criteria, picks a winner,
explains the decision, and synthesizes a final combined answer.
Accepts typed JSON input: {question, arguments (array), round (1|2), final (bool)}
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
MODEL = os.getenv("MODEL", "claude-sonnet-4-6")
PORT = int(os.getenv("PORT", "9404"))


def _normalize_arg(a) -> dict:
    """Normalize an arguments entry to a dict regardless of whether the LLM passed a string or object."""
    if isinstance(a, dict):
        return a
    return {"agent": "opponent", "argument": str(a), "approach": "unknown"}


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
        return {"scores": [], "winner": "unknown", "winner_reason": text.strip()[:200], "synthesis": ""}

SYSTEM_PROMPT = """You are an impartial debate judge. You evaluate arguments strictly on merit.

Scoring criteria (each 0-10):
- clarity: how clearly the argument is expressed
- relevance: how directly it addresses the question
- logic: soundness of reasoning
- evidence: strength and quality of supporting evidence or insights
- persuasiveness: overall impact and conviction

Your job:
1. Score each argument on all 5 criteria
2. Identify the winner (highest total score)
3. Explain briefly WHY that argument won (2-3 sentences)
4. If final=true: synthesize the best elements of ALL arguments into one superior final answer

Output format — return ONLY valid JSON:
{
  "scores": [
    {
      "agent": "agent_evidence",
      "clarity": 8, "relevance": 9, "logic": 7, "evidence": 9, "persuasiveness": 8,
      "total": 41,
      "summary": "one sentence assessment"
    }
  ],
  "winner": "agent_evidence",
  "winner_reason": "brief explanation of why this argument won",
  "synthesis": "the final combined answer drawing the best from all arguments (only when final=true, otherwise empty string)"
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


class JudgeExecutor(AgentExecutor):
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
                    data = {}

        question = data.get("question", "")
        arguments = data.get("arguments", [])
        round_num = data.get("round", 1)
        is_final = data.get("final", False)

        await event_queue.enqueue_event(_emit_status(
            context.task_id, context.context_id,
            TaskState.TASK_STATE_WORKING,
            {"current_task": f"Round {round_num} — reading {len(arguments)} arguments", "status": "evaluating"},
        ))

        try:
            args_text = "\n\n".join(
                f"=== {a.get('agent', f'agent_{i}')} (approach: {a.get('approach', 'unknown')}) ===\n{a.get('argument', '') or a.get('main_point', '')}"
                for i, a in enumerate(_normalize_arg(x) for x in arguments)
            )

            user_content = (
                f"Question being debated: {question}\n\n"
                f"Arguments to judge:\n{args_text}\n\n"
                f"Round: {round_num}\n"
                f"Final synthesis required: {is_final}"
            )

            client = anthropic.Anthropic(api_key=ANTHROPIC_API_KEY)

            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {"current_task": "Scoring arguments", "status": "judging"},
            ))

            response = client.messages.create(
                model=MODEL,
                max_tokens=2048,
                system=SYSTEM_PROMPT,
                messages=[{"role": "user", "content": user_content}],
            )
            raw = response.content[0].text
            result = _parse_json(raw)

            winner = result.get("winner", "unknown")
            await event_queue.enqueue_event(_emit_status(
                context.task_id, context.context_id,
                TaskState.TASK_STATE_WORKING,
                {
                    "current_task": f"Verdict: {winner} wins",
                    "status": "finalizing",
                    "winner": winner,
                    "reason": result.get("winner_reason", "")[:120],
                },
            ))

            artifact = Artifact()
            artifact.artifact_id = "verdict"
            artifact.name = "verdict.json"
            part = artifact.parts.add()
            part.text = json.dumps({**result, "round": round_num, "final": is_final})
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
            part.text = f"agent_judge error: {exc}"
            await event_queue.enqueue_event(err)

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        ev = TaskStatusUpdateEvent()
        ev.task_id = context.task_id
        ev.context_id = context.context_id
        ev.status.state = TaskState.TASK_STATE_CANCELED
        await event_queue.enqueue_event(ev)


def make_agent_card() -> AgentCard:
    card = AgentCard()
    card.name = "agent-judge"
    card.description = (
        "Impartial debate judge. Scores multiple arguments on clarity, relevance, logic, evidence, and persuasiveness. "
        "Picks a winner, explains why, and synthesizes a final combined answer when requested. "
        "Input: JSON with fields: question (string), arguments (array of argument objects), round (1|2), final (bool). "
        "Output: scores per agent, winner, winner_reason, and synthesis (if final=true)."
    )
    card.version = "1.0.0"
    card.icon_url = "gavel"
    iface = card.supported_interfaces.add()
    iface.url = f"http://agent-judge:{PORT}"
    card.capabilities.streaming = False
    card.capabilities.push_notifications = False

    skill = card.skills.add()
    skill.id = "judge_debate"
    skill.name = "Debate Judge"
    skill.description = (
        "Scores all debater arguments, picks the winner, explains the verdict, and synthesizes a final answer. "
        "Input: JSON {question, arguments (array), round (1|2), final (bool)}. "
        "Output: JSON {scores, winner, winner_reason, synthesis}."
    )
    skill.input_modes.append("application/json")
    skill.output_modes.append("application/json")

    return card


def create_app() -> FastAPI:
    app = FastAPI(title="agent-judge")
    card = make_agent_card()
    handler = DefaultRequestHandler(
        agent_executor=JudgeExecutor(),
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
