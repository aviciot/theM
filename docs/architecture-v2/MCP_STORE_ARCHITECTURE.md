# MCP Store Architecture — Discovery, Registry, Canvas Integration & Runtime

Status: design proposal  
Date: 2026-08-24  
Author: Chaya Friedman  
Scope: end-to-end MCP (Model Context Protocol) support in the-M — dedicated MCP service, admin
registry, canvas visual builder integration, and runtime credential injection.

---

## 0. Executive Summary

We introduce a first-class **MCP Store** into the-M: a managed registry of MCP servers that the
platform discovers, health-checks, and makes available as draggable nodes in the canvas agent builder.

**`them-mcp-service` is a dedicated Go binary and container**, separate from `them-go-bridge`. It
owns the health-check loop, tool discovery, and credential-aware tool execution. It can be
built, tested, deployed, and restarted without touching any existing service.

At runtime, MCP credentials are resolved from per-application configuration — the same pattern
already established for LLM provider keys (`them.llm_providers`) — so the canvas definition stays
secret-free and portable.

The feature has five independent layers, each deliverable in isolation:

| Layer | What it is | Where it lives |
|---|---|---|
| **MCP Registry** | DB tables + CRUD admin API for MCP server records | `them-go-bridge` (`internal/admin/`) |
| **MCP Service** | Health-check loop, connectivity probe, tool discovery, tool execution | **`them-mcp-service`** (new binary + container) |
| **App Credentials** | Per-app MCP token/auth config (mirrors `llm_providers` pattern) | `them-go-bridge` (`internal/admin/`) |
| **Canvas Nodes** | `mcp_server` node type in the visual builder | frontend + `them-go-bridge` node-type registry |
| **Runtime Binding** | Orchestrator calls MCP Service to execute tools during a run | `them-go-bridge` → HTTP → `them-mcp-service` |

---

## 1. Terminology

| Term | Meaning in this doc |
|---|---|
| **MCP Server** | An HTTP or SSE MCP-protocol server exposing a set of tools |
| **MCP Tool** | A single callable function exposed by an MCP Server |
| **MCP Registry** | The `them.mcp_servers` table — one row per registered server |
| **App MCP Credential** | A `them.app_mcp_credentials` row: per-application auth token/secret for a server |
| **Canvas MCP Node** | A `mcp_server` node on the canvas — represents one server and its selected tools |
| **MCP Service** | `them-mcp-service` container — the dedicated Go service that manages all MCP interaction |

---

## 2. Service Architecture

### 2.1 New container: `them-mcp-service`

```
┌─────────────────────────────────────────────────────────┐
│  them-go-bridge  :8002                                   │
│                                                          │
│  Admin API  ──────────────── mcp-servers CRUD            │
│  Orchestrator ─────────────► POST /internal/execute      │
│                              (tool call dispatch)        │
└──────────────────────────────────┬──────────────────────┘
                                   │ HTTP (internal network)
                              :8010 ▼
┌─────────────────────────────────────────────────────────┐
│  them-mcp-service  :8010                                 │
│                                                          │
│  /health/live, /health/ready                             │
│  /internal/probe/{server_id}   — on-demand health probe  │
│  /internal/execute             — tool call execution     │
│                                                          │
│  Background: health-check loop (all enabled servers)     │
│  Background: tool discovery loop (writes tools_manifest) │
└──────────────────────────────────┬──────────────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │                             │
               them-postgres                 them-redis
               (reads mcp_servers,           (manifest cache,
                writes health/manifest)       pub/sub invalidation)
```

Key properties:
- `them-mcp-service` is **not** behind Traefik. It is an internal service only, reachable at
  `http://them-mcp-service:8010` on `them-network`. No public-facing routes.
- `them-go-bridge` calls it over the internal network for on-demand probes and tool execution.
- `them-mcp-service` writes health state and `tools_manifest` directly to the DB. The admin API
  in `them-go-bridge` reads them back on `GET /api/v1/admin/mcp-servers`.
