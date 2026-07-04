# the-M — Claude Session Guide
# multi-agent orchestration platform
# Last updated: 2026-07-04

---

## Read These First

Before touching any code, read these docs if you haven't this session:

| Doc | When to read |
|---|---|
| `docs/INDEX.md` | Find the right doc fast |
| `docs/ARCHITECTURE.md` | Any time you touch `app/` — how the orchestrator works |
| `docs/SCHEMA.md` | Touching `models.py` or writing queries |
| `docs/REDIS.md` | Touching anything that reads/writes Redis |
| `docs/ADAPTERS.md` | Adding/changing an agent transport |
| `docs/STATUS.md` | Know what's broken/pending before you start |
| `docs/LESSONS.md` | Before any judgment call — read what burned us before |
| `scripts/tests/INDEX.md` | Before running or writing tests |

---

## This Project

**the-M** is a multi-agent orchestration platform. Fully isolated stack — own Postgres, own Redis, own Docker network.

Brand rules: UI/docs say **the-M**. Code identifiers use **them** / **THE_M_** (no exceptions).

---

## Core Mental Model (memorize this)

Each enabled `them.agents` row = **ONE LLM tool** named `agent__<slug>`.
The agent's `description` column is the tool description the LLM uses to decide when to call it.

```
User goal → WS /ws/orchestrate/{name} → load orchestrator config
         → build tool list from allowed agents (each = NeutralTool named agent__<slug>)
         → LLM agentic loop (≤ max_iterations)
               LLM picks tool(s) → route via adapter → stream result back → feed to LLM → loop
         → stream final answer to user
```

**Parallel calls:** when LLM returns multiple ToolCalls in one iteration, run with `asyncio.gather()`
bounded by `orchestrator.max_parallel_tools` and per-agent `max_concurrency` semaphore.

---

## Scalability Design (multi-replica from day 1)

| State | Location | Replica-safe? |
|---|---|---|
| Token cache L1 | in-process dict per replica | No — each replica caches independently |
| Token cache L2 | Redis `them:session:token:*` TTL 300s | Yes — shared |
| Rate limiting | Redis INCR `rl:them:*` | Yes |
| Agent config cache | Redis `them:agents:registry` + in-process | Yes — pub/sub invalidation |
| Orchestrator config | Redis `them:orchestrators:{name}` TTL 600s | Yes — pub/sub invalidation |
| Run state | Postgres `them.runs` | Yes |
| WS connections | in-process per replica | By design — Traefik sticky sessions |
| Replica heartbeat | Redis `them:bridge:{INSTANCE_ID}:heartbeat` 30s TTL | Yes |

---

## Container Map

| Container | Role | Port | Source dir |
|---|---|---|---|
| `them-postgres` | PG16 — DB: `them` | 5432 (internal) | bind mount `./data/them-postgres/pgdata` |
| `them-redis` | Redis DB 0 | 6379 (internal) | bind mount `./data/them-redis` |
| `them-auth-service` | Auth/IAM microservice | **8701** | `auth_service/` |
| `them-bridge` | Orchestrator API + WS (replica 1) | **8001** | `app/` |
| `them-bridge-2` | Replica 2 (`profiles: [replica]`) | **8001** | `app/` |
| `them-frontend` | Next.js dashboard | **3200** | `frontend/` |
| `mock-agent-*` | WS mock agents for testing | 9000 (internal) | `mock_agent/` |
| `vision-agent` | Vision/maps agent | 9100 (internal) | `agents/vision_agent/` |

---

## Key Source Locations

| Concern | Location |
|---|---|
| Orchestrator agentic loop | `app/services/orchestrator_service.py` |
| Agent registry → NeutralTool list | `app/services/agent_registry.py` |
| Agent transport adapters | `app/adapters/` (base, omni_ws_adapter, a2a_adapter, factory) |
| Orchestrator WS endpoint | `app/routers/ws_orchestrator.py` |
| Dashboard WS (multiplexed channels) | `app/routers/ws_dashboard.py` |
| LLM providers | `app/services/providers/` |
| Token cache (L1+L2) | `app/services/token_cache.py` |
| Run recording | `app/services/run_recorder.py` |
| DB models | `app/models.py` |
| Config + env vars | `app/config.py` |
| DB schema source of truth | `db/001_schema.sql` |
| Auth schema source of truth | `auth_service/SCHEMA.sql` |
| Frontend proxy route | `frontend/src/app/api/them/[...path]/route.ts` |
| Frontend auth cookies | `frontend/src/app/api/auth/` |

---

## Git Workflow

```bash
git add <files>
git commit -m "description"
git push origin main
```

---

## Common Commands

