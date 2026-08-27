# AI Interactive Canvas Assistant — Architecture Design

**Status:** Design / Pre-implementation  
**Scope:** Interactive LLM-driven canvas authoring for Canvas Agents  
**Primary canvas:** Agent Builder (`input → http → transform → llm → mcp_call → response`)  
**Out of scope:** Application topology canvas (`entryPoint → orchestrator → agent`)  
**Depends on:** `AI_PLATFORM_FOUNDATION.md` (node config schemas, wire format, validation)  
**Last updated:** 2026-08-27

---

## 1. Intended User Experience

The user has the Agent Builder open (`/admin/agents/builder`) editing a draft agent definition. An Assistant panel sits alongside the canvas — a persistent chat window. The canvas is live React Flow; it never fully reloads.

### Example multi-turn session

```
User: "Add an HTTP node that fetches weather data and connect it to the input."

Assistant thinks:
  → get_node_schema("http")           discovers required fields: method, url_template, extractions
  → add_node({ type:"http", label:"Weather API",
               config:{ method:"GET",
                        url_template:"https://api.open-meteo.com/v1/forecast?latitude={{.lat}}&longitude={{.lon}}&current_weather=true",
                        extractions:[{var:"weather_raw", json_path:"current_weather"}],
                        timeout_seconds:10 }})
  → connect_nodes({ from:"step-input-1", from_port:"output",
                    to:"step-http-1",    to_port:"input" })
  → validate_canvas()

"Done — I added a Weather API HTTP node and wired it to the input. The API returns
 `current_weather` which I'm extracting into `weather_raw`. Canvas is valid."

[Node appears on canvas immediately. Edge drawn. No page reload.]

User: "Extract only the temperature from that response and pass it to an LLM
       that writes a one-sentence weather summary."

Assistant thinks:
  → get_node_schema("transform")
  → get_node_schema("llm")
  → add_node({ type:"transform", label:"Extract Temperature",
               config:{ functions:[
                 { fn:"json_path", input_var:"weather_raw",
                   output_var:"temperature", args:{path:"temperature"} }
               ]}})
  → connect_nodes({ from:"step-http-1",      to:"step-transform-1" })
  → add_node({ type:"llm", label:"Weather Summary",
               config:{ provider:"anthropic", model:"claude-haiku-4-5-20251001",
                        user_prompt:"Temperature is {{.temperature}}°C. Write one sentence.",
                        output_var:"summary" }})
  → connect_nodes({ from:"step-transform-1", to:"step-llm-1" })
  → validate_canvas()

"Added a Transform node to extract temperature, then an LLM node to generate
 the summary. Both connected. Canvas valid — you need a Response node to complete
 the pipeline."

User: "Add the response node and save."

Assistant thinks:
  → add_node({ type:"response", label:"Output",
               config:{ from_var:"summary", media_type:"text/plain" }})
  → connect_nodes({ from:"step-llm-1", to:"step-response-1" })
  → validate_canvas()   ← valid
  → save_draft()

"Pipeline complete: input → HTTP → Transform → LLM → response. Saved as draft."
```

### Key experience properties

- Every tool call produces an immediate, visible canvas change — nodes appear, edges draw, validation badges update.
- The LLM never regenerates and reloads the entire definition JSON.
- The user retains full control: they can move nodes, edit fields in the properties panel, and undo any AI change.
- The assistant knows which definition and which skill it is editing throughout the conversation.
- Destructive changes (remove a node with downstream connections, replace a configured node) require a brief preview before execution.

---

## 2. Current Architecture and What Can Be Reused

### 2.1 What exists today

| Component | Location | Status |
|---|---|---|
| Agent Builder React canvas | `frontend/src/app/admin/agents/builder/page.tsx` | Exists — two React Flow instances (agent-level + pipeline) |
| Undo/redo (agent builder) | Same file — `undoStack`/`redoStack` useRef stacks, 100-snapshot limit | Exists |
| Live validation debounce | Same file — useEffect + setTimeout + AbortController | Exists |
| `StepNodeData._debug`, `._validation`, `._stub` | `frontend/.../builder/types.ts` | Exists — visual debug hooks already on each node |
| Node type registry | `go/internal/agentgen/noderegistry.go` — `NodeDef`, `AllNodeTypeInfos()` | Exists |
| `GET /api/v1/admin/node-types` | `go/cmd/them/main.go` — `NodeTypesHandler` | Exists (public, no auth) |
| Definition CRUD | `go/internal/admin/agent_definitions.go` | Exists — create, update draft, validate, publish |
| Validate endpoint (live JSON) | `POST /api/v1/admin/agent-definitions/{id}/validate` | Exists — accepts raw definition in body |
| Transform function catalog | `GET /api/v1/admin/transform-functions` | Exists |
| Transform test | `POST /api/v1/admin/transform-test` | Exists |
| `AgentValidationReport` | `go/internal/admin/service/agent_definitions_publish.go` | Exists — `{valid, issues[]Issue, step_contracts}` |
| MCP supervisor + client | `go/internal/mcp/` | Exists — client only, no MCP server |
| Auth middleware | `go/internal/auth/middleware.go` | Exists — `ClaimsFromCtx`, `TokenInfoFromCtx` |

### 2.2 What does not exist

| Component | Gap |
|---|---|
| `config_schema` on `NodeTypeInfo` | Missing — LLM cannot learn field shapes from `/node-types` |
| Canvas definition wire format endpoint | Missing — `canvasDefinition` struct is internal |
| MCP server implementation | Missing — the platform is only an MCP client today |
| Canvas Assistant chat UI | Missing |
| Incremental canvas mutation API | Missing — save is always full-document PUT |
| Server-push for canvas changes | Missing — no WebSocket/SSE for canvas state |
| Revision/conflict guard on draft updates | Missing — no optimistic lock |
| Session/context tracking for LLM conversation | Missing |

### 2.3 Relevant existing event infrastructure

