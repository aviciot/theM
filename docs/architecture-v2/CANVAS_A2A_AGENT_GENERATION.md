# Visual Canvas for A2A Agent Generation

> Status: **Design proposal** — 2026-08-18
> Author: architecture research (Opus)
> Scope: one subsystem per session; this doc spans four phases, each a self-contained session.
> Audience: the-M Go/TypeScript team. Python is permanently retired — every generated artifact is Go.

---

## 0. Executive Summary

Today, A2A agents in the-M are **hand-coded Python** (`agents/a2a_echo/`, `agents/a2a_slow/`, …): a FastAPI app + `a2a-sdk==1.1.0` executor + `make_agent_card()` + Dockerfile per agent. That contradicts the platform's migration goal ("Python is permanently retired"). This document specifies a system where a user **designs an A2A agent visually** on a ReactFlow canvas and the platform **runs it as a Go A2A agent** with no hand-written code.

**Headline recommendation — Option B (interpreted runtime), hybrid with codegen as an opt-in export.**
Ship a single generic Go A2A runner (`them-agent-runtime`) that reads an `AgentDefinition` JSON at startup and serves a spec-compliant A2A agent. Do **not** `go build` per agent in the default path. Codegen (Option A) becomes a "Export Go source" button for users who want to own/fork the code — it is a convenience, not the runtime mechanism. This fits the-M's Docker-Compose, no-Kubernetes, single-Go-gateway reality far better than per-agent image builds.

**Two mechanisms already exist and must not be conflated** (confirmed in codebase research):
- `go/internal/a2a/` — **inbound**: exposes an *orchestrator* as an A2A agent at `POST /a2a/{app_slug}` + card at `/.well-known/agent.json`. Only dispatches `message/send`.
- `go/internal/agentregistry/` — **outbound**: invokes *registered* A2A agents (rows in `them.agents`, `transport='a2a_async'`) with a two-level Redis cache.

A canvas-generated agent is a **new external A2A service** (outbound-invoked), registered in `them.agents`. It is a peer of `a2a-echo`, not a re-use of the orchestrator-as-agent server.

---

## 1. Go A2A Agent Generation Approach

### 1.1 Is there a Go A2A SDK?

**Yes.** `github.com/a2aproject/a2a-go` (official, under the same org as the Python `a2aproject/a2a-a2a-python`).

- Module: `github.com/a2aproject/a2a-go/v2`
- **Requires Go 1.25.0+** (uses `iter.Seq2` range-over-func iterators in the executor interface). **`go/go.mod` is currently `go 1.23`** — this is a confirmed, active blocker, not a hypothetical (see §7).
- Packages: `a2a` (protocol types), `a2asrv` (server), `a2aclient` (client), `a2agrpc` (gRPC binding).
- Transport bindings: JSON-RPC, REST, gRPC.

The SDK gives us a spec-compliant JSON-RPC 2.0 server for free, including `tasks/get`, `tasks/list`, `tasks/cancel`, `message/send`, `message/stream`, push-config, and the well-known card handler. That is a superset of what the hand-coded Python agents and the current Go inbound server implement.

### 1.2 Minimal generated Go A2A agent (using the official SDK)

The core contract is the `AgentExecutor` interface (`a2asrv/agentexec.go`):

```go
type AgentExecutor interface {
    Execute(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
    Cancel(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
}
```

`ExecutorContext` carries the triggering message and task identity:

```go
type ExecutorContext struct {
    Message      *a2a.Message   // nil on cancel
    TaskID       a2a.TaskID
    StoredTask   *a2a.Task
    RelatedTasks []*a2a.Task
    ContextID    string
    Metadata     map[string]any
    User         *User
    // ...
}
func (ec *ExecutorContext) TaskInfo() a2a.TaskInfo // implements a2a.TaskInfoProvider
```

`Execute` returns an event iterator. Events are constructed via `a2a` helpers (all implement `a2a.Event`: `*Message`, `*Task`, `*TaskStatusUpdateEvent`, `*TaskArtifactUpdateEvent`):

```go
a2a.NewSubmittedTask(execCtx, execCtx.Message)                    // *Task
a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil)      // working
a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(result))           // output artifact
a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil)    // done
```

A minimal `main.go` the runner would run (or codegen would emit):

