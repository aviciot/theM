# AI Platform Foundation — Making the-M Self-Describing

**Status:** Design / Pre-implementation  
**Scope:** Platform infrastructure only — no AI features, no user-facing assistant  
**Enables later:** AI Canvas Agent Builder, AI Run Debugger  
**Last updated:** 2026-08-27

---

## 1. Executive Summary

Two distinct authoring schemas coexist in the-M. The majority of this foundation addresses the **Canvas Agent system**, not the application topology. The distinction matters because they serve different concerns.

**Canvas Agent (primary scope).** An agent definition is a typed pipeline of steps: `input → http → transform → llm → mcp_call → response`. The Go `agentgen` package owns the `NodeDef` registry, the typed config structs, the 7-stage compiler, and the interpreter runtime. This is what an AI Agent Builder must understand to construct, validate, and debug pipelines.

**Application topology (secondary scope).** The Python `compile_graph()` function wires `entryPoint → orchestrator → agent` together. This is platform composition — connecting existing agents into a deployed application — not agent authoring. An LLM building agents does not need to understand it at authoring time; it only matters when deploying.

### What already exists

The platform has more foundation than is commonly understood:

- `GET /api/v1/admin/node-types` — already exists (`NodeTypesHandler`, public, no auth). Returns `[]NodeTypeInfo` with type, label, description, emoji, output_arity, is_source, is_sink, single_input, edges (min/max in/out), input_ports, output_ports, app_params, executable.
- `POST /api/v1/admin/agent-definitions/{id}/validate` — already returns `AgentValidationReport` with `Issues []Issue` (machine-readable: Severity, Code, Message, SkillID, NodeID, Field) and `StepContracts` (per-step VarRef input/output contracts).
- `GET /api/v1/admin/transform-functions` — returns the transform function catalog (`FunctionDef` includes `Examples []Example`).
- `POST /api/v1/admin/transform-test` — tests a transform chain against sample input, returns `TraceResult`.
- The validate endpoint accepts live canvas JSON directly via request body — no prior DB write required.

### What is missing

1. `GET /api/v1/admin/node-types` does not include the typed config struct schema for each step type. An LLM cannot construct a valid `LLMStepConfig` or `HTTPStepConfig` without knowing the field names, required fields, and enum values.
2. No endpoint returns the canvas definition wire format (the `canvasDefinition` struct is internal to the compiler). An LLM must guess at `{agent_root, skills[{skill_id, name, steps[{id, type, config, next, branches, inputs}]}]}`.
3. No run execution trace for canvas-agent pipeline steps. The interpreter walks `StepSpec` slices and enforces VarRef contracts but records nothing at the step level.
4. No `diagnose → preview patch → validate → user approval → apply` debugger workflow.
5. The Application topology compiler (`compile_graph`) returns raw 422 strings, not structured Issue objects — unlike agentgen.

### Confidence summary

Components building on agentgen's existing infrastructure (Phases 1.1–1.3) are high confidence — they are additive changes to already-working code. The run debugger (Phase 2) requires new interpreter instrumentation and a DB migration; the complexity of that change is medium.

---

## 2. Design Principles

**1. NodeDef registry is the authoritative source.**  
The Go `NodeDef` struct in `noderegistry.go` drives validation (`nd.Validate`), execution (`nd.Execute`), and data-flow analysis (`nd.DeriveInputs`, `nd.DeriveOutputs`). Typed config schemas exposed to an LLM must be co-located with the NodeDef registration in `nodes.go`, not maintained in a separate store. If a NodeDef changes, the schema changes.

**2. No drift by construction.**  
Every piece of metadata the LLM receives is generated from the same Go structs used at runtime. Parallel representations that require manual synchronization are rejected.

**3. Build on what exists.**  
The validate endpoint already returns machine-readable `Issue` structs and `StepContracts`. The node-types endpoint already exists. The roadmap extends these, it does not replace them.

**4. Structured errors everywhere.**  
`agentgen.Issue` (Severity, Code, Message, SkillID, NodeID, Field) is the standard. The Application topology compiler (`compile_graph`) must be brought to the same standard — its raw 422 strings are a gap, not a model.

**5. Debugger works from the exact spec used by the run.**  
A failed canvas-agent run executed against a specific `DefinitionID` and a published `AgentSpec`. The debugger reads that spec snapshot from `agent_runtime_specs.spec_json`, not the current draft. Debugging against the wrong definition is worse than no debugger.

---

## 3. What an LLM Needs to Build a Canvas Agent

This section maps each step in the agent-building workflow to the endpoint that serves it, and identifies where the gap is.

**Step 1 — Discover node types (EXISTS)**

`GET /api/v1/admin/node-types` → `[]NodeTypeInfo`

Returns: type, label, description, emoji, output_arity, is_source, is_sink, single_input, edges (min/max in/out), input_ports, output_ports, app_params, executable.

Gap: does not include the typed config struct schema for each node type. An LLM reading this response knows that an `llm` step exists and has one input port and one output port, but cannot determine that the config requires `provider`, `model`, `user_prompt`, and `output_var`, or that `inject_mode` on `http` accepts `"header"`, `"query"`, `"basic"`, or `"custom_header"`.

**Step 2 — Discover transform functions (EXISTS)**

`GET /api/v1/admin/transform-functions` → `[]FunctionDef`