The WS and SSE handlers (`go/internal/ws/handler.go`, `go/internal/sse/handler.go`) are wired to orchestration runs — events like `token`, `tool_call`, `done` sourced from Redis Streams. They are not connected to canvas state. Redis pub/sub channels exist for cache invalidation (`them:agents:changed`, `them:ep:config:changed`) but carry no canvas mutation payloads. **There is currently no server-push channel for canvas mutations.**

The agent builder frontend has no WebSocket or SSE subscriptions. All canvas state is local React state.

---

## 3. Architecture Comparison

### Approach A — Complete JSON generation and reload

The LLM generates a full canvas definition JSON in one turn and the frontend imports it, replacing the current canvas.

**How it works:** User sends instruction → LLM generates `canvasDefinition` JSON → frontend calls existing import/restore endpoint or `PUT /agent-definitions/{id}` with full body → frontend calls `loadDef()` which runs `docToCanvas()` and replaces all React Flow state → dagre re-runs layout.

**Pros:** Simple. No new backend primitives. Works with existing save flow.

**Cons:**
- Loses all user-placed node positions on every turn.
- Loses unsaved in-progress edits.
- No visibility into what changed between turns.
- The LLM must hold the entire canvas state in context for every turn.
- Cannot do incremental refinement ("change just the URL in that HTTP node").
- Cannot validate mid-construction — only after the full JSON is produced.
- Feels like document replacement, not conversation.

**Verdict: Rejected.** This is exactly the experience the product goal explicitly excludes.

### Approach B — Browser-owned state, controlled through messages

The LLM sends structured commands directly to the browser. The frontend owns all canvas state and applies mutations locally.

**How it works:** LLM generates a command sequence like `[{op:"add_node",...}, {op:"connect",...}]` → browser receives via WebSocket or SSE → applies to local React Flow state → no backend write until user saves.

**Pros:** Fastest visual feedback (no round-trip). No backend mutation API needed.

**Cons:**
- Canvas state lives only in the browser — no shared source of truth.
- Validation must happen in the browser (incomplete — server-side `agentgen.Validate` is authoritative).
- The LLM has no reliable way to read current canvas state between turns (browser is not a server).
- State is lost on page refresh between turns.
- Security: accepting mutations from an LLM directly into browser state with no server validation is unsafe — an adversarial prompt could inject arbitrary node configs.
- Concurrent edits (user + LLM both modifying nodes) are invisible and unrecoverable.

**Verdict: Rejected.** Security and state-ownership problems are fundamental.

### Approach C — Backend-owned draft with incremental REST commands

A set of fine-grained REST endpoints mutate the backend draft. The frontend polls or manually refreshes.

**How it works:** LLM calls `POST /api/v1/admin/agent-definitions/{id}/skills/{skill_id}/steps` to add a node, `POST /.../edges` to connect, etc. → backend applies to the stored `definition` JSON → frontend polls or user manually refreshes.

**Pros:** Backend is authoritative. No browser security issues. Works with existing save flow.

**Cons:**
- Without a live-push mechanism, changes are invisible until the user refreshes.
- Polling is janky and creates stale-state windows.
- Fine-grained REST endpoints duplicate the canvas state machine in the backend.
- The LLM needs many round-trips for a single user instruction (discover → add → connect → validate = 4 separate HTTP calls with latency).

**Verdict: Partially useful** but incomplete without a live-push mechanism. REST endpoints are the right backend primitive; polling is unacceptable for the UX.

### Approach D — MCP tools over a shared Canvas service (Recommended)

A Canvas Service owns the authoritative in-flight draft state. An MCP server exposes canvas operations as tools to the LLM. The frontend subscribes to a push channel and applies patches instantly.

**How it works:**
- Canvas Service (Go): thin stateful service wrapping the `agent_definitions` draft. Exposes incremental mutations (add node, update config, connect, disconnect, remove, validate, save) with revision tracking.
- MCP Server (Go): exposes those mutations as MCP tools. Runs inside the existing `them-go-bridge` process — not a separate service.
- LLM: calls MCP tools in sequence within a single conversation turn. Each tool call is a round-trip to the Canvas Service.
- Frontend: opens a SSE subscription to a per-session push channel. Canvas Service publishes each mutation as a patch event. React Flow applies patch immediately — no full reload.
- Revision guard: every mutation carries a `revision` token. If the draft was modified concurrently (by the user or another LLM call), the mutation is rejected and the LLM retries with the current revision.

**Pros:**
- Every tool call produces an immediate visible change on the canvas.
- The LLM discovers node schemas from the same `NodeDef` registry used by the runtime — no drift.
- Validation uses the existing `agentgen.Validate()` — same 7-stage compiler.
- The draft in `agent_definitions` remains the durable representation.
- The MCP server reuses the Canvas Service's Go code — no logic duplication.
- Tenant context, auth, and audit all flow through existing Go middleware.
- The same Canvas Service later supports the Run Debugger (highlight failed node, propose fix, apply patch).

**Cons:**
- Requires a new MCP server implementation (none exists today).
- Requires a new SSE push channel for canvas patches.
- Requires a revision field and optimistic locking on draft updates (currently absent).
- More moving parts than Approach A.

**Verdict: Recommended.** The interactive experience is only achievable with approach D.

---

## 4. Recommended Architecture

