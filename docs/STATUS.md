# Odin Status
# Last updated: 2026-07-03

## Build Progress

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Skeleton & infra | ✓ Complete | config, database, models, health, docker-compose |
| Phase 1 — Auth | ✓ Complete | auth_service copied + adapted, httpOnly cookie auth |
| Phase 2 — LLM providers admin | ✓ Complete | admin_llm_providers.py, providers/ copied from Omni |
| Phase 3 — Agent registry & adapters | ✓ Complete | adapters/, agent_registry.py, admin_agents.py, admin_orchestrators.py |
| Phase 4 — Token cache & rate limiter | ✓ Complete | token_cache.py, rate_limiter.py, admin_tokens.py, _deps.py |
| Phase 5 — Orchestrator loop | ✓ Complete | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | ✓ Complete | ws_dashboard.py, runs.py, Redis pub/sub multiplexing |
| Phase 6.5 — Frontend admin UI | ✓ Complete | Orchestrators, Agents, Tokens, Runs pages; per-orch LLM config |
| Phase 6.6 — Playground UI | ✓ Complete | Split-pane chat + real-time Redis trace; mock agents |
| Phase 7 — Full tests + compose finalize | **Next** | integration tests, .env.example, hardening |

## Infrastructure (as of 2026-07-03)

Fully isolated — zero dependency on Omni containers.

| Container | Image/Source | Data |
|---|---|---|
| odin-postgres | postgres:16-alpine | volumes/postgres/pgdata/ |
| odin-redis | redis:7-alpine | volumes/redis/ |
| odin-auth-service | auth_service/ | — |
| odin-bridge | app/ | volumes/logs/ |
| odin-frontend | frontend/ | — |
| mock-agent-assistant | mock_agent/ | port 9000 |
| mock-agent-researcher | mock_agent/ | port 9000 |
| mock-agent-coder | mock_agent/ | port 9000 |

## API Routes (live)

| Route | Method | Status |
|---|---|---|
| `/health`, `/health/live`, `/health/ready` | GET | ✓ Live |
| `/api/v1/admin/llm-providers` | CRUD | ✓ Live |
| `/api/v1/admin/agents` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators/{id}/test-llm` | POST | ✓ Live |
| `/api/v1/admin/tokens` | CRUD | ✓ Live |
| `/ws/orchestrate/{name}` | WebSocket | ✓ Live (accepts JWT or access token) |
| `/ws/dashboard` | WebSocket | ✓ Live (static + dynamic `run:{uuid}` channels) |
| `/api/v1/runs` | GET/DELETE | ✓ Live |

## Frontend Pages (live)

| Page | Path | Status |
|---|---|---|
| Login | `/login` | ✓ |
| Dashboard | `/dashboard` | ✓ |
| Agents | `/agents` | ✓ |
| Run History | `/runs` | ✓ |
| Orchestrators | `/admin/orchestrators` | ✓ — includes LLM config + test button |
| Access Tokens | `/admin/tokens` | ✓ |
| Playground | `/admin/playground` | ✓ — split chat + trace pane |

## End-to-End Verified (2026-07-03)

Full agentic loop confirmed working:
- User sends message in Playground
- Claude LLM picks `agent__assistant` tool
- OmniWsAdapter connects to mock-agent-assistant (WS)
- Mock agent streams word-by-word reply
- Claude synthesizes final answer, streams to browser
- Redis pub/sub delivers trace events (iteration, tool_start, tool_done, usage, run_end) to trace pane in real time
- Run recorded to Postgres (when orchestrator row exists in DB — see Open Items)

## Open Items

- **DB reset issue**: if Postgres is wiped but Redis survives, orchestrator cache references stale FK IDs → run INSERT fails silently. Fix: recreate orchestrators via UI after any DB reset. Long-term: add cache-busting on FK violation.
- Phase 7: live end-to-end integration test (real WS orchestrate + tool fan-out)
- Phase 7: `.env.example` finalization + compose hardening
- Phase 7: replica-2 smoke test (optional, needs `--profile replica`)
- Traefik hostname: needs `ODIN_HOSTNAME` env var set per deployment
- mock agents need `docker compose build` (not just restart) to pick up code changes — no volume mount
