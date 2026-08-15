# Go-Native Engineering Gate
# Date: 2026-07-26
# Scope: Full Go codebase review — architecture, lifecycle, concurrency, security, tenant readiness
# Requested by: NEXT_SESSION_HANDOVER.md — gate must complete before Wave 7 Phase 3

---

## Purpose

This document evaluates whether the current Go gateway is architecturally Go-native or a
structural copy of the Python implementation. It produces:

1. Findings classified by severity
2. A mandatory checklist for all future Go work
3. A gate verdict: whether Wave 7 Phase 3 may proceed

---

## 1. Architecture Boundaries

### 1.1 Handler → Service → DAL dependency direction

The layering is correctly implemented and consistently applied:

- `cmd/them/main.go` wires concrete types and passes them via interfaces — no business logic.
- `internal/admin/*.go` handlers are thin HTTP translators: parse input, call service, write response.
- `internal/admin/service/` holds validation, defaults, error mapping, crypto orchestration.
- `internal/admin/dal/` holds all SQL. Handlers never see SQL; the service layer never sees `pgx`.
- `internal/crypto/` is a pure computation package with zero imports from the `internal/` graph
  except stdlib.

The import direction is clean: `handler → service → dal → (pgx, stdlib)`.
`crypto` is imported by `service` only. `handler` never imports `crypto`. Correct.

**Verdict: PASS.**

### 1.2 Package ownership and dependency direction

The `internal/transport/` package correctly defines shared interfaces (`Authenticator`,
`SessionStore`, `GateStore`, `EPConfigLoader`, `TemporalClientExecutor`) that both `ws` and `sse`
consume via type aliases — avoiding duplication without creating circular imports.

The `internal/auth/` package owns JWT + bearer token logic. The admin middleware correctly calls
`auth.ClaimsFromCtx` (context accessor) without depending on crypto internals.

**Finding A-1 (low-risk cleanup):** `newID()` is defined three times — in
`internal/ws/id.go`, inline in `internal/sse/handler.go` (line 56), and in `internal/a2a/server.go`.
All three are identical UUID v4 generators. This is minor code duplication, not a correctness
risk (UUID v4 is deterministic by stdlib), but it creates three separate maintenance points.
A single `internal/idgen/id.go` package would eliminate this.

### 1.3 Duplicated business logic

No business logic duplication was found between WS and SSE handlers. Both import `transport`
interfaces; authentication, gate, EP config loading, and session registration follow identical
patterns (as they must — same wire contract). The duplication is structural (same flow), not
logical (no divergent rules).

**Finding A-2 (architecture debt):** The WS and SSE `ServeHTTP` methods are ~450 lines each and
are structurally near-identical. They share interfaces but not implementation. The common
auth→gate→session→orchestration→stream sequence is not extracted into a shared function.
This is currently not a correctness issue, but future changes to the admission flow (e.g., adding
tenant context injection) must be applied in two files — a maintenance risk. A shared
`transport.HandleSession` function accepting transport-specific callbacks would eliminate it.
This is deferred, not blocking.

### 1.4 Oversized wiring / hidden coupling

`cmd/them/main.go` is 278 lines of explicit dependency injection. Each component is constructed
in order with its dependencies visible. There is no service locator or global state. This is
correct Go construction — not hidden coupling.

One observation: `LLMProviderService` is not yet wired in `main.go` because Phase 3 handlers
don't exist yet. When Phase 3 is implemented, the handler must receive `cfg.SecretKey` (or a
pre-derived key) through the constructor. The pattern is already established by the service
itself (`NewLLMProviderService(d Dal, secretKey string)`).

---

## 2. Go-Native Lifecycle

### 2.1 context.Context propagation

All handler paths propagate `r.Context()` to service calls, gate checks, session operations,
and DB queries. The orchestrator receives a cancellable context derived from the request context.
The LLM HTTP call (`internal/llm/anthropic.go`) uses the context for cancellation.

In `ws/handler.go` and `sse/handler.go`, a child context with cancel is created at step 11
(`ctx, cancel := context.WithCancel(r.Context())`). This cancel propagates to:
- The inline orchestrator goroutine
- The Temporal workflow runner goroutine
- The `streamEvents` loop

