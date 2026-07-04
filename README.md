# the-M — Multi-Agent Orchestration Platform

> Route any user goal through a pool of AI agents. Each agent is a tool. The LLM decides which ones to call, in what order, in parallel — then streams the answer back.

---

## What is the-M?

**the-M** (them) is a production-grade multi-agent orchestration platform built on a clean agentic loop:

```
User message
    ↓
WebSocket /ws/orchestrate/{name}
    ↓
Load orchestrator config (system prompt, allowed agents, limits)
    ↓
Build LLM tool list  ←  each enabled agent = one NeutralTool named agent__<slug>
    ↓
Agentic loop (≤ max_iterations)
    LLM picks tools → parallel fan-out via adapters → stream results → feed back to LLM
    ↓
Stream final answer to client
```

Agents are transport-agnostic. Today: WebSocket (`omni_ws`), A2A sync (`a2a`), A2A async (`a2a_async`). New transports: add an adapter.

---

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │              them-network                │
                    │                                          │
  Browser / Client  │  ┌──────────────┐   ┌────────────────┐  │
  ───────────────►  │  │ them-bridge  │   │them-auth-svc   │  │
  WS + REST API     │  │  (FastAPI)   │◄──│  (FastAPI)     │  │
                    │  │  port 8001   │   │  port 8701     │  │
                    │  └──────┬───────┘   └───────┬────────┘  │
                    │         │                   │            │
                    │  ┌──────▼───────────────────▼────────┐  │
                    │  │        them-postgres (PG 16)       │  │
                    │  │  schema: them  +  auth_service     │  │
                    │  └────────────────────────────────────┘  │
                    │  ┌────────────────────────────────────┐  │
                    │  │         them-redis (Redis 7)        │  │
                    │  │  token cache · rate limits · pubsub │  │
                    │  └────────────────────────────────────┘  │
                    │                                          │
                    │  ┌────────────────────────────────────┐  │
                    │  │    them-frontend (Next.js 16)       │  │
                    │  │         port 3200                   │  │
                    │  └────────────────────────────────────┘  │
                    └─────────────────────────────────────────┘
```

**Fully isolated.** Zero dependency on any external stack — own Postgres, own Redis, own network. All data bind-mounted under `data/` and survives `docker compose down --build`.

---

## Stack

| Layer | Technology |
|---|---|
| Orchestrator API | Python 3.13 · FastAPI · asyncpg · SQLAlchemy async |
| Auth service | Python 3.11 · FastAPI · bcrypt · JWT (HS256) |
| Database | PostgreSQL 16 |
| Cache / PubSub | Redis 7 · AOF persistence |
| Frontend | Next.js 16 · TypeScript · Tailwind CSS 4 · Zustand |
| Container | Docker Compose · Traefik-ready labels |

---

## Features

- **Agentic loop** — LLM drives tool selection over multiple iterations
- **Parallel fan-out** — multiple tool calls per iteration via `asyncio.gather()`, bounded by `max_parallel_tools` and per-agent `max_concurrency`
- **WebSocket streaming** — tokens stream to the client in real time; tool events visible as they happen
- **Run recording** — every run, step, token count, and cost written to Postgres
- **Agent registry** — CRUD API, auth tokens Fernet-encrypted at rest, L1+L2 Redis cache with pub/sub invalidation
- **Two auth paths** — opaque Bearer tokens for WS orchestration; JWT for admin REST API
- **Rate limiting** — Redis INCR fixed-window per user per hour
- **Dashboard WS** — multiplexed channels (`runs`, `agents`, `metrics`) via Redis pub/sub
- **Playground UI** — split-pane chat + real-time trace pane

---

## Quick Start

### Prerequisites
- Docker Desktop (Windows/Mac) or Docker Engine (Linux)
- Python 3.x (for secret generation and test runner)

### 1. Clone and generate secrets

```powershell
# Windows
git clone <repo>
cd odin
.\generate-env.ps1        # creates .env from secrets.local
```
```bash
# Linux / Mac
git clone <repo>
cd odin
./generate-env.sh         # creates .env from secrets.local
```

### 2. Start the stack

```bash
# Local dev (no Traefik required)
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
```

### 3. Initialize the database

```powershell
docker cp db/001_schema.sql them-postgres:/tmp/them_001_schema.sql
docker cp auth_service/SCHEMA.sql them-postgres:/tmp/them_auth_schema.sql
docker cp db/002_seed.sql them-postgres:/tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql
```

### 4. Run the tests

```bash
python scripts/tests/run_tests.py
```

### 5. Open the dashboard

```
http://localhost:3200
```

Login: `admin` / `admin123` (pre-filled in dev mode)

---

## Container Map

| Container | Role | Port |
|---|---|---|
| `them-postgres` | PostgreSQL 16 | internal |
| `them-redis` | Redis 7 (AOF) | internal |
| `them-auth-service` | Auth / IAM microservice | 8701 (internal) |
| `them-bridge` | Orchestrator API + WebSocket | **8001** |
| `them-frontend` | Next.js dashboard | **3200** |
| `mock-agent-*` | Mock WS agents for testing | internal |
| `a2a-echo` / `a2a-slow` / `a2a-stream` | A2A v1.0 test agents (`--profile test-agents`) | internal |

---

## API Reference

### Auth service (port 8701)
| Method | Path | Description |
|---|---|---|
| POST | `/auth/login` | Login → sets `them_access_token` + `them_refresh_token` cookies |
| POST | `/auth/refresh` | Refresh access token |
| POST | `/auth/logout` | Clear cookies, blacklist tokens |
| GET | `/auth/me` | Current user from JWT |

### Bridge (port 8001)
| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` `/health/live` `/health/ready` | — | Health checks |
| WS | `/ws/orchestrate/{name}` | Bearer token | Run an orchestrator |
| WS | `/ws/dashboard` | JWT | Live event stream |
| CRUD | `/api/v1/admin/agents` | JWT | Agent registry |
| CRUD | `/api/v1/admin/orchestrators` | JWT | Orchestrator configs |
| CRUD | `/api/v1/admin/tokens` | JWT | Access token management |
| GET | `/api/v1/runs` | JWT | Run history + stats |

