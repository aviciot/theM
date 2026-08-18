# Visual Canvas A2A Agent Generation — Full Design Document
# Last updated: 2026-08-18
# Status: Approved design — ready for implementation
# Authors: architecture research (Opus) + session discussion

---

## 0. What Problem This Solves

Today, every A2A agent in the-M is hand-coded:

```
agents/a2a_echo/   — Python FastAPI + a2a-sdk executor + Dockerfile
agents/a2a_slow/   — same
agents/a2a_stream/ — same
```

This contradicts two platform goals:
1. **Python is permanently retired** — new agents should be Go, not Python
2. **the-M is a platform, not a code editor** — users should compose agents visually, not write source code

This document specifies a system where a user designs an A2A agent on a visual ReactFlow canvas and the platform runs it as a live Go A2A agent — no code required.

---

## 1. What the User Actually Does

### Step-by-step user flow

1. Open **Admin → Agents → New Agent → Build Visually**
2. A canvas opens (separate from the application canvas — see §4)
3. User drags blocks from the palette onto the canvas and connects them with arrows
4. Fills in config in the properties panel (model, prompt, URL, condition, etc.)
5. Clicks **Validate** — platform checks for errors (cycles, missing required fields, unresolved secret refs)
6. Clicks **Publish** — platform compiles the canvas, writes a DB row, and starts a live Go A2A agent
7. The agent immediately appears in the **application canvas palette** — it can be wired into any application as a tool

### What the user draws

An agent is a **flowchart**. Each block is a step. Arrows are the data/control flow between steps.

The top-level canvas has two kinds of blocks:
- **Agent Root** — card metadata (name, description, icon, capabilities)
- **Skill blocks** — each skill is one capability of the agent (e.g. "Summarize text", "Search database", "Generate report")

Double-clicking a Skill block opens its **pipeline sub-canvas** — the actual step-by-step logic for that skill.

---

## 2. All Canvas Block Types

### Structure tier (top-level canvas)

| Block | What it does | Config fields |
|---|---|---|
| **Agent Root** | The agent's identity card — served at `/.well-known/agent-card.json` | Display name, description, version, icon, category, streaming on/off, push notifications on/off |
| **Skill** | One capability the agent advertises. The LLM uses `description` to decide when to call this skill | Skill ID, name, description, tags, input MIME types (`text/plain` / `application/json`), output MIME types, input JSON Schema, output JSON Schema |

### Pipeline tier (inside a Skill sub-canvas)

Every skill has a pipeline: a small directed graph of steps, from **Input** to **Response**.

#### Basic steps

| Block | What it does | Config | Difficulty to implement |
|---|---|---|---|
| **Input** | Entry point. Reads the incoming message parts and binds them to named variables (`user_text`, `query`, `payload`, etc.) | Part mapping: part type → variable name | Simple |
| **LLM Step** | Calls Claude. You write a system prompt and a user prompt, both using `{{.var_name}}` template syntax over the pipeline variables. Output stored in a named variable | Model (`claude-opus-4-8` / `claude-sonnet-5` / `claude-haiku-4-5`), system prompt template, user prompt template, max tokens, effort level, output variable name | Simple — reuses existing `internal/llm` Provider |
| **HTTP Tool** | Calls any external REST API. URL and body are templates over variables. Response JSON fields extracted into named variables | Method, URL template, headers (supports `{{secret "NAME"}}` references), body template, JSONPath extractions | Simple |
| **Transform** | Pure data reshaping between steps — rename a variable, combine two strings, extract a JSON field, format a number. No I/O | Expression per output variable (template or jq-style) | Simple |
| **Response** | Terminal block. Takes a variable and emits it as the skill's output artifact. Every pipeline must end here | Source variable, output media type (`text/plain` / `application/json` / `text/html` / `text/markdown`) | Simple |

#### Advanced steps

