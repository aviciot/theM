# Wave 8 — Application Special Operations: Migration Review
# Date: 2026-08-02
# Branch: main @ 28b781d
# Scope: Five Python-owned Application admin endpoints — migration or deferral decision.

---

## Purpose

This document reviews the five Python-only Application Special Operations endpoints
to decide whether each should be migrated to Go in Wave 8, deferred, redesigned, or
deprecated. It does not implement any Go handlers. It does not port `app_compiler.py`.

---

## Endpoints Under Review

| # | Method | Path | Python handler |
|---|---|---|---|
| 1 | GET | `/api/v1/admin/applications/{id}/export` | `admin_applications.py:807` |
| 2 | POST | `/api/v1/admin/applications/import` | `admin_applications.py:828` |
| 3 | PUT | `/api/v1/admin/applications/{id}/restore` | `admin_applications.py:867` |
| 4 | POST | `/api/v1/admin/applications/bulk-delete` | `admin_applications.py:618` |
| 5 | PUT | `/api/v1/admin/applications/{id}/runtime` | `admin_applications.py:780` |

---

## Shared Infrastructure Context

Before reviewing each endpoint, note what is already available in Go:

| Component | Status |
|---|---|
| `internal/crypto` — Fernet encrypt/decrypt | **Exists** (`go/internal/crypto/fernet.go`) |
| `internal/admin/dal` — applications CRUD DAL | **Exists** (`go/internal/admin/dal/applications.go`) |
| `internal/epconfig` — runtime_config parser | **Exists**, reads all relevant fields |
| `internal/epconfig.Loader.InvalidateApp()` | **Exists** — evicts cached configs by app_id |
| `internal/admin.CacheInvalidator` | **Exists** — Redis pub/sub flush for EP config |
| `compile_graph` / `export_graph` equivalent | **Does NOT exist** in Go |
| `validate_graph` equivalent | **Does NOT exist** in Go |
| App orchestrator upsert/delete logic | **Does NOT exist** in Go (Go Create/Update are stubs) |

---

## Endpoint 1 — GET `/api/v1/admin/applications/{id}/export`

### What it does

Returns a portable JSON snapshot of an application:

```json
{
  "name": "my-app",
  "presentation": {...},
  "graph": {
    "nodes": [...],  // orchestrator + entryPoint + agent + middleware nodes
    "edges": [...]
  },
  "canvas": {...}   // React Flow layout positions
}
```

The snapshot is the inverse of `compile_graph`: it calls `export_graph()` which reads
`app_orchestrators`, `entry_points`, and `middleware_wirings` rows and reconstructs the
graph representation. **API keys are NOT exported** — only `llm_api_key_hint` appears;
the encrypted column is never serialized.

### Service/DAL calls

1. `_get_or_404(db, app_id)` — loads application with `entry_points`, `app_orchestrators`, `middleware_wirings`
2. `export_graph(entry_points, ao_list, mw_wirings, canvas)` — pure Python function, no DB I/O

### Redis/cache side effects

None. Export is read-only.

### Authorization

No explicit `require_admin` decorator — inherits the router-level admin JWT middleware.

### Frontend callers

**None.** Neither `api.ts` nor the applications page (`page.tsx`) calls `/export`.
This endpoint is not wired to any UI element. It is a backend-to-backend or CLI-only
operation (drag-and-drop or programmatic backup).

### Test coverage

Test 30 (`test_30_graph_compiler`) verifies `export_graph` is defined and callable but
does not test the HTTP endpoint end-to-end.

### Go dependencies missing

`export_graph` is the only dependency. It is a pure function (no DB I/O) that converts
relational rows to a graph JSON structure. It must be ported to Go, but the output
schema is stable and well-defined. **Fernet crypto is NOT required** — API keys are
excluded from the export.

### Analysis

- **Required by product?** Not exposed in UI. Used programmatically for backup/clone workflows.
- **Contract worth preserving?** Yes — the `{name, graph, canvas}` format is the stable interchange between export/import/restore. Changing it breaks round-trips.
- **Legacy structure dependency?** No — the graph format captures the current relational model accurately.
- **Throwaway work risk?** Low. The graph node format is tied to the React Flow canvas model, which is intentionally the stable Application Definition format going forward.
- **Migration complexity:** Low-Medium. Port `export_graph()` (~100 lines) to Go. No DB writes. No crypto.

