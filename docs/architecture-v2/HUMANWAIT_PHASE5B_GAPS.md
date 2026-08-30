# HumanWait Phase 5-B — Revised Gap Analysis & Implementation Proposal
# Author: Claude (session 2026-08-30, revised)
# Status: PENDING APPROVAL — no code written yet

---

## Executive Summary

Phase 5-B (commit `3b1052f`) shipped the structural skeleton (HITLStore, Submit,
SignalCanvasStep, executeSkill HITL path, signalHITL endpoint). Seven correctness and
security gaps remain. This document describes each gap precisely, proposes the exact fix,
and specifies the lifecycle/state model that ties them together.

---

## Background: What Phase 5-B Delivered

| Component | Location | Status |
|---|---|---|
| `HITLStore` — Redis `them:hitl:{task_id}`, TTL 24h | `internal/agentgen/hitl_store.go` | ✅ correct structure |
| `PlanHasHumanWait` — scans top-level nodes only | `internal/agentgen/plan_compiler.go` | ⚠️ does not recurse into SubPlans |
| `TemporalExecutor.Submit()` — start without blocking | `internal/temporal/temporal_executor.go` | ✅ correct |
| `TemporalExecutor.SignalCanvasStep()` — deliver signal | `internal/temporal/temporal_executor.go` | ✅ correct |
| `executeSkill` HITL path — detach ctx, Submit, store, return | `cmd/agent-runtime/main.go` | ⚠️ emits InputRequired too early |
| `signalHITL` handler — reads handle, calls SignalCanvasStep | `cmd/agent-runtime/main.go` | ❌ unauthenticated, not idempotent |
| Workflow signal receive — `sigCh.Receive` after WaitingForHuman | `internal/temporal/canvas_workflow.go` | ⚠️ outer DAG only; loop body missing; no timeout |

---

## The Correct Lifecycle / State Model

Understanding the intended state machine is necessary before describing the gaps.

### Phase 5-B (as shipped) — wrong flow

```
Client → message/send
    → executeSkill detects HITL plan
    → Submit() (non-blocking)
    → hitlStore.Store(taskID, wfID, runID, stepID)
    → yield TaskStateInputRequired   ← WRONG: workflow hasn't reached hw node yet
    → return
```

The workflow may still be executing pre-HumanWait steps (LLM calls, HTTP requests, etc.)
when `InputRequired` is returned. The caller is told to provide human input before the
workflow is ready to receive it. If the signal arrives before the workflow reaches the
`human_wait` node, Temporal buffers it on the channel — this avoids data loss but produces
incorrect ordering semantics and confusing UX.

### Correct flow — event-driven InputRequired

```
Client → message/send
    → executeSkill detects HITL plan
    → Submit() (non-blocking)
    → hitlStore.Store(taskID, wfID, runID, state=submitted)
    → yield TaskStateWorking    ← correct: workflow started but not yet at HumanWait node
    → return

Temporal workflow runs pre-HumanWait steps...

Workflow reaches human_wait node:
    → ExecuteStepActivity → WaitingForHuman=true
    → workflow calls workflow.UpsertSearchAttributes or emits a Temporal Update/Signal back
      to notify the agent-runtime that it is now waiting
    OR (simpler, no external notification):
    → workflow.GetSignalChannel(ctx, "human_wait_ready:"+taskID).Send(ctx, waitToken)

agent-runtime receives the ready notification:
    → updates HITLStore state to waiting + records wait_token
    → (async) pushes a TaskStateInputRequired event to the A2A task queue
    → yield TaskStateInputRequired to any active subscriber
```

**Practical note on notification delivery**: The Temporal workflow is a separate process
(dag-worker). There is no open HTTP connection between the workflow and agent-runtime at
this point. The correct mechanism is a **Temporal signal back from the workflow**:

```
workflow → workflow.GetSignalChannel(ctx, "hitl_ready:"+taskID).Send(...)
         OR
workflow → workflow.SetQueryHandler("hitl_status", func() string {...})
```

However, implementing a full two-way notification channel between dag-worker and
agent-runtime is significant scope. The **pragmatic alternative** accepted here is:

> **Lazy resolution**: continue to emit `TaskStateWorking` immediately after Submit.
> Emit `TaskStateInputRequired` only when the `signalHITL` endpoint confirms the handle
> is present (i.e., the human UI has polled for status and the workflow has reached the
> HumanWait node). The dag-worker writes a `wait_token` signal when it reaches the node;
> the signal endpoint does not fire until that token exists in the handle.

This avoids the two-way notification plumbing while still preventing premature signals.
See Gap 2 (wait_token) for the mechanism.

---

## Gap 1 — Authentication on `signalHITL` is absent and `parseInvocationContext` is not auth

**Severity: CRITICAL**

### What the code does

`POST /agents/{slug}/tasks/{task_id}/signal` is an unauthenticated plain chi route.
`parseInvocationContext` (line 492) reads the four `X-Them-*` headers and trusts them
verbatim:

```go
func (rt *Runtime) parseInvocationContext(r *http.Request) (*agentgen.InvocationContext, error) {
    tenantID := r.Header.Get("X-Them-Tenant-Id")
    appID    := r.Header.Get("X-Them-Application-Id")
    ...
    return &agentgen.InvocationContext{...}, nil  // no verification whatsoever
}
```

These headers are only safe for the _internal_ A2A `message/send` path because:
1. Port 9300 is not exposed through Traefik (Docker network boundary is the guard).
2. The headers are injected by `agentregistry.InvokeWithMeta` — a known internal caller.

The signal endpoint is a **new external-facing operation** (a human operator or UI calls
it after approving a HITL task). It is not gated by the same internal caller and cannot
rely on the header-trust model. An attacker on the Docker network — or any compromised
service — can forge the headers and signal any workflow with arbitrary data.

Additionally, the compose file includes `THE_M_INVOCATION_JWT_KEY` as an environment
variable that is **never read** by the config (`go/internal/config/config.go` has no field
for it). This key was provisioned for "Phase 3" (signed JWT invocation) but was never
implemented.

### What the call chain actually needs

The signal endpoint will be called by:
- The **admin UI** (user has approved/rejected a task) — routed through `them-go-bridge`
  on port 8088, behind a valid super_admin JWT.
- Potentially by a **frontend app user** with a valid A2A session token.

The signal must be authorized to the correct tenant. The simplest correct model is:

**The signal endpoint lives on the `them-go-bridge` (port 8088, behind Traefik + JWT
auth), not on `them-agent-runtime` (internal port 9300).** The go-bridge verifies the
user's JWT, looks up the HITL handle in Redis (same `them:hitl:{task_id}` key), and
calls `client.SignalWorkflow` directly or forwards to the agent-runtime over the internal
network with injected `X-Them-*` headers.

This is consistent with how `POST /runs/{run_id}/signal` already works: the go-bridge
admin router owns it, validates JWT, calls `temporal.SignalRun`.

### Proposed fix

**Move `signalHITL` from `cmd/agent-runtime/main.go` to `go/internal/admin/` on the
go-bridge.** Specifically:

1. Add `POST /api/v1/canvas-tasks/{task_id}/signal` to the go-bridge admin router
   (`go/internal/admin/router.go`), behind `RequireSuperAdmin` middleware.
2. The handler reads `them:hitl:{task_id}` from Redis (using the same `HITLStore` via a
   new `CanvasTaskStore` interface injection into the admin service).
3. Validates that the caller's tenant matches `handle.TenantID` (tenant field added to
   `HITLHandle` — see Gap 2).
4. Calls `temporalSignaler.SignalCanvasStep(...)` (same `CanvasSignaler` interface,
   injected into the admin router alongside the existing `TemporalSignaler`).

**Remove `signalHITL` from `cmd/agent-runtime/main.go` entirely.** The route
`r.Post("/agents/{slug}/tasks/{task_id}/signal", rt.signalHITL)` is deleted.

**Files changed:**
- `go/internal/agentgen/hitl_store.go` — add `TenantID string` to `HITLHandle`
- `go/cmd/agent-runtime/main.go` — remove `signalHITL` handler and its route
- `go/internal/admin/router.go` — add new route `POST /canvas-tasks/{task_id}/signal`
- `go/internal/admin/canvas_tasks.go` (new file) — handler that reads HITLStore and signals
- `go/internal/admin/service/service.go` — add `CanvasTaskSignaler` interface
- `go/internal/admin/service/canvas_tasks.go` (new file) — service layer (HITLStore + CanvasSignaler)
- `go/internal/admin/dal/hitl.go` (new file) — Redis HITLStore DAL (thin wrapper, reuses `agentgen.HITLStore`)

