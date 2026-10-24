# A2A Artifact Forwarding — Wire Format & Protocol Compliance
_Written: 2026-08-31_

---

## Short Answer

**The `artifact-update` event kind is a platform-local extension, not defined by A2A v1.0.**

A2A v1.0 uses proto-JSON field-name discrimination (`artifactUpdate`, `statusUpdate`) — no `kind` field exists in the spec. Our outbound SSE envelope (the-M acting as an A2A agent toward external callers) uses a `kind` discriminator because it mirrors the platform's own internal event format. The two sides of the pipeline are deliberately asymmetric:

| Direction | Format | Defined by |
|---|---|---|
| Inbound: sub-agent → the-M orchestrator | `{"result":{"artifactUpdate":{…}}}` | A2A v1.0 proto-JSON |
| Outbound: the-M → caller via SSE | `{"jsonrpc":"2.0","method":"stream/event","params":{"event":{"kind":"artifact-update",…}}}` | Platform extension |

---

## The Two Artifact Shapes

### 1. Inbound — how sub-agents send artifacts to the orchestrator

Source: `go/internal/agentregistry/registry.go` — `invokeA2AStreaming`

Each SSE line from a streaming sub-agent is a JSON-RPC 2.0 response wrapping a `StreamResponse`:

```json
{
  "result": {
    "artifactUpdate": {
      "artifact": {
        "artifactId": "...",
        "name": "report.pdf",
        "parts": [
          {
            "filename": "report.pdf",
            "raw": "<base64-encoded bytes>",
            "mediaType": "application/pdf"
          }
        ]
      },
      "append": false,
      "lastChunk": true
    }
  }
}
```

Key fields:
- `append` / `lastChunk` — chunked delivery protocol; the platform only acts on `lastChunk: true`
- `parts[].raw` — base64-encoded file content (inline, no URL)
- `parts[].filename` — used as the stored artifact name
- `parts[].mediaType` — MIME type

The orchestrator's `invokeA2AStreaming` calls the `onArtifact(name, contentType, base64)` callback on each complete artifact. The orchestrator then calls `emitArtifactEvent`, which:
1. Persists the artifact to `them.artifacts` in Postgres via `RecordArtifact`
2. Uploads/stores the file and generates a `download_url`
3. Publishes a `"file"` bus event with the DB artifact's metadata

### 2. Bus event — internal pipeline after artifact is persisted

Source: `go/internal/orchestrator/orchestrator.go` — `emitArtifactEvent`

```json
{
  "artifact_id":  "uuid",
  "filename":     "report.pdf",
  "content_type": "application/pdf",
  "size":         12345,
  "run_id":       "uuid",
  "download_url": "https://…/files/report.pdf"
}
```

This is the internal `"file"` event type on the in-process event bus. It carries a **URL** (not inline data) because the artifact is already persisted at this point.

### 3. Outbound SSE — what SSE clients receive

Source: `go/internal/sse/handler.go` — `formatSSE` `case "file":`

```json
{
  "type": "artifact-update",
  "artifact_id": "uuid",
  "filename": "report.pdf",
  "content_type": "application/pdf",
  "url": "https://…/files/report.pdf"
}
```

Sent as `data: <json>\n\n` in the SSE stream. This is the **platform-native** SSE format (not A2A wire format).

### 4. Outbound A2A stream/event — what A2A callers receive

Source: `go/internal/a2a/server.go` — `handleMessageStream` `case "file":`

```json
{
  "jsonrpc": "2.0",
  "method": "stream/event",
  "params": {
    "event": {
      "kind": "artifact-update",
      "parts": [
        {
          "url": "https://…/files/report.pdf",
          "mediaType": "application/pdf",
          "name": "report.pdf"
        }
      ]
    }
  }
}
```

Sent as `data: <json>\n\n` in the SSE stream. This is the **platform extension** A2A streaming format.

**Why `url` and not inline base64?** By the time this event is emitted, the artifact is already persisted and a download URL exists. Sending a URL is more efficient for large files and avoids duplicating binary data in the stream.

---

## Stream Event Kinds — Full Outbound Reference

All event kinds in `stream/event` frames emitted by `internal/a2a/server.go`:

| `kind` | When | Key fields |
|---|---|---|
| `message-delta` | A token arrives from the LLM | `role: "assistant"`, `parts: [{text: "…"}]` |
| `artifact-update` | A sub-agent artifact is ready | `parts: [{url, mediaType, name}]` |
| `task-status-update` | Run completes or fails | `taskId`, `status.state: "completed"/"failed"` |

