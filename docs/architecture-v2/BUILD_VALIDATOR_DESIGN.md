# BuildValidator Architecture — Canvas Validation Design

**Status:** Approved, pending implementation  
**Last updated:** 2026-08-23  
**Applies to:** `go/internal/agentgen/compiler.go`, `go/internal/agentgen/nodes.go`, `go/internal/agentgen/noderegistry.go`

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

**No real-time UX contract.** The frontend has no authoritative contract for what constitutes an immediate error vs. a pre-publish warning on any given node type. `NodeDef` in the registry is the right place to encode this, but it currently only holds a `Validate` function — not severity metadata per check.

**Publish gate.** No code checks that every step type in the spec has `Execute != nil` before writing to `agent_runtime_specs`.

---

## 2. Goals

1. **Real-time canvas feedback** — the builder highlights nodes red/amber as the user edits, without a round-trip per keystroke.
2. **Authoritative backend validation** — `/validate` and `/publish` always re-run the full pipeline; backend is source of truth.
3. **Structured issues** — every issue carries `code`, `severity`, `skill_id`, `node_id`, `field`, `message` so the canvas can pinpoint exactly what is wrong.
4. **Context-sensitive severity** — stub/non-executable nodes are a **warning** at validate time (user is still building) and a hard **error** at publish time.
5. **Clean separation** — `NodeDef` owns node-level rules, `GraphValidator` owns graph-level rules, `Compile` / `Validate` / `CompileForPublish` are the public API.
6. **No `forPublish bool`** — explicit public functions instead; call sites are self-documenting.

---

## 3. Design

### 3.1 `Issue` type (replaces `CompileError`)

```go
// Issue is a structured validation finding returned by the BuildValidator.
// Severity: "error" blocks save/publish; "warning" is advisory only.
// SkillID/NodeID/Field are empty at higher scopes (spec-level, skill-level, node-level).
type Issue struct {
    Severity string `json:"severity"` // "error" | "warning"
    Code     string `json:"code"`
    Message  string `json:"message"`
    SkillID  string `json:"skill_id,omitempty"`
    NodeID   string `json:"node_id,omitempty"`
    Field    string `json:"field,omitempty"`
}
```

**Migration:** alias `type CompileError = Issue` for one commit so existing tests and `AgentCompileError` compile without changes. Remove the alias in a follow-up cleanup commit.

`Severity = "error"` on all existing call sites in commit 1 — no behavior change.

---

### 3.2 `NodeDef` — node-level rules and capabilities

`NodeDef` in `noderegistry.go` gains one more field:

```go
type NodeDef struct {
    // ... existing fields (Type, Version, Label, Emoji, OutputArity, IsSource, IsSink, SingleInput, InputField) ...

    // Validate checks node-specific config rules. Runs during both validate and publish.
    // The wrapper in the BuildValidator stamps SkillID and NodeID on returned issues.
    Validate func(step canvasStep, knownSlots map[string]bool) []Issue `json:"-"`

    // Execute is the runtime handler. nil = stub node (not yet implemented).
    // Derived: ToInfo().Executable = (Execute != nil).
    Execute func(ctx context.Context, ...) error `json:"-"`
}
```

No new fields on `NodeDef` for the stub/warning behaviour — that is fully derivable from `Execute == nil`. `NodeDef` does not need to know about validation modes; the BuildValidator applies the mode-specific severity when it reads `Execute`.

**What the canvas gets from `NodeDef` metadata (via `GET /admin/node-types`):**

- `executable: false` → show amber "not yet implemented" badge on the node
- `output_arity: "none"` → block drawing outgoing edges from this node
- `is_source: true` → block drawing incoming edges to this node
- `single_input: true` → block a second incoming edge
- `is_sink: true` → block outgoing edges

These rules enable **instant local UX validation** without a backend round-trip.

---

### 3.3 Internal stage functions

The implicit stages in `Compile()` become named private functions:

```go
func validateStructural(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*agentSpec, []Issue)
func validateNodes(spec *agentSpec) []Issue
func validateGraph(spec *agentSpec) []Issue
func validateExecutability(spec *agentSpec, severity string) []Issue
```

