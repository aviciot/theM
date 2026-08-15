# Phase R-0 Implementation Plan
# Date: 2026-07-26
# Status: APPROVED — implementation authorized
# Session HEAD: 671ee23

---

## Scope confirmation

Phase R-0 fixes five architecture-debt items only.
No new data-plane features, no tenant schema changes, no external API contract changes.

---

## Item definitions and acceptance criteria

### L-1 — Terminal event must-deliver guarantee (OD-1)

**Finding:** `InMemoryBus.Publish` uses a non-blocking send for ALL events including `done`
and `error`. If the subscriber's 256-event buffer is full when a terminal event is published,
the terminal event is silently dropped. The WS/SSE client never receives `done` or `error`
and hangs indefinitely.

**Fix:**
- Add `termCh chan event.Event` (capacity 1) to `chanEntry` in `internal/event/bus.go`
- `Publish` checks `ev.Type == "done" || ev.Type == "error"`; if true, sends to `termCh`
  via non-blocking select (second terminal event silently discarded — only one can arrive)
- `Subscribe` returns two channels: `evCh <-chan Event` and `termCh <-chan Event`
- **Breaking change to `Bus` interface:** `Subscribe` signature changes from
  `(ctx, topic, bufSize) (<-chan Event, func())` to
  `(ctx, topic, bufSize) (<-chan Event, <-chan Event, func())`
- `streamEvents` in `ws/handler.go` and `sse/handler.go` adds a `termCh` drain case
- All existing call sites updated to accept the new return value

**Acceptance criteria:**
- `TestBus_TerminalEventDeliveredOnFullBuffer`: fill 256-event buffer, publish `done`,
  subscriber receives `done` from `termCh`
- `TestBus_TerminalEventDroppedIfTermChFull`: second terminal event silently discarded
- `go test -race ./internal/event/...` passes

### L-2 — Pod-heartbeat goroutine context (L-2)

**Finding:** The pod-heartbeat goroutine in `cmd/them/main.go` is started with the process-level
`ctx` which is `context.Background()`. `context.Background()` is never cancelled, so this
goroutine has no shutdown path and leaks on process exit.

**Fix:**
- In `cmd/them/main.go`, create `runCtx, runCancel := context.WithCancel(context.Background())`
  at the top of `run()` and defer `runCancel()`
- Pass `runCtx` to the heartbeat goroutine and to all other goroutines started in `run()`
  (token cache Subscribe, agent registry Subscribe, reconciler, EP config Subscribe)
- The goroutine already checks `<-ctx.Done()`, so it terminates on `runCancel()`
- Add focused unit test to `internal/health/health_test.go` (heartbeat context derivation)

**Acceptance criteria:**
- Heartbeat goroutine exits when `runCancel()` is called
- No goroutine leak detected by goleak in health tests

### L-3 — Subscribe goroutines stopped before Redis close (L-3)

**Finding:** `tokenCache.Subscribe(ctx)` and `epLoader.Subscribe(ctx, ...)` run in goroutines
using the process-level `ctx`. On shutdown, `server.ListenAndServe` drains HTTP then calls
`database.Close()` and `redisCache.Close()` via registered Closers. But the Subscribe goroutines
are still running at this point, holding Redis connections that are closed from under them.
This can cause a panic or error-log storm during shutdown.

**Fix:**
- `auth.Cache.Subscribe` already accepts `ctx` and returns when ctx is cancelled
- `epconfig.Loader.Subscribe` also blocks on ctx
- The fix is ordering: with the `runCtx` fix from L-2, calling `runCancel()` before server
  shutdown ensures Subscribe goroutines exit before Redis closes
- `defer runCancel()` executes in LIFO order. We need `runCancel()` called BEFORE
  `server.ListenAndServe()` returns (which triggers database/redis Close). To guarantee
  this ordering, call `runCancel()` explicitly at the start of the shutdown sequence rather
  than relying on defer ordering.
- In `internal/server/server.go`: add a `stopFuncs []func()` field; add
  `RegisterStopFunc(fn func())` method; call all stop funcs before `httpServer.Shutdown`
- In `main.go`: register `runCancel` as a stop func on the server before `ListenAndServe`

**Implementation note:** The simpler approach is to call `runCancel()` in a deferred func
in `run()` before `srv.ListenAndServe()` blocks — but `ListenAndServe` blocks until signal.
The cleanest fix: server calls stop funcs BEFORE draining HTTP, so subscribers stop first.

**Revised approach (simpler):** Since `runCtx` is derived from `context.Background()` and
not from the HTTP server's context, we can have `ListenAndServe` call a pre-drain hook.
Add `WithPreDrainHook(fn func())` to server; call it before `httpServer.Shutdown`. In
`main.go` pass `runCancel` as the pre-drain hook.

**Acceptance criteria:**
- `tokenCache.Subscribe` goroutine exits before Redis closes
- No subscribe goroutine holds a Redis connection after shutdown

### T-2 — AppID and TenantID in SessionInfo (OD-2)

**Finding:** `session.SessionInfo` has `AppID string` field but it is never populated in
`ws/handler.go` or `sse/handler.go`. `TenantID` field does not exist at all.
This blocks future tenant-scoped session admin queries.

