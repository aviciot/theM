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
| 10 | `run_tests.py::test_10_run_recorder` | structural | no | `run_recorder.py` functions, `orchestrator_service.py` functions, WS event types, `ws_orchestrator.py` structure |
| 11 | `run_tests.py::test_11_ws_orchestrate` | live | yes | WS route exists + responds, bearer token creation, bridge still healthy |
| 12 | `run_tests.py::test_12_runs_api` | live | yes | Runs API requires auth (401/403), bad JWT rejected, bridge healthy |
| 13 | `run_tests.py::test_13_dashboard_ws` | structural | no | `dashboard_broadcaster.py` functions, `ws_dashboard.py` structure, `runs.py` functions, `main.py` wiring |
| 14 | `run_tests.py::test_14_e2e_orchestrate` | e2e | yes + JWT | Full flow: create token → create agent+orch → hit WS route → check runs API → cleanup |
| 15 | `run_tests.py::test_15_compose_health` | live | yes | All core containers running + healthy, HTTP endpoints, inter-container TCP connectivity |
| 16 | `run_tests.py::test_16_a2a_agents` | structural | no | A2A test agents exist (echo/slow/stream), docker-compose test-agents profile, seed SQL, A2aAsyncAdapter importable |

**Types:**
- **live** — makes real HTTP/Docker calls against the running stack
- **structural** — AST + import checks, no containers needed
- **e2e** — full integration, requires `ADMIN_JWT` env var

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
| `app/services/run_recorder.py`, `orchestrator_service.py` | 10 |
| `app/routers/admin_agents.py` | 05 |
| `app/routers/admin_orchestrators.py` | 06 |
| `app/routers/admin_tokens.py` | 08 09 |
| `app/routers/ws_orchestrator.py` | 11 |
| `app/routers/runs.py` | 12 |
| `app/routers/ws_dashboard.py`, `app/services/dashboard_broadcaster.py` | 13 |
| `docker-compose.yml`, `Dockerfile`, infra config | 15 |
| `agents/a2a_*`, `docker-compose.yml` test-agents profile | 16 |
| Before release / PR merge | all + 14 (with JWT) |

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
```

## Adding a New Test

1. Add a `test_NN_name()` function to `run_tests.py`
2. Register it in the `ALL_TESTS` list at the bottom of that file
3. Add a row to this INDEX.md
4. Add a trigger rule in CLAUDE.md under "Rules — Testing"
