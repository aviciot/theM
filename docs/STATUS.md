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
| Phase 5 — Orchestrator loop | Pending | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | Pending | ws_dashboard.py, dashboard_broadcaster.py, runs.py |
| Phase 7 — Full tests + compose finalize | Pending | |

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
| `/api/v1/admin/tokens` | CRUD | Phase 4 |
| `/ws/orchestrate/{name}` | WebSocket | Phase 5 |
| `/ws/dashboard` | WebSocket | Phase 6 |
| `/api/v1/runs` | GET | Phase 6 |

## Test Suite

All Phase 3 tests green. Run with: `bash scripts/tests/run_phase3_tests.sh`
See `docs/tests/TEST_INDEX.md` for full index.

## Open Items

- SCHEMA.sql `ARRAY[]::text[]` fixed (was untyped empty array in seed)
- Redis key prefix: `odin:` (DB index 0, own Redis)
- Traefik hostname: needs `ODIN_HOSTNAME` env var set per deployment
- Phase 4 next: token cache L1+L2, rate limiter, access token CRUD, auth deps
