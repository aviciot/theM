---
name: a2a
description: A2A expert guide for the-M platform — Go implementation. Loads ground-truth reference for the hand-rolled JSON-RPC server, AgentSpec/SkillSpec/StepSpec contracts, interpreter pipeline, part types, wire format, and anti-patterns. Invoke before any A2A or canvas agent builder work.
---

# A2A Go Reference — the-M Platform
# Ground truth: actual code in go/cmd/agent-runtime/ and go/internal/agentgen/
# Python is permanently OFF — every A2A reference here is Go only.

---

## 1. Architecture Overview

```
Canvas Builder UI
  ↓ save/publish
AgentDefinition (JSONB in them.agent_definitions)
  ↓ compile (agentgen.Compile)
AgentSpec (JSONB in them.agent_runtime_specs)
  ↓ load at request time (60s in-process cache)
them-agent-runtime (Go, port 9300)
  POST /agents/{slug}              ← A2A JSON-RPC 2.0 endpoint
  GET  /agents/{slug}/.well-known/agent-card.json
```

`them-agent-runtime` is a **stateless Go binary** that reads `AgentSpec` from PostgreSQL and serves any canvas-designed agent. One process serves all agents (topology 1). No per-agent codegen or image build.

**Two mechanisms — never confuse them:**
- `go/internal/a2a/` — **inbound**: exposes an orchestrator as an A2A agent at `POST /a2a/{app_slug}`
- `go/internal/agentgen/` + `go/cmd/agent-runtime/` — **outbound-served**: canvas agents invoked by the orchestrator via `agentregistry`

---

## 2. Wire Format — JSON-RPC 2.0

All A2A communication is `POST /agents/{slug}` with JSON-RPC 2.0 body.

### Request envelope
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "message/send",
  "params": { ... }
}
```

### Methods implemented in them-agent-runtime
| Method | Handler | Notes |
|---|---|---|
| `message/send` | `handleMessageSend` | Runs pipeline, returns completed task |
| `tasks/get` | `handleTasksGet` | Looks up task in Redis by ID |
| `tasks/cancel` | `handleTasksCancel` | Marks task canceled in Redis |
| anything else | `writeJSONRPCError(-32601)` | Method not found |

**Phase D (not yet done):** adopt `github.com/a2aproject/a2a-go/v2` SDK which adds `message/stream`, push notifications, and `tasks/list` for free. Go 1.25.0 is already in go.mod — SDK is unblocked.

### Error codes
| Code | Meaning |
|---|---|
| `-32700` | Parse error (malformed JSON) |
| `-32602` | Invalid params |
| `-32601` | Method not found |
| `-32603` | Internal error (pipeline failed, credential error) |
| `-32001` | Task not found |

---

## 3. Part Types — the A2A Message Contract

A message carries `[]parts`. Each part has exactly ONE of:

| Field | Go struct field | Use |
|---|---|---|
| `kind:"text"` | `Text string` | Plain text, user message |
| `kind:"data"` | `Data json.RawMessage` | Structured JSON input (any shape) |
| `kind:"raw"` | (not yet implemented) | Binary bytes |
| `kind:"url"` | (not yet implemented) | Remote file reference |

### How parts map to pipeline variables

```go
// From handleMessageSend — the authoritative mapping:
inputText := ""
dataVars := map[string]any{}
for _, p := range params.Message.Parts {
    switch p.Kind {
    case "text":
        if p.Text != "" && inputText == "" {
            inputText = p.Text       // → vars["input"]
        }
    case "data":
        if len(p.Data) > 0 {
            var obj map[string]any
            if err := json.Unmarshal(p.Data, &obj); err == nil {
                for k, v := range obj {
                    dataVars[k] = v  // → vars["city"], vars["title"], etc.
                }
            }
        }
    }
}
// Execute call: rt.interp.Execute(ctx, ic, skill, inputText, dataVars)
```

**Rules:**
- First non-empty `text` part → `vars["input"]`
- Each top-level key in a `data` part → named pipeline var
- Both can coexist; `data` keys merge after `"input"` is set
- A `data` key named `"input"` WILL override the text-part input (documented, intentional)

### Wire JSON examples

Text only:
```json
{"parts": [{"kind": "text", "text": "What is the weather in Paris?"}]}
```

Data only (skill declares `input_modes: ["application/json"]`):
```json
{"parts": [{"kind": "data", "data": {"city": "Paris", "units": "celsius"}}]}
```

Mixed (context text + typed data):
```json
{"parts": [
  {"kind": "text", "text": "[prior context summary]"},
  {"kind": "data", "data": {"city": "Paris", "format": "html"}}
]}
```

---

## 4. AgentSpec — the Runtime Contract

`go/internal/agentgen/spec.go` is the single source of truth.

```go
type AgentSpec struct {
    ID              string               // == agents.id == component_definitions.id
    DefinitionID    string               // which agent_definitions revision compiled this
    Slug            string
    TenantID        string
    Card            CardSpec
    Skills          []SkillSpec
    CredentialSlots []CredentialSlotSpec // slot names only, never values
    DefaultModel    string
}