`FunctionDef` includes `Name`, `Category`, `Description`, `Args []ArgDef`, `Examples []Example`. The catalog is the effective config schema for the `transform` node because a transform step's config is a chain of `{fn, input_var, output_var, args}` entries drawn from this catalog.

Gap assessment: `Catalog()` returns `[]FunctionDef` directly, so Examples should be present. Verify this before Phase 1.2 work begins. If the HTTP handler serializes a different shape, fix it.

**Step 3 — Learn the canvas definition wire format (MISSING)**

No endpoint describes the shape of the JSON submitted as an agent definition. The `canvasDefinition` struct is internal to `agentgen/compiler.go`. An LLM must guess at the format.

This is addressed by Component 2 below.

**Step 4 — Validate while building (EXISTS)**

`POST /api/v1/admin/agent-definitions/{id}/validate`  
Body: `{"definition": <raw canvas definition JSON>}`

Returns `AgentValidationReport`:
```json
{
  "valid": false,
  "issues": [
    {
      "severity": "error",
      "code": "UNRESOLVED_INPUT",
      "skill_id": "main",
      "node_id": "step-3",
      "field": "",
      "message": "variable 'customer_id' is not guaranteed on all paths reaching this step"
    }
  ],
  "step_contracts": {
    "step-3": {
      "inputs":  [{ "name": "customer_id", "required": true, "port_id": "input" }],
      "outputs": [{ "name": "llm_result",  "required": false, "port_id": "output" }]
    }
  }
}
```

The validate endpoint accepts the live definition in the request body — no prior DB write is needed. An LLM can iterate through validate → correct → validate without touching the database, as long as it has an existing agent-definition ID to use as the path parameter.

**Step 5 — Self-correct from Issues (EXISTS once Step 4 works)**

Each Issue carries `Code`, `NodeID`, `Field`, and `Message`. An LLM can map each code to a corrective action. The full code set is defined in the compiler and documented in Component 2.

**Step 6 — Publish (EXISTS)**

`POST /api/v1/admin/agent-definitions/{id}/publish`  
Returns `AgentPublishResult`:
```json
{
  "agent_id": "...",
  "definition_id": "...",
  "revision": 3,
  "spec_hash": "a3f4b2c1..."
}
```

**Primary gap:** Step 3 (wire format) and the node-type config schemas (Step 1 gap). Everything else exists or is a minor fix.

**Flow constraint:** Step 4–6 require an existing agent-definition ID. For a purely generative flow (no prior draft), the LLM must first `POST /api/v1/admin/agent-definitions` to create a draft, then use that ID.

---

## 4. Component 1: Node Type Config Schemas (Phase 1)

### Problem

`GET /api/v1/admin/node-types` returns `NodeTypeInfo` which describes graph topology — ports, edges, arity — but not the typed config struct required in each step's `config` field. The `config` field is `json.RawMessage` at the wire level; its shape is entirely determined by step type.

### Design

Add a `config_schema` field (JSON Schema object) to `NodeTypeInfo`. The schema is a literal `json.RawMessage` constant defined alongside the NodeDef registration in `nodes.go`. It is part of the NodeDef, serialized by `ToInfo()`, and returned by the existing `/node-types` handler with no other changes.

This is backward compatible — existing clients receive an additional field and ignore it.

**Alternative rejected:** generating schema via reflection from the typed struct. Reflection produces field names and Go types but loses descriptions, enum values, inter-field constraints (`inject_header_name` required when `inject_mode=custom_header`), and template semantics. Literal schemas are more accurate.

### Config schema examples per step type

**`llm`**
```json
{
  "type": "object",
  "required": ["provider", "model", "user_prompt", "output_var"],
  "properties": {
    "provider":      { "type": "string", "description": "LLM provider slug (e.g. anthropic, openai, groq)." },
    "model":         { "type": "string", "description": "Model identifier for the chosen provider." },
    "system_prompt": { "type": "string" },
    "user_prompt":   { "type": "string", "description": "Go template. PipelineVars are available as {{.var_name}}." },
    "max_tokens":    { "type": "integer", "default": 1024 },
    "effort":        { "type": "string", "enum": ["low", "medium", "high"], "description": "Reasoning effort hint. Provider-dependent." },
    "output_var":    { "type": "string", "description": "PipelineVars key written with the LLM response text." },
    "stream":        { "type": "boolean", "default": false }
  }
}
```

**`http`**
```json
{
  "type": "object",
  "required": ["method", "url_template"],
  "properties": {
    "method":            { "type": "string", "enum": ["GET", "POST", "PUT", "PATCH", "DELETE"] },
    "url_template":      { "type": "string", "description": "Go template. PipelineVars available as {{.var_name}}." },
    "headers":           { "type": "object", "additionalProperties": { "type": "string" } },
    "body_template":     { "type": "string", "description": "Go template for request body." },
    "extractions": {
      "type": "array",
      "description": "JSONPath extractions from the response body into PipelineVars.",
      "items": {
        "type": "object",
        "required": ["var", "json_path"],
        "properties": {
          "var":       { "type": "string", "description": "PipelineVars key to write." },
          "json_path": { "type": "string", "description": "Dot-separated path into JSON response body." }
        }
      }
    },
    "timeout_seconds":    { "type": "integer", "default": 30 },
    "app_param_key":      { "type": "string", "description": "AppParamDecl key whose value is injected as a credential." },
    "inject_mode":        { "type": "string", "enum": ["header", "query", "basic", "custom_header"], "description": "How the credential is injected. Default: Authorization Bearer header." },
    "inject_header_name": { "type": "string", "description": "Required when inject_mode=custom_header." }
  }
}
```