**Verdict: context propagation is correct and complete.**

### 2.2 Cancellation

WS handler: client disconnect triggers `cancel()` via the `clientGone` channel in `streamEvents`.
The cancelled context stops the LLM HTTP call (context propagated to `http.NewRequestWithContext`
in `internal/llm/anthropic.go`). The Temporal workflow goroutine checks `wfRun.Get(ctx, nil)` —
on context cancel, this returns immediately.

SSE handler: `r.Context()` is cancelled when the client disconnects (standard Go net/http behavior).
The `ctx.Done()` case in `streamEvents` handles this. Write errors also call `cancel()`.

**Finding L-1 (correctness risk):** In the WS `streamEvents`, when `orchDone` fires and there
are no more buffered events, the function returns immediately. If the WS client has not yet
received the `done` event (it may be buffered in the event channel), the connection closes before
the client sees termination. The drain loop after `orchDone` mitigates this for the common case,
but the drain exits on the first `default:` (empty select), which can race with events arriving
in the same goroutine scheduler turn. This is a latent event-loss risk under high event throughput
or slow channel operations. The risk is low under normal conditions (orchestration produces events
serially, and the buffer is 256), but it is not zero.

**Classification: low-risk — mitigated by the 256-event buffer. Defer.**

### 2.3 Goroutine ownership

Each goroutine created in the handlers is bounded by the request lifetime via `orchDone` or
`ctx.Done()`. The pod heartbeat goroutine in `main.go` (line 239) is bounded by the process
lifetime context. The `agentReg.Subscribe` and `tokenCache.Subscribe` goroutines are similarly
bounded. The `reconciler.Run` goroutine is bounded by `ctx`.

**Finding L-2 (architecture debt):** The pod heartbeat goroutine in `main.go` uses `ctx :=
context.Background()` (line 65) which is never cancelled. The ticker goroutine therefore has no
shutdown signal other than the process dying. On graceful shutdown (`ListenAndServe` returns),
this goroutine continues to run until the process exits. In practice this is harmless (the
process exits seconds later), but it prevents clean goroutine leak detection in tests and
contradicts the "bounded goroutines" principle. The fix is to derive the heartbeat context from
a cancellable context stored in `run()` that is cancelled before `ListenAndServe` returns.

**Classification: architecture debt — low operational risk. Defer.**

### 2.4 Graceful shutdown

`server.ListenAndServe` listens for SIGTERM/SIGINT, calls `httpServer.Shutdown(ctx)` with a
5-second drain timeout, then calls `Close()` on registered closers in order. The PostgreSQL pool
and Redis client are registered as closers.

**Finding L-3 (architecture debt):** Only `database` and `redisCache` are passed as closers.
The Temporal client is closed via `defer temporalCli.Close()` in `run()` — which fires when
`run()` returns (after `ListenAndServe` completes). This ordering is correct. However, the
`epLoader.Subscribe` goroutine (line 203) and the `agentReg.Subscribe` goroutine (line 133)
are not explicitly stopped before the Redis client closes. These goroutines call
`redisCache.Client().Subscribe(...)` which will fail or panic when the Redis client is already
closed. The race is narrow (drain timeout is 5s), but it exists.

**Classification: architecture debt. Mitigate before production scale-up. Defer for now.**

### 2.5 Channel ownership and bounded queues

The event bus (`internal/event/bus.go`) uses a buffered channel of 256 events per subscriber.
Slow consumers are handled by non-blocking send with `select/default` (drop). The send is
performed while holding `b.mu` to prevent send-on-closed-channel panics. This is correct.

The `orchDone` channel in WS/SSE handlers is an unbuffered struct channel with a single closer
(`defer close(orchDone)`). The `streamEvents` function selects on `orchDone` — no deadlock risk.

**Verdict: channel ownership is correct.**

---

## 3. Concurrency Safety

### 3.1 Shared mutable state

`auth.Cache` (`internal/auth/token_cache.go`) uses `sync.Map` for L1. This is correct for
concurrent reads and writes without external locking.

