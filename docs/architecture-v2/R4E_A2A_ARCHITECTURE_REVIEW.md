# R-4e A2A Architecture Review
# Inbound A2A Tenant Propagation and Execution Path Alignment
# Date: 2026-08-01
# Status: REVIEW ONLY — no production code changes in this document

---

## 1. Purpose

This document answers the architecture questions for R-4e before any code is written.

The current inbound A2A path (`/a2a/{app_slug}`) bypasses authentication, EP config,
tenant identity, admission gate, session registration, and Temporal. It calls the
orchestrator directly in-process. This document:

- documents the current lifecycle precisely
- challenges the proposed target flow
- states what fits, what does not, and why
- proposes a minimal design that closes the gap without overengineering
- defines exact R-4e scope, risks, and exclusions

---

## 2. Current Inbound A2A Request Lifecycle

### 2.1 Actual flow in `go/internal/a2a/server.go`

```
POST /a2a/{app_slug}
│
├── 1. JSON-RPC parse  — decode body into rpcRequest
├── 2. Version check   — jsonrpc == "2.0" guard only
├── 3. Method dispatch — "message/send" → handleMessageSend
│
└── handleMessageSend:
    ├── 4. Param decode        — extract messageSendParams
    ├── 5. Text extraction     — first text part only; empty → error
    ├── 6. newID() × 2         — runID, contextID (random hex, not UUIDs)
    ├── 7. bus.Subscribe       — event bus subscription BEFORE orch.Run
    ├── 8. recorder.CreateRun  — INSERT them.runs (id, context_id, status, started_at only)
    │                            NO entry_point_slug, NO tenant_id
    └── 9. orch.Run            — DIRECT in-process orchestrator call
                                 returns finalText string
    ├── 10. drainEvents        — best-effort channel drain
    └── 11. writeRPCResult     — A2A response with "completed" status + text artifact
```

### 2.2 What the current A2A path does NOT do

| Capability | WS/SSE | A2A |
|---|---|---|
| Bearer token authentication | Yes (tryAuthenticate) | **No** |
| EP config resolution (DB) | Yes (epLoader.Load) | **No** |
| Access check (enabled, blocked list) | Yes (epconfig.CheckAccess) | **No** |
| Admission gate (cap, rate limit, queue) | Yes (gate.Check/Confirm/Release) | **No** |
| Redis session registration | Yes (session.Register) | **No** |
| Run records with tenant_id | Yes (R-4d) | **No — tenant_id missing** |
| Run records with entry_point_slug | Yes | **No** |
| Temporal workflow dispatch | Yes | **No — direct orch.Run** |
| Tenant identity propagation | Yes (R-4d) | **No** |
| Context cancellation to Temporal | Yes | **No** |
| HITL support | Yes (via Temporal signal) | **No** |
| Graceful shutdown integration | Yes (workflow cancel) | **No** |

### 2.3 What `app_slug` currently means in A2A

The route is `POST /a2a/{app_slug}`. The `app_slug` parameter is extracted by chi but
**never read** inside `handleRPC` or `handleMessageSend`. It is present in the URL but
ignored entirely. The handler has no concept of which application it is serving.

### 2.4 What `agentregistry.Registry` does (outbound A2A)

`agentregistry` is the **outbound** A2A invoker. When the orchestrator's tool loop needs
to call an external agent, it goes through `Registry.Invoke → invokeA2A`, which:
- builds a `message/send` JSON-RPC request
- POSTs it to the agent's `EndpointURL`
- sets `Authorization: Bearer <cfg.AuthToken>` if configured
- returns the text from the response artifact

`agentregistry` has no role in the **inbound** A2A path. It is the client side;
`a2a/server.go` is the server side. They are independent.

---

## 3. Current Authentication Behavior

**None.** The A2A handler reads the `Authorization` header from `http.Request` but never
calls any auth method. There are no calls to `auth.Cache.Validate`, `extractBearer`, or
any middleware on the `/a2a/` route. Any HTTP client can POST to `/a2a/{any_slug}` with
no credentials and will receive an orchestrator response.

The agent card at `GET /.well-known/agent.json` declares no security requirements
(`Streaming: false` only). This understates the actual security posture the platform
requires.

