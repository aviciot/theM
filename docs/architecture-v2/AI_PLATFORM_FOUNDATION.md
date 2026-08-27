# AI Platform Foundation — Making the-M Self-Describing

**Status:** Design / Pre-implementation  
**Scope:** Platform infrastructure only — no AI features, no user-facing assistant  
**Enables later:** AI Agent Builder, AI Run Debugger  
**Last updated:** 2026-08-27

---

## Executive Summary

The-M can orchestrate complex multi-agent workflows, but an LLM trying to work with the platform has no way to discover its own structure. Node types live as Python constants. Validation errors are raw strings. A failing run produces a single error field with no causal chain. The platform is opaque to machines.

This document designs the **eight infrastructure components** needed to make the-M self-describing. The goal is not to build an AI assistant — it is to ensure that when one is built, it reads the platform's actual authoritative state rather than stale documentation or hallucinated schemas.

Every component described here derives its data from the same definitions used by the compiler and runtime. No separate AI-specific schemas are introduced that could drift.

**What this unlocks:**

- An AI Agent Builder that can list available resources, construct a valid graph, submit it, and self-correct from structured validation errors — without any pre-loaded knowledge of the platform.
- An AI Run Debugger that can explain why a run failed in structured terms and propose concrete configuration changes to fix it.

**Confidence summary:**

- High confidence: Components 1 (node type registry), 2 (graph summary), 3 (tool manifest), 4 (structured validation). These are pure reads or minor refactors of existing logic.
- Medium confidence: Component 5 (run failure analysis), Component 6 (graph versioning). The failure classifier will handle ~70% of real cases; the long tail is difficult. Graph versioning needs careful benchmarking of snapshot size before committing to JSONB storage.
- Lower confidence: Component 8 (per-EP agent card). The A2A spec does not explicitly endorse per-skill sub-cards at sub-path URLs — interoperability with third-party A2A clients is uncertain.

---

## Design Principles

**1. Single source of truth — always.**  
Node type definitions are read from `app/services/app_compiler.py` constants and `frontend/src/app/admin/applications/constants.ts` rules. They are never re-declared in a separate AI metadata store. If the compiler changes, the meta endpoint changes automatically.

**2. No drift by construction.**  
Every new endpoint in this design reads directly from the same DB columns, Go structs, or compiler constants that govern runtime behavior. If an approach requires maintaining a parallel representation, it is rejected.

**3. Read-first, write-never.**  
This foundation adds only GET endpoints and one structured validation response format. It does not add tables, columns, or new write paths — except Component 6 (graph versioning), which is explicitly deferred to Phase 3 precisely because it requires a DB migration.

**4. Structured errors are a first-class output.**  
Every validation path must return `{rule, path, message, suggestion, severity}` tuples. Raw strings are not acceptable outputs for machine consumers.

**5. Auth parity.**  
New endpoints follow the same auth rules as their nearest existing neighbor. Metadata endpoints under `/api/v1/meta/` are admin-JWT-gated. Run analysis endpoints follow the same rules as `/api/v1/runs/{id}`.

---

## Component 1: Node Type Schema Registry

### Problem

The four node types (`entryPoint`, `orchestrator`, `agent`, `middleware`), their required fields, valid edge targets, and validation rules exist only in:

- Python: `_VALID_NODE_TYPES` set and `validate_graph()` in `app/services/app_compiler.py`
- TypeScript: `CANVAS_RULES` constant in `frontend/src/app/admin/applications/constants.ts`

No endpoint exposes this as machine-readable data. An LLM building a graph must hallucinate the schema.

### Design

**`GET /api/v1/meta/node-types`**

Auth: Admin JWT  
Implementation location: Go, new handler in `go/internal/admin/` — reads from Go constants that mirror the Python compiler definitions. Constants are the authoritative source; the handler is a thin serializer.

**Response schema:**

