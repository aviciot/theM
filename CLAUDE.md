# the-M — Claude Session Guide
# multi-agent orchestration platform
# Last updated: 2026-08-15

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
| `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md` | Docker, deployment, environment, or container recreation work |

For Go work, also read `go/CLAUDE.md` — it governs everything under `go/`.

**Before Docker, deployment, environment, or container recreation work, read `docs/architecture-v2/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md`. Never commit or print real secrets.**

---

## This Project

**the-M** is a multi-agent orchestration platform. Fully isolated stack — own Postgres, own Redis, own Docker network.

Brand rules: UI/docs say **the-M**. Code identifiers use **them** / **THE_M_** (no exceptions).
DB name and schema: **them** — never `odin`.

---

## Migration Goal

**The long-term goal is a complete migration from Python to Go.**

Migration order:
1. Bridge (Python `app/`) → Go (`go/`) — the main API, WS, SSE, admin, run recording
2. Auth service (`auth_service/`) → Go
3. Temporal worker/orchestration/LLM layer → Go
4. Remove Python entirely

**One focused subsystem per task.** Do not migrate multiple subsystems in a single session.

Current state: Agents Store slice complete (agents CRUD + discover/test/security-scan + Go auth). See `docs/architecture-v2/CURRENT.md` for exact state and next steps. See `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` for route ownership.

---

## Model Selection

- **Opus** — architecture decisions, migration planning, wave scoping, complex trade-offs
- **Sonnet** — implementation, testing, debugging, routine changes

---

## Long Answers

Long explanations, detailed reviews, analysis reports, and migration plans must be written to Markdown files under `docs/architecture-v2/`. Return only the file path and a one-paragraph summary in chat.

---

## Workflow

Every task follows this order:

1. **Plan** — read relevant docs, design the change, confirm scope before writing code
2. **Implement** — one focused subsystem; do not widen scope mid-task
3. **Test** — run the applicable test suite; zero new failures before commit
4. **Commit** — commit all changed files together with a clear message
5. **Report** — return a short summary: files changed, tests passed, commit hash

---

## Session Lifecycle — Mandatory

Do not wait until context quality has already degraded.

**Prepare a handover when any of these triggers occur:**
- the current focused task is complete and tested
- a new subsystem or migration wave is about to begin
- roughly 5–8 meaningful commits have been created in the session
- context reliability is uncertain (re-reading the same files, conflicting statements, forgetting constraints)
- a major architecture decision is next

**Handover procedure:**
1. Stop implementation.
2. Finish and test the current focused task only — do not widen scope.
3. Commit all safe changes.
4. Push if credentials are available (`git push origin main`).
5. Update `docs/architecture-v2/CURRENT.md` with:
   - current HEAD (`git log --oneline -1`)
   - deployment state
   - current migration slice and what was completed
   - next recommended task
   - known blockers
   - any new hard constraints
   Do NOT create a new `NEXT_SESSION_*.md` file — always update CURRENT.md.
6. Recommend closing the current Claude session and opening a new one.
7. Return the exact startup commands and first prompt for the next session.

**Do not begin the next subsystem in the same session.**

---

## Git Rules

```bash
git add <files>
git commit -m "description"
git push origin main
```

- Commit only files relevant to the current task — do not use `git add .` or `git add -A`
- Do not push unless credentials are already available
- Do not amend previous commits — create a new commit
- Do not skip hooks (`--no-verify`)

---

## Secrets and Deployment State

