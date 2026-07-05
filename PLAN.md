# the-M — A2A-Native Platform Plan
# Last updated: 2026-07-04
# Status: COMPLETE

---

## Goal

Transform the-M from an LLM-tool-loop orchestrator into a fully A2A-native platform:
- **Inbound:** the-M is itself an A2A agent — external systems can delegate tasks to it
- **Outbound:** the-M delegates to child agents via A2A, tracking real task lifecycle
- **Memory:** shared context flows through the task graph, not in-RAM accumulators
- **Governance:** budget envelopes, deadlines, token roll-up enforced at every level
- **Playground:** agent debug panel with memory inspection, task graph, artifact view
- **Tests:** real A2A SDK agents, full integration coverage, stale tests removed

**Rules:**
- No patches or workarounds — rewrite cleanly when the old shape is wrong
- Stable and robust over clever
- Every phase ships independently (no big-bang)
- Update docs/ and README on completion of each phase

---

## Phase Overview

| Phase | Name | Status |
|---|---|---|
| 1 | A2A Server — the-M as an A2A agent | ✓ Done |
| 2 | Task graph — durable first-class tasks | ✓ Done |
| 3 | Durable planner — context from DB, not RAM | ✓ Done |
| 4 | Async delegation — long-running agents, push, governance | ✓ Done |
| 5 | Shared context — memory across task graph | ✓ Done |
| 6 | Playground — agent debug panel | ✓ Done |
| 7 | A2A test agents — real SDK, integration tests | ✓ Done |

---

## Phase 1 — A2A Server: the-M as an A2A Agent

**Goal:** External systems can delegate tasks to the-M via A2A protocol.
Zero changes to the existing orchestration loop — this wraps it.

### What to build

**1a. Agent Card endpoint**
`GET /.well-known/agent-card.json`
- Served by `app/routers/a2a_server.py` (new file)
- Built dynamically: each `orchestrators` row where `a2a_exposed=true` becomes one A2A skill
- Security scheme: Bearer token (existing opaque tokens)
- Capabilities: streaming=true, pushNotifications=false (v1)
- Add column: `them.orchestrators.a2a_exposed BOOLEAN NOT NULL DEFAULT false`

**1b. Inbound A2A JSON-RPC endpoint**
`POST /a2a`
- Methods: `SendMessage`, `GetTask`, `CancelTask`
- `SendMessage` → validate bearer token → load orchestrator → run existing loop → return Task
- Task state machine: submitted → working → completed | failed
- Returns A2A-compliant Task JSON with artifacts containing the final answer
- No schema change for tasks yet (Phase 2) — use ephemeral in-memory task tracking backed by `runs`

**1c. Wire `contextId` through outbound adapter**
- `A2aAdapter._send_message_body()` currently sends no context
- Add `context_id` parameter, set as `params.message.contextId` in the JSON-RPC body
- Backward compatible — existing callers pass `None`

### Files changed
- `app/routers/a2a_server.py` — NEW: Agent Card + inbound JSON-RPC handler
- `app/adapters/a2a_adapter.py` — add `context_id` to `_send_message_body`
- `app/main.py` — wire new router
- `db/001_schema.sql` — add `a2a_exposed` column to orchestrators
- `app/models.py` — add `a2a_exposed` field to Orchestrator model

### Tests
- New test: `test_16_a2a_server.py` — GET agent-card returns valid JSON, POST SendMessage returns Task

---

## Phase 2 — Task Graph: Durable First-Class Tasks

**Goal:** Every orchestration run is backed by a durable Task in Postgres.
Tasks survive disconnects, form a parent→child graph, carry budget envelopes.

### Schema additions (db/001_schema.sql)

```sql
CREATE TABLE them.tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID REFERENCES them.runs(id) ON DELETE SET NULL,
    parent_task_id  UUID REFERENCES them.tasks(id) ON DELETE CASCADE,
    orchestrator_id UUID REFERENCES them.orchestrators(id),
    agent_id        UUID REFERENCES them.agents(id),
    context_id      UUID NOT NULL,
    state           TEXT NOT NULL DEFAULT 'submitted'
                    CHECK (state IN ('submitted','working','input-required',
                                     'completed','failed','canceled','rejected')),
    kind            TEXT NOT NULL DEFAULT 'root'
                    CHECK (kind IN ('root','delegated')),
    remote_task_id  TEXT,
    push_url        TEXT,
    status_message  JSONB,
    input_message   JSONB NOT NULL,
    budget_tokens   INTEGER,
    deadline        TIMESTAMPTZ,
    max_depth       INTEGER NOT NULL DEFAULT 5,
    tokens_used     INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE them.artifacts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    context_id    UUID NOT NULL,
    artifact_id   TEXT NOT NULL,
    name          TEXT,
    parts         JSONB NOT NULL,
    append_index  INTEGER NOT NULL DEFAULT 0,
    last_chunk    BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, artifact_id, append_index)
);

CREATE TABLE them.task_messages (
    id          BIGSERIAL PRIMARY KEY,
    task_id     UUID NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('user','agent','system')),
    parts       JSONB NOT NULL,
    seq         INTEGER NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, seq)
);

-- Indexes
CREATE INDEX idx_tasks_context    ON them.tasks(context_id, created_at);
CREATE INDEX idx_tasks_parent     ON them.tasks(parent_task_id);
CREATE INDEX idx_tasks_state      ON them.tasks(state)
    WHERE state IN ('submitted','working','input-required');
CREATE INDEX idx_tasks_remote     ON them.tasks(remote_task_id);
CREATE INDEX idx_artifacts_ctx    ON them.artifacts(context_id, created_at);
CREATE INDEX idx_artifacts_task   ON them.artifacts(task_id);
CREATE INDEX idx_task_messages    ON them.task_messages(task_id, seq);
```

**Extend `them.agents`:**
```sql
ALTER TABLE them.agents
    ADD COLUMN agent_card         JSONB,
    ADD COLUMN agent_card_url     TEXT,
    ADD COLUMN supports_streaming BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN supports_push      BOOLEAN NOT NULL DEFAULT false;
```

### New service: `app/services/task_store.py`
- `create_task(...)` → INSERT, enforce state machine
- `transition(task_id, new_state, ...)` → UPDATE with guard (rejects illegal transitions)
- `get_task(task_id)` → SELECT
- `record_artifact(task_id, context_id, artifact_id, parts, ...)` → INSERT into artifacts
- `record_message(task_id, role, parts)` → INSERT into task_messages
- Publishes Redis `them:tasks:{id}:events` on every transition
- All methods async, all errors caught + logged (same pattern as run_recorder.py)

