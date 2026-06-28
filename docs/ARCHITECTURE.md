# Odin Architecture
# Last updated: 2026-06-28

## Core Mental Model

Each enabled `odin.agents` row = ONE LLM tool named `agent__<slug>`.
The agent's `description` is the tool description — the LLM uses it to decide when to call this agent.

The orchestrator engine is Omni's `llm_service.py` agentic loop with one change:
MCP tool execution → agent adapter invocation.

## Entry Points

| Path | Method | Auth | Purpose |
|---|---|---|---|
| `/ws/orchestrate/{name}` | WebSocket | Bearer token (odin.access_tokens) | Main user-facing orchestrator endpoint |
| `/ws/dashboard` | WebSocket | JWT | Multiplexed dashboard events (named channels) |
| `/api/v1/admin/agents` | REST | JWT (admin) | Agent registry CRUD |
| `/api/v1/admin/orchestrators` | REST | JWT (admin) | Orchestrator config CRUD |
| `/api/v1/admin/tokens` | REST | JWT (admin) | Access token management |
| `/api/v1/runs` | REST | JWT | Run history |
| `/health`, `/health/ready`, `/health/live` | GET | None | Health checks |

## Orchestrator Lifecycle

1. Client connects to `/ws/orchestrate/{name}`
2. Bearer token validated: L1 in-process cache → L2 Redis `odin:session:token:{hash}` → DB lookup
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
   e. Results fed back to LLM as tool_results
   f. LLM continues or emits final answer
7. Each LLM call → `run_recorder.record_usage()` → `odin.run_usage`
8. Each agent call → `run_recorder.record_step()` → `odin.run_steps`
9. On completion → `run_recorder.complete_run()` → `odin.runs` status=completed

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
  - Connects to Omni agentic gateway via WebSocket + Bearer token
  - Decrypts auth_token_encrypted via crypto.decrypt_value()
  - Sends: {"type": "message", "content": input["message"]}
  - Parses Omni WS stream events → AdapterEvent

A2aAdapter (a2a_adapter.py)  ← STUB, raises NotImplementedError
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

## Background Tasks (planned, Phase 5+)

- `agent_registry_refresh_loop` — every 600s, re-loads agents from DB, publishes `odin:agents:changed`
- `heartbeat_loop` — every 10s, writes `odin:bridge:{INSTANCE_ID}:heartbeat`
- `config_change_listener` — xreads `odin:control:events` for cache invalidation signals
