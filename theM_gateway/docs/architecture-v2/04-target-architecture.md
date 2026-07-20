# Phase 4 "” Target Architecture (Go Monolith)

---

## 4.1 Package Structure

The Go binary lives in `cmd/them/main.go`. All business logic lives under `internal/`. The tree below is the authoritative package inventory.

```
cmd/
  them/
    main.go              "” wires everything, calls server.Run()

internal/
  config/               "” configuration
  server/               "” HTTP/WS server lifecycle
  auth/                 "” JWT + bearer token validation
  session/              "” Redis session lifecycle
  gate/                 "” runtime gate (rate limit, session cap, queue)
  registry/             "” agent + orchestrator registry
  middleware/           "” middleware pipeline
  edge/                 "” entry-point adapters (WS, SSE, A2A, REST, Voice, WebRTC)
  orchestration/        "” Temporal workflow + activity definitions
  agent/                "” agent adapter (A2A, sub-orchestrator)
  llm/                  "” LLM provider abstraction + Anthropic + OpenAI impls
  history/              "” conversation history (DB read/write, pagination)
  db/                   "” Postgres connection pool + query layer
  cache/                "” Redis client wrapper + pub/sub hub
  event/                "” internal event bus (dashboard, invalidation, control)
  crypto/               "” encryption/decryption helpers for stored secrets
  audit/                "” audit log writer
  health/               "” liveness + readiness probes
  telemetry/            "” OTel + Prometheus initialization
  migrate/              "” SQL migration runner
  pii/                  "” PII guard scanner
  worker/               "” Temporal worker registration + lifecycle
```

### Per-package detail

**`internal/config`**
- Responsibility: Loads all env vars, validates required secrets at startup, exposes a single immutable `Config` struct.
- Exports: `Config`, `Load() (*Config, error)`
- Must NOT import: anything else in `internal/` (zero-dep leaf)
- Why separate: all other packages depend on it; keeping it leaf prevents circular imports

**`internal/server`**
- Responsibility: Constructs the HTTP router (chi), registers all route handlers, manages graceful shutdown.
- Exports: `Server`, `New(deps) *Server`, `Run(ctx) error`
- Must NOT import: `orchestration`, `agent`, `llm` directly "” only their interfaces defined in the domain packages
- Why separate: the HTTP wiring layer must be independently testable and replaceable

**`internal/auth`**
- Responsibility: Validates JWTs locally (RS256, no HTTP call), validates bearer tokens (L1 in-process + L2 Redis + DB fallback), broadcasts token invalidation via Redis pub/sub.
- Exports: `Validator`, `JWTClaims`, `BearerPayload`, `ErrExpired`, `ErrRevoked`, `ErrInvalid`
- Must NOT import: `edge`, `orchestration`, `middleware`
- Why separate: auth is a cross-cutting security concern; isolating it makes key-rotation and cache logic auditable independently

**`internal/session`**
- Responsibility: Redis-backed session Hash lifecycle (register, end, touch, heartbeat, admin disconnect signal). Owns the session Hash (`them:sess:{id}`) and shadow TTL keys. Also owns SREM on End (paired atomically with shadow key deletion).
- Exports: `Store`, `SessionInfo`, `Register()`, `End()`, `Touch()`, `Get()`, `WriteHeartbeat()`
- Must NOT import: `auth`, `gate`, `orchestration`
- Why separate: the TTL-mismatch and ghost-session bugs require a clean atomic redesign isolated from business logic
- **Ownership boundary**: SessionManager writes the Hash and manages shadow TTL keys. It does NOT perform the admission-time SADD — that belongs to gate (see below). SessionManager does own SREM on End, because cleanup must be symmetric with whoever did the SADD.

**`internal/gate`**
- Responsibility: Sole owner of Set membership (`them:ep:*:sessions`, `them:app:*:sessions`) at admission time. Enforces runtime limits (rate per user, session cap per EP, queue-wait-and-retry) using a single atomic Lua script: cap check → rate limit INCR → SADD membership Sets → SET shadow TTL keys — all in one Redis round-trip. No partial state is possible: either all succeed or the connection is rejected.
- Exports: `Gate`, `GateResult`, `ErrCapExceeded`, `ErrRateLimited`, `ErrQueueFull`
- Must NOT import: `session`, `orchestration`
- Why separate: the Lua script logic and queue semantics are complex enough to own independently; they are also the primary source of correctness bugs; sole ownership of Set membership eliminates the duplicate-SADD failure window

**`internal/registry`**
- Responsibility: Two-level cache (L1 in-process `sync.Map`, L2 Redis) for agents and orchestrators; subscribes to `them:agents:changed` for invalidation; maps `AppOrchestrator` rows as pseudo-agents for sub-orchestrator delegation.
- Exports: `Registry`, `AgentRecord`, `OrchestratorRecord`, `GetAgents()`, `GetOrchestrator()`, `Invalidate()`
- Must NOT import: `orchestration`, `edge`, `llm`
- Why separate: the two-level cache with pub/sub invalidation is a distinct caching concern

**`internal/middleware`**
- Responsibility: Loads per-app/per-agent middleware wiring from DB/cache; executes the pipeline (PII guard, rate-limit augment, logging, custom transforms) for each LLM invocation.
- Exports: `Pipeline`, `MiddlewareDef`, `Run(ctx, req) (req, error)`, `Middleware` interface
- Must NOT import: `orchestration`, `llm` (imports `llm` interfaces only, not implementations)
- Why separate: the per-invocation pipeline resolution with Redis short-circuit is a distinct optimization target

**`internal/edge`**
- Responsibility: Per-protocol adapters that translate protocol-specific I/O into a uniform `RunRequest` / `EventEmitter` pair consumed by `orchestration`.
- Sub-packages: `edge/ws`, `edge/sse`, `edge/a2a`, `edge/rest`, `edge/voice`, `edge/webrtc`
- Exports: `Adapter` interface, `RunRequest`, `EventEmitter` interface
- Must NOT import: `orchestration` internals (only the `OrchestrationEngine` interface)
- Why separate: each protocol has distinct framing, error codes, and backpressure semantics

**`internal/orchestration`**
- Responsibility: Temporal workflow definition (`OrchestrationWorkflow`), all activity implementations, and the bridge client that starts/queries/cancels workflows from edge handlers.
- Exports: `Engine` interface, `BridgeClient`, `WorkflowInput`, `WorkflowHandle`
- Must NOT import: `edge` (the orchestration layer is protocol-agnostic)
- Why separate: Temporal SDK imports and workflow serialization constraints must be contained

**`internal/agent`**
- Responsibility: A2A async adapter (submit â†’ poll/SSE), connection pool per agent, semaphore-based concurrency limiting, sub-orchestrator delegation adapter.
- Exports: `Adapter` interface, `A2AAdapter`, `SubOrchestratorAdapter`, `InvocationResult`
- Must NOT import: `orchestration` internals, `edge`
- Why separate: agent I/O (HTTP connection pools, polling loops, SSE parsing) is complex enough to own independently

**`internal/llm`**
- Responsibility: Typed LLM provider interface; Anthropic and OpenAI-compat implementations; canonical message format; streaming with context cancellation.
- Sub-packages: `llm/anthropic`, `llm/openai`
- Exports: `Provider` interface, `Message`, `ToolCall`, `ToolResult`, `StreamEvent`, `NeutralTool`
- Must NOT import: `orchestration`, `edge`, `agent`
- Why separate: provider implementations carry SDK dependencies that must not pollute the rest of the codebase

**`internal/history`**
- Responsibility: Reads and writes conversation history (`them.task_messages`) with DB-level cursor pagination; translates between DB rows and canonical `llm.Message` format.
- Exports: `Store`, `Page`, `LoadPage()`, `Append()`, `TrimToWindow()`
- Must NOT import: `llm` implementations (only `llm` types), `orchestration`
- Why separate: the O(n) full-scan bug fix requires a clean pagination contract; isolating the history layer makes it testable with a DB stub

**`internal/db`**
- Responsibility: `pgx/v5` connection pool, query helpers, transaction helpers; owns the `them` schema namespace.
- Exports: `Pool`, `Open()`, `Close()`, `Tx()`, query-typed result types
- Must NOT import: anything in `internal/` except `config`
- Why separate: the DB layer must be a zero-business-logic infrastructure leaf

**`internal/cache`**
- Responsibility: `rueidis` Redis client wrapper; pub/sub hub with channel-scoped subscription management; pipeline helpers.
- Exports: `Client`, `PubSubHub`, `Subscribe()`, `Publish()`, `Pipeline()`
- Must NOT import: anything in `internal/` except `config`
- Why separate: all pub/sub logic must route through a single hub to share connections and avoid the N-subscriptions-per-module anti-pattern

**`internal/event`**
- Responsibility: Typed internal event bus bridging Redis pub/sub channels to in-process Go channel consumers (dashboard WS, admin disconnect, token invalidation, agent registry invalidation).
- Exports: `Bus`, `Event`, `EventType`, `Subscribe()`, `Publish()`
- Must NOT import: `orchestration`, `edge`, `auth` (uses interfaces only)
- Why separate: decouples producers from consumers without forcing direct Redis pub/sub awareness on every package

