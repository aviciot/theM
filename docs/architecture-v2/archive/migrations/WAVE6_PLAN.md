# Wave 6 Plan — Monitoring Config + LLM Routing Config
# Generated: 2026-07-25 (revised)
# Status: PLAN ONLY — no implementation started

---

## Executive Summary

Wave 6 migrates exactly 4 HTTP operations from Python to Go:

```
GET /api/v1/admin/monitoring-config
PUT /api/v1/admin/monitoring-config
GET /api/v1/admin/llm-providers/routing/config
PUT /api/v1/admin/llm-providers/routing/config
```

Both route groups operate exclusively on `them.config` (key/value JSONB table). Neither touches encrypted credentials. Neither has Redis state, external service calls, or orchestration dependencies. No new Go packages are needed — only one new DAL file, one new service file, two new handler files, and one new integration test file.

---

## 1. Crypto Dependency Analysis

**Does `GET/PUT /llm-providers/routing/config` require Fernet or encrypted credentials?**

No. The routing config handlers (`get_routing` / `set_routing`) access only `them.config WHERE config_key='llm_routing'`. The `config_value` JSONB contains only four plain-text string fields (`default_provider`, `default_model`, `fallback_provider`, `fallback_model`). No `api_key_encrypted` column is read or written. No `decrypt_value` / `encrypt_value` calls appear in either handler. Fernet is not required.

**Why the full `llm-providers` CRUD is deferred to Wave 7:**

The five remaining provider routes (`GET/POST /llm-providers`, `GET/PATCH/DELETE /llm-providers/{id}`) all interact with `api_key_encrypted` — either to decrypt-and-mask in responses (all GET variants) or to encrypt on write (POST, PATCH). Implementing those without a verified Go Fernet AES-128-CBC/HMAC-SHA256 helper would be unsafe. Wave 7 builds the crypto package first, then the full provider CRUD on top of it.

---

## 2. Selected Wave 6 Scope

| # | Method | Path | Python handler |
|---|---|---|---|
| 1 | GET | `/api/v1/admin/monitoring-config` | `get_monitoring_config` in `admin_monitoring_config.py` |
| 2 | PUT | `/api/v1/admin/monitoring-config` | `put_monitoring_config` in `admin_monitoring_config.py` |
| 3 | GET | `/api/v1/admin/llm-providers/routing/config` | `get_routing` in `admin_llm_providers.py` |
| 4 | PUT | `/api/v1/admin/llm-providers/routing/config` | `set_routing` in `admin_llm_providers.py` |

**Why these four belong together:** All four are config-table operations on `them.config`, all four are `RequireSuperAdmin` admin routes, none requires crypto, none has Redis or external dependencies, and all four are pure JSON in/JSON out with straightforward validation. From the frontend's perspective they are read on the same admin settings page load. Migrating all four in one wave keeps the settings page coherent.

---

## 3. Route Inventory

### Route 1: `GET /api/v1/admin/monitoring-config`

| Attribute | Detail |
|---|---|
| Python handler | `get_monitoring_config` — `app/routers/admin_monitoring_config.py:62` |
| DB table | `them.config WHERE config_key='monitoring'` |
| Redis | None |
| External calls | None |
| Auth | `RequireSuperAdmin` (JWT) |
| Response | `MonitoringConfig` — 8 integer fields |
| Default behaviour | Row absent → return hardcoded defaults; row present → return stored values |
| Fernet/crypto | None |

### Route 2: `PUT /api/v1/admin/monitoring-config`

| Attribute | Detail |
|---|---|
| Python handler | `put_monitoring_config` — `app/routers/admin_monitoring_config.py:69` |
| DB table | `them.config` — UPSERT on `config_key='monitoring'` |
| Redis | None |
| External calls | None |
| Auth | `RequireSuperAdmin` |
| Body | `MonitoringConfig` — same 8 fields, all required |
| Validation | `heatmap_low < heatmap_medium < heatmap_high` and `edge_thin < edge_medium < edge_thick` — 422 if violated |
| Response | `MonitoringConfig` (echo of stored values) |
| Fernet/crypto | None |

### Route 3: `GET /api/v1/admin/llm-providers/routing/config`

| Attribute | Detail |
|---|---|
| Python handler | `get_routing` — `app/routers/admin_llm_providers.py:186` |
| DB table | `them.config WHERE config_key='llm_routing'` |
| Redis | None |
| External calls | None |
| Auth | `RequireSuperAdmin` |
| Response | `LLMRoutingConfig` — 4 string fields with defaults if row absent |
| Fernet/crypto | None |