```json
{
  "node_types": [
    {
      "type": "entryPoint",
      "label": "Entry Point",
      "description": "Protocol gateway. Every application must have at least one. Connects to exactly one orchestrator.",
      "fields": [
        {
          "name": "slug",
          "type": "string",
          "required": true,
          "constraints": { "pattern": "^[a-z0-9_-]{1,64}$", "unique_within_graph": true },
          "description": "URL-safe identifier for the entry point. Used in WebSocket and SSE paths."
        },
        {
          "name": "epType",
          "type": "enum",
          "required": true,
          "enum_values": ["websocket", "sse", "webrtc", "a2a", "voice"],
          "description": "Protocol this entry point accepts."
        },
        {
          "name": "accessMode",
          "type": "enum",
          "required": true,
          "enum_values": ["token", "public"],
          "description": "Authentication requirement for callers."
        },
        {
          "name": "convTokenLimit",
          "type": "integer",
          "required": false,
          "default": null,
          "constraints": { "minimum": 1 },
          "description": "Maximum conversation token budget. Null means unlimited."
        },
        {
          "name": "maxConcurrentSessions",
          "type": "integer",
          "required": false,
          "default": null,
          "constraints": { "minimum": 1 },
          "description": "Session cap for this entry point. Null means no cap."
        },
        {
          "name": "queueTimeout",
          "type": "integer",
          "required": false,
          "default": null,
          "description": "Seconds a caller waits when at capacity before being rejected. Null disables queuing."
        },
        {
          "name": "queueMessage",
          "type": "string",
          "required": false,
          "default": null,
          "description": "Message sent to callers while they wait in the queue."
        }
      ],
      "valid_source_for_edge_targets": [],
      "valid_target_for_edge_sources": [],
      "valid_edge_targets": ["orchestrator"],
      "validation_rules": [
        { "rule": "AT_LEAST_ONE_EP", "severity": "error", "message": "The application must have at least one entry point." },
        { "rule": "EP_SLUG_NONEMPTY", "severity": "error", "message": "Entry point slug must not be empty." },
        { "rule": "EP_SLUG_FORMAT", "severity": "error", "message": "Slug must match ^[a-z0-9_-]{1,64}$." },
        { "rule": "EP_SLUG_UNIQUE", "severity": "error", "message": "Slug must be unique within the graph and across other applications." },
        { "rule": "EP_HAS_ORCH", "severity": "error", "message": "Each entry point must have exactly one outgoing edge to an orchestrator." },
        { "rule": "VOICE_EP_NEEDS_STT_TTS", "severity": "warning", "message": "Voice entry points should connect to an orchestrator with STT and TTS configured." }
      ]
    },
    {
      "type": "orchestrator",
      "label": "Orchestrator",
      "description": "LLM planning loop. Receives goals from an entry point, calls agents and MCP tools, and returns results.",
      "fields": [
        {
          "name": "displayName",
          "type": "string",
          "required": true,
          "description": "Human-readable name used in run logs and the agent card."
        },
        {
          "name": "llmProvider",
          "type": "string",
          "required": true,
          "description": "Slug of an enabled LLM provider from them.llm_providers."
        },
        {
          "name": "llmModel",
          "type": "string",
          "required": true,
          "description": "Model identifier for the chosen provider."
        },
        {
          "name": "systemPrompt",
          "type": "string",
          "required": false,
          "default": null,
          "description": "Instruction prepended to every conversation. Never exposed externally."
        },
        {
          "name": "kind",
          "type": "enum",
          "required": false,
          "default": "standard",
          "enum_values": ["standard", "router", "voice"],
          "description": "Orchestrator variant. 'router' delegates without executing. 'voice' enables STT/TTS."
        },
        {
          "name": "maxIterations",
          "type": "integer",
          "required": false,
          "default": 10,
          "constraints": { "minimum": 1, "maximum": 100 }
        },
        {
          "name": "maxParallelTools",
          "type": "integer",
          "required": false,
          "default": 1,
          "constraints": { "minimum": 1 }
        },
        {
          "name": "historyWindow",
          "type": "integer",
          "required": false,
          "default": 20,
          "description": "Number of prior conversation turns kept in context."
        },
        {
          "name": "delegatable",
          "type": "boolean",
          "required": false,
          "default": false,
          "description": "If true, this orchestrator can be invoked as a sub-orchestrator from another orchestrator."
        },
        {
          "name": "budgetTokens",
          "type": "integer",
          "required": false,
          "default": null,
          "description": "Token budget for the entire run. Null means unlimited."
        },
        {
          "name": "mcpServers",
          "type": "array",
          "required": false,
          "default": [],
          "description": "List of MCP server slugs (and optional tool allowlists) this orchestrator may call."
        },
        {
          "name": "transcriptionProvider",
          "type": "string",
          "required": false,
          "description": "Required when kind=voice. STT provider slug."
        },
        {
          "name": "ttsProvider",
          "type": "string",
          "required": false,
          "description": "Required when kind=voice. TTS provider slug."
        },
        {
          "name": "ttsVoice",
          "type": "string",
          "required": false,
          "description": "Voice identifier for TTS."
        }
      ],
      "valid_edge_targets": ["agent", "middleware", "orchestrator"],
      "validation_rules": [
        { "rule": "ORCH_HAS_AGENT", "severity": "warning", "message": "Orchestrator has no connected agents or MCP tools. It can only use its base LLM." },
        { "rule": "VOICE_EP_NEEDS_STT_TTS", "severity": "warning", "message": "Orchestrator connected to a voice entry point should have transcriptionProvider and ttsProvider set." }
      ],
      "notes": [
        "allowed_agent_ids is always derived from graph edges — never set directly in node data.",
        "An orchestrator connected to another orchestrator (Orch→Orch edge) is treated as a delegatable sub-orchestrator. The target must have delegatable=true."
      ]
    },
    {
      "type": "agent",
      "label": "Agent",
      "description": "External A2A agent invocation node. All configuration lives in the agents table — this node is a reference.",
      "fields": [
        {
          "name": "agentId",
          "type": "uuid",
          "required": true,
          "description": "FK to them.agents. The agent must exist and be enabled."
        }
      ],
      "valid_edge_targets": [],
      "valid_edge_sources": ["orchestrator", "middleware"],
      "validation_rules": [
        { "rule": "AGENT_EXISTS", "severity": "error", "message": "Referenced agent UUID must exist in them.agents." },
        { "rule": "AGENT_ENABLED", "severity": "error", "message": "Referenced agent must be enabled." }
      ]
    },
    {
      "type": "middleware",
      "label": "Middleware",
      "description": "Guard or cache layer inserted between orchestrator and agent. Middleware chains are supported.",
      "fields": [
        {
          "name": "defId",
          "type": "uuid",
          "required": true,
          "description": "FK to them.middleware_defs."
        },
        {
          "name": "configOverride",
          "type": "object",
          "required": false,
          "default": {},
          "description": "Per-instance override of the middleware definition's default configuration."
        }
      ],
      "valid_edge_targets": ["agent", "middleware"],
      "valid_edge_sources": ["orchestrator", "middleware"],
      "middleware_kinds": [
        { "kind": "guard", "description": "Blocks or allows agent calls based on policy." },
        { "kind": "cache", "description": "Returns cached agent responses for identical inputs." }
      ]
    }
  ],
  "edge_rules": [
    { "source": "entryPoint", "target": "orchestrator", "required": true, "cardinality": "one-to-one" },
    { "source": "orchestrator", "target": "orchestrator", "required": false, "description": "Delegation — target must have delegatable=true." },
    { "source": "orchestrator", "target": "agent", "required": false },
    { "source": "orchestrator", "target": "middleware", "required": false },
    { "source": "middleware", "target": "agent", "required": false },
    { "source": "middleware", "target": "middleware", "required": false, "description": "Chains of middleware are allowed." }
  ],
  "tool_naming": {
    "agent": "agent__{slug}",
    "mcp_tool": "mcp__{server_slug}__{tool_name}",
    "sub_orchestrator": "orch__{orchestrator_name}"
  }
}
```