```
┌─────────────────────────────────────────────────────┐
│  Browser (Next.js)                                   │
│                                                      │
│  Agent Builder Canvas (React Flow)                   │
│    ├── nodes / edges state (useNodesState)           │
│    ├── undo/redo stacks (existing)                   │
│    ├── live validation badge (existing debounce)     │
│    └── SSE subscription → applyPatch()              │
│                                                      │
│  Assistant Panel (new)                               │
│    ├── conversation history                          │
│    ├── sends user message to Assistant API           │
│    └── streams LLM response tokens                  │
└──────────────────┬──────────────────────────────────┘
                   │ SSE (canvas patches)
                   │ REST (save, publish, undo)
                   ▼
┌─────────────────────────────────────────────────────┐
│  them-go-bridge (Go, port 8002)                      │
│                                                      │
│  Canvas Service (new)                                │
│    ├── Reads/writes agent_definitions draft          │
│    ├── Applies incremental patches                   │
│    ├── Calls agentgen.Validate() after each patch    │
│    ├── Manages revision tokens                       │
│    ├── Publishes SSE patch events per session        │
│    └── Maintains undo stack (server-side shadow)     │
│                                                      │
│  Canvas MCP Server (new — runs in-process)           │
│    ├── Implements MCP 2024-11-05 (streamable-HTTP)   │
│    ├── Tools: inspect, get_node_schema, add_node,    │
│    │         update_node, connect, disconnect,        │
│    │         remove_node, validate, save_draft        │
│    └── All tools delegate to Canvas Service          │
│                                                      │
│  Assistant Endpoint (new)                            │
│    ├── POST /api/v1/canvas-assistant/sessions        │
│    ├── POST /api/v1/canvas-assistant/{session}/turn  │
│    ├── Calls LLM with MCP tool loop                  │
│    └── Streams response tokens to browser            │
│                                                      │
│  Existing: agentgen compiler/validator               │
│  Existing: agent_definitions DAL                     │
│  Existing: auth middleware (JWT/bearer)              │
│  Existing: /admin/node-types handler                 │
└─────────────────────────────────────────────────────┘
```

---

## 5. Canvas State and Session Model

### 5.1 Where canvas state lives

The **durable state** is the `agent_definitions` draft row in Postgres (`definition` JSONB column). This is the source of truth.

The **in-flight state** during an assistant session is a **Canvas Session** record held in Redis:

```
Key: them:canvas:session:{session_id}
TTL: 30 minutes (extended on each interaction)
Value (JSON):
{
  "session_id": "cs_...",
  "tenant_id": "...",
  "user_id": 42,
  "definition_id": "...",
  "skill_id": "main",
  "revision": 7,
  "undo_stack": [ "<patch-1-inverse>", "<patch-2-inverse>", ... ],   // max 50
  "created_at": "...",
  "last_active_at": "..."
}
```

The `revision` integer matches the `definition.revision` in Postgres. Every mutation increments it atomically.

The **browser canvas state** is React Flow's local `useNodesState`/`useEdgesState`. It stays in sync via SSE patches. The browser does not call save after each patch — save is explicit (user clicks Save or LLM calls `save_draft`).

### 5.2 How the LLM identifies what it is editing

When the user opens the Assistant Panel on an agent definition, the frontend creates a Canvas Session:

```
POST /api/v1/canvas-assistant/sessions
Body: { "definition_id": "...", "skill_id": "main" }
Response: { "session_id": "cs_...", "revision": 7 }
```

Every subsequent LLM turn carries the `session_id`. The Canvas Service looks up the session to get `definition_id`, `skill_id`, and `revision`. The LLM never needs to track these itself — the session is the context anchor.

The `skill_id` scopes the canvas. The pipeline canvas in `builder/page.tsx` already has an "active skill" concept; the session is initialized with the currently-active skill.

### 5.3 SSE push channel

The frontend subscribes immediately after session creation:

```
GET /api/v1/canvas-assistant/sessions/{session_id}/events
Accept: text/event-stream
```

Each canvas mutation published by the Canvas Service arrives as:

```
data: {
  "type": "canvas_patch",
  "session_id": "cs_...",
  "revision": 8,
  "patch": {
    "op": "add_node",
    "node": { "id": "step-http-1", "type": "http", "label": "Weather API",
              "position": { "x": 400, "y": 200 },
              "config": { "method": "GET", "url_template": "..." } }
  },
  "validation": { "valid": true, "issues": [] }
}
```

The browser applies each patch to React Flow state using `setNodes`/`setEdges` — not `loadDef`, not `docToCanvas`. No layout recalculation unless the user triggers it.

---

## 6. Incremental Command Model

### 6.1 What a mutation is

A mutation is a single atomic operation on the canvas. Examples:

| Operation | What changes |
|---|---|
| `add_node` | One new step added to the skill's step list |
| `update_node` | One step's `config` or `label` updated |
| `connect_nodes` | One edge added between two ports |
| `disconnect_nodes` | One edge removed |
| `remove_node` | One step removed; its edges removed implicitly |
| `reorder_steps` | `next` references updated to change topology |

### 6.2 Atomic batches

An LLM instruction typically requires a small batch: "add an HTTP node and connect it" = `add_node` + `connect_nodes`. These should be applied as one atomic operation on the backend to avoid a briefly-invalid intermediate state on the canvas (node added but not yet connected).

**Design decision: the MCP layer batches internally, not at the protocol level.** Each MCP tool represents one logical operation from the LLM's perspective. The Canvas Service applies the operations from a single LLM turn as an atomic transaction — all committed together, published as ordered patch events.

The LLM calls tools one at a time within a turn. The Canvas Service buffers them in the session and flushes atomically when the LLM turn completes (or when `validate_canvas` is called).

**Exception:** `validate_canvas` is always synchronous and blocks until the current buffer is flushed and validated. This gives the LLM immediate feedback before continuing.

### 6.3 Patch representation

Each mutation is represented as a patch structure stored in the undo stack and published over SSE:

```go
type CanvasPatch struct {
    Op     string          `json:"op"`     // "add_node" | "update_node" | "connect" | "disconnect" | "remove_node"
    NodeID string          `json:"node_id,omitempty"`
    Node   *CanvasNode     `json:"node,omitempty"`    // for add_node
    Config json.RawMessage `json:"config,omitempty"`  // for update_node
    Edge   *CanvasEdge     `json:"edge,omitempty"`    // for connect/disconnect
}

type InversePatch struct {
    Forward  CanvasPatch `json:"forward"`
    Inverse  CanvasPatch `json:"inverse"`
}
```

The undo stack holds `InversePatch` entries. Undo applies the `Inverse` operation.

### 6.4 Should a command contain one operation or a batch?