**Alternatively (simpler, no new files):** Add the `HITLStore` and `CanvasSignaler` directly
to the existing `adminHandler` struct in `go/cmd/them/main.go` wiring, and add the route to
the existing admin router. No new service layer required — the handler is a two-step
operation (Redis read + Temporal signal), both already have interfaces.

**New tests:** CSIG-1 (valid JWT + correct tenant → signals workflow and returns 200),
CSIG-2 (valid JWT + wrong tenant → 403), CSIG-3 (missing JWT → 401),
CSIG-4 (unknown task_id → 404)

---

## Gap 2 — `HITLHandle` needs a `TenantID`, a `wait_token`, and an atomic state field

**Severity: HIGH**

### What the code does

`HITLHandle` currently stores:
```go
type HITLHandle struct {
    WorkflowID string `json:"workflow_id"`
    RunID      string `json:"run_id"`
    StepID     string `json:"step_id"`
}
```

### Problems

**a) No tenant isolation.** Cross-tenant task-ID collision (low probability but possible)
would allow tenant A's approval to signal tenant B's workflow. Fixing Gap 1 (moving the
signal to go-bridge + JWT auth) partially mitigates this, but the Redis key should also
carry the authoritative tenant so the handler can enforce it without a DB round-trip.

**b) One static StepID cannot support multiple HumanWait nodes.** A plan may contain
`hw1 → (some steps) → hw2`. After `hw1` is signalled, the workflow continues to `hw2`.
The `HITLHandle` still points to `hw1` — any subsequent `signalHITL` call would
re-signal `hw1` (which has already been consumed by the workflow; Temporal would buffer
it on a dead channel).

The fix described in the design doc (`wait_token`) addresses this: the workflow emits a
unique opaque token when it actually arrives at a `human_wait` node. The signal endpoint
only accepts a signal accompanied by the current valid `wait_token`. When the token
changes (workflow moved to the next `human_wait`), old tokens are automatically rejected.

**c) No idempotency.** Nothing prevents two concurrent HTTP requests from both reading the
handle and both calling `SignalWorkflow`. Temporal buffers signals — the second one stays
on the channel and will be consumed by the *next* `human_wait` node (if any) or hang
indefinitely if there is none. In a repeated-wait loop scenario (loop body with
`human_wait`), this causes the Nth iteration to receive the signal that was meant for
the (N+1)th iteration.

**d) No terminal state.** Once the workflow completes (Completed, Failed, or Cancelled),
nothing deletes the handle. A stale signal request after completion would still find the
key and attempt to signal a finished workflow, producing a Temporal error.

### Proposed `HITLHandle` schema

```go
type HITLHandle struct {
    WorkflowID string `json:"workflow_id"`
    RunID      string `json:"run_id"`
    TenantID   string `json:"tenant_id"`   // authoritative — used for access control
    // WaitToken is a unique opaque string set by the workflow each time it reaches a
    // human_wait node. It changes on every wait occurrence (repeated waits, loop bodies).
    // The signal endpoint requires the caller to present the current WaitToken.
    // Empty string means the workflow has not yet reached any human_wait node.
    WaitToken  string `json:"wait_token"`
    // StepID is the current step the workflow is waiting on. Updated alongside WaitToken.
    StepID     string `json:"step_id"`
    // State is the lifecycle state of this handle.
    State      string `json:"state"` // "submitted" | "waiting" | "signalled" | "done"
}
```

**State transitions (atomic via Redis WATCH + MULTI/EXEC or Lua script):**

```
submitted  →  waiting    (workflow reaches human_wait node; dag-worker writes wait_token)
waiting    →  signalled  (signal endpoint presents correct wait_token; handle updated)
signalled  →  waiting    (workflow resumes and reaches another human_wait; new wait_token)
signalled  →  done       (workflow completes Completed/Failed/Cancelled; handle deleted or TTL)
waiting    →  done       (workflow cancelled/timed out while waiting)
```

