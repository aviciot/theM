---
title: Lessons — A2A & Agents
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-07-05 — A2A SDK v1.1 broke AgentCard.url and added executor contract

**Symptom:** A2A agent containers crashed at startup with `AttributeError: Protocol message AgentCard has no "url" field`.
**Root cause:** SDK v1.1 removed `AgentCard.url`. The URL now lives in `card.supported_interfaces.add().url`.
**Fix:** Replace `card.url = "http://..."` with:
```python
iface = card.supported_interfaces.add()
iface.url = "http://..."
```
**Watch for:** Any agent built against SDK <1.1 will break on upgrade. Always check the AgentCard proto fields after an SDK upgrade.

---

---

## 2026-07-05 — A2A SDK v1.1 executor must enqueue Task object before TaskStatusUpdateEvent

**Symptom:** SDK raised `INVALID_AGENT_RESPONSE`: "Agent should enqueue Task before TaskStatusUpdateEvent" after the executor called `enqueue_event(TaskStatusUpdateEvent(...))` first.
**Root cause:** SDK v1.1 `DefaultRequestHandler` enforces ordering: the executor must enqueue a `Task` (with at minimum `id`, `context_id`, and `status.state=TASK_STATE_SUBMITTED`) before any `TaskStatusUpdateEvent`. The contract is documented in the `AgentExecutor.execute()` docstring.
**Fix:** Add this block at the very start of every executor's `execute()` method:
```python
task = Task()
task.id = context.task_id
task.context_id = context.context_id
task.status.state = TaskState.TASK_STATE_SUBMITTED
await event_queue.enqueue_event(task)
```
**Watch for:** Every new `AgentExecutor` subclass needs this. Forgetting it gives a misleading "invalid response" error at the SDK handler level, not in your code.

---

---

## 2026-07-05 — A2A v1.0 wire protocol: role is int, part has no "kind", method is "SendMessage"

**Symptom:** Live test of `A2aAsyncAdapter` got three successive RPC errors when calling SDK v1.1 agents:
  - `Invalid enum value user` (role field)
  - `Message type has no field named kind` (part format)
  - `Method not found (-32601)` (method name)
  - `configuration has no field named blocking` (configuration field)
  - `A2A version '0.3' is not supported` (missing version header)

**Root cause:** Our adapter was written against an earlier spec. The A2A v1.0 proto enforces:
  - `role` is an integer enum (`ROLE_USER=1`), not a string `"user"`
  - `Part` uses oneof field names directly (`{"text": "..."}`) — no outer `"kind"` wrapper
  - JSON-RPC method name is `"SendMessage"` (CamelCase), not `"message/send"`
  - Non-blocking submit uses `configuration.returnImmediately: true`, not `blocking: false`
  - `A2A-Version: 1.0` header required — missing header triggers version validation failure (defaults to `0.3`)
  - Terminal state strings from SDK v1.1 are `TASK_STATE_COMPLETED` etc. (proto enum names), not lowercase `"completed"`

**Fix:** Updated `app/adapters/a2a_async_adapter.py`:
  - `_ROLE_USER = 1`, used directly as integer in message body
  - Part: `{"text": message}` (no `"kind"` key)
  - Method: `"SendMessage"`
  - Config: `{"returnImmediately": True}`
  - Headers: added `"A2A-Version": "1.0"`
  - `_TERMINAL` / `_INPUT_REQUIRED` sets include both `TASK_STATE_*` names and lowercase variants
  - `submit()` extracts `result.task.id` (SDK wraps result in `SendMessageResponse`)

**Watch for:** If the A2A spec or SDK changes the proto field names, state enum names, or method names — all three must stay in sync: agent executors, platform adapter, and `_TERMINAL`/`_INPUT_REQUIRED` sets. Verify with a live `stream_invoke` call against a real container, not just structural tests.

---

---

## 2026-07-09 — A2A typed input: `Part.data` vs string concatenation

**Symptom:** `docu_writer` received `FORMAT: html\nTITLE: ...\nCONTENT:\n...` as plain text and parsed it with regex — brittle, breaks if LLM varies the format, and requires agent-specific instructions in the orchestrator system prompt.

**Root cause:** The adapter always sent `{"parts": [{"text": message}]}`. The agent card had no `input_modes` declaration. The LLM had no way to know the agent expected structured input.

**Fix:**
1. Declare `input_modes: ["application/json"]` on agent skills so the LLM and adapter know the contract.
2. `_build_parts()` in the adapter sends `{"data": input_dict}` when `application/json` is declared.
3. Agent reads `part.HasField("data")` + `json_format.MessageToDict(part.data.struct_value)` — no regex.
4. `_ensure_agent_skills` stores `input_modes`/`output_modes` from the fetched card so the factory can wire them.
5. Orchestrator system prompt only describes the goal — never agent-specific format strings.

**Watch for:** Any new A2A agent that expects structured fields: declare `input_modes: ["application/json"]` on its skills. Never encode format contracts in the orchestrator system prompt — the agent card is the sole contract.

---

---

## 2026-09-02 — A2A playground requires double-click to send first message

**Symptom:** In the playground, typing a message and clicking Send enters a sending state but nothing happens. Must click Stop then Send again.

**Root cause:** The `for await` SSE loop in `useChatConnection.ts` only cleared `busy=false` inside `completed`/`failed` status event branches. If the stream closed without emitting either event (connection race on the very first request), `busy` stayed `true` permanently.

**Fix (commit 7a792c4):** Changed `try/catch` to `try/catch/finally`. `setBusy(false); busyRef.current = false` moved to the `finally` block so it always runs when the stream ends regardless of whether a terminal status event was received.

---