```powershell
# Stack
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d
docker compose -f docker-compose.yml -f docker-compose.local.yml ps
docker compose logs -f them-bridge

# DB init (run once after first up, or after wiping data/)
# Copy schema files then apply:
docker cp db/001_schema.sql them-postgres:/tmp/them_001_schema.sql
docker cp auth_service/SCHEMA.sql them-postgres:/tmp/them_auth_schema.sql
docker cp db/002_seed.sql them-postgres:/tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql

# DB access
docker exec -it them-postgres psql -U them -d them

# Run tests (cross-platform — Windows + Linux)
python scripts/tests/run_tests.py            # full suite
python scripts/tests/run_tests.py 01 02 03 04 15   # sanity only

# Secrets / .env (run before first up)
.\generate-env.ps1    # Windows
./generate-env.sh     # Linux/Mac

# Enable replica 2
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile replica up -d them-bridge-2
```

---

## Rules — Testing (when to run what)

See `scripts/tests/INDEX.md` for full test descriptions.

**Sanity (tests 01 02 03 04 15) — run after every `docker compose up` or deploy:**
```
python scripts/tests/run_tests.py 01 02 03 04 15
```
Takes ~15s. Confirms DB, Redis, auth service, bridge, and all containers are healthy.

**After touching `app/`:**
```
python scripts/tests/run_tests.py
```
Full suite, ~30s. Zero failures required before committing.

**Trigger map — which tests to run after changing what:**

| Changed | Run tests |
|---|---|
| `db/001_schema.sql` or `app/models.py` | 01 (DB schema) |
| `app/adapters/` | 07 (adapter factory) |
| `app/services/rate_limiter.py` or `token_cache.py` | 09 (rate limiter) |
| `app/services/run_recorder.py` or `orchestrator_service.py` | 10 (run recorder) |
| `app/routers/admin_agents.py` | 05 (agents CRUD) |
| `app/routers/admin_orchestrators.py` | 06 (orchestrators CRUD) |
| `app/routers/admin_tokens.py` | 08 (tokens CRUD) |
| `app/routers/ws_orchestrator.py` | 11 (WS orchestrate) |
| `app/routers/runs.py` | 12 (runs API) |
| `app/routers/ws_dashboard.py` or `dashboard_broadcaster.py` | 13 (dashboard WS) |
| Any infrastructure change | 15 (compose health) |
| Before a release / PR merge | Full suite + E2E (14, needs `ADMIN_JWT`) |

**E2E test (14) — needs a JWT:**
```
# Get a token first:
curl -s -X POST http://localhost:8701/auth/login -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python -c "import sys,json; print(json.load(sys.stdin)['access_token'])"

# Then run:
ADMIN_JWT=<token> python scripts/tests/run_tests.py 14
```

---

## Rules — Code

- **Never** query `auth_service.*` tables directly — use `app/services/auth_client.py` (HTTP to 8701)
- All Redis on **DB index 0** (we own the entire Redis instance). Key prefixes: `them:session:`, `rl:them:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`
- New agent transport → new file in `app/adapters/` + register in `factory.py` + doc in `docs/ADAPTERS.md`
- **Never** use DB name `odin` or schema `odin` — everything is `them`
- Naming: UI/docs = **the-M**, code identifiers = **them** / **THE_M_**
- Use Opus for architecture/planning decisions, Sonnet for coding and QA

---

## Rules — Documentation (mandatory)

- New Redis key → `docs/REDIS.md`
- New DB table or column → `docs/SCHEMA.md` + `db/001_schema.sql`
- New/changed flow → `docs/ARCHITECTURE.md`
- Bug fix or non-obvious behavior → `docs/LESSONS.md`
- Unresolved at session end → `docs/STATUS.md`
- Trust code over docs; always update the doc when they diverge

---

## Database Schemas — Quick Reference

**`auth_service` schema** — owned by `them-auth-service` (port 8701). Never access directly from bridge.
Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

**`them` schema** — owned by `them-bridge`.
Tables: `llm_providers`, `config`, `agents`, `orchestrators`, `access_tokens`, `runs`, `run_steps`, `run_usage`, `audit_logs`

**Credentials:** derived via HMAC-SHA256 from `secrets.local`. Re-run `.\generate-env.ps1` to regenerate `.env`.
DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`

---

## Known State (2026-07-04)

- **Stack:** fully deployed locally. All core containers healthy.
- **Users seeded:** `admin` / `admin123` (super_admin), `avi` / `avi123` (super_admin)
- **Agents seeded:** assistant, coder, researcher (all mock WS agents)
- **Orchestrators seeded:** `default` (claude-sonnet-4-6)
- **Dev login:** pre-filled in login page when `NODE_ENV=development`
- **`vision-agent`:** unhealthy — needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` in `.env`
- **Replica 2:** compose profile `replica`, not running by default
- **Git hooks:** not wired — planned as GitHub Actions (future)
- **Frontend URL:** http://localhost:3200
- **Bridge API:** http://localhost:8001