`validateExecutability` iterates all steps, and for each with `def.Execute == nil` emits an issue at the given severity. The severity is controlled by the caller — not by a bool or mode enum embedded in the function.

`SkillID` and `NodeID` are stamped inside the stage functions (not by callers):
- `validateNodes` wraps each `def.Validate()` call and stamps `SkillID = skill.SkillID`, `NodeID = step.ID` on every returned issue.
- `validateGraph` stamps `SkillID` on every graph issue; `NodeID` where a specific step is implicated.

---

### 3.4 Public API — two explicit entry points

```go
// Validate runs structural + node + graph checks and returns all issues.
// Stub nodes (Execute == nil) emit warnings, not errors — the user is still building.
// Returns a non-nil *AgentSpec even when issues exist (so callers can inspect the parsed graph).
// Returns nil spec only if structural parsing fails.
func Validate(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*AgentSpec, []Issue)

// CompileForPublish runs all four stages. Stub nodes emit errors (not warnings).
// Returns nil spec + issues if any error-severity issue exists.
// Used exclusively by the publish HTTP handler.
func CompileForPublish(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*AgentSpec, []Issue)
```

Internal implementation:

```go
func Validate(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*AgentSpec, []Issue) {
    spec, issues := validateStructural(tenantID, agentID, displayName, slug, raw)
    if hasErrors(issues) { return nil, issues }
    issues = append(issues, validateNodes(spec)...)
    issues = append(issues, validateGraph(spec)...)
    issues = append(issues, validateExecutability(spec, "warning")...)
    return spec, issues
}

func CompileForPublish(tenantID, agentID, displayName, slug string, raw json.RawMessage) (*AgentSpec, []Issue) {
    spec, issues := validateStructural(tenantID, agentID, displayName, slug, raw)
    if hasErrors(issues) { return nil, issues }
    issues = append(issues, validateNodes(spec)...)
    issues = append(issues, validateGraph(spec)...)
    issues = append(issues, validateExecutability(spec, "error")...)
    if hasErrors(issues) { return nil, issues }
    return spec, issues
}
```

Key differences:
- `Validate` does **not** early-exit after node/graph stages — it returns all issues so the canvas can highlight everything at once.
- `CompileForPublish` early-exits if any error exists after each stage — no point checking graph if nodes are broken.
- Stub severity is the **only** difference between the two paths; shared logic lives in the internal stages.

**`Compile()` is kept** as a thin wrapper over `CompileForPublish` for backward compatibility with the service layer, until callers are updated:

```go
// Compile is a backward-compatible alias for CompileForPublish.
// Deprecated: call Validate or CompileForPublish directly.
func Compile(...) (*AgentSpec, []Issue) { return CompileForPublish(...) }
```

---

### 3.5 HTTP handler wiring

| Handler | Calls | Blocks on warnings? |
|---|---|---|
| `POST /agent-definitions/{id}/validate` | `agentgen.Validate()` | No — returns 200 with all issues including warnings |
| `POST /agent-definitions/{id}/publish` | `agentgen.CompileForPublish()` | Yes — 422 if any error-severity issue |

Validate response:

```json
{
  "valid": true,
  "issues": [
    {
      "severity": "warning",
      "code": "NODE_NOT_EXECUTABLE",
      "message": "node type \"branch\" is not yet implemented",
      "skill_id": "s1",
      "node_id": "step_branch_1",
      "field": ""
    }
  ]
}
```

`"valid": true` even when warnings exist — the canvas uses this to allow saving drafts. The publish handler returns `"valid": false` + 422 when errors exist.

---

### 3.6 Canvas real-time validation contract

The canvas builder does **not** call `/validate` on every keystroke. Instead it uses two sources:

**Source 1 — Node metadata from `GET /admin/node-types` (fetched once on mount, cached).**

The builder applies these rules instantly in the UI without any API call:
- `executable: false` → amber "not yet implemented" warning badge on the node
- `is_source` / `is_sink` / `single_input` / `output_arity` → block illegal edge draws in the canvas drag-and-drop logic
- Unknown type → red "unknown node" error