type SkillSpec struct {
    ID          string
    Name        string
    Description string
    Tags        []string
    InputModes  []string   // MIME types: "text/plain" | "application/json"
    OutputModes []string
    Steps       []StepSpec // topologically ordered by compiler
}

type StepSpec struct {
    ID       string
    Type     StepType        // see §5
    Config   json.RawMessage // type-specific config struct
    Next     []string        // step IDs to execute next
    Branches []BranchArm     // for branch steps only
}
```

**Never store secret values in AgentSpec or AgentDefinition JSONB.** Credential slots hold names only; values are resolved at request time from `app_agent_bindings` via Fernet decryption.

---

## 5. Step Types and Config Structs

All in `go/internal/agentgen/spec.go`.

### `input` — bind message parts to vars
```go
type InputStepConfig struct {
    Bindings map[string]string // part_type → variable_name
    // e.g. {"text": "user_query"} → vars["user_query"] = vars["input"]
}
```
The input step runs first. It copies `vars["input"]` into a named var for downstream steps.

### `llm` — call Anthropic
```go
type LLMStepConfig struct {
    Provider        string // "anthropic" (only option)
    Model           string // "claude-opus-4-8" | "claude-sonnet-5" | "claude-haiku-4-5-20251001"
    SystemPrompt    string // Go text/template over pipeline vars
    UserPrompt      string // Go text/template; empty → falls back to vars["input"]
    MaxTokens       int
    Effort          string // optional: "low"|"medium"|"high"|"xhigh"|"max"
    OutputVar       string // var to write completion into; default "output"
    Stream          bool
    ProviderKeySlot string // credential slot name for LLM API key; empty → platform key
}
```
**Template fallback rule:** if `UserPrompt` renders to empty string, the LLM step uses `vars["input"]` as the user message. This is how text-only skills work without an explicit user_prompt template.

### `http` — outbound HTTP tool call
```go
type HTTPStepConfig struct {
    Method          string            // "GET"|"POST"|"PUT"|"DELETE"
    URLTemplate     string            // Go text/template over vars
    Headers         map[string]string // static non-secret headers only
    BodyTemplate    string            // Go text/template; empty → no body
    Extractions     []JSONPathExtract // [{Var, JSONPath}] — dot-separated path into response JSON
    CredentialSlot  string            // slot name; value injected at runtime
    CredentialInject CredentialInject
    TimeoutSeconds  int              // 0 → 30s default
}