| Block | What it does | Config | Difficulty |
|---|---|---|---|
| **Branch (conditional)** | Routes execution to one of N downstream paths based on a condition. Supports chaining multiple conditions (if / else-if / else) | Condition expression per branch arm (e.g. `{{eq .status "ok"}}`), default arm | Simple |
| **Loop** | Repeats a set of steps up to N times, or until a boolean condition becomes true. Each iteration can update variables. Emit a streaming artifact chunk each iteration if enabled | Steps to repeat (referenced by ID), max iterations, exit condition, accumulator variable | Medium — requires cycle detection at compile time |
| **Parallel** | Runs N branches of steps concurrently. All branches must complete before the pipeline continues. Results merged into a map variable | Branch definitions (each a sub-list of step IDs), merge strategy (merge all / first wins / concat) | Medium — goroutines + race-safe variable merge |
| **Call Another Agent** | Invokes any other A2A agent registered in the platform — including other canvas-generated agents. Uses the existing `agentregistry` outbound path. Enables **agent composition** | Agent reference `{namespace, name, version}`, input variable, output variable, timeout | Medium — bridge event streams |
| **Human-in-the-Loop** | Pauses the agent, emits `input-required` status back to the caller with a prompt message, waits for a follow-up `message/send` with the same `taskId`, then resumes. Built-in A2A protocol feature | Prompt message to display, variable to bind the human's reply into | Medium — requires task store to hold paused state across HTTP requests |
| **Stream Output** | Instead of waiting until the end, streams partial results to the caller as they arrive from Claude. Each LLM token or chunk is emitted as an artifact event with `last_chunk=false` | Enabled per-step; connected to an LLM Step | Medium — wire LLM stream events → `TaskArtifactUpdateEvent` chunks |

#### Complex steps (hard — later phases)

| Block | What it does | Difficulty |
|---|---|---|
| **Sub-Agent** | Embeds a whole other canvas-designed agent as a single step — full nested execution with its own skill pipeline | Hard — full interpreter reentry |
| **Memory Read / Write** | Reads/writes to the platform's per-session context memory (the existing `history` + `summary` infrastructure in `internal/history`) | Hard — cross-agent memory namespacing |

### What the canvas does NOT support

The step menu is intentionally bounded. These are NOT canvas blocks:
- Arbitrary code execution or scripting
- Dynamic type coercion or reflection
- Complex CEL/OPA policy evaluation
- Database queries (use an HTTP tool to call an API instead)

Bounded = safe, testable, and scannable by the security scanner on publish.

---

## 3. The A2A Protocol — What Backs This

A2A (Agent-to-Agent) is JSON-RPC 2.0 over HTTP. Every agent:
- Serves `/.well-known/agent-card.json` — describes what it can do
- Accepts `POST /` — JSON-RPC `message/send` (blocking or async)
- Accepts `POST /` — JSON-RPC `message/stream` (streaming, SSE)
- Accepts `POST /` — `tasks/get`, `tasks/cancel`, `tasks/list`

### Task lifecycle

```
submitted → working → completed   ← happy path
                    → failed
                    → canceled
                    → input-required → working   ← human-in-the-loop resume
```

`input-required` is a first-class protocol state — it is how the **Human-in-the-Loop** block works. The agent pauses, the caller gets back `input-required`, they send another `message/send` with the same `taskId`, and the agent resumes.

### Message parts

Messages carry typed parts — not just text:

| Part type | Use |
|---|---|
| `text` | Plain text / instructions |
| `data` | Structured JSON (typed input for `application/json` skills) |
| `raw` | Binary (base64-encoded) |
| `url` | Remote file reference |

The **Input** block on the canvas maps incoming parts to pipeline variables by type.

---

## 4. The Go A2A SDK

### Does an official Go SDK exist?

Yes — `github.com/a2aproject/a2a-go/v2` (same org as the Python `a2aproject/a2a-python`).

It provides:
- `a2a` package — all protocol types (`AgentCard`, `AgentSkill`, `Task`, `TaskStatusUpdateEvent`, `TaskArtifactUpdateEvent`, `Message`, `Part`, etc.)
- `a2asrv` package — HTTP server, JSON-RPC dispatch, task store, well-known card handler
- `a2aclient` package — outbound client (for the `Call Another Agent` step)
- Full `message/send`, `message/stream`, `tasks/get`, `tasks/cancel`, `tasks/list` implementation

### The executor interface

```go
type AgentExecutor interface {
    Execute(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
    Cancel(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
}
```

`Execute` returns an iterator that yields events. The server streams them to the caller (SSE for `message/stream`, collects into a result for `message/send`). Events:

```go
a2a.NewSubmittedTask(ec, ec.Message)               // task accepted
a2a.NewStatusUpdateEvent(ec, TaskStateWorking, nil) // working
a2a.NewArtifactEvent(ec, a2a.NewTextPart(text))    // output (can repeat)
a2a.NewStatusUpdateEvent(ec, TaskStateCompleted, nil) // done
// or:
a2a.NewStatusUpdateEvent(ec, TaskStateInputRequired, msg) // paused — human needed
a2a.NewStatusUpdateEvent(ec, TaskStateFailed, errMsg)
```

### The Go version situation

- The SDK requires **Go 1.25** (uses `iter.Seq2` range-over-func iterators)
- `go/go.mod` is currently `go 1.23`
- **However:** Go 1.25 is already installed on this server (`go version go1.25.13 linux/amd64`) and `golang:1.25-alpine` Docker image is already pulled locally

**Upgrading is 4 lines across 4 files:**

| File | Change |
|---|---|
| `go/go.mod` | `go 1.23` → `go 1.25` |
| `Dockerfile.go` | `FROM golang:1.23-alpine` → `FROM golang:1.25-alpine` |
| `Dockerfile.go-worker` | same |
| `Dockerfile.auth-go` | same |

This is the first step of Phase 1 — 5 minutes of work, not a real blocker.

---

## 5. Architecture: How It All Works

### Two existing mechanisms (do not confuse them)

```
go/internal/a2a/         INBOUND  — exposes the orchestrator AS an A2A agent
                                     POST /a2a/{app_slug} + /.well-known/agent.json
                                     External callers call into the platform

go/internal/agentregistry/ OUTBOUND — invokes registered A2A agents
                                     Calls agents in them.agents (transport='a2a_async')
                                     The orchestrator calls out to agents
```

A canvas-generated agent is **outbound** — it is a new external service registered in `them.agents`, invoked by `agentregistry`. It is a peer of `a2a-echo`, not a reuse of the inbound server.

### The two-form split (mirrors the application canvas pattern)

| Form | Stored where | Purpose |
|---|---|---|
| `AgentDefinition` | `them.agent_definitions` (JSONB, immutable revisions) | What the canvas serializes. Human-readable, editable, revisioned |
| `AgentSpec` | `them.agent_runtime_specs` (JSONB) | Compiled form the runtime loads. Resolved refs, frozen prompts, topologically ordered steps |

This mirrors the existing split: application Definition JSON → compiled `app_orchestrators` + `entry_points` projection.

### The generation strategy: Interpreted Runtime (Option B)

One generic Go binary (`them-agent-runtime`) reads an `AgentSpec` JSON and serves any A2A agent. No `go build` per agent.

**Why not per-agent codegen (Option A)?**
- Docker Compose, no Kubernetes — no image registry or rollout story for per-agent builds
- `go build` per agent = minutes of iteration; re-reading JSON = instant
- One security-scanned image vs N images multiplying the scan surface
- Fixed step menu = exactly the case where an interpreter wins over codegen

**Codegen (Option A) becomes an "Export Go source" button** — for developers who want to fork and own the code. The exported source is guaranteed behavior-equivalent to the running interpreted agent because both consume the same `AgentSpec`.

### Runtime topology

**Topology 1 (start here):** One `them-agent-runtime` container serves all canvas agents, routing by agent slug:
```
POST /agents/{slug}/           → JSON-RPC dispatch for that slug's AgentSpec
GET  /agents/{slug}/.well-known/agent-card.json
```

**Topology 2 (scale-out, Phase 4):** One container per agent, `AGENT_SLUG` env var selects the spec. Better isolation, honors per-agent `max_concurrency`. The `AgentSpec` contract makes this a deployment change, not a code change.

### End-to-end flow