### Implementation note

The Go handler at `go/internal/admin/meta.go` (new file) returns this structure as a compile-time constant. It does not query the DB. When the Python compiler's `_VALID_NODE_TYPES` or `CANVAS_RULES` change, the corresponding Go constant must be updated in the same commit — enforced by a linter check that diffs the two files' enum lists.

---

## Component 2: Application Graph Summary

### Problem

`GET /api/v1/admin/applications/{id}/export` exists but is designed for human import/restore, not LLM consumption. It returns a ReactFlow node/edge graph that an LLM must interpret structurally. There is no flat, semantic summary of "what this application does and what it can call."

### Design

**`GET /api/v1/admin/applications/{id}/ai-summary`**

Auth: Admin JWT  
ETag: SHA-256 of `application.updated_at.UnixNano()` as hex — lets callers cache and detect staleness.

**Response:**

```json
{
  "application_id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "Customer Support Bot",
  "enabled": true,
  "graph_etag": "a3f4b2c1d0e9f8a7",
  "entry_points": [
    {
      "slug": "support-ws",
      "type": "websocket",
      "access_mode": "token",
      "conversation_token_limit": 50000,
      "max_concurrent_sessions": 100,
      "orchestrator_name": "Support Orchestrator"
    }
  ],
  "orchestrators": [
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "name": "Support Orchestrator",
      "kind": "standard",
      "llm_provider": "anthropic",
      "llm_model": "claude-sonnet-5",
      "max_iterations": 15,
      "delegatable": false,
      "reachable_agents": [
        {
          "slug": "crm-lookup",
          "tool_name": "agent__crm-lookup",
          "display_name": "CRM Lookup",
          "description": "Retrieves customer records by email or account ID.",
          "input_schema": {
            "type": "object",
            "properties": {
              "query": { "type": "string", "description": "Email address or account ID." }
            },
            "required": ["query"]
          },
          "output_schema": null,
          "skills": [
            { "id": "lookup", "name": "Customer Lookup", "description": "Find a customer record." }
          ]
        }
      ],
      "reachable_mcp_tools": [
        {
          "tool_name": "mcp__zendesk__create_ticket",
          "server_slug": "zendesk",
          "description": "Create a new support ticket in Zendesk.",
          "input_schema": {
            "type": "object",
            "properties": {
              "subject": { "type": "string" },
              "body": { "type": "string" },
              "priority": { "type": "string", "enum": ["low", "normal", "high", "urgent"] }
            },
            "required": ["subject", "body"]
          }
        }
      ],
      "sub_orchestrators": []
    }
  ],
  "canvas_warnings": [
    {
      "rule": "ORCH_HAS_AGENT",
      "severity": "warning",
      "node_id": "orch-2",
      "message": "Orchestrator 'Router' has no connected agents."
    }
  ]
}
```