### Route 4: `PUT /api/v1/admin/llm-providers/routing/config`

| Attribute | Detail |
|---|---|
| Python handler | `set_routing` — `app/routers/admin_llm_providers.py:194` |
| DB table | `them.config` — UPSERT on `config_key='llm_routing'` |
| Redis | None |
| External calls | None |
| Auth | `RequireSuperAdmin` |
| Body | `LLMRoutingConfig` — 4 fields |
| Response | `LLMRoutingConfig` |
| Fernet/crypto | None |

---

## 4. DB Schema

### `them.config`

```sql
CREATE TABLE them.config (
    config_key   TEXT PRIMARY KEY,
    config_value JSONB NOT NULL DEFAULT '{}',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Config keys used by Wave 6:

| config_key | Used by |
|---|---|
| `'monitoring'` | Routes 1–2 |
| `'llm_routing'` | Routes 3–4 |

**No schema changes required.** Both keys already exist in the running schema.

---

## 5. Python API Contract Requirements

### MonitoringConfig — exact JSON shape

```json
{
  "heatmap_low": 1,
  "heatmap_medium": 10,
  "heatmap_high": 50,
  "edge_thin": 1,
  "edge_medium": 10,
  "edge_thick": 50,
  "panel_max_sessions": 50,
  "stats_window_seconds": 300
}
```

Defaults (applied when `config_key='monitoring'` row is absent):

```go
var monitoringDefaults = MonitoringConfig{
    HeatmapLow:         1,
    HeatmapMedium:      10,
    HeatmapHigh:        50,
    EdgeThin:           1,
    EdgeMedium:         10,
    EdgeThick:          50,
    PanelMaxSessions:   50,
    StatsWindowSeconds: 300,
}
```

Python merges defaults with stored values using `dict.update`. Go must do the same: unmarshal stored JSONB into a struct pre-filled with defaults (Go zero-value fields would give `0` for absent keys — wrong). Correct approach: initialise with defaults struct, then unmarshal stored JSONB on top (absent keys keep default values because Go json.Unmarshal does not zero fields that are missing from the JSON).

Field constraints (from Python Pydantic `Field(gt=0, le=10000)`):
- All 8 fields: `gt=0` (>0) and `le=10000` (≤10000)
- Python Pydantic enforces these on PUT. Go must validate the same bounds.
- `stats_window_seconds`: additionally `le=86400`.

Ordering invariants on PUT:
- `heatmap_low < heatmap_medium < heatmap_high` → 422 if violated
- `edge_thin < edge_medium < edge_thick` → 422 if violated

Error envelope: `{"detail": "..."}` — matches existing Go admin error format from `writeError`.

### LLMRoutingConfig — exact JSON shape

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-6",
  "fallback_provider": null,
  "fallback_model": null
}
```

Defaults (applied when `config_key='llm_routing'` row is absent):
- `default_provider`: `"anthropic"`
- `default_model`: `"claude-sonnet-4-6"`
- `fallback_provider`: `null`
- `fallback_model`: `null`

Python returns `LLMRoutingConfig()` (default constructor) when the row is absent — Go must return the same default values. `fallback_provider` and `fallback_model` are `*string` in Go (`null` in JSON when nil).

No body validation on PUT beyond JSON parsing (Python applies no ordering or range checks to routing config).

---

## 6. Handler → Service → DAL Structure

```
internal/
  admin/
    monitoring.go          ← MonitoringConfigHandler: Routes, Get, Put
    llm_routing.go         ← LLMRoutingConfigHandler: Routes, Get, Put
    router.go              ← mount new handlers (no new parameters needed)
    service/
      config.go            ← ConfigService: GetMonitoring, PutMonitoring, GetLLMRouting, PutLLMRouting
      config_test.go       ← unit tests for ConfigService
      service.go           ← extend Dal interface with GetConfig + UpsertConfig
    dal/
      config.go            ← ConfigRow type; GetConfig, UpsertConfig DAL functions
```

### Call chain (GET /monitoring-config)

```
GET /api/v1/admin/monitoring-config
  → MonitoringConfigHandler.Get(w, r)
      → svc.GetMonitoring(ctx)
          → dal.GetConfig(ctx, "monitoring")  →  *ConfigRow or nil
          → if nil: return monitoringDefaults
          → unmarshal ConfigRow.Value into MonitoringConfig (pre-filled with defaults)
          → return MonitoringConfig
      → writeJSON(w, 200, cfg)
```

