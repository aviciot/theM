# Canvas MCP Server — Design

**Status:** Pre-implementation  
**Requested:** `mcp/CANVAS_MCP_DESIGN_REQUEST.md`  
**Depends on:** `AI_PLATFORM_FOUNDATION.md`, `AI_INTERACTIVE_CANVAS_ASSISTANT_DESIGN.md`  
**Last updated:** 2026-08-29

---

## 1. Feasibility Verdict

**Yes — this is feasible and the codebase is unusually well-positioned for it.**

Foundation Phase 0 prerequisites are already complete:

| Required primitive | Status |
|---|---|
| `GET /admin/node-types` — full `NodeTypeInfo[]` with `ConfigFields`, `UsageNotes`, `Examples`, `AllowedSuccessors`, port defs, edge rules | **Done** |
| `GET /admin/agent-definitions/schema` — wire format + 16 issue codes + node types in one call | **Done** |
| `GET /admin/transform-functions` — catalog with examples | **Done** |
| `POST /admin/agent-definitions/{id}/validate` — accepts inline JSON body without DB save | **Done** |
| `POST /admin/agent-definitions` / `PUT /{id}` — create/update drafts | **Done** |
| MCP client infrastructure (`go/internal/mcp/client.go`) — protocol reference | **Done** |
| `agentgen.AllNodeTypeInfos()`, `agentgen.ValidateDefinitionJSON()` — Go-callable | **Done** |
| LLM integration (`go/internal/llm/`, `anthropicCompleter` in `agent_definition_schema.go`) | **Done** |
| SSE infrastructure pattern (`go/internal/sse/handler.go` — Redis pub/sub → SSE) | **Done** |

What is genuinely new: MCP JSON-RPC server (not client), incremental JSON patch on definition JSONB, per-session SSE push channel, and a test-run endpoint.

---

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────────┐
│  External                                            │
│  Claude Desktop / other LLM clients                 │
└─────────┬───────────────────────────────────────────┘
          │ MCP 2024-11-05 HTTP (POST /mcp)
          │ Authorization: Bearer <user-jwt-or-pat>
          ▼
