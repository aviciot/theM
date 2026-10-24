# AI Assistant Foundation — Design Document
# the-M Canvas Agent Builder + Runtime Debugger
# Status: DESIGN — for implementation by dedicated session
# Last updated: 2026-08-27

---

## Vision

A user describes what they want in plain language. An AI assistant builds the canvas agent
flow on screen, validates it, explains every decision, and helps debug failures when they occur.
The assistant is itself a canvas agent running inside the-M — it uses the same node types,
the same APIs, the same runtime as every other agent. No external service, no separate model
deployment. The platform describes itself.

---

## Guiding Principles

1. **Schema-driven, not prompt-driven.** The AI never works from a static document about the-M.
   It calls live APIs to discover what nodes exist, what functions are available, what agents
   are registered. When the platform evolves, the assistant evolves automatically.

2. **Validate-and-correct loop.** The AI always calls the validate endpoint before presenting
   a result. It self-corrects on errors without user involvement.

3. **The assistant is a canvas agent.** It is built with the same tools it builds for others.
   This proves the platform can host agentic workloads, and it dogfoods every feature.

4. **Debug is first-class.** The assistant can read run logs, step outputs, and error traces.
   It can suggest fixes and offer to apply them.

---

## Two Modes

### Mode A — Builder Assistant
User describes a flow. Assistant builds the canvas JSON, validates it, presents it.
User can refine in natural language. Assistant patches and re-validates.

### Mode B — Debug Assistant
User shares a failing run ID or pastes an error. Assistant reads the run trace,
identifies the broken step, explains why it failed, suggests a fix in the canvas JSON,
and can apply it if the user confirms.

Both modes share the same foundation: enriched schema APIs + run introspection APIs.

---

## Part 1 — What the-M Must Expose (New APIs)

### 1.1 Enriched Node Schema — extend `GET /api/v1/admin/node-types`

**Current response per node:**
```json
{
  "type": "transform",
  "label": "Transform",
  "edges": {"MinIn": 1, "MaxIn": 1, "MinOut": 1, "MaxOut": 0},
  "input_ports": null,
  "output_ports": null
}
```

**Required additions per node:**
```json
{
  "type": "transform",
  "label": "Transform",
  "description": "Evaluates a chain of named functions over pipeline variables. Each function reads one input var and writes one output var. Use this to extract fields from JSON, reformat strings, or compute derived values.",
  "when_to_use": "After http or mcp_call when you need to extract specific fields from the JSON response. Before llm when you need to pass a clean string rather than a raw JSON blob. When you need to rename, reformat, or combine pipeline variables.",
  "when_not_to_use": "Not for conditional branching (use branch). Not for external calls (use http or mcp_call). Not for LLM inference (use llm).",
  "pipeline_reads": "Any pipeline variable — specified per function via input_var.",
  "pipeline_writes": "One new variable per function step, named by output_var.",
  "fields": [
    {
      "name": "functions",
      "type": "array<FunctionStep>",
      "required": true,
      "description": "Ordered list of function calls. Each step reads input_var, applies fn, writes output_var. Steps run in order — a step can read a var written by a previous step.",
      "example": [
        {"fn": "to_string",  "input_var": "http_response", "output_var": "raw_str"},
        {"fn": "json_path",  "input_var": "raw_str",       "output_var": "city",   "args": {"path": "$.city"}},
        {"fn": "concat",     "input_var": "city",          "output_var": "label",  "args": {"prefix": "City: "}}
      ]
    }
  ],
  "edges": {"MinIn": 1, "MaxIn": 1, "MinOut": 1, "MaxOut": 0},
  "input_ports": null,
  "output_ports": null
}
```

**Implementation location:** `go/internal/agentgen/nodes.go` — add `WhenToUse`, `WhenNotToUse`,
`PipelineReads`, `PipelineWrites`, `Fields []FieldDef` to `NodeDef` struct.
The existing `/api/v1/admin/node-types` handler serializes `NodeDef` — extending the struct
is sufficient, no new route needed.

