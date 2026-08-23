# A2A Streaming Transport — Full Gap Analysis
_Investigated: 2026-08-23_

---

## 1. A2A v1.0 Streaming Specification

### Two distinct RPC methods

| Method | Transport | Response shape |
|---|---|---|
| `SendMessage` | HTTP POST → JSON body | Complete `Task` object with `task.artifacts[]` |
| `SendStreamingMessage` | HTTP POST → **SSE stream** | Sequence of `StreamResponse` events |
| `SubscribeToTask` | HTTP POST → SSE stream | Subscribe to events on an existing task |

**`SendStreamingMessage`** uses the same `SendMessageRequest` proto as `SendMessage`. The difference is purely in the response: the server returns `Content-Type: text/event-stream` and emits a stream of `StreamResponse` events until the task completes.

### `StreamResponse` event types (from SDK inspection)

```
StreamResponse fields:
  task           — full Task snapshot (terminal event)
  message        — a Message (mid-stream agent output)
  status_update  — TaskStatusUpdateEvent  (state transitions: submitted → working → completed/failed)
  artifact_update — TaskArtifactUpdateEvent (progressive artifact delivery)
```

### `TaskArtifactUpdateEvent` fields

```
task_id, context_id, artifact, append (bool), last_chunk (bool), metadata
```

- `append=false, last_chunk=true` → single-shot artifact (complete in one event)
- `append=true, last_chunk=false` → chunk arriving, more to come
- `append=true, last_chunk=true` → final chunk, artifact is now complete

### How a client knows which to use

The `AgentCard` has `capabilities.streaming = true/false`. A compliant A2A client reads the card, checks this flag, and calls `SendStreamingMessage` if true, `SendMessage` if false.

The-M stores `supports_streaming` per agent in the DB and exposes it in the API — **but never uses it in the invoke path**.

---

## 2. Current the-M Outbound Transport — Exact State

### `invokeA2A` in `go/internal/agentregistry/registry.go`

```go
// Always SendMessage — hardcoded, no streaming path
rpcReq := a2aRequest{
    Method: "SendMessage",
    Params: a2aParams{
        Configuration: a2aConfiguration{ReturnImmediately: false},
    },
}
// Blocks until full response body is available
respBytes, err := io.ReadAll(resp.Body)
```

- HTTP client timeout: **3 minutes** (`httpInvokeTimeout = 3 * time.Minute`)
- No SSE parsing. `io.ReadAll` on an SSE stream would block until the agent closes the connection.
- `supports_streaming` flag is stored in DB but **never read** in the invoke path.

### What happens today for each duration

| Agent duration | Outcome |
|---|---|
| < 3 min | Works. Blocks goroutine until agent responds. |
| > 3 min | HTTP timeout → tool call fails → orchestrator reports error |
| SSE streaming agent | `io.ReadAll` reads the raw SSE text as one blob → JSON parse fails → error |

---

## 3. Current the-M Inbound — Entry Points

### WebSocket (`internal/ws/handler.go`)

Events flow: **Orchestrator → `publishJSON` → event bus → Redis Stream → WS handler → client**

The orchestrator calls `o.publishJSON(ctx, contextID, runID, "file", payload)` inside `emitArtifactEvent` **after** `RecordArtifact` completes. The WS handler subscribes to the run's Redis Stream and forwards events as they arrive.

**Artifact events are already streamed progressively to the client** — the `"file"` event is emitted as soon as each artifact is recorded, before the run completes. The Artifacts tab polls `/api/v1/runs/{id}/artifacts` every 3s, so the user sees artifacts appear within seconds of recording.

### SSE (`internal/sse/handler.go`)

Same event bus / Redis Stream mechanism. Same progressive delivery.

### Event bus

`internal/event/bus.go` is a pub/sub fan-out bus. The orchestrator publishes typed events; WS/SSE handlers subscribe by run ID. This bus is the **correct place to inject streaming agent events** — an agent streaming 5 artifact chunks would each trigger `emitArtifactEvent` → `publishJSON("file", ...)` → WS client sees each artifact progressively.

---

## 4. The Full Gap — Concrete Scenarios

### Scenario A: Large PDF agent (45s to generate)
- **Today**: Works. `invokeA2A` blocks for 45s (within 3-min timeout), returns complete PDF. User sees artifact appear after 45s.
- **With streaming**: Could show "generating..." status updates as the agent works, then deliver the artifact when ready. Better UX, same correctness.

### Scenario B: SSE streaming agent (chunks a report)
- **Today**: `io.ReadAll` reads raw SSE text → JSON unmarshal fails → tool call error. **Broken.**
- **Should**: Open SSE connection, consume `StreamResponse` events, call `emitArtifactEvent` for each `artifact_update` event, publish status updates to bus as `status_update` events arrive.

### Scenario C: Agent returns 3 artifacts (PDF + PNG + CSV)
- **Today**: First file returned, rest silently dropped. *(Separate multi-artifact bug — see `A2A_MULTI_ARTIFACT_PLAN.md`)*
- **With streaming**: Each `TaskArtifactUpdateEvent` triggers one `emitArtifactEvent` → all 3 recorded independently → all 3 appear in Artifacts tab.

### Scenario D: Long-running pipeline with progressive status + artifacts
- **Today**: Entire Temporal activity blocks. User sees nothing for N minutes. If > 3 min → timeout error.
- **Should**: Status events stream to WS client in real time. Artifacts appear as produced. Run stays alive.

