# the-M Architecture
# Last updated: 2026-07-06

## Core Mental Model

Each enabled `them.agents` row = ONE LLM tool named `agent__<slug>`.
The agent's `description` is the tool description — the LLM uses it to decide when to call this agent.

The platform runs two orchestration paths in parallel:
- **Legacy path** (`orchestrator_service.py`): in-RAM loop, run/step recorded to Postgres
- **A2A-native path** (`task_runner.py`): durable task graph, tasks/artifacts stored in Postgres; the default for new deployments

## Network Topology (Traefik)

All external traffic enters on a single port (default **8088**) via `them-traefik` (Traefik v3.6).
The frontend and bridge are never exposed directly.

```
Browser → :8088 (them-traefik)
  PathPrefix(/api/v1)  → them-bridge-svc  (priority 100, sticky cookie them_lb)
  PathPrefix(/ws)      → them-bridge-svc  (priority 100, sticky cookie them_lb)
  PathPrefix(/health)  → them-bridge-svc  (priority 90)
  PathPrefix(/)        → them-ui-svc      (priority 10)

Sticky session: Traefik injects Set-Cookie: them_lb=<server> on first request.
All subsequent requests from the same browser hit the same bridge replica — required for WS.
```

Traefik config: `traefik/traefik.yml` (static), Docker labels on services (dynamic).
Dashboard (read-only): `http://localhost:8089` (127.0.0.1 only).

**Local dev:** `docker-compose.local.yml` overrides router rules to `PathPrefix(...)` only (no `Host` constraint) so any IP/hostname works.

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
| `/api/v1/runs/{id}/tasks` | REST | JWT | Task graph for a run |
| `/api/v1/runs/{id}/artifacts` | REST | JWT | Artifacts for a run |
| `/api/v1/runs/context/{context_id}/artifacts` | REST | JWT | Context-scoped artifact query |
| `/a2a/push/{task_id}` | POST | Bearer | Push webhook for A2A agent callbacks |
| `/.well-known/agent-card.json` | GET | None | the-M's own A2A agent card |
| `/health`, `/health/ready`, `/health/live` | GET | None | Health checks |

## Auth Flow (Frontend → Bridge)

```
Browser
  └─ POST /api/auth/login  (Next.js route handler)
       └─ proxies to auth-service → gets JWT
       └─ sets httpOnly cookies: them_access_token, them_refresh_token
  └─ GET /api/auth/me  (Next.js route handler)
       └─ reads httpOnly cookie → proxies to auth-service /me
       └─ returns {id, email, name, role} to browser JS
  └─ GET /api/them/[...path]  (Next.js route handler)
       └─ reads httpOnly cookie server-side
       └─ adds Authorization: Bearer header
       └─ proxies to them-bridge (via Traefik)

WebSocket connections (can't use httpOnly cookies):
  └─ Browser fetches GET /api/auth/token → returns raw JWT as JSON (playground only)
       └─ auto-refreshes if token has < 30s left (uses them_refresh_token cookie)
  └─ Opens ws://<host>:8088/ws/orchestrate/{name}?token=<jwt>  ← derived from window.location
  └─ Opens ws://<host>:8088/ws/dashboard?token=<jwt>
```

**Security note:** `/api/auth/token` returns the raw JWT to JS — acceptable only for the admin playground where the token is used transiently for WS connection and never stored.

**WS URL derivation:** `NEXT_PUBLIC_BRIDGE_WS_URL` is set to `""` in docker-compose. The playground derives the WS base from `window.location.host` at runtime so it always uses the correct host/port regardless of environment — no hardcoded `:8001`.

## A2A-Native Orchestrator (task_runner.py) — Primary Path

The A2A-native path treats every orchestration run as a durable task graph.

### Flow

```
Client → WS /ws/orchestrate/{name}
  └─ ws_orchestrator.py: parse auth, load orchestrator config, create root Task in DB
       └─ task_runner.run(root_task, orchestrator, agents, ws)
            └─ Build tool list from agents (NeutralTool per agent)
            └─ LLM agentic loop (≤ max_iterations):
                 LLM call → zero or more ToolCalls
                 Per ToolCall → route via adapter → child task in DB
                 Parallel: asyncio.gather() bounded by max_parallel_tools + per-agent Semaphore
                 Artifacts stored via context_service.record_and_cache_artifact()
                 Budget check: tokens_used vs budget_tokens on each iteration
            └─ Final answer → artifact recorded in DB + streamed to WS
            └─ Root task transitioned to completed/failed
  └─ WS sends: ready, task_id, context_id, token, tool_start, tool_done, done, error
```

### Task State Machine

```
submitted → working → completed
                   ↘ failed
                   ↘ canceled
                   ↘ rejected (input-required received but not handled)
```

State transitions enforced by `task_store.transition()` — illegal transitions silently dropped.