**New struct additions to `NodeDef`:**
```go
type NodeDef struct {
    // ... existing fields ...
    WhenToUse      string     `json:"when_to_use,omitempty"`
    WhenNotToUse   string     `json:"when_not_to_use,omitempty"`
    PipelineReads  string     `json:"pipeline_reads,omitempty"`
    PipelineWrites string     `json:"pipeline_writes,omitempty"`
    Fields         []FieldDef `json:"fields,omitempty"`
    Example        any        `json:"example_config,omitempty"`
}

type FieldDef struct {
    Name        string `json:"name"`
    Type        string `json:"type"`
    Required    bool   `json:"required"`
    Description string `json:"description"`
    Example     any    `json:"example,omitempty"`
}
```

---

### 1.2 Transform Functions Schema — `GET /api/v1/admin/transform-functions`

**Already exists** in `go/internal/admin/router.go`. Returns `FunctionDef` list with
`Name`, `Description`, `Args`, `Examples`. The AI calls this to know what `fn` values
are valid in a transform step and what `args` each requires.

**No change needed.** Just ensure the AI assistant agent calls this endpoint as a tool.

**Verify it is reachable:**
```bash
curl -s -H "Authorization: Bearer $JWT" \
  http://localhost:8088/api/v1/admin/transform-functions | jq '.[0:3]'
```

---

### 1.3 Available Agents — `GET /api/v1/admin/agents`

**Already exists.** Returns all registered agents with slug, endpoint, transport type.
The AI calls this when building a flow that needs an `a2a_call` step — it can show
the user which agents are available and wire the correct slug.

**No change needed.**

---

### 1.4 Available MCP Servers — `GET /api/v1/admin/mcp-servers`

**Already exists.** Returns MCP servers with slug, transport, tool manifest.
The AI calls this when building a flow that needs an `mcp_call` step.

**No change needed.**

---

### 1.5 Run Trace API — new endpoint for debug mode

**New:** `GET /api/v1/runs/{run_id}/trace`

Returns per-step execution details for a completed or failed run:

```json
{
  "run_id": "abc123",
  "status": "failed",
  "steps": [
    {
      "step_id": "fetch",
      "step_type": "http",
      "status": "completed",
      "started_at": "2026-08-27T13:00:00Z",
      "duration_ms": 312,
      "vars_written": {"http_response": "{\"fact\":\"...\",\"length\":42}"},
      "error": null
    },
    {
      "step_id": "extract",
      "step_type": "transform",
      "status": "failed",
      "started_at": "2026-08-27T13:00:00.312Z",
      "duration_ms": 1,
      "vars_written": {},
      "error": "json_path: key \"fact\" not found"
    }
  ]
}
```

**Implementation:** The run recorder already writes `run_steps` rows to Postgres.
This endpoint reads them and joins with the step config from the agent spec.
Location: `go/internal/admin/dal/runs.go` + new handler in `go/internal/admin/runs.go`.

**Why this is the most important new API for debug mode.** Without step-level trace,
the AI can only see the final error. With it, the AI can say: "step `extract` failed
because `json_path` couldn't find key `fact` — this usually means `to_string` was
applied to a map that serializes differently. Try using `http_response_str` as input."

---

### 1.6 Canvas Diff / Patch endpoint — `PATCH /api/v1/admin/agent-definitions/{id}/patch`

**New.** Accepts a JSON Patch (RFC 6902) or a simplified step-level patch:

```json
{
  "ops": [
    {"op": "update_step", "step_id": "extract", "config": {"functions": [...]}},
    {"op": "add_step",    "after": "extract",   "step": {"id": "fmt", "type": "transform", ...}},
    {"op": "rewire",      "from": "extract",    "to": "fmt"},
    {"op": "rewire",      "from": "fmt",        "to": "out"}
  ]
}
```

**Why:** The AI assistant produces a patch, not a full canvas replacement, when the user
asks for a small change. This enables the frontend to animate only the changed nodes,
and it prevents the AI from accidentally overwriting parts of the canvas it didn't intend to touch.

**Implementation:** Service layer reads current definition, applies ops, validates result,
writes updated definition. Returns the new definition + validation issues.

---

### 1.7 Agent Canvas Schema Export — `GET /api/v1/admin/agent-definitions/{id}/schema`

**New.** Returns the compiled schema of a published agent — what vars each step reads and
writes, in the same shape as `step_contracts` from the validate endpoint — but as a
standalone GET so the debug assistant can load it without re-validating.

```json
{
  "agent_id": "...",
  "slug": "catfact-demo",
  "step_contracts": {
    "fetch":   {"inputs": [], "outputs": [{"name": "http_response", "type": "object"}]},
    "extract": {"inputs": [{"name": "http_response"}], "outputs": [{"name": "cat_fact"}, {"name": "cat_length"}]},
    "out":     {"inputs": [{"name": "cat_fact"}], "outputs": []}
  }
}
```