---

## 4. How the Target Agent/Application Is Selected

Currently: it is not. The `app_slug` path parameter is extracted by the chi router but
discarded at the handler level. The server holds a single `*orchestrator.Orchestrator`
reference injected at construction time. Every A2A request to any slug hits the same
orchestrator.

This means:
- There is no per-application orchestrator selection
- There is no per-application rate limiting
- There is no entry-point validation
- There is no way to serve multiple applications through the A2A path

---

## 5. What A2A Currently Represents

The current inbound A2A implementation represents **one fixed orchestrator**, not:
- one platform application (no application resolution)
- one exposed agent (no agent card skill mapping)
- a generic multi-agent endpoint (no routing between applications)

It is effectively a thin JSON-RPC wrapper around a hardcoded orchestrator call.
The `{app_slug}` in the URL is purely cosmetic.

The correct model — what R-4e should move toward — is **one A2A endpoint per application
entry point**. An application that has been configured to expose itself as an A2A agent
registers a slug, and `POST /a2a/{app_slug}` routes to that application's orchestrator
with that application's tenant identity, EP config, and admission controls.

---

## 6. Where Tenant and Application Identity Should Be Resolved

### The proposed flow says:

> authenticate trusted caller → resolve target application / entry point server-side
> → derive TenantID and ApplicationID from trusted platform data

### Assessment: correct in principle, but the binding needs clarity.

**TenantID and ApplicationID must come from the EP config resolved by `app_slug`.**

The `app_slug` in the URL uniquely identifies an entry point (the `them.entry_points.slug`
column is UNIQUE). The `epconfig.Loader.Load(ctx, app_slug)` call retrieves the
`EPConfig` struct which contains both `AppID` and `TenantID` from the DB. This is the
same trusted server-side resolution that WS and SSE use.

**TenantID must never come from:**
- Request headers (`X-Tenant-ID` or any variant)
- Query parameters
- JSON-RPC payload fields (`params`, `message`, `metadata`)
- Caller-supplied metadata in the A2A message

**The `app_slug` is the trusted binding.** It is a URL path segment controlled by the
platform route table, not the caller. A caller can only reach a specific `app_slug` if
that path exists. The `EPConfig` resolved from that slug is authoritative for identity.

### Should the A2A path require a bearer token?

**Yes, for most entry points. Conditionally for public entry points.**

The existing `epconfig.AccessMode` field already encodes this decision:
- `AccessModeToken` ("token") → bearer token required; reject 401 if absent/invalid
- `AccessModePublic` ("public") → no bearer token required

WS and SSE already implement this: `tryAuthenticate` is non-enforcing (it reads the token
if present) and `CheckAccess` enforces the policy. The A2A handler should follow the
same pattern:

1. Attempt bearer token extraction (non-enforcing)
2. Load EPConfig from `app_slug` (enforcing — 404 if slug not found)
3. Call `CheckAccess` with token hash and user ID (enforcing — 403 if blocked/disabled)
4. If `AccessMode == token` and no valid token → 401

This preserves the access mode flexibility that the WS/SSE path already supports.

### Bearer token vs A2A auth metadata vs agent card URL as trusted binding

| Binding | Trusted? | Notes |
|---|---|---|
| `app_slug` in URL path | **Yes** | Platform-controlled route; DB-resolved to EPConfig |
| Bearer token in `Authorization` header | **Yes** | Platform-issued opaque token; resolved via `auth.Cache` |
| A2A auth metadata in JSON-RPC params | **No** | Caller-supplied; cannot be trusted for identity |
| Agent card URL | **No** | Static card; cannot carry per-request identity |
| `X-Tenant-ID` header | **No** | Never trusted; hard rule from R-4b |

**Recommended binding:** `app_slug` → EPConfig (primary identity). Bearer token →
token hash for rate limiting and block-list checks (secondary). This exactly mirrors
the WS/SSE pattern.

---

## 7. How `message/send` Maps to a Temporal Run

### Current mapping (A2A → direct orch.Run)

```
message/send → orch.Run(ctx, runID, contextID, userMsg, nil) → finalText
```