### Recommendation: **migrate in Wave 8**

The export function is the foundation for import and restore. It is a pure read that converts existing relational rows to JSON — no business logic complexity. Without it, import and restore cannot be migrated either.

---

## Endpoint 2 — POST `/api/v1/admin/applications/import`

### What it does

Creates a new application from an exported JSON snapshot. Accepts `ApplicationExport`
body (the same format as export). Creates a fresh `applications` row, then calls
`compile_graph()` to populate `app_orchestrators`, `entry_points`, and
`middleware_wirings`.

### Service/DAL calls

1. `AppGraph(nodes, edges)` — Pydantic deserialization
2. `validate_graph(graph)` — structural validation (node types, slug format, EP→orch connectivity, no orphan orchestrators)
3. `db.add(Application(...))` + `db.flush()` — creates application row
4. `_load_all_orch_names(db)` — loads all existing orchestrator names for uniqueness check
5. `compile_graph(...)` — full graph compiler: upserts app_orchestrators, upserts entry_points, replaces middleware_wirings. **Calls `encrypt_value()`** for any LLM API key fields present in the node data.
6. `db.commit()`
7. `_flush_orch_caches(app_id, touched_names)` — deletes `them:app:{app_id}:orch:{name}`, `them:orch:loc:{name}`, `them:agents:registry`; publishes `them:ep:config:changed`

### Redis/cache side effects

- `DELETE them:app:{app_id}:orch:{name}` for each touched orchestrator
- `DELETE them:orch:loc:{name}` for each touched orchestrator
- `DELETE them:agents:registry`
- `PUBLISH them:ep:config:changed {app_id}` — triggers Go `epconfig.Loader.InvalidateApp()`

### Authorization

Admin JWT middleware (router-level).

### Crypto dependency

`compile_graph()` calls `encrypt_value(api_key)` when an LLM API key is present in graph node data. The `internal/crypto` Fernet implementation already exists in Go — this is not a blocker.

### Frontend callers

**None.** `api.ts` does not expose `importApplication`. The frontend has no import UI.

### Test coverage

Test 30 verifies `compile_graph` is called and `export_graph` is defined. No HTTP-level import test exists.

### Go dependencies missing

1. `validate_graph()` — ~60 lines of pure structural validation
2. `compile_graph()` — ~265 lines; the largest single piece of logic to port. Handles:
   - slug conflict check (DB query)
   - agent existence check (DB query)
   - app_orchestrators upsert keyed by `node_id`
   - entry_points upsert keyed by `slug`
   - middleware_wirings full-replace
   - agent list derivation from graph edges (`_resolve_agents_for_orch`)
   - middleware chain resolution (`_resolve_mw_chains`)
   - orchestrator name generation with collision avoidance
   - Fernet encryption of API key fields
3. `_flush_orch_caches()` — the Redis side of `compile_graph`. Most of the cache key logic is already modeled in `CacheInvalidator` but the specific key pattern for `them:app:{app_id}:orch:{name}` + `them:agents:registry` is not wired in Go admin yet.

### Analysis

- **Required by product?** Not exposed in UI. CLI/backup workflow only.
- **Contract worth preserving?** Yes — the `ApplicationExport` body is the stable import format.
- **Legacy structure dependency?** No — the graph format maps directly to current relational schema.
- **Throwaway work risk:** Medium. `compile_graph` is complex and touches `app_orchestrators` + `middleware_wirings` — tables Go's admin handlers currently only partially manage (no middleware_wirings DAL exists in Go at all).
- **Migration complexity:** High. `compile_graph` requires porting ~340 lines of business logic including graph traversal, Fernet crypto, and multi-table transactional writes. It is the largest single porting effort in the Applications domain.

### Recommendation: **defer to Wave 9 (after export is live and validated)**

Import depends on `export_graph` and `compile_graph`. `compile_graph` also drives `restore`. Porting all three together is the right unit of work. Import alone without restore is not useful. The correct wave scope is: export first, then import+restore together once `compile_graph` is ported.

