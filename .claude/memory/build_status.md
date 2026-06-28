---
name: build-status
description: "Which Odin build phases are complete, what's pending, open questions"
metadata:
  type: project
---

## Phase Status (2026-06-28)
| Phase | Status |
|---|---|
| 0 — Skeleton (config, db, models, health, compose) | ✓ Done |
| 1 — Auth (auth_service copy, auth_client) | ✓ Done |
| 2 — LLM providers + providers/ copy | Pending |
| 3 — Agent registry + adapters + admin CRUD | Pending |
| 4 — Token cache + rate limiter + deps | Pending |
| 5 — Orchestrator loop + run_recorder + ws_orchestrator | Pending |
| 6 — Dashboard WS + runs API | Pending |
| 7 — Full tests, compose finalize | Pending |

## Next session: start Phase 2
- Copy `app/services/providers/` from Omni verbatim
- Write `app/routers/admin_llm_providers.py`
- Reads/writes `odin.llm_providers` + `odin.config['llm_routing']`

## Open questions
- Run `./scripts/init_db.sh` to verify DB init (auth_service SCHEMA.sql needs odin DB user to have CREATE SCHEMA rights)
- Confirm ODIN_HOSTNAME for Traefik on billing-43