- Secrets are derived via HMAC-SHA256 from `secrets.local` — re-run `./generate-env.sh` (Linux/Mac) or `.\generate-env.ps1` (Windows) to regenerate `.env`
- Never commit `.env` or `secrets.local`
- DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`
- All Redis on DB index 0. Key prefixes: `them:session:`, `rl:them:`, `them:agents:`, `them:orchestrators:`, `them:bridge:`, `them:dash:`
- Never use DB name `odin` or schema `odin`

---

## Tenant-Aware Design

Every session, run, rate-limit, and runtime gate is scoped to an Application (the tenant boundary).
Application ID must flow through every new feature:
- DB queries must include `application_id` or `entry_point_id` as appropriate
- Redis keys for per-app state must include the app ID or slug in the key
- Rate limiting and session caps must be applied per-app, not globally
- New routes under `/apps/{slug}/` inherit the app slug from the path

---

## Rules — Code

- **Never** query `auth_service.*` tables directly — use `app/services/auth_client.py` (HTTP to 8701) from Python, or `internal/auth/` from Go
- **Never** use DB name `odin` or schema `odin` — everything is `them`
- New agent transport → new file in `app/adapters/` + register in `factory.py` + doc in `docs/ADAPTERS.md`
- **A2A work** (adapters, agents, agent cards, typed parts, orchestrator↔agent wiring) → invoke `/a2a` skill first — it loads the full SDK reference and platform gap list
- Work under `go/` must also follow `go/CLAUDE.md`

---

## Rules — Documentation (mandatory)

- New Redis key → `docs/REDIS.md`
- New DB table or column → `docs/SCHEMA.md` + `db/001_schema.sql`
- New/changed flow → `docs/ARCHITECTURE.md`
- Bug fix or non-obvious behavior → `docs/LESSONS.md`
- Unresolved at session end → `docs/STATUS.md`
- Trust code over docs; always update the doc when they diverge

---

## Rules — Testing (mandatory, non-negotiable)

- **Every code change that touches `app/` or `go/` MUST have a corresponding test** — new behavior = new test, changed behavior = updated test
- **After every change run the full suite** — zero new failures allowed before committing
- **`scripts/tests/INDEX.md` MUST be updated** whenever a Python test is added, changed, or its coverage expands
- **`go/TEST_INDEX.md` MUST be updated** whenever a Go test is added, changed, or its coverage expands
- **CLAUDE.md trigger maps MUST be kept in sync** with their respective INDEX.md files
- Never commit with a test regression — fix the code or the test; do not skip or delete tests

### Python test runner

```bash
# MUST use python3.12 — system python3 is 3.6 and silently breaks all docker calls
python3.12 scripts/tests/run_tests.py            # full suite
python3.12 scripts/tests/run_tests.py 01 02 03 04 15   # sanity only (~15s)
```

Expected clean result: N passed, 0 failed, ≤5 skipped

Skips are legitimate env gaps — not failures:
- `structlog`/`fastapi` missing on host (tests 07/19 import checks) → skip, run fine in CI
- `ADMIN_JWT` not set (test 14 e2e) → set via `ADMIN_JWT=<token> python3.12 ...`
- `code_agent` unreachable (test 24) → external service, expected skip

### Python trigger map — which tests to run after changing what

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
| `docker-compose.yml` labels, `traefik/traefik.yml`, `docker-compose.dev.yml` | 20 (Traefik routing + multi-replica) |
| `app/routers/a2a_server.py`, `app/services/task_store.py`, `app/services/token_cache.py`, `db/004_phase9.sql` | 21 (A2A Phase 9 hardening) |
| `app/routers/admin_applications.py`, `app/routers/apps.py`, `app/main.py`, `app/models.py` (EntryPoint), `frontend/src/app/admin/applications/`, `frontend/src/lib/api.ts` | 22 27 + `scripts/test_multi_ep.py` (inside them-bridge) |
| `app/temporal/loaders.py` | 28 (loaders resolution) |
| `db/014_app_orchestrators.sql`, `app/models.py` (AppOrchestrator), `app/routers/admin_applications.py` (_flush_orch_caches) | 01 29 (app_orchestrators migration + model) |
| `app/services/app_compiler.py`, `db/018_graph_compiler.sql`, `app/routers/admin_applications.py` (graph/export/import/restore), `frontend/src/app/admin/applications/page.tsx` (handleSave graph payload) | 27 30 (canvas rules + graph compiler) |
| `app/services/session_manager.py`, `app/routers/ws_orchestrator.py` (session wiring), `app/routers/apps.py` (session wiring), `app/main.py` (_pod_heartbeat_loop) | 31 (session context manager) |
| `app/routers/admin_monitoring_config.py`, `frontend/src/app/admin/settings/page.tsx` (Monitoring tab), `frontend/src/app/admin/applications/page.tsx` (SessionsView heatmap) | 32 (monitoring config CRUD + heatmap thresholds) |
| `app/services/runtime_manager.py`, `app/routers/apps.py` (runtime_gate + control listener), `app/routers/ws_orchestrator.py` (runtime_gate + control listener), `app/routers/admin_sessions.py` (disconnect endpoint), `app/services/session_manager.py` (signal_disconnect), `db/022_runtime_limits.sql`, `app/models.py` (EntryPoint.max_concurrent_sessions), `app/routers/admin_applications.py` (EntryPointIn/Out.max_concurrent_sessions), `frontend/src/lib/api.ts` (disconnectSession), `frontend/src/app/admin/applications/page.tsx` (SessionsView terminate/limits) | 33 (runtime management layer — session cap, terminate, EP limits) |
| `db/023_app_runtime.sql`, `app/models.py` (Application.runtime_config), `app/routers/admin_applications.py` (AppRuntimeConfig, PUT /{app_id}/runtime), `app/services/runtime_manager.py` (app gate: blocked tokens/users, app rate limit, soft app cap), `app/routers/apps.py` (app_runtime + token_hash + hashlib.sha256), `app/routers/ws_orchestrator.py` (ep_max_concurrent/app_runtime/token_hash=None), `docs/REDIS.md` (rl:them:app:), `frontend/src/lib/api.ts` (AppRuntimeConfig, getAppRuntime, putAppRuntime), `frontend/src/app/admin/applications/page.tsx` (RuntimeView, Runtime button, onRuntime) | 34 (application runtime layer — blocked tokens/users, app rate limit, soft session cap, RuntimeView UI) |
| `db/024_ep_queue.sql`, `app/models.py` (EntryPoint.queue_timeout_seconds/queue_message), `app/services/app_compiler.py` (queue fields in compile_graph/export_graph), `app/routers/admin_applications.py` (EntryPointIn/Out queue fields), `app/services/runtime_manager.py` (RuntimeQueueFull, ep_gate_try, queue params), `app/routers/apps.py` (RuntimeQueueFull/ep_gate_try import, waiting message, retry loop), `frontend/src/lib/api.ts` (queue fields on EntryPoint), `frontend/src/app/admin/applications/page.tsx` (EP builder queue panel, saveEpLimit removed) | 35 (EP queue config — per-EP wait queue with timeout and custom message) |
| `app/services/task_runner.py` (`_ensure_agent_skills`, `_CARD_TTL_SECONDS`), `agents/docu_writer/`, `db/007_docu_stack.sql` | 23 (A2A skill auto-discovery) |
| `db/007_docu_stack.sql` code_agent endpoint/token | 24 (code_agent live) |
| `agents/docu_writer/main.py`, `app/adapters/a2a_async_adapter.py`, `app/adapters/factory.py`, `app/services/task_runner.py` (typed A2A), `db/007_docu_stack.sql` | 25 (true A2A typed input) |
| `agents/security_scanner/`, `app/routers/admin_agents.py` (security-scan endpoint), `app/routers/ws_dashboard.py` (agent: channel), `app/services/dashboard_broadcaster.py` (scan helpers), `db/009_security_scan.sql`, `frontend/src/app/admin/agents/page.tsx` (scan UI) | 26 (security scan) |
| `app/services/task_runner.py` (history), `app/models.py` (history_window), `app/routers/admin_orchestrators.py` | 10 + MT (multi-turn behavioral) |
| `app/temporal/activities.py`, `app/temporal/workflows.py`, `app/temporal/serde.py` | Full suite + `scripts/test_temporal_workflow.py` (inside them-worker) + **restart them-worker** |
| `app/temporal/bridge_client.py`, `app/routers/ws_orchestrator.py` (Temporal path) | 10 11 + `scripts/test_temporal_workflow.py` + **restart them-worker** |
| `app/routers/runs.py` (signal endpoint) | 12 + `scripts/test_temporal_phase5.py` |
| `docker-compose.yml` labels, `traefik/traefik.yml`, `docker-compose.dev.yml` | 20 (Traefik routing + multi-replica) |
| `go/internal/authserver/`, `go/cmd/auth-server/`, `Dockerfile.auth-go`, `docker-compose.yml` (them-auth-go / THE_M_AUTH_URL / AUTH_SERVICE_URL), `frontend/src/app/api/auth/*` | `go test ./internal/authserver/...` (in `go/`) + 15 (compose health) + live login/me/refresh/logout smoke through them-auth-go |
| Before a release / PR merge | Full suite + E2E (14, needs `ADMIN_JWT`) + MT + `scripts/test_temporal_workflow.py` |

### E2E test (14) — needs a JWT

```bash
# Get a token first:
curl -s -X POST http://localhost:8701/auth/login -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python -c "import sys,json; print(json.load(sys.stdin)['access_token'])"

# Then run:
ADMIN_JWT=<token> python3.12 scripts/tests/run_tests.py 14
```

### Temporal worker restart — REQUIRED after editing activity/workflow files

```bash
# Temporal activities are registered at worker startup. If you edit:
#   app/temporal/activities.py, app/temporal/workflows.py, app/temporal/shared.py
# the running worker still has the OLD code. Always restart after changes:
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-worker
docker logs them-worker --tail 5   # confirm "temporal_worker: polling"
# Symptom if forgotten: new params on activities silently receive None at runtime.
```

---

## Container Map

| Container | Role | Port | Source dir |
|---|---|---|---|
| `them-traefik` | Reverse proxy — single entry point, path-based routing, sticky LB | **8088** (host), 127.0.0.1:**8089** (dashboard) | `traefik/` |
| `them-postgres` | PG16 — DB: `them` | 5432 (internal) | bind mount `./data/them-postgres/pgdata` |
| `them-redis` | Redis DB 0 | 6379 (internal) | bind mount `./data/them-redis` |
| `them-auth-go` | Go auth service — UI-facing login/me/refresh/logout + verify/validate (replaces them-auth-service for the UI contract) | 8703 (internal) | `go/cmd/auth-server/`, `go/internal/authserver/` |
| `them-auth-service` | Python Auth/IAM microservice — users/roles/teams/permissions admin CRUD only (UI auth moved to them-auth-go) | 8701 (internal) | `auth_service/` |
| `them-bridge` | Python orchestrator API + WS (replica 1) | 8001 (internal) | `app/` |
| `them-bridge-2` | Python replica 2 (`profiles: [replica]`) | 8001 (internal) | `app/` |
| `them-go-bridge` | Go gateway (routes progressively migrated from Python) | 8002 (internal) | `go/` |
| `them-frontend` | Next.js dashboard | 3200 (internal) | `frontend/` |
| `vision-agent` | Vision/maps agent | 9100 (internal) | `agents/vision_agent/` |
| `them-security-agent` | Security scanner A2A agent (`profiles: [security]`) | 9500 (internal) | `agents/security_scanner/` |
| `a2a-echo` | A2A v1.0 echo test agent (`profiles: [test-agents]`) | 9200 (internal) | `agents/a2a_echo/` |
| `a2a-slow` | A2A v1.0 slow test agent (5s delay) (`profiles: [test-agents]`) | 9201 (internal) | `agents/a2a_slow/` |
| `a2a-stream` | A2A v1.0 streaming test agent (`profiles: [test-agents]`) | 9202 (internal) | `agents/a2a_stream/` |

---

## Key Source Locations

| Concern | Location |
|---|---|
| Orchestrator agentic loop (Python) | `app/services/task_runner.py` |
| Agent registry → NeutralTool list | `app/services/agent_registry.py` |
| Agent transport adapters | `app/adapters/` (base, a2a_async_adapter, factory) |
| Orchestrator WS endpoint (Python) | `app/routers/ws_orchestrator.py` |
| Dashboard WS (multiplexed channels) | `app/routers/ws_dashboard.py` |
| Temporal workflow (agentic loop) | `app/temporal/workflows.py` |
| Temporal activities (all I/O) | `app/temporal/activities.py` |
| Temporal bridge client | `app/temporal/bridge_client.py` |
| Temporal worker entrypoint | `app/temporal/worker.py` |
| LLM providers (Python) | `app/services/providers/` |
| Token cache (L1+L2) | `app/services/token_cache.py` |
| Run recording (Python) | `app/services/run_recorder.py` |
| DB models (Python) | `app/models.py` |
| Config + env vars (Python) | `app/config.py` |
| DB schema source of truth | `db/001_schema.sql` |
| Auth schema source of truth | `auth_service/SCHEMA.sql` |
| Frontend proxy route | `frontend/src/app/api/them/[...path]/route.ts` |
| Frontend auth cookies | `frontend/src/app/api/auth/` |
| Go source root | `go/` — see `go/CLAUDE.md` for full package map |
| Go admin DAL | `go/internal/admin/dal/` |
| Go WS/SSE handlers | `go/internal/ws/`, `go/internal/sse/` |
| Go auth middleware | `go/internal/auth/` |

---

## Common Commands

```bash
# ── Local dev ────────────────────────────────────────────────────────────────
# Stack (core only)
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
docker compose -f docker-compose.yml -f docker-compose.dev.yml ps
docker compose logs -f them-bridge

# Stack with Temporal (required for orchestration — TEMPORAL_ENABLED=true in bridge)
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile temporal ps
docker logs them-worker
# Temporal UI: http://localhost:8088/temporal

# ── Hetzner (production) ──────────────────────────────────────────────────────
./scripts/deploy.sh up          # start / adopt stack
./scripts/deploy.sh status      # container states
./scripts/deploy.sh logs [svc]  # tail logs

# DB init (run once after first up, or after wiping data/)
docker cp db/001_schema.sql them-postgres:/tmp/them_001_schema.sql
docker cp auth_service/SCHEMA.sql them-postgres:/tmp/them_auth_schema.sql
docker cp db/002_seed.sql them-postgres:/tmp/them_002_seed.sql
docker exec them-postgres psql -U them -d them -c "CREATE SCHEMA IF NOT EXISTS auth_service;"
docker exec them-postgres psql -U them -d them -f /tmp/them_001_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_auth_schema.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_002_seed.sql
# Apply remaining migrations in order: 003 through 018 (see CLAUDE.md history or docs/STATUS.md for full list)

# DB access
docker exec -it them-postgres psql -U them -d them

# Secrets / .env (run before first up)
.\generate-env.ps1    # Windows
./generate-env.sh     # Linux/Mac

# Enable replica 2
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile replica up -d them-bridge-2
```

---

## Database Schemas — Quick Reference

**`auth_service` schema** — owned by `them-auth-service` (port 8701). Never access directly from bridge.
Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

**`them` schema** — owned by `them-bridge`.
Tables: `llm_providers`, `config`, `agents`, `orchestrators`, `access_tokens`, `runs` (has `entry_point_slug`), `run_steps`, `run_usage`, `audit_logs`, `tasks`, `artifacts`, `task_messages`, `applications` (parent), `entry_points` (child of applications — one row per WS/SSE/WebRTC door)

**Credentials:** derived via HMAC-SHA256 from `secrets.local`. Re-run `.\generate-env.ps1` to regenerate `.env`.
DB user: `them`, DB name: `them`, DB host (internal): `them-postgres:5432`
