# the-M Architecture
# Last updated: 2026-08-15

---

## Current Stack (Go/Python hybrid)

the-M is migrating from Python to Go. Both services run behind a shared Traefik instance. Go routes
take priority via higher Traefik priority numbers; Python handles everything else.

```
Browser / Client
      │
      ▼
them-traefik :8088  (Traefik v3.6, path-based routing)
      │
      ├─► them-auth-go :8703     Go auth service (login/me/refresh/logout)
      ├─► them-go-bridge :8002   Go gateway (migrated routes, priority 110–130)
      └─► them-bridge :8001      Python bridge (remaining routes, priority 100)

them-frontend :3200  (Next.js — served via Traefik at priority 10)
them-postgres :5432  (PostgreSQL 16, DB: them)
them-redis    :6379  (Redis 7, DB 0)
```

Compose files:
- `docker-compose.yml` — base (all environments)
- `docker-compose.dev.yml` — local dev overrides
- `docker-compose.hetzner.yml` — Hetzner production overrides

---

## Authentication

Two auth paths — see `docs/AUTH.md` for full detail.

**Human UI (JWT):** Browser → Next.js → `them-auth-go:8703` → HS256 JWT in httpOnly cookies.
All Go admin routes use `AdminTenantMiddleware`: reads tenant from JWT claims, falls back to bootstrap tenant for super_admin users.

**Machine/data-plane (opaque token):** `them.access_tokens` table → sha256 lookup → L1 sync.Map → L2 Redis → PostgreSQL.
Used for WS/SSE/A2A endpoints.

---

## Orchestration (Temporal)

All orchestration runs through Temporal `OrchestrationWorkflow`. The bridge is a thin edge:
authenticate → start/signal workflow → relay Redis token stream to client. Bridge is stateless.

```
Client → WS/SSE → them-bridge or them-go-bridge
       → Temporal: OrchestrationWorkflow
             load context + agents + prior history
             agentic loop (≤ max_iterations):
                  LLM turn → pick tool(s) → stream tokens to Redis
                  invoke_agent × N (parallel, bounded by max_parallel_tools)
                  record_tool_results → DB (durable history)
             finalize_run
       → Bridge relays Redis stream → Client
```

Workers:
- `them-worker` — Python Temporal worker (primary, `them-orchestration` queue)
- `them-go-worker` — Go Temporal worker (registered on same queue)

---

## Traefik routing (priority order)

Higher priority wins. Go routes are registered at p=110–130; Python at p=100.

| Priority | Owner | Rule |
|---|---|---|
| 130 | Go | `/health/live`, `/health/ready` |
| 120 | Go | `/api/v1/admin/tokens`, `/api/v1/admin/sessions`, `/apps/{slug}/ws`, `/apps/{slug}/sse`, `/ws/orchestrate/{app}/{ep}`, `/sse/orchestrate/{app}/{ep}`, LLM providers, monitoring-config |
| 116 | Go | `POST /api/v1/admin/agents/(discover\|{id}/test\|{id}/security-scan)` |
| 115 | Go | Admin CRUD writes (agents, orchestrators, applications, entry-points) |
| 110 | Go | Admin CRUD reads (agents, orchestrators, applications, runs GET) |
| 100 | Python | All `/api/v1`, `/ws`, `/apps`, `/a2a` (fallback) |
| 90 | Python | `/health` (bare) |
| 10 | Frontend | `/` (catch-all) |

---

## Agent model

Each enabled `them.agents` row = one LLM tool named `agent__<slug>`.
The agent's `description` is the tool description — the LLM uses it to decide when to invoke.

Transport types: `a2a_async` (A2A JSON-RPC 2.0), `http`, `omni_ws`.
A2A agents serve `/.well-known/agent-card.json` describing their skills.

Agent categories (auto-classified by Anthropic): Research, Coding, Vision, Security, A2A, Data, Communication, Agent.

---

## Application model

Applications are the tenant boundary. Each application has:
- One or more **entry points** (WS, SSE, WebRTC doors — `them.entry_points`)
- Per-app orchestrator instances (`them.app_orchestrators`)
- Optional middleware wirings (`them.middleware_wirings`)
- Runtime config (session caps, rate limits — `applications.runtime_config`)

Compiled projection: canvas edits write to `them.app_orchestrators` + `them.entry_points` via `app_compiler.py`.
Runtime readers (epconfig, Temporal loaders) read only the projection — never the canvas JSON directly.

---

## Key source locations

| Concern | Location |
|---|---|
| Go binary entrypoint | `go/cmd/them/main.go` |
| Go auth service | `go/cmd/auth-server/`, `go/internal/authserver/` |
| Go admin handlers | `go/internal/admin/` |
| Go admin DAL | `go/internal/admin/dal/` |
| Go WS/SSE handlers | `go/internal/ws/`, `go/internal/sse/` |
| Go auth middleware | `go/internal/auth/` |
| Go Temporal worker | `go/internal/temporal/` |
| Python orchestration loop | `app/services/task_runner.py` |
| Python agent adapters | `app/adapters/` |
| Python WS endpoint | `app/routers/ws_orchestrator.py` |
| Python A2A server | `app/routers/a2a_server.py` |
| DB schema | `db/001_schema.sql` (source of truth) |
| Auth schema | `auth_service/SCHEMA.sql` |
| Frontend proxy | `frontend/src/app/api/them/[...path]/route.ts` |

---

## Data stores

**PostgreSQL** (`them-postgres`, DB: `them`):
- `them.*` schema — main application data
- `auth_service.*` schema — user/role/session data (owned by auth service)
- See `docs/SCHEMA.md` for full table reference

**Redis** (`them-redis`, DB 0):
- Key prefixes: `them:session:`, `them:token:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`, `them:ep:config:`
- See `docs/REDIS.md` for full key reference