### Scenario E: the-M as A2A agent (orch-as-agent, `/a2a/*`)
- **Today**: `internal/a2a/server.go` exposes the `SendMessage` path. No streaming response.
- **Gap**: External orchestrators calling the-M with `SendStreamingMessage` get an error or wrong response. The-M cannot stream back status/artifact events as they occur.

---

## 5. Options

### Option A — Keep `SendMessage`, increase timeout to 10 min
- **Enables**: Nothing new. Fixes Scenario A edge cases.
- **Requires**: One constant change.
- **Compliance**: Non-compliant for streaming agents. Ignores `supports_streaming`.
- **Verdict**: Band-aid. Not acceptable for a platform.

### Option B — Dual path: `SendMessage` or `SendStreamingMessage` based on `supports_streaming`
- **Enables**: Scenarios B, C, D. Streaming agents work correctly.
- **Requires**: Second invoke function `invokeA2AStreaming` that opens SSE, consumes events, calls `emitArtifactEvent` per artifact, publishes status events to bus. `AgentConfig` loads `supports_streaming` from DB. Registry routes by flag.
- **Compliance**: Full A2A v1.0 compliance.
- **Interface change**: `AgentConfig` gains `SupportsStreaming bool`. No change to `AgentInvoker` interface — streaming path settles into same `(json.RawMessage, error)` return after consuming the stream.
- **Temporal concern**: Temporal activities must not block for long-running streams. Solution: activity sends heartbeats (`activity.RecordHeartbeat`) while consuming SSE. Temporal keeps the activity alive as long as heartbeats arrive.

### Option C — Always use `SendStreamingMessage` for all A2A agents
- **Enables**: Same as B.
- **Requires**: All agents must support streaming. Breaks echo, docu_writer (they don't declare `streaming=true`). Must update all agents or add fallback.
- **Verdict**: Premature. Not all agents need to stream.

### Option D — Streaming `AgentInvoker` variant (`InvokeStreaming`)
- **Enables**: Clean separation — callers opt in to streaming.
- **Requires**: Interface change, new Temporal activity variant, orchestrator change to call `InvokeStreaming` for streaming agents.
- **Verdict**: Cleaner long-term but more disruption. Worth considering at the same time as Option B.

---

## 6. Recommended Architecture

**Implement Option B now, with Option D's interface as the long-term target.**

### Implementation order

1. **`AgentConfig`** — add `SupportsStreaming bool`, load from DB `supports_streaming` column (already stored).

2. **`invokeA2AStreaming`** — new function in `registry.go`:
   - Sends `SendStreamingMessage` method
   - Reads SSE line-by-line (`bufio.Scanner`)
   - For each `data:` line: unmarshal `StreamResponse`, dispatch:
     - `status_update` → publish to bus as `"status"` event (no recording needed)
     - `artifact_update` → call a provided callback `func(artifactBody)` per artifact chunk; on `last_chunk=true` → emit full artifact
     - `task` (terminal) → return final result
   - Heartbeats to Temporal on each event

3. **`Registry.InvokeForRun`** — route by `cfg.SupportsStreaming`:
   ```go
   if cfg.SupportsStreaming {
       return r.invokeA2AStreaming(ctx, cfg, input, artifactCallback)
   }
   return r.invokeA2A(ctx, cfg, input)
   ```

4. **`artifactCallback`** threading — the streaming invoke needs to call `emitArtifactEvent` as artifacts arrive. Two clean options:
   - Pass `func(artifactBody)` callback into `invokeA2AStreaming` (clean, no interface change to `AgentInvoker`)
   - Or: make streaming return a channel of `artifactBody` and let `executeTools` drain it

   **Recommendation**: callback. Keeps `InvokeForRun` signature unchanged.

5. **Scenario E (the-M as streaming A2A agent)** — `internal/a2a/server.go` must implement `on_message_send_stream` (SDK handler method). This publishes `TaskStatusUpdateEvent` and `TaskArtifactUpdateEvent` as the orchestrator's event bus emits them. Requires wiring the run's Redis Stream to the SSE response generator.

### How it integrates with existing infrastructure

```
SSE agent
  └─ invokeA2AStreaming (bufio.Scanner over SSE)
       ├─ status_update  → publishJSON(bus, "status", ...)  → WS/SSE client sees it
       └─ artifact_update (last_chunk) → emitArtifactEvent → RecordArtifact → publishJSON(bus, "file", ...) → WS/SSE client sees it
```

The existing event bus, Redis Stream, WS/SSE handlers require **zero changes**. The orchestrator's `emitArtifactEvent` is already designed for this — it was always meant to be called per-artifact.

---

## 7. What's NOT in Scope

- **WebRTC** — different transport entirely, separate planning required
- **Push notifications** — A2A `TaskPushNotificationConfig` (agent pushes to a webhook). Separate feature.
- **Agent-side streaming implementation** — this doc covers the-M as a *client* of streaming agents. Building new streaming agents (updating docu_writer etc.) is separate.
- **`append=true` chunked artifact reassembly** — for now, only `last_chunk=true` triggers recording. Chunked binary streaming deferred.

---

## 8. Recommended Next Step

**Before implementation:** add `SupportsStreaming` to `AgentConfig` and load it from DB. This is a 2-line change, zero risk, and is the prerequisite for everything else. Confirms the flag flows correctly before building the streaming invoke path.

**Then:** implement `invokeA2AStreaming` as a standalone function with a unit test that replays a canned SSE fixture. That test pins the SSE parsing contract before wiring it into the live path.

**Trigger:** implement the full path when the first real streaming agent is connected — either an external agent declaring `streaming=true` in its card, or when docu_writer is updated to stream progress events during the Claude call.
