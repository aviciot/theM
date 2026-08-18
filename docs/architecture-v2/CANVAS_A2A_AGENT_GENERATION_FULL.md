# Visual Canvas A2A Agent Generation — Full Design Document
# Last updated: 2026-08-18
# Status: Approved design (revised for multi-tenant runtime model) — ready for implementation
# Authors: architecture research (Opus) + session discussion

---

## 0. What Problem This Solves

Today, every A2A agent in the-M is hand-coded:

```
agents/a2a_echo/   — legacy hand-coded executor + Dockerfile
agents/a2a_slow/   — same
agents/a2a_stream/ — same
```

This contradicts two platform goals:
1. **Python is permanently retired** — every runtime component is Go + TypeScript/React; new agents are Go, never Python
2. **the-M is a platform, not a code editor** — users compose agents visually, not by writing source code

This document specifies a system where a user designs an A2A agent on a visual ReactFlow canvas and the platform runs it as a live Go A2A agent — no code required — and where **one published agent version can be wired into many applications simultaneously, each supplying its own credentials and config, without cloning the agent.**

> **This revision (2026-08-18)** corrects a multi-tenancy flaw in the earlier draft: the compiled `AgentSpec` used to carry `Secrets []string` (env var names), implying secrets are provisioned globally per-agent into the runtime container environment. That model cannot support the same agent being used by two applications with different credentials. The corrected model separates the **reusable agent definition** (no secrets, slot declarations only) from the **per-application binding** (encrypted credential values), resolved at runtime from a signed invocation context. See §5.6 and §5.7.

---

## 1. What the User Actually Does

### Agent author flow (builds the reusable component)

1. Open **Admin → Agents → New Agent → Build Visually**
2. A canvas opens (separate from the application canvas — see §4)
3. User drags blocks from the palette onto the canvas and connects them with arrows
4. Fills in config in the properties panel (model, prompt, URL, condition, etc.)
5. Declares **credential slots** the agent needs (e.g. `salesforce_api`, `slack_token`) — slot **names** only, never values
6. Clicks **Validate** — platform checks for errors (cycles, missing required fields, unresolved credential-slot references)
7. Clicks **Publish** — platform compiles the canvas, writes DB rows, and makes the agent available to the runtime pool
8. The agent immediately appears in the **application canvas palette** — it can be wired into any application

### Application admin flow (binds credentials for one application)

9. On the **application canvas**, drop the published agent as an Agent node
10. Open the node's **Configure** panel — it lists the agent's declared credential slots and config-override knobs
11. Fill in this application's own secret for each slot (e.g. Application A's Salesforce org A key). Values are Fernet-encrypted and stored in `them.app_agent_bindings` — never in the agent definition, never in the AgentSpec, never in the application definition JSONB
12. Optionally set config overrides (model override, max_tokens cap, timeouts) and policies (rate limit, allowed skills)
13. Publish the application — at runtime, invocations of this agent carry the application's identity, and the runtime resolves the correct credentials for that application

**Key invariant:** the SAME published agent version is reused by many applications simultaneously. Each application provides different credentials/config through its own binding row. No agent cloning.

### What the author draws

An agent is a **flowchart**. Each block is a step. Arrows are the data/control flow between steps.

The top-level canvas has two kinds of blocks:
- **Agent Root** — card metadata (name, description, icon, capabilities) + **credential-slot declarations**
- **Skill blocks** — each skill is one capability of the agent (e.g. "Summarize text", "Search database", "Generate report")

Double-clicking a Skill block opens its **pipeline sub-canvas** — the actual step-by-step logic for that skill.

---

## 2. All Canvas Block Types

### Structure tier (top-level canvas)

| Block | What it does | Config fields |
|---|---|---|
| **Agent Root** | The agent's identity card — served at `/.well-known/agent-card.json` — plus the agent's **credential-slot schema** | Display name, description, version, icon, category, streaming on/off, push notifications on/off, **credential slots** (list of `{slot_name, description, required}`) |
| **Skill** | One capability the agent advertises. The LLM uses `description` to decide when to call this skill | Skill ID, name, description, tags, input MIME types (`text/plain` / `application/json`), output MIME types, input JSON Schema, output JSON Schema |

**Credential slots are declared here, at the Agent Root, as names only.** They are the contract an application must satisfy when it binds the agent. Example: a `salesforce-agent` declares slot `salesforce_api` (required) and `slack_token` (optional). The slot schema is written into `component_definitions.credential_schema` at publish time and referenced by name in step configs. No slot ever carries a value in the definition.

### Pipeline tier (inside a Skill sub-canvas)

Every skill has a pipeline: a small directed graph of steps, from **Input** to **Response**.

#### Basic steps

| Block | What it does | Config | Difficulty to implement |
|---|---|---|---|
| **Input** | Entry point. Reads the incoming message parts and binds them to named variables (`user_text`, `query`, `payload`, etc.) | Part mapping: part type → variable name | Simple |
| **LLM Step** | Calls Claude. You write a system prompt and a user prompt, both using `{{.var_name}}` template syntax over the pipeline variables. Output stored in a named variable. Optionally uses the application's own LLM API key via a **provider key slot** | Model (`claude-opus-4-8` / `claude-sonnet-5` / `claude-haiku-4-5`), system prompt template, user prompt template, max tokens, effort level, output variable name, **provider key slot** (optional) | Simple — reuses existing `internal/llm` Provider |
| **HTTP Tool** | Calls any external REST API. URL and body are templates over variables. Auth header is injected at runtime from a **credential slot** resolved per-invocation — the value is NOT in the spec. Response JSON fields extracted into named variables | Method, URL template, static headers (non-secret only), **credential slot** (which resolved credential to inject and how), body template, JSONPath extractions | Simple |
| **Transform** | Pure data reshaping between steps — rename a variable, combine two strings, extract a JSON field, format a number. No I/O | Expression per output variable (template or jq-style) | Simple |
| **Response** | Terminal block. Takes a variable and emits it as the skill's output artifact. Every pipeline must end here | Source variable, output media type (`text/plain` / `application/json` / `text/html` / `text/markdown`) | Simple |

#### Advanced steps

| Block | What it does | Config | Difficulty |
|---|---|---|---|
| **Branch (conditional)** | Routes execution to one of N downstream paths based on a condition. Supports chaining multiple conditions (if / else-if / else) | Condition expression per branch arm (e.g. `{{eq .status "ok"}}`), default arm | Simple |
| **Loop** | Repeats a set of steps up to N times, or until a boolean condition becomes true. Each iteration can update variables. Emit a streaming artifact chunk each iteration if enabled | Steps to repeat (referenced by ID), max iterations, exit condition, accumulator variable | Medium — requires cycle detection at compile time |
| **Parallel** | Runs N branches of steps concurrently. All branches must complete before the pipeline continues. Results merged into a map variable | Branch definitions (each a sub-list of step IDs), merge strategy (merge all / first wins / concat) | Medium — goroutines + race-safe variable merge |
| **Call Another Agent** | Invokes any other A2A agent registered in the platform — including other canvas-generated agents. Uses the existing `agentregistry` outbound path and **propagates the invocation context** (tenant/application) downstream. Enables **agent composition** | Agent reference `{namespace, name, version}`, input variable, output variable, timeout | Medium — bridge event streams + context propagation |
| **Human-in-the-Loop** | Pauses the agent, emits `input-required` status back to the caller with a prompt message, waits for a follow-up `message/send` with the same `taskId`, then resumes. Built-in A2A protocol feature. Paused state persists in **Redis** so any replica can resume | Prompt message to display, variable to bind the human's reply into | Medium — requires **Redis** task store to hold paused state across HTTP requests and replicas |
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
- **Literal secret values in any field** — credentials are always slot references resolved at runtime

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

