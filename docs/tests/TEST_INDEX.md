# Odin Test Index
# Last updated: 2026-07-02

All test scripts live in `scripts/tests/`. They are designed to be run both manually
during development and as part of the deployment checklist.

---

## Running Tests

```bash
# Full suite (all phases)
bash scripts/tests/run_all_tests.sh

# Full suite with live E2E (requires admin JWT)
ADMIN_JWT=<token> bash scripts/tests/run_all_tests.sh

# Run individual phase
bash scripts/tests/run_phase7_tests.sh

# Run individual test
bash scripts/tests/test_01_db.sh
python3.12 scripts/tests/test_07_adapter_factory.py

# Override container/URL defaults
BRIDGE_CONTAINER=odin-bridge BRIDGE_PORT=8001 bash scripts/tests/run_all_tests.sh
```

---

## Test Scripts

| Script | Phase | Needs | What it tests |
|---|---|---|---|
| `test_01_db.sh` | 0 | odin-postgres running | DB connectivity, odin schema, all tables exist |
| `test_02_redis.sh` | 0 | odin-redis running | Redis DB 0 reachable, read/write, namespace isolation |
| `test_03_auth_service.sh` | 1 | odin-auth-service running | Auth service /health, /health/live, /health/ready |
| `test_04_bridge_health.sh` | 0/1 | odin-bridge running | Bridge /health, /health/live, /health/ready |
| `test_05_agents_api.sh` | 3 | odin-bridge running | Full agent CRUD: create, get, patch, delete, conflict, invalid transport |
| `test_06_orchestrators_api.sh` | 3 | odin-bridge running | Full orchestrator CRUD: create, get, patch, delete, conflict |
| `test_07_adapter_factory.py` | 3 | Python only (no containers) | AdapterEvent, factory routing, A2aAdapter stub, AgentAdapter ABC |
| `test_08_tokens_api.sh` | 4 | odin-bridge running | Token CRUD, opaque token returned once, disable flow |
| `test_09_rate_limiter.py` | 4 | Python only (no containers) | Rate limiter structure, Redis key format, slot logic |
| `test_10_run_recorder.py` | 5 | Python only (no containers) | Run recorder structure, function signatures |
| `test_11_ws_orchestrate.sh` | 5 | odin-bridge running | WS route registered, auth required, orchestrator endpoint |
| `test_12_runs_api.sh` | 6 | odin-bridge running | Runs list/stats/detail: auth required, route exists |
| `test_13_dashboard_ws.py` | 6 | Python only (no containers) | Dashboard broadcaster + WS structure, channel routing |
| `test_14_e2e_orchestrate.sh` | 7 | odin-bridge + ADMIN_JWT | Full flow: create token → agent → orchestrator → WS → verify run |
| `test_15_compose_health.sh` | 7 | All containers running | Container running, healthcheck state, cross-container network |

---

## Phase Test Suites

| Runner | Phase | Status |
|---|---|---|
| `run_phase3_tests.sh` | 3 — Adapters + Registry + Admin CRUD | ✓ All green |
| `run_phase4_tests.sh` | 4 — Token cache + Rate limiter | ✓ All green |
| `run_phase5_tests.sh` | 5 — Orchestrator loop + WS endpoint | ✓ All green |
| `run_phase6_tests.sh` | 6 — Dashboard WS + Runs API | ✓ All green |
| `run_phase7_tests.sh` | 7 — Compose health + E2E | ✓ Available |
| `run_all_tests.sh` | Full suite (all phases) | ✓ Available |

---

## Deployment Checklist

When deploying Odin to a new environment:

1. Copy `.env.example` → `.env`, fill in all `[REQUIRED]` values
2. `docker compose up -d odin-postgres odin-redis` — start data tier
3. `bash scripts/tests/test_01_db.sh` — verify DB (schema auto-applied by postgres/init/)
4. `bash scripts/tests/test_02_redis.sh` — verify Redis
5. `docker compose up -d odin-auth-service`
6. `bash scripts/tests/test_03_auth_service.sh` — verify auth
7. `docker compose up -d odin-bridge`
8. `bash scripts/tests/test_04_bridge_health.sh` — verify bridge
9. `bash scripts/tests/run_all_tests.sh` — full suite (E2E skips without ADMIN_JWT)
10. Get an admin JWT: `POST /auth/login` on odin-auth-service (port 8701)
11. `ADMIN_JWT=<token> bash scripts/tests/test_14_e2e_orchestrate.sh` — live E2E

---

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `BRIDGE_CONTAINER` | `odin-bridge` | Docker container for curl calls |
| `BRIDGE_PORT` | `8001` | odin-bridge port |
| `ADMIN_JWT` | _(empty)_ | JWT for admin API — required by E2E test |
| `POSTGRES_CONTAINER` | `odin-postgres` | Docker container name for psql |
| `POSTGRES_DB` | `odin` | DB name |
| `POSTGRES_USER` | `odin` | DB user |
| `REDIS_CONTAINER` | `odin-redis` | Docker container name for redis-cli |
| `REDIS_DB` | `0` | Redis DB index (Odin owns index 0) |