### Implementation

Reads from: `them.entry_points` → `them.app_orchestrators` → `them.agents` (via `allowed_agent_ids`) → `them.mcp_servers.tools_manifest` (via `app_orchestrators.mcp_servers` JSONB join).

All joins are in one SQL query plus one Redis lookup for MCP manifests. No new DB columns required.

---

## Component 3: Unified Tool Manifest per Orchestrator

### Problem

An LLM cannot ask "what tools does orchestrator X have access to?" in a single call. Answering requires joining `app_orchestrators.allowed_agent_ids` → `agents` and `app_orchestrators.mcp_servers` → `mcp_servers.tools_manifest` — a multi-table join that no current endpoint performs.

### Design

**`GET /api/v1/admin/orchestrators/{id}/tools`**

Auth: Admin JWT

**Response:**

```json
{
  "orchestrator_id": "660e8400-e29b-41d4-a716-446655440001",
  "orchestrator_name": "Support Orchestrator",
  "agents": [
    {
      "slug": "crm-lookup",
      "tool_name": "agent__crm-lookup",
      "display_name": "CRM Lookup",
      "description": "Retrieves customer records.",
      "input_schema": { "type": "object", "properties": { "query": { "type": "string" } }, "required": ["query"] },
      "output_schema": null,
      "skills": [{ "id": "lookup", "name": "Customer Lookup", "tags": ["crm", "read"] }],
      "capabilities": { "streaming": false, "push_notifications": false },
      "enabled": true
    }
  ],
  "mcp_tools": [
    {
      "tool_name": "mcp__zendesk__create_ticket",
      "server_slug": "zendesk",
      "server_name": "Zendesk",
      "description": "Create a new support ticket.",
      "input_schema": {
        "type": "object",
        "properties": {
          "subject": { "type": "string" },
          "body": { "type": "string" }
        },
        "required": ["subject", "body"]
      }
    }
  ],
  "sub_orchestrators": [
    {
      "slug": "billing-orch",
      "tool_name": "orch__billing-orch",
      "display_name": "Billing Orchestrator",
      "description": "Handles billing inquiries and refunds."
    }
  ]
}
```

### Implementation

- `agents`: `SELECT * FROM them.agents WHERE id = ANY($1::uuid[])` where `$1` is `app_orchestrators.allowed_agent_ids`.
- `mcp_tools`: parse `app_orchestrators.mcp_servers` JSONB → for each entry, read `them.mcp_servers.tools_manifest` where `slug = entry.slug` and `application_id = orchestrator.application_id`. Flatten all tool entries. Apply per-orchestrator tool allowlist if present in the JSONB.
- `sub_orchestrators`: `SELECT * FROM them.app_orchestrators WHERE application_id = $1 AND delegatable = true AND id IN (SELECT target_orch_id FROM them.orch_delegations WHERE source_orch_id = $2)`.

No new DB columns required. All data exists; this endpoint assembles it.

---

## Component 4: Structured Validation Responses

### Problem

`compile_graph()` and `validate_graph()` raise `HTTPException(status_code=422, detail="<string>")`. An LLM receiving this cannot determine which rule fired, which node or field is at fault, or what to change. It can only see a human-readable sentence.

### Design

**`ValidationResult` schema** — replaces raw 422 strings everywhere validation occurs:

```json
{
  "valid": false,
  "errors": [
    {
      "rule": "EP_SLUG_FORMAT",
      "severity": "error",
      "path": "nodes[0].data.slug",
      "message": "Slug 'My Entry Point' contains spaces and uppercase letters.",
      "suggestion": "Use only lowercase letters, digits, hyphens, and underscores. Example: 'my-entry-point'.",
      "current_value": "My Entry Point"
    },
    {
      "rule": "EP_HAS_ORCH",
      "severity": "error",
      "path": "nodes[2]",
      "message": "Entry point 'support-ws' has no outgoing edge to an orchestrator.",
      "suggestion": "Add an edge from node 'ep-support-ws' to an orchestrator node.",
      "current_value": null
    }
  ],
  "warnings": [
    {
      "rule": "ORCH_HAS_AGENT",
      "severity": "warning",
      "path": "nodes[1]",
      "message": "Orchestrator 'Router' has no connected agents or MCP tools.",
      "suggestion": "Connect at least one agent or MCP server, or configure MCP tools on the orchestrator.",
      "current_value": null
    }
  ]
}
```

**`ValidationError` object fields:**

| Field | Type | Description |
|---|---|---|
| `rule` | string | Machine-readable rule ID from `CANVAS_RULES` or compiler |
| `severity` | `"error"` or `"warning"` | Errors block save; warnings allow save |
| `path` | string | JSONPath into the submitted `{nodes, edges}` graph |
| `message` | string | Human-readable description of what is wrong |
| `suggestion` | string | Concrete corrective action |
| `current_value` | any | The value that triggered the error, if applicable |

**Where applied:**

1. `POST /api/v1/admin/applications` and `PATCH /api/v1/admin/applications/{id}` — currently returns 422 with string; replace with `ValidationResult` body (HTTP 422 status preserved).
2. `POST /api/v1/admin/applications/{id}/validate` — new endpoint, dry-run only, always returns `ValidationResult` (200 even when `valid=false`).
3. `POST /api/v1/admin/agent-definitions/{id}/validate` — same pattern.

**Breaking change notice:** The 422 body shape changes from `{"detail": "string"}` to `{"valid": false, "errors": [...], "warnings": [...]}`. Any existing client parsing the detail string must be updated. The frontend currently parses compile errors as `error.response.data.detail` — this must be updated before the Python side is changed. Coordinate Python and frontend in the same PR.

---

## Component 5: Run Failure Analysis Endpoint

### Problem

When a run fails, `them.runs.error` contains a raw exception string. `them.run_steps` records individual agent call failures. There is no endpoint that aggregates this into a causal chain, classifies the failure type, or suggests a corrective action.

### Design

**`GET /api/v1/runs/{id}/analysis`**

Auth: same as `GET /api/v1/runs/{id}`

**Response:**

```json
{
  "run_id": "770e8400-e29b-41d4-a716-446655440000",
  "run_summary": {
    "goal": "Look up account and create a refund ticket",
    "status": "failed",
    "iterations_completed": 3,
    "total_tokens_used": 12450,
    "duration_seconds": 18.4
  },
  "failure_point": {
    "type": "agent_step",
    "step_id": "880e8400-e29b-41d4-a716-446655440002",
    "iteration": 3,
    "agent_slug": "crm-lookup",
    "error": "Request timeout after 30s"
  },
  "error_classification": {
    "code": "agent_timeout",
    "description": "An agent did not respond within its configured timeout.",
    "is_transient": true
  },
  "contributing_factors": [
    {
      "factor": "low_timeout",
      "description": "Agent crm-lookup has timeout_seconds=30. The upstream CRM API has a documented P95 latency of 25s.",
      "evidence": "run_step latency_ms=30012"
    },
    {
      "factor": "no_retry",
      "description": "Agent crm-lookup has max_retries=0. A single timeout caused immediate failure.",
      "evidence": "them.agents.max_retries=0"
    }
  ],
  "suggested_fixes": [
    {
      "target_type": "agent",
      "target_id": "990e8400-e29b-41d4-a716-446655440003",
      "target_name": "crm-lookup",
      "field": "timeout_seconds",
      "current_value": 30,
      "suggested_value": 60,
      "reason": "Increase to exceed P95 upstream latency."
    },
    {
      "target_type": "agent",
      "target_id": "990e8400-e29b-41d4-a716-446655440003",
      "target_name": "crm-lookup",
      "field": "max_retries",
      "current_value": 0,
      "suggested_value": 2,
      "reason": "Allow retries for transient timeouts."
    }
  ],
  "graph_version": null
}
```

**`error_classification.code` enum:**

| Code | Trigger condition |
|---|---|
| `agent_timeout` | `run_steps.status = 'error'` and error contains "timeout" |
| `agent_unreachable` | error contains "connection refused" or "no route to host" |
| `llm_refusal` | error contains "content policy" or "safety" |
| `context_overflow` | error contains "context length" or `total_tokens_in > 0.95 * convTokenLimit` |
| `budget_exceeded` | `run.total_tokens_in + total_tokens_out >= budget_tokens` |
| `invalid_tool_call` | `run_steps.error` contains "invalid arguments" or "schema validation" |
| `max_iterations_reached` | `run.iterations >= orchestrator.max_iterations` at failure time |
| `agent_error` | Agent returned a non-transient error response |
| `orchestrator_error` | Error in the LLM planning loop itself |
| `unknown` | Does not match any above pattern |

