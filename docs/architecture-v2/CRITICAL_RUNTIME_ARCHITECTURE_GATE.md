# Critical Runtime Architecture Gate
# Date: 2026-07-26
# Branch: main — HEAD e5bbca5
# Status: GATE DOCUMENT — must be approved before data-plane implementation begins
# Requested by: Post-Wave-7 planning session

---

## Purpose

This document defines the Go-native runtime architecture for the full orchestration data-plane:
the path from an arriving WebSocket or SSE connection through authentication, admission, session,
run lifecycle, orchestration, agent/tool/LLM execution, streaming, and durable audit.

It does not mirror Python's architecture. It designs the Go runtime from first principles,
informed by what Python does correctly, what it does poorly, and what the existing Go packages
have already proven.

**This is a gate document.** No data-plane implementation work may begin until the open
decisions in §10 are resolved and this gate is approved.

---

## 1. Runtime Boundaries

Each subsystem below has a single, bounded responsibility. No subsystem reaches into another's
internals. All cross-boundary communication goes through typed interfaces.

### 1.1 Entry Point Adapters

**Responsibility:** accept an inbound transport connection, authenticate it, and produce a
standardized `Session` value that the Session Runtime can accept.

| Adapter | Transport | Notes |
|---|---|---|
| `internal/ws` | WebSocket (RFC 6455) | Already exists. Text framing, 30s first-message deadline. |
| `internal/sse` | Server-Sent Events | Already exists. Unidirectional streaming. |
| `internal/a2a` | JSON-RPC 2.0 over HTTP | Already exists. A2A v1.1.0 message/send. |
| `internal/voice` (future) | WebRTC / audio framing | Not implemented. Blocked until audio-path design is approved. |

Each adapter is responsible for:
- Transport-layer upgrade and framing (WS upgrade, SSE `text/event-stream` header)
- Token extraction from header or query param
- Forwarding the raw token and resolved EPConfig to the Admission layer
- Receiving a typed `RunEvent` stream from the Session Runtime and serializing it
  to the transport wire format
- Detecting client disconnects and cancelling the context

Adapters must NOT:
- Load orchestrator configuration
- Start runs or allocate DB resources
- Hold any shared mutable state across connections

**Wire format contract** (currently live, must be preserved):
```
Client→Server:  {"type":"message","content":"<user text>","last_event_id":"<optional cursor>"}
Server→Client:  {"type":"token","content":"<delta>"}
                {"type":"tool_call","name":"...","input":{}}
                {"type":"tool_result","name":"...","output":{}}
                {"type":"done","run_id":"..."}
                {"type":"error","message":"..."}
                {"type":"replay_unavailable","message":"..."}
```

### 1.2 Admission and Rate-Limit Layer

**Responsibility:** make the binary admit/reject decision before any session or DB resource is
allocated. The decision must be atomic, bounded in latency, and rollback-safe on failure.

This layer is the Go implementation of what Python's `runtime_manager.runtime_gate()` does,
plus the enhancements already in `internal/gate`. It is the existing `internal/gate` package.

Admission sequence (already implemented, must be preserved):
1. `Gate.Check()` — atomic Lua: ghost-prune EP Set + App Set, count live sessions, rate-limit
   INCR, SADD with short reservation TTL (10s). Returns -1 (EP cap), -2 (App cap), -3 (rate).
2. Transport upgrade (WS) or begin SSE stream.
3. `Session.Register()` — write session Hash to Redis.
4. `Gate.Confirm()` — extend shadow TTL from 10s to 90s.
5. If Register fails: `Gate.Rollback()` — immediately removes Set membership + wakes one waiter.
6. On session end: `Session.End()` + `Gate.Release()`.

The queue protocol (BLPOP-based compete, not guarantee) is correct. No change to this sequence.

Admission checks, in order:
1. Blocked token hash (sha256 of raw bearer token in app_runtime.blocked_tokens)
2. Blocked user ID
3. App-level rate limit (Redis INCR per hour-slot, soft)
4. App-level session cap (SCARD, soft — coarse guardrail, not atomic)
5. EP-level session cap (Lua atomic with ghost-prune + SADD)
6. Per-token rate limit (already in Gate.Check Lua script)
7. Queue if EP cap is full and queue_timeout_seconds is set

### 1.3 Session Runtime

**Responsibility:** own the in-flight session state for exactly one connection lifetime.

A session is:
- A `context.Context` with a cancellable lifetime
- A Redis Hash (`them:sess:{id}`) with heartbeat extension
- A membership entry in the EP Set and App Set (owned by Gate, cleared on End)
- An active counter in the process-level atomic (`activeSessions`)

Session state in Redis Hash (current fields + planned):
```
session_id, instance_id, user_id, orchestrator_name, ep_slug,
context_id, started_at
+ tenant_id (future tenant wave)
+ app_id (architecture debt T-2 — must be set, currently not)
```

The session is NOT the run. A session may produce zero or many runs (multi-turn).

Session lifetime:
- Created at Gate.Check success
- Registered after WS upgrade / SSE stream begin
- Heartbeat refreshed every 30s via the pod-heartbeat loop
- Ended on: client disconnect, context cancel, or admin disconnect signal
- Redis keys cleaned atomically via Lua SREM+DEL on End

**Architecture debt T-2** (from Gate): `AppID` is NOT currently stored in the session Hash.
This blocks future tenant-scoped session admin queries. Fix must be in the session redesign wave.

### 1.4 Run Runtime

**Responsibility:** own one agentic conversation turn. A run has a stable ID, is persisted in
`them.runs`, and produces a stream of events. A run's lifetime is bounded by the session's
context but may be shorter.

Run lifecycle:
1. `CreateRun` — write `them.runs` row with `status=running`, `events_transport=<mode>`
2. Subscribe to the event source BEFORE starting the workflow (race-free bootstrap)
3. Start Temporal workflow OR inline orchestrator goroutine
4. Stream events to the Entry Point adapter
5. `UpdateStatus` — write `them.runs` status on completion/failure
6. Optional: record final artifact

The `events_transport` column on `them.runs` determines which event delivery path is used:
- `pubsub` — Redis Pub/Sub channel `them:dash:run:{runID}:tokens`
- `streams` — Redis Stream `them:dash:run:{runID}:stream` with XRANGE replay + live XREAD

Both paths must remain in place until Phase 11c-C staging period is complete (explicit approval
gate required before removing Pub/Sub — see implementation-status.md §Phase 11c-D).

### 1.5 Orchestration / Decision Layer

**Responsibility:** the agentic loop. Load the orchestrator config, call the LLM, interpret
tool calls, fan out to agents, collect results, repeat until stop or max_iterations.

The loop is in `internal/orchestrator`. The selected state model for the Go implementation is
**in-memory accumulation with durable checkpoints** — see §10 (OD-3) for the full analysis
and rationale. The model is described here for completeness:

- Message history is accumulated in-process during a run; not reloaded from DB each iteration
- After every completed LLM iteration, the assistant turn and tool results are written to
  `them.task_messages` as a checkpoint
- On reconnect (new WS/SSE connection to the same context_id), history is loaded from DB
  up to the last completed checkpoint, then the Temporal workflow resumes from that point
- Budget token state is maintained in-process and also checkpointed to the `tasks` row
- This model is crash-safe because Temporal owns the durable run boundary; the orchestrator
  never needs to rebuild from DB mid-run unless it is recovering after a crash

**Current limitation:** the Go orchestrator (`internal/orchestrator`) does not yet implement
the full Python feature set. Gaps identified from `task_runner.py`:

| Feature | Python | Go |
|---|---|---|
| Multi-turn context history load | Yes (prior root tasks by context_id) | Partial (history via HistoryLoader) |
| Per-iteration DB context rebuild | Yes | No — messages built once per run |
| Budget token enforcement | Yes | No |
| Agent skill auto-discovery (A2A card fetch) | Yes (TTL-cached) | No |
| Memory injection (summarize_every_n_calls) | Yes | No |
| File artifact parts (filename + media_type) | Yes | No |
| Parallel tool fan-out with semaphore | Yes (asyncio.Semaphore) | No (serial) |
| Agent-specific concurrency limit | Yes (per-agent max_concurrency) | No |
| Per-iteration token usage recording | Yes | Partial |
| Child task row creation per agent call | Yes (them.tasks delegated rows) | No |

These gaps are explicitly **out of scope for this gate document**. The gate designs the
container. The gaps are listed here to prevent the implementation from discovering them late.

### 1.6 Execution Engine (Agent / Tool / LLM)

**Responsibility:** call one agent, tool, or LLM and return a result.

Three execution targets:

**LLM provider** (`internal/llm`): `Provider.Stream()` — HTTP to Anthropic or other provider.
Context cancellation propagates to the HTTP request. Mock provider for tests. Already correct.

**A2A agent** (`internal/agentregistry`): two-level Redis cache → HTTP to agent endpoint.
Returns `json.RawMessage`. No streaming from agents to the orchestrator loop (agents stream
internally, but the result is aggregated). This matches Python's `_invoke_agent`.

**Local tool** (future): Go function registered with the orchestrator. Not currently needed.

The key design decision: **the orchestrator does not stream agent output to the client during
agent execution.** It aggregates agent results, then feeds them back to the LLM. The LLM's
next text response is what streams to the client. This matches Python's current behavior.

