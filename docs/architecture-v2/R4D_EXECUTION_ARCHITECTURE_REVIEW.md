# R-4d Execution Architecture Review
# Shared Execution Service — Proposal Evaluation and Gap Analysis
# Date: 2026-08-01
# Status: REVIEW ONLY — no implementation in this document

---

## 1. Purpose

This document answers the critical question raised before R-4d:

> Does THEM currently have one shared execution pipeline used by all entry points,
> or is execution logic duplicated across WS, SSE, A2A, and future WebRTC paths?

It also evaluates the proposed `ExecutionRequest / Shared Execution Service` refactor against
the actual code, and produces the corrected scope and acceptance criteria for R-4d.

---

## 2. Current Execution Flow — Per Entry Point

### 2.1 WS (`go/internal/ws/handler.go`)

```
1. tryAuthenticate        → bearer token → auth.TokenInfo (non-enforcing)
2. epLoader.Load          → EPConfig (AppID, TenantID, limits, access mode)
3. CheckAccess            → enabled + block-list enforcement
4. gate.Check             → admission (cap, rate limit, queue)
5. upgrader.Upgrade       → WebSocket upgrade
6. newID() × 2            → runID, contextID (local)
7. session.Register       → Redis Hash + Set + shadow (SessionInfo with TenantID, AppID)
8. gate.Confirm           → seat confirmed
9. recorder.CreateRun     → INSERT them.runs (no tenant_id column on run row yet)
10. readClientMessage     → domain.Message from client
11. bus.Subscribe         → in-process event bus (no-op for Temporal path)
12. h.runEvents(subscribe)→ Redis Streams / Pub/Sub before workflow start
13. ExecuteWorkflow       → Temporal, GoTaskQueue, WorkflowInput{RunID, ContextID,
                            EntryPointSlug, OrchestratorName, UserMessage}
14. streamEvents          → relay rsEvCh → WebSocket
```

### 2.2 SSE (`go/internal/sse/handler.go`)

```
1. tryAuthenticate        → identical to WS
2. extractMessage         → ?message= or POST body
3. epLoader.Load          → identical to WS
4. CheckAccess            → identical to WS
5. gate.Check             → identical to WS
6. SSE headers written
7. newID() × 2            → runID, contextID (local)
8. session.Register       → identical to WS
9. gate.Confirm           → identical to WS
10. recorder.CreateRun    → identical to WS
11. bus.Subscribe         → no-op for Temporal path
12. h.runEvents(subscribe)→ identical to WS
13. ExecuteWorkflow       → identical to WS (same WorkflowInput)
14. streamEvents          → relay rsEvCh → SSE response
```

### 2.3 A2A (`go/internal/a2a/server.go`)

```
1. JSON-RPC parse         → method "message/send"
2. Extract text from parts
3. newID() × 2            → runID, contextID (local)
4. bus.Subscribe          → in-process event bus
5. recorder.CreateRun     → INSERT them.runs (minimal: RunID, ContextID only)
6. orch.Run               → DIRECT ORCHESTRATOR CALL (no Temporal, no gate, no session,
                            no EP config, no admission, no auth, no TenantID)
```

**Critical finding:** The A2A server is a different execution path in every dimension:
- No authentication
- No entry-point config resolution
- No admission gate
- No session registration in Redis
- No Temporal workflow — direct in-process orchestrator call
- No tenant identity
- Runs are created with no `ApplicationID`, no `EntryPointSlug`, no tenant
- The `domain.Run` passed to recorder has only `ID`, `ContextID`, and `Status`

### 2.4 Where Runs Are Created

| Entry point | Creates run? | Caller | tenant_id on row? |
|---|---|---|---|
| WS | Yes | `recorder.CreateRun` | No (column not yet on runs table) |
| SSE | Yes | `recorder.CreateRun` | No |
| A2A | Yes | `recorder.CreateRun` | No |

Run creation happens independently in each entry-point handler. No shared service mediates it.

### 2.5 Where Temporal Workflows Are Started

| Entry point | Starts workflow? | Queue | Input type |
|---|---|---|---|
| WS | Yes | `GoTaskQueue` | `temporal.WorkflowInput` |
| SSE | Yes | `GoTaskQueue` | `temporal.WorkflowInput` |
| A2A | **No** | n/a | Direct `orch.Run` call |

The A2A handler bypasses Temporal entirely.