### Call chain (PUT /monitoring-config)

```
PUT /api/v1/admin/monitoring-config
  → MonitoringConfigHandler.Put(w, r)
      → json.NewDecoder(r.Body).Decode(&input)  →  422 on bad JSON
      → svc.PutMonitoring(ctx, input)
          → validate bounds + ordering invariants  →  ErrValidation on violation
          → marshal input → jsonBytes
          → dal.UpsertConfig(ctx, "monitoring", jsonBytes)
          → return input
      → writeJSON(w, 200, result)
```

### Call chain (GET /llm-providers/routing/config)

```
GET /api/v1/admin/llm-providers/routing/config
  → LLMRoutingConfigHandler.Get(w, r)
      → svc.GetLLMRouting(ctx)
          → dal.GetConfig(ctx, "llm_routing")  →  *ConfigRow or nil
          → if nil: return llmRoutingDefaults
          → unmarshal ConfigRow.Value into LLMRoutingConfig (pre-filled with defaults)
          → return LLMRoutingConfig
      → writeJSON(w, 200, cfg)
```

### Call chain (PUT /llm-providers/routing/config)

```
PUT /api/v1/admin/llm-providers/routing/config
  → LLMRoutingConfigHandler.Put(w, r)
      → json.NewDecoder(r.Body).Decode(&input)
      → svc.PutLLMRouting(ctx, input)
          → marshal input → jsonBytes
          → dal.UpsertConfig(ctx, "llm_routing", jsonBytes)
          → return input
      → writeJSON(w, 200, result)
```

---

## 7. Dal Interface Extension

Add two methods to the `Dal` interface in `internal/admin/service/service.go`:

```go
// Config table
GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
UpsertConfig(ctx context.Context, key string, value []byte) error
```

`GetConfig` returns `nil, nil` (not an error) when the row does not exist. This matches Python's `db.get(Config, key)` returning `None`.

---

## 8. DAL Types and SQL

**`internal/admin/dal/config.go`**

```go
// ConfigRow is a row from them.config.
type ConfigRow struct {
    Key   string
    Value []byte  // raw JSONB bytes; caller unmarshal
}

// GetConfig returns the config row for key, or nil if not found.
func (d *DB) GetConfig(ctx context.Context, key string) (*ConfigRow, error) {
    const q = `SELECT config_key, config_value::text FROM them.config WHERE config_key=$1`
    var row ConfigRow
    err := d.q.QueryRow(ctx, q, key).Scan(&row.Key, &row.Value)
    if dal.IsNoRows(err) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    return &row, nil
}

// UpsertConfig inserts or updates a config row.
func (d *DB) UpsertConfig(ctx context.Context, key string, value []byte) error {
    const q = `
        INSERT INTO them.config (config_key, config_value, updated_at)
        VALUES ($1, $2::jsonb, now())
        ON CONFLICT (config_key) DO UPDATE
          SET config_value = EXCLUDED.config_value,
              updated_at   = now()`
    return d.q.Exec(ctx, q, key, string(value))
}
```

---

## 9. Implementation Phases and Commit Boundaries

### Phase 1 — DAL: config table (1 commit)

**Files:** `internal/admin/dal/config.go`

1. Define `ConfigRow` type.
2. Implement `GetConfig(ctx, key) (*ConfigRow, error)`.
3. Implement `UpsertConfig(ctx, key string, value []byte) error`.
4. Add `GetConfig` and `UpsertConfig` to the `Dal` interface in `internal/admin/service/service.go`.
5. Add stub implementations to the fake DAL in `internal/admin/service/service_test.go` (or a shared fake file) so existing tests compile.
6. Run: `go test ./internal/admin/...` — all existing tests must pass.

**Commit:** `feat(admin/dal): add config table GetConfig + UpsertConfig`

### Phase 2 — Service: ConfigService (1 commit)

**Files:** `internal/admin/service/config.go`, `internal/admin/service/config_test.go`