**One MCP tool = one logical canvas operation.** The LLM expresses intent in natural language and the MCP tool makes it precise. A single user instruction maps to 1–5 tool calls in sequence. The Canvas Service's atomic flush at turn end ensures consistency.

Exposing a "batch" MCP tool would require the LLM to compose complex JSON arrays — error-prone and loses the benefit of validation feedback between operations.

---

## 7. MCP Server Design

### 7.1 Where it runs

The Canvas MCP Server runs **inside `them-go-bridge`** (port 8002) — not as a separate process. There is no operational benefit to a new process, and it would add a new service, Dockerfile, and compose entry for what is essentially a request-routing layer. The existing Go binary already handles admin routes, WS, SSE, and A2A — one more handler is straightforward.

This is consistent with how the MCP client (`them-mcp-service`) is a dedicated service because it needs a persistent supervisor goroutine; the Canvas MCP Server has no such requirement.

The MCP server is mounted at:
```
/api/v1/canvas-mcp
```

### 7.2 Transport

MCP 2024-11-05 streamable-HTTP (same protocol version as the existing MCP client):
- `POST /api/v1/canvas-mcp` — all JSON-RPC requests
- Response may be JSON or `text/event-stream` depending on whether the tool streams

The LLM calls this endpoint directly via its MCP client during an assistant turn. Auth: the Assistant Endpoint forwards the user's JWT to MCP tool calls as a bearer token. The Canvas MCP Server validates it using the same `auth.JWTMiddleware`.

### 7.3 Tool inventory

These are the tools the LLM can call. Names are lowercase_snake_case following MCP conventions.

**Discovery (read-only, no revision required)**

| Tool | Input | Output | Description |
|---|---|---|---|
| `inspect_canvas` | `{session_id}` | Current step list with IDs, types, labels, connection topology | Shows the LLM the current pipeline state |
| `get_node_types` | `{}` | `[]NodeTypeInfo` with `config_schema` per type | Lists available step types. Delegates to `AllNodeTypeInfos()` |
| `get_node_schema` | `{node_type: string}` | `NodeTypeInfo` with `config_schema` | Schema for a single node type |
| `get_transform_functions` | `{}` | `[]FunctionDef` with Examples | Lists transform functions for constructing TransformStepConfig |

**Mutations (require `session_id` and `revision`)**

| Tool | Key inputs | Description |
|---|---|---|
| `add_node` | `{session_id, revision, type, label?, config, position?}` | Adds a step to the active skill. If `position` omitted, Canvas Service assigns via layout hint |
| `update_node` | `{session_id, revision, node_id, config?, label?}` | Updates a step's config or label. Partial update — unset fields preserved |
| `connect_nodes` | `{session_id, revision, from_node_id, from_port?, to_node_id, to_port?}` | Adds an edge. Port defaults: `from_port="output"`, `to_port="input"` where unambiguous |
| `disconnect_nodes` | `{session_id, revision, from_node_id, to_node_id}` | Removes an edge |
| `remove_node` | `{session_id, revision, node_id}` | Removes a step and all its edges. Requires preview if node has downstream dependents |
| `validate_canvas` | `{session_id}` | Flushes pending mutations, runs `agentgen.Validate()`, returns `AgentValidationReport` |
| `save_draft` | `{session_id}` | Flushes and saves the current state to `agent_definitions` draft. Does not publish. |

**Example `add_node` tool schema (MCP format):**

```json
{
  "name": "add_node",
  "description": "Add a step node to the currently active skill pipeline. Call get_node_schema first to learn the required config fields for the node type.",
  "inputSchema": {
    "type": "object",
    "required": ["session_id", "revision", "type"],
    "properties": {
      "session_id":  { "type": "string" },
      "revision":    { "type": "integer", "description": "Current canvas revision. Obtained from inspect_canvas or the previous tool response." },
      "type":        { "type": "string", "description": "Step type. See get_node_types for valid values." },
      "label":       { "type": "string" },
      "config":      { "type": "object", "description": "Step config matching the node type's config_schema." },
      "position":    { "type": "object", "description": "Optional {x, y}. If omitted, assigned automatically." }
    }
  }
}
```

**Every mutation tool response includes:**
```json
{
  "ok": true,
  "revision": 8,
  "node_id": "step-http-1",
  "validation": { "valid": true, "issues": [] }
}
```

Or on revision conflict:
```json
{
  "ok": false,
  "error": "revision_conflict",
  "current_revision": 9,
  "message": "Canvas was modified since revision 7. Call inspect_canvas to get current state."
}
```

### 7.4 How tool schemas are generated from NodeDef

`get_node_types` and `get_node_schema` return `NodeTypeInfo` extended with `config_schema` (from `AI_PLATFORM_FOUNDATION.md` Component 1.1). The Canvas MCP Server does not maintain its own node definitions. It calls `agentgen.AllNodeTypeInfos()` and serializes the result. If `NodeDef` changes, MCP tool inputs change automatically.

**The MCP server has no knowledge of individual node types.** It exposes `get_node_schema` as a discovery tool. The LLM is expected to call it before constructing a config — this is the pattern that eliminates drift.

### 7.5 Validation flow in the MCP layer

`validate_canvas` calls `agentgen.Validate(agentID, tenantID, definitionID, slug, currentDefinitionJSON)`. This is the same 7-stage compiler used by `POST /agent-definitions/{id}/validate`. The MCP server does not implement its own validation.

The validation result is embedded in every mutation tool response (post-mutation validation). The LLM sees issues after every tool call, not only at the end.

---

## 8. Backend Service Boundaries

### 8.1 Canvas Service

A new Go service layer in `go/internal/canvas/` responsible for:

1. **Session management** — create, look up, extend TTL, delete
2. **Draft reads** — load current `canvasDefinition` from `agent_definitions`
3. **Incremental mutations** — apply `CanvasPatch` to in-memory JSON, write back to Postgres
4. **Revision tracking** — atomic increment using Postgres `UPDATE ... RETURNING revision`
5. **Post-mutation validation** — call `agentgen.Validate()` after each flush
6. **SSE event publication** — write patch events to a per-session Redis channel
7. **Undo stack** — push `InversePatch` to session's Redis undo list