**In a multi-replica runtime, the resume request may land on a different replica than the one that paused the task.** Task state (status, accumulated artifacts, paused pipeline state) therefore lives in Redis, not in any replica's memory — see §5.8.

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

Yes — `github.com/a2aproject/a2a-go/v2` (same org as the reference `a2aproject/a2a-python`).

It provides:
- `a2a` package — all protocol types (`AgentCard`, `AgentSkill`, `Task`, `TaskStatusUpdateEvent`, `TaskArtifactUpdateEvent`, `Message`, `Part`, etc.)
- `a2asrv` package — HTTP server, JSON-RPC dispatch, task store, well-known card handler
- `a2aclient` package — outbound client (for the `Call Another Agent` step)
- Full `message/send`, `message/stream`, `tasks/get`, `tasks/cancel`, `tasks/list` implementation

> Note: the SDK's default task store is in-memory. In a multi-replica deployment we replace it with a **Redis-backed task store** (§5.8). The `a2asrv` server accepts a custom task store implementation, so this is a store-implementation swap, not a fork.

### The executor interface

```go
type AgentExecutor interface {
    Execute(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
    Cancel(ctx context.Context, execCtx *ExecutorContext) iter.Seq2[a2a.Event, error]
}
```

`Execute` returns an iterator that yields events. The server streams them to the caller (SSE for `message/stream`, collects into a result for `message/send`). Events:

```go
a2a.NewSubmittedTask(ec, ec.Message)                  // task accepted
a2a.NewStatusUpdateEvent(ec, TaskStateWorking, nil)    // working
a2a.NewArtifactEvent(ec, a2a.NewTextPart(text))        // output (can repeat)
a2a.NewStatusUpdateEvent(ec, TaskStateCompleted, nil)  // done
// or:
a2a.NewStatusUpdateEvent(ec, TaskStateInputRequired, msg) // paused — human needed
a2a.NewStatusUpdateEvent(ec, TaskStateFailed, errMsg)
```

### The Go version situation

- The SDK requires **Go 1.25** (uses `iter.Seq2` range-over-func iterators)
- `go/go.mod` is currently `go 1.23`
- Go 1.25 is already installed on this server (`go version go1.25.x linux/amd64`) and `golang:1.25-alpine` is already pulled locally

**Upgrading is 4 lines across 4 files:**

| File | Change |
|---|---|
| `go/go.mod` | `go 1.23` → `go 1.25` |
| `Dockerfile.go` | `FROM golang:1.23-alpine` → `FROM golang:1.25-alpine` |
| `Dockerfile.go-worker` | same |
| `Dockerfile.auth-go` | same |

This is the first step of Phase 1 — minutes of work, not a real blocker.

---

## 5. Architecture: How It All Works

### 5.1 Two existing mechanisms (do not confuse them)

```
go/internal/a2a/           INBOUND  — exposes the orchestrator AS an A2A agent
                                      POST /a2a/{app_slug} + /.well-known/agent-card.json
                                      External callers call into the platform

go/internal/agentregistry/ OUTBOUND — invokes registered A2A agents
                                      Calls agents in them.agents (transport='a2a_async')
                                      The orchestrator calls out to agents
```

A canvas-generated agent is **outbound** — a new external service registered in `them.agents`, invoked by `agentregistry`. It is a peer of `a2a-echo`, not a reuse of the inbound server.

### 5.2 The three-form split

The earlier design had two forms (Definition, Spec). The revised model has **three**, separating the reusable agent from the per-application binding:

| Form | Stored where | Purpose | Contains secrets? |
|---|---|---|---|
| `AgentDefinition` | `them.agent_definitions` (JSONB, immutable revisions) | What the canvas serializes. Human-readable, editable, revisioned. Declares credential **slots** | **No** — slot names only |
| `AgentSpec` | `them.agent_runtime_specs` (JSONB) | Compiled, immutable, **reusable** form the runtime loads. Resolved refs, frozen prompts, topologically ordered steps, credential **slot names** in step configs | **No** — slot names only |
| `AppAgentBinding` | `them.app_agent_bindings` (row per application×agent) | Per-application instance of the agent: encrypted credential values keyed by slot name, config overrides, policies | **Yes** — Fernet ciphertext only |

This mirrors the existing application-canvas split (Definition JSON → compiled `app_orchestrators` + `entry_points`) but adds the binding layer so one compiled `AgentSpec` serves many applications.

**Invariant:** `AgentSpec` is reusable and immutable. It never contains a secret value or an env-var name. The only credential information in it is a **slot name** (`credential_slot: "salesforce_api"`). The value is resolved at invocation time from the calling application's binding.

### 5.3 The generation strategy: Interpreted Runtime (Option B)

One generic Go binary (`them-agent-runtime`) reads an `AgentSpec` JSON and serves any A2A agent. No `go build` per agent.

**Why not per-agent codegen (Option A)?**
- Docker Compose, no Kubernetes — no image registry or rollout story for per-agent builds
- `go build` per agent = minutes of iteration; re-reading JSON = instant
- One security-scanned image vs N images multiplying the scan surface
- Fixed step menu = exactly the case where an interpreter wins over codegen
- **Multi-tenant reuse is trivial with an interpreter:** the same spec + a different binding = a different tenant's instance, with zero rebuild

**Codegen (Option A) becomes an "Export Go source" button** — for developers who want to fork and own the code. Exported source is behavior-equivalent because both consume the same `AgentSpec`. Exported source contains slot names only; the developer wires their own secret source. Exports never contain secret values.

### 5.4 Runtime topology — horizontally scalable from day one

Horizontal scalability is a **first-class Phase 1 requirement**, not a Phase 4 afterthought.

`them-agent-runtime` runs as **N identical stateless replicas** behind Docker's internal DNS (round-robin) or Traefik. Any replica can serve any request for any agent and any application.

```
                          ┌──────────────────────────────────────────┐
                          │  agentregistry (in them-go-bridge / worker)│
                          │  outbound A2A call + invocation context     │
                          └───────────────────┬──────────────────────┘
                                               │ POST http://them-agent-runtime:9300/agents/{slug}
                                               │ + signed invocation context (JWT or X-Them-* headers)
                                               ▼
                              Docker DNS / Traefik round-robin
                    ┌──────────────┬───────────────┬──────────────┐
                    ▼              ▼               ▼              ▼
             ┌───────────┐  ┌───────────┐   ┌───────────┐  ┌───────────┐
             │ runtime#1 │  │ runtime#2 │   │ runtime#3 │  │ runtime#N │
             │ stateless │  │ stateless │   │ stateless │  │ stateless │
             └─────┬─────┘  └─────┬─────┘   └─────┬─────┘  └─────┬─────┘
                   │              │               │              │
       spec cache  │  spec cache  │   spec cache  │  spec cache  │  (sync.Map, TTL 60s,
       (per repl.) │              │               │              │   invalidated by pub/sub)
                   └──────────────┴───────┬───────┴──────────────┘
                                          │
                    ┌─────────────────────┴──────────────────────┐
                    ▼                                             ▼
          ┌──────────────────┐                        ┌──────────────────────┐
          │   PostgreSQL      │                        │        Redis          │
          │  agent_runtime_   │                        │  them:agent:task:{id} │  ← task store (24h TTL)
          │  specs            │                        │  them:agents:registry:│  ← spec-invalidation
          │  app_agent_       │                        │  {tenant_id} (pub/sub)│    pub/sub
          │  bindings         │                        └──────────────────────┘
          └──────────────────┘
```