- They share the same Postgres and Redis, but `them-mcp-service` never touches any table other
  than `them.mcp_servers` and `them.app_mcp_credentials`.

### 2.2 Source layout

```
go/cmd/mcp-service/
  main.go            — wire DB, Redis, HTTP server, start background loops

go/internal/mcp/
  client.go          — HTTP/SSE MCP protocol client (initialize, tools/list, tools/call)
  registry.go        — in-process cache of mcp_servers rows (TTL + Redis pub/sub invalidation)
  health.go          — background health-check loop
  discovery.go       — tools/list probe → writes tools_manifest back to DB
  resolver.go        — resolves (slug, application_id) → (url, authed http.Client)
  executor.go        — handles POST /internal/execute (called by orchestrator)
  server.go          — chi HTTP router: /health/*, /internal/*
```

New Dockerfile: `Dockerfile.mcp-service` (mirrors `Dockerfile.auth-go` pattern):

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /build
RUN apk add --no-cache git
COPY go/go.mod ./
RUN go mod tidy
COPY go/ ./
RUN go mod tidy && \
    go test ./internal/mcp/... && \
    CGO_ENABLED=0 GOOS=linux go build -o /mcp-service ./cmd/mcp-service/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY --from=builder /mcp-service ./mcp-service
EXPOSE 8010
CMD ["./mcp-service"]
```

### 2.3 docker-compose entry

```yaml
them-mcp-service:
  build:
    context: .
    dockerfile: Dockerfile.mcp-service
  container_name: them-mcp-service
  environment:
    - APP_PORT=8010
    - DATABASE_HOST=them-postgres
    - DATABASE_PORT=5432
    - DATABASE_NAME=them
    - DATABASE_USER=${THE_M_DB_USER:-them}
    - DATABASE_PASSWORD=${THE_M_DB_PASSWORD:-them_secret}
    - REDIS_HOST=them-redis
    - REDIS_PORT=6379
    - REDIS_PASSWORD=${THE_M_REDIS_PASSWORD:-}
    - SECRET_KEY=${THE_M_SECRET_KEY:-change-this-secret-key}   # for Fernet decrypt
    - MCP_HEALTH_INTERVAL_SECONDS=60
    - LOG_FORMAT=json
  networks:
    - them-network
  depends_on:
    - them-postgres
    - them-redis
  restart: unless-stopped
  healthcheck:
    test: ["CMD", "curl", "-f", "http://localhost:8010/health/live"]
    interval: 15s
    timeout: 5s
    retries: 3
  labels:
    - "traefik.enable=false"   # internal only — never exposed through Traefik
