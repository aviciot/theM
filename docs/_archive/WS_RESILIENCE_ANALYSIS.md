# WebSocket Resilience Architecture Analysis

**Status:** Analysis / design proposal
**Date:** 2026-08-19
**Scope:** Go bridge (`them-go-bridge`) + Go worker (`them-go-worker`) run lifecycle
**Author:** distributed-systems review

---

## 0. TL;DR

The run's durability story is **already 80% built** and is better than the problem
statement implies. Temporal owns durable execution on a server-owned context; the
Go worker publishes every event to a durable Redis Stream (`them:dash:run:{runID}:stream`,
MAXLEN ~5000); and `StreamFromRedis` already does gap-free replay from a client
`last_event_id` cursor. The worker never sees the client's WS context.

**There is exactly one bug that makes the run die on disconnect, and it is small:**
the WS handler passes the **client-scoped `ctx`** into `Lifecycle.Start` →
`temporal.ExecuteWorkflow(ctx, …)`. In the Temporal Go SDK the context passed to
`ExecuteWorkflow` governs **only the Start RPC**, so this does *not* cancel a running
workflow — but the same `ctx` is also used for the `wfRun.Get(ctx, …)` in SSE
(the SSE handler passes `ctx`; the WS handler already correctly uses
`context.Background()`), and, more importantly, the *design intent* documented in
`go/CLAUDE.md` ("Context cancellation must propagate from WS disconnect → Temporal
cancel → LLM HTTP") is the thing that must be **deliberately severed** for resilience.

The real gaps are not in durability — they are in **reconnect routing** (a
reconnecting client cannot find the run's `runID` from a `context_id`), **the gate/session
model** (a surviving run holds no session, and reconnect must re-admit correctly),
and **completion-after-disconnect** (a client that reconnects after `done` must be
told the final answer from the DB, not the trimmed stream).

The rest of this document answers the eight questions precisely and gives a phased
plan: an MVP that is ~15 lines of change, and a "done right" target.

---

## 1. Where exactly does context cancellation propagate from WS close → workflow?

### The precise chain in `ws/handler.go`

1. `ServeHTTP` builds a cancelable context from the request:
   ```go
   ctx, cancel := context.WithCancel(r.Context())   // line 280
   defer cancel()
   ```
   `r.Context()` is the **HTTP request context**. For a hijacked WebSocket, Go's
   `net/http` server cancels `r.Context()` when the underlying connection goes away,
   and `defer cancel()` fires when `ServeHTTP` returns. Both are client-lifetime bound.

2. `streamEvents` (line 414) runs a reader goroutine:
   ```go
   go func() {
       for { if _, _, err := conn.ReadMessage(); err != nil { return } } // closes clientGone
   }()
   ```
   When the socket drops (network blip, tab close, laptop sleep, ping/pong deadline
   at line 340–343), `ReadMessage` errors, `clientGone` closes, and the `select` hits:
   ```go
   case <-clientGone:
       cancel()          // line 471 — cancels ctx
       return
   ```
   A failed `writeEvent` (line 432) also calls `cancel()`.

3. `ctx` was passed into two places:
   - `h.runEvents(ctx, …)` → `StreamFromRedis(ctx, …)`. Cancel here just stops the
     **reader** — correct and harmless; the stream in Redis is untouched.
   - `h.lc.Start(ctx, handle, input)` → `lc.temporal.ExecuteWorkflow(ctx, …)`
     (lifecycle.go:345). **This is the coupling point.**

### What `ExecuteWorkflow(ctx, …)` actually does with `ctx`

In the Temporal Go SDK, the `context.Context` passed to `client.ExecuteWorkflow`
scopes **only the StartWorkflowExecution gRPC call** to the Temporal frontend. Once
the workflow is started, its execution is **owned entirely by the Temporal service +
worker**, not by that context. So — importantly — **cancelling `ctx` after Start
returns does NOT cancel the workflow.** The workflow keeps running on the worker.

**Conclusion:** contrary to the problem statement, a WS disconnect **does not** kill
the workflow via context cancellation today. The workflow survives. What dies is:

- **The event delivery** — `StreamFromRedis` reader stops (fine; events keep being
  written to Redis by the worker).
- **`Lifecycle.Release` runs via defer** (handler.go:259): this calls
  `session.End` + `gate.Release`, freeing the session slot. That is *appropriate*
  (the client is gone) — but it means the run is now **orphaned from any session**,
  and there is nothing tracking that a reconnect should be allowed to re-attach.
- The WS handler's `wfRun.Get` goroutine (line 364–374) uses
  `context.Background()` — **already correct**, it observes completion regardless of
  transport. The SSE handler's equivalent (sse/handler.go:293) uses `ctx` — **this
  one is transport-coupled and is a latent bug** (a slow/dropped SSE client cancels
  the `Get`, losing the completion observation, though again not the workflow itself).

### The one place cancellation *would* propagate to the workflow

If a future change ever wires WS-close → `Signaler`/`CancelWorkflow`
(the `go/CLAUDE.md` "propagate to Temporal cancel" intent), *that* is where a
disconnect would truly kill the run. **That intent must be explicitly reversed** as
the first design decision below. There is currently no such call in the read path,
which is why runs already survive — but the documented intent is a trap waiting for
a well-meaning future edit.

---

## 2. The correct decoupling point

### Principle

The **workflow lifetime must be owned by a server context**, and the **transport
(WS/SSE) lifetime is a detachable viewer** of a durable event log. These are two
independent lifecycles that meet only at the Redis Stream.

```
   ┌─ client WS ctx ─┐      ┌──────── server-owned ─────────┐
   │ upgrade         │      │ Temporal workflow (durable)   │
   │ read/write loop │      │  worker → orchestrator loop   │
   │ StreamFromRedis ├──────┤  XADD → them:dash:run:{id}:…  │
   │  (viewer)       │ read │  DB checkpoints (task_messages)│
   └─────────────────┘      └───────────────────────────────┘
        detach freely            keeps running on disconnect
```

### Where the split already is, and where to harden it

- **Start RPC context:** `Lifecycle.Start` currently receives the client `ctx`.
  Change it to derive an internal bounded context for the ExecuteWorkflow RPC only:
  ```go
  startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  wfRun, err := lc.temporal.ExecuteWorkflow(startCtx, wfOpts, …)
  ```
  This removes any dependency of the *start* on client liveness. (Functionally the
  current code already survives because the workflow outlives the RPC, but making the
  Start RPC use a server context makes the invariant explicit and immune to a future
  "cancel on disconnect" edit.)

- **`wfRun.Get` context:** WS already uses `context.Background()` (good). **Fix SSE**
  to do the same (sse/handler.go:293 `wfRun.Get(ctx, …)` → `context.Background()`),
  otherwise SSE completion observation is transport-coupled.

- **Never** wire WS-close to `CancelWorkflow`. If cancellation is ever needed
  (e.g. user clicks "stop"), it must be an **explicit client action** (a
  `{"type":"cancel"}` frame → `Signaler` cancel), never an implicit consequence of a
  transport drop. Update `go/CLAUDE.md` to say: *"Transport disconnect MUST NOT cancel
  the workflow. Only an explicit user cancel signal or a workflow-level timeout ends a
  run."*

### Implications for Temporal's signal/cancel path

- **HITL signal (`submit_human_response`)** already flows via a server-side
  `Signaler` keyed on the workflow ID `ctx-{contextID}` (lifecycle.go:341). This is
  **already disconnect-safe**: a paused `input_required` workflow waits on
  `GetSignalChannel(...).Receive` (workflow.go:118) indefinitely, independent of any
  WS. A reconnecting client can submit the human response through a normal API call
  and the workflow resumes. Good — **do not change this.**
- **Workflow ID is deterministic:** `ctx-{contextID}`. This is the key that makes
  reconnect routing possible (see §3). One caveat: because the ID is
  `ctx-{contextID}`, **two runs on the same context_id cannot both be running** under
  the default `WorkflowIDReusePolicy` — a second Start with the same context while the
  first is still running will collide. That is actually the behavior we want for
  reconnect (see §4/§6) but must be handled explicitly rather than surfacing as a raw
  Temporal "workflow already started" error.

---

## 3. Reconnect + replay design

### What must happen on reconnect to an existing `context_id`

The client reconnects and sends (per `clientMsg`, handler.go:46):
```json
{"type":"message", "context_id":"<ctx>", "last_event_id":"<streamID>"}
```
(For a *resume* — as opposed to a new turn — `content` is empty and this is a
"re-attach", not a new message.)

The server must **distinguish four states** and route accordingly:

| State | How to detect | Action |
|---|---|---|
| Run **in progress** | Temporal `DescribeWorkflowExecution(ctx-{contextID})` → `RUNNING`; or DB `runs.status='running'` | Re-attach: `StreamFromRedis(runID, last_event_id)` → replay gap → live |
| Run **completed/failed** while gone | DB `runs.status` terminal; workflow closed | Do **not** start a new run. Send final result from DB (see §5) |
| Run **paused (input_required)** | Temporal open + `runs.status='input_required'` | Re-attach stream; UI shows the prompt; human response → Signal |
| **New turn** (client sent new `content`) | Prior run terminal + non-empty content | Normal `Admit`/`Start` with the same `context_id` → new `runID`, history loaded from `task_messages` |

### The missing primitive: `context_id → runID`

The stream key is `them:dash:run:{runID}:stream`, but the client reconnects with a
**`context_id`**, not a `runID`. Today the WS handler *generates a fresh `runID`
every connection* (`Admit` → `newRunID()`, lifecycle.go:185) and starts a new run —
so reconnect currently produces a **duplicate run**, not a re-attach. This is the
core routing gap.

Two ways to resolve `context_id → current runID`:

1. **DB (authoritative):** `SELECT id, status FROM them.runs WHERE context_id = $1
   AND tenant_id = $2 ORDER BY created_at DESC LIMIT 1`. `GetRunContextID` already
   exists (dal/runs.go:108) for the reverse direction; add the forward lookup.
2. **Redis pointer (fast path):** on Start, `SET them:ctx:{contextID}:run {runID}`
   with a TTL ≥ max run duration. Reconnect reads this first, falls back to DB.

The DB is the source of truth (it also carries `status`, which we need anyway to pick
the branch). The Redis pointer is an optional latency optimization.

### Redis Streams semantics: XREAD-with-last-ID vs consumer groups

`StreamFromRedis` already implements the correct model and it is **plain
XRANGE-then-XREAD with a continuous cursor, not consumer groups** — this is the right
choice:

- **Consumer groups (XREADGROUP)** are for *competing* consumers where each message
  is delivered to exactly one member and acked. That is wrong here: multiple viewers
  of the *same* run must each see *every* event (fan-out, not work-sharing), and there
  is no "ack/redelivery" need because the stream itself is the durable log.
- **XRANGE `(cursor +` → then XREAD BLOCK from the same cursor** (streamer.go:157–235)
  gives gap-free replay: the cursor advances entry-by-entry, so nothing written
  between "replay done" and "live read start" is dropped. This is exactly right and
  needs **no change**.

### Offset tracking

- The client is the cursor authority: it holds `last_event_id` (the Redis stream
  entry ID of the last event it rendered) and sends it on reconnect. The server is
  stateless w.r.t. offset — it just replays from whatever the client sends. This is
  the correct, horizontally-scalable design (any pod can serve any reconnect).
- **Trim handling already exists:** if `last_event_id` predates the oldest retained
  entry (MAXLEN 5000 trimmed it), `StreamFromRedis` emits `replay_unavailable`
  (streamer.go:144–154) and resumes from the oldest available entry. The client shows
  a "some history unavailable" notice. For a run that produced >5000 events while the
  client was gone, this is the graceful-degradation path; the **final answer** is
  still recoverable from the DB (§5).

### In-progress vs completed

- **In progress:** the stream has **no terminal event yet**. `StreamFromRedis` will
  replay history then block live (XREAD BLOCK 5s loop) until a terminal
  (`done/error/canceled/terminated/timed_out`, streamer.go:55–61) arrives.
- **Completed:** the stream **contains a terminal event**. Replay reaches it and
  `StreamFromRedis` closes the channel (streamer.go:193). But if the stream was
  **trimmed past the terminal event** (run finished long ago, MAXLEN evicted
  everything), replay yields nothing → the reconnecting client would hang on live
  XREAD forever. **This is why the DB status check must happen BEFORE opening the
  stream** (see §5 and §6 case 1).

---

## 4. Session gate interaction

### Today

`gate` + `session` are **admission-time, connection-scoped** constructs:
`ep:{slug}:sessions` and `app:{id}:sessions` count *live WS/SSE connections*, gated
by `EPMaxConcurrent`/`AppMaxConcurrent`, with a 90s heartbeat-refreshed shadow TTL.
When the WS closes, `Release` → `session.End` frees the slot. **The gate counts
connections, not runs.** This is the correct mental model to preserve.

### The interaction problem for a surviving run

When a run outlives its connection:
- The session slot is (correctly) **freed on disconnect** — a dead connection should
  not hold a concurrency slot.
- On **reconnect**, the client must go through the gate again and get a **new
  session** (new `sessionID`), re-attaching to the **existing run**. This is fine —
  it means "am I allowed to open a viewer connection right now?" which is exactly what
  the gate should answer. The run's existence is orthogonal to the gate.

So the model is: **gate/session governs viewers; Temporal governs the run.** A run in
progress with zero connected viewers holds **zero** session slots — which is correct
and is what lets a laptop-sleep run continue without pinning capacity.

### Can a second client connect to the same `context_id` while a run is in progress?

**Streaming-wise: yes, and it should just work.** Because delivery is XRANGE/XREAD
fan-out (not a consumer group), N viewers of the same `runID` each replay from their
own `last_event_id` and all see live events. Two tabs on the same conversation is a
legitimate, supported case.

**Turn-submission-wise: no — must be serialized.** Two clients must not both submit a
*new message* on the same `context_id` concurrently, because the workflow ID
`ctx-{contextID}` is single-writer. The second Start collides. Resolution:

- A **re-attach** (empty `content`, just `context_id`+`last_event_id`) is always
  allowed for any number of viewers — it opens no workflow, just a stream reader.
- A **new turn** while a run is in progress on that context must be **rejected or
  queued** (return a clear "a response is already in progress" error), not silently
  started as a second workflow. Detect via DescribeWorkflow/DB status before Start.

### Gate config must not double-count re-attach

A pure re-attach (viewer) still consumes a WS connection and thus should still pass
through the gate for connection-cap purposes — but consider whether re-attach viewers
should count against `EPMaxConcurrent` the same as new-run connections. Recommended:
**yes, keep it simple — a connection is a connection.** Do not add a re-attach bypass;
it would complicate the cap accounting the shadow-TTL model was designed to keep
correct.

---

## 5. Completion notification (run finished while disconnected)

The reconnecting client needs the **final output** even if the stream is gone.
Ordering on reconnect:

1. **Resolve `context_id → runID` + status from the DB** (authoritative), *before*
   touching the stream.
2. **If status is terminal** (`completed`/`failed`/`canceled`/`timed_out`):
   - Read the final answer from the DB. The workflow returns
     `WorkflowResult{FinalText, Status}` and the last assistant turn is checkpointed
     in `task_messages`. Send a synthesized terminal frame to the client:
     ```json
     {"type":"done","run_id":"<id>","content":"<final assistant text>"}
     ```
     (plus `replay_unavailable`-style note if intermediate tokens are gone).
   - Do **not** open a live XREAD (it would block forever on a trimmed stream — §3).
3. **If status is non-terminal**, open `StreamFromRedis(runID, last_event_id)`; the
   terminal event will arrive live and close the channel normally.

There is a **race** between step 1 (DB status) and the workflow finishing: the run
could be `running` at the DB read and finish a millisecond later. That is safe because
the stream still carries the terminal event and `StreamFromRedis` will deliver it
live. The dangerous direction (DB says terminal but we open a blocking live read) is
avoided by the branch in step 2.

**Belt-and-suspenders:** the worker should keep writing the terminal `done` event to
the stream with a longer MAXLEN floor, *and* the `WorkflowResult.FinalText` should be
persisted to `runs` (a `final_text`/`result` column) so completion is always
recoverable from the DB regardless of stream trim. If a `final_text` column does not
exist yet, add it (schema change — update `db/001_schema.sql` + `docs/SCHEMA.md`).

---

## 6. Edge cases

**1. Client reconnects to a run that completed 10 minutes ago (stream trimmed).**
DB status = terminal. Branch to §5 step 2: serve final answer from DB, never open a
live XREAD. Without the pre-stream DB check this hangs forever — this is the single
most important edge case to get right.

**2. Client reconnects while run is in iteration 3 of 10.**
DB status = running. Resolve `runID`, `StreamFromRedis(runID, last_event_id)`: replay
the gap (iterations the client missed) from XRANGE, then live-tail the rest. If
`last_event_id` was trimmed, `replay_unavailable` fires and it resumes from the oldest
retained entry — the client loses some middle tokens but still sees the run finish.

**3. Two clients connect to the same `context_id` simultaneously.**
Both re-attach as viewers → both stream fine (fan-out). If both try to submit a *new
turn*, the second must be rejected with "response already in progress" (workflow ID
`ctx-{contextID}` is single-writer). Never start a second workflow on the same context.

**4. Go worker pod crashes mid-run.**
Temporal detects the missed activity heartbeat (`HeartbeatTimeout = 15s`,
workflow.go:96; the activity heartbeats every 5s, activities.go:79–90) and — subject
to the retry policy — reschedules. **But note `MaximumAttempts: 1`** (workflow.go:98):
the activity is **not retried** on failure; "orchestrator handles retries internally."
So a mid-activity pod crash **fails the run** rather than resuming it. This is a
deliberate current choice, but it is a resilience gap: a pod crash should not lose a
long run. Two mitigations, in order of effort:
   - **Stream state is safe regardless:** events already written to Redis persist;
     the client re-attaches and sees whatever was produced, then the terminal
     `error`/`failed`. No stream corruption.
   - **To actually survive a crash**, the workflow must be made *replayable*: raise
     `MaximumAttempts` and make the orchestrator loop **idempotent per iteration**
     (checkpoint each turn to `task_messages`; on activity retry, resume from the last
     checkpointed turn rather than re-emitting completed tokens). This is the biggest
     piece of "done right" and is out of scope for the MVP. Until then, document that a
     worker crash fails the in-flight run.
   - **Duplicate-token risk on naive retry:** if `MaximumAttempts` is raised *without*
     idempotent checkpointing, a retry replays the whole activity and **re-XADDs
     tokens already streamed**, so a re-attached client sees duplicated output. Do not
     raise attempts until the loop resumes from a checkpoint.

**5. Run exceeds budget / max_iterations while client is disconnected.**
The orchestrator enforces budget/iteration caps inside the activity; it emits a
terminal event (`done` with a truncation note, or `error`) to the stream and the
workflow returns. The disconnected client, on reconnect, gets the terminal event
(live if in-progress, or from DB if trimmed). No transport involvement needed — this
already works because enforcement is server-side.

**6. (Implicit) `last_event_id` from a *different* run.**
A stale client could send a `last_event_id` belonging to a previous run on the same
context. Because the stream key is per-`runID`, replaying an old cursor against the
new run's stream is a numeric ID comparison against a different stream — it will
usually look "trimmed" and emit `replay_unavailable`, which is acceptable, but the
reconnect logic should **reset `last_event_id` to `0-0` whenever it resolves a
different `runID` than the client last saw.** The client should also send the `runID`
it last knew so the server can detect the mismatch.

---

## 7. Minimum viable first step vs. "done right"

### MVP — make runs survive disconnect (small, safe, high-value)

The workflow **already** survives disconnect (§1). The MVP is about **not creating a
duplicate run on reconnect** and **serving completion from the DB**. Concretely:

1. **Make Start use a server context** for the ExecuteWorkflow RPC and confirm no
   code path wires WS-close → CancelWorkflow (lifecycle.go:326–345). Fix SSE's
   `wfRun.Get(ctx…)` → `context.Background()` (sse/handler.go:293). *(≈15 lines.)*
2. **Reconnect routing:** when a client connects with a non-empty `context_id` (and
   empty `content`, or `content` but a run is already open on that context):
   - Look up latest run + status for `(tenantID, context_id)` in the DB.
   - **Terminal** → send `done` with `final_text` from DB; do not Start, do not open a
     live stream.
   - **Running/input_required** → resolve `runID`, `StreamFromRedis(runID,
     last_event_id)`, do not Start.
   - **No prior run / new turn on idle context** → existing `Admit`/`Start` path.
3. **Reject concurrent new-turn** on a context whose workflow is still open (map
   Temporal "already started" / DB `running` → a clean "response in progress" frame).
4. **Persist `final_text`** on the `runs` row at workflow completion so §5 works even
   after stream trim. *(Schema + recorder change.)*

This gives: laptop-sleep / tab-switch / network-blip all resume correctly, no
duplicate runs, correct completion after long disconnects — **without** touching the
worker's execution model.

### Done right — full target

- **Idempotent, resumable orchestrator loop** with per-iteration checkpointing to
  `task_messages`, `MaximumAttempts > 1`, and dedup so activity retry after a pod
  crash resumes rather than restarts (fixes §6 case 4 fully).
- **`context_id → runID` Redis pointer** with TTL for fast reconnect routing, DB as
  fallback/authority.
- **Explicit user cancel** frame (`{"type":"cancel"}`) → `Signaler` cancel, cleanly
  separated from transport drop.
- **Stream retention policy** tuned per expected run length (MAXLEN and/or a
  time-based `MINID` trim), with the DB as the guaranteed floor for final results.
- **Reconnect-aware metrics:** reattach count, replay-gap size, replay_unavailable
  rate, orphaned-run reconciliation (the reconciler already needs to scan `admitted`
  rows — extend it to detect `running` rows whose workflow has closed).
- **Multi-viewer support** documented and tested (two tabs, one run).

---

## 8. Invariants that MUST NOT change

1. **Tenant isolation.** `TenantID`/`ApplicationID` come exclusively from
   server-resolved `EPConfig`, never from client data (lifecycle.go:334–338,
   activities.go:56–76). Reconnect routing **must** scope every `context_id → runID`
   lookup by `tenant_id` — a client must never re-attach to a run in another tenant by
   guessing a `context_id`. This is the single most important security invariant for
   this feature.
2. **Identity never from the wire.** `runID`, `sessionID`, `contextID` are generated
   server-side; only `context_id` is client-supplied and it must be **validated
   against the caller's tenant** before use as a lookup key. Do not trust a
   client-supplied `runID` for stream access without confirming it belongs to the
   caller's tenant/context.
3. **Gate reservation pattern.** Check (10s reservation) → Register → Confirm (90s) →
   Rollback-on-failure, with shadow-TTL ghost pruning (gate.go, session.go). Reconnect
   goes through the **full gate** for its new viewer connection. Do not add bypasses.
4. **Session caps count connections.** `EPMaxConcurrent`/`AppMaxConcurrent` semantics
   stay "concurrent live connections," enforced via the shadow-TTL Lua. A surviving
   run with no viewer holds no slot.
5. **Bootstrap ordering.** Subscribe to the run-stream **before** `Start`
   (handler.go:283 before :314) so no early event is missed. Reconnect preserves this:
   open `StreamFromRedis` before any Start on a new turn.
6. **Bounded cleanup context.** `Release` derives its own `context.Background()`-based
   5s context (lifecycle.go:415) because the request context is already dead. Keep
   this — it is what makes cleanup reliable on disconnect.
7. **Orphan-run prevention.** `runCreated && !startedOK → mark Failed`
   (lifecycle.go:426). Keep, and extend the reconciler for the new `running`-but-closed
   case.
8. **HITL signal path.** `submit_human_response` via workflow ID `ctx-{contextID}` is
   already disconnect-safe (workflow.go:118). Do not couple it to transport.
9. **At-least-once stream semantics.** The Redis Stream is the durable event log;
   XRANGE/XREAD fan-out (not consumer groups). Keep the continuous-cursor replay and
   `replay_unavailable` trim handling exactly as-is (streamer.go).

---

## Appendix — file/line index of the key mechanics

| Mechanism | Location |
|---|---|
| Client `last_event_id` / `context_id` fields | `ws/handler.go:46–51` |
| Client-scoped ctx built from `r.Context()` | `ws/handler.go:280` |
| WS-close → `cancel()` | `ws/handler.go:471` |
| `wfRun.Get` on `context.Background()` (WS — correct) | `ws/handler.go:369` |
| `wfRun.Get(ctx …)` (SSE — transport-coupled, fix) | `sse/handler.go:293` |
| Start passes client `ctx` into ExecuteWorkflow | `execution/lifecycle.go:326,345` |
| Deterministic workflow ID `ctx-{contextID}` | `execution/lifecycle.go:341` |
| Fresh `runID` every connection (reconnect gap) | `execution/lifecycle.go:185` |
| Release frees session/gate on disconnect | `execution/lifecycle.go:409` |
| Gap-free replay (XRANGE → XREAD, continuous cursor) | `runstream/streamer.go:157–235` |
| `replay_unavailable` on MAXLEN trim | `runstream/streamer.go:144–154` |
| Terminal event set | `runstream/streamer.go:55–61` |
| Stream MAXLEN 5000 | `runstream/publisher.go:55` |
| Activity heartbeat 5s / timeout 15s | `temporal/workflow.go:96`, `activities.go:79–90` |
| Activity `MaximumAttempts: 1` (no crash retry) | `temporal/workflow.go:98` |
| HITL signal receive (disconnect-safe) | `temporal/workflow.go:118` |
| `context_id` of run (reverse lookup exists) | `admin/dal/runs.go:108` |