Requirements enforced by this topology:

- **Stateless execution.** No sticky sessions. No per-replica local state. No shared filesystem. Everything durable is in PostgreSQL or Redis.
- **Spec cache.** Each replica keeps an in-process `sync.Map` of `agent_id → AgentSpec`, TTL 60s. On Redis pub/sub `them:agents:registry:{tenant_id}` (already used by `agentregistry`), all replicas invalidate the affected tenant's cached specs.
- **Binding load.** Per-invocation from `them.app_agent_bindings` (short-TTL cache per `binding_id`/`application_id+agent_id` allowed). Decrypted credential values are held only in the request-scoped `InvocationContext.Credentials` map.
- **Task store in Redis.** Required for `tasks/get` and for `human-wait` resume across replicas. See §5.8.
- **Routing.** `them.agents.endpoint_url` points at the pool DNS name: `http://them-agent-runtime:9300/agents/{slug}`. Docker/Traefik round-robins. The **application context travels in the invocation JWT/headers, not in the URL** — the URL has no per-application variation.

### 5.5 The invocation context — identity on every call

Every outbound request from `agentregistry` to `them-agent-runtime` carries the caller's identity so the runtime can resolve the right binding and credentials. Preferred: a **signed JWT** scoped to the invocation (prevents header spoofing). Fallback/debug: `X-Them-*` headers.

```
X-Them-Tenant-Id:      {tenant_id      UUID}
X-Them-Application-Id: {application_id  UUID}
X-Them-Agent-Id:       {agent_id       UUID}   ← which agent/version
X-Them-Binding-Id:     {binding_id     UUID}   ← which app-level binding (optional; runtime can
                                                  look up by application_id + agent_id instead)
```

Signed-JWT form (preferred): claims `tenant_id`, `application_id`, `agent_id`, `binding_id`, `iat`, `exp` (short, e.g. 60s), signed with the internal invocation signing key (`THE_M_INVOCATION_JWT_KEY`). The runtime verifies the signature before trusting any identity claim.

The runtime uses these to:
1. Load the correct `AgentSpec` from DB/cache (by `agent_id`)
2. Load the correct `app_agent_bindings` row (by `binding_id`, else `application_id + agent_id`)
3. Resolve credential slots: `binding.credential_bindings[slot_name]` → `crypto.DecryptStored` → actual value → `InvocationContext.Credentials[slot_name]`
4. Apply `config_overrides` (e.g. model override, max_tokens cap)
5. Apply `policies` (rate limit, allowed skills)
6. Execute the pipeline with the resolved context

**The runtime NEVER reads env vars for per-agent credentials.** Env vars are for runtime **infrastructure only**: DB password, Redis URL, JWT signing key, the internal crypto key used for `DecryptStored`.

### 5.6 The Application Binding Layer (the Salesforce example, concretely)

This section is the heart of the revision. It makes the reuse invariant tangible.

**Setup — one reusable agent, declared once:**

The agent author builds `salesforce-agent` on the canvas. Its Agent Root declares one credential slot:

```
credential slot: salesforce_api   (required)   "Salesforce REST API bearer token"
```

Inside its "Query Accounts" skill, the HTTP Tool step references the slot by name:

```
HTTP Tool "GET /services/data/v60.0/query"
  credential_slot: "salesforce_api"      ← name only, no value
  inject_as: header "Authorization" = "Bearer {credential}"
```

At publish, `component_definitions.credential_schema` records:

```json
{ "slots": [ { "name": "salesforce_api", "required": true, "description": "Salesforce REST API bearer token" } ] }
```

The compiled `AgentSpec` records `credential_slot: "salesforce_api"` on the HTTP step. **No token anywhere.**

**Two applications, two different orgs, one agent:**

```
Application A  ──binds──►  app_agent_bindings row A
                          credential_bindings = { "salesforce_api": <Fernet(org-A-token)> }

Application B  ──binds──►  app_agent_bindings row B
                          credential_bindings = { "salesforce_api": <Fernet(org-B-token)> }
```

Both bindings target the **same** `agent_id` (and optionally the same pinned `definition_id`). Nothing about `salesforce-agent` is copied.

**At runtime:**

```
Application A's orchestrator calls salesforce-agent
   agentregistry → POST http://them-agent-runtime:9300/agents/salesforce-agent
                   JWT{ tenant, application_id=A, agent_id=SF, binding_id=A }
   runtime:
     load AgentSpec(SF)                         (cached)
     load binding(app=A, agent=SF) → row A
     DecryptStored(row A.credential_bindings["salesforce_api"]) → org-A-token   (in memory only)
     HTTP step injects: Authorization: Bearer <org-A-token>
     → hits Salesforce org A

Application B's orchestrator calls salesforce-agent   (possibly the same replica, same second)
   JWT{ application_id=B, ... binding_id=B }
   runtime:
     load AgentSpec(SF)                         (same cached spec object)
     load binding(app=B, agent=SF) → row B
     DecryptStored(row B...) → org-B-token
     HTTP step injects: Authorization: Bearer <org-B-token>
     → hits Salesforce org B
```

Same spec, same replica, same instant — different credentials, correctly isolated. The decrypted token exists only in each request's `InvocationContext.Credentials` map and is discarded when the request ends. It is never logged, never persisted, never placed in an artifact or Temporal history.

**The Configure panel:** the existing application-canvas Agent node (`AgentNodeData`) gains a **Configure** panel. It reads the agent's `credential_schema` from `component_definitions`, renders one masked input per slot, and on save writes the Fernet-encrypted values to `app_agent_bindings.credential_bindings`. This is analogous to how `entry_points` carry per-EP config overrides today — per-application configuration stored beside the reference, not baked into the referenced definition.

### 5.7 Distinguishing the two credential concepts

Do **not** conflate these:

| Field | Direction | Purpose |
|---|---|---|
| `them.agents.auth_token_encrypted` | **Inbound** | Auth the runtime requires from *callers* (if the agent demands the platform authenticate to it). This is auth **TO** the agent. |
| `them.app_agent_bindings.credential_bindings` | **Outbound** | Credentials the agent uses **during execution** to call external systems (Salesforce, Slack, an LLM provider). This is auth the agent uses **FROM** its steps. |

Different tables, different directions, different lifecycles.

### 5.8 Redis task store (Phase 1, not optional)

The A2A protocol requires `tasks/get` to return a task's current state, and `human-wait` requires paused pipeline state to survive across separate HTTP requests. In-memory task state does not survive a request landing on a different replica.

