# Odin Status
# Last updated: 2026-06-28

## Build Progress

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Skeleton & infra | ✓ Complete | config, database, models, health, docker-compose |
| Phase 1 — Auth | ✓ Complete | auth_service copied + adapted, auth_client.py |
| Phase 2 — LLM providers admin | ✓ Complete | admin_llm_providers.py, providers/ copied from Omni |
| Phase 3 — Agent registry & adapters | Pending | agent_registry.py, adapters/, admin_agents.py, admin_orchestrators.py |
| Phase 4 — Token cache & rate limiter | Pending | token_cache.py, rate_limiter.py, admin_tokens.py, _deps.py |
| Phase 5 — Orchestrator loop | Pending | orchestrator_service.py, run_recorder.py, ws_orchestrator.py |
| Phase 6 — Dashboard WS + runs API | Pending | ws_dashboard.py, dashboard_broadcaster.py, runs.py |
| Phase 7 — Full tests + compose finalize | Pending | |

## Day-1 Scope
- `omni_ws` adapter only
- `a2a` adapter is a stub (NotImplementedError)
- Voice transport deferred
- Dashboard UI not started

## Open Questions
- Confirm SCHEMA.sql from auth_service creates `auth_service` schema correctly in odin DB
- Traefik hostname for odin on billing-43 (needs ODIN_HOSTNAME env var)
- Run `./scripts/init_db.sh` to verify DB init works end-to-end
