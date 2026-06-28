# ODIN — Claude Session Guide
# /opt/docker/odin · multi-agent orchestration platform
# Last updated: 2026-06-28

---

## Read These First

Before touching any code, read these docs if you haven't this session:

| Doc | When to read |
|---|---|
| `docs/INDEX.md` | Find the right doc fast |
| `docs/ARCHITECTURE.md` | Any time you touch app/ — how the orchestrator works |
| `docs/SCHEMA.md` | Touching models.py or writing queries |
| `docs/REDIS.md` | Touching anything that reads/writes Redis |
| `docs/ADAPTERS.md` | Adding/changing an agent transport |
| `docs/STATUS.md` | Know what's broken/pending before you start |
| `docs/LESSONS.md` | Before any judgment call — read what burned us before |

---

## This Project

Odin is a **multi-agent orchestration platform** — completely separate from Omni (`/opt/docker/omni-stack`).

It **shares containers only**: `omni-postgres` (DB: `odin`) and `omni-redis` (DB index: `1`).
Everything else is isolated: own schema, own credentials, own ports, own network attachment.

---

## Core Mental Model (memorize this)

Each enabled `odin.agents` row = **ONE LLM tool** named `agent__<slug>`.
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

This is Omni's `llm_service.py` agentic loop — the ONLY difference is adapters replace MCP tool execution.

---

## Scalability Design (multi-replica from day 1)

| State | Location | Replica-safe? |
|---|---|---|
| Token cache L1 | in-process dict per replica | No — each replica caches independently |
| Token cache L2 | Redis `odin:session:token:*` TTL 300s | Yes — shared |
| Rate limiting | Redis INCR `rl:odin:*` | Yes |
| Agent config cache | Redis `odin:agents:registry` + in-process | Yes — pub/sub invalidation |
| Orchestrator config | Redis `odin:orchestrators:{name}` TTL 600s | Yes — pub/sub invalidation |
| Run state | Postgres `odin.runs` | Yes |
| WS connections | in-process per replica | By design — Traefik sticky sessions |
| Replica heartbeat | Redis `odin:bridge:{INSTANCE_ID}:heartbeat` 30s TTL | Yes |

---

## Container Map

| Container | Role | Port | Source dir |
|---|---|---|---|
| `omni-postgres` | shared PG16 — DB: `odin` | 5432 | omni infra |
| `omni-pgbouncer` | shared pooler | 5432 | omni infra |
| `omni-redis` | shared Redis — **DB index 1** | 6379 | omni infra |
| `odin-auth-service` | Auth/IAM microservice | **8701** | `auth_service/` |
| `odin-bridge` | Orchestrator API + WS (replica 1) | **8001** | `app/` |
| `odin-bridge-2` | Replica 2 (`profiles: [replica]`) | **8001** | `app/` |

---

## Key Source Locations

| Concern | Location |
|---|---|
| Orchestrator agentic loop | `app/services/orchestrator_service.py` |
| Agent registry → NeutralTool list | `app/services/agent_registry.py` |
| Agent transport adapters | `app/adapters/` (base, omni_ws_adapter, a2a_adapter, factory) |
| Orchestrator WS endpoint | `app/routers/ws_orchestrator.py` |
| Dashboard WS (multiplexed channels) | `app/routers/ws_dashboard.py` |
| LLM providers (from Omni) | `app/services/providers/` |
| Token cache (L1+L2) | `app/services/token_cache.py` |
| Run recording | `app/services/run_recorder.py` |
| DB models | `app/models.py` |
| Config + env vars | `app/config.py` |
| DB schema source of truth | `db/001_schema.sql` |

---

## Git Workflow

```bash
git add <files>
git commit -m "description"
git push origin main
```

---

## Common Commands

```bash
docker compose ps
docker compose logs -f odin-bridge
docker compose restart odin-bridge
./scripts/init_db.sh           # create odin DB + apply schema
./scripts/run_tests.sh         # run tests

# Enable replica 2
docker compose --profile replica up -d odin-bridge-2

# DB access
docker exec omni-postgres psql -U odin -d odin
```

---

## Rules — Code

- **Never** query `auth_service.*` tables directly — use `app/services/auth_client.py` (HTTP to 8701)
- All Redis on **DB index 1**. Key prefixes: `odin:session:`, `rl:odin:`, `odin:agents:`, `odin:orchestrators:`, `odin:bridge:`, `odin:dash:`
- New agent transport → new file in `app/adapters/` + register in `factory.py` + doc in `docs/ADAPTERS.md`
- After every `app/` change: `./scripts/run_tests.sh` — zero failures allowed
- **Never** touch `/opt/docker/omni-stack/` from this project
- **Never** use Redis DB 0 (Omni's) or DB name `omni`
- `a2a_adapter.py` is a **STUB** — never set `transport='a2a'` in production data
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

**`auth_service` schema** — owned by `odin-auth-service` (port 8701). Never access directly from bridge.
Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

**`odin` schema** — owned by `odin-bridge`.
Tables: `llm_providers`, `config`, `agents`, `orchestrators`, `access_tokens`, `runs`, `run_steps`, `run_usage`, `audit_logs`

---

## Known State (2026-06-28)

- **Build phase:** 0+1 complete (skeleton + auth). Phases 2–7 pending.
- **Day-1 scope:** `omni_ws` adapter only; `a2a` is a stub; voice transport deferred; dashboard UI not started.
- **Single replica:** `odin-bridge-2` in compose with `profiles: [replica]`, not running yet.
- **Omni reuse:** `providers/`, `crypto.py`, `logger.py`, `auth_client.py` adapted from Omni. `auth_service/` copied verbatim (port/title changed only).