### Durable Context (context_service.py)

- Every task carries a `context_id` that groups related tasks across sessions
- Artifacts stored in `them.artifacts`; the-M is the sole writer
- Redis hot cache `them:ctx:{context_id}:heads` (TTL 300s) for artifact lookup
- Cache-aside: miss → query Postgres; hit → return cached list
- `context_service.record_and_cache_artifact()` writes to DB then invalidates cache

### Budget Enforcement

- Root task created with `budget_tokens` (from orchestrator config) and `deadline`
- On each loop iteration: `tokens_used >= budget_tokens` → fail with "Budget exceeded"
- Reaper background task (runs every 60s) transitions tasks past `deadline` to `failed`

### Redis Events

Published by `task_store.transition()` and `task_store.record_artifact()`:
- `them:tasks:{task_id}:events` — every state change and artifact chunk (consumed by ws_orchestrator)
- `them:dash:run:{run_id}` — reformatted run trace events (consumed by ws_dashboard subscribers)

## Context Memory (memory_service.py) — Phase 8.4

When `orchestrator.memory_enabled = true`, the task_runner maintains a rolling summary of agent call results across iterations.

```
Every N agent calls (summarize_every_n_calls):
  └─ memory_service.summarize_context(context_id, orch, artifacts, root_task_id, db)
       └─ Fetches recent context artifacts
       └─ Calls summarizer LLM (default: anthropic/haiku, cheapest available)
       └─ Stores summary text in Redis: them:ctx:{context_id}:summary  TTL 3600s
       └─ Persists as artifact: name="summary-{timestamp}" in them.artifacts

On next agent call batch:
  └─ memory_service.get_injected_context(context_id) → reads Redis summary
  └─ Prepends "[Context summary]\n{summary}\n\n" to each agent tool call input
```

**Context threading:** The frontend passes `context_id` in the WS message payload on follow-up messages. The server reuses the same `context_id` instead of generating a fresh UUID — so the Redis summary from the previous turn is found and injected.

**Redis key:** `them:ctx:{context_id}:summary` — TTL 3600s. Written by memory_service, read by task_runner before each agent batch.

## Pluggable Edge Adapters (app/edges/) — Phase 8.6 / Phase 10

Edges are transport wrappers. They translate a client protocol into `EdgeRequest` and relay `task_runner` events back in that protocol's encoding. Zero business logic — same orchestrator and agents regardless of edge.

```
EdgeAdapter (base.py) — ABC
  name: str
  emit(event: dict) → None (async)
  close() → None (async)

WebsocketEdge (websocket_edge.py)
  name = "websocket"
  Wraps FastAPI WebSocket; re-raises WebSocketDisconnect
  Used by: /ws/orchestrate/{name}, /apps/{slug}/ws

SSEEdge (sse_edge.py)
  name = "sse"
  asyncio.Queue-backed. stream() yields raw SSE byte frames.
  Token events → data: <text>\n\n
  Other events → event: <type>\ndata: <json>\n\n
  Terminal    → event: done\ndata: {}\n\n
  Used by: GET /apps/{slug}/sse

WebRTCEdge — planned (future phase)

get_edge_class(name) → Type[EdgeAdapter]   # registry.py
VALID_EDGES = frozenset({"websocket", "sse"})
```

**Edge guard:** `Orchestrator.edges TEXT[]` — if "websocket" is not in the list, the WS connection is rejected after auth with a clear error. Defaults to `{websocket}` for all existing orchestrators.

**SSE entry point flow:**
```
GET /apps/{slug}/sse?message=<text>&context_id=<uuid>
  → auth + app load → SSEEdge() created
  → asyncio.create_task(_run_and_stream())  ← detached task fills the queue
  → StreamingResponse(edge.stream())        ← HTTP response drains the queue
  → X-Accel-Buffering: no                   ← disables Traefik/Nginx response buffering
```

## Legacy Orchestrator (orchestrator_service.py) — Retained

Kept for backward compatibility. Uses in-RAM accumulator, records to `them.runs`/`them.run_steps`/`them.run_usage`. No task graph or artifacts.

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

Redis key mapping: channel `run:abc` → pub/sub channel `them:dash:run:abc`

## Playground Architecture

```
Browser Playground
  ├─ Left pane: chat
  │    └─ WebSocket → /ws/orchestrate/{name}?token=<jwt>
  │         sends: {type:"message", content:"...", context_id:"<uuid>"}  ← context_id optional
  │         streams: ready (run_id, task_id, context_id), token, tool_start, tool_done, done, error
  │         context_id: server assigns fresh UUID on first message; client sends same UUID on
  │                     follow-up messages so memory summary is reused across turns
  └─ Right pane: debug tabs
       ├─ Trace tab — WS → /ws/dashboard, subscribe: ["run:{run_id}"]
       ├─ Tasks tab — GET /api/v1/runs/{run_id}/tasks  (on done event)
       ├─ Artifacts tab — GET /api/v1/runs/{run_id}/artifacts  (on done event)
       └─ Memory tab — context artifacts + per-agent "Fetch Agent Card" button
                        GET {endpoint}/.well-known/agent-card.json (proxied via frontend API)
```