---

## Endpoint 3 — PUT `/api/v1/admin/applications/{id}/restore`

### What it does

Overwrites an existing application from an exported JSON snapshot. Differs from import:
- Accepts an `{id}` in the path (the existing app to overwrite)
- Calls `compile_graph()` with `existing_entry_points` and `existing_ao_list` populated — this triggers the diff logic (upserts vs creates, deletes removed nodes)
- Updates `app.name`, `app.presentation`, `app.canvas` from the snapshot

### Service/DAL calls

Same as import plus `_get_or_404(db, app_id)` to fetch the existing application.

### Differences from import

The key difference is that `compile_graph()` is called in "update" mode: it receives the
existing rows so it can diff against them (upsert existing AOs by `node_id`, delete AOs
no longer in the graph, upsert EPs by slug). This logic is already inside `compile_graph()`
— the caller simply passes the existing lists.

### Redis/cache side effects

Same as import.

### Frontend callers

**None.** No restore UI in the current frontend.

### Test coverage

Test 30 verifies `export_graph` and `compile_graph` are called in the restore endpoint.

### Go dependencies missing

Same as import. Restore is the "update" flavor of import — it does not add new Go dependencies beyond what import requires.

### Analysis

- **Required by product?** Not exposed in UI. CLI/backup workflow only.
- **Migration complexity:** High (same as import — `compile_graph` dominates).
- **Throwaway work risk:** Medium. Same as import.

### Recommendation: **defer to same wave as import**

Restore and import are the same porting unit. They share `compile_graph` entirely. Porting them separately would duplicate the work and risk inconsistency.

---

## Endpoint 4 — POST `/api/v1/admin/applications/bulk-delete`

### What it does

Deletes up to 200 applications by UUID list in a single request.

### Service/DAL calls

1. `select(Application).where(Application.id.in_(app_ids)).options(selectinload(app_orchestrators))` — pre-load orchestrator names for cache flush
2. `delete(Application).where(Application.id.in_(app_ids))` — single SQL DELETE
3. `db.commit()`
4. `_flush_orch_caches(app_id, orch_names)` — per-app loop after commit

### Transaction behavior

- Pre-load and delete are in the same SQLAlchemy session
- Commit is a single transaction covering all deletions
- Cache flush happens AFTER commit (best-effort, not transactional)
- Partial failure: if DB delete fails, the transaction rolls back — no apps are deleted. If cache flush fails, the DB is consistent but stale cache entries remain (they expire in 30s via TTL).

### Cascade behavior

`applications` → `entry_points`, `app_orchestrators`, `middleware_wirings` all have `ON DELETE CASCADE`. The `DELETE applications WHERE id IN (...)` implicitly cascades to all child rows in a single DB-level operation. No explicit child deletion needed.

**Runs are NOT cascaded.** `runs.orchestrator_id` references `them.orchestrators` (the global orchestrator table), not `applications`. Deleting an application does not delete its historical runs.

### Limits

Hard limit: `max_length=200` on `app_ids`. No pagination. Empty list returns `{"deleted": 0}` immediately.

### Frontend callers

`api.ts:482`: `bulkDeleteApplications(appIds)` → `POST /admin/applications/bulk-delete`
`page.tsx:5014`: `handleBulkDelete()` → `themApi.bulkDeleteApplications(Array.from(selectedApps))`

**Active UI caller exists** — the bulk-select checkbox and "Delete Selected" button in the applications list view.

### Go dependencies missing

1. `DELETE WHERE id IN (...)` — standard SQL, trivially portable
2. Per-app Redis cache flush (`them:app:{app_id}:orch:{name}`, `them:orch:loc:{name}`, `them:agents:registry`, `PUBLISH them:ep:config:changed`) — existing `CacheInvalidator` interface must be extended
3. Pre-load of orchestrator names before delete (needed to know which keys to flush)

### Analysis

