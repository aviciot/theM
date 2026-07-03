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

## A2aAdapter (app/adapters/a2a_adapter.py)

Calls an A2A v1.0 JSON-RPC 2.0 HTTP endpoint. Compatible with Omni's `/a2a/{gateway_name}/` router.

**Protocol:**
1. `POST {endpoint_url}` with JSON-RPC 2.0 `SendMessage` method
2. Message body: `{"parts": [{"text": input["message"]}]}`
3. Auth: `Authorization: Bearer <decrypted token>` header
4. Omni returns a completed Task synchronously — result in `task.artifacts[0].parts[0].text`
5. If task state is `WORKING`/`SUBMITTED`, polls via `GetTask` (up to 30s, 0.5s interval)
6. Streams result word-by-word as `token` events, then emits `done`

**Agent card discovery:** `GET {base_url}/a2a/{gateway_name}/.well-known/agent-card.json`
Requires `Authorization` header — returns skills list, supported interfaces, security schemes.

**To add an A2A agent in Odin:**
- `transport`: `a2a`
- `endpoint_url`: `http://<host>/a2a/<gateway_name>/`
- `auth_token_encrypted`: bearer token encrypted via `crypto.encrypt_value()`

## Adding a New Transport

1. Create `app/adapters/{name}_adapter.py` implementing `AgentAdapter`
2. Add `transport IN (...)` to the DB CHECK constraint in `db/001_schema.sql`
3. Register in `app/adapters/factory.py` `get_adapter()` switch
4. Add the transport name to `odin.agents.transport` CHECK constraint
5. Document the protocol here in this file
6. Add a test in `tests/test_{name}_adapter.py`