```go
package main

import (
    "context"
    "iter"
    "net/http"
    "os"

    "github.com/a2aproject/a2a-go/v2/a2a"
    "github.com/a2aproject/a2a-go/v2/a2asrv"
)

type echoExecutor struct{ def *AgentDefinition }

func (e *echoExecutor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
    return func(yield func(a2a.Event, error) bool) {
        text := firstText(ec.Message) // helper: read parts
        if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
            return
        }
        if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
            return
        }
        out, err := e.runPipeline(ctx, ec, text) // §4.4 — LLM / HTTP / transform steps
        if err != nil {
            yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))), nil)
            return
        }
        if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(out)), nil) {
            return
        }
        yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
    }
}

func (e *echoExecutor) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
    return func(yield func(a2a.Event, error) bool) {
        yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
    }
}

func main() {
    def := loadDefinition(os.Getenv("AGENT_DEFINITION_PATH")) // Option B: read JSON at boot
    card := buildAgentCard(def)                                // §4.2

    handler := a2asrv.NewHandler(&echoExecutor{def: def})
    mux := http.NewServeMux()
    mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
    mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
    // a2asrv.WellKnownAgentCardPath == "/.well-known/agent-card.json"
    _ = http.ListenAndServe(":"+envOr("PORT", "9300"), mux)
}
```

The **only** thing that differs between two agents is `def` (the `AgentDefinition`) and the `card`. That is the entire argument for Option B: the code is identical; only the data changes.

### 1.3 If the Go SDK is unusable (Go 1.25 blocker) — minimal hand-rolled server

If the-M cannot adopt Go 1.25 in the near term (see §7), a hand-rolled JSON-RPC 2.0 server is small and we already have a reference: `go/internal/a2a/server.go`. It currently implements `message/send` only. A standalone runner would extend it with `tasks/get` + `tasks/cancel` and the correct well-known path. The wire structs are ~60 lines (already written in `internal/a2a`), reusable verbatim. This is the **fallback** design, kept minimal:

```go
// Reuse the existing internal/a2a wire structs (rpcRequest/rpcResponse/rpcResult/rpcArtifact/rpcTextPart).
// Add an in-memory task store keyed by taskId for tasks/get + tasks/cancel.
switch req.Method {
case "message/send": ...   // run pipeline synchronously, return rpcResult
case "tasks/get":    ...   // look up stored task
case "tasks/cancel": ...   // mark canceled
default: writeError(-32601, "method not found")
}
// Card at GET /.well-known/agent-card.json (note: NOT agent.json — see §7 mismatch)
```

**Recommendation:** target the official SDK if Go 1.25 is acceptable; otherwise ship the hand-rolled variant now and swap to the SDK when the toolchain is bumped. The `AgentDefinition` → runtime contract (§3) is identical either way, so this choice is reversible.

### 1.4 Template-generating Go from a canvas (Option A, export-only)

For the "Export Go source" button, `text/template` over the `AgentDefinition`:

```
them-agent-gen/
  templates/
    main.go.tmpl        // the §1.2 skeleton, parameterized by {{.Def.Slug}} etc.
    executor.go.tmpl    // step pipeline unrolled into explicit Go (one func per step)
    card.go.tmpl        // buildAgentCard literal
    Dockerfile.tmpl
    go.mod.tmpl
  gen.go                // exec templates -> tar.gz, optionally `go build` in a scratch container
```

The generated `executor.go` unrolls the interpreted pipeline into named functions — the same logic Option B runs dynamically, made static and forkable. Because both paths consume the identical `AgentDefinition`, the exported source is guaranteed behavior-equivalent to the running interpreted agent.

---

## 2. Canvas Design for A2A Agent Authoring

### 2.1 How this differs from the existing application canvas

The existing canvas (`frontend/src/app/admin/applications/page.tsx`, ~6451 lines) composes an **application**: orchestrator nodes + entry-point nodes + agent (tool) nodes → compiled by the Go publish service into `app_orchestrators` + `entry_points`. Its unit of composition is *wiring already-existing components together*.

The Agent Builder composes the **internals of one agent**: skills, input/output schema, and per-skill execution pipelines (LLM call, HTTP tool call, data transform, response). Its unit of composition is *defining a new component*. The output is not a runtime projection into `app_orchestrators`; it is a `component_definitions` (kind=`agent`) row + `agents` subtype row + a deployed runtime.

**Decision: separate canvas view — "Agent Builder"** — reached from the Agents page ("New Agent → Build visually"), not a mode of the application Definition view. Rationale: different node vocabulary, different palette, different compile target, different validation. Sharing one 6451-line component would couple two unrelated compile pipelines. Reuse the ReactFlow *infrastructure* (node/edge model, drag-drop, autolayout, save plumbing, the `frontend/src/lib/api.ts` client) but not the node set.