### WebSocket orchestration protocol
```jsonc
// Client connects: ws://host:8001/ws/orchestrate/{name}?token=<bearer>
// Client sends:
{ "content": "Summarize last week's transactions" }

// Server streams:
{ "type": "ready", "orchestrator": "default" }
{ "type": "tool_start", "tool": "agent__assistant", "iteration": 1 }
{ "type": "token", "text": "Based on the data..." }
{ "type": "tool_done", "tool": "agent__assistant", "duration_ms": 1240 }
{ "type": "done", "run_id": "...", "total_tokens": 1820, "iterations": 2 }
```

---

## Project Structure

```
odin/
├── app/                        # them-bridge (FastAPI)
│   ├── adapters/               # Agent transport layer
│   │   ├── base.py             # AgentAdapter ABC + AdapterEvent
│   │   ├── omni_ws_adapter.py  # WebSocket transport
│   │   ├── a2a_adapter.py      # A2A sync JSON-RPC transport
│   │   ├── a2a_async_adapter.py # A2A async (submit → poll/SSE)
│   │   └── factory.py          # Transport → adapter routing
│   ├── routers/                # API endpoints
│   │   ├── ws_orchestrator.py  # /ws/orchestrate/{name}
│   │   ├── ws_dashboard.py     # /ws/dashboard
│   │   ├── admin_agents.py
│   │   ├── admin_orchestrators.py
│   │   ├── admin_tokens.py
│   │   └── runs.py
│   └── services/
│       ├── orchestrator_service.py   # Agentic loop
│       ├── agent_registry.py         # L1+L2 cached agent list
│       ├── token_cache.py            # Bearer token validation
│       ├── rate_limiter.py           # Redis INCR rate limiting
│       ├── run_recorder.py           # Postgres run logging
│       └── dashboard_broadcaster.py  # Redis pub/sub events
├── auth_service/               # them-auth-service (FastAPI)
├── frontend/                   # them-frontend (Next.js 16)
│   └── src/app/
│       ├── login/              # Login page
│       ├── dashboard/          # Command center
│       ├── agents/             # Agent registry view
│       ├── runs/               # Run history
│       └── admin/              # Orchestrators, tokens, playground
├── agents/                     # Optional specialist agents
│   ├── vision_agent/
│   ├── a2a_echo/               # A2A v1.0 echo test agent (profile: test-agents)
│   ├── a2a_slow/               # A2A v1.0 slow test agent (5s delay)
│   └── a2a_stream/             # A2A v1.0 streaming test agent (word-by-word artifacts)
├── mock_agent/                 # Lightweight WS mock agents for testing
├── postgres/init/              # SQL auto-run on first Postgres boot
├── redis/config/               # Redis config (AOF, memory limits)
├── db/                         # Schema DDL + seed data
│   ├── 001_schema.sql
│   └── 002_seed.sql
├── data/                       # Bind-mounted persistent data (git-ignored)
│   ├── them-postgres/pgdata/
│   ├── them-redis/
│   └── them-logs/
├── scripts/
│   └── tests/
│       ├── run_tests.py        # Cross-platform test runner (Windows + Linux)
│       └── INDEX.md            # Test index — what each test covers
├── docs/                       # Architecture, schema, Redis, lessons learned
│   └── INDEX.md                # Doc index — what each doc covers + update triggers
├── generate-env.ps1            # Secret derivation (Windows)
├── generate-env.sh             # Secret derivation (Linux/Mac)
├── secrets.local.example       # Template for secrets.local
├── docker-compose.yml          # Production compose (Traefik-ready)
└── docker-compose.local.yml    # Local dev override (no Traefik)
```

