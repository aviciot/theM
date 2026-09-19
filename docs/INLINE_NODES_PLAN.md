# Inline Nodes — Phase 1 Implementation Plan
# the-M — App Canvas Inline Execution Nodes (LLM + Condition)
# Date: 2026-09-17
# Design source: `docs/INLINE_NODES_DESIGN_BRIEF.md`
# Baseline HEAD: `ae37ff2a` (AppFlow Phase B complete)

---

## 0. Executive summary

Phase 1 adds two inline node kinds to the app canvas — **LLM** and **Condition** — that
execute inside the AppFlow graph without a backing registered agent.

The design rests on five decisions, each justified in §1:

1. **Config lives in the definition JSON** on `ComponentInstance.config`, reusing
   `definition_ref.kind = "inline"`. No new DB table, no new column.
2. **Node semantics are copied from `agentgen`, not reinvented.** `agentgen` already ships a
   production LLM node and a Branch (condition) node with Go-template prompt rendering,
   `output_var` data flow, and `ctrl-out-true`/`ctrl-out-false` control ports. AppFlow inline
   nodes adopt the same config field names and the same evaluation semantics.
3. **The data bus is upgraded from `accumulated string` to a variables map**, carried
   alongside the existing string so every current node kind keeps working unchanged.
4. **Temporal is the only execution backend in Phase 1.** Local mode today does not execute
   the AppFlowSpec at all (verified — see §4.1); building a second in-process graph walker is
   a separate slice and is explicitly deferred to Phase 2.
5. **Condition coexists with Router** — it does not replace it. Router is an LLM
   *classifier* over N labels; Condition is a *deterministic* 2-way expression test.

Scope of Phase 1: 2 node kinds, ~7 Go files touched, ~5 frontend files touched,
1 file split required, 0 migrations.

---

## 1. Answers to the brief's open design questions

Every question from the brief, answered with the code evidence that decided it.

### 1.1 Which node kinds in Phase 1?

**LLM + Condition only.** Tool/MCP and Transform are deferred.

Rationale: the LLM calling infrastructure already exists in `dag-worker`
(`multiLLMFactory`, `dbRouterLLMCaller.resolveKey` — `go/cmd/dag-worker/main.go:469-707`),
so the LLM node is largely wiring. Condition is pure in-workflow logic (no activity at all —
see §5.3), so it is nearly free once the variables bus exists. Tool/MCP needs MCP server
attachment resolution per app plus a new activity with its own auth path; Transform needs the
`agentgen/transform` function-step engine ported. Both are Phase 2.

### 1.2 Should Condition replace or extend Router?

**Coexist.** They answer different questions and have different failure modes:

| | Router (`kind: "router"`) | Condition (`kind: "condition"`) |
|---|---|---|
| Decision made by | LLM classifier | Go template expression |
| Outgoing edges | N, matched by `label` | exactly 2, `true` / `false` |
| Cost | 1 LLM call per traversal | zero |
| Fails when | LLM returns an unknown label | expression fails to render |

Router is the right tool for "which of these 5 agents should handle this?". Condition is the
right tool for "did the previous node return APPROVED?". Merging them would force an LLM call
onto deterministic branching. Router stays untouched in Phase 1.

### 1.3 What is the input/output contract for each node?

**Structured, via a variables map — not bare `string`.**

Today `AppFlowWorkflow` threads a single `accumulated string` between nodes
(`go/internal/appflow/workflow.go:292`). That is sufficient for agent→agent chaining but
cannot express "the LLM node named `classify` produced `sentiment`, and the condition three
nodes later tests it".

Phase 1 introduces `FlowVars map[string]string` threaded alongside `accumulated`:

- `vars["input"]` is always the current `accumulated` value (so `{{.input}}` works everywhere).
- An LLM node writes its response to `vars[output_var]`, defaulting to `output`.
- A Condition node reads `vars` and writes nothing.
- `accumulated` continues to be updated by the LLM node, so a downstream **agent** node
  receives the LLM output as its user message with no extra config. This is what keeps
  existing flows working.

`map[string]string` rather than `map[string]any`: values cross the Temporal
serialisation boundary on every activity call, and every Phase 1 producer and consumer is
text. `agentgen` uses `map[string]any` because its HTTP node extracts JSON objects; AppFlow
has no such node in Phase 1. Revisit when the Transform node lands.

### 1.4 Should inline config be stored in `ComponentInstance.config`?

**Yes.** `ComponentInstance.config` is already `json.RawMessage` on the Go side
(`compiler.go:68`) and `Record<string, unknown>` on the wire
(`frontend/src/lib/apiTypes.ts:514`), lands in the `application_definitions.definition`
jsonb column, and is already how Router and HIL carry their config. The brief's constraint
("Inline node config lives in the definition JSON — not a new table") is satisfied with zero
new persistence work.

### 1.5 Should inline nodes be `ComponentDefinitionSummary` references at all?

**No — they are self-contained, and they must use `definition_ref.kind = "inline"`.**

This is the one decision with a hard code constraint behind it. `ValidateDefinition` and
`PublishDefinition` resolve every component against the DB registry, with exactly one
hardcoded exemption:

```go
// go/internal/admin/service/publish.go:177
if s.registry != nil && string(comp.DefinitionRef.Kind) != "flow_control" {
    _, resolveErr := s.registry.ResolveForPublish(ctx, tenantID, comp.DefinitionRef, "")
```

and again at `publish.go:296`. A brand-new `kind` would fail `ResolveForPublish` with
`component_not_found`, so publish would reject every canvas containing an inline node.

Two options were considered:

- **(A) Reuse `kind: "flow_control"`** with `name: "llm"` / `"condition"`. Zero backend
  publish changes. But it makes "flow control" mean both graph topology (fork/join/router/hil)
  and data-plane execution (an LLM call), and the frontend's single `flowControl` node type
  would have to render two visually distinct families.
- **(B) Add `kind: "inline"`** and extend the two guards to a small set.

**Chosen: (B).** The guards become a shared helper so the exemption list has one home:

```go
// go/internal/admin/service/publish.go (new, near the top)

// builtinKinds are definition_ref kinds implemented in code rather than
// registered in them.component_definitions. They are exempt from registry
// resolution at validate and publish time.
//   flow_control — router, hil, fork, join (graph topology)
//   inline       — llm, condition (self-contained execution nodes)
func isBuiltinKind(kind string) bool {
    return kind == "flow_control" || kind == "inline"
}
```

Both call sites become `if s.registry != nil && !isBuiltinKind(string(comp.DefinitionRef.Kind))`.
This keeps the taxonomy honest and gives Tool/Transform a home in Phase 2 without touching
publish again.

### 1.6 How does the canvas serialise inline nodes with no agent/orchestrator backing?

Exactly as it already serialises flow_control nodes — `canvasToDoc` synthesises the
`definition_ref` from the node's own data rather than copying one from a palette item
(`CanvasHelpers.ts:140`). The inline branch is the same shape:

```ts
} else if (n.type === 'inline') {
  const d = n.data as unknown as InlineNodeData;
  components.push({
    instance_id: n.id,
    definition_ref: { kind: 'inline', namespace: 'builtin', name: d.node_type, version: 1 },
    config: { ...d.config, node_type: d.node_type, display_name: d.display_name },
  });
}
```

`version: 1` matters — `ValidateDefinition` rejects `version <= 0` with `missing_version`
(`publish.go:154`) regardless of kind.

### 1.7 Should local mode use the same compiled graph as Temporal?

**Yes when local execution is built — but it is not built in Phase 1.** See §4.1 for the
verification that local mode currently ignores the AppFlowSpec entirely, and §4.2 for the
deferral rationale and the Phase 2 sketch.

### 1.8 Where does the local runner live?

Deferred to Phase 2. When built: a new package `go/internal/appflow/local/` implementing a
walker over the same `AppFlowSpec`. Not `internal/orchestrator/` (that is the agentic
tool-calling loop, a different execution model) and not inside `internal/appflow/` root
(which must stay importable by the Temporal worker without dragging in bridge dependencies).

### 1.9 How does local mode stream LLM output to the WS/SSE client?

Deferred with §1.8. The mechanism is already determined by the existing contract, though:
publish `{"type":"token","content":"..."}` to `them:dash:run:{runID}:stream`. See §6 — this
is identical for Temporal mode, which is why Phase 1 gets streaming without local mode.

### 1.10 Should inline nodes be a new node-library section?

**Yes — a new "Inline / Logic" section** in the `CanvasBuilderView` component palette,
placed directly below "Flow Control". Note the palette that matters is the one in
`CanvasBuilderView.tsx:506-528`, not `NodeLibrary.tsx` — the latter is the legacy
(`buildNodesFromApp`) canvas and has no flow-control section at all.

### 1.11 What is the visual distinction from agent nodes?

Agent/orchestrator nodes: solid 56px circle, Material Symbols icon, solid border when
selected. Flow-control nodes: **dashed** border (`CanvasNodes.tsx:309`), emoji glyph.

