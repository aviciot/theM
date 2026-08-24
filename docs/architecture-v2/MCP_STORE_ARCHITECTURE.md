# MCP Store Architecture — Discovery, Registry, Canvas Integration & Runtime

Status: design proposal  
Date: 2026-08-24  
Author: Chaya Friedman  
Scope: end-to-end MCP (Model Context Protocol) support in the-M — service layer, admin registry,
canvas visual builder integration, and runtime credential injection.

---

## 0. Executive Summary

We introduce a first-class **MCP Store** into the-M: a managed registry of MCP servers that the
platform discovers, health-checks, and makes available as draggable nodes in the canvas agent builder.
At runtime, MCP credentials are resolved from per-application configuration — the same pattern already
established for LLM provider keys — so the canvas definition stays secret-free and portable.

The feature has five independent layers, each deliverable in isolation:

| Layer | What it is | New Go package |
|---|---|---|
| **MCP Registry** | DB + CRUD admin API for MCP server records | `internal/admin/dal/mcp_servers.go` |
| **MCP Service** | Health-check, connectivity probe, capability discovery | `internal/mcp/` (new service) |
| **App Credentials** | Per-app MCP token/auth config (mirrors `llm_providers` pattern) | `db/040_mcp_app_credentials.sql` |
| **Canvas Nodes** | `mcp_server` and `mcp_tool` node types in the visual builder | frontend + node-type registry |
| **Runtime Binding** | Go runtime resolves MCP credentials and calls tools during orchestration | `internal/mcp/client.go` |

---

## 1. Terminology

| Term | Meaning in this doc |
|---|---|
| **MCP Server** | An HTTP or stdio MCP-protocol server exposing a set of tools |
| **MCP Tool** | A single callable function exposed by an MCP Server |
| **MCP Registry** | The `them.mcp_servers` table — one row per registered server |
| **App MCP Credential** | A `them.app_mcp_credentials` row: per-application auth token/secret for a server |
| **Canvas MCP Node** | A `mcp_server` node on the canvas — represents one server and its selected tools |

---

## 2. Data Model

### 2.1 `them.mcp_servers` — platform-level registry

```sql
CREATE TABLE IF NOT EXISTS them.mcp_servers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,                          -- tenant boundary
    name            TEXT        NOT NULL,                          -- display name
    slug            TEXT        NOT NULL,                          -- identifier used in canvas docs + runtime
    description     TEXT,
    transport       TEXT        NOT NULL CHECK (transport IN ('http', 'sse', 'stdio')),
    url             TEXT,                                          -- NULL for stdio
    auth_type       TEXT        NOT NULL CHECK (auth_type IN ('none', 'bearer', 'header', 'oauth2'))
                                DEFAULT 'none',
    -- health state (updated by MCP service, never by admin API writes)
    health_status   TEXT        NOT NULL DEFAULT 'unknown'
                                CHECK (health_status IN ('unknown', 'healthy', 'degraded', 'unreachable')),
    last_checked_at TIMESTAMPTZ,
    last_error      TEXT,
    -- discovered capabilities (written by MCP service after tool discovery probe)
    tools_manifest  JSONB       NOT NULL DEFAULT '[]',             -- [{name, description, inputSchema}]
    capabilities    JSONB       NOT NULL DEFAULT '{}',             -- server-level MCP capabilities block
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);
```

Key design decisions:
- `slug` is the stable identifier used in canvas docs and runtime resolution; `id` is internal.
- `tools_manifest` is written by the MCP Service (health loop), never by the admin API.
- Auth type declares what credential shape is expected. Actual secrets **never** live here.
- `transport` supports future stdio-tunnel support; initial implementation is `http` and `sse` only.

### 2.2 `them.app_mcp_credentials` — per-application credentials

