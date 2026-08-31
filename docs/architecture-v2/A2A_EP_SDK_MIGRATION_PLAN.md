# A2A EP — SDK Migration Plan
# Migrate `internal/a2a/server.go` to use the official `a2aproject/a2a-go/v2` SDK
_Written: 2026-08-31_

---

## Problem

`internal/a2a/server.go` exposes applications and orchestrators as A2A agents via `/a2a/{app_slug}/{ep_slug}`. It was written by hand and emits a non-compliant wire format:

| What we emit today | What A2A v1.0 requires |
|---|---|
| `{"method":"stream/event","params":{"event":{"kind":"message-delta",...}}}` | `{"result":{"message":{...}}}` SSE frame |
| `{"method":"stream/event","params":{"event":{"kind":"artifact-update",...}}}` | `{"result":{"artifactUpdate":{...}}}` SSE frame |
| `{"method":"stream/event","params":{"event":{"kind":"task-status-update",...}}}` | `{"result":{"statusUpdate":{...}}}` SSE frame |
| `{"result":{"taskId":...,"status":{"state":"completed"},"artifacts":[...]}}` | Same shape — this one is actually correct |

The SDK (`github.com/a2aproject/a2a-go/v2`) is already in `go.mod` and used correctly in `cmd/agent-runtime/`. It was simply never applied to the EP-facing server.

---

## What Does NOT Change

Everything in the admission pipeline is correct and stays untouched:

- `execution.Lifecycle.Admit` — auth, EPConfig lookup, access control, gate, session, run creation
- `execution.Lifecycle.Start` — Temporal workflow dispatch
- `execution.Lifecycle.Release` — gate release, session cleanup
- `runstream.StreamFromRedis` — cross-process event delivery from the worker
- `internal/a2a/pgx.go` — `CardLoader` / `PgxCardLoader` — agent card DB query
- All tests that verify auth/gate/session/Temporal behavior

---

## What Changes

**Only the HTTP handler and event-serialization layer in `server.go`:**

### 1. Method dispatch — replace hand-rolled JSON-RPC router

```go
// REMOVE: manual decode + switch on req.Method
switch req.Method {
case "message/send":   s.handleMessageSend(...)
case "message/stream": s.handleMessageStream(...)
default:               writeRPCError(...)
}

// REPLACE WITH:
a2asrv.NewJSONRPCHandler(handler).ServeHTTP(w, r)
// SDK dispatches message/send, message/stream, tasks/get, tasks/cancel,
// tasks/resubscribe automatically — all correct A2A v1.0 wire format.
```

### 2. Streaming event serialization — replace hand-rolled SSE loop

```go
// REMOVE: hand-rolled bus drain loop
switch ev.Type {
case "token": writeSSE(streamEvent{Kind: "message-delta", ...})
case "file":  writeSSE(streamEvent{Kind: "artifact-update", ...})
case "done":  sendStatus("completed")
}

// REPLACE WITH: SDK AgentExecutorFunc that yields a2a.Event
executor := a2asrv.AgentExecutorFunc(func(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
    return func(yield func(a2a.Event, error) bool) {
        // yield a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil)
        // drain bus: token → yield message part, file → yield artifact, done → yield completed
        yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
    }
})
```

### 3. Agent card — replace hand-rolled JSON with SDK handler

```go
// REMOVE: hand-rolled writeJSON(w, ..., agentCard{...})

// REPLACE WITH:
card := buildSDKAgentCard(row)  // same pattern as agent-runtime
a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
```

### 4. message/send result — already correct shape, minor cleanup only

The `rpcResult` struct we return from `handleMessageSend` matches the A2A v1.0 task object shape. The SDK's `SendMessage` return type (`a2a.SendMessageResult`) will replace it but the data is the same.

---

## New File Structure

`internal/a2a/` after migration:

| File | Role | Status |
|---|---|---|
| `server.go` | Server struct, `NewServer`, `Routes`, admission pipeline, SDK executor wiring | Rewritten (~300 lines, down from 761) |
| `executor.go` | `orchExecutor` — the `AgentExecutorFunc` implementation that bridges the run-stream bus to `iter.Seq2[a2a.Event, error]` | New file |
| `card.go` | `buildSDKAgentCard` — constructs `*a2a.AgentCard` from DB row or fallback | New file (extracted from server.go) |
| `pgx.go` | `CardLoader` / `PgxCardLoader` | Unchanged |

