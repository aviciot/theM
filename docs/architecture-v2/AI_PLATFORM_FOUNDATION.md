# AI Platform Foundation — Making the-M Self-Describing

**Status:** Design / Pre-implementation  
**Scope:** Platform infrastructure only — no AI features, no user-facing assistant  
**Enables later:** AI Canvas Agent Builder, AI Run Debugger  
**Last updated:** 2026-08-27

---

## 1. Executive Summary

Two distinct authoring schemas coexist in the-M. The majority of this foundation addresses the **Canvas Agent system**, not the application topology.

**Canvas Agent (primary scope).** An agent definition is a typed pipeline of steps: `input → http → transform → llm → mcp_call → response`. The Go `agentgen` package owns the `NodeDef` registry, the typed config structs, the 7-stage compiler, and the interpreter runtime.

**Application topology (secondary scope).** The Python `compile_graph()` function wires `entryPoint → orchestrator → agent`. This is platform composition, not agent authoring.

### LLM knowledge loading strategy

An LLM working with the-M needs three categories of information. They are loaded differently.

**Load once per deployment — static platform knowledge:**
- What node types exist and how they are configured (`GET /api/v1/admin/node-types`)
- The canvas definition wire format and validation rule codes (`GET /api/v1/admin/agent-definitions/schema`)
- The transform function catalog (`GET /api/v1/admin/transform-functions`)

These change only when a developer registers a new node type or transform function — rare. An LLM or assistant panel loads them once at session start and caches them. They are the platform's self-description. They are also the primary source material for the LLM's system prompt — the Copilot learns what it can build from these endpoints, not from static documentation.

**Load once per session — tenant ecosystem knowledge:**
- Which LLM providers are configured for this tenant (`GET /api/v1/admin/llm-providers`)
- Which A2A agents are available as `a2a_call` targets (`GET /api/v1/admin/agents?enabled=true`)
- Which MCP servers and tools are reachable for `mcp_call` steps (via MCP manifest)

These are tenant-specific and change when an admin registers a new agent or provider. They are loaded at session start and refreshed if the user references something that was not in the initial load. Without this data the Copilot would suggest an `anthropic` LLM provider when the tenant has only `groq` configured — a broken config the user has to fix manually.

**Call on demand — dynamic operations:**
- Validate a canvas being built or modified
- Add, modify, connect, or remove nodes
- Inspect the current canvas state
- Test a transform chain
- Analyze a failed run trace

These are called during the actual workflow, once per user action. The LLM is never re-reading schemas mid-conversation — it already knows the platform and the tenant's ecosystem. It only calls APIs to act.

This three-tier structure is what makes the Copilot feel knowledgeable from the first message rather than needing to discover everything incrementally.

### What already exists

- `GET /api/v1/admin/node-types` — `NodeTypesHandler` (public, no auth). Returns `[]NodeTypeInfo`.
- `POST /api/v1/admin/agent-definitions/{id}/validate` — returns `AgentValidationReport` with `Issues []Issue` (Severity, Code, Message, SkillID, NodeID, Field) and `StepContracts`.
- `GET /api/v1/admin/transform-functions` — full catalog including `Examples`.
- `POST /api/v1/admin/transform-test` — tests a transform chain, returns `TraceResult`.
- The validate endpoint accepts live canvas JSON in the body — no prior DB write required.

### What is missing

1. `GET /api/v1/admin/node-types` does not include per-field config documentation. An LLM cannot construct a valid `LLMStepConfig` or `HTTPStepConfig` without knowing field names, required fields, enum values, and descriptions.
2. No canvas definition wire format endpoint. The `canvasDefinition` struct is internal to the compiler.
3. No step-level run execution trace. The interpreter enforces VarRef contracts but records nothing at the step level.
4. No structured `diagnose → preview patch → validate → approve → apply` debugger workflow.
5. The Application topology compiler returns raw 422 strings, not structured Issue objects.

---

## 2. Design Principles

**1. NodeDef registry is the single source of truth.**  
The Go `NodeDef` struct in `noderegistry.go` already drives validation (`nd.Validate`), execution (`nd.Execute`), and data-flow analysis (`nd.DeriveInputs`, `nd.DeriveOutputs`). It must also be the source of everything an LLM or frontend reads about a node type. There is no separate AI schema document, no generated JSON file, no manually maintained mapping.