```
User draws canvas
        │
        ▼ save draft
POST /api/v1/admin/agent-definitions         → them.agent_definitions (JSONB, revision N)
        │
        ▼ validate
POST /api/v1/admin/agent-definitions/{id}/validate
        │  ├─ check no cycles in step graph
        │  ├─ check all model IDs are valid
        │  ├─ check secret refs are declared in credential_schema
        │  └─ check a2a-call refs resolve to registered agents
        │
        ▼ publish
POST /api/v1/admin/agent-definitions/{id}/publish
        │  ├─ compile: AgentDefinition → AgentSpec (flatten + resolve)
        │  ├─ write them.agent_runtime_specs (JSONB)
        │  ├─ write them.component_definitions (kind='agent') + them.agents   ← ONE TRANSACTION, SHARED UUID
        │  └─ signal runtime to reload: Redis pub/sub them:agents:registry:{tenant_id}
        │
        ▼
them-agent-runtime picks up new spec from DB
        │
        ▼
Agent appears in application canvas palette automatically
(palette reads GET /api/v1/admin/component-definitions, filtered to kind='agent', enabled=true)
        │
        ▼
User wires agent into an application → orchestrator calls it via agentregistry
```

---

## 6. Go Code: The Runtime Spec Types

```go
// go/internal/agentgen/spec.go

// AgentSpec is the compiled form the runtime loads. Frozen at publish time.
type AgentSpec struct {
    Slug         string            `json:"slug"`
    TenantID     string            `json:"tenant_id"`
    Card         CardSpec          `json:"card"`
    Skills       []SkillSpec       `json:"skills"`
    Secrets      []string          `json:"secrets"`    // env var names; values never stored
    DefaultModel string            `json:"default_model"`
}

type CardSpec struct {
    Name               string              `json:"name"`
    Description        string              `json:"description"`
    Version            string              `json:"version"`
    Icon               string              `json:"icon,omitempty"`
    Category           string              `json:"category,omitempty"`
    Capabilities       CapabilitiesSpec    `json:"capabilities"`
}

type CapabilitiesSpec struct {
    Streaming         bool `json:"streaming"`
    PushNotifications bool `json:"push_notifications"`
}

type SkillSpec struct {
    ID          string     `json:"id"`
    Name        string     `json:"name"`
    Description string     `json:"description"`
    Tags        []string   `json:"tags"`
    InputModes  []string   `json:"input_modes"`
    OutputModes []string   `json:"output_modes"`
    Steps       []StepSpec `json:"steps"`  // topologically ordered by compiler
}

// StepSpec is one pipeline node, compiled from the canvas.
type StepSpec struct {
    ID       string          `json:"id"`
    Type     StepType        `json:"type"`
    Config   json.RawMessage `json:"config"`
    Next     []string        `json:"next"`               // step IDs to run after this one
    Branches []BranchArm     `json:"branches,omitempty"` // for branch/loop steps
}

type StepType string
const (
    StepInput      StepType = "input"
    StepLLM        StepType = "llm"
    StepHTTP       StepType = "http"
    StepTransform  StepType = "transform"
    StepResponse   StepType = "response"
    StepBranch     StepType = "branch"
    StepLoop       StepType = "loop"
    StepParallel   StepType = "parallel"
    StepA2ACall    StepType = "a2a_call"
    StepHumanWait  StepType = "human_wait"
    StepStreamOut  StepType = "stream_out"
)

type BranchArm struct {
    Condition string   `json:"condition"` // Go template expression → bool
    Next      []string `json:"next"`
}

// Step config shapes (one per StepType):

type LLMStepConfig struct {
    Provider     string `json:"provider"`       // "anthropic"
    Model        string `json:"model"`           // "claude-opus-4-8" | "claude-sonnet-5" | "claude-haiku-4-5"
    SystemPrompt string `json:"system_prompt"`   // Go text/template over vars
    UserPrompt   string `json:"user_prompt"`
    MaxTokens    int    `json:"max_tokens"`
    Effort       string `json:"effort,omitempty"` // "low"|"medium"|"high"|"xhigh"|"max"
    OutputVar    string `json:"output_var"`
    Stream       bool   `json:"stream"`
}

type HTTPStepConfig struct {
    Method          string              `json:"method"`
    URLTemplate     string              `json:"url_template"`
    Headers         map[string]string   `json:"headers"` // values may be {{secret "NAME"}}
    BodyTemplate    string              `json:"body_template,omitempty"`
    Extractions     []JSONPathExtract   `json:"extractions"`
    CredentialSlot  string              `json:"credential_slot,omitempty"`
    TimeoutSeconds  int                 `json:"timeout_seconds"`
}

type JSONPathExtract struct {
    Var      string `json:"var"`
    JSONPath string `json:"json_path"`
}

type A2ACallStepConfig struct {
    Ref       DefinitionRef `json:"ref"`      // {namespace, name, version}
    InputVar  string        `json:"input_var"`
    OutputVar string        `json:"output_var"`
    TimeoutSeconds int      `json:"timeout_seconds"`
}

type HumanWaitConfig struct {
    Prompt   string `json:"prompt"`   // message shown to user
    ReplyVar string `json:"reply_var"` // variable to bind the human's reply
}

type LoopConfig struct {
    BodySteps     []string `json:"body_steps"`  // step IDs to repeat
    Condition     string   `json:"condition"`    // exit when true (template → bool)
    MaxIterations int      `json:"max_iterations"`
    AccumVar      string   `json:"accum_var,omitempty"` // collect outputs across iterations
}

type ParallelConfig struct {
    Branches  [][]string `json:"branches"`  // each branch is a list of step IDs
    MergeVar  string     `json:"merge_var"` // receives map[branch_index]result
}
```

