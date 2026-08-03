# the-M Architecture
# Last updated: 2026-07-11

## Core Mental Model

Each enabled `them.agents` row = ONE LLM tool named `agent__<slug>`.
The agent's `description` is the tool description — the LLM uses it to decide when to call this agent.

All orchestration runs through a single durable path: the Temporal `OrchestrationWorkflow`.
The bridge (`app/routers/ws_orchestrator.py`) is a thin edge — it authenticates the WS
connection, starts/signals the workflow via `bridge_client.py`, and relays Redis token
streams back to the client. All orchestration state (message history, iteration count,
token budget) lives in Temporal, not in the bridge process. The bridge is fully stateless:
any replica can serve any connection, and a bridge restart mid-run does not lose the run.

```
User goal → WS /ws/orchestrate/{name} → ws_orchestrator.py authenticates + starts Temporal workflow
         → OrchestrationWorkflow (them-worker):
               load context + agents + prior history
               create run + root task rows
               agentic loop (≤ max_iterations):
                    plan_turn → LLM picks tool(s) → stream tokens to Redis
                    invoke_agent × N → asyncio.gather (bounded by max_parallel_tools)
                    record_tool_results → persist to DB for multi-turn history
                    loop
               finalize_run → complete run, write final artifact
         → Bridge relays Redis token stream → WS client
```

## Network Topology (Traefik)

All external traffic enters on a single port (default **8088**) via `them-traefik` (Traefik v3.6).
The frontend and bridge are never exposed directly. Bridge is stateless — any replica handles any request.

```
Browser → :8088 (them-traefik)
  PathPrefix(/api/v1)  → them-bridge-svc  (priority 100)
  PathPrefix(/ws)      → them-bridge-svc  (priority 100)
  PathPrefix(/health)  → them-bridge-svc  (priority 90)
  PathPrefix(/)        → them-ui-svc      (priority 10)
```

Traefik config: `traefik/traefik.yml` (static), Docker labels on services (dynamic).
Dashboard (read-only): `http://localhost:8089` (127.0.0.1 only).

**Local dev:** `docker-compose.dev.yml` overrides router rules to `PathPrefix(...)` only (no `Host` constraint) so any IP/hostname works.

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
| `/api/v1/runs/{run_id}/signal` | POST | JWT | HITL: submit human response to paused workflow |
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

## Durable Orchestration — Temporal OrchestrationWorkflow

Every orchestration run executes as a Temporal workflow (`app/temporal/workflows.py`,
`OrchestrationWorkflow`). One workflow instance per `context_id` (conversation thread).
The workflow is deterministic Python; all I/O happens in Activities running in the
`them-worker` container.

### Flow

```
Client → WS /ws/orchestrate/{name}
  └─ ws_orchestrator.py: authenticate, resolve orchestrator, start workflow
       via bridge_client.start_orchestration() — signal_with_start keyed by context_id
  └─ Bridge subscribes to Redis them:dash:run:{context_id}:ctx then :{run_id}:tokens
       and relays token / tool_start / tool_done / done / error frames to the WS client

OrchestrationWorkflow.run():
  1. load_orchestration_context   → orch config, agent list, tool defs, prior history (sanitized)
  2. init_run                     → create them.runs + them.tasks(root), emit run_start
  3. agentic loop (≤ max_iterations):
       plan_turn                  → one LLM streaming turn; streams tokens to Redis; records assistant turn
       (no tool calls) → final answer → break
       invoke_agent × N           → asyncio.gather bounded by max_parallel_tools semaphore
                                     each _invoke_one catches ActivityError → returns failed result
                                     (guarantees 1:1 tool_use ↔ tool_result pairing)
       record_tool_results        → persist tool_result (role='user') message to DB
       summarize_context          → (if memory_enabled) rolling summary every N calls
  4. finalize_run                 → complete them.runs, write final artifact, close root task
                                    (always runs — even on cancellation)
```

### Activities (app/temporal/activities.py)

| Activity | Responsibility |
|---|---|
| `load_orchestration_context` | Load orchestrator + agents, build NeutralTool list, load & sanitize prior history |
| `init_run` | Create `them.runs` + root `them.tasks`; save user message as `task_message seq=0` |
| `plan_turn` | One LLM planning turn; stream tokens to Redis; record usage + assistant turn to DB |
| `invoke_agent` | Route one tool call via adapter; persist child task, step, artifact; heartbeat on task_created |
| `record_tool_results` | Persist tool_result message (role='user') so multi-turn history reloads correctly |
| `summarize_context` | Rolling context summary artifact for memory injection |
| `finalize_run` | Complete run, write Final Answer artifact, transition root task |