Total: ~500 lines across 4 files. Currently 761 lines in one file.

---

## The Executor Bridge — Key Design

The SDK calls our `AgentExecutorFunc` once per `message/send` or `message/stream` request. Inside it we run the full admission + Temporal pipeline and drain the run-stream bus, converting each bus event to an SDK `a2a.Event`:

```
Bus event type → SDK a2a.Event
─────────────────────────────────────────────────────
"token"   → a2a.MessageDeltaEvent  (text part)
"file"    → a2a.ArtifactUpdateEvent (file part with URL)
"done"    → a2a.StatusUpdateEvent  (TaskStateCompleted)
"error"   → a2a.StatusUpdateEvent  (TaskStateFailed)
```

The SDK then serializes these to the correct A2A v1.0 SSE frames — no manual JSON marshalling.

**File artifact wire format after migration (A2A v1.0 compliant):**

```json
{
  "result": {
    "artifactUpdate": {
      "taskId": "run-uuid",
      "contextId": "ctx-uuid",
      "artifact": {
        "artifactId": "art-uuid",
        "parts": [{ "file": { "uri": "https://…/files/report.pdf", "mimeType": "application/pdf", "name": "report.pdf" } }]
      },
      "append": false,
      "lastChunk": true
    }
  }
}
```

**Token streaming wire format after migration:**

```json
{
  "result": {
    "message": {
      "role": "agent",
      "parts": [{ "text": "hello " }]
    }
  }
}
```

**Status update wire format after migration:**

```json
{
  "result": {
    "statusUpdate": {
      "taskId": "run-uuid",
      "contextId": "ctx-uuid",
      "status": { "state": "completed" },
      "final": true
    }
  }
}
```

---

## Task Store

The SDK requires a `taskstore.TaskStore` to persist task state across reconnects (`tasks/get`, `tasks/resubscribe`). `agent-runtime` uses `agentgen.NewRedisA2ATaskStore`.

For the EP server we have two options:

| Option | Trade-off |
|---|---|
| **A. Use same `RedisA2ATaskStore`** | Zero new code — reuse `agentgen` store. Couples `internal/a2a` to `internal/agentgen`. |
| **B. New thin `a2a.RedisTaskStore` wrapper** | Clean package boundary. ~50 lines. |

**Recommendation: Option A for now.** The coupling is shallow (one interface, one Redis key prefix). We can extract later if agentgen grows too large.

---

## Agent Card Migration

Current card serving uses our `agentCard` struct (hand-rolled JSON). After migration:

1. `buildSDKAgentCard(row EPCardRow) *a2a.AgentCard` — constructs an SDK card from the DB row, injecting `SupportedInterfaces` with the EP's public URL.
2. `a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)` — serves it with correct headers and JSON shape.

The synthesized card stored in `entry_points.agent_card` is already JSON — it gets unmarshalled into `*a2a.AgentCard` and served by the SDK handler.

---

## Tests

### Tests that stay unchanged (auth/gate/session/Temporal behavior)
All existing `TestA2A_*` and `TestA2AStream_*` tests cover admission and Temporal dispatch. They test behavior, not wire format — they will pass after the migration with minor fixture updates (SDK test server instead of our hand-rolled one).

### New tests to add
| Test | What it proves |
|---|---|
| `TestA2AStream_ArtifactUpdateIsSpecCompliant` | `file` bus event → `artifactUpdate` SSE frame with `artifact.parts[0].file.uri` |
| `TestA2AStream_TokenIsSpecCompliant` | `token` bus event → `message` SSE frame with `parts[0].text` |
| `TestA2AStream_StatusUpdateIsSpecCompliant` | `done` bus event → `statusUpdate` SSE frame with `state: "completed"` |
| `TestA2ASend_ResultIsSpecCompliant` | `message/send` result matches A2A v1.0 task object shape |

---

## Migration Steps