The `GET /api/v1/admin/node-types` endpoint calls `AllNodeTypeInfos()` and serializes the registry live at request time. When a developer registers a new node type or adds enrichment to an existing one, the endpoint reflects it immediately — no rebuild, no cache invalidation, no documentation update.

**2. Enrich NodeDef, not a parallel structure.**  
Anything the LLM or frontend needs to know about a node — field descriptions, enum values, examples, usage notes — lives on `NodeDef` itself, co-located with the runtime registration in `nodes.go`. If it is not in `NodeDef`, it does not exist from the platform's perspective.

**3. Load once, act on demand.**  
Static platform knowledge (node types, wire format, transform catalog) is loaded once per session. Dynamic operations (validate, mutate, analyze) are called per user action. The LLM is never re-reading schemas mid-conversation.

**4. Structured errors everywhere.**  
`agentgen.Issue` (Severity, Code, Message, SkillID, NodeID, Field) is the standard. The Application topology compiler must reach the same standard.

**5. Debugger works from the exact spec used by the run.**  
A failed run executed against a specific `DefinitionID` and published `AgentSpec`. The debugger reads that snapshot, not the current draft.

---

## 3. Component 1: Enriched NodeDef Registry (Phase 1 — primary change)

### Problem

`GET /api/v1/admin/node-types` already returns `NodeTypeInfo` but carries no per-field config documentation. An LLM reading it knows that an `http` step exists and has one input port, but cannot determine that `url_template` is required, that `inject_mode` accepts `"header"`, `"query"`, `"basic"`, or `"custom_header"`, or what `extractions` looks like.

### Solution: add enrichment fields to NodeDef

Extend `NodeDef` (and `NodeTypeInfo`) with fields that carry human-authored documentation about the node's config. These are co-located with the `RegisterNode()` call — same file, same registration block.

```go
// Added to NodeDef and NodeTypeInfo in noderegistry.go:

type ConfigFieldDoc struct {
    Field       string   `json:"field"`                  // matches JSON key in typed config struct
    Description string   `json:"description"`
    Required    bool     `json:"required"`
    Type        string   `json:"type"`                   // "string"|"integer"|"boolean"|"array"|"object"
    Enum        []string `json:"enum,omitempty"`
    Default     string   `json:"default,omitempty"`
    Example     string   `json:"example,omitempty"`
}

type NodeExample struct {
    Description string          `json:"description"`
    Config      json.RawMessage `json:"config"`
}

// Added fields on NodeDef (and therefore NodeTypeInfo via ToInfo()):
ConfigFields []ConfigFieldDoc `json:"config_fields,omitempty"`
UsageNotes   string           `json:"usage_notes,omitempty"`
Examples     []NodeExample    `json:"examples,omitempty"`
```

### Registration example — HTTP node

```go
RegisterNode(NodeDef{
    Type:        StepHTTP,
    Label:       "HTTP Request",
    Description: "Makes an HTTP request to an external API. Supports credential injection via app params.",
    UsageNotes:  "Use app_param_key to reference a credential stored in the agent's app params. The credential is never exposed in pipeline vars.",
    ConfigFields: []ConfigFieldDoc{
        {Field:"method",             Required:true,  Type:"string",  Enum:[]string{"GET","POST","PUT","PATCH","DELETE"}},
        {Field:"url_template",       Required:true,  Type:"string",  Description:"Go template. Use {{.var_name}} to inject pipeline variables.", Example:"https://api.example.com/users/{{.user_id}}"},
        {Field:"body_template",      Required:false, Type:"string",  Description:"Go template for request body. Usually JSON."},
        {Field:"extractions",        Required:false, Type:"array",   Description:"JSONPath extractions from response body into pipeline vars. Each entry: {var, json_path}."},
        {Field:"timeout_seconds",    Required:false, Type:"integer", Default:"30"},
        {Field:"app_param_key",      Required:false, Type:"string",  Description:"AppParamDecl key whose value is injected as a credential."},
        {Field:"inject_mode",        Required:false, Type:"string",  Enum:[]string{"header","query","basic","custom_header"}, Default:"header", Description:"How the credential is injected. 'header' = Authorization Bearer."},
        {Field:"inject_header_name", Required:false, Type:"string",  Description:"Required when inject_mode=custom_header."},
    },
    Examples: []NodeExample{
        {Description:"GET JSON API with bearer token",
         Config: json.RawMessage(`{"method":"GET","url_template":"https://api.example.com/data","app_param_key":"api_key","extractions":[{"var":"result","json_path":"data"}]}`)},
    },
    // ... existing fields unchanged ...
})
```