### Phase 1 update
- `a2a_server.py` `SendMessage` now creates a real `them.tasks` row
- Returns `task.id` as the A2A task id
- `GetTask` reads from `them.tasks`

### Files changed
- `db/001_schema.sql` — 3 new tables + agent columns + indexes
- `app/models.py` — Task, Artifact, TaskMessage ORM models + Agent extensions
- `app/services/task_store.py` — NEW: task CRUD + state machine
- `app/services/run_recorder.py` — extend to also write a `tasks` row per run (shadow write)
- `app/routers/a2a_server.py` — wire to real task_store

### Tests
- New test: `test_17_task_store.py` — structural: state machine guards, create/transition/artifact
- Update test_01 to check new tables exist in schema

---

## Phase 3 — Durable Planner: Context from DB, Not RAM

**Goal:** The orchestration loop rebuilds LLM context from the task store instead of
an in-RAM `messages` list. Runs survive disconnects. WS becomes a subscriber.

### The core change

**Current (orchestrator_service.py):**
```python
messages = provider.init_messages(user_goal)          # in-RAM list
for iteration in range(max_iterations):
    async for event in provider.stream_call(messages, tools):
        ...
    messages = provider.append_assistant_response(messages, ...)
    messages = provider.append_tool_results(messages, ...)
```

**New (task_runner.py):**
```python
async def run(root_task_id: UUID, db, redis):
    task = await task_store.get_task(root_task_id)
    orchestrator = await _load_orchestrator(task.orchestrator_id, db)
    
    for iteration in range(orchestrator.max_iterations):
        # Rebuild from DB — this is what makes it resumable
        messages = await _build_messages_from_store(task.context_id, db)
        
        async for event in provider.stream_call(messages, tools):
            if event.type == "token":
                await _publish(task.id, event)         # Redis → WS subscribers
            elif event.type == "tool_calls":
                child_tasks = await _delegate(task, event.tool_calls, db)
                await _wait_for_children(child_tasks)
            elif event.type == "done":
                await task_store.record_artifact(task.id, ...)
                await task_store.transition(task.id, "completed")
                break
```

### New file: `app/services/task_runner.py`
Replaces the body of `orchestrator_service.run_orchestrator`. Clean rewrite.
- `run(root_task_id, publish_fn, db, redis)` — main entry, replaces the generator
- `_build_messages_from_store(context_id, db)` — query task_messages + artifacts by context
- `_delegate(parent_task, tool_calls, db)` — create child task rows, dispatch via adapter
- `_wait_for_children(child_task_ids)` — asyncio.gather for fast agents; suspend for slow
- `_publish(task_id, event)` — Redis publish to `them:tasks:{id}:events`

### WS endpoint becomes a subscriber
`app/routers/ws_orchestrator.py` — rewrite:
1. Auth, load orchestrator (same as today)
2. Create root task via `task_store.create_task()`
3. Launch `task_runner.run(task.id)` as `asyncio.create_task()` — **detached from socket**
4. Subscribe to Redis `them:tasks:{task.id}:events`
5. Relay events to WS client
6. On disconnect: subscription ends, task keeps running

**Flag:** `orchestrators.a2a_exposed` used to gate — orchestrators with this flag use the new path.
All others keep the old `run_orchestrator` path during migration.
Remove flag and delete old path once proven stable.

### Files changed
- `app/services/task_runner.py` — NEW: durable loop
- `app/routers/ws_orchestrator.py` — rewrite as subscriber
- `app/services/orchestrator_service.py` — keep for legacy path during migration, mark deprecated
- `docs/ARCHITECTURE.md` — update loop description

### Tests
- Update test_10 (run recorder) — extend to cover task_store
- Update test_11 (WS orchestrate) — test reconnect scenario
- New test_18 — task survives WS disconnect (structural)

---

## Phase 4 — Async Delegation: Long-Running Agents, Push, Governance

**Goal:** Child agents can run for minutes/hours. Budget enforced. Reaper kills overdue tasks.

### New adapter: `app/adapters/a2a_async_adapter.py`
Transport value: `a2a_async`
- `submit(input, context_id, push_url)` → POST SendMessage, return `remote_task_id` immediately (non-blocking)
- `stream_events(remote_task_id)` → SSE stream if agent supports streaming
- Registers push callback URL in SendMessage params when `agent.supports_push=true`
- Falls back to `GetTask` polling with long configurable deadline (not hardcoded 30s)
- **Preserves full artifact parts array** — no more "first text part wins" truncation
- Yields new `AdapterEvent` types: `task_created`, `artifact`, `status`

### Extended `AdapterEvent` (app/adapters/base.py)
```python
@dataclass
class AdapterEvent:
    type: str   # token | artifact | status | done | error | task_created
    text: str | None = None
    result: str | None = None
    error: str | None = None
    remote_task_id: str | None = None   # on task_created
    state: str | None = None             # on status
    artifact: dict | None = None         # full A2A parts array
    input_required: bool = False
```

### Push webhook: `POST /a2a/push/{task_id}`
- New endpoint in `a2a_server.py`
- Child agent calls this when its task state changes
- Idempotent: `ON CONFLICT DO NOTHING` / state machine guards prevent double-processing
- Looks up child task by `task_id`, updates state, publishes to Redis
- Wakes the parent's `task_runner` via Redis signal

### Governance
**Budget envelope** on `them.tasks`:
- `budget_tokens` — max tokens this task tree may consume (inherited from orchestrator config)
- `deadline` — absolute timestamp (not a timeout offset — survives reconnects)
- `max_depth` — prevents infinite delegation chains
- `tokens_used` — rolls up from children; checked before each planning turn

**Reaper background task** (app/main.py):
- Runs every 60s
- `SELECT * FROM them.tasks WHERE state IN ('working','submitted') AND deadline < now()`
- For each: call `task_store.transition(id, 'failed', error='deadline exceeded')`
- Publish cancellation event, surface error to parent

**Token roll-up:**
- `run_recorder.record_usage()` also increments `them.tasks.tokens_used` for the root task
- Before each planning turn: check `tokens_used >= budget_tokens` → fail gracefully

### Files changed
- `app/adapters/a2a_async_adapter.py` — NEW
- `app/adapters/base.py` — extend AdapterEvent
- `app/adapters/factory.py` — add `a2a_async` branch
- `app/routers/a2a_server.py` — add push webhook endpoint
- `app/main.py` — add reaper background task
- `db/001_schema.sql` — already added budget columns in Phase 2
- `docs/ADAPTERS.md` — document new transport

