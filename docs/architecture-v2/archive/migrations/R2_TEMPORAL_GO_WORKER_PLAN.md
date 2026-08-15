# Phase R-2: Temporal-Owned Go Worker Plan
# Date: 2026-07-28
# Branch: main — HEAD 6aed825
# Prepared with: Claude Opus (architecture session)
# Preceding phase: R-1 complete (Prometheus metrics + structured logging)

---

## Executive Summary

R-2 replaces the Python Temporal worker with a Go Temporal worker so that all run
execution is owned by `Temporal → Go Worker → Go Activity → Go Orchestrator`.
Simultaneously, R-2 eliminates the inline Go orchestrator path (the `temporalEnabled=false`
branch in `ws/handler.go` and `sse/handler.go`), which currently allows orchestration to
run entirely outside Temporal. After R-2, Temporal is the unconditional, single durable
owner of every run.

---

## Critical Correction: Hybrid Execution Model

### Identified violation

`go/internal/ws/handler.go` lines 481–561 and the equivalent in `go/internal/sse/handler.go`
contain two branches:

```
if h.temporalEnabled && h.temporalClient != nil {
    // 11a. Temporal path — starts Python OrchestrationWorkflow
} else {
    // 11b. Go-inline path — calls h.orch.Run() directly (NO Temporal)
}
```

The inline path (`11b`) is documented as a "permanent fallback" and is used whenever
`temporalEnabled=false` (the default when `WithTemporal` is not called, including all
non-integration tests). This path runs the agentic loop — LLM calls, agent calls, tool
calls, all orchestration iterations — entirely in a goroutine inside the handler, with no
Temporal durability, no crash recovery, no HITL support, and no retry.

**This directly violates the mandatory architecture constraint:**
> No Agent, LLM, Tool or orchestration execution may run through an independent fast path
> outside the Temporal-managed run.

### How R-2 fixes it

After R-2:
1. `temporalEnabled` is removed as a runtime flag. The `WithTemporal` call is mandatory.
2. There is no inline orchestrator path in the WS/SSE handlers.
3. Every run goes through `ExecuteWorkflow → Go Worker → RunOrchestratorActivity → Go Orchestrator`.
4. The Go worker runs in the same process as the Go bridge (or a separate binary — see §6).

### Documents that must be corrected after R-2

The following documents describe or imply a valid inline path and must be updated:

| Document | Incorrect statement |
|---|---|
| `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.4 | "Start Temporal workflow OR inline orchestrator goroutine" — must become "Start Temporal workflow (unconditional)" |
| `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §9.0 Phase R-0 | States A-2 partial (extract shared preamble) is in scope for R-0; the reason given was to support the inline path equally — the full extraction is now in R-2 scope |
| `go/internal/ws/handler.go` comment on `WithTemporal` | "When WithTemporal is not called … all connections use the inline path" — remove this |
| `go/internal/sse/handler.go` comment on `WithTemporal` | Same |
| `go/TEST_INDEX.md` | Any test that tests the inline orchestrator path from the handler must be reclassified or removed after R-2 |

---

## 1. Exact Responsibilities

### 1.1 Temporal Workflow (`internal/temporal/workflow.go`)

**What it owns:**
- The durable execution boundary for one run
- Retry semantics at the run level: if `RunOrchestratorActivity` fails with an
  infrastructure error (Temporal retries based on retry policy), the workflow re-executes
  the activity; the orchestrator rebuilds from checkpointed `task_messages`
- HITL pause and resume: when the activity returns `TaskInputRequired`, the workflow
  blocks on `GetSignalChannel(SignalHumanInput)` and re-executes the activity with the
  human response appended
- Workflow ID namespace: `ctx-{contextID}` — must be preserved for HITL signal routing;
  Python and Go must use the same scheme

**What it must not own:**
- Individual LLM tokens
- Agent call results (these are produced inside the activity)
- Session lifecycle (Redis Hash, heartbeat)
- WebSocket or SSE connection state
- Event delivery to the client

**Temporal sees:** one workflow per run, one activity execution per HITL segment.

**Current state:** `internal/temporal/workflow.go` is correct. No change required for R-2.

### 1.2 Go Temporal Worker

**What it owns:**
- Registration of `RunOrchestratorActivity` on task queue `them-orchestration`
- Worker lifecycle: start on process startup, stop on graceful shutdown
- Activity heartbeat plumbing (the activity implementation heartbeats; the worker polles
  the task queue)

**Deployment model for R-2 (see §6):** The Go worker may run in the same binary as the
Go bridge (registered at startup in `cmd/them/main.go`) or in a separate binary. The
smallest safe slice for R-2 uses the same binary. The task queue name `them-orchestration`
is shared with the Python worker during the transition period; Temporal routes tasks to
whichever worker is registered and available.

**What it must not own:**
- Business logic — that is the activity's job
- Event delivery
- Session or gate state

### 1.3 Go Activity (`RunOrchestratorActivity`)

**What it owns:**
- Calling `orchestrator.Orchestrator.Run()` with the inputs from `WorkflowInput`
- Heartbeat loop (every 5s via `activity.RecordHeartbeat`)
- Converting `ErrTaskInputRequired` to `temporal.ApplicationError{Type: "TaskInputRequired"}`
- Translating any other orchestrator error into a form Temporal can retry

