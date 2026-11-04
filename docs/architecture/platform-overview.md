---
title: Platform Overview & Container Map
type: architecture
status: current
owns:
  - docker-compose.yml
  - docker-compose.dev.yml
  - docker-compose.temporal.yml
  - docker-compose.hetzner.yml
  - traefik/
  - go/cmd/them/main.go
  - go/internal/server/
last_verified: 2026-11-04   # bridge runs with TEMPORAL_ENABLED=false (F-140); hetzner overlay DB DSN env (F-103)
---

# Platform Overview & Container Map

## Purpose
One page to answer: what runs, what talks to what, and which door a request comes in through.
Subsystem detail lives in the other `docs/architecture/` docs.

## Shape of the system

```
browser / API client / external agent
        │  :8088
   them-traefik  (path-based routing, Docker-label discovery)
   ├─ /product                  → them-product-site (Next.js Marketing, dev only, also on :3001)
   ├─ /                         → them-frontend (Next.js Dashboard)
   ├─ /auth                     → them-auth-go
   ├─ /api/v1 /ws /sse /health /static/tenants /ws/dashboard
   ├─ /{tenant}/apps /a2a /llm  → them-go-bridge  (single Go binary, :8002)
   ├─ /temporal                 → temporal-ui          (profile temporal)
   ├─ /livekit                  → livekit              (profile voice)
   └─ /auth/keycloak            → them-keycloak        (profile sso)

them-go-bridge ──► them-postgres (DB them, two RLS pools)   ──► them-redis (DB 0)
              ├──► Temporal (start workflows)  ──► workers (below)
              ├──► them-mcp-service (MCP tool exec)  ──► external MCP servers
              ├──► A2A agents (vision-agent, them-agent-runtime, test/demo agents)
              └──► them-redis-events (optional, events pipeline → see events-pipeline.md)
```

Only Traefik (8088 app, 8089 dashboard) and LiveKit (7882) publish host ports; everything else is
internal on `them-network`. The Python bridge and Python auth service are **gone** (`app/` and
`auth_service/` do not exist; `them-auth-go` replaced the latter in Aug 2026).

## Containers (from the rendered compose, all profiles)