None of these are A2A v1.0 spec kinds. They are platform extensions that mirror the internal event bus types in an A2A-compatible SSE envelope.

---

## Protocol Compliance Assessment

### Where we are compliant with A2A v1.0

- JSON-RPC 2.0 transport (`jsonrpc: "2.0"`, `method`, `params`, `id`)
- Agent card at `/.well-known/agent.json` with `capabilities.streaming: true`
- `Content-Type: text/event-stream` for streaming responses
- `A2A-Version: 1.0` header sent on outbound sub-agent calls
- SSE framing: `data: <json>\n\n`
- Inbound `StreamResponse` parsing matches A2A v1.0 proto-JSON field names

### Where we diverge (platform extensions)

- **Outbound `kind` field**: A2A v1.0 uses proto field presence as the discriminator, not a `kind` string. Our `kind: "artifact-update"` is a platform extension.
- **File part shape (outbound)**: A2A v1.0 file parts use inline data. We use a `url` reference because artifacts are pre-persisted. An external A2A client expecting the spec's `DataPart` with inline bytes will not find them.
- **`message-delta` kind**: Not defined in A2A v1.0 (which uses `message` field on `StreamResponse`). Platform extension.
- **No `taskId` on `artifact-update`**: The platform does not populate `taskId` on artifact-update events, making it impossible for the caller to correlate which task produced the artifact if multiple tasks are running concurrently.

### Practical consequence

An external A2A-compliant client calling the-M's `/a2a/{app}/{ep}` streaming endpoint will:
- Receive valid JSON-RPC 2.0 frames ✓
- See events with an unfamiliar `kind` field (tolerable — spec says ignore unknown fields) ✓
- Receive file references via URL (acceptable if the client follows the URL) ✓
- Not receive inline binary data in spec-compliant `DataPart` shape ✗

For internal consumers (the-M playground, frontend SSE listeners) this is fully sufficient.

---

## Data Flow Diagram

```
Sub-agent (A2A v1.0)
  └─ StreamResponse { artifactUpdate: { artifact: { parts[{ raw: base64, filename }] } } }
       │
       ▼ invokeA2AStreaming (registry.go)
       │   onArtifact(name, contentType, base64)
       │
       ▼ orchestrator.emitArtifactEvent (orchestrator.go)
       │   1. RecordArtifact → them.artifacts (Postgres)
       │   2. publishJSON("file", { artifact_id, filename, content_type, download_url })
       │
       ├─► SSE client (sse/handler.go formatSSE case "file")
       │     data: {"type":"artifact-update","filename":"...","url":"...","content_type":"..."}
       │
       └─► A2A streaming caller (a2a/server.go handleMessageStream case "file")
             data: {"jsonrpc":"2.0","method":"stream/event",
                    "params":{"event":{"kind":"artifact-update",
                                       "parts":[{"url":"...","mediaType":"...","name":"..."}]}}}
```

---

## Files Involved

| File | Role |
|---|---|
| `go/internal/agentregistry/registry.go` | Parses inbound A2A v1.0 `artifactUpdate` events; calls `onArtifact` callback |
| `go/internal/orchestrator/orchestrator.go` | `emitArtifactEvent` — persists artifact, publishes `"file"` bus event |
| `go/internal/sse/handler.go` | `formatSSE case "file"` — converts bus event to platform SSE frame |
| `go/internal/a2a/server.go` | `handleMessageStream case "file"` — converts bus event to A2A stream/event frame |
| `go/internal/sse/handler_test.go` | `TestSSEFileEventForwardedAsArtifactUpdate` |
| `go/internal/a2a/server_test.go` | `TestA2AStream_FileEventForwardedAsArtifactUpdate` |

---

## Future Alignment with A2A v1.0

If we need to be fully spec-compliant on the outbound side:

1. Replace `kind` discriminator with proto field presence — emit `"artifactUpdate"` as a field name instead of `kind: "artifact-update"`
2. Add `taskId` population on all streaming events
3. For file parts: either serve inline base64 (impractical for large files) or adopt the A2A v1.0 `FileContent.uri` field (`{"file":{"uri":"…","mimeType":"…"}}`) which is URL-based and spec-correct

The inbound parsing path (`invokeA2AStreaming`) already matches A2A v1.0 exactly and requires no changes.