**The activity is the boundary between Temporal and Go application code.** It receives
the `WorkflowInput`, calls the orchestrator, and returns `WorkflowResult`. It has no
knowledge of Redis, WebSocket, or session state.

**Current state:** `internal/temporal/activities.go` has the correct skeleton. The
`OrchestratorRunner` interface is already defined. The heartbeat goroutine is correct.
The missing piece is a fully implemented `orchestrator.Orchestrator.Run()` (see R-1
orchestrator feature parity — that is a prerequisite for R-2; see §6 Blockers).

**Change required for R-2:** Switch `WorkflowInput` from `PythonOrchestrationInput` to
the native Go `WorkflowInput`. The activity already accepts `WorkflowInput`; the handler
must be updated to build and send `WorkflowInput` instead of `PythonOrchestrationInput`.
`python_input.go` becomes dead code once the Python worker is deactivated.

### 1.4 Go Orchestrator (`internal/orchestrator/orchestrator.go`)

**What it owns:**
- The agentic loop: load history, call LLM, interpret tool calls, fan out agents,
  accumulate results, iterate until stop or max_iterations
- In-memory message accumulation (not DB rebuild per iteration — OD-3 decision)
- Durable checkpoints after each iteration to `them.task_messages`
- Budget token enforcement (in-process counter + checkpoint to `tasks.tokens_used`)
- Parallel agent fan-out (sync.WaitGroup + dual semaphore — OD-4 decision)
- Memory injection when threshold is reached (OD-5 decision)
- A2A agent card auto-discovery (TTL-cached via `agentregistry`)
- Publishing events to the in-process event bus

**What it must not own:**
- Session, gate, or admission state
- Redis key management
- WebSocket or SSE writes
- Temporal lifecycle

**Current state:** `internal/orchestrator/orchestrator.go` is a correct skeleton (~303
lines) but is missing 8 of 13 Python feature equivalents (see gate document §1.5 gap table).
These gaps are the prerequisite work in R-1 (orchestrator feature parity). R-2 does not
add new orchestrator features — it wires the existing/R-1-completed orchestrator into the
Temporal activity path and removes the inline path.

### 1.5 Go Bridge (`internal/ws/`, `internal/sse/`)

**What it owns:**
- Transport upgrade (WS, SSE)
- Authentication (`auth.Cache.Validate`)
- Admission gate (`gate.Check → session.Register → gate.Confirm`)
- Run creation (`recorder.CreateRun`) — the bridge creates the run row BEFORE starting
  the workflow, so the run ID is canonical and known before Temporal starts
- Subscribing to the run-stream BEFORE calling `ExecuteWorkflow` (race-free bootstrap)
- Starting the Temporal workflow with `WorkflowInput` (after R-2: not `PythonOrchestrationInput`)
- Streaming events from Redis run-stream to the client
- Cleanup on disconnect: `session.End`, `gate.Release`, context cancel

**What it must not own after R-2:**
- The inline orchestrator path (`h.orch.Run()` called directly from the handler)
- The `temporalEnabled` flag — Temporal is always required

**Change required for R-2:**
1. Remove `temporalEnabled` flag and the inline branch (`11b`) from both handlers
2. Replace `PythonOrchestrationInput` with `WorkflowInput` in `ExecuteWorkflow` call
3. `WithTemporal` becomes mandatory (no-temporal is only valid in unit tests with a mock)

### 1.6 Event Bus / Redis Streams

**What they own (event delivery only, not execution):**

| Channel | Owner | Direction | Temporal involvement |
|---|---|---|---|
| In-process event bus | Go Orchestrator (inside activity) | Orchestrator → Bridge | None — bus is in-process |
| Redis Streams `them:dash:run:{runID}:stream` | Go Activity (publishes via orchestrator) | Activity → Bridge (via Redis) | None — Redis is outside Temporal |
| Redis Pub/Sub `them:dash:run:{runID}:tokens` | Legacy Python path | Python Activity → Bridge | None |

**The event bus is the delivery channel, not the execution engine.**

After R-2, the Go activity runs inside the Go worker, which may be in the same process
as the Go bridge. When they are co-located, the in-process event bus works directly:
`Orchestrator.Run()` publishes to the bus; the bridge handler subscribes on the same bus
instance and streams events to the client. This is the same mechanism the current inline
path uses — but now the publisher lives inside a Temporal activity goroutine rather than
a raw handler goroutine.

When the Go worker is in a separate process (future, not R-2 scope), the event delivery
switches to Redis Streams. The bridge subscribes to the stream before starting the
workflow; the activity publishes to the stream. The `runstream.Dispatcher` already handles
both modes based on `RUN_EVENTS_MODE`. No change to the dispatcher is needed for R-2.

**Redis Streams must NOT carry execution state.** They carry events that have already been
produced by a completed Temporal activity execution. No orchestration decision may be
based on stream content.

---

## 2. Run Ownership

