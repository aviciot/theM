# the-M — Current Status
# Last updated: 2026-08-15
# HEAD: ca29acd

---

## What is running

### Local Linux (dev overlay) — startup command
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
```
UI: `http://<server-ip>:8088`

| Container | Source | Port | Status |
|---|---|---|---|
| `them-traefik` | traefik:v3.6 | 8088 (host) | Running |
| `them-postgres` | postgres:16 | 5432 (internal) | Running |
| `them-redis` | redis:7 | 6379 (internal) | Running |
| `them-auth-go` | `go/cmd/auth-server` | 8703 (internal) | Running — sole auth service |
| `them-bridge` | `app/` (Python FastAPI) | 8001 (internal) | Running — remaining Python routes |
| `them-frontend` | `frontend/` (Next.js, dev mode) | 3200 (internal) | Running |
| `them-worker` | Python Temporal worker | — | Running |
| `vision-agent` | `agents/vision_agent` | 9100 | Running |
| `temporal-frontend` | temporalio/auto-setup | 7233 (internal) | Running |
| `temporal-ui` | temporalio/ui | via Traefik /temporal/ | Running |

**Optional profiles:**
- `--profile go` — adds `them-go-bridge` (Go gateway, Waves 1-8 routes)
- `--profile go-worker` — adds `them-go-worker` + `them-go-worker-2` (Go Temporal workers)
- `--profile test-agents` — adds A2A echo/slow/stream test agents
- `--profile security` — adds `them-security-agent`
- `--profile debate` — adds debate stack agents

**Removed:** `them-auth-service` (Python auth — removed August 2026, replaced by `them-auth-go`)

**Note:** `them-go-bridge` runs in `--profile go`. Without it, Python bridge handles all routes.
Add `--profile go` to enable Go routing (Waves 1-8 admin + WS/SSE ownership).

---

## Go vs Python route ownership

### Go-owned (through Traefik)

| Domain | Routes |
|---|---|
| Health | `GET /health/live`, `GET /health/ready` |
| Admin agents | Full CRUD + discover + test + security-scan |
| Admin orchestrators | Full CRUD |
| Admin applications | Full CRUD + entry-points + runtime + bulk-delete + sub-routes |
| Admin tokens | Full CRUD |
| Admin sessions | List + disconnect |
| Admin LLM providers | Full CRUD + routing/config |
| Admin monitoring-config | GET + PUT |
| Runs (reads) | `GET /api/v1/runs`, `GET /api/v1/runs/stats`, `GET /api/v1/runs/{id}`, `GET /api/v1/runs/{id}/tasks`, `GET /api/v1/runs/{id}/artifacts`, `POST /api/v1/runs/{id}/signal` |
| WS/SSE | `/apps/{slug}/ws`, `/apps/{slug}/sse`, `/ws/orchestrate/{app}/{ep}`, `/sse/orchestrate/{app}/{ep}` |
| Auth | `/api/v1/auth/*` (login, me, refresh, logout) |

### Python-owned (still on them-bridge)

| Domain | Routes |
|---|---|
| Runs (remaining) | context/{ctx}/artifacts, cancel, delete, bulk-delete |
| Applications (remaining) | export, import, restore, middleware-wirings, orchestrator test-llm/tts/voice |
| A2A server | `/a2a/*` |
| Health (bare) | `GET /health` |
| Legacy WS | `/ws/orchestrate/{name}` (single-segment, deprecated) |

---

## Current migration target

**Runs READ slice** — complete as of cf953cf.

All 5 GET runs endpoints are Go-owned: `/runs`, `/runs/stats`, `/runs/{id}`, `/runs/{id}/tasks`, `/runs/{id}/artifacts`.

Next: Runs writes (cancel, delete, bulk-delete).

---

## Known blockers / issues

- Python Temporal worker (`them-worker`) is still the primary orchestration engine. Go worker (`them-go-worker`) is registered but Python handles the main queue.
- Auth admin CRUD (users/roles/teams/permissions) is not exposed — was in Python `them-auth-service` which is removed. Needs Go implementation before user management is available again.
- `them-auth-service` source code remains in `auth_service/` but is not deployed.

---

## Deployment environments

| Environment | Compose files | Entry point |
|---|---|---|
| Local dev | `docker-compose.yml` + `docker-compose.dev.yml` | `http://localhost:8088` |
| Hetzner prod | `docker-compose.yml` + `docker-compose.hetzner.yml` | `https://them.avico78.com` |

Secrets: derived via HMAC-SHA256 from `secrets.local`. Run `./generate-env.sh` to regenerate `.env`.