The classifier is **rule-based, not LLM-generated.** It runs purely against the DB record. Pattern matching on error strings is deliberately kept simple; false classifications default to `unknown` rather than producing confident wrong answers.

**`GET /api/v1/runs/{id}/trace`**

Returns the full ordered event timeline for a run:

```json
{
  "run_id": "770e8400-e29b-41d4-a716-446655440000",
  "events": [
    { "seq": 1, "type": "run_start", "timestamp": "2026-08-27T10:00:00.000Z", "data": { "goal": "Look up account..." } },
    { "seq": 2, "type": "llm_call", "timestamp": "2026-08-27T10:00:01.200Z", "data": { "tokens_in": 1200, "tokens_out": 45 } },
    { "seq": 3, "type": "tool_call", "timestamp": "2026-08-27T10:00:01.800Z", "data": { "tool": "agent__crm-lookup", "input": { "query": "test@example.com" } } },
    { "seq": 4, "type": "tool_error", "timestamp": "2026-08-27T10:00:31.900Z", "data": { "tool": "agent__crm-lookup", "error": "Request timeout after 30s", "latency_ms": 30012 } },
    { "seq": 5, "type": "run_failed", "timestamp": "2026-08-27T10:00:32.100Z", "data": { "error": "Agent invocation failed: timeout" } }
  ]
}
```

Trace events are assembled from `them.run_steps` + `them.run_usage` + `them.tasks` ordered by timestamp. No new columns required for Phase 2; `graph_version` in the analysis response remains null until Component 6 is implemented.

---

## Component 6: Graph Version Tracking

### Problem

`them.runs.definition_id` exists in the schema but is never populated. An LLM analyzing a failed run cannot know which graph revision was active at the time — the current graph may have already been updated since the run.

### Design

**New table: `them.application_graph_versions`**

```sql
CREATE TABLE them.application_graph_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    graph_hash  CHAR(64) NOT NULL,          -- SHA-256 hex of canonical graph JSON
    graph_snapshot JSONB NOT NULL,           -- full {nodes, edges} at time of compile
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, graph_hash)
);

CREATE INDEX ON them.application_graph_versions(application_id, created_at DESC);
```

**Upsert on `compile_graph()` success:**

1. Serialize `{nodes, edges}` to canonical JSON (sorted keys, no canvas layout data).
2. Compute SHA-256.
3. `INSERT INTO them.application_graph_versions ... ON CONFLICT (application_id, graph_hash) DO NOTHING`.
4. Return the `id` of the matching version row.
5. Store this `id` in `them.runs.definition_id` when a new run is created.

**New endpoint — run-to-version diff:**

**`GET /api/v1/runs/{id}/graph-diff`**

Returns a structural diff between the graph that executed the run and the current live graph:

```json
{
  "run_id": "770e8400-e29b-41d4-a716-446655440000",
  "run_graph_hash": "a3f4b2c1d0e9f8a7b6c5d4e3f2a1b0c9",
  "current_graph_hash": "b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9",
  "graphs_match": false,
  "diff": {
    "added_nodes": [],
    "removed_nodes": [],
    "changed_nodes": [
      {
        "node_id": "orch-1",
        "node_type": "orchestrator",
        "changes": [
          { "field": "max_iterations", "run_value": 10, "current_value": 20 }
        ]
      }
    ],
    "added_edges": [],
    "removed_edges": []
  }
}
```

**Storage concern:** `graph_snapshot` JSONB can be large for complex applications. Before implementing, benchmark the P99 graph size across existing applications. If median graph JSON exceeds 50KB, consider storing only the hash and implementing a separate snapshot retrieval endpoint, with old snapshots pruned after 90 days.

This component is Phase 3 because it requires a DB migration and changes to the Python `compile_graph()` path and the run-creation path in both Python and Go.

---

## Component 7: Agent Schema Endpoint

### Problem

`GET /api/v1/admin/agents` returns a full list with all fields including internal ones (encrypted tokens, scan results). An LLM building tool call schemas needs a narrow, stable endpoint for a single agent's input/output contract.

### Design

**`GET /api/v1/admin/agents/{id}/schema`**

Auth: Admin JWT

**Response:**