## Adapter Abstraction

```
AgentAdapter (base.py)
  └── stream_invoke(input: dict) → AsyncGenerator[AdapterEvent, None]

AdapterEvent
  type: "token" | "done" | "error" | "task_created" | "status" | "artifact"
  text: str | None          # token events
  result: str | None        # done events
  error: str | None         # error events
  remote_task_id: str|None  # task_created events
  state: str | None         # status events ("working", "completed", ...)
  artifact: dict | None     # full A2A artifact dict
  input_required: bool      # True when agent emits input-required state

OmniWsAdapter (omni_ws_adapter.py)  transport="omni_ws"
  - WebSocket to agent; sends {"type":"message","content":...}
  - Parses WS stream → token/done/error AdapterEvents
  - Used for mock-agent-* containers

A2aAdapter (a2a_adapter.py)  transport="a2a"
  - HTTP JSON-RPC SendMessage; polls GetTask up to 30s
  - Synchronous — collects full result then re-streams as tokens
  - Legacy; kept for simple A2A use cases

A2aAsyncAdapter (a2a_async_adapter.py)  transport="a2a_async"
  - Non-blocking submit → stream via SSE or polling
  - SSE: GET {endpoint}tasks/{id}/events; falls back to polling on HTTP error
  - Deduplicates artifacts by artifact_id
  - Full artifact parts preservation — passes dict to task_store.record_artifact()
  - Used for a2a-echo, a2a-slow, a2a-stream test agents
  - Supports long-running tasks (configurable poll_interval, max_poll_seconds)
```

See `docs/ADAPTERS.md` for complete transport protocol details.

## A2A Test Agents (profiles: test-agents)

Three real A2A SDK agents in `agents/` for integration testing:

| Agent | Port | Purpose |
|---|---|---|
| `a2a-echo` | 9200 | Echoes input verbatim. Tests basic task lifecycle. |
| `a2a-slow` | 9201 | Waits `SLOW_DELAY_S` seconds (default 5). Tests deadline and async delegation. |
| `a2a-stream` | 9202 | Streams response word-by-word as artifact chunks. Tests SSE and artifact assembly. |

All use `a2a-sdk 1.1.0` (`AgentExecutor` ABC, `EventQueue.enqueue_event()`, `InMemoryTaskStore`, `add_a2a_routes_to_fastapi`).

Enable with:
```bash
docker compose --profile test-agents up -d a2a-echo a2a-slow a2a-stream
```

Seeded in `db/002_seed.sql` with `enabled=false` — enable via admin API when running integration tests.

## A2A Push Webhook

When an agent supports push notifications, it can POST task updates to:
```
POST /a2a/push/{task_id}
Authorization: Bearer <access_token>
Body: {"status": {"state": "completed"}, "artifacts": [...]}
```

Handler (`a2a_server.py`):
- Resolves task from `them.tasks`
- Idempotent if task already terminal
- Calls `task_store.transition()` + `task_store.record_artifact()` for each artifact

## Multi-Replica Scalability

| State | File | Replica-safe? | Mechanism |
|---|---|---|---|
| Token cache L1 | token_cache.py | No | in-process dict, independent per replica |
| Token cache L2 | token_cache.py | Yes | Redis `them:session:token:*` TTL 300s |
| Rate limiting | rate_limiter.py | Yes | Redis INCR fixed-window |
| Agent registry cache | agent_registry.py | Yes | Redis `them:agents:registry`, pub/sub invalidation |
| Orchestrator config | task_runner.py | Yes | Redis `them:orchestrators:{name}` TTL 600s |
| Task + artifact state | task_store.py | Yes | Postgres `them.tasks`, `them.artifacts` |
| Context artifact cache | context_service.py | Yes | Redis `them:ctx:{context_id}:heads` TTL 300s |
| Run state (legacy) | run_recorder.py | Yes | Postgres `them.runs` |
| WS connections | ws_orchestrator.py | No (by design) | Traefik sticky sessions cookie `them_lb` |
| Replica heartbeat | main.py bg task | Yes | Redis `them:bridge:{ID}:heartbeat` 30s TTL |

## Background Tasks (main.py lifespan)

- `agent_registry_refresh_loop` — every 600s, re-loads agents from DB, publishes `them:agents:changed`
- `heartbeat_loop` — every 10s, writes `them:bridge:{INSTANCE_ID}:heartbeat`
- `config_change_listener` — xreads `them:control:events` for cache invalidation signals
- `_reaper_loop` — every 60s, finds tasks past `deadline`, transitions them to `failed`