### Orchestrator Resolution (`app/temporal/loaders.py`)

`load_orchestrator_row(name, db)` resolves an orchestrator name to a config object in three
steps:

```
1. Redis cache  them:orchestrators:{name}  (TTL 600s)
   → deserializes to _OrchestratorProxy dataclass
   → proxy.is_app_orchestrator = data.get("is_app_orchestrator", False)

2. them.app_orchestrators WHERE name = name AND enabled = true   (Phase 6 primary)
   → sets is_app_orchestrator = True when written to cache

3. them.orchestrators WHERE name = name AND enabled = true       (legacy fallback)
   → sets is_app_orchestrator = False when written to cache
```

**`_OrchestratorProxy`** is a dataclass with an `is_app_orchestrator: bool = False` field.
On a **cache hit** the return value is always a `_OrchestratorProxy`, never an ORM instance,
so `isinstance(proxy, AppOrchestrator)` always returns `False` — code must check
`proxy.is_app_orchestrator` to distinguish the two table sources.

**`load_agents(orch, db)`** builds the tool list for an orchestrator. Beyond real `them.agents`
rows it also includes delegatable sub-orchestrators whose IDs appear in
`orch.allowed_agent_ids`:

- Primary: `AppOrchestrator` rows with `delegatable = True` (Phase 6 field)
- Fallback: legacy `Orchestrator` rows with `a2a_exposed = True` (pre-migration rows)

### Determinism & Durability Guarantees

- Workflow uses `workflow.uuid4()` for `run_id`/`root_task_id` — retries are idempotent.
- `finalize_run` always runs, even on cancellation, so `them.runs` never leaks a `running` row.
- **Cancellation:** Stop from the client cancels the workflow; the workflow inspects
  `ActivityError.cause` for `CancelledError` and records `status=canceled` (not `failed`).
- **Per-activity heartbeat timeouts:** `plan_turn` = 90s (large-context TTFT);
  `invoke_agent` = `agent_timeout + 30s` (slow A2A submit + poll). `invoke_agent_activity`
  sends a heartbeat on `task_created` event so Temporal knows the activity is alive after the
  slow HTTP submit.

### HITL (Human-in-the-Loop)

When an agent emits `input-required`, the workflow pauses on
`workflow.wait_condition(self._human_response is not None, timeout=10m)`. The bridge forwards
the human reply via `POST /api/v1/runs/{run_id}/signal` → `submit_human_response` signal,
which unblocks the workflow and injects the reply as the tool result for the paused slot.

### Parallel Tool Calls

When the LLM returns multiple tool calls in one iteration, the workflow fans them out via
`asyncio.gather(*invoke_coros)` bounded by `orchestrator.max_parallel_tools` (via
`asyncio.Semaphore`) and per-agent `max_concurrency`. Each `_invoke_one` coroutine wraps
`execute_activity` in try/except — any `ActivityError` becomes a failed `InvokeAgentResult`,
guaranteeing that the tool_results message always has exactly as many blocks as the assistant
turn had tool_use blocks. This invariant is required by the Anthropic API.

## Context Memory (memory_service.py) — Phase 8.4

When `orchestrator.memory_enabled = true`, the workflow maintains a rolling summary of agent
call results across iterations.

```
Every N agent calls (summarize_every_n_calls):
  └─ summarize_context_activity:
       → Fetches recent context artifacts
       → Calls summarizer LLM (default: anthropic/haiku)
       → Stores summary text in Redis: them:ctx:{context_id}:summary  TTL 3600s
       → Persists as artifact: name="summary-{timestamp}" in them.artifacts

On next agent call batch:
  └─ injected_context = memory_service.get_injected_context(context_id)
  └─ Prepended "[Context summary]\n{summary}\n\n" to each agent tool call input
```

