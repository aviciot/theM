# Odin Architecture
# Last updated: 2026-07-03

## Core Mental Model

Each enabled `odin.agents` row = ONE LLM tool named `agent__<slug>`.
The agent's `description` is the tool description — the LLM uses it to decide when to call this agent.

The orchestrator engine is Omni's `llm_service.py` agentic loop with one change:
MCP tool execution → agent adapter invocation.

## Entry Points

| Path | Method | Auth | Purpose |
|---|---|---|---|
| `/ws/orchestrate/{name}` | WebSocket | Bearer token OR admin JWT | Main orchestrator endpoint |
| `/ws/dashboard` | WebSocket | JWT | Multiplexed dashboard events (named channels) |
| `/api/v1/admin/agents` | REST | JWT (admin) | Agent registry CRUD |
| `/api/v1/admin/orchestrators` | REST | JWT (admin) | Orchestrator config CRUD |
| `/api/v1/admin/orchestrators/{id}/test-llm` | POST | JWT (admin) | Validate LLM API key |
| `/api/v1/admin/tokens` | REST | JWT (admin) | Access token management |
| `/api/v1/runs` | REST | JWT | Run history |
| `/health`, `/health/ready`, `/health/live` | GET | None | Health checks |

## Auth Flow (Frontend → Bridge)

```
Browser
  └─ POST /api/auth/login  (Next.js route handler)
       └─ proxies to auth-service → gets JWT
       └─ sets httpOnly cookies: odin_access_token, odin_refresh_token
  └─ GET /api/auth/me  (Next.js route handler)
       └─ reads httpOnly cookie → proxies to auth-service /me
       └─ returns {id, email, name, role} to browser JS
  └─ GET /api/odin/[...path]  (Next.js route handler)
       └─ reads httpOnly cookie server-side
       └─ adds Authorization: Bearer header
       └─ proxies to odin-bridge

WebSocket connections (can't use httpOnly cookies):
  └─ Browser fetches GET /api/auth/token → returns raw JWT as JSON (playground only)
  └─ Opens ws://bridge:8001/ws/orchestrate/{name}?token=<jwt>
  └─ Opens ws://bridge:8001/ws/dashboard?token=<jwt>
```

**Security note:** `/api/auth/token` returns the raw JWT to JS — acceptable only for the admin playground where the token is used transiently for WS connection and never stored.

## Orchestrator Lifecycle

1. Client connects to `/ws/orchestrate/{name}`
2. Auth: opaque access token → L1 cache → L2 Redis → DB; OR admin JWT (for playground)
3. Orchestrator config loaded from Redis `odin:orchestrators:{name}` (600s TTL, DB fallback)
4. Agent list built: `SELECT * FROM odin.agents WHERE id = ANY(allowed_agent_ids) AND enabled`
   (empty `allowed_agent_ids` = all enabled agents)
5. Each agent → `NeutralTool(name=f"agent__{slug}", description=agent.description, schema=agent.input_schema)`
6. LLM agentic loop starts (≤ `max_iterations`):
   a. LLM receives tools list + system prompt + user message
   b. LLM may emit one or more ToolCalls in a single iteration
   c. Parallel execution: `asyncio.gather()` over all ToolCalls in iteration,
      bounded by `orchestrator.max_parallel_tools` + per-agent `asyncio.Semaphore(max_concurrency)`
   d. Each ToolCall → `factory.get_adapter(agent)` → `adapter.stream_invoke(input)` → collect result
   e. **Redis publish:** every event (iteration_start, tool_start, tool_done, usage, run_end) is published
      to `odin:dash:run:{run_id}` (full) and `odin:dash:runs` (summary)
   f. Results fed back to LLM as tool_results
   g. LLM continues or emits final answer
7. Each LLM call → `run_recorder.record_usage()` → `odin.run_usage`
8. Each agent call → `run_recorder.record_step()` → `odin.run_steps`
9. On completion → `run_recorder.complete_run()` → `odin.runs` status=completed

## Dashboard WebSocket — Channel Multiplexing

`/ws/dashboard` is a single persistent WS connection that fans out multiple Redis pub/sub channels.

