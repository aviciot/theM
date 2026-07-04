# Agent Adapters
# Last updated: 2026-07-04

## AdapterEvent (app/adapters/base.py)

```python
@dataclass
class AdapterEvent:
    type: str                        # see event types below
    text: Optional[str] = None       # streaming token text (on "token")
    result: Optional[str] = None     # assembled result text (on "done")
    error: Optional[str] = None      # error message (on "error")
    remote_task_id: Optional[str] = None  # A2A task id (on "task_created")
    state: Optional[str] = None      # task state (on "status")
    artifact: Optional[dict] = None  # full A2A artifact dict with parts (on "artifact")
    input_required: bool = False     # True when remote task is in "input-required" state
```

**Event types:**

| type | When yielded | Key fields |
|---|---|---|
| `token` | Each streaming chunk | `text` |
| `done` | Task completed | `result` (assembled text) |
| `error` | Task failed / network error | `error` |
| `task_created` | Remote task submitted (non-blocking) | `remote_task_id` |
| `status` | Remote task state changed | `state`, `input_required` |
| `artifact` | Remote agent produced an artifact | `artifact` (full parts array) |

## AgentAdapter ABC

```python
class AgentAdapter(ABC):
    @abstractmethod
    async def stream_invoke(
        self, input: dict, timeout: float
    ) -> AsyncGenerator[AdapterEvent, None]: ...
```

All transports implement `stream_invoke()`. The orchestrator loop:
- Collects `token` events for streaming to the user
- Uses `done.result` as the tool result fed back to the LLM
- Stores full `artifact.artifact` (all parts) in `them.artifacts`

---

## OmniWsAdapter (transport: `omni_ws`)

Connects to an Omni agentic gateway WebSocket endpoint.

**Protocol:**
1. `websockets.connect(endpoint_url, extra_headers={"Authorization": f"Bearer {token}"})`
2. Send: `{"type": "message", "content": input["message"]}`
3. Parse incoming JSON events:
   - `{"type": "token", "text": "..."}` → `AdapterEvent(type="token", text=...)`
   - `{"type": "done"}` → `AdapterEvent(type="done", result=assembled_text)`
   - `{"type": "error", "message": "..."}` → `AdapterEvent(type="error", error=...)`
4. Decrypts `agent.auth_token_encrypted` at connect time
5. Honours `agent.timeout_seconds`

---

## A2aAdapter (transport: `a2a`)

Synchronous A2A v1.0 JSON-RPC 2.0 over HTTP. Use for agents that complete quickly.

**Protocol:**
1. `POST {endpoint_url}` with JSON-RPC 2.0 `SendMessage` (method name is `"SendMessage"`, CamelCase)
2. Message: `{"role": 1, "parts": [{"text": input["message"]}], "messageId": "<uuid>"}` — `role` is proto int `ROLE_USER=1`, Part has no `"kind"` key
3. Headers: `A2A-Version: 1.0`, `Authorization: Bearer <decrypted token>`
4. Config: `{"returnImmediately": True}` (not `blocking`)
5. If task is not terminal: polls via `GetTask` up to 30s (60 × 0.5s)
6. Terminal states: `TASK_STATE_COMPLETED`, `TASK_STATE_FAILED`, `TASK_STATE_CANCELED`, `TASK_STATE_REJECTED` (SDK v1.1 proto enum names)
7. On completed: streams result word-by-word as `token` events, then `done`
8. On failure: `error` event

**Passes** `contextId` in message when `context_id` is set (enables A2A context threading).

**Limitation:** No streaming — result assembled first, then re-streamed word-by-word.

**Use for:** Fast agents, Omni A2A gateway, agents that complete synchronously.

---

## A2aAsyncAdapter (transport: `a2a_async`)

**Phase 4.** Non-blocking A2A for long-running agents. Submits task, polls or streams.

**Protocol:**
1. `POST {endpoint_url}` with JSON-RPC 2.0 `SendMessage`
2. Returns `remote_task_id` immediately → yields `task_created` event
3. If `supports_streaming=True`: subscribes to SSE stream at `GET {endpoint}/tasks/{id}/events`
4. Otherwise: polls `GetTask` every `poll_interval` (default 1s) up to `max_poll_seconds` (default 300s)
5. Yields `status` events on state changes
6. Yields `artifact` events with full parts array as each artifact arrives
7. On `completed`: assembles all text parts → yields `done`
8. On failure/timeout: yields `error`

**Push notifications:** If `push_url` is set, includes it in `SendMessage` params so the remote
agent can POST state changes to `POST /a2a/push/{task_id}` on the-M instead of being polled.

**Preserves full artifact parts array** — no "first text part wins" truncation.

**SSE fallback:** If the SSE stream returns a non-200, falls back to polling automatically.

**Use for:** Slow agents (10s–5min), agents with streaming capability, deadline-governed tasks.

### Constructor

```python
A2aAsyncAdapter(
    agent_slug="...",
    endpoint_url="http://agent-host/",
    auth_token_encrypted=None,
    context_id=None,
    push_url=None,
    supports_streaming=False,
    poll_interval=1.0,
    max_poll_seconds=300.0,
)
```

---

## Adding a New Transport

1. Create `app/adapters/{name}_adapter.py` implementing `AgentAdapter`
2. Add transport name to the `CHECK` constraint in `db/001_schema.sql`
3. Register in `app/adapters/factory.py` `get_adapter()`
4. Document here in ADAPTERS.md
5. Add a test in `scripts/tests/test_07_adapter_factory.py`