### Tests
- Update test_07 — cover new AdapterEvent types, a2a_async factory branch
- New test_19 — push webhook idempotency (structural)
- New test_20 — reaper logic (structural)

---

## Phase 5 — Shared Context: Memory Across Task Graph

**Goal:** Agents share context naturally. The-M controls all writes. No separate memory service.

### How it works

When the orchestrator delegates to a child agent:
1. Query `them.artifacts WHERE context_id = $1 ORDER BY created_at` (recent N artifacts)
2. Select relevant ones (recency + size budget)
3. Inline them in the outbound A2A message parts
4. Child agent sees the context; we persist its response as a new artifact

That's the memory read/write cycle. The-M is the sole writer.

### Redis hot cache
Key: `them:ctx:{context_id}:heads`
- Short TTL (300s) — cached list of artifact IDs + previews for active runs
- Invalidated on every `record_artifact()` call
- Falls through to Postgres on miss (same L1/L2 pattern as token_cache)

### New service: `app/services/context_service.py`
- `get_shared_context(context_id, limit, db)` → list of artifacts for a context
- `record_artifact(task_id, context_id, parts, db)` → write artifact + invalidate cache
- Called by `task_runner.py` — never by adapters or external code

### No agent-facing API
Agents receive context inlined in messages. They never call our memory endpoints.

### Redis key added
`them:ctx:{context_id}:heads` — document in `docs/REDIS.md`

### Files changed
- `app/services/context_service.py` — NEW
- `app/services/task_runner.py` — call context_service on delegate + on result
- `docs/REDIS.md` — add `them:ctx:*`
- `docs/ARCHITECTURE.md` — update memory section

### Tests
- New test_21 — context_service: write artifact, read it back, cache invalidation (structural)

---

## Phase 6 — Playground: Agent Debug Panel

**Goal:** Right tray in playground shows per-agent detail with memory inspection,
task graph, artifact viewer, and live status.

### New right tray sections (frontend/src/app/admin/playground/page.tsx)

Replace the current flat trace list with tabbed debug panel:

**Tab 1 — Task Graph**
- Tree view: root task → child tasks
- Each node: agent name, state badge (submitted/working/completed/failed), duration
- Click node → expand to see input/output artifacts

**Tab 2 — Agents**
- Card per agent invoked in this run
- Shows: slug, transport, state, latency, token count
- Button: "Fetch Agent Card" → GET `{agent.endpoint_url}/.well-known/agent-card.json`
  → display skills, capabilities, supported interfaces

**Tab 3 — Artifacts**
- List of all artifacts produced in this run, by task
- Expandable: show full `parts` array (text, file refs, data)
- Filter by agent/task

**Tab 4 — Memory / Context**
- Shows artifacts that were *inlined as context* for each delegation
- "What did this agent know when it was called?"
- Source: the `them:ctx:{context_id}:heads` cache + artifact detail from DB
- New API endpoint: `GET /api/v1/runs/{run_id}/context` → returns context snapshots per delegation

**New button on each agent card:** "Fetch Memories"
- Calls `GET /api/v1/context/{context_id}/artifacts`
- Displays what's stored under this context
- Shows artifact count, sizes, timestamps

### New API endpoints (app/routers/runs.py)
- `GET /api/v1/runs/{run_id}/tasks` — task graph for a run
- `GET /api/v1/runs/{run_id}/artifacts` — artifacts for a run
- `GET /api/v1/context/{context_id}/artifacts` — all artifacts for a context (memory inspector)

### Files changed
- `frontend/src/app/admin/playground/page.tsx` — full UI rewrite of right tray
- `app/routers/runs.py` — 3 new endpoints
- `frontend/src/lib/api.ts` — new API calls

### Tests
- New test_22 — runs/{id}/tasks, runs/{id}/artifacts endpoints return expected shape (live)

---

## Phase 7 — A2A Test Agents: Real SDK, Integration Tests

**Goal:** Spin up 2-3 real A2A agents built on the official Python A2A SDK.
Confirm the platform handles them correctly. Fix anything that breaks.

### Test agents to build (agents/a2a_*/):

**Agent 1: `a2a-echo`** (simple, synchronous)
- Skills: `echo` — returns the input message verbatim
- Transport: A2A, non-streaming, immediate completion
- Tests: basic SendMessage → Task(completed), artifact extraction

**Agent 2: `a2a-slow`** (long-running)
- Skills: `slow_task` — waits 10-60s before completing
- Tests: polling fallback, deadline enforcement, budget governance

**Agent 3: `a2a-stream`** (streaming)
- Skills: `stream_words` — streams a response word by word via SSE
- Advertises `capabilities.streaming=true` in Agent Card
- Tests: A2aAsyncAdapter SSE consumption, artifact assembly from chunks

### Each agent:
- Built with official `a2a-sdk` Python package (latest)
- Runs in Docker, added to `docker-compose.yml` under `profiles: [test-agents]`
- Has its own `/.well-known/agent-card.json`
- Seeded into `them.agents` with `transport=a2a_async`

### New tests replacing stale ones:

**Remove:**
- `test_07_adapter_factory.py` — replace entirely (tests old A2aAdapter assumptions)
- `test_14_e2e_orchestrate.sh` — replace with Python E2E using real A2A agents

**New:**
- `test_07_adapters.py` — factory, AdapterEvent types, a2a + a2a_async + omni_ws
- `test_14_e2e_a2a.py` — full E2E: create token → connect WS → orchestrator calls a2a-echo → verify task + artifact in DB
- `test_23_a2a_slow.py` — E2E: slow agent, verify deadline enforcement
- `test_24_a2a_stream.py` — E2E: streaming agent, verify artifact assembly

### Claude API token usage (from DB)
- Tests retrieve the ANTHROPIC_API_KEY from `them.llm_providers` where `name='anthropic'`
- Each E2E test uses the shortest possible goal ("echo: hello") to minimize token spend
- Tests share a single orchestration run where possible

### Files changed
- `agents/a2a_echo/` — NEW: echo agent
- `agents/a2a_slow/` — NEW: slow agent  
- `agents/a2a_stream/` — NEW: streaming agent
- `docker-compose.yml` — add 3 test agent services under `profiles: [test-agents]`
- `db/002_seed.sql` — seed the 3 A2A test agents
- `scripts/tests/` — remove stale, add new tests
- `scripts/tests/INDEX.md` — update test index
- `docs/STATUS.md` — record test results
- `README.md` — update once all phases complete