The Canvas Service does **not** own LLM inference, MCP protocol framing, or frontend rendering. It owns canvas state mutations and consistency.

```go
type CanvasService struct {
    db       DBQuerier       // agent_definitions reads/writes
    redis    RedisClient     // session state + SSE channel
    compiler agentgenAPI     // interface over agentgen.Validate
}

func (s *CanvasService) CreateSession(ctx, tenantID string, userID int64, definitionID, skillID string) (*CanvasSession, error)
func (s *CanvasService) GetSession(ctx, sessionID string) (*CanvasSession, error)
func (s *CanvasService) ApplyMutation(ctx, sessionID string, revision int, patch CanvasPatch) (*MutationResult, error)
func (s *CanvasService) ValidateCanvas(ctx, sessionID string) (*AgentValidationReport, error)
func (s *CanvasService) SaveDraft(ctx, sessionID string) error
func (s *CanvasService) Undo(ctx, sessionID string) (*MutationResult, error)
```

### 8.2 Assistant Endpoint

A new handler in `go/internal/admin/` that drives the LLM conversation:

```
POST /api/v1/canvas-assistant/sessions
POST /api/v1/canvas-assistant/{session_id}/turns
GET  /api/v1/canvas-assistant/{session_id}/events  (SSE)
POST /api/v1/canvas-assistant/{session_id}/undo
```

The `/turns` endpoint:
1. Authenticates the request (JWT middleware — same as all admin routes)
2. Loads the canvas session
3. Appends the user message to the conversation history
4. Calls the LLM API with the conversation history + MCP tool definitions
5. Processes the tool-call loop: for each `tool_use`, calls the Canvas MCP Server internally (same process — no HTTP round-trip)
6. Streams LLM response tokens back to the browser via SSE on the `/events` channel

The LLM is called using the Anthropic API (Claude) with `tools` set to the Canvas MCP tool schemas. The existing `go/internal/agentgen/interpreter.go` `LLMFactory` interface is the right abstraction to reuse for the LLM call — no new provider abstraction needed.

**Conversation history** is stored in Redis per session (same TTL as session). It is NOT stored in `them.runs` or `them.tasks` — this is an authoring conversation, not an orchestration run. This distinction matters for billing, auditing, and data retention.

### 8.3 What REST APIs support directly

The browser continues to use REST for:
- `PUT /api/v1/admin/agent-definitions/{id}` — explicit saves
- `POST /api/v1/admin/agent-definitions/{id}/publish` — publish
- `GET /api/v1/admin/agent-definitions/{id}` — load on page open
- `POST /api/v1/canvas-assistant/sessions` — create session
- `POST /api/v1/canvas-assistant/{session_id}/undo` — undo button

The LLM uses MCP tools for all canvas mutations. The REST mutation endpoints (`add_node`, etc.) may also be offered as REST for testing — they share the same Canvas Service code.

---

## 9. Frontend Integration

### 9.1 Applying patches in React Flow

The SSE event handler receives `canvas_patch` events and applies them using React Flow's `setNodes`/`setEdges` — **not** `loadDef` or `docToCanvas`. This is what prevents full reloads.

```typescript
// In builder/page.tsx — new useEffect on session
useEffect(() => {
  if (!sessionId) return;
  const es = new EventSource(`/api/v1/canvas-assistant/${sessionId}/events`);
  es.onmessage = (e) => {
    const event = JSON.parse(e.data);
    if (event.type === 'canvas_patch') {
      applyCanvasPatch(event.patch, setLocalPipeNodes, setLocalPipeEdges);
      setCurrentRevision(event.revision);
      setValidationIssues(event.validation?.issues ?? []);
    }
  };
  return () => es.close();
}, [sessionId]);
```

`applyCanvasPatch` is a new pure function that maps each patch op to React Flow state mutations:

```typescript
function applyCanvasPatch(patch: CanvasPatch, setNodes, setEdges) {
  switch (patch.op) {
    case 'add_node':
      setNodes(ns => [...ns, patchToReactFlowNode(patch.node)]);
      break;
    case 'update_node':
      setNodes(ns => ns.map(n => n.id === patch.node_id
        ? { ...n, data: { ...n.data, config: patch.config } } : n));
      break;
    case 'connect':
      setEdges(es => [...es, patchToReactFlowEdge(patch.edge)]);
      break;
    case 'disconnect':
      setEdges(es => es.filter(e => !(e.source === patch.edge.from && e.target === patch.edge.to)));
      break;
    case 'remove_node':
      setNodes(ns => ns.filter(n => n.id !== patch.node_id));
      setEdges(es => es.filter(e => e.source !== patch.node_id && e.target !== patch.node_id));
      break;
  }
}
```

### 9.2 Undo/redo integration

The agent builder already has `undoStack`/`redoStack` `useRef` stacks. These capture `JSON.stringify(buildDefinitionDoc())` snapshots.

For AI-driven changes, undo is handled **server-side** (the Canvas Service maintains the `InversePatch` stack in the session). When the user clicks Undo after an AI change, the browser calls:

```
POST /api/v1/canvas-assistant/{session_id}/undo
```

The Canvas Service applies the inverse patch, publishes the reversed `canvas_patch` event over SSE, and the canvas reverts. The browser's local undo stack is bypassed for AI-originating changes.

**Consistency rule:** AI changes push to the server-side undo stack. User changes (drag, properties panel edit) push to the browser's local undo stack. `Ctrl+Z` / Undo button always hits the local stack first; if empty, falls through to the server-side stack. The UX makes this seamless.

### 9.3 Node positioning when the LLM adds nodes

When `add_node` does not include a `position`, the Canvas Service assigns one using a simple placement heuristic:

1. Find all existing nodes in the active skill.
2. Compute the bounding box of existing nodes.
3. Place the new node to the right of the rightmost node (or below the bottommost in TB layout), with `ranksep=100` gap.