```sql
CREATE TABLE IF NOT EXISTS them.app_mcp_credentials (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    mcp_server_id   UUID        NOT NULL REFERENCES them.mcp_servers(id) ON DELETE CASCADE,
    -- encrypted credential blob (Fernet, same scheme as llm_providers.api_key_encrypted)
    credential_encrypted TEXT,
    -- header name for 'header' auth type; ignored for 'bearer'
    auth_header_name     TEXT    DEFAULT 'Authorization',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, mcp_server_id)
);
```

This mirrors `them.llm_providers` for LLM keys:
- Admins enter credentials once per (application, MCP server) pair in the App settings UI.
- Credentials are Fernet-encrypted at rest, never logged, never in canvas JSON.
- If a server has `auth_type = 'none'`, no credential row is required.

### 2.3 Canvas doc schema — `mcp_server` node

Inside `them.agent_definitions.definition` (JSONB canvas doc), an MCP node looks like:

```jsonc
{
  "id": "node-abc123",
  "type": "mcp_server",
  "data": {
    "mcp_server_slug": "github-mcp",          // resolves via mcp_servers.slug at publish
    "selected_tools": ["create_issue", "list_prs"],  // [] = expose all tools
    "expose_all_tools": false,
    "label": "GitHub MCP"
  }
}
```

Rules:
- `mcp_server_slug` is the only reference; no ID or URL in the canvas doc.
- `selected_tools` empty array + `expose_all_tools: true` → LLM sees all tools from `tools_manifest`.
- `selected_tools` non-empty → LLM sees only those tools (subset filtering enforced at runtime).
- No credentials, no tokens, no URLs in the canvas doc. Fully exportable/importable.

### 2.4 Canvas doc — manual credential override (test-time)

For developers testing in the canvas before an application credential exists, the properties panel
provides a "Test credential" field that is:
- stored only in React state / `sessionStorage` — never persisted to the definition
- sent as `test_mcp_credentials: { [slug]: "token" }` in the validate/test API call body
- discarded by the backend after the session ends

This satisfies the requirement for "insert manually to test" without ever serialising secrets.

---

## 3. MCP Service (`internal/mcp/`)

This is a **new Go package** providing three capabilities: health checks, tool discovery, and
MCP tool execution at runtime.

### 3.1 Package layout

```
go/internal/mcp/
  client.go          — HTTP/SSE MCP client (initialize, tools/list, tools/call)
  registry.go        — in-process cache of mcp_servers rows (TTL + pub/sub invalidation)
  health.go          — background health-check loop
  discovery.go       — tools/list probe → writes tools_manifest back to DB
  resolver.go        — resolves (slug, application_id) → (url, authed http.Client)
  executor.go        — called by orchestrator to invoke an MCP tool during a run
  health_api.go      — exposes /internal/mcp/health (used by admin API)
```

### 3.2 Health-check loop (`health.go`)

A goroutine started at server boot performs periodic health checks:

```
interval: 60s per server (configurable via them.config key 'mcp_health_interval_seconds')
probe:     GET {url}/  (or MCP initialize handshake for strict mode)
on success → UPDATE them.mcp_servers SET health_status='healthy', last_checked_at=now(), last_error=NULL
on failure → UPDATE ... SET health_status='unreachable', last_error='<message>'
```

Health state transitions:

```
unknown  →  healthy    (first successful probe)
unknown  →  unreachable
healthy  →  degraded   (tools/list returns but tool count decreased >20% vs last manifest)
healthy  →  unreachable
degraded →  healthy    (tools/list stable again)
unreachable → healthy  (probe succeeds)
```

The health loop also triggers `discovery.go` on every successful probe cycle to keep `tools_manifest`
current.

### 3.3 Tool discovery (`discovery.go`)

On successful health probe, if manifest is stale (> 5 min since last discovery):
1. Issue MCP `initialize` request.
2. Issue MCP `tools/list` request.
3. Write result JSON to `them.mcp_servers.tools_manifest`.
4. Publish `them:mcp:manifest:changed:{server_slug}` to Redis so running sessions can invalidate
   their cached tool list.

### 3.4 Runtime execution (`executor.go`)

