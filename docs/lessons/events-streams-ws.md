---
title: Lessons — Events, Streams & WebSockets
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-07-11 — Trace tab empty: events on wrong Redis channel

**Symptom:** Playground trace tab shows no events even though runs complete successfully.

**Root cause:** Two Redis channels exist for each run:
- `them:dash:run:{run_id}` — dashboard pub/sub channel; `ws_dashboard` multiplexes to `run:{run_id}` channel subscribers
- `them:dash:run:{run_id}:tokens` — streaming side-channel; bridge's `stream_run_events` reads this and forwards to the WS client directly

`tool_start` and `tool_done` events were only published to `:tokens`. The dashboard WS (which feeds the trace tab) subscribes to the plain channel — so trace received nothing except `usage`/`run_start`/`run_end`.

**Fix:** Publish `tool_start`, `tool_done`, and `iteration_start` to BOTH channels: use `_publish_dash()` (which fans out to the plain channel) AND separately publish to `:tokens`.

**Watch for:** Any new event type added in activities must be published to both channels if it should appear in both the trace tab AND the streaming client view. `:tokens` = real-time stream to the connected WS client; plain channel = observable audit log for dashboard/trace.

---

---

## 2026-07-14 — Ready-event race: subscribe to Redis BEFORE starting the workflow

**Symptom:** WS client hangs for 15 seconds then receives "Timed out waiting for workflow ready event", even though the workflow completed successfully.

**Root cause:** `stream_run_events` subscribed to the Redis context channel AFTER calling `start_workflow`. Fast workflows (echo agents) published their `ready` event before the subscriber was registered — the event was missed, the 15s timeout fired.

**Fix:** In `start_orchestration_workflow`, subscribe to the context channel BEFORE calling `client.start_workflow()`. Return the pre-subscribed `pubsub` object to the caller, who passes it to `stream_run_events(..., pubsub=pubsub)`. `stream_run_events` skips its own subscribe when a pre-subscribed pubsub is passed in.

**Watch for:** Any new code path that starts a Temporal workflow and then tries to listen for its Redis events must use the pre-subscribe pattern. Subscribe first, start second — never the reverse.

---

---

## 2026-07-14 — Poisoned context_id: dead workflow silently swallows all future messages

**Symptom:** User sends a message, gets an error. Subsequent messages on the same context are also silent — no response, no error.

**Root cause:** On `WorkflowAlreadyStartedError`, the old code re-attached to the existing workflow handle without checking if it was already closed. A closed workflow streams nothing and returns immediately — every subsequent message attached to the same dead workflow ID and silently completed with no output.

**Fix:** On `WorkflowAlreadyStartedError`, call `handle.describe()` and check the status. If the workflow is in a closed state (`COMPLETED`, `FAILED`, `CANCELED`, `TERMINATED`, `TIMED_OUT`), raise `DeadContextError`. The router catches it and emits `{"type": "error", "context_id": null}` — the `null` context_id is the protocol signal for the client to clear its stored context_id and start fresh.

**Watch for:** Any new entry point (WS, SSE, REST) that calls `start_orchestration_workflow` must catch `DeadContextError` and emit `context_id: null` to the client. Without this signal, the client's localStorage retains the dead context_id and every future session on that tab is poisoned.

---

---

## 2026-07-16 — Live notifications that survive page navigation: Hash + Pub/Sub pattern

**Symptom:** Scan progress badge stuck on "Scanning…" after navigating away and back. Pub/Sub events fired while WS was closed are gone forever.

**Root cause:** Redis Pub/Sub is fire-and-forget. No replay. If the browser is not subscribed at the exact moment an event fires, it is lost permanently.

**The correct pattern — Hash (snapshot) + Pub/Sub (live):**