**Implementation:** Read from `agent_runtime_specs.spec` — the compiled spec already has
`Inputs`/`Outputs` per step (populated by the compiler in Stage A/B/C explicit bindings work).

---

## Part 2 — The AI Assistant Agent (canvas agent, runs in the-M)

The assistant is itself a canvas agent. It is built once and reused for both builder and
debug mode. The skill selection distinguishes mode.

### 2.1 Agent Structure

```
Skill: build_flow
  Input → LLM (plan) → HTTP (get_node_types) → HTTP (get_transform_fns)
        → LLM (generate canvas JSON) → HTTP (validate) → LLM (fix if errors)
        → Response (validated canvas JSON)

Skill: debug_run
  Input (run_id) → HTTP (get_run_trace) → HTTP (get_agent_schema)
                 → LLM (diagnose) → Response (diagnosis + suggested patch)
```

In practice: the `http` steps call the-M's own admin APIs using the platform's
internal URL (`http://them-go-bridge:8002`). The LLM steps use the system prompt below.

### 2.2 System Prompt (LLM step)

```
You are a canvas agent builder for the-M, a multi-agent orchestration platform.

You build agent flows as JSON matching the canvas definition schema.
You always call get_node_types before generating any JSON — never guess node configs.
You always call validate_canvas after generating JSON and fix any errors before responding.

Canvas JSON structure:
{
  "schema_version": 1,
  "agent_root": {"display_name": "...", "description": "...", "version": "1.0.0"},
  "skills": [{
    "skill_id": "sk1",
    "name": "...",
    "steps": [
      {"id": "in",   "type": "input",    "config": {},                    "next": ["step2"]},
      {"id": "step2","type": "<type>",   "config": {<see field defs>},    "next": ["out"]},
      {"id": "out",  "type": "response", "config": {"from_var": "<var>"}, "next": []}
    ]
  }]
}

Rules:
- Every skill must start with an "input" step and end with a "response" step.
- Step IDs must be unique within a skill. Use short lowercase names: "fetch", "extract", "out".
- "next" is a list of step IDs that follow this step (control flow).
- HTTP responses are available as "http_response" (map object). Use transform + to_string + json_path to extract fields.
- Transform functions chain: each step reads input_var, writes output_var. Steps run in order.
- Never put secret values in the canvas JSON. Use app_param_ref for credentials.
- When you need to combine two string vars, use concat with prefix/suffix — there is no template function.
```

### 2.3 Tools the LLM step calls

These are implemented as `http` steps in the skill, with the LLM invoking them
via a2a_call or by structuring the pipeline to fetch them first:

| Tool | Canvas step type | Endpoint |
|---|---|---|
| `get_node_types` | http | `GET /api/v1/admin/node-types` |
| `get_transform_functions` | http | `GET /api/v1/admin/transform-functions` |
| `get_available_agents` | http | `GET /api/v1/admin/agents` |
| `get_available_mcp_servers` | http | `GET /api/v1/admin/mcp-servers` |
| `validate_canvas` | http | `POST /api/v1/admin/agent-definitions/{id}/validate` |
| `get_run_trace` | http | `GET /api/v1/runs/{run_id}/trace` (new) |
| `get_agent_schema` | http | `GET /api/v1/admin/agent-definitions/{id}/schema` (new) |

---

## Part 3 — Frontend Integration

### 3.1 "Build with AI" panel in the canvas builder

Location: `frontend/src/app/admin/agents/builder/page.tsx`

A collapsible right drawer (separate from the existing RightPanel properties panel):
- Text area: "Describe your agent..."
- Submit button: sends description to the AI assistant agent via A2A `SendMessage`
- The response is a validated canvas JSON
- Frontend calls `loadCanvasFromJSON(json)` — same function used by the load/import path
- Canvas re-renders with new nodes

**No new API needed.** The frontend calls the AI assistant agent via the existing
`POST /agents/{slug}` A2A endpoint on agent-runtime.

### 3.2 Realtime node animation (Phase 2)

Instead of loading the full JSON at once, the AI assistant streams a sequence of patch ops.
The frontend applies each op as it arrives:

```
SSE stream from agent-runtime (streaming skill):
  data: {"op":"add_node","type":"input","id":"in","position":{"x":50,"y":200}}
  data: {"op":"add_node","type":"http","id":"fetch","config":{...},"position":{"x":250,"y":200}}
  data: {"op":"connect","from":"in","to":"fetch"}
  data: {"op":"add_node","type":"transform","id":"extract",...}
  data: {"op":"connect","from":"fetch","to":"extract"}
  data: {"op":"done","canvas_json":{...}}
```

Frontend patch handler in `page.tsx` applies each op to `localPipeNodes`/`localPipeEdges`
state — nodes visually appear one by one as the AI generates them.

**Requires:** `stream_out` skill on the AI assistant agent + frontend SSE patch consumer.
This is Phase 2 — not required for Phase 1.

### 3.3 Debug panel in the run history view

Location: `frontend/src/app/admin/runs/` (or wherever the run detail view lives)

A "Debug with AI" button on a failed run:
- Sends `run_id` + `agent_id` to the AI assistant debug skill
- Response is a markdown diagnosis: which step failed, why, what to fix
- Optionally includes a suggested canvas patch
- "Apply fix" button sends the patch to `PATCH /agent-definitions/{id}/patch` (new endpoint)
- Canvas opens with the patched definition pre-loaded

---

## Part 4 — Implementation Phases

### Phase 1 — Foundation (implement first, enables everything else)

| Item | Location | Effort |
|---|---|---|
| Enrich `NodeDef` with `WhenToUse`, `WhenNotToUse`, `Fields[]` | `go/internal/agentgen/nodes.go` | Medium — fill in annotations for all 12 node types |
| Enrich `GET /api/v1/admin/node-types` response | Already serializes `NodeDef` — no handler change | Zero once struct is filled |
| `GET /api/v1/runs/{run_id}/trace` | `go/internal/admin/dal/runs.go` + handler | Medium |
| `GET /api/v1/admin/agent-definitions/{id}/schema` | Read from `agent_runtime_specs.spec` | Small |
| Build the AI assistant canvas agent (builder skill) | Canvas builder UI or JSON POST | Small once APIs are ready |

### Phase 2 — Patch + Streaming (enables realtime UX)

| Item | Location | Effort |
|---|---|---|
| `PATCH /api/v1/admin/agent-definitions/{id}/patch` | New service + DAL + handler | Medium |
| Streaming skill on AI assistant agent | `stream_out` node type | Small |
| Frontend patch consumer (SSE node-by-node animation) | `page.tsx` | Medium |

### Phase 3 — Debug mode

| Item | Location | Effort |
|---|---|---|
| Debug skill on AI assistant agent | Additional skill in same agent | Small once trace API exists |
| "Debug with AI" button in run detail view | Frontend | Small |
| "Apply fix" flow (patch + reload canvas) | Frontend + Phase 2 patch endpoint | Depends on Phase 2 |

---

## Part 5 — What NOT to build

- **A separate AI service.** The assistant runs inside the-M as a canvas agent. No new Docker container.
- **A fine-tuned model.** The enriched schema is the training data. The base Claude model reads it at runtime.
- **A separate canvas editor for the AI.** The AI produces JSON; the existing canvas renders it.
- **Hardcoded node knowledge in the system prompt.** The LLM calls `get_node_types` every time. The system prompt only explains the envelope format and rules.
- **MCP tools for canvas mutation (yet).** Phase 1 is JSON-in/JSON-out. True realtime MCP tool control is Phase 2+.

---

## Part 6 — Key Design Decisions & Rationale

### Why is the assistant a canvas agent and not a standalone service?

Because it proves the platform. If the-M can host an agent that builds other agents,
it validates the entire stack: canvas JSON format, compiler, runtime, A2A wire protocol,
tool calling via HTTP steps. Every bug in the platform that would affect a real user
also affects the assistant — it is the most demanding integration test.

### Why does the LLM call the schema API at runtime instead of having it in the system prompt?

A hardcoded system prompt about node types rots the moment a new node is added or a field
is renamed. The validate endpoint will catch the mismatch, but the LLM will keep making
the same mistake until someone updates the prompt. With a live API call, the LLM always
works from current truth.

### Why is `GET /api/v1/runs/{run_id}/trace` the most important new API?

