# HumanWait Phase 5-B — Gap Analysis & Implementation Proposal
# Author: Claude (session 2026-08-30)
# Status: PENDING APPROVAL — no code written yet

---

## Background

Phase 5-B (commit `3b1052f`) implemented the core async HumanWait skeleton:

- **`HITLStore`** — Redis-backed handle store (`them:hitl:{task_id}`, TTL 24h)
- **`PlanHasHumanWait`** — exported from `plan_compiler.go`
- **`TemporalExecutor.Submit()`** — starts the Temporal canvas workflow without blocking
- **`TemporalExecutor.SignalCanvasStep()`** — delivers a `human_input:{stepID}` signal
- **`executeSkill` HITL path** — detaches from HTTP context, calls `Submit()`, stores handle,
  returns `TaskStateInputRequired` immediately
- **`signalHITL` handler** — `POST /agents/{slug}/tasks/{task_id}/signal` reads handle from
  Redis and calls `SignalCanvasStep`

This document records the gaps found during a full audit against the Phase 5-B requirements,
and proposes exact fixes for each.

---

## What Was Audited

| File | Findings |
|---|---|
| `go/internal/agentgen/hitl_store.go` | Correct. Key schema, TTL, CRUD. |
| `go/internal/agentgen/spec.go` | `HumanWaitConfig` missing `TimeoutSeconds` field (struct/doc mismatch). |
| `go/internal/temporal/canvas_workflow.go` | `runBranch` handles `WaitingForHuman`. `runBodyBranch` (loop body path) does **not**. |
| `go/internal/temporal/canvas_activities.go` | `ExecuteStepActivity` returns `WaitingForHuman=true` for `human_wait` nodes. Correct. |
| `go/internal/temporal/temporal_executor.go` | `Submit()` and `SignalCanvasStep()` correct. No `AwaitResult()` method yet. |
| `go/cmd/agent-runtime/main.go` | `signalHITL` has no auth. No idempotency guard. No CancelTask override. No SubscribeToTask override. |
| A2A SDK `a2asrv.NewHandler` | Uses `localManager` by default. `Resubscribe` requires active in-memory execution — fails after HITL executor returns `input-required`. |

---

## Gap Analysis

### Gap 1 — `signalHITL` has no authentication

**Risk: HIGH**

`POST /agents/{slug}/tasks/{task_id}/signal` is an unauthenticated chi route. Any caller
who learns or guesses a task ID can deliver an arbitrary signal payload to a running
production workflow. This is a security vulnerability.

Additionally, the `HITLHandle` stored in Redis does not record the `tenant_id` — if a
cross-tenant task-ID collision occurred (unlikely but not impossible), a signal from tenant
A could unblock tenant B's workflow.

**Proposed fix**

1. Add `TenantID string` to `HITLHandle` in `hitl_store.go`.
2. At `Store()` time in `executeSkill`, pass `ic.TenantID`.
3. In `signalHITL`, call `parseInvocationContext(r)` to validate the bearer token and
   extract the caller's `InvocationContext`.
4. Cross-check `ic.TenantID == handle.TenantID` — return 403 if mismatch.

**Files**: `go/internal/agentgen/hitl_store.go`, `go/cmd/agent-runtime/main.go`
**New tests**: RT-HITL-6 (wrong tenant → 403), RT-HITL-7 (correct tenant → 200)

---

### Gap 2 — Signal delivery is not idempotent

**Risk: MEDIUM**

Calling `signalHITL` twice on the same `task_id` calls `client.SignalWorkflow` twice.
The Temporal workflow's `sigCh.Receive(ctx, &humanVars)` only reads once — the second
signal sits on the channel forever, and if the workflow is ever re-used with the same
step ID (e.g. after resume), it would incorrectly consume the stale signal.

**Proposed fix**

Delete the `HITLHandle` from Redis immediately after a successful `SignalCanvasStep` call.
The second call gets `ErrHITLNotFound` → 404. This makes signal delivery "consume-once":
exactly the right semantic for a single human approval.

**Files**: `go/cmd/agent-runtime/main.go` (add `rt.hitlStore.Delete` after successful signal)
**New tests**: RT-HITL-8 (duplicate signal → 404 on second call)

---

### Gap 3 — Per-step HITL timeout not enforced

**Risk: MEDIUM**

The node `ConfigFieldDoc` documents a `timeout_seconds` field for `human_wait` nodes, but
`HumanWaitConfig` in `spec.go` only has `Prompt` and `ReplyVar`. The `timeout_seconds`
value is never parsed or used.

The workflow's `sigCh.Receive(ctx, &humanVars)` blocks on the parent workflow context
(24h TTL) regardless of the per-node configured timeout. A canvas author who sets
`timeout_seconds: 300` gets no timeout enforcement.

**Proposed fix**