Exception: `agent_status` events (state changes during agent execution) are published to the
event bus and forwarded to the client — these are transient informational events, not data.

### 1.7 Streaming Pipeline

**Responsibility:** move events from where they are generated to where they are consumed,
with bounded buffers, defined drop policy, and guaranteed terminal event delivery.

Two event flows exist simultaneously:

**Flow A — In-process bus (inline Go orchestrator path):**
```
Orchestrator.Run() → event.InMemoryBus.Publish() → buffered channel → WS/SSE write
```
Buffer: 256 events per subscriber. Slow consumers: events dropped (non-blocking send).
Terminal events (`done`, `error`) must not be dropped. Current code drops them on buffer full
— this is Finding L-1 (architecture debt). Must be fixed before production scale-up.

**Flow B — Redis run-stream (Temporal path):**
```
Python/Go activity → Redis Stream XADD (or Pub/Sub PUBLISH) → XRANGE replay + XREAD BLOCK → WS/SSE write
```
Replay cursor: `last_event_id` from client reconnect. MAXLEN trim handled by `replay_unavailable` event.

The two flows are routed by `runstream.Dispatcher` based on `RUN_EVENTS_MODE` and
`runs.events_transport`. This design is correct and must be preserved.

### 1.8 Temporal Durable Layer

**Responsibility:** provide durable execution, retry on failure, and HITL support for
long-running orchestration. Temporal owns workflow execution, not event delivery.

**What Temporal handles (correct):**
- Durable run execution — survives process restarts
- HITL pause/resume via `submit_human_response` signal
- Activity-level retry (currently MaximumAttempts=1, orchestrator handles internally)
- Workflow ID namespace (`ctx-{contextID}`) — matches Python, required for HITL signals

**What Temporal must NOT handle (existing violations in Python, must not repeat in Go):**
- Individual LLM tokens — never emit one Temporal activity per token
- Individual agent status events
- Redis session state — session lifecycle is outside Temporal
- WebSocket connection lifecycle — connections are process-local
- Event delivery to the client — that is the streaming pipeline's job

**Correct Temporal boundary:**
```
One workflow per run:
  OrchestrationWorkflow(input) {
    result = ExecuteActivity(RunOrchestratorActivity)
    if HITL → GetSignalChannel(SignalHumanInput).Receive() → re-execute
    return result
  }
```
The workflow wraps the entire run. The activity runs the agentic loop. This is already the
design in `internal/temporal/workflow.go` — it is correct and must not be changed.

### 1.9 Redis Coordination

**Responsibility:** session membership, admission counts, rate limits, event delivery (Pub/Sub
and Streams), cache (token L2, orchestrator config, A2A agent cards).

Redis key inventory (all on DB index 0):

| Key pattern | Owner | TTL | Purpose |
|---|---|---|---|
| `them:ep:{slug}:sessions` | Gate | Implicit via shadow | EP session membership Set |
| `them:ep:{slug}:shadow:{sid}` | Gate | 10s (reservation) / 90s (confirmed) | Ghost-prune TTL |
| `them:app:{id}:sessions` | Gate | Implicit via shadow | App session membership Set |
| `them:app:{id}:shadow:{sid}` | Gate | 10s / 90s | Ghost-prune TTL |
| `them:ep:gate:queue:{slug}` | Gate | Implicit | BLPOP wait channel |
| `them:sess:{id}` | Session | 90s (refreshed) | Session Hash |
| `rl:them:token:{hash}:{minute}` | Gate | 90s | Per-token rate limit |
| `rl:them:app:{id}:{slot}` | RuntimeManager | 7200s | App rate limit |
| `them:token:{sha256}` | Auth L2 | 5m | Bearer token L2 cache |
| `them:token:revoked` | Auth | Pub/Sub channel | Cross-pod L1 eviction |
| `them:dash:run:{runID}:tokens` | RunStream | Message lifetime | Pub/Sub events channel |
| `them:dash:run:{runID}:stream` | RunStream | MAXLEN 2000 | Redis Stream events |
| `them:orch:tmpl:{name}` | OrchestratorCache | 600s | Orchestrator template cache |
| `them:orch:loc:{name}` | OrchestratorCache | 600s | Orchestrator location pointer |
| `them:app:{id}:orch:{name}` | OrchestratorCache | 600s | App-scoped orchestrator cache |
| `them:sess:control:{sid}` | RuntimeManager | Pub/Sub | Disconnect signal channel |
| `them:agents:{slug}` | AgentRegistry L2 | 3600s | A2A agent card cache |
| `them:orchestrators:*` | AgentRegistry | 3600s | Orchestrator registry cache |
| `them:bridge:*` | PodHeartbeat | 90s | Pod presence / session count |
| `them:dash:runs` | DashBroadcaster | Pub/Sub | Cross-run summary channel |

All keys must gain a `{tenant_id}:` segment in Tier 2 tenant isolation (future). The key
builder in Tier 2 must be a central function, not inline string concatenation.

### 1.10 PostgreSQL Persistence

**Responsibility:** durable business state — runs, steps, usage, tasks, messages, artifacts,
agents, orchestrators, applications, entry points, access tokens.

Access rules (already established, must not regress):
- No SQL in handlers or orchestrator — all queries in DAL layer
- No ORM — pgx/v5 direct queries with named parameters
- DB-level LIMIT on history queries — never full-scan then slice
- Connection pool shared via pgxpool — never per-request connections
- Every mutation that changes tenant-scoped data must carry `tenant_id` in the query
  (once tenant columns exist — see TENANT_FOUNDATION_DECISIONS.md §6)

Key tables in the orchestration data-plane:
- `them.runs` — one row per agentic turn; `events_transport` column routes event delivery
- `them.run_steps` — one row per agent invocation (billing/analytics)
- `them.run_usage` — token counts and cost per LLM call per run
- `them.tasks` — root task per run + delegated task per agent call
- `them.task_messages` — serialized LLM message history per task (durable planner state)
- `them.artifacts` — file/data outputs produced by agent calls

### 1.11 Artifact Delivery

**Responsibility:** move large binary and structured artifacts from agent output to the client
without embedding them inline in the streaming event channel.

**Rule:** artifacts with `filename` + `media_type` (file artifacts) must be delivered by
reference, not inline. Inline artifact embedding in the event stream is limited to:
- JSON data artifacts (`application/json`, no filename) — exposed as text to the LLM
- Short text fragments (already concatenated into `result_text`)

File artifact delivery path (not yet implemented in Go):
```
Agent produces artifact → Record in them.artifacts (id + metadata + storage reference)
→ Emit {"type":"file","artifact_id":"...","filename":"...","media_type":"..."} event
→ Client fetches artifact via GET /api/v1/runs/{run_id}/artifacts/{artifact_id}
```
The current Python implementation embeds file part text inline in the WS event, which works
for small outputs but does not scale to binary files. The Go design must deliver by reference.

### 1.12 Audit and Observability

**Responsibility:** emit structured audit rows for mutations, and structured log events for
operationally significant transitions.

Audit rows must be written for:
- Session created / ended
- Run created / completed / failed
- Admin mutations (create/update/delete agents, orchestrators, applications, LLM providers)
- Token created / revoked
- Disconnect signals
- (Future) Tenant context switches by Platform Admins

Observability:
- All packages accept `*slog.Logger` via constructor — no `slog.Default()` in library code
- Every log line includes: `run_id`, `session_id`, `ep_slug`, `instance_id` as applicable
- Prometheus metrics (not yet implemented) — required before production scale-up
- 5xx responses use static strings — never `err.Error()` from service or DAL layers

---

## 2. Tenant Boundary

Reference: `TENANT_FOUNDATION_DECISIONS.md`.

### 2.0 Correction: Tenant vs Application

The original gate document contained an error in this statement:

> "The tenant boundary is the application, enforced at every layer."

This is wrong. **Tenant is the security and execution boundary. Application is a
tenant-owned resource** — it is one of the things that a tenant owns, not the definition
of the tenant itself. Conflating the two makes it impossible to enforce cross-application
quotas, cross-application audit, or cross-application session limits for a single customer.

The corrected model:

```
Tenant          — the customer organisation; the security and billing boundary
  └── Application — a tenant-owned product surface (chatbot, API, workflow)
        └── Entry Point — a transport door into one Application
              └── Session — one connection lifetime through one Entry Point
                    └── Run — one agentic conversation turn within one Session
```

**Tenant is never derived from the application.** The application confirms the tenant, it
does not define it. The resolution chain goes: authenticated identity → tenant_id →
then verify that the requested application belongs to that tenant.

### 2.1 Runtime Identity

Every execution context carries this identity tuple, fully resolved before any DB resource
is allocated:

```
tenant_id       — UUID; the security boundary; from JWT claim or access_tokens.tenant_id
application_id  — UUID; the product surface; resolved from EP slug → applications.id;
                  verified: applications.tenant_id == tenant_id
user_id         — int64 (or 0 for anonymous/public EPs); from bearer token or JWT sub
session_id      — UUID; generated at Gate.Check; lives for this connection lifetime only
run_id          — UUID; generated at CreateRun; lives for this agentic turn only
```

