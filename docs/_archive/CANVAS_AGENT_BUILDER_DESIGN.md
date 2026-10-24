# Canvas Agent Builder — Design Specification
# Last updated: 2026-08-19
# Status: Forward-looking spec for Phase A onwards. Phases 1–4 are complete.

---

## 1. Purpose and Scope

The Canvas Agent Builder lets a platform user design, configure, and publish an A2A agent entirely through a visual UI — no Go code, no Dockerfile, no hand-written JSON. The user defines what the agent does (its skills and pipeline steps), and the platform compiles and runs it via the generic `them-agent-runtime` service.

### What is done (Phases 1–4)

- `them-agent-runtime` (`go/cmd/agent-runtime/`) is live: stateless Go binary, serves all canvas agents over A2A JSON-RPC 2.0 at `http://them-agent-runtime:9300/agents/{slug}`
- `go/internal/agentgen/` has the full type system: `AgentSpec`, `SkillSpec`, `StepSpec`, all step config structs, the compiler, interpreter, and Redis task store
- The frontend builder (`frontend/src/app/admin/agents/builder/page.tsx`) has two-level ReactFlow canvas: agent-level (AGENT ROOT + SKILL nodes) and skill-level (pipeline step nodes with edges)
- Save, load, validate, and publish are all wired end-to-end through the Go backend and DB

### The gap that remains

Step nodes in the pipeline canvas save `config: {}` — the right-hand properties panel shows only Step ID, Label, and Type. There is no UI to fill in LLM prompts, HTTP URLs, transform expressions, or response variable bindings. Every published agent therefore runs with empty step config, which works only for trivial echo-style pipelines.

### Scope of this document

This document specifies the next four development phases:

| Phase | Scope | Backend changes |
|---|---|---|
| A | Step config panels (the core gap) | None |
| B | Skill card editor + data flow variable visualization | None |
| C | Data part input mode support in runtime | Go |
| D | A2A SDK adoption | Go — ready (repo is Go 1.25, SDK not yet imported) |

Phases A and B are frontend-only. Phase C is a targeted Go change to `go/cmd/agent-runtime/main.go`. Phase D was previously deferred pending Go 1.25 — the repo is now `go 1.25.0` (bumped during Phase 1 of the canvas work), so that blocker is resolved. The SDK (`github.com/a2aproject/a2a-go/v2`) has not yet been added to `go/go.mod`. Phase D can be scheduled as the next Go session after Phase C.

---

## 2. The A2A Agent Mental Model

Understanding this distinction is critical before designing the UI. The canvas agent builder is fundamentally different from the application canvas.

### Application canvas (what already exists)

The application canvas edits a **graph of agents and orchestrators**: node A calls node B, which calls nodes C and D. The topology is what the canvas expresses. Each node is an external service or agent the orchestrator routes to.

### Agent canvas (this feature)

The agent canvas edits a **single agent's internal pipeline**: a sequence of steps that runs when someone calls this agent. The agent presents itself to the world as a black box with a named skill. Internally, it executes steps — read input, call an LLM, fetch a URL, transform data, return a result.

```
Caller (orchestrator)
  │
  │  A2A message/send  →  skill: "analyze_data"
  ▼
them-agent-runtime
  │
  │  Loads AgentSpec from DB
  │  Runs pipeline:
  │    [input] → [llm] → [transform] → [response]
  ▼
Artifact returned to caller
```

The pipeline is the agent's private implementation. The **skill card** is its public interface.

### The three layers

```
Agent (root)
├── agent_card:  name, version, description, capabilities, endpoint_url
│
├── Skill 1
│   ├── skill_card: name, description, input_modes, output_modes, tags, examples
│   └── Pipeline: input → llm → transform → response
│
└── Skill 2
    ├── skill_card: ...
    └── Pipeline: input → http → response
```

The canvas builder edits all three layers. The agent root and skill cards form the **A2A contract** — what external callers (and their LLMs) see. The pipeline is the **internal implementation**.

---

## 3. Skill Description and input_modes — The A2A Contract

### Why this matters

When the orchestrator's LLM decides which agent to call, it reads `AgentSkill.description` and `AgentSkill.input_modes` from the agent card. This is the A2A contract — not the step config, not the pipeline.