1. Define Go types:
   ```go
   type MonitoringConfig struct {
       HeatmapLow         int `json:"heatmap_low"`
       HeatmapMedium      int `json:"heatmap_medium"`
       HeatmapHigh        int `json:"heatmap_high"`
       EdgeThin            int `json:"edge_thin"`
       EdgeMedium          int `json:"edge_medium"`
       EdgeThick           int `json:"edge_thick"`
       PanelMaxSessions    int `json:"panel_max_sessions"`
       StatsWindowSeconds  int `json:"stats_window_seconds"`
   }

   type LLMRoutingConfig struct {
       DefaultProvider  string  `json:"default_provider"`
       DefaultModel     string  `json:"default_model"`
       FallbackProvider *string `json:"fallback_provider"`
       FallbackModel    *string `json:"fallback_model"`
   }
   ```
2. Implement `ConfigService{dal Dal}` with four methods: `GetMonitoring`, `PutMonitoring`, `GetLLMRouting`, `PutLLMRouting`.
3. `GetMonitoring`: pre-fill result with defaults, unmarshal stored JSONB on top, return.
4. `PutMonitoring`: validate all 8 fields `>0` and `≤10000` (`stats_window_seconds` also `≤86400`); validate ordering invariants; marshal; call `UpsertConfig`.
5. `GetLLMRouting`: pre-fill with defaults, unmarshal stored JSONB on top, return.
6. `PutLLMRouting`: no validation beyond JSON parsing; marshal; call `UpsertConfig`.
7. Unit tests in `config_test.go`:
   - `TestConfigService_GetMonitoring_NoRow_ReturnsDefaults`
   - `TestConfigService_GetMonitoring_StoredValues_Returned`
   - `TestConfigService_PutMonitoring_ValidInput_UpsertsCalled`
   - `TestConfigService_PutMonitoring_BadOrdering_Heatmap_422`
   - `TestConfigService_PutMonitoring_BadOrdering_Edge_422`
   - `TestConfigService_PutMonitoring_ZeroField_422`
   - `TestConfigService_PutMonitoring_FieldOverLimit_422`
   - `TestConfigService_GetLLMRouting_NoRow_ReturnsDefaults`
   - `TestConfigService_GetLLMRouting_StoredValues_Returned`
   - `TestConfigService_PutLLMRouting_UpsertsCalled`
8. Run: `go test ./internal/admin/...`

**Commit:** `feat(admin/service): add ConfigService for monitoring + llm routing config`

### Phase 3 — HTTP handlers (1 commit)

**Files:** `internal/admin/monitoring.go`, `internal/admin/llm_routing.go`, update `router.go`

1. `MonitoringConfigHandler` with `Routes(r chi.Router)`:
   ```go
   r.Get("/monitoring-config", h.Get)
   r.Put("/monitoring-config", h.Put)
   ```
2. `LLMRoutingConfigHandler` with `Routes(r chi.Router)`:
   ```go
   r.Get("/llm-providers/routing/config", h.Get)
   r.Put("/llm-providers/routing/config", h.Put)
   ```
   **Path note:** These literal routes do not conflict with `/{id}` because chi routes
   literal path segments at higher priority than wildcards when registered correctly.
   No ordering change needed in the existing agents/providers routes since
   `llm-providers/routing/config` is a completely separate chi sub-path, not a
   `/{id}` sub-route of any existing handler.
3. Update `router.go`: instantiate and mount both handlers inside the existing
   `admin.Route("/admin", ...)` block. No new parameters to `BuildRouter`.
4. Add handler-level unit tests to `admin_test.go`:
   - `TestGetMonitoringConfig_NoRow_ReturnsDefaults`
   - `TestPutMonitoringConfig_InvalidOrdering_422`
   - `TestPutMonitoringConfig_ValidInput_200`
   - `TestGetLLMRoutingConfig_NoRow_ReturnsDefaults`
   - `TestPutLLMRoutingConfig_Valid_200`
5. Run: `go test ./internal/admin/...`

**Commit:** `feat(admin): add monitoring-config + llm routing config HTTP handlers`

### Phase 4 — Traefik cutover (1 commit)

**Files:** `docker-compose.yml` (Traefik labels only)

Add labels targeting Go bridge (priority 120):
```yaml
- "traefik.http.routers.go-admin-monitoring.rule=PathPrefix(`/api/v1/admin/monitoring-config`)"
- "traefik.http.routers.go-admin-monitoring.priority=120"
- "traefik.http.routers.go-admin-llm-routing.rule=Path(`/api/v1/admin/llm-providers/routing/config`)"
- "traefik.http.routers.go-admin-llm-routing.priority=120"
```