---

## What Does NOT Change

| Component | Why |
|---|---|
| `runs` / `run_steps` / `run_usage` | Kept as billing/analytics log. `tasks` links via `run_id`. |
| `OmniWsAdapter` | Fast sync WS agents still work as-is |
| `A2aAdapter` (transport=`a2a`) | Legacy Omni sync path kept for backward compat |
| Token cache L1/L2 pattern | Solid, proven, multi-replica safe |
| Agent registry L1/L2 + pub/sub | Kept, extended with agent_card fetch |
| Auth service (port 8701) | Untouched |
| Rate limiter | Untouched |
| LLM providers | Untouched — LLM is still the planner |

---

## Docs to Update Per Phase

| Phase | Docs |
|---|---|
| 1 | ARCHITECTURE.md (A2A server), ADAPTERS.md (contextId) |
| 2 | SCHEMA.md (3 new tables), ARCHITECTURE.md (task graph) |
| 3 | ARCHITECTURE.md (durable planner loop) |
| 4 | ADAPTERS.md (a2a_async), REDIS.md (no new keys), ARCHITECTURE.md (governance) |
| 5 | REDIS.md (them:ctx:*), ARCHITECTURE.md (memory) |
| 6 | STATUS.md (playground features) |
| 7 | README.md (final update), STATUS.md (test results) |

---

## Progress Tracking

- [x] Phase 1 — A2A Server
- [x] Phase 2 — Task Graph Schema
- [x] Phase 3 — Durable Planner
- [x] Phase 4 — Async Delegation + Governance
- [x] Phase 5 — Shared Context / Memory
- [x] Phase 6 — Playground Debug Panel
- [x] Phase 7 — Real A2A Test Agents + Integration Tests

---
---

# Phase 8 — Native A2A Orchestration, Discovery, Pluggable Edges, Context Memory, OpenAI
# Added: 2026-07-05
# Status: PLANNED — ready for implementation agents

---

## 0. Context — what the code actually looks like today (read before starting)

Findings from the current codebase this plan is built against. Trust these over older prose.

- **Agents are already invoked via real A2A.** The "fake NeutralTool" concern is conceptual, not literal. In `task_runner.run()` each enabled agent becomes a `NeutralTool` (base.py: `NeutralTool = dict`) named `agent__<slug>`. When the LLM fires that tool, `_invoke_agent()` calls `get_adapter(agent)` → `A2aAsyncAdapter.stream_invoke()`, which sends a genuine A2A `SendMessage` JSON-RPC. The outbound A2A path exists and is live-tested. **Phase 8.1 is about removing legacy transports and building the tool list from the agent card, not inventing A2A sending.**
- **OpenAI already exists.** `app/services/providers/openai_compat.py` (`OpenAICompatProvider`) + `app/services/providers/__init__.py` `create_provider(name, api_key, model)` already handle `openai | groq | gemini`. BUT `task_runner._build_provider()` bypasses the factory and hardcodes `AnthropicProvider`. Goal 7 is mostly *wiring*.
- **Schema already carries** `them.agents.agent_card` (JSONB), `agent_card_url` (TEXT), `supports_streaming`, `supports_push`, and `them.orchestrators.a2a_exposed`. The `transport` CHECK already allows `omni_ws | a2a | a2a_async`. Several Phase 8 columns exist; we mostly *use* and *tighten* them.
- **`a2a_server.py` inbound is half-durable.** `_handle_send_message` runs `task_runner_run` correctly but tracks the returned Task in an in-process `_tasks: dict` (Phase-1 leftover) instead of reading `them.tasks`. Goal 5 requires replacing that dict with durable task reads.
- **`admin_agents.py` `VALID_TRANSPORTS = {"omni_ws", "a2a"}`** — does not even include `a2a_async` today, and `AgentOut` does not expose the card. Discovery (Goal 2) rewrites this router's create/update surface.
- **Edge = `ws_orchestrator.py`.** Parses auth, reads one client message, iterates `task_runner_run` directly relaying event dicts. No edge abstraction exists yet. Goal 4 introduces one.

### Exact signatures to build against (do not guess)

```
# app/services/providers/__init__.py
create_provider(name: str, api_key: str, model: str) -> LLMProvider

# app/services/providers/base.py
LLMProvider.stream_call(system, messages, tools, max_tokens) -> AsyncGenerator[LLMStreamEvent]
LLMProvider.call(system, messages, tools, max_tokens) -> LLMIterationResult
LLMProvider.init_messages(user_message) -> list
LLMProvider.append_assistant_response(messages, raw_response) -> None
LLMProvider.append_tool_results(messages, tool_calls, results) -> None
NeutralTool = dict  # {"name","description","schema"}
ToolCall(id, name, input); TokenUsage(input_tokens, output_tokens, cached_tokens)
LLMStreamEvent(type, text, mcp, tool, parameters, result, error, usage)   # type: token|tool_call|tool_calls_ready|done|error

# app/services/task_store.py
create_task(db, *, context_id, input_message, kind="root", run_id, parent_task_id,
            orchestrator_id, agent_id, budget_tokens, deadline, max_depth=5) -> Task
transition(db, task_id, new_state, *, error, status_message, remote_task_id, tokens_used_delta) -> Task|None
get_task(db, task_id) -> Task|None
get_context_artifacts(db, context_id, limit=20) -> list[Artifact]
record_artifact(db, *, task_id, context_id, artifact_id, parts, name, append_index, last_chunk) -> Artifact|None
record_message(db, *, task_id, role, parts, seq) -> TaskMessage|None
add_tokens_used(db, task_id, delta) -> None

# app/services/context_service.py
get_context_artifacts(context_id, db, limit=10) -> list[dict]        # returns portable dicts
record_and_cache_artifact(*, task_id, context_id, artifact_id, parts, name, db) -> None
invalidate_context_cache(context_id) -> None

# app/adapters/factory.py
get_adapter(agent, *, context_id=None) -> AgentAdapter

# app/adapters/a2a_async_adapter.py
A2aAsyncAdapter(*, agent_slug, endpoint_url, auth_token_encrypted, context_id=None,
                push_url=None, supports_streaming=False, poll_interval=1.0, max_poll_seconds=300.0)
```

### Redis key prefixes (reuse only these)
`them:session:`, `rl:them:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`,
`them:ctx:`, `them:tasks:`. Phase 8 adds exactly one sub-namespace: `them:ctx:{id}:summary`.

