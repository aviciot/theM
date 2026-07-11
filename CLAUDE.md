# the-M — Claude Session Guide
# multi-agent orchestration platform
# Last updated: 2026-07-07

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
| `docs/A2A_AGENTS.md` | Working with A2A test agents — start/stop, enable, test commands |
| `docs/A2A_REFERENCE.md` | A2A SDK v1.1.0 ground truth — Part types, AgentCard/Skill fields, wire format, platform gaps |
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
| `them-traefik` | Reverse proxy — single entry point, path-based routing, sticky LB | **8088** (host), 127.0.0.1:**8089** (dashboard) | `traefik/` |
| `them-postgres` | PG16 — DB: `them` | 5432 (internal) | bind mount `./data/them-postgres/pgdata` |
| `them-redis` | Redis DB 0 | 6379 (internal) | bind mount `./data/them-redis` |
| `them-auth-service` | Auth/IAM microservice | 8701 (internal) | `auth_service/` |
| `them-bridge` | Orchestrator API + WS (replica 1) | 8001 (internal) | `app/` |
| `them-bridge-2` | Replica 2 (`profiles: [replica]`) | 8001 (internal) | `app/` |
| `them-frontend` | Next.js dashboard | 3200 (internal) | `frontend/` |
| `vision-agent` | Vision/maps agent | 9100 (internal) | `agents/vision_agent/` |
| `a2a-echo` | A2A v1.0 echo test agent (`profiles: [test-agents]`) | 9200 (internal) | `agents/a2a_echo/` |
| `a2a-slow` | A2A v1.0 slow test agent (5s delay) (`profiles: [test-agents]`) | 9201 (internal) | `agents/a2a_slow/` |
| `a2a-stream` | A2A v1.0 streaming test agent (`profiles: [test-agents]`) | 9202 (internal) | `agents/a2a_stream/` |

---

## Key Source Locations

| Concern | Location |
|---|---|
| Orchestrator agentic loop | `app/services/task_runner.py` |
| Agent registry → NeutralTool list | `app/services/agent_registry.py` |
| Agent transport adapters | `app/adapters/` (base, a2a_async_adapter, factory) |
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
docker cp db/003_phase8.sql them-postgres:/tmp/them_003_phase8.sql
docker cp db/004_phase9.sql them-postgres:/tmp/them_004_phase9.sql
docker cp db/005_phase10.sql them-postgres:/tmp/them_005_phase10.sql
docker cp db/006_phase11.sql them-postgres:/tmp/them_006_phase11.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_003_phase8.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_004_phase9.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_005_phase10.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_006_phase11.sql

# DB access
docker exec -it them-postgres psql -U them -d them

# Run tests (cross-platform — Windows + Linux)
python scripts/tests/run_tests.py            # full suite
python scripts/tests/run_tests.py 01 02 03 04 15   # sanity only

# Multi-turn behavioral test (runs inside bridge; auto-fetches JWT)
docker cp scripts/test_multiturn.py them-bridge:/tmp/test_multiturn.py
docker exec them-bridge python3 /tmp/test_multiturn.py

# Secrets / .env (run before first up)
.\generate-env.ps1    # Windows
./generate-env.sh     # Linux/Mac

# Enable replica 2
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile replica up -d them-bridge-2

# A2A test agents (profile: test-agents)
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile test-agents up -d
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile test-agents ps

# Enable A2A agents in DB (required before using them in orchestrator)
docker exec them-postgres psql -U them -d them -c "UPDATE them.agents SET enabled=true WHERE slug IN ('a2a_echo','a2a_slow','a2a_stream');"

# Disable A2A agents (when stopping test-agents profile)
docker exec them-postgres psql -U them -d them -c "UPDATE them.agents SET enabled=false WHERE slug IN ('a2a_echo','a2a_slow','a2a_stream');"

# Bust Redis cache after enabling/disabling agents or changing a2a_test orchestrator
docker exec them-redis redis-cli DEL them:orchestrators:a2a_test them:agents:registry

# Rebuild A2A agents after code change (no volume mount — must rebuild)
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile test-agents build a2a-echo a2a-slow a2a-stream
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile test-agents up -d a2a-echo a2a-slow a2a-stream

# Test A2A adapter directly (no LLM) from inside bridge container
docker exec them-bridge python3 -c "
import asyncio, sys; sys.path.insert(0, '/app')
from app.adapters.a2a_async_adapter import A2aAsyncAdapter
async def t(slug, url, msg):
    adapter = A2aAsyncAdapter(agent_slug=slug, endpoint_url=url, auth_token_encrypted=None, poll_interval=1.0, max_poll_seconds=30.0)
    async for e in adapter.stream_invoke({'message': msg}, timeout=30.0): print(e)