The position is included in the `canvas_patch` event so the browser places the node immediately without running dagre. The user can then drag it if desired.

The user's existing "Auto Layout" button in the builder still works and triggers dagre over the current React Flow state — unchanged.

### 9.4 Validation visual feedback

`StepNodeData._validation` and `._errorMsg` already exist on every step node. When the SSE event carries `validation.issues`, the browser updates the affected nodes:

```typescript
if (event.validation?.issues?.length) {
  const byNode = groupBy(event.validation.issues, i => i.node_id);
  setLocalPipeNodes(ns => ns.map(n => ({
    ...n,
    data: {
      ...n.data,
      _validation: byNode[n.id] ? (byNode[n.id].some(i => i.severity === 'error') ? 'error' : 'warning') : null,
      _errorMsg: byNode[n.id]?.[0]?.message
    }
  })));
}
```

This uses the existing visual debug hooks — no new node component changes needed for basic validation display.

---

## 10. Revision, Concurrency, and Conflict Handling

### 10.1 The problem

The `agent_definitions` draft has an `UpdateDraftAgentDefinition` call guarded only by `WHERE status='draft'`. There is no `WHERE revision = $expected` check. Concurrent edits (user editing a field in the properties panel while the LLM applies a patch) can silently overwrite each other.

### 10.2 Optimistic locking

Add `revision` as a precondition to all Canvas Service mutations:

```sql
UPDATE them.agent_definitions
SET definition = $new_definition,
    definition_hash = $hash,
    revision = revision + 1,
    updated_at = now()
WHERE id = $id
  AND tenant_id = $tenant_id
  AND status = 'draft'
  AND revision = $expected_revision
RETURNING revision
```

If `0 rows affected`, the Canvas Service returns `revision_conflict` and the LLM receives an error from its tool call instructing it to re-read current state.

### 10.3 Concurrent user edits during LLM session

When the user edits the canvas manually (properties panel, drag-connect) while a session is open, the browser increments a local `pendingRevision` and sends it in any subsequent REST saves. The Canvas Service reconciles:

- If the user saves a manual change that conflicts with a recent LLM patch, the user's save wins (last-write-wins within the session) and the session revision is updated. The LLM's next tool call will get the conflict error and re-read.
- The session SSE channel also receives the user's own changes (published by the save handler) so the LLM's context stays current.

### 10.4 What does NOT require revision checks

Read-only tools (`inspect_canvas`, `get_node_types`, `get_node_schema`) never conflict — no revision required.

---

## 11. Approval, Preview, and Safety

### 11.1 Which changes apply immediately

| Operation | Apply immediately? |
|---|---|
| `add_node` | Yes — always safe |
| `update_node` | Yes — reversible |
| `connect_nodes` | Yes — reversible |
| `disconnect_nodes` | Yes — reversible |
| `validate_canvas` | Yes — read-only |
| `save_draft` | Yes — user invoked via LLM instruction |
| `remove_node` with no downstream dependents | Yes |
| `remove_node` with downstream dependents | **Preview required** |
| Replacing a configured node's type | **Preview required** |

### 11.2 Preview flow

When the Canvas Service determines a mutation requires preview, it returns a `preview_required` response instead of applying the change:

```json
{
  "ok": false,
  "reason": "preview_required",
  "preview": {
    "description": "Removing 'LLM Summary' will also disconnect 'Response' (which reads its output). Continue?",
    "impact": ["disconnect: step-llm-1 → step-response-1", "remove: step-llm-1"],
    "confirm_token": "prev_abc123",
    "expires_at": "2026-08-27T10:05:00Z"
  }
}
```

The LLM presents this to the user. The user confirms in the chat ("yes, remove it"). The LLM calls the tool again with `confirm_token`:

```json
{ "op": "remove_node", "node_id": "step-llm-1", "confirm_token": "prev_abc123" }
```

`confirm_token` is a signed, short-lived (60s) opaque token stored in Redis. The Canvas Service validates it before applying the destructive operation.

### 11.3 What the LLM cannot do

The Canvas MCP Server enforces these invariants regardless of LLM instruction:
- Cannot remove the `input` (source) node — it is the skill entry point
- Cannot add a node type with `Execute=nil` (stub) and `status=published` path — `validate_canvas` will catch it
- Cannot add edges that violate `EdgeRules.MaxIn` or `MaxOut`
- Cannot set `config` fields not in the node's `config_schema` — rejected at the Canvas Service layer
- Cannot access definitions belonging to another tenant — session is bound to a tenant at creation

---

## 12. Security, Authorization, and Auditability

### 12.1 Authentication

All `/api/v1/canvas-assistant/*` endpoints are behind the same JWT middleware as admin routes. TenantID is extracted from the JWT claim (never from request parameters). Sessions are created with the TenantID and UserID from the token; all subsequent operations validate that the session's TenantID matches the request's TenantID.

The LLM does not have its own credentials. It uses the session (which was created by the authenticated user) to perform mutations. The user is the principal for all LLM-driven changes.

### 12.2 What the LLM can and cannot access

The Canvas MCP Server only exposes tools that operate on the specific `definition_id` bound to the session. It cannot:
- List agent definitions for other tenants
- Access `them.agents` data outside the definition being edited
- Call the orchestration runtime, A2A agents, or MCP tools (those are separate systems)
- Read or write `agent_definitions` rows for other tenants

### 12.3 Audit trail

Every Canvas Service mutation writes an audit record:

```go
type CanvasAuditEvent struct {
    SessionID    string          `json:"session_id"`
    TenantID     string          `json:"tenant_id"`
    UserID       int64           `json:"user_id"`
    DefinitionID string          `json:"definition_id"`
    Op           string          `json:"op"`
    Patch        json.RawMessage `json:"patch"`
    AIGenerated  bool            `json:"ai_generated"`
    Timestamp    time.Time       `json:"timestamp"`
    Revision     int             `json:"revision"`
}
```

`AIGenerated=true` for MCP tool-driven changes. `AIGenerated=false` for user REST saves. These are written to `them.audit_logs`. This enables administrators to review what an AI assistant changed and when.