---

## Sub-phase overview

| Sub-phase | Title | Ships | Depends on |
|---|---|---|---|
| 8.1 | Single A2A transport + drop `omni_ws`/`a2a` | One clean adapter path | — |
| 8.2 | Agent-card discovery + card-driven tool list | "Discover" button; card = tool desc | 8.1 |
| 8.3 | OpenAI as first-class LLM (main + summarizer) via factory | Per-orch OpenAI orchestration | — |
| 8.4 | Context summarization memory | Compacted context injected to agents | 8.3 |
| 8.5 | Orchestrator-as-agent (durable inbound A2A) | One orch calls another via A2A | 8.1, 8.3 |
| 8.6 | Pluggable edge adapters | WS edge behind an abstraction | 8.1 |

Recommended order: **8.1 → 8.3 → 8.2 → 8.4 → 8.5 → 8.6**. (8.3 can start immediately; it unblocks 8.4 and 8.5.)

---

## The new orchestrator loop design (pseudocode)

Keeps the LLM tool-calling API (right interface for structured decisions) but every tool is a real A2A
agent, routed through the single `A2aAsyncAdapter`. Conceptual changes vs today: tools built from the
**agent card** (8.2); context injected into each `SendMessage` is a **summary** when one exists (8.4);
provider chosen via `create_provider()` (8.3).

```
async def run(orchestrator_name, user_message, user_id, token_payload, db, session_id, context_id):
    orch   = load_orchestrator(orchestrator_name)          # Redis→DB, unchanged
    agents = load_allowed_agents(orch)                     # all transport == "a2a_async" after 8.1

    tools = build_tools_from_cards(agents)                 # 8.2: name=agent__<slug>,
                                                           #      description=card+skills, schema=input_schema
    provider = create_provider(orch.llm_provider or "anthropic",
                               api_key=decrypt(orch.llm_api_key_encrypted) or default,
                               model=orch.llm_model or default)      # 8.3

    run_id    = run_recorder.start_run(...)
    root_task = task_store.create_task(kind="root", budget_tokens=orch.budget_tokens, ...)
    task_store.transition(root_task, "working")
    yield {"type":"ready", run_id, task_id}

    msg_seq = 0; agent_calls_since_summary = 0             # 8.4 counter

    while iteration < orch.max_iterations:
        iteration += 1
        budget_guard(root_task)                            # unchanged
        messages = build_messages_from_store(root_task)    # durable replay (provider-neutral after 8.3)

        async for ev in provider.stream_call(system=orch.system_prompt, messages=messages,
                                             tools=tools, max_tokens=4096):
            relay token / capture tool_calls_ready / done / error   # unchanged shape

        record_usage(); add_tokens_used(root_task, ...)
        persist_assistant_turn(root_task, raw_response, msg_seq); msg_seq += 1

        if not tool_calls: break                           # final answer

        results = await gather_bounded(                    # REAL A2A SendMessage per tool call
            [invoke_agent_a2a(agent_by_slug[tc.slug], tc, root_task, run_id, iteration)
             for tc in tool_calls], limit=orch.max_parallel_tools)

        agent_calls_since_summary += len(tool_calls)
        persist_tool_results(root_task, tool_calls, results, msg_seq); msg_seq += 1
        provider.append_tool_results(messages, tool_calls, results)

        if orch.memory_enabled and agent_calls_since_summary >= orch.summarize_every_n_calls:   # 8.4
            await memory.summarize_context(context_id, orch, root_task, db)
            agent_calls_since_summary = 0

    finalize_run(); record_final_artifact(); transition(root_task, terminal)
    yield {"type":"done", ...}
```

### `invoke_agent_a2a` — the single, non-hacky A2A path (replaces `_invoke_agent`)

```
async def invoke_agent_a2a(agent, tool_call, root_task, run_id, iteration, db):
    child = task_store.create_task(kind="delegated", parent_task_id=root_task.id, agent_id=agent.id,
                                   context_id=root_task.context_id,
                                   input_message={"parts":[{"kind":"text","text": tool_call.input["message"]}]})
    task_store.transition(child, "working")
    step_id = run_recorder.record_step(...)                # billing log kept

    memory_prefix   = await memory.get_injected_context(root_task.context_id, orch, db)   # 8.4
    outbound_message = compose_message(memory_prefix, tool_call.input["message"])

    adapter = get_adapter(agent, context_id=str(root_task.context_id))   # always A2aAsyncAdapter (8.1)
    async for ev in adapter.stream_invoke(input={"message": outbound_message},
                                          timeout=agent.timeout_seconds):
        token->accumulate; done->result; artifact->record; error->fail

    if completed: context_service.record_and_cache_artifact(child, ..., parts)
    task_store.transition(child, terminal); run_recorder.complete_step(...)
    return result_text_or_error_marker
```

Only ONE adapter branch survives after 8.1. `get_adapter` never returns `OmniWsAdapter` or sync `A2aAdapter`.

---

## The memory / summarization flow (pseudocode) — Sub-phase 8.4

New module `app/services/memory_service.py`. Redis key `them:ctx:{context_id}:summary`, TTL 3600s.
DB (`them.artifacts`) is source of truth for raw artifacts — never deleted.

```
async def get_injected_context(context_id, orch, db) -> str:
    """What gets prepended to each agent SendMessage."""
    if orch.memory_enabled:
        summary = redis.get(f"them:ctx:{context_id}:summary")
        if summary: return summary["summary"]              # compact — replaces raw artifacts
    arts = context_service.get_context_artifacts(context_id, db, limit=orch.memory_raw_fallback_n)
    return render_artifacts_as_text(arts)                  # current behavior (fallback)

async def summarize_context(context_id, orch, root_task, db):
    if not orch.memory_enabled: return
    raw = task_store.get_context_artifacts(db, context_id, limit=200)
    if not raw: return
    prov_name, model, key = resolve_summarizer(orch)       # resolution order below
    summarizer = create_provider(prov_name, api_key=key, model=model)      # 8.3 factory
    result = await summarizer.call(system=SUMMARY_SYSTEM_PROMPT,
                                   messages=summarizer.init_messages(render_artifacts_as_text(raw)),
                                   tools=[], max_tokens=1024)
    redis.setex(f"them:ctx:{context_id}:summary", 3600, json.dumps({
        "summary": result.text, "covered_artifact_ids": [a.artifact_id for a in raw],
        "updated_at": now(), "model": model}))
    context_service.record_and_cache_artifact(             # persist summary for audit/replay
        task_id=root_task.id, context_id=context_id, artifact_id=f"summary-{ts}",
        name="context summary", parts=[{"kind":"text","text": result.text}], db=db)
```