### What the endpoint returns

`GET /api/v1/admin/node-types` — no handler change needed. `AllNodeTypeInfos()` calls `ToInfo()` per registered node. `ToInfo()` copies the new fields. The endpoint already serializes the full struct.

The LLM loads this once. It now knows:
- Every node type that exists
- Every config field for each type, its type, whether it is required, valid enum values, and an example
- Edge rules, port names, which nodes are sources/sinks, which are stubs
- Usage notes for non-obvious constraints

### Why this approach is correct

The registry is already the runtime source of truth. Adding enrichment fields to `NodeDef` means:
- One file to update when a node changes (`nodes.go`)
- No JSON to hand-write separately
- No build step or code generation
- No CI test needed to detect drift — drift is structurally impossible
- The endpoint is always live — reflects the current registered state

If tomorrow a developer adds a `StepGraphQL` node and registers it with `ConfigFields`, it immediately appears in the endpoint response. The LLM knows about it on next session start.

---

## 4. Component 2: Canvas Definition Wire Format (Phase 1)

### Problem

The `canvasDefinition` struct is internal to `agentgen/compiler.go`. No endpoint documents what JSON shape must be submitted as an agent definition.

### Design

**`GET /api/v1/admin/agent-definitions/schema`**

Auth: Admin JWT. Returns two things together:

1. **Wire format JSON Schema** — the exact shape of `{agent_root, skills[{skill_id, name, steps[{id, type, config, next}]}]}`
2. **Validation issue code reference** — all 19 `agentgen.Issue` codes with meaning and corrective action

These belong together because an LLM needs both to build a definition and understand why validation rejected it. This endpoint is also loaded once at session start.

Implementation: static Go constant in `go/internal/admin/agent_definition_schema.go`. No DB query. Must be registered before `{id}` parameter routes to avoid routing ambiguity.

The wire format schema references `GET /api/v1/admin/node-types` for step type config — the LLM is expected to have loaded that already.

---

## 5. Component 3: Transform Function Catalog (Phase 1 — verify)

`GET /api/v1/admin/transform-functions` returns `[]FunctionDef`. `FunctionDef` already includes `Examples []Example`. Verify the HTTP handler serializes the full struct including examples. If it does, this component is already complete.

`POST /api/v1/admin/transform-test` returns `TraceResult` with per-step `{fn, in, out, error, ok, duration_ns}`. An LLM can use this to verify a transform chain before embedding it in a definition — call on demand, not loaded at startup.

---

## 6. LLM Session Startup Sequence

When a Canvas Copilot session opens the backend executes this sequence before the first user message is processed. The results feed directly into the LLM's system prompt — this is not a cache, it is the foundation of the Copilot's knowledge for the session.

### Tier 1 — Platform knowledge (static, shared across all tenants)

```
GET /api/v1/admin/node-types
→ Full NodeTypeInfo[] with ConfigFields, Examples, UsageNotes per type.
→ Feeds system prompt section: "Available node types and their configuration"

GET /api/v1/admin/agent-definitions/schema
→ Wire format JSON Schema + all 19 validation issue codes with meanings.
→ Feeds system prompt section: "Canvas definition format and validation rules"

GET /api/v1/admin/transform-functions
→ Full function catalog with examples.
→ Feeds system prompt section: "Transform functions available in transform nodes"
```

These three calls are idempotent and can be cached at the process level with a long TTL (invalidated only when a new node type is registered, which requires a deployment). In practice they are near-constant.

### Tier 2 — Tenant ecosystem (per-tenant, loaded per session)