| Question | Answer |
|---|---|
| Who creates the run? | **Go Bridge** — `recorder.CreateRun()` is called in the handler before `ExecuteWorkflow`, using a pre-generated `runID`. The run row exists with `status=running` before Temporal sees the request. |
| Who owns retries? | **Temporal** — the workflow's retry policy governs whether `RunOrchestratorActivity` is re-executed on infrastructure failure. `MaximumAttempts=1` means Temporal retries once on transient failure; the orchestrator handles its own internal iteration retries. |
| Who owns cancellation? | **Go Bridge** (context cancel propagation). Client disconnect → `cancel()` in handler → orchestration `context.Context` is cancelled → `Provider.Stream()` HTTP call aborted → activity returns error → Temporal marks workflow as cancelled. The bridge also listens for admin disconnect signals via Redis pub/sub (`them:sess:control:{sid}`) and cancels the context. |
| Who owns timeout? | **Temporal** via `StartToCloseTimeout=10m` on the activity. The Go bridge also cancels the context on client disconnect, which cuts the run short before Temporal's timeout fires. LLM HTTP calls use a 120s context timeout (per gate document §3.5). |
| Who records final status? | The **Go Orchestrator** calls `recorder.UpdateStatus(ctx, runID, domain.RunStatusCompleted/Failed)` at the end of `Run()`. If the activity panics or the context is cancelled before the orchestrator can update, the **Run Reconciler** (`internal/reconciler/reconciler.go`) periodically marks stale `running` runs as `failed`. |
| What happens after worker crash? | Temporal detects heartbeat timeout (heartbeat every 5s, timeout 15s). Temporal reschedules `RunOrchestratorActivity` on an available worker. The new activity execution calls `orchestrator.Run()`, which calls `HistoryLoader.LoadHistory()` to rebuild the message slice from the last checkpoint in `them.task_messages`. Execution resumes from the last complete iteration. No events are re-sent to the client; the client reconnects and uses `last_event_id` to replay from Redis Streams. |
| What happens after bridge disconnect? | The WS/SSE handler's `defer` chain runs: `session.End`, `gate.Release`, `cancel()`. The Temporal workflow continues running — the Go worker is decoupled from the bridge. When the client reconnects, a new handler calls `recorder.GetRun()` to check if the run is still active, then subscribes to the Redis stream (with the previous `last_event_id`) and presents the in-progress workflow output. |

---

## 3. Execution Flow

### 3.1 Single User Request (happy path)

```
Client → WS connect
  Bridge handler:
    1. Token extract + auth.Cache.Validate → TokenInfo
    2. epconfig.Load(epSlug) → EPConfig
    3. gate.Check(ctx, gateCfg) → SADD + reservation
    4. WS upgrade
    5. session.Register(ctx, sessInfo) → Redis Hash
    6. gate.Confirm(ctx, gateCfg) → extend TTL
    7. recorder.CreateRun(ctx, run) → DB run row (status=running)
    8. runstream subscribe BEFORE ExecuteWorkflow (race-free)
    9. Read first client message (30s deadline)
   10. Build WorkflowInput (runID, contextID, userMsg, HistoryWindow)
   11. temporalClient.ExecuteWorkflow("ctx-{contextID}", "OrchestrationWorkflow", input)
         │
         └── Temporal assigns to Go Worker
               │
               └── RunOrchestratorActivity(ctx, input)
                     │
                     ├── heartbeat goroutine (every 5s)
                     │
                     └── orchestrator.Run(ctx, runID, contextID, userMsg, history=[])
                           │
                           ├── buildMessages([], userMsg)
                           ├── HistoryLoader.LoadHistory(ctx, contextID, limit=historyWindow)
                           ├── provider.Stream(ctx, messages, tools, opts)
                           │     └── emit "token" events → event bus → bridge → client
                           ├── executeTools(ctx, toolCalls) → agentregistry.Invoke × N
                           │     └── emit "tool_result" events → event bus → bridge → client
                           ├── checkpoint: write task_messages to DB
                           ├── [repeat until stop or max_iterations]
                           ├── recorder.UpdateStatus(runID, completed)
                           └── publish "done" event → event bus → bridge → client
                     │
                     └── return WorkflowResult{Status: completed}
         │
         └── Workflow returns → bridge's wfRun.Get(ctx) goroutine unblocks → close(orchDone)
  Bridge handler:
   12. streamEvents drains evCh/rsEvCh until orchDone
   13. Send "done" to client if not already received
   14. defer: session.End → gate.Release → cancel → conn.Close
```

### 3.2 Multiple Orchestration Iterations

The orchestrator loop handles this entirely within the single activity execution:

```
RunOrchestratorActivity calls orchestrator.Run()
  Loop iteration 1:
    provider.Stream() → LLM returns tool_calls
    executeTools() → parallel agentregistry.Invoke × N
    CHECKPOINT: write assistant turn + tool results to task_messages
    append results to in-memory messages slice

  Loop iteration 2:
    provider.Stream() with updated messages (in-memory, no DB reload)
    LLM returns stop
    CHECKPOINT: write final assistant turn to task_messages
    recorder.UpdateStatus(completed)
    publish done
```

Temporal history records: one activity start, one activity complete. It does not
record individual iterations, LLM calls, or agent calls.

### 3.3 Parallel Agent Calls

Inside the orchestrator's `executeTools()` (after R-1 feature parity):

```
tool calls = [agentA, agentB, agentC, agentD, agentE]
max_parallel_tools = 2

goroutine 1: parallel_sem.Acquire → agent_sem[A].Acquire → agentregistry.Invoke(A)
goroutine 2: parallel_sem.Acquire → agent_sem[B].Acquire → agentregistry.Invoke(B)
goroutine 3: wait parallel_sem (blocked until goroutine 1 or 2 releases)
goroutine 4: wait parallel_sem
goroutine 5: wait parallel_sem

  → goroutine 1 completes: parallel_sem.Release → goroutine 3 unblocks
  → ...all 5 complete, results[0..4] populated
  → WaitGroup.Wait()
  → checkpoint: write all 5 tool results to task_messages
  → append to messages slice
```