`session.Store` uses `sync/atomic` for the `activeSessions` counter. Correct — no lock
needed for a single integer counter.

`event.InMemoryBus` uses `sync.Mutex` for both `Subscribe` and `Publish`. Publish holds the
lock during all channel sends to prevent send-on-closed-channel. This is architecturally correct
but can cause brief publish latency if many subscribers have full buffers. For the current usage
(at most a handful of subscribers per context ID), this is acceptable.

### 3.2 Lock scope

`InMemoryBus.Publish` holds `b.mu` for the entire duration of iterating subscribers and sending.
All sends are non-blocking (`select/default`). The lock duration is O(subscribers) with O(1)
per send — acceptable for the expected cardinality.

`auth.Cache` L1 uses `sync.Map` — no explicit lock management needed.

### 3.3 Race and leak risks

The Go race detector ran on `internal/admin/...` and `internal/crypto/...` with 0 data races
reported (Phase 2 result). No race conditions were found during review.

**Finding C-1 (correctness risk):** In `LLMProviderService.maskKey`, after `crypto.Decrypt`
returns plaintext bytes, the code builds a `masked` string as `string(plainBytes[:4]) + "..." +
string(plainBytes[n-4:])`. This performs two string allocations that copy the key bytes. The
subsequent zeroing loop operates on `plainBytes`, but the Go runtime may have already copied
those bytes into the heap for the string allocation. The zeroing is documented as "best-effort
defensive" in the implementation report. This is a known limitation of Go's memory model for
secrets, not a bug introduced here. **No action required before Phase 3.**

### 3.4 Slow-consumer behavior

The event bus drops events for slow consumers (buffer full → `default` in select). This is
intentional and documented. A WS client that cannot receive events fast enough will miss
intermediate tokens; the `done` or `error` event still arrives when the buffer empties.
This is acceptable for the current use case.

### 3.5 Send-on-closed-channel risks

The `InMemoryBus.Publish` holds `b.mu` during sends, and `unsub` closes the channel only while
holding `b.mu`. This correctly serializes the close and the final send. **No risk.**

---

## 4. Resource Efficiency

### 4.1 HTTP, DB, and Redis client reuse

PostgreSQL uses `pgxpool` (connection pool). Redis uses `rueidis` (connection multiplexing).
Both are constructed once in `main.go` and shared via interfaces across all handlers.
No per-request connection creation was found.

### 4.2 Repeated DB/Redis lookups