### 2.6 Where RuntimeIdentity Is Created and Propagated

`transport.RuntimeIdentity` is defined in `go/internal/transport/transport.go`:

```go
type RuntimeIdentity struct {
    TenantID  string
    AppID     string
    UserID    int64
    SessionID string
    RunID     string
}
```

**Current usage:** This struct is defined but **not used by any handler today**.

- WS and SSE materialize the five fields piecemeal across local variables and
  `session.SessionInfo`. They set `TenantID` and `AppID` on `SessionInfo` from
  `resolvedCfg` (steps 6–7), but never construct a `RuntimeIdentity` value.
- The `runID` is generated before `ExecuteWorkflow` but is not part of any
  identity struct that flows to Temporal.
- `WorkflowInput` carries `RunID`, `ContextID`, `EntryPointSlug`, `OrchestratorName`,
  and `UserMessage` — but **no TenantID and no AppID**.
- The A2A handler never creates any identity at all.

**Result:** RuntimeIdentity exists as a declared type only. No handler constructs it,
and tenant identity does not propagate into Temporal workflows or the Go Worker.

### 2.7 Duplicated Orchestration Logic

Steps 1–13 of WS and SSE (`handler.go`) are nearly identical — approximately 250 lines
of duplicated logic. The only genuine differences are:
- WS: upgrades to WebSocket, reads first message post-upgrade
- SSE: sets SSE headers pre-upgrade, reads message from query param / POST body
- SSE: uses `Last-Event-ID` header as stream cursor (WS uses `last_event_id` in first message)
- WS: `streamEvents` writes `websocket.TextMessage`; SSE: `streamEvents` writes `"data: ...\n\n"`

Everything else — auth, EP config load, access check, gate, session, run creation,
Temporal start, stream subscribe — is duplicated line-for-line.

### 2.8 Whether a Shared Execution Service Already Exists

**No.** The closest thing to shared infrastructure is:

- `internal/transport/transport.go` — shared interfaces and `RuntimeIdentity` type definition
- `internal/epconfig/` — shared EP config loader (used by WS and SSE, not A2A)
- `internal/gate/` — shared admission gate (used by WS and SSE, not A2A)
- `internal/runrecorder/` — shared recorder (used by WS, SSE, and A2A)

But there is no service that encapsulates: (a) validate app belongs to tenant, (b) create or
resume a run, (c) build RuntimeIdentity, (d) start or signal the Temporal workflow. Each
entry-point handler does all of these independently.

---

## 3. Architectural Gaps Before Runtime Tenant Propagation

### Gap 1: TenantID absent from WorkflowInput

`temporal.WorkflowInput` has no `TenantID` field. The Go Worker executing
`RunOrchestratorActivity` receives the input and calls `orch.Run` with no tenant context.
Any resource access inside the activity (future: per-tenant LLM provider selection,
per-tenant audit, per-tenant rate limiting inside the workflow) has no tenant identity
to work with.

### Gap 2: TenantID absent from `them.runs` rows

`recorder.CreateRun` inserts into `them.runs` without a `tenant_id` column.
The R-4a migration added `tenant_id` to `them.runs` but the Go recorder does not write it.
Runs are not correctly tenant-scoped in the DB.

### Gap 3: A2A is a completely different, untenanted execution path

The A2A handler bypasses auth, the admission gate, EP config, session registration,
Temporal, and tenant identity. It calls the orchestrator directly in-process.
This is not just a "missing field" — it is a divergent execution pipeline.

### Gap 4: RuntimeIdentity is declared but never used

The type exists; nothing constructs or propagates it. TenantID is available in WS/SSE
at step 2 (from EPConfig) but is dropped before Temporal is invoked.

### Gap 5: WS and SSE duplicate ~250 lines of identical logic

This is a maintainability gap. Every future cross-cutting concern (e.g. runtime
tenant propagation, request tracing, HITL lifecycle) must be added twice.

### Gap 6: No application-to-tenant validation at workflow start

When `ExecuteWorkflow` is called, nothing verifies that the resolved `EPConfig.AppID`
belongs to `EPConfig.TenantID`. The check is implicit (both come from the same DB query
in epconfig), but there is no explicit runtime assertion, and nothing prevents future
code from passing a mismatched pair.

---

## 4. Evaluation of the Proposed Shared Execution Service

### Proposal Summary

The proposal asks for:

```
ExecutionRequest {
  RuntimeIdentity { tenant_id, application_id, user_id, session_id, run_id }
  entry_point_type
  input
  metadata
}
```

With a single `ExecutionService` as the sole owner of: validate tenant→app, create run,
build RuntimeIdentity, start/signal Temporal, return references.

Entry-point handlers become thin adapters: auth → parse input → call ExecutionService →
relay events.

---

### 4.1 What I Agree With

**1. RuntimeIdentity must flow into Temporal.**
`WorkflowInput` is missing `TenantID` and `AppID`. Both must be added and the activity
must forward them. This is a concrete, well-scoped, and necessary change.

**2. TenantID must be written to `them.runs` at creation time.**
`recorder.CreateRun` must write `tenant_id` (already added to DB schema in R-4a) so every
run row is correctly scoped. This is a separate, narrow fix.

**3. The WS/SSE duplication is a real problem.**
The 250-line duplication means every future change must be made twice and creates a latent
bug surface. Extracting shared logic is correct in principle.

**4. A2A must be brought into the tenant-aware, Temporal-backed path.**
The current A2A handler is an isolated island that bypasses all platform guarantees: no
auth, no gate, no Temporal, no tenant. It will accumulate drift with every wave.

**5. The "bridge transports, does not orchestrate" principle is correct.**
Entry-point handlers should not own orchestration decisions. That belongs in the Worker.

---

### 4.2 What I Reject

**Reject: A single `ExecutionService` struct as the gating abstraction.**

The proposal introduces a new service layer between entry-point handlers and Temporal.
The specific problem: WS and SSE have meaningfully different pre-workflow steps
(gate lifecycle, session lifecycle, stream subscription ordering) that cannot be
collapsed without introducing branching inside the service. A service that branches
on `entry_point_type` is not a service — it is a dispatcher with worse readability
than the current code.

More concretely: the WS handler must subscribe to the run-stream BEFORE calling
`ExecuteWorkflow` (critical ordering documented in handler.go). The A2A handler
currently has no streaming. SSE must write HTTP headers before the gate/session steps
because headers cannot be set after the first write. These constraints are
protocol-specific and belong in the protocol adapter, not in a shared service.

**Reject: run creation and workflow start in one service.**

The proposal says the service should "create or resume a run" and "start or signal the
Temporal workflow" as a single unit. But:
- Run creation is a DB write (recorder) — it can be factored out cleanly.
- Workflow start is a Temporal call — it requires the stream subscription to happen first.
- Signal/resume is a different code path (HITL) that does not create a run.

Collapsing these into one service hides the ordering constraints that are currently
explicit in the handler.

**Reject: One service validates "application belongs to tenant".**

This check is already done implicitly by the epconfig Loader — it queries
`entry_points JOIN applications` and returns a combined `EPConfig` that has both
`AppID` and `TenantID` from the same row. There is no way to get a mismatched pair from
this query. An additional explicit validation layer adds code without adding safety.
The right fix is to propagate what the Loader already provides into WorkflowInput.

---

### 4.3 What I Would Change

**Change 1: Narrow `WorkflowInput` extension (not a new service).**

Add `TenantID` and `ApplicationID` (string UUID) to `temporal.WorkflowInput`.
Both WS and SSE already have these values from `resolvedCfg` before they call
`ExecuteWorkflow`. The fix is two field additions and two assignment lines.

```go
// temporal/workflow.go
type WorkflowInput struct {
    RunID            string
    ContextID        string
    TenantID         string   // NEW — from epconfig.EPConfig.TenantID
    ApplicationID    string   // Change int64 → string UUID; from epconfig.EPConfig.AppID
    EntryPointSlug   string
    OrchestratorName string
    UserMessage      domain.Message
    History          []domain.Message
}
```

Note: `ApplicationID int64` in the current `WorkflowInput` is the wrong type — the DB
and EPConfig use UUID strings. This is a latent type mismatch that should be fixed here.

**Change 2: Write `tenant_id` in `recorder.CreateRun`.**

Add `TenantID string` to `domain.Run` and pass it through `recorder.CreateRun`.
The SQL already has the column (R-4a). WS and SSE set it from `resolvedCfg.TenantID`.

**Change 3: Extract a `sessionStart` helper, not a service.**