### 12.4 Prompt injection risk

A user's instruction could contain adversarial text intended to manipulate the LLM into calling unexpected MCP tools. Mitigations:

- The Canvas MCP Server enforces hard invariants regardless of tool call content (see Section 11.3).
- The `confirm_token` preview flow requires explicit user confirmation for destructive operations.
- The LLM's system prompt instructs it to only call canvas tools and never execute arbitrary instructions embedded in user messages.
- Node config values are stored as JSON and passed to the compiler — never executed as code.

---

## 13. Run Debugger Extension (Future)

The same Canvas Service and MCP tools form the foundation for an interactive Run Debugger:

1. User opens a failed run trace (from `GET /api/v1/runs/{id}/trace` — Phase 2 of Foundation).
2. Debugger creates a Canvas Session pointing to the definition that was active for that run.
3. The failed step is highlighted using `StepNodeData._debug.state = 'error'` — the existing debug hook in the agent builder.
4. The LLM's `inspect_canvas` returns the current canvas with the error state visible.
5. The LLM diagnoses the failure (rule-based classifier — see Foundation doc), proposes a patch, calls `update_node` or `add_node`, validates, and presents to the user for approval.
6. User approves → `save_draft` → optionally publish.

The only addition needed for the debugger beyond the assistant foundation is:
- An API to "open a debug session from a run" that populates `_debug` state on the appropriate nodes.
- The run trace endpoint (Foundation Phase 2).

No new canvas mutation primitives are needed.

---

## 14. Failure Handling and Reconnection

### 14.1 SSE reconnection

Browser SSE clients reconnect automatically using the `Last-Event-ID` header. Each patch event carries an `id` field (monotonic per session). On reconnect, the Canvas Service replays events from that cursor using the session's Redis event log (short ring buffer, last 50 events, matching the WS replay pattern in `ws/handler.go`).

If the ring buffer has been trimmed (session very old or restarted), the Canvas Service sends a `canvas_reload` event instead of `replay_unavailable`:

```
data: {"type": "canvas_reload", "definition": <full current definition JSON>}
```

The browser reloads the canvas from this payload (equivalent to `loadDef`) — the only full reload path, triggered only on SSE reconnect after buffer expiry.

### 14.2 LLM tool call failure

If a Canvas Service mutation fails (DB error, revision conflict), the MCP tool returns an error. The LLM is expected to handle it:
- `revision_conflict`: call `inspect_canvas` to re-read current state, retry the operation.
- `preview_required`: surface the preview to the user, wait for confirmation.
- DB error: report to the user and stop the turn.

No partial canvas state is persisted on tool call failure — the Canvas Service uses a transaction-per-mutation pattern.

### 14.3 Session expiry

Sessions expire after 30 minutes of inactivity (Redis TTL). The SSE connection is closed. The browser shows a "Session expired — start a new conversation" message. No canvas data is lost — the last saved draft remains in Postgres.

---

## 15. Testing Strategy

### 15.1 Unit tests

- `CanvasService.ApplyMutation` — test each op type, revision conflict path, preview trigger conditions
- `applyCanvasPatch` (TypeScript) — test each op type against React Flow node/edge arrays
- Canvas MCP Server tool handlers — test schema validation, tenant isolation, preview token generation

### 15.2 Integration tests

- Full turn: create session → call `add_node` tool → verify SSE event published → verify `agent_definitions` definition JSONB updated → verify `agentgen.Validate` result returned
- Revision conflict: two concurrent `update_node` calls with same revision → second returns conflict
- Undo: apply 3 mutations → undo twice → verify canvas state matches post-first-mutation state

### 15.3 End-to-end

The smallest E2E test proving the interactive experience:

```
1. POST /canvas-assistant/sessions { definition_id, skill_id }
2. Open SSE /sessions/{id}/events
3. Call Canvas MCP: add_node { type:"http", config:{...} }
4. Assert SSE receives canvas_patch with new node
5. Assert agent_definitions definition JSONB contains new step
6. Assert AgentValidationReport.valid reflects current canvas state
7. Call Canvas MCP: connect_nodes
8. Repeat assertions
```

This test can be run without a browser. The SSE event delivery is the critical path — verify it before any frontend integration.

### 15.4 Testing the MCP server without an LLM

The Canvas MCP Server can be called directly via HTTP with `Content-Type: application/json` and MCP JSON-RPC payloads. No LLM is needed to test the canvas mutation logic. Use `POST /api/v1/canvas-mcp` with `{"method":"tools/call","params":{"name":"add_node","arguments":{...}}}`.

---

## 16. Phased Implementation Plan

### Phase 0 — Prerequisite: Node config schemas and wire format (Foundation doc Phase 1)

Before the assistant can work, the LLM must be able to discover node schemas dynamically. This requires:
- `config_schema` added to `NodeTypeInfo` (Foundation 1.1) — `go/internal/agentgen/noderegistry.go`, `nodes.go`
- Canvas definition wire format endpoint (Foundation 1.2) — `GET /api/v1/admin/agent-definitions/schema`

Without Phase 0, the LLM must hardcode node schemas, which will drift. Phase 0 is a prerequisite, not optional.

**Estimate: 2–3 days.**

### Phase 1 — Minimum end-to-end proof (proves the interactive loop)

**Goal:** User instruction → LLM tool call → canvas patch → SSE event → immediate frontend update.

1. Canvas Service: `CreateSession`, `GetSession`, `ApplyMutation` (add_node, connect_nodes only), `ValidateCanvas`
2. Canvas MCP Server: `inspect_canvas`, `get_node_types`, `get_node_schema`, `add_node`, `connect_nodes`, `validate_canvas`
3. Assistant Endpoint: `POST /sessions`, `POST /sessions/{id}/turns` (no streaming yet — simple request/response)
4. SSE push: simple Redis pub/sub channel per session; browser EventSource subscription
5. Frontend: `applyCanvasPatch` for `add_node` and `connect` ops; session creation on panel open; SSE subscription