type CredentialInject struct {
    Mode          string // "header" (default) | "query" | "basic"
    HeaderName    string // default "Authorization"
    ValueTemplate string // default "Bearer {credential}"; {credential} replaced with slot value
    QueryParam    string // for mode="query"
}
```

### `transform` — pure mapping, no I/O
```go
type TransformStepConfig struct {
    Expressions map[string]string // output_var → Go text/template expression
    // e.g. {"greeting": "Hello, {{.user_query}}!"}
}
```

### `response` — terminal step, emits artifact
```go
type ResponseStepConfig struct {
    FromVar   string // pipeline var to read; default "output"
    MediaType string // "text/plain" | "text/html" | "text/markdown"; default "text/plain"
}
```

### `branch`, `loop`, `parallel`, `a2a_call`, `human_wait`, `stream_out`
Declared in spec.go but **not yet implemented in the interpreter** (Phase 1 scope). Attempting to execute them returns: `"step type %q not implemented in Phase 1"`.

---

## 6. Pipeline Execution — the Interpreter

`go/internal/agentgen/interpreter.go`

```go
// Execute runs the pipeline for a skill.
// inputText → vars["input"]
// extraVars (data part fields) merged into vars after "input" is set.
func (interp *Interpreter) Execute(
    ctx context.Context,
    ic *InvocationContext,
    skill *SkillSpec,
    inputText string,
    extraVars ...map[string]any,
) (*ExecutionResult, error)
```

**Execution order:**
1. Seed `vars = {"input": inputText}`
2. Merge all `extraVars` maps into `vars` (data part fields)
3. Find start step: first `StepInput` step, or `skill.Steps[0]` if none
4. Walk `step.Next[0]` chain until no next step or a cycle is detected
5. `StepResponse` writes `vars[fromVar]` into `ExecutionResult.Text`

**Template engine:** `text/template` with `missingkey=zero` — missing vars render as empty string, not an error.

**InvocationContext** — injected per-request, never cached:
```go
type InvocationContext struct {
    TenantID        string
    ApplicationID   string
    AgentID         string
    BindingID       string
    Credentials     map[string]string  // slot name → decrypted value (NEVER logged)
    ConfigOverrides map[string]any
    Policies        map[string]any
    Spec            *AgentSpec         // set after spec load, before interpreter
}
// String() masks all credential values — safe for logging
```

---

## 7. Agent Card Wire Format

Served at `GET /agents/{slug}/.well-known/agent-card.json` — **note: `agent-card.json` not `agent.json`**.

```json
{
  "name": "My Agent",
  "description": "What this agent does",
  "version": "1.0.0",
  "url": "http://them-agent-runtime:9300/agents/my-agent",
  "capabilities": {
    "streaming": false,
    "pushNotifications": false
  },
  "skills": [
    {
      "id": "skill-1",
      "name": "Skill Name",
      "description": "What this skill does and what JSON fields it expects",
      "tags": ["weather", "data"]
    }
  ]
}
```

`buildAgentCard` in `cmd/agent-runtime/main.go` builds this from `AgentSpec`. **Phase D:** this becomes a proper `a2a.AgentCard` struct from the SDK, with `inputModes`/`outputModes` per skill.

**Discovery:** `agentregistry` fetches this card when an orchestrator invokes the agent. The card drives tool routing — `skill.description` is what the LLM reads to decide which agent to call and what to send.

---

## 8. Invocation Context Headers

The orchestrator (`agentregistry`) passes identity to the runtime via internal-only headers:

| Header | Content | Requirement |
|---|---|---|
| `X-Them-Tenant-Id` | Tenant UUID | Required |
| `X-Them-Application-Id` | Application UUID | Required |
| `X-Them-Agent-Id` | Agent UUID | Required |
| `X-Them-Binding-Id` | Binding UUID | Optional (falls back to app+agent lookup) |

These headers are **only trusted from the internal Docker network**. Phase 3 upgrades to signed JWT (`THE_M_INVOCATION_JWT_KEY`).

---

## 9. Security Invariants — never violate

| Invariant | Where enforced |
|---|---|
| Credential values never in AgentSpec/logs | `InvocationContext.String()` masks them; spec stores slot names only |
| Cross-tenant task isolation | `RedisTaskStore.Get` enforces `tenantID + applicationID` in key |
| Binding stale-check | Runtime rejects if `binding.DefinitionID != spec.DefinitionID` |
| URL slug ↔ agent_id cross-check | `spec.Slug != slug` → 403 (prevents slug squatting) |
| Credential values never in DB JSONB | Only encrypted values in `app_agent_bindings.credential_bindings`; decrypted in-memory only |

---

## 10. Publish Pipeline — what commits a canvas agent to the runtime

Phase 3 (compile/publish) flow:
1. `agentgen.Compile(definition JSONB) → AgentSpec` — validates step types, resolves refs, detects cycles, topological sort
2. Write `them.agent_runtime_specs` (the spec)
3. Write `them.component_definitions` + `them.agents` in **one transaction with a shared UUID** (FK: `agents.id → component_definitions.id`)
4. `endpoint_url = "http://them-agent-runtime:9300/agents/{slug}"`
5. `transport = "a2a_async"` (existing transport, already in CHECK constraint)
6. Pub/sub `them:agents:changed` → agentregistry invalidates cache

**The two-row-one-tx invariant is critical.** `CreateAgent` (generic DAL) does NOT create the `component_definitions` row. Canvas publish uses a dedicated write path in `agentgen` service.

---

## 11. Skill Design Rules

Good skill descriptions drive correct LLM routing — the description is the contract:

```
// Good: tells the LLM exactly what JSON to send
"Fetches current weather for a city. Input: JSON with field 'city' (string, e.g. 'Paris').
 Output: weather summary as plain text."

