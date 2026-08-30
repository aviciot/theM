# HumanWait Async Design
# Last updated: 2026-08-30

## Status: NOT YET IMPLEMENTED

The current 24h timeout fix (`planHasHumanWait` + `humanWaitWorkflowTimeout`) is a
**partial mitigation only**. It prevents the workflow from being killed at 12 minutes,
but the fundamental problem remains: the calling HTTP request is still synchronously
blocked waiting for the workflow to complete. This means:

- The HTTP connection is held open for up to 24h (practically: until proxy/load-balancer
  timeout kills it).
- If the client disconnects, the Go `ctx` is cancelled, which triggers
  `CancelWorkflow` — silently terminating the HITL session.
- There is no way for the client to reconnect and receive the result after cancellation.

**HumanWait is not safe for production use until the async design below is implemented.**

---

## Required Design

### 1. Asynchronous task return

When `executeSkill` detects a plan with a `human_wait` node, it must NOT block.
Instead:

1. Submit the Temporal workflow (as today).
2. Return immediately to the A2A SDK with `TaskState = "working"` so the HTTP
   connection is released.
3. The A2A SDK task ID (the stable `execCtx.TaskID`) is stored as the "pending
   invocation handle" alongside the Temporal workflow ID.

The caller receives a task with state `working` and a task ID. They can reconnect
later (via `tasks/resubscribe` or `tasks/get`) to learn the final result.

### 2. Workflow signal path (human provides input)

When a human responds via the UI/API:

1. The frontend calls a new endpoint: `POST /apps/{slug}/tasks/{task_id}/signal`
   with the human's input as the body.
2. The agent-runtime (or them-go-bridge) looks up the pending invocation by
   `task_id`, retrieves the Temporal workflow ID from the task store, and calls
   `TemporalClient.SignalWorkflow(ctx, workflowID, runID, SignalHumanInputPrefix+stepID, payload)`.
3. The workflow receives the signal via `sigCh.Receive` and continues.

### 3. Reconnect / resubscribe

The A2A SDK already supports `tasks/resubscribe` (method `SubscribeToTask`). The
agent-runtime must:

1. Store the mapping `{task_id → workflow_id + run_id}` in Redis with a TTL matching
   the workflow timeout (e.g., 24h key).
2. On `SubscribeToTask`, reconnect to the existing Temporal workflow via
   `client.GetWorkflow(ctx, workflowID, runID)` and re-stream events from it.

Redis key schema (to be added to `docs/REDIS.md` when implemented):

```
them:hitl:{task_id}   →  {"workflow_id":"...", "run_id":"...", "step_id":"..."}
TTL: 24h
```

### 4. Explicit user cancellation

The frontend can cancel a pending HITL task via `tasks/cancel` (method `CancelTask`).
This must:

1. Look up `{task_id → workflow_id}` from Redis.
2. Call `client.CancelWorkflow` — NOT triggered by HTTP disconnect.
3. HTTP context cancellation (proxy timeout, client TCP close) must NOT be wired to
   `CancelWorkflow` for HITL workflows. The workflow should outlive the HTTP session.

Implementation: in `executeSkill`, for HITL plans, do NOT propagate the HTTP request
`ctx` to `TemporalExecutor.Execute`. Use a separate background context with the
workflow TTL as deadline.

### 5. HTTP context isolation

```go
// executeSkill: for human_wait plans, detach from the HTTP request context
// so proxy timeouts and client disconnects don't cancel the Temporal workflow.
wfCtx := context.Background() // not r.Context()
if isHITL {
    var cancelFn context.CancelFunc
    wfCtx, cancelFn = context.WithTimeout(context.Background(), 24*time.Hour)
    defer cancelFn()
}
result, err := backend.Execute(wfCtx, ic, plan, initial)
```

---

## Phase to implement

This work is a separate task. Do not expand HumanWait in any commit that does not
implement the full async pattern above. The partial 24h-timeout fix prevents silent
12-minute kills and is a safe incremental step, but shipping a canvas agent with
`human_wait` nodes to production requires this full design.

**Prerequisites:**
- Redis task store extended with `{task_id → workflow handle}` mapping
- New `tasks/resubscribe` handler in agent-runtime
- New signal endpoint in them-go-bridge or agent-runtime
- Frontend reconnect logic (poll or EventSource reconnect)
- Integration tests for: disconnect-and-reconnect, cancellation, signal delivery

---

## What the current 24h timeout fix does (correctly)

- Prevents `WorkflowExecutionTimedOut` at 12 minutes for HITL workflows.
- `planHasHumanWait` correctly detects the presence of any `human_wait` node.
- `HumanWaitTimeout int64` is stored in `CanvasAgentWorkflowInput` for future use by
  the dag-worker (e.g., to set activity-level timeouts appropriately).

## What it does NOT do

- Does not detach the HTTP request context from the workflow.
- Does not implement async task return.
- Does not implement reconnect/resubscribe.
- Does not implement the signal endpoint.
- A proxy/LB timeout or client disconnect will still cancel the workflow.