During orchestration, when the LLM emits a tool call whose name matches `mcp__{slug}__{tool_name}`:

1. Look up `mcp_servers` by slug (from in-process cache).
2. Call `resolver.go` with `(slug, application_id)` → returns `(url, authedClient)`.
3. Credential resolution precedence:
   ```
   1. app_mcp_credentials WHERE application_id=? AND mcp_server_id=?  → decrypt + inject header
   2. if auth_type='none' → no credential needed
   3. if required but missing → reject with a clear runtime error (never silently proceed)
   ```
4. Issue MCP `tools/call` with the LLM-provided arguments.
5. Return result as a tool response back into the LLM context.
6. Emit a `run_step` event so the run recorder captures it.

Tool call name format: `mcp__{slug}__{tool_name}` — consistent with the existing `agent__<slug>`
and `orch__<name>` naming scheme used by the orchestrator.

---

## 4. Admin API

### 4.1 New routes (all under `/api/v1/admin/`)

```
POST   /mcp-servers              Create MCP server record
GET    /mcp-servers              List all (tenant-scoped), includes health_status + tool count
GET    /mcp-servers/{id}         Get single (includes full tools_manifest)
PUT    /mcp-servers/{id}         Update (name, url, auth_type, enabled — NOT tools_manifest)
DELETE /mcp-servers/{id}         Delete

POST   /mcp-servers/{id}/probe   Trigger immediate health + discovery probe (returns result inline)
GET    /mcp-servers/{id}/tools   Returns tools_manifest (cached from last discovery)

-- Per-application credentials (nested under applications)
PUT    /applications/{app_id}/mcp-credentials/{server_id}    Set/update credential (encrypted)
DELETE /applications/{app_id}/mcp-credentials/{server_id}    Remove credential
GET    /applications/{app_id}/mcp-credentials                List (returns server slug + auth status, NEVER the decrypted value)
```

Go files added:
- `go/internal/admin/mcp_servers.go` — handler
- `go/internal/admin/dal/mcp_servers.go` — DAL
- `go/internal/admin/service/mcp_servers.go` — service
- `go/internal/admin/mcp_app_credentials.go` — credential handler (piggybacks applications handler)

### 4.2 Traefik routing

Two new Traefik router entries at priority 115 (admin writes) and 110 (admin reads):

```yaml
them-go-admin-mcp-write:
  rule: "PathPrefix(`/api/v1/admin/mcp-servers`) && Method(`POST`,`PUT`,`DELETE`)"
  priority: 115
  service: them-go-bridge

them-go-admin-mcp-read:
  rule: "PathPrefix(`/api/v1/admin/mcp-servers`)"
  priority: 110
  service: them-go-bridge
```

---

## 5. Canvas Visual Builder Integration

### 5.1 New node type: `mcp_server`

Registered in `go/internal/admin/node_types.go` (existing NodeTypeRegistry pattern):

```json
{
  "type": "mcp_server",
  "category": "integrations",
  "label": "MCP Server",
  "icon": "plug",
  "inputs": ["control_flow"],
  "outputs": ["control_flow"],
  "config_schema": {
    "mcp_server_slug": { "type": "string", "required": true },
    "selected_tools":  { "type": "array",  "items": { "type": "string" } },
    "expose_all_tools":{ "type": "boolean","default": false }
  }
}
```

### 5.2 Properties panel

When an `mcp_server` node is selected in the canvas:

```
┌─ MCP Server ──────────────────────────────────────────┐
│ Server  [dropdown: GitHub MCP ▼]  ● healthy            │
│                                                         │
│ Tools                                                   │
│  ◉ All tools (expose full manifest to LLM)             │
│  ○ Selected tools only                                  │
│   ☑ create_issue   ☑ list_prs   ☐ delete_issue         │
│                                                         │
│ ▼ Test credential (canvas session only, not saved)      │
│   [Bearer token ________________]                       │
│   [Test connection]                                     │
└────────────────────────────────────────────────────────┘
```