All of this happens inside `RunOrchestratorActivity`, inside one Temporal activity.
Temporal does not see individual agent calls.

### 3.4 Tool and A2A Calls

Tool calls are agent calls (every tool in this platform maps to an A2A agent):

```
orchestrator.executeTools(ctx, toolCalls):
  for each toolCall:
    slug = strip("agent__", toolCall.Name)
    input = marshal(toolCall.Input)
    agentregistry.Invoke(ctx, slug, input)
      → L1 in-process cache lookup (agent card)
      → L2 Redis cache lookup (them:agents:{slug})
      → DB lookup if cache miss
      → HTTP POST to agent endpoint with context (ctx propagated → cancellable)
      → return json.RawMessage result
```

The `ctx` passed to `agentregistry.Invoke` is the activity context — cancellation from
client disconnect propagates through the activity context to the agent HTTP call.

### 3.5 Final Response

After the last LLM iteration stops:
1. Orchestrator publishes `"done"` event to in-process bus
2. Activity returns `WorkflowResult{FinalText, Status: completed}`
3. Workflow receives the result, returns it to Temporal
4. Bridge's `wfRun.Get(ctx)` goroutine receives the result, closes `orchDone`
5. `streamEvents` loop in the handler detects `orchDone` and exits
6. Deferred cleanup runs

### 3.6 Error Path

```
LLM error or orchestrator error:
  orchestrator.publishError(ctx, ..., err)  → "error" event to bus → client
  recorder.UpdateStatus(runID, failed)
  return err

Activity receives error:
  if not ErrTaskInputRequired:
    return WorkflowResult{failed}, temporal.ApplicationError wrapping err
  Temporal workflow marks workflow as failed (MaximumAttempts=1, no retry on app error)

Bridge wfRun.Get(ctx) returns err:
  log Warn
  orchDone closes
  handler exits via defer chain
```

### 3.7 Cancellation Path

```
Client disconnects (WS read error):
  handler calls cancel()
  orchestration context cancelled
    → provider.Stream() HTTP call gets context.Canceled → loop exits
    → agentregistry.Invoke HTTP call gets context.Canceled → returns error
    → orchestrator.Run() returns error (context.Canceled)
  Activity returns error to Temporal
  Temporal marks workflow as cancelled/failed
  Bridge wfRun.Get(ctx) returns (ctx was also cancelled → returns ctx.Err())
  deferred cleanup: session.End, gate.Release
```

---

## 4. Streaming

### 4.1 What Bypasses Temporal

Redis Streams and the in-process event bus are **event delivery channels**, not execution
paths. They carry events that have already been produced by the orchestrator (which runs
inside a Temporal activity). They bypass Temporal in the sense that Temporal does not
record individual events — and this is correct and required: recording 1000 LLM tokens
per run in Temporal history would explode the history size.

| What bypasses Temporal? | Reason it's correct to bypass |
|---|---|
| LLM token events | Transient delivery; not durable state; Temporal history must not record streaming tokens |
| Tool call/result events | Delivery only; durable state is in `task_messages` DB checkpoint |
| `agent_status` events | Purely informational; no state |
| `done` / `error` events | Delivered via event bus; durable outcome is in `runs.status` (DB) and Temporal workflow result |

**Nothing that bypasses Temporal changes the run's outcome or execution state.** The
run's durable state is owned by: (a) Temporal workflow history, (b) `them.runs` status,
(c) `them.task_messages` checkpoints.

### 4.2 Event Correlation

Every event carries `run_id` and `context_id`. The bridge handler subscribes to
`context_id` on the in-process bus (or `run_id` on Redis Streams). The correlation is
established at run creation time (step 7 in §3.1):

```
runID = newUUID()           // pre-generated by bridge
contextID = resolveContext() // from session, or new UUID for new conversations
recorder.CreateRun(... RunID=runID, ContextID=contextID ...)
eventBus.Subscribe(ctx, contextID)    // in-process path
runstream.Subscribe(ctx, runID)       // Redis Streams path
ExecuteWorkflow(..., WorkflowInput{RunID: runID, ContextID: contextID, ...})
```

The workflow input carries both IDs. The activity passes them to the orchestrator.
The orchestrator uses `contextID` as the event bus topic and `runID` in all event payloads.
The bridge's subscription key (`contextID`) matches what the orchestrator publishes to.

### 4.3 Duplicate Terminal Event Prevention

The R-0 `termCh` fix (already in place) handles the in-process bus:
- `termCh` has capacity 1 — a second `done` or `error` event to the same subscriber
  is dropped silently (non-blocking send, buffer already full)
- The handler loop exits after processing the first terminal event from either `evCh`
  or `termCh` — it does not re-enter the loop

For the Redis Streams path:
- XADD is idempotent in the sense that events are appended; the client reads sequentially
  with a cursor (`last_event_id`)
- The bridge only publishes the final `done` event once (orchestrator publishes once,
  activity completes once)
- If the bridge reconnects mid-stream, XRANGE from `last_event_id` replays unprocessed
  events; the `done` event is replayed at most once per stream position

### 4.4 Client Reconnect

When a client reconnects to an in-progress run:

```
1. Client sends last_event_id in the initial message
2. Bridge handler authenticates and creates a NEW session (new session_id)
   (the run already exists — bridge checks recorder.GetRun(runID) is still running)
3. Bridge subscribes to Redis stream with last_event_id as cursor:
   runstream.Subscribe(ctx, runID, lastEventID) → XRANGE from cursor, then XREAD BLOCK
4. Bridge does NOT start a new Temporal workflow — the existing workflow is still running
5. Events replayed from cursor + live events flow to the reconnected client
6. When the in-progress activity completes, the "done" event arrives on the stream
   and is delivered to the reconnected client normally
```

If the run completed before the client reconnected:
- `recorder.GetRun(runID)` returns `status=completed`
- If `events_transport=streams`: XRANGE provides the full replay up to `done`
- If `events_transport=pubsub`: reply `{"type":"replay_unavailable","message":"..."}`
- If MAXLEN trim removed events: `{"type":"replay_unavailable","message":"events trimmed"}`

---

## 5. State

### 5.1 What Stays in Temporal History

Temporal's own event history (not the LLM message history) records:

- `WorkflowExecutionStarted` — `WorkflowInput` (runID, contextID, userMsg, historyWindow)
- `ActivityTaskScheduled` — `RunOrchestratorActivity` input
- `ActivityTaskStarted` — which worker picked it up
- `ActivityTaskCompleted` — `WorkflowResult` (finalText, status)
- `WorkflowExecutionCompleted` — final result
- On HITL: `WorkflowExecutionSignaled` — `domain.Message` (human response)
- On failure: `ActivityTaskFailed`, `WorkflowExecutionFailed`

**Temporal history must not contain:**
- Individual LLM tokens
- Individual agent call inputs/outputs
- Redis key values
- Session or gate state

### 5.2 What Stays in PostgreSQL

The durable business record:

| Table | What is stored | Written by |
|---|---|---|
| `them.runs` | One row per run: runID, contextID, sessionID, status, events_transport, created_at, completed_at | Bridge (create), Orchestrator (update status) |
| `them.run_steps` | One row per agent invocation: runID, agentSlug, input, output, latency | Orchestrator (after R-1 feature parity) |
| `them.run_usage` | Token counts and cost per LLM call per run | Orchestrator (after R-1) |
| `them.tasks` | Root task per run; delegated task per agent call; tokens_used budget counter | Orchestrator |
| `them.task_messages` | Serialized LLM message history per completed iteration (durable checkpoint) | Orchestrator (after each iteration — OD-3) |
| `them.artifacts` | File outputs produced by agent calls (after R-2 artifact scope, §9 of gate) | Orchestrator |

### 5.3 What Stays in Go Memory (In-Process)

Within the lifetime of one `orchestrator.Run()` call (= one activity execution):

| State | Lifetime | Owner |
|---|---|---|
| `messages []domain.Message` | Activity execution (from run start to activity return) | Orchestrator |
| `tokensUsed int` | Activity execution | Orchestrator (checkpointed to DB) |
| `iterationCount int` | Activity execution | Orchestrator |
| Event bus subscriber channel | Handler goroutine lifetime (WS/SSE handler) | Bridge |
| `wfRun temporalclient.WorkflowRun` | Handler goroutine lifetime | Bridge |
| `sessionID` | Handler goroutine lifetime | Bridge handler |

None of this in-process state survives a process restart. Temporal owns restartability.

### 5.4 Checkpoint Timing

Per the OD-3 decision (already resolved):

```
Run start:
  → HistoryLoader reads task_messages (DB LIMIT = historyWindow)
  → Initialize messages slice in-process

After each LLM response:
  → CHECKPOINT: write assistant turn to task_messages

After each tool batch completes:
  → CHECKPOINT: write tool results to task_messages

Per iteration:
  → Update tasks.tokens_used (DB write)

Run end:
  → recorder.UpdateStatus(runs, completed/failed)
```

Checkpoint granularity: one DB write per LLM call (assistant turn), one DB write per
tool batch. At most 2 DB writes per orchestrator iteration. This is the minimum needed
for correct crash recovery.

### 5.5 Crash Recovery Sequence

```
Worker process crashes mid-activity (heartbeat timeout = 15s):
  Temporal detects: no heartbeat received
  Temporal reschedules RunOrchestratorActivity on available worker

New activity execution begins:
  orchestrator.Run() called with same WorkflowInput (same runID, contextID)
  HistoryLoader.LoadHistory(contextID, historyWindow):
    → reads task_messages WHERE context_id = contextID ORDER BY created_at
    → returns messages through the last completed checkpoint
  messages slice rebuilt from checkpoints
  Iteration count and budget read from tasks.tokens_used
  Run continues from next iteration (iteration N+1 after checkpoint at N)

LLM is NOT re-called for iterations that were checkpointed.
Redis Streams replay provides client-side recovery of missed events.
```

---

## 6. Migration: Python Worker → Go Worker

### 6.1 Current State

The current execution path when `temporalEnabled=true`:

```
Bridge handler → ExecuteWorkflow(PythonOrchestrationInput, "OrchestrationWorkflow")
             → Temporal → Python worker (them-worker container)
                           → Python OrchestrationWorkflow
                           → Python RunOrchestratorActivity
                           → Python task_runner.py + providers/
                           → Events published to Redis Pub/Sub or Streams
```

