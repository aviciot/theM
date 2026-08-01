# Execution Lifecycle Unification — Design
# internal/execution/: shared admission + run-start pipeline for WS, SSE, A2A
# Status: PROPOSED — not yet implemented
# Author: architecture review 2026-08-01

---

## 1. Problem

Three inbound protocol handlers each contain ~250 lines of identical pipeline logic:

| Handler | File | Shared-pipeline lines (approx) |
|---|---|---|
| WebSocket | `go/internal/ws/handler.go` | L221–L565 |
| SSE | `go/internal/sse/handler.go` | L231–L570 |
| A2A JSON-RPC | `go/internal/a2a/server.go` | L247–L520 |

Every handler duplicates the same ordered sequence:

```
tryAuthenticate → epLoader.Load → CheckAccess → gate.Check → gate.Rollback guard
→ session.Register → gate.Confirm → bus.Subscribe → recorder.CreateRun
→ ExecuteWorkflow → (stream or block) → defer: session.End + gate.Release
```

Additionally, A2A uses a 16-byte random hex ID (`newID()` at `server.go:54`) while WS and SSE
use UUID v4 (`newID()` at `sse/handler.go:57`). IDs must be unified to UUID v4 across all
three paths because the Python Temporal worker parses them as `uuid.UUID()`.

**Result**: a bug fix or constraint change (e.g. new gate sentinel, new tenant field) must be
applied in three places. Any missed copy is a correctness defect.

---

## 2. Proposed Package Layout

```
go/internal/execution/
    lifecycle.go          # Lifecycle struct, Admit, Start, Release
    errors.go             # AdmitError, AdmitErrorKind, sentinel values
    request.go            # ExecutionRequest, ExecutionHandle, ExecutionResult
    lifecycle_test.go     # unit tests with fakes for all deps
```

The package imports:
- `internal/auth`
- `internal/epconfig`
- `internal/gate`
- `internal/session`
- `internal/runrecorder`
- `internal/event`
- `internal/temporal`
- `internal/domain`
- `internal/config`
- `go.temporal.io/sdk/client` (for `WorkflowRun`)

The package does **not** import `internal/ws`, `internal/sse`, or `internal/a2a`.

---

## 3. API — Types and Lifecycle

### 3.1 ExecutionRequest

```go
// ExecutionRequest carries the per-call inputs that the caller (protocol handler)
// has already resolved before calling Admit. The Lifecycle does not parse HTTP.
type ExecutionRequest struct {
    EPSlug        string           // entry-point slug from URL path
    RawToken      string           // bearer token string; empty if none presented
    TokenInfo     *auth.TokenInfo  // nil if public EP or token absent
    UserMessage   domain.Message   // parsed user message (content + role)
    ContextID     string           // caller-supplied; empty → Lifecycle generates UUID v4
    RunEventsMode config.RunEventsMode
    InstanceID    string           // pod/replica identity for session record
}
```

### 3.2 ExecutionHandle

Returned by `Admit` on success. The caller subscribes to the event bus using `ContextID`
before calling `Start`, satisfying the bootstrap ordering invariant.

```go
// ExecutionHandle is the admission ticket. It carries all IDs and config
// needed for the caller to subscribe to the run-stream before starting the workflow.
type ExecutionHandle struct {
    RunID           string
    ContextID       string
    SessionID       string
    EPConfig        *epconfig.EPConfig
    EventsTransport string // "pubsub" | "streams" | "dual"
}
```

### 3.3 ExecutionResult

```go
// ExecutionResult is returned by Start (for callers that block synchronously, e.g. A2A).
// Streaming callers (WS, SSE) call wfRun.Get() themselves after Start returns.
type ExecutionResult struct {
    FinalText   string
    Status      domain.RunStatus
    WorkflowRun temporalclient.WorkflowRun // always non-nil on success
}
```

### 3.4 Lifecycle