**`internal/crypto`**
- Responsibility: AES-256-GCM encrypt/decrypt for stored secrets (agent auth tokens, LLM API keys); key derivation from `SECRET_KEY`.
- Exports: `Encrypt(plaintext, key) ([]byte, error)`, `Decrypt(ciphertext, key) ([]byte, error)`
- Must NOT import: anything in `internal/`
- Why separate: cryptographic primitives must be auditable in isolation

**`internal/audit`**
- Responsibility: Writes to `them.audit_logs` table asynchronously via a buffered channel; does not block the request path.
- Exports: `Logger`, `Log(ctx, entry AuditEntry)`
- Must NOT import: `orchestration`, `edge`
- Why separate: async audit writes with buffering require their own goroutine lifecycle

**`internal/health`**
- Responsibility: Exposes `/healthz` (liveness) and `/readyz` (readiness) endpoints with per-dependency status checks.
- Exports: `Handler`, `DependencyChecker` interface
- Must NOT import: `orchestration`, `edge`, `agent`
- Why separate: health logic must start and pass before other components accept traffic

**`internal/telemetry`**
- Responsibility: Initializes OTel tracer/meter providers, Prometheus exporter, structured logger; exports span helpers and metric instruments.
- Exports: `Init()`, `Shutdown()`, `Tracer()`, `Meter()`, `Logger()`
- Must NOT import: anything in `internal/` except `config`
- Why separate: OTel SDK initialization is global and must happen before any other package runs

**`internal/migrate`**
- Responsibility: Runs `goose` (or `atlas`) SQL migrations at startup; blocks readiness until complete.
- Exports: `Run(ctx, pool) error`
- Must NOT import: anything in `internal/` except `db`, `config`
- Why separate: migration must be an explicit, auditable step in the startup sequence

**`internal/pii`**
- Responsibility: Scans LLM request and response bodies for PII patterns (regex + optional model-based); redacts or blocks based on configured policy.
- Exports: `Scanner`, `ScanResult`, `Scan(text) ScanResult`
- Must NOT import: `llm`, `orchestration`
- Why separate: PII patterns and redaction policy are independently configurable and testable

**`internal/worker`**
- Responsibility: Registers all Temporal activities and the workflow with the Temporal worker; owns worker lifecycle.
- Exports: `Worker`, `Start(ctx, temporalClient, deps) (*Worker, error)`, `Stop()`
- Must NOT import: `edge`, `server`
- Why separate: Temporal worker and HTTP server have separate lifecycles and must drain independently

---

## 4.2 Major Interfaces

### LLMProvider

```
Name: llm.Provider

Methods:
  Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
  Stream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
  CountTokens(ctx context.Context, req ChatRequest) (int, error)
  Name() string
  DefaultModel() string

Purpose:
  Abstracts over Anthropic and OpenAI-compat APIs. ChatRequest carries
  typed Messages (not raw dicts), typed Tools (NeutralTool structs),
  and cache_control hints. StreamEvent is a discriminated union
  (token | tool_use_start | tool_use_delta | tool_use_end | done | error).
  context.Context cancellation MUST be propagated to the underlying HTTP
  request so in-flight streaming calls abort when the client disconnects.
```

### AgentAdapter

```
Name: agent.Adapter

Methods:
  Invoke(ctx context.Context, req InvocationRequest) (<-chan AdapterEvent, error)
  AgentID() uuid.UUID
  Transport() string

Purpose:
  Abstracts over A2A remote agents and sub-orchestrator delegations.
  InvocationRequest carries the canonical message parts, context_id,
  and budget. AdapterEvent is a discriminated union
  (token | tool_start | tool_done | artifact | status | done | error | input_required).
  Cancellation via ctx must propagate to the remote A2A call or Temporal
  sub-workflow cancellation.
```

### EdgeAdapter

```
Name: edge.Adapter

Methods:
  Receive(ctx context.Context) (*RunRequest, error)
  Emit(ctx context.Context, event Event) error
  Protocol() string
  Close(ctx context.Context) error

Purpose:
  Translates protocol-specific I/O (WebSocket frames, SSE writes, A2A
  JSON-RPC responses, HTTP response bodies) into the uniform RunRequest
  for the orchestration engine and back out. Each edge adapter owns
  backpressure and partial-failure semantics for its protocol.
```

### Middleware

```
Name: middleware.Middleware

Methods:
  Name() string
  Process(ctx context.Context, req *MiddlewareRequest) (*MiddlewareRequest, error)

Purpose:
  A single step in the per-invocation middleware pipeline. MiddlewareRequest
  wraps the LLM ChatRequest with metadata (app_id, agent_id, user_id).
  Implementations: PII guard, rate-limit augment, audit logger, custom
  transform. The pipeline short-circuits (skips Redis load) when the
  wiring table is empty for the given (app_id, agent_id) pair.
```

### OrchestrationEngine

```
Name: orchestration.Engine

Methods:
  Start(ctx context.Context, req WorkflowInput) (*WorkflowHandle, error)
  Cancel(ctx context.Context, workflowID string) error
  StreamEvents(ctx context.Context, handle *WorkflowHandle, emit func(Event) error) error
  SendSignal(ctx context.Context, workflowID string, signal Signal) error

Purpose:
  Entry-point adapters use this interface to start and interact with
  Temporal workflows without importing Temporal SDK types directly.
  WorkflowHandle carries workflow_id and the Redis pub/sub channel name
  used for real-time event streaming.
```

### SessionStore

```
Name: session.Store

Methods:
  Register(ctx context.Context, info SessionInfo) error
  End(ctx context.Context, sessionID uuid.UUID, epSlug *string, appID *uuid.UUID) error
  Touch(ctx context.Context, sessionID uuid.UUID) error
  Get(ctx context.Context, sessionID uuid.UUID) (*SessionInfo, error)
  CountEP(ctx context.Context, epSlug string) (int, error)
  CountApp(ctx context.Context, appID uuid.UUID) (int, error)
  SetActiveAgent(ctx context.Context, sessionID uuid.UUID, agentSlug string) error
  ClearActiveAgent(ctx context.Context, sessionID uuid.UUID) error
  WriteHeartbeat(ctx context.Context, podID string, sessions int) error

Purpose:
  All Redis session CRUD. WriteHeartbeat writes the REAL active session
  count (not 0) using an atomic counter maintained by Register/End.
```

### TokenStore

```
Name: auth.TokenStore

Methods:
  ValidateBearer(ctx context.Context, rawToken string) (*BearerPayload, error)
  ValidateJWT(token string) (*JWTClaims, error)
  Invalidate(ctx context.Context, tokenHash string) error
  InvalidateUser(ctx context.Context, userID int64) error
  RotatePublicKeys(keys []rsa.PublicKey) error

Purpose:
  Bearer tokens: L1 in-process sync.Map (TTL enforced via stored expiry
  timestamp) â†’ L2 Redis them:token:{hash} (TTL 300s) â†’ DB lookup.
  JWT validation is purely local RS256 "” no HTTP call.
  Invalidate() writes to L2 and publishes them:token:revoked channel
  so other pods flush L1 immediately.
```

---

## 4.3 Runtime Model

### Startup Sequence

The following steps are strictly ordered. Each step is a dependency gate "” later steps do not begin until the earlier step returns without error.

1. **`config.Load()`** "” parse env vars, validate all required fields, assert no `change-this-in-production` defaults. Fatal exit on any validation failure.

2. **`telemetry.Init()`** "” initialize OTel tracer and meter providers, Prometheus registry, structured logger (`slog` with JSON handler). All subsequent log calls use the initialized logger.

3. **`db.Open()`** "” open `pgx/v5` pool (min 5, max 20 connections), ping with 10s timeout. Fatal exit if Postgres unreachable.

4. **`cache.Open()`** "” connect `rueidis` client, ping Redis. Fatal exit if Redis unreachable.

5. **`migrate.Run()`** "” run pending SQL migrations. Fatal exit on migration failure. This gate ensures the schema is current before any query runs.

6. **`auth.Init()`** "” load RS256 public keys from DB or config-mounted PEM. Fatal exit if no valid key found. Start background key-rotation watcher goroutine.

7. **`registry.Init()`** "” warm L1 cache from Redis (or DB on cold start). Start change-listener goroutine subscribing to `them:agents:changed`.

8. **`worker.Start()`** "” connect Temporal client, register workflow and all activities, start Temporal worker goroutine. Fatal exit on Temporal connect failure.

9. **`event.Bus.Start()`** "” start the internal event bus, subscribe to all Redis pub/sub channels (`them:token:revoked`, `them:agents:changed`, `them:dash:*`).

10. **`server.Start()`** "” bind HTTP listener. Health `/healthz` returns 200 immediately. Readiness `/readyz` returns 200 only after this step completes. Readiness check verifies DB ping, Redis ping, and Temporal worker status.

### Health Check Strategy

**Liveness (`GET /healthz`)**: Returns 200 as soon as the HTTP server is bound. It does not check dependencies. A failing liveness probe means the process is deadlocked or the HTTP server is down "” Kubernetes will restart the pod.

**Readiness (`GET /readyz`)**: Returns 200 only when ALL dependency checks pass:
- Postgres: `SELECT 1` with 2s timeout
- Redis: PING with 1s timeout
- Temporal: worker reports `Running` state
- Migration: no pending migrations

Returns 503 with a JSON body listing which dependencies are unhealthy. The pod is removed from the load balancer until all checks pass.