- **Required by product?** **Yes** — active UI caller in the applications list page.
- **Contract worth preserving?** Yes — `{app_ids: [...]}` is the stable contract.
- **Legacy structure dependency?** No — uses standard UUID list delete.
- **Throwaway work risk:** Low. Bulk delete is not tied to the graph/canvas model; it operates at the application row level only.
- **Migration complexity:** Low-Medium. The SQL and cascade are simple. The only non-trivial piece is the per-app orchestrator cache flush, which requires loading orchestrator names before the delete. The Go `CacheInvalidator` needs two new keys: `them:app:{app_id}:orch:{name}` and `them:orch:loc:{name}`.

### Recommendation: **migrate in Wave 8**

Bulk delete has an active UI caller, simple SQL logic, no graph/compiler dependency, and no crypto requirement. It is the most straightforward of the five endpoints and should be Wave 8's first implementation target.

---

## Endpoint 5 — PUT `/api/v1/admin/applications/{id}/runtime`

### What it does

Replaces an application's `runtime_config` JSONB column with the provided payload:

```json
{
  "max_concurrent_sessions": 10,
  "rate_limit_rpm": 60,
  "blocked_tokens": ["sha256hex1", "sha256hex2"],
  "blocked_user_ids": [42, 99],
  "session_timeout_minutes": null   // defined but not consumed
}
```

The Python schema is `AppRuntimeConfig` (5 fields). Go's `epconfig.parseRuntimeConfig`
currently parses 4 of those 5 fields (`max_concurrent_sessions`, `rate_limit_rpm`,
`blocked_tokens`, `blocked_user_ids`). The `session_timeout_minutes` field is defined
in Python but is **never consumed** by any runtime enforcement code in Python or Go.

### Service/DAL calls

1. `_get_or_404(db, app_id)` — loads application row
2. `app.runtime_config = body.model_dump()` — sets JSONB column
3. `db.commit()`
4. `_flush_orch_caches(app_id, [ao.name for ao in app.app_orchestrators])` — triggers `epconfig` cache eviction via `PUBLISH them:ep:config:changed {app_id}`

### Redis/cache side effects

- `DELETE them:app:{app_id}:orch:{name}` for each orchestrator
- `DELETE them:orch:loc:{name}` for each orchestrator
- `DELETE them:agents:registry`
- `PUBLISH them:ep:config:changed {app_id}` — triggers Go `epconfig.Loader.InvalidateApp(app_id)`

The Go `epconfig.Loader` already handles this pub/sub channel correctly: receiving a UUID-shaped payload evicts all EP configs belonging to that application.

### Go epconfig schema alignment

The Go `appRuntimeConfig` struct already parses all 4 meaningful fields. The schema is
a plain JSONB column with no migrations needed. The `session_timeout_minutes` field
should be added to the Go schema for future use but is not blocking.

### Frontend callers

`api.ts:486`: `putAppRuntime(appId, config)` → `PUT /admin/applications/{appId}/runtime`
`page.tsx:4013`: `onRuntime` handler in `RuntimeView` → `themApi.putAppRuntime(app.id, payload)`

**Active UI caller exists** — the Runtime panel in the applications admin page.

### Go dependencies missing

1. `UPDATE applications SET runtime_config = $1 WHERE id = $2` — trivial SQL, 2 lines
2. Cache flush: the existing `CacheInvalidator.InvalidateApp(appID)` pub/sub path already works. The additional `them:app:{app_id}:orch:*` key deletion is missing from Go (same gap as bulk-delete).
3. Pre-load of orchestrator names before flush — same pattern as bulk-delete.

### Analysis

- **Required by product?** **Yes** — active UI caller in the RuntimeView panel.
- **Contract worth preserving?** Yes — the `AppRuntimeConfig` schema is consumed at runtime by Go's `epconfig` loader. Schema changes would require coordinated updates to the consumer.
- **Legacy structure dependency?** No — `runtime_config` is a plain JSONB field with no graph/canvas coupling.
- **Throwaway work risk:** Low. The `runtime_config` schema is stable and is the correct model going forward (Go already enforces it at connection time).
- **Migration complexity:** Low. Single `UPDATE` + cache flush. The cache flush pattern is identical to bulk-delete, so once that gap is filled for bulk-delete, runtime gets it for free.

### Recommendation: **migrate in Wave 8 (after bulk-delete, shares cache flush implementation)**

---

## Decision Table