The Go worker (`internal/temporal/activities.go`) has the `RunOrchestratorActivity`
struct and heartbeat, but its `Runner` field is satisfied by `orchestrator.Orchestrator`,
which currently lacks the 8 feature gaps identified in the gate document §1.5.

The inline path (`temporalEnabled=false`) uses the Go orchestrator directly without
Temporal, bypassing all durability.

### 6.2 Prerequisite: Orchestrator Feature Parity (R-1 scope)

R-2 requires the Go orchestrator to be feature-complete relative to Python `task_runner.py`.
The 8 gaps that block Go-worker readiness:

| Gap | Required for R-2? | Notes |
|---|---|---|
| In-memory message accumulation | Yes — OD-3 | Core correctness |
| Budget token enforcement | Yes | Prevents runaway cost |
| Durable checkpoints (task_messages) | Yes | Required for crash recovery |
| Parallel tool fan-out (WaitGroup + semaphore) | Yes | Production parity |
| A2A agent card auto-discovery | Yes | Agents won't work otherwise |
| Per-iteration token usage recording (run_usage) | Yes | Billing |
| Child task row creation (tasks.delegated) | Yes | Audit trail |
| Memory injection | No — defer to R-3 | Additive, not blocking |

Memory injection is the only gap that does not block R-2. All other gaps must be resolved
in the R-1 orchestrator feature parity wave before R-2 begins.

### 6.3 Which Python Activities Remain Temporarily

During R-2, the Python worker continues to run in parallel:

| Python component | Status during R-2 | Remove when |
|---|---|---|
| `app/temporal/workflows.py` `OrchestrationWorkflow` | Running on Python worker | After Go worker validated under load |
| `app/temporal/activities.py` `RunOrchestratorActivity` | Running on Python worker | After Go worker validated under load |
| `app/temporal/worker.py` | Polling `them-orchestration` | After Go worker validated |
| `app/services/task_runner.py` | Called by Python activity | After Python worker removed |
| `app/services/providers/` | LLM providers (Python) | After Python worker removed |
| `temporal.PythonOrchestrationInput` (Go) | Present but unused when Go worker active | Remove once Python worker decommissioned |

Temporal routes workflow tasks to whichever worker polls `them-orchestration` first.
During parallel operation, some runs go to Python and some go to Go. Both workers
register the same `WorkflowType = "OrchestrationWorkflow"` and the same `TaskQueue`.

**Important:** when switching the bridge to send `WorkflowInput` (not `PythonOrchestrationInput`),
the Python workflow cannot accept this input format. The cutover is therefore:
1. Deploy Go worker (registers on `them-orchestration`)
2. Update bridge to send `WorkflowInput`
3. Stop Python worker
This must happen atomically (as a coordinated deploy), not incrementally.

### 6.4 Compatibility Boundary

The compatibility boundary during R-2 is the Temporal task queue and workflow type:

```
Task queue: "them-orchestration"      ← both Python and Go workers poll this
Workflow type: "OrchestrationWorkflow" ← both implement this type
Workflow ID: "ctx-{contextID}"         ← must be preserved in both; HITL signals use this
```

**HITL signal compatibility:**
`SignalHumanInput = "submit_human_response"` must match between Go and Python.
Currently it does: Python registers `submit_human_response`, Go uses `SignalHumanInput = "submit_human_response"`.

**Input format break:**
`WorkflowInput` (Go-native) and `PythonOrchestrationInput` have different field sets.
The bridge currently sends `PythonOrchestrationInput`. Switching to `WorkflowInput`
breaks Python-worker compatibility. This is why the cutover must be atomic.

**Rollback:** if the Go worker has problems, restore the Python worker to the
`them-orchestration` queue and revert the bridge to send `PythonOrchestrationInput`.
The workflow ID scheme and signal names are preserved, so active HITL workflows survive.

### 6.5 Smallest Safe Vertical Slice for R-2

The smallest slice that delivers the mandatory architecture model:

**Phase R-2A: Orchestrator feature parity (R-1 proper)**

This is actually the R-1 scope from the gate document. The handover note named R-1 as
"Observability & Metrics" (the Prometheus phase), but the gate document Phase R-1 is
"Orchestrator Feature Parity". These two streams are being renamed here for clarity:

| Previous label | Content | Status |
|---|---|---|
| R-1 (gate doc) | Orchestrator feature parity | Not yet started |
| R-1 (handover label) | Prometheus metrics + structured logging | Complete (HEAD 39d505c) |

**For this plan, the phases are:**

```
R-1 (complete) = Prometheus metrics
R-2A           = Orchestrator feature parity (8 gaps in task_runner equivalence)
R-2B           = Go Temporal Worker wiring + inline path removal
```

**R-2B scope (the binding delivery for R-2):**

1. Register `Activities{Runner: orchestrator}` on Go Worker in `cmd/them/main.go`
2. Start the Go Temporal worker at process startup
3. Remove `temporalEnabled` flag from WS and SSE handlers
4. Remove inline path branches (`11b`) from both handlers
5. Replace `PythonOrchestrationInput` with `WorkflowInput` in `ExecuteWorkflow` call
6. Make `WithTemporal` unconditional (always required — tests use a mock temporal client)
7. Update `cmd/them/main.go` to pass `temporal.Activities{Runner: orch}` to worker
8. Write tests: Go activity round-trip with mock orchestrator, worker registration test
9. Coordinated deploy: Go worker up → bridge updated → Python worker down