```
On every state update (backend):
  1. HSET them:<feature>:state:<id>  field value  ← update snapshot first
  2. PUBLISH them:dash:<channel>  {payload}        ← then publish live

On WS subscribe (server):
  1. Subscribe to Pub/Sub first                    ← no gap, events buffered
  2. HGETALL them:<feature>:state:<id>             ← read current snapshot
  3. Send snapshot to browser as one message       ← browser renders current state
  4. Forward live Pub/Sub events as they arrive    ← live updates from here

On scan complete:
  DEL them:<feature>:state:<id>  (or short TTL)
```

**Why subscribe to Pub/Sub BEFORE reading the Hash:**
If you read the Hash first, then subscribe — any event that fires in that gap is missed. Subscribe first means Pub/Sub is already buffering before you read the snapshot. No gap.

**Why update Hash BEFORE publishing:**
Pub/Sub is faster than a Hash read. If you publish first, a reconnecting client might read a stale Hash. Hash first guarantees snapshot is never behind live events.

**Frontend:**
- Handle a `snapshot` event type that sets state directly (not incremental)
- Use a version/sequence number so a delayed Pub/Sub event cannot overwrite newer state

**Apply this pattern anywhere you need:**
- Live progress that survives page navigation
- Any feature where the browser might connect after events already fired
- Any "current state" that a new subscriber needs immediately

**What NOT to do:**
- Do not use Pub/Sub alone for anything that needs replay
- Do not use Redis Streams for live UIs — they replay ALL history, causing stale messages to flash on screen
- Do not poll the DB for N items on reconnect — use the Hash snapshot via the existing WS subscribe flow
- Do not add complex reconnect logic on the frontend — the server handles catch-up, frontend just reconnects normally

**Watch for:** Any new real-time feature (run progress, agent status, task updates) — always ask: "can the user navigate away while this is running?" If yes, use Hash + Pub/Sub.

---

---

## `/ws/dashboard`'s `run:*` channels never delivered live events, only a one-shot snapshot (found 2026-09-23)

While building App Canvas Debug Mode Phase 5's frontend (`docs/_archive/APP_CANVAS_DEBUG_PLAN.md`), a real
debug run against the live stack consistently produced `{"type":"subscribed",...}` and then
**nothing** over `/ws/dashboard` for a `run:{id}` channel — despite the run completing successfully
and its Redis Stream (`them:dash:run:{id}:stream`) containing the full, correct
`node_start`/`node_done`/`done` sequence, confirmed via direct `XRANGE`.

**Why:** `internal/runstream.PublishEvent` — called by every AppFlow node's `emitTrace`, the single
choke point all trace events flow through — only ever calls `XAdd` (a Redis Stream write). Nothing
in this codebase ever `PUBLISH`es these events to a pub/sub channel. `/ws/dashboard`'s `run:*`
handling (`sendRunSnapshot`) was a **one-shot** `XRevRange` taken exactly once, at subscribe time —
there was no live delivery mechanism for `run:*` at all, only ever a snapshot of whatever already
existed in the Stream the instant you subscribed. Any run fast enough to finish before that single
Redis round-trip landed — every mock-LLM AppFlow debug run tested this session completed in well
under 1 second end-to-end — delivered **zero** events to the client, live or otherwise.

**Why it wasn't caught earlier:** the real live-tailing primitive
(`internal/runstream.StreamFromRedis`, replay-then-`XREAD BLOCK`) has existed since Phase 11c-B and
is used correctly by the production `internal/ws`/`internal/sse` routes — so anyone reading just
those two packages would reasonably assume the pattern was applied everywhere trace events need to
reach a browser. `/ws/dashboard` is a separate, generic pub/sub multiplexer (`runs`, `agent:*`,
`sessions:*`, `scan:*`, `run:*`) built for channels that mostly *are* genuinely published to
(`dashboard.SessionPublisher` does real `PUBLISH` calls for session lifecycle events) — `run:*` was
the one channel type that looked like it fit the same pattern but never actually had a publisher.
The playground's own `run:*` trace pane (`useChatConnection.ts`'s `openDashWs`) has had this exact
same latent gap the whole time; it went unnoticed there because that pane is secondary (the primary
chat response streams over a different, correctly-wired WS) and orchestrator-mode runs are usually
slow enough that a human clicking around happens to subscribe after some entries already exist by
chance.