| Endpoint | UI Caller? | Go crypto needed? | Graph compiler needed? | Complexity | Recommendation |
|---|---|---|---|---|---|
| GET `/{id}/export` | No | No | `export_graph` only (pure) | Low-Med | **Migrate Wave 8** |
| POST `/import` | No | Yes (Fernet) | `compile_graph` (full) | High | **Defer to Wave 9** |
| PUT `/{id}/restore` | No | Yes (Fernet) | `compile_graph` (full) | High | **Defer to Wave 9** |
| POST `/bulk-delete` | **Yes** | No | No | Low-Med | **Migrate Wave 8** |
| PUT `/{id}/runtime` | **Yes** | No | No | Low | **Migrate Wave 8** |

---

## Approved Wave 8 Scope

Wave 8 will migrate **three** of the five endpoints:

```
POST /api/v1/admin/applications/bulk-delete
PUT  /api/v1/admin/applications/{id}/runtime
GET  /api/v1/admin/applications/{id}/export
```

**Implementation order (dependencies determine sequence):**

1. **`PUT /{id}/runtime`** — Simplest: single UPDATE + cache flush. Establishes the `CacheInvalidator` extension pattern used by the next two.
2. **`POST /bulk-delete`** — Requires the same cache flush extension. Depends on pattern from step 1.
3. **`GET /{id}/export`** — Port `export_graph()`. No writes. Establishes the graph serialization format needed for Wave 9 (import/restore).

**Rationale for this order:**
- Steps 1 and 2 have active UI callers — they are the most urgent from a product perspective.
- Step 3 has no UI caller but is the prerequisite for Wave 9. Migrating `export_graph` in Wave 8 proves the round-trip format before Wave 9 adds the write paths.
- Steps 1 and 2 share the same cache flush extension, so they should be implemented in the same commit or back-to-back.

---

## Deferred to Wave 9

| Endpoint | Why deferred |
|---|---|
| POST `/admin/applications/import` | Requires `compile_graph` — largest porting effort (~340 lines incl. crypto) |
| PUT `/admin/applications/{id}/restore` | Same dependency as import; port together |

Wave 9 scope: **port `compile_graph` + `validate_graph` to Go**, then wire import and restore. The Go Fernet crypto (`internal/crypto`) is already available.

---

## Required Go Infrastructure Changes for Wave 8

### 1. Cache flush extension — `CacheInvalidator` interface

Current `CacheInvalidator` in Go handles EP config pub/sub (`them:ep:config:changed`).
Wave 8 adds two new key patterns for the orch-level cache flush:

```
DELETE them:app:{app_id}:orch:{name}   (per orchestrator name)
DELETE them:orch:loc:{name}            (per orchestrator name)
DELETE them:agents:registry            (single shared key)
PUBLISH them:ep:config:changed {app_id}  (already implemented)
```

The interface needs a new method: `FlushApplicationOrchCaches(ctx, appID string, orchNames []string) error`

This must pre-load orchestrator names from DB before deletion (runtime endpoint) or from the pre-loaded list (bulk-delete). In both cases a small DAL query is needed: `SELECT name FROM app_orchestrators WHERE application_id = $1`.

### 2. `export_graph` implementation

A pure Go function converting `[]AppOrchestratorsRow + []EntryPointRow + []MiddlewareWiringRow` → `ApplicationExport` JSON. No DB writes. Mirrors Python `export_graph()` in `app_compiler.py:556-651`.

Output format (stable):
```json
{
  "nodes": [...orchestrator, entryPoint, agent, middleware nodes],
  "edges": [...edges],
  "canvas": {}
}
```

### 3. DAL additions

- `ListAppOrchestrators(ctx, appID) ([]AppOrchNameRow, error)` — for runtime + bulk-delete cache flush pre-load
- `BulkDeleteApplications(ctx, ids []string) (int, error)` — `DELETE WHERE id = ANY($1)`, returns rows affected
- `GetApplicationForRuntime(ctx, appID) (*AppRuntimeRow, error)` — for export: full row with EPs, AOs, MW wirings
- `UpdateRuntimeConfig(ctx, appID string, config []byte) error` — `UPDATE applications SET runtime_config = $1 WHERE id = $2`

