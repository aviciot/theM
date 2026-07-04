# the-M Status
# Last updated: 2026-07-05

## Build Progress

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Skeleton & infra | ✓ Complete | config, database, models, health, docker-compose |
| Phase 1 — Auth | ✓ Complete | auth_service, httpOnly cookie auth (`them_access_token`, `them_refresh_token`) |
| Phase 2 — LLM providers admin | ✓ Complete | admin_llm_providers.py, providers/ |
| Phase 3 — Agent registry & adapters | ✓ Complete | adapters/, agent_registry.py, admin_agents.py, admin_orchestrators.py |
| Phase 4 — Token cache & rate limiter | ✓ Complete | token_cache.py, rate_limiter.py, admin_tokens.py, _deps.py |
| Phase 5 — Orchestrator loop | ✓ Complete | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | ✓ Complete | ws_dashboard.py, runs.py, Redis pub/sub multiplexing |
| Phase 6.5 — Frontend admin UI | ✓ Complete | Orchestrators, Agents, Tokens, Runs pages; per-orch LLM config |
| Phase 6.6 — Playground UI | ✓ Complete | Split-pane chat + real-time Redis trace; mock agents |
| Phase 7 — Tests + compose finalize | ✓ Complete | 140+ tests passing (cross-platform Python runner), compose hardened |
| Rename: Odin → the-M | ✓ Complete | All identifiers, schemas, containers, Redis keys, cookies renamed |
| Local deployment | ✓ Complete | Stack running, DB seeded, users created, login works |
| **A2A migration Phase 3** | ✓ Complete | task_runner.py (durable agentic loop), task_store.py, ws_orchestrator rewired |
| **A2A migration Phase 4** | ✓ Complete | A2aAsyncAdapter, AdapterEvent extended, push webhook (/a2a/push), reaper |
| **A2A migration Phase 5** | ✓ Complete | context_service.py, Redis artifact cache `them:ctx:{ctx_id}:heads` |
| **A2A migration Phase 6** | ✓ Complete | runs/{id}/tasks, runs/{id}/artifacts, runs/context/{ctx_id}/artifacts endpoints; playground debug tabs |
| **A2A migration Phase 7** | ✓ Complete | a2a-echo, a2a-slow, a2a-stream agents; test-agents compose profile; seed SQL; test_16 |

## Infrastructure (as of 2026-07-04)

| Container | Image/Source | Data | Port |
|---|---|---|---|
| `them-postgres` | postgres:16-alpine | `./data/them-postgres/pgdata/` | 5432 (internal) |
| `them-redis` | redis:7-alpine | `./data/them-redis/` | 6379 (internal) |
| `them-auth-service` | `auth_service/` | — | 8701 (internal) |
| `them-bridge` | `app/` | `./data/them-logs/` | 8001 (host + internal) |
| `them-frontend` | `frontend/` | — | 3200 (host + internal) |
| `mock-agent-assistant` | `mock_agent/` | — | 9000 (internal) |
| `mock-agent-researcher` | `mock_agent/` | — | 9000 (internal) |
| `mock-agent-coder` | `mock_agent/` | — | 9000 (internal) |
| `vision-agent` | `agents/vision_agent/` | — | 9100 (internal) — **unhealthy** |
| `a2a-echo` | `agents/a2a_echo/` | — | 9200 (internal) — **profile: test-agents** |
| `a2a-slow` | `agents/a2a_slow/` | — | 9201 (internal) — **profile: test-agents** |
| `a2a-stream` | `agents/a2a_stream/` | — | 9202 (internal) — **profile: test-agents** |

## Users Seeded

| Username | Password | Role |
|---|---|---|
| `admin` | `admin123` | super_admin |
| `avi` | `avi123` | super_admin |

## API Routes (live)

| Route | Method | Status |
|---|---|---|
| `/health`, `/health/live`, `/health/ready` | GET | ✓ Live |
| `/api/v1/admin/llm-providers` | CRUD | ✓ Live |
| `/api/v1/admin/agents` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators/{id}/test-llm` | POST | ✓ Live |
| `/api/v1/admin/tokens` | CRUD | ✓ Live |
| `/ws/orchestrate/{name}` | WebSocket | ✓ Live |
| `/ws/dashboard` | WebSocket | ✓ Live |
| `/api/v1/runs` | GET/DELETE | ✓ Live |
| `/api/v1/runs/{id}/tasks` | GET | ✓ Live (A2A Phase 6) |
| `/api/v1/runs/{id}/artifacts` | GET | ✓ Live (A2A Phase 6) |
| `/api/v1/runs/context/{ctx_id}/artifacts` | GET | ✓ Live (A2A Phase 6) |
| `/a2a/push/{task_id}` | POST | ✓ Live (A2A Phase 4) |
| `/.well-known/agent-card.json` | GET | ✓ Live (A2A Phase 4) |

## Frontend Pages (live, http://localhost:3200)

| Page | Path | Status |
|---|---|---|
| Login | `/login` | ✓ — credentials pre-filled in dev mode |
| Dashboard | `/dashboard` | ✓ |
| Agents | `/agents` | ✓ |
| Run History | `/runs` | ✓ |
| Orchestrators | `/admin/orchestrators` | ✓ |
| Access Tokens | `/admin/tokens` | ✓ |
| Playground | `/admin/playground` | ✓ — chat + debug tabs (Trace, Tasks, Artifacts, Memory) |

## Open Items

- **`vision-agent` unhealthy**: needs `GOOGLE_MAPS_API_KEY` and `FAL_API_KEY` set in `.env`. Not blocking anything else.
- **Git hooks not wired**: test runner exists (`python scripts/tests/run_tests.py`) but no pre-push hook. Planned as GitHub Actions.
- **Replica 2**: compose profile `replica`, not running by default. Enable with `--profile replica`.
- **DB reset trap**: if Postgres is wiped but Redis survives, orchestrator cache holds stale FK IDs → run INSERT fails. After any DB wipe: re-run DB init steps from CLAUDE.md, then recreate orchestrators via UI to refresh Redis cache.
- **`them-frontend` shows unhealthy in `docker ps`**: false alarm — Docker healthcheck uses `curl -f -L` but Next.js dev mode takes >30s to compile first request. App works fine; healthcheck timing is aggressive.