Synchronous, blocking, in-process. No Temporal involvement.

### Proposed mapping (A2A → Temporal)

```
message/send
  → resolve EPConfig (from app_slug)
  → auth check
  → create run record (with tenant_id, entry_point_slug)
  → gate.Check (admission)
  → session.Register (Redis)
  → gate.Confirm
  → bus.Subscribe (BEFORE ExecuteWorkflow)
  → temporal.ExecuteWorkflow(WorkflowInput{...})
  → block until workflow completes (aggregate events from run-stream)
  → drain result from WorkflowResult
  → cleanup: session.End, gate.Release
  → return A2A rpcResult with "completed" state + text artifact
```

This is the correct design. It aligns A2A with the WS/SSE execution contract:
Temporal is the single durable owner of every run.

### Is synchronous blocking on ExecuteWorkflow safe for A2A?

**For the initial R-4e scope: yes, with a timeout.**

The A2A spec supports both synchronous (`returnImmediately: false`, the default for
`message/send`) and asynchronous semantics. For R-4e, implementing synchronous A2A
(block HTTP connection until workflow completes) is simpler and correct:
- HTTP connections can be long-lived; the A2A caller controls the client timeout
- The orchestrator already runs within Temporal's `activityStartToClose` (10 min)
- Context cancellation from HTTP disconnect propagates correctly through Go's
  `r.Context()` → `ExecuteWorkflow` → workflow cancel → activity cancel → LLM HTTP cancel

**Async A2A (task ID + polling) is out of scope for R-4e.** It requires task state
persistence, `GetTask` implementation, and A2A-specific task lifecycle management.
That is a separate, larger feature.

---

## 8. Synchronous vs Asynchronous A2A Semantics

### A2A spec semantics

The A2A spec `message/send` can be:
- **Synchronous** (`returnImmediately: false`, default): server blocks HTTP until task
  reaches a terminal state, then returns the result inline.
- **Asynchronous** (`returnImmediately: true`): server returns a `taskId` immediately;
  caller polls `GetTask` or streams `subscribeToTask`.

### R-4e recommendation

**Implement synchronous A2A only (blocking).**

Rationale:
1. The current implementation is synchronous (`orch.Run` blocks)
2. The WS/SSE paths stream events but also block for the duration of the run
3. Synchronous A2A preserves the existing contract for any caller already using `/a2a`
4. Temporal handles durability, heartbeating, and crash recovery internally
5. Async A2A requires task state storage and polling endpoints — a separate feature

For R-4e, `message/send` should block until `ExecuteWorkflow` returns, then return the
`WorkflowResult.FinalText` as a text artifact with state "completed" or the error code
on failure. This is what the current code does, just through Temporal instead of
direct `orch.Run`.

---

## 9. Task ID, Context ID, Run ID, Session ID Relationships

### A2A spec concepts

| ID | A2A purpose |
|---|---|
| `taskId` | Identifies a specific task execution within a context |
| `contextId` | Groups related tasks (conversation/session) |
| `messageId` | Identifies a single message within a task |

### Platform concepts

| ID | Platform purpose |
|---|---|
| `runID` | `them.runs.id` — a single execution, persisted to DB |
| `contextID` | In-memory grouping for event bus fan-out, also A2A contextId |
| `sessionID` | Redis session hash key — admitted gate entry |

### Recommended mapping for R-4e

| A2A concept | Platform mapping |
|---|---|
| `taskId` | `runID` (new hex random ID, same as WS/SSE) |
| `contextId` | `contextID` (from `params.message.contextId` if provided; else generate new) |
| `messageId` | not persisted; used for request deduplication only |
| `sessionID` | new random ID for the gate/session lifecycle, scoped to this HTTP request |

**The `contextId` from the A2A request params is trusted for grouping but NOT for
identity.** If the caller provides a `contextId`, it can be used as the event bus
key (matching the A2A multi-turn threading model). If absent, generate a new one.
Either way, `TenantID` and `ApplicationID` come from EPConfig, not from `contextId`.

A `contextId` carrying a pre-existing conversation thread is fine — it means the
orchestrator should load prior message history for that context. This is already how
the WS `last_event_id` / SSE `Last-Event-ID` works for multi-turn continuations.