**Source 2 — Backend `/validate` call (debounced ~800ms after graph changes).**

Returns structured issues that the builder maps to node highlight state:

```ts
interface Issue {
  severity: 'error' | 'warning';
  code: string;
  message: string;
  skill_id?: string;
  node_id?: string;
  field?: string;
}
```

Builder logic per issue:
- `node_id` present → highlight that node red (error) or amber (warning)
- `field` present → highlight that field inside the node config panel
- `skill_id` only → highlight at skill/tab level
- No node_id → show in a global issues panel

**Source of truth:** backend. The canvas local rules are UX sugar — the backend always re-validates on save and publish.

---

### 3.7 Connection validation

The canvas blocks illegal edges **locally** using `NodeDef` metadata:

| Rule | Source |
|---|---|
| No edge from a sink node | `is_sink: true` |
| No edge into a source node | `is_source: true` (except the implicit input) |
| Max one incoming edge | `single_input: true` |
| Branch/parallel require labeled exits | `output_arity: "multi"` → canvas requires branch labels |

The backend `validateGraph` stage validates the same rules authoritatively and returns `INVALID_CONNECTION`, `MISSING_BRANCH_LABEL`, etc. as error-severity issues.

---

## 4. What is deliberately excluded

| Idea | Why excluded |
|---|---|
| `forPublish bool` parameter on `Compile` | Makes call sites opaque; two named functions are clearer |
| `ValidationMode` enum | Same problem as bool — one fewer type is simpler |
| Public stage functions | Callers don't need partial pipelines |
| `FieldErrors map[string][]Issue` | Premature; flat `Field string` is sufficient for highlighting |
| Stub nodes as errors at validate time | Blocks saving a draft mid-build; counterproductive UX |
| Real-time validation via polling | Debounced call to `/validate` is sufficient; no WebSocket needed |

---

## 5. Migration Plan (3 commits, all additive)

### Commit 1 — Enrich the Issue type

- Rename `CompileError` → `Issue`; add `Severity`, `SkillID`, `NodeID`, `Field`
- Alias `type CompileError = Issue` for backward compat
- Set `Severity = "error"` on all existing call sites
- **No behavior change. All tests pass.**

### Commit 2 — Extract stage functions + two public entry points

- Extract `validateStructural`, `validateNodes`, `validateGraph`, `validateExecutability`
- Add `Validate()` and `CompileForPublish()`; keep `Compile()` as deprecated alias
- Thread `SkillID` and `NodeID` stamping through stage functions
- Update `agent_definitions.go`: validate handler → `agentgen.Validate()`, publish handler → `agentgen.CompileForPublish()`
- **Behavior change: stub nodes now return warnings from validate, errors from publish**
- Add tests: stub node warning on validate, stub node error on publish

### Commit 3 — Canvas frontend integration

- Update builder to read `node_id` / `field` / `severity` from validate response and apply highlight state
- Use `executable` from cached node types for instant amber badge on stub nodes
- Block illegal edge draws using `is_source` / `is_sink` / `single_input` / `output_arity`
- **No backend changes in this commit**

---

## 6. File map

| File | Change |
|---|---|
| `go/internal/agentgen/compiler.go` | `CompileError → Issue`, add fields, extract stage functions, add `Validate()` + `CompileForPublish()`, keep `Compile()` alias |
| `go/internal/agentgen/nodes.go` | Update `Validate` func signature to return `[]Issue` (via alias, no actual change needed in commit 1) |
| `go/internal/agentgen/noderegistry.go` | No change |
| `go/internal/admin/agent_definitions.go` | validate handler → `agentgen.Validate()`, publish handler → `agentgen.CompileForPublish()` |
| `go/internal/admin/service/agent_definition_service.go` | `AgentCompileError` — update if `CompileError` type name changes |
| `go/internal/agentgen/compiler_test.go` | Add stub warning/error tests; update `CompileError` refs |
| `frontend/src/app/admin/agents/builder/page.tsx` | Consume `node_id`/`field`/`severity` from validate response; block illegal edges using NodeDef metadata |
| `go/TEST_INDEX.md` | Update coverage notes |