```

`them-go-bridge` adds `them-mcp-service` to its `depends_on` so the orchestrator can call it
at runtime. The bridge also gets a new env var:

```yaml
MCP_SERVICE_URL: "http://them-mcp-service:8010"
```

---

## 3. Data Model

### 3.1 `them.mcp_servers` — platform-level registry

```sql
-- db/040_mcp_registry.sql
CREATE TABLE IF NOT EXISTS them.mcp_servers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    name            TEXT        NOT NULL,
    slug            TEXT        NOT NULL,   -- stable canvas/runtime key
    description     TEXT,
    transport       TEXT        NOT NULL CHECK (transport IN ('http', 'sse', 'stdio'))
                                DEFAULT 'http',
    url             TEXT,                   -- NULL for stdio (future)
    auth_type       TEXT        NOT NULL CHECK (auth_type IN ('none', 'bearer', 'header', 'oauth2'))
                                DEFAULT 'none',
    -- written by them-mcp-service only; never by admin API writes
    health_status   TEXT        NOT NULL DEFAULT 'unknown'
                                CHECK (health_status IN ('unknown', 'healthy', 'degraded', 'unreachable')),
    last_checked_at TIMESTAMPTZ,
    last_error      TEXT,
    tools_manifest  JSONB       NOT NULL DEFAULT '[]',   -- [{name, description, inputSchema}]
    capabilities    JSONB       NOT NULL DEFAULT '{}',   -- MCP server capabilities block
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);
```

### 3.2 `them.app_mcp_credentials` — per-application secrets

```sql
-- db/041_mcp_app_credentials.sql
CREATE TABLE IF NOT EXISTS them.app_mcp_credentials (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id       UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    mcp_server_id        UUID NOT NULL REFERENCES them.mcp_servers(id) ON DELETE CASCADE,
    credential_encrypted TEXT,                          -- Fernet, same scheme as llm_providers
    auth_header_name     TEXT DEFAULT 'Authorization',  -- only for auth_type='header'
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, mcp_server_id)
);
```

Mirrors `them.llm_providers` credential pattern:
- Admins enter credentials once per (application, MCP server) pair.
- Fernet-encrypted at rest, never logged, never in canvas JSON.
- `auth_type = 'none'` → no credential row required.

### 3.3 Canvas doc — `mcp_server` node

```jsonc
{
  "id": "node-abc123",
  "type": "mcp_server",
  "data": {
    "mcp_server_slug": "github-mcp",
    "selected_tools": ["create_issue", "list_prs"],
    "expose_all_tools": false,
    "label": "GitHub MCP"
  }
}
```

Rules:
- Only `mcp_server_slug` in the doc — no ID, URL, or credential ever serialised here.
- `expose_all_tools: true` + empty `selected_tools` → LLM sees all tools from `tools_manifest`.
- Non-empty `selected_tools` → LLM sees only those (subset enforced at runtime by MCP Service).
- Fully exportable/importable — no secrets.

### 3.4 Test credential (canvas only)

For testing in the canvas before an application credential exists:
- Properties panel shows a collapsible "Test credential" field.
- Value lives in React state only — never persisted to the definition.
- Sent as `test_mcp_credentials: { [slug]: "token" }` in validate/test API call body.
- MCP Service uses it for the duration of that request only; discards immediately after.

---

## 4. MCP Service Internals

### 4.1 Health-check loop (`health.go`)

Runs in a goroutine started at `them-mcp-service` boot. Iterates over all `enabled=true` servers
every `MCP_HEALTH_INTERVAL_SECONDS` (default 60s):

```
probe:     MCP initialize handshake to {url}
success  → health_status='healthy',    last_checked_at=now(), last_error=NULL
timeout  → health_status='unreachable', last_error='timeout after 10s'
error    → health_status='unreachable', last_error='<http error>'
```

Health state machine:

```
unknown     → healthy      (first successful probe)
unknown     → unreachable  (first failed probe)
healthy     → degraded     (probe OK but tool count dropped >20% vs last manifest)
healthy     → unreachable  (probe failed)
degraded    → healthy      (tool count stable again)
degraded    → unreachable  (probe failed)
unreachable → healthy      (probe succeeds)
```

After each successful probe the loop calls `discovery.go` if `tools_manifest` is stale (> 5 min).

### 4.2 Tool discovery (`discovery.go`)

1. MCP `initialize` request.
2. MCP `tools/list` request.
3. Write manifest to `them.mcp_servers.tools_manifest`.
4. Publish `them:mcp:manifest:changed:{slug}` to Redis → bridge caches invalidated.

### 4.3 On-demand probe (`POST /internal/probe/{server_id}`)

Called by `them-go-bridge` when the admin clicks "Test connection" in the properties panel or
settings UI. Runs the full health + discovery cycle synchronously and returns the result inline.
Responds in < 15s (hard timeout); returns `{ health_status, tools_count, last_error }`.

### 4.4 Tool execution (`POST /internal/execute`)

Called by `them-go-bridge` orchestrator when the LLM emits a tool call matching `mcp__{slug}__{tool}`.

Request body:

```json
{
  "application_id": "uuid",
  "mcp_server_slug": "github-mcp",
  "tool_name": "create_issue",
  "arguments": { "title": "Bug", "body": "..." },
  "test_credential": null
}
```

Execution steps:
1. Resolve `mcp_servers` by `(tenant_id, slug)` — tenant derived from `application_id`.
2. Resolve credential from `app_mcp_credentials` → Fernet-decrypt → inject auth header.
   - If `test_credential` is present (canvas test only): use it instead of DB credential.
   - If `auth_type != 'none'` and no credential: return 422 with clear error (fail-closed).
3. Issue MCP `tools/call`.
4. Return `{ result, error }` to orchestrator.

Response used by orchestrator to inject the tool result into the LLM message context.

---

## 5. Admin API (in `them-go-bridge`)

### 5.1 New routes

```
POST   /api/v1/admin/mcp-servers                              Create
GET    /api/v1/admin/mcp-servers                              List (tenant-scoped, includes health_status + tool count)
GET    /api/v1/admin/mcp-servers/{id}                         Get single (full tools_manifest)
PUT    /api/v1/admin/mcp-servers/{id}                         Update (name/url/auth_type/enabled — NOT tools_manifest)
DELETE /api/v1/admin/mcp-servers/{id}                         Delete (blocked if slug in published definition)

