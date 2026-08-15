# the-M — Current Status
# Last updated: 2026-08-15
# HEAD: 888861b

---

## What is running

| Container | Source | Port | Status |
|---|---|---|---|
| `them-traefik` | traefik:v3.6 | 8088 (host) | Running |
| `them-postgres` | postgres:16 | 5432 (internal) | Running |
| `them-redis` | redis:7 | 6379 (internal) | Running |
| `them-auth-go` | `go/cmd/auth-server` | 8703 (internal) | Running — sole auth service |
| `them-bridge` | `app/` (Python FastAPI) | 8001 (internal) | Running — remaining Python routes |
| `them-go-bridge` | `go/cmd/them` | 8002 (internal) | Running — Go routes (profile: go) |
| `them-frontend` | `frontend/` (Next.js) | 3200 (internal) | Running |
| `them-worker` | Python Temporal worker | — | Running |
| `them-go-bridge-2` | Go replica | 8002 (internal) | Running (profile: temporal) |
| `them-go-worker` | Go Temporal worker | — | Running |
| `them-security-agent` | `agents/security_scanner` | 9500 | Running (profile: security) |
| `vision-agent` | `agents/vision_agent` | 9100 | Running |
| A2A test agents | `agents/a2a_*` | 9200-9202 | Available (profile: test-agents) |

**Removed:** `them-auth-service` (Python auth — removed August 2026, replaced by `them-auth-go`)

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
| Runs | `GET /api/v1/runs`, `GET /api/v1/runs/{id}`, `POST /api/v1/runs/{id}/signal` |
| WS/SSE | `/apps/{slug}/ws`, `/apps/{slug}/sse`, `/ws/orchestrate/{app}/{ep}`, `/sse/orchestrate/{app}/{ep}` |
| Auth | `/api/v1/auth/*` (login, me, refresh, logout) |

### Python-owned (still on them-bridge)

| Domain | Routes |
|---|---|
| Runs (remaining) | stats, contexts, tasks, artifacts, cancel, delete, bulk-delete |
| Applications (remaining) | export, import, restore, middleware-wirings, orchestrator test-llm/tts/voice |
| A2A server | `/a2a/*` |
| Health (bare) | `GET /health` |
| Legacy WS | `/ws/orchestrate/{name}` (single-segment, deprecated) |

---

## Current migration target

**Agents Store slice** — complete as of 888861b.

Next: Runs read/audit tail — port `/stats`, `/contexts`, `/{id}/tasks`, `/{id}/artifacts` to Go.

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
