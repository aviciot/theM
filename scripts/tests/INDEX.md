# Test Suite Index — the-M

Runner: `python scripts/tests/run_tests.py` (cross-platform, Windows + Linux)
Requires: `docker` in PATH, stack running via `docker compose up`.

---

## Quick Reference

| ID | File | Type | Needs stack? | What it tests |
|---|---|---|---|---|
| 01 | `run_tests.py::test_01_db` | live | yes | DB connectivity + all 9 `them.*` tables exist |
| 02 | `run_tests.py::test_02_redis` | live | yes | Redis PING + read/write on DB 0 |
| 03 | `run_tests.py::test_03_auth_service` | live | yes | Auth service `/health`, `/health/live`, `/health/ready` |
| 04 | `run_tests.py::test_04_bridge_health` | live | yes | Bridge `/health`, `/health/live`, `/health/ready` |
| 05 | `run_tests.py::test_05_agents_api` | live | yes | Agents admin API — full CRUD cycle + conflict + validation |
| 06 | `run_tests.py::test_06_orchestrators_api` | live | yes | Orchestrators admin API — full CRUD cycle |
| 07 | `run_tests.py::test_07_adapter_factory` | structural | no | `AdapterEvent`, `get_adapter()`, `A2aAdapter` error path, `AgentAdapter` abstractness |
| 08 | `run_tests.py::test_08_tokens_api` | live | yes | Access tokens admin API — full CRUD + enable/disable + revocation rejected on WS connect |
| 09 | `run_tests.py::test_09_rate_limiter` | structural | no | `_slot()`, `check_rate_limit()` with no Redis, token hash logic, `_deps.py` structure, `token_cache` L1 logic + `_is_user_active` / `invalidate_user_active` present |
| 10 | `run_tests.py::test_10_run_recorder` | structural | no | `run_recorder.py` functions, `task_runner.py` functions + multi-turn (`_load_context_history`, user msg seq=0, prior_history prepend), WS event types, `ws_orchestrator.py` structure |
| 11 | `run_tests.py::test_11_ws_orchestrate` | live | yes | WS route exists + responds, bearer token creation, bridge still healthy |
| 12 | `run_tests.py::test_12_runs_api` | live | yes | Runs API requires auth (401/403), bad JWT rejected, bridge healthy |
| 13 | `run_tests.py::test_13_dashboard_ws` | structural | no | `dashboard_broadcaster.py` functions, `ws_dashboard.py` structure, `runs.py` functions, `main.py` wiring |
| 14 | `run_tests.py::test_14_e2e_orchestrate` | e2e | yes + JWT | Full flow: create token → create agent+orch → hit WS route → check runs API → cleanup |
| 15 | `run_tests.py::test_15_compose_health` | live | yes | All core containers running + healthy, HTTP endpoints, inter-container TCP connectivity |
| 16 | `run_tests.py::test_16_a2a_agents` | structural | no | A2A test agents exist (echo/slow/stream), docker-compose test-agents profile, seed SQL, A2aAsyncAdapter importable |
| 17 | `run_tests.py::test_17_memory` | structural | no | Phase 8.4 context summarization memory: memory_service.py functions, Redis key prefix, models.py memory columns, task_runner integration, REDIS.md docs, 003_phase8.sql migration, api.ts types |
| 18 | `run_tests.py::test_18_orch_as_agent` | structural | no | Phase 8.5 durable inbound A2A: a2a_server.py rewired to them.tasks (_tasks dict removed), SendMessage/GetTask/CancelTask handlers, returnImmediately, push webhook, a2a_exposed + budget_tokens columns |
| 19 | `run_tests.py::test_19_edges` | structural | no | Phase 8.6 pluggable edge adapters: app/edges/ files (base, websocket, voice stub, rest stub, registry), EdgeAdapter ABC, WebsocketEdge wired in ws_orchestrator, VALID_EDGES set, edges column in migration |
| 20 | `run_tests.py::test_20_traefik` | live + structural | yes | Traefik routing (bridge + frontend), sticky session cookie, docker-compose label correctness, multi-replica LB + shared Postgres (skips replica checks if bridge-2 not running) |
| 21 | `run_tests.py::test_21_a2a_hardening` | structural | no | Phase 9 A2A hardening: rate limit, body+batch limits, token expiry, ownership isolation, agent card system_prompt strip, default deadline, TOCTOU scope fix, task_store helpers, Application model, 004_phase9.sql migration |
| 22 | `run_tests.py::test_22_applications` | structural | no | Phase 9 Phase 2+3: admin_applications.py CRUD, apps.py entry points (REST + WS + poll), main.py wiring, api.ts Application type + methods, frontend applications page, Sidebar nav |

