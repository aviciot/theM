# Wave 6 Implementation Report
# Generated: 2026-07-25

---

## Summary

Wave 6 migrated 4 Python admin operations to Go, following the approved plan in `WAVE6_PLAN.md`.
All operations are now served by the Go bridge through Traefik at priority 120.

---

## Scope — 4 Operations Migrated

| Route | Method | Python file | Config key |
|---|---|---|---|
| `/api/v1/admin/monitoring-config` | GET | `app/routers/admin_monitoring_config.py` | `them.config['monitoring']` |
| `/api/v1/admin/monitoring-config` | PUT | same | same |
| `/api/v1/admin/llm-providers/routing/config` | GET | `app/routers/admin_llm_providers.py` | `them.config['llm_routing']` |
| `/api/v1/admin/llm-providers/routing/config` | PUT | same | same |

**Not migrated (Wave 7):** LLM provider CRUD (GET/POST/PATCH/DELETE `/llm-providers`), Fernet API key encryption/decryption.

---

## Architecture

```
Handler (monitoring.go / llm_routing.go)
  ↓ parse JSON, write HTTP
Service (service/config.go — ConfigService)
  ↓ merge defaults, validate ordering, marshal/unmarshal
DAL (dal/config.go — GetConfig, UpsertConfig)
  ↓ SQL: SELECT / INSERT ON CONFLICT DO UPDATE
them.config table (config_key, config_value::jsonb)
```

---

## Files Changed

| File | Status | Description |
|---|---|---|
| `go/internal/admin/dal/config.go` | NEW | `ConfigRow` type; `GetConfig` (nil,nil on not-found); `UpsertConfig` (INSERT ON CONFLICT) |
| `go/internal/admin/service/config.go` | NEW | `MonitoringConfig`, `LLMRoutingConfig` types; `ConfigService` with 4 methods |
| `go/internal/admin/service/config_test.go` | NEW | 10 unit tests: defaults, merge, stored round-trip, validation errors, upsert key/value |
| `go/internal/admin/service/service.go` | MODIFIED | Extended `Dal` interface with `GetConfig` + `UpsertConfig` |
| `go/internal/admin/service/service_test.go` | MODIFIED | Added `fakeDal` fields + stubs for config methods |
| `go/internal/admin/monitoring.go` | NEW | `MonitoringConfigHandler` — GET + PUT |
| `go/internal/admin/llm_routing.go` | NEW | `LLMRoutingHandler` — GET + PUT |
| `go/internal/admin/router.go` | MODIFIED | Registered `monitoring` + `llmRouting` handlers in admin route group |
| `go/internal/admin/config_handler_test.go` | NEW | 7 handler tests: defaults, valid PUT, bad thresholds→422, bad JSON→400 |
| `docker-compose.yml` | MODIFIED | Added Wave 6 Traefik labels at priority 120; fixed JWT env var wiring bug |
| `docs/architecture-v2/WAVE6_PLAN.md` | NEW | Approved scope document |
| `docs/architecture-v2/WAVE6_IMPLEMENTATION_REPORT.md` | NEW | This file |
| `docs/architecture-v2/implementation-status.md` | MODIFIED | Updated wave state, route map, package inventory, test count |
| `docs/architecture-v2/lessons-learned.md` | MODIFIED | Added L-10: JWT env var naming |
| `go/TEST_INDEX.md` | MODIFIED | Added 17 new test entries (10 service, 7 handler) |

---

## Commits

| Commit | Description |
|---|---|
| `69e2dca` | Phase 1: DAL — `config.go`, `dal.ConfigRow`, `GetConfig`, `UpsertConfig`; extended `Dal` interface + fakeDal stubs |
| `e78a6bd` | Phase 2: Service — `ConfigService` with 4 methods + 10 unit tests |
| `b0d1a31` | Phase 3: Handlers — `MonitoringConfigHandler`, `LLMRoutingHandler`, router wiring + 7 handler tests |
| `55eb923` | Phase 5: Cutover — Traefik Wave 6 labels + JWT env var fix |
| (this commit) | Phase 6: Docs — TEST_INDEX.md, implementation-status.md, lessons-learned.md, WAVE6_IMPLEMENTATION_REPORT.md |