**Summarizer model resolution** (first non-null wins):
1. `orchestrators.summarizer_provider` + `summarizer_model` + `summarizer_api_key_encrypted`
2. `them.config` key `summarizer.default` → `{"provider","model"}` (key from that provider's `llm_providers` row)
3. Fallback to the orchestrator's main `llm_provider` / `llm_model` / key.

**Rules:** raw artifacts never deleted/mutated; summary additive (Redis + persisted artifact); best-effort —
any failure logs a warning and the loop falls back to raw injection (must never break a run); runs *between*
iterations only (never blocks the user token stream); cross-replica double-summarize is harmless (last write wins).

---

## The edge adapter abstraction — Sub-phase 8.6

New package `app/edges/`. An edge translates client-native input → normalized request, relays runner
events back in the edge's encoding. Conceptually every edge produces an A2A-shaped SendMessage to the orchestrator.

```
# app/edges/base.py
@dataclass
class EdgeRequest:
    orchestrator_name: str; user_message: str; user_id: int; token_payload: dict
    session_id: uuid.UUID; context_id: uuid.UUID | None; modality: str   # "text"|"audio"

class EdgeAdapter(ABC):
    name: str                                        # "websocket"|"voice"|"rest"
    async def parse_request(self, raw) -> EdgeRequest: ...
    async def emit(self, event: dict) -> None: ...   # runner event → client encoding
    async def close(self) -> None: ...

# app/edges/websocket_edge.py  WebsocketEdge over a FastAPI WebSocket
# app/edges/registry.py        get_edge(name) -> EdgeAdapter class; validated against orch.edges
# orchestrators.edges TEXT[] DEFAULT '{websocket}'  (values ⊆ {websocket, voice, rest})
```

`ws_orchestrator.py` becomes a thin shell:
```
edge = WebsocketEdge(websocket)
req  = await edge.parse_request(await websocket.receive_text())
if "websocket" not in orch.edges: reject
async for ev in task_runner_run(**req_fields): await edge.emit(ev)
await edge.close()
```

Voice + REST edges are **registered stubs** in 8.6 (return "edge not enabled"). Voice ships later reusing
the existing `transcription_*` / `tts_*` orchestrator columns.

---

## Schema changes — new migration `db/003_phase8.sql` (idempotent)

Mirror every column into `db/001_schema.sql` (source of truth) + `app/models.py`; document in `docs/SCHEMA.md`.

```sql
-- 8.1: constrain transport to A2A only (migrate rows BEFORE tightening constraint)
UPDATE them.agents SET transport = 'a2a_async' WHERE transport IN ('omni_ws','a2a');
ALTER TABLE them.agents DROP CONSTRAINT IF EXISTS agents_transport_check;
ALTER TABLE them.agents ADD CONSTRAINT agents_transport_check CHECK (transport IN ('a2a_async'));
ALTER TABLE them.agents ALTER COLUMN transport SET DEFAULT 'a2a_async';

-- 8.2: discovery provenance (agent_card/agent_card_url/supports_* already exist)
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS card_fetched_at TIMESTAMPTZ;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS skills JSONB NOT NULL DEFAULT '[]';

-- 8.4: per-orchestrator memory / summarization
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarize_every_n_calls INTEGER NOT NULL DEFAULT 3;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_raw_fallback_n INTEGER NOT NULL DEFAULT 5;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_provider TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_model TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_api_key_encrypted TEXT;

-- 8.6: pluggable edges
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS edges TEXT[] NOT NULL DEFAULT '{websocket}';

-- 8.5: budget_tokens is read via getattr(orch,"budget_tokens") today but no column exists → always None.
--      Add it so the budget guard is real.
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS budget_tokens INTEGER;

-- 8.3: global summarizer default (config row)
INSERT INTO them.config (config_key, config_value)
VALUES ('summarizer.default', '{"provider":"openai","model":"gpt-4o-mini"}')
ON CONFLICT (config_key) DO NOTHING;
```

`app/models.py` additions: `Agent.card_fetched_at`, `Agent.skills`; `Orchestrator.memory_enabled`,
`summarize_every_n_calls`, `memory_raw_fallback_n`, `summarizer_provider`, `summarizer_model`,
`summarizer_api_key_encrypted`, `edges`, `budget_tokens`. Update `_load_orchestrator_row()` cache payload
in `task_runner.py` to serialize the new orch fields.

---

## What gets DELETED

| Item | File | Notes |
|---|---|---|
| `OmniWsAdapter` class | `app/adapters/omni_ws_adapter.py` (delete file) | No agents use it after migration UPDATE |
| `omni_ws` branch | `app/adapters/factory.py` | Remove import + branch |
| Sync `A2aAdapter` | `app/adapters/a2a_adapter.py` (delete file) | Superseded by `a2a_async` |
| `a2a` branch | `app/adapters/factory.py` | Only `a2a_async` remains |
| omni_ws test + `websockets` import | `app/routers/admin_agents.py` `test_agent()` | Only A2A card-fetch test remains |
| `_tasks: dict` in-mem store | `app/routers/a2a_server.py` | Replaced by durable `them.tasks` reads (8.5) |
| `orchestrator_service.py` | keep for now | Legacy RAM loop; out of Phase 8 scope. Note in STATUS as deletion candidate. |

Do NOT delete the `NeutralTool` type — it stays the internal contract between the loop and providers.
"Dropping NeutralTool" means *stop treating agents as fake tools conceptually*; the dict shape is still how
tools are handed to `provider.stream_call()`. The change: its `description` now comes from the agent card (8.2).

---

## SUB-PHASE 8.1 — Single A2A transport, drop `omni_ws` and sync `a2a`

**Goal:** Collapse three transports into one (`a2a_async`); every agent is a real async A2A citizen.

**What changes:**
- `db/003_phase8.sql`: migrate rows, tighten `agents_transport_check` to `('a2a_async')`, set default.
- `app/adapters/factory.py`: delete `omni_ws` + `a2a` branches; return only `A2aAsyncAdapter`; raise clear error otherwise.
- Delete `app/adapters/omni_ws_adapter.py`, `app/adapters/a2a_adapter.py`.
- `app/routers/admin_agents.py`: `VALID_TRANSPORTS = {"a2a_async"}`; default create transport `a2a_async`; strip omni_ws handshake from `test_agent()`.
- `db/001_schema.sql` + `app/models.py`: reflect tightened constraint/default.
- `docker-compose*.yml`: remove/stop `mock-agent-*` (omni_ws) services; keep `a2a-echo/slow/stream`. Note in STATUS.
- `db/002_seed.sql`: seed `assistant/coder/researcher` as `a2a_async` at real A2A endpoints, or mark disabled.
- Docs: `docs/ADAPTERS.md` (drop omni_ws + a2a sections), `docs/ARCHITECTURE.md` adapter section, `CLAUDE.md` container map.

**What stays:** `A2aAsyncAdapter`, `AdapterEvent`, task graph, run recorder, loop structure.

**Tests:** `test_07_adapter_factory.py` — assert `get_adapter` returns `A2aAsyncAdapter` for `a2a_async`, `ValueError` for `omni_ws`/`a2a`; remove positive omni_ws/a2a cases. `test_01_db.sh` — verify new CHECK. `test_16` still green. Sanity: `run_tests.py 01 07 15 16`.

**Risk / notes:** Constraint must be applied *after* the UPDATE, in one transaction, or live rows break. Mock agents speak omni_ws only — delete them or exclude from any orchestrator's allowed set. Add a `docs/LESSONS.md` entry on migration-before-constraint ordering.

---

## SUB-PHASE 8.2 — Agent-card discovery + card-driven tool description

**Goal:** "Discover" fetches `/.well-known/agent-card.json`, auto-fills the form; card description/skills become the LLM tool description.

**What changes:**
- New `POST /api/v1/admin/agents/discover` in `admin_agents.py`: body `{endpoint_url, auth_token?}`; fetch `{endpoint}/.well-known/agent-card.json` (reuse `httpx` + `A2A-Version: 1.0` from `test_agent()`); return `{suggested_slug, display_name, description, skills, supports_streaming, supports_push, agent_card, agent_card_url}`. `suggested_slug` = card name → `[a-z0-9_]`. Tool `description` = card description + one line per skill (`name: description`).
- `AgentCreate`/`AgentUpdate`/`AgentOut` Pydantic: add `agent_card`, `agent_card_url`, `skills`, `supports_streaming`, `supports_push`, `card_fetched_at`; persist on write; set `card_fetched_at=now()` when a card is stored.
- `agent_registry.py` `_agent_to_dict()`: include `skills`, `supports_streaming`, `supports_push`.
- `task_runner.py`: `description = compose_tool_description(agent)` (card-derived, fallback to `agent.description`).
- Frontend `frontend/src/app/agents/page.tsx`: "Discover" button next to endpoint URL → POST `/discover` via `/api/them/[...path]` proxy → populate fields, show skills read-only.

**What stays:** card/support columns (exist); `agent__<slug>` naming; `input_schema` as tool schema.

**Tests:** `test_05_agents_api.sh` — discover happy-path vs `a2a-echo` (needs `--profile test-agents`), assert non-empty `display_name`/`skills`, assert created agent stores `agent_card`. Unit: `compose_tool_description()`. Sanity: `run_tests.py 05 16`.

**Risk / notes:** Cards vary (A2A SDK v1.1 `skills:[{id,name,description,tags,inputModes,outputModes}]`) — parse defensively, never 500 (`{ok:false,detail}`). Discover only *suggests* slug; create still enforces uniqueness (409). Any card write calls `invalidate_registry()`.

---

## SUB-PHASE 8.3 — OpenAI as first-class LLM provider (main + summarizer)

**Goal:** Orchestration + summarization run on OpenAI (and groq/gemini) exactly like Anthropic, via the existing factory.

**What changes:**
- `task_runner.py`: replace `_build_provider()` to call `create_provider(orch.llm_provider or "anthropic", api_key=..., model=...)`; remove hardcoded `AnthropicProvider` import; annotate return `LLMProvider`.
- `app/config.py`: add `OPENAI_API_KEY`, `OPENAI_MODEL` (default `gpt-4o-mini`); extend `LLMConfig`/add openai block. `generate-env.ps1`/`.sh`: OpenAI key passthrough (blank ok).
- `admin_orchestrators.py`: `llm_provider` accepts `anthropic|openai|groq|gemini`; `test-llm` validates via `create_provider(...).call()` 1-token ping (not Anthropic-assuming).
- `db/002_seed.sql`: ensure an `openai` row in `them.llm_providers` (`ON CONFLICT DO NOTHING`).
- `requirements.txt`: confirm `openai` pinned.
- Docs: `docs/ARCHITECTURE.md` provider section.

**What stays:** `AnthropicProvider`, `OpenAICompatProvider`, `create_provider()` (all exist); `NeutralTool`; streaming event shapes.

**Tests:** provider unit — `create_provider("openai")`→`OpenAICompatProvider`, `("anthropic")`→`AnthropicProvider`. `test_06_orchestrators_api.sh` — create `llm_provider="openai"` orch, `test-llm` ok (skip if `OPENAI_API_KEY` unset). E2E 14 once with OpenAI orch (manual).

**Risk / notes — REAL BUG to fix here:** durable replay is Anthropic-only today. `_persist_assistant_turn()` reads `raw_response.content` (Anthropic block list); OpenAI's raw is `_SyntheticResponse` with `.choices[0].message`. `_build_messages_from_store()` also rebuilds Anthropic-shaped `content`. **Cleanest fix: add `provider.serialize_turn(raw_response) -> list[dict]` and `provider.deserialize_history(rows) -> list` to `LLMProvider`, implement per provider, and have `task_runner` delegate history (de)serialization instead of hand-rolling Anthropic blocks.** Without this, an OpenAI-backed run breaks on the second iteration.

---

## SUB-PHASE 8.4 — Context summarization memory

**Goal:** After every N agent calls a cheap model summarizes accumulated artifacts; the summary (not raw) is injected into subsequent agent messages; raw always preserved in DB.

**What changes:**
- New `app/services/memory_service.py`: `get_injected_context()`, `summarize_context()`, `resolve_summarizer()` (see pseudocode). Redis `them:ctx:{context_id}:summary` TTL 3600s.
- `task_runner.py`: `agent_calls_since_summary` counter; call `summarize_context()` after tool results at threshold; in `invoke_agent_a2a`, prepend `get_injected_context()` to the outbound message.
- Schema: orch memory columns (see `003_phase8.sql`).
- `admin_orchestrators.py` + `Orchestrator` model + `_load_orchestrator_row` cache: expose new fields.
- Frontend orch form: "Memory" section — toggle, N, summarizer provider/model (reuse LLM provider list).
- Docs: `docs/REDIS.md` (new key), `docs/ARCHITECTURE.md` memory flow, `docs/SCHEMA.md`.

**What stays:** `them.artifacts` (raw, never deleted); `context_service` cache-aside; `them:ctx:{id}:heads`.

**Tests:** new `test_17_memory.py` — seed context with >N artifacts; `summarize_context()` with stub summarizer; assert Redis summary written, `summary-*` artifact persisted, raw intact; `get_injected_context()` returns summary when present, raw fallback when absent; summarizer failure falls back with no exception. Task-runner integration: `memory_enabled` run yields a summary artifact after N delegations.

**Risk / notes:** Re-summarize the full recent window each checkpoint (track `covered_artifact_ids`) to avoid stale coverage. Default cheap model (`gpt-4o-mini`/haiku). Summarize between iterations only. No cross-replica lock needed.

---

## SUB-PHASE 8.5 — Orchestrator-as-agent (durable inbound A2A composability)

**Goal:** Each `a2a_exposed` orchestrator is callable by another orchestrator via A2A `SendMessage`, backed by the durable task graph (no in-memory dict).

**What changes:**
- `a2a_server.py`: rewrite `_handle_send_message` to run `task_runner_run` and build the returned Task from `them.tasks`/`them.artifacts` (via `task_store.get_task` + `get_context_artifacts`), not `_tasks`. Delete `_tasks`. `_handle_get_task`/`_handle_cancel_task` read/transition via `task_store`.
- Async semantics: honor `configuration.returnImmediately` — return the working Task immediately and continue the run detached (`asyncio.create_task`), so orch→orch works through the *same* `A2aAsyncAdapter` (submit → poll `GetTask` until terminal).
- Self-registration recipe: an orchestrator registered as an `agent` row (`transport=a2a_async`, `endpoint_url`= the-M's own `/a2a`, metadata `skill=<orch name>`). Document it. Discovery (8.2) against the-M's own card already lists exposed orchestrators as skills.
- `agent_card()`: set `capabilities` to reality (polling works); `url` from config, not hardcoded `localhost:8001`.
- Recursion guard: propagate/decrement `max_depth` on child tasks (column exists); reject A2A calls exceeding depth; add per-context task-count ceiling.

**What stays:** `/a2a` JSON-RPC dispatcher, `/a2a/push/{task_id}` webhook, agent-card shape, bearer auth `_resolve_bearer`.

**Tests:** new `test_18_orch_as_agent.py` — expose orch A (`a2a_exposed=true`), register as agent, orch B calls A via A2A, assert B gets A's answer, assert durable `them.tasks` rows for both, assert depth guard rejects self-referential loop beyond `max_depth`. Card check: the-M card lists exposed orchestrators.

**Risk / notes:** Infinite recursion is the sharp edge — enforce `max_depth` + per-context ceiling. Auth: inbound A2A uses `them.access_tokens`; decide + document how an orchestrator obtains a token to call another (service token vs propagate caller's). Blocking vs async must match what `A2aAsyncAdapter` polls for — the handler must persist state transitions so external pollers see progress.

---

## SUB-PHASE 8.6 — Pluggable edge adapters

**Goal:** Client transports (websocket now; voice/rest later) sit behind an `EdgeAdapter` abstraction declared per orchestrator.

**What changes:**
- New `app/edges/`: `base.py` (`EdgeAdapter`, `EdgeRequest`), `websocket_edge.py` (`WebsocketEdge`), `registry.py` (`get_edge`), stubs `voice_edge.py`/`rest_edge.py` (registered, return "edge not enabled").
- `ws_orchestrator.py`: refactor to instantiate `WebsocketEdge`, parse → `EdgeRequest`, enforce `"websocket" in orch.edges`, iterate `task_runner_run`, `edge.emit()` each event. Wire protocol unchanged.
- Schema: `orchestrators.edges TEXT[] DEFAULT '{websocket}'`.
- `admin_orchestrators.py` + model + cache + frontend: `edges` multi-select (websocket/voice/rest).
- Docs: `docs/ARCHITECTURE.md` edge section, `docs/INDEX.md`.

**What stays:** WS wire protocol (`ready/token/tool_start/tool_done/done/error`), auth flow, `task_runner_run` signature.

**Tests:** `test_11_ws_orchestrate.sh` must pass unchanged (proves transparent refactor). New: orchestrator whose `edges` excludes `websocket` rejects the WS with a clear error. Sanity: `run_tests.py 11 13`.

**Risk / notes:** Behavior-preserving refactor — `test_11` passing unchanged is the acceptance bar. No voice/rest bodies here (stubs only).

---

## Cross-cutting: documentation updates (mandatory per CLAUDE.md)

| Change | Doc |
|---|---|
| `them:ctx:{id}:summary` key | `docs/REDIS.md` |
| New orch/agent columns | `docs/SCHEMA.md` + `db/001_schema.sql` |
| New loop / memory / edge flows | `docs/ARCHITECTURE.md` |
| Removed transports | `docs/ADAPTERS.md` |
| Migration ordering; provider-neutral history bug | `docs/LESSONS.md` |
| Per-sub-phase completion + unresolved | `docs/STATUS.md` |
| Tests 17, 18 | `scripts/tests/INDEX.md` |

## Trigger-map additions (for CLAUDE.md testing section)

| Changed | Run tests |
|---|---|
| `app/services/memory_service.py` | 17 |
| `app/routers/a2a_server.py` | 18 |
| `app/edges/` | 11 |
| `app/services/providers/` | provider unit + 06 |

## Acceptance for "Phase 8 complete"

1. Only `a2a_async` transport exists; `omni_ws`/`a2a` code deleted; factory has one branch.
2. Discover button fills the agent form from a live card; card description drives LLM routing.
3. An orchestrator runs end-to-end on OpenAI (`test-llm` ok, full run ok); durable replay works for both providers.
4. With `memory_enabled`, a run produces a summary artifact after N delegations; raw artifacts intact; agents receive the compact summary.
5. Orchestrator A is callable by orchestrator B over A2A with durable tasks and depth-guarded recursion.
6. WS edge runs behind `EdgeAdapter`; `test_11` passes unchanged; unlisted edges rejected.
7. Full suite green: `python scripts/tests/run_tests.py` (0 failures), plus new 17, 18.