**`transform`**
```json
{
  "type": "object",
  "required": ["functions"],
  "properties": {
    "functions": {
      "type": "array",
      "description": "Ordered chain of transform functions. Each step reads input_var and writes output_var.",
      "items": {
        "type": "object",
        "required": ["fn", "input_var", "output_var"],
        "properties": {
          "fn":         { "type": "string", "description": "Function name from GET /api/v1/admin/transform-functions." },
          "input_var":  { "type": "string", "description": "PipelineVars key to read." },
          "output_var": { "type": "string", "description": "PipelineVars key to write." },
          "args":       { "type": "object", "additionalProperties": { "type": "string" }, "description": "Function arguments. See ArgDef in catalog." }
        }
      }
    }
  }
}
```

**`branch`**
```json
{
  "type": "object",
  "required": ["expression", "true_next", "false_next"],
  "properties": {
    "expression": { "type": "string", "description": "Go template evaluated against PipelineVars. Truthy values: non-empty, not 'false', not '0'." },
    "true_next":  { "type": "string", "description": "Step ID to go to when expression is truthy." },
    "false_next": { "type": "string", "description": "Step ID to go to when expression is falsy." }
  }
}
```

**`mcp_call`**
```json
{
  "type": "object",
  "required": ["mcp_server_slug", "tool_name", "output_var"],
  "properties": {
    "mcp_server_slug": { "type": "string", "description": "Slug of an MCP server configured for this application." },
    "tool_name":       { "type": "string", "description": "Tool name from the MCP server's tools_manifest." },
    "args_template":   { "type": "string", "description": "JSON Go template rendered into MCP tool arguments." },
    "output_var":      { "type": "string", "description": "PipelineVars key written with the MCP tool result." }
  }
}
```

**`response`**
```json
{
  "type": "object",
  "required": ["from_var"],
  "properties": {
    "from_var":   { "type": "string", "description": "PipelineVars key whose value is sent as the agent response." },
    "media_type": { "type": "string", "default": "text/plain", "description": "MIME type of the response." }
  }
}
```

**`input`**
```json
{
  "type": "object",
  "properties": {
    "bindings": {
      "type": "object",
      "description": "Maps incoming message part types to PipelineVars keys. Default: {\"text\": \"input\"}.",
      "additionalProperties": { "type": "string" }
    }
  }
}
```

### Implementation

1. Add `ConfigSchema json.RawMessage \`json:"config_schema,omitempty"\`` to `NodeDef` and `NodeTypeInfo` in `noderegistry.go`.
2. Update `ToInfo()` to copy `ConfigSchema`.
3. For each `RegisterNode(NodeDef{...})` call in `nodes.go`, add a `ConfigSchema: json.RawMessage(\`{...}\`)` literal.
4. No handler changes — the existing `/node-types` handler calls `AllNodeTypeInfos()` which calls `ToInfo()`.

---

## 5. Component 2: Canvas Definition Wire Format Schema (Phase 1)

### Problem

The `canvasDefinition` struct is internal to `agentgen/compiler.go`. No endpoint documents the shape of the JSON an LLM must submit as a definition. Without this, an LLM building an agent must reverse-engineer the format from validation errors.

### Design

**`GET /api/v1/admin/agent-definitions/schema`**

Auth: Admin JWT (consistent with neighbor endpoints under `/agent-definitions`)  
Implementation: static Go constant in a new handler in `go/internal/admin/`. No DB query.

This endpoint returns two coupled documents: the wire format schema and the validation issue code reference. They belong together because an LLM needs both to build a definition and understand why validation rejected it.

**Response:**