1. Add `TimeoutSeconds int \`json:"timeout_seconds"\`` to `HumanWaitConfig` in `spec.go`.
2. In `canvas_workflow.go` `runBranch` HumanWait block, if `TimeoutSeconds > 0`, use
   `workflow.NewTimer(ctx, duration)` alongside the signal channel in a `workflow.Select`
   block. On timeout, either fail the workflow step or inject empty vars and continue
   (the exact behavior should be configurable — failing is safer).

**Files**: `go/internal/agentgen/spec.go`, `go/internal/temporal/canvas_workflow.go`
**New tests**: CT-HW-1 (timeout fires → workflow step fails/continues as configured),
CT-HW-2 (no timeout configured → blocks until signal arrives normally)

---

### Gap 4 — `CancelTask` does not reach the Temporal workflow

**Risk: MEDIUM**

When a client calls `tasks/cancel` (A2A `CancelTask` method), the SDK's `localManager`
runs its cancel logic against the in-memory execution map. For HITL tasks, the executor
already returned `input-required` and exited — the in-memory execution no longer exists.
The Temporal workflow keeps running indefinitely in the background with no way to stop it
short of the 24h workflow timeout.

**Proposed fix**

Wrap the SDK's `RequestHandler` in a thin `HITLRequestHandler` that intercepts `CancelTask`.
Before delegating to the inner handler:

1. Call `rt.hitlStore.Get(ctx, taskID)`.
2. If a handle is found, call `rt.canvasSignaler` (or the Temporal client directly) with
   `client.CancelWorkflow(ctx, handle.WorkflowID, handle.RunID)`.
3. Delete the handle from Redis.
4. Delegate to the inner handler to update the A2A task state.

This requires no SDK internals — it's a simple `RequestHandler` decorator.

**Files**: `go/cmd/agent-runtime/main.go` (or new `go/cmd/agent-runtime/hitl_handler.go`
if needed to stay under 500 lines)
**New interface on `TemporalExecutor`**: `CancelWorkflow(ctx, workflowID, runID string) error`
with a matching `CanvasCanceler` interface (consistent with `CanvasSubmitter`/`CanvasSignaler`)
**New tests**: RT-HITL-9 (CancelTask finds HITL handle → CancelWorkflow called + handle deleted),
RT-HITL-10 (CancelTask on non-HITL task → delegates to inner handler, no Temporal call)

---

### Gap 5 — Reconnect / `SubscribeToTask` fails after `input-required`

**Risk: HIGH**

The design doc states: "the A2A SDK already supports `tasks/resubscribe`… reconnect to the
existing Temporal workflow via `client.GetWorkflow`".

This is **not true with `NewHandler`'s default configuration**. `NewHandler` creates a
`localManager` whose `Resubscribe` method does:

```go
func (m *localManager) Resubscribe(...) (Subscription, error) {
    execution, ok := m.executions[taskID]
    if !ok {
        return nil, fmt.Errorf("no active execution")  // ← always hit for HITL
    }
    ...
}
```

For HITL tasks, `executeSkill` returns `input-required` and exits — the execution is
removed from the in-memory map. A subsequent `tasks/resubscribe` or `SubscribeToTask`
call returns an error to the client with no way to learn when the human has approved.

**Proposed fix (Option B — custom `SubscribeToTask` in the `HITLRequestHandler` wrapper)**

The `HITLRequestHandler` wraps the SDK `RequestHandler` and intercepts `SubscribeToTask`:

1. Check `rt.hitlStore.Get(ctx, taskID)`.
2. If a handle is found, the task is still paused waiting for a human signal. Return a
   streaming response that blocks on `client.GetWorkflow(ctx, handle.WorkflowID,
   handle.RunID).Get(ctx, &out)` — i.e. re-attaches to the running workflow and streams
   the final `Completed` event when it finishes.
3. If no handle is found, delegate to the inner handler (existing active execution or
   terminal task).

This requires a new `CanvasAwaiter` interface and `AwaitResult` method on `TemporalExecutor`:

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

The `HITLRequestHandler.SubscribeToTask` then:
- Calls `rt.canvasAwaiter.AwaitResult(ctx, handle.WorkflowID, handle.RunID)`
- On success, synthesizes an `ArtifactEvent` + `Completed` status update event and streams them
- On error, synthesizes a `Failed` status event

This approach is process-restart-safe (uses stable workflowID from Redis) and consistent
with the existing `CanvasSubmitter`/`CanvasSignaler` interface pattern.

**Files**: `go/internal/temporal/temporal_executor.go` (add `AwaitResult` + `CanvasAwaiter`),
`go/cmd/agent-runtime/main.go` (add `canvasAwaiter` field to `Runtime`; wire `TemporalExecutor`;
`HITLRequestHandler` intercepts `SubscribeToTask`)
**New tests**: RT-HITL-11 (SubscribeToTask with HITL handle → awaits and streams Completed),
RT-HITL-12 (SubscribeToTask with no handle → delegates to inner handler)

---

### Gap 6 — HumanWait does not work inside loop bodies

**Risk: HIGH**