### 2.2 Node types

An A2A agent = one card + N skills; each skill = a pipeline (a small DAG) that turns an input message into an output artifact. Two tiers of nodes:

**Structure tier** (the card):
| Node | Purpose | Config |
|---|---|---|
| `agent-root` | The agent card metadata | display_name, description, version, icon, category, `capabilities.streaming` |
| `skill` | One `AgentSkill` | id, name, description, tags[], inputModes[], outputModes[], input_schema (JSON Schema), output_schema |

**Pipeline tier** (inside a skill — the executor logic):
| Palette node | Runtime step type | Config |
|---|---|---|
| `input` | `input` | Binds the incoming message; maps message parts → named variables per input_schema |
| `llm-step` | `llm` | model (`claude-opus-4-8` default; `claude-sonnet-5`/`claude-haiku-4-5`), system prompt template, user prompt template (Go `text/template` over vars), max_tokens, effort |
| `http-tool` | `http` | method, URL template, headers (secret refs), body template, response JSONPath extract |
| `transform` | `transform` | pure mapping — jq-style expression or templated string; no I/O |
| `branch` | `branch` | condition expression → routes to one of N downstream steps |
| `a2a-call` | `a2a_call` | invoke another registered agent by `{namespace,name,version}` (reuses `agentregistry`) — enables agent composition |
| `response` | `response` | terminal — the artifact returned to the caller; maps a var → output part(s) |

Edges are **data/control flow** between pipeline steps within a skill. A `skill` node contains (or links to) its own pipeline sub-graph. Recommended UX: double-clicking a `skill` node opens its pipeline sub-canvas (nested ReactFlow), keeping the top level readable.

### 2.3 Node type definitions (TypeScript / JSON shape)

Canvas node data, mirroring the existing canvas's ReactFlow node convention:

```typescript
// frontend/src/app/admin/agents/builder/types.ts
type AgentBuilderNode =
  | { type: 'agent-root'; data: AgentRootData }
  | { type: 'skill';      data: SkillData }
  | { type: 'input';      data: InputStepData }
  | { type: 'llm-step';   data: LlmStepData }
  | { type: 'http-tool';  data: HttpStepData }
  | { type: 'transform';  data: TransformStepData }
  | { type: 'branch';     data: BranchStepData }
  | { type: 'a2a-call';   data: A2aCallStepData }
  | { type: 'response';   data: ResponseStepData };

interface AgentRootData {
  displayName: string;
  description: string;
  version: string;            // semver-ish, maps to component_definitions.version (int) via ordinal
  icon?: string;              // Material Symbols name -> them.agents.icon
  category?: string;          // -> them.agents.category
  capabilities: { streaming: boolean; pushNotifications: boolean };
}

interface SkillData {
  skillId: string;            // AgentSkill.id
  name: string;
  description: string;
  tags: string[];
  inputModes: string[];       // e.g. ["text/plain"]
  outputModes: string[];
  inputSchema: JSONSchema;    // -> them.agents.input_schema for the primary skill
  outputSchema: JSONSchema;
  pipeline: string;           // id of the sub-graph (nested canvas) implementing it
}

interface LlmStepData {
  provider: 'anthropic';
  model: 'claude-opus-4-8' | 'claude-sonnet-5' | 'claude-haiku-4-5';
  systemPrompt: string;       // Go text/template over pipeline vars
  userPrompt: string;
  maxTokens: number;
  effort?: 'low' | 'medium' | 'high' | 'xhigh' | 'max';
  outputVar: string;          // name bound with the completion
}

interface HttpStepData {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE';
  urlTemplate: string;
  headers: Record<string, string>;     // values may be {{secret "NAME"}} refs
  bodyTemplate?: string;
  extract?: { var: string; jsonPath: string }[];
  credentialSlot?: string;             // -> CredentialSlot on the component def
}

interface A2aCallStepData {
  ref: { namespace: string; name: string; version: number }; // DefinitionRef minus kind
  inputVar: string;
  outputVar: string;
}

interface ResponseStepData { fromVar: string; mediaType: string; }
```

---

## 3. The Generation Pipeline (end-to-end)

### 3.1 Flow