Without step-level trace, debug mode is guessing. The LLM can only see the final error
message and the canvas JSON. With step-level trace it sees exactly which step failed,
what variables were available at that point, and what error the function returned.
This is the difference between "something went wrong in transform" and "json_path failed
because the input was a Go map, not a JSON string — add to_string first."

### Why not expose canvas mutation as MCP tools (add_node, connect, etc.)?

Because it requires a stateful session linking the LLM, the browser tab, and the canvas
editor — a non-trivial coordination problem. Phase 1 (JSON in/out) achieves 80% of the
user value with 20% of the complexity. Phase 2 adds streaming patch ops over SSE which
gives the realtime feel without requiring full bidirectional state synchronization.

### Why is validate-and-correct a core loop, not optional?

The canvas compiler enforces hard constraints (missing required fields, broken edges,
unresolved variable references). An LLM that generates JSON without validating will
produce broken agents. The validate loop means the user always receives a working agent
definition, not a plausible-looking one that fails on publish.

---

## Part 7 — Concrete First Step for the Implementing Session

The implementing session should tackle Phase 1 items in this order:

1. **Enrich all 12 `NodeDef` entries** in `go/internal/agentgen/nodes.go` with
   `WhenToUse`, `WhenNotToUse`, `PipelineReads`, `PipelineWrites`, `Fields[]`, `Example`.
   Run `go test ./internal/agentgen/...` — zero new failures expected.

2. **Add `GET /api/v1/runs/{run_id}/trace`** — DAL reads `run_steps` joined with
   step config from the compiled spec in `agent_runtime_specs`. Handler in
   `go/internal/admin/runs.go`. One new test per step status (completed, failed, skipped).

3. **Add `GET /api/v1/admin/agent-definitions/{id}/schema`** — DAL reads
   `agent_runtime_specs.spec` and returns `step_contracts`. One new test.

4. **Build the AI assistant canvas agent** using the enriched APIs. Start with the
   builder skill only (not debug). Validate it produces a valid canvas JSON for 3
   different user descriptions before declaring Phase 1 complete.

5. **Document the assistant agent's definition ID** in `CURRENT.md` so future sessions
   can update it as the schema evolves.

---

## Appendix — catfact demo agent (working reference)

This agent was built and validated live on 2026-08-27. It demonstrates the full
HTTP → Transform (multi-field extraction) → Response pipeline that the AI assistant
will generate for similar user requests.

```json
{
  "schema_version": 1,
  "agent_root": {
    "display_name": "Cat Fact Multi-Field Demo",
    "description": "HTTP → Transform (extract fact + length) → Response",
    "version": "1.0.0"
  },
  "skills": [{
    "skill_id": "sk1",
    "name": "fetch_cat_fact",
    "input_modes": ["text/plain"],
    "output_modes": ["text/plain"],
    "steps": [
      {"id": "in",      "type": "input",    "config": {},                          "next": ["fetch"]},
      {"id": "fetch",   "type": "http",     "config": {
        "method": "GET",
        "url_template": "https://catfact.ninja/fact",
        "headers": {"Accept": "application/json"},
        "extractions": [],
        "timeout_seconds": 10
      },                                                                             "next": ["extract"]},
      {"id": "extract", "type": "transform","config": {"functions": [
        {"fn": "to_string", "input_var": "http_response",  "output_var": "http_str"},
        {"fn": "json_path", "input_var": "http_str",        "output_var": "cat_fact",   "args": {"path": "$.fact"}},
        {"fn": "json_path", "input_var": "http_str",        "output_var": "cat_length", "args": {"path": "$.length"}},
        {"fn": "concat",    "input_var": "cat_fact",        "output_var": "fact_line",  "args": {"prefix": "Fact: "}},
        {"fn": "concat",    "input_var": "cat_length",      "output_var": "len_line",   "args": {"prefix": "Length: ", "suffix": " chars"}}
      ]},                                                                            "next": ["out"]},
      {"id": "out",     "type": "response", "config": {"from_var": "cat_fact"},    "next": []}
    ]
  }]
}
```

**What this proves:**
- `http_response` is a Go map — `to_string` serializes it to a JSON string before `json_path` can parse it
- Two independent `json_path` calls on the same `http_str` input produce two independent output vars
- Both `cat_fact` and `cat_length` are available downstream — the Response node picks one, but both exist in the pipeline
- Validated with 0 errors, published, invoked via A2A, returned a live cat fact