```
GET /api/v1/admin/llm-providers
→ Configured providers for this tenant: slugs, supported models, enabled status.
→ Feeds system prompt section: "LLM providers available for llm nodes"
→ Critical: Copilot must only suggest providers that are actually configured.

GET /api/v1/admin/agents?enabled=true
→ Available A2A agents: slugs, descriptions, input schemas, skills.
→ Feeds system prompt section: "A2A agents available for a2a_call nodes"

GET /api/v1/admin/mcp-servers (with tool manifests)
→ Available MCP servers and their tools for this tenant.
→ Feeds system prompt section: "MCP tools available for mcp_call nodes"
```

These are tenant-specific. They are loaded fresh at each session start. If the user references an agent or provider not in this list, the Copilot can offer to reload — but it must not hallucinate tool names or provider slugs that are not in the response.

### Tier 3 — Current canvas state (per-definition)

```
GET /api/v1/admin/agent-definitions/{id}
→ The definition being edited: current skills, steps, connections, validation state.
→ Feeds system prompt section: "Current canvas state"
→ Also used to set the opening context: "This is a [description] agent with [N] steps..."
```

After this sequence the system prompt is assembled and the LLM is ready. The first user message gets a Copilot that already knows what it can build, what the tenant has configured, and what is already on the canvas.

---

## 7. On-Demand Operations

These are called during the workflow — once per user action. The Copilot never re-reads static platform knowledge mid-conversation.

### Building and validation

```
POST /api/v1/admin/agent-definitions/{id}/validate
→ AgentValidationReport: {valid, issues[{severity,code,node_id,field,message}], step_contracts}
→ Called after every canvas mutation. LLM maps issue codes (known from startup)
  to corrections and self-heals before replying to the user.

POST /api/v1/admin/agent-definitions (create draft)
PUT  /api/v1/admin/agent-definitions/{id} (save draft)
POST /api/v1/admin/agent-definitions/{id}/publish

POST /api/v1/admin/transform-test
→ TraceResult with per-step {fn, in, out, ok}. Called to verify a transform chain
  before embedding it. Gives the Copilot evidence that the chain does what was intended.
```

### Ecosystem refresh (on user request)

If the user says "use the new Groq agent I just added" and it was not in the session-start load, the Copilot refreshes:

```
GET /api/v1/admin/agents?enabled=true   → re-load agent list
GET /api/v1/admin/llm-providers         → re-load provider list
```

This is the only time Tier 2 data is re-fetched mid-session.

### Debugging (Phase 2)

```
GET /api/v1/runs/{id}/trace
→ Step-level execution trace: failed step, variable state, error.
→ Called once when the user opens a failed run.

POST /api/v1/admin/agent-definitions/{id}/validate
→ Reused to validate a proposed fix before showing it to the user for approval.
```

---

## 8. Canvas Agent Builder Workflow (with enriched node types)

```
Session start (once):
  GET /node-types          → know all node types and their config fields
  GET /agent-definitions/schema → know wire format and error codes
  GET /transform-functions → know transform functions and examples

Building:
  → Construct {agent_root, skills[{steps}]} from loaded knowledge
  → POST /validate         → get Issues with codes
  → Self-correct using code → meaning mapping (loaded at start)
  → Repeat until valid
  → POST /publish

Per-action (on demand):
  → POST /transform-test   → verify a specific transform chain
  → GET /agents            → find available a2a_call targets
  → GET /mcp-servers       → find available mcp_call tools
```

---

## 9. Application Topology Layer (Secondary — Phase 1)

### A. Application Graph Summary

**`GET /api/v1/admin/applications/{id}/ai-summary`**

Flat, LLM-readable view: entry_points, orchestrators with reachable agents and MCP tools. ETag from `application.updated_at`. No new DB columns — joins existing tables.

Canvas agents appearing as `canvas_a2a` adapter type are listed with their AgentSpec summary.

### B. Structured Application Validation

`compile_graph()` currently raises raw 422 strings. Must produce the same `{severity, code, path, message, suggestion}` structure as agentgen. Application CANVAS_RULES become the code set: `AT_LEAST_ONE_EP`, `EP_SLUG_FORMAT`, `EP_HAS_ORCH`, etc.

Python change + frontend change must ship in the same PR (frontend currently parses `error.response.data.detail` as a string).

### C. Unified Tool Manifest

**`GET /api/v1/admin/orchestrators/{id}/tools`**

Returns agents, MCP tools, and sub-orchestrators reachable from an orchestrator — in one call. Joins `allowed_agent_ids → agents`, `mcp_servers JSONB → tools_manifest`. No new DB columns.