These five values travel together through the entire execution path. No layer may invent,
derive, or modify any of them. They are set once at the Entry Point adapter and propagated
downward via `context.Context`.

Go type (to be introduced in `internal/domain` or `internal/transport`):

```go
type RuntimeIdentity struct {
    TenantID      string // UUID string
    ApplicationID string // UUID string
    UserID        int64  // 0 for anonymous
    SessionID     string // UUID string
    RunID         string // UUID string; empty until CreateRun
}
```

### 2.2 Tenant Context Resolution Chain

Resolution is strict and sequential. A failure at any step produces 403, not 404, and
not a fallthrough to the next step.

```
Step 1 — Authenticate the caller
  Bearer token:
    → auth.Cache.Validate(rawToken)
    → returns TokenInfo{TenantID, UserID, ...}
    → TenantID is the authoritative tenant; set in ctx
  JWT (admin APIs):
    → jwt.Validate(token)
    → returns Claims{TenantID, Role, ...}
    → TenantID is the authoritative tenant; set in ctx
  Anonymous / public EP:
    → No token required; TenantID is DEFERRED to Step 2
    → UserID = 0

Step 2 — Resolve Entry Point and Application
  → epconfig.Load(ctx, epSlug)
  → reads them.entry_points JOIN them.applications WHERE ep_slug = $1
  → returns EPConfig{AppID, TenantID, ...}

  Verification (must pass before any resource is allocated):
  IF caller is authenticated:
    ASSERT EPConfig.TenantID == ctx.TenantID  → else 403
  IF caller is anonymous:
    SET ctx.TenantID = EPConfig.TenantID
    (anonymous public EPs are scoped to the EP's tenant)
  ASSERT EPConfig.AppEnabled == true          → else 403

  At this point ctx carries: TenantID, ApplicationID, EPSlug

Step 3 — Admission Gate (Gate.Check)
  Gate admission keys include AppID, not just EPSlug:
    them:ep:{slug}:sessions        — EP-level cap
    them:app:{appID}:sessions      — App-level cap  (existing)
    them:tenant:{tenantID}:sessions — Tenant-level cap (future Tier 0+)
  Rate limit key includes TenantID to prevent cross-app leakage within a tenant.

Step 4 — Session creation
  SessionInfo includes TenantID, ApplicationID, UserID, EPSlug, SessionID
  Written atomically to Redis Hash — none of these fields are mutable after creation.

Step 5 — Run creation (CreateRun)
  runs row carries: tenant_id, application_id, user_id (via session), session_id
  None of these are updatable after row creation.

Step 6 — Orchestration and execution
  Orchestrator config is loaded from DB scoped to the ApplicationID:
    app_orchestrators WHERE application_id = $appID AND name = $name
  LLM API key resolved: app_orchestrators.llm_api_key_encrypted (tenant's key)
                     OR llm_providers.api_key (platform fallback — no tenant scope)
  A2A agent calls carry: context_id (run-scoped), session_id
  NO tenant_id in the LLM API call itself — the correct key is already selected

Step 7 — Event delivery
  Event bus topics keyed by context_id and run_id — no cross-tenant sharing possible
  at current cardinality. Tier 2: all Redis keys gain {tenant_id}: prefix.

Step 8 — Logs and Audit
  Every slog line carries: tenant_id, application_id, session_id, run_id, ep_slug
  Audit rows (future): tenant_id, application_id on every mutation row
```

**The verification rule at Step 2 is the tenant security enforcement point.** All downstream
layers trust the resolved `RuntimeIdentity` in the context — they do not re-verify. If
the identity is not in the context, the request must be rejected at the boundary where it
is missing; it must not be inferred from the payload.

### 2.3 Application Ownership Verification

Before runtime execution begins, the following must be confirmed in order:

1. **Authentication succeeds** → produces `tenant_id` (or defers for anonymous public EPs)
2. **EP exists and belongs to the tenant** → `epconfig.Load` returns `EPConfig.TenantID`;
   verified equal to authenticated `tenant_id`; mismatch = 403
3. **EP and Application are enabled** → `EPConfig.AppEnabled && EPConfig.EPEnabled`; else 403
4. **Block-list checks pass** → `blocked_tokens`, `blocked_user_ids` in `app_runtime` config
5. **Admission gate passes** → session cap, rate limit, queue

Only after all five steps does any DB resource (session, run) get allocated. A failure at
step 2 must not leak whether the EP exists under a different tenant. 403 is always returned,
never 404, for a cross-tenant mismatch.

### 2.4 Tenant Quotas and Fairness

Quota enforcement layers, ordered from broadest to tightest:

| Layer | Scope | Key | Enforcement | Current state |
|---|---|---|---|---|
| Platform rate limit | All tenants | `rl:them:global:{slot}` | Soft INCR | Not implemented |
| Tenant session cap | All apps in tenant | `them:tenant:{id}:sessions` | Soft SCARD | Not implemented |
| Tenant rate limit | All requests by tenant | `rl:them:tenant:{id}:{slot}` | Redis INCR | Not implemented |
| App session cap | All EPs in app | `them:app:{id}:sessions` | Soft SCARD | Implemented |
| App rate limit | All connections for app | `rl:them:app:{id}:{slot}` | Redis INCR | Implemented |
| EP session cap | One entry point | `them:ep:{slug}:sessions` | Atomic Lua | Implemented |
| Per-token rate limit | One bearer token | `rl:them:token:{hash}:{min}` | Atomic Lua | Implemented |

Tenant-level quota enforcement (the first three rows) requires `tenant_id` columns and is
deferred to Phase R-4. The existing per-app and per-EP layers are correctly implemented
today and provide meaningful isolation at the application level.

**Fairness principle:** one tenant's exhaustion of their EP or app quota must not affect
any other tenant's EPs. This is structurally guaranteed by the per-EP and per-app key
scoping already in place.

### 2.5 Isolation Tiers (planning only — no implementation in current gate)

| Tier | Description | Required before |
|---|---|---|
| Tier 0 | Logical shared — tenant_id filter on every query; tenant-level quotas | First paying customer |
| Tier 1 | Dedicated Temporal task queues per tenant | N tenants (O-07 — unresolved) |
| Tier 2 | Redis key prefix per tenant (`them:{tenantID}:…`) | Explicit decision on O-05 |
| Tier 3 | Separate PG schema/database per tenant | Not scheduled |
| Tier 4 | Fully dedicated stack per tenant | Not scheduled |

---

## 3. Go-Native Lifecycle

### 3.1 context.Context Ownership

Every component receives a context from its caller. No component creates `context.Background()`
except at process scope. The ownership tree is:

```
main.go run() context (cancellable, process lifetime)
  ├── server.ListenAndServe context (bounded by shutdown signal)
  │     └── [per-request] r.Context() — cancelled on connection close
  │           ├── Gate.Check(ctx, ...)         30s deadline via child context
  │           ├── Session.Register(ctx, ...)    no additional timeout
  │           ├── Recorder.CreateRun(ctx, ...)  no additional timeout
  │           └── orchestration context: context.WithCancel(r.Context())
  │                 ├── Orchestrator.Run(ctx, ...)
  │                 │     └── Provider.Stream(ctx, ...)  HTTP cancel on ctx.Done()
  │                 │           └── AgentRegistry.Invoke(ctx, ...)
  │                 └── runstream.Stream(ctx, ...)
  ├── pod-heartbeat goroutine (bounded by run() context — Architecture debt L-2, must fix)
  ├── epconfig.Subscribe goroutine (bounded by run() context — Architecture debt L-3, must fix)
  └── agentRegistry.Subscribe goroutine (bounded by run() context — Architecture debt L-3)
```

**Rule:** any goroutine started in `main.go` must receive the process-level cancellable context
and must terminate on `ctx.Done()`. The pod-heartbeat goroutine currently uses
`context.Background()` — this is Finding L-2, must be fixed before the runtime redesign begins.

### 3.2 Cancellation Propagation

The critical cancellation chain:

```
Client disconnects (WS read error or SSE r.Context() cancelled)
  → cancel() called in streamEvents or on r.Context() cancellation
  → orchestration context is cancelled
  → Provider.Stream() → http.NewRequestWithContext cancelled → LLM HTTP call aborted
  → AgentRegistry.Invoke → HTTP call aborted (if context propagated — verify this)
  → Temporal workflow goroutine: wfRun.Get(ctx, nil) returns on ctx.Done()
  → Redis run-stream XREAD BLOCK returns on ctx cancel
```

**Gap identified:** `internal/agentregistry/registry.go` must propagate context to the outbound
HTTP call to the agent endpoint. If it uses `http.Get` without context, agent calls will not be
cancelled on client disconnect. Verify before implementation begins.

### 3.3 Goroutine Ownership — Rules

1. Every goroutine started by a handler must be bounded by the request context or by
   a `done` channel that is closed when the handler returns.
2. Every goroutine started in `main.go` must be bounded by the process-level context.
3. Goroutines must not be started in `init()` or package-level `var` blocks.
4. No goroutine may hold a Redis or Postgres client reference past the process shutdown sequence.
5. Background goroutines that need cleanup (Subscribe loops) must register a stop function with
   the server's shutdown sequence, not rely on Redis client closure.