```json
{
  "id": "990e8400-e29b-41d4-a716-446655440003",
  "slug": "crm-lookup",
  "tool_name": "agent__crm-lookup",
  "display_name": "CRM Lookup Agent",
  "description": "Retrieves customer records by email or account ID.",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": { "type": "string", "description": "Email address or account ID." }
    },
    "required": ["query"]
  },
  "output_schema": null,
  "skills": [
    {
      "id": "lookup",
      "name": "Customer Lookup",
      "description": "Find a customer record by identifier.",
      "tags": ["crm", "read"],
      "input_modes": ["text/plain", "application/json"],
      "output_modes": ["application/json"]
    }
  ],
  "capabilities": {
    "streaming": false,
    "push_notifications": false
  },
  "example_calls": null
}
```

`output_schema` is null for most agents as they do not declare one in their A2A card. `example_calls` is populated only if `agent_card.examples` is present (not common in current deployments).

Implementation: single `SELECT` on `them.agents WHERE id = $1`. No new columns required. This is the simplest component in the design.

---

## Component 8: Per-Entry-Point Capability Card

### Problem

The platform exposes one A2A agent card at `GET /.well-known/agent-card.json` listing all A2A-type entry points as skills. An LLM or A2A client calling a specific entry point slug cannot get an EP-specific capability description without discovering the full platform card and scanning skill IDs.

### Design

**`GET /.well-known/agent-card/{ep-slug}.json`**

No auth required (mirrors platform-wide card behavior).

Returns a card scoped to the named entry point only:

```json
{
  "name": "the-M — Customer Support",
  "description": "Multi-agent customer support orchestration.",
  "url": "https://platform.example.com/apps/support-ws",
  "version": "1.0.0",
  "capabilities": {
    "streaming": false,
    "pushNotifications": true
  },
  "defaultInputModes": ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "skills": [
    {
      "id": "support-ws",
      "name": "Customer Support",
      "description": "Handle customer inquiries, account lookups, and ticket creation.",
      "tags": [],
      "inputModes": ["text/plain"],
      "outputModes": ["text/plain"]
    }
  ],
  "securitySchemes": { "bearer": { "type": "http", "scheme": "bearer" } },
  "security": [{"bearer": []}]
}
```

Returns 404 if the slug does not exist or the entry point is not of type `a2a` or `websocket`.

**Confidence caveat:** The A2A 1.0 spec does not define sub-path agent cards. Some A2A clients discover agents exclusively at `/.well-known/agent-card.json`. This endpoint is additive — it does not replace the platform-wide card — but its adoption by third-party A2A clients is uncertain. Implement last in Phase 2.

---

## LLM Integration Pattern

This section describes how the components work together in an AI Agent Builder workflow. This is illustrative — no AI feature is being built in this task.

### Building a new application

```
1. GET /api/v1/meta/node-types
   → LLM learns: what node types exist, what fields are required, what edges are valid.

2. GET /api/v1/admin/agents (filter: enabled=true)
   GET /api/v1/admin/orchestrators (list available LLM providers + models)
   → LLM learns: which agents can be connected, what tools they provide.

3. LLM constructs {nodes, edges} graph JSON following node-type field schemas.

4. POST /api/v1/admin/applications/{id}/validate
   Body: {graph: {nodes, edges}}
   → Returns ValidationResult. If valid=false:

5. For each error in ValidationResult.errors:
   - Read rule, path, suggestion
   - Apply correction to the graph JSON
   → Loop back to step 4.

6. Once valid=true:
   PATCH /api/v1/admin/applications/{id}
   Body: {graph: {nodes, edges}}
   → Platform compiles and saves the application.
```

### Debugging a failed run

```
1. GET /api/v1/runs/{id}/analysis
   → Returns error_classification, contributing_factors, suggested_fixes.

2. For each fix in suggested_fixes:
   - target_type=agent: PATCH /api/v1/admin/agents/{target_id} {field: suggested_value}
   - target_type=orchestrator: PATCH /api/v1/admin/orchestrators/{target_id} {field: suggested_value}
   - target_type=graph: construct graph patch and re-validate via step 4 above.

3. (Phase 3) GET /api/v1/runs/{id}/graph-diff
   → Confirm whether the graph has already changed since the failing run.
   → If graphs_match=false, the issue may already be fixed.
```

---

## Implementation Roadmap

### Phase 1 — Self-Description Foundation (no DB migrations)

Target: all reads from existing data, one validation refactor.

| Component | Work | Risk |
|---|---|---|
| Component 1: Node Type Schema Registry | New Go handler at `go/internal/admin/meta.go`. Compile-time constants. | Low |
| Component 2: Application Graph Summary | New Go handler. SQL join + Redis lookup. | Low |
| Component 3: Unified Tool Manifest | New Go handler. SQL join. | Low |
| Component 4: Structured Validation Responses | Refactor Python `compile_graph` error returns. Update frontend error parsing. | Medium — breaking API change |