---

## 10. Component 4: Pipeline Dry-Run / Synthetic Data Testing (Phase 1.5)

### Problem

The Copilot can build a syntactically valid pipeline but cannot verify logical correctness before publishing. An HTTP step may be configured correctly but point to a URL that requires a live credential. An LLM step may have a well-formed prompt but the template reference `{{.temperature}}` might not resolve if the upstream transform uses a different output variable name. The user cannot know any of this until they publish and run — which wastes a real execution, costs tokens, and may fail against production systems.

The Copilot needs to be able to validate pipeline **logic** before publish by injecting synthetic test data at any step and tracing what flows through.

### Existing infrastructure

The interpreter in `go/internal/agentgen/interpreter.go` already supports dependency injection for all side-effecting interfaces:

| Interface | Purpose | How mocked today |
|---|---|---|
| `HTTPDoer` | Makes HTTP calls | `httptest.NewServer` in tests |
| `LLMFactory` | Creates LLM providers | `fakeLLMFactory` in tests |
| `MCPCaller` | Calls MCP tool | `stubMCPCaller` in tests |

These interfaces exist because the test suite already needs them. The production code path creates real clients; the test code path injects fakes. **There is no runtime mechanism to activate mock/fixture mode per-step.** This is what Component 4 adds.

The transform pipeline already has its own test endpoint (`POST /api/v1/admin/transform-test`) that accepts a function chain and input vars and returns a `TraceResult` — this is the model for Component 4's step-level testing.

### Design: DryRunFixture per step

**Step 1: Add `DryRunFixture` to `StepSpec`**

```go
// In go/internal/agentgen/spec.go, added to StepSpec:
type DryRunFixture struct {
    // MockOutputVars replaces the step's real execution outputs.
    // Keys are pipeline variable names; values are the mock values to inject.
    // Any vars NOT listed here that the step would normally write are left unchanged.
    MockOutputVars map[string]any `json:"mock_output_vars,omitempty"`
}

// On StepSpec:
DryRunFixture *DryRunFixture `json:"dry_run_fixture,omitempty"`
```

`DryRunFixture` is **not stored in the published `AgentSpec`** — it is stripped by `CompileForPublish`. It is valid only in the input to `Validate()` and the dry-run endpoint. This keeps production specs clean and prevents accidental synthetic data in production runs.

**Step 2: Add dry-run mode to `InvocationContext`**

```go
// In go/internal/agentgen/interpreter.go, added to InvocationContext:
DryRun     bool `json:"dry_run,omitempty"`     // if true, steps with DryRunFixture short-circuit
StrictDryRun bool `json:"strict_dry_run,omitempty"` // if true, steps WITHOUT a fixture that would make a real call error instead
```

**Step 3: Interpreter short-circuit logic**

In `executeStep`, before calling `def.Execute`:

```go
if ic.DryRun && step.DryRunFixture != nil {
    // Short-circuit: inject mock output vars directly into PipelineVars
    for k, v := range step.DryRunFixture.MockOutputVars {
        vars[k] = v
    }
    // Record the step in the trace with status="fixture"
    return nil
}
if ic.StrictDryRun {
    // Steps without fixtures that would make real network calls must error
    switch step.Type {
    case StepHTTP, StepLLM, StepMCPCall, StepA2ACall:
        return fmt.Errorf("step %q (%s) has no dry-run fixture — cannot make real calls in StrictDryRun mode", step.ID, step.Type)
    }
}
```

`transform` and `branch` steps always execute for real — they are pure computation with no side effects, no credentials, and no network calls. There is no reason to mock them; their logic is what the user most needs to verify.

**Step 4: `DryRunStepTrace` in results**

Extend `ExecutionResult` with per-step trace entries:

```go
type DryRunStepResult struct {
    StepID     string         `json:"step_id"`
    StepType   string         `json:"step_type"`
    Status     string         `json:"status"`    // "fixture" | "executed" | "skipped" | "error"
    InputVars  map[string]any `json:"input_vars,omitempty"`  // vars available at step entry (credentials excluded)
    OutputVars map[string]any `json:"output_vars,omitempty"` // vars written by step
    Error      string         `json:"error,omitempty"`
    DurationMs int64          `json:"duration_ms"`
}
```