```
Canvas (Agent Builder)
  │  save
  ▼
AgentDefinition JSON  ──►  POST /api/v1/admin/agent-definitions   (new Go route)
  │                              │ validate (schema, cycles, model IDs, secret refs)
  │                              ▼
  │                        them.agent_definitions  (immutable revision, like application_definitions)
  │  publish
  ▼
POST /api/v1/admin/agent-definitions/{id}/publish
  │  ├─ compile: AgentDefinition -> runtime AgentSpec (resolved refs, frozen prompts)
  │  ├─ write component_definitions (kind='agent') + agents subtype row (SHARED UUID, one txn)
  │  ├─ write runtime AgentSpec into them.agent_runtime_specs (JSONB the runner reads)
  │  └─ deploy: reconcile a them-agent-runtime container bound to that spec
  ▼
Running Go A2A agent  ──►  agentregistry discovers card at /.well-known/agent-card.json
                            appears in application-canvas palette (kind=agent)
```

### 3.2 The `AgentDefinition` (design-time) vs `AgentSpec` (runtime)

- **`AgentDefinition`** — exactly what the canvas serializes (nodes/edges + config). Human-editable, revisioned in `them.agent_definitions` (immutable per-revision, `definition JSONB` + `definition_hash`), mirroring the `application_definitions` pattern from migration 029.
- **`AgentSpec`** — the *compiled* form the runtime executes: pipeline flattened to an ordered/branча step list per skill, prompts frozen, `a2a-call` refs resolved to endpoint URLs, secret refs validated against configured credential slots. This is what `them-agent-runtime` loads. Separating the two mirrors the existing `app_compiler` split (canvas JSON → compiled runtime rows).

```go
// go/internal/agentgen/spec.go  (runtime spec — read by the runner)
type AgentSpec struct {
    Slug         string            `json:"slug"`
    Card         a2a.AgentCard     `json:"card"`      // pre-built, served verbatim
    Skills       []SkillSpec       `json:"skills"`
    Secrets      []string          `json:"secrets"`   // env var names the runner must have
    DefaultModel string            `json:"default_model"`
}
type SkillSpec struct {
    ID    string     `json:"id"`
    Steps []StepSpec `json:"steps"` // topologically ordered
}
type StepSpec struct {
    ID       string          `json:"id"`
    Type     string          `json:"type"` // input|llm|http|transform|branch|a2a_call|response
    Config   json.RawMessage `json:"config"`
    Next     []string        `json:"next"`
    Branches []BranchArm     `json:"branches,omitempty"`
}
```

### 3.3 Which generation strategy? (A / B / C)

| Option | Mechanism | Pros for the-M | Cons for the-M |
|---|---|---|---|
| **A. Template codegen** | `text/template` → Go source → `go build` in Docker → new image per agent | Full transparency; users can fork; no runtime interpreter | Requires a Go build toolchain in the deploy path; each agent = a new image + compose service; slow iterate loop (build minutes); **no Kubernetes** means no image registry/rollout story — you'd rebuild+recreate containers by hand or via a builder service. Heavy for Docker-Compose. |
| **B. Interpreted runtime** | One generic `them-agent-runtime` reads `AgentSpec` JSON, serves A2A dynamically | No per-agent build; publish = write JSON + start/point a container; instant iterate; one image to maintain and security-scan; fits compose exactly | Interpreter must be written and hardened; step types are a fixed menu (no arbitrary Go); a bug in the runner affects all agents |
| **C. WASM plugins** | Compile agent logic to WASM, load in the Go runner (wazero) | Sandbox isolation; per-agent logic without per-agent process | Highest complexity; canvas → WASM toolchain to build and maintain; overkill for a fixed step menu; debugging WASM is painful; no team WASM experience implied |

**Recommendation: B (interpreted runtime) as the product; A (codegen) as an export convenience; C rejected.**

Reasoning grounded in the-M's constraints:
- **Docker-Compose, no Kubernetes.** Option A's "build an image per agent" has no clean rollout mechanism here. Option B deploys by writing a DB row and pointing a container at it — the same operational shape as the existing `[test-agents]` profile.
- **One Go gateway, security scanning exists** (`security_scanner` agent, `them.agents.last_scan_result`). One runtime image is scanned once; N generated images multiply the scan surface.
- **Fixed step menu.** The canvas offers a bounded set of node types. That is exactly the case where an interpreter wins over codegen — there is no arbitrary user code to compile, so WASM's isolation buys nothing and codegen's flexibility is unused.
- **Iterate speed.** Canvas users expect edit→test in seconds. `go build` per change is a poor loop; re-reading a JSON spec is instant (or a container restart at worst).