```go
// Lifecycle executes the shared admission-and-run-start pipeline.
// Constructed once at server startup and shared across all protocol handlers.
type Lifecycle struct {
    auth     transport.Authenticator
    epLoader transport.EPConfigLoader
    gate     transport.GateStore
    sessions transport.SessionStore
    recorder *runrecorder.Recorder
    bus      *event.Bus
    temporal transport.TemporalClientExecutor
    logger   *slog.Logger
}

func NewLifecycle(
    auth     transport.Authenticator,
    epLoader transport.EPConfigLoader,
    gate     transport.GateStore,
    sessions transport.SessionStore,
    recorder *runrecorder.Recorder,
    bus      *event.Bus,
    temporal transport.TemporalClientExecutor,
    logger   *slog.Logger,
) *Lifecycle

// Admit runs the admission pipeline:
//   tryAuthenticate-check → epLoader.Load → CheckAccess → gate.Check
//   → session.Register → gate.Confirm → recorder.CreateRun
//
// On success returns a handle the caller uses to subscribe to the run-stream.
// On failure returns a typed *AdmitError — never raw internal error strings.
// Rollback of gate reservation is performed internally if session.Register fails.
func (lc *Lifecycle) Admit(ctx context.Context, req ExecutionRequest) (*ExecutionHandle, error)

// Start launches the Temporal workflow. The caller MUST have subscribed to the
// run-stream (bus.Subscribe with h.ContextID) before calling Start.
//
// Returns the WorkflowRun so streaming handlers can iterate events concurrently
// while A2A blocks on wfRun.Get().
func (lc *Lifecycle) Start(ctx context.Context, h *ExecutionHandle, input temporal.WorkflowInput) (temporalclient.WorkflowRun, error)

// Release ends the session and releases the gate reservation.
// Must be called exactly once, always in a defer in the protocol handler.
// Safe to call even if Admit failed mid-way — it is a no-op when h is nil.
func (lc *Lifecycle) Release(ctx context.Context, h *ExecutionHandle)
```

---

## 4. Two-Phase Contract and Ordering Invariants

```
Phase 1 — Admit():
  tryAuthenticate  (check token validity; public EPs allowed without token)
  epLoader.Load    (resolve EPConfig; TenantID/AppID come exclusively from here)
  CheckAccess      (EP disabled or blocked → AdmitErrForbidden)
  gate.Check       (cap/rate/queue; Rollback registered here if needed)
  session.Register (write session hash; on failure → gate.Rollback → return error)
  gate.Confirm     (promote 10s reservation to full 90s TTL)
  recorder.CreateRun (persist run record with TenantID+AppID from EPConfig)
  ← returns ExecutionHandle

CALLER must call bus.Subscribe(ctx, handle.ContextID, 256) here
  (bootstrap ordering: subscription must be in place before workflow emits first event)

Phase 2 — Start():
  build temporal.WorkflowInput (RunID, ContextID, TenantID, ApplicationID from handle)
  temporalClient.ExecuteWorkflow(...)
  ← returns WorkflowRun (streaming handlers) or blocks on wfRun.Get() (A2A)

Cleanup — Release() — always in defer:
  sessions.End(ctx, h.SessionID, epSlug, appID)
  gate.Release(ctx, gateCfg)
```

**Non-negotiable ordering rules (carry forward from existing handlers):**
1. `bus.Subscribe` MUST happen between `Admit` return and `Start` call.
2. `gate.Rollback` MUST be called if `session.Register` fails, before returning the error.
3. `gate.Confirm` MUST happen after `session.Register` succeeds, before `CreateRun`.
4. TenantID and ApplicationID are populated from `EPConfig` only — never from request data.
5. Both `sessions.End` and `gate.Release` are called in `Release`, regardless of whether
   the workflow started, failed, or was cancelled.

---

## 5. Error Model

### 5.1 AdmitError