**Finding R-1 (performance risk):** The LLM provider service `Update` method performs two
database round-trips per PATCH request: `GetProvider` (SELECT) followed by `UpdateProvider`
(UPDATE RETURNING). This is the fetch-then-modify pattern documented in the Wave 7 plan as
an intentional choice over dynamic SQL. For an admin-only, low-frequency endpoint, this is
acceptable. However, this means every PATCH operation — even a single-field update — touches
the DB twice. Under concurrent PATCH operations from multiple admin users, the non-atomic
fetch-then-modify pattern can produce a lost-update anomaly (two concurrent PATCHes may both
read the same old state and overwrite each other's changes).

**Classification: performance risk at low volume; correctness risk under concurrent PATCH.
Acceptable for admin control-plane API (superadmin-only, low concurrency). Defer.**

### 4.3 Unnecessary per-request allocations

No significant per-request allocations were found beyond what is expected for JSON
encoding/decoding and Lua script execution.

### 4.4 Connection and buffer limits

PostgreSQL pool: `DATABASE_POOL_SIZE` env var, default 20. Reasonable.
Redis: `rueidis` default connection pool.
WS event bus subscriber buffer: 256 events per subscriber. Reasonable.
`readTimeout: 15s`, `writeTimeout: 30s`, `idleTimeout: 60s` — set in `server.go`. Correct.

---

## 5. Error and Security Design

### 5.1 Typed errors

The service layer uses typed sentinel errors (`ErrValidation`, `ErrUnprocessable`, `ErrNotFound`,
`ErrConflict`, `ErrTemporalUnavailable`). The `writeServiceError` helper in `admin/middleware.go`
maps these to HTTP status codes.

**Finding S-1 (correctness risk — must fix before Phase 3):** `ErrConflict` is defined in
`service/errors.go` and returned by `LLMProviderService.Create`. However, `writeServiceError`
in `admin/middleware.go` does NOT handle `ErrConflict`. It handles `ErrValidation`,
`ErrUnprocessable`, `ErrNotFound`, and `ErrTemporalUnavailable` — but not `ErrConflict`. When
the Phase 3 handler calls `writeServiceError` on a `Create` conflict, it falls through to
`return false`, and the handler must then write a 500 response. This means a duplicate-name
POST will return 500 instead of 409.

The existing agents/orchestrators/applications handlers do not currently call service methods
that return `ErrConflict` (those handlers have inline duplicate-key checks at the DAL level).
The LLM provider service is the first to use `ErrConflict` through the standard service path.

**Required fix:** Add `case errors.Is(err, service.ErrConflict): writeError(w, http.StatusConflict, err.Error())`
to `writeServiceError` in `admin/middleware.go` before Phase 3 handlers call it.

**File:** `go/internal/admin/middleware.go` line 116.

### 5.2 Safe logging

The `LLMProviderService` logs only `provider_id` and `error_category` on decrypt failure.
No plaintext, ciphertext, or key material appears in log output. The `config.SafeString()`
method excludes `SecretKey`, `JWTSecret`, `DBPassword`, and `AnthropicAPIKey`.

**Finding S-2 (correctness risk):** Multiple admin handlers (agents, applications, runs) include
raw `err.Error()` in 500 responses:

```
writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
writeError(w, http.StatusInternalServerError, "create agent: "+err.Error())
```

PostgreSQL error messages can include table names, column names, constraint names, and query
fragments. These are internal implementation details. Exposing them in HTTP responses to
authenticated admin users is a low-severity information disclosure (admin users already have
elevated trust), but it violates the principle of not leaking internal structure. For the
existing admin-read routes (GET agents, GET orchestrators, etc.), this is a minor issue.
For the LLM provider CRUD routes in Phase 3, it must not leak crypto or key-related error
details.

**Required fix for Phase 3 handlers only:** Phase 3 handlers must not forward `err.Error()`
from service calls to the HTTP response. Use generic messages: `"internal server error"` for
unexpected errors. The service layer already provides specific typed errors for all expected
failure modes.

**Classification: low severity for existing read-only routes. Must fix for Phase 3.**

### 5.3 Crypto boundaries

Crypto is correctly isolated in `internal/crypto/fernet.go`. The service layer imports crypto
directly; handlers do not. `DeriveKey` is called once at service construction and the key is
stored as `[]byte` in the service struct. No key derivation happens per-request.

`THE_M_SECRET_KEY` / `SECRET_KEY` is validated at startup (`config.validate()`): must be
non-empty and non-default. Fail-fast before serving any traffic. Correct.

The Fernet HMAC check precedes decryption (constant-time compare via `crypto/subtle`). Correct.

### 5.4 No secret leakage

`api_key_encrypted` is never serialized to JSON (the `LLMProviderOut` struct has no such field).
`apiKeyEncrypted` never appears in any log call. The `TokenHash` field in `dal.Token` has
`json:"-"`. The `SafeString()` method redacts all secrets. **PASS.**

### 5.5 Correct error translation by layer

DAL → service: `dal.IsNoRows(err)` → `service.ErrNotFound`. `dal.IsUniqueViolation(err)` →
`service.ErrConflict`. The DAL never returns service errors; the service layer translates.
Service → handler: typed errors → HTTP status via `writeServiceError`. Correct pattern.

---

## 6. Tenant Readiness

Reference: `TENANT_FOUNDATION_DECISIONS.md`.

### 6.1 Global ownership assumption

The Wave 7 LLM provider routes operate on a platform-global table with no `tenant_id`. This is
the correct design per `TENANT_FOUNDATION_DECISIONS.md §3`. No tenant assumption is encoded.

### 6.2 Tenant context propagation path

The auth middleware places claims in context via `auth.ClaimsFromCtx`. When tenant context
is added (future wave), a `TenantID` field in `Claims` will propagate naturally through the
existing context chain. No new propagation infrastructure is needed for Wave 7.

**Finding T-1 (architecture debt):** `session.SessionInfo` has no `TenantID` field. When tenant
context is required (per `TENANT_FOUNDATION_DECISIONS.md §5`), adding `TenantID` to
`SessionInfo` will require changes to:
- `session.Register` / `session.End` call sites in WS and SSE handlers
- `sessionInfoToFields` / `fieldsToSessionInfo` serialization helpers
- Redis session hash stored in `them:sess:{id}`

This is planned work, not a defect. The session struct is correctly minimal for now.

**Finding T-2 (architecture debt):** `session.SessionInfo.AppID` is set from
`resolvedCfg.AppID` in the WS handler (line 319 via `gateCfg`), but `sessInfo.AppID` is NOT
set in the WS handler session registration (lines 358–366). The `AppID` field exists on
`SessionInfo` but is left empty when registering the session hash. This means
`CountAppSessions` and `ListAppSessions` can only work correctly if the gate has already
registered the session with the app Set — which it does. However, the session Hash itself
does not record `AppID`, making it impossible to derive the application boundary from a
session lookup alone without joining through the EP slug. This is an acceptable limitation
today (the gate owns app membership), but will complicate tenant-scoped session admin queries
in the future.

**Classification: architecture debt — acceptable for current single-tenant operation. Defer.**

### 6.3 Expensive retrofitting risk

The `internal/admin/dal/` functions for agents, orchestrators, and applications do not include
a `tenantID` parameter. When tenant scoping is added (future wave), every DAL function
signature and every SQL query in those files must be updated. This is expected and documented
in `TENANT_FOUNDATION_DECISIONS.md §6` (Tier 0 required additions).

The `service.Dal` interface will similarly need `tenantID` parameters added to all tenant-scoped
operations. Because Go interfaces are structural, all existing implementations (`dal.DB`,
test fakes) must be updated simultaneously. This is a planned, bounded migration — not
unexpected complexity introduced by the current design.

**No blocking findings.** The current code does not make the tenant migration more expensive
than already planned.

---

## 7. Python-Copy Detection

The migration goal is to preserve Python's wire contract while discarding Python's
implementation structure. The following review classifies each area.

### 7.1 Areas that correctly discard Python structure

| Area | Python approach | Go approach | Classification |
|---|---|---|---|
| History loading | Python full-scan then slice | Go DB-level `LIMIT` in query | **Go-native** |
| Ghost session pruning | Python TTL bug (no pruning) | Go atomic Lua SREM+shadow pattern | **Go-native improvement** |
| Session count | Python hardcoded 0 in heartbeat | Go `atomic.LoadInt32` counter | **Go-native improvement** |
| HMAC check order | Python delegates to library | Go explicit constant-time before decrypt | **Go-native** |
| Fernet key derivation | Python base64-encodes for Fernet | Go uses raw bytes directly | **Go-native** (same result) |
| Token L1 cache | Python no cache (auth service every time) | Go sync.Map L1 + Redis L2 | **Go-native improvement** |
| Admission gate | Python no gate (no concurrent session cap) | Go atomic Lua admission script | **Go-native improvement** |

### 7.2 Areas that are acceptable compatibility code

| Area | Retained Python detail | Reason acceptable |
|---|---|---|
| Fernet wire format | `enc:` prefix, 0x80 version byte, big-endian timestamp | Required for interoperability with existing DB data |
| Masking rules | `"****"` for ≤8 chars, `first4...last4` for >8 chars | Wire-compatible API contract |
| `model_pricing` defaults to `{}` | Same as Python ORM default | API contract |
| `enabled` defaults to `true` | Same as Python Pydantic default | API contract |
| Hard delete for LLM providers | `DELETE` not soft-disable | Python contract |
| Workflow ID scheme `ctx-{contextID}` | Matches Python's `OrchestrationWorkflow` registration | Required for HITL signals |
| `?token=` query param auth | Same as Python WS auth fallback | Client compatibility |

All of these are **acceptable compatibility code** — they preserve the wire contract, not the
Python implementation internals.

### 7.3 Areas that copy Python structure without justification

**Finding P-1 (architecture debt):** The WS and SSE `ServeHTTP` methods copy Python's sequential
admit-register-confirm pattern step-by-step with numbered comments (`── 1.`, `── 2.`, etc.).
This is not wrong — it is the correct sequence — but the comments suggest the code was written
by translating Python flow control into Go rather than designing from the Go interface contracts.
The correctness is not affected, but the style makes it harder to see where Go has improved on
Python versus where it is merely replicating it.

**Finding P-2 (low-risk cleanup):** `slog.Warn("llm_providers: api_key decrypt failed", ...)` in
`LLMProviderService.maskKey` uses `slog.Warn` directly rather than the injected logger. This
means the log entry does not carry the structured context of the request (request ID, instance ID)
that a properly injected `*slog.Logger` would provide. The Python code similarly has no request
correlation for decrypt failures. Acceptable for now; the handler logger should be passed to
the service in a future cleanup.

**Finding P-3 (low-risk cleanup):** `epconfig.CheckAccess` is a free function in `epconfig`
package called from both WS and SSE handlers. This mirrors Python's procedural style. A
method on `EPConfig` would be more idiomatic, but this is a style concern only.

### 7.4 Summary classification

| Finding | Classification |
|---|---|
| A-1: triple `newID()` definition | low-risk cleanup |
| A-2: WS/SSE code duplication | architecture debt |
| L-1: orchDone drain race | low-risk |
| L-2: heartbeat goroutine no shutdown | architecture debt |
| L-3: Subscribe goroutines not stopped before Redis close | architecture debt |
| R-1: fetch-then-modify PATCH | performance risk (acceptable) |
| **S-1: ErrConflict missing from writeServiceError** | **correctness risk — must fix** |
| S-2: err.Error() in 500 responses | low severity (admin-only); must fix in Phase 3 handlers |
| T-1: SessionInfo no TenantID | architecture debt (planned) |
| T-2: AppID not set in session hash | architecture debt (planned) |
| P-1: numbered sequential comments | low-risk cleanup |
| P-2: slog.Warn not injected logger | low-risk cleanup |
| P-3: epconfig.CheckAccess free function | low-risk cleanup |

---

## 8. Mandatory Checklist for Future Go Work

Every future Go migration task must verify each item before committing.

### 8.1 Before writing any handler

- [ ] Is this control-plane or data-plane? (See `TENANT_FOUNDATION_DECISIONS.md §7.5`)
- [ ] What Python contract must be preserved? (status codes, field names, ordering, defaults)
- [ ] What Python implementation detail must NOT be copied? (full-scan, no caching, no pooling)
- [ ] Is the resource platform-global or tenant-owned? (no tenant param if global)
- [ ] Which Traefik router rule takes priority over others? (verify no overlap with existing rules)

### 8.2 Context and cancellation

- [ ] Every DB/Redis/HTTP call receives a `context.Context` from the request or a bounded child.
- [ ] Long-running goroutines have a shutdown path via `ctx.Done()` or a `done` channel.
- [ ] Context is derived from `r.Context()` (not `context.Background()`) unless at process scope.
- [ ] Cancellation propagates: WS disconnect → cancel → LLM HTTP cancel.

### 8.3 Concurrency and buffer limits

- [ ] No shared mutable state without mutex or atomic.
- [ ] Channel sends are non-blocking or the send site cannot block the critical path.
- [ ] Goroutines are bounded — caller can observe their completion.
- [ ] Event bus subscriptions always have `defer unsub()`.

### 8.4 Resource ownership

- [ ] DB connections are from the pool — never opened per-request.
- [ ] Redis clients are from the shared factory in `cache/` — never constructed per-request.
- [ ] HTTP clients for outbound calls (LLM, agents) reuse a shared `*http.Client`.
- [ ] `rows.Close()` is always deferred after `Query`.

### 8.5 Graceful shutdown

- [ ] New long-running goroutines in `main.go` are bounded by a cancellable context.
- [ ] Resources opened in `main.go` are registered as `Closer` with the server, or closed via defer.
- [ ] New goroutines do not hold Redis/DB clients past process shutdown.

### 8.6 Observability

- [ ] New packages accept `*slog.Logger` via constructor — no `slog.Default()` calls in library code.
- [ ] Log lines include structured key-value fields, not interpolated strings with sensitive data.
- [ ] 5xx responses never include raw `err.Error()` from internal packages.
- [ ] Prometheus metrics are registered for new high-cardinality operations (future wave).

### 8.7 Error handling by layer

- [ ] DAL returns raw `pgx` errors. Service maps them to typed sentinels. Handler maps sentinels to HTTP.
- [ ] `writeServiceError` handles every sentinel that any called service method can return.
- [ ] No SQL in handlers. No HTTP types in service. No pgx types in service.

### 8.8 Security

- [ ] No secret (API key, hash, plaintext key material) in any log line.
- [ ] `crypto.Decrypt` output is zeroed after use.
- [ ] `api_key_encrypted` never appears in JSON output structs.
- [ ] 500 responses use generic messages — never `err.Error()` from internal packages.
- [ ] New config fields that are secrets must be excluded from `SafeString()`.

### 8.9 Tenant ownership

- [ ] Platform-global resources: no `tenant_id` parameter anywhere.
- [ ] Tenant-owned resources (future): DAL function signatures include `tenantID string`.
- [ ] Context resolution: tenant comes from JWT claim or bearer token lookup — never from request body or query param.
- [ ] Cross-tenant access returns 403, not 404.

### 8.10 Tests

- [ ] Every new handler method has at least one unit test in `admin_test.go`.
- [ ] Every new service method has a unit test in `service/*_test.go` using `fakeDal`.
- [ ] Every new DAL method has an integration test in `dal/*_integration_test.go`.
- [ ] `go test ./...` passes before every commit.
- [ ] `go test -race ./internal/<package>/...` passes before every PR merge.
- [ ] `TEST_INDEX.md` is updated in the same commit as the test file.

### 8.11 Route ownership

- [ ] Handler exists in `go/internal/`.
- [ ] Route registered in `cmd/them/main.go` (or via `BuildRouter`).
- [ ] Traefik label applied in `docker-compose.yml`.
- [ ] Live request through port 8088 confirmed in Go bridge logs.
- [ ] Python equivalent left in place until cutover is confirmed.

### 8.12 Rollback

- [ ] Traefik router block is isolated (one block per migration unit).
- [ ] Removing the block and restarting Go bridge restores Python serving within seconds.
- [ ] Python tests still pass after Go cutover (Python bridge on 8001 is unchanged).

---

## 9. Overall Go Quality Verdict

The Go gateway is **architecturally Go-native** where it matters:

- DB-level history LIMIT (not Python full-scan)
- Atomic Lua admission gate (not Python no-gate)
- Shadow-TTL ghost pruning (fixes Python TTL bug)
- Atomic session counter in heartbeat (fixes Python hardcoded 0)
- Two-level token cache with cross-pod pub/sub eviction
- Correct HMAC-before-decrypt ordering in Fernet
- stdlib-only Fernet (no external crypto dependency)
- Clean Handler → Service → DAL → Crypto separation
- Interfaces at every boundary (testable without live infra)

The code contains **no structural copies of Python implementation** in the critical paths.
Where Python patterns appear (sequential admit-register-confirm steps), they represent the
correct contract, not Python internals.

The identified findings are categorized as follows:

---

## 10. Must-Fix Findings (block Phase 3)

### MF-1 — ErrConflict not handled in writeServiceError

**File:** `go/internal/admin/middleware.go` line 116  
**Finding:** S-1 (correctness risk)  
**Impact:** Phase 3 `LLMProvidersHandler.Create` returns `ErrConflict` on duplicate name.
`writeServiceError` has no case for `ErrConflict`. The handler falls through, writes no response,
and the Go runtime sends a 500. The correct response is 409 Conflict.  
**Fix:** Add one `case errors.Is(err, service.ErrConflict): writeError(w, http.StatusConflict, err.Error())` to `writeServiceError`.  
**Effort:** 2 lines. Must be in the same commit as the Phase 3 handler.

### MF-2 — Phase 3 handlers must not forward err.Error() in 500 responses

**Finding:** S-2 (must fix for Phase 3 handlers, low severity for existing handlers)  
**Impact:** Service-layer errors for LLM provider operations could include DB error messages
with internal table/column names. These must not be returned to HTTP clients.  
**Fix:** Phase 3 handler 500 responses must use static strings (`"internal server error"`) not
`"create provider: " + err.Error()`. The service layer already provides typed errors for all
expected failure modes; only unexpected panics/infra errors should reach the 500 path.  
**Effort:** Enforce in code review for Phase 3. No code change to existing files needed.

**Must-Fix Count: 2**

One requires a code change (MF-1). One is a review constraint enforced during Phase 3
implementation (MF-2).

---

## 11. Deferred Findings

| ID | Finding | Classification | Effort estimate |
|----|---------|----------------|----------------|
| A-1 | Triple `newID()` definition | low-risk cleanup | 30 min |
| A-2 | WS/SSE ServeHTTP code duplication | architecture debt | 2–3 days |
| L-1 | orchDone drain race | low-risk | 1 hour |
| L-2 | Heartbeat goroutine missing shutdown | architecture debt | 30 min |
| L-3 | Subscribe goroutines vs Redis close order | architecture debt | 1–2 hours |
| R-1 | Fetch-then-modify PATCH double SELECT | performance risk | — (acceptable, document) |
| T-1 | SessionInfo missing TenantID | architecture debt | planned tenant wave |
| T-2 | AppID not set in session Hash | architecture debt | planned tenant wave |
| P-1 | Numbered sequential comments | low-risk cleanup | style-only |
| P-2 | maskKey uses slog.Warn not injected logger | low-risk cleanup | 30 min |
| P-3 | epconfig.CheckAccess free function style | low-risk cleanup | style-only |

**Deferred Count: 11**

None of the deferred findings affect correctness of the Wave 7 Phase 3 routes, which are
admin-only control-plane APIs on a platform-global table.

---

## 12. Whether Wave 7 Phase 3 May Proceed

**YES — Wave 7 Phase 3 may proceed, with one mandatory pre-commit fix.**

### Condition

Before the first Phase 3 commit is made, `writeServiceError` in `go/internal/admin/middleware.go`
must be updated to handle `service.ErrConflict` → HTTP 409. This is a 2-line change. It can be
added in the same commit as the Phase 3 handlers, but must not be omitted.

### Rationale

- All critical security constraints (key encryption, no plaintext in responses or logs, startup
  validation, HMAC ordering) are correctly implemented and tested.
- The Handler → Service → DAL → Crypto layering is clean and complete.
- The `LLMProviderService` and its DAL are fully tested with unit and integration tests.
- The Fernet compatibility is confirmed bidirectionally with known-vector tests.
- The platform-global resource model is correct for Wave 7 routes — no tenant parameter needed.
- The existing Traefik rollback pattern is proven (used in Waves 5 and 6).
- The single must-fix finding (ErrConflict in writeServiceError) is small, well-scoped, and
  not a risk to any existing routes.

### Exact Next Action

1. Add `case errors.Is(err, service.ErrConflict): writeError(w, http.StatusConflict, err.Error())`
   to `writeServiceError` in `go/internal/admin/middleware.go`.
2. Verify `go test ./internal/admin/...` still passes.
3. Implement Phase 3 handlers following the Wave 7 plan exactly:
   - `GET /admin/llm-providers` → `service.List`
   - `POST /admin/llm-providers` → `service.Create` (body validation; APIKey must not be logged)
   - `GET /admin/llm-providers/{id}` → `service.Get`
   - `PATCH /admin/llm-providers/{id}` → `service.Update` (APIKeyPresent via raw JSON decode)
   - `DELETE /admin/llm-providers/{id}` → `service.Delete`
4. Add handler tests following the existing `admin_test.go` fake-DB pattern.
5. Register the handler in `admin.BuildRouter`.
6. Update `TEST_INDEX.md` in the same commit.
7. Implement Traefik cutover in a separate commit after all unit tests pass.
8. Confirm live request reaches Go bridge via logs on port 8088.