**Fix (`go/internal/dashboard/handler.go`):** `run:*` channels are no longer handed to the generic
pub/sub `Subscribe` call. Each is tailed by a new `tailRunChannel` method using
`internal/runstream.StreamFromRedis` directly — the exact same primitive `internal/ws`/`internal/sse`
already use — so a `run:*` subscriber gets full history-so-far plus every event published after
subscribing, with no gap. `Handler` gained a `streamer runstream.RedisStreamer` field (threaded from
`cmd/them/main.go`'s existing `rsStreamer`, already built for the WS/SSE handlers — no new Redis
client instance needed), nil-safe so old tests constructing a `Handler` without one still exercise
the legacy one-shot path unchanged.

**How this was found without a browser:** no browser-automation tool was available in this
environment (confirmed: Playwright's Chromium downloaded, but its shared-library dependencies
couldn't be installed without interactive `sudo`). Root-caused instead via a Node script run inside
the already-running `them-frontend` container (same Docker network as `them-go-bridge`/
`them-auth-go`) that logged in, created a real throwaway app + draft + entry point, called
`debug/start`, and drove the exact `/ws/dashboard` subscribe flow the new frontend hook uses —
combined with a temporary `slog.Warn` dropped into `sendRunSnapshot` (rebuilt, restarted, observed,
then reverted before the real fix) to prove the snapshot code path was reached with `n_entries: 0`
at exactly the right moment, ruling out a channel-name or tenant-scoping bug before concluding the
delivery mechanism itself was missing.

**Watch for:** any new per-run or per-entity event type that needs to reach a browser live. Check
whether the write side actually `PUBLISH`es to the channel a WS/SSE consumer subscribes to — an
`XADD`-only write (or any Stream-only write) will pass every "does the data exist in Redis" check
while still delivering nothing live to a plain pub/sub subscriber. If a channel needs both replay
and live delivery, reuse `internal/runstream.StreamFromRedis` rather than a bespoke snapshot.

---

## Frontend was silently dropping every real WS event by gating on the wrong field (found 2026-09-23)

`useAppFlowDebugSession.ts`'s `ws.onmessage` handler checked `msg.type` first (`if (!type ||
type === 'ping' || type === 'subscribed') return;`) before ever looking at `msg.event`. But
`/ws/dashboard`'s actual wire shape for every real trace/data event is `{"channel": "...",
"event": {"type": "node_start", ...}}` — the *outer* envelope has **no top-level `type` field at
all**. Only the three WS-protocol control messages (`ping`, `{"type":"error",...}` for a malformed
subscribe, `{"type":"subscribed",...}` for the ack) have one. Since `msg.type` was `undefined` for
every real event, the original `if (!type ...) return;` matched and silently discarded every single
event this hook was built to consume — the entire Phase 5 debug panel had never actually displayed
a live node state in a real browser, from the session it first shipped in through to this fix.

**Why it wasn't caught earlier:** the bug was introduced and shipped in the same session that first
built this hook, and that session's own live verification used a hand-written Node script that
checked `msg.event?.type` directly — bypassing the exact buggy branch — so the verification
"proved" the backend contract worked while never actually exercising the frontend code as written.
The gap between "I wrote a script that proves the wire protocol works" and "I proved the actual
shipped frontend code parses that wire protocol correctly" is where this hid.

**Fix:** check `msg.type === 'ping' || msg.type === 'subscribed'` for the control messages, handle
`msg.type === 'error' && !msg.event` for the WS-protocol error case explicitly, then unconditionally
try `msg.event` for everything else — presence of `event`, not `type`, is what distinguishes a real
trace message.

**Watch for:** when writing a "verification script" to test a WS/SSE consumer's wire-format
handling, either drive the actual shipped parsing function directly, or diff the script's own
parsing logic against the consumer's line-by-line before trusting the script's pass/fail as proof
the shipped code works. A script that independently re-implements "the same" parsing logic can
silently diverge from what's actually shipped and prove nothing about it.