When `DryRun=true`, every step appends a `DryRunStepResult` to the trace — whether it ran real, used a fixture, or was skipped.

### New endpoint: POST /api/v1/admin/agent-definitions/{id}/dry-run

Auth: Admin JWT (same as validate).

**Request:**

```json
{
  "skill_id": "main",
  "seed_input": "Weather check for London",
  "fixtures": {
    "step-http-1": {
      "mock_output_vars": {
        "http_response": { "current_weather": { "temperature": 12.3, "weathercode": 1 } }
      }
    },
    "step-llm-1": {
      "mock_output_vars": {
        "output": "It's a mild 12.3°C with clear skies over London."
      }
    }
  },
  "strict": false
}
```

The `{id}` is the agent definition ID. The endpoint loads the current draft definition, injects the fixtures into the relevant `StepSpec.DryRunFixture` fields (in-memory, not persisted), creates a minimal `InvocationContext` with `DryRun=true`, and runs the interpreter.

Alternatively, the request body may include `definition_json` directly (the full canvas JSON) instead of relying on the stored draft — allowing the Copilot to test a definition that has not been saved yet.

**Response (`PipelineDryRunResult`):**

```json
{
  "ok": true,
  "final_output": "It's a mild 12.3°C with clear skies over London.",
  "steps": [
    {
      "step_id": "step-input-1",   "step_type": "input",     "status": "executed",
      "output_vars": { "input": "Weather check for London" },        "duration_ms": 0
    },
    {
      "step_id": "step-http-1",    "step_type": "http",      "status": "fixture",
      "output_vars": { "http_response": { "current_weather": { "temperature": 12.3 } } }, "duration_ms": 0
    },
    {
      "step_id": "step-transform-1","step_type": "transform", "status": "executed",
      "input_vars":  { "http_response": { "current_weather": { "temperature": 12.3 } } },
      "output_vars": { "temperature": 12.3 },                        "duration_ms": 1
    },
    {
      "step_id": "step-llm-1",     "step_type": "llm",       "status": "fixture",
      "output_vars": { "output": "It's a mild 12.3°C..." },          "duration_ms": 0
    },
    {
      "step_id": "step-response-1","step_type": "response",  "status": "executed",
      "input_vars": { "output": "It's a mild 12.3°C..." },
      "output_vars": { "response": "It's a mild 12.3°C..." },        "duration_ms": 0
    }
  ],
  "variable_state": {
    "input": "Weather check for London",
    "http_response": { "current_weather": { "temperature": 12.3, "weathercode": 1 } },
    "temperature": 12.3,
    "output": "It's a mild 12.3°C with clear skies over London."
  }
}
```

`variable_state` is the final `PipelineVars` state after all steps — the complete view of what was computed.

### Single-step test endpoint

For testing one step in isolation (useful during construction, before the pipeline is connected end-to-end):

**`POST /api/v1/admin/agent-definitions/{id}/dry-run-step`**

```json
{
  "skill_id": "main",
  "step_id": "step-transform-1",
  "input_vars": {
    "http_response": { "current_weather": { "temperature": 12.3, "weathercode": 1 } }
  }
}
```

The endpoint creates a minimal `PipelineVars` from `input_vars`, finds the step in the definition, and calls `def.Execute` directly (no interpreter loop — no `input` step needed, no `next` step). Returns a single `DryRunStepResult`.

This mirrors `POST /admin/transform-test` but works for all node types, not just transform chains.

### Per-node fixture support by node type

| Node type | Fixture semantics | Notes |
|---|---|---|
| `input` | N/A — always executes (populates `vars["input"]` from `seed_input`) | No fixture needed |
| `http` | Fixture replaces the HTTP response body. `mock_output_vars["http_response"]` is the parsed response. | Real call skipped |
| `transform` | Always executes real transform logic. Fixture not applicable — transform is pure computation. | Use `test_step` to verify with specific input vars |
| `llm` | Fixture replaces LLM completion. `mock_output_vars["output"]` (or `output_var`) is the mock text. | Real LLM call skipped |
| `branch` | Always executes real template evaluation. No fixture needed — branch logic is deterministic given input vars. | |
| `mcp_call` | Fixture replaces the MCP tool response. `mock_output_vars["<output_var>"]` is the mock JSON result. | |
| `a2a_call` | Fixture replaces the A2A agent response. `mock_output_vars["<output_var>"]` is the mock. | |
| `response` | Always executes — just reads a var. Fixture not needed. | |
| `loop`, `parallel`, `human_wait` | Stub nodes — fixture may be useful to skip the stub and continue the pipeline. | |