```json
{
  "canvas_definition_schema": {
    "type": "object",
    "required": ["agent_root", "skills"],
    "properties": {
      "agent_root": {
        "type": "object",
        "required": ["display_name"],
        "properties": {
          "display_name": { "type": "string" },
          "description":  { "type": "string" },
          "version":      { "type": "string", "default": "1.0.0" },
          "icon":         { "type": "string" },
          "category":     { "type": "string" },
          "default_model":{ "type": "string", "description": "Default LLM model identifier for LLM steps that omit provider/model." },
          "capabilities": {
            "type": "object",
            "properties": {
              "streaming":          { "type": "boolean", "default": false },
              "push_notifications": { "type": "boolean", "default": false }
            }
          }
        }
      },
      "skills": {
        "type": "array",
        "minItems": 1,
        "description": "Each skill is an independently callable pipeline within this agent.",
        "items": {
          "type": "object",
          "required": ["skill_id", "name", "steps"],
          "properties": {
            "skill_id":    { "type": "string", "description": "Unique within this definition. Becomes SkillSpec.ID." },
            "name":        { "type": "string" },
            "description": { "type": "string" },
            "tags":        { "type": "array", "items": { "type": "string" } },
            "input_modes": { "type": "array", "items": { "type": "string" }, "default": ["text/plain"] },
            "output_modes":{ "type": "array", "items": { "type": "string" }, "default": ["text/plain"] },
            "steps": {
              "type": "array",
              "description": "Unordered step list. Execution order is determined by next/branch references via topological sort.",
              "items": {
                "type": "object",
                "required": ["id", "type"],
                "properties": {
                  "id":    { "type": "string", "description": "Unique within this skill. Referenced by next and branch entries." },
                  "label": { "type": "string" },
                  "type":  { "type": "string", "description": "Registered StepType. See GET /api/v1/admin/node-types." },
                  "config":{ "type": "object", "description": "Typed config. Shape defined by NodeTypeInfo.config_schema for this type." },
                  "next":  { "type": "array", "items": { "type": "string" }, "description": "Step IDs to execute after this step. Must satisfy EdgeRules.MaxOut for this type." },
                  "branches": {
                    "type": "array",
                    "description": "Used by branch nodes. Overrides next when condition is met.",
                    "items": {
                      "type": "object",
                      "properties": {
                        "condition": { "type": "string" },
                        "next":      { "type": "array", "items": { "type": "string" } }
                      }
                    }
                  },
                  "inputs": {
                    "type": "object",
                    "description": "Explicit port bindings. Key = port ID from NodeTypeInfo.input_ports[].id. Optional: compiler infers bindings when omitted.",
                    "additionalProperties": {
                      "type": "object",
                      "required": ["from_step", "from_port"],
                      "properties": {
                        "from_step": { "type": "string" },
                        "from_port": { "type": "string" }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  },
  "validation_issue_codes": [
    { "code": "INVALID_JSON",          "severity": "error",   "meaning": "Definition is not valid JSON." },
    { "code": "MISSING_FIELD",         "severity": "error",   "meaning": "Required field absent (display_name, skill_id)." },
    { "code": "DUPLICATE_SKILL",       "severity": "error",   "meaning": "Two skills share the same skill_id." },
    { "code": "INVALID_SLUG",          "severity": "error",   "meaning": "agent_slug does not match ^[a-z][a-z0-9_-]{0,47}$." },
    { "code": "DUPLICATE_STEP",        "severity": "error",   "meaning": "Two steps in a skill share the same id." },
    { "code": "UNKNOWN_STEP_TYPE",     "severity": "error",   "meaning": "type is not in the node registry. Check GET /api/v1/admin/node-types." },
    { "code": "DANGLING_NEXT",         "severity": "error",   "meaning": "A next entry references a step id that does not exist in this skill." },
    { "code": "DANGLING_BRANCH",       "severity": "error",   "meaning": "A branch next entry references a step id that does not exist." },
    { "code": "MISSING_INPUT_EDGE",    "severity": "error",   "meaning": "A non-source step has fewer incoming edges than EdgeRules.MinIn for its type." },
    { "code": "TOO_MANY_INPUT_EDGES",  "severity": "error",   "meaning": "A step has more incoming edges than EdgeRules.MaxIn for its type." },
    { "code": "SOURCE_HAS_INPUT",      "severity": "error",   "meaning": "A node with is_source=true has incoming edges. Source nodes must have no predecessors." },
    { "code": "MISSING_OUTPUT_EDGE",   "severity": "error",   "meaning": "A non-sink step has fewer outgoing next entries than EdgeRules.MinOut." },
    { "code": "SINK_HAS_OUTPUT",       "severity": "error",   "meaning": "A node with is_sink=true has outgoing next entries. Sink nodes must terminate." },
    { "code": "TOO_MANY_OUTPUT_EDGES", "severity": "error",   "meaning": "A step has more next entries than EdgeRules.MaxOut (0 = unlimited)." },
    { "code": "CYCLE_DETECTED",        "severity": "error",   "meaning": "The step graph contains a cycle. Pipelines must be DAGs (loop nodes excluded)." },
    { "code": "NODE_NOT_EXECUTABLE",   "severity": "error",   "meaning": "Step type is registered but Execute=nil (stub node). Cannot publish. Use Validate (not publish) to test draft definitions with stubs." },
    { "code": "UNRESOLVED_INPUT",      "severity": "error",   "meaning": "A required input variable is not guaranteed to be defined on all execution paths reaching this step. Add a step that writes the variable on the missing path, or make the input optional." },
    { "code": "BROKEN_BINDING",        "severity": "error",   "meaning": "An explicit inputs binding references a from_step or from_port that does not exist." },
    { "code": "INVALID_CONFIG",        "severity": "error",   "meaning": "Per-node Validate func rejected the config. See message for specifics (e.g. mcp_call: mcp_server_slug is required)." }
  ]
}
```

### Implementation

New file `go/internal/admin/agent_definition_schema.go` with a single handler returning the above constant. Route registered in `BuildRouter` alongside the existing agent-definitions routes:

```go
r.Get("/agent-definitions/schema", agentDefSchemaHandler)
```

Must be registered before the `{id}` parameter routes to avoid routing ambiguity.

---

## 6. Component 3: Transform Function Catalog with Examples (Phase 1 — verify first)

### Status assessment

`GET /api/v1/admin/transform-functions` calls `transform.Catalog()` which returns `[]FunctionDef`. `FunctionDef` already contains `Examples []Example`. If the HTTP handler serializes the full struct (no field omission), this component is already complete.

**Verify before implementing anything:** call the endpoint and confirm `examples` is present in the response. If it is absent (e.g. the handler uses a projection struct), add the `examples` field.

### Why examples matter for LLM use

