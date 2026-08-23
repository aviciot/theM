# BuildValidator Architecture — Canvas Validation Design

**Status:** Approved, pending implementation  
**Last updated:** 2026-08-23  
**Applies to:** `go/internal/agentgen/compiler.go`, `go/internal/agentgen/nodes.go`

---

## 1. Current State (post commit 51aedbe)

### What exists

`compiler.go` → `Compile()` has three implicit stages:

| Stage | Where | What it checks |
|---|---|---|
| Structural | top of `Compile()` | JSON parse, `display_name`, slot dedup |
| Node | `compileSkillSteps` | `LookupNode` type check + `def.Validate()` per step |
| Graph | `compileSkillSteps` (tail) | dangling Next/Branch refs, DFS cycle detection, topo-sort |

**Stage 4 (publish gate) is absent.** Both `/validate` and `/publish` call the same `Compile()`. Publishing an agent whose graph contains stub nodes (`branch`, `loop`, `parallel`, `a2a_call`, `human_wait`, `stream_out`) produces no error — the agent is stored as published but fails silently at runtime.

### What's missing

**Structured error fields.** `CompileError` today:

```go
type CompileError struct {
    Code    string
    Message string
    Context string   // free-form — unparseable by the canvas UI
}
```

The canvas builder cannot highlight a specific node or field from this. It needs `SkillID`, `NodeID`, and `Field` as first-class fields.

**Publish gate.** No code checks that every step type in the spec has `Execute != nil` before writing to `agent_runtime_specs`.

---

## 2. Proposed Design

### 2.1 Enriched `Issue` type

Replace `CompileError` with `Issue`:

```go
type Issue struct {
    Severity string // "error" | "warning"
    Code     string
    Message  string
    SkillID  string // empty = spec-level issue
    NodeID   string // empty = skill-level issue
    Field    string // empty = node-level issue (no specific field)
}
```

**Migration note:** alias `CompileError = Issue` for one commit to keep existing tests and service-layer code (`AgentCompileError`) green, then remove the alias in a follow-up.

`Severity` defaults to `"error"` on all existing callsites — no behavior change in commit 1.

### 2.2 Four named stage functions (internal)

Extract the implicit stages into named private functions:

```go
func validateStructural(spec *agentSpec) []Issue
func validateNodes(spec *agentSpec) []Issue
func validateGraph(spec *agentSpec) []Issue
func validatePublishable(spec *agentSpec) []Issue
```

`Compile()` public signature stays **identical** — it delegates internally:

```go
func Compile(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*AgentSpec, []Issue) {
    spec, issues := validateStructural(...)
    if hasErrors(issues) { return nil, issues }
    issues = append(issues, validateNodes(spec)...)
    if hasErrors(issues) { return nil, issues }
    issues = append(issues, validateGraph(spec)...)
    if hasErrors(issues) { return nil, issues }
    return spec, nil
}
```

Early-exit behavior is preserved: each stage only runs if the previous stage produced zero errors. (Warnings do not block later stages.)

### 2.3 Publish gate — `validatePublishable`

```go
func validatePublishable(spec *agentSpec) []Issue {
    var issues []Issue
    for _, skill := range spec.Skills {
        for _, step := range skill.Steps {
            def, ok := LookupNode(step.Type)
            if !ok { continue } // already caught by validateNodes
            if def.Execute == nil {
                issues = append(issues, Issue{
                    Severity: "error",
                    Code:     "NODE_NOT_EXECUTABLE",
                    Message:  fmt.Sprintf("node type %q is not yet implemented and cannot be published", step.Type),
                    SkillID:  skill.SkillID,
                    NodeID:   step.ID,
                })
            }
        }
    }
    return issues
}
```

The **publish HTTP handler** runs all 4 stages (including `validatePublishable`).  
The **validate HTTP handler** runs only stages 1–3 — it shows the user what's structurally wrong, not what's not-yet-implemented.

### 2.4 `SkillID` stamping

`compileSkillSteps` (and its callers) already receive `skillID`. Thread it into the stage functions so every `Issue` returned from node/graph validation has `SkillID` stamped automatically. Callers do not set it manually.

`NodeID` is stamped at the point where a step is being validated: `def.Validate(step, knownSlots)` becomes the locus for `NodeID = step.ID`.

### 2.5 `def.Validate` signature stays the same for now

`NodeDef.Validate func(step canvasStep, knownSlots map[string]bool) []CompileError` (aliased to `[]Issue`) does not change in this refactor. Per-type validate functions already exist for `llm` and `http` (slot checks). They'll naturally gain `NodeID` from the wrapper that calls them.

---

## 3. What is deliberately excluded

| Idea | Why excluded |
|---|---|
| Public `BuildValidator` struct | No runtime state to carry; package-level functions are simpler |
| Warning severity at validate time for stubs | Warnings without actionability add noise. Stubs are hard errors at publish, invisible at validate |
| `FieldErrors map[string][]Issue` | Premature. Flat `Field string` on `Issue` is sufficient for canvas highlighting and can be promoted later |
| Separate public stage functions | Callers don't need partial pipelines; `Compile()` is the single entry point |

---

## 4. Migration Plan (3 commits, all additive)

### Commit 1 — Enrich the error type

- Add `Severity`, `SkillID`, `NodeID`, `Field` to `CompileError`
- Set `Severity = "error"` on all existing construction sites
- Alias `CompileError = Issue` (or rename and update references)
- **No behavior change. All tests pass.**

### Commit 2 — Extract stage functions

- Pull `validateStructural`, `validateNodes`, `validateGraph` out of `Compile()` / `compileSkillSteps`
- `Compile()` signature unchanged — delegates to the three stage functions
- Thread `SkillID` and `NodeID` through so issues are stamped automatically
- **No behavior change. All tests pass.**

### Commit 3 — Add publish gate

- Implement `validatePublishable` (executable check)
- Wire into publish HTTP handler only (not validate)
- Add test: publish an agent with a `branch` step → 422 with `NODE_NOT_EXECUTABLE`
- **Behavior change at publish: previously-publishable stub-containing agents now return 422**

---

## 5. Frontend impact

No frontend changes are required until canvas node highlighting is built. When that work begins, the richer `Issue` fields (`SkillID`, `NodeID`, `Field`) are already present in the API response — the builder can map them directly to node highlight state.

The `/validate` response shape changes from:

```json
{"valid": false, "errors": [{"code": "...", "message": "...", "context": "..."}]}
```

to:

```json
{"valid": false, "errors": [{"severity": "error", "code": "...", "message": "...", "skill_id": "...", "node_id": "...", "field": ""}]}
```

This is additive — new fields, old fields remain — so existing frontend code that reads `code`/`message` continues to work.

---

## 6. File map

| File | Change |
|---|---|
| `go/internal/agentgen/compiler.go` | Rename `CompileError` → `Issue`, add fields, extract stage functions, add publish gate call |
| `go/internal/agentgen/nodes.go` | No change — `NodeDef.Validate` signature unchanged |
| `go/internal/agentgen/noderegistry.go` | No change |
| `go/internal/admin/agent_definitions.go` | Publish handler calls `validatePublishable` (or `Compile` gains a `forPublish bool` param) |
| `go/internal/admin/service/agent_definition_service.go` | Update `AgentCompileError` if `CompileError` type name changes |
| `go/internal/agentgen/compiler_test.go` | Add `NODE_NOT_EXECUTABLE` test, update field references |
| `go/TEST_INDEX.md` | Update coverage notes |