Two runtime topologies for Option B, pick per scale:
1. **Multi-tenant runner (start here):** one `them-agent-runtime` process serves many agents, routing by path (`/agents/{slug}/...` + per-agent well-known card). Simplest; one container. Good until per-agent isolation or noisy-neighbor becomes a concern.
2. **One container per agent (scale-out):** same image, `AGENT_SPEC_ID` env var selects the spec; deploy reconciler starts/stops containers. Better isolation and per-agent `max_concurrency`. This is where a lightweight deploy reconciler (Phase 4) earns its keep.

Start with topology 1; the `AgentSpec` contract makes moving to topology 2 a deployment change, not a code change.

---

## 4. Go A2A Server Implementation (the runtime)

### 4.1 Structs / JSON-RPC compliance

Use the official SDK types (§1.1) — do not re-derive them. The SDK's `NewJSONRPCHandler` already implements the full method set. If on the hand-rolled fallback, reuse the wire structs already in `go/internal/a2a/server.go` and add a task store.

### 4.2 Serving the card

Build an `a2a.AgentCard` from the `AgentSpec` at boot and serve it statically:

```go
func buildAgentCard(spec *AgentSpec, publicURL string) *a2a.AgentCard {
    card := &a2a.AgentCard{
        Name:              spec.Card.Name,
        Description:       spec.Card.Description,
        Version:           spec.Card.Version,
        DefaultInputModes:  []string{"text/plain"},
        DefaultOutputModes: []string{"text/plain"},
        Capabilities: a2a.AgentCapabilities{Streaming: spec.Card.Capabilities.Streaming},
        SupportedInterfaces: []*a2a.AgentInterface{{
            URL:             publicURL,
            ProtocolBinding: a2a.TransportProtocolJSONRPC,
        }},
    }
    for _, s := range spec.Skills {
        card.Skills = append(card.Skills, a2a.AgentSkill{
            ID: s.ID, Name: s.Name, Description: s.Description,
            Tags: s.Tags, InputModes: s.InputModes, OutputModes: s.OutputModes,
        })
    }
    return card
}
// served at a2asrv.WellKnownAgentCardPath = "/.well-known/agent-card.json"
```

**Consistency note:** the existing Go inbound server serves `/.well-known/agent.json` while Python agents and the discover flow use `/.well-known/agent-card.json`. Generated agents **must** use `agent-card.json` (matches Python agents, the a2a-echo healthcheck, and the SDK constant). See §7.

### 4.3 SendMessage → task lifecycle → artifacts

The executor (§1.2) yields, in order: `NewSubmittedTask` → `NewStatusUpdateEvent(Working)` → run pipeline → `NewArtifactEvent(parts…)` → `NewStatusUpdateEvent(Completed)` (or `Failed`). The SDK's JSON-RPC handler serializes these into the `message/send` result (or streams them for `message/stream` when `capabilities.streaming`). `tasks/get`/`tasks/cancel` are handled by the SDK's task store.

### 4.4 An LLM-backed step

The `llm` step calls Anthropic via the official Go SDK (`github.com/anthropics/anthropic-sdk-go`). **This SDK is not yet in `go/go.mod`** — it must be added in Phase 1. `ANTHROPIC_API_KEY` is already wired into `them-go-bridge`'s environment and would be wired into the runtime container the same way. (Verify how the existing Go LLM path — if any — calls Anthropic before adding a second client; reuse it if present.)

```go
func (r *Runner) runLLMStep(ctx context.Context, c LlmStepConfig, vars map[string]any) (string, error) {
    sys := renderTemplate(c.SystemPrompt, vars)
    usr := renderTemplate(c.UserPrompt, vars)
    msg, err := r.anthropic.Messages.New(ctx, anthropic.MessageNewParams{
        Model:     anthropic.Model(orDefault(c.Model, "claude-opus-4-8")),
        MaxTokens: int64(orDefaultInt(c.MaxTokens, 4096)),
        System:    []anthropic.TextBlockParam{{Text: sys}},
        Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(usr))},
        // adaptive thinking + effort for opus/sonnet-5; see claude-api skill
    })
    if err != nil {
        return "", err
    }
    return firstTextBlock(msg.Content), nil
}
```

