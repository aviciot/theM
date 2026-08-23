# A2A Multi-Artifact Analysis
_Investigated: 2026-08-23_

## What the A2A v1.0 spec supports

`Task.artifacts` is a **repeated** field — a single task response can legally contain any number of `Artifact` objects. Each `Artifact` can itself contain multiple `Part` objects (text, raw bytes, structured data).

`TaskArtifactUpdateEvent` (streaming path) carries **one artifact at a time** with `append` (bool) and `last_chunk` (bool) flags for chunked delivery. The streaming path is explicitly designed for progressive artifact emission during execution.

**Conclusion: the spec fully supports multiple artifacts per task, both in non-streaming (task.artifacts[]) and streaming (one event per artifact) modes.**

## Current gap — exactly what breaks

In `go/internal/agentregistry/registry.go`, `extractA2AResult()` (line ~421):

```go
for _, artifact := range artifacts {
    for _, part := range artifact.Parts {
        if part.Filename == "" { continue }
        // ... build payload ...
        return out, nil   // ← returns on FIRST file found, drops the rest
    }
}
```

If an agent returns `[report.pdf, diagram.png, data.csv]`, only `report.pdf` is returned. The other two are silently dropped. No log, no error.

In `go/internal/orchestrator/orchestrator.go`, `executeTools()` (line ~693):

```go
var ap artifactPayload   // holds ONE artifact
json.Unmarshal(out, &ap)
if ap.Artifact != nil {
    o.emitArtifactEvent(...)   // records ONE artifact
}
```

The single-artifact JSON shape `{"artifact": {...}}` is baked into both the extractor and the orchestrator handler.

## Proposed elegant solution — minimal, no interface change

**Key insight:** avoid changing `AgentInvoker.Invoke` signature. Instead change the internal JSON shape from singular to plural, handled entirely within `extractA2AResult` and `executeTools`.

**Step 1 — `extractA2AResult` returns all artifacts:**

Change the internal result shape from `{"artifact": {...}}` to `{"artifacts": [{...}, {...}]}`.
Collect all file parts across all artifacts into a slice and return them in one JSON blob.

```go
// collect all file artifacts
var bodies []artifactBody
for _, artifact := range artifacts {
    for _, part := range artifact.Parts {
        // ... same encoding logic ...
        bodies = append(bodies, artifactBody{...})
    }
}
if len(bodies) > 0 {
    filenames := // join names
    out, _ := json.Marshal(map[string]any{
        "output":    fmt.Sprintf("%d file artifact(s): %s", len(bodies), filenames),
        "artifacts": bodies,   // plural key
    })
    return out, nil
}
```

**Step 2 — `executeTools` fans out recording:**

```go
// handle both singular (legacy) and plural (new) artifact keys
var aps struct {
    Artifact  *artifactBody   `json:"artifact"`
    Artifacts []artifactBody  `json:"artifacts"`
}
json.Unmarshal(out, &aps)
all := aps.Artifacts
if aps.Artifact != nil { all = append(all, *aps.Artifact) }
for _, body := range all {
    o.emitArtifactEvent(ctx, contextID, runID, rctx, &body)
}
// strip both keys before sending to LLM
```

This is backward-compatible: existing single-artifact agents still work via the `artifact` key fallback.

**LLM sees:** `"2 file artifacts: report.pdf, diagram.png"` — clean, no base64 in context.

## Verdict

**Defer.** No current agent returns multiple files. The spec supports it cleanly, the solution is well-understood and ~30 lines of change. The realistic trigger is an agent that does compound work in one shot (e.g. a code-gen agent returning source + tests + README). Add the warning log now; implement when that agent exists.

**Warning log to add now** (2-minute change in `extractA2AResult`):

```go
if skipped > 0 {
    slog.Warn("agentregistry: multi-artifact response — only first file returned",
        "total_artifacts", len(artifacts), "skipped", skipped)
}
```