---

## 7. Canvas Node Types (TypeScript)

```typescript
// frontend/src/app/admin/agents/builder/types.ts

// Top-level canvas nodes
type AgentBuilderNode =
  | { type: 'agent-root'; data: AgentRootData }
  | { type: 'skill';      data: SkillData };

// Pipeline sub-canvas nodes (inside a Skill)
type PipelineNode =
  | { type: 'input';      data: InputStepData }
  | { type: 'llm-step';   data: LlmStepData }
  | { type: 'http-tool';  data: HttpStepData }
  | { type: 'transform';  data: TransformStepData }
  | { type: 'branch';     data: BranchStepData }
  | { type: 'loop';       data: LoopStepData }
  | { type: 'parallel';   data: ParallelStepData }
  | { type: 'a2a-call';   data: A2aCallStepData }
  | { type: 'human-wait'; data: HumanWaitData }
  | { type: 'stream-out'; data: StreamOutData }
  | { type: 'response';   data: ResponseStepData };

interface AgentRootData {
  displayName: string;
  description: string;
  version: string;
  icon?: string;
  category?: string;
  capabilities: { streaming: boolean; pushNotifications: boolean };
}

interface SkillData {
  skillId: string;
  name: string;
  description: string;         // LLM uses this to decide when to call the skill
  tags: string[];
  inputModes: string[];        // ["text/plain"] | ["application/json"]
  outputModes: string[];
  inputSchema: JSONSchema;
  outputSchema: JSONSchema;
}

interface LlmStepData {
  provider: 'anthropic';
  model: 'claude-opus-4-8' | 'claude-sonnet-5' | 'claude-haiku-4-5-20251001';
  systemPrompt: string;        // {{.var_name}} template syntax
  userPrompt: string;
  maxTokens: number;
  effort?: 'low' | 'medium' | 'high' | 'xhigh' | 'max';
  outputVar: string;
  stream: boolean;
}

interface HttpStepData {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';
  urlTemplate: string;
  headers: Record<string, string>;    // values may be {{secret "NAME"}}
  bodyTemplate?: string;
  extractions: { var: string; jsonPath: string }[];
  credentialSlot?: string;
  timeoutSeconds: number;
}

interface BranchStepData {
  arms: { condition: string; label: string }[];  // last arm is default (else)
}

interface LoopStepData {
  condition: string;       // template expression → bool; true = exit
  maxIterations: number;
  accumVar?: string;
}

interface ParallelStepData {
  branchCount: number;
  mergeVar: string;
}

interface A2aCallStepData {
  ref: { namespace: string; name: string; version: number };
  inputVar: string;
  outputVar: string;
  timeoutSeconds: number;
}

interface HumanWaitData {
  prompt: string;
  replyVar: string;
}

interface ResponseStepData {
  fromVar: string;
  mediaType: string;
}
```

---

## 8. Database Schema

Two new tables. No new columns on `them.agents` — all needed columns already exist.