Large `max_tokens` must stream (`r.anthropic.Messages.NewStreaming`); the runner streams the completion into an artifact when the skill declares `streaming: true`. Default model `claude-opus-4-8`; expose `claude-sonnet-5`/`claude-haiku-4-5` in the node config for cost/latency.

### 4.5 Declaring skills in Go

Skills are **data**, not code, in Option B — they come from `AgentSpec.Skills` and become `a2a.AgentSkill` entries on the card (§4.2). No per-skill Go type. (In the Option A export, each skill becomes a named executor branch.)

---

## 5. Integration with the Existing Platform

### 5.1 Registering in `them.agents` — the subtype constraint

Migration 030 made agents a **subtype**: `agents.id` has FK `fk_agents_base_def → component_definitions(id)`. Research flagged that the current Go `DB.CreateAgent` (`go/internal/admin/dal/agents.go`) does **not** create the matching `component_definitions` row and omits `agent_card`/`skills`/`input_schema`. **Publishing a canvas agent must therefore write both rows in one transaction with a shared UUID:**

```sql
-- inside one tx, id generated once and reused
INSERT INTO them.component_definitions
  (id, kind, namespace, name, version, display_name, description,
   implementation_type, configuration_schema, default_config, capabilities,
   input_schema, output_schema, credential_schema, scope, tenant_id, status, content_hash, enabled)
VALUES ($id, 'agent', $ns, $name, $ver, $display, $desc,
   'canvas_a2a', '{}', '{}', $caps, $inSchema, $outSchema, $creds,
   'tenant', $tenant, 'published', $hash, true);

INSERT INTO them.agents
  (id, tenant_id, slug, display_name, description, transport, endpoint_url,
   input_schema, agent_card, agent_card_url, skills,
   supports_streaming, supports_push, icon, category,
   namespace, version, scope, status, content_hash, enabled)
VALUES ($id, $tenant, $slug, $display, $desc, 'a2a_async', $endpoint,
   $inSchema, $card, $cardURL, $skills, $streaming, $push, $icon, $cat,
   $ns, $ver, 'tenant', 'published', $hash, true);
```

`endpoint_url` = the runtime's internal DNS address (topology 1: `http://them-agent-runtime:9300/agents/{slug}`; topology 2: `http://agent-{slug}:9300`). `transport='a2a_async'` (already the default; already in the transport CHECK). Internal/locked semantics use **tags** (`them.agents.tags`), consistent with migration 034 and the recent internal/locked commits — a canvas agent is *not* tagged internal/locked (it is user-owned and deletable).

This is a deliberate new write path (`agentgen` service), not a patch to the generic `CreateAgent` — canvas agents carry more (card, skills, spec) than the hand-registered agents that path serves.

### 5.2 Component registry tracking

The `component_definitions` row (kind=`agent`, `implementation_type='canvas_a2a'`) *is* the registry entry. `{namespace, name, version}` gives portable identity (`DefinitionRef`). `agentgen` publishing bumps `version` (int) per revision, mirroring `registry`'s versioning. This makes the agent resolvable by the existing `registry.Resolve`/`ResolveByRef` and appear in the application-canvas palette automatically (the palette reads `GET /api/v1/admin/component-definitions` filtered to published+enabled+visible-to-tenant).

### 5.3 New DB tables/columns

Only two new tables; no new `them.agents` columns (all needed columns already exist):

```sql
-- db/0NN_canvas_a2a.sql
CREATE TABLE them.agent_definitions (          -- design-time, immutable revisions (mirrors application_definitions)
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_slug      TEXT NOT NULL,
    tenant_id       UUID NOT NULL REFERENCES them.tenants(id) ON DELETE RESTRICT,
    revision        INTEGER NOT NULL,
    definition      JSONB NOT NULL,            -- the canvas AgentDefinition
    definition_hash TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_slug, revision)
);

CREATE TABLE them.agent_runtime_specs (        -- compiled form the runner loads
    id               UUID PRIMARY KEY,          -- == agents.id / component_definitions.id
    tenant_id        UUID NOT NULL REFERENCES them.tenants(id) ON DELETE RESTRICT,
    definition_id    UUID NOT NULL REFERENCES them.agent_definitions(id),
    spec             JSONB NOT NULL,            -- AgentSpec (§3.2)
    spec_hash        TEXT NOT NULL,
    deployed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Secrets (credential slot values) are **not** stored in the spec — they are `env var names` the runtime container is provisioned with, resolved from the-M's existing secret plumbing (`secrets.local` / `generate-env.sh`), consistent with the "never store real secrets" rule. `them.agents.auth_token_encrypted` (Fernet, `internal/crypto`) covers inbound-auth to the agent if required.

### 5.4 Traefik / Docker-Compose changes

Add one service (topology 1):

```yaml
  them-agent-runtime:
    build: { context: ., dockerfile: Dockerfile.agent-runtime }
    container_name: them-agent-runtime
    profiles: [agents]
    expose: ["9300"]
    environment:
      - DATABASE_HOST=them-postgres
      - DATABASE_NAME=them
      - REDIS_HOST=them-redis
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY:-}
    networks: [them-network]
    depends_on: [them-postgres, them-redis]
    restart: unless-stopped
    healthcheck:
      test: ["CMD","wget","-qO-","http://localhost:9300/healthz"]