POST   /api/v1/admin/mcp-servers/{id}/probe                   On-demand probe (proxies to MCP Service)
GET    /api/v1/admin/mcp-servers/{id}/tools                   Returns tools_manifest

PUT    /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}   Set/update credential
DELETE /api/v1/admin/applications/{app_id}/mcp-credentials/{server_id}   Remove credential
GET    /api/v1/admin/applications/{app_id}/mcp-credentials               List (slug + key_set bool, NEVER decrypted value)
```

Go files in `them-go-bridge`:
- `go/internal/admin/mcp_servers.go` — handler
- `go/internal/admin/dal/mcp_servers.go` — DAL
- `go/internal/admin/service/mcp_servers.go` — service
- `go/internal/admin/mcp_app_credentials.go` — credential handler

### 5.2 Traefik routing

New Traefik labels on `them-go-bridge`:

```yaml
# MCP admin writes (priority 115)
- "traefik.http.routers.them-go-mcp-write.rule=PathPrefix(`/api/v1/admin/mcp-servers`) && (Method(`POST`) || Method(`PUT`) || Method(`DELETE`))"
- "traefik.http.routers.them-go-mcp-write.entrypoints=web"
- "traefik.http.routers.them-go-mcp-write.priority=115"
- "traefik.http.routers.them-go-mcp-write.service=them-go-bridge-svc"