```sql
-- db/0NN_canvas_a2a.sql

-- Design-time: immutable revisions (mirrors application_definitions pattern)
CREATE TABLE them.agent_definitions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    agent_slug      TEXT        NOT NULL,
    revision        INTEGER     NOT NULL,
    definition      JSONB       NOT NULL,        -- AgentDefinition canvas JSON
    definition_hash TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'draft'
                                CHECK (status IN ('draft', 'published')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_slug, revision)
);
CREATE INDEX agent_definitions_tenant_slug ON them.agent_definitions (tenant_id, agent_slug);

-- Runtime: compiled form the runner loads
CREATE TABLE them.agent_runtime_specs (
    id               UUID        PRIMARY KEY,   -- == agents.id == component_definitions.id
    tenant_id        UUID        NOT NULL,
    definition_id    UUID        NOT NULL REFERENCES them.agent_definitions(id),
    spec             JSONB       NOT NULL,       -- AgentSpec (compiled)
    spec_hash        TEXT        NOT NULL,
    deployed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Critical: the subtype FK constraint

Migration 030 added `fk_agents_base_def`: `agents.id` REFERENCES `component_definitions(id)`.

**Publishing a canvas agent must write both rows in one transaction with a shared UUID:**

```sql
BEGIN;

-- 1. Generate one UUID for both rows
-- id = gen_random_uuid() — done in Go before the transaction

-- 2. Base row in component_definitions
INSERT INTO them.component_definitions
    (id, kind, namespace, name, version, display_name, description,
     implementation_type, configuration_schema, default_config,
     capabilities, input_schema, output_schema, credential_schema,
     scope, tenant_id, status, content_hash, enabled)
VALUES
    ($id, 'agent', $namespace, $name, $version, $display_name, $description,
     'canvas_a2a', '{}', '{}',
     $capabilities, $input_schema, $output_schema, $credential_schema,
     'tenant', $tenant_id, 'published', $content_hash, true);

-- 3. Subtype row in agents (FK satisfied — same $id)
INSERT INTO them.agents
    (id, tenant_id, slug, display_name, description,
     transport, endpoint_url,
     input_schema, agent_card, agent_card_url, skills,
     supports_streaming, supports_push, icon, category,
     namespace, version, scope, status, content_hash, enabled)
VALUES
    ($id, $tenant_id, $slug, $display_name, $description,
     'a2a_async', $endpoint_url,
     $input_schema, $agent_card, $agent_card_url, $skills,
     $streaming, $push, $icon, $category,
     $namespace, $version, 'tenant', 'published', $content_hash, true);

-- 4. Write runtime spec
INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, spec, spec_hash)
VALUES ($id, $tenant_id, $definition_id, $spec, $spec_hash);

COMMIT;
```

`endpoint_url` = internal Docker network address:
- Topology 1: `http://them-agent-runtime:9300/agents/{slug}`
- Topology 2: `http://agent-{slug}:9300`

---

## 9. Docker Compose Integration

```yaml
# docker-compose.yml addition (profile: agents)
  them-agent-runtime:
    build:
      context: .
      dockerfile: Dockerfile.agent-runtime
    container_name: them-agent-runtime
    profiles: [agents]
    expose: ["9300"]
    environment:
      - DATABASE_HOST=them-postgres
      - DATABASE_NAME=them
      - DATABASE_USER=them
      - DATABASE_PASSWORD=${DATABASE_PASSWORD}
      - REDIS_HOST=them-redis
      - REDIS_PORT=6379
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY:-}
      - LOG_LEVEL=info
    networks: [them-network]
    depends_on: [them-postgres, them-redis]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9300/healthz"]
      interval: 10s
      timeout: 5s
      retries: 3
```

**No new Traefik routing.** Canvas agents are invoked outbound by `agentregistry` over the internal Docker network — they are not public entry points. The only new external API is the `agent-definitions` CRUD on `them-go-bridge`:

```
Priority 120 — them-go-agent-defs
Rule: PathPrefix(`/api/v1/admin/agent-definitions`)
```

---

## 10. New Admin API Routes

```
POST   /api/v1/admin/agent-definitions               Create draft (save canvas)
GET    /api/v1/admin/agent-definitions               List all (tenant-scoped)
GET    /api/v1/admin/agent-definitions/{id}          Get one (with definition JSON)
PUT    /api/v1/admin/agent-definitions/{id}          Update draft (re-save canvas)
DELETE /api/v1/admin/agent-definitions/{id}          Delete draft (only if not published)

POST   /api/v1/admin/agent-definitions/{id}/validate Validate (returns errors list, never 4xx)
POST   /api/v1/admin/agent-definitions/{id}/publish  Compile + deploy + register

GET    /api/v1/admin/agent-definitions/{id}/export   Export Go source (Option A — zip download)
```