### 3.4 Graceful Shutdown

Current shutdown sequence (from `internal/server`):
1. SIGTERM/SIGINT received
2. `httpServer.Shutdown(ctx)` with 5s drain — stops accepting new connections, drains in-flight
3. `database.Close()` and `redisCache.Close()` called via registered Closers

**Architecture debt L-3** (must fix before production): Subscribe goroutines (`epconfig.Subscribe`,
`agentRegistry.Subscribe`) call Redis pub/sub and are not stopped before Redis closes. The fix:
1. Store the stop function returned by Subscribe
2. Call stop functions before registering database and Redis closers
3. Order: stop subscribers → drain HTTP → close DB → close Redis

Shutdown drain timeout must be configurable (default 10s). In-flight requests that are actively
streaming LLM responses need up to the LLM's response time to complete; 5s is insufficient for
long Anthropic responses. Recommended: 30s drain, with active run count metric to observe.

### 3.5 Timeout Hierarchy

```
Process shutdown drain:   30s  (configurable; current 5s is too short for LLM responses)
WebSocket first-message:  30s  (existing, correct)
Gate.Check deadline:      10s  (implied by admission gate — should be explicit)
LLM HTTP request:         No explicit timeout currently — must add; recommend 120s
Agent HTTP call:          Per-agent timeout_seconds (from DB config) — must propagate to ctx
DB query timeout:         No explicit timeout — recommend 5s for CRUD, 30s for history load
Redis operation:          No explicit timeout — recommend 2s
```

Timeouts must be set via `context.WithTimeout` child contexts at each boundary, not via global
HTTP client timeouts (which would affect all requests on a shared client).

### 3.6 Retry Boundaries

| Layer | Retries | Owner |
|---|---|---|
| LLM call | None (Temporal or caller) | Temporal activity retry policy |
| Agent HTTP | None (fail fast, return error text) | AgentInvoker |
| Gate.Check | Via queue (BLPOP compete) | Gate |
| DB query | None (fail fast, propagate error) | Handler |
| Redis operation | None (fail-open on infra errors) | Gate, Session |
| Temporal workflow | MaximumAttempts=1 (orchestrator retries internally) | Workflow |

### 3.7 Cleanup Rules

```go
// Entry point handler cleanup (WS example):
defer conn.Close()
defer cancel()
defer unsub()                                            // event bus unsubscribe
defer sessions.End(ctx, sessionID, epSlug, appID)        // session hash cleanup
defer gateStore.Release(ctx, gateCfg)                    // wake one queued waiter
// Gate SREM + shadow delete happens inside Session.End via Lua SREM+DEL pattern
```

Cleanup must execute even on panics — use `defer` for all resource release. Cleanup must not
fail silently — log errors from `sessions.End` and `gateStore.Release` at Warn level.

---

## 4. Streaming and Backpressure

### 4.1 Event Classification

| Event type | Classification | Delivery path |
|---|---|---|
| `token` (LLM text delta) | Transient | In-process bus OR Redis run-stream |
| `tool_call` | Transient | In-process bus OR Redis run-stream |
| `tool_result` | Transient | In-process bus OR Redis run-stream |
| `agent_status` (state transitions) | Transient | In-process bus only |
| `done` | Terminal — must not be dropped | In-process bus OR Redis run-stream |
| `error` | Terminal — must not be dropped | In-process bus OR Redis run-stream |
| `run_start`, `run_end` | Durable business state | Redis run-stream + DB |
| `iteration_start`, `usage` | Durable business state | Redis run-stream |
| `file` (artifact reference) | Durable business state | In-process bus + DB artifact row |
| `replay_unavailable` | Transient (reconnect notice) | In-process bus |
| Audit rows | Audit | DB (them.audit_logs) |
| Session created/ended | Audit | DB + slog |

### 4.2 Which Events Use Which Path

**In-process bus only (inline orchestrator path):**
- All `token`, `tool_call`, `tool_result`, `agent_status`, `done`, `error` events
- Bounded by 256-event buffer per subscriber
- No persistence — events are lost on process restart

**Redis run-stream (Temporal path, `events_transport=streams`):**
- All events published by Python activity via `stream_publish.lua` (atomic XADD + PUBLISH)
- MAXLEN 2000 (rolling trim)
- Replay via XRANGE from `last_event_id` on reconnect
- Live delivery via XREAD BLOCK

**Redis Pub/Sub only (`events_transport=pubsub`, legacy):**
- All events published to `them:dash:run:{runID}:tokens`
- No replay on reconnect
- Must remain until Phase 11c-D approval

**DB only:**
- `them.runs` status transitions
- `them.run_steps` per agent invocation
- `them.run_usage` per LLM call
- `them.task_messages` per iteration (durable planner state)
- `them.artifacts` per file output

### 4.3 Bounded Buffer Policy

The in-process event bus (`internal/event/bus.go`) uses a 256-event buffer per subscriber.
Publish is non-blocking (`select { case ch <- ev: default: }`). This is the correct policy
for transient events. However:

**Finding L-1 fix required:** Terminal events (`done`, `error`) must not be dropped. When the
buffer is full and a terminal event arrives, the bus must either:
- Block briefly (with a timeout, not indefinitely) to ensure the terminal event is delivered, OR
- Separate terminal events into a dedicated 1-element unbuffered channel that bypasses the main buffer

**Recommended fix:** add a `termCh chan event.Event` (buffer 1) alongside the main channel.
`Publish` checks `ev.Type == "done" || ev.Type == "error"` and routes to `termCh`. The
subscriber drains `termCh` after `evCh` is empty. This is a small, targeted change.

**Slow consumer handling:**
- In-process: drop transient events, never drop terminal events (with fix above)
- Redis run-stream: MAXLEN trim handles slow consumers on replay side; live XREAD does not buffer
- WS write errors: cancel context, close connection, log at Warn

### 4.4 Coalescing and Drop Rules

| Event | May drop? | Coalesce? |
|---|---|---|
| `token` | Yes (buffer full) | No (ordered) |
| `tool_call` | Yes (buffer full) | No |
| `tool_result` | Yes (buffer full) | No |
| `agent_status` | Yes | Yes (only last state per agent) |
| `iteration_start` | Yes | Yes (only keep latest) |
| `usage` | Yes | No |
| `done` | **NEVER** | No |
| `error` | **NEVER** | No |
| `replay_unavailable` | Yes | No |
| Audit rows | **NEVER** | No — write to DB synchronously |

### 4.5 Artifact Delivery

Artifacts with `filename` and `media_type` must be delivered by reference:
1. Agent returns artifact parts with `filename`, `media_type`, and `storage_reference` or inline bytes
2. Go runtime stores artifact in `them.artifacts` row with `id`, `run_id`, `filename`, `media_type`, `data`
3. Emits `{"type":"file","artifact_id":"...","filename":"...","media_type":"..."}` event (not the bytes)
4. Client fetches artifact via `GET /api/v1/runs/{run_id}/artifacts/{artifact_id}`
5. The artifact endpoint streams the bytes from the DB or object store

**Current Python behavior:** embeds file part bytes inline in the WS event when `text` is present.
This works for small text outputs but must not be replicated for binary files. The Go design
must gate on size: if `len(data) < 4096`, inline is acceptable; otherwise, deliver by reference.

---

## 5. Temporal Boundary

### 5.1 What Temporal Handles (approved scope)