---

## 10. Should the Admission Gate and Redis Session Registration Apply to A2A?

**Yes, both must apply.**

### Gate (admission control)

The gate enforces:
- EP-level concurrent session cap (`EPMaxConcurrent`)
- App-level concurrent session cap (`AppMaxConcurrent`)
- Per-token rate limit (`RateLimitRPM`)
- Queue with timeout when cap is reached and `QueueTimeout > 0`

An A2A call that bypasses the gate can:
- exceed the EP session cap, causing policy violations
- bypass rate limits, allowing abuse
- interfere with the dashboard session count (which reads gate-managed Redis sets)

A2A requests are long-running (synchronous, can take minutes). They must be gated
with the same admission rules as WS/SSE connections.

**The full gate lifecycle applies: `Check → Register → Confirm` on entry;
`End + Release` on exit.** The gate config is populated from EPConfig exactly as
WS/SSE do it.

### Session registration (Redis)

Session registration in Redis (`session.Register`) serves:
- Dashboard visibility (session counts, active session list)
- Admin disconnect (`session.SignalDisconnect`)
- Pod heartbeat accurate session count

An A2A request that bypasses session registration:
- is invisible to the dashboard
- cannot be forcibly disconnected by an admin
- does not count toward the pod's active session count

For R-4e, session registration must apply. The session type can be indicated via
`EPType` (already on `EPConfig`) or via a new field on `SessionInfo` if A2A-specific
metadata is needed. No new fields are required for R-4e; the existing `SessionInfo`
fields are sufficient.

---

## 11. How Results, Status Updates, Artifacts, and Errors Map to A2A Responses

### Current response shape (correct for synchronous A2A)

```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": {"state": "completed"},
    "artifacts": [
      {"parts": [{"kind": "text", "text": "<final LLM output>"}]}
    ]
  },
  "id": "<request id>"
}
```

This shape is structurally valid A2A. The issues are:
1. The part type field is `"kind"` — A2A spec uses `"text"` as the discriminator key,
   not `"kind"`. The current wire format is `{"kind": "text", "text": "..."}` which
   should be `{"text": "..."}` (the field name alone is the discriminator, per spec).
   This is a minor wire format bug. Whether existing callers depend on `"kind"` needs
   verification before changing it.
2. No `taskId` in the response. A2A spec includes the task ID in the result so callers
   can correlate the response. Add `TaskID string` to `rpcResult`.
3. Error states should map to A2A error codes, not just `-32603 internal error`.

### Recommended minimal mapping for R-4e

| Platform event | A2A response |
|---|---|
| `WorkflowResult.Status == completed` | `status.state = "completed"` + text artifact |
| `WorkflowResult.Status == failed` | JSON-RPC error response (code -32603) |
| `ErrCapExceeded` (gate) | HTTP 429 (rate limit) OR JSON-RPC error |
| `ErrDisabled` (EP) | HTTP 403 |
| `ErrNotFound` (EP) | HTTP 404 |
| Context cancelled (client disconnect) | Workflow cancelled; no response (connection gone) |

**HITL (`input_required`) for synchronous A2A:** not required in R-4e. A synchronous
A2A request cannot interactively receive human input mid-flight. If the workflow reaches
`input_required`, it should fail with an appropriate error. HITL A2A support requires
async semantics and is out of scope.

---

## 12. Whether `agentregistry.go` Belongs to Inbound A2A, Outbound A2A, or Both

**`agentregistry` is outbound A2A only.**

| Direction | Package | Description |
|---|---|---|
| Inbound | `internal/a2a/server.go` | Platform exposes itself as an A2A agent to external callers |
| Outbound | `internal/agentregistry/registry.go` | Platform calls external agents as A2A clients |

`agentregistry` is called by the orchestrator tool loop when a `NeutralTool` with
`adapter_type = "a2a"` is invoked. It is the agent client. It has no awareness of
inbound requests and should not be changed for R-4e.

The two roles are independent and should remain in separate packages. `agentregistry`
does not need tenant propagation for R-4e — it already passes `AuthToken` from the
agent config. Tenant context for outbound A2A calls (if ever needed) would be a
separate concern.

