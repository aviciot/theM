# A2A Multi-Artifact: Full Compliance Plan
_Investigated: 2026-08-23_

---

## 1. A2A v1.0 Wire Format — What the Spec Supports

From SDK protobuf inspection:

```
Artifact fields: artifact_id, name, description, parts[], metadata, extensions
Part fields:     text, raw (bytes), url, data (Value/JSON), metadata, filename, media_type
TaskArtifactUpdateEvent fields: task_id, context_id, artifact, append, last_chunk, metadata
```

**Key facts:**
- `Task.artifacts` is **repeated** — a single SendMessage response legally contains N artifacts
- `TaskArtifactUpdateEvent` carries **one artifact at a time** with `append`/`last_chunk` for streaming delivery
- A single `Artifact` can have multiple `Part` objects (e.g. a cover image + content + metadata CSV)
- `part.raw` = bytes field (base64 in JSON wire) — correct for binary files
- `part.text` = string — for HTML/markdown/plain text files
- `part.data` = google.protobuf.Value — for structured JSON payloads (input, not file output)

**Conclusion:** Any A2A agent can legally return multiple artifacts in one response. Our current extractor returns on the first file found and silently drops the rest.

---

## 2. Current Gap — Exact Lines

**`go/internal/agentregistry/registry.go` — `extractA2AResult` (line ~432):**
```go
for _, artifact := range artifacts {
    for _, part := range artifact.Parts {
        // ... build payload ...
        return out, nil   // ← STOPS HERE — remaining artifacts dropped
    }
}
```

**`go/internal/orchestrator/orchestrator.go` — `executeTools` (line ~693):**
```go
var ap artifactPayload   // singular: {"artifact": {...}}
json.Unmarshal(out, &ap)
if ap.Artifact != nil {
    o.emitArtifactEvent(...)   // records exactly ONE artifact
}
delete(stripped, "artifact")
```

The `artifactPayload` struct itself is singular — only one `artifact` key.

---

## 3. Proposed Solution — Minimal, Interface-Stable

### Principle
Keep `AgentInvoker.Invoke(ctx, tenantID, slug, input) (json.RawMessage, error)` unchanged.
Absorb the plural-to-singular impedance mismatch inside the internal JSON envelope.

### Change 1 — `go/internal/orchestrator/orchestrator.go`

Add plural support to `artifactPayload`, keep singular as backward-compat fallback:

```go
// before
type artifactPayload struct {
    Artifact *artifactBody `json:"artifact,omitempty"`
}

// after
type artifactPayload struct {
    Artifact  *artifactBody   `json:"artifact,omitempty"`   // legacy single-artifact
    Artifacts []artifactBody  `json:"artifacts,omitempty"`  // multi-artifact (new)
}
```

In `executeTools`, fan out recording and build clean LLM summary:

```go
// before
var ap artifactPayload
json.Unmarshal(out, &ap)
if ap.Artifact != nil {
    o.emitArtifactEvent(ctx, contextID, runID, rctx, ap.Artifact)
    delete(stripped, "artifact")
}

// after
var ap artifactPayload
json.Unmarshal(out, &ap)

// Normalise to slice — handles both singular (legacy) and plural (new)
bodies := ap.Artifacts
if ap.Artifact != nil {
    bodies = append([]artifactBody{*ap.Artifact}, bodies...)
}

var filenames []string
for i := range bodies {
    o.emitArtifactEvent(ctx, contextID, runID, rctx, &bodies[i])
    filenames = append(filenames, bodies[i].Filename)
}
if len(bodies) > 0 {
    delete(stripped, "artifact")
    delete(stripped, "artifacts")
    // LLM sees clean summary — no base64, no binary
    stripped["output"] = fmt.Sprintf("%d file artifact(s) ready: %s",
        len(bodies), strings.Join(filenames, ", "))
    out, _ = json.Marshal(stripped)
}
```

### Change 2 — `go/internal/agentregistry/registry.go`

Replace early-return with full collection in `extractA2AResult`:

```go
// before — returns on first file found
for _, artifact := range artifacts {
    for _, part := range artifact.Parts {
        if part.Filename == "" { continue }
        // ... encode ...
        return out, nil  // ← drops rest
    }
}

// after — collect all file parts
var bodies []map[string]string
for _, artifact := range artifacts {
    for _, part := range artifact.Parts {
        if part.Filename == "" { continue }
        var encoded, contentType string
        if part.Raw != "" {
            encoded, contentType = part.Raw, part.MediaType
        } else if part.Text != "" {
            encoded = base64.StdEncoding.EncodeToString([]byte(part.Text))
            contentType = part.MediaType
        } else {
            continue
        }
        if contentType == "" { contentType = "application/octet-stream" }
        name := part.Filename
        if artifact.Name != "" { name = artifact.Name }
        bodies = append(bodies, map[string]string{
            "filename": name, "content_type": contentType, "data_base64": encoded,
        })
    }
}
if len(bodies) == 1 {
    // Single artifact — keep legacy {"artifact": {...}} shape for compat
    out, _ := json.Marshal(map[string]any{
        "output":   "File artifact: " + bodies[0]["filename"],
        "artifact": bodies[0],
    })
    return out, nil
}
if len(bodies) > 1 {
    names := make([]string, len(bodies))
    for i, b := range bodies { names[i] = b["filename"] }
    out, _ := json.Marshal(map[string]any{
        "output":    fmt.Sprintf("%d file artifacts: %s", len(bodies), strings.Join(names, ", ")),
        "artifacts": bodies,
    })
    return out, nil
}
```

---

## 4. LLM Context Hygiene

The LLM **never** sees base64 or binary data. After stripping `artifact`/`artifacts` keys, the tool result message contains only:

```
"2 file artifacts ready: report.pdf, diagram.png"
```

This is human-readable and small. The orchestrator's existing `delete(stripped, "artifact")` pattern is extended to also delete `"artifacts"` before the result goes into LLM history.

---

## 5. Streaming Path — Scope

`TaskArtifactUpdateEvent` (append/last_chunk) is **out of scope** for this change. It applies only to agents using streaming delivery. Our current agents (docu_writer, etc.) use non-streaming `SendMessage` which returns `Task.artifacts[]` synchronously. This plan covers that path fully.

Streaming path deferred — implement when a streaming agent exists.

---

## 6. Test Plan

**`internal/agentregistry/registry_test.go`** — add:
- `TestExtractA2AResult_MultiArtifact_TwoParts` — agent returns two file parts in one artifact; verify both bodies in `"artifacts"` key
- `TestExtractA2AResult_MultiArtifact_TwoArtifacts` — agent returns two separate Artifact objects; verify both collected
- `TestExtractA2AResult_SingleArtifact_LegacyShape` — single file still returns `"artifact"` key (backward compat)
- `TestExtractA2AResult_MixedParts` — artifact with text + file parts; only file parts collected

**`internal/orchestrator/` (new file `orchestrator_artifact_test.go`)** — add:
- `TestExecuteTools_MultiArtifact_FansOut` — mock agent returns `{"artifacts":[...]}` with 2 bodies; verify `emitArtifactEvent` called twice, LLM sees clean summary
- `TestExecuteTools_SingleArtifact_LegacyCompat` — mock agent returns old `{"artifact":{...}}`; verify single emit, existing behaviour unchanged

---

## 7. Rollout Order — One Atomic Commit

All changes are internal to two files. No interface changes, no schema changes, no DB migrations. Safe to ship in a single commit:

1. `go/internal/agentregistry/registry.go` — `extractA2AResult` plural collection
2. `go/internal/orchestrator/orchestrator.go` — `artifactPayload` + `executeTools` fan-out
3. Tests in both packages
4. `go/TEST_INDEX.md` updated

---

## 8. Risk Assessment

| Risk | Likelihood | Mitigation |
|---|---|---|
| Existing single-artifact agents break | None | Legacy `"artifact"` key path preserved exactly |
| LLM receives base64 | None | `delete(stripped, "artifacts")` mirrors existing `delete(stripped, "artifact")` |
| RecordArtifact called twice for one file | None | Each body is a distinct decoded artifact |
| Size check bypass on 2nd artifact | Low | `emitArtifactEvent` already has size guard per call |
| orchestrator_test.go doesn't exist yet | Low | Create it; pattern matches `registry_test.go` fakes |

Existing test coverage: `TestExtractA2AResult_*` tests in `registry_test.go` cover the single-artifact path and will catch regressions. The new plural tests pin the new behaviour.

**Manual verification after deploy:** Run the docu_writer pdf test — confirm single artifact still recorded correctly. Then write a throwaway test agent that returns two file parts and confirm both appear in the Artifacts tab.
