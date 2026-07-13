"""
OrchestrationWorkflow — durable replacement for task_runner.run().

Workflow rules (Temporal determinism):
- No asyncio.sleep, no datetime.now(), no random, no I/O of any kind
- All non-deterministic work in Activities
- workflow.uuid4() for ID generation
- workflow.now() for timestamps
- Pure Python (asyncio.Semaphore, list ops) is allowed
"""

import asyncio
from datetime import timedelta
from decimal import Decimal
from typing import Optional

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ActivityError, CancelledError

from app.temporal.activities import (
    finalize_run_activity,
    init_run_activity,
    invoke_agent_activity,
    load_orchestration_context_activity,
    plan_turn_activity,
    record_tool_results_activity,
    summarize_context_activity,
)
from app.temporal.shared import RecordToolResultsInput
from app.temporal.serde import (
    deserialize_messages,
    dict_to_tool_call,
    serialize_messages,
    tool_call_to_dict,
)
from app.temporal.shared import (
    FinalizeRunInput,
    InvokeAgentInput,
    InvokeAgentResult,
    OrchestrationInput,
    PlanTurnInput,
    SummarizeContextInput,
)


@workflow.defn(name="OrchestrationWorkflow")
class OrchestrationWorkflow:
    """
    One Workflow per context_id (conversation thread).
    Each user turn arrives via start_workflow or signal_with_start.
    """

    def __init__(self) -> None:
        self.messages: list[dict] = []
        self.context_summary: Optional[str] = None
        self.tokens_used: int = 0
        self.iteration: int = 0
        self._tokens_carry: int = 0
        self._iteration_carry: int = 0
        # Signal state
        self._human_response: Optional[dict] = None
        self._cancel_requested: bool = False

    @workflow.signal
    def submit_human_response(self, payload: dict) -> None:
        self._human_response = payload

    @workflow.query
    def get_status(self) -> dict:
        return {
            "iteration": self.iteration,
            "tokens_used": self.tokens_used,
            "messages_count": len(self.messages),
            "has_human_response": self._human_response is not None,
        }

    @workflow.run
    async def run(self, inp: OrchestrationInput) -> dict:
        """
        Main orchestration loop. Returns {run_id, task_id, status, iterations}.
        """
        run_id: Optional[str] = None
        root_task_id: Optional[str] = None
        final_answer: Optional[str] = None
        run_status = "completed"
        run_error: Optional[str] = None
        total_in = 0
        total_out = 0
        total_cost = Decimal("0")
        msg_seq = 1

        # Restore counters carried across continue_as_new (0 on a fresh start)
        self.tokens_used = inp.tokens_used_carry
        self.iteration = inp.iteration_carry

        try:
            # ── 1. Load orchestration context (agents, tools, prior history) ──
            ctx_result = await workflow.execute_activity(
                load_orchestration_context_activity,
                args=[
                    inp.orchestrator_name,
                    inp.user_id,
                    inp.token_payload,
                    inp.context_id,
                    # current_task_id not yet known; pass placeholder — loader
                    # uses it only to exclude current task from history
                    "00000000-0000-0000-0000-000000000000",
                    inp.history_window,
                ],
                schedule_to_close_timeout=timedelta(seconds=30),
                retry_policy=RetryPolicy(maximum_attempts=3),
            )

            orch_dict = ctx_result["orch"]
            agent_dicts = ctx_result["agents"]
            tools = ctx_result["tools"]
            prior_history = ctx_result["prior_history"]
            orch_config = _dict_to_orch_config(orch_dict)

            # Seed message state: prior turns + current user message
            self.messages = list(prior_history)
            user_msg = _build_user_message(inp.user_message)
            self.messages.append(user_msg)

            # Build agent lookup by slug
            agents_by_slug = {a["slug"]: a for a in agent_dicts}

            # ── 2. Create run + root task rows ─────────────────────────────
            generated_run_id = str(workflow.uuid4())
            generated_root_task_id = str(workflow.uuid4())

            init_result = await workflow.execute_activity(
                init_run_activity,
                args=[
                    inp.orchestrator_name,
                    orch_config["id"],
                    inp.user_message,
                    inp.user_id,
                    inp.session_id,
                    inp.context_id,
                    generated_run_id,
                    generated_root_task_id,
                    orch_config.get("budget_tokens"),
                ],
                schedule_to_close_timeout=timedelta(seconds=30),
                retry_policy=RetryPolicy(maximum_attempts=3),
            )

            run_id = init_result["run_id"]
            root_task_id = init_result["root_task_id"]

            # ── 3. Agentic loop ────────────────────────────────────────────
            max_iterations = orch_config.get("max_iterations", 10)
            budget_tokens = orch_config.get("budget_tokens")
            max_parallel = orch_config.get("max_parallel_tools", 4)
            memory_enabled = orch_config.get("memory_enabled", False)
            summarize_threshold = orch_config.get("summarize_every_n_calls", 3)
            agent_calls_since_summary = 0
            parallel_sem = asyncio.Semaphore(max_parallel)

            while self.iteration < max_iterations:
                self.iteration += 1

                # Budget check
                if budget_tokens is not None and self.tokens_used >= budget_tokens:
                    run_error = f"Budget exceeded: {self.tokens_used} tokens (limit: {budget_tokens})"
                    run_status = "failed"
                    break

                # Plan turn
                plan_input = PlanTurnInput(
                    run_id=run_id,
                    context_id=inp.context_id,
                    root_task_id=root_task_id,
                    orchestrator_name=inp.orchestrator_name,
                    system_prompt=orch_config.get("system_prompt", ""),
                    provider_name=orch_config.get("llm_provider", "anthropic"),
                    model=orch_config.get("llm_model", ""),
                    api_key_encrypted=orch_config.get("llm_api_key_encrypted"),
                    base_url=orch_config.get("llm_base_url"),
                    messages=serialize_messages(self.messages),
                    tools=tools,
                    max_tokens=2048,
                    msg_seq=msg_seq,
                    price_in=orch_config.get("price_in", "0"),
                    price_out=orch_config.get("price_out", "0"),
                    user_id=inp.user_id,
                    llm_provider=orch_config.get("llm_provider", "anthropic"),
                    budget_tokens=budget_tokens,
                    tokens_used_so_far=self.tokens_used,
                    iteration=self.iteration,
                )

                plan_result = await workflow.execute_activity(
                    plan_turn_activity,
                    plan_input,
                    schedule_to_close_timeout=timedelta(minutes=5),
                    heartbeat_timeout=timedelta(seconds=90),
                    retry_policy=RetryPolicy(maximum_attempts=3),
                )

                total_in += plan_result.input_tokens
                total_out += plan_result.output_tokens
                self.tokens_used += plan_result.input_tokens + plan_result.output_tokens
                msg_seq = plan_result.msg_seq_after

                # Append assistant turn to Workflow message state
                if plan_result.serialized_assistant_turn is not None:
                    import json as _json
                    decoded_turn = _json.loads(plan_result.serialized_assistant_turn)
                    assistant_msg = _build_assistant_message(
                        decoded_turn,
                        plan_result.tool_calls,
                    )
                    self.messages.append(assistant_msg)

                # No tool calls → final answer
                if not plan_result.tool_calls:
                    final_answer = plan_result.final_answer
                    break

                # Fan out agent invocations in parallel (bounded by max_parallel_tools)
                async def _invoke_one(tc_dict: dict, agent_dict: dict) -> InvokeAgentResult:
                    slug = tc_dict["name"].removeprefix("agent__")
                    if agent_dict is None:
                        return InvokeAgentResult(
                            status="failed",
                            result_text=f"[Unknown agent: {slug}]",
                            file_parts=[],
                            latency_ms=0,
                            error=f"Unknown agent: {slug}",
                        )
                    invoke_inp = InvokeAgentInput(
                        run_id=run_id,
                        context_id=inp.context_id,
                        root_task_id=root_task_id,
                        iteration=self.iteration,
                        agent_id=agent_dict["id"],
                        agent_slug=agent_dict["slug"],
                        agent_name=agent_dict["name"],
                        transport=agent_dict["transport"],
                        endpoint_url=agent_dict.get("endpoint_url"),
                        auth_token_encrypted=agent_dict.get("auth_token_encrypted"),
                        timeout_seconds=agent_dict.get("timeout_seconds", 30),
                        tool_call_id=tc_dict["id"],
                        tool_call_name=tc_dict["name"],
                        tool_input=tc_dict["input"],
                        injected_context=self.context_summary,
                        input_schema=agent_dict.get("input_schema"),
                        max_retries=max(1, int(agent_dict.get("max_retries", 2) or 2)),
                    )
                    try:
                        async with parallel_sem:
                            agent_timeout = int(agent_dict.get("timeout_seconds", 30))
                            return await workflow.execute_activity(
                                invoke_agent_activity,
                                invoke_inp,
                                schedule_to_close_timeout=timedelta(seconds=agent_timeout + 60),
                                heartbeat_timeout=timedelta(seconds=agent_timeout + 30),
                                retry_policy=RetryPolicy(maximum_attempts=invoke_inp.max_retries),
                            )
                    except (ActivityError, Exception) as exc:
                        cause_str = str(exc.cause) if hasattr(exc, "cause") and exc.cause else str(exc)
                        return InvokeAgentResult(
                            status="failed",
                            result_text=f"[Agent {slug} failed: {cause_str}]",
                            file_parts=[],
                            latency_ms=0,
                            error=cause_str,
                        )

                invoke_coros = [
                    _invoke_one(
                        tc,
                        agents_by_slug.get(tc["name"].removeprefix("agent__")),
                    )
                    for tc in plan_result.tool_calls
                ]
                invoke_results: list[InvokeAgentResult] = list(await asyncio.gather(*invoke_coros))

                # Handle input-required: pause and wait for human signal
                input_required_results = [
                    (tc, res)
                    for tc, res in zip(plan_result.tool_calls, invoke_results)
                    if res.status == "input-required"
                ]
                if input_required_results:
                    # Wait up to 10 minutes for human response
                    self._human_response = None
                    await workflow.wait_condition(
                        lambda: self._human_response is not None,
                        timeout=timedelta(minutes=10),
                    )
                    if self._human_response is not None:
                        # Inject human response as the tool result for input-required slots
                        human_text = self._human_response.get("content", "")
                        for i, (_, result_res) in enumerate(zip(plan_result.tool_calls, invoke_results)):
                            if result_res.status == "input-required":
                                invoke_results[i] = InvokeAgentResult(
                                    status="completed",
                                    result_text=human_text,
                                    file_parts=[],
                                    latency_ms=result_res.latency_ms,
                                )
                        self._human_response = None
                    else:
                        # Timeout — treat as cancellation
                        run_status = "failed"
                        run_error = "Human response timeout (10 minutes)"
                        break

                # Append tool results to message state and persist to DB
                tool_result_msg = _build_tool_results_message(plan_result.tool_calls, invoke_results)
                self.messages.append(tool_result_msg)
                await workflow.execute_activity(
                    record_tool_results_activity,
                    RecordToolResultsInput(
                        root_task_id=root_task_id,
                        msg_seq=msg_seq,
                        tool_results=[
                            {"tool_use_id": tc["id"], "content": res.result_text}
                            for tc, res in zip(plan_result.tool_calls, invoke_results)
                        ],
                    ),
                    schedule_to_close_timeout=timedelta(seconds=30),
                    retry_policy=RetryPolicy(maximum_attempts=3),
                )
                msg_seq += 1

                agent_calls_since_summary += len(plan_result.tool_calls)

                # Optionally summarize context
                if memory_enabled and agent_calls_since_summary >= summarize_threshold:
                    sum_inp = SummarizeContextInput(
                        context_id=inp.context_id,
                        root_task_id=root_task_id,
                        orch_id=orch_config["id"],
                        memory_enabled=memory_enabled,
                        summarize_every_n_calls=summarize_threshold,
                        memory_raw_fallback_n=orch_config.get("memory_raw_fallback_n", 5),
                        summarizer_provider=orch_config.get("summarizer_provider"),
                        summarizer_model=orch_config.get("summarizer_model"),
                        summarizer_api_key_encrypted=orch_config.get("summarizer_api_key_encrypted"),
                        llm_provider=orch_config.get("llm_provider", "anthropic"),
                        llm_model=orch_config.get("llm_model", ""),
                        llm_api_key_encrypted=orch_config.get("llm_api_key_encrypted"),
                    )
                    try:
                        summary = await workflow.execute_activity(
                            summarize_context_activity,
                            sum_inp,
                            schedule_to_close_timeout=timedelta(seconds=60),
                            retry_policy=RetryPolicy(maximum_attempts=2),
                        )
                        if summary:
                            self.context_summary = summary
                    except Exception:
                        pass
                    agent_calls_since_summary = 0

                # continue_as_new threshold: prevent unbounded Event History
                total_messages = len(self.messages)
                if total_messages > 200 or self.iteration >= 50:
                    carry = OrchestrationInput(
                        orchestrator_name=inp.orchestrator_name,
                        user_message=inp.user_message,
                        user_id=inp.user_id,
                        token_payload=inp.token_payload,
                        session_id=inp.session_id,
                        context_id=inp.context_id,
                        tokens_used_carry=self.tokens_used,
                        iteration_carry=self.iteration,
                        history_window=inp.history_window,
                    )
                    workflow.continue_as_new(carry)

            else:
                run_status = "stopped"
                run_error = f"Reached max iterations ({max_iterations})"

        except CancelledError:
            run_status = "canceled"
            run_error = "Workflow cancelled"
        except ActivityError as exc:
            # If the root cause is a cancellation, treat as canceled (not failed)
            cause_str = str(exc.cause) if exc.cause else str(exc)
            if isinstance(exc.cause, CancelledError) or "cancel" in cause_str.lower():
                run_status = "canceled"
                run_error = "Workflow cancelled"
            else:
                run_status = "failed"
                run_error = cause_str
            workflow.logger.error(f"OrchestrationWorkflow: activity error: {cause_str}")
        except Exception as exc:
            run_status = "failed"
            run_error = str(exc)
            workflow.logger.error(f"OrchestrationWorkflow: unexpected error: {run_error}")

        # ── 4. Finalize — always runs (catches any remaining cancellation) ──
        if run_id is not None and root_task_id is not None:
            fin_inp = FinalizeRunInput(
                run_id=run_id,
                root_task_id=root_task_id,
                context_id=inp.context_id,
                orchestrator_name=inp.orchestrator_name,
                status=run_status,
                final_answer=final_answer,
                iterations=self.iteration,
                total_tokens_in=total_in,
                total_tokens_out=total_out,
                total_cost_usd=str(total_cost),
                error=run_error,
                user_id=inp.user_id,
            )
            await workflow.execute_activity(
                finalize_run_activity,
                fin_inp,
                schedule_to_close_timeout=timedelta(seconds=30),
                retry_policy=RetryPolicy(maximum_attempts=5),
            )

        return {
            "run_id": run_id or "",
            "root_task_id": root_task_id or "",
            "status": run_status,
            "iterations": self.iteration,
            "error": run_error,
        }