---

## Required Contract Tests for Wave 8

| Test | Verifies |
|---|---|
| `GET /export` returns `{name, graph, canvas}` matching the DB state | Export serialization correctness |
| `GET /export` → `POST /import` → compare original vs imported app structure | Round-trip integrity (Wave 9 pre-req) |
| `POST /bulk-delete` deletes all listed apps, cascades to EPs and AOs | Delete + cascade |
| `POST /bulk-delete` with empty list returns `{"deleted": 0}` | Empty-list guard |
| `POST /bulk-delete` with non-existent IDs returns `{"deleted": 0}` | Graceful handling of unknown IDs |
| `PUT /{id}/runtime` updates `runtime_config`, subsequent GET reflects change | Write + read-back |
| `PUT /{id}/runtime` triggers cache eviction within 30s | Cache invalidation live test |
| `PUT /{id}/runtime` with empty `{}` clears limits (unlimited) | Default/clear behavior |

---

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| `export_graph` output format differs between Python and Go | Medium | Add round-trip test: export via Go, import via Python, compare |
| Cache flush for `them:app:{app_id}:orch:{name}` uses different key format in different services | Medium | Grep all producers/consumers of this key pattern before implementation |
| Bulk-delete partial failure (commit succeeds, cache flush fails) | Low | Already accepted in Python — log and continue. Cache TTL (30s) bounds staleness |
| `session_timeout_minutes` in `AppRuntimeConfig` never consumed | Low | Document as no-op; include in Go schema for completeness but do not enforce |
| Go `applications` handler currently has no tenant-scoped queries for these new operations | Low | All Go admin handlers use `tenantctx.MustTenantIDFromCtx` — same pattern applies |

---

## Traefik Routing Note

The Wave 8 endpoints all fall under existing Traefik rules that already route to Go after the routing fix commit (`28b781d`):

- `POST /bulk-delete` — matches `PathPrefix(/api/v1/admin/applications)` POST → `them-go-apps-update` (p=115, fixed UUID regex) — **Wait**: bulk-delete path is `/admin/applications/bulk-delete` which matches `[^/]+` on `bulk-delete`. ✓
- `PUT /{id}/runtime` — `/admin/applications/{uuid}/runtime` — does NOT match the current `them-go-apps-update` rule which anchors at `^/api/v1/admin/applications/[^/]+$` (no sub-paths). This path has a second segment (`/runtime`). It will fall through to Python (p=100). **A new Traefik rule is required for Wave 8.**
- `GET /{id}/export` — matches `PathPrefix(/api/v1/admin/applications) && Method(GET)` → `them-go-admin-reads` (p=110). ✓

**New Traefik rule needed for Wave 8:**
```
# Wave 8 — application sub-routes (runtime, export, etc.)
them-go-apps-subroutes:
  rule: PathRegexp(`^/api/v1/admin/applications/[^/]+/.+$`) && (Method(`PUT`) || Method(`POST`) || Method(`PATCH`) || Method(`DELETE`))
  priority: 115
  service: them-go-svc / them-go-bridge-svc
```
This covers `PUT /{id}/runtime`, `PUT /{id}/restore`, `POST /{id}/orchestrators/{ao_id}/test-*`,
and any future sub-route writes.

---

## Summary

Wave 8 migrates three of the five Application Special Operation endpoints:

**Migrate in Wave 8:**
1. `PUT /{id}/runtime` — low complexity, active UI caller, Go `epconfig` already consumes the field
2. `POST /bulk-delete` — low-medium complexity, active UI caller, simple SQL + cache flush
3. `GET /{id}/export` — low-medium complexity, no UI caller but foundational for Wave 9 round-trip

**Defer to Wave 9:**
4. `POST /import` — requires `compile_graph` port (~340 lines)
5. `PUT /{id}/restore` — same dependency as import; port together with import

**Infrastructure required before Wave 8 starts:**
- `FlushApplicationOrchCaches` in Go `CacheInvalidator`
- New Traefik rule for `/{id}/sub-path` write routes
- DAL additions: `BulkDeleteApplications`, `UpdateRuntimeConfig`, `ListAppOrchestrators`, `GetApplicationForExport`