| 23 | `run_tests.py::test_23_a2a_skill_discovery` | structural | no | A2A agent card auto-discovery: `_ensure_agent_skills` helper, TTL constant, httpx fetch, A2A-Version header, Bearer auth, write-back to DB (skills/agent_card/card_fetched_at), failure handling, call order before tool list, docu_writer agent files, 007_docu_stack.sql seed |
| 24 | `run_tests.py::test_24_code_agent_live` | live | yes | code_agent A2A live call: agent card reachable, list_repos + query_graph skills present, SendMessage returns real repo data, no TextContent serialization error |

| MT | `scripts/test_multiturn.py` | e2e | yes + JWT (auto-fetched) | Multi-turn conversation history: recall across fresh WS connections, `history_window` behavioral proof (window=1 forgets old turns) |

**Types:**
- **live** — makes real HTTP/Docker calls against the running stack
- **structural** — AST + import checks, no containers needed
- **e2e** — full integration, requires `ADMIN_JWT` env var (or auto-fetches via admin credentials)

---

## When to Run What

### After `docker compose up` — always
```
python scripts/tests/run_tests.py 01 02 03 04 15
```
~15s. Confirms the stack is healthy before doing anything else.

### After any `app/` change — before committing
```
python scripts/tests/run_tests.py
```
~30s. Full suite, zero failures required.

### Trigger map

| You changed | Run |
|---|---|
| `db/001_schema.sql`, `app/models.py` | 01 |
| `app/adapters/` | 07 |
| `app/services/rate_limiter.py`, `token_cache.py` | 08 09 |
| `app/services/run_recorder.py`, `app/services/task_runner.py`, `orchestrator_service.py` | 10 |
| `app/routers/admin_agents.py` | 05 |
| `app/routers/admin_orchestrators.py` | 06 |
| `app/routers/admin_tokens.py` | 08 09 |
| `app/routers/ws_orchestrator.py` | 11 |
| `app/routers/runs.py` | 12 |
| `app/routers/ws_dashboard.py`, `app/services/dashboard_broadcaster.py` | 13 |
| `docker-compose.yml`, `Dockerfile`, infra config | 15 |
| `agents/a2a_*`, `docker-compose.yml` test-agents profile | 16 |
| `app/services/memory_service.py`, `db/003_phase8.sql` (memory columns) | 17 |
| `app/routers/a2a_server.py` (orch-as-agent sections), `app/models.py` (a2a_exposed/budget_tokens) | 18 |
| `app/edges/` | 19 |
| `docker-compose.yml` (bridge/frontend labels), `traefik/traefik.yml`, `docker-compose.local.yml` | 20 |
| `app/routers/a2a_server.py`, `app/services/task_store.py`, `app/services/token_cache.py`, `db/004_phase9.sql` | 21 |
| `app/routers/admin_applications.py`, `app/routers/apps.py`, `app/main.py`, `frontend/src/app/admin/applications/`, `frontend/src/lib/api.ts`, `frontend/src/components/Sidebar.tsx` | 22 |
| `app/services/task_runner.py` (history), `app/models.py` (history_window), `app/routers/admin_orchestrators.py` | 10 + MT |
| Before release / PR merge | all + 14 (with JWT) + MT |

---

## Running Tests

```bash
# Full suite
python scripts/tests/run_tests.py

# Specific tests
python scripts/tests/run_tests.py 01 02 03 04 15

# E2E (needs a JWT — get one from auth service first)
ADMIN_JWT=<token> python scripts/tests/run_tests.py 14   # Linux/Mac
$env:ADMIN_JWT="<token>"; python scripts/tests/run_tests.py 14   # Windows PowerShell

# Multi-turn behavioral test (auto-fetches JWT, must run inside bridge container)
docker cp scripts/test_multiturn.py them-bridge:/tmp/test_multiturn.py
docker exec them-bridge python3 /tmp/test_multiturn.py
```

## Adding a New Test

1. Add a `test_NN_name()` function to `run_tests.py`
2. Register it in the `ALL_TESTS` list at the bottom of that file
3. Add a row to this INDEX.md
4. Add a trigger rule in CLAUDE.md under "Rules — Testing"