**Key distinction**: Readiness failing does NOT restart the pod. It removes it from traffic. This is the correct behavior during Temporal outages or Redis flaps.

### Graceful Shutdown Sequence

Triggered by `SIGTERM` or `SIGINT`. A `context.WithTimeout(30s)` governs the entire drain.

1. **Stop accepting new connections**: HTTP server calls `Shutdown(ctx)` "” existing connections drain, new ones are refused (Traefik health check fails within one probe cycle).
2. **Close WebSocket entry points**: broadcast `{"type": "draining"}` to all active WS clients, then close gracefully with code 1001.
3. **Wait for active sessions to end or timeout**: poll `session.Store.CountAll()` every 500ms. After 20s, proceed regardless.
4. **Stop Temporal worker**: `worker.Stop()` "” the Go SDK drains in-flight activities before stopping. Activities have their own `context.Context` which is cancelled at this point.
5. **Flush telemetry**: `telemetry.Shutdown()` "” flush OTel spans and Prometheus metrics.
6. **Close DB and Redis**: `db.Close()`, `cache.Close()`.

### Goroutine Model

**Long-lived goroutines** (started at startup, cancelled via root context):
- `auth.keyRotationWatcher` "” polls for JWKS rotation every 5 minutes
- `registry.changeListener` "” subscribes to `them:agents:changed` pub/sub
- `event.Bus.dispatcher` "” fan-out from Redis pub/sub to in-process subscribers
- `session.heartbeatLoop` "” writes pod heartbeat every 15s
- `session.reaperLoop` "” scans `them.tasks` for deadline violations every 60s
- `worker.temporalWorker` "” Temporal Go SDK worker (manages its own goroutine pool internally)
- `server.appLivenessLoop` "” probes enabled entry-point URLs every 30s

**Per-request goroutines** (scoped to request lifetime, cancelled when request context is cancelled):
- `edge/ws: cancelListener` "” reads WebSocket frames looking for `{"type":"cancel"}`
- `edge/ws: controlListener` "” subscribes to `them:sess:control:{session_id}` for admin disconnect
- `orchestration: streamEvents` "” reads from Redis pub/sub channel for the workflow's run events
- `agent: pollLoop` "” polls A2A `GetTask` until terminal state (per agent invocation)
- `llm: streamReader` "” reads SSE from LLM provider, sends to channel (per LLM call)

---

## 4.4 Session Model

### Redis Key Structure

The existing Python key structure is preserved for coexistence, with one additive change (the pod session counter).

```
them:sess:{session_id}           Hash   TTL=90s, refreshed by Touch()
  Fields: instance_id, user_id, orchestrator_name, ep_slug,
          app_id, context_id, started_at, last_active_at,
          active_agent (optional)

them:ep:{ep_slug}:sessions       Set    no TTL "” member = session_id string
them:app:{app_id}:sessions       Set    no TTL "” member = session_id string

them:pod:{pod_id}                Hash   TTL=30s (refreshed every 15s)
  Fields: instance_id, started_at, session_count (NEW "” atomic integer)

them:pods                        Set    no TTL "” member = pod_id string
them:pod:{pod_id}:sess_count     String TTL=30s, value = integer (NEW)
                                 Authoritative session count for this pod.
                                 Incremented by Register(), decremented by End()
                                 using INCR/DECR (atomic, no race).

them:sess:control:{session_id}   Pub/Sub channel (no stored key)
```

### Atomic Session Entry (Lua script: `REGISTER_SESSION`)

`session.Store.Register()` writes the Hash only. **It does NOT touch Set membership.** Set membership is written exclusively by `Gate.Check()` before `Register()` is ever called (see Gate/Session Ownership below).

```lua
-- KEYS: [1]=sess_key, [2]=pod_count_key
-- ARGV: [1]=session_id, [2]=sess_ttl, [3]=pod_count_ttl, [4...]=field-value pairs for HSET

-- 1. Write the session hash with TTL
redis.call('HSET', KEYS[1], unpack(ARGV, 4))
redis.call('EXPIRE', KEYS[1], ARGV[2])

-- 2. Increment pod session counter atomically
redis.call('INCR', KEYS[2])
redis.call('EXPIRE', KEYS[2], ARGV[3])

return 1
```

### Atomic Session Exit (Lua script: `END_SESSION`)

`session.Store.End()` deletes the Hash and removes from the Set index. The SREM is here because cleanup must be symmetric with whoever did the SADD (Gate). Gate.Release() is called separately by the edge handler to wake any queued waiters.

```lua
-- KEYS: [1]=sess_key, [2]=ep_set (or ""), [3]=app_set (or ""), [4]=pod_count_key,
--       [5]=ep_shadow (or ""), [6]=app_shadow (or "")
-- ARGV: [1]=session_id

-- 1. Remove the session hash
redis.call('DEL', KEYS[1])

-- 2. Remove from EP index and delete EP shadow
if KEYS[2] ~= "" then
  redis.call('SREM', KEYS[2], ARGV[1])
end
if KEYS[5] ~= "" then
  redis.call('DEL', KEYS[5])
end

-- 3. Remove from App index and delete App shadow
if KEYS[3] ~= "" then
  redis.call('SREM', KEYS[3], ARGV[1])
end
if KEYS[6] ~= "" then
  redis.call('DEL', KEYS[6])
end

-- 4. Decrement pod counter, floor at 0
local count = tonumber(redis.call('GET', KEYS[4])) or 0
if count > 0 then
  redis.call('DECR', KEYS[4])
end

return 1
```

### Gate/Session Ownership and Caller Contract