Rather than a shared service, extract a private `func sessionStart(...)` or a small
`sessionLifecycle` struct that WS and SSE both call for the shared steps (auth, EP config,
gate, session register, run create). The protocol-specific steps (WS upgrade, SSE headers,
message extraction, stream subscription, workflow start) stay in the handlers.

This is a refactor, not a new abstraction. It removes the duplication without hiding the
ordering constraints. It does not need a new package.

**Change 4: A2A must adopt the Temporal path.**

The A2A handler should be brought in line with WS/SSE:
- Add bearer token authentication (same `tryAuthenticate` logic)
- Add EP config resolution via `epLoader.Load`
- Adopt Temporal `ExecuteWorkflow` (same `WorkflowInput`)
- Register a session in Redis for the duration of the A2A call
- Remove direct `orch.Run` call

This is a significant A2A change and should be a dedicated sub-task, not bundled with
the tenant propagation fix.

**Change 5: Do not introduce `ExecutionRequest`.**

The `ExecutionRequest` envelope adds a layer of indirection without adding capability.
The `WorkflowInput` struct already carries the same information (or will after Change 1).
A second named type for the same concept creates a translation step that adds no value.

---

## 5. Recommended Minimal Refactor to Reach the Design

Ordered by risk (lowest first):

| Step | What | Where | Risk |
|---|---|---|---|
| 1 | Add `TenantID`, fix `ApplicationID` type in `WorkflowInput` | `internal/temporal/workflow.go` | Low — additive struct change |
| 2 | Add `TenantID` to `domain.Run`, write it in `recorder.CreateRun` | `internal/domain/domain.go`, `internal/runrecorder/recorder.go` | Low — additive column write |
| 3 | Pass `TenantID`, `ApplicationID` from WS/SSE to `WorkflowInput` | `internal/ws/handler.go`, `internal/sse/handler.go` | Low — two-line changes per handler |
| 4 | Extract shared `sessionStart` helper (WS + SSE only) | New private file in `internal/transport/` or inline refactor | Medium — must not break ordering constraints |
| 5 | Bring A2A onto Temporal + auth path | `internal/a2a/server.go` | High — separate sub-task, changes A2A semantics |

Steps 1–3 together constitute the minimal tenant propagation fix. Steps 4–5 are
independent improvements that should be separate tasks.

---

## 6. Corrected Scope and Acceptance Criteria for R-4d

### What the NEXT_SESSION_HANDOVER.md says R-4d is

```
Goal: DAL-level cross-tenant enforcement + integration test coverage.
Scope:
1. Verify all DAL queries use AND tenant_id = $N::uuid in WHERE clauses
2. Add integration tests verifying cross-tenant reads return 404
3. Token endpoint audit: POST /admin/tokens must scope to requesting tenant
4. Entry point queries: verify entry_points includes tenant scoping
```

### Assessment: Is this still correct?

**DAL enforcement:** Completed in R-4c1. All DAL SELECT/INSERT/UPDATE/DELETE on
tenant-owned entities include `tenant_id` scoping. This was verified and tested in
R-4c1 with 21 two-tenant isolation tests. The handover doc's statement "DAL-level
cross-tenant enforcement" describes work that is already done.

**Integration tests for cross-tenant 404:** Not yet added. This is legitimate remaining work.

**Token endpoint audit:** `POST /admin/tokens` — the DAL `CreateToken` in R-4c1 already
takes `tenantID string` and includes it in the INSERT. The handler in R-4c2 calls
`MustTenantIDFromCtx` to get the tenant. This is complete for the Go admin path.