Using `Path` (exact match) for the routing/config route avoids accidentally capturing the full `llm-providers/*` namespace which remains Python-owned in Wave 6.

Rebuild and restart Go bridge. Run smoke tests (see §11). Run Python suite.

**Commit:** `cutover(wave6): enable Traefik routing for monitoring-config + llm routing config`

### Phase 5 — Documentation update (1 commit)

**Files:** `docs/architecture-v2/implementation-status.md`, `go/TEST_INDEX.md`

1. Add 4 routes to the Go-owned route map in `implementation-status.md`.
2. Add S1-26 (ConfigService unit tests) and handler test additions to S1-15 in `TEST_INDEX.md`.
3. Update total test count.
4. Update trigger maps in `go/CLAUDE.md` for `internal/admin/service/config.go` and `internal/admin/dal/config.go`.

**Commit:** `docs(wave6): update implementation status + test index`

---

## 10. Test Plan

### Unit tests (`go test ./internal/admin/...`)

| Suite | File | Tests |
|---|---|---|
| S1-26 | `service/config_test.go` | 10 (listed in Phase 2) |
| S1-15 (extend) | `admin_test.go` | +5 (listed in Phase 3) |

Run after each phase commit. Zero new failures allowed.

### Integration tests

Add `internal/admin/config_integration_test.go` (build tag `integration`). Run against live Postgres:

```bash
TEST_POSTGRES_DSN="host=localhost port=15432 dbname=them user=them password=them_secret sslmode=disable" \
go test -tags=integration -v ./internal/admin/...
```

| Test | What it proves |
|---|---|
| `TestIntegration_GetMonitoringConfig_Defaults` | GET with no stored row → 8 default values |
| `TestIntegration_PutMonitoringConfig_200` | PUT valid config → 200, row persisted |
| `TestIntegration_PutMonitoringConfig_InvalidOrdering_422` | PUT with `heatmap_low >= heatmap_medium` → 422 |
| `TestIntegration_GetMonitoringConfig_ReturnsStoredValues` | PUT then GET → same values returned |
| `TestIntegration_GetLLMRoutingConfig_Defaults` | GET with no stored row → default provider/model |
| `TestIntegration_PutLLMRoutingConfig_200` | PUT valid routing config → 200 |
| `TestIntegration_GetLLMRoutingConfig_ReturnsStoredValues` | PUT then GET → same values |

### Contract tests (before cutover, direct port comparison)

Both Go (port 8002) and Python (port 8001) must return identical JSON for these cases:

```bash
TOKEN=$(curl -s -X POST http://localhost:8701/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

# Monitoring config — defaults match
diff \
  <(curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8001/api/v1/admin/monitoring-config) \
  <(curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8002/api/v1/admin/monitoring-config)

# LLM routing config — defaults match
diff \
  <(curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8001/api/v1/admin/llm-providers/routing/config) \
  <(curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8002/api/v1/admin/llm-providers/routing/config)
```

Expected: zero diff. Any difference is a contract bug to fix before cutover.

### Live cutover smoke tests (after Traefik labels applied)

```bash
# Confirm Go bridge handles the requests (check logs)
docker logs them-go-bridge --tail 20 | grep -E "monitoring-config|routing/config"

# Round-trip through Traefik
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/monitoring-config | python3 -m json.tool

curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/v1/admin/llm-providers/routing/config | python3 -m json.tool

# PUT round-trip
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"heatmap_low":2,"heatmap_medium":20,"heatmap_high":100,"edge_thin":1,"edge_medium":5,"edge_thick":25,"panel_max_sessions":50,"stats_window_seconds":300}' \
  http://localhost:8088/api/v1/admin/monitoring-config | python3 -m json.tool
```

### Python suite (after cutover)

```bash
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 32
```

Test 32 covers `admin_monitoring_config.py` routes. After cutover these requests hit Go. Expected: same pass count as before cutover.

---

## 11. Rollback Plan

These routes are read-heavy admin UI routes with no side effects beyond the `them.config` table.

1. Remove the two Traefik labels added in Phase 4 from `docker-compose.yml`.
2. Rebuild and restart Go bridge: `docker compose --profile go up -d them-go-bridge them-go-bridge-2`
3. Traefik falls back to Python at lower priority — no Python changes required.
4. No DB migration to reverse — no schema changes.
5. No Redis state to clean — no Redis keys touched by these routes.

Rollback time: ~2 minutes.

---

## 12. Missing Go Components