---

## Scalability

the-M is multi-replica from day one:

| State | Where | Replica-safe |
|---|---|---|
| Token cache L1 | In-process per replica | Each replica caches independently |
| Token cache L2 | Redis `them:session:token:*` TTL 300s | Yes — shared |
| Rate limiting | Redis INCR `rl:them:*` | Yes |
| Agent registry | Redis `them:agents:registry` + pub/sub | Yes — invalidated on write |
| Orchestrator config | Redis `them:orchestrators:{name}` TTL 600s | Yes |
| Run state | Postgres `them.runs` | Yes |
| WS connections | In-process per replica | By design — Traefik sticky sessions |

Enable replica 2:
```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile replica up -d them-bridge-2
```

---

## Testing

```bash
# Full suite — cross-platform, 61+ structural tests (test_16 adds A2A agent checks)
python scripts/tests/run_tests.py

# Sanity only (after docker compose up) — ~15s
python scripts/tests/run_tests.py 01 02 03 04 15

# E2E (requires admin JWT)
ADMIN_JWT=<token> python scripts/tests/run_tests.py 14
```

See `scripts/tests/INDEX.md` for the full test index.

---

## Environment Variables

All secrets are derived from a single master passphrase in `secrets.local`. Run `.\generate-env.ps1` (Windows) or `./generate-env.sh` (Linux) to generate `.env`.

| Variable | Description |
|---|---|
| `THE_M_DB_PASSWORD` | Postgres password (derived) |
| `THE_M_SECRET_KEY` | Bridge signing key (derived) |
| `THE_M_JWT_SECRET` | Auth service JWT key (derived) |
| `THE_M_REDIS_PASSWORD` | Redis password (optional) |
| `ANTHROPIC_API_KEY` | LLM provider key (add manually) |
| `THE_M_HOSTNAME` | Traefik hostname for bridge (prod) |
| `THE_M_UI_HOSTNAME` | Traefik hostname for frontend (prod) |

---

## Adding an Agent

1. POST to `/api/v1/admin/agents`:
```json
{
  "slug": "my_agent",
  "display_name": "My Agent",
  "description": "What this agent does — the LLM reads this to decide when to call it",
  "transport": "omni_ws",
  "endpoint_url": "ws://my-agent-host:9000/ws",
  "auth_token": "plaintext-token-stored-encrypted",
  "timeout_seconds": 30,
  "max_concurrency": 3
}
```

2. Add the agent's ID to an orchestrator's `allowed_agent_ids`.

3. Connect: `ws://localhost:8001/ws/orchestrate/{orchestrator_name}?token=<bearer>`

---

## License

© 2026 Avi Cohen. All rights reserved.