---

## 11. Integration: How the Agent Appears Everywhere

### In the application canvas palette

Because publish writes a `component_definitions` row (kind=`agent`, status=`published`, enabled=`true`), the agent automatically appears in the application canvas palette. The palette fetches:

```
GET /api/v1/admin/component-definitions
```

No extra work. The registry resolver already handles `KindAgent`. Dropping the agent node onto an application canvas wires it as a tool — the orchestrator calls it via `agentregistry`, which looks up `endpoint_url` from `them.agents`.

### In the agent registry

`agentregistry` caches agent configs in Redis at `them:agents:registry:{tenant_id}`. After publish, the runtime signals invalidation via Redis pub/sub on that channel. Within the TTL window (600s default) all workers pick up the new agent.

### In the security scanner

The security scanner agent (`them-security-agent`) runs on demand today. Publishing a canvas agent triggers an automatic scan on the `AgentDefinition` JSON — the same endpoint as `POST /api/v1/admin/agents/{id}/security-scan`. Results stored in `them.agents.last_scan_result`.

---

## 12. Known Issues to Fix Before / During Phase 1

### Well-known path inconsistency

`go/internal/a2a/server.go` serves `/.well-known/agent.json` (missing `-card`).
Python agents, the discover flow, the security scanner, and the official SDK constant all use `/.well-known/agent-card.json`.

**Generated agents must use `agent-card.json`.** The inbound server should also be fixed (separate commit, flag in `docs/LESSONS.md`).

### `CreateAgent` predates the FK

The existing Go `CreateAgent` DAL path (`go/internal/admin/dal/agents.go`) does not create the base `component_definitions` row. It will violate `fk_agents_base_def` if called for canvas agents. Canvas agents **must** use the new `agentgen` publish path (which writes both rows in one transaction). Do not route canvas agent creation through the generic `CreateAgent`.

### `anthropic-sdk-go` not yet in `go.mod`

The Anthropic Go SDK (`github.com/anthropics/anthropic-sdk-go`) is needed by the `llm` step in the runtime. It must be added in Phase 1 (`go get github.com/anthropics/anthropic-sdk-go`). The existing `internal/llm` provider already wraps the Anthropic API — verify whether it's using the Go SDK or raw HTTP before adding a second client. Reuse whichever is there.

---

## 13. Security Considerations

| Risk | Mitigation |
|---|---|
| **Prompt injection** via LLM step | Prompts are frozen at compile time (in `AgentSpec`). Caller input is strictly template *data*, never template source. Validated at publish. |
| **Secret leakage** | Secrets referenced by name only in `AgentSpec` (e.g. `{{secret "STRIPE_KEY"}}`). Actual values come from env vars provisioned to the runtime container via `generate-env.sh`. Never stored in JSONB, never in logs, never in exports. |
| **Runaway loops** | `max_iterations` enforced by the interpreter with a hard ceiling (e.g. 100). Per-step timeouts prevent hanging HTTP/LLM calls. |
| **Tenant isolation** | Every DB row carries `tenant_id`. Runtime scopes spec loading per tenant. Redis keys include tenant ID: `them:agents:registry:{tenant_id}`. |
| **Agent-calling-agent blast radius** | `a2a-call` steps resolve through `agentregistry` → tenant-scoped — agents cannot cross tenant boundaries. |
| **Runtime bug blast radius** | Per-step panic recovery (`recover()`) prevents one bad agent from crashing the runtime for all agents. |
| **Scan on publish** | Security scanner triggered automatically on every publish. |

---

## 14. Implementation Roadmap

Each phase is one self-contained session. Do not begin the next phase in the same session.

### Phase 1 — Go A2A Runtime (`them-agent-runtime`)

**Goal:** prove one hand-written `AgentSpec` file starts a working A2A agent that `agentregistry` can discover and invoke.