# MCP admin reads (priority 110)
- "traefik.http.routers.them-go-mcp-read.rule=PathPrefix(`/api/v1/admin/mcp-servers`)"
- "traefik.http.routers.them-go-mcp-read.entrypoints=web"
- "traefik.http.routers.them-go-mcp-read.priority=110"
- "traefik.http.routers.them-go-mcp-read.service=them-go-bridge-svc"
```

`them-mcp-service` is NOT added to Traefik — it is internal-only.

---

## 6. Canvas Visual Builder Integration

### 6.1 New node type: `mcp_server`

Registered in `go/internal/admin/node_types.go`:

```json
{
  "type": "mcp_server",
  "category": "integrations",
  "label": "MCP Server",
  "icon": "plug",
  "inputs": ["control_flow"],
  "outputs": ["control_flow"],
  "config_schema": {
    "mcp_server_slug":  { "type": "string",  "required": true },
    "selected_tools":   { "type": "array",   "items": { "type": "string" } },
    "expose_all_tools": { "type": "boolean", "default": false }
  }
}
```

### 6.2 Properties panel

```
┌─ MCP Server ──────────────────────────────────────────┐
│ Server  [GitHub MCP ▼]           ● healthy             │
│                                                        │
│ Tools                                                  │
│  ◉ All tools (LLM sees full manifest)                  │
│  ○ Selected tools only                                 │
│   ☑ create_issue   ☑ list_prs   ☐ delete_issue        │
│                                                        │
│ ▼ Test credential  (canvas session only — not saved)   │
│   Bearer token [_______________________________]       │
│   [Test connection ▶]                                  │
└────────────────────────────────────────────────────────┘
```

- Server dropdown from `GET /api/v1/admin/mcp-servers` (enabled rows only).
- Health badge reflects `health_status` from the last MCP Service probe.
- Tool checklist from `GET /api/v1/admin/mcp-servers/{id}/tools` (live `tools_manifest`).
- "Test connection" → `POST /api/v1/admin/mcp-servers/{id}/probe` → inline result.
- Test credential: React state only, never serialised.

### 6.3 Application settings — MCP Credentials tab

New tab in App settings (mirrors LLM Providers tab):

```
┌─ MCP Credentials ─────────────────────────────────────┐
│ GitHub MCP          bearer    [key set ●]   [Edit]     │
│ Slack MCP           header    [no key  ○]   [Set]      │
│ Internal Tools      none      [n/a    ]                │
└────────────────────────────────────────────────────────┘
```

- Edit/Set → modal with masked input → `PUT .../mcp-credentials/{server_id}`.
- Credential never shown after save — badge only.

---

## 7. Publish-time Validation

`service/agent_definitions_publish.go` gains an MCP phase:

1. For each `mcp_server` node: resolve `mcp_server_slug` → `them.mcp_servers`.
2. Server not found or disabled → **validation error** (block publish).
3. `auth_type != 'none'` and no `app_mcp_credentials` row → **validation warning** (publish allowed, runtime will fail).
4. `selected_tools` entries not in current `tools_manifest` → **validation warning** (tools may have been removed).
5. Results added to `ValidationResult` alongside existing A2A/schema checks.

---

## 8. Redis Keys

New keys (add to `docs/REDIS.md`):

| Key pattern | Type | TTL | Purpose |
|---|---|---|---|
| `them:mcp:manifest:{slug}` | JSON string | 5 min | Manifest cache in MCP Service (avoids DB on every execution) |
| `them:mcp:health:{slug}` | JSON string | 90 s | Last health probe result |
| `them:mcp:manifest:changed` | Pub/Sub channel | — | Broadcast after discovery → bridge invalidates tool-list cache |

---

## 9. Migration Files

```
db/040_mcp_registry.sql          — CREATE TABLE them.mcp_servers
db/041_mcp_app_credentials.sql   — CREATE TABLE them.app_mcp_credentials
```

No changes to existing tables. Safe to apply to a running stack.

---

## 10. Implementation Phases

Each phase is independently deployable and does not break existing services.

### Phase MCP-1 — Registry + Admin CRUD (1–2 sessions)

Work entirely in `them-go-bridge`. `them-mcp-service` does not exist yet.

Deliverables:
- `db/040_mcp_registry.sql` + `db/041_mcp_app_credentials.sql`
- `go/internal/admin/` — DAL, service, handlers for mcp-servers and app credentials
- Frontend: Settings → MCP Servers page (list, create, edit, delete)
- Tests: DAL integration tests, service unit tests, handler tests

Health status shows `unknown` for all servers (MCP Service not running yet). Probe endpoint
returns 503 with "MCP Service not configured" until Phase MCP-2.

### Phase MCP-2 — MCP Service binary + health/discovery loop (1 session)

Build the new service in complete isolation — no changes to `them-go-bridge`.

Deliverables:
- `go/cmd/mcp-service/main.go`
- `go/internal/mcp/client.go`, `health.go`, `discovery.go`, `server.go`
- `Dockerfile.mcp-service`
- `docker-compose.yml` — new `them-mcp-service` service entry
- `/health/live`, `/health/ready` endpoints
- `/internal/probe/{server_id}` endpoint
- Background loop writing health + manifest to DB
- Frontend: health badge now live in MCP Servers list
- Tests: mock MCP server, health-transition unit tests

No changes to `them-go-bridge`. No canvas changes. No runtime changes.

### Phase MCP-3 — Canvas Node + Properties Panel (1–2 sessions)

Deliverables:
- `mcp_server` node type in `go/internal/admin/node_types.go`
- Frontend canvas: draggable node, properties panel, tool checklist, test credential field
- Frontend: App Settings → MCP Credentials tab
- Publish-time validation for MCP nodes
- `them-go-bridge` wires `MCP_SERVICE_URL` env var; probe endpoint proxies to MCP Service
- Tests: node type tests, publish validation tests

No runtime execution — a run with an MCP node fails gracefully: "MCP execution not yet enabled".

### Phase MCP-4 — Runtime Tool Execution (1–2 sessions)

Deliverables:
- `go/internal/mcp/resolver.go`, `executor.go`
- `/internal/execute` endpoint in `them-mcp-service`
- `them-go-bridge` orchestrator: dispatch `mcp__{slug}__{tool}` calls to MCP Service
- Run recorder captures MCP tool calls as `run_step` events
- End-to-end test: canvas agent using a real MCP server (or test echo MCP agent)
- Tests: executor unit tests with mock MCP client

---

## 11. Container Map Update

| Container | Role | Port | Source |
|---|---|---|---|
| `them-mcp-service` | MCP health/discovery loop + tool execution engine | 8010 (internal only) | `go/cmd/mcp-service/` |

This row will be added to the Container Map in `CLAUDE.md` when Phase MCP-2 ships.

---

## 12. Security Constraints

- Credentials Fernet-encrypted at rest (same key and scheme as `llm_providers.api_key_encrypted`).
- No credential value ever in canvas JSON, logs, error messages, or API responses.
- `GET` endpoints return `key_set: bool` only — never the decrypted value.
- MCP server `url` validated to be HTTPS at create/update time (HTTP allowed only if
  `MCP_ALLOW_HTTP=true` env var is set, for local dev).
- `them-mcp-service` is not on Traefik — unreachable from outside `them-network`.
- Tool call arguments from LLM passed verbatim to MCP server — argument validation is the MCP
  server's responsibility.
- Tenant isolation enforced in `resolver.go`: `mcp_server.tenant_id` must equal the
  application's `tenant_id` before any credential lookup or tool call proceeds.
- Stdio transport disabled by default; requires `MCP_ALLOW_STDIO=true` env var.

---

## 13. What We Are NOT Building

- **MCP Server hosting** — the-M connects to external MCP servers; it does not host them.
- **OAuth2 flow UI** — `auth_type='oauth2'` reserved in schema; Phase 1–4 implement `none`,
  `bearer`, `header` only.
- **Per-tool ACL** — all selected tools are available to the LLM once a node is wired up.
- **Streaming MCP responses** — `tools/call` is request-response only in this design.
- **Curated server catalog** — Phase 1 is bring-your-own-URL; a marketplace UI is a later layer.

---

## 14. Open Questions

| # | Question | Recommendation |
|---|---|---|
| Q1 | Health check interval: per-server or global? | Global env var `MCP_HEALTH_INTERVAL_SECONDS` with optional per-server `override_interval_seconds` column added later |
| Q2 | What happens when a referenced MCP server is deleted? | Block hard delete if any published definition references the slug; disable (soft-delete) is the safe default |
| Q3 | Should test credentials persist across canvas sessions? | No — React state only, cleared on node deselect or page reload |
| Q4 | MCP servers that require per-user OAuth? | Out of scope; `auth_type='oauth2'` placeholder exists for later |
| Q5 | Should `them-mcp-service` expose a status page for admins? | Add `GET /status` returning all server health summaries — useful for ops, no auth required on internal port |

---

## 15. Doc Updates Required (when implementing)

| Doc | What to add |
|---|---|
| `CLAUDE.md` container map | `them-mcp-service` row |
| `CLAUDE.md` trigger map | `go/internal/mcp/`, `go/cmd/mcp-service/` → run `go test ./internal/mcp/...` |
| `docs/SCHEMA.md` | `them.mcp_servers`, `them.app_mcp_credentials` |
| `docs/REDIS.md` | `them:mcp:*` keys |
| `docs/ARCHITECTURE.md` | MCP Service as separate container, tool-call dispatch path |
| `go/TEST_INDEX.md` | MCP client, health, executor test files |
