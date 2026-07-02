# Odin Status
# Last updated: 2026-07-02

## Build Progress

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Skeleton & infra | ✓ Complete | config, database, models, health, docker-compose |
| Phase 1 — Auth | ✓ Complete | auth_service copied + adapted, auth_client.py |
| Phase 2 — LLM providers admin | ✓ Complete | admin_llm_providers.py, providers/ copied from Omni |
| Phase 3 — Agent registry & adapters | ✓ Complete | adapters/, agent_registry.py, admin_agents.py, admin_orchestrators.py |
| Phase 4 — Token cache & rate limiter | ✓ Complete | token_cache.py, rate_limiter.py, admin_tokens.py, _deps.py |
| Phase 5 — Orchestrator loop | ✓ Complete | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | ✓ Complete | ws_dashboard.py, dashboard_broadcaster.py, runs.py |
| Phase 7 — Full tests + compose finalize | **Next** | integration tests, .env.example, hardening |

## Infrastructure (as of 2026-07-02)

Fully isolated — zero dependency on Omni containers.

| Container | Image/Source | Data |
|---|---|---|
| odin-postgres | postgres:16-alpine | volumes/postgres/pgdata/ |
| odin-redis | redis:7-alpine | volumes/redis/ |
| odin-auth-service | auth_service/ | — |
| odin-bridge | app/ | volumes/logs/ |

All volumes are bind-mounted — data survives `docker compose down --build`.

## API Routes (live)

| Route | Method | Status |
|---|---|---|
| `/health`, `/health/live`, `/health/ready` | GET | ✓ Live |
| `/api/v1/admin/llm-providers` | CRUD | ✓ Live |
| `/api/v1/admin/agents` | CRUD | ✓ Live |
| `/api/v1/admin/orchestrators` | CRUD | ✓ Live |
| `/api/v1/admin/tokens` | CRUD | ✓ Live |
| `/ws/orchestrate/{name}` | WebSocket | ✓ Live |
| `/ws/dashboard` | WebSocket | ✓ Live |
| `/api/v1/runs` | GET/DELETE | ✓ Live |

## Test Suite

| Phase | Runner | Suites | Status |
|---|---|---|---|
| Phase 3 | run_phase3_tests.sh | 7 | ✓ All green |
| Phase 4 | run_phase4_tests.sh | 9 | ✓ All green |
| Phase 5 | run_phase5_tests.sh | 11 | ✓ All green |
| Phase 6 | run_phase6_tests.sh | 13 | ✓ All green |

See `docs/tests/TEST_INDEX.md` for full index.

## Open Items

- Phase 7: live end-to-end integration test (real WS orchestrate + tool fan-out)
- Phase 7: `.env.example` finalization + compose hardening
- Phase 7: replica-2 smoke test (optional, needs --profile replica)
- Traefik hostname: needs `ODIN_HOSTNAME` env var set per deployment