┌─────────────────────────────────────────────────────┐
│  them-canvas-mcp   (new container)                  │
│  Go binary, port 8012 (internal only)               │
│                                                     │
│  ┌─────────────────────────────────────────────┐   │
│  │  MCP JSON-RPC server                        │   │
│  │  tools/list  tools/call                     │   │
│  │  Stateless — no in-process session state    │   │
│  └────────────────────┬────────────────────────┘   │
│                       │ HTTP (service-to-service)   │
└───────────────────────┼─────────────────────────────┘
                        │
          ┌─────────────▼────────────────┐
          │   Redis  (them-redis DB 0)   │
          │   Canvas session cache       │
          │   Key: them:canvas:sess:{id} │
          │   Patch pub/sub channel      │
          └─────────────┬────────────────┘
                        │
          ┌─────────────▼────────────────┐
          │   them-go-bridge / 8002      │
          │   Internal Canvas API        │
          │   /internal/canvas/*         │
          │   (not exposed via Traefik)  │
          └─────────────┬────────────────┘
                        │ pgx
          ┌─────────────▼────────────────┐
          │   them-postgres              │
          │   them.agent_definitions     │
          └──────────────────────────────┘

Browser (Agent Builder) subscribes to SSE:
  GET /api/v1/canvas-assistant/{session_id}/events
  → them-go-bridge → Redis sub → SSE stream
  → React Flow receives patch events, applies incrementally
```

**Two independent deployables:** `them-canvas-mcp` is the MCP protocol adapter. `them-go-bridge` owns all business logic (mutations, validation, compilation). The MCP server is stateless across replicas.

---

## 3. Answering the Design Questions

### 3.1 Are existing endpoints sufficient?

**Mostly yes for read-only tools. Three gaps for mutations and debug:**

| Gap | Description | Effort |
|---|---|---|
| No `revision` / optimistic lock on drafts | `PUT /agent-definitions/{id}` is a full replace with no concurrency guard | 1 migration + 1 service change |
| No incremental mutation API | The MCP server needs to add/update/remove one node at a time; no patch endpoint exists | New internal endpoint or MCP-side JSON patch |
| No `test-run` endpoint | Per-step trace is designed in `AI_PLATFORM_FOUNDATION.md` Phase 1.5 but not implemented | ~2 days |

### 3.2 Do we need a dedicated internal Canvas API?

**Yes — a small typed internal mutation surface inside `them-go-bridge`**, mounted at `/internal/canvas/` and unreachable externally (no Traefik label). This keeps all business logic inside the authoritative service while making the MCP server a pure protocol adapter.

Endpoints (`/internal/canvas/`):

```
POST   /sessions                     create canvas session in Redis
GET    /sessions/{id}                read session + current definition snapshot
DELETE /sessions/{id}                end session

POST   /sessions/{id}/nodes          add_node
PATCH  /sessions/{id}/nodes/{step}   update_node
DELETE /sessions/{id}/nodes/{step}   remove_node (with edge cleanup)
POST   /sessions/{id}/edges          connect_nodes
DELETE /sessions/{id}/edges          disconnect_nodes

POST   /sessions/{id}/validate       validate current definition (no DB write)
POST   /sessions/{id}/save           write definition to agent_definitions, bump revision
POST   /sessions/{id}/test-run       run pipeline with seed input, return per-step trace
```

Each mutation:
1. Reads the current definition from the session Redis key (full JSONB blob cached there, not re-fetched from DB on every call)
2. Applies the structural change in Go
3. Optionally calls `agentgen.ValidateDefinitionJSON()` inline
4. Writes the new definition back to the session key
5. Publishes an SSE patch event to `them:canvas:patch:{session_id}`
6. Returns the new revision

### 3.3 Single typed PATCH API with optimistic locking?

**Yes for session-based edits, with a simpler mechanism than full OCC:**

The session Redis key (`them:canvas:sess:{id}`) is the live editing buffer. It holds the current definition blob and a monotonic `revision` counter. The MCP server passes the expected revision with every mutation. The Go handler atomically checks and increments using a Lua script (`WATCH`-free because it is a single key per session). DB writes (`save_draft`) also carry a revision field on the `agent_definitions` row.

This avoids full OCC complexity while preventing interleaved concurrent MCP calls from corrupting the graph.

### 3.4 Live UI updates: SSE, WebSocket, or other?

**SSE (Server-Sent Events), same pattern as the existing dashboard WS.**

Flow:
1. Browser opens `GET /api/v1/canvas-assistant/{session_id}/events` — SSE connection to `them-go-bridge`
2. `them-go-bridge` subscribes to Redis pub/sub channel `them:canvas:patch:{session_id}`
3. Each MCP mutation publishes a patch event (add/update/remove node or edge)
4. The SSE handler streams it to the browser as a JSON event
5. The React Flow canvas receives the event and applies it via `setNodes`/`setEdges` (no reload)

Why SSE over WebSocket: canvas patch events are server→browser unidirectional. SSE is simpler, cheaper, and consistent with `them:dash:*` patterns already in the codebase.

### 3.5 MCP Tool List

All tools require a `canvas_session_id` argument (acquired from `create_session`). Read-only tools also work without a session by accepting a `definition_id` directly.

#### Discovery (read-only, no mutation)

| Tool | Delegates to | Notes |
|---|---|---|
| `get_node_types` | `agentgen.AllNodeTypeInfos()` | Returns full type catalog with config schemas, examples, port defs |
| `get_node_schema` | `agentgen.NodeTypeForType(type)` | Single node type; call before `add_node` |
| `get_transform_functions` | `transform.Catalog()` | Full function catalog for Transform node config |
| `inspect_canvas` | Internal `/sessions/{id}` | Returns step list, edges, port bindings, current issues |

#### Mutations (require session)

| Tool | Delegates to | Notes |
|---|---|---|
| `add_node` | `POST /internal/canvas/sessions/{id}/nodes` | Creates step with generated `step_id`, returns new node |
| `update_node` | `PATCH /internal/canvas/sessions/{id}/nodes/{step}` | Partial config update only |
| `connect_nodes` | `POST /internal/canvas/sessions/{id}/edges` | Control or data edge; validates port compatibility |
| `disconnect_nodes` | `DELETE /internal/canvas/sessions/{id}/edges` | Edge removal |
| `remove_node` | `DELETE /internal/canvas/sessions/{id}/nodes/{step}` | Removes all attached edges |
| `validate_canvas` | `POST /internal/canvas/sessions/{id}/validate` | Returns `[]Issue{Severity, Code, NodeID, Field, Message}` |
| `save_draft` | `POST /internal/canvas/sessions/{id}/save` | Writes to `agent_definitions` table |

#### Debug

| Tool | Delegates to | Notes |
|---|---|---|
| `test_pipeline` | `POST /internal/canvas/sessions/{id}/test-run` | Accepts `{"input": "..."}`, returns per-step trace (depends on Foundation Phase 1.5) |

#### Session management (used by the host app, not usually exposed to the LLM)

| Tool | Delegates to | Notes |
|---|---|---|
| `create_session` | `POST /internal/canvas/sessions` | Returns `session_id`, connects to a definition_id or starts blank |
| `end_session` | `DELETE /internal/canvas/sessions/{id}` | Cleans up Redis state |

### 3.6 Security

#### External MCP auth (Canvas MCP Server → caller)

The MCP server accepts:
- **User JWT** (same RS256 token issued by `them-auth-go`) — for browser-side assistant panel calls
- **Personal Access Token** (opaque bearer, same resolution as `them-go-bridge`) — for Claude Desktop and external clients

Token validation: the MCP server calls `GET http://them-go-bridge:8002/internal/auth/verify` — a new one-line internal endpoint that validates the token and returns `{user_id, tenant_id, roles}`. The MCP server never validates tokens locally; the Go bridge remains the authority.

#### Tenant isolation

Every MCP tool call flow:
1. Token → `{user_id, tenant_id}` via internal verify endpoint
2. `session_id` is keyed per user+tenant: `them:canvas:sess:{tenant_id}:{session_id}` — cross-tenant session access is structurally impossible
3. All internal Canvas API calls include `X-Tenant-ID` and `X-User-ID` headers (service-to-service)
4. The Go bridge validates these headers against the JWT claim on every request

#### Service-to-service auth

`them-canvas-mcp` → `them-go-bridge` internal calls use a shared HMAC secret derived from `secrets.local` (same `THE_M_INTERNAL_SECRET` env var used elsewhere). The bridge's internal route group requires this header.

#### RBAC

The MCP tools inherit the calling user's roles from the JWT:
- `admin` / `superadmin` — full read/write access to all canvas tools
- `viewer` — read-only (discover + inspect); mutations rejected with 403
- Publish via `save_draft` is always restricted to admin; the MCP server checks before calling

#### Audit logging

Every mutation tool call writes one row to `audit_logs`:
```
entity_type: canvas_mcp
entity_id:   definition_id
action:      add_node / connect_nodes / etc.
actor_id:    user_id from JWT
tenant_id:   tenant_id from JWT
metadata:    {session_id, tool, args_summary}
```
The MCP server emits the audit row via a fire-and-forget POST to the internal audit endpoint after each successful mutation.

#### LLM prompt injection prevention

The LLM never sees raw user data in a position where it can override instructions. Node configs supplied via MCP tool calls are validated against the registered `NodeDef.ConfigFields` schema before being applied. The system prompt is constructed server-side from trusted registry data, not from user input.

### 3.7 Stateless replicas

The MCP server is **fully stateless**. All session state lives in Redis (`them:canvas:sess:*`). The MCP server holds no in-process state beyond config. Multiple replicas can handle requests for the same session without coordination. The `revision` field in the Redis session key ensures mutation ordering without sticky sessions.

### 3.8 `test_pipeline` / per-step trace

The test-run endpoint accepts `{"definition": <json>, "input": "user message string"}` and:
1. Runs the full pipeline using the existing `agentgen` executor (not a live orchestration session — a local dry-run)
2. Captures per-step `StepTrace{step_id, input_vars, output_vars, error, duration_ms}`
3. Returns `ExecutionResult{traces []StepTrace, final_output string, total_duration_ms int}`

Integration with the existing runtime: the executor already exists in `agentgen` (each node's `Execute` function). The test-run harness calls them sequentially, collecting traces. Steps with `Execute == nil` (stub nodes like `loop`, `parallel`) return a synthetic `{status: stub, error: "not_yet_implemented"}` trace entry rather than failing the whole run.

---

## 4. Missing Backend Work (minimum for MVP)

Listed in dependency order:

| # | Work item | Where | Effort |
|---|---|---|---|
| 1 | `revision INT` column on `agent_definitions` + optimistic lock in `UpdateDraft` | DB migration + `go/internal/admin/dal/` | 0.5 day |
| 2 | Internal Canvas API (`/internal/canvas/sessions/*`) — session CRUD, node/edge mutations, validate, save, test-run | `go/internal/canvas/` (new package) | 3–4 days |
| 3 | SSE push channel per session — Redis pub/sub `them:canvas:patch:{session_id}` → new SSE route | `go/internal/canvas/sse.go` | 1 day |
| 4 | Internal auth verify endpoint `GET /internal/auth/verify` | `go/internal/auth/` | 0.5 day |
| 5 | Canvas MCP Server binary — MCP JSON-RPC handler, tool dispatch, auth delegation | `go/cmd/canvas-mcp/` + `go/internal/canvasmcp/` | 3–4 days |
| 6 | `Dockerfile.canvas-mcp` + `docker-compose.yml` service entry | infra | 0.5 day |
| 7 | Foundation Phase 1.5: `test-run` endpoint (standalone, no blocker on MCP delivery) | `go/internal/canvas/` | 2 days |
| 8 | Frontend: SSE subscription + incremental `applyCanvasPatch` in React Flow | `frontend/src/app/admin/agents/builder/` | 3–4 days |

**Total MVP backend: ~8–10 days. Full Phase 1 (including frontend): ~12–15 days.**

---

## 5. Phased Implementation Plan

### Phase 0 — Foundation (already done)

- Node type registry with LLM-facing metadata ✓
- Schema endpoint ✓
- Validate endpoint ✓
- MCP client infrastructure (reference) ✓

### Phase 1a — Internal Canvas API (no MCP yet)

Goal: mutations work via REST; SSE pushes patches to browser.

1. DB migration: `revision` column on `agent_definitions`
2. `go/internal/canvas/` package:
   - `session.go` — Redis session CRUD (`them:canvas:sess:{tenant}:{id}`)
   - `mutations.go` — `AddNode`, `UpdateNode`, `RemoveNode`, `ConnectEdge`, `DisconnectEdge` (JSON patch on definition blob)
   - `validate.go` — thin wrapper over `agentgen.ValidateDefinitionJSON()`
   - `save.go` — write definition to DB with revision check
   - `sse.go` — subscribe Redis pub/sub, stream patch events
3. Register internal routes in `cmd/them/main.go` (no Traefik label)
4. Tests: `go test ./internal/canvas/...`

### Phase 1b — Canvas MCP Server

Goal: Claude Desktop can edit a canvas.

1. `go/internal/canvasmcp/` package:
   - `server.go` — MCP 2024-11-05 JSON-RPC handler (`initialize`, `tools/list`, `tools/call`)
   - `tools.go` — tool definitions (JSON Schema for each tool's `inputSchema`)
   - `handlers.go` — one function per tool; each calls Internal Canvas API or `agentgen` directly
   - `auth.go` — token verification via internal endpoint
2. `go/cmd/canvas-mcp/main.go` — binary entry point
3. `Dockerfile.canvas-mcp`
4. `docker-compose.yml` — `them-canvas-mcp` service, internal network only
5. Tests: `go test ./internal/canvasmcp/...`

### Phase 1c — Frontend SSE Integration

Goal: tool calls from Claude Desktop (or the future assistant panel) appear live on the open canvas.

1. `useCanvasSSE(sessionId)` hook — opens EventSource to `/api/v1/canvas-assistant/{id}/events`
2. `applyCanvasPatch(patch)` — translates patch events to `setNodes`/`setEdges` calls
3. Assistant panel UI (optional in Phase 1c — can use Claude Desktop directly)

### Phase 2 — Full Feature Set

- Streaming LLM responses in the assistant panel
- Undo stack (per-session undo stored in Redis session key)
- `test_pipeline` tool (after Phase 1.5 test-run endpoint)
- Publish flow via MCP (`publish_canvas` tool calling existing publish endpoint)
- Rate limiting on MCP tool calls per tenant

---

## 6. Redis Keys (new)

| Key pattern | TTL | Purpose |
|---|---|---|
| `them:canvas:sess:{tenant_id}:{session_id}` | 30 min (sliding) | Canvas editing session — holds definition blob + revision + undo stack |
| `them:canvas:patch:{session_id}` | N/A (pub/sub) | Per-session patch event channel; SSE handler subscribes |

Document both in `docs/REDIS.md` when implementing.

---

## 7. Network / Traefik Boundary

```
External (port 8088 via Traefik):
  /api/v1/canvas-assistant/*     → them-go-bridge (SSE events endpoint, session create)
  /mcp/*  (if exposing externally) → them-canvas-mcp  [optional; prefer API gateway in prod]

Internal only (not in Traefik):
  them-canvas-mcp:8012           → them-go-bridge:8002 /internal/canvas/*
  them-canvas-mcp:8012           → them-go-bridge:8002 /internal/auth/verify
```

`them-canvas-mcp` is on the `them-internal` Docker network only. It is never reachable from outside the Docker network without an explicit Traefik rule. For Claude Desktop access during development, either:
- Add a narrow Traefik rule for `/mcp` path → `them-canvas-mcp`
- Or access via SSH tunnel directly to port 8012

---

## 8. What the MCP Server Is NOT Responsible For

- Business logic (graph validation rules, node schema enforcement) — `them-go-bridge` / `agentgen`
- LLM provider management — `them-go-bridge`
- Agent publishing / runtime compilation — `them-go-bridge`
- Authentication authority — `them-auth-go`
- Storing definitions — `them-postgres` via `them-go-bridge`

The MCP server is a **protocol adapter**: it translates MCP JSON-RPC tool calls into authenticated HTTP calls to the Internal Canvas API and returns the results in MCP wire format.

---

## 9. Risks and Open Questions

| Risk | Mitigation |
|---|---|
| JSON patch on definition JSONB: field ordering and ID stability | Generate `step_id` as stable UUIDs on add; never sort steps during patch |
| Concurrent user (manual edit) + MCP edit on the same definition | Optimistic lock (`revision`) on DB write; Canvas API write wins, MCP gets 409 and retries with latest |
| Claude Desktop not supporting SSE-based canvas preview | Not a blocker; Claude Desktop gets tool results; only the browser needs SSE |
| Stub nodes (`loop`, `parallel`) returning non-actionable errors | `test_pipeline` returns `{status: stub}` trace; clearly documented in tool description |
| MCP session orphan (browser closed without `end_session`) | 30-min TTL on session key; no cleanup daemon needed |