---

## Test Results

### Go unit tests
```
docker compose --profile go build them-go-bridge
# All tests pass (builder stage runs go test ./...)
# No new failures introduced
```

Unit tests added:
- `service/config_test.go`: 10 tests
- `config_handler_test.go`: 7 tests
- Total new: 17 tests

### Python test suite
```
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 20
87 passed, 1 skipped
```

### Contract tests (live — Python vs Go comparison)

| Test | Result |
|---|---|
| GET monitoring-config: Python vs Go values | ✅ MATCH (both 200, identical JSON) |
| PUT monitoring-config (valid): Python vs Go | ✅ MATCH (both 200, identical JSON) |
| PUT monitoring-config (invalid threshold): Python vs Go | ✅ MATCH (both 422) |
| PUT monitoring-config (write persists to DB): Python read confirms Go write | ✅ VERIFIED |
| GET llm-providers/routing/config: Python vs Go | ✅ MATCH (both 200, identical JSON) |
| PUT llm-providers/routing/config: Python vs Go | ✅ MATCH (both 200, identical JSON) |

### Smoke tests via Traefik (port 8088)
```
GET  /api/v1/admin/monitoring-config         → 200 (Go bridge)
GET  /api/v1/admin/llm-providers/routing/config → 200 (Go bridge)
PUT  /api/v1/admin/monitoring-config         → 200, persisted (Go bridge)
```

---

## Bug Fixed

**Pre-existing JWT env var wiring bug in `docker-compose.yml`:**
- Go bridge was using `SECRET_KEY` (wrong variable) instead of the auth service's `JWT_SECRET`
- This caused all HS256 JWT validation to fail with 401 in local deployments
- Fixed by renaming `SECRET_KEY=${SECRET_KEY:-}` → `SECRET_KEY=${THE_M_SECRET_KEY:-}` and adding `JWT_SECRET=${THE_M_JWT_SECRET:-}`
- Documented in `lessons-learned.md` as L-10

---

## Traefik Routing

Two new router blocks added to `docker-compose.yml` at priority 120:
- `them-go-monitoring-config`: `Path("/api/v1/admin/monitoring-config")` — exact match
- `them-go-llm-routing-config`: `Path("/api/v1/admin/llm-providers/routing/config")` — literal path, does not conflict with `/llm-providers/{id}` wildcard (Python still owns that)

**Rollback:** Remove the two router blocks and restart Go bridge. Python resumes serving both routes immediately (still running, never disabled).

---

## Python API Contract Preservation

| Field | Python default | Go default |
|---|---|---|
| `heatmap_low` | 1 | 1 ✅ |
| `heatmap_medium` | 10 | 10 ✅ |
| `heatmap_high` | 50 | 50 ✅ |
| `edge_thin` | 1 | 1 ✅ |
| `edge_medium` | 10 | 10 ✅ |
| `edge_thick` | 50 | 50 ✅ |
| `panel_max_sessions` | 50 | 50 ✅ |
| `stats_window_seconds` | 300 | 300 ✅ |

| Field | Python default | Go default |
|---|---|---|
| `default_provider` | `"anthropic"` | `"anthropic"` ✅ |
| `default_model` | `"claude-sonnet-4-6"` | `"claude-sonnet-4-6"` ✅ |
| `fallback_provider` | `null` | `null` ✅ |
| `fallback_model` | `null` | `null` ✅ |

Validation: `heatmap_low < heatmap_medium < heatmap_high` → 422 (Python: pydantic model_validator ValueError → 422; Go: `unprocessable()` → `ErrUnprocessable` → `writeServiceError` → 422). ✅

---

## What Was NOT Started (Wave 7)

- LLM provider CRUD: GET/POST/PATCH/DELETE `/api/v1/admin/llm-providers`
- Fernet decryption/encryption of API keys
- No DB schema changes
- No new Redis keys

---

## Next Session

Wave 7 planning — see `CLAUDE.md` for handover procedure.
Suggested scope: LLM provider CRUD (GET/POST/PATCH/DELETE `/api/v1/admin/llm-providers`) with Fernet migration to Go's AES-128-CBC equivalent.