**Ownership boundary:**
- `Gate` (`internal/gate`) is the **sole owner** of Set membership at admission time: SADD on Check, SREM on Rollback/End (via luaAdmit and rollbackScript Lua).
- `SessionManager` (`internal/session`) owns the Hash (`them:sess:{id}`) and the shadow TTL extension (Confirm). It also does SREM on End (symmetric with Gate's SADD) and DELs the shadow key.

**Three-step caller contract** (enforced in `internal/ws/handler.go` and `internal/sse/handler.go`):

```go
// Step 1: Gate.Check() — atomic Lua: ghost prune → cap check → rate limit INCR
//         → SADD EP set + app set → SET shadow EX ReservationTTL (10s)
res, err := gate.Check(ctx, cfg)
if err != nil { /* reject */ return }

// Step 2: session.Register() — writes Hash only, no Set writes
err = session.Register(ctx, info)
if err != nil {
    gate.Rollback(ctx, cfg)   // SREM + DEL shadow + LPush "1"
    return
}

// Step 3: Gate.Confirm() — extends shadow from 10s to ShadowTTL (90s)
gate.Confirm(ctx, cfg)

defer func() {
    session.End(ctx, ...)    // DEL Hash + SREM + DEL shadow
    gate.Release(ctx, cfg)   // LPush "1" — wakes one queued waiter
}()
```

**Reservation TTL:** If the process crashes between Check and Confirm, the shadow key expires in ≤10s. The next admission attempt prunes the ghost automatically via `luaAdmit`. No manual intervention required.

### Session Capacity Enforcement and Queue Protocol

`Gate.Check()` runs the `luaAdmit` Lua script before any Hash is written:
1. Prunes ghost sessions (Set members whose shadow key has expired)
2. Checks `SCARD them:ep:{slug}:sessions` against `ep_max_concurrent`
3. Checks `SCARD them:app:{app_id}:sessions` against `app_max_concurrent`
4. Checks rate limit via INCR `rl:them:token:{hash}:{minute}`
5. On all checks pass: SADD both Sets + SET shadow keys EX `ReservationTTL` (10s)

If at EP capacity and `QueueTimeout > 0`: `Gate.Check()` blocks on `BLPop(them:ep:gate:queue:{slug}, QueueTimeout)`. On wake-up, `luaAdmit` runs again from scratch — **this is a compete, not a guaranteed slot.** If a concurrent session took the slot first, `ErrCapExceeded` is returned immediately with no re-queue.

`Gate.Release()` calls `LPush(them:ep:gate:queue:{slug}, "1")` to wake exactly one waiter. It is called on session end and on Rollback. The queue key is a pure signal channel — no session IDs are ever pushed.

### Pod Heartbeat with Real Session Count

The `session.heartbeatLoop` goroutine (every 15s) reads `them:pod:{pod_id}:sess_count` (the atomic counter) and writes it to `them:pod:{pod_id}` Hash field `session_count`. This fixes the Python bug where `write_pod_heartbeat()` always wrote `0` (the Python implementation did not read from any counter).

### Multi-Pod Session Visibility

Any pod can enumerate active sessions across all pods via:
1. `SMEMBERS them:pods` â†’ list of all pod IDs
2. For each pod_id: `HGET them:pod:{pod_id} session_count` â†’ per-pod count
3. `SUNION them:ep:{slug}:sessions them:app:{app_id}:sessions` â†’ session IDs (cross-pod visibility, Set members survive pod death until `End()` is called or the TTL-expired session hash is detected by a reaper sweep)

Session hash TTL of 90s is the dead-session detection window. The reaper loop removes Set members whose hash has expired.

---

## 4.5 Auth Model

> **Two distinct validation paths — do not conflate them:**
>
> | Token type | Source | Validation method |
> |---|---|---|
> | **JWT** (user session tokens issued by auth service) | `Authorization: Bearer eyJ...` (three-part base64) | Local RS256 signature verification — no network hop. `JWTMiddleware` in `internal/auth/middleware.go`. |
> | **Opaque bearer token** (API access tokens in `them.access_tokens`) | `Authorization: Bearer them_...` (opaque string) | L1 in-process `sync.Map` → L2 Redis `them:token:{sha256}` → PostgreSQL `them.access_tokens`. `BearerMiddleware` in `internal/auth/middleware.go`. |
>
> RS256 is a JWT signing algorithm. It does NOT apply to opaque bearer tokens. The bearer token validation path never performs any cryptographic signature check — it is a three-level cache lookup against the database.

### JWT: Local RS256 Validation

No HTTP call on the hot path. The `auth.Init()` step loads the RS256 public key(s) at startup.

**Key loading**: Public keys are loaded from:
1. `AUTH_PUBLIC_KEY_PEM` env var (inline PEM) "” preferred for Kubernetes secrets mount
2. `AUTH_PUBLIC_KEYS_DB_KEY` config key in `them.config` table (JWKS JSON) "” for dynamic rotation without restart

**Validation logic** (in-process, no network):
1. Parse JWT header to identify `kid`
2. Select matching public key from the in-process `[]rsa.PublicKey` slice by `kid`
3. Verify RS256 signature
4. Check `exp`, `iat`, `nbf` claims
5. Check revocation: look up `jti` in Redis `them:token:revoked:{jti}` (SET with `exp`-relative TTL). Cache miss = not revoked (no network call on the common path).

**Key rotation**: The `auth.keyRotationWatcher` goroutine polls the `them.config` table every 5 minutes. On change, it atomically replaces the in-process key slice (`sync.RWMutex`). No restart required.

### Bearer Tokens: Cache Design

Three-layer lookup (same as Python, but typed):

**L1**: In-process `sync.Map[string, cachedBearer]` where `cachedBearer` holds `BearerPayload` + `expiresAt time.Time`. Entries expire passively (checked on read). Capacity-bounded: if L1 > 10,000 entries, evict 10% LRU. This bounds memory usage on pods handling many unique tokens.

**L2**: Redis `them:token:{sha256(rawToken)}` (string, JSON-encoded `BearerPayload`, TTL 300s).

**L3**: DB lookup against `them.access_tokens` where `token_hash = sha256(rawToken) AND enabled = true AND (expires_at IS NULL OR expires_at > now())`. Updates `last_used_at` asynchronously.

On hit at any layer: update TTL of lower layers.

### Invalidation Broadcast

When an admin revokes a token (`DELETE /api/v1/tokens/{id}`):
1. Write `them:token:revoked:{jti}` (for JWT) or delete `them:token:{hash}` (for bearer) from Redis
2. Publish `them:token:revoked` channel: `{"token_hash": "...", "user_id": N}`
3. All pods subscribe via `event.Bus` and flush their L1 `sync.Map` entry

This closes the 5-minute revocation window (currently a pod holding an L1 cache entry will continue accepting a revoked token for up to 5 minutes).

### Admin Role Enforcement

JWT `role` claim is checked in-process after local RS256 validation:
```
role IN ("admin", "superadmin", "super_admin") â†’ admin access granted
```
No separate admin-check HTTP call. The role is embedded in the signed JWT.

### MCP Tokens

MCP tokens are opaque bearer tokens stored in `them.access_tokens` with a `mcp:` prefix in the `label` field. They go through the identical three-layer bearer validation path. The `mcp:` prefix is cosmetic/namespace only "” no special validation logic. This preserves wire-format compatibility with the Python implementation.

### Startup Key Loading

`auth.Init()` is a hard startup gate (step 6 in the startup sequence). If neither `AUTH_PUBLIC_KEY_PEM` nor the DB key is found, the process exits with a fatal error and a clear message. There is no fallback to a weak default.

---

## 4.6 Orchestration / Runtime Model

### Temporal Workflow Structure (Go SDK)

**`OrchestrationWorkflow`** (`temporal.io/sdk/workflow`)

The workflow is a Go function registered with `workflow.RegisterWithOptions`. It is deterministic: all non-deterministic operations (DB reads, LLM calls, agent calls) are activities.

**Workflow input** (`WorkflowInput`):
```
OrchestratorName  string
UserMessage       string
UserID            int64
ContextID         uuid.UUID
SessionID         uuid.UUID
TokenPayload      json.RawMessage
HistoryWindow     int
RunID             uuid.UUID (pre-allocated by edge layer, returned in "ready" event)
EventChannel      string    (Redis pub/sub channel name: them:run:{context_id}:events)
```

### Activity Definitions

| Activity | Side effects | Retries | Timeout |
|---|---|---|---|
| `LoadContextActivity` | DB read (paginated history) | 3, backoff | 10s |
| `InitRunActivity` | DB write (Run row) | 3, backoff | 10s |
| `PlanTurnActivity` | LLM call (streaming) | 1 (non-idempotent) | 120s |
| `InvokeAgentActivity` | A2A HTTP or sub-orch Temporal | 2, backoff | 300s |
| `RecordToolResultsActivity` | DB write (RunStep, TaskMessage) | 3, backoff | 15s |
| `SummarizeContextActivity` | LLM call (compact history) | 1 | 60s |
| `FinalizeRunActivity` | DB write (Run status, cost) | 3, backoff | 15s |
| `PublishEventActivity` | Redis PUBLISH | 5, immediate | 2s |

**`PlanTurnActivity`** streams from the LLM provider. The activity uses `activity.RecordHeartbeat()` on each streamed token chunk to keep the Temporal heartbeat alive and enable cancellation detection. The activity returns only when the stream is fully consumed (all tool_use blocks collected).

**`InvokeAgentActivity`** invokes a single agent. For A2A: submits the task, then polls or streams SSE. For sub-orchestrators: calls `BridgeClient.Start()` for a child workflow and waits for its `done` event. Cancellation via `ctx.Done()` cancels the remote A2A task (sends `CancelTask` JSON-RPC) or cancels the child workflow.

### Context/History Management

**Canonical message format** (`llm.Message`):
```go
type Message struct {
    Role    Role           // user | assistant | tool_result
    Parts   []Part         // discriminated union: TextPart | ToolUsePart | ToolResultPart | DataPart
    Seq     int            // monotonic sequence number
    Created time.Time
}
```

Messages are stored in `them.task_messages` as `(task_id, seq, role, parts JSONB)`. The JSONB `parts` column stores the canonical format "” never provider-specific format.

**DB pagination**: `LoadContextActivity` loads history using a keyset-paginated query:
```sql
SELECT seq, role, parts FROM them.task_messages
WHERE task_id = $1 AND seq > $2
ORDER BY seq ASC LIMIT $3
```
The activity loads `history_window` messages (default 20) counting backwards from the most recent `seq`. On subsequent turns (continue_as_new), only the window tail is loaded "” no full scan.

### Budget and Token Tracking

- `PlanTurnActivity` returns `TokensIn`, `TokensOut` from the LLM response metadata.
- The workflow accumulates `TotalTokensIn`, `TotalTokensOut`, `TotalCostUSD` across iterations.
- Before each `PlanTurnActivity` call: `CountTokens()` is called with the current context window. If estimated tokens + `budget_tokens` would exceed the model's context window, `SummarizeContextActivity` runs first.
- `daily_budget_usd` is checked at `InitRunActivity` time against the Redis `them:budget:{orchestrator_id}:{date}` counter. Exceeded budget returns an immediate error.

### HITL Signal Handling

The workflow registers a signal channel:
```go
signalCh := workflow.GetSignalChannel(ctx, "human_response")
```

When `PlanTurnActivity` returns `InputRequired: true`, the workflow sets its status to `awaiting_input` and blocks on `signalCh.Receive()`. The `OrchestrationEngine.SendSignal()` method (called from the `/api/v1/runs/{id}/respond` REST endpoint) sends the signal to the waiting workflow.

### Sub-Orchestrator Delegation

When `InvokeAgentActivity` processes an agent with `kind = "sub_orchestrator"`, it calls `workflow.ExecuteChildWorkflow()` (not a separate Temporal workflow start via HTTP "” it uses the Go SDK's child workflow API). The child workflow result is awaited. This eliminates the Python pattern of starting a separate workflow via the bridge client for sub-orchestrators.

### continue_as_new Strategy

After every 20 iterations (configurable via `max_iterations_before_continue`), the workflow calls `workflow.NewContinueAsNewError()` with updated `WorkflowInput`. The new input carries:
- `HistorySinceSeq int` "” the seq number of the last persisted message (the new run starts by loading only messages after this seq)
- `AccumulatedTokens`, `AccumulatedCostUSD` "” carried forward for budget tracking

This caps Temporal workflow history size at ~20 iterations per workflow run, preventing the unbounded event-history growth that would eventually cause Temporal to reject the workflow.

### Cancellation Propagation

Every activity receives a `context.Context`. When the Temporal workflow is cancelled (via `cancel_workflow()` from the edge layer):
1. Temporal SDK cancels the activity's context
2. `PlanTurnActivity` detects `ctx.Done()` during streaming and calls the LLM client's `CancelStream()` method, which closes the underlying HTTP request
3. `InvokeAgentActivity` sends `CancelTask` to the A2A agent and returns
4. `FinalizeRunActivity` writes `status = "cancelled"` to the DB

The full cancellation path: WebSocket `{"type":"cancel"}` â†’ `cancel_workflow()` â†’ Temporal cancels workflow â†’ activity `ctx.Done()` â†’ HTTP request cancelled â†’ LLM connection closed.

---

## 4.7 Agent Runtime

### Agent Registry Design

Two-level cache with write-through invalidation:

**L1**: `sync.Map[string, agentCacheEntry]` where key is agent slug, value is `AgentRecord + loadedAt time.Time`. Entries older than 10 minutes are considered stale and trigger a background refresh (serve stale, refresh async "” never blocks the hot path).

**L2**: Redis `them:agents:registry` (Hash, field = slug, value = JSON-encoded `AgentRecord`, TTL = 600s).

**Invalidation**: On any agent CRUD (create/update/delete) via admin API, publish `them:agents:changed` with payload `{"slug": "..."}`. The `registry.changeListener` goroutine receives the signal, deletes the specific L1 entry and the L2 Hash field, then does a lazy DB re-fetch on next access.

**AppOrchestrator pseudo-agents**: `AppOrchestrator` rows are loaded into the registry with a synthesized slug `app_orch:{node_id}` and kind `sub_orchestrator`. They are updated when the applications admin API publishes `them:agents:changed`.

### A2A Adapter Connection Pool

The Python implementation creates a new `httpx.AsyncClient` per adapter instantiation (per agent invocation). The Go implementation uses a shared `*http.Client` per agent (stored in the registry `AgentRecord`), configured with:
- `MaxIdleConnsPerHost: 10`
- `IdleConnTimeout: 90s`
- `TLSHandshakeTimeout: 10s`
- Per-agent `timeout_seconds` set as `http.Client.Timeout`

The shared client is initialized once when the `AgentRecord` is first loaded into L1 cache and stored on the record. On invalidation, the old client is closed (`CloseIdleConnections()`) after a 5s drain delay.

### Concurrent Agent Invocation Limits

Each `AgentRecord` carries a `*semaphore.Weighted` (from `golang.org/x/sync/semaphore`) initialized with `agent.max_concurrency`. `InvokeAgentActivity` acquires the semaphore before dispatching and releases it in a deferred call.

If `max_concurrency` is 0 (unlimited): no semaphore is used.

The semaphore lives on the L1 cache entry. On invalidation, the old semaphore is drained (wait for all holders to release) before the entry is replaced, preventing goroutine leaks.

### Agent Card Refresh Strategy

Agent cards (`agent_card JSONB`) are loaded as part of the `AgentRecord`. The A2A spec allows cards to change at `/.well-known/agent.json`. The registry refreshes the agent card by re-fetching from the agent's endpoint every 60 minutes (background goroutine per registered A2A agent). If the fetch fails, the stale card is retained. Card changes are applied to L1 only (not written back to DB unless an admin explicitly triggers a sync via the admin API).

### Error Taxonomy

**Retryable errors** (Temporal activity retries):
- HTTP 429 (rate limit) "” backoff exponential, up to 3 retries
- HTTP 502/503/504 (gateway/service unavailable) "” backoff, up to 3 retries
- Connection timeout "” immediate retry once, then backoff
- A2A `task.state = FAILED` with `retryable: true` in the error message

**Fatal errors** (no retry, workflow enters error state):
- HTTP 400/422 (bad request "” agent rejected the input)
- A2A `task.state = REJECTED` or `CANCELED` (explicit rejection)
- Budget exceeded
- Context too long (LLM `context_length_exceeded` error)
- Content filter (LLM content policy violation)
- Semaphore acquire timeout (agent at max concurrency for > 30s)

---

## 4.8 LLM Provider Design

### Typed Interface (no dict-based NeutralTool)

```go
// NeutralTool is a typed struct, NOT map[string]interface{}
type NeutralTool struct {
    Name        string          // snake_case, matches agent skill name
    Description string
    InputSchema json.RawMessage // JSON Schema object, validated at registration time
    CacheHint   CacheControlType // ephemeral | persistent | none
}

type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []NeutralTool
    MaxTokens   int
    Temperature *float32
    System      string
    CacheHints  []CacheHint   // index positions where cache_control should be injected
}
```

`NeutralTool.InputSchema` is validated as a JSON Schema at agent registration time (not at invocation time). Invalid schemas are rejected at the admin API layer before they reach any LLM call.

### Canonical Message Format Stored in DB

`them.task_messages.parts` (JSONB) stores messages in the canonical `[]Part` format:

```go
type Part interface { partType() string }

type TextPart       struct { Text   string `json:"text"` }
type ToolUsePart    struct { ID string; Name string; Input json.RawMessage }
type ToolResultPart struct { ToolUseID string; Content []Part; IsError bool }
type DataPart       struct { MimeType string; Data json.RawMessage }
```

Provider-specific format conversion (Anthropic `content_block` arrays, OpenAI `tool_calls` arrays) happens in `llm/anthropic` and `llm/openai` respectively "” never stored. On load from DB, the canonical format is translated to the target provider's format.

### Streaming with Context Cancellation

`Provider.Stream()` returns `<-chan StreamEvent`. The implementation:
1. Opens an HTTP POST to the provider's API with `stream: true`
2. Starts a goroutine that reads SSE lines and sends to the channel
3. When `ctx.Done()` fires, the goroutine calls `resp.Body.Close()` (which aborts the HTTP request) and closes the channel with a `StreamEvent{Type: Cancelled}`

The channel is buffered (size 16) to decouple the SSE reader goroutine from the consumer without blocking on slow consumers. The channel is never leaked: the goroutine always closes it in a deferred call, whether the stream completes normally, is cancelled, or errors.

### Error Classification

```go
type LLMErrorClass int
const (
    ErrTransient         LLMErrorClass = iota // HTTP 5xx, connection reset "” retry
    ErrRateLimit                              // HTTP 429 "” retry with backoff
    ErrContextTooLong                         // HTTP 400 + "context_length_exceeded" "” no retry, must compact
    ErrContentFilter                          // HTTP 400 + content_policy "” no retry, fatal
    ErrAuthFailure                            // HTTP 401/403 "” fatal, alert ops
    ErrInvalidRequest                         // HTTP 400 other "” fatal
)
```

The `anthropic` package maps Anthropic error JSON `{"type": "error", "error": {"type": "..."}}` to these classes. The `openai` package maps OpenAI error JSON `{"error": {"code": "..."}}`.

### Anthropic cache_control Placement Strategy

`cache_control` blocks are injected by the `PlanTurnActivity` at the Anthropic-provider translation layer, not stored in DB:

1. **System prompt**: always `ephemeral` (the system prompt is large and stable within a run)
2. **Tool definitions**: `ephemeral` on the last tool in the tools array (Anthropic caches the full tools block)
3. **History breakpoint**: `ephemeral` on the last message of the loaded history window (marks the stable prefix that won't change on the next turn)

The placement is recalculated fresh on every LLM call "” not persisted. This ensures correct cache_control positions as the conversation grows.

### Token Pre-flight Check

Before `PlanTurnActivity` calls `Provider.Stream()`, it calls `Provider.CountTokens()` with the assembled `ChatRequest`. If `estimated_tokens > model_context_window * 0.85`, `SummarizeContextActivity` is triggered first. This prevents the `context_length_exceeded` error mid-stream.

`CountTokens()` uses Anthropic's `/v1/messages/count_tokens` endpoint (a separate, non-streaming call). For OpenAI-compat providers without a count endpoint, the implementation uses a tiktoken approximation.

---

## 4.9 Event Model

### Redis Pub/Sub: What Stays, What Changes

| Channel | Direction | Purpose | Change |
|---|---|---|---|
| `them:run:{context_id}:events` | Temporal â†’ edge | Run event streaming (tokens, tool_start, done) | No change |
| `them:agents:changed` | admin API â†’ registry | Cache invalidation | No change |
| `them:sess:control:{session_id}` | admin API â†’ WS edge | Admin disconnect | No change |
| `them:dash:apps` | liveness loop â†’ dashboard | App reachability metrics | No change |
| `them:dash:runs` | Temporal activities â†’ dashboard | Run lifecycle events | No change |
| `them:token:revoked` | auth admin API â†’ auth | Token invalidation broadcast | **NEW** |

### Channel Naming Conventions

- `them:{domain}:{entity_id}:{action}` "” entity-scoped event channels
- `them:{domain}:{type}` "” broadcast channels (all pods listen)
- All channels prefixed `them:` to share Redis DB 0 with session/cache keys without collision

### Dashboard Event Model

Unchanged from Python. The dashboard WebSocket handler (`edge/ws/dashboard.go`) subscribes to:
- `them:dash:apps` "” application liveness
- `them:dash:runs` "” run lifecycle events
- `them:dash:agents` "” agent registry change notifications

The `event.Bus` holds a single pub/sub connection to Redis and fan-outs to registered in-process subscribers via Go channels. Multiple dashboard WebSocket clients share a single Redis subscription (not N subscriptions for N clients).

### Admin Disconnect Signal

`signal_disconnect()` (Python) becomes `session.Store.SignalDisconnect()` in Go. It publishes `"terminate"` to `them:sess:control:{session_id}`. The WS edge layer's `controlListener` goroutine receives this and triggers workflow cancellation.

### Token Invalidation Broadcast (NEW)

When `auth.TokenStore.Invalidate()` is called:
1. Delete `them:token:{hash}` from Redis
2. Publish `{"token_hash": "...", "user_id": N}` to `them:token:revoked`
3. All pods' `event.Bus` dispatcher delivers this to `auth.revokedListener`, which deletes the entry from L1 `sync.Map`

This closes the revocation window from 5 minutes (L1 TTL expiry) to sub-second (pub/sub delivery latency).

### What SHOULD NOT Use Pub/Sub

- **LLM streaming token delivery**: tokens flow through the already-established `them:run:{context_id}:events` channel, which is per-run. This is fine. Do NOT create a separate channel per LLM provider stream.
- **Session heartbeat**: heartbeat writes directly to `them:pod:{pod_id}` "” no pub/sub needed.
- **DB query results**: never route DB results through pub/sub. Use direct DB access in activities.
- **Middleware pipeline state**: pipeline execution is in-process, per request. No pub/sub.
- **Rate limit counters**: Redis INCR/DECR operations directly. No pub/sub.

---

## 4.10 Security Model

### Secret Validation at Startup

`config.Load()` validates every secret field:
- `SECRET_KEY`: must be present, length >= 32 bytes, must NOT equal any of: `change-this-in-production`, `secret`, `changeme`, `your-secret-key`, `replace-me`
- `DATABASE_PASSWORD`: must be non-empty
- `ANTHROPIC_API_KEY`: must start with `sk-ant-` if provided
- JWKS / public key: at least one valid RS256 key must be loadable

Validation failure: `log.Fatal()` with a clear message listing which fields failed. The binary exits with code 1 before binding any ports. This prevents misconfigured pods from accepting traffic.

### CORS Configuration

`CORS_ORIGINS` env var is a comma-separated list of allowed origins. Wildcard `*` is rejected in non-development configurations (detected by `APP_ENV != "development"`). Chi's CORS middleware is configured with:
- `AllowedOrigins`: from config
- `AllowedMethods`: GET, POST, PUT, DELETE, OPTIONS
- `AllowedHeaders`: Authorization, Content-Type, X-Request-ID
- `MaxAge`: 300 (5-minute preflight cache)

### PII Guard Improvements

The Python PII guard (in `app.middleware`) scans request bodies only. The Go implementation adds response-side scanning.

**False-positive reduction**: the regex set is tiered:
- **High-confidence patterns** (always redact): SSN `\d{3}-\d{2}-\d{4}`, credit card (Luhn-validated), full US phone with country code
- **Medium-confidence patterns** (redact when `pii_guard_strict = true` in middleware config): email addresses, IP addresses, partial phone numbers
- **Low-confidence patterns** (log only, never redact): names in structured contexts

Luhn validation is applied to all credit card candidates before redaction, eliminating the most common false-positive class (random 16-digit number strings).

### Rate Limiting Design

Three layers:
1. **IP-based rate limit** (Traefik, external): handled by Traefik middleware. Not reimplemented in Go.
2. **User-level rate limit** (Redis INCR with sliding window): enforced by `gate.Gate` per user_id per orchestrator, using `them:ratelimit:{orchestrator_id}:{user_id}:{minute_bucket}` keys.
3. **Orchestrator RPM limit** (`rate_limit_rpm` column): enforced by `gate.Gate` against a shared `them:ratelimit:{orchestrator_id}:{minute_bucket}` counter.

### Audit Logging

All admin write operations (create/update/delete on any entity) write to `them.audit_log` via the async `audit.Logger`. The logger uses a buffered channel (size 256) and a background goroutine that batch-inserts up to 50 records at a time. The goroutine flushes on shutdown.

Audit log fields: `user_id`, `action` (CREATE/UPDATE/DELETE), `entity_type`, `entity_id`, `details JSONB` (diff of changed fields), `created_at`.

### Secrets Encryption at Rest

Agent `auth_token` and LLM provider `api_key` are stored encrypted (AES-256-GCM) in the DB, using a key derived from `SECRET_KEY` via HKDF-SHA256. Decryption happens at query time in the DB layer (`db.GetAgent()` returns the plaintext token). The plaintext never hits the ORM layer "” decryption is explicit in the query function, not a model hook.

---

## 4.11 Observability Model

### OTel Trace Structure

Every request (WS connection, SSE stream, A2A call, REST call) creates a root span. Key child spans:

| Span name | Attributes |
|---|---|
| `them.edge.{protocol}` | `orchestrator.name`, `user.id`, `session.id` |
| `them.auth.validate` | `auth.type` (jwt/bearer), `cache.hit` (l1/l2/db/miss) |
| `them.gate.check` | `ep.slug`, `gate.result` (ok/queued/rejected) |
| `them.orchestration.start` | `workflow.id`, `context.id`, `run.id` |
| `them.activity.{name}` | `run.id`, `iteration`, `agent.slug` (for InvokeAgent) |
| `them.llm.chat` | `provider`, `model`, `tokens.in`, `tokens.out`, `cache.hit` |
| `them.agent.invoke` | `agent.slug`, `transport`, `latency_ms` |
| `them.session.register` | `session.id`, `ep.slug` |

Traces propagate through Temporal activity boundaries via `workflow.GetInfo(ctx).RunID` embedded in the span context (stored as a Temporal workflow search attribute, recovered at activity start).

### Prometheus Metrics

| Metric | Type | Labels |
|---|---|---|
| `them_requests_total` | Counter | `protocol`, `orchestrator`, `status` |
| `them_sessions_active` | Gauge | `ep_slug`, `app_id` |
| `them_llm_tokens_total` | Counter | `provider`, `model`, `direction` (in/out) |
| `them_llm_latency_seconds` | Histogram | `provider`, `model` |
| `them_agent_invocations_total` | Counter | `agent_slug`, `transport`, `status` |
| `them_agent_latency_seconds` | Histogram | `agent_slug`, `transport` |
| `them_gate_decisions_total` | Counter | `ep_slug`, `decision` (ok/queued/rejected) |
| `them_auth_validations_total` | Counter | `type`, `cache_layer`, `result` |
| `them_temporal_workflow_duration_seconds` | Histogram | `orchestrator` |
| `them_pod_sessions` | Gauge | `pod_id` |

### Structured Logging

**Recommendation: `log/slog` (stdlib, Go 1.21+)**

Rationale:
- Zero external dependencies
- Structured JSON output by default with `slog.NewJSONHandler`
- Level-based filtering with `slog.SetLogLoggerLevel()`
- `slog.With()` creates child loggers that carry request-scoped fields (user_id, session_id, run_id) without global mutation
- OTel integration: `otelslog` handler wraps the JSON handler to automatically extract trace/span IDs into log records

Rejected alternatives:
- `zerolog`: excellent but external dependency; stdlib slog is sufficient for this use case
- `zap`: external dependency, more complex API, async writer complexity

### Health Endpoint Design

`GET /healthz` (liveness):
```json
{"status": "ok", "version": "1.0.0", "build": "abc1234"}
```

`GET /readyz` (readiness):
```json
{
  "status": "ok|degraded|unavailable",
  "dependencies": {
    "postgres": {"status": "ok", "latency_ms": 2},
    "redis":    {"status": "ok", "latency_ms": 1},
    "temporal": {"status": "ok", "worker_state": "running"},
    "migrations": {"status": "ok", "pending": 0}
  }
}
```

Returns 200 if all dependencies are `ok`. Returns 503 if any is `unavailable`. Returns 200 with `status: degraded` if a non-critical dependency (e.g., temporal) is slow but responsive.

---

## 4.12 Deployment Model

### Docker Compose Changes

The Go binary replaces the Python container progressively (see Phase 5). During migration, both `them-gateway-py` and `them-gateway-go` services exist in `docker-compose.yml`. Traefik routes traffic via labels.

New service definition (post-migration):
```yaml
them-gateway:
  image: them-gateway:${VERSION}
  environment:
    - DATABASE_URL
    - REDIS_URL
    - SECRET_KEY
    - AUTH_PUBLIC_KEY_PEM
    - TEMPORAL_HOST_PORT
    - ANTHROPIC_API_KEY
    - LIVEKIT_API_KEY
    - LIVEKIT_API_SECRET
    - APP_ENV
    - OTEL_EXPORTER_OTLP_ENDPOINT
  ports:
    - "8001:8001"
    - "9090:9090"  # Prometheus metrics
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:8001/readyz"]
    interval: 15s
    timeout: 5s
    retries: 3
    start_period: 30s
  labels:
    - "traefik.enable=true"
    - "traefik.http.routers.gateway.rule=Host(`...`)"
```

### Environment Variable Validation

Validated at startup (before binding any port). Rules defined in `config.Load()` as a slice of `Validator` structs with field name, validation function, and human-readable failure message. Unknown env vars are logged at WARN level (not fatal), enabling gradual migration.

### Secrets Management

Python used `secrets.local` dotenv files. Go uses environment variables only.

For local development: `.env` file loaded by `godotenv.Load()` (called only when `APP_ENV == "development"`).

For production: secrets injected via Docker secrets or Kubernetes Secret volumes. The config loader supports:
- Direct env var: `SECRET_KEY=...`
- File-from-env: `SECRET_KEY_FILE=/run/secrets/secret_key` "” the value of the `_FILE` variant is treated as a path; the file's content is the actual secret. This is the Docker secrets convention.

### Multi-Replica Session Stickiness

WebSocket connections require stickiness because the WS edge layer holds the connection and reads cancel signals. However, stickiness is NOT required for the orchestration state "” that lives in Temporal.

**Stickiness strategy**: Traefik `sticky: true` with `cookieName: them_sticky` and `secure: true`. The cookie is set to `SameSite=Lax; HttpOnly`.

**Can the requirement be eliminated?** Partially. If the WS edge sends token + context_id in the initial message, a reconnecting client can resume the run from any pod by re-subscribing to `them:run:{context_id}:events`. The run continues in Temporal regardless of pod death. The WS `cancelListener` and `controlListener` goroutines would need to be re-established on reconnect. This is a Phase 6 improvement, not a Phase 4 requirement.

### DB Migration Strategy

SQL migration files are kept in `migrations/` (existing directory, already used by Python via `alembic`). The Go binary uses `pressly/goose` v3:
- Migrations are embedded via `//go:embed migrations/*.sql`
- `migrate.Run()` calls `goose.Up()` at startup
- Goose version table: `them.goose_db_version` (separate from Alembic's `alembic_version` "” both can coexist during migration phase)
- Migration files are SQL-only (no Go migration functions) for portability

---

## 4.13 Technology Decisions

### Go Version
**Choice: Go 1.23**
- Reason: Range-over-func iterators (1.22), improved `sync.Map` performance, `log/slog` stable since 1.21, `slices`/`maps` stdlib packages. 1.23 is the current stable release.
- Rejected: 1.21 "” missing `slices` and `maps` packages that reduce boilerplate

### HTTP Framework
**Choice: `go-chi/chi` v5**
- Reason: Stdlib-compatible (`net/http`), composable middleware, no runtime reflection, excellent routing performance, zero-dependency core. Well-matched to a service that already has a defined API surface rather than needing an opinionated framework.
- Rejected:
  - `fiber` (Fasthttp-based) "” not `net/http` compatible; OTel and many middleware packages require `net/http`. Performance difference is irrelevant at this service's scale.
  - `echo` "” similar to chi but uses reflection-based route params. Chi's explicit `URLParam` is more readable.
  - `stdlib only` "” route pattern matching without a library requires significant boilerplate for parameterized routes

### DB Driver
**Choice: `jackc/pgx` v5 (direct, no ORM)**
- Reason: Best-in-class asyncpg-equivalent for Go. Typed row scanning without reflection. Named parameters. COPY protocol for bulk inserts (audit log). Compatible with `pgxpool` for connection pooling. `pgx` is what `asyncpg` wraps internally.
- Rejected:
  - `gorm` "” ORM magic hides queries; the existing codebase has complex JSONB queries and array operations that GORM handles poorly
  - `sqlc` "” excellent but requires a code generation step that complicates the CI pipeline. Direct `pgx` with typed scan functions achieves the same type safety with less tooling overhead
  - `database/sql` with `lib/pq` "” no native JSONB support, no `pgxpool`, lower performance

### Redis Client
**Choice: `redis/rueidis`**
- Reason: Auto-pipelining by default (multiplexes commands over a single connection, eliminating per-command RTT), first-class Lua scripting support via `EVALSHA`, built-in pub/sub with proper goroutine model, Redis 7.x compatible. Significantly outperforms `go-redis` in benchmarks due to auto-pipelining.
- Rejected:
  - `go-redis/redis` v9 "” good library but no auto-pipelining; each INCR/EXPIRE pair requires two RTTs unless manually pipelined. The session model's Lua scripts require EVALSHA support that rueidis handles more cleanly.

### Temporal Go SDK Version
**Choice: `go.temporal.io/sdk` v1.x (latest stable at time of development)**
- Reason: The existing Python Temporal integration uses `temporalio/sdk-python` v1.x with the same protocol. The Go SDK is the primary SDK, with full feature support including versioning, continue_as_new, child workflows, and signals.
- No rejection consideration "” Temporal Go SDK is the canonical choice for Go.

### OTel SDK
**Choice: `go.opentelemetry.io/otel` v1.x + OTLP exporter**
- Reason: Vendor-neutral. OTLP exporter sends to any OTel collector (Jaeger, Honeycomb, Grafana Tempo). Prometheus metrics via `go.opentelemetry.io/otel/exporters/prometheus`.
- No rejection consideration "” OTel is the standard.

### Logging Library
**Choice: `log/slog` (stdlib)**
- Reason: Stable since Go 1.21, zero external dependencies, structured JSON output, child logger pattern, OTel integration via `otelslog`. Adequate for this service's logging needs.
- Rejected: `uber-go/zap` and `rs/zerolog` "” both are excellent but introduce external dependencies without meaningful capability improvements for this use case.

### JWT Library
**Choice: `golang-jwt/jwt` v5**
- Reason: Most widely used, well-audited, supports RS256/ES256/HS256, `RegisteredClaims` struct with standard validation. v5 has breaking changes from v4 that improve type safety (claims must implement the `Claims` interface).
- Rejected: `lestrrat-go/jwx` "” more complete JOSE implementation (JWE, JWK sets) but significantly more complex API. We only need RS256 validation; the extra complexity is not warranted.

### Config Library
**Choice: `spf13/viper` + `joho/godotenv`**
- Reason: Viper reads env vars, maps them to a typed struct, supports `_FILE` suffix convention for Docker secrets, and validates required fields. `godotenv` loads `.env` files in development.
- Rejected:
  - `kelseyhightower/envconfig` "” simpler but lacks the `_FILE` convention and custom validators
  - `caarlos0/env` "” good but less ecosystem support than Viper

### Testing Approach
**Choice: stdlib `testing` + `testify/assert` + `testcontainers-go` for integration tests**
- Unit tests: `testing` package with `testify/assert` for readable assertions. Table-driven tests for auth, gate, and LLM error classification logic.
- Integration tests: `testcontainers-go` spins up real Postgres and Redis containers. Tests in `internal/db`, `internal/session`, `internal/gate`, and `internal/auth` run against real containers. No mocking of the DB or Redis layers.
- Temporal tests: Temporal Go SDK's `testsuite` package for workflow/activity unit tests with a test server.
- Rejected: `gomock` "” mock generation is fragile and produces tests that test the mock, not the behavior. `testcontainers-go` is preferred for data-layer tests.

---

## 4.14 Decision Log (ADR Format)

---

**ADR-001**
**Title**: Local RS256 JWT Validation (No Auth Service HTTP Call)
**Status**: Accepted
**Context**: The current Python implementation calls `them-auth-service:8701` on every JWT validation. This adds ~5-15ms of latency per authenticated request, creates a hard dependency on the auth service being available (cascade failure), and produces a CPU-intensive fan-out pattern under load. The auth service validates the JWT cryptographically "” an operation that can be done locally with the public key.
**Decision**: Load the RS256 public key(s) at startup and perform JWT validation in-process. Publish a revocation channel (`them:token:revoked`) for soft-revocation with Redis SET entries keyed by `jti`. The auth service is no longer on the hot path.
**Consequences**:
- Positive: Eliminates 5-15ms per-request latency, removes auth service as single point of failure for request processing.
- Positive: JWT validation becomes testable in isolation without running the auth service.
- Negative: Key rotation requires a coordination mechanism (DB config row + watcher goroutine). A misconfigured rotation that removes the active key causes all validations to fail until the watcher picks up the new key (max 5-minute window).
- Negative: The auth service retains authority over user management and token issuance; the gateway only handles validation. This split must be documented.

---

**ADR-002**
**Title**: Single Temporal Code Path (No task_runner.py Parallel Path)
**Status**: Accepted
**Context**: The Python codebase has `_TEMPORAL_ENABLED = True` hardcoded in `ws_orchestrator.py` but maintains a parallel `task_runner.py` code path that diverges in behavior (different history loading, different event publication, different error handling). This creates two maintenance surfaces and means bugs fixed in one path may not be fixed in the other. The non-Temporal path was a migration artifact.
**Decision**: The Go binary has exactly one orchestration code path: Temporal. `task_runner.go` is not ported. All entry points call `orchestration.Engine.Start()` which starts a Temporal workflow. The Temporal Go SDK's test suite is used for workflow unit testing without a running Temporal server.
**Consequences**:
- Positive: One code path to maintain, test, and debug. Temporal's event history provides a built-in audit trail for every run.
- Positive: HITL, sub-orchestrators, continue_as_new, and cancellation all have a single implementation.
- Negative: Temporal is now a hard dependency. If the Temporal cluster is unavailable, no orchestration can start. Mitigation: readiness probe includes Temporal worker status; Traefik removes the pod from traffic when Temporal is down.
- Negative: Simple single-turn queries still go through Temporal overhead (~200ms workflow start latency). Mitigation: an `express` orchestrator type with `max_iterations=1` can be added post-migration to use a direct activity path without the full workflow.

---

**ADR-003**
**Title**: Reservation Pattern for Gate/Session Atomicity — Gate Owns Admission, SessionManager Owns Hash
**Status**: Accepted
**Context**: The Python session_manager has two concurrency bugs: (1) TTL mismatch — Hash expires but Set members persist, creating ghost sessions. (2) Dual-write — both `runtime_manager` (gate) and `session_manager` called SADD into the same Sets. If Hash creation failed after gate's SADD, the session was in the Set with no Hash — ghost from birth, with no bounded cleanup window.
**Decision**: Three-step transaction boundary with a reservation TTL:
1. **`Gate.Check()`** runs a single atomic Lua script: ghost prune (SREM expired shadows) → EP cap check → app cap check → rate limit INCR → SADD membership Sets → SET shadow keys with `ReservationTTL` (10s). Gate is the sole owner of Set membership. The short TTL bounds the ghost window if the caller never reaches step 2.
2. **`session.Store.Register()`** writes the Hash (HSET, EXPIRE 90s). Owned entirely by SessionManager. Does NOT touch Sets.
3. **`Gate.Confirm()`** refreshes shadow keys from `ReservationTTL` (10s) to `ShadowTTL` (90s). Must be called after Register succeeds. If the process crashes between Check and Confirm, the shadow expires in ≤10s and the ghost is pruned automatically on the next admission attempt (no explicit cleanup required).

If `Register()` fails, callers MUST call **`Gate.Rollback()`** which atomically SREMs the Set entry, DELs the shadow, and calls `Release()` to wake any queued waiters immediately.

**Queue protocol**: When the EP cap is full and `QueueTimeout > 0`, `Gate.Check()` blocks on `BLPop(them:ep:gate:queue:{slug})`. `Gate.Release()` calls `LPush("1")` to wake exactly one waiter. On wake-up, the waiter re-runs the full `luaAdmit` script from scratch — this is a compete, not a guarantee. If the slot was taken by a concurrent waiter, `ErrCapExceeded` is returned immediately (no re-queue). `Gate.Rollback()` and `Gate.Release()` are also called on session end so queued sessions are not starved.

**Caller contract** (enforced in `internal/ws/handler.go` and `internal/sse/handler.go`):
```
ok, err := gate.Check(ctx, cfg)   // short reservation written
if err != nil { reject; return }
err = session.Register(ctx, info) // Hash written
if err != nil { gate.Rollback(ctx, cfg); return }
gate.Confirm(ctx, cfg)            // shadow extended to full TTL
defer func() {
    session.End(ctx, ...)
    gate.Release(ctx, cfg)        // wake next queued session
}()
```
**Consequences**:
- Positive: Failure window is bounded to `ReservationTTL` (10s) even on process crash — no manual intervention required.
- Positive: Rollback is explicit and immediate when Register fails — no waiting for TTL.
- Positive: Ghost sessions caused by process crash cannot persist longer than 10s before being pruned on the next admission attempt.
- Positive: Queue wake-up re-competes rather than assuming a guaranteed slot — correct under concurrent waiters.
- Positive: Pod heartbeat reports real session count from the atomic counter.
- Positive: Eliminates duplicate-SADD and dual-owner race from the Python design.
- Negative: Callers must follow the Check → Register → Confirm contract. A missed Confirm means sessions expire after 10s (acting as a circuit breaker, but may cause unexpected session drops). Mitigated by the deferred Rollback pattern.
- Negative: Lua scripts are opaque to Redis monitoring tools; NOSCRIPT errors on Redis flush require script reload.

---

**ADR-004**
**Title**: rueidis Over go-redis for Redis Client
**Status**: Accepted
**Context**: The session model requires atomic Lua scripts (`EVALSHA`), pub/sub for several channels, and high-throughput INCR/EXPIRE pairs for rate limiting. The standard `go-redis` library sends each command as a separate round-trip unless manually pipelined. The session Lua scripts alone require correct `EVALSHA` / `EVAL` fallback handling.
**Decision**: Use `redis/rueidis` as the Redis client. Its auto-pipelining transparently multiplexes concurrent commands over a single connection, reducing RTT for the rate-limit counter pattern from 2 (INCR + EXPIRE) to near-zero additional overhead. Its `rueidis.Lua` type handles `EVALSHA` with automatic `EVAL` fallback on `NOSCRIPT` errors.
**Consequences**:
- Positive: Rate-limit INCR/EXPIRE pairs benefit from pipelining without explicit pipeline construction.
- Positive: Lua script loading is handled by the client; no manual SHA management.
- Negative: `rueidis` has a different API from `go-redis`. Team members familiar with `go-redis` will need to adjust. The API is well-documented but less familiar in the Go ecosystem.
- Negative: `rueidis` is newer and has less community precedent than `go-redis`. Mitigation: the `internal/cache` package isolates the client behind an interface, allowing replacement if needed.

---

**ADR-005**
**Title**: pgx v5 Direct Queries (No ORM)
**Status**: Accepted
**Context**: The Python codebase uses SQLAlchemy 2.0 async ORM with complex JSONB queries, PostgreSQL array operations (`ARRAY UUID`, `&&` operator), and full-text search on JSONB fields. ORMs in Go (GORM, ent) handle basic CRUD well but produce poor SQL for these patterns. The DB schema is fixed by the Python side during migration (constraint: same schema).
**Decision**: Use `pgx/v5` directly with hand-written SQL. Query functions are typed: they accept typed parameters and return typed result structs. SQL strings are defined as package-level constants (not inline). A thin `db.Queries` struct groups related query functions (one struct per domain: agents, orchestrators, runs, sessions, tokens).
**Consequences**:
- Positive: Full SQL expressivity for JSONB, array operators, CTEs, and `RETURNING` clauses.
- Positive: No ORM magic; every query is auditable in the query file.
- Positive: `pgx` row scanning with `pgx.RowToStructByName` eliminates manual scan boilerplate while remaining type-safe.
- Negative: Schema changes require updating query files manually (no auto-generation). Mitigation: the schema is frozen during migration; post-migration schema ownership transfers to Go which can optionally add `sqlc` generation.
- Negative: More query code to write than GORM. Mitigation: `pgx/v5`'s batch query support and `RowToStructByName` reduce the per-query boilerplate significantly.

---

**ADR-006**
**Title**: chi Router Over Fiber
**Status**: Accepted
**Context**: The service must expose HTTP (REST, SSE), WebSocket, and standard middleware (CORS, auth, logging, rate-limiting). OTel HTTP instrumentation, the Prometheus metrics handler, and most Go HTTP middleware packages assume `net/http` compatible handlers (`http.Handler`). Fiber uses `fasthttp` which is incompatible with `net/http`.
**Decision**: Use `go-chi/chi` v5. All handlers are `http.HandlerFunc`. WebSocket upgrade uses `gorilla/websocket`. SSE uses plain `http.ResponseWriter` with `Flusher`. The chi router composes standard `net/http` middleware cleanly.
**Consequences**:
- Positive: Full `net/http` ecosystem compatibility (OTel, testify/httptest, standard middleware packages).
- Positive: WebSocket and SSE work without adapter layers.
- Positive: Familiar to any Go developer; no framework lock-in.
- Negative: Marginally lower raw throughput compared to Fiber/fasthttp. Irrelevant at this service's request volumes.

---

**ADR-007**
**Title**: Embedded SQL Migrations with Goose (Alembic Coexistence)
**Status**: Accepted
**Context**: The Python side uses Alembic for schema migrations. During the migration phase, both Python and Go run against the same database. Alembic's version table (`alembic_version`) must remain valid as long as Python is running. Goose uses a separate version table (`goose_db_version`). Post-migration, all schema authority transfers to Go/Goose.
**Decision**: Embed migration SQL files in the Go binary using `//go:embed`. Goose runs `goose.Up()` at startup, blocking the readiness gate until complete. During the migration phase, Goose only runs additive migrations (new tables, new columns, new indexes) "” it does not alter or drop any table that Alembic manages. The `alembic_version` table is left untouched.
**Consequences**:
- Positive: Migrations are atomic with binary deployment "” a rollback of the binary also means the migration hasn't run.
- Positive: No external Alembic dependency in the Go deployment.
- Negative: Two version tables during migration creates a risk of confusion. The migration checklist must document which tool owns which tables at each phase.
- Negative: Goose down-migrations (rollback) must be written and tested before any destructive migration is applied. Mitigation: all Phase 5 migrations are additive only.

---

**ADR-008**
**Title**: Token Revocation via Redis Pub/Sub Broadcast
**Status**: Accepted
**Context**: The Python bearer token cache (L1 in-process dict, TTL 300s) means a revoked token continues to be accepted for up to 5 minutes on any pod that has it in L1. For a multi-pod deployment, this is a security window. JWT tokens have the same issue: a revoked JWT's `jti` may not be in every pod's local revocation set for up to 5 minutes.
**Decision**: Add a `them:token:revoked` pub/sub channel. When a token is revoked (admin DELETE or token expiry enforcement), the revoking pod publishes the token hash. All pods' `event.Bus` dispatcher delivers this to the `auth.revokedListener`, which immediately deletes the L1 cache entry. For JWTs, a Redis key `them:jwt:revoked:{jti}` (TTL = token remaining lifetime) is also written, checked on every JWT validation after local RS256 verification.
**Consequences**:
- Positive: Revocation propagates across all pods in sub-second (pub/sub latency), closing the 5-minute window.
- Positive: The Redis key provides durable revocation that survives pod restarts.
- Negative: Every JWT validation now includes a Redis lookup for the revocation key (after the free local crypto check). This is one Redis GET per JWT validation "” acceptable overhead (~0.5ms) for the security benefit.
- Negative: If Redis is briefly unavailable, the revocation check is skipped (fail-open). Mitigation: for high-security deployments, the JWT revocation check can be made fail-closed (reject all JWTs when Redis is unreachable), configurable via `AUTH_JWT_STRICT_REVOCATION=true`.

---