# ─────────────────────────────────────────────────────────────────────────────
# Message format helpers (provider-native — Anthropic dict format)
# ─────────────────────────────────────────────────────────────────────────────

def _build_user_message(text: str) -> dict:
    return {"role": "user", "content": text}


# Fields kept when a tool result is a JSON object — enough for routing, elides heavy text.
_COMPACT_JSON_KEEP = {"agent", "main_point", "confidence", "approach", "round",
                      "winner", "winner_reason", "scores", "final", "synthesized_answer",
                      "question", "field", "insight", "status"}
# Fields in tool_use inputs that contain large nested argument blobs
_HEAVY_INPUT_FIELDS = {"arguments", "opponent_arguments"}
_MAX_TOOL_RESULT_CHARS = 2000  # fallback for non-JSON results (~500 tokens)


def _compact_tool_result(text: str) -> str:
    """
    JSON-aware compaction: keep small routing fields, drop heavy argument bodies.
    Falls back to char truncation for non-JSON results.
    Full content is always in DB artifacts.
    """
    import json as _json
    stripped = text.strip()
    # Try single JSON object
    if stripped.startswith("{"):
        try:
            obj = _json.loads(stripped)
            compact = {k: v for k, v in obj.items() if k in _COMPACT_JSON_KEEP}
            if compact:
                compacted = _json.dumps(compact, ensure_ascii=False)
                dropped = [k for k in obj if k not in _COMPACT_JSON_KEEP]
                if dropped:
                    compacted += f'\n[fields elided from context: {", ".join(dropped)} — full result in artifacts]'
                return compacted
        except _json.JSONDecodeError:
            pass
    # Try JSON array of objects
    if stripped.startswith("["):
        try:
            arr = _json.loads(stripped)
            if isinstance(arr, list) and all(isinstance(x, dict) for x in arr):
                compacted_arr = [{k: v for k, v in x.items() if k in _COMPACT_JSON_KEEP} for x in arr]
                return _json.dumps(compacted_arr, ensure_ascii=False)
        except _json.JSONDecodeError:
            pass
    # Non-JSON fallback: truncate on whitespace boundary
    if len(text) > _MAX_TOOL_RESULT_CHARS:
        cut = text.rfind(" ", 0, _MAX_TOOL_RESULT_CHARS)
        cut = cut if cut > 0 else _MAX_TOOL_RESULT_CHARS
        return text[:cut] + "\n[truncated — full result in artifacts]"
    return text