The transform node's `config` is a chain of `{fn, input_var, output_var, args}` entries. An LLM cannot construct this chain without knowing what `strip_fences`, `json_path`, or `extract_code_block` do and what arguments they accept. The `FunctionDef.Examples` field — with `In`, `Args`, and `Out` — is the only way to convey function behavior without natural language documentation.

### Transform test endpoint

`POST /api/v1/admin/transform-test` already accepts a chain and sample input and returns `TraceResult` (per-step: fn, input_var, output_var, in, out, error, ok, duration_ns). An LLM building a transform chain can use this to verify behavior before embedding the chain in an agent definition.

---

## 7. LLM Workflow — Canvas Agent Builder

The complete endpoint sequence for an LLM building a canvas agent from scratch:

```
1. GET /api/v1/admin/node-types
   → Receives []NodeTypeInfo, each with config_schema (after Component 1).
   → Learns: which step types exist, edge constraints (min/max in/out),
     which are stubs (executable=false), what config fields each type requires.

2. GET /api/v1/admin/agent-definitions/schema
   → Receives canvas definition wire format JSON Schema + validation issue code reference.
   → Learns: exact shape of {agent_root, skills[{skill_id, name, steps[{id, type, config, next}]}]}.

3. GET /api/v1/admin/transform-functions
   → Receives []FunctionDef with examples.
   → Learns: how to construct TransformStepConfig.Functions[].

4. GET /api/v1/admin/agents?enabled=true        (for a2a_call steps)
   GET /api/v1/admin/mcp-servers                 (for mcp_call steps)
   → Learns: which a2a_call targets and mcp_call server slugs/tool names are valid.

5. POST /api/v1/admin/agent-definitions
   Body: {"agent_slug": "my-agent", "definition": null}
   → Creates draft. Response: {id: "<draft-id>", ...}

6. Construct canvas definition JSON following the wire format schema.

7. POST /api/v1/admin/agent-definitions/{draft-id}/validate
   Body: {"definition": <constructed JSON>}
   → Receives AgentValidationReport: {valid, issues[{severity, code, node_id, field, message}], step_contracts}
   → If valid=false:
       For each issue: read code from validation_issue_codes (from step 2), apply correction.
       Loop back to step 6.
   → step_contracts: verify VarRef inputs/outputs are satisfied between steps.

8. POST /api/v1/admin/agent-definitions/{draft-id}/publish
   → Receives AgentPublishResult: {agent_id, definition_id, revision, spec_hash}
   → Agent is now available as adapter_type=canvas_a2a in the agent registry.
```

Note on step 7: the validate body is optional — when `definition` is provided in the body, the endpoint validates that JSON directly without reading the DB-stored draft. The LLM can call validate before even saving the definition. The draft ID in the URL path is still required (for tenant auth), but the body definition takes precedence.

---

## 8. Component 4: Run Execution Trace (Phase 2 — requires new instrumentation)

### Problem

When a canvas-agent run fails, the current recording captures:
- `them.run_steps`: agent-level invocations (agent slug, input JSON, output, latency, status)
- `them.runs.error`: a raw error string

Canvas agents execute typed pipeline steps (`input → http → llm → response`). Individual step execution — which PipelineVars were available, what each step read and wrote, which step failed and why — is not recorded. The interpreter's `Execute` loop enforces VarRef contracts at runtime but persists nothing.

This gap makes the AI Debugger impossible without new instrumentation.

### Design

New DB table:

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
    status          TEXT NOT NULL,  -- "completed" | "failed" | "skipped"
    config_snapshot JSONB,          -- step config at execution time (credentials excluded)
    inputs          JSONB,          -- PipelineVars read by this step (credentials excluded)
    outputs         JSONB,          -- PipelineVars written by this step (credentials excluded)
    error           TEXT
);
CREATE INDEX ON them.run_pipeline_steps(run_id, seq);
```

**New endpoint:**

`GET /api/v1/runs/{id}/trace`

For canvas-agent runs (identifiable by the presence of `run_pipeline_steps` rows for this run_id):

```json
{
  "run_id": "770e8400-e29b-41d4-a716-446655440000",
  "agent_id": "...",
  "definition_id": "...",
  "skill_id": "main",
  "status": "failed",
  "pipeline_steps": [
    {
      "step_id": "step-1",
      "type": "input",
      "seq": 1,
      "started_at": "2026-08-27T10:00:00.000Z",
      "completed_at": "2026-08-27T10:00:00.001Z",
      "duration_ms": 0,
      "status": "completed",
      "outputs": { "input": "what is the refund policy?" }
    },
    {
      "step_id": "step-2",
      "type": "http",
      "seq": 2,
      "started_at": "2026-08-27T10:00:00.002Z",
      "completed_at": "2026-08-27T10:00:01.240Z",
      "duration_ms": 1238,
      "status": "completed",
      "config_snapshot": { "method": "GET", "url_template": "https://api.example.com/kb" },
      "outputs": { "kb_response": "{\"articles\": [...]}" }
    },
    {
      "step_id": "step-3",
      "type": "llm",
      "seq": 3,
      "started_at": "2026-08-27T10:00:01.241Z",
      "completed_at": "2026-08-27T10:00:04.441Z",
      "duration_ms": 3200,
      "status": "failed",
      "config_snapshot": { "provider": "anthropic", "model": "claude-sonnet-5" },
      "inputs": { "input": "what is the refund policy?", "kb_response": "(truncated for display)" },
      "error": "context length exceeded: 128000 tokens"
    }
  ],
  "failed_step_id": "step-3",
  "error": "context length exceeded: 128000 tokens"
}
```

For non-canvas runs (Application topology runs using `them.run_steps`), the existing response format is returned unchanged.

### Credential safety (non-negotiable)

`InvocationContext.AgentParams` and `AppAPIKey` are already marked `// NEVER logged` in the interpreter. The trace persistence must enforce the same rule at the write layer:

- Before writing `inputs` or `outputs` JSONB, remove any key whose name matches a registered `AppParamDecl.Key` for this agent's spec.
- The `AgentSpec.RequiredParams` list is available in the `InvocationContext.Spec`. Use it to build a blocklist before persisting.
- This rule must be covered by an explicit test that verifies credential keys do not appear in `run_pipeline_steps` rows even when they are present in `PipelineVars` at execution time.

### Implementation

The interpreter's `Execute` loop in `interpreter.go` must emit step-level records. Preferred approach: pass a `StepRecorder` interface (similar to `runrecorder.Recorder`) into the `Interpreter` via a constructor option. When nil (default), no persistence occurs — existing behavior is unchanged. When set (production canvas-agent runs), each step writes a row.

The `InvocationContext` already carries `RunID` implicitly via the caller — this needs to be made explicit as a field if it is not already.

---

## 9. Component 5: AI Debugger Workflow (Phase 2)

### Design principle

The debugger reads the **exact AgentSpec used by the failing run**, not the current draft. The published AgentSpec is stored in `agent_runtime_specs.spec_json`. `AgentSpec.DefinitionID` links back to the `agent_definitions` row. Both are available without any new schema.

### Workflow: diagnose → preview patch → validate → user approval → apply

**Step 1 — Diagnose**

Inputs:
- `GET /api/v1/runs/{id}/trace` → `pipeline_steps`, `failed_step_id`, `error`
- Published AgentSpec from `agent_runtime_specs` (via `agents.id` on the run)
- `GET /api/v1/admin/agent-definitions/{definition_id}/params` → parameter fill status

Cross-reference `failed_step_id` against the AgentSpec's compiled `StepSpec` to get the step's `Type` and compiled `Config`. Cross-reference `config_snapshot` from the trace against `NodeTypeInfo.config_schema` to identify misconfigured fields.

Rule-based classifier output:

```json
{
  "run_id": "...",
  "failed_step": {
    "step_id": "step-3",
    "type": "llm",
    "error": "context length exceeded: 128000 tokens"
  },
  "classification": {
    "code": "context_overflow",
    "is_transient": false,
    "description": "The LLM step received more input tokens than the model context window allows."
  },
  "contributing_factors": [
    {
      "factor": "large_upstream_output",
      "description": "The http step at step-2 wrote a response of ~95000 characters to kb_response without truncation.",
      "evidence": "run_pipeline_steps.outputs[kb_response] length: 95000 characters"
    }
  ]
}
```

Classification codes for canvas-agent runs:

| Code | Trigger |
|---|---|
| `step_not_executable` | step type is a stub (`NODE_NOT_EXECUTABLE` in published spec) |
| `context_overflow` | LLM step error contains "context length" or "context window" |
| `http_timeout` | HTTP step error contains "timeout", or `duration_ms >= timeout_seconds * 1000` |
| `http_error_response` | HTTP step received 4xx or 5xx status code |
| `mcp_unavailable` | MCP step error contains "connection refused" or "no route to host" |
| `unresolved_variable` | step error indicates a required PipelineVars key was missing at runtime |
| `invalid_config` | step error from per-node Validate func |
| `llm_refusal` | LLM step error contains "content policy" or "safety" |
| `unknown` | fallback for all other patterns |

The classifier is rule-based. It does not call an LLM. Ambiguous patterns default to `unknown` rather than producing a confident wrong classification.

**Step 2 — Preview patch**

The debugger generates structured patches against the definition JSON. Patches are presented for user review, not applied automatically.

```json
{
  "proposed_patches": [
    {
      "patch_id": "p1",
      "description": "Insert a transform step after step-2 to truncate kb_response before passing it to the LLM step.",
      "action": "insert_step_after",
      "after_step_id": "step-2",
      "new_step": {
        "id": "step-2b",
        "type": "transform",
        "config": {
          "functions": [
            {
              "fn": "substring",
              "input_var": "kb_response",
              "output_var": "kb_response",
              "args": { "end": "8000" }
            }
          ]
        },
        "next": ["step-3"]
      },
      "also_update": [
        { "step_id": "step-2", "field": "next", "new_value": ["step-2b"] }
      ]
    },
    {
      "patch_id": "p2",
      "description": "Switch the LLM step to a model with a larger context window.",
      "action": "update_config_field",
      "target_step_id": "step-3",
      "field_path": "model",
      "current_value": "claude-sonnet-5",
      "suggested_value": "claude-opus-4-8"
    }
  ]
}
```

**Step 3 — Validate patch**

Apply selected patches to the definition JSON (in memory) and submit for validation:

```
POST /api/v1/admin/agent-definitions/{id}/validate
Body: {"definition": <patched definition JSON>}
```

If `valid=false`, present the Issues to the user before proceeding. An LLM proposing a patch that introduces new structural errors must correct the patch before asking for approval.

**Step 4 — User approval (explicit, not automated)**

The debugger presents:
1. The original failure trace with `failed_step_id` and `error`.
2. Each proposed patch with its description.
3. The validation result (valid or issues).