Phase 1 alone provides enough foundation for an AI Agent Builder prototype.

### Phase 2 — Run Analysis and Agent Schema

Target: read-only endpoints over existing run recording schema.

| Component | Work | Risk |
|---|---|---|
| Component 5: Run Failure Analysis | New Go handler. Rule-based classifier. | Medium — classifier coverage |
| Component 7: Agent Schema Endpoint | New Go handler. Single SQL select. | Low |
| Component 8: Per-EP Capability Card | New route in Go A2A handler. | Low-medium — spec conformance |

### Phase 3 — Graph Versioning (requires DB migration)

| Component | Work | Risk |
|---|---|---|
| Component 6: Graph Version Tracking | New migration, upsert in compile_graph, run creation change in Python and Go. | Medium — migration + two code paths |

---

## Confidence and Risks

### High confidence

**Node Type Schema Registry (Component 1).** The compiler's `_VALID_NODE_TYPES` and `CANVAS_RULES` are stable, well-defined, and have not changed significantly across the codebase history visible in git. Mirroring them as Go compile-time constants is a low-risk, high-value operation.

**Unified Tool Manifest (Component 3).** All required data exists in DB columns (`allowed_agent_ids`, `mcp_servers` JSONB, `tools_manifest` JSONB). The join is straightforward. The only risk is MCP manifest staleness — manifests are cached with a 5-minute TTL, so the response may lag behind a recently connected MCP server.

**Agent Schema Endpoint (Component 7).** A thin wrapper over existing columns. Output schema will be null for most agents because agents rarely declare it in their A2A cards. This is an honest limitation, not a design flaw.

### Medium confidence

**Structured Validation Responses (Component 4).** The validation logic is clear, but changing the 422 response shape is a breaking change for any caller parsing `detail` as a string. The frontend currently does this. The risk is mitigated by shipping the frontend change in the same PR as the Python change. However, if any external callers (scripts, other services) parse the old format, they will break silently.

**Run Failure Classifier (Component 5).** Pattern matching on error strings covers the common cases well. The current run data shows predictable error messages for timeouts, connection failures, and context overflows. However, LLM refusals and invalid tool call errors have more variable message formats across providers. Estimate: rule-based classifier handles ~70% of real failures correctly; remaining 30% classify as `unknown`. This is acceptable — `unknown` is an honest answer. The risk of wrong classification (e.g., classifying a non-transient error as `agent_timeout`) is higher. Mitigate by requiring both string match AND structural evidence (e.g., latency near timeout value).

**Graph Versioning (Component 6).** The SHA-256 canonical hash approach is correct in principle, but canonical JSON serialization requires careful key ordering to be stable. Use `encoding/json` with sorted map keys in Go, not standard `json.Marshal` (which does not guarantee map key order). The JSONB snapshot storage risk depends on graph sizes in production — benchmark before implementing.

### Lower confidence

**Per-Entry-Point Capability Card (Component 8).** The A2A 1.0 specification defines agent discovery at `/.well-known/agent-card.json` (singular, at root). Sub-path cards (`/.well-known/agent-card/ep-slug.json`) are not part of the spec. Third-party A2A clients that hardcode the root path will not discover EP-specific cards. This endpoint is useful for the platform's own AI builder (which can be told to use the right URL) but may not interoperate with external A2A clients without a spec extension. Implement last; document the non-standard nature explicitly.

**Output schemas.** Several components reference `output_schema` (Components 3, 7). In the current agent registry, `output_schema` is null for all agents because A2A agents typically do not declare output schemas in their cards. The foundation exposes this null honestly. Any AI feature built on top must handle null output schemas gracefully rather than assuming structured output.

### Cross-cutting risks

**Drift between Python compiler and Go meta handler.** Component 1 requires the Go constants to mirror the Python compiler's validation rules. There is no automated enforcement of this today. Risk: a developer updates `CANVAS_RULES` in TypeScript or `_VALID_NODE_TYPES` in Python without updating the Go meta handler. Mitigation: add a CI test that compares the Go handler output against a snapshot generated from the Python constants — fail if they diverge.

**Validation response versioning.** If the structured `ValidationResult` format needs to change after external callers have adopted it, a versioned endpoint (`/v2/...`) will be needed. Agree on the format before shipping Phase 1 to minimize future breakage.

**Auth on `/api/v1/meta/node-types`.** Node type schemas are not sensitive, but exposing them without auth could aid attackers in understanding the platform's internal model. Admin-JWT requirement is intentionally conservative. If a future AI builder feature requires the LLM to call this endpoint autonomously without admin credentials, a separate read-only scoped token type will be needed.