---

## 13. Exact Minimal Implementation Scope for R-4e

### What must change

**`go/internal/a2a/server.go`** — the entire `handleMessageSend` function and the
`Server` struct dependencies:

1. **Add dependencies to `Server`:**
   - `auth.Cache` (for `tryAuthenticate` — same as WS/SSE)
   - `epconfig.Loader` (for `epLoader.Load` — same as WS/SSE)
   - `gate.Gate` (for admission control — same as WS/SSE)
   - `session.Store` (for session registration — same as WS/SSE)
   - `temporal.Client` (for `ExecuteWorkflow` — same as WS/SSE)
   - Remove: `orchestrator.Orchestrator` (no longer needed for inbound)

2. **Replace `handleMessageSend` logic:**

```
handleMessageSend:
  a. tryAuthenticate(r) → tokenInfo (non-enforcing)
  b. app_slug := chi.URLParam(r, "app_slug")
  c. resolvedCfg := epLoader.Load(ctx, app_slug) → ErrNotFound → 404
  d. epconfig.CheckAccess(resolvedCfg, tokenHash, userID) → ErrDisabled → 403
  e. if resolvedCfg.AccessMode == "token" && tokenInfo == nil → 401
  f. parse params → userText
  g. runID := newID(), contextID from params.message.contextId or newID()
  h. sessionID := newID()
  i. gate.Check(ctx, gate.Config{EPSlug, AppID, TokenHash, SessionID, limits})
     → ErrCapExceeded → 429 or 503 (with Retry-After)
     → ErrRateLimited → 429
  j. session.Register(ctx, session.SessionInfo{
       SessionID, EPSlug: app_slug, AppID, TenantID, ContextID: contextID,
       OrchestratorName: resolvedCfg.OrchestratorName (TBD — see §13.1)
     })
  k. if Register fails → gate.Rollback
  l. gate.Confirm
  m. recorder.CreateRun(ctx, domain.Run{
       ID: runID, TenantID: resolvedCfg.TenantID, ApplicationID: resolvedCfg.AppID,
       EntryPointSlug: app_slug, Status: Running, StartedAt: now()
     })
  n. bus.Subscribe BEFORE ExecuteWorkflow
  o. temporal.ExecuteWorkflow(GoTaskQueue, WorkflowInput{
       RunID: runID, ContextID: contextID,
       TenantID: resolvedCfg.TenantID, ApplicationID: resolvedCfg.AppID,
       EntryPointSlug: app_slug, UserMessage: domain.TextMessage(...), History: nil
     })
  p. block until workflow.Get(ctx, &result)
  q. session.End(ctx, sessionID, app_slug, resolvedCfg.AppID)
  r. gate.Release(ctx, gate.Config{...})
  s. writeRPCResult / writeRPCError
```

3. **Update `NewServer` signature** to accept the new dependencies and drop
   `*orchestrator.Orchestrator`.

4. **Update `cmd/them/main.go`** to wire the new A2A Server dependencies.

5. **Update `Routes()`** — no route changes needed. `/a2a/{app_slug}` and
   `/.well-known/agent.json` stay.

6. **Update agent card** — add `security_schemes` entry indicating bearer token auth.
   Low priority but correct.

### 13.1 OrchestratorName resolution

`WorkflowInput.OrchestratorName` currently comes from the EPConfig in WS/SSE. The
`EPConfig` struct does not currently include `OrchestratorName`. WS/SSE read it from
a separate DB query or it is embedded in EPConfig. This needs verification before
implementing:

- Check `go/internal/ws/handler.go` to see how `OrchestratorName` is set in the
  `WorkflowInput` before implementing A2A
- If `OrchestratorName` is not on `EPConfig`, the `epconfig.EPConfigRow` and
  `EPConfig` structs need a new field, and the SQL query in `epconfig/pgx.go` needs
  an additional column join

This is a scoping question that should be resolved before R-4e implementation starts.
It may add one or two file changes to the scope.

### What does NOT change