**Fix:**
- Add `TenantID string` field to `session.SessionInfo` (with JSON tag `tenant_id,omitempty`)
- Update `sessionInfoToFields` and `fieldsToSessionInfo` helpers in `session.go`
- In `ws/handler.go` step 6: `sessInfo.AppID = resolvedCfg.AppID` and
  `sessInfo.TenantID = resolvedCfg.TenantID` (when `resolvedCfg != nil`)
- Same in `sse/handler.go` step 7
- Also populate `AppID` in the deferred `session.End` call (line 386/401) — currently passes
  empty string; pass `epSlug`'s associated `appID` from `resolvedCfg`

**Acceptance criteria:**
- `TestSession_AppIDAndTenantIDPopulated`: register session with mock EPConfig having
  AppID="app-1" and TenantID="tenant-1"; verify Redis Hash contains both fields
- `TestWS_SessionHasAppIDAndTenantID`: WS handler populates both fields in sessInfo
- `TestSSE_SessionHasAppIDAndTenantID`: SSE handler populates both fields in sessInfo

### OD-7 — Shutdown drain timeout (OD-7)

**Finding:** `internal/server/server.go` hardcodes `drainTimeout = 5 * time.Second`. Anthropic
LLM responses can take 20–60s. A 5s drain forces active LLM streams to cancel mid-response.

**Fix:**
- Add `ShutdownDrainSeconds int` field to `config.Config`
- Load from `SHUTDOWN_DRAIN_SECONDS` env var (default 30, minimum 5, enforced at load time)
- Pass drain seconds to server constructor; use it in `ListenAndServe` instead of constant
- Remove the `drainTimeout` constant

**Acceptance criteria:**
- `TestLoad_ShutdownDrainSeconds_Default`: missing env → 30
- `TestLoad_ShutdownDrainSeconds_Custom`: `SHUTDOWN_DRAIN_SECONDS=60` → 60
- `TestLoad_ShutdownDrainSeconds_BelowMinimum`: `SHUTDOWN_DRAIN_SECONDS=3` → 5 (clamped)
- `TestServer_GracefulShutdownWith30sDrain`: mock LLM takes 25s; SIGTERM → response completes

---

## Implementation checklist

| # | Item | Files changed | Tests |
|---|---|---|---|
| 1 | L-1: termCh in event bus | `internal/event/bus.go`, `internal/ws/handler.go`, `internal/sse/handler.go` | `TestBus_TerminalEventDeliveredOnFullBuffer`, `TestBus_TerminalEventDroppedIfTermChFull` |
| 2 | T-2: AppID+TenantID in SessionInfo | `internal/session/session.go`, `internal/ws/handler.go`, `internal/sse/handler.go` | `TestSession_AppIDAndTenantIDPopulated`, WS/SSE tests |
| 3 | OD-7: drain timeout configurable | `internal/config/config.go`, `internal/server/server.go` | `TestLoad_ShutdownDrain*`, `TestServer_GracefulShutdownWith30sDrain` |
| 4 | L-2: heartbeat context | `cmd/them/main.go` | `TestServer_GracefulShutdownWith30sDrain` (lifecycle) |
| 5 | L-3: subscribe stop ordering | `internal/server/server.go`, `cmd/them/main.go` | shutdown ordering test |

---

## Commit plan

1. `fix(event): R-0 L-1 — termCh for terminal event must-deliver guarantee`
   Files: `internal/event/bus.go`, `internal/event/bus_test.go`,
          `internal/ws/handler.go`, `internal/ws/handler_test.go`,
          `internal/sse/handler.go`, `internal/sse/handler_test.go`,
          `go/TEST_INDEX.md`

2. `fix(session): R-0 T-2 — AppID and TenantID in SessionInfo`
   Files: `internal/session/session.go`, `internal/session/session_test.go`,
          `internal/ws/handler.go`, `internal/sse/handler.go`

3. `fix(server): R-0 OD-7+L-2+L-3 — configurable drain timeout, heartbeat ctx, subscribe stop ordering`
   Files: `internal/config/config.go`, `internal/config/config_test.go`,
          `internal/server/server.go`, `internal/server/server_test.go`,
          `cmd/them/main.go`

4. `docs: R-0 implementation report and handover`
   Files: `docs/architecture-v2/R0_IMPLEMENTATION_REPORT.md`,
          `docs/architecture-v2/implementation-status.md`,
          `docs/architecture-v2/lessons-learned.md`,
          `docs/architecture-v2/NEXT_SESSION_HANDOVER.md`

---

## Items explicitly NOT in R-0

- A-2 (shared transport.HandleSession preamble) — deferred
- R-1 through R-5 — all frozen until R-0 passes go test -race ./...
- Any new data-plane feature
- Tenant schema changes
- External API contract changes

---

## Boundary check

Every planned change belongs to R-0 per CRITICAL_RUNTIME_BLOCKING_DECISIONS.md:
- L-1 → OD-1 → Phase R-0 ✓
- T-2 → OD-2 → Phase R-0 ✓
- OD-7 → Phase R-0 ✓
- L-2 → listed in NEXT_SESSION_HANDOVER.md Phase R-0 scope ✓
- L-3 → listed in NEXT_SESSION_HANDOVER.md Phase R-0 scope ✓