`canvas_workflow.go` has two separate branch-walk functions:

- `runBranch` (outer DAG, lines 140–281) — handles `WaitingForHuman` at lines 225–243
- `runBodyBranch` (loop body, lines 610–718) — **no `WaitingForHuman` block**

A `human_wait` node inside a loop body would:
1. Run `ExecuteStepActivity` → `WaitingForHuman=true` returned
2. `runBodyBranch` falls through to `nextIDs` logic with empty `bodyOut.Vars`
3. Pipeline variables for `reply_var` are never populated
4. Loop body proceeds with missing data — silent data loss

The requirement explicitly states: "HumanWait must behave identically in the outer DAG
and inside Loop bodies".

**Proposed fix**

Add the identical signal-receive block to `runBodyBranch` immediately after the
`actErr != nil` error check, mirroring lines 225–243 of `runBranch` exactly:

```go
if bodyOut.WaitingForHuman {
    sigCh := workflow.GetSignalChannel(ctx, SignalHumanInputPrefix+bodyNode.StepID)
    var humanVars agentgen.PipelineVars
    sigCh.Receive(ctx, &humanVars)
    if ctx.Err() != nil {
        return
    }
    for k, v := range humanVars {
        localVars[k] = v
    }
    prevID = bodyNode.StepID
    if len(bodyNode.Next) > 0 {
        currentID = bodyNode.Next[0]
    } else {
        currentID = ""
    }
    continue
}
```

When Gap 3 (per-step timeout) is also fixed, both `runBranch` and `runBodyBranch` will
get the `workflow.Select` timeout block at the same time to stay in sync.

**Files**: `go/internal/temporal/canvas_workflow.go`
**New tests**: CT-HW-3 (human_wait inside loop body pauses until signal, then loop continues
correctly with `reply_var` populated)

---

## Complete Change Set

| # | Gap | Risk | Files | New tests |
|---|---|---|---|---|
| 1 | Auth on signal endpoint + tenant scope in handle | HIGH | `hitl_store.go`, `main.go` | RT-HITL-6, 7 |
| 2 | Idempotent signal (delete handle on delivery) | MEDIUM | `main.go` | RT-HITL-8 |
| 3 | Per-step timeout (`TimeoutSeconds` in config) | MEDIUM | `spec.go`, `canvas_workflow.go` | CT-HW-1, 2 |
| 4 | `CancelTask` → `CancelWorkflow` | MEDIUM | `temporal_executor.go`, `main.go` | RT-HITL-9, 10 |
| 5 | `SubscribeToTask` reconnect via `AwaitResult` | HIGH | `temporal_executor.go`, `main.go` | RT-HITL-11, 12 |
| 6 | HumanWait in loop body (`runBodyBranch`) | HIGH | `canvas_workflow.go` | CT-HW-3 |

**Total new tests**: 10 (CT-HW-1/2/3, RT-HITL-6..12)

No new files required unless `main.go` grows past ~500 lines, in which case the
`HITLRequestHandler` wrapper (Gaps 4+5) moves to `go/cmd/agent-runtime/hitl_handler.go`.

**Existing 14 Phase 5-B tests** (HS-1..5, TE-10..13, RT-HITL-1..5) remain valid and pass.

---

## What Is NOT Changing

- `HITLStore` Redis key schema (adding `TenantID` to the JSON value only, same key pattern)
- `TemporalExecutor.Submit()` / `SignalCanvasStep()` — already correct
- `executeSkill` HITL submit path — already correct
- SDK `NewHandler` / `localManager` internals — we wrap, never fork
- All existing tests

---

## Implementation Order (recommended)

1. **Gap 6** first — pure workflow code, no auth/SDK dependency, easiest to test in isolation
2. **Gap 3** — `spec.go` + `canvas_workflow.go`, can be done alongside Gap 6
3. **Gap 2** — one-line fix in `main.go`, trivial
4. **Gap 1** — auth; requires adding `TenantID` to `HITLHandle` (schema bump in Redis)
5. **Gap 4** — `CanvasCanceler` + `HITLRequestHandler.CancelTask`
6. **Gap 5** — `CanvasAwaiter` + `HITLRequestHandler.SubscribeToTask` (most complex)

All six gaps should ship as a single commit titled `fix(hitl): Phase 5-B hardening`.

---

## Decision Required Before Implementation

For Gap 5, the `HITLRequestHandler.SubscribeToTask` calls `AwaitResult` which blocks
until the Temporal workflow completes. This means the HTTP connection is held open for
the duration of the human approval wait. The client must handle long-lived SSE connections
(standard A2A behaviour — `SubscribeToTask` is explicitly a streaming method).

**Confirm approach**: `CanvasAwaiter` interface on `TemporalExecutor` (Option A), or
pass the `client.Client` directly to the handler (Option B, simpler but less testable).

Recommendation: **Option A** — consistent with `CanvasSubmitter`/`CanvasSignaler` pattern.