The signal endpoint rejects any request when `State != "waiting"` or `wait_token != presented_token`.
This prevents duplicate signals and stale signals after completion.

### How `wait_token` is written

The dag-worker (`canvas_workflow.go`) emits the token when it reaches a `human_wait`
node, **before** calling `sigCh.Receive`. The token is a UUID. The workflow sends a
Temporal **outbound signal** to a fixed channel that the agent-runtime subscribes to:

```
Signal name: "hitl_ready:{taskID}"
Payload: { "wait_token": "<uuid>", "step_id": "<nodeID>" }
```

The agent-runtime runs a background listener (started alongside the workflow in
`executeSkill`) that receives `hitl_ready:{taskID}` and atomically updates the
`HITLHandle` from `state=submitted` to `state=waiting` with the new `wait_token`.

**Alternative (no reverse signal plumbing):** The agent-runtime polls the Temporal
workflow for a query result (`workflow.SetQueryHandler("hitl_status", ...)`) on
SubscribeToTask / GetTask calls, which avoids a persistent background listener.
Polling is acceptable here because the wait time is human-scale (seconds to minutes).

**Decision point for approval:** Reverse signal (push, lower latency) vs. query poll
(pull, simpler, no background goroutine). Recommendation: **poll via Temporal workflow
query** — simpler to implement and test, and human approval latency makes the difference
imperceptible.

### `HITLStore` API changes

```go
// New methods needed:
func (s *HITLStore) UpdateWaitToken(ctx context.Context, taskID, waitToken, stepID string) error
func (s *HITLStore) TrySignal(ctx context.Context, taskID, waitToken string) (HITLHandle, error)
    // atomic CAS: only succeeds if state==waiting && handle.WaitToken==waitToken
    // transitions state to signalled; returns the handle for use in SignalCanvasStep
func (s *HITLStore) MarkDone(ctx context.Context, taskID string) error
    // transitions any state → done; used when workflow finishes
```

**Files changed:**
- `go/internal/agentgen/hitl_store.go` — `HITLHandle` schema + new methods
- `go/internal/agentgen/hitl_store_test.go` — tests for new methods

**New tests:** HS-6 (UpdateWaitToken changes state + wait_token),
HS-7 (TrySignal succeeds with correct token, state → signalled),
HS-8 (TrySignal with wrong token → error, state unchanged),
HS-9 (TrySignal when state != waiting → error),
HS-10 (MarkDone removes handle / sets state=done),
HS-11 (repeated wait: UpdateWaitToken when state=signalled → state=waiting, new token)

---

## Gap 3 — `InputRequired` emitted before workflow reaches `human_wait` node

**Severity: HIGH**

### What the code does

`executeSkill` currently emits `TaskStateInputRequired` immediately after `Submit()`:

```go
submitted, err := rt.canvasSubmitter.Submit(bgCtx, ic, plan, initial)
// ...
yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired, nil), nil)
return
```

### Problem

The workflow may execute several LLM / HTTP / Transform steps before reaching the
`human_wait` node. The client receives `InputRequired` while the workflow is still running
its preamble. The client's UI would present the "waiting for human input" prompt before
the agent has finished generating the context that requires human review.

### Proposed fix

Emit `TaskStateWorking` immediately after Submit (correct — workflow is running). Emit
`TaskStateInputRequired` only when the `HITLHandle` transitions to `state=waiting` (i.e.,
the workflow has actually reached the `human_wait` node and written a `wait_token`).

**Delivery mechanism**: the `SubscribeToTask` implementation (Gap 5) is the natural place
to stream `InputRequired` to reconnecting clients. For the initial `message/send` response,
`TaskStateWorking` is sufficient and correct.

```go
// executeSkill HITL path — correct version:
yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil)
return
// (not InputRequired — that is delivered via SubscribeToTask when state=waiting)
```

**Files changed:** `go/cmd/agent-runtime/main.go` (change one line)
**No new tests needed** — RT-HITL-1 must be updated to expect `TaskStateWorking`.

---

## Gap 4 — `PlanHasHumanWait` does not scan SubPlans (loop bodies)