```

**No new Traefik routing.** Canvas agents are invoked **outbound** by the orchestrator via `agentregistry` over the internal `them-network` (container DNS), exactly like `a2a-echo`. They are not public entry points. The only externally-reachable surface change is the existing Go admin routes plus new `agent-definitions` CRUD routes on `them-go-bridge` (priority 115–120, same block as `them-go-agents-*`):

```
them-go-agent-defs: PathPrefix(`/api/v1/admin/agent-definitions`)  priority 120
```

### 5.5 Appearing in the application palette

Because publish writes a `component_definitions` (kind=`agent`) row, the agent shows up in the existing application-canvas palette with zero extra work — the palette already lists agent-kind component definitions. Dropping it onto an application canvas wires it as a tool node whose runtime invocation resolves through `agentregistry` to the runtime's endpoint. This is the payoff of reusing the subtype/registry model instead of inventing a parallel one.

---

## 6. Implementation Roadmap

One focused subsystem per session, per `CLAUDE.md`. Each phase is independently shippable and testable; do not begin the next in the same session.

### Phase 1 — Go A2A agent runtime (`them-agent-runtime`)
- New module `go/internal/agentgen/` (spec types) + `go/cmd/agent-runtime/`.
- Implement the interpreter: `input`, `llm` (Anthropic), `transform`, `response` steps (defer `http`, `branch`, `a2a_call` to a later cut).
- Serve A2A via official SDK **if Go 1.25 adopted**, else the hand-rolled server reusing `internal/a2a` structs.
- Load `AgentSpec` from a JSON file/env for now (no DB yet); prove one hand-written spec serves a working agent that `agentregistry` can discover and invoke.
- Dockerfile.agent-runtime + compose service (profile `agents`).
- **Tests (Go):** executor lifecycle (submitted→working→artifact→completed), card served at correct well-known path, `message/send` round-trip, LLM step with a mocked Anthropic client. Update `go/TEST_INDEX.md`.

### Phase 2 — Canvas "Agent Builder" view
- `frontend/src/app/admin/agents/builder/` — new ReactFlow canvas, node types from §2.3, nested skill sub-canvas.
- Save/load `AgentDefinition` via new `POST/GET /api/v1/admin/agent-definitions` (Go, `admin` + `admin/dal` + `admin/service`).
- `db/0NN_canvas_a2a.sql` (agent_definitions table only this phase).
- No compile/deploy yet — just author + persist revisions.
- **Tests:** Go DAL/handler for agent-definitions CRUD; frontend `tsconfig` build clean. Update `go/TEST_INDEX.md` + `scripts/tests/INDEX.md` if a Python-adjacent test is touched (none expected).

### Phase 3 — Generation/compile/deploy pipeline
- `agentgen.Compile(AgentDefinition) → AgentSpec` (flatten pipeline, resolve refs, validate model IDs + secret refs + cycles).
- `POST /api/v1/admin/agent-definitions/{id}/publish`: compile → write `agent_runtime_specs` → write `component_definitions` + `agents` in one tx (shared UUID, §5.1) → signal runtime to load the spec (topology 1: runtime polls/`agent_runtime_specs` or receives a Redis pub/sub `them:agents:` message).
- Runtime loads specs from DB on boot + on notification.
- **Tests:** compile determinism (definition_hash → spec_hash stable), the two-row-one-tx invariant (FK satisfied), publish→discover→invoke end-to-end against `them-agent-runtime`. Update `go/TEST_INDEX.md`.

### Phase 4 — Live management
- Update (new revision + republish), disable/enable (`them.agents.enabled`), delete (cascade: agents row, component_definitions row, runtime spec; refuse if referenced by a published application definition).
- Topology-2 deploy reconciler (optional): one container per agent, `max_concurrency` honored.
- Security scan integration: run the existing `security_scanner` against generated agents on publish; store `last_scan_result`.
- **Tests:** revision bump, referential-integrity guards on delete, scan-on-publish. Update indexes.

---

## 7. Blockers & Risks Specific to the-M

1. **Go 1.25 requirement for the official SDK — confirmed active.** `a2a-go/v2` needs Go 1.25 (range-over-func in `AgentExecutor`); **`go/go.mod` is `go 1.23`**. Adopting the SDK forces a toolchain bump across the repo and CI/Docker images (`Dockerfile.go`), which affects every Go binary, not just the runtime. **Mitigation:** the hand-rolled fallback (§1.3) removes this dependency entirely and keeps the repo on 1.23; the `AgentSpec` contract is SDK-agnostic, so the choice is reversible. **Recommendation: ship Phase 1 on the hand-rolled server (no toolchain bump), adopt the SDK only if/when the repo moves to Go 1.25 for other reasons.**

2. **Well-known path inconsistency.** `internal/a2a` serves `/.well-known/agent.json`; Python agents, the a2a-echo healthcheck, the discover flow, and the SDK constant use `/.well-known/agent-card.json`. Generated agents must use `agent-card.json`. Worth aligning the inbound server too, but that is out of scope here — flag in `docs/LESSONS.md`.

3. **The subtype FK trap.** `CreateAgent` predates `fk_agents_base_def` and does not create the base `component_definitions` row. A naive insert into `them.agents` will violate the FK. Publishing **must** create both rows in one transaction with a shared UUID (§5.1). Verify the exact FK behavior against the running DB before Phase 3 — the research noted the Go path "appears not to have been updated."

4. **Prompt-injection surface.** `llm` step prompts are user-authored templates over caller-supplied input. A hostile skill author or a hostile caller could attempt injection. **Mitigation:** freeze prompts at compile time; treat caller input strictly as template *data* (never as template source); run the `security_scanner` on publish; keep the `ANTHROPIC_API_KEY` scoped to the runtime container only.

5. **Secret handling.** `http`/`a2a_call` steps may need credentials. Never store secrets in `AgentSpec` or `agent_definitions` JSONB (both are readable via admin API and revisioned). Reference secrets by env-var name only; provision the runtime container via `generate-env.sh`. Honor the org rule: never commit or print real secrets.

6. **No Kubernetes → topology-2 deploy story is manual.** One-container-per-agent needs a reconciler that starts/stops compose services or docker containers. That is real work (Phase 4) and a source of drift. **Mitigation:** ship topology 1 (multi-tenant runner) first; it needs zero orchestration.

7. **Interpreter blast radius.** In Option B one runtime bug affects every generated agent. **Mitigation:** per-step timeouts and panic recovery in the interpreter; per-agent `max_concurrency` from `them.agents`; the fixed step menu keeps the interpreter small and testable.

8. **Tenant isolation.** Every new row (`agent_definitions`, `agent_runtime_specs`, and the `component_definitions`/`agents` pair) must carry `tenant_id`, and the runtime must scope spec loading and agent invocation per tenant — consistent with the platform's tenant-aware design rule. Redis keys for any per-agent runtime state use the `them:agents:` prefix with the tenant/slug in the key.

---

## 8. Recommendation Summary

- **Build the interpreted runtime (Option B).** One `them-agent-runtime` Go service reads a compiled `AgentSpec` and serves a spec-compliant A2A agent. Codegen (Option A) ships later as an "Export Go source" button. Reject WASM (Option C).
- **Start on the hand-rolled server** reusing `internal/a2a` wire structs — the repo is on Go 1.23 and the official `a2a-go` SDK needs 1.25. Adopt the SDK only when/if the repo bumps to 1.25 for other reasons. The `AgentSpec` contract makes this reversible.
- **Separate "Agent Builder" canvas**, reusing ReactFlow infrastructure but not the application node set.
- **Publish writes the subtype pair** (`component_definitions` + `agents`, shared UUID, one transaction) so the agent is automatically registry-resolvable and appears in the application palette; invocation reuses `agentregistry` outbound. No new Traefik routing; agents live on the internal network like `a2a-echo`.
- **Two new tables** (`agent_definitions`, `agent_runtime_specs`); no new `them.agents` columns.
- **Four phases**, one per session: runtime → builder canvas → compile/deploy → live management.