It does not apply any change until the user explicitly confirms. This is a hard requirement: no automated writes to production agent definitions.

**Step 5 — Apply**

```
PUT /api/v1/admin/agent-definitions/{id}
Body: {"agent_slug": "...", "definition": <patched definition JSON>}
```

Optionally followed by:
```
POST /api/v1/admin/agent-definitions/{id}/publish
```

The apply step uses only existing endpoints. No new write endpoints are required for the debugger.

### Why this order matters

Validate before apply: prevents a patch from introducing new structural errors. User approval before apply: prevents silent automated changes to production agents. Reading the exact spec used by the run: prevents debugging the wrong definition.

---

## 10. Application Topology Layer (Secondary — Phase 1)

This layer addresses platform composition (wiring agents into applications), not agent authoring. It is a lower priority than the canvas-agent foundation above. Brief designs follow; full detail is in the previous document version in git history.

### A. Application Graph Summary

**`GET /api/v1/admin/applications/{id}/ai-summary`**

Returns a flat, LLM-readable view:
- `entry_points[]`: slug, type, access_mode, conversation_token_limit, orchestrator_name
- `orchestrators[]`: id, name, kind, llm_provider, llm_model, max_iterations, and for each:
  - `reachable_agents[]`: slug, tool_name (`agent__{slug}`), description, input_schema, skills (canvas agents include their published AgentSpec summary)
  - `reachable_mcp_tools[]`: tool_name (`mcp__{server}__{tool}`), server_slug, description, input_schema
  - `sub_orchestrators[]`: slug, tool_name (`orch__{name}`), description
- `canvas_warnings[]`: CANVAS_RULES violations with severity and node reference

ETag: SHA-256 hex of `application.updated_at.UnixNano()`. Lets callers detect staleness without re-reading the full summary.

Implementation: join across `app_orchestrators`, `entry_points`, `agents` (via `allowed_agent_ids`), `mcp_servers.tools_manifest` (via `app_orchestrators.mcp_servers` JSONB). No new DB columns.

Canvas agents appearing as `AdapterType=canvas_a2a` in the agent registry are listed with their AgentSpec's `card.description` and `skills` summary.

### B. Structured Application Validation

`compile_graph()` currently raises `HTTPException(status_code=422, detail="<string>")`. This must produce the same structured Issue shape as agentgen:

```json
{
  "valid": false,
  "errors": [
    {
      "code": "EP_HAS_ORCH",
      "severity": "error",
      "path": "nodes[0]",
      "message": "Entry point 'support-ws' has no outgoing edge to an orchestrator.",
      "suggestion": "Add an edge from this entry point node to an orchestrator node."
    }
  ],
  "warnings": [...]
}
```

Application CANVAS_RULES as issue codes: `AT_LEAST_ONE_EP`, `EP_SLUG_NONEMPTY`, `EP_SLUG_UNIQUE`, `EP_SLUG_FORMAT`, `EP_HAS_ORCH`, `ORCH_HAS_AGENT`, `VOICE_EP_NEEDS_STT_TTS`.

This is a Python change. The 422 body shape changes from `{"detail": "string"}` to the structured format above. The frontend currently parses `error.response.data.detail` — the frontend change must ship in the same PR.

A dry-run validate endpoint (`POST /api/v1/admin/applications/{id}/validate`) should also be added, returning 200 always with the ValidationResult body (even when `valid=false`), matching the agentgen pattern.

### C. Unified Tool Manifest

**`GET /api/v1/admin/orchestrators/{id}/tools`**

Returns in one call what an orchestrator can invoke:
- `agents[]`: slug, tool_name, display_name, description, input_schema, output_schema, skills, capabilities
- `mcp_tools[]`: tool_name, server_slug, server_name, description, input_schema
- `sub_orchestrators[]`: slug, tool_name, display_name, description

Implementation: `allowed_agent_ids` → agents table; `mcp_servers` JSONB → `mcp_servers.tools_manifest`; delegatable app_orchestrators reachable from this one. No new DB columns.

---

## 11. Components Deferred or Removed

**Removed: Per-Entry-Point Agent Card** (`GET /.well-known/agent-card/{ep-slug}.json`)

The A2A 1.0 specification defines agent discovery at `/.well-known/agent-card.json` (singular). Sub-path cards are non-standard. Third-party A2A clients that hardcode the root discovery path will not find sub-path cards. This endpoint adds complexity without clear interoperability benefit and is not needed for the Canvas Agent Builder or Debugger.

**Deferred to Phase 3: Graph Version Tracking**

Persisting application graph snapshots (new `them.application_graph_versions` table, SHA-256 hash, run-to-version link, graph-diff endpoint) requires a DB migration and changes to both `compile_graph()` and the run creation path. Useful for the debugger's "graph changed since this run" question, but not a blocker for Phase 1 or 2. Full design in git history (`AI_PLATFORM_FOUNDATION.md` before this revision).

**Deferred: Agent Schema Endpoint** (`GET /api/v1/admin/agents/{id}/schema`)

Partially covered by `GET /api/v1/admin/agent-definitions/{id}/params` (for canvas agents). Full schema endpoint is useful but not blocking Phase 1 or 2.

---

## 12. Implementation Roadmap

### Phase 1 — Canvas Agent Self-Description (no DB migrations required)