**Note:** The summarizer targets **agent inputs** (what agents receive), not the planner's
own message history. It helps agents remember prior context across turns, but does not shrink
`self.messages` (the planner's growing conversation). Intra-run context growth is addressed by
JSON-aware tool_result compaction in the workflow (see LESSONS.md).

## Multi-Turn Conversation History (Phase 11)

Each user message creates a new root task. Multi-turn history is reconstructed from
`them.task_messages` on every new turn.

```
Turn 1 (context_id=X):
  root_task_1 created
  task_messages: seq=0 user msg, seq=1 assistant turn, seq=2 tool_result, seq=3 assistant turn, ...

Turn 2 (same context_id=X):
  root_task_2 created
  _load_context_history(context_id=X, exclude=root_task_2)
    → loads root_task_1's task_messages in order
    → passes through _sanitize_history() — drops orphaned tool_use/tool_result pairs
    → returns [{role:user, ...}, {role:assistant, content:[tool_use, ...]}, {role:user, content:[tool_result, ...]}, ...]
  messages = prior_history + [current user msg]  ← sent to LLM
```

**Key invariant:** Every tool_use in an assistant turn has a matching tool_result message
persisted to DB by `record_tool_results_activity`. Without this, resumed conversations see
orphaned tool_use IDs and the Anthropic API returns HTTP 400.

**History sanitization (`_sanitize_history`):** Full-pass sanitizer — collects all
`tool_result` IDs in the history, then rebuilds it dropping any assistant message whose
`tool_use` IDs have no matching result (handles pre-fix DB rows and interrupted runs).
Also drops the orphaned tool_result message following a dropped assistant turn to maintain
valid role alternation.

**Resilient across reconnects:** history is DB-backed — survives WS disconnects, bridge
restarts, or replica changes. Full conversation reconstructed from Postgres on every turn.

**Memory + multi-turn:** both coexist. Memory summarizes agent call artifacts (what agents
did); multi-turn history preserves the user↔orchestrator dialogue. They complement each other.

### Frontend Session Resume

The playground persists the active `context_id` to `localStorage['them:playground:context_id']`.
On page load it verifies the session still exists in the API and shows a "Resume last
conversation?" banner with orchestrator name, turn count, age, and topic. The Sessions debug
tab lists all prior contexts and also writes `context_id` to localStorage on resume.
Follow-up messages carry `context_id` in the WS payload, so the backend reloads prior history.

## Legacy Orchestrator (task_runner.py) — Removed from Live Path

The `task_runner.run()` agentic loop and `orchestrator_service.py` in-RAM loop are no longer
reachable. `ws_orchestrator.py` hardcodes `_TEMPORAL_ENABLED = True`. The `TEMPORAL_ENABLED`
config flag in `config.py` is dead. The legacy modules remain in the tree as reference and
are slated for deletion.

## Pluggable Edge Adapters (app/edges/) — Phase 8.6 / Phase 10

Edges are transport wrappers. They translate a client protocol into `EdgeRequest` and relay
orchestration events back in that protocol's encoding. Zero business logic — same orchestrator
and agents regardless of edge.

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

**Edge guard:** `Orchestrator.edges TEXT[]` — if "websocket" is not in the list, the WS
connection is rejected after auth with a clear error. Defaults to `{websocket}`.

**SSE entry point flow:**
```
GET /apps/{slug}/sse?message=<text>&context_id=<uuid>
  → auth + app load → SSEEdge() created
  → asyncio.create_task(_run_and_stream())  ← detached task fills the queue
  → StreamingResponse(edge.stream())        ← HTTP response drains the queue
  → X-Accel-Buffering: no                   ← disables Traefik/Nginx response buffering
```

## Applications and Entry Points (Phase 9 / Phase 10)

Applications are the external-facing product layer. Each application owns one or more **entry points** — uniquely slugged URL endpoints that external clients connect to.

### Data model

```
them.applications          (parent)
  id, name, orchestrator_id, presentation, enabled

them.entry_points          (child — one row per door)
  id, application_id, slug (globally unique), entry_point_type, access_policy, conversation_token_limit, enabled

them.runs.entry_point_slug  — which door each run came through
```

**One app → many entry points.** Each EP has its own URL:
- `websocket`  → `ws://<host>/apps/{slug}/ws`
- `sse`        → `GET http://<host>/apps/{slug}/sse?message=<text>&context_id=<uuid>`
- `webrtc`     → `http://<host>/apps/{slug}/voice`
- `voice`      → `POST http://<host>/apps/{slug}/voice/transcribe` (STT) + `POST http://<host>/apps/{slug}/voice/tts` (TTS)
- `a2a`        → `POST /a2a` with `skillId=<slug>` in the JSON-RPC body

All entry points on the same app route to the same orchestrator (set on the application row).

### Entry point diff — slug as identity

When updating an application (`PATCH /api/v1/admin/applications/{id}`), the server receives the full desired `entry_points` array and diffs it against the current DB state **by slug** (not by id):

- Slug in both current and desired → **UPDATE** mutable fields (`entry_point_type`, `access_policy`, `conversation_token_limit`, `enabled`)
- Slug only in desired → **CREATE** new EP row
- Slug only in current → **DELETE** the EP row

Slug is the stable identity because it is the URL endpoint. Renaming a slug is a breaking change for external clients — it is modeled as delete + create. The frontend never needs to send the EP's database `id`.

### WS entry point flow

```
ws://<host>/apps/{slug}/ws
  → load EntryPoint by slug → load Application → auth (public or token)
  → start_orchestration_workflow(entry_point_slug=slug, ...)
  → stream_run_events() → relay to client
  → them.runs.entry_point_slug = slug  (traceability)
```

### Voice entry point flow (HTTP STT/TTS)

Two stateless HTTP endpoints — the orchestrator only ever sees plain text.

```
POST /apps/{slug}/voice/transcribe   multipart audio (webm/wav/mp4/m4a)
  → auth (public or token)
  → voice_service.transcribe()  [Groq whisper-large-v3 or OpenAI whisper-1]
  → { "text": "..." }

POST /apps/{slug}/voice/tts          JSON { "text": "..." }
  → auth (public or token)
  → voice_service.stream_tts()  [OpenAI tts-1 or ElevenLabs]
  → StreamingResponse audio/mpeg
```

STT provider, TTS provider, voices, and API keys are configured per `app_orchestrators` row (kind=`voice`). The voice EP is transport-only — the orchestrator has no awareness of audio.

### Mobile voice + A2A pattern

STT and TTS are codec steps on the client. The orchestrator is called via A2A for the conversation:

```
[mic] → POST /apps/{slug}/voice/transcribe → text
text  → POST /a2a  skillId=<a2a-ep-slug>  contextId=<uuid>  → reply text + optional data artifacts
reply → POST /apps/{slug}/voice/tts → audio/mpeg → [speaker]
```

- `contextId` threads turns into a shared conversation — server maintains history (`history_window` turns)
- Client stores only the `contextId` UUID between turns, not message history
- Voice EP and A2A EP are independent doors into the same orchestrator; the orchestrator is unaware which transport the client uses

### Access policy

`access_policy` is a JSONB column:
- `{"mode": "public"}` — no token required; anyone with the URL can connect
- `{"mode": "token"}` — requires a valid `them.access_tokens` bearer token

### Canvas builder

`frontend/src/app/admin/applications/page.tsx` — React Flow canvas:
- Drag EP nodes (websocket / sse / webrtc) and orchestrator node onto canvas
- Connect EP → orchestrator with an edge
- Multiple EP nodes allowed; each gets its own slug
- Save sends full `entry_points` array; server diffs atomically by slug
- Playground "Test" button opens the app's EPs in the multi-EP playground

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

**Dual-channel publishing:** Trace events (tool_start, tool_done, iteration_start) are
published to BOTH `them:dash:run:{run_id}` (trace tab / dashboard WS) AND
`them:dash:run:{run_id}:tokens` (streaming side-channel → bridge → WS client). Both channels
must receive these events for the UI to show them in both the trace tab and the status bar.

## Playground Architecture

```
Browser Playground
  ├─ Target selector — orchestrators OR per-app entry points (grouped by app)
  ├─ Tabs — one tab per added target; switching is a view toggle (WS stays alive)
  │    └─ ChatColumn (per tab) — self-contained WS state machine
  │         target: { kind:'orchestrator', name } | { kind:'entrypoint', slug, epType, ... }
  │         WS URL: /ws/orchestrate/{name} or /apps/{slug}/ws
  │         context_id: persisted to localStorage key them:playground:ctx:{target-id}
  │         sends: {type:"message", content:"...", context_id:"<uuid>"}
  │         streams: ready, token, tool_start, tool_done, iteration_start,
  │                  agent_status, file, done, canceled, error
  │         on error with context_id=null: clears localStorage (dead context signal)
  │         webrtc EP: shows voice-room button only, no chat column
  ├─ Compare mode — 2+ tabs side-by-side; shared composer broadcasts to all active columns
  └─ Debug tabs (per active tab)
       ├─ Trace — WS → /ws/dashboard, subscribe: ["run:{run_id}"]
       ├─ Tasks — GET /api/v1/runs/{run_id}/tasks
       ├─ Artifacts — GET /api/v1/runs/{run_id}/artifacts
       ├─ Memory — context artifacts + per-agent "Fetch Agent Card" button
       └─ Sessions — list prior context sessions; click to resume with same context_id
```

**Poisoned context_id protection:** If a workflow closed (failed/completed/cancelled) and the client resends the same `context_id`, the server raises `DeadContextError` and emits `{"type":"error","context_id":null}`. The `null` context_id signals the client to clear localStorage and start fresh — preventing silent hung sessions.

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

A2aAsyncAdapter (a2a_async_adapter.py)  transport="a2a_async"
  - Non-blocking submit → stream via SSE or polling
  - SSE: GET {endpoint}tasks/{id}/events; falls back to polling on HTTP error
  - Deduplicates artifacts by artifact_id
  - Full artifact parts preservation — passes dict to task_store.record_artifact()
  - Used for all debate agents and docu_writer
  - Supports long-running tasks (configurable poll_interval, max_poll_seconds)
  - Sends heartbeat on task_created so Temporal knows submit completed
```

See `docs/ADAPTERS.md` for complete transport protocol details.

## Debate Stack (db/008_debate_stack.sql)

Four A2A agents implementing a structured 2-round debate with a final verdict.

| Agent | Port | Model | Role |
|---|---|---|---|
| `agent-evidence` | 9401 | Haiku | Argues using empirical data and documented facts |
| `agent-logic` | 9402 | Haiku | Argues from first principles and logical deduction |
| `agent-creative` | 9403 | Haiku | Argues from a surprising lateral field |
| `agent-judge` | 9404 | Sonnet | Scores all arguments, picks winner, synthesizes final answer |

Orchestrated by `debate_flow` (Haiku, `max_iterations=12`). The orchestrator passes only
summary fields `{agent, main_point, confidence, approach, round}` between agents — never full
argument text — to prevent context explosion. Full arguments are preserved in DB artifacts.

**Context compaction (workflows.py):**
- `_compact_tool_result`: JSON-aware — keeps only `_COMPACT_JSON_KEEP` routing fields from tool results
- `_slim_tool_use_inputs`: strips heavy nested arrays from tool_use blocks stored in `self.messages`

## A2A Test Agents (profiles: test-agents)

Three real A2A SDK agents in `agents/` for integration testing:

| Agent | Port | Purpose |
|---|---|---|
| `a2a-echo` | 9200 | Echoes input verbatim. Tests basic task lifecycle. |
| `a2a-slow` | 9201 | Waits `SLOW_DELAY_S` seconds (default 5). Tests deadline and async delegation. |
| `a2a-stream` | 9202 | Streams response word-by-word as artifact chunks. Tests SSE and artifact assembly. |

All use `a2a-sdk 1.1.0` (`AgentExecutor` ABC, `EventQueue.enqueue_event()`, `InMemoryTaskStore`).

## A2A Push Webhook

When an agent supports push notifications, it can POST task updates to:
```
POST /a2a/push/{task_id}
Authorization: Bearer <access_token>
Body: {"status": {"state": "completed"}, "artifacts": [...]}
```

Handler (`a2a_server.py`): resolves task, idempotent if already terminal,
calls `task_store.transition()` + `task_store.record_artifact()` for each artifact.

## Multi-Replica Scalability

| State | Location | Replica-safe? | Mechanism |
|---|---|---|---|
| Token cache L1 | in-process dict per replica | No | independent per replica |
| Token cache L2 | Redis `them:session:token:*` TTL 300s | Yes | shared |
| Rate limiting | Redis INCR `rl:them:*` | Yes | fixed-window |
| Agent config cache | Redis `them:agents:registry` | Yes | pub/sub invalidation |
| Orchestrator config | Redis `them:orchestrators:{name}` TTL 600s | Yes | pub/sub invalidation |
| Run state | Postgres `them.runs` | Yes | |
| Task + artifact state | Postgres `them.tasks`, `them.artifacts` | Yes | |
| WS connections | ws_orchestrator.py | Yes | Temporal holds all state; any replica serves any WS |
| Replica heartbeat | Redis `them:bridge:{INSTANCE_ID}:heartbeat` 30s TTL | Yes | |

## Background Tasks (main.py lifespan)

- `agent_registry_refresh_loop` — every 600s, re-loads agents from DB, publishes `them:agents:changed`
- `heartbeat_loop` — every 10s, writes `them:bridge:{INSTANCE_ID}:heartbeat`
- `config_change_listener` — xreads `them:control:events` for cache invalidation signals
- `_reaper_loop` — every 60s, finds tasks past `deadline`, transitions them to `failed` (safety net for inbound A2A tasks; Temporal handles WS-run timeouts)
