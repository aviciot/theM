---
title: MCP Service
type: architecture
status: current
owns:
  - go/internal/mcp/
  - go/cmd/mcp-service/main.go
  - go/internal/orchestrator/mcp.go
  - go/internal/admin/mcp_servers.go
  - go/internal/admin/service/mcp_servers.go
  - go/internal/admin/dal/mcp_servers.go
last_verified: 2026-10-24   # code read + live read-only DB query; no MCP call made
---

# MCP Service

## Purpose
`them-mcp-service` (container, port 8010, internal only) keeps a registry of **MCP servers** (Model Context
Protocol tool servers registered per tenant), watches their health, caches their tool lists, and executes
tool calls on behalf of the orchestrator using per-application credentials.

## How it works

```
admin UI/API  ── /api/v1/admin/mcp-servers (CRUD, probe) ──► them.mcp_servers (+ app_mcp_credentials)
                                                              │
them-mcp-service ─ Supervisor (leader only) ─ one worker per enabled server ─► MCP server (HTTP)
        │   probe: initialize → tools/list → status + manifest → DB + Redis + pub/sub
        ▼
orchestrator run ── LLM emits tool call `mcp__{server}__{tool}` ──► POST /internal/execute
        └─ Executor: app → tenant → server (slug+tenant) → credential → tool in manifest → MCP tools/call
```

### Registration (admin, `admin/mcp_servers.go`, tenant-admin scope, RLS via `BeginTenantTx`)
Fields: `slug`, `name`, `transport` (`http|sse|streamable-http`), `url`, `auth_type` (`none|bearer|header|oauth2`), optional `probe_token` (encrypted with the platform key; used only for probing). Per-application credentials live in `app_mcp_credentials` (encrypted). `POST .../{id}/probe` proxies to the service's `/internal/probe/{id}`. Orchestrators attach servers through `app_orchestrators.mcp_servers` (`[{slug, tools[] allowlist}]`).

### Supervisor and workers (`supervisor.go`, `health.go`, `leader.go`)
- **Leader election:** Redis `SET them:mcp:leader <instance> NX PX 30s`, renewed on each 30s reconcile. Only the leader runs the Supervisor; other replicas just serve `/internal/execute`.
- **Reconcile (every 30s):** load all enabled servers across tenants (`ListEnabledServers`), repopulate the in-process `Registry`, start a worker goroutine for each new ID, stop workers for removed/disabled IDs.
- **Worker per server:** own ticker (base 30s, `MCP_HEALTH_INTERVAL_SECONDS`), panic recovery, one persistent `Client` (initialize once, re-initialize on HTTP 404). Status: `healthy`; `degraded` (tools/list failed, session expired, or tool count dropped >20% → manifest **not** updated); `unreachable` (initialize failed; interval doubles to max 10 min). Each probe writes `mcp_servers.health_status/last_error/tools_manifest`, Redis `them:{tenant}:mcp:health:{slug}` (90s) and `them:{tenant}:mcp:manifest:{slug}` (5 min), and publishes the slug on `them:mcp:manifest:changed` so bridges drop cached tool lists.

### Execute (`executor.go`, `server.go`)
`POST /internal/execute {application_id, mcp_server_slug, tool_name, arguments, test_credential?}`:
1. tenant = tenant of `application_id` (DB); 2. server = enabled row with that slug **and** tenant; 3. credential: `auth_type=none` → none; `test_credential` (canvas test) if given; else `app_mcp_credentials` for (app, server), decrypted, `bearer` gets a `Bearer ` prefix; 4. tool must exist in the cached manifest (skipped if manifest empty/unparsable); 5. new `Client` → `initialize` → `tools/call`; result returned as `{result}`, errors as 422 `{error}`. A `tool_call` event (status, duration) is emitted when events mode is `dual` (not in `postgres` mode), and an MCP call counter is recorded.

### Orchestrator side (`orchestrator/mcp.go`, `tools.go`, `temporal/workerconfig/loader.go`)
At run start the worker loads the orchestrator's `mcp_servers`, reads each manifest from the DB and builds LLM tool definitions named `mcp__{slug}__{tool}` (filtered by the attachment's `tools` allowlist; an empty allowlist = all). At execution, any tool name starting `mcp__` is split at the first `__` after the prefix and posted to the service with the run's `ApplicationID`. Results (or error text) are published as `tool_result` events and fed back to the LLM.