Task state lives in **Redis** (consistent with the rest of the-M's session/run state):

- **Key:** `them:agent:task:{task_id}` — TTL 24h (refreshed on write)
- **Value (JSON):** `{ status, artifacts[], paused_pipeline_state, tenant_id, application_id, agent_id, binding_id, created_at, updated_at }`
  - `paused_pipeline_state` captures the interpreter's variables + the next step ID for `human-wait` resume
  - the decrypted credentials are **not** stored here — on resume, the runtime re-decrypts from the binding using the invocation context
- **All replicas read/write the same Redis.** Any replica can resume a paused task.

The SDK's task-store interface is implemented by a `redistaskstore` type in `go/internal/agentgen/`. No shared filesystem; no in-memory task map.

### 5.9 End-to-end flow

```
AUTHOR: draws agent canvas
        │  save draft
        ▼
POST /api/v1/admin/agent-definitions            → them.agent_definitions (JSONB, revision N)
        │  validate
        ▼
POST /api/v1/admin/agent-definitions/{id}/validate
        │  ├─ no cycles in step graph
        │  ├─ all model IDs valid
        │  ├─ every step credential_slot is declared in Agent Root credential slots
        │  └─ a2a-call refs resolve to registered agents
        │  publish
        ▼
POST /api/v1/admin/agent-definitions/{id}/publish
        │  ├─ compile: AgentDefinition → AgentSpec  (slot names only; NO secrets)
        │  ├─ write them.agent_runtime_specs (JSONB AgentSpec)
        │  ├─ write them.component_definitions (kind='agent', credential_schema=slots) + them.agents
        │  │       ← ONE TRANSACTION, SHARED UUID
        │  └─ signal reload: Redis pub/sub them:agents:registry:{tenant_id}
        ▼
them-agent-runtime replicas invalidate cached spec for tenant; next call reloads from DB
        │
        ▼
Agent appears in application canvas palette (GET /component-definitions, kind='agent', enabled=true)

────────────────────────────────────────────────────────────────────────────

APP ADMIN: drops agent on application canvas, opens Configure panel
        │  fills credential slots + overrides
        ▼
PUT /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}
        │  ├─ Fernet-encrypt each slot value (crypto.EncryptStored)
        │  └─ upsert them.app_agent_bindings (application_id, agent_id, credential_bindings, ...)
        ▼
publish application → orchestrator wired to call the agent

────────────────────────────────────────────────────────────────────────────

RUNTIME: orchestrator invokes agent via agentregistry
        │  attach signed invocation context (tenant, application, agent, binding)
        ▼
POST http://them-agent-runtime:9300/agents/{slug}  (round-robined to any replica)
        │  ├─ verify JWT → InvocationContext
        │  ├─ load AgentSpec (cache/DB)
        │  ├─ load app_agent_bindings row
        │  ├─ decrypt slots → InvocationContext.Credentials (in memory)
        │  ├─ apply config_overrides + policies
        │  └─ execute pipeline (task state in Redis)
        ▼
artifact events streamed back to caller; task persisted at them:agent:task:{task_id}
```

---

## 6. Go Code: Runtime Spec, Binding, and Invocation Types

### 6.1 AgentSpec (reusable, immutable, NO secrets)

```go
// go/internal/agentgen/spec.go

// AgentSpec is the compiled, reusable form the runtime loads. Frozen at publish.
// It contains NO secret values and NO env-var names — only credential SLOT NAMES.
type AgentSpec struct {
    Slug         string      `json:"slug"`
    TenantID     string      `json:"tenant_id"`
    Card         CardSpec    `json:"card"`
    Skills       []SkillSpec `json:"skills"`
    // CredentialSlots is the contract every binding must satisfy. Names only.
    CredentialSlots []CredentialSlotSpec `json:"credential_slots"`
    DefaultModel    string               `json:"default_model"`
    // NOTE: the old `Secrets []string` field is REMOVED. Secrets are never in the spec.
}

type CredentialSlotSpec struct {
    Name        string `json:"name"`        // e.g. "salesforce_api"
    Description string `json:"description"`
    Required    bool   `json:"required"`
}

type CardSpec struct {
    Name         string           `json:"name"`
    Description  string           `json:"description"`
    Version      string           `json:"version"`
    Icon         string           `json:"icon,omitempty"`
    Category     string           `json:"category,omitempty"`
    Capabilities CapabilitiesSpec `json:"capabilities"`
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
    Steps       []StepSpec `json:"steps"` // topologically ordered by compiler
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
    StepInput     StepType = "input"
    StepLLM       StepType = "llm"
    StepHTTP      StepType = "http"
    StepTransform StepType = "transform"
    StepResponse  StepType = "response"
    StepBranch    StepType = "branch"
    StepLoop      StepType = "loop"
    StepParallel  StepType = "parallel"
    StepA2ACall   StepType = "a2a_call"
    StepHumanWait StepType = "human_wait"
    StepStreamOut StepType = "stream_out"
)

type BranchArm struct {
    Condition string   `json:"condition"` // Go template expression → bool
    Next      []string `json:"next"`
}
```

### 6.2 Step config shapes (credential handling is slot-only)

```go
type LLMStepConfig struct {
    Provider     string `json:"provider"`      // "anthropic"
    Model        string `json:"model"`         // "claude-opus-4-8" | "claude-sonnet-5" | "claude-haiku-4-5"
    SystemPrompt string `json:"system_prompt"` // Go text/template over vars
    UserPrompt   string `json:"user_prompt"`
    MaxTokens    int    `json:"max_tokens"`
    Effort       string `json:"effort,omitempty"` // "low"|"medium"|"high"|"xhigh"|"max"
    OutputVar    string `json:"output_var"`
    Stream       bool   `json:"stream"`
    // ProviderKeySlot: if set, the LLM API key is resolved from the application binding's
    // credential slot of this name (BYO-key). If empty, the runtime falls back to the
    // platform key (see §13). The key value is NEVER in the spec.
    ProviderKeySlot string `json:"provider_key_slot,omitempty"`
}

type HTTPStepConfig struct {
    Method      string            `json:"method"`
    URLTemplate string            `json:"url_template"`
    // Headers holds NON-SECRET static headers only (Accept, Content-Type, etc.).
    // It MUST NOT contain {{secret "NAME"}} templates or any credential value.
    Headers      map[string]string `json:"headers,omitempty"`
    BodyTemplate string            `json:"body_template,omitempty"`
    Extractions  []JSONPathExtract `json:"extractions"`
    // CredentialSlot names the slot whose decrypted value is injected at runtime.
    // This is the ONLY way credentials enter an HTTP step. The value is resolved
    // per-invocation from app_agent_bindings — never embedded here.
    CredentialSlot string             `json:"credential_slot,omitempty"`
    CredentialInject CredentialInject `json:"credential_inject,omitempty"`
    TimeoutSeconds   int              `json:"timeout_seconds"`
}

// CredentialInject describes HOW the resolved slot value is applied to the request.
type CredentialInject struct {
    // Mode: "header" (default) | "query" | "basic"
    Mode string `json:"mode"`
    // For header mode: HeaderName + ValueTemplate, e.g. HeaderName="Authorization",
    // ValueTemplate="Bearer {credential}". {credential} is substituted at runtime.
    HeaderName    string `json:"header_name,omitempty"`
    ValueTemplate string `json:"value_template,omitempty"`
    // For query mode: the query parameter name.
    QueryParam string `json:"query_param,omitempty"`
}

type JSONPathExtract struct {
    Var      string `json:"var"`
    JSONPath string `json:"json_path"`
}

type A2ACallStepConfig struct {
    Ref            DefinitionRef `json:"ref"` // {namespace, name, version}
    InputVar       string        `json:"input_var"`
    OutputVar      string        `json:"output_var"`
    TimeoutSeconds int           `json:"timeout_seconds"`
    // Downstream calls propagate the current InvocationContext (tenant/application),
    // so the callee resolves its own binding for the same application.
}

type HumanWaitConfig struct {
    Prompt   string `json:"prompt"`    // message shown to user
    ReplyVar string `json:"reply_var"` // variable to bind the human's reply
}

type LoopConfig struct {
    BodySteps     []string `json:"body_steps"`          // step IDs to repeat
    Condition     string   `json:"condition"`           // exit when true (template → bool)
    MaxIterations int      `json:"max_iterations"`
    AccumVar      string   `json:"accum_var,omitempty"` // collect outputs across iterations
}

type ParallelConfig struct {
    Branches [][]string `json:"branches"`  // each branch is a list of step IDs
    MergeVar string     `json:"merge_var"` // receives map[branch_index]result
}
```

### 6.3 Invocation context (per-request; holds decrypted values in memory only)

```go
// go/internal/agentgen/context.go

// InvocationContext is built per request from the signed invocation JWT/headers.
// Credentials holds DECRYPTED values and lives only for the duration of one request.
// It is never logged, never serialized, never written to Redis or Temporal history.
type InvocationContext struct {
    TenantID        string
    ApplicationID   string
    AgentID         string
    BindingID       string
    Spec            *AgentSpec         // loaded from DB/cache (by AgentID)
    Credentials     map[string]string  // slot_name → decrypted value (in-memory only)
    ConfigOverrides map[string]any     // scalar overrides (model, max_tokens, timeouts)
    Policies        InvocationPolicies
}

type InvocationPolicies struct {
    MaxConcurrentTasks int
    AllowedSkillIDs    []string // nil = all skills allowed
    RateLimitPerMinute int
}

// String is deliberately redacted to prevent accidental credential logging.
func (ic InvocationContext) String() string {
    return fmt.Sprintf("InvocationContext{tenant=%s app=%s agent=%s binding=%s slots=%d}",
        ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID, len(ic.Credentials))
}
```

### 6.4 Binding model (Go mirror of the DB row)

```go
// go/internal/agentgen/binding.go

// AppAgentBinding is the per-application instance of a reusable agent.
// credential_bindings on the wire/DB are Fernet CIPHERTEXT keyed by slot name.
type AppAgentBinding struct {
    ID            string
    ApplicationID string
    AgentID       string
    DefinitionID  *string // nil = floating (current active version); non-nil = pinned revision

    // EncryptedCredentials: slot_name → Fernet ciphertext. NEVER plaintext.
    EncryptedCredentials map[string]string
    ConfigOverrides      map[string]any
    Policies             InvocationPolicies

    CreatedAt time.Time
    UpdatedAt time.Time
}

// ResolveCredentials decrypts each slot into a request-scoped map.
// The returned map must never be logged or persisted.
func (b AppAgentBinding) ResolveCredentials(dec func(string) (string, error)) (map[string]string, error) {
    out := make(map[string]string, len(b.EncryptedCredentials))
    for slot, ct := range b.EncryptedCredentials {
        pt, err := dec(ct) // crypto.DecryptStored
        if err != nil {
            return nil, fmt.Errorf("decrypt slot %q: %w", slot, err)
        }
        out[slot] = pt
    }
    return out, nil
}
```

### 6.5 Runtime request flow (pseudocode)

```go
func (rt *Runtime) handle(w http.ResponseWriter, r *http.Request) {
    ic, err := rt.parseInvocation(r) // verify JWT / headers → tenant, app, agent, binding
    if err != nil { http.Error(w, "unauthorized", 401); return }

    spec, err := rt.specCache.Load(r.Context(), ic.AgentID)      // DB/cache, TTL 60s
    binding, err := rt.bindings.Load(r.Context(), ic.ApplicationID, ic.AgentID, ic.BindingID)

    creds, err := binding.ResolveCredentials(rt.crypto.DecryptStored) // in-memory only
    ic.Spec, ic.Credentials = spec, creds
    ic.ConfigOverrides, ic.Policies = binding.ConfigOverrides, binding.Policies

    if err := rt.policies.CheckRate(ic); err != nil { http.Error(w, "rate limited", 429); return }

    rt.interpreter.Execute(r.Context(), ic, w) // task state in Redis; artifacts streamed out
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

interface CredentialSlot {
  name: string;          // e.g. "salesforce_api" — NAME ONLY, never a value
  description: string;
  required: boolean;
}

interface AgentRootData {
  displayName: string;
  description: string;
  version: string;
  icon?: string;
  category?: string;
  capabilities: { streaming: boolean; pushNotifications: boolean };
  credentialSlots: CredentialSlot[];   // the binding contract; declared here
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
  providerKeySlot?: string;    // optional BYO-key slot name (resolved from the binding)
}

interface HttpStepData {
  method: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';
  urlTemplate: string;
  headers: Record<string, string>;     // NON-SECRET static headers only
  bodyTemplate?: string;
  extractions: { var: string; jsonPath: string }[];
  credentialSlot?: string;             // slot NAME; value resolved at runtime
  credentialInject?: {                 // how the resolved value is applied
    mode: 'header' | 'query' | 'basic';
    headerName?: string;
    valueTemplate?: string;            // e.g. "Bearer {credential}"
    queryParam?: string;
  };
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

### Application-canvas Configure panel (binding UI)

```typescript
// frontend/src/app/admin/applications/  — Agent node "Configure" panel

interface AgentBindingForm {
  agentId: string;
  definitionId?: string;                          // pin a revision, or omit for floating
  credentialValues: Record<string, string>;       // slot_name → plaintext entered by admin
                                                   // (sent over TLS, encrypted server-side)
  configOverrides: {                              // scalars only — never secrets
    model?: string;
    maxTokens?: number;
    timeoutSeconds?: number;
  };
  policies: {
    maxConcurrentTasks?: number;
    allowedSkillIds?: string[];
    rateLimitPerMinute?: number;
  };
}
```

The panel fetches the agent's `credential_schema` (from `component_definitions`) to know which slots to render, one masked field each. On save it PUTs to the binding route (§10); the server Fernet-encrypts each value before storing. The form never receives previously stored plaintext back — only a "set / not set" indicator per slot.

---

## 8. Database Schema

Three tables. No new columns on `them.agents` — all needed columns already exist.

```sql
-- db/0NN_canvas_a2a.sql

-- Design-time: immutable revisions (mirrors application_definitions pattern)
CREATE TABLE them.agent_definitions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    agent_slug      TEXT        NOT NULL,
    revision        INTEGER     NOT NULL,
    definition      JSONB       NOT NULL,        -- AgentDefinition canvas JSON (slot NAMES only)
    definition_hash TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'draft'
                                CHECK (status IN ('draft', 'published')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_slug, revision)
);
CREATE INDEX agent_definitions_tenant_slug ON them.agent_definitions (tenant_id, agent_slug);