### 6.6 Cutover and Rollback

**Cutover sequence:**

```bash
# 1. Build and start Go bridge with Go worker compiled in (temporalEnabled mandatory)
docker compose --profile go build them-go-bridge
docker compose --profile go up -d them-go-bridge

# 2. Verify Go worker is polling
docker logs them-go-bridge | grep "temporal worker: polling"

# 3. Verify Go worker picks up a test run
# (create a test run via the admin API, verify it completes via Go worker logs)

# 4. Stop Python worker (Temporal will route all new tasks to Go worker)
docker compose --profile temporal stop them-worker

# 5. Monitor: watch for workflow failures in Temporal UI (localhost:3111)
# 6. If failures: restart Python worker, revert Go bridge to Python input format
```

**Rollback:**

```bash
# Restore Python worker
docker compose --profile temporal start them-worker

# Revert bridge image to previous version (last Go bridge image without R-2B)
docker compose --profile go up -d --no-build them-go-bridge  # if using image tag
# OR: revert the WorkflowInput change and rebuild
```

Active workflows survive rollback because workflow ID and signal name are preserved.

---

## 7. Architecture Documents to Correct

The following sections in existing documents imply or describe a valid inline execution
path outside Temporal. They must be corrected in the same commit as R-2B implementation:

### 7.1 `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.4 Run Runtime

**Current text (incorrect):**
> 3. Start Temporal workflow OR inline orchestrator goroutine

**Corrected text:**
> 3. Start Temporal workflow (unconditional — inline path removed in R-2B)

### 7.2 `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.7 Streaming Pipeline

**Current text (partially incorrect):**
> Flow A — In-process bus (inline Go orchestrator path):
> Orchestrator.Run() → event.InMemoryBus.Publish() → ...

**Corrected framing:** Flow A is not the "inline handler path" but the "Go Activity path":
the orchestrator runs inside the Temporal activity, and the activity is in the same process
as the bridge (same binary). The in-process bus remains correct as an implementation
detail — the bus is still in-process — but it is no longer a "fast path that bypasses
Temporal". The orchestrator publishes to the bus from within an activity.

**Corrected text:**
> Flow A — In-process bus (Go activity path, same-binary deployment):
> Activity.Run() → Orchestrator.Run() → event.InMemoryBus.Publish() → buffered channel → WS/SSE write
> Note: this path uses Temporal for durability. The event bus is the delivery mechanism
> for events produced inside the activity, not a bypass of Temporal.

### 7.3 `CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §9 Delivery Plan Phase R-0

**Current text:**
> A-2 partial: extract shared auth→gate→session preamble into transport.HandleSession

This was listed as Phase R-0 scope. It was partially completed (the WS and SSE handlers
share common logic but it was not extracted into `transport.HandleSession`). R-2B completes
this extraction as part of removing the inline path — the common preamble can be cleanly
extracted because the two branches (inline vs Temporal) collapse to one.

### 7.4 `go/internal/ws/handler.go` comment on `WithTemporal`

**Current text:**
> When WithTemporal is not called (e.g. in tests), temporalEnabled defaults to
> false and all connections use the inline path.

**After R-2B:** remove this comment. Tests must inject a mock Temporal client.
The inline path does not exist post-R-2B.

### 7.5 `go/internal/sse/handler.go` same comment

Same correction as 7.4.

---

## 8. Confirmed Single-Owner Execution Model

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Go Bridge (internal/ws or internal/sse)                                 │
│  - Auth, Gate, Session, CreateRun                                        │
│  - Subscribe to event channel BEFORE ExecuteWorkflow                     │
│  - Start Temporal workflow (UNCONDITIONAL — no inline path)              │
│  - Stream events from bus/Redis to client                                │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │  ExecuteWorkflow(WorkflowInput)
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  Temporal Workflow (OrchestrationWorkflow)                               │
│  - Durable execution boundary                                            │
│  - HITL pause/resume via Signal                                          │
│  - Owns retry policy                                                     │
│  - Knows nothing about Redis, sessions, WebSocket                        │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │  ExecuteActivity(RunOrchestratorActivity)
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  Go Temporal Worker (same binary as bridge in R-2)                       │
│  - Polls them-orchestration task queue                                   │
│  - Dispatches to RunOrchestratorActivity                                 │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  RunOrchestratorActivity (internal/temporal/activities.go)               │
│  - Heartbeat every 5s                                                    │
│  - Calls orchestrator.Run() directly                                     │
│  - Returns ErrTaskInputRequired as Temporal ApplicationError             │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │  orchestrator.Run()
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  Go Orchestrator (internal/orchestrator)                                 │
│  - Agentic loop: LLM calls, agent calls, iterations                     │
│  - In-memory message accumulation                                        │
│  - Durable checkpoints to task_messages                                  │
│  - Parallel fan-out (WaitGroup + semaphore)                              │
│  - Publishes events to in-process bus (→ bridge → client)                │
└──────────────────────────────────┬───────────────────────────────────────┘
                                   │  Events (separate delivery channel)
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│  Event Delivery (NOT execution)                                          │
│  In-process bus (same-binary) OR Redis Streams (cross-process)          │
│  - Carries events produced inside the activity                           │
│  - Does not carry execution state                                        │
│  - Correlated by context_id / run_id                                     │
└──────────────────────────────────────────────────────────────────────────┘