| # | Component | What changes | File(s) |
|---|---|---|---|
| 1.1 | Node Type Config Schemas | Add `ConfigSchema json.RawMessage` to `NodeDef` + `NodeTypeInfo`; add literal schema per step in `nodes.go` `init()` | `go/internal/agentgen/noderegistry.go`, `go/internal/agentgen/nodes.go` |
| 1.2 | Canvas Definition Wire Format | New static `GET /api/v1/admin/agent-definitions/schema` handler | `go/internal/admin/agent_definition_schema.go` (new) |
| 1.3 | Transform Catalog Verification | Verify Examples in `/transform-functions` response; fix handler if absent | `go/internal/admin/transform*.go` |
| 1.4 | Application Graph Summary | New `GET /api/v1/admin/applications/{id}/ai-summary` handler | `go/internal/admin/applications.go` or new file |
| 1.5 | Application Validation Issues | Structured Issue-equivalent from `compile_graph`; add dry-run validate endpoint | `app/services/app_compiler.py`, `app/routers/admin_applications.py`, frontend |

Phase 1 alone is sufficient to support an AI Canvas Agent Builder prototype.

### Phase 2 — Run Trace and Debugger (DB migration required)

| # | Component | What changes | Notes |
|---|---|---|---|
| 2.1 | Pipeline Step Trace | New table `them.run_pipeline_steps`; interpreter emits step records via `StepRecorder` interface | Migration + interpreter change; credential-exclusion rule must be tested |
| 2.2 | Trace Endpoint | `GET /api/v1/runs/{id}/trace` extended for canvas-agent step detail | Requires 2.1 |
| 2.3 | Failure Classifier | Rule-based classifier over `run_pipeline_steps` | Requires 2.1; ~70% coverage on real failure patterns |
| 2.4 | Debugger Patch Workflow | Diagnosis + patch preview; reuses existing validate and PUT endpoints for apply | Requires 2.2 + 2.3; no new write endpoints |

### Phase 3 — Graph Versioning (lower priority)

Application graph snapshot table, run-to-version link, graph-diff endpoint. See git history for full design.

---

## 13. Confidence and Risks

### High confidence

**Component 1.1 (node type config schemas).** NodeDef is stable; the typed config structs have been stable across the visible commit history. Adding literal JSON Schema alongside `init()` registration is additive and low-risk. No runtime behavior changes. The only maintenance burden is updating the schema literal when a config struct field changes — this is acceptable given the alternative (reflection-generated schemas with poor descriptions).

**Component 1.2 (canvas definition wire format).** The `canvasDefinition` struct and all compiler constraints are fully documented in `compiler.go`. A static schema endpoint is a serialization of known constants — low risk, no runtime dependencies.

**Component 1.3 (transform catalog).** `FunctionDef.Examples` already exists. If the handler serializes it, this is already done. At worst, one line change to include the field.

**Existing validate endpoint.** Already returns machine-readable `Issue` structs with `Code`, `NodeID`, `Field`. An LLM consuming this today can already perform self-correction loops. Phase 1 does not change this behavior, only the surrounding context that makes it easier to use.

### Medium confidence

**Component 1.5 (application validation issues).** Requires refactoring the Python `compile_graph` error path. This is a breaking API change: the 422 body changes from `{"detail": "string"}` to the structured format. The frontend parses `detail` as a string in multiple places. The Python change and the frontend change must ship together. Risk: any external caller (scripts, integrations) that parses the old `detail` string breaks silently. Mitigation: audit callers before shipping.

**Component 2.1 (pipeline step trace).** The interpreter's `Execute` loop is straightforward, but adding per-step DB writes changes its performance profile. Benchmark write overhead per step on the target hardware before committing to synchronous persistence. If overhead is unacceptable, consider batching writes at skill completion or using a background goroutine with a small in-memory buffer.

**Component 2.3 (failure classifier).** Pattern-matching on error strings covers the common cases. LLM refusal messages and provider-specific error formats vary. Estimate: rule-based classifier handles ~70% of real failures correctly. The remaining ~30% classify as `unknown`, which is honest. The risk is false classification — e.g. a non-transient HTTP error classified as `http_timeout` because duration is close to the timeout value. Mitigate by requiring two independent signals (error message AND timing) for sensitive classifications.

### Lower confidence

**Phase 2 timeline.** Component 2.1 (interpreter instrumentation) is the dependency for the entire debugger. Its complexity depends on whether pipeline-step persistence can be added without restructuring the `Execute` loop. The `Execute` function is currently synchronous and stateless with respect to persistence. The cleanest approach is a `StepRecorder` interface injected via the `Interpreter` constructor — nil by default, wired in for production. Estimate: 2–3 days of focused implementation and testing.

### Key risk — credential safety in trace

`InvocationContext.AgentParams` and `AppAPIKey` are already marked `// NEVER logged` in the interpreter. The `run_pipeline_steps` write path must enforce the same constraint at the persistence layer, not just at the display layer.

Enforcement rule: before writing `inputs` or `outputs` JSONB, remove any key whose name matches any `AgentParamSpec.Key` in `InvocationContext.Spec.RequiredParams`. This blocklist is derived from the same AgentSpec that governed the run, so it is always accurate.

This rule must be covered by an explicit test: create an agent with a secret param, run it, confirm the param key does not appear in `run_pipeline_steps.inputs` or `.outputs` even when it was present in `PipelineVars` during execution.
