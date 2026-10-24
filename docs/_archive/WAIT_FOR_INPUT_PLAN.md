# Wait-for-Input Node — Design & Implementation Plan
# Last updated: 2026-10-03
# Status: Planning → Phase 1 ready to build

---

## What is it?

A **Wait-for-Input** node pauses the workflow and waits for the end user to send
another message over the same WebSocket/SSE connection. When the message arrives,
it is stored in a named flow variable and the flow continues.

This is the missing piece that makes the Cycle node useful for real clarification
loops — without it, a Cycle body can run LLMs but can't get new user input between
iterations.

**Analogy:** LangGraph's `interrupt()` / `Command(resume=value)` pattern, applied
to our WS/SSE connection model.

---

## Real-world use case — refund validation loop

```
[Entry: user message]
    |
    v
[Cycle: break when amount_valid = true, max 3]
    |
    +-- LLM: extract refund_amount from input
    |
    +-- Condition: numgt .refund_amount "0"
    |       true  → set amount_valid = true → Cycle exits
    |       false → LLM: "I couldn't understand the amount, please re-enter"
    |             → Wait-for-Input → stores reply in "input" → next iteration
    |
[Condition: large refund?]
    ...
```

Without Wait-for-Input: the Cycle loops but never gets a new user message.
With Wait-for-Input: the flow genuinely pauses, the user retypes, the Cycle
re-extracts and re-validates.

---

## How it works (Temporal)

The workflow already knows how to pause on a signal — HIL does it for manager
approval. Wait-for-Input uses the same pattern but the signal comes from the
user's next WS/SSE message, not an admin API call.

**Signal name:** `appflow_user_input:{nodeID}` — scoped to the node so a flow
with multiple wait points doesn't mix up messages.

**Signal sender:** the WS/SSE handler, when it receives a new message on a run
that is currently paused at a Wait-for-Input node. The handler checks the run's
pending-wait state (stored in Redis) and signals the Temporal workflow.

**Flow var written:** configurable `output_var` (default: `input`) — replaces
the accumulated input for downstream nodes in the same way an LLM node's
output does.

---

## Phase 1 — Core implementation

### 1A — Go: WaitForInputConfig + node kind

**`go/internal/appflow/compiler.go`** — add after `HILConfig`:

```go
type WaitForInputConfig struct {
    // Prompt is the message sent to the user while waiting (rendered against flow vars).
    Prompt    string `json:"prompt,omitempty"`
    // OutputVar is the flow variable that receives the user's reply. Default: "input".
    OutputVar string `json:"output_var,omitempty"`
    // TimeoutSeconds: 0 = wait indefinitely (default for user-facing waits).
    TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}
```

Add `"wait_for_input"` case in `compileNode`'s flow_control switch.

### 1B — Go: signal name constant + execution

**`go/internal/appflow/workflow.go`** — add constant:
```go
AppFlowSignalUserInput = "appflow_user_input"
```

**New file: `go/internal/appflow/wait.go`**

`execWaitForInputNode`:
1. If `cfg.Prompt` is set — render it against vars, publish it as a `node_start`
   trace so the WS/SSE handler streams it to the user
2. Optionally persist a "pending_wait" record in Redis (so the WS handler knows
   the run is paused — see 1D)
3. Block on `workflow.GetSignalChannel(ctx, AppFlowSignalUserInput+":"+node.ID).Receive`
4. On receive: store payload in `vars[outputVar]` and `accumulated`
5. `traceNode` node_done

Wire into `workflow.go` dispatch loop:
```go
case "wait_for_input":
    var waitErr error
    accumulated, vars, waitErr = execWaitForInputNode(ctx, node, input, accumulated, vars, shortAO)
    if waitErr != nil {
        out.Status = "failed"
        retErr = waitErr
        return
    }
    currentID = firstEdgeTarget(outEdgesBySource[node.ID])
    continue
```

### 1C — Go: signal sender in WS/SSE handler

When a new user message arrives on a WebSocket connection that has an active run:

**Current behavior:** message is sent as the initial workflow input at run start.
There is no path for a second message on the same run.

**New behavior:** if the run is in `status = running` AND there is a
`pending_wait` record in Redis for this run → signal the Temporal workflow
instead of starting a new run.

**Redis key:** `them:wait:{runID}` → `nodeID` (set by execWaitForInputNode
activity, TTL = workflow timeout)

**Signal call:**
```go
temporalClient.SignalWorkflow(ctx,
    appflow.WorkflowIDForRun(tenantID, runID),
    "",
    appflow.AppFlowSignalUserInput+":"+nodeID,
    userMessage,
)
```

### 1D — Go: pending-wait activity

A short activity (like HIL's persist activity) that writes the Redis key:
`them:wait:{runID}` = `nodeID`, TTL = 4 hours.

Cleared when: the signal is received (workflow deletes it) OR the run completes
(FinalizeRunActivity deletes it).

### 1E — Validate

Add to `validate.go` — `wait_for_input` node:
- Must have exactly one outgoing edge
- `output_var` if set must be a valid identifier (no spaces)

### 1F — Canvas UI

New node type `wait_for_input` in the node registry (Go):
- Emoji: ⏳
- Color: amber (`#f59e0b`)
- Label: "Wait for Input"
- Description: "Pause and wait for the user's next message"
- One target handle (in), one source handle (out)

Properties panel: prompt textarea + output_var input.

### 1G — Tests

| Test | What it proves |
|---|---|
| `TestWaitForInputCompile` | compiles to kind="wait_for_input" with WaitForInputConfig |
| `TestWaitForInputValidation` | output_var with spaces → error |
| `TestWaitForInputSignalName` | signal name constant format |

---

## Files that will change

| File | Change |
|---|---|
| `go/internal/appflow/compiler.go` | `WaitForInputConfig` type, `"wait_for_input"` in compileNode |
| `go/internal/appflow/workflow.go` | `AppFlowSignalUserInput` constant, `case "wait_for_input"` |
| `go/internal/appflow/wait.go` | New — `execWaitForInputNode`, pending-wait activity |
| `go/internal/appflow/validate.go` | wait_for_input validation rules |
| `go/internal/appflow/noderegistry.go` | wait_for_input node def (emoji, color, ports) |
| `go/internal/ws/handler.go` | Check pending-wait Redis key on new message; signal instead of reject |
| `go/internal/sse/handler.go` | Same as WS |
| `go/internal/appflow/wait_test.go` | New — compile + validate tests |
| `go/TEST_INDEX.md` | New test rows |
| `docs/REDIS.md` | Document `them:wait:{runID}` key |

---

## What this unlocks

Once shipped, the Smart Refund Handler can have a real clarification loop:
- User types bad amount → flow asks for clarification → **pauses**
- User retypes → flow resumes → re-extracts → re-validates
- Valid amount → continues to HIL/Cycle approval path

This is the last missing primitive for a genuinely conversational AppFlow.