**Severity: MEDIUM**

### What the code does

```go
func PlanHasHumanWait(plan *ExecutionPlan) bool {
    // comment: "Loop body nodes are not scanned — human_wait inside a loop body is not
    // supported and should be caught at compile time."
    for _, n := range plan.Nodes {
        if n.Type == StepHumanWait { return true }
    }
    return false
}
```

### Problem

A canvas designer can place a `human_wait` node inside a loop body. The comment says
"not supported and should be caught at compile time" but:
1. No compile-time error is emitted for this case.
2. The requirement explicitly states it must work.
3. The `validateLoopBodies` function does not reject `human_wait` nodes in bodies.

When `PlanHasHumanWait` returns false for a plan where the only `human_wait` is inside
a loop body, `executeSkill` takes the normal synchronous path and calls `Execute` instead
of `Submit`. The workflow then gets a 12-minute timeout (not 24h) and the HTTP connection
is held open — the original async-HITL problem.

### Proposed fix

Make `PlanHasHumanWait` recursive over `node.SubPlan`:

```go
func PlanHasHumanWait(plan *ExecutionPlan) bool {
    if plan == nil { return false }
    for _, n := range plan.Nodes {
        if n.Type == StepHumanWait { return true }
        if n.SubPlan != nil && PlanHasHumanWait(n.SubPlan) { return true }
    }
    return false
}
```

**Files changed:** `go/internal/agentgen/plan_compiler.go`
**New tests:** PC-HW-1 (human_wait in loop body → PlanHasHumanWait returns true),
PC-HW-2 (no human_wait anywhere → false), PC-HW-3 (nested SubPlan depth-2 → scanned)

---

## Gap 5 — HumanWait does not work inside loop bodies

**Severity: HIGH**

### What the code does

`runBranch` (outer DAG) handles `WaitingForHuman` at lines 225–243:

```go
if stepOut.WaitingForHuman {
    sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+node.StepID)
    var humanVars agentgen.PipelineVars
    sigCh.Receive(ctx, &humanVars)
    ...
}
```

`runBodyBranch` (loop body, lines 665–718) has **no such block**. After `ExecuteActivity`
returns `WaitingForHuman=true`, `runBodyBranch` falls through to the `nextIDs` logic with
empty `bodyOut.Vars`. The loop body continues with a missing `reply_var`.

### Additional problem: per-node timeout not enforced in either path

`HumanWaitConfig` has `Prompt string` and `ReplyVar string` but **no `TimeoutSeconds`
field**, despite the node's `ConfigFieldDoc` listing it. The `sigCh.Receive` in
`runBranch` blocks on the parent workflow context (24h) regardless of what the canvas
author configured.

### Proposed fixes

**a) Add signal-receive block to `runBodyBranch`:**

Immediately after the `actErr != nil` check (line 684), add:

```go
if bodyOut.WaitingForHuman {
    sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+bodyNode.StepID)
    var humanVars agentgen.PipelineVars
    sigCh.Receive(ctx, &humanVars)
    if ctx.Err() != nil { return }
    for k, v := range humanVars { localVars[k] = v }
    prevID = bodyNode.StepID
    if len(bodyNode.Next) > 0 { currentID = bodyNode.Next[0] } else { currentID = "" }
    continue
}
```

**b) Add `TimeoutSeconds int` to `HumanWaitConfig`:**

```go
type HumanWaitConfig struct {
    Prompt         string `json:"prompt"`
    ReplyVar       string `json:"reply_var"`
    TimeoutSeconds int    `json:"timeout_seconds"` // 0 = no timeout (wait forever)
}
```

**c) Use `workflow.Select` with a timer in both `runBranch` and `runBodyBranch`:**

When `TimeoutSeconds > 0`, replace the bare `sigCh.Receive` with a `workflow.Select`:

```go
if node.TimeoutSeconds > 0 {
    timerCh := workflow.NewTimer(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
    workflow.Select(ctx,
        workflow.SelectCase(sigCh, func(c workflow.ReceiveChannel, more bool) {
            c.Receive(ctx, &humanVars)
        }),
        workflow.SelectCase(timerCh, func(c workflow.ReceiveChannel, more bool) {
            // timeout — fail the step
            errCh.Send(ctx, fmt.Errorf("step %q: human_wait timed out after %ds",
                node.StepID, config.TimeoutSeconds))
            cancelAll()
        }),
    )
} else {
    sigCh.Receive(ctx, &humanVars)
}
```