def _slim_tool_use_inputs(content: list) -> list:
    """
    For the planner's own context copy, elide heavy nested arrays from tool_use inputs.
    The wire copy (sent to agents) is unaffected — this only touches self.messages.
    """
    import json as _json
    slimmed = []
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "tool_use":
            slimmed.append(block)
            continue
        inp = block.get("input", {})
        if not isinstance(inp, dict) or not any(k in inp for k in _HEAVY_INPUT_FIELDS):
            slimmed.append(block)
            continue
        slim_inp = {}
        for k, v in inp.items():
            if k in _HEAVY_INPUT_FIELDS and isinstance(v, list):
                # Keep only compact fields from each element
                slim_inp[k] = [
                    {fk: fv for fk, fv in elem.items() if fk in _COMPACT_JSON_KEEP}
                    if isinstance(elem, dict) else elem
                    for elem in v
                ]
            else:
                slim_inp[k] = v
        slimmed.append({**block, "input": slim_inp})
    return slimmed


def _build_assistant_message(serialized_turn, tool_calls: list[dict]) -> dict:
    """Reconstruct the assistant message dict, slimming heavy tool_use inputs for planner context."""
    if isinstance(serialized_turn, list):
        return {"role": "assistant", "content": _slim_tool_use_inputs(serialized_turn)}
    if isinstance(serialized_turn, str):
        return {"role": "assistant", "content": serialized_turn}
    return {"role": "assistant", "content": []}


def _build_tool_results_message(tool_calls: list[dict], results: list[InvokeAgentResult]) -> dict:
    """Build the user-role tool_result message using JSON-aware compaction.

    Full results are persisted in DB/artifacts. The planner only needs routing fields.
    """
    content = []
    for tc, res in zip(tool_calls, results):
        content.append({
            "type": "tool_result",
            "tool_use_id": tc["id"],
            "content": _compact_tool_result(res.result_text),
        })
    return {"role": "user", "content": content}


def _dict_to_orch_config(d: dict) -> dict:
    """Return the orch config dict as-is — used for attribute access."""
    return d