```go
type AdmitErrorKind int

const (
    AdmitErrNotFound     AdmitErrorKind = iota // EP slug unknown
    AdmitErrUnauthorized                       // token required, absent or invalid
    AdmitErrForbidden                          // EP disabled or blocked user/token
    AdmitErrCapExceeded                        // gate: session cap full
    AdmitErrRateLimited                        // gate: rate limit hit
    AdmitErrQueueFull                          // gate: queue full
    AdmitErrInternal                           // DB/Redis/Temporal failure — log internally, static string to client
)

type AdmitError struct {
    Kind       AdmitErrorKind
    HTTPStatus int
    // Internal cause is logged by Lifecycle; never exposed in this struct.
}

func (e *AdmitError) Error() string // static string per Kind — never err.Error() from internals
```

### 5.2 Protocol mapping

Each handler maps `*AdmitError` to its wire format:

| Kind | HTTP status | WS close | SSE data | A2A JSON-RPC |
|---|---|---|---|---|
| NotFound | 404 | 4404 | 404 text/plain | code -32001 "not found" |
| Unauthorized | 401 | 4401 | 401 | code -32600 "unauthorized" |
| Forbidden | 403 | 4403 | 403 | code -32600 "forbidden" |
| CapExceeded | 429 | 4429 | 429 | code -32009 "session cap exceeded" |
| RateLimited | 429 | 4429 | 429 | code -32008 "rate limit exceeded" |
| QueueFull | 503 | 4503 | 503 | code -32007 "queue full" |
| Internal | 500 | 4500 | 500 | code -32603 "internal error" |

The mapping lives in each protocol handler — not in `internal/execution/`. The Lifecycle
returns a typed `*AdmitError`; the handler writes the wire response.

`Start` returns a plain `error` (not typed) on workflow-launch failure. Handlers log the
error and write a static 500 / WS 4500 / A2A -32603 response.

---

## 6. Cleanup Model

`Release` is designed for use in a single `defer` immediately after `Admit` returns
the handle:

```go
h, err := lc.Admit(ctx, req)
if err != nil {
    // write error response; no Release needed — Admit cleaned up internally
    return
}
defer lc.Release(context.Background(), h) // context.Background(): req ctx may be cancelled

// ... subscribe, Start, stream ...
```

**Exactly-once guarantee:**
- If `Admit` fails mid-pipeline, it rolls back whatever it has done before returning
  the error. The caller does NOT call `Release`.
- If `Admit` succeeds, the caller defers `Release`. `Release` always calls both
  `sessions.End` and `gate.Release`, even if `Start` was never called (e.g. the
  bus.Subscribe step failed).
- `Release(ctx, nil)` is a no-op, guarding against accidental double-defer patterns.
- `Release` uses a fresh `context.Background()` so session/gate cleanup is not
  affected by the cancelled request context (client disconnect).

---

## 7. What Stays in Each Protocol Handler

The handler retains only protocol-specific logic:

### WebSocket (`internal/ws/handler.go`)
- HTTP upgrade (gorilla/websocket)
- Read loop: parse incoming JSON frames, build `domain.Message`
- Write loop: serialize events to WS frames
- `lastEventID` header for resume
- Metrics: `ActiveWSConnections` gauge
- Error → WS close code mapping

### SSE (`internal/sse/handler.go`)
- `Content-Type: text/event-stream` header, flusher
- Write loop: serialize events to SSE format (`data: ...\n\n`)
- `lastEventID` header for resume
- Metrics: `ActiveSSEConnections` gauge
- Error → HTTP status mapping

### A2A (`internal/a2a/server.go`)
- Parse `application/json` body as JSON-RPC 2.0 request
- Validate `jsonrpc: "2.0"` field
- Extract `appSlug` from URL path (maps to `EPSlug`)
- Build `params.Message` → `domain.Message`
- Call `wfRun.Get(ctx)` after `Start` returns (synchronous block)
- Map `WorkflowResult` → A2A task result JSON
- Error → JSON-RPC error response (`writeRPCError`)

All three handlers call `Admit` → subscribe → `Start` → stream/block → `defer Release`.