-- Runtime: compiled, reusable form the runner loads (NO secrets — slot names only)
CREATE TABLE them.agent_runtime_specs (
    id               UUID        PRIMARY KEY,   -- == agents.id == component_definitions.id
    tenant_id        UUID        NOT NULL,
    definition_id    UUID        NOT NULL REFERENCES them.agent_definitions(id),
    spec             JSONB       NOT NULL,       -- AgentSpec (compiled, reusable)
    spec_hash        TEXT        NOT NULL,
    deployed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 8.1 NEW: per-application credential bindings

```sql
-- Application-level binding of a reusable agent. One row per (application, agent).
-- This is where OUTBOUND credentials, per-app config, and policies live.
-- The SAME agent_id can appear in many rows (one per application) without cloning.
CREATE TABLE them.app_agent_bindings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id      UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    agent_id            UUID NOT NULL REFERENCES them.agents(id) ON DELETE RESTRICT,
    -- The specific published definition version this binding targets:
    --   NULL     = always use the current active version (floating)
    --   non-NULL = pinned to an exact revision
    definition_id       UUID REFERENCES them.agent_definitions(id),
    -- Per-slot credential bindings: slot_name → Fernet ciphertext.
    -- Same encryption as them.agents.auth_token_encrypted (crypto.EncryptStored /
    -- crypto.DecryptStored). NEVER store plaintext. NEVER copy into AgentSpec JSONB.
    -- Shape: {"salesforce_api": "<fernet-ciphertext>", "slack_token": "<fernet-ciphertext>"}
    credential_bindings JSONB NOT NULL DEFAULT '{}',
    -- Per-application config overrides for this agent instance.
    -- Scalars only (timeouts, model overrides, max_tokens) — NEVER secrets.
    config_overrides    JSONB NOT NULL DEFAULT '{}',
    -- Policies: rate limits, max concurrent tasks, allowed skill IDs.
    policies            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, agent_id)
);
CREATE INDEX app_agent_bindings_app   ON them.app_agent_bindings (application_id);
CREATE INDEX app_agent_bindings_agent ON them.app_agent_bindings (agent_id);
```

`ON DELETE RESTRICT` on `agent_id` prevents deleting an agent that applications still bind. `ON DELETE CASCADE` on `application_id` drops bindings when their application is deleted. `credential_bindings` is Fernet ciphertext keyed by slot name; the runtime decrypts per-invocation with `internal/crypto.DecryptStored`.

### 8.2 The subtype FK constraint (unchanged)

Migration 030 added `fk_agents_base_def`: `agents.id` REFERENCES `component_definitions(id)`.

**Publishing a canvas agent writes both rows in one transaction with a shared UUID.** `credential_schema` on the base row records the declared slots (names only):

```sql
BEGIN;

-- id = gen_random_uuid() in Go before the transaction

INSERT INTO them.component_definitions
    (id, kind, namespace, name, version, display_name, description,
     implementation_type, configuration_schema, default_config,
     capabilities, input_schema, output_schema, credential_schema,
     scope, tenant_id, status, content_hash, enabled)
VALUES
    ($id, 'agent', $namespace, $name, $version, $display_name, $description,
     'canvas_a2a', '{}', '{}',
     $capabilities, $input_schema, $output_schema,
     $credential_schema,        -- {"slots":[{"name":"salesforce_api","required":true},...]}
     'tenant', $tenant_id, 'published', $content_hash, true);

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

INSERT INTO them.agent_runtime_specs (id, tenant_id, definition_id, spec, spec_hash)
VALUES ($id, $tenant_id, $definition_id, $spec, $spec_hash);

COMMIT;
```

`endpoint_url` = the runtime pool DNS name — **no per-application variation**:
```
http://them-agent-runtime:9300/agents/{slug}
```
The application context travels in the invocation JWT/headers, never in the URL.

---

## 9. Docker Compose Integration — multi-replica

```yaml
# docker-compose.yml addition (profile: agents)
  them-agent-runtime:
    build:
      context: .
      dockerfile: Dockerfile.agent-runtime
    image: them-agent-runtime
    # NO container_name — allows multiple replicas
    profiles: [agents]
    deploy:
      replicas: 2               # horizontally scalable; bump freely — replicas are stateless
    expose: ["9300"]
    environment:
      # Infrastructure only. NO per-agent credentials here.
      - DATABASE_HOST=them-postgres
      - DATABASE_NAME=them
      - DATABASE_USER=them
      - DATABASE_PASSWORD=${DATABASE_PASSWORD}
      - REDIS_HOST=them-redis
      - REDIS_PORT=6379
      # Internal crypto key used to DECRYPT credential slots from app_agent_bindings.
      - THE_M_CRYPTO_KEY=${THE_M_CRYPTO_KEY}
      # Signing key used to VERIFY the invocation-context JWT from agentregistry.
      - THE_M_INVOCATION_JWT_KEY=${THE_M_INVOCATION_JWT_KEY}
      # OPTIONAL platform-wide LLM key — used ONLY as a fallback when an LLM step
      # has no provider_key_slot bound. NOT a per-agent credential source.
      - ANTHROPIC_API_KEY_PLATFORM=${ANTHROPIC_API_KEY_PLATFORM:-}
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

Notes:
- **No `ANTHROPIC_API_KEY` for per-agent use.** The old env var is gone. A platform-wide key exists only as a labelled fallback (`ANTHROPIC_API_KEY_PLATFORM`) for LLM steps with no `provider_key_slot`. Per-agent/per-application LLM keys come from the binding.
- **`deploy: replicas: 2`** and **no `container_name`** — the pool scales horizontally. Docker's internal DNS round-robins `them-agent-runtime` across replicas.
- **Redis task store** (§5.8) is what makes replicas interchangeable; there is no per-replica local state or shared filesystem.

**No new Traefik routing for the runtime.** Canvas agents are invoked outbound by `agentregistry` over the internal Docker network — not public entry points. The only new external API is the `agent-definitions` + `agent-bindings` CRUD on `them-go-bridge`:

```
Priority 120 — them-go-agent-defs
Rule: PathPrefix(`/api/v1/admin/agent-definitions`)

Priority 120 — them-go-agent-bindings
Rule: PathPrefix(`/api/v1/admin/applications/`) && PathRegexp(`/agent-bindings/`)
```

---

## 10. New Admin API Routes

### Agent definitions (author side)

```
POST   /api/v1/admin/agent-definitions               Create draft (save canvas)
GET    /api/v1/admin/agent-definitions               List all (tenant-scoped)
GET    /api/v1/admin/agent-definitions/{id}          Get one (with definition JSON)
PUT    /api/v1/admin/agent-definitions/{id}          Update draft (re-save canvas)
DELETE /api/v1/admin/agent-definitions/{id}          Delete draft (only if not published)

POST   /api/v1/admin/agent-definitions/{id}/validate Validate (returns errors list, never 4xx)
POST   /api/v1/admin/agent-definitions/{id}/publish  Compile + deploy + register

GET    /api/v1/admin/agent-definitions/{id}/export   Export Go source (Option A — zip, slots only)
```

### Application-level agent bindings (application-admin side) — NEW

```
GET    /api/v1/admin/applications/{app_id}/agent-bindings
       List all agent bindings for this application (credentials returned as
       "set"/"unset" flags only — NEVER plaintext, NEVER ciphertext)

GET    /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}
       Get one binding (slot set/unset flags, config overrides, policies — no secrets)