| Component | Where | Status |
|---|---|---|
| `ConfigRow` type + `GetConfig` + `UpsertConfig` | `internal/admin/dal/config.go` | **New — Phase 1** |
| `ConfigService` + 4 methods | `internal/admin/service/config.go` | **New — Phase 2** |
| `MonitoringConfigHandler` | `internal/admin/monitoring.go` | **New — Phase 3** |
| `LLMRoutingConfigHandler` | `internal/admin/llm_routing.go` | **New — Phase 3** |
| `Dal` interface extensions (2 methods) | `internal/admin/service/service.go` | **Modified — Phase 1** |
| Router mount | `internal/admin/router.go` | **Modified — Phase 3** |
| Traefik labels | `docker-compose.yml` | **Modified — Phase 4** |

**No new packages.** All new code lives inside existing `internal/admin/` package tree.

**No Fernet/crypto component.** Not required by any of the four selected routes.

---

## 13. Wave 7 Deferred Scope

The following routes are explicitly out of scope for Wave 6:

| Route | Reason for deferral |
|---|---|
| `GET /api/v1/admin/llm-providers` | Requires Fernet decrypt to produce masked API key in response |
| `POST /api/v1/admin/llm-providers` | Requires Fernet encrypt to store API key |
| `GET /api/v1/admin/llm-providers/{id}` | Requires Fernet decrypt + masking |
| `PATCH /api/v1/admin/llm-providers/{id}` | Requires conditional Fernet re-encrypt on key rotation |
| `DELETE /api/v1/admin/llm-providers/{id}` | Trivial SQL but belongs with the rest of provider CRUD |
| Fernet AES-128-CBC/HMAC-SHA256 helper | Prerequisite for all provider CRUD; build first in Wave 7 |

Wave 7 recommended order:
1. `internal/crypto/fernet.go` + known-vector tests (decrypt a Python-generated ciphertext)
2. `internal/admin/dal/providers.go` — `LLMProvider` type + 5 DAL functions
3. `internal/admin/service/providers.go` — `LLMProviderService` with masking
4. `internal/admin/providers.go` — HTTP handler (5 operations)
5. Traefik cutover for `ALL /api/v1/admin/llm-providers*` (excluding `/routing/config` already Go-owned after Wave 6)

Also deferred to later waves:
- `POST /admin/agents/{id}/test`, `POST /admin/agents/discover`, `POST /admin/agents/{id}/security-scan`
- `GET/POST/PUT /admin/applications/export|import|restore` (requires graph compiler port)
- `POST /admin/applications/bulk-delete`, `PUT /{id}/runtime` (requires orch-cache key migration)
- `PUT /admin/orchestrators/{name}/test-llm` (requires LLM provider Go client)
- `/ws/dashboard`, one-segment `/ws/orchestrate/{name}`

---

## 14. Files Changed Summary

**New files (5):**
- `internal/admin/dal/config.go`
- `internal/admin/service/config.go`
- `internal/admin/service/config_test.go`
- `internal/admin/monitoring.go`
- `internal/admin/llm_routing.go`

**Modified files (6):**
- `internal/admin/service/service.go` — extend `Dal` interface
- `internal/admin/router.go` — mount two new handlers
- `internal/admin/admin_test.go` — add 5 handler-level tests
- `docker-compose.yml` — add 2 Traefik label pairs (Phase 4)
- `go/TEST_INDEX.md` — add S1-26, extend S1-15 (Phase 5)
- `docs/architecture-v2/implementation-status.md` — add 4 Go-owned routes (Phase 5)

**No changes to:**
- `db/001_schema.sql` — no schema changes
- `docs/REDIS.md` — no Redis keys
- `app/` — no Python changes
- `traefik/traefik.yml` — no Traefik config changes
- `internal/crypto/` — Fernet deferred to Wave 7

---

## 15. First Implementation Task

Generate a test vector from the live Python stack (needed to verify Fernet in Wave 7, not Wave 6 — noted here for completeness):

```bash
docker exec them-bridge python3 -c "
from app.utils.crypto import encrypt_value
print(repr(encrypt_value('test-api-key-1234')))
"
```

**Wave 6 actual first task:** Implement `internal/admin/dal/config.go` — the `ConfigRow` type, `GetConfig`, and `UpsertConfig` functions. This is the only dependency required before the service and handler layers can be written.

Use **Sonnet** for all Wave 6 implementation phases. No architecture decisions remain open.