asyncio.run(t('a2a_echo', 'http://a2a-echo:9200', 'hello'))
"
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
| `app/services/rate_limiter.py` or `token_cache.py` | 08 09 (rate limiter + token cache) |
| `app/services/run_recorder.py` or `app/services/task_runner.py` | 10 (run recorder + task runner) |
| `app/routers/admin_agents.py` | 05 (agents CRUD) |
| `app/routers/admin_orchestrators.py` | 06 (orchestrators CRUD) |
| `app/routers/admin_tokens.py` | 08 09 (tokens CRUD + cache) |
| `app/routers/ws_orchestrator.py` | 11 (WS orchestrate) |
| `app/routers/runs.py` | 12 (runs API) |
| `app/routers/ws_dashboard.py` or `dashboard_broadcaster.py` | 13 (dashboard WS) |
| Any infrastructure change | 15 (compose health) |
| `agents/a2a_*`, docker-compose test-agents profile | 16 (A2A agent structure) |
| `app/services/memory_service.py`, `db/003_phase8.sql` (memory columns) | 17 (context summarization memory) |
| `app/routers/a2a_server.py` (orch-as-agent sections), `app/models.py` (a2a_exposed/budget_tokens) | 18 (orchestrator-as-agent) |
| `app/edges/` | 19 (pluggable edge adapters) |
| `docker-compose.yml` labels, `traefik/traefik.yml`, `docker-compose.local.yml` | 20 (Traefik routing + multi-replica) |
| `app/routers/a2a_server.py`, `app/services/task_store.py`, `app/services/token_cache.py`, `db/004_phase9.sql` | 21 (A2A Phase 9 hardening) |
| `app/routers/admin_applications.py`, `app/routers/apps.py`, `app/main.py`, `frontend/src/app/admin/applications/`, `frontend/src/lib/api.ts`, `frontend/src/components/Sidebar.tsx` | 22 (applications CRUD + entry points) |
| `app/services/task_runner.py` (`_ensure_agent_skills`, `_CARD_TTL_SECONDS`), `agents/docu_writer/`, `db/007_docu_stack.sql` | 23 (A2A skill auto-discovery) |
| `db/007_docu_stack.sql` code_agent endpoint/token | 24 (code_agent live) |
| `agents/docu_writer/main.py`, `app/adapters/a2a_async_adapter.py`, `app/adapters/factory.py`, `app/services/task_runner.py` (typed A2A), `db/007_docu_stack.sql` | 25 (true A2A typed input) |
| `app/services/task_runner.py` (history), `app/models.py` (history_window), `app/routers/admin_orchestrators.py` | 10 + MT (multi-turn behavioral) |
| Before a release / PR merge | Full suite + E2E (14, needs `ADMIN_JWT`) + MT |

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
- **A2A work** (adapters, agents, agent cards, typed parts, orchestrator↔agent wiring) → invoke `/a2a` skill first — it loads the full SDK reference and platform gap list

---

## Rules — Documentation (mandatory)

- New Redis key → `docs/REDIS.md`
- New DB table or column → `docs/SCHEMA.md` + `db/001_schema.sql`
- New/changed flow → `docs/ARCHITECTURE.md`
- Bug fix or non-obvious behavior → `docs/LESSONS.md`
- Unresolved at session end → `docs/STATUS.md`
- Trust code over docs; always update the doc when they diverge

## Rules — Tests (mandatory, non-negotiable)

- **Every code change that touches `app/` MUST have a corresponding test** — new behavior = new test, changed behavior = updated test
- **After every change run the full suite** (`python3.12 scripts/tests/run_tests.py`) — zero new failures allowed before committing
- **`scripts/tests/INDEX.md` MUST be updated** whenever a test is added, changed, or its coverage expands — description, type, trigger map
- **`scripts/tests/run_tests.py` is the canonical runner** — standalone `.sh`/`.py` test files must mirror the same checks; if they diverge, fix both
- **CLAUDE.md trigger map MUST be kept in sync** with `INDEX.md` — if you add/change a test, update both
- Never commit with a test regression — if a test breaks, fix the code or the test before pushing; do not skip or delete tests to make the suite pass

---

## Database Schemas — Quick Reference

**`auth_service` schema** — owned by `them-auth-service` (port 8701). Never access directly from bridge.
Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

**`them` schema** — owned by `them-bridge`.
Tables: `llm_providers`, `config`, `agents`, `orchestrators`, `access_tokens`, `runs`, `run_steps`, `run_usage`, `audit_logs`, `tasks`, `artifacts`, `task_messages`, `applications`

**Credentials:** derived via HMAC-SHA256 from `secrets.local`. Re-run `.\generate-env.ps1` to regenerate `.env`.
DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`

---

## Known State (2026-07-07)

- **Stack:** fully deployed locally. All core containers healthy.
- **Users seeded:** `admin` / `admin123` (super_admin), `avi` / `avi123` (super_admin)
- **Agents seeded:** assistant, coder, researcher (mock WS) + a2a_echo, a2a_slow, a2a_stream (A2A test agents)
- **Orchestrators seeded:** `default` (claude-sonnet-4-6), `a2a_test` (haiku, all 3 A2A agents)
- **A2A test agents:** running (`--profile test-agents`), all enabled in DB, ready to use via `a2a_test` orchestrator
- **Phase 9 applied:** `db/004_phase9.sql` migrated to running Postgres (tasks.user_id + them.applications table)
- **ANTHROPIC_API_KEY:** set in `.env` — bridge picks it up on restart
- **Dev login:** pre-filled in login page when `NODE_ENV=development`
- **`vision-agent`:** unhealthy — needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` in `.env`
- **Replica 2:** compose profile `replica`, not running by default
- **Git hooks:** not wired — planned as GitHub Actions (future)
- **Frontend URL:** http://localhost:8088
- **Bridge API (direct, internal):** http://localhost:8001 — use http://localhost:8088 from browser
- **Traefik dashboard:** http://localhost:8089