PostgreSQL: run record, task_messages checkpoints, run_usage, run_steps, tasks
Redis:      session state, gate admission, rate limits, event delivery, token cache
Temporal:   workflow history, retry, HITL signals
```

**Rule:** No LLM call, agent call, or orchestration iteration may execute outside this
stack. The in-process bus and Redis Streams are delivery channels, not execution paths.
The inline handler path (`h.orch.Run()` called directly from the WS/SSE handler) is
eliminated in R-2B.

---

## 9. Summary Tables

### 9.1 Smallest R-2 Implementation Scope

**R-2A (prerequisite): Orchestrator feature parity**
8 items from gate document §1.5 gap table (excluding memory injection which is deferred):
- In-memory message accumulation (OD-3)
- Budget token enforcement
- Durable checkpoints to `task_messages`
- Parallel fan-out (OD-4)
- A2A agent card auto-discovery
- Per-iteration token usage recording (`run_usage` rows)
- Child task row creation (`tasks.delegated`)
- `TestOrchestrator_CheckpointRecovery`, `TestOrchestrator_ParallelFanOut` (required tests)

**R-2B (core R-2): Go Temporal Worker wiring + inline path removal**
1. `cmd/them/main.go`: register Go Temporal worker at startup
2. `internal/ws/handler.go`: remove inline branch, remove `temporalEnabled`, use `WorkflowInput`
3. `internal/sse/handler.go`: same
4. `internal/temporal/python_input.go`: mark deprecated (remove after Python worker off)
5. Tests: worker registration test, activity round-trip with mock orchestrator
6. Coordinated deploy: Go worker up → bridge updated → Python worker stopped
7. Document corrections (§7.1–7.5 above)

### 9.2 Components Reused

All 25 "reusable as-is" components from gate document §7.1:
gate, session, auth, epconfig, ratelimit, runrecorder, runstream, event bus (R-0 fix in
place), llm, agentregistry, temporal workflow, reconciler, domain, config, server, db,
cache, crypto, admin handlers + DAL

Additionally reused from R-1:
- `internal/metrics/` — Prometheus metrics already wired to WS/SSE handlers

### 9.3 Components Rewritten / Extended

| Component | Change |
|---|---|
| `internal/orchestrator/orchestrator.go` | Add 7 missing features (R-2A) |
| `internal/temporal/activities.go` | Fully implement (was skeleton); integrate with full orchestrator |
| `internal/ws/handler.go` | Remove inline path, remove temporalEnabled flag, use WorkflowInput |
| `internal/sse/handler.go` | Same as WS handler |
| `cmd/them/main.go` | Register and start Go Temporal worker |
| `internal/temporal/python_input.go` | Deprecated (not deleted until Python worker off) |

### 9.4 Blockers

| Blocker | Current state | Required for |
|---|---|---|
| Orchestrator feature parity (R-2A) | Not started — 7 of 8 gaps unimplemented | R-2B (Go worker cannot replace Python worker without full parity) |
| Test suite for orchestrator features | Not started | R-2A commit gate |
| `WorkflowInput` vs `PythonOrchestrationInput` cutover coordination | Partially designed (Go `WorkflowInput` exists; Python does not accept it) | Atomic deploy in R-2B |
| Go Temporal SDK worker registration in `cmd/them/main.go` | Not done | R-2B |
| R-1 gate (from gate document) | Complete (R-0 gate passed, R-1 metrics complete) | Unblocked |

### 9.5 Exact First Implementation Task

**Start R-2A: Orchestrator feature parity.**

Session: fresh Sonnet session.

First task: implement in-memory message accumulation + durable checkpoints (OD-3) in
`internal/orchestrator/orchestrator.go`:

1. Add `HistoryLoader` injection to `Orchestrator.New()` (already in interface)
2. Call `loader.LoadHistory(ctx, contextID, historyWindow)` at start of `Run()` to build
   initial messages slice (if `history` param is non-nil, use it; otherwise load from DB)
3. After each LLM iteration: write assistant turn to `them.task_messages` via a
   new `CheckpointWriter` interface (to be implemented in `internal/runrecorder` or a
   new `internal/checkpoint` package)
4. After each tool batch: write tool results to `them.task_messages`
5. Write `TestOrchestrator_CheckpointRecovery`
6. Run `go test ./...` — zero failures
7. Commit `internal/orchestrator/`, `go/TEST_INDEX.md`

Proceed to next gap (budget enforcement), then parallel fan-out, then the remaining gaps,
each as a focused commit. After all 7 gaps are committed and tests pass, begin R-2B.

### 9.6 Whether R-2 Implementation May Begin

**R-2A: YES — may begin immediately in a fresh Sonnet session.**

The orchestrator feature parity work does not require any Temporal changes, any Python
worker changes, or any Docker/Redis/DB changes. It is pure Go orchestrator code and tests.

**R-2B: NOT YET — blocked on R-2A completion.**

R-2B (Go worker wiring + inline path removal) may begin only after:
- R-2A orchestrator feature parity is complete and `go test -race ./...` passes
- Integration test confirms the Go activity produces correct output for a 2-iteration
  run with one agent call

---

*Plan written: 2026-07-28. Next session: start R-2A with a fresh Sonnet session.*
*Mandatory constraint: Temporal is the single durable owner of every run. No orchestration*
*execution may run outside the Temporal-managed activity after R-2B is complete.*