A poorly written description means the orchestrator may call the wrong skill, pass the wrong data shape, or ignore the agent entirely. A well-written description with examples means the LLM knows exactly when and how to call the skill.

### Builder UX implications

The **Skill node properties panel** (visible when a skill node is selected in agent-level view) must treat the skill card fields as first-class:

- `description` — multiline textarea, labeled "Skill Description (what the orchestrator LLM sees)". Hint: "Describe what this skill does, what input it expects, and what it returns. This is the LLM's contract — be specific."
- `examples` — a list of example inputs the orchestrator should send. Add/remove rows. Labeled "Example inputs (shown to LLM for few-shot guidance)".
- `input_modes` — MIME type selector. Controls what part type the caller sends. See §6.
- `output_modes` — MIME type selector.
- `tags` — comma-separated tag editor.

The skill card fields are **external-facing**. Step config fields are **internal**. The UI should visually separate them — for example, the skill card fields appear in a "Skill Contract" section at the top of the panel with a distinct background, and the "Edit Pipeline" button sits below that.

### Contrast: step config

Step config (system prompt, URL, expressions) is the pipeline's private implementation. The caller never sees it. A change to system prompt does not change the A2A contract. This distinction must be clear in the UI — step config panels should never be reachable from the agent-level view.

---

## 4. Step Config Panel Design

### 4.1 Data model changes required

Three changes to `frontend/src/app/admin/agents/builder/page.tsx`:

**Extend `StepData` interface** (line ~71):
```ts
interface StepData {
  step_id: string;
  step_type: string;
  label: string;
  config: Record<string, unknown>; // ADD THIS — was always {}
}
```

**Fix `buildDefinitionDoc`** (line ~315): change `config: {}` to `config: stepd.config ?? {}`.

**Fix `loadDefinitionDoc`** (line ~283): when rebuilding pipeline nodes from a saved definition, restore `config` from the step JSON:
```ts
data: { step_id: step.id, step_type: step.type, label: step.type, config: step.config ?? {} }
```

Add a `updateStepConfig(key: string, value: unknown)` helper that merges into the config sub-object of the selected node:
```ts
function updateStepConfig(key: string, value: unknown) {
  if (!selectedNode) return;
  const updater = activeView === 'skill' ? setSkillNodes : setAgentNodes;
  updater(prev => prev.map(n =>
    n.id !== selectedNode.id ? n :
    { ...n, data: { ...n.data, config: { ...(n.data.config as Record<string, unknown> ?? {}), [key]: value } } }
  ));
  setSelectedNode(prev => prev ? {
    ...prev, data: { ...prev.data, config: { ...(prev.data.config as Record<string, unknown> ?? {}), [key]: value } }
  } : prev);
  setDirty(true);
}
```

### 4.2 Input step

Reads the incoming A2A message parts and binds values to named pipeline variables. The output of this step is always a set of named vars — downstream steps reference them by name.

**Config shape** (`go/internal/agentgen/spec.go` `InputStepConfig`):
```json
{ "bindings": { "text": "user_query" } }
```

**Panel fields:**

| Field | UI element | Notes |
|---|---|---|
| Text binding variable name | Text input | Default `input`. This is the var name downstream steps use to reference the user's message. |

Label hint: "The user's message text will be bound to this variable name. Use `{{.variable_name}}` in downstream steps."

When `input_modes` includes `application/json`, additional bindings for structured fields will be supported in Phase C (see §7).

### 4.3 LLM step

Calls an LLM with a system prompt and user prompt, stores the response in a named variable.

**Config shape** (`LLMStepConfig`):
```json
{
  "model": "claude-haiku-4-5",
  "system_prompt": "You are a helpful assistant.",
  "user_prompt": "{{.user_query}}",
  "max_tokens": 1024,
  "output_var": "output",
  "provider_key_slot": ""
}
```

**Panel fields:**