POST   /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}
       Create a binding: encrypt each provided slot value, store in app_agent_bindings

PUT    /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}
       Update a binding: re-encrypt changed slots, update overrides/policies.
       Slots omitted from the payload keep their existing encrypted value.

DELETE /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}
       Remove the binding
```

Binding write handlers Fernet-encrypt every slot value server-side with `crypto.EncryptStored` before persisting. Read handlers never return decrypted or encrypted credential material — only a per-slot boolean indicating whether a value is set.

---

## 11. Integration: How the Agent Appears Everywhere

### In the application canvas palette

Because publish writes a `component_definitions` row (kind=`agent`, status=`published`, enabled=`true`), the agent appears automatically in the application canvas palette:

```
GET /api/v1/admin/component-definitions
```

Dropping the agent node onto an application canvas wires it as a tool. The node's **Configure** panel (§5.6, §7) reads the agent's `credential_schema` and lets the application admin bind credentials. The orchestrator calls the agent via `agentregistry`, which looks up `endpoint_url` from `them.agents` and attaches the invocation context.

### In the agent registry

`agentregistry` caches agent configs in Redis at `them:agents:registry:{tenant_id}`. After publish, the runtime signals invalidation via Redis pub/sub on that channel; all workers and all runtime replicas pick up the new agent within the TTL window (600s default; pub/sub is immediate).

On every outbound call, `agentregistry` now also **attaches the invocation context** (signed JWT or `X-Them-*` headers): `tenant_id`, `application_id`, `agent_id`, and the binding lookup key (`binding_id`, or `application_id + agent_id` for the runtime to look up). See §5.5 and §14 Phase 1 step 7.

### In the security scanner

The security scanner agent (`them-security-agent`) runs on demand today. Publishing a canvas agent triggers an automatic scan on the `AgentDefinition` JSON — same endpoint as `POST /api/v1/admin/agents/{id}/security-scan`. Results stored in `them.agents.last_scan_result`. Because definitions contain slot names only, scans never see secret material.

---

## 12. Known Issues to Fix Before / During Phase 1

### Well-known path inconsistency

`go/internal/a2a/server.go` serves `/.well-known/agent.json` (missing `-card`). The discover flow, the security scanner, and the official SDK constant all use `/.well-known/agent-card.json`.

**Generated agents must use `agent-card.json`.** The inbound server should also be fixed (separate commit, flagged in `docs/LESSONS.md`).

### `CreateAgent` predates the FK

The existing Go `CreateAgent` DAL path (`go/internal/admin/dal/agents.go`) does not create the base `component_definitions` row and would violate `fk_agents_base_def` for canvas agents. Canvas agents **must** use the new `agentgen` publish path (both rows in one transaction). Do not route canvas agent creation through the generic `CreateAgent`.

### `anthropic-sdk-go` / `internal/llm`

The `llm` step reuses the existing `internal/llm` provider. Verify whether it uses the Anthropic Go SDK or raw HTTP before adding a second client, and reuse whichever is present. The provider must accept a **per-invocation API key** (from `LLMStepConfig.ProviderKeySlot` → binding), falling back to the platform key only when no slot is bound.

### Crypto + invocation-JWT keys

The runtime needs `THE_M_CRYPTO_KEY` (to `DecryptStored` credential slots) and `THE_M_INVOCATION_JWT_KEY` (to verify the invocation JWT from `agentregistry`). Both are derived by `generate-env.sh` and are infrastructure secrets — never per-agent.

---

## 13. Security Considerations

| Risk | Mitigation |
|---|---|
| **Prompt injection** via LLM step | Prompts frozen at compile time in `AgentSpec`. Caller input is strictly template *data*, never template source. Validated at publish. |
| **Cross-application credential leakage** | Credentials are per-application in `app_agent_bindings`, keyed by slot name, Fernet-encrypted. The runtime resolves them ONLY from the binding matching the invocation's `application_id`. Same agent + different application = different binding = isolated credentials. No global per-agent env vars. |
| **Secret at rest** | Only Fernet ciphertext in `app_agent_bindings.credential_bindings`. Never plaintext in DB, JSONB, definitions, or specs. |
| **Secret in transit / at runtime** | Decrypted value lives only in `InvocationContext.Credentials` for one request. Never logged (`InvocationContext.String()` is redacted), never in artifacts, never in Redis task state, never in Temporal history, never in HTTP responses, never in exports. |
| **Header spoofing of identity** | Invocation context is a signed JWT (`THE_M_INVOCATION_JWT_KEY`), verified before any identity claim is trusted. Plain `X-Them-*` headers are debug-only / internal-network only. |
| **BYO LLM key isolation** | `LLMStepConfig.ProviderKeySlot` resolves the LLM key from the application binding. Falls back to the platform key only if unset. The key never appears in the spec. |
| **Runaway loops** | `max_iterations` enforced by the interpreter with a hard ceiling (e.g. 100). Per-step timeouts prevent hanging HTTP/LLM calls. |
| **Tenant isolation** | Every DB row carries `tenant_id`. Runtime scopes spec loading per tenant. Redis keys include tenant/task ID: `them:agents:registry:{tenant_id}`, `them:agent:task:{task_id}`. |
| **Agent-calling-agent blast radius** | `a2a-call` resolves through `agentregistry` (tenant-scoped) and propagates the invocation context, so the callee resolves ITS OWN binding for the SAME application. Agents cannot cross tenant/application boundaries. |
| **Runtime bug blast radius** | Per-step panic recovery (`recover()`) prevents one bad agent from crashing the runtime for all agents/applications. Stateless replicas mean a crashed replica loses no durable state. |
| **Scan on publish** | Security scanner triggered automatically on every publish. Definitions carry slot names only — scans never see secret values. |

### Credential isolation (explicit rules)

1. Slot resolution happens **once per request**, from the binding matching the invocation's application.
2. Decrypted values live only in the request-scoped `InvocationContext.Credentials` map.
3. Nothing decrypted is ever logged, serialized, persisted to Redis/Postgres, put in artifacts, or returned in an HTTP response or export.
4. Caches are for ciphertext bindings and specs only — never for plaintext credentials.
5. `InvocationContext.String()` and any structured-log encoder must redact `Credentials`.

---

## 14. Implementation Roadmap

Each phase is one self-contained session. Do not begin the next phase in the same session.

### Phase 1 — Go A2A Runtime (`them-agent-runtime`), multi-tenant + horizontally scalable

**Goal:** prove one hand-written `AgentSpec` + one `app_agent_bindings` row starts a working, horizontally-scalable A2A agent that `agentregistry` can discover and invoke with a signed invocation context, resolving credentials per-application from the binding.

Steps:
1. Bump `go/go.mod` to `go 1.25`; update all three Dockerfiles to `golang:1.25-alpine`
2. `go get github.com/a2aproject/a2a-go/v2` — add official SDK
3. New package `go/internal/agentgen/` — `AgentSpec` (no `Secrets` field), `StepSpec`, all config structs (`HTTPStepConfig.CredentialSlot`/`CredentialInject`, `LLMStepConfig.ProviderKeySlot`), **`InvocationContext`**, **`InvocationPolicies`**, **`AppAgentBinding`**, `context.go`, `binding.go`
4. New binary `go/cmd/agent-runtime/main.go` —
   - parse + verify invocation JWT/headers → `InvocationContext`
   - load `AgentSpec` from DB (or file for dev) with a 60s `sync.Map` cache
   - load `app_agent_bindings` row from DB (by `binding_id`, else `application_id+agent_id`)
   - `crypto.DecryptStored` each credential slot into `InvocationContext.Credentials` (in memory)
   - apply config overrides + policies
   - build card, start A2A HTTP server using the SDK
5. Interpreter: `input`, `llm` (reuse `internal/llm`, honor `ProviderKeySlot` with platform fallback), `transform`, `response` steps only
6. **Redis task store** (`go/internal/agentgen/redistaskstore.go`) — `them:agent:task:{task_id}`, TTL 24h; wired as the SDK's task store (Phase 1, not optional)
7. `Dockerfile.agent-runtime` + compose service under `profiles: [agents]` with `deploy: replicas: 2`, **no `container_name`**, no per-agent `ANTHROPIC_API_KEY`
8. `agentregistry` extension: sign + attach the invocation context (JWT or `X-Them-*` headers) on every outbound call so the runtime can identify the calling application
9. Verify: start the runtime (2 replicas) with a hand-written spec + a hand-inserted `app_agent_bindings` row → `agentregistry` discovers the card → `message/send` (round-robined across replicas) resolves the app's credential from the binding and returns correct output; `tasks/get` works from a different replica than the one that created the task

Tests (update `go/TEST_INDEX.md`):
- Executor lifecycle: submitted → working → artifact → completed
- Card served at `/.well-known/agent-card.json` (not `agent.json`)
- `message/send` round-trip with LLM step mocked
- Input variable binding from text/data parts
- Invocation JWT verify + `InvocationContext` construction; reject unsigned/expired
- Binding resolution + slot decrypt; two bindings for the SAME agent resolve DIFFERENT credentials
- `ProviderKeySlot` chooses binding key; empty falls back to platform key
- Redis task store round-trip; `tasks/get` from a "second replica" (fresh store instance, same Redis)
- `InvocationContext` never logs `Credentials` (redaction test)

### Phase 2 — Agent Builder Canvas UI + Definition CRUD API

**Goal:** user can author and save agent definitions visually, including declaring credential slots. No compile/deploy yet.

Steps:
1. `db/0NN_canvas_a2a.sql` — `agent_definitions` table
2. Go admin routes: `POST/GET/PUT/DELETE /api/v1/admin/agent-definitions` — handlers + DAL + service
3. `frontend/src/app/admin/agents/builder/` — ReactFlow canvas, all node types from §7 (structure tier incl. **credential-slot editor on Agent Root**, pipeline tier), nested skill sub-canvas, palette sidebar, properties panel (HTTP step uses `credentialSlot` picker referencing declared slots; no free-text secret fields)
4. Wire to new API — save/load/list agent definitions
5. Traefik label for `them-go-agent-defs` at priority 120

Tests: Go DAL/handler CRUD; TypeScript compiles clean.

### Phase 3 — Compile + Publish Pipeline + Application Binding UI

**Goal:** clicking Publish deploys a live reusable agent; application admins bind credentials per application.

Steps:
1. `db/0NN_canvas_a2a.sql` — `agent_runtime_specs` **and `app_agent_bindings`** tables
2. `agentgen.Compile(AgentDefinition) → AgentSpec` — flatten pipeline DAG (topological sort), resolve `a2a-call` refs, validate model IDs, **validate every step `credential_slot`/`provider_key_slot` is declared in the Agent Root slots**, detect cycles. Emits slot names only — never secrets.
3. `POST .../validate` handler — returns `{valid, errors}` (never 4xx; errors are a payload)
4. `POST .../publish` handler — compile → write all rows in one transaction (§8: `component_definitions` w/ `credential_schema`, `agents`, `agent_runtime_specs`) → signal `agentregistry` cache invalidation
5. Application-canvas Agent-node **Configure** panel + binding CRUD routes (§10) — Fernet-encrypt slot values server-side into `app_agent_bindings`
6. Runtime: load spec + binding from DB on demand; reload spec on Redis pub/sub signal
7. `agentregistry` uses `application_id + agent_id` (or `binding_id`) so the runtime resolves the right binding

Tests: compile determinism (hash stability), all-rows-one-tx FK invariant, binding encrypt/decrypt round-trip, publish→bind→discover→invoke E2E, two applications binding the same agent get isolated credentials, stale spec replaced on republish.

### Phase 4 — Live Management + Advanced Steps

**Goal:** full lifecycle — update, disable, delete, security scan. Advanced pipeline steps.

Steps:
1. Update (new revision + republish), disable/enable (`them.agents.enabled`), delete (cascade rows; `ON DELETE RESTRICT` refuses deleting an agent still bound by an application)
2. Security scan triggered on publish — store result in `them.agents.last_scan_result`
3. Advanced interpreter steps: `branch`, `loop`, `parallel`, `a2a-call` (with invocation-context propagation), `human-wait` (Redis-backed paused state, resume on any replica), `stream-out`
4. "Export Go source" button — `text/template` codegen → zip download (slot names only; no secret material)
5. Scale/HA hardening: per-binding policy enforcement (rate limit via Redis), `max_concurrent_tasks`, allowed-skill allow-list

Tests: revision bump, referential-integrity guards on delete, scan-on-publish, cross-replica `human-wait` resume, per-application policy enforcement, each new step type's interpreter logic.

---

## 15. Summary

| Question | Answer |
|---|---|
| What does an author do? | Draw a flowchart, declare credential **slots** (names only), click Publish |
| What does an application admin do? | Drop the agent on the app canvas, fill its slots with this app's own credentials (Configure panel) |
| Can one agent serve many apps? | Yes — the SAME published `AgentSpec` is reused; each app has its own `app_agent_bindings` row. No cloning. |
| Where do secrets live? | Only as Fernet ciphertext in `app_agent_bindings.credential_bindings`. Never in definitions, specs, logs, exports, or Temporal history. |
| How does the runtime know which credentials? | Every call carries a signed invocation context (tenant/app/agent/binding); the runtime resolves the binding for that application and decrypts its slots in memory. |
| What runs the agent? | One generic, **stateless, horizontally-scalable** Go binary (`them-agent-runtime`, `replicas: 2+`) interpreting the compiled `AgentSpec` |
| How is task state shared across replicas? | Redis task store `them:agent:task:{task_id}` (Phase 1, not optional) — enables `tasks/get` and cross-replica `human-wait` resume |
| Official Go SDK? | Yes — after a trivial Go 1.23→1.25 bump (image already pulled); Redis task store swapped for the SDK's in-memory default |
| Does it appear in the app canvas? | Yes — automatically, via the component registry; the Agent node gains a Configure/binding panel |
| New infrastructure? | One new Docker service (`them-agent-runtime`, multi-replica), three new DB tables, two new Traefik rules |
| New columns on `them.agents`? | None — all needed columns already exist |
| How many phases? | Four, one session each |
| What's first? | Phase 1: Go 1.25 bump + SDK + runtime binary with `InvocationContext` (load binding, decrypt slots) + Redis task store + `agentregistry` invocation context + 4 basic step types |