Steps:
1. Bump `go/go.mod` to `go 1.25`; update all three Dockerfiles to `golang:1.25-alpine`
2. `go get github.com/a2aproject/a2a-go/v2` — add official SDK
3. New package `go/internal/agentgen/` — `AgentSpec`, `StepSpec`, all config structs
4. New binary `go/cmd/agent-runtime/main.go` — loads spec from DB (or file for dev), builds card, starts A2A HTTP server using SDK
5. Interpreter: `input`, `llm` (reuse `internal/llm`), `transform`, `response` steps only (simplest four)
6. `Dockerfile.agent-runtime` + compose service under `profiles: [agents]`
7. Verify: start runtime with a hand-written spec → `agentregistry` discovers card → `message/send` returns correct output

Tests (update `go/TEST_INDEX.md`):
- Executor lifecycle: submitted → working → artifact → completed
- Card served at `/.well-known/agent-card.json` (not `agent.json`)
- `message/send` round-trip with LLM step mocked
- Input variable binding from text/data parts

### Phase 2 — Agent Builder Canvas UI + Definition CRUD API

**Goal:** user can author and save agent definitions visually. No compile/deploy yet.

Steps:
1. `db/0NN_canvas_a2a.sql` — `agent_definitions` table
2. Go admin routes: `POST/GET/PUT/DELETE /api/v1/admin/agent-definitions` — handlers + DAL + service
3. `frontend/src/app/admin/agents/builder/` — ReactFlow canvas, all node types from §7 (structure tier + pipeline tier), nested skill sub-canvas, palette sidebar, properties panel
4. Wire to new API — save/load/list agent definitions
5. Traefik label for `them-go-agent-defs` at priority 120

Tests: Go DAL/handler CRUD; TypeScript compiles clean.

### Phase 3 — Compile + Publish Pipeline

**Goal:** clicking Publish deploys a live agent that appears in the application canvas.

Steps:
1. `db/0NN_canvas_a2a.sql` — `agent_runtime_specs` table (Phase 2 only added `agent_definitions`)
2. `agentgen.Compile(AgentDefinition) → AgentSpec` — flatten pipeline DAG (topological sort), resolve `a2a-call` refs, validate model IDs, validate secret ref names, detect cycles
3. `POST .../validate` handler — returns `{valid, errors}` (never 4xx; errors are a payload)
4. `POST .../publish` handler — compile → write both DB rows in one transaction (§8 FK constraint) → signal `agentregistry` cache invalidation
5. Runtime: poll `agent_runtime_specs` on boot + reload on Redis pub/sub signal

Tests: compile determinism (hash stability), two-row-one-tx FK invariant, publish→discover→invoke E2E, stale spec replaced on republish.

### Phase 4 — Live Management + Advanced Steps

**Goal:** full lifecycle — update, disable, delete, security scan. Advanced pipeline steps.

Steps:
1. Update (new revision + republish), disable/enable (`them.agents.enabled`), delete (cascade both rows; refuse if referenced by a published application definition)
2. Security scan triggered on publish — store result in `them.agents.last_scan_result`
3. Advanced interpreter steps: `branch`, `loop`, `parallel`, `a2a-call`, `human-wait`, `stream-out`
4. "Export Go source" button — `text/template` codegen → zip download
5. Topology 2 (optional): one container per agent via a lightweight deploy reconciler

Tests: revision bump, referential-integrity guards on delete, scan-on-publish, each new step type's interpreter logic.

---

## 15. Summary

| Question | Answer |
|---|---|
| What does a user do? | Draw a flowchart on a canvas, click Publish |
| What runs the agent? | One generic Go binary (`them-agent-runtime`) interprets the compiled AgentSpec |
| Does it use the official Go SDK? | Yes — after a trivial Go 1.23→1.25 bump (5 minutes; image already pulled) |
| What can a pipeline do? | LLM calls, HTTP tool calls, data transforms, conditionals, loops, parallel branches, human-in-the-loop pauses, agent-to-agent calls, streaming |
| Does it appear in the app canvas? | Yes — automatically, via the component registry |
| New infrastructure? | One new Docker service (`them-agent-runtime`), two new DB tables, one new Traefik rule |
| New columns on `them.agents`? | None — all needed columns already exist |
| How many phases? | Four, one session each |
| What's first? | Phase 1: runtime binary + Go 1.25 bump + 4 basic step types |