| Field | UI element | Default | Notes |
|---|---|---|---|
| Model | Dropdown | `claude-haiku-4-5` | Options: `claude-haiku-4-5`, `claude-sonnet-5`, `claude-opus-4-8`, `claude-fable-5` |
| System Prompt | Textarea (large) | `""` | Go template. Hint: "Static instructions. Use `{{.var}}` to include pipeline variables." |
| User Prompt | Textarea | `""` | Go template. Hint: "The LLM's user message. If empty, falls back to `{{.input}}` automatically." |
| Max Tokens | Number input | `1024` | Range 1–32000 |
| Output Variable | Text input | `output` | Name of the var that receives the LLM's reply |
| API Key Slot | Dropdown (from agent's credential_slots) or blank | `""` | If blank, platform key is used. Only slot names appear here — never key values. |

Template hints are visible as placeholder text and a collapsible "Available variables" chip that lists variables set by upstream steps.

**Security:** `provider_key_slot` holds only a slot name (e.g. `"anthropic_key"`), never the key value. The key is resolved from `app_agent_bindings` at runtime. If `provider_key_slot` is empty the platform's `ANTHROPIC_API_KEY` is used. The config panel must never show or accept actual API key values.

### 4.4 HTTP step

Makes an outbound HTTP request, optionally injects a credential, and extracts fields from the JSON response.

**Config shape** (`HTTPStepConfig`):
```json
{
  "method": "GET",
  "url_template": "https://api.example.com/data/{{.user_query}}",
  "headers": { "Accept": "application/json" },
  "body_template": "",
  "timeout_seconds": 30,
  "credential_slot": "my_api_key",
  "credential_inject": {
    "mode": "header",
    "header_name": "Authorization",
    "value_template": "Bearer {credential}"
  },
  "extractions": [
    { "var": "result_text", "json_path": "$.data.text" }
  ]
}
```

**Panel fields:**

| Field | UI element | Notes |
|---|---|---|
| Method | Dropdown | GET, POST, PUT, PATCH, DELETE |
| URL Template | Text input | Go template. Hint: "Use `{{.var_name}}` to interpolate pipeline variables." |
| Headers | Key→value row editor | Static, non-secret headers only (Accept, Content-Type, etc.). **Secret values must use Credential Slot, not this field.** |
| Body Template | Textarea | Go template. Shown only for POST/PUT/PATCH. |
| Timeout (seconds) | Number input | Default 30 |
| Credential Slot | Dropdown (from agent's credential_slots) or blank | Which declared slot's resolved value to inject. Only slot names shown. |
| Inject Mode | Dropdown | `header` (default), `query`, `basic`. Shown only when a slot is selected. |
| Header Name | Text input | Shown when inject mode = `header`. Default `Authorization`. |
| Value Template | Text input | Shown when inject mode = `header`. Default `Bearer {credential}`. The literal string `{credential}` is replaced with the slot value at runtime. |
| Query Param | Text input | Shown when inject mode = `query`. |
| Response Extractions | Add/remove rows: Var name + JSONPath | Each row extracts one field from the JSON response into a named pipeline variable. Example JSONPath: `$.data.items[0].title` |

**Security note** (displayed in the panel as a static warning): "Headers set here are stored in the agent definition and must not contain secret values. Use a Credential Slot for API keys, tokens, and passwords."

### 4.5 Transform step

Applies Go template expressions to pipeline variables, producing new named variables.

**Config shape** (`TransformStepConfig`):
```json
{
  "expressions": {
    "greeting": "Hello, {{.user_query}}!",
    "summary": "Result: {{.output}}"
  }
}
```

**Panel fields:**

An add/remove row table. Each row has:
- Output variable name (text input, left column)
- Go template expression (text input, right column). Hint: "Go `text/template` syntax. `{{.var_name}}` references an upstream variable."

Rows can be added and removed. At least one row is required if the step is included.

Hint text below the table: "Variables defined here are available in downstream steps."

### 4.6 Response step

Terminates the pipeline and emits the result as an A2A artifact.

**Config shape** (`ResponseStepConfig`):
```json
{
  "from_var": "output",
  "media_type": "text/plain"
}
```

**Panel fields:**

| Field | UI element | Notes |
|---|---|---|
| From Variable | Dropdown or text input | Variable name whose value becomes the artifact text. Ideally a dropdown populated from variables set by upstream steps in this pipeline. |
| Media Type | Dropdown | `text/plain` (default), `text/html`, `text/markdown` |

The "From Variable" dropdown approach: compute the set of output variables defined by all steps that appear before this one in the pipeline (based on edge ordering). For Phase A, a plain text input is acceptable; Phase B can add the dropdown.

---

## 5. Skill-Level Canvas: Data Flow Visualization

### Current state

Step nodes show only their type label and step ID. There is no visual indication of which variables a step produces or consumes.

### Proposed improvements (Phase B)

**Step node output label:** Each step node should display the key output variable it writes, below the type label:

| Step type | Output label shown on node |
|---|---|
| input | `→ {bindings.text or "input"}` |
| llm | `→ {output_var or "output"}` |
| http | `→ {first extraction var or "http_response"}` |
| transform | `→ {comma-separated output var names}` |
| response | `(terminal)` |

This is purely a display change — the `StepData` already has the config after Phase A lands, so the node render function can read it.

**Variable scope indicator in config panel:** In Phase B, the step config panel should show a collapsible "Variables in scope" section listing every variable name that upstream steps produce. This helps the user write correct template expressions without having to mentally trace the pipeline.

Computing variables in scope: traverse the pipeline graph from the start node to the currently selected step, collect all output variable names defined by each step's config. The compiler already does a similar traversal for cycle detection — this can reuse the same edge ordering.

---

## 6. Skill Card Editor

### Currently missing fields

The skill node's right panel (visible when a skill is selected in agent-level view) currently offers Name, Description, and an "Edit Pipeline" button. It is missing:

- `tags` — string list
- `examples` — string list (example inputs the LLM should use for few-shot selection)
- `input_modes` — MIME type list (the A2A contract for what part type the caller sends)
- `output_modes` — MIME type list

### input_modes — the critical one

`input_modes` determines what the calling orchestrator sends to this agent:

| input_modes value | Orchestrator sends | Runtime reads |
|---|---|---|
| `["text/plain"]` | `{"kind": "text", "text": "..."}` part | `inputText` string (current behavior) |
| `["application/json"]` | `{"kind": "data", "data": {...}}` part | Structured fields as pipeline vars (Phase C) |

For Phase A/B, only `text/plain` is supported end-to-end. The builder should allow setting `input_modes` to `application/json` but show a warning: "Data part input requires Phase C runtime support."

### Skill card editor panel (Phase B)

```
[ Skill Contract ]  ← section header, distinct background

Name:         [___________________________]
Description:  [___________________________]  ← multiline
              [___________________________]  ← hint: "This is what the orchestrator LLM reads"

Tags:         [tag1] [tag2] [+ add]

Example inputs:  ← hint: "Sample inputs shown to the orchestrator LLM"
  [_______________________________] [x]
  [_______________________________] [x]
  [+ add example]

Input modes:  [ ] text/plain  [ ] application/json
Output modes: [ ] text/plain  [ ] text/html  [ ] text/markdown

[ Edit Pipeline → ]  ← button, below contract section
```

In Phase A the Name, Description, and "Edit Pipeline" button from the existing panel are sufficient. The full card editor above is Phase B scope.

---

## 7. Data Part Input Mode (Phase C)

### The gap

When a skill declares `input_modes: ["application/json"]`, the calling orchestrator sends a structured JSON data part:

```json
{
  "parts": [
    { "kind": "data", "data": { "city": "Tel Aviv", "units": "celsius" } }
  ]
}
```

The current `handleMessageSend` in `go/cmd/agent-runtime/main.go` only reads `kind:"text"` parts:

```go
for _, p := range params.Message.Parts {
    if p.Kind == "text" && p.Text != "" {
        inputText = p.Text
        break
    }
}
```

A data part is silently ignored — `inputText` is empty, and the LLM step falls back to `vars["input"]` which is also empty.

### Phase C fix

Extend `handleMessageSend` to parse data parts:

```go
dataVars := map[string]any{}
for _, p := range params.Message.Parts {
    switch p.Kind {
    case "text":
        if inputText == "" { inputText = p.Text }
    case "data":
        if err := json.Unmarshal(p.RawData, &dataVars); err == nil {
            // structured fields available as pipeline vars
        }
    }
}
```

Merge `dataVars` into the initial `PipelineVars` alongside `"input"`:
```go
vars := agentgen.PipelineVars{"input": inputText}
for k, v := range dataVars { vars[k] = v }
```

This means a skill receiving `{"city": "Tel Aviv"}` will have `vars["city"] = "Tel Aviv"` — the LLM step can use `{{.city}}` in its user prompt template directly.

### Wire format note

The a2a-go/v2 SDK uses clean Go types with no protobuf dependency: `a2a.NewDataPart(any)` produces a `{"kind": "data", "data": ...}` JSON part. When the repo moves to Go 1.25 and adopts the SDK (Phase D), `p.RawData` above becomes `p.Data` (an `any` already unmarshalled). The hand-rolled path and the SDK path produce identical wire JSON, so agents built on Phase C are forward-compatible with Phase D.

---

## 8. Implementation Plan

### Phase A — Step config panels (frontend only, one session)

**Files changed:** `frontend/src/app/admin/agents/builder/page.tsx` only.

**Work items:**
1. Extend `StepData` interface: add `config: Record<string, unknown>`
2. Add `updateStepConfig(key, value)` helper (see §4.1)
3. Fix `buildDefinitionDoc` line ~315: `config: stepd.config ?? {}`
4. Fix `loadDefinitionDoc` line ~283: restore `config` from saved step
5. Replace the step properties panel (lines ~751–773) with a `StepConfigPanel` component that switches on `d.step_type` and renders the type-specific fields described in §4.2–§4.6
6. For the credential slot picker in LLM and HTTP panels: read `credentialSlots` from the agent root node's data (already in `agentNodes`) and render as a dropdown

**Tests:** `go test ./...` (unchanged — no Go changes). Frontend `tsconfig` build must be clean.

**Definition of done:** Create a canvas agent with an LLM step. Set system prompt, model, and leave user prompt empty. Publish. Call the agent through the orchestrator playground. Verify the LLM receives the correct system prompt and falls back to `{{.input}}` for the user message.

### Phase B — Skill card editor + data flow visualization (frontend only, one session)

**Files changed:** `frontend/src/app/admin/agents/builder/page.tsx` only.

**Work items:**
1. Extend `SkillData` interface: add `tags`, `input_modes`, `output_modes`, `examples`
2. Fix `buildDefinitionDoc` and `loadDefinitionDoc` to round-trip these fields
3. Expand skill node properties panel to show full skill card editor (§6)
4. Update `StepNode` render function to display output variable name below type label (§5)
5. Add "Variables in scope" collapsible in step config panel

**Tests:** Same as Phase A.

### Phase C — Data part input mode in runtime (Go, one session)

**Files changed:** `go/cmd/agent-runtime/main.go`

**Work items:**
1. Extend the `Parts` struct in `handleMessageSend` to include `Kind:"data"` with `RawData json.RawMessage`
2. Parse data parts and merge fields into `PipelineVars`
3. Update `go/TEST_INDEX.md`

**New tests required:** `TestHandleMessageSend_DataPart` — verify that a `kind:"data"` part with a JSON object makes its fields available as pipeline vars.

**Definition of done:** Skill with `input_modes: ["application/json"]`. Send `{"kind": "data", "data": {"city": "Paris"}}`. LLM step's user prompt `{{.city}}` renders to `"Paris"`.

### Phase D — A2A SDK adoption

**Status: ready to schedule.** The Go 1.25 blocker is resolved — `go/go.mod` is `go 1.25.0` (bumped during Phase 1 of the canvas work). `github.com/a2aproject/a2a-go/v2` has not yet been added to `go/go.mod`; it can be added with a single `go get` without touching other binaries or Dockerfiles.

**What changes when the SDK is adopted:**

The `AgentExecutor` interface (what we implement in `go/cmd/agent-runtime/main.go`) becomes:
```go
type AgentExecutor interface {
    Execute(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
    Cancel(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
}
```

Part types move from hand-rolled structs to `a2a.NewTextPart()`, `a2a.NewDataPart(any)`, `a2a.NewFileURLPart(url, mimeType)` — no protobuf dependency. The AgentCard wire format is identical to what we hand-roll today, so published agents remain compatible without republishing.

**Phase D is not a prerequisite for A, B, or C.** Schedule it as the first Go session after Phase C ships. It replaces the hand-rolled JSON-RPC dispatch in `go/cmd/agent-runtime/main.go` with the SDK handler and cleans up the manual Part parsing added in Phase C.

---

## 9. What NOT to Build

The following are explicitly out of scope and should not be started without a new design decision:

**Per-agent Docker containers (topology 2).** The current topology-1 design (one shared `them-agent-runtime` serving all agents) is sufficient. Topology-2 (one container per agent) requires a compose reconciler, image build pipeline, and lifecycle management that is significant infrastructure work. The `them-agent-runtime` already runs two replicas behind Docker DNS load balancing.

**Codegen / "Export Go source".** The "Export Go source" button (generating a standalone Go agent binary) is a deferred opt-in convenience. The interpreted runtime is the production path.

**branch / loop / parallel / a2a_call / human_wait / stream_out step types.** These step types are defined in `go/internal/agentgen/spec.go` but not yet implemented in the interpreter. The builder's step palette currently lists them — they should be marked "coming soon" or removed from the palette until the interpreter handles them.

**A2A streaming.** `capabilities.streaming` is wired in the spec and agent card but `handleMessageSend` returns a single synchronous response. Streaming (SSE-based `message/stream`) is a separate implementation effort.

**Security scanner integration on publish.** The existing `security_scanner` agent could be called automatically on every publish, with results stored in `agents.last_scan_result`. This was noted in the original Phase 4 plan but is not part of the current scope.

---

## 10. Security Constraints

These apply to every phase of this feature and must be enforced at every layer.

**Credential values never in config.** Step configs are stored as JSONB in `agent_definitions.definition` and `agent_runtime_specs.spec`. Both are readable via the admin API. Credential values (API keys, passwords, tokens) must never appear in either table. Only credential **slot names** appear in config (e.g. `"provider_key_slot": "anthropic_key"`). Values are resolved from `them.app_agent_bindings.credential_bindings` at request time, decrypted in memory, never logged.

**Static headers only in HTTPStepConfig.Headers.** The `headers` field is for non-secret headers (Accept, Content-Type, X-App-Name, etc.). The UI must display a visible warning on the HTTP step config panel. API keys injected via the credential slot mechanism never appear in `headers`.

**Spec and definition are not secret.** Because both JSONB blobs are readable by admins, treat them as potentially loggable. The security model relies on slot indirection — a spec that says `"provider_key_slot": "anthropic_key"` reveals nothing without the binding.

**Invocation context headers.** `go/cmd/agent-runtime/main.go:parseInvocationContext` reads `X-Them-Tenant-Id`, `X-Them-Application-Id`, `X-Them-Agent-Id`, `X-Them-Binding-Id` from HTTP headers. These are trusted because the runtime is on the internal Docker network (`them-network`) and not reachable from outside. If the runtime is ever exposed externally, upgrade to signed JWT invocation context (the comment in the code already notes this as Phase 3 of that concern).

---

## 11. File Reference Map

| Concern | File |
|---|---|
| AgentSpec, SkillSpec, StepSpec, all config structs | `go/internal/agentgen/spec.go` |
| Pipeline interpreter (executes steps) | `go/internal/agentgen/interpreter.go` |
| Compiler (canvas JSON → AgentSpec) | `go/internal/agentgen/compiler.go` |
| Redis task store | `go/internal/agentgen/redistaskstore.go` |
| Invocation context, binding | `go/internal/agentgen/context.go`, `binding.go` |
| Runtime binary (message/send, agentCard, specCache) | `go/cmd/agent-runtime/main.go` |
| Publish CTE (3-table atomic write) | `go/internal/admin/dal/agent_definitions_publish.go` |
| Agent admin handlers (discover, test, publish route) | `go/internal/admin/agents.go` |
| Frontend canvas builder | `frontend/src/app/admin/agents/builder/page.tsx` |
| Frontend API types and calls | `frontend/src/lib/api.ts` |
| DB migration: canvas_a2a transport | `db/037_agents_transport_canvas.sql` |
| Agent card served at | `GET /agents/{slug}/.well-known/agent-card.json` |
| Agent JSON-RPC at | `POST /agents/{slug}` |