Inline nodes: **solid border, emoji glyph, and a `kind` caption reading `llm` / `condition`**
— deliberately between the two families, because an inline node *does* real work (unlike a
pure topology marker) but has no external endpoint (unlike an agent). Colors: LLM
`#d0bcff` (purple, matching `agentgen`'s LLM node `rgba(208,188,255,0.6)`), Condition
`#f97316` (orange, matching `agentgen`'s Branch node `rgba(249,115,22,0.5)`). Reusing the
agent-builder palette means one mental model across both canvases.

### 1.12 Inline panel or side drawer for prompt/expression config?

**Side panel — `CanvasNodePropertiesPanel`**, which is where Router and HIL are already
configured. But that file is **1003 lines today** and `go/CLAUDE.md` + root `CLAUDE.md` both
cap files at ~400 (hard stop at 500). Extending it is not an option. See §7.1 for the
mandatory split, which is a prerequisite for the UI work, not an optional cleanup.

### 1.13 Should the canvas block publish on incomplete inline config?

**Yes, and the plumbing already exists** — but it needs one fix first.

`publish()` in `CanvasBuilderView.tsx:276-293` calls `themApi.validateDefinition`, refuses to
publish when `report.valid === false`, and maps each error's `instance_id` onto the offending
node (`_error` / `_errorMsg`), which `CanvasNodes.tsx` renders as a red ring plus tooltip. So
any new server-side validation error code automatically gets canvas highlighting with zero
frontend work.

**The fix:** `ValidateDefinition` never calls `appflow.Validate` — verified, `internal/admin/`
imports `appflow` only in `hil_approvals.go`. All compiler-level structural validation
(`router_no_edges`, `fork_insufficient_branches`, `unresolved_agent`) today runs *only* at run
start inside `ws.startAppFlow` (`internal/ws/handler.go:511`), where a failure surfaces to the
end user as "failed to start appflow workflow" rather than to the builder at publish time.
Phase 1 closes this gap (§8.2) — otherwise a malformed Condition node publishes cleanly and
fails at runtime, which is the worst outcome for the operator.

### 1.14 Connection type: `'inline'` or `'delegation'`?

**Reuse `'flow_control'`.** Do not add an `'inline'` connection type.

`ConnectionDef.type` drives two things and neither benefits from a new value:

- **Compiler routing** — irrelevant. `compileEP` builds edges from *all* connections between
  reachable nodes (`compiler.go:226`); it never switches on `conn.Type`. Only `conn.Label` is
  read, and only for router sources (`compiler.go:229`).
- **Canvas rendering** — `docToCanvas` accepts a fixed allowlist
  (`'tool' | 'delegation' | 'middleware' | 'flow_control'`, `CanvasHelpers.ts:198`) and
  renders all four identically as `type: 'default'`.

A new `'inline'` value would therefore be a no-op that still requires touching the union type,
`docToCanvas`'s allowlist, and any future exhaustive switch. `'flow_control'` already means
"a control/logic edge on the app graph", which is accurate for inline node edges.

**Consequence:** `canvasToDoc`'s existing flow-control branch must be widened to also fire for
inline endpoints, since edges are typed by their endpoint node types (`CanvasHelpers.ts:154`).

---

## 2. Data model

### 2.1 Definition JSON (the wire + storage format)

An LLM node as stored in `application_definitions.definition`:

```json
{
  "instance_id": "inline_llm_1",
  "definition_ref": { "kind": "inline", "namespace": "builtin", "name": "llm", "version": 1 },
  "config": {
    "node_type": "llm",
    "display_name": "Summarize",
    "provider": "",
    "model": "",
    "system_prompt": "You are a concise summarizer.",
    "user_prompt": "Summarise this: {{.input}}",
    "max_tokens": 1024,
    "temperature": 0.7,
    "output_var": "summary"
  }
}
```

A Condition node:

```json
{
  "instance_id": "inline_condition_1",
  "definition_ref": { "kind": "inline", "namespace": "builtin", "name": "condition", "version": 1 },
  "config": {
    "node_type": "condition",
    "display_name": "Approved?",
    "expression": "{{eq .summary \"APPROVED\"}}"
  }
}
```

Its two outgoing connections carry `label: "true"` and `label: "false"`:

```json
{ "source": "inline_condition_1", "target": "agent_1", "type": "flow_control", "label": "true"  },
{ "source": "inline_condition_1", "target": "agent_2", "type": "flow_control", "label": "false" }
```

Edge labels are already plumbed end to end — `connDef.Label` → `AppFlowEdge.Label`
(`compiler.go:227`, test AF-08) and the workflow's `findEdgeByLabel` does a
case-insensitive match (`workflow.go:775`). Condition reuses both, so `true`/`false` routing
needs no new edge machinery. One compiler change is required: labels are currently attached
only when the source is a router (`compiler.go:229`), which must be widened to include
condition sources.

### 2.2 Go config types — `go/internal/appflow/inline.go` (new file, ~90 lines)

Field names are deliberately identical to `agentgen.LLMStepConfig`
(`internal/agentgen/spec.go:206`) and `agentgen.BranchStepConfig` (`spec.go:310`) so that a
future canvas-to-canvas migration is a straight copy and operators learn one vocabulary.

```go
package appflow

// InlineLLMConfig is the config stored on an inline LLM node
// (definition_ref.kind="inline", name="llm").
//
// Field names intentionally match agentgen.LLMStepConfig so the app canvas and
// the agent builder expose one vocabulary for the same concept.
type InlineLLMConfig struct {
    // Provider is the LLM provider slug ("anthropic", "openai", "groq", "ollama",
    // "vllm", "lmstudio", "mock"). Empty inherits the entry point's orchestrator
    // provider (AppFlowWorkflowInput.LLMProviderName).
    //
    // This is the first per-node LLM selection in the AppFlow path: today every
    // node in a flow shares the one bound orchestrator's provider/model, because
    // LLMProviderName/LLMModel are resolved once from app_orchestrators at submit
    // time (ws/handler.go:502). An inline node can now pick a cheap model for a
    // classification step and a strong one for generation in the same flow.
    Provider string `json:"provider,omitempty"`
    // Model is the model identifier. Empty inherits the EP orchestrator model,
    // then falls back to the activity's default.
    Model string `json:"model,omitempty"`
    // SystemPrompt supports {{.varname}} Go-template interpolation over flow vars.
    SystemPrompt string `json:"system_prompt,omitempty"`
    // UserPrompt supports {{.varname}} interpolation. When empty, falls back to
    // vars["input"] (the accumulated upstream output).
    UserPrompt string `json:"user_prompt,omitempty"`
    // MaxTokens caps the response. 0 → activity default (1024).
    MaxTokens int `json:"max_tokens,omitempty"`
    // Temperature is the sampling temperature. nil → provider default.
    Temperature *float64 `json:"temperature,omitempty"`
    // OutputVar names the flow variable receiving the response. Empty → "output".
    OutputVar string `json:"output_var,omitempty"`
}

// InlineConditionConfig is the config stored on an inline Condition node
// (definition_ref.kind="inline", name="condition").
//
// Evaluation is deterministic and happens inside the workflow — no activity,
// no LLM call. Matches agentgen.BranchStepConfig semantics.
type InlineConditionConfig struct {
    // Expression is a Go template rendered against flow vars. The result is
    // truthy unless it is "", "false", "0", or "<no value>".
    Expression string `json:"expression"`
}
```

Defaults are applied by the consumer, never written back into the stored config, so an
operator who leaves a field blank keeps inheriting platform changes.

### 2.3 Compiled `AppFlowNode` kinds

`AppFlowNode.Kind` gains `"llm"` and `"condition"`. `Config` carries the raw JSON, exactly as
Router and HIL already do.

### 2.4 The flow variables bus

```go
// go/internal/appflow/inline.go

// FlowVars carries named values between nodes in one AppFlow execution.
// Always contains "input" = the current accumulated upstream output.
type FlowVars map[string]string
```

Threaded through the workflow as a local alongside `accumulated`, and passed into activity
inputs that need it. It is **not** persisted and **not** part of `AppFlowSpec` — it is
per-execution state living in Temporal workflow history.

---

## 3. Compiler changes — `go/internal/appflow/compiler.go`

Three surgical edits plus a stale-comment fix.

> **Implemented (step 2) — file split was required after all.** This section originally claimed
> "the file is 441 lines and stays under the cap". It did not: the three new `Validate` rule
> blocks plus `hasTrueFalseLabels` pushed `compiler.go` to **540 lines**, past the 500 hard stop,
> because each `ValidationError` literal in this codebase's style spans 4–5 lines. Rather than
> leave it over the limit, validation was extracted to a new file along the natural seam
> (`Compile`: doc JSON → spec, vs `Validate`: spec → errors — independent concerns, no shared
> state):
>
> | File | Content | Lines |
> |---|---|---|
> | `internal/appflow/compiler.go` | doc types + `Compile` | 387 |
> | `internal/appflow/validate.go` | `ValidationError`, `Validate`, `hasTrueFalseLabels` | 166 |
>
> This also gives Phase 2's Tool/Transform validation rules somewhere to land without
> re-breaching the cap. `strings` moved with the validation code and was dropped from
> `compiler.go`'s imports. A file-layout map was added to the package doc comment.
> Note `workflow.go` is already 803 lines at baseline — step 3 must plan for its own split.

### 3.1 `compileNode` — new case

```go
switch c.DefinitionRef.Kind {
case "agent":
    // ... unchanged
case "middleware":
    node.Kind = "middleware"
case "inline":
    switch c.DefinitionRef.Name {
    case "llm":
        node.Kind = "llm"
    case "condition":
        node.Kind = "condition"
    default:
        // Unknown inline name: keep the kind so Validate reports it as
        // unknown_inline_node rather than the workflow failing at run time.
        node.Kind = "inline"
    }
case "flow_control":
    // ... unchanged
```

Falling back to `node.Kind = "inline"` rather than passing the raw name through is what makes
a typo (`name: "lmm"`) a *validation* error at publish rather than an `UnknownNodeKind`
non-retryable workflow failure in front of a user.

### 3.2 Edge-label attachment widened

```go
// compiler.go ~line 229 — attach labels for any label-routing source kind.
if src, ok := compByID[conn.Source]; ok && isLabelRoutingSource(src) {
    edge.Label = conn.edgeLabel()
}
```

```go
// isLabelRoutingSource reports whether a component's outgoing edges carry
// routing labels: flow_control/router (intent labels) or inline/condition
// (true/false).
func isLabelRoutingSource(c *compInst) bool {
    switch c.DefinitionRef.Kind {
    case "flow_control":
        return c.DefinitionRef.Name == "router"
    case "inline":
        return c.DefinitionRef.Name == "condition"
    }
    return false
}
```

### 3.3 `Validate` — new rules

Appended inside the existing per-node loop, using the existing `outCount` / `inCount` maps:

```go
if n.Kind == "condition" {
    var cfg InlineConditionConfig
    if len(n.Config) > 0 {
        _ = json.Unmarshal(n.Config, &cfg)
    }
    if strings.TrimSpace(cfg.Expression) == "" {
        errs = append(errs, ValidationError{
            Code:       "condition_no_expression",
            Message:    "condition node has no expression configured",
            InstanceID: n.ID,
        })
    }
    if outCount[n.ID] != 2 {
        errs = append(errs, ValidationError{
            Code:       "condition_edge_count",
            Message:    fmt.Sprintf("condition node has %d outgoing edge(s); need exactly 2 (true/false)", outCount[n.ID]),
            InstanceID: n.ID,
        })
    } else if !hasTrueFalseLabels(ep.Edges, n.ID) {
        errs = append(errs, ValidationError{
            Code:       "condition_missing_labels",
            Message:    "condition node outgoing edges must be labelled \"true\" and \"false\"",
            InstanceID: n.ID,
        })
    }
}
if n.Kind == "llm" {
    var cfg InlineLLMConfig
    if len(n.Config) > 0 {
        _ = json.Unmarshal(n.Config, &cfg)
    }
    if strings.TrimSpace(cfg.SystemPrompt) == "" && strings.TrimSpace(cfg.UserPrompt) == "" {
        errs = append(errs, ValidationError{
            Code:       "llm_no_prompt",
            Message:    "llm node needs a system_prompt or user_prompt",
            InstanceID: n.ID,
        })
    }
    if outCount[n.ID] == 0 && inCount[n.ID] == 0 {
        errs = append(errs, ValidationError{
            Code:       "llm_orphan",
            Message:    "llm node is not connected to the flow",
            InstanceID: n.ID,
        })
    }
}
if n.Kind == "inline" {
    errs = append(errs, ValidationError{
        Code:       "unknown_inline_node",
        Message:    "unrecognised inline node type — re-add it from the canvas palette",
        InstanceID: n.ID,
    })
}
```

`strings` is already imported? — No: `compiler.go` imports only `encoding/json` and `fmt`.
Add `strings`.

**Deliberately NOT validated: provider keys.** The brief proposes "LLM node must have
`provider` set (or app must have that provider key configured)". Key resolution is a runtime
DB lookup across three tiers — app `provider_keys` → tenant `llm_providers` → platform
default (`dag-worker/main.go:648-707`) — and `appflow.Validate` is a pure function with no DB
access, which is precisely what makes it unit-testable without a database (`compiler.go:124`).
Validating keys would require either breaking that purity or duplicating the resolution chain.
Instead: an empty `provider` legitimately means "inherit from the entry point", and a genuinely
missing key produces a clear activity error, `no API key configured for provider %q — set a
key in App Runtime` (`dag-worker/main.go:476`). Revisit only if operators report confusion.

---

## 4. Execution backends

### 4.1 Verified current state: local mode does not run the AppFlowSpec

This is the single most consequential finding of the design review, and it changes the shape
of Phase 1.

`go/internal/ws/handler.go:383`:

```go
if handle.EPConfig.ExecutionBackend == "temporal" {
    afRun, afErr := h.startAppFlow(ctx, handle, userMsg)   // compiles + runs AppFlowSpec
    ...
} else {
    // 7a. Resolve orchestrator name from EP binding (SEC-04)
    orchName := handle.EPConfig.OrchestratorName
    ...
    input := temporal.WorkflowInput{ OrchestratorName: orchName, ... }
    orchRun, startErr := h.lc.Start(ctx, handle, input)     // OrchestrationWorkflow
}
```

So "local" does not mean "AppFlow in-process" — in fact it does not mean *in-process* at all.
`Lifecycle.Start` submits `temporal.OrchestrationWorkflow` on the standard task queue, executed
by `them-go-worker`; **"local" is a misnomer for "the orchestrator-bound workflow rather than
the AppFlow DAG workflow"**. The canvas toolbar's `Local (Orchestrator)` label
(`CanvasBuilderView.tsx:411`) is the accurate description.

What matters for this design: in the else-branch, `handle.EPConfig.ActiveDefinitionJSON` is
never read and `appflow.Compile` is never called. **The canvas graph is ignored entirely.** The
only thing carried forward from the canvas is the single `OrchestratorName` +
`AppOrchestratorID` projected into `app_orchestrators` at publish time — routers, HIL gates,
fork/join, and agent-node topology are all invisible to the non-Temporal backend.
`internal/execution/lifecycle.go` has exactly one AppFlow entry point, `StartAppFlow`
(line 634), and it hard-requires a Temporal client (`line 635`). There is no local AppFlow
walker to extend.

(`internal/temporal/temporal_executor.go` is unrelated to this path — it is the
`agentgen.ExecutionBackend` for `CanvasAgentWorkflow`, the single-agent canvas.)

### 4.2 Phase 1 is Temporal-only — and what that means for the operator

Building an in-process AppFlow graph walker means reimplementing, in the bridge: sequential
node walking, fork/join parallelism, HIL pause/resume (which needs durable state — the whole
reason HIL uses Temporal signals), retry policy, and the finalize-on-every-exit-path guarantee
that `AppFlowWorkflow` gets from `workflow.NewDisconnectedContext` (`workflow.go:210`). That is
a larger slice than both inline node kinds combined, and `CLAUDE.md` mandates one focused
subsystem per task.

**Therefore Phase 1 ships inline nodes on the Temporal backend only.** The brief states inline
nodes "must work in both modes"; this plan delivers one mode and makes the other an explicit,
named follow-up rather than a silent gap. The concrete operator impact:

- An app with `execution_backend: "temporal"` → inline nodes execute. Full feature.
- An app with `execution_backend: "local"` → inline nodes are **silently ignored today**,
  because the whole graph is ignored. That silence is the real hazard, so Phase 1 adds a
  **canvas warning** (§7.5): a non-blocking banner when inline or flow-control nodes exist
  while the backend is `local`, reading *"Inline and flow-control nodes only execute on the
  Temporal backend. This app is set to Local (Orchestrator) — the canvas graph is not
  executed."* This is the highest-value hour in the whole plan: it converts a confusing
  no-op into a legible one.

Phase 2 sketch (not built here): `go/internal/appflow/local/walker.go` walking the same
`AppFlowSpec`, with node execution behind an interface implemented once for in-process and
once for Temporal activities; HIL and fork/join rejected at validate time for local backend.

### 4.3 Temporal activity design

#### `InlineLLMActivity`

```go
// go/internal/appflow/workflow.go — constant alongside the existing four
const AppFlowInlineLLMActivityName = "AppFlowInlineLLMActivity"

// InlineLLMActivityInput is the input to AppFlowInlineLLMActivity.
// No API key is ever present — the activity resolves it from the DB at
// execution time so it never enters Temporal workflow history.
type InlineLLMActivityInput struct {
    RunID         string `json:"run_id"`
    TenantID      string `json:"tenant_id"`
    ApplicationID string `json:"application_id"`
    NodeID        string `json:"node_id"`

    // Rendered prompts. Templates are rendered in the ACTIVITY, not the
    // workflow — text/template is not deterministic-safe for workflow code.
    SystemPrompt string   `json:"system_prompt,omitempty"`
    UserPrompt   string   `json:"user_prompt,omitempty"`
    Vars         FlowVars `json:"vars,omitempty"`

    Provider    string   `json:"provider,omitempty"`
    Model       string   `json:"model,omitempty"`
    MaxTokens   int      `json:"max_tokens,omitempty"`
    Temperature *float64 `json:"temperature,omitempty"`

    // Stream, when true, publishes token events to the run's Redis stream.
    Stream bool `json:"stream,omitempty"`
}

// InlineLLMActivityOutput is returned by AppFlowInlineLLMActivity.
type InlineLLMActivityOutput struct {
    // ResponseText is the model's reply.
    ResponseText string `json:"response_text"`
    // OutputVar is echoed back so the workflow knows which var to set without
    // re-parsing node config.
    OutputVar string `json:"output_var"`
}
```

**Why templates render in the activity:** Temporal workflow code must be deterministic across
replays. `text/template` execution over a map is in practice deterministic for these inputs,
but map iteration order inside `{{range}}` is not, and the rule in `go/CLAUDE.md` is to keep
non-workflow-safe work in activities. The workflow passes the raw templates plus `Vars`; the
activity renders. This also makes render errors retryable-or-not by the activity's own policy.

Implementation on `AppFlowActivities`, reusing the existing `LLMCaller` dependency via a new
narrow interface rather than widening `RouterLLMCaller` (which is shaped specifically for
label classification):

```go
// InlineLLMCaller is the interface the inline LLM activity uses.
// The implementation resolves the API key from the DB using
// providerName + tenantID + applicationID, so keys never reach workflow history.
type InlineLLMCaller interface {
    Complete(ctx context.Context, req InlineLLMRequest) (string, error)
}

type InlineLLMRequest struct {
    SystemPrompt  string
    UserPrompt    string
    ProviderName  string
    Model         string
    MaxTokens     int
    Temperature   *float64
    TenantID      string
    ApplicationID string
}
```

`AppFlowActivities` gains one field: `InlineLLM InlineLLMCaller`. Nil → the activity returns a
non-retryable `NoInlineLLMCaller` error, matching the existing nil-dependency pattern
(`workflow.go:558`).

In `dag-worker`, `dbRouterLLMCaller` is extended to implement it — it already owns the
three-tier `resolveKey` chain and a `multiLLMFactory`. Rename to `dbLLMCaller` and give it
both methods; ~30 lines, no new key-resolution code:

```go
func (c *dbLLMCaller) Complete(ctx context.Context, req appflow.InlineLLMRequest) (string, error) {
    providerName := req.ProviderName
    if providerName == "" {
        providerName = "anthropic"
    }
    apiKey := c.resolveKey(ctx, providerName, req.TenantID, req.ApplicationID)
    if apiKey == "" && providerName != "ollama" && providerName != "mock" {
        return "", fmt.Errorf("inline llm: no API key for provider %q", providerName)
    }
    maxTokens := req.MaxTokens
    if maxTokens <= 0 {
        maxTokens = 1024
    }
    provider, err := c.factory.NewProvider(providerName, req.Model, maxTokens, apiKey)
    if err != nil {
        return "", fmt.Errorf("inline llm: create provider: %w", err)
    }
    return provider.Complete(ctx, req.SystemPrompt, req.UserPrompt)
}
```

**Temperature caveat, stated plainly:** `agentgen.LLMProvider.Complete` is
`Complete(ctx, systemPrompt, userPrompt) (string, error)` — it has no options parameter, and
neither `anthropicAdapter` nor `openAIAdapter` threads temperature through to `llm.Options`
(`dag-worker/main.go:501-552`). Phase 1 therefore **accepts and stores `temperature` but does
not apply it**, and the properties panel labels the field accordingly. Wiring it means changing
the shared `agentgen.LLMProvider` interface, which is used by the agent builder's interpreter
and its whole test suite — out of scope for this slice and called out in §11 as Phase 2 work.
The alternative (silently dropping a field the UI presents as functional) is worse.

#### Condition: no activity

Condition evaluation is deliberately **workflow-local**. It needs no I/O; it renders a template
and picks an edge. Adding an activity would cost a task-queue round trip per traversal and put
a trivially deterministic decision behind a retry policy.

Rendering happens via a small deterministic-safe helper in the appflow package, copied from
`agentgen.renderTemplate` + `isTruthy` (`internal/agentgen/interpreter.go:623`, `:459`), kept
local so `appflow` does not import `agentgen`:

```go
// go/internal/appflow/inline.go

// flowFuncs registers pure string predicates. Go text/template has NO string
// helpers built in — {{contains ...}} is a Sprig function, not stdlib — so
// without this map the documented condition examples fail to parse.
// Every entry must be pure to keep rendering workflow-safe.
var flowFuncs = template.FuncMap{
    "contains": strings.Contains, "hasPrefix": strings.HasPrefix,
    "hasSuffix": strings.HasSuffix, "lower": strings.ToLower,
    "upper": strings.ToUpper, "trim": strings.TrimSpace,
}

// renderFlowTemplate executes a Go text/template over flow vars.
// Safe for workflow code: no I/O, no clock, no randomness, and FlowVars is
// map[string]string accessed only by explicit key.
func renderFlowTemplate(tmpl string, vars FlowVars) (string, error)

// isTruthy reports whether a rendered expression counts as true.
// False for "", "false", "0", and "<no value>".
func isTruthy(s string) bool
```

### 4.4 Workflow dispatch — `go/internal/appflow/workflow.go`

Two new cases in the main loop. Both also go into `walkBranch` so inline nodes work inside
fork branches (the brief notes `walkBranch` currently handles only agent/orchestrator/middleware,
`workflow.go:731`).

> **Implemented (step 3), with two file splits it forced.** Adding the two cases took
> `workflow.go` from 441 → 526, over the cap again, so the two largest self-contained node
> protocols moved out to a new `nodes.go` (`execRouterNode`, `execHILNode` — 142 lines),
> leaving `workflow.go` at 434. Final package layout: `compiler.go` 392, `validate.go` 166,
> `inline.go` 114, `workflow.go` 434, `nodes.go` 142, `activities.go` 419, `graph.go` 184.
> Phase 2's Tool/Transform cases now have room in `nodes.go`.
>
> Two implementation decisions worth recording:
> - **`walkBranch` uses a branch-local `FlowVars`** rather than threading vars through its
>   signature. Safe because fork branches already have no cross-branch variable visibility:
>   each starts from its own copy of `accumulated` and collapses to one string via
>   `mergeBranchResults`. If a join-time condition ever needs branch vars, that is a real
>   signature change deserving its own decision.
> - **A stream-publish failure does not fail `InlineLLMActivity`.** This deliberately diverges
>   from `FinalizeRunActivity`, which *does* return `XAdd` errors: Finalize is idempotent and
>   safe to retry, whereas retrying the inline LLM activity to fix a Redis hiccup would
>   re-call and re-bill the LLM. Commented at the call site.

```go
case "llm":
    var cfg InlineLLMConfig
    if len(node.Config) > 0 {
        _ = json.Unmarshal(node.Config, &cfg)
    }
    // Provider/model inherit from the EP orchestrator binding when unset.
    provider, model := cfg.Provider, cfg.Model
    if provider == "" {
        provider = input.LLMProviderName
    }
    if model == "" {
        model = input.LLMModel
    }
    vars["input"] = accumulated

    var llmOut InlineLLMActivityOutput
    err := workflow.ExecuteActivity(ctx, AppFlowInlineLLMActivityName, InlineLLMActivityInput{
        RunID:         input.RunID,
        TenantID:      input.TenantID,
        ApplicationID: input.ApplicationID,
        NodeID:        node.ID,
        SystemPrompt:  cfg.SystemPrompt,
        UserPrompt:    cfg.UserPrompt,
        Vars:          vars,
        Provider:      provider,
        Model:         model,
        MaxTokens:     cfg.MaxTokens,
        Temperature:   cfg.Temperature,
        Stream:        true,
    }).Get(ctx, &llmOut)
    if err != nil {
        out.Status = "failed"
        retErr = fmt.Errorf("llm %q: %w", node.ID, err)
        return
    }
    outVar := llmOut.OutputVar
    if outVar == "" {
        outVar = "output"
    }
    vars[outVar] = llmOut.ResponseText
    if llmOut.ResponseText != "" {
        accumulated = llmOut.ResponseText
    }
    currentID = firstEdgeTarget(outEdgesBySource[node.ID])
    continue

case "condition":
    var cfg InlineConditionConfig
    if len(node.Config) > 0 {
        _ = json.Unmarshal(node.Config, &cfg)
    }
    vars["input"] = accumulated
    rendered, rErr := renderFlowTemplate(cfg.Expression, vars)
    if rErr != nil {
        out.Status = "failed"
        retErr = temporalerr.NewNonRetryableApplicationError(
            fmt.Sprintf("condition %q: render expression: %v", node.ID, rErr),
            "ConditionRenderFailed", nil,
        )
        return
    }
    branch := "false"
    if isTruthy(rendered) {
        branch = "true"
    }
    nextID := findEdgeByLabel(outEdgesBySource[node.ID], branch)
    if nextID == "" {
        out.Status = "failed"
        retErr = temporalerr.NewNonRetryableApplicationError(
            fmt.Sprintf("condition %q: no outgoing edge labelled %q", node.ID, branch),
            "ConditionNoMatch", nil,
        )
        return
    }
    currentID = nextID
    continue
```

Note the asymmetry with Router's fallback: Router takes the single outgoing edge when its label
does not match (`workflow.go:332`). Condition does **not** — a missing `true` or `false` edge is
a canvas error caught by `condition_edge_count` / `condition_missing_labels` at publish, and
silently taking the wrong branch on a boolean gate would be a correctness bug, not a
convenience.

`accumulated` is intentionally left unchanged by Condition: a gate observes, it does not transform.

### 4.5 Worker registration — `go/cmd/dag-worker/main.go`

```go
appFlowActs := &appflow.AppFlowActivities{
    LLMCaller:     llmCaller,   // renamed from routerCaller
    InlineLLM:     llmCaller,   // same object, second interface
    DB:            rlsPools.Admin,
    StatusUpdater: statusUpdater,
    StreamPub:     streamPub,
    AgentInvoker:  agentCaller,
}
appFlowWorker.RegisterActivityWithOptions(appFlowActs.InlineLLMActivity, temporalactivity.RegisterOptions{
    Name: appflow.AppFlowInlineLLMActivityName,
})
```

**Deployment gate:** `go/CLAUDE.md` and root `CLAUDE.md` both require a worker restart after
touching `internal/temporal/` or `cmd/dag-worker/` — activities register at startup. And per
the Phase A lesson recorded in `docs/LESSONS.md`, `build` + `restart` is insufficient:

```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d --force-recreate them-dag-worker
```

---

## 5. Run observability

### 5.1 Streaming — reuses the existing contract exactly

`AppFlowActivities.StreamPub` is already wired
(`cache.NewRunStreamerWriterRedisClient`, `dag-worker/main.go:161`) and
`FinalizeRunActivity` already publishes to `them:dash:run:{runID}:stream` with
`{"data": "<json>"}` (`workflow.go:666-683`). The inline LLM activity publishes token events
to the same key with the shape the transports already parse:

```json
{"type":"token","content":"...","run_id":"..."}
```

Both `ws.writeEvent` (`internal/ws/handler.go:653`) and the SSE equivalent
(`internal/sse/handler.go:537`) already handle `type: "token"` — so **inline LLM streaming
works with zero transport changes**. Unknown event types are dropped silently
(`handler.go:691: default: return nil`), which also means any new node-lifecycle event type
is safe to publish before the transports learn it.

**Context worth knowing: AppFlow runs are currently silent.** The only events the AppFlow path
publishes are `done` and `error`, both from `FinalizeRunActivity` (`workflow.go:639-687`). There
are zero per-node and zero token events — a client sees `ready`, then nothing for the entire
DAG (however many agents, routers and HIL gates it traverses), then one terminal frame. By
contrast the orchestrator path emits real `token` deltas plus `tool_call`
(`internal/orchestrator/orchestrator.go:420-424`).

So the inline LLM node's `token` event becomes **the first per-node client visibility in the
AppFlow path**. That is a genuine improvement, but it also creates an inconsistency worth
naming: a flow with one inline LLM node will stream that node's output and nothing else. Full
per-node AppFlow event coverage (node_started / node_completed for every kind) is the coherent
follow-up, listed in §11.

Phase 1 emits per-token deltas only where the provider path supports it. `agentgen.LLMProvider.Complete`
is a blocking call that returns the full string — the streaming already happens *inside*
`anthropicAdapter.Complete`, which consumes `p.Stream(...)` and accumulates
(`dag-worker/main.go:501-523`). To emit deltas, the inline activity needs a streaming-aware
caller rather than `Complete`. **Phase 1 decision:** publish a single `token` event with the
complete response text once the call returns. This gives the client the text through the
existing channel with no interface surgery; true incremental streaming is Phase 2 alongside
the temperature wiring, since both need the same `LLMProvider` interface change. Stated so
nobody expects character-by-character output from Phase 1.

### 5.2 `run_steps` — inline nodes recorded, no migration

`them.run_steps.agent_id` is `UUID FK→agents ON DELETE SET NULL` (nullable) and `agent_slug`
is denormalized `TEXT` (`docs/SCHEMA.md:198-213`). An inline node therefore records cleanly as
`agent_id = NULL`, `agent_slug = "inline:llm:" || node_id`, `output = response text`. No schema
change.

**Phase 1 scope call: do not record steps yet.** The AppFlow path writes no `run_steps` at all.
`AppFlowActivities` has no recorder field (`workflow.go:512-523`, wiring at
`dag-worker/main.go:167-172`); `InvokeAgentActivity` only returns text (`workflow.go:617`). The
only DB writes an AppFlow run makes are `them.runs` status transitions and the HIL approval row.
The hooks are ready and unused — `runrecorder.Recorder.RecordAgentStep` (`recorder.go:191`) and
`RecordStep(ctx, runID, stepType, content)` (`recorder.go:214`) — so wiring them is a matter of
adding one field to `AppFlowActivities`.

Recording inline nodes *alone* would produce a run history that is actively misleading: inline
nodes visible, agent nodes invisible, implying the agents never ran. Step recording for **all**
AppFlow node kinds is one coherent slice (§11 item 6) and Phase 1 does not start it. The
`run_steps` row shape above is documented here so that slice starts from a decision rather than
a question.

---

## 6. Frontend plan

### 6.1 Prerequisite: split `CanvasNodePropertiesPanel.tsx`

**1003 lines today.** Adding two config panels would push it past 1200, violating a rule both
CLAUDE.md files state twice ("If a file approaches 500 lines, stop and propose a logical split
before adding more code"). The split is therefore a prerequisite, not cleanup, and it is
mechanical — the file is already a sequence of independent `if (selectedNode.type === ...)`
blocks.

Proposed structure under `components/cbv/panels/` (per the rule, this is the proposal to
approve before the code is written):

| File | Content | Est. lines |
|---|---|---|
| `cbv/CanvasNodePropertiesPanel.tsx` | Shell: props, shared styles, `SectionHeader`, dispatch by `selectedNode.type` | ~120 |
| `cbv/panels/panelShared.tsx` | `fieldStyle`, `selectStyle`, `chipStyle`, `sectionHdrStyle`, `SectionHeader`, `isSectionOpen` | ~60 |
| `cbv/panels/OrchestratorNodePanel.tsx` | orchestrator block + MCP server attachment UI | ~260 |
| `cbv/panels/AgentNodePanel.tsx` | agent block | ~40 |
| `cbv/panels/EntryPointNodePanel.tsx` | EP block incl. access scenarios | ~215 |
| `cbv/panels/MiddlewareNodePanel.tsx` | File Guard block + wiring state/effects | ~230 |
| `cbv/panels/FlowControlNodePanel.tsx` | Router + HIL blocks | ~150 |
| `cbv/panels/InlineNodePanel.tsx` | **new** — LLM + Condition blocks | ~180 |

Every file lands under 400. The middleware wiring `useEffect`s move with their block into
`MiddlewareNodePanel`, which also removes the current oddity of middleware-specific API calls
running in the shell for every node selection.

> **Implemented (step 6, commit follows this plan's structure).** Actual line counts: shell 131,
> `panelShared.tsx` 50, Orchestrator 289, Agent 61, EntryPoint 245, Middleware 252,
> FlowControl 172 — all under 400. Note the shared file is `.tsx`, not `.ts`: it exports the
> `SectionHeader` component, and JSX requires the `.tsx` extension under this tsconfig.
> Verified pure: every `cfg.*` key, `themApi.*` call, config setter, and all 73 user-visible
> strings (labels/placeholders/option text) are byte-identical across the split, with nothing
> added; `tsc --noEmit` 0 errors before and after. The shell keeps its exact export name and
> `Props` signature, so its sole caller (`CanvasBuilderView.tsx`) needed no change.

### 6.2 Types — `frontend/src/app/admin/applications/types.ts`

```ts
export interface InlineNodeData {
  _kind: 'inline';
  instance_id: string;
  node_type: 'llm' | 'condition';
  display_name: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}

export type CanvasNodeData =
  | OrchNodeData | AgentNodeData | MwNodeData | EpNodeData
  | FlowControlNodeData | InlineNodeData;
```

Shape mirrors `FlowControlNodeData` exactly, so serialisation and error-decoration code paths
stay uniform.

`frontend/src/lib/apiTypes.ts` — extend the component kind union only:

```ts
kind: 'orchestrator' | 'agent' | 'middleware' | 'entry_point' | 'tool' | 'inline';
```

`ConnectionDef.type` is **unchanged** (§1.14).

### 6.3 Node rendering — `CanvasNodes.tsx` (335 lines, stays under cap)

New `INLINE_META` map and an `InlineNode` component modelled on `FlowControlNode` but with a
solid border:

```ts
const INLINE_META: Record<string, { emoji: string; color: string; label: string }> = {
  llm:       { emoji: '🧠', color: '#d0bcff', label: 'LLM' },
  condition: { emoji: '⑂',  color: '#f97316', label: 'Condition' },
};
```

Condition renders **two labelled source handles** so the operator wires true/false explicitly
rather than relying on edge order — matching `agentgen`'s Branch node
(`ControlOutputPorts` with ids `true`/`false`, `internal/agentgen/nodes.go:426`):

```tsx
{data.node_type === 'condition' ? (
  <>
    <Handle type="source" id="true"  position={sourcePos} style={{ ...handleStyle, background: '#4ade80', left: '35%' }} />
    <Handle type="source" id="false" position={sourcePos} style={{ ...handleStyle, background: '#f87171', left: '65%' }} />
  </>
) : (
  <Handle type="source" position={sourcePos} style={handleStyle} />
)}
```

Register `inline: InlineNode as any` in `NODE_TYPES`.

### 6.4 Serialisation — `CanvasHelpers.ts`

**`genInstanceId`** — add `'inline'` to the kind union, producing `inline_llm_1`,
`inline_condition_1`:

```ts
else if (kind === 'inline') base = 'inline_' + sanitize(defName ?? 'node');
```

**`canvasToDoc`** — new node branch (§1.6) plus the edge-typing fix. The current flow-control
edge branch (`CanvasHelpers.ts:154`) must also fire for inline endpoints, and the
condition true/false handle must become the connection label:

```ts
if (srcType === 'flowControl' || tgtType === 'flowControl' ||
    srcType === 'inline'      || tgtType === 'inline') {
  // Condition nodes carry their branch on the sourceHandle ("true"/"false");
  // Router edges carry an operator-assigned label in e.data.label.
  const handleLabel = srcType === 'inline' && e.sourceHandle ? e.sourceHandle : undefined;
  const edgeLabel = handleLabel ?? ((e.data as Record<string, unknown> | undefined)?.label as string | undefined);
  connections.push({ source: e.source, target: e.target, type: 'flow_control', ...(edgeLabel ? { label: edgeLabel } : {}) });
}
```

**`docToCanvas`** — restore inline nodes and re-attach the handle so a reloaded canvas keeps
its true/false wiring:

```ts
} else if (c.definition_ref.kind === 'inline') {
  const nodeType = (c.config.node_type as string) ?? c.definition_ref.name;
  const defaultName: Record<string, string> = { llm: 'LLM', condition: 'Condition' };
  nodes.push({
    id: c.instance_id, type: 'inline', position: pos,
    data: {
      _kind: 'inline', instance_id: c.instance_id, node_type: nodeType,
      display_name: (c.config.display_name as string) || defaultName[nodeType] || nodeType,
      config: c.config,
    } as unknown as Record<string, unknown>,
  });
}
```

and in the connection loop, set `sourceHandle: conn.label` when the source is an inline
condition node. Without this, reloading a saved canvas would render both edges from the same
handle and the next save would lose the labels — a silent data-loss bug.

**Edge id collision:** `docToCanvas` builds ids as `e_${source}_${target}`
(`CanvasHelpers.ts:200`). A condition with both branches pointing at the same target would
produce duplicate ids. Include the label: `e_${source}_${target}${conn.label ? '_' + conn.label : ''}`.

### 6.5 Palette + drop handler — `CanvasBuilderView.tsx`

New "Inline / Logic" palette section below Flow Control, same markup pattern as
`CanvasBuilderView.tsx:506-528`:

```tsx
{([
  { node_type: 'llm'       as const, emoji: '🧠', label: 'LLM',       desc: 'Call a model with a prompt template; output feeds the next node', color: '208,188,255' },
  { node_type: 'condition' as const, emoji: '⑂',  label: 'Condition', desc: 'Branch true/false on an expression over flow variables',          color: '249,115,22' },
]).map(n => ( /* draggable, nodeType: 'inline', nodeData: { node_type } */ ))}
```

Drop handler branch:

```tsx
} else if (nodeType === 'inline' && payload.node_type) {
  const nt = payload.node_type as 'llm' | 'condition';
  const id = genInstanceId('inline', nt, existingIds);
  const names: Record<string, string> = { llm: 'LLM', condition: 'Condition' };
  const defaults: Record<string, Record<string, unknown>> = {
    llm:       { user_prompt: '{{.input}}', output_var: 'output', max_tokens: 1024 },
    condition: { expression: '' },
  };
  setNodes(ns => [...ns, {
    id, type: 'inline', position: pos,
    data: { _kind: 'inline', instance_id: id, node_type: nt, display_name: names[nt], config: defaults[nt] } as unknown as Record<string, unknown>,
  }]);
}
```

Seeding `user_prompt: '{{.input}}'` means a freshly dropped LLM node is immediately valid and
does the obvious thing (pass the upstream output to the model) instead of failing
`llm_no_prompt`.

Also widen the `payload` type on line 334 to include `node_type` (already present for
flow_control, so no change needed) — verify during implementation.

### 6.6 Connection rules — `constants.ts`

```ts
inline: {
  accepts: ['request', 'task', 'signal', 'fc_in', 'fc_out', 'result'],
  emits:   ['fc_out', 'fc_in', 'request', 'task'],
},
```

Same port vocabulary as `flowControl` (`constants.ts:503`) — inline nodes sit in the same
positions in a flow, so any wiring legal for a Router is legal for an inline node.
`CanvasInner.tsx:35` reads `NODE_PORTS[sourceType]`, so an absent entry would silently reject
all connections to inline nodes.

Minimap color — `CanvasInner.tsx:358`: add `n.type === 'inline' ? '#d0bcff' :` to the chain.

> **Blocker found during step 8 (must be fixed in step 10).** `validateConnection`
> (`CanvasInner.tsx:42`) rejects any second edge between the same source/target pair:
> ```ts
> if (edges.some(e => e.source === sourceId && e.target === targetId)) {
>     return `These nodes are already connected`;
> }
> ```
> It does not consider `sourceHandle`. A condition node whose **true and false branches both
> route to the same node** — a legitimate and common shape, e.g. "log either way, then continue" —
> would have its second branch silently refused, and the user gets "already connected" with no
> explanation.
>
> Fix: make the duplicate check handle-aware, comparing `(source, sourceHandle, target)` rather
> than `(source, target)`. That preserves the existing guard for every other node type (their
> `sourceHandle` is undefined on both sides, so the comparison is unchanged) while allowing a
> condition's two distinct handles to reach one target. Note the existing check is also what stops
> accidental duplicate edges, so do not simply delete it.

### 6.7 Properties panel — `cbv/panels/InlineNodePanel.tsx`

**LLM node fields:** Display Name; Provider (`select`: inherit from entry point / anthropic /
openai / groq / ollama / vllm / lmstudio / mock — the exact set `multiLLMFactory` supports,
`dag-worker/main.go:482-493`); Model (text, placeholder "inherit from entry point");
System Prompt (textarea); User Prompt (textarea, hint `Use {{.input}} for the previous
node's output, or {{.varname}} for a named variable`); Output Variable (text, default
`output`); Max Tokens (number); Temperature (number 0–2, **labelled "stored, not yet applied —
see Phase 2"** per §4.3).

**Condition node fields:** Display Name; Expression (textarea, monospace, with three
copyable examples drawn from the Branch node's documented examples,
`internal/agentgen/nodes.go:438-441`):

```
{{eq .output "APPROVED"}}
{{gt (len .output) 100}}
{{contains .summary "error"}}
```

Plus a live read-only summary of which node each branch points to, derived from `edges` — the
panel already receives `nodes` and `edges`, and an operator reading a boolean gate needs to
see both destinations without tracing the graph by eye.

All writes go through the existing `updateFcConfig`-style setter (rename `updateNodeConfig`),
which sets `config` on the node and flips `setIsDirty(true)` + `setLogoResult('none')` —
identical to the Router/HIL path, so autosave and the publish gate pick inline edits up with
no extra wiring.

### 6.8 Backend-mismatch warning (§4.2)

In `CanvasBuilderView`, a non-blocking amber banner below the validation banner:

```tsx
{executionBackend !== 'temporal' && nodes.some(n => n.type === 'inline' || n.type === 'flowControl') && (
  <div /* amber banner */>
    Inline and flow-control nodes only execute on the Temporal backend. This app is set to
    Local (Orchestrator) — the canvas graph is not executed. Switch Execution to
    “Temporal DAG” to run this flow.
  </div>
)}
```

Advisory, not blocking: an operator may legitimately be mid-build. The wording names the
consequence rather than just the setting, and it covers flow-control nodes too — which have
had this same silent no-op since Phase 2 and never warned.

---

## 7. Validation summary

### 7.1 Compiler rules (`appflow.Validate`)

| Code | Node | Condition | Severity |
|---|---|---|---|
| `condition_no_expression` | condition | `expression` empty | error |
| `condition_edge_count` | condition | outgoing edges ≠ 2 | error |
| `condition_missing_labels` | condition | outgoing edges not labelled `true` + `false` | error |
| `llm_no_prompt` | llm | both `system_prompt` and `user_prompt` empty | error |
| `llm_orphan` | llm | no incoming and no outgoing edges | error |
| `unknown_inline_node` | inline | `definition_ref.name` not `llm`/`condition` | error |

Not validated, with reasons in §3.3: provider key availability (would break `Validate`'s
DB-free purity), template syntax (`{{`-balance checking is a poor proxy for
`template.Parse`; a parse error surfaces as a clear non-retryable `ConditionRenderFailed`).

### 7.2 Wire `appflow.Validate` into the publish/validate endpoint

Per §1.13, `ValidateDefinition` currently never runs the compiler, so every rule in §7.1 —
plus the existing `router_no_edges`, `fork_insufficient_branches`, `join_insufficient_branches`
— is invisible at publish time. Phase 1 closes this in `go/internal/admin/service/publish.go`,
appended to `ValidateDefinition` after the existing structural checks:

```go
// Compiler-level structural validation. Only meaningful for canvases that
// execute as an AppFlow graph; a local-backend app ignores the graph entirely.
if doc.ExecutionBackend == "temporal" {
    // Agent resolution is not available pre-publish (_resolved_agent_ids is
    // stamped AT publish), so compile with an empty map and drop
    // unresolved_agent — the registry check above already covers agent identity.
    if spec, cErr := appflow.Compile(raw, map[string]string{}); cErr == nil {
        for _, ve := range appflow.Validate(spec) {
            if ve.Code == "unresolved_agent" {
                continue
            }
            errs = append(errs, ValidationError{
                InstanceID: ve.InstanceID, Code: ve.Code, Message: ve.Message,
            })
        }
    }
}
```

Three deliberate choices here:

- **Gated on `execution_backend == "temporal"`.** A local-backend canvas does not execute the
  graph (§4.1); failing its publish on fork/join topology would block operators on rules that
  do not apply to them. This also means the new rules cannot regress any existing local app.
- **`unresolved_agent` filtered.** `_resolved_agent_ids` is stamped *during* publish
  (`dal/publish.go:100`), so it is absent when validating a draft — every agent node would
  report unresolved. Agent identity is already validated by the registry resolution loop above.
- **Compile errors ignored, not surfaced.** A `Compile` failure here means malformed JSON or a
  bad `schema_version`, both of which the existing checks report with better messages.

This is a behavioural change to an existing endpoint: canvases with pre-existing Temporal-backend
topology errors will start failing publish. That is the intent — they fail at runtime today —
and it needs a line in the migration notes (§10).

---

## 8. Test plan

Per `go/CLAUDE.md`: every change needs a test, `TEST_INDEX.md` updated in the same commit,
`go test ./...` green before commit.

### 8.1 New Go tests

**`internal/appflow/compiler_test.go`** — extends S1-112 (currently AF-01..14, AF-C-01..04):

| ID | Test | Asserts |
|---|---|---|
| AF-15 | `TestCompile_InlineLLMNode` | `kind:"inline"`/`name:"llm"` → `Kind=="llm"`, config preserved verbatim |
| AF-16 | `TestCompile_InlineConditionNode` | → `Kind=="condition"` |
| AF-17 | `TestCompile_InlineUnknownName` | `name:"lmm"` → `Kind=="inline"` (not the raw name) |
| AF-18 | `TestCompile_ConditionEdgeLabels` | `true`/`false` labels survive into `AppFlowEdge.Label` |
| AF-19 | `TestValidate_ConditionNoExpression` | `condition_no_expression` |
| AF-20 | `TestValidate_ConditionEdgeCount` | 1 edge and 3 edges both → `condition_edge_count` |
| AF-21 | `TestValidate_ConditionMissingLabels` | 2 edges labelled `yes`/`no` → `condition_missing_labels` |
| AF-22 | `TestValidate_LLMNoPrompt` | `llm_no_prompt`; and passes when only `system_prompt` set |
| AF-23 | `TestValidate_LLMOrphan` | `llm_orphan` for a disconnected LLM node |
| AF-24 | `TestValidate_UnknownInlineNode` | `unknown_inline_node` |
| AF-25 | `TestValidate_InlineValidTopology` | LLM → Condition → 2 agents produces zero errors |

**`internal/appflow/inline_test.go`** (new):

| ID | Test | Asserts |
|---|---|---|
| AF-IN-01 | `TestRenderFlowTemplate_Substitution` | `{{.input}}` / `{{.summary}}` interpolate |
| AF-IN-02 | `TestRenderFlowTemplate_MissingVar` | missing key → `""`, no error (see note below) |
| AF-IN-03 | `TestRenderFlowTemplate_ParseError` | unbalanced `{{` returns an error |
| AF-IN-04 | `TestIsTruthy` | table: `"true"`→t, `"TRUE"`→t, `"1"`→t, `"x"`→t; `""`/`"false"`/`"0"`/`"<no value>"`/`"  "`→f |

> **Correction (verified empirically during step 1).** An earlier draft of this plan claimed a
> missing template variable renders as `<no value>`. That is true only for `map[string]any` and
> struct data. Because `FlowVars` is `map[string]string`, `missingkey=zero` substitutes the zero
> value of the map's *value type* — the empty string. Verified:
> `map[string]string` → `"hello "`; `map[string]any` → `"hello <no value>"`.
> Behaviourally this is benign and slightly better: `isTruthy("")` is already `false`, so a
> condition over an unset variable cleanly takes the false branch. `<no value>` stays in
> `isTruthy`'s guard list defensively in case `FlowVars` ever widens to `map[string]any` (§11).

> **Correction 2 (caught by test AF-IN-02b during step 1).** `contains` is **not** a Go
> `text/template` builtin — it is a Sprig/Helm function. An earlier draft listed
> `{{contains .summary "error"}}` as a properties-panel example (§6.7), which would have
> shipped a copyable hint that fails at run time with `function "contains" not defined`.
> Fixed by registering a small pure-function map (`flowFuncs`, above) providing `contains`,
> `hasPrefix`, `hasSuffix`, `lower`, `upper`, `trim`. Test AF-IN-02b now asserts every
> expression form the UI advertises actually renders — including over missing variables.
> **Rule for later steps: any expression added to the UI examples must have a case in AF-IN-02b.**
| AF-IN-05 | `TestInlineLLMConfig_JSONRoundTrip` | all fields incl. `*float64` temperature survive marshal/unmarshal |
| AF-IN-06 | `TestInlineConditionConfig_JSONRoundTrip` | expression survives |

**`internal/appflow/workflow_test.go`** — activity-level (matching the existing
AF-WF-04..06 pattern for `InvokeAgentActivity`, which tests the activity method directly
with a fake dependency rather than spinning a workflow test environment):

| ID | Test | Asserts |
|---|---|---|
| AF-WF-10 | `TestInlineLLMActivity_Success` | renders prompts, calls `InlineLLMCaller`, returns text + `OutputVar` |
| AF-WF-11 | `TestInlineLLMActivity_NilCaller` | non-retryable `NoInlineLLMCaller` |
| AF-WF-12 | `TestInlineLLMActivity_RenderError` | bad template → non-retryable, caller never invoked |
| AF-WF-13 | `TestInlineLLMActivity_UserPromptFallsBackToInput` | empty `user_prompt` → `vars["input"]` used |
| AF-WF-14 | `TestInlineLLMActivity_NoKeyInInput` | reflect over `InlineLLMActivityInput` asserting no field name contains `key`/`token`/`secret` — guards the "no secrets in workflow history" invariant |
| AF-WF-15 | `TestInlineLLMActivity_StreamPublishesToken` | `Stream:true` → fake `StreamPub` receives one `type:"token"` entry on `them:dash:run:{id}:stream` |
| AF-WF-16 | `TestInlineLLMActivity_MaxTokensDefault` | `MaxTokens:0` → caller receives 1024 |

AF-WF-14 is worth its weight: "secrets never in Temporal history" is a stated architectural
invariant (`workflow.go:122`) enforced today only by convention.

**`internal/admin/service/publish_test.go`** — extends the publish suite:

| ID | Test | Asserts |
|---|---|---|
| PUB-IN-01 | `TestValidateDefinition_InlineKindSkipsRegistry` | `kind:"inline"` publishes without a `component_definitions` row (no `component_not_found`) |
| PUB-IN-02 | `TestValidateDefinition_InlineMissingVersion` | `version:0` → `missing_version` (builtin exemption does not bypass structural checks) |
| PUB-IN-03 | `TestValidateDefinition_CompilerErrorsSurfaced` | temporal backend + 1-edge condition → `condition_edge_count` in the report |
| PUB-IN-04 | `TestValidateDefinition_LocalBackendSkipsCompilerRules` | same doc with `execution_backend:"local"` → valid |
| PUB-IN-05 | `TestValidateDefinition_UnresolvedAgentFiltered` | agent node pre-publish does not emit `unresolved_agent` |

**`cmd/dag-worker`** — `go build ./cmd/dag-worker/` must pass (per the trigger map); the
`dbLLMCaller.Complete` key-resolution path is DB-dependent and covered by the E2E test rather
than a unit test, consistent with how `dbRouterLLMCaller` is treated today.

### 8.2 E2E test

`scripts/tests/test_42_appflow_inline_nodes.py`, modelled on
`test_40_appflow_canvas_e2e.py` (17 checks) and `test_41_appflow_fork_join.py` (13):

1. Login, create app + WS entry point.
2. Create a definition: `EP → orchestrator → LLM(mock provider) → Condition → agent_a | agent_b`,
   `execution_backend: "temporal"`.
3. `POST validate` → expect `valid: true`.
4. Negative: strip one condition edge, re-validate → expect `condition_edge_count`, and assert
   `instance_id` matches the condition node (this is what drives canvas highlighting).
5. Negative: clear both prompts on the LLM node → expect `llm_no_prompt`.
6. Restore, publish → revision increments.
7. Open WS, send a message that the mock provider's reply satisfies → assert `done`, and assert
   the run's final text came from the `true` branch agent.
8. Send a message taking the `false` branch → assert the other agent's text.
9. Assert at least one `token` event arrived on the WS before `done`.
10. Assert `them.runs.status = 'completed'` for both runs.
11. Cleanup: delete app (cascades).

The `mock` provider (`multiLLMFactory` case `"mock"`, `dag-worker/main.go:483`) keeps this test
zero-cost and offline — but note its replies are randomly chosen from five canned strings
(`main.go:558`), so step 7's condition expression must test something stable
(`{{gt (len .output) 0}}`) rather than matching reply text. Branch-specific assertions use two
different agents rather than two different LLM outputs.

### 8.3 Regression surface

`go test ./...` full suite (1096 tests at baseline, expect ~1130). Specifically:
`./internal/appflow/...` (trigger map), `./internal/admin/...` (publish change),
`./internal/ws/... ./internal/sse/...` (untouched but they own the dispatch switch), and
`go build ./cmd/dag-worker/`. Then `go test -race ./...` before merge.

Frontend: `npm run build` in `frontend/` (type-check is the real gate — the `CanvasNodeData`
union change will surface any unhandled node type).

### 8.4 Doc updates required in the same commits

- `go/TEST_INDEX.md` — new rows (S1-112 extension, new S1-115 for `inline_test.go`,
  publish-suite rows), suite totals, trigger map entry for `internal/appflow/inline.go`.
- `docs/CURRENT.md` — Phase C slice: what shipped, HEAD, migration notes, next task.
- `docs/LESSONS.md` — two entries earned by this design work: (1) `ValidateDefinition` did not
  run `appflow.Validate`, so canvas topology errors only surfaced at run time; (2) publish
  registry resolution is exempted by a hardcoded kind check, so any new `definition_ref.kind`
  must be added to `isBuiltinKind` or every publish containing it fails.
- `docs/INDEX.md` — add this plan.
- No `docs/SCHEMA.md` or `docs/REDIS.md` change: no new table, column, or Redis key
  (`them:dash:run:{runID}:stream` is pre-existing).

---

## 9. Implementation order

Each step ends green (`go test ./...` / `npm run build`) and is independently committable.

| # | Step | Files | Gate |
|---|---|---|---|
| 1 | Config types + template helpers | `internal/appflow/inline.go` (new) | AF-IN-01..06 |
| 2 | Compiler: kinds, edge labels, validation | `internal/appflow/compiler.go` | AF-15..25 |
| 3 | Activity types + `InlineLLMActivity` + workflow cases (main loop **and** `walkBranch`) | `internal/appflow/workflow.go` | AF-WF-10..16 |
| 4 | Worker wiring: `dbLLMCaller.Complete`, register activity | `cmd/dag-worker/main.go` | `go build`, full suite |
| 5 | Publish: `isBuiltinKind`, wire `appflow.Validate` | `internal/admin/service/publish.go` | PUB-IN-01..05 |
| 6 | **Split properties panel** (no behaviour change) | `cbv/` + `cbv/panels/*` | `npm run build`, manual smoke on all 5 existing node types |
| 7 | Frontend types + node rendering | `types.ts`, `apiTypes.ts`, `CanvasNodes.tsx` | build |
| 8 | Serialisation + palette + ports + minimap | `CanvasHelpers.ts`, `CanvasBuilderView.tsx`, `constants.ts`, `CanvasInner.tsx` | build; save→reload round-trip preserves true/false wiring |
| 9 | `InlineNodePanel` | `cbv/panels/InlineNodePanel.tsx` | build |
| 10 | Backend-mismatch warning | `CanvasBuilderView.tsx` | build |
| 11 | E2E + docs | `scripts/tests/test_42_*.py`, `TEST_INDEX.md`, `CURRENT.md`, `LESSONS.md`, `INDEX.md` | E2E green, full suite |

Step 6 is the one step with no user-visible change and real regression risk — keep it in its
own commit so a bisect can isolate it.

---

## 10. Deployment notes

- **No DB migration.**
- Rebuild + **force-recreate** `them-dag-worker` (new activity registered at startup):
  ```bash
  docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
    --profile temporal up -d --force-recreate them-dag-worker
  docker logs them-dag-worker --tail 5   # confirm appflow-worker polling
  ```
- Rebuild + force-recreate `them-go-bridge` (publish validation change).
- Rebuild `them-frontend`.
- `build` + `restart` is **not** sufficient — see the Phase A lesson in `docs/LESSONS.md`.
- **Behavioural change to flag to operators:** publish now enforces AppFlow topology rules for
  `execution_backend: "temporal"` apps (§7.2). Existing published apps are unaffected until
  their next re-publish; a canvas with a malformed router/fork/join will then be rejected at
  publish instead of failing at run time. Recommend re-publishing Temporal-backend apps
  deliberately after deploy to surface any latent topology errors in the builder.
- No compiled-spec cache to invalidate: `AppFlowSpec` is recompiled from
  `EPConfig.ActiveDefinitionJSON` on **every connection** (`ws/handler.go:507`); only the raw
  definition is persisted (`dal/publish.go:124`). Compiler fixes therefore take effect for
  existing published apps on their next run — no republish needed except to refresh
  `_resolved_agent_ids`.
  (Aside: the doc comment at `internal/appflow/compiler.go:16` claims the spec is "serialised
  into `application_definitions.definition` for use by the workflow executor". That is stale —
  no table or column holds an `AppFlowSpec`. Worth correcting in step 2, per the CLAUDE.md rule
  to trust code over docs and fix the doc when they diverge.)

---

## 11. Explicitly deferred to Phase 2

Named here so none of it is mistaken for an oversight:

1. **Local-backend inline execution** (§4.2) — needs an in-process AppFlowSpec walker;
   larger than both Phase 1 node kinds combined. Phase 1 ships the warning banner instead.
2. **True incremental token streaming** (§5.1) — needs a streaming variant of
   `agentgen.LLMProvider`, which is shared with the agent builder. Phase 1 emits one `token`
   event carrying the full response.
3. **`temperature` application** (§4.3) — same shared-interface blocker as (2). Phase 1
   stores the value and labels the field as not-yet-applied.
4. **Tool/MCP inline node** — needs per-app MCP server attachment resolution + a new activity
   with its own auth path.
5. **Transform inline node** — port `agentgen/transform` function steps; also the point at
   which `FlowVars` should likely widen from `map[string]string` to `map[string]any` (§1.3).
   Per the recorded convention, transform outputs come only from `functions[].output_var` —
   no separately declared output vars.
6. **`run_steps` recording + per-node stream events for all AppFlow node kinds** (§5.2, §5.1) —
   one slice: add a recorder to `AppFlowActivities` and emit `node_started`/`node_completed`
   for every kind, so an AppFlow run's detail view stops being empty where a local run's is
   fully populated. Coherent only if agent nodes are covered too; the `run_steps` row shape and
   the existing unused hooks are documented so that slice starts from a decision, not a question.
7. **Per-node LLM overrides from App Runtime** — `agentgen` supports
   `NodeLLMOverrides` keyed by step id (`dag-worker/main.go:438`); the AppFlow equivalent
   would let an operator retarget an inline node's provider without editing the canvas.