### Why transform and branch always run real

`transform` is the node the Copilot most needs to verify — it is the most likely place for a logic error (wrong JSONPath, wrong function chain, wrong output variable name). Running the real transform with injected input vars from an HTTP fixture is exactly the right test: the real extract-and-reshape logic runs against plausible data.

`branch` evaluates a Go template expression (`{{ gt .score 0.9 }}`). This is deterministic given the input vars and must be verified to ensure the right branch fires. There is no value in mocking a branch — the whole point is to check its logic.

### Credential safety in dry-run

The same credential exclusion rule from Component 5 (Section 11, credential safety) applies to dry-run output. Any variable matching `AgentParamSpec.Key` in the spec's `RequiredParams` is excluded from `DryRunStepResult.InputVars` and `OutputVars`. The dry-run trace must not leak secrets even in the authoring path.

### Implementation files

```
go/internal/agentgen/spec.go         — add DryRunFixture struct, DryRunFixture *DryRunFixture on StepSpec
go/internal/agentgen/interpreter.go  — add DryRun/StrictDryRun to InvocationContext; short-circuit in executeStep
go/internal/agentgen/compiler.go     — strip DryRunFixture in CompileForPublish
go/internal/admin/agent_definitions.go — add dry-run and dry-run-step handlers
go/internal/agentgen/spec.go         — add DryRunStepResult, PipelineDryRunResult
```

---

## 11. Component 5: Run Execution Trace (Phase 2 — requires DB migration)

*(Previously numbered Component 4 — renumbered to make room for Component 4: Pipeline Dry-Run above)*

New table `them.run_pipeline_steps` — one row per canvas-agent step execution:

```sql
CREATE TABLE them.run_pipeline_steps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL REFERENCES them.runs(id) ON DELETE CASCADE,
    step_id         TEXT NOT NULL,
    step_type       TEXT NOT NULL,
    skill_id        TEXT NOT NULL,
    seq             INT  NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    duration_ms     INT,
    status          TEXT NOT NULL,   -- "completed" | "failed" | "skipped"
    config_snapshot JSONB,           -- step config at run time (credentials excluded)
    inputs          JSONB,           -- pipeline vars read (credentials excluded)
    outputs         JSONB,           -- pipeline vars written (credentials excluded)
    error           TEXT
);
CREATE INDEX ON them.run_pipeline_steps(run_id, seq);
```

**Credential safety (non-negotiable):** Before writing `inputs`/`outputs`, remove any key matching `AgentParamSpec.Key` in `InvocationContext.Spec.RequiredParams`. Tested explicitly.

`GET /api/v1/runs/{id}/trace` returns the ordered step execution with `failed_step_id`, error, and variable snapshots. Called on demand when the user opens a failed run.

---

## 11. Component 6: AI Debugger Workflow (Phase 2)

Works from the exact `AgentSpec` stored in `agent_runtime_specs.spec_json` for the failing run — not the current draft.

```
1. GET /runs/{id}/trace           → identify failed_step_id, error, variable state
2. Rule-based classify            → code: context_overflow | http_timeout | unresolved_variable | ...
3. Generate proposed patch        → structured {op, target_step, change} — not applied yet
4. POST /agent-definitions/{id}/validate (with patched definition)
   → AgentValidationReport — shown to user before approval
5. User approves
6. PUT /agent-definitions/{id}    → save patched definition
7. POST /agent-definitions/{id}/publish (optional)
```

All steps use existing endpoints. No new write endpoints needed for the debugger.

---

## 12. Implementation Roadmap

### Phase 1 — Platform self-description (no DB migrations)