**Step 1 — Add SDK imports to `internal/a2a/server.go`**
Import `github.com/a2aproject/a2a-go/v2/a2a` and `github.com/a2aproject/a2a-go/v2/a2asrv`. No logic change yet.

**Step 2 — Create `executor.go`**
Implement `orchExecutor` as an `a2asrv.AgentExecutorFunc`. It contains the admission pipeline + bus drain loop, yielding `a2a.Event` instead of writing SSE directly. This is the largest single change.

**Step 3 — Create `card.go`**
Extract `buildSDKAgentCard` and `handleAgentCard` logic. Replace hand-rolled JSON with `a2asrv.NewStaticAgentCardHandler`.

**Step 4 — Rewrite `server.go` handler**
Replace `handleRPC` / `handleMessageSend` / `handleMessageStream` with a single `handle` method that:
1. Resolves the EP slug from the URL
2. Constructs the `orchExecutor` for this request
3. Calls `a2asrv.NewJSONRPCHandler(handler).ServeHTTP(w, r)`

Remove all hand-rolled JSON-RPC types (`rpcRequest`, `rpcResult`, `streamEvent`, `streamEventBody`, etc.) — the SDK owns these now.

**Step 5 — Update tests**
Update `server_test.go` to use SDK test infrastructure. Add the 4 wire-format compliance tests.

**Step 6 — Build + full test suite + deploy**

---

## What We Keep From Current `server.go`

| Kept | Why |
|---|---|
| `Server` struct fields: `lc`, `bus`, `authenticator`, `instanceID`, `logger`, `runStreamer`, `publicURL`, `cardLoader` | All still needed |
| `NewServer`, `WithRunStreamer`, `WithPublicURL`, `WithCardLoader` | API unchanged |
| `Routes()` — route paths unchanged | `/a2a/{app_slug}/{ep_slug}` stays |
| `extractRawToken` | Still needed in the executor |
| `mapAdmitError` | Still needed — maps `*execution.AdmitError` to HTTP/JSON-RPC errors |
| `pgx.go` entirely | No change |

## What Gets Deleted From Current `server.go`

| Deleted | Replaced by |
|---|---|
| `rpcRequest`, `rpcResult`, `rpcStatus`, `rpcArtifact`, `rpcTextPart`, `rpcFilePart` | SDK types |
| `streamEvent`, `streamEventParam`, `streamEventBody`, `rpcStreamStatus` | SDK SSE serialization |
| `rpcIncomingPart`, `messageSendParams` | SDK request parsing |
| `agentCard`, `agentCardCapability` | `a2a.AgentCard` |
| `handleRPC`, `handleMessageSend`, `handleMessageStream` | `orchExecutor` + `a2asrv.NewJSONRPCHandler` |
| `writeRPCResult`, `writeRPCError`, `writeHTTPError`, `writeJSON` | SDK error handling (keep `mapAdmitError`) |
| `codeParseError`, `codeMethodNotFound`, `codeInternalError` | SDK constants |

---

## Risk & Rollback

**Risk:** The SDK's `AgentExecutorFunc` is a pull-based iterator (`iter.Seq2`) — the SDK calls `yield` for each event. Our bus drain is push-based (Go channel). Bridging these requires a goroutine that reads from the channel and calls `yield`. This is the only non-trivial pattern change — same as how `agent-runtime` does it in `executeSkill`.

**Rollback:** Git revert. The old `server.go` is fully self-contained. The route paths and Lifecycle wiring are unchanged, so no downstream config changes are needed.

---

## Summary

| | Before | After |
|---|---|---|
| Wire format | Non-compliant (invented `kind` discriminator) | 100% A2A v1.0 |
| Lines of code | 761 in one file | ~500 across 4 focused files |
| SDK usage | None in `internal/a2a/` | `a2asrv.NewJSONRPCHandler` + `a2a.Event` types |
| Methods supported | `message/send`, `message/stream` | + `tasks/get`, `tasks/cancel`, `tasks/resubscribe` (SDK handles these for free) |
| File artifacts | Custom `kind: "artifact-update"` + `url` field | `artifactUpdate.artifact.parts[].file.uri` — spec compliant |
| Token streaming | Custom `kind: "message-delta"` | `message.parts[].text` — spec compliant |