- `internal/agentregistry/registry.go` — no changes
- `internal/orchestrator/orchestrator.go` — no changes
- `internal/ws/handler.go` — no changes
- `internal/sse/handler.go` — no changes
- `internal/temporal/workflow.go` — no changes (R-4d already added TenantID/ApplicationID)
- `internal/temporal/activities.go` — no changes
- `internal/gate/gate.go` — no changes
- `internal/session/session.go` — no changes
- Docker / Compose — no changes
- Python bridge — no changes

---

## 14. Evaluation of the Proposed Target Flow

### Proposed flow (from the task brief)

```
Inbound A2A request
→ authenticate trusted caller
→ resolve target application / entry point server-side
→ derive TenantID and ApplicationID from trusted platform data
→ validate access and policy
→ create run with tenant_id
→ build the same temporal.WorkflowInput used by WS/SSE
→ start or signal Temporal
→ Go Worker executes orchestrator/agents/tools/A2A
→ return A2A-compliant response mapped from run events/result
```

### What fits

| Step | Verdict | Notes |
|---|---|---|
| Authenticate trusted caller | **Accept** | Use same `tryAuthenticate` pattern as WS/SSE |
| Resolve app/EP server-side | **Accept** | `epLoader.Load(ctx, app_slug)` — exact same mechanism |
| Derive TenantID/AppID from trusted data | **Accept** | From EPConfig, never from request payload |
| Validate access and policy | **Accept** | `epconfig.CheckAccess` — exact same check |
| Create run with tenant_id | **Accept** | `recorder.CreateRun` with R-4d fields |
| Build same WorkflowInput as WS/SSE | **Accept** | WorkflowInput already has TenantID/ApplicationID (R-4d) |
| Start Temporal | **Accept** | `ExecuteWorkflow` on GoTaskQueue |
| Go Worker executes loop | **Accept** | No change needed; Worker already handles this |
| Return A2A-compliant response | **Accept** | Map WorkflowResult → rpcResult |

### What does not fit

| Step | Verdict | Notes |
|---|---|---|
| "or signal Temporal" | **Reject for R-4e** | Signal is for HITL resume. A2A synchronous path starts a new workflow; it does not signal an existing one. Conflating start+signal hides a meaningful distinction. |
| "from run events/result" | **Partial** | For synchronous A2A, `WorkflowResult` is sufficient. Event streaming from the run-stream is not needed for a blocking response. Streaming A2A is out of scope. |

### What is missing from the proposed flow

The proposed flow omits the admission gate and session registration, which are
mandatory. The full correct sequence is:

```
→ authenticate caller
→ resolve EPConfig from app_slug (TenantID + AppID)
→ CheckAccess (enabled, block-list)
→ gate.Check (cap, rate limit, queue)
→ session.Register → gate.Confirm (or gate.Rollback on failure)
→ recorder.CreateRun
→ bus.Subscribe (BEFORE ExecuteWorkflow)
→ ExecuteWorkflow
→ block until result
→ session.End + gate.Release
→ return A2A response
```

The gate and session steps are not optional. Omitting them produces a path that
exceeds EP caps silently and is invisible to the dashboard.

### "Do not introduce a broad ExecutionService"

**Agreed.** R-4d already established this: no shared service. A2A `handleMessageSend`
can directly call the same primitives WS/SSE use. The similarity between handlers is
the correct level of abstraction at this stage.

---

## 15. Risks, Compatibility Concerns, and Required Tests

### Risk 1: Breaking existing A2A callers (medium)

If any caller currently uses `/a2a/{slug}` without a bearer token, adding auth will
break them. Mitigation:
- Check EPConfig's `AccessMode` — if "public", bearer token is not required
- Document that token EP callers must include `Authorization: Bearer <token>`
- R-4e should not add a hard 401 gate until access mode is verified per-EP

The current agent card declares no security requirements. Update the card to declare
`Bearer` auth when AccessMode is "token".

### Risk 2: Part field name `"kind"` vs A2A discriminator (low)

Current response: `{"kind": "text", "text": "..."}`.
A2A spec: the discriminator is the field presence, not a `"kind"` field.
The correct wire format is `{"text": "..."}` only.