**Note**: `node.Config` must be parsed into `HumanWaitConfig` inside the workflow. The
compiled `PlanNode.Config` (`json.RawMessage`) is already in the plan, so this is a
single `json.Unmarshal` call. No new Temporal types needed.

**Files changed:** `go/internal/agentgen/spec.go` (add `TimeoutSeconds`),
`go/internal/temporal/canvas_workflow.go` (add loop-body block + timer select in both paths)

**New tests:** CT-HW-1 (`runBodyBranch` human_wait pauses and resumes correctly),
CT-HW-2 (outer DAG timeout fires → workflow fails with typed error),
CT-HW-3 (loop body timeout fires → workflow fails with typed error),
CT-HW-4 (no timeout configured → workflow waits indefinitely until signal)

---

## Gap 6 — Multiple `human_wait` nodes require per-occurrence `wait_token`, not static StepID

**Severity: MEDIUM** (blocks correct multi-step and loop-body HITL flows)

### The problem

The current `HITLHandle.StepID` is set once at submission time to the first `human_wait`
node found by a linear scan of `plan.Nodes`. For:

- Plans with `hw1 → (steps) → hw2`: after `hw1` is signalled, `StepID` still points to
  `hw1`. The next `signalHITL` call would signal `hw1` again (dead channel).
- Loop bodies with `human_wait`: the same loop iteration's `StepID` is reused for every
  iteration. Signal delivery to iteration N signals via `human_input:hw1` — but in
  Temporal, `workflow.GetSignalChannel` returns the **same channel** across iterations
  (channels are keyed by name globally within a workflow execution). This means all
  iterations compete on the same channel.

### Required mechanism: `wait_token`

A `wait_token` is a UUID generated fresh **each time the workflow reaches a `human_wait`
node** — regardless of which step, which iteration, or which branch. It is:

1. Generated by the workflow coroutine the instant it is about to block on `sigCh.Receive`.
2. Written into `HITLHandle` via an outbound Temporal signal (`"hitl_ready:{taskID}"`)
   or workflow query (see Gap 2 decision point).
3. Presented by the signal endpoint caller alongside the human's response payload.
4. Validated by `HITLStore.TrySignal` (atomic CAS on token + state).

**Signal name for the Temporal inbound signal remains `SignalHumanInputPrefix + node.StepID`**
(unchanged — this uniquely identifies the channel for each step ID). The `wait_token`
is purely for the Redis-layer CAS guard; it is not used in the Temporal signal name.

For **loop body iterations**, the StepID is fixed (`hw1` for every iteration), so all
iterations share the same Temporal signal channel. To support repeated waits in a loop,
the workflow must drain and re-register the channel each iteration. This is already how
`workflow.GetSignalChannel` works in Temporal — it is idempotent and returns the same
channel object; `Receive` blocks until the next unread message arrives. The `wait_token`
ensures the correct iteration's signal is being delivered (the signal endpoint can only
call `Receive` once per `wait_token` issuance).

**Files changed:** `go/internal/agentgen/hitl_store.go` (already covered in Gap 2),
`go/internal/temporal/canvas_workflow.go` (emit `hitl_ready` outbound signal or
register a query handler before each `sigCh.Receive`)

**No new files.** Tests covered under Gap 2 (HS-6..HS-11) and Gap 5 (CT-HW-1..CT-HW-4).

---

## Gap 7 — `CancelTask` and `SubscribeToTask` do not reach the Temporal workflow

**Severity: HIGH**

### `CancelTask`

The SDK `localManager.Cancel` runs the executor's cancel logic against the in-memory
execution map. For HITL tasks, the executor returned `TaskStateWorking` and exited — the
in-memory execution no longer exists. The Temporal workflow keeps running.

**Fix**: Wrap the SDK `RequestHandler` in a `HITLRequestHandler` that intercepts `CancelTask`:

```go
func (h *HITLRequestHandler) CancelTask(ctx context.Context, req *a2a.CancelTaskRequest) (*a2a.Task, error) {
    handle, err := h.hitlStore.Get(ctx, string(req.TaskID))
    if err == nil && handle.State != "done" {
        _ = h.canvasCanceler.CancelWorkflow(ctx, handle.WorkflowID, handle.RunID)
        _ = h.hitlStore.MarkDone(ctx, string(req.TaskID))
    }
    return h.inner.CancelTask(ctx, req)
}
```

This requires a `CanvasCanceler` interface on `TemporalExecutor`:

```go
type CanvasCanceler interface {
    CancelWorkflow(ctx context.Context, workflowID, runID string) error
}

func (e *TemporalExecutor) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
    return e.client.CancelWorkflow(ctx, workflowID, runID)
}
```

### `SubscribeToTask` (reconnect)

The SDK `localManager.Resubscribe` checks `m.executions[taskID]`. For HITL tasks, this
map entry does not exist (the execution returned after emitting `TaskStateWorking`).
`SubscribeToTask` returns "no active execution" — the client cannot reconnect.

**Fix**: `HITLRequestHandler.SubscribeToTask` intercepts and handles the HITL case:

```go
func (h *HITLRequestHandler) SubscribeToTask(ctx context.Context, req *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
    handle, err := h.hitlStore.Get(ctx, string(req.TaskID))
    if err != nil {
        return h.inner.SubscribeToTask(ctx, req)  // not a HITL task
    }
    switch handle.State {
    case "waiting":
        // Workflow is paused. Stream InputRequired, then block until signal is delivered.
        return streamHITLWaiting(ctx, h.execCtx, handle)
    case "signalled":
        // Human responded; workflow is resuming. Block until completion via AwaitResult.
        return streamHITLCompletion(ctx, h.execCtx, handle, h.canvasAwaiter)
    case "done":
        // Workflow finished. Delegate to inner (task store has terminal state).
        return h.inner.SubscribeToTask(ctx, req)
    default: // "submitted"
        // Workflow started but hasn't reached human_wait yet. Block until state changes
        // to "waiting" (poll the HITLHandle) then proceed as "waiting" case.
        return streamHITLPollingUntilWaiting(ctx, h.execCtx, handle, h.hitlStore, h.canvasAwaiter)
    }
}
```

`streamHITLCompletion` calls `h.canvasAwaiter.AwaitResult(ctx, workflowID, runID)` and
synthesizes `ArtifactEvent` + `Completed` status from the result. This is the
`CanvasAwaiter` interface (approved in original design):

```go
type CanvasAwaiter interface {
    AwaitResult(ctx context.Context, workflowID, runID string) (*agentgen.ExecutionResult, error)
}

func (e *TemporalExecutor) AwaitResult(ctx context.Context, workflowID, runID string) (*agentgen.ExecutionResult, error) {
    run := e.client.GetWorkflow(ctx, workflowID, runID)
    var out CanvasAgentWorkflowOutput
    if err := run.Get(ctx, &out); err != nil {
        return nil, fmt.Errorf("TemporalExecutor: await result: %w", err)
    }
    return &agentgen.ExecutionResult{Text: out.ResultText, MediaType: out.ResultMT}, nil
}
```

**Files changed:**
- `go/internal/temporal/temporal_executor.go` — add `AwaitResult` + `CancelWorkflow` + interfaces
- `go/cmd/agent-runtime/main.go` — add `HITLRequestHandler` wrapper; wire `canvasAwaiter` and
  `canvasCanceler` fields on `Runtime`; use `HITLRequestHandler` in `handle()`

**New tests:** RT-HITL-9 (CancelTask on HITL task → CancelWorkflow called, handle marked done),
RT-HITL-10 (CancelTask on non-HITL task → inner handler called, no Temporal call),
RT-HITL-11 (SubscribeToTask with state=waiting → emits InputRequired then blocks),
RT-HITL-12 (SubscribeToTask with state=signalled → AwaitResult called, emits Completed),
RT-HITL-13 (SubscribeToTask with no handle → delegates to inner handler)

---

## Complete Change Set

