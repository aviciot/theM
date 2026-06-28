# Agent Adapters
# Last updated: 2026-06-28

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

## A2aAdapter (app/adapters/a2a_adapter.py) — STUB

Raises `NotImplementedError`. Do not create agents with `transport='a2a'` in production.
A2A (Google Agent-to-Agent protocol) implementation is planned for a future phase.

## Adding a New Transport

1. Create `app/adapters/{name}_adapter.py` implementing `AgentAdapter`
2. Add `transport IN (...)` to the DB CHECK constraint in `db/001_schema.sql`
3. Register in `app/adapters/factory.py` `get_adapter()` switch
4. Add the transport name to `odin.agents.transport` CHECK constraint
5. Document the protocol here in this file
6. Add a test in `tests/test_{name}_adapter.py`