**Not in Phase 1:** undo, preview/approval, streaming LLM tokens, all op types.

**Success criterion:** The example conversation in Section 1 works end-to-end with a real LLM, a real canvas, and real SSE-pushed updates.

**Estimate: 1–2 weeks.**

### Phase 2 — Full mutation set and undo

1. Remaining ops: `update_node`, `disconnect_nodes`, `remove_node`, `save_draft`
2. Preview/approval for destructive ops (confirm tokens)
3. Server-side undo stack + `POST /sessions/{id}/undo`
4. Browser undo integration (AI undo via server, user undo via local stack)
5. Streaming LLM response tokens to the browser over SSE
6. Revision conflict guard (optimistic locking on draft updates)

**Estimate: 1 week.**

### Phase 3 — Polish, debugger, and audit

1. Run Debugger session type (open from failed run trace)
2. `_debug` state population on nodes from run trace
3. Audit log (`them.canvas_audit_events` or reuse `them.audit_logs`)
4. Session expiry and SSE reconnection with buffer replay
5. Production hardening: rate limiting on tool calls per session, max turn depth

**Estimate: 1 week.**

---

## 17. Open Decisions and Alternatives Considered

### 17.1 Should conversation history be stored in Postgres or Redis?

**Redis (recommended):** Simple, TTL-managed, no schema migration. Authoring conversations are ephemeral — a user doesn't need to resume a 3-day-old assistant session. Consistent with session storage.

**Postgres:** Persistent, queryable, auditable. Required if we want to surface "previous conversations" in a UI or if regulatory compliance requires durable retention of LLM interactions.

Decision: start with Redis (Phase 1), plan for optional Postgres persistence in Phase 3 if compliance requires it.

### 17.2 Should the Canvas MCP Server use stdio or HTTP transport?

The LLM client in the Assistant Endpoint is in-process — it doesn't need a network transport. **stdio** would work for local embedding. However, the existing MCP client infrastructure (`go/internal/mcp/client.go`) uses HTTP. Using the same transport makes the Canvas MCP Server testable from the command line without an in-process LLM.

**HTTP (recommended)** — consistent with existing MCP infrastructure, testable independently.

### 17.3 Should node positions be managed by the server or the browser?

**Server-assigned (recommended for LLM-added nodes):** The Canvas Service computes a placement hint based on existing node positions. This is sent in the `canvas_patch` and the browser uses it. No dagre run needed on the browser for each AI-added node.

**Browser-assigned:** The browser runs dagre after receiving a new node without position. This works but introduces a full-layout recalculation on every add, potentially repositioning all nodes unexpectedly.

Recommendation: server assigns position for AI-added nodes (simple right-of-last heuristic). User can trigger full dagre relayout via the existing Auto Layout button. User-dragged positions are always respected.

### 17.4 Should `validate_canvas` run automatically after every mutation or only on demand?

**After every mutation (recommended):** The Canvas Service runs `agentgen.Validate()` as part of `ApplyMutation`. Validation result is included in every `canvas_patch` event. The LLM always has current validation state. Validation is fast (in-memory, no DB) — suitable for per-mutation invocation.

**On demand:** LLM must explicitly call `validate_canvas`. Simpler but means the LLM can accumulate an invalid canvas state without knowing it.

Recommendation: always run post-mutation validation. `validate_canvas` as an explicit tool is retained for the LLM to force a validation check before deciding what to do next.

### 17.5 A2A agents vs. MCP tools — are they confused here?

They are explicitly kept separate. The Canvas MCP Server exposes **canvas operations** — mutations to the definition being authored. A2A agents are **peer services** that the finished canvas agent will invoke at runtime (via `a2a_call` step nodes). They are not redefined as canvas operations. An `a2a_call` step in the canvas is a reference to an A2A agent — the LLM learns its slug from `GET /api/v1/admin/agents` and puts it in the step config. The A2A agent itself is never called during canvas authoring.

---

## 18. Confidence Assessment

**High confidence:**
- SSE-based live push for canvas patches. The existing WS/SSE handlers (`go/internal/ws/handler.go`, `go/internal/sse/handler.go`) use exactly this pattern (Redis pub/sub → SSE events). The Canvas Service follows the same pattern for a new channel.
- `applyCanvasPatch` in the browser. React Flow's `setNodes`/`setEdges` APIs are well-suited for incremental updates. `StepNodeData._validation` and `._debug` hooks already exist.
- MCP server inside `them-go-bridge`. The Go binary already mounts multiple handler trees. Adding a Canvas MCP handler is straightforward.
- Node schema discovery via `AllNodeTypeInfos()`. The registry is the authoritative source. The Foundation Phase 0 work (adding `config_schema`) is the only prerequisite.

**Medium confidence:**
- Optimistic locking via `WHERE revision = $expected`. Adding the column and check is simple; ensuring every write path uses it requires auditing the existing `UpdateDraftAgentDefinition` call sites (currently one in the service layer).
- Server-side undo stack with inverse patches. For simple ops (`add_node` → inverse is `remove_node`; `connect` → inverse is `disconnect`), this is mechanical. For `update_node`, the inverse requires reading the previous config before applying the mutation — adds a read-before-write to every update.
- LLM tool-call loop quality. The experience quality depends heavily on the LLM's ability to call tools in the right order, handle revision conflicts gracefully, and produce valid node configs. This is a prompt engineering and model selection concern, not a backend concern. Claude models with tool use are well-suited; weaker models may produce invalid configs that validation rejects.

**Lower confidence:**
- Concurrent user edits and LLM session interoperability. The revision conflict mechanism handles the correctness case. The UX of "the LLM just overwrote my manual change" is jarring even when technically handled. The design needs a clear UX signal (e.g. "AI is working — canvas locked for editing") during active LLM turns.
- Phase 1 estimate (1–2 weeks). The MCP server implementation is new territory for this codebase. The existing MCP code is a client only. Building the server side — even following the same protocol — adds implementation risk. The estimate assumes the Foundation Phase 0 is already done.