**Protocol:**
```
Client → Server:  {"type": "subscribe", "channels": ["runs", "run:abc-uuid"]}
Server → Client:  {"type": "subscribed", "channels": [...]}
Server → Client:  {"channel": "run:abc-uuid", "event": {...}}
Server → Client:  {"type": "ping"}   — every 30s keepalive
```

**Static channels:** `runs`, `agents`, `metrics`
**Dynamic channels:** `run:{uuid}` — subscribes to a specific run's trace events

Redis key mapping: channel `run:abc` → pub/sub channel `odin:dash:run:abc`

## Playground Architecture

```
Browser Playground
  ├─ Left pane: chat
  │    └─ WebSocket → /ws/orchestrate/{name}?token=<jwt>
  │         streams: token, tool_start, tool_done, done, error
  └─ Right pane: trace
       └─ On "ready" event (contains run_id):
            WebSocket → /ws/dashboard?token=<jwt>
            subscribe: ["run:{run_id}"]
            receives: run_start, iteration_start, tool_start, tool_done, usage, run_end
```

The trace pane shows the orchestrator's internal reasoning in real time via Redis pub/sub — completely separate from the user-facing token stream.

## Redis Pub/Sub — Run Trace Events

Published by `orchestrator_service._publish_run_event()` to two channels per event:
- `odin:dash:run:{run_id}` — full event including tool inputs/outputs
- `odin:dash:runs` — summary (no `input` field) for global dashboard widgets

Event types:
| type | Fields | When |
|---|---|---|
| `run_start` | orchestrator, goal | Before first LLM call |
| `iteration_start` | iteration | Start of each LLM call |
| `tool_start` | tool, input, iteration | Before adapter invocation |
| `tool_done` | tool, output, iteration | After adapter completes |
| `usage` | iteration, input_tokens, output_tokens | After each LLM call |
| `run_end` | status, iterations, total_tokens_in, total_tokens_out, error | Run complete |
| `error` | message | On any fatal error |

## Adapter Abstraction

```
AgentAdapter (base.py)
  └── stream_invoke(input: dict) → AsyncGenerator[AdapterEvent, None]

AdapterEvent
  type: "token" | "done" | "error"
  text: str | None       # for token events
  result: str | None     # for done events (full agent response)
  error: str | None      # for error events

OmniWsAdapter (omni_ws_adapter.py)
  - Connects to agent via WebSocket + Bearer token
  - Sends: {"type": "message", "content": input["message"]}
  - Parses WS stream events → AdapterEvent

A2aAdapter (a2a_adapter.py)  ← STUB, raises NotImplementedError

MockAgent (mock_agent/agent.py)
  - Standalone Python WS server (websockets>=12)
  - Reads AGENT_NAME, AGENT_PERSONA, AGENT_DELAY, PORT, AUTH_TOKEN env vars
  - Streams word-by-word reply then sends {"type":"done"}
  - Used for dev/testing only — three instances in docker-compose
  - IMPORTANT: no volume mount, requires `docker compose build` to pick up code changes
```

## Multi-Replica Scalability

| State | File | Replica-safe? | Mechanism |
|---|---|---|---|
| Token cache L1 | token_cache.py | No | in-process dict, independent per replica |
| Token cache L2 | token_cache.py | Yes | Redis `odin:session:token:*` TTL 300s |
| Rate limiting | rate_limiter.py | Yes | Redis INCR fixed-window |
| Agent registry cache | agent_registry.py | Yes | Redis `odin:agents:registry`, pub/sub invalidation |
| Orchestrator config | orchestrator_service.py | Yes | Redis `odin:orchestrators:{name}` TTL 600s |
| Run state | run_recorder.py | Yes | Postgres `odin.runs` |
| WS connections | ws_connection_manager.py | No (by design) | Traefik sticky sessions `odin_sticky` |
| Replica heartbeat | main.py bg task | Yes | Redis `odin:bridge:{ID}:heartbeat` 30s TTL |

## Background Tasks

- `agent_registry_refresh_loop` — every 600s, re-loads agents from DB, publishes `odin:agents:changed`
- `heartbeat_loop` — every 10s, writes `odin:bridge:{INSTANCE_ID}:heartbeat`
- `config_change_listener` — xreads `odin:control:events` for cache invalidation signals