- Server dropdown is populated from `GET /api/v1/admin/mcp-servers` (only `enabled` + `healthy`/`degraded`).
- Health badge (`● healthy`, `⚠ degraded`, `✕ unreachable`) reflects live `health_status`.
- Tool checklist is populated from `tools_manifest` fetched via `GET /mcp-servers/{id}/tools`.
- Test credential field: React state only — never serialised; cleared on node deselect.
- "Test connection" triggers `POST /mcp-servers/{id}/probe` and shows inline result.

### 5.3 Runtime application settings UI

In the **Application settings → MCP Credentials** tab (new tab, modelled after the existing
LLM Providers tab):

```
┌─ MCP Credentials ─────────────────────────────────────┐
│ GitHub MCP          auth: bearer   [key set ●]  [Edit] │
│ Slack MCP           auth: header   [no key  ○]  [Set]  │
│ Internal Tools MCP  auth: none     [n/a]               │
└────────────────────────────────────────────────────────┘
```

- Shows all `mcp_servers` with `enabled=true`.
- Edit/Set opens a modal: single credential field (masked), confirm, saves to
  `PUT /applications/{app_id}/mcp-credentials/{server_id}`.
- Credential value never rendered after save — only shows `key set` / `no key` badge.

---

## 6. Publish-time Validation

The agent definition publish pipeline (`service/agent_definitions_publish.go`) gains an MCP
validation phase:

1. For every `mcp_server` node in the canvas doc, resolve `mcp_server_slug` → `mcp_servers` row.
2. If server does not exist or is disabled: **validation error** (block publish).
3. If server has `auth_type != 'none'` and no `app_mcp_credentials` row exists for
   `(application_id, server_id)`: **validation warning** (allow publish, warn on deploy).
4. Verify each `selected_tools` entry exists in the current `tools_manifest`:
   **validation warning** (tools may have been removed; allow publish, warn).
5. Add validation results to the existing `ValidationResult` struct alongside A2A/schema checks.

---

## 7. Redis Keys

New keys (added to `docs/REDIS.md`):

| Key pattern | Type | TTL | Purpose |
|---|---|---|---|
| `them:mcp:manifest:{slug}` | JSON string | 5 min | In-process manifest cache (backup; primary is DB) |
| `them:mcp:health:{slug}` | JSON string | 90 s | Last health probe result (for dashboard display) |
| `them:mcp:manifest:changed` | Pub/Sub channel | — | Invalidation signal after discovery probe |

---

## 8. Migration Files

```
db/040_mcp_registry.sql          — CREATE TABLE them.mcp_servers
db/041_mcp_app_credentials.sql   — CREATE TABLE them.app_mcp_credentials
```

No changes to existing tables. Both migrations are safe to apply to a running stack
(no locks on existing tables, no data movement).

---

## 9. Implementation Phases

Each phase is independently deployable and backward-compatible.

### Phase MCP-1 — Registry + Admin CRUD (1–2 sessions)

Deliverables:
- `db/040_mcp_registry.sql` + `db/041_mcp_app_credentials.sql`
- Go: DAL, service, handlers for `/api/v1/admin/mcp-servers`
- Go: app credentials endpoints under `/api/v1/admin/applications/{id}/mcp-credentials`
- Frontend: Settings → MCP Servers list page (list, create, delete; no canvas yet)
- Tests: DAL integration tests, service unit tests, handler tests (mirror agent_definitions pattern)

No canvas changes. No runtime changes. Provides the data foundation.

### Phase MCP-2 — MCP Service + Health Loop (1 session)

Deliverables:
- `go/internal/mcp/client.go`, `health.go`, `discovery.go`
- Background goroutine in `go/cmd/them/main.go`
- `POST /mcp-servers/{id}/probe` endpoint
- `GET /mcp-servers/{id}/tools` endpoint
- Health status badge visible in frontend MCP Servers list
- Tests: mock MCP server in tests, health transition tests