| # | Gap | Severity | Files changed | New tests |
|---|---|---|---|---|
| 1 | Auth: move signal endpoint to go-bridge admin router | CRITICAL | `hitl_store.go`, `agent-runtime/main.go`, `admin/router.go`, `admin/canvas_tasks.go` (new), `admin/service/service.go`, `admin/service/canvas_tasks.go` (new) | CSIG-1..4 |
| 2 | `HITLHandle` schema: add TenantID, WaitToken, State; atomic CAS methods | HIGH | `hitl_store.go`, `hitl_store_test.go` | HS-6..11 |
| 3 | Emit `TaskStateWorking` (not `InputRequired`) immediately after Submit | HIGH | `agent-runtime/main.go` | RT-HITL-1 updated |
| 4 | `PlanHasHumanWait` recursive over SubPlans | MEDIUM | `plan_compiler.go`, `plan_compiler_test.go` | PC-HW-1..3 |
| 5 | HumanWait in loop body + per-step timeout | HIGH | `spec.go`, `canvas_workflow.go` | CT-HW-1..4 |
| 6 | Multiple waits / loop: `wait_token` per occurrence | MEDIUM | `hitl_store.go` (covered by Gap 2), `canvas_workflow.go` (emit token before Receive) | Covered by HS-6..11, CT-HW-1..4 |
| 7 | CancelTask + SubscribeToTask reach Temporal | HIGH | `temporal_executor.go`, `agent-runtime/main.go` | RT-HITL-9..13 |

**Total new/updated tests:** CSIG-1..4, HS-6..11, PC-HW-1..3, CT-HW-1..4, RT-HITL-1 (update), RT-HITL-9..13 = 26 tests

**Existing 14 Phase 5-B tests** that still pass unchanged: HS-1..5 (store CRUD),
TE-10..13 (Submit/Signal/PlanHasHumanWait), RT-HITL-2..8 (with RT-HITL-1 updated).

---

## Implementation Order

1. **Gap 4** — `PlanHasHumanWait` recursive scan (isolated, no dependencies)
2. **Gap 5a** — Add `TimeoutSeconds` to `HumanWaitConfig`; loop-body signal block (no auth dependency)
3. **Gap 2** — `HITLHandle` schema + atomic CAS methods (foundation for all other gaps)
4. **Gap 3** — Change `InputRequired` → `TaskStateWorking` (one-line fix)
5. **Gap 6** — Emit `wait_token` from workflow before `sigCh.Receive` (depends on Gap 2)
6. **Gap 7** — `CanvasCanceler`, `CanvasAwaiter`, `HITLRequestHandler` (depends on Gaps 2+3)
7. **Gap 1** — Move signal endpoint to go-bridge admin router (depends on all above)

All ship as a single commit: `fix(hitl): Phase 5-B hardening — auth, state model, multi-wait, loop body, reconnect`

---

## Open Decision Required Before Implementation

**Gap 2 / Gap 6 — How does the workflow notify agent-runtime that it has reached a `human_wait` node?**

**Option A (push — Temporal outbound signal):**
- Workflow emits `workflow.GetSignalChannel(ctx, "hitl_ready:"+taskID).Send(ctx, readyPayload)` before `sigCh.Receive`.
- Agent-runtime runs a background goroutine per HITL task that subscribes to this signal and updates `HITLHandle` state.
- Pro: real-time, sub-second latency.
- Con: requires a persistent background goroutine in agent-runtime; goroutine lifecycle management; not cancellable if pod restarts.

**Option B (pull — Temporal workflow query):**
- Workflow registers `workflow.SetQueryHandler("hitl_status", ...)` returning `{state, wait_token, step_id}`.
- Agent-runtime polls `client.QueryWorkflow(ctx, workflowID, runID, "hitl_status")` on `GetTask` / `SubscribeToTask` calls.
- Pro: no background goroutine; process-restart-safe; simple to test with mocks.
- Con: polling adds latency (acceptable at human timescales); poll interval needs tuning.

**Recommendation: Option B (workflow query poll)**. Human approval latency is seconds-to-minutes; a 2-second poll lag is imperceptible. The implementation is simpler, has no goroutine lifecycle issues, and is testable without a live Temporal server.

**Please confirm Option B or redirect before implementation starts.**