| # | What | Files |
|---|---|---|
| 1.1 | Add `ConfigFields`, `UsageNotes`, `Examples` to `NodeDef`/`NodeTypeInfo`; populate in `nodes.go` | `go/internal/agentgen/noderegistry.go`, `nodes.go` |
| 1.2 | `GET /api/v1/admin/agent-definitions/schema` — wire format + issue codes | `go/internal/admin/agent_definition_schema.go` (new) |
| 1.3 | Verify transform catalog includes Examples; fix if not | `go/internal/admin/transform*.go` |
| 1.4 | Application graph summary endpoint | `go/internal/admin/applications.go` |
| 1.5 | Structured errors from `compile_graph` | `app/services/app_compiler.py` + frontend |

### Phase 1.5 — Pipeline dry-run / synthetic data testing (no DB migrations)

| # | What | Files |
|---|---|---|
| 1.5.1 | `DryRunFixture` struct; add `DryRunFixture *DryRunFixture` to `StepSpec`; add `DryRun`/`StrictDryRun` to `InvocationContext` | `go/internal/agentgen/spec.go`, `go/internal/agentgen/interpreter.go` |
| 1.5.2 | `executeStep` short-circuit logic for fixture mode; `DryRunStepResult` trace accumulation | `go/internal/agentgen/interpreter.go` |
| 1.5.3 | Strip `DryRunFixture` in `CompileForPublish` (publish safety) | `go/internal/agentgen/compiler.go` |
| 1.5.4 | `POST /api/v1/admin/agent-definitions/{id}/dry-run` endpoint | `go/internal/admin/agent_definitions.go` |
| 1.5.5 | `POST /api/v1/admin/agent-definitions/{id}/dry-run-step` endpoint | `go/internal/admin/agent_definitions.go` |
| 1.5.6 | `test_pipeline` and `test_step` MCP tools in Canvas MCP Server | `go/internal/canvas/mcp_server.go` (new) |

### Phase 2 — Run trace and debugger (DB migration required)

| # | What | Notes |
|---|---|---|
| 2.1 | `them.run_pipeline_steps` table + interpreter instrumentation | Migration + `StepRecorder` interface |
| 2.2 | `GET /api/v1/runs/{id}/trace` | Requires 2.1 |
| 2.3 | Rule-based failure classifier | ~70% coverage; `unknown` is honest fallback |
| 2.4 | Debugger patch workflow | Reuses existing validate + PUT endpoints |

### Phase 3 — Graph versioning (lower priority)

Application graph snapshot table, run-to-version link, graph-diff endpoint.

---

## 13. Confidence and Risks

**High confidence:**
- Component 1.1 (enriched NodeDef): Adding fields to `NodeDef` and populating them in `nodes.go` is low-risk and additive. The registry is already the runtime source of truth — enrichment co-located there cannot drift. This is the approach with the strongest correctness guarantee.
- Existing validate endpoint: already returns machine-readable `Issue` structs. An LLM can self-correct today. Phase 1 only makes the surrounding context richer.
- Load-once / act-on-demand split: clearly maps to existing endpoint categories. Static endpoints (`/node-types`, `/agent-definitions/schema`, `/transform-functions`) are already idempotent reads. Dynamic endpoints are already per-action calls.

**Medium confidence:**
- Component 1.5 (application validation issues): Breaking API change — 422 body shape changes. Frontend and any external callers must be updated in the same PR.
- Component 2.1 (pipeline step trace): Interpreter instrumentation adds per-step DB writes. Benchmark write overhead before committing to synchronous persistence; consider batching if needed.

**High confidence — dry-run (Component 4):**
The dependency injection interfaces (`HTTPDoer`, `LLMFactory`, `MCPCaller`) already exist on the interpreter specifically because the test suite needed them. The dry-run feature reuses this established pattern at the request level rather than the test level. `DryRunFixture` on `StepSpec` is purely additive — no existing field changes. Stripping fixtures in `CompileForPublish` is a single-line filter. The biggest implementation risk is the `StrictDryRun` path — deciding which node types count as "real call" nodes requires a list that must stay in sync with new node types as they are added (a `HasSideEffects() bool` field on `NodeDef` is the clean way to express this).

**Key risk — enrichment maintenance discipline:**
`ConfigFields` on `NodeDef` are human-authored. When a developer changes a config struct field name, they must also update the `ConfigFieldDoc` entry. This is one file (`nodes.go`), co-located, but still a human step. Mitigation: a test that cross-checks `ConfigFieldDoc.Field` values against the JSON keys present in an example config for each node type — catches missing or misspelled field names at CI.