No canvas changes. No runtime changes. Provides health/discovery.

### Phase MCP-3 — Canvas Node + Properties Panel (1–2 sessions)

Deliverables:
- `mcp_server` node type registration in node_types.go
- Frontend canvas: draggable MCP node, properties panel, tool checklist
- Frontend: App Settings → MCP Credentials tab
- Publish-time validation phase for MCP nodes
- Tests: node type tests, publish validation tests

No runtime execution yet — a published agent with MCP nodes will fail gracefully with
"MCP execution not yet implemented".

### Phase MCP-4 — Runtime Execution (1–2 sessions)

Deliverables:
- `go/internal/mcp/resolver.go`, `executor.go`
- Integration with Go orchestrator: tool-call dispatch for `mcp__*__*` names
- Run recorder captures MCP tool calls as `run_steps`
- End-to-end test: canvas agent with GitHub MCP node calling `list_repos`
- Tests: executor unit tests with mock MCP client, integration test with real MCP echo server

---

## 10. Security Constraints

- Credentials stored as Fernet-encrypted TEXT (same scheme as `llm_providers.api_key_encrypted`).
- No credential value ever in canvas JSONB, logs, error messages, or API responses.
- `GET /mcp-servers` and `GET /applications/{id}/mcp-credentials` never return decrypted values —
  only `key_set: true/false`.
- MCP server `url` must be HTTPS in production (validated at create/update time).
- Tool call arguments from LLM are passed verbatim to MCP server — no server-side arg validation
  beyond JSON schema (MCP server is responsible for its own input validation).
- Each MCP tool call is tenant-scoped: resolver always checks `mcp_server.tenant_id == application.tenant_id`.
- Stdio transport is disabled by default; requires explicit feature flag (`mcp_allow_stdio=true` in
  `them.config`).

---

## 11. What We Are NOT Building (in scope of this design)

- **MCP Server hosting**: the-M does not host MCP servers; it connects to external ones.
- **OAuth2 flow UI**: OAuth2 `auth_type` is reserved for future; Phase 1–4 implement `none`,
  `bearer`, and `header` only.
- **Per-tool access control**: all selected tools are available to the LLM; no per-tool ACL.
- **Streaming MCP responses**: MCP `tools/call` is request-response; streaming tool results are
  out of scope.
- **MCP Server marketplace**: Phase 1 is bring-your-own-URL; a curated catalog of well-known
  servers (GitHub, Slack, Linear) is a future UX layer, not a backend concern.

---

## 12. Open Questions

| # | Question | Recommendation |
|---|---|---|
| Q1 | Should `mcp_servers` be global (platform-level) or per-tenant? | Per-tenant (`tenant_id` column) — consistent with all other the-M resources |
| Q2 | Should health check interval be per-server or global? | Global config key `mcp_health_interval_seconds` with per-server `override_interval_seconds` column added in a later migration |
| Q3 | Should test credentials persist across canvas sessions? | No — React state only, cleared on node deselect or page reload |
| Q4 | What happens when an MCP server is deleted that is referenced by a published definition? | Block delete if any published definition references the slug; soft-delete (disable) is the safe path |
| Q5 | How do we handle MCP servers that require per-user OAuth? | Out of scope for now; `auth_type='oauth2'` placeholder in schema, not implemented |

---

## 13. Doc Updates Required (when implementing)

| Doc | What to add |
|---|---|
| `docs/SCHEMA.md` | `them.mcp_servers`, `them.app_mcp_credentials` |
| `docs/REDIS.md` | New `them:mcp:*` keys |
| `docs/ARCHITECTURE.md` | MCP Service goroutine, tool-call dispatch path |
| `go/TEST_INDEX.md` | MCP DAL, service, executor test files |
| `scripts/tests/INDEX.md` | Any new Python integration tests (unlikely; Go-only feature) |
| `CLAUDE.md` trigger map | Add MCP files to the "changed → run tests" table |