If external callers depend on the `"kind"` field, changing it is a breaking change.
Fix in R-4e since the A2A path currently has no known external production callers.

### Risk 3: OrchestratorName not on EPConfig (medium — scoping risk)

If `OrchestratorName` is not carried in `EPConfig`, R-4e requires an additional field
on `EPConfigRow`/`EPConfig` and an updated SQL query in `epconfig/pgx.go`. This adds
scope. Verify before starting implementation.

### Risk 4: Synchronous long-running HTTP connections (low)

A2A `message/send` is synchronous in R-4e. If an orchestration run takes 10 minutes,
the HTTP connection is held open for 10 minutes. This is:
- correct per A2A spec for synchronous mode
- compatible with Go's HTTP server (goroutine per connection)
- protected by Temporal's `activityStartToClose` timeout (10 min)
- safe for the existing test agents (fast, echo-style responses)

Client proxy timeouts (Traefik, nginx) may need configuration for long-running A2A
calls. Out of scope for R-4e but should be documented.

### Risk 5: Context cancellation on HTTP disconnect (low-medium)

If the HTTP client disconnects mid-orchestration, `r.Context()` is cancelled, which
propagates to `ExecuteWorkflow`. Temporal will attempt to cancel the workflow. This is
correct behavior. Verify the cancellation propagation in tests.

### Required tests for R-4e

All tests should be unit tests (no live Temporal/Redis/Postgres required):

| Test | What it verifies |
|---|---|
| `TestA2A_MissingSlug_404` | Unknown `app_slug` → EPConfig ErrNotFound → 404 JSON error |
| `TestA2A_DisabledEP_403` | EP or App disabled → CheckAccess ErrDisabled → 403 |
| `TestA2A_BlockedToken_403` | Token on block list → CheckAccess ErrBlocked → 403 |
| `TestA2A_MissingTokenOnTokenEP_401` | AccessMode "token" + no bearer → 401 |
| `TestA2A_PublicEP_NoToken_OK` | AccessMode "public" + no bearer → proceed |
| `TestA2A_CapExceeded_429` | Gate returns ErrCapExceeded → 429 |
| `TestA2A_TenantIDFromEPConfig` | Run created with TenantID from EPConfig, not from request |
| `TestA2A_AppSlugIgnoredByTenantID` | Request body cannot override TenantID |
| `TestA2A_WorkflowInputHasTenantID` | ExecuteWorkflow called with correct TenantID + AppID |
| `TestA2A_SessionRegistered` | session.Register called before ExecuteWorkflow |
| `TestA2A_SessionEndedOnCompletion` | session.End called after workflow completes |
| `TestA2A_GateReleasedOnCompletion` | gate.Release called after session.End |
| `TestA2A_GateRollbackOnRegisterFail` | gate.Rollback called if session.Register fails |
| `TestA2A_RPCResult_CompletedState` | Successful workflow → "completed" state + text artifact |
| `TestA2A_RPCError_WorkflowFailed` | Failed workflow → JSON-RPC error response |
| `TestA2A_ContextIDFromParams` | Caller-provided contextId used as event bus key |
| `TestA2A_ContextIDGeneratedIfAbsent` | No contextId in params → new ID generated |

Additionally, update `go/TEST_INDEX.md` with all new tests in the same commit.

---

## 16. What Should Explicitly Remain Out of Scope for R-4e

| Item | Reason |
|---|---|
| Async A2A (`returnImmediately: true`, task polling, `GetTask`, `subscribeToTask`) | Requires task state storage, separate endpoints, full A2A task lifecycle. Separate feature. |
| HITL (input-required) for A2A | Requires async semantics. Out of scope. |
| Push notifications (webhook on state change) | Separate A2A feature. |
| Streaming A2A (SSE from `/a2a` endpoint) | Separate transport mode. |
| Multiple orchestrators per A2A endpoint | Requires orchestrator-routing logic beyond EPConfig scope. |
| WS/SSE handler deduplication (sessionStart helper) | Separate refactor task; do not bundle. |
| Outbound A2A tenant propagation in `agentregistry` | Separate concern; agentregistry uses agent-config token, not platform tenant. |
| New DB columns on `them.entry_points` or `them.runs` | R-4a/4d already added what is needed. |
| Docker / Compose changes | Not required for A2A auth/gate/Temporal wiring. |
| Python bridge changes | Python A2A path is independent; Go A2A is the R-4e target only. |
| `agentregistry` changes | No changes needed for inbound A2A. |
| Agent card `extended_agent_card` authentication | Out of scope. |