// Bad: vague, LLM can't construct the input
"Gets weather information."
```

`input_modes` values:
- `"text/plain"` — skill expects a `text` part; `vars["input"]` is the message
- `"application/json"` — skill expects a `data` part; top-level JSON keys become named pipeline vars

Both modes work simultaneously — the runtime always parses both part types and merges them into `vars`.

---

## 12. Common Patterns

### Pattern 1 — Simple text-in, text-out LLM skill
```
input step → llm step (UserPrompt empty → uses vars["input"]) → response step
```

### Pattern 2 — Structured data in, LLM formats it
```
input step → llm step (UserPrompt: "Summarize this data: {{.city}} {{.metric}}") → response step
```

### Pattern 3 — HTTP tool + LLM synthesis
```
input step → http step (fetch data, extract to vars) → llm step (synthesize) → response step
```

### Pattern 4 — Transform then respond (no LLM)
```
input step → transform step (expression: "Hello, {{.name}}!") → response step (fromVar: "greeting")
```

---

## 13. Phase D — SDK Adoption Checklist (not yet done)

When adopting `github.com/a2aproject/a2a-go/v2`:

- [ ] `go get github.com/a2aproject/a2a-go/v2` (go 1.25.0 already in go.mod — unblocked)
- [ ] Replace hand-rolled `jsonRPCRequest`/`writeJSONRPCError` dispatch with `a2asrv.NewJSONRPCHandler`
- [ ] Implement `AgentExecutor` interface: `Execute(ctx, *ExecutorContext) iter.Seq2[a2a.Event, error]`
- [ ] Emit events in order: `a2a.NewSubmittedTask` → `a2a.NewStatusUpdateEvent(Working)` → `a2a.NewArtifactEvent(parts...)` → `a2a.NewStatusUpdateEvent(Completed)`
- [ ] Replace `buildAgentCard` map with proper `a2a.AgentCard` struct
- [ ] Add `inputModes`/`outputModes` per skill to card (currently missing from card output)
- [ ] `tasks/list` and `message/stream` come for free from SDK handler
- [ ] Update `go/TEST_INDEX.md` S1-53 with new tests for SDK wiring

SDK import path: `github.com/a2aproject/a2a-go/v2/a2a`, `a2asrv`, `a2aclient`
Well-known card path constant: `a2asrv.WellKnownAgentCardPath == "/.well-known/agent-card.json"` ✓ matches current

---

## 14. Anti-Patterns — never do these

- **Python SDK patterns** — there is no Python in this runtime; `HasField("data")`, `json_format.MessageToDict`, `AgentExecutor.execute()` with `event_queue` are Python patterns
- **Regex parsing on text parts** — use `kind:"data"` parts with named fields
- **Storing credential values in AgentSpec JSONB** — slot names only
- **`agent.json` as well-known path** — it's `agent-card.json`
- **Using `CreateAgent` DAL for canvas publish** — it does not create the `component_definitions` row; use the dedicated agentgen publish path
- **Global Redis key for agent registry** — must be `them:agents:registry:{tenant_id}` (tenant-scoped)
- **Embedding secret env-var values in Definition or Spec JSONB** — env-var NAMES only

---

## 15. Key File Locations

| What | Where |
|---|---|
| Runtime binary | `go/cmd/agent-runtime/main.go` |
| AgentSpec / StepSpec / all config structs | `go/internal/agentgen/spec.go` |
| Interpreter (Execute, all step handlers) | `go/internal/agentgen/interpreter.go` |
| Compiler (canvas → AgentSpec) | `go/internal/agentgen/compiler.go` |
| InvocationContext, AppAgentBinding | `go/internal/agentgen/context.go`, `binding.go` |
| Redis task store | `go/internal/agentgen/redistaskstore.go` |
| Tests | `go/internal/agentgen/agentgen_test.go` |
| Design doc (full architecture) | `docs/architecture-v2/CANVAS_A2A_AGENT_GENERATION.md` |
| Wire format reference (Python-era, avoid) | `docs/A2A_REFERENCE.md` |
