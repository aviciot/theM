# Agent Adapters
# Last updated: 2026-07-04

## AgentAdapter ABC (app/adapters/base.py)

```python
class AdapterEvent:
    type: str          # "token" | "done" | "error"
    text: str | None   # streaming token text
    result: str | None # full assembled result (on "done")
    error: str | None  # error message (on "error")

class AgentAdapter(ABC):
    @abstractmethod
    async def stream_invoke(
        self, input: dict, timeout: float
    ) -> AsyncGenerator[AdapterEvent, None]: ...
```

The orchestrator collects all `token` events for streaming to the user,
then uses the `done.result` as the tool result fed back to the LLM.

## OmniWsAdapter (app/adapters/omni_ws_adapter.py)

Connects to an Omni agentic gateway WebSocket endpoint.

**Protocol:**
1. `websockets.connect(endpoint_url, extra_headers={"Authorization": f"Bearer {token)"})`
2. Send: `{"type": "message", "content": input["message"]}`
3. Parse incoming JSON events from Omni's WS stream:
   - `{"type": "token", "text": "..."}` → emit `AdapterEvent(type="token", text=...)`
   - `{"type": "done"}` → emit `AdapterEvent(type="done", result=assembled_text)`
   - `{"type": "error", "message": "..."}` → emit `AdapterEvent(type="error", error=...)`
4. Decrypts `agent.auth_token_encrypted` via `crypto.decrypt_value()` at connect time
5. Honours `agent.timeout_seconds` via `asyncio.wait_for()`

## A2aAdapter (app/adapters/a2a_adapter.py)

**Status: implemented.** Calls a generic A2A v1.0 JSON-RPC 2.0 HTTP endpoint.

**Protocol:**
1. `POST {endpoint_url}` with JSON-RPC 2.0 `SendMessage` method
2. Message body: `{"parts": [{"text": input["message"]}]}`
3. Auth: `Authorization: Bearer <decrypted token>` header
4. If the returned task state is not yet terminal (`WORKING`, `SUBMITTED`, or any other non-terminal state), polls via `GetTask` — up to 30s (60 polls × 0.5s interval)
5. Terminal states: `TASK_STATE_COMPLETED`, `TASK_STATE_FAILED`, `TASK_STATE_CANCELED`, `TASK_STATE_REJECTED`
6. On `TASK_STATE_COMPLETED`: streams result word-by-word as `token` events, then emits `done`
7. On `TASK_STATE_FAILED` or `TASK_STATE_REJECTED`: emits `error` event

**Limitation:** no native streaming — the full result is collected first, then re-streamed word-by-word as `token` events.

**To add an A2A agent in the-M:**
- `transport`: `a2a`
- `endpoint_url`: the A2A v1.0 endpoint URL (e.g. `http://<host>:<port>/`)
- `auth_token_encrypted`: bearer token encrypted via `crypto.encrypt_value()`

## Adding a New Transport

1. Create `app/adapters/{name}_adapter.py` implementing `AgentAdapter`
2. Add `transport IN (...)` to the DB CHECK constraint in `db/001_schema.sql`
3. Register in `app/adapters/factory.py` `get_adapter()` switch
4. Add the transport name to `them.agents.transport` CHECK constraint
5. Document the protocol here in this file
6. Add a test in `tests/test_{name}_adapter.py`