### Client (`client.go`)
Hand-written MCP streamable-HTTP client (protocol `2024-11-05`): JSON-RPC over POST, accepts JSON or SSE replies, keeps `Mcp-Session-Id`, sends `notifications/initialized`. Only HTTP(S) POST — no stdio, no legacy SSE transport.

## Key invariants
- Tenant isolation: executor resolves the tenant from the application, then loads the server by `(slug, tenant_id)` — a tenant cannot name another tenant's server through `/internal/execute`.
- Credentials are encrypted at rest, decrypted only inside the service, and never returned in API responses or logs; test credentials are used once and discarded.
- Probing never uses per-app credentials, only the server's optional probe token.
- Workers are independent: a slow or panicking server cannot block others.

## Failure modes
| What fails | Effect |
|---|---|
| MCP server down | Worker marks `unreachable`, backs off; executes fail with `mcp initialize failed` (422) |
| Leader replica dies | No probing for up to ~30s until another replica wins the lock; executes keep working on all replicas |
| Redis down | Leader check errors → supervisor skips reconcile; manifest/health cache writes logged only |
| `them-mcp-service` down | Orchestrator `mcp__` calls return errors to the LLM; rest of the run continues |
| No credential for app+server | 422 "no credential configured … set one in Application settings" |
| Server removes >20% of tools | `degraded` forever until counts recover; manifest stays stale |

## Known gaps (found while writing, 2026-10-24; from reading code + live DB; no MCP call made)
See `docs/plans/code-findings-backlog.md` F-56…F-66. Highlights:
1. **`/internal/execute` and `/internal/probe` have no authentication.** The service trusts `application_id` (and `test_credential`) from the body; any container on `them-network` can run tools with any application's stored credentials.
2. **No URL validation → SSRF.** Any scheme/host is accepted (live rows already point at an internal IP and Docker hostnames). `MCP_ALLOW_HTTP` / `MCP_ALLOW_STDIO` exist in config but are never read.
3. **Admin probe has no tenant check.** `POST /admin/mcp-servers/{id}/probe` forwards the raw ID; the service loads by ID only, so one tenant's admin can probe (and read health/tool count/last_error of) another tenant's server if they know the UUID.
4. **Allow-list is not enforced at execution.** The attachment's `tools` allowlist only limits what the LLM is shown; the executor accepts any tool in the manifest of any tenant server the app has a credential for.
5. **Workers never refresh.** A worker keeps the `Server` struct it started with; editing a server's URL or probe token has no effect until it is disabled/re-enabled or the service restarts.
6. **Client hardening:** no response-size cap, only the first SSE `data:` line is read, fixed 10s timeout for tool calls, redirects followed with the auth header, a full `initialize` handshake on every call.
7. Admin error text says transports are "http, sse, stdio" but `stdio` is rejected and `streamable-http` is accepted; `sse` is accepted but the client can't speak legacy SSE. Slug is not validated (`__` in a slug breaks `mcp__{slug}__{tool}` parsing).

## Where to change things
| To… | Edit |
|---|---|
| Change probe behaviour / health states | `mcp/health.go`, `supervisor.go` |
| Change execution or credential rules | `mcp/executor.go`, `mcp/dal.go` |
| Change the MCP wire client | `mcp/client.go` |
| Add admin fields / validation | `admin/service/mcp_servers.go`, `admin/dal/mcp_servers.go`, handler `admin/mcp_servers.go` |
| Change how tools reach the LLM | `temporal/workerconfig/loader.go` (`resolveMCPServers`), `orchestrator/tools.go`, `orchestrator/mcp.go` |

After editing `go/internal/mcp/` or `cmd/mcp-service/`: `go test ./internal/mcp/...`, rebuild + restart `them-mcp-service`. Admin changes: `go test ./internal/admin/...`.

## Related
- `run-lifecycle.md` — orchestrator loop and `tool_result` events
- `events-pipeline.md` — `tool_call` events; `multi-tenancy-rls.md` — `mcp_servers` RLS; `platform-overview.md` — container
- Canvas-agent `mcp_call` steps (agentgen) are covered with item #9