| Container | Profile | Role | Source |
|---|---|---|---|
| `them-traefik` | core | Reverse proxy | `traefik/traefik.yml` + labels |
| `them-postgres`, `them-redis` | core | Postgres 16 (DB `them`; also hosts Temporal's DBs), Redis DB 0 | — |
| `them-auth-go` | core | Login/me/refresh/logout, verify | `go/cmd/auth-server/`, `internal/authserver/` |
| `them-go-bridge` | core | The API: admin, WS/SSE, A2A, LLM gateway, apps | `go/cmd/them/` |
| `them-go-worker`, `-2` | core | Temporal worker, queue `them-orchestration-go` (orchestrator runs) | `go/cmd/worker/` |
| `them-mcp-service` | core | MCP server supervisor/executor, internal only | `go/cmd/mcp-service/` |
| `them-frontend` | core | Next.js UI Dashboard (dev compose swaps in `Dockerfile.dev`) | `frontend/` |
| `them-product-site` | dev-only | Next.js marketing/product site (GSAP + Framer Motion; :3001 direct, :8088/product via Traefik; deployed to Vercel in production, not Docker) | `product-site/` |
| `vision-agent` | core | Vision/maps A2A agent | `agents/vision_agent/` |
| `temporal-frontend`, `temporal-ui`, `temporal-admin-tools` | temporal | Temporal server + UI | image |
| `them-dag-worker`, `-2`, `-3`, `-4` | temporal | Temporal worker: canvas agent DAG steps (queue `canvas-dag-nodes`) and AppFlow (queue `appflow-dag`) | `go/cmd/dag-worker/` |
| `them-dag-worker-debug` | temporal | Same image tag **built separately**; AppFlow debug runs on queue `appflow-dag-debug` | `go/cmd/dag-worker/` |
| `them-agent-runtime` | agents | Generic A2A server for canvas-built agents (reads AgentSpec from Postgres); `docker-compose.temporal.yml` sets `TEMPORAL_ENABLED=true` | `go/cmd/agent-runtime/` |
| `them-clickhouse`, `them-redis-events`, `them-event-writer` | events | See `events-pipeline.md` | — |
| `them-middleware-worker`, `them-minio`, `them-minio-init`, `them-clamd`, `them-security-agent` | security | Artifact scan jobs, the quarantine reaper and the object janitor (stored files of deleted run artifacts; `OBJECT_JANITOR_INTERVAL_MINUTES`); needs `THEM_DB_URL_APP/ADMIN` like every Go binary (polls `them.middleware_jobs`), ClamAV, object store, security scanner A2A agent | `go/cmd/middleware-worker/` |
| `them-keycloak` | sso | SSO | `keycloak/` |
| `livekit`, `livekit-agent` | voice | Voice | `livekit/` |
| `a2a-echo`, `a2a-slow`, `a2a-stream` | test-agents | A2A test agents | `agents/` |
| `agent-evidence/-logic/-creative/-judge` | debate | Debate demo agents | `agents/debate/` |
| `docu-writer` | docu | Demo agent | `agents/docu_writer/` |
| `them-workflow-advisor` | advisor | Demo agent | `agents/workflow_advisor/` |

`docker-compose.hetzner.yml` is the production variant: it defines `them-go-bridge-2` (second replica,
`THE_M_INSTANCE_ID=go-bridge-2`, profile `temporal`) and only `them-dag-worker`, `-2`, `-debug`
(no `-3/-4`), and omits Keycloak, test-agents, debate, docu and voice services. It passes `THEM_DB_URL_APP` and
`THEM_DB_URL_ADMIN` to every Go service that needs them (bridge-2, both go-workers, all dag workers; fixed 2026-11-02,
F-103): a container recreated without them crash-loops. Rollout steps: `docs/plans/production-rollout-2026-11.md`.

## Request entry points

Traefik routes by label priority (higher wins): `/product` 200 (dev-only product site), `/ws/dashboard` 150,
`/temporal` 150, `/health` 130, `/auth` 120, `/ws` 120, `/static/tenants` 120, `/api/v1` 110, `/livekit` 100,
frontend `/` = 10 (catch-all). Tenant-scoped routes are regex rules: `^/[^/]+/apps`, `^/[^/]+/a2a`, `^/[^/]+/llm`.

Inside `them-go-bridge` (`internal/server/server.go`, wired in `cmd/them/main.go`):
`/ws`, `/ws/dashboard`, `/sse`, `/api/v1` (admin + runs), artifacts, `/static/tenants/*`, the LLM
gateway (`/{tenant}/llm/v1/chat/completions`), and a catch-all `/*` handled by `appsDispatcher`
which dispatches `/{tenant}/apps/*` (WS, SSE, voice) and `/{tenant}/a2a/*`.
**Do not `Mount("/")` for A2A**: it conflicts with the `/*` handler in chi (this caused an outage; see comment at `main.go:470`).

## Startup wiring order of `them-go-bridge` (`main.go`)
config → telemetry → Postgres + two RLS pools (`THEM_DB_URL_APP`, `THEM_DB_URL_ADMIN`, both required)
→ Redis → optional events Redis → in-process event bus → session store → run recorder → rate limiter →
admission gate → agent registry → auth middleware → health + HTTP server → bearer-token cache →
Temporal client (optional: with `TEMPORAL_ENABLED=false` the gateway-only mode runs, see `run-lifecycle.md` gap 1) + run reconciler + data retention job (`internal/retention`, dry-run by default, writes on in `docker-compose.dev.yml` via `THE_M_RETENTION_DRY_RUN=false`; see `data-retention.md`) (reconciler writes on in `docker-compose.dev.yml` via `RECONCILER_DRY_RUN=false`; code default and the Hetzner overlay stay dry-run) → entry-point config loader → execution
lifecycle + quota/role enforcers → WS, SSE, dashboard WS → heartbeat/liveness loops → A2A → artifacts →
admin API → LLM gateway → apps dispatcher → `ListenAndServe` (graceful drain on SIGTERM). Right after the RLS pools are up the bridge (like auth, the dag workers and the orchestrator worker) runs `dbversion.Enforce`: it logs an ERROR when the newest migration the code needs is not recorded in `them.schema_migrations` and refuses to start only with `THE_M_REQUIRE_MIGRATIONS=true`.

## Key invariants
- Application is the tenant boundary; per-app state carries the app ID/slug in Redis keys and SQL.
- Subscribe to the run-event bus **before** starting the Temporal workflow.
- The bridge never runs workflow code; Temporal workers do. Changing worker code requires restarting **all** worker containers of that image.
- Each `them-dag-worker-*` service builds its own image tag; rebuild all of them (including `-debug`) or some keep running the old binary.

## Failure modes
| Down | Effect |
|---|---|
| Postgres / Redis | Bridge cannot start (required at startup); health endpoints fail |
| Temporal server | If `TEMPORAL_ENABLED=true`, bridge cannot start (`client.Dial` at startup); if `false`, bridge runs without orchestrated runs |
| Workers only | Bridge runs, but orchestrated/AppFlow runs do not progress |
| `them-mcp-service` | MCP tool calls fail; rest of the platform unaffected |
| Event Redis / ClickHouse | See `events-pipeline.md`; never blocks requests |
| `them-auth-go` | New logins fail; existing JWTs keep validating (the bridge checks them locally) |

## Known gaps (found while writing, 2026-10-24)
- Root `CLAUDE.md` "Container Map" still lists the removed `them-auth-service` (8701) and omits workers, `them-agent-runtime`, `them-middleware-worker` and others; it also still describes Python (`app/adapters/`, `auth_client.py`, `generate-env`-era docs). Replace with a pointer here (do in the auth/run-lifecycle items).
- `CLAUDE.md` says the DAG worker has "two replicas"; compose defines four plus a debug worker (dev) and two plus debug (hetzner).
- `docker-compose.yml` still has `version:` (compose warns it is obsolete).
- The repo root holds many stray status/result files (`FINAL_STATUS.md`, `SUCCESS.md`, `HANDOVER_*.md`, `perf_*.json`, `*_RESULTS*.md`, ...) outside `docs/`; triage/archive them.

## Where to change things
| To… | Edit |
|---|---|
| Add a route | `internal/server/server.go` (mount), `cmd/them/main.go` (wire), compose Traefik labels with explicit `priority` |
| Add a container | `docker-compose.yml` (+ dev/hetzner as needed), choose a profile, update the table above |
| Add a tenant-scoped route | extend `appsDispatcher` in `cmd/them/main.go`; add a Traefik regex router |

Verify a route is live by checking bridge logs for real hits, not only labels (`go/CLAUDE.md` route-ownership rule).
Tests: see `go/TEST_INDEX.md`; compose label changes → bring stack up and check all containers healthy.
Runbook: `docs/guides/local-dev-and-deploy.md`.