---

## 17. Verdict on NEXT_SESSION_HANDOVER.md R-4e Description

The current handover doc describes R-4e as:

> Propagate TenantID and ApplicationID from the request context into the A2A execution path.
> Specifically:
> - a2a/server.go: resolve tenant identity from the bearer token/JWT at the A2A handler level
> - agentregistry/registry.go: pass tenant context through to A2A invocations
> - Validate TenantID is non-empty before dispatching (consistent with R-4d boundary check)

**This description is materially incomplete and partially incorrect:**

1. **Incomplete:** Does not mention authentication, EP config resolution, admission gate,
   session registration, or Temporal dispatch. R-4e is not a "pass TenantID through"
   change — it is a full alignment of the A2A path with the WS/SSE execution pipeline.
2. **Incorrect:** `agentregistry/registry.go` does NOT need to change for R-4e. It is
   outbound A2A; the R-4e work is entirely on inbound A2A (`a2a/server.go`).
3. **Incomplete:** Does not mention removing the direct `orch.Run` call — which is the
   most important change (replacing it with Temporal dispatch).

The handover doc description should be updated to reflect this review's findings.
NEXT_SESSION_HANDOVER.md is updated below.

---

## 18. Recommended Minimal R-4e Implementation Order

Ordered by dependency (lowest risk first):

| Step | What | Files | Risk |
|---|---|---|---|
| 1 | Verify `OrchestratorName` on EPConfig (or add it) | `epconfig/epconfig.go`, `epconfig/pgx.go` | Low–Medium |
| 2 | Replace `Server` struct deps: add auth.Cache, epconfig.Loader, gate.Gate, session.Store, temporal.Client; remove orch | `a2a/server.go` | Low |
| 3 | Replace `handleMessageSend`: auth → EP config → access check → gate → session → run → temporal | `a2a/server.go` | Medium |
| 4 | Update `NewServer` signature | `a2a/server.go` | Low |
| 5 | Update `cmd/them/main.go` wiring | `cmd/them/main.go` | Low |
| 6 | Fix A2A wire format: `"kind"` → remove, use field-presence discriminator | `a2a/server.go` | Low |
| 7 | Add `TaskID` to `rpcResult` | `a2a/server.go` | Low |
| 8 | Write all tests from §15 | `a2a/server_test.go` | Medium |
| 9 | Update `go/TEST_INDEX.md` | `go/TEST_INDEX.md` | Low |

**Do not start Step 3 before Step 1 is resolved.** `OrchestratorName` is needed in
`WorkflowInput`; if it is not on `EPConfig`, the scoping changes before any handler
logic is written.

---

## 19. Summary

| Question | Answer |
|---|---|
| Current A2A auth | None — any caller can invoke any slug |
| Current identity binding | None — `app_slug` in URL is discarded |
| Current Temporal usage | None — direct `orch.Run` |
| Correct identity binding | `app_slug` → EPConfig (TenantID + AppID from DB) |
| Bearer token role | Access check + rate limiting; required for token EPs, optional for public EPs |
| TenantID source | EPConfig.TenantID only; never from request payload or headers |
| ApplicationID source | EPConfig.AppID only |
| Temporal mapping | message/send → ExecuteWorkflow(GoTaskQueue) → block → WorkflowResult |
| Synchronous A2A | Block HTTP until workflow completes; correct for R-4e |
| Async A2A | Out of scope |
| Gate applies to A2A | Yes — same Check/Confirm/Release/Rollback pattern |
| Session registration | Yes — same session.Register/End pattern |
| `agentregistry` role in R-4e | None — outbound only, no changes |
| A2A wire format bug | `"kind"` field is non-spec; fix in R-4e |
| HITL for A2A | Out of scope |
| OrchestratorName on EPConfig | Verify before implementing; may add scope |