**Entry point queries:** Entry points are scoped through their parent application's `tenant_id`
(foreign key). The DAL does not add a direct `tenant_id` to entry point queries; this
was a deliberate decision in R-4c1 (per the report: "Entry point methods: NO tenantID
(scoped through parent app FK)"). This is correctly documented and done.

### The actual gap that R-4d should address

Based on this review, the meaningful remaining work in the R-4 tenant wave is:

1. **Runtime tenant propagation (execution path):**
   - `WorkflowInput` missing `TenantID` and has wrong `ApplicationID` type
   - `recorder.CreateRun` not writing `tenant_id` to `them.runs`
   - WS/SSE not passing TenantID/AppID into workflow start

2. **Integration tests for DAL cross-tenant 404** (as stated in handover)

3. **A2A handler is completely untenanted** (larger scope — may be R-4e)

### Proposed corrected R-4d scope

**R-4d: Runtime Tenant Propagation**

Accept criteria:
- [ ] `temporal.WorkflowInput` has `TenantID string` and `ApplicationID string` (UUID)
- [ ] `domain.Run` has `TenantID string`; `recorder.CreateRun` writes it to DB
- [ ] WS handler passes `resolvedCfg.TenantID` and `resolvedCfg.AppID` into `WorkflowInput` before `ExecuteWorkflow`
- [ ] SSE handler does the same
- [ ] Integration test: create a run via WS or SSE; confirm `them.runs.tenant_id` matches the token's tenant
- [ ] Integration test (from handover): cross-tenant GET /admin/runs returns 404
- [ ] `go test ./...` passes; no new failures
- [ ] `go test -race ./...` passes

**R-4e (separate, future): A2A Tenant Alignment**

- [ ] A2A handler adopts bearer token auth
- [ ] A2A handler uses EP config loader
- [ ] A2A handler starts Temporal workflow (not direct orch.Run)
- [ ] A2A sessions registered in Redis
- [ ] A2A runs carry TenantID

**Not in R-4d:**
- WS/SSE handler deduplication (`sessionStart` helper) — separate refactor task
- A2A changes — R-4e
- New shared ExecutionService — not recommended; see §4.2

---

## 7. Final Verdict on the Proposed Architecture

### Proposal verdict

| Claim | Verdict | Reason |
|---|---|---|
| Entry points should be thin adapters | **Accept** | Correct principle; partially true today for WS/SSE, false for A2A |
| Shared Execution Service owns run creation + Temporal start | **Reject** | Creates wrong abstraction; hides protocol-specific ordering constraints |
| `ExecutionRequest` type as shared command | **Reject** | Redundant with `WorkflowInput`; adds translation step |
| RuntimeIdentity must flow to Temporal | **Accept** | Critical gap confirmed; add to WorkflowInput |
| Application → tenant validation in shared service | **Reject** | Already enforced implicitly by epconfig Loader; explicit layer adds no safety |
| A2A must join the Temporal path | **Accept** | Current A2A bypasses all platform guarantees; correct architectural direction |
| Single start/signal path | **Partial accept** | Start and signal/resume are different; keep them explicit, not collapsed into one method |
| Smallest safe refactor first | **Accept** | Steps 1–3 (WorkflowInput + recorder + handler assignment) are the right first move |

### Current architecture verdict

**Partially shared.** WS and SSE share the same components (auth, epconfig, gate, session,
recorder, Temporal) but duplicate ~250 lines implementing the same protocol-agnostic
logic independently. A2A is a divergent execution path that bypasses most platform
guarantees. A formal `ExecutionService` abstraction is premature; the immediate fix is
propagating `TenantID` and `ApplicationID` through the existing path (WorkflowInput +
recorder.CreateRun). The duplication and A2A alignment are real problems to address
as separate tasks after the tenant propagation is in.

### Whether existing documentation incorrectly describes R-4d

**Yes.** The `NEXT_SESSION_HANDOVER.md` describes R-4d as "DAL-level cross-tenant
enforcement + integration test coverage." The DAL enforcement is already complete (R-4c1).
The remaining meaningful work in the R-4 tenant wave is **runtime tenant propagation
into Temporal** (WorkflowInput, recorder.CreateRun) and integration test coverage
for cross-tenant reads — a combination of one item from the handover doc and new work
identified by this review.

The handover doc should not be relied upon as the complete R-4d scope. This review
document is the authoritative source for the next session.

---

## 8. Files Relevant to the Next Task

| File | Why |
|---|---|
| `go/internal/temporal/workflow.go` | Add TenantID, fix ApplicationID type in WorkflowInput |
| `go/internal/domain/domain.go` | Add TenantID to Run |
| `go/internal/runrecorder/recorder.go` | Write tenant_id in CreateRun |
| `go/internal/ws/handler.go` | Pass TenantID, AppID from resolvedCfg into WorkflowInput |
| `go/internal/sse/handler.go` | Same |
| `go/internal/temporal/activities.go` | Confirm activity receives and can use TenantID |
| `go/internal/a2a/server.go` | R-4e: bring onto Temporal path (do not touch in R-4d) |
| `go/TEST_INDEX.md` | Must be updated when new tests are added |
| `db/026_tenant_foundation.sql` | Confirms tenant_id column exists on them.runs |