- Durable run execution across process restarts
- HITL pause and resume (`submit_human_response` signal)
- Activity heartbeat (every 5s) — proves the activity is alive to Temporal
- Workflow history (Temporal's own history, not the LLM message history)
- Temporal-native retry of the entire `RunOrchestratorActivity` on infrastructure failure

### 5.2 What Must Stay Outside Temporal

| Anti-pattern | Why it's wrong | Where to do it instead |
|---|---|---|
| One activity per LLM token | History explosion; Temporal is not a message bus | In-process streaming, Redis Pub/Sub |
| One activity per agent call | Workflow history bloat; latency per call | Inline in `RunOrchestratorActivity` |
| Session state in Temporal | Sessions are process-local, not durable workflow state | Redis Hash |
| WS message send from activity | Activities can't own connections | Entry point adapter (in-process) |
| Redis key management in workflow | Workflows are deterministic — no Redis | Session/Gate packages |

### 5.3 Temporal Boundary Rule (enforced)

```
OrchestrationWorkflow contains exactly one activity: RunOrchestratorActivity.
RunOrchestratorActivity runs the entire agentic loop (all LLM calls, all agent calls, all iterations).
Temporal sees: one workflow, one activity (or two on HITL resume).
```

The current `internal/temporal/workflow.go` implements this correctly. The Python implementation
matches. No change to this boundary is approved.

### 5.4 Go-Native Temporal Activity (future wave)

When the Python worker is replaced by Go, `RunOrchestratorActivity` will be implemented in Go.
It will call `internal/orchestrator.Orchestrator.Run()` directly, not via HTTP. The activity
registers on the `them-orchestration` task queue. The workflow ID scheme `ctx-{contextID}` must
be preserved for HITL signal compatibility.

The Go activity must:
- Heartbeat every 5s
- Propagate context cancellation to the orchestrator loop
- Return `*ErrTaskInputRequired` as a Temporal ApplicationError type `"TaskInputRequired"`

---

## 6. Performance Targets

**Assumptions are marked (A). Targets require benchmark validation before they become commitments.**

### 6.1 Concurrency

| Metric | Initial target | Assumption | Measurement method |
|---|---|---|---|
| Concurrent connected sessions per replica | 500 | (A) Each session holds ~1.5MB RSS when idle | pprof heap profile at N sessions |
| Active streaming runs per replica | 100 | (A) Each active run holds event bus subscriber + stream goroutine + LLM HTTP connection | pprof goroutine count |
| Max goroutines per active run | 3–5 | (A) orchestrator, streamEvents, clientGone drain, run-stream XREAD | pprof goroutine count |

### 6.2 Latency

| Metric | Target | Assumption |
|---|---|---|
| Gate.Check + Session.Register (admission path) | < 5ms p99 | (A) Redis round-trips on same network |
| First token to client after LLM starts streaming | < 50ms overhead | (A) Go buffer/marshal overhead only |
| Context cancellation propagation (disconnect → LLM cancel) | < 100ms | (A) HTTP cancel + TCP RST |

### 6.3 Memory

| Metric | Target | Assumption |
|---|---|---|
| Memory per idle session | < 2KB heap (Redis Hash is external) | (A) Session struct is small |
| Memory per active streaming run | < 3MB (LLM HTTP body buffer + event channel) | (A) 256-event buffer * ~1KB per event |
| Event bus subscriber buffer | 256 events * avg event size | Measured: avg ~500B per event |

### 6.4 Reliability

| Metric | Target | Assumption |
|---|---|---|
| Graceful shutdown with active runs | < 30s drain | (A) LLM responses complete within 30s |
| Recovery after process restart (Temporal path) | < 5s for Temporal to reassign activity | Temporal default |
| Ghost session cleanup on crash | < 10s (gate shadow TTL = ReservationTTL) | Existing design |

### 6.5 Tenant Fairness

| Metric | Target | Notes |
|---|---|---|
| One tenant's rate-limit exhaustion | Does not affect other tenants | Redis INCR key is scoped per-app |
| One tenant's session cap hit | Does not affect other EPs | EP Set is per-EP-slug |
| Memory monopoly prevention | Requires bounded per-run limits | Not yet enforced — deferred |

---

## 7. Current Code Classification

### 7.1 Reusable As-Is

These components are correct, tested, and require no changes for the runtime redesign.

| Component | Location | Status |
|---|---|---|
| Admission gate (Lua atomic + queue) | `internal/gate/gate.go` | Reusable as-is |
| Session lifecycle (Hash + shadow TTL + Lua End) | `internal/session/session.go` | Reusable as-is |
| Bearer token two-level cache (L1+L2+pub/sub) | `internal/auth/token_cache.go` | Reusable as-is |
| JWT RS256/HS256 validation | `internal/auth/jwt.go` | Reusable as-is |
| EP config loader (DB + Redis cache + pub/sub) | `internal/epconfig/` | Reusable as-is |
| Rate limiter (Redis INCR per-token + per-app) | `internal/ratelimit/limiter.go` | Reusable as-is |
| Run recorder (runs + steps + usage) | `internal/runrecorder/recorder.go` | Reusable as-is |
| Run stream (Pub/Sub + Redis Streams dispatcher) | `internal/runstream/` | Reusable as-is |
| In-process event bus | `internal/event/bus.go` | Reusable after L-1 fix |
| LLM provider interface + Anthropic impl | `internal/llm/` | Reusable as-is |
| A2A agent registry (2-level cache + HTTP) | `internal/agentregistry/registry.go` | Reusable as-is |
| Temporal workflow + HITL | `internal/temporal/workflow.go` | Reusable as-is |
| Temporal Go-native activity stub | `internal/temporal/activities.go` | Reusable as-is |
| Run reconciler | `internal/reconciler/reconciler.go` | Reusable as-is |
| Domain types | `internal/domain/domain.go` | Reusable as-is |
| Config + startup validation | `internal/config/config.go` | Reusable as-is |
| Server + graceful shutdown | `internal/server/server.go` | Reusable after L-3 fix |
| pgxpool wrapper | `internal/db/db.go` | Reusable as-is |
| rueidis wrapper | `internal/cache/cache.go` | Reusable as-is |
| Crypto (Fernet, key derivation) | `internal/crypto/fernet.go` | Reusable as-is |
| Admin CRUD handlers + DAL | `internal/admin/`, `internal/admin/dal/` | Reusable as-is |

**Reusable component count: 25**

### 7.2 Reusable With Refactor

These components are correct in their current scope but need changes to support the full
data-plane runtime.

| Component | Location | Required changes |
|---|---|---|
| WS handler | `internal/ws/handler.go` | Extract shared auth→gate→session→orch flow; fix AppID in sessInfo (T-2); deferred SSE deduplication (A-2) |
| SSE handler | `internal/sse/handler.go` | Same refactor as WS |
| Orchestrator | `internal/orchestrator/orchestrator.go` | Add: budget enforcement (in-memory counter + checkpoint), checkpoint writes (task_messages after each iter), parallel fan-out, memory injection, A2A card TTL, file artifact routing; do NOT add per-iteration DB full-reload |
| Transport interfaces | `internal/transport/transport.go` | Add `TenantID` to types when tenant wave begins |
| Event bus | `internal/event/bus.go` | Fix L-1: terminal event must-deliver guarantee |
| Server shutdown | `internal/server/server.go` | Fix L-3: register Subscribe goroutine stop functions before closing Redis |
| main.go | `cmd/them/main.go` | Fix L-2: pod-heartbeat goroutine must use cancellable context |

**Reusable-with-refactor count: 7**

### 7.3 Temporary Compatibility Layers

These exist to maintain wire-contract compatibility with Python during the migration. They must
be retained until the component they bridge is fully replaced by Go.

| Component | Purpose | Remove when |
|---|---|---|
| `temporal.PythonOrchestrationInput` | Input shape expected by Python `OrchestrationWorkflow` | Python worker replaced by Go |
| Workflow ID scheme `ctx-{contextID}` | Matches Python's workflow ID for HITL signal routing | Python worker replaced by Go |
| Redis Pub/Sub event path | Legacy delivery for `events_transport=pubsub` runs | Phase 11c-D (explicit approval) |
| Python bridge fallback (Traefik priority 100) | Routes non-Go-owned paths to Python | Each route is migrated and cut over |

### 7.4 Go-Native Redesign Required

These areas exist in Python and partially in Go but require a full Go-native design before
they can serve production traffic.

| Component | Current state | Why redesign required |
|---|---|---|
| Full orchestrator (agent fan-out, memory, budgets) | Partial Go skeleton | Missing 8 of 13 Python features (see §1.5 gap table) |
| Task persistence (task_messages, artifacts) | Not in Go | Required for durable planner / multi-turn |
| File artifact delivery | Not in Go | Must deliver by reference (§4.5) |
| Go Temporal activity (replacing Python worker) | Stub only | Activity body not implemented in Go |
| Voice entry point | Placeholder (501) | Audio framing + STT/TTS not designed |
| Tenant foundation (tenant_id columns + middleware) | Not started | Pre-requisite for multi-tenant |
| Prometheus metrics | Not started | Required before production scale-up |

**Redesign-required count: 7**

### 7.5 Remove Instead of Migrate

These Python components should not be ported to Go line-by-line. They either implement
patterns that Go replaces structurally or are bugs that Go has already fixed.

| Python component | Disposition |
|---|---|
| `app/services/session_manager.py` (ghost accumulation) | Go gate + session already correct |
| `app/services/rate_limiter.py` (non-replica-safe) | `internal/ratelimit` already correct |
| `app/routers/ws_orchestrator.py` (no gate, no cap) | `internal/ws` already correct |
| `app/adapters/factory.py` + adapters | Replace with `internal/agentregistry` |
| `app/services/token_cache.py` (single-level cache) | `internal/auth/token_cache.go` already 2-level |
| `app/routers/admin_*.py` routes already migrated | Already removed from the Go routing path |

### 7.6 Frozen Components

These components must not be changed until the architecture gate is approved and the
relevant design decisions are made.

| Component | Frozen until |
|---|---|
| `internal/temporal/workflow.go` and `activities.go` | Go-native Temporal activity design is approved |
| `internal/runstream/` (all stream and dispatcher files) | Phase 11c-D approval gate passes |
| `internal/event/bus.go` | L-1 fix is designed and reviewed |
| `app/temporal/activities.py` (Python worker) | Python worker replacement is designed |
| `theM_gateway/docker-compose.traefik.yml` | Each wave's route block must be isolated; no mass changes |
| `db/001_schema.sql` and migration files | Tenant foundation wave is designed and approved |

---

## 8. Validation Strategy

### 8.1 Benchmark Harness

Before any data-plane component is marked production-ready:

```bash
# Connection concurrency test
# Tool: wrk or hey or a custom Go benchmark
# Test: establish N concurrent WS connections, send one message each, measure:
#   - admission latency (Gate.Check + Session.Register)
#   - first-token latency
#   - peak RSS and goroutine count
# Target: 500 concurrent sessions, < 5ms admission p99

# Load test with mock LLM
# Tool: custom Go harness using MockProvider
# Test: 100 concurrent active runs, 10 iterations each, 10 tool calls per iteration
# Measure: events/second throughput, event bus drop rate, terminal event delivery rate
# Target: 0 terminal event drops

# Slow consumer test
# Tool: custom Go harness
# Test: subscriber reads events at 10 events/second (much slower than emission rate of ~100/s)
# Verify: transient events are dropped, terminal event (done/error) is delivered
```

### 8.2 Race Detector Tests

```bash
go test -race ./...                    # must pass, zero races
go test -race ./internal/ws/...        # WS handler concurrent connections
go test -race ./internal/event/...     # bus subscribe/publish/unsubscribe concurrency
go test -race ./internal/gate/...      # concurrent gate admit/release/rollback
go test -race ./internal/session/...   # concurrent register/end
```

### 8.3 Goroutine Leak Tests

```go
// Use goleak (github.com/uber-go/goleak) in test main to detect leaked goroutines.
// Every test that starts a WS/SSE session must verify:
//   - goroutine count returns to baseline after handler returns
//   - no orphan goroutines waiting on closed channels
```

### 8.4 Cancellation Tests

- WS client disconnects mid-stream → LLM HTTP call is cancelled within 100ms
- Context timeout fires before LLM responds → error event emitted, session cleaned up
- Gate timeout expires while waiting in queue → ErrQueueFull returned, no session registered
- Admin disconnect signal arrives → context cancelled, session ended

### 8.5 Restart / Recovery Tests

- Process restarts while Temporal workflow is running → workflow resumes on new process
- Process restarts while session Hash has TTL > 0 → heartbeat on new process refreshes Hash
- Redis unavailable → gate fails open, session registration fails, 503 returned to client
- DB unavailable → EP config load fails, 503 returned before WS upgrade

### 8.6 Multi-Tenant Isolation Tests

- Tenant Alpha's rate limit exhaustion → Tenant Bravo unaffected (different Redis key)
- Tenant Alpha at EP session cap → Tenant Bravo's EP unaffected (different EP Set)
- Tenant Alpha admin lists runs → only Alpha's runs returned (future, after tenant_id columns)
- Cross-tenant session lookup → 403 not 404

### 8.7 Both-Replica Tests

- Two replicas serving the same EP slug → session counts correctly aggregated via Gate
- Pod heartbeat on replica 1 → session Hash on replica 2 not mistakenly pruned
- Token revocation published by replica 1 → L1 cache on replica 2 evicted within 1 Redis pub/sub RTT

---

## 9. Delivery Plan

Phases are ordered by dependency, not by Python package structure. Each phase must be fully
tested and merged before the next begins.

### Phase R-0 — Architecture Debt Fixes (prerequisite for all data-plane work)

**Scope:** fix the deferred findings from the Go-Native Engineering Gate that affect
correctness or safety of the data-plane.

1. **L-1 fix:** event bus terminal event guarantee — add dedicated `termCh` to InMemoryBus
2. **L-2 fix:** pod-heartbeat goroutine — derive from cancellable process context, not Background()
3. **L-3 fix:** Subscribe goroutines — register stop functions, stop before Redis close
4. **T-2 fix:** `SessionInfo.AppID` — populate in WS and SSE handler session registration
5. **A-2 partial:** extract shared auth→gate→session preamble into `transport.HandleSession`

**Validation:** `go test -race ./...`, goroutine leak tests, shutdown test.
**Output:** no new features. No wire contract changes. Zero test regressions.

**Implementation may begin:** YES — after gate document approval and open decisions resolved.

### Phase R-1 — Orchestrator Feature Parity

**Scope:** bring `internal/orchestrator` to full feature parity with Python `task_runner.py`,
using the in-memory-accumulation-with-durable-checkpoints model (OD-3 decision).

1. **In-memory message accumulation** — message slice built once at run start from DB history
   (existing HistoryLoader with DB LIMIT); appended in-process each iteration; not reloaded from DB
2. **Budget token enforcement** — in-process counter incremented each iteration; checked before
   each LLM call; checkpointed to `tasks.tokens_used` after each iteration
3. **Durable checkpoints** — after each completed LLM iteration: write assistant turn and tool
   results to `them.task_messages`; this is the recovery boundary
4. **Parallel tool fan-out** — `sync.WaitGroup` + per-agent semaphore + global parallel semaphore;
   orchestrator owns the goroutines
5. **A2A agent card auto-discovery** — `_ensure_agent_skills` equivalent; Redis TTL cache
6. **Per-iteration token usage recording** — DB `run_usage` row + in-process `tasks.tokens_used`
7. **Child task row creation** — `them.tasks` delegated row per agent call
8. **Memory injection** — inline in orchestrator loop after N agent calls; controlled by
   `memory_enabled` flag on orchestrator config

**Validation:** behavioral equivalence tests against Python task_runner. Multi-turn context test.
Budget enforcement test. Parallel fan-out correctness test.

**Implementation may begin:** after Phase R-0 is complete.

### Phase R-2 — File Artifact Delivery

**Scope:** deliver file artifacts by reference instead of inline in the event stream.

1. Artifact DB row creation in `them.artifacts` (Go analog of `context_service.record_and_cache_artifact`)
2. `{"type":"file","artifact_id":"...","filename":"...","media_type":"..."}` event emission
3. `GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` endpoint (new Go route)
4. Size gate: inline if `len(data) < 4096`, by-reference otherwise

**Validation:** integration test — agent returns file artifact, client fetches it via URL.

**Implementation may begin:** after Phase R-1 is complete.

### Phase R-3 — Go Temporal Activity

**Scope:** implement `RunOrchestratorActivity` in Go so the Python worker can be removed.

1. Go Temporal activity registered on `them-orchestration` task queue
2. Activity calls `orchestrator.Orchestrator.Run()` directly
3. Heartbeat every 5s via `workflow.RecordHeartbeat()`
4. `ErrTaskInputRequired` returned as `temporal.ApplicationError{Type: "TaskInputRequired"}`
5. Python worker runs in parallel until Go activity is validated under load
6. Task queue name constant `"them-orchestration"` — shared with Python during transition

**Validation:** Go activity handles HITL workflow. Temporal workflow history clean. Python worker disabled.

**Implementation may begin:** after Phase R-2 is complete and approved.

### Phase R-4 — Tenant Foundation

**Scope:** add `tenant_id` columns, backfill, and propagate through all layers.

This is a **dedicated, isolated wave** as mandated by D-14 in `TENANT_FOUNDATION_DECISIONS.md`.
It must not be mixed with any other migration wave.

1. Schema migration: `tenant_id UUID NOT NULL` on `applications`, `agents`, `orchestrators`,
   `access_tokens`, `runs`, `audit_logs`
2. Backfill all existing rows with `default-tenant` UUID
3. Auth middleware: resolve `TenantID` from JWT claim or bearer token lookup
4. DAL: all admin queries gain `tenantID` parameter and `WHERE tenant_id = $n`
5. Session: `SessionInfo.TenantID` populated (T-1 debt)
6. Run recorder: `CreateRun` gains `tenant_id` parameter
7. Cross-tenant 403 enforcement

**Implementation may begin:** after Phase R-3 is complete AND open decisions O-01 through
O-08 (from TENANT_FOUNDATION_DECISIONS.md) are resolved.

### Phase R-5 — Observability

**Scope:** Prometheus metrics, structured audit log, trace propagation.

1. Prometheus metrics for: admission decisions, active sessions, active runs, event throughput,
   event bus drop rate, LLM call latency, agent call latency
2. `them.audit_logs` writes for: session created/ended, run created/completed, admin mutations
3. OpenTelemetry trace propagation (deferred — requires provider decision)

**Implementation may begin:** after Phase R-0 is complete (metrics can begin independently).

### 9.1 Compatibility Boundary

Python bridge (`them-bridge`) continues to serve all routes not yet owned by Go. This is the
existing Traefik priority model (Python at 100, Go at 110+). Each wave cuts over one route group
atomically. The compatibility boundary shrinks monotonically — no route ever moves back.

### 9.2 Cutover and Rollback

Every wave follows the existing pattern:
1. Implement Go handler + tests
2. Register route in `cmd/them/main.go`
3. Add Traefik router block in `docker-compose.yml` and `theM_gateway/docker-compose.traefik.yml`
4. Live smoke test: confirm request reaches Go bridge via logs
5. Python sanity suite: confirm Python bridge passes all existing tests
6. Rollback: remove Traefik block, recreate Go bridge container → Python serves immediately

---

## 10. Blocking Decisions — Resolved

The original gate document listed two inconsistent blocker sets: `{OD-1, OD-3, OD-7, OD-8}`
in one place and `{OD-1, OD-2, OD-7, OD-8}` in another. The correct set of Phase R-0
blockers is **{OD-1, OD-2, OD-7, OD-8}**. OD-3 blocks Phase R-1, not Phase R-0.

All four Phase R-0 blocking decisions are resolved below. OD-3 through OD-6 are also
resolved here so that Phase R-1 implementation may proceed immediately after Phase R-0.

---

### OD-1 — Terminal event must-deliver guarantee (blocks Phase R-0 / L-1 fix)

**Decision: separate `termCh chan event.Event` with buffer 1.**

**Selected option:** add a `termCh chan event.Event` (capacity 1) alongside the existing
`evCh` (capacity 256) in each `InMemoryBus` subscriber record. `Publish` routes to `termCh`
when `ev.Type == "done" || ev.Type == "error"`. The subscriber drains `termCh` in a
post-`evCh` select case. Publish to `termCh` is a non-blocking send with `select/default`
— if `termCh` already holds an event (i.e., two terminal events published), the second is
silently discarded. This is correct: only one terminal event per run should ever be published,
and if two arrive, the first is the authoritative one.

**Rationale:** the blocking-send-with-timeout option (50ms) would hold the publish mutex for
up to 50ms, stalling all other subscribers. The priority-flag option requires every consumer
to change its read loop. The `termCh` approach requires only two changes: the `Subscribe`
return type (add `termCh`) and the `streamEvents` select (add a `termCh` case). Zero other
call sites change.

**Failure behavior:** if the subscriber goroutine exits before draining `termCh`, the channel
is garbage-collected. This is safe — the connection is already gone.

**Test required:** `TestBus_TerminalEventDeliveredOnFullBuffer` — fill the 256-event buffer
with transient events, then publish `done`; subscriber must receive `done` even though
`evCh` was full at publish time.

**Blocks Phase R-0:** YES.

---

### OD-2 — ApplicationID and TenantID in SessionInfo (blocks Phase R-0 / T-2 fix)

**Decision: populate both ApplicationID and TenantID in SessionInfo in Phase R-0; no Redis
migration required for existing sessions.**

**Selected option:** in `ws/handler.go` and `sse/handler.go`, after `epconfig.Load` resolves
`EPConfig`, set `sessInfo.AppID = resolvedCfg.AppID` and `sessInfo.TenantID = resolvedCfg.TenantID`
before calling `sessions.Register`. The `SessionInfo` struct gains both fields. The Redis Hash
serialization adds two new fields; existing Hash entries without them simply will not have
the field — when a session is looked up and `AppID` is absent, it is treated as empty string
(not an error). The shadow TTL is 90s; all pre-migration sessions expire naturally within 90s.
No Lua migration script is needed.

**Rationale:** per the corrected §2 tenant boundary, the five-element RuntimeIdentity
(`tenant_id`, `application_id`, `user_id`, `session_id`, `run_id`) must be fully populated
in the session at creation. Adding AppID without TenantID is a half-fix; both must be added
in the same commit.

**Failure behavior:** if `resolvedCfg` is nil (EP config loader not wired — test paths),
both fields remain empty string. This is safe for tests; production always has EP config wired.

**Test required:** `TestSession_AppIDAndTenantIDPopulated` — register a session with a
mock EPConfig; verify the Redis Hash contains `app_id` and `tenant_id` fields.

**Blocks Phase R-0:** YES.

---

### OD-3 — Orchestration state model (blocks Phase R-1)

**Decision: in-memory accumulation with durable per-iteration checkpoints.**

**Analysis:**

| Criterion | Full DB rebuild each iteration | In-memory + checkpoints | Event-sourced / hybrid |
|---|---|---|---|
| Latency per LLM call | Adds 1 DB round-trip per iteration (5–30ms) | Zero per-iteration DB reads | Zero per-iteration DB reads |
| DB load | Linear with iteration count × concurrent runs | One write per iteration per run | One write per iteration per run |
| Crash recovery | Full rebuild from DB; safe but slow | Rebuild to last checkpoint; then Temporal resumes activity | Same as in-memory + checkpoints |
| Multi-replica behavior | Each replica reads the same DB state; correct but expensive | Each replica keeps its own in-memory slice for its active runs; correct — Temporal owns durability | Same as in-memory + checkpoints |
| Reconnect behavior | New connection loads full history from DB | New connection loads history from DB (existing HistoryLoader with LIMIT) — identical to full rebuild at run start | Same |
| Tenant isolation | Full rebuild does not cross tenant boundaries (WHERE clauses) | In-memory slice is process-local; cannot cross tenants | Same |
| Implementation complexity | Lowest — mirrors Python directly | Low — add checkpoint writes; rebuild only at run start or after crash | High — requires event log schema and replay |

**Why not full DB rebuild:**
- Python does this because Python has no choice: async Python shares an event loop across all
  connections, meaning a single slow DB read delays all concurrent runs. In Go, each run has
  its own goroutine — the serialization problem does not exist. The DB rebuild in Python is
  a workaround for Python's concurrency model, not a desirable property.
- At 100 concurrent active runs, each averaging 10 iterations and 5 agent calls, full DB
  rebuild generates approximately 1000 additional DB round-trips per minute that carry no
  new information (the messages were just written in the previous iteration).
- The DB rebuild only helps if a process crashes mid-iteration. Temporal already handles this:
  the activity is re-executed from the beginning, and the HistoryLoader reads from DB at that
  point — exactly when a DB read is genuinely needed.

**Why not event-sourced:**
- Adds a schema dependency (event log table) and replay logic that is not needed. The
  existing `task_messages` table already serves as the checkpoint log. No new schema required.

**Selected model — in-memory accumulation with durable checkpoints:**

```
Run start:
  → Load history from DB (HistoryLoader with LIMIT = history_window)
  → Initialize in-memory messages slice

Each LLM iteration:
  → Append user message to messages slice (already done)
  → Call Provider.Stream() with in-memory messages slice
  → Accumulate response in-process
  → CHECKPOINT: write assistant turn to task_messages (durable)
  → Fan out tool calls (in parallel goroutines)
  → CHECKPOINT: write tool results to task_messages (durable)
  → Append results to in-memory messages slice
  → Update tokens_used on tasks row (in-process counter → DB write)

On crash (Temporal re-executes the activity):
  → HistoryLoader reads task_messages from DB up to the last checkpoint
  → Reconstructs messages slice from checkpoints
  → Run continues from the last complete iteration

On reconnect (new WS/SSE connection, same context_id):
  → Same as crash recovery: load from DB, Temporal workflow is still running
  → Redis run-stream provides replay of already-emitted events via XRANGE
```

**Failure behavior:** if a checkpoint write fails (DB unavailable), the iteration still
completes in-process. The next crash recovery will replay from the previous successful
checkpoint. Token usage may be slightly under-counted if the `tasks` row write fails;
this is acceptable (it is not a billing inconsistency, just a partial count).

**Test required:** `TestOrchestrator_CheckpointRecovery` — simulate a crash after iteration 2
of a 5-iteration run; restart; verify the recovered run reads the correct checkpoint history
from DB and continues from iteration 3 without re-calling the LLM for iterations 1–2.

**Blocks Phase R-1:** YES.

---

### OD-4 — Parallel agent fan-out (blocks Phase R-1)

**Decision: orchestrator owns goroutines via `sync.WaitGroup` + semaphores.**

**Selected option:** for each batch of tool calls in one iteration, spawn one goroutine
per tool call. The orchestrator controls two semaphores: one global `parallel_sem`
(bounded by `max_parallel_tools`) and one per-agent `agent_sem` (bounded by the agent's
`max_concurrency`). Both must be acquired before an agent HTTP call begins. Goroutines
write results into a pre-allocated `[]result` slice (indexed by position, not by append)
— no mutex needed on the result slice. `sync.WaitGroup.Wait()` is called after all
goroutines are started.

**Rationale:** `AgentInvoker.Invoke` is already synchronous — making it async would push
concurrency complexity into the registry, which should not own scheduling. The orchestrator
already knows the full tool-call batch; it is the correct owner of fan-out.

**Failure behavior:** if one agent goroutine fails, its error is recorded in the result
slice. Other goroutines continue. After `WaitGroup.Wait()`, failed results are reported
back to the LLM as tool_result errors (matching Python's behavior).

**Test required:** `TestOrchestrator_ParallelFanOut` — submit a batch of 5 tool calls
with `max_parallel_tools=2`; verify at most 2 agent HTTP calls are in-flight at any time;
verify all 5 results are returned.

**Blocks Phase R-1:** YES.

---

### OD-5 — Memory injection placement (blocks Phase R-1)

**Decision: inline in orchestrator loop.**

**Selected option:** memory injection (context summarization) runs inside the orchestrator
loop after N agent calls, controlled by `memory_enabled` and `summarize_every_n_calls` on
the orchestrator config. The orchestrator calls a `MemoryStore.Inject(ctx, contextID)` 
interface method if `memory_enabled` is true and the threshold is reached. This keeps the
summarization trigger co-located with the agentic loop state that drives it.

**Rationale:** a middleware/interceptor approach would require the loop to be restructured
around a hook-chain, adding significant complexity for one optional feature.

**Blocks Phase R-1:** YES.

---

### OD-6 — Artifact storage backend (blocks Phase R-2)

**Decision: DB BYTEA column with 1MB hard limit; object store deferred.**

**Selected option:** `them.artifacts.data BYTEA` with a hard limit of 1MB enforced at the
Go service layer before the INSERT. Artifacts larger than 1MB return an error to the
orchestrator; the agent call result records an error in the tool result. Object store
integration (S3/MinIO) is a future wave, not blocked on this gate.

**Failure behavior:** oversized artifact → service returns `ErrArtifactTooLarge`; handler
maps to 413; orchestrator records tool error text.

**Blocks Phase R-2:** YES.

---

### OD-7 — Shutdown drain timeout (blocks Phase R-0)

**Decision: increase from 5s to 30s.**

**Selected option:** set the `httpServer.Shutdown` context timeout to 30s. Make it
configurable via `SHUTDOWN_DRAIN_SECONDS` env var (default 30, minimum 5). Add a
`them:bridge:active_runs:{instanceID}` key that the pod-heartbeat loop publishes — Kubernetes
`preStop` hooks can poll this to gate process termination until active runs drop to zero
or 30s elapses.

**Rationale:** Anthropic claude-3-5-sonnet responses can take 20–60s for long outputs. A
5s drain forces active LLM streams to be cancelled, breaking the client's run. 30s covers
the large majority of responses.

**Failure behavior:** if a run does not complete within 30s, the process exits and Temporal
re-executes the activity on another instance. The client may see a disconnect; on reconnect
the run-stream replay provides missed events if using `events_transport=streams`.

**Test required:** `TestServer_GracefulShutdownWith30sDrain` — start a mock LLM that delays
25s; issue SIGTERM; verify the response completes before the server exits.

**Blocks Phase R-0:** YES.

---

### OD-8 — Agent HTTP context propagation (blocks Phase R-0 verification)

**Decision: RESOLVED — no change required. Context is already correctly propagated.**

Inspection of `internal/agentregistry/registry.go`:
- `invokeA2A` (line 236): `http.NewRequestWithContext(ctx, http.MethodPost, ...)` — correct
- `invokeHTTP` (line 293): `http.NewRequestWithContext(ctx, http.MethodPost, ...)` — correct
- `httpClient.Timeout = 60s` (line 68) — provides a per-call ceiling independent of context

Context cancellation from a client disconnect propagates to in-flight agent HTTP calls.
No code change is required. This item is closed.

**Blocks Phase R-0:** NO (verified clean).

---

## 11. Recommended Target Runtime Architecture

The Go runtime is a **layered, context-owned, interface-bounded monolith** with Temporal for
durability and Redis for coordination:

```
┌──────────────────────────────────────────────────────────────┐
│  Entry Point Adapters (WS / SSE / A2A)                       │
│  - Transport upgrade, auth extraction, wire format           │
├──────────────────────────────────────────────────────────────┤
│  Admission Layer (Gate + RateLimit)                          │
│  - Atomic Lua: ghost-prune, cap check, SADD, rate-limit      │
│  - Queue: BLPOP compete, not guarantee                       │
├──────────────────────────────────────────────────────────────┤
│  Session Runtime                                             │
│  - Redis Hash + shadow TTL + heartbeat                       │
│  - Bounded by request context                                │
├──────────────────────────────────────────────────────────────┤
│  Run Runtime                                                 │
│  - CreateRun → subscribe BEFORE workflow start               │
│  - events_transport: pubsub | streams                        │
├──────────────────────────────────────────────────────────────┤
│  Orchestration / Decision Layer                              │
│  - Agentic loop: DB-rebuild per iteration, budget guard,     │
│    parallel fan-out, memory injection, task_messages persist │
├──────────────────────────────────────────────────────────────┤
│  Execution Engine (LLM / A2A / Tool)                         │
│  - Provider.Stream(): context-cancelled HTTP                 │
│  - AgentRegistry.Invoke(): 2-level cache + HTTP              │
├──────────────────────────────────────────────────────────────┤
│  Streaming Pipeline                                          │
│  - In-process bus (inline path, 256+1 term buffer)           │
│  - Redis Streams (Temporal path, XRANGE replay + XREAD)      │
├──────────────────────────────────────────────────────────────┤
│  Temporal (durability boundary)                              │
│  - One workflow, one activity per run                        │
│  - HITL via Signal                                           │
├──────────────────────────────────────────────────────────────┤
│  PostgreSQL (persistence)  │  Redis (coordination/cache)     │
│  - runs, tasks, messages   │  - session, gate, rate-limit    │
│  - artifacts, audit        │  - token cache, orch cache      │
└──────────────────────────────────────────────────────────────┘
```

Each layer communicates only with the layer directly below it via typed Go interfaces. No
layer reaches across. Context flows down; events flow up through the streaming pipeline.
The tenant boundary is the application, enforced at every layer once tenant_id columns exist.

---

## 12. Summary Tables

### 12.1 Decisions — All Resolved

| # | Decision | Resolution | Blocks |
|---|---|---|---|
| OD-1 | Terminal event delivery mechanism | **Separate `termCh chan event.Event` (buffer 1)** | Phase R-0 |
| OD-2 | AppID + TenantID in SessionInfo | **Populate both from resolvedCfg in WS/SSE handlers** | Phase R-0 |
| OD-3 | Orchestration state model | **In-memory accumulation with per-iteration checkpoints** (not DB rebuild) | Phase R-1 |
| OD-4 | Parallel fan-out ownership | **Orchestrator owns goroutines via WaitGroup + dual semaphore** | Phase R-1 |
| OD-5 | Memory injection placement | **Inline in orchestrator loop** | Phase R-1 |
| OD-6 | Artifact storage backend | **DB BYTEA with 1MB limit; object store deferred** | Phase R-2 |
| OD-7 | Shutdown drain timeout | **Increase to 30s; configurable via SHUTDOWN_DRAIN_SECONDS** | Phase R-0 |
| OD-8 | Agent HTTP context propagation | **RESOLVED — context already correctly propagated in both invokeA2A and invokeHTTP** | ~~Phase R-0~~ (closed) |

All eight decisions are resolved. See §10 for full rationale, failure behaviors, and required tests.
Full decision summary: `docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md`.

### 12.2 Reusable Components (25, as-is)

gate, session, auth (JWT + token cache), epconfig, ratelimit, runrecorder, runstream,
event bus (after L-1 fix), llm, agentregistry, temporal workflow, reconciler, domain,
config, server (after L-3 fix), db, cache, crypto, admin handlers + DAL (all waves)

### 12.3 Redesign Required (7)

Full orchestrator (8 feature gaps), task persistence, file artifact delivery,
Go Temporal activity, voice entry point, tenant foundation, Prometheus metrics

### 12.4 Frozen Components

- `internal/temporal/workflow.go` and `activities.go` — until Go-native Temporal activity design approved
- `internal/runstream/` — until Phase 11c-D approval
- `internal/event/bus.go` — until L-1 fix design is reviewed
- `app/temporal/activities.py` — until Python worker replacement designed
- `theM_gateway/docker-compose.traefik.yml` — mass changes blocked; per-wave blocks only
- `db/001_schema.sql` and migration files — until tenant foundation wave designed

### 12.5 Initial Performance Targets

| Metric | Target | Assumption |
|---|---|---|
| Concurrent sessions per replica | 500 | Benchmark required |
| Active streaming runs per replica | 100 | Benchmark required |
| Admission latency (p99) | < 5ms | Redis on same network |
| Cancellation propagation | < 100ms | HTTP cancel |
| Memory per idle session | < 2KB | Session struct is small |
| Memory per active run | < 3MB | Event buffer + HTTP body |
| Shutdown drain | < 30s | LLM response bound |

### 12.6 Exact Next Implementation Task

**Phase R-0 Architecture Debt Fixes** — all decisions are resolved; implementation may begin
in a fresh Sonnet session. Single focused commit set:

1. **OD-1 / L-1**: Add `termCh chan event.Event` (buffer 1) to each subscriber record in
   `internal/event/bus.go`; route `done`/`error` events there; add `termCh` case to
   `streamEvents` select in `ws/handler.go` and `sse/handler.go`
2. **OD-2 / T-1 / T-2**: Populate `AppID` + `TenantID` in `SessionInfo` from `resolvedCfg`
   in both `ws/handler.go` and `sse/handler.go`
3. **OD-7**: Increase shutdown drain from 5s to 30s; add `SHUTDOWN_DRAIN_SECONDS` env var
4. **L-2**: Fix `internal/health/health.go` heartbeat goroutine to use a derived context, not
   `context.Background()`
5. **L-3**: Stop Subscribe goroutines before Redis close in `internal/auth/token_cache.go`
6. Run `go test -race ./...` — must pass
7. Run goroutine leak test — must pass
8. Run Python sanity suite `01 02 03 04 15` — zero regressions
9. Commit all in one commit; update `TEST_INDEX.md` in the same commit

### 12.7 Whether Implementation May Begin

**YES — Phase R-0 implementation may begin.**

All blocking decisions (OD-1, OD-2, OD-7, OD-8) are resolved:
- OD-8 is verified clean — no code change required
- OD-1: `termCh` approach selected — see §10
- OD-2: populate both AppID + TenantID in SessionInfo — see §10
- OD-7: increase to 30s — see §10

The architecture gate is satisfied. Phase R-0 implementation is authorized.

**Gate for Phase R-1** (orchestrator redesign) remains closed until Phase R-0 is complete
and `go test -race ./...` passes.

**Gate for Phase R-2 through R-5** remains closed until Phase R-1 is complete.

Start implementation in a fresh Sonnet session. Do not mix architecture review and
implementation in the same session.