---

## 8. ID Generation

A single `newRunID()` function is defined in `internal/execution/lifecycle.go`:

```go
func newRunID() string { return uuid.New().String() }
```

All three handlers call `Lifecycle.Admit`, which generates `RunID` and `ContextID`
internally using this function. The A2A server's existing `newID()` (random hex) is
retired; A2A callers receive UUID v4 IDs after migration.

---

## 9. Test Plan

### Unit tests (`lifecycle_test.go`)

All deps are fakes implementing `transport.*` interfaces.

| Scenario | Assertion |
|---|---|
| Happy path (WS-style) | Admit returns handle; Start returns wfRun; Release calls End+Release |
| EP not found | Admit returns AdmitErrNotFound; no gate/session calls made |
| Token required, absent | Admit returns AdmitErrUnauthorized |
| Gate cap exceeded | Admit returns AdmitErrCapExceeded; gate.Rollback NOT called (never registered) |
| session.Register fails | Admit returns error; gate.Rollback called before return |
| recorder.CreateRun fails | Admit returns AdmitErrInternal |
| Start: temporal fails | Start returns error; Release still cleans up |
| Release(ctx, nil) | no-op, no panic |
| TenantID/AppID from EPConfig | WorkflowInput fields match EPConfig values, not request |
| ContextID provided by caller | handle.ContextID equals input; not overwritten |
| ContextID empty | handle.ContextID is non-empty UUID v4 generated by Lifecycle |

### Integration (build tag `integration`)

Run against live Redis (gate/session) and Postgres (recorder):
- Full happy-path Admit + Start via fake Temporal client
- Confirm gate reservation promoted after Admit (TTL > 10s)
- Confirm session hash written to Redis
- Confirm run row written to DB with correct TenantID/AppID

### Handler-level tests (existing suites — no change required)

Existing `ws/handler_test.go`, `sse/handler_test.go`, and `a2a/server_test.go` are
updated to inject a `*Lifecycle` with fakes rather than duplicating dep injection.
All existing test cases (including test 7/gate-rollback, test 15/Temporal path) remain —
they are adapted to the new call sequence, not deleted.

---

## 10. Migration Steps (implementation order)

1. Create `go/internal/execution/` with types and empty stubs.
2. Implement `Admit` with full pipeline; unit tests pass.
3. Implement `Start`; unit tests pass.
4. Implement `Release`; unit tests pass.
5. Migrate `internal/ws/handler.go`: replace duplicated pipeline with `lc.Admit/Start/Release`.
   Run `go test ./internal/ws/...` — zero regressions.
6. Migrate `internal/sse/handler.go`. Run `go test ./internal/sse/...`.
7. Migrate `internal/a2a/server.go`. Replace `newID()` with Lifecycle-generated UUID.
   Run `go test ./internal/a2a/...`.
8. Run `go test ./...` — full suite green.
9. Wire `Lifecycle` in `cmd/them/main.go`; rebuild container; smoke test all three paths.

Each step is a separate commit. Steps 5–7 can each be reverted independently.

---

## 11. Out of Scope

The following are explicitly excluded from this design:

- **Streaming event loops** (WS read/write goroutines, SSE flush loop) — stay in handlers.
- **A2A agent-card endpoint** (`/.well-known/agent.json`) — no pipeline; stays in `server.go`.
- **Admin routes** — no execution pipeline involved.
- **History pre-loading** — remains in `temporal.WorkflowInput` construction in handlers
  (requires DB access pattern specific to each call type).
- **God-object ExecutionService** — `Lifecycle` exposes only three methods; it does not
  own streaming, metrics, or logging beyond the pipeline steps.
- **Python bridge migration** — Python `ws_orchestrator.py` / `apps.py` are not touched;
  this design is Go-only.
- **Temporal signal (HITL)** — out of scope; handled post-Start in individual handlers.
- **A2A outbound invocation** (agentregistry) — not part of the inbound admission pipeline.
