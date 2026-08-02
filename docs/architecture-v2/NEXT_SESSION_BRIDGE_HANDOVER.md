# Next Session Bridge Handover
# Updated: 2026-08-02

---

## Current State

**Branch:** `main`
**HEAD:** see `git log --oneline -1` — latest commit after this session's doc push
**Push status:** Up to date with `origin/main`

---

## This Session's Work

### Commits this session

| SHA | Message |
|---|---|
| `f92081f` | docs: route ownership inventory — 73-route map with inconsistency report |
| `28b781d` | fix(traefik): correct UUID regex and runs path scope in Go routing rules |
| `c099619` | docs: Wave 8 application special ops review + handover update |
| *(this session's doc commit)* | docs: Application Model Architecture Review + handover update |

### What was done

1. **Route ownership inventory** (`f92081f`) — Created authoritative 73-route map showing all externally exposed routes, their Python/Go impl status, live Traefik owner, and 5 major inconsistencies.

2. **Routing fixes** (`28b781d`):
   - Fixed `[0-9]+` → `[^/]+` in three Traefik rules: `them-go-agents-update`, `them-go-apps-update`, `them-go-eps-writes` (UUID IDs never matched `[0-9]+`).
   - Narrowed `them-go-admin-reads` runs rule from `PathPrefix(/api/v1/runs)` to `Path(/api/v1/runs) || PathRegexp(^/api/v1/runs/[^/]+$$)` — stops routing Python-only sub-paths to Go.
   - Applied same runs rule fix to both `docker-compose.traefik.yml` and `docker-compose.yml`.
   - Added `scripts/tests/test_routing_fix_contracts.py`.

3. **Wave 8 special ops review** (`c099619`) — Analyzed all 5 Application Special Operation endpoints. Decisions:
   - `PUT /{id}/runtime` → migrate Wave 8
   - `POST /bulk-delete` → migrate Wave 8
   - `GET /{id}/export` → migrate Wave 8
   - `POST /import` → defer Wave 9 (requires `compile_graph`)
   - `PUT /{id}/restore` → defer Wave 9 (same dependency as import)

4. **Application Model Architecture Review** — Answered 12 architecture questions governing how the Application model is represented, persisted, exported, imported, compiled, and versioned before Wave 8 implementation begins. Key decisions:
   - Relational rows are source of truth; graph is derived (compile-on-save preserved)
   - No versioning in Wave 8 or Wave 9
   - Application Definition v1: `{schema_version:1, name, presentation, graph, canvas}`
   - ADK compatibility: no schema change; agent UUID FK is sufficient
   - Wave 8 scope confirmed: runtime + bulk-delete + export
   - Wave 9 scope: compile_graph port + import + restore + Python tenant_id fix
   - `session_timeout_minutes`: accept + persist in Go, do not enforce
   - Deprecated `orchestrator_id` FK: treat as dead, do not populate
   Full document: `docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md`

---

## Routing Validation Result

**Test:** `scripts/tests/test_routing_fix_contracts.py` run from inside `them-bridge` container via:
```bash
docker cp scripts/tests/test_routing_fix_contracts.py them-bridge:/tmp/test_routing_fix_contracts.py
docker exec them-bridge bash -c "
  TRAEFIK_BASE=http://them-traefik:8088 \
  PYTHON_BASE=http://localhost:8001 \
  GO_BASE=http://them-go-bridge:8002 \
  AUTH_SERVICE=http://them-auth-service:8701 \
  python3 /tmp/test_routing_fix_contracts.py
"
```

**Result: 11 passed, 0 failed, 2 skipped**

Skips are legitimate: the Python bridge in this environment has no applications, so
the application/EP write routing tests skip (no test data to route against). Agent write
test passed using an existing agent UUID.

**Critical environment note:** The `them-go-bridge` container running on port 8002 does
NOT have Traefik routing labels in its container labels. The Go routing labels from
`docker-compose.yml` (profile `go`) were never applied to the running container.
**All traffic currently goes to Python through Traefik.** The routing fix labels are
correctly written in `docker-compose.yml` and `docker-compose.traefik.yml` but require
a `docker compose up` restart with the `--profile go` flag to take effect.

To activate Go routing, run:
```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile go up -d them-go-bridge
```

---

## Route Ownership Summary (post-fix)

| Category | Count |
|---|---|
| Total externally exposed routes | 73 |
| Go-owned (live via Traefik, when go profile is active) | 27 |
| Python-owned (live via Traefik) | 46 |
| Legacy / deprecation candidates | 4 |

Full inventory: `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`

---

## Wave 8 Approved Scope

**Three endpoints to migrate**, in this order:

1. `PUT /api/v1/admin/applications/{id}/runtime` — single UPDATE + cache flush
2. `POST /api/v1/admin/applications/bulk-delete` — bulk DELETE + cache flush (same pattern)
3. `GET /api/v1/admin/applications/{id}/export` — port `export_graph()` (~95 lines, pure function)

Review document: `docs/architecture-v2/WAVE8_APPLICATION_SPECIAL_OPS_REVIEW.md`

**Deferred to Wave 9:** `POST /import` and `PUT /{id}/restore` (require `compile_graph` port).

---

## Infrastructure Required Before Wave 8 Starts

### 1. New Traefik rule for application sub-route writes

`PUT /{id}/runtime` and future sub-path writes are NOT covered by the existing
`them-go-apps-update` rule (which anchors at `^/api/v1/admin/applications/[^/]+$` — single segment only).
Add to both `docker-compose.traefik.yml` and `docker-compose.yml`:

```yaml
# Wave 8 — app sub-routes: runtime, restore, export writes
- "traefik.http.routers.them-go-apps-subroutes.rule=PathRegexp(`^/api/v1/admin/applications/[^/]+/.+$`) && (Method(`PUT`) || Method(`POST`) || Method(`PATCH`) || Method(`DELETE`))"
- "traefik.http.routers.them-go-apps-subroutes.entrypoints=web"
- "traefik.http.routers.them-go-apps-subroutes.priority=115"
- "traefik.http.routers.them-go-apps-subroutes.service=them-go-svc"
```

Note: `GET /{id}/export` is already covered by `them-go-admin-reads` (`PathPrefix(/api/v1/admin/applications) && Method(GET)`, p=110). Only writes need the new rule.

### 2. Go `CacheInvalidator` extension

Add `FlushApplicationOrchCaches(ctx, appID string, orchNames []string) error` to the
`CacheInvalidator` interface in `go/internal/admin/`. This method must:
- `DEL them:app:{app_id}:orch:{name}` for each name
- `DEL them:orch:loc:{name}` for each name
- `DEL them:agents:registry`
- `PUBLISH them:ep:config:changed {app_id}`

The pub/sub publish already exists for the EP config channel in `epconfig`. The new
key pattern `them:app:{app_id}:orch:{name}` is Python's orchestrator config cache.
Verify the exact key format from `admin_applications.py:_flush_orch_caches`.

### 3. DAL additions for Wave 8

In `go/internal/admin/dal/applications.go`:

```
ListAppOrchestrators(ctx, tenantID, appID string) ([]struct{Name string}, error)
  -- SELECT name FROM them.app_orchestrators WHERE application_id = $1

BulkDeleteApplications(ctx, tenantID string, ids []string) (int64, error)
  -- DELETE FROM them.applications WHERE id = ANY($1) AND tenant_id = $2 RETURNING id

UpdateRuntimeConfig(ctx, tenantID, appID string, configJSON []byte) error
  -- UPDATE them.applications SET runtime_config = $1 WHERE id = $2 AND tenant_id = $3

GetApplicationWithChildren(ctx, tenantID, appID string) (*ApplicationExportRow, error)
  -- SELECT with JOINs for app_orchestrators, entry_points, middleware_wirings
  -- (for export endpoint)
```

---

## Known Open Issues

| Issue | Impact | Status |
|---|---|---|
| Go routing labels not active in live environment | All traffic goes to Python | Labels are in compose files; requires `docker compose --profile go up -d` restart |
| `them-go-bridge` service has no Traefik labels at runtime | Go bridge unreachable via Traefik | Same as above |
| `session_timeout_minutes` in AppRuntimeConfig never consumed | Dead field | Document as no-op; include in Go schema for completeness |
| `POST /import` and `PUT /{id}/restore` still in Python | No UI impact (no frontend callers) | Deferred to Wave 9 |
| `middleware_wirings` table has no Go DAL at all | Blocks import/restore migration | Not needed for Wave 8; required for Wave 9 |
| Contract test skips when no applications exist in environment | Reduced coverage | Use Hetzner or a seeded test environment for full coverage |

---

## Tests Run This Session

| Test | Result | Notes |
|---|---|---|
| `test_routing_fix_contracts.py` (from inside container) | 11 passed, 0 failed, 2 skipped | Skips: no applications in this environment |
| `python3.12 scripts/tests/run_tests.py` (full Python suite) | Not run this session | No Python code changed |
| `go test ./...` | Not run this session | No Go code changed |

---

## Architecture Decisions Made This Session

1. **Wave 8 scope = runtime + bulk-delete + export** (not all 5 special ops). Rationale: import and restore share `compile_graph` dependency which is a 340-line port including graph traversal and crypto — separate wave.

2. **`export_graph` is Wave 8, not Wave 9.** It is a prerequisite for the Wave 9 round-trip test (export via Go, import via Python). Migrating it in Wave 8 proves the graph serialization format before the write paths are ported.

3. **New Traefik rule `them-go-apps-subroutes` required.** The existing `them-go-apps-update` rule anchors at `[^/]+$` (no sub-paths). Any write to `/{id}/runtime`, `/{id}/restore`, `/{id}/orchestrators/{ao_id}/...` requires a new rule.

4. **Test strategy: use existing data, not newly-created resources.** Agent/app write contract tests use the first existing agent/application from the Python bridge rather than creating throwaway resources, because the Python `agents` table has a NOT NULL `tenant_id` that Python's admin API doesn't expose.

---

## Temporary Compatibility Code

- Python `ws_orchestrator.py` still has `app_runtime=None` for runtime gate (line 164) — this is intentional, not a bug.
- `docker-compose.yml` and `docker-compose.traefik.yml` have Wave 2–7 Go labels — these only take effect when the go profile container is started.

---

## Files Most Relevant to Wave 8

| File | Relevance |
|---|---|
| `docs/architecture-v2/WAVE8_APPLICATION_SPECIAL_OPS_REVIEW.md` | Wave 8 design decisions |
| `app/routers/admin_applications.py:617-791` | Python handlers for all 5 endpoints |
| `app/services/app_compiler.py` | `export_graph`, `compile_graph`, `validate_graph` |
| `go/internal/admin/applications.go` | Current Go handler (missing the 5 special ops) |
| `go/internal/admin/dal/applications.go` | Go DAL (needs new methods) |
| `go/internal/admin/router.go` | Go admin router wiring |
| `go/internal/admin/service/` | Go service layer (pattern reference from llm_providers.go) |
| `go/internal/crypto/fernet.go` | Fernet encrypt/decrypt (available for Wave 9) |
| `go/internal/epconfig/epconfig.go` | runtime_config consumer (Go side) |
| `docker-compose.traefik.yml` | Wave 2–7 Traefik routing labels (needs Wave 8 addition) |
| `docker-compose.yml` | Base Traefik labels (needs same Wave 8 addition) |

---

## Exact Next Task: Wave 8 — Application Special Operations Migration

### First prompt for the next session

```
Continue from main (latest commit). Read these docs first:
  docs/architecture-v2/APPLICATION_MODEL_ARCHITECTURE_REVIEW.md  ← architecture decisions
  docs/architecture-v2/WAVE8_APPLICATION_SPECIAL_OPS_REVIEW.md   ← per-endpoint analysis
  docs/architecture-v2/NEXT_SESSION_BRIDGE_HANDOVER.md
  go/CLAUDE.md

Implement Wave 8 — Application Special Operations — in this order:

Step 1: Add Traefik rule `them-go-apps-subroutes` for /{id}/sub-path write routes
(exact rule is in the review doc, "Infrastructure Required" section).
Apply to both docker-compose.traefik.yml and docker-compose.yml.

Step 2: Add FlushApplicationOrchCaches to Go CacheInvalidator interface and Redis
implementation. Key patterns from admin_applications.py:_flush_orch_caches:
  DEL them:app:{app_id}:orch:{name} (per orchestrator name)
  DEL them:orch:loc:{name} (per orchestrator name)
  DEL them:agents:registry
  PUBLISH them:ep:config:changed {app_id}

Step 3: Add DAL methods in go/internal/admin/dal/applications.go:
  ListAppOrchestrators, BulkDeleteApplications, UpdateRuntimeConfig,
  GetApplicationWithChildren (for export)

Step 4: Implement handlers in go/internal/admin/applications.go:
  PUT /{id}/runtime
  POST /bulk-delete
  GET /{id}/export

Step 5: Wire new routes in go/internal/admin/router.go.

Step 6: Write Go tests for all three handlers. Update go/TEST_INDEX.md.

Step 7: Run go test ./... (must pass, zero new failures).

Step 8: Deploy and smoke-test through Traefik:
  docker compose -f docker-compose.yml -f docker-compose.local.yml --profile go up -d them-go-bridge

Step 9: Run scripts/tests/test_routing_fix_contracts.py from inside them-bridge.
        Must see the application write tests PASS (not skip).

Step 10: Commit and push. Update REMAINING_ROUTE_OWNERSHIP_INVENTORY.md with new Go ownership.

Do NOT implement import or restore. Do NOT port compile_graph. Stop after step 10.
```

### Startup commands

```bash
# Session startup
cd /home/avi/them
git log --oneline -5
docker compose -f docker-compose.yml -f docker-compose.local.yml ps

# Bring up Go bridge with routing labels (if not running)
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile go up -d them-go-bridge
docker compose --profile go logs -f them-go-bridge

# Run Go tests before starting
cd go && go test ./...
```

---

## Hard Constraints for Next Session

1. **Do not port `compile_graph` in Wave 8** — it is Wave 9 scope.
2. **Do not implement `/import` or `/restore` in Wave 8.**
3. **Do not use `git add .` or `git add -A`** — stage only Wave 8 files.
4. **Run `go test ./...` before every commit** — zero new failures required.
5. **Update `go/TEST_INDEX.md`** in the same commit as any new Go test.
6. **Update `docs/architecture-v2/REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`** after cutover to reflect the new Go ownership.
7. **Secrets**: Never commit `.env` or `secrets.local`. DB name is `them`, never `odin`.
