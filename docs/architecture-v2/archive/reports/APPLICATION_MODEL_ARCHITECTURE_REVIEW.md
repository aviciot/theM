# Application Model Architecture Review
# Scope: How THEM should represent, persist, version, edit, export, import, restore, and compile Applications
# Date: 2026-08-02

---

## Purpose

This document answers twelve architecture questions about the Application model before any
special-operation endpoints (export, import, restore, bulk-delete, runtime) are migrated
from Python to Go. Its goal is to produce agreed decisions that constrain implementation
choices in Wave 8 and Wave 9 — not to design ADK agents, not to redesign the Canvas UI,
and not to port any Python behavior automatically.

---

## 1. Current-State Diagram

```
┌───────────────────────────────────────────────────────────────────────────┐
│  EDIT PATH (admin UI → API → DB)                                          │
│                                                                           │
│  React Flow Canvas                                                        │
│   {nodes: [...], edges: [...]}                                            │
│         │                                                                 │
│         ▼  POST/PATCH /api/v1/admin/applications[/{id}]                  │
│  Python  admin_applications.py                                            │
│   1. validate_graph()    — pure structural check                          │
│   2. compile_graph()     — graph → relational rows (DB I/O)              │
│         │  returns: touched AppOrchestrator.names[]                       │
│   3. _flush_orch_caches()— Redis DEL + PUBLISH                           │
│         │                                                                 │
│         ▼  SQLAlchemy AsyncSession                                        │
│  PostgreSQL them schema                                                   │
│   ┌─────────────────────────────────────────────────────────┐            │
│   │  applications                                           │            │
│   │    id, tenant_id, name, presentation (JSONB)            │            │
│   │    canvas (JSONB)   ← React Flow viewport + positions   │            │
│   │    runtime_config (JSONB) ← max_sessions, rpm, etc.     │            │
│   │    enabled, created_at, updated_at                       │            │
│   │                                                          │            │
│   │  app_orchestrators  (CASCADE from applications.id)       │            │
│   │    id, application_id, tenant_id, orchestrator_id (FK)  │            │
│   │    name (tenant-scoped unique), node_id (canvas stable) │            │
│   │    kind, delegatable, allowed_agent_ids (UUID[])         │            │
│   │    system_prompt, llm_*, voice_*, tts_*, memory_*        │            │
│   │    edges (JSONB), history_window, budget_tokens          │            │
│   │    llm_api_key_encrypted, transcription_api_key_enc,     │            │
│   │    tts_api_key_encrypted  ← Fernet(sha256(SECRET_KEY))  │            │
│   │                                                          │            │
│   │  entry_points        (CASCADE from applications.id)      │            │
│   │    id, application_id, app_orchestrator_id (FK/CASCADE) │            │
│   │    slug (tenant-scoped unique via 026 migration),        │            │
│   │    entry_point_type, access_policy (JSONB),              │            │
│   │    conversation_token_limit, max_concurrent_sessions,    │            │
│   │    queue_timeout_seconds, queue_message                  │            │
│   │                                                          │            │
│   │  middleware_wirings  (CASCADE from applications.id)      │            │
│   │    id, application_id, agent_id, def_id                  │            │
│   │    position, config_override (JSONB), node_id            │            │
│   └─────────────────────────────────────────────────────────┘            │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────────────────────────────────────────┐
│  READ PATH (Go, connection-time gate)                                     │
│                                                                           │
│  Client → Traefik → them-go-bridge                                       │
│                   ├── internal/epconfig  (30s in-process cache)           │
│                   │     SELECT id, entry_point_type, access_policy,       │
│                   │            app_orchestrator_id, enabled               │
│                   │     FROM entry_points WHERE slug=$1                   │
│                   │     + JOIN applications for runtime_config            │
│                   │     Invalidated: them:ep:config:changed pub/sub       │
│                   │                                                       │
│                   └── internal/admin/applications.go                     │
│                         GET/POST/PATCH/DELETE applications + entry-points │
│                         (Go DAL reads/writes applications + entry_points) │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────────────────────────────────────────┐
│  EXPORT/IMPORT PATH (Python only today)                                   │
│                                                                           │
│  export_graph()  relational → nodes+edges JSON                           │
│    app_orchestrators + entry_points + middleware_wirings → graph payload  │
│    Node types: orchestrator, entryPoint, agent, middleware                │
│    Encrypted keys are NOT included in export (omitted by export_graph)   │
│                                                                           │
│  import  → parse graph → Application() insert → compile_graph()          │
│  restore → load app → compile_graph() on existing rows (update mode)     │
│                                                                           │
└───────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Architecture Questions and Decisions

### Q1: What is the canonical representation of an Application?

**Current state.** The DB is the source of truth at rest. The graph (nodes+edges) is the
edit surface — sent by the UI on save, immediately compiled into relational rows, then
discarded. There is no stored graph version. Canvas layout is stored separately in
`applications.canvas` (JSONB) and round-trips through `export_graph` intact.

**Decision: Keep the DB as the single source of truth. The graph is a transient edit
payload, not a persisted artifact.**

Rationale: The relational model is consumed by Temporal workers, the admission gate
(`epconfig`), the run recorder, and rate-limiting code that reads columns directly. Storing
a graph blob alongside the relational rows creates a second source of truth and a sync
problem. The current "compile on save" pattern is correct. `export_graph` reconstructs the
graph on demand from the relational rows — this is also correct.

**What this means for Go migration:** Go must implement the same compile-on-save approach.
The compile logic (`compile_graph`) is pure graph→relational transformation — it can be
ported to Go as a service function or kept in Python until Wave 9. The export direction
(`export_graph`) is a pure relational→graph transformation with no DB writes — it is safe
and simple to port in Wave 8.

---

### Q2: Should the Application model be versioned?

**Current state.** No versioning. A PATCH overwrites in-place. There is no history, no
rollback. The only recovery mechanism is `PUT /{id}/restore` which re-runs compile_graph
from a previously exported snapshot.

**Decision: Do not add versioning in Wave 8 or Wave 9.**

Rationale: Versioning adds significant DAL complexity (version table, pointer columns,
activation semantics). The current use case is a single operator editing applications via
admin UI — not a multi-author workflow. The export/restore pair already provides the
recovery path a solo operator needs. Versioning is a future consideration if the platform
evolves to support CI/CD-style application deployment.

**What this means for Go migration:** No new version table, no version FK, no
`version: number` field in export payloads. Export format stays flat: `{name, presentation,
graph: {nodes, edges, canvas}, canvas}`.

---

### Q3: What is the Application Definition v1 export format?

**Current format** (from `export_graph` + `ApplicationExport`):

```json
{
  "name": "My Application",
  "presentation": { "color": "#abc", "description": "..." },
  "graph": {
    "nodes": [
      { "id": "orch_abc", "type": "orchestrator", "data": { "displayName": "...", "systemPrompt": "...", "llmProvider": "...", "llmModel": "...", "maxIterations": 10, "maxParallelTools": 3, "historyWindow": 20, "delegatable": false, "kind": "standard", "budgetTokens": null, "name": "stable_name", "transcriptionProvider": null, "transcriptionModel": null, "ttsProvider": null, "ttsVoice": null } },
      { "id": "agent_<uuid>", "type": "agent", "data": { "agentId": "<uuid>" } },
      { "id": "ep_<slug>", "type": "entryPoint", "data": { "epId": "<uuid>", "slug": "...", "epType": "websocket", "accessMode": "token", "convTokenLimit": null, "maxConcurrentSessions": null, "queueTimeout": null, "queueMessage": null } },
      { "id": "mw_abc", "type": "middleware", "data": { "defId": "<uuid>", "configOverride": {}, "enabled": true } }
    ],
    "edges": [
      { "id": "e_ep_...", "source": "ep_<slug>", "target": "orch_abc" },
      { "id": "e_orch_agent_...", "source": "orch_abc", "target": "agent_<uuid>" }
    ],
    "canvas": { "layout": {...}, "viewport": {...} }
  },
  "canvas": { "layout": {...}, "viewport": {...} }
}
```

**Notable properties of this format:**
- `graph.canvas` and the top-level `canvas` field are the same value (duplicated for convenience).
- Encrypted API keys are **not** exported. The fields `llmApiKey`, `transcriptionApiKey`, `ttsApiKey` are absent from export — they must be re-entered after import.
- Agent references are by UUID (`agentId`). Import will fail if the target environment doesn't have an agent with that UUID. This is by design — agents are catalog-level resources, not bundled with applications.
- Orchestrator `name` is included in export. It is the immutable stable identifier used by Temporal workers. On import, compile_graph regenerates a unique name if a conflict exists.

**Decision: Adopt this format as Application Definition v1, with one clarification: add a
`schema_version: 1` field at the top level.**

```json
{
  "schema_version": 1,
  "name": "...",
  "presentation": {...},
  "graph": { "nodes": [...], "edges": [...], "canvas": {...} },
  "canvas": {...}
}
```

The `schema_version` field allows future evolution without breaking imports. Python's export
endpoint should add it. Go's export endpoint must emit it. Import in both Python and Go
should accept the field but not require it (omitting it implies version 1).

---

### Q4: What is the source-of-truth decision for the graph vs. relational representation?

**Decision: Relational rows are the source of truth. The graph is derived.**

This is already the design. The key invariant is:

- `compile_graph` is called on every save (create, update, restore, import).
- `export_graph` reconstructs the graph from relational rows — it does not read stored graph blobs.
- The Canvas layout (`applications.canvas`) is stored separately and is NOT compiled into relational rows; it travels as a side-channel in save/export payloads.

**Consequences:**
1. The Go export handler must JOIN `app_orchestrators + entry_points + middleware_wirings`
   to reconstruct the graph. It cannot read a stored `graph_blob` column (none exists).
2. Any Wave 8 or Wave 9 Go handler that modifies app structure must invalidate the epconfig
   cache via Redis pub/sub (`them:ep:config:changed`).
3. Middleware wirings are partially represented: `export_graph` emits middleware nodes but
   does not reconstruct edges _from_ middleware _to_ agents. The agent edges are only on
   orchestrator nodes. This is a known limitation of the current export format — noted
   under risks (Section 4).

---

### Q5: Should the Go compile_graph port change the compilation model?

**Decision: No. Port the compilation model exactly, including the "compile on save"
pattern. Do not redesign.**

The Python `compile_graph` (265 lines) does the following:
1. Slug conflict check against other apps (cross-DB)
2. Agent existence check (cross-DB)
3. Upsert `app_orchestrators` keyed by `node_id` (canvas-stable identity)
4. Delete removed orchestrators
5. Upsert `entry_points` keyed by `slug`
6. Delete removed entry_points
7. Replace `middleware_wirings`

The key design decision to preserve: **`app_orchestrators` are keyed by `node_id` (canvas
identity), not by name.** Names are generated once on creation and are immutable. This
allows the canvas to rename an orchestrator display name without changing the Temporal
worker's reference (`app_orchestrators.name`).

The `delegatable` and `allowed_agent_ids` fields are **derived from graph edges**, not
from user input. This invariant must be preserved in Go.

**LLM API keys are Fernet-encrypted during compile.** The Go crypto package
(`go/internal/crypto/fernet.go`) is already byte-for-byte compatible. Wave 9 Go import
handler must call it.

**What can be simplified in Wave 9:** The `_apply_orch_data` helper in Python handles
camelCase/snake_case dual keys (e.g. `llmProvider` / `llm_provider`). In Go, the struct
binding via JSON tags handles this at the handler boundary — the DAL upsert can use a
single canonical form.

---

### Q6: What is the relationship between Application and the global Orchestrators catalog?

**Current state.** `app_orchestrators.orchestrator_id` is a nullable FK to
`them.orchestrators`. In practice it is never set. The Python `compile_graph` does not
populate it. The relationship exists as a legacy scaffold from when applications were
expected to reference global orchestrators rather than embed their own.

**Decision: Treat `orchestrator_id` as dead. Do not populate it in Go. Do not add Go
DAL logic to join on it.**

The `them.orchestrators` table remains for the legacy orchestration path (non-Application
WS connections). Applications use `app_orchestrators` exclusively. The FK exists in the
schema but is not a live dependency for any Wave 8 or Wave 9 operation.

---

### Q7: Should the Application model accommodate future ADK-based agent definitions?

**Constraint from user:** We are NOT building ADK agents now. The Application model must
not block a future where Applications reference ADK-based agent definitions.

**Current model binding:** `app_orchestrators.allowed_agent_ids` is a `UUID[]` pointing to
`them.agents`. `MiddlewareWiring.agent_id` also points to `them.agents`. Both use the
catalog-level agent UUID.

**Decision: No schema changes for ADK compatibility now. The current UUID reference is
sufficient as a foreign key binding point.**

When ADK agents arrive, they will either:
(a) Be registered as `them.agents` rows with a new `adapter_type` (e.g. `adk`), inheriting
    the existing UUID reference — no schema change required; or
(b) Live in a separate `them.adk_agent_definitions` table, requiring a new FK column on
    `app_orchestrators` (e.g. `adk_agent_def_id UUID`).

Option (a) is strongly preferred and is consistent with the adapter pattern already in
`app/adapters/`. The Application model does not need to change for ADK — the agent catalog
does. This decision keeps the Application model stable.

---

### Q8: Does the Go Application DAL need middleware_wirings support in Wave 8?

**Current Go DAL state:** `go/internal/admin/dal/applications.go` has no
`middleware_wirings` queries. The Go admin handler only reads/writes
`applications` and `entry_points`.

**Decision: Wave 8 does not require middleware_wirings in the DAL.**

The three Wave 8 endpoints are:
- `PUT /{id}/runtime` — touches only `applications.runtime_config`
- `POST /bulk-delete` — hard-deletes application rows (CASCADE handles wirings)
- `GET /{id}/export` — reads `app_orchestrators`, `entry_points`, `middleware_wirings`

Export does read middleware_wirings. The Go export handler needs a read-only JOIN query
for `middleware_wirings`. It does NOT need upsert/delete methods for wirings.

Wave 9 (import + restore) will require full `middleware_wirings` upsert/delete support.

---

### Q9: What changes are required to the Python Application model before Wave 8?

**Decision: No changes to Python models or Python routes before Wave 8.**

Two divergences exist between Python SQLAlchemy models and the live DB schema:

1. `Application` and `AppOrchestrator` have no `tenant_id` mapped in Python
   (`app/models.py`). The column exists in DB (added via `026_tenant_foundation.sql`) but
   Python inserts rows with `tenant_id = NULL` using the older INSERT path.
   **This is a known Python gap. Do not fix it now. Go handles tenant_id correctly via
   its DAL. Python's legacy path is acceptable until Python is removed.**

2. `applications` table has a deprecated `orchestrator_id NOT NULL` column
   (`015_phase12_drop_deprecated.sql` exists but was never applied to live DB). This
   column is dead. **Do not apply the drop migration now. It is a live-DB-only risk;
   coordinate separately.**

---

### Q10: How should the runtime_config model be treated in Go?

**Current Python schema (`AppRuntimeConfig`):**
```python
max_concurrent_sessions: Optional[int]
rate_limit_rpm: Optional[int]
blocked_tokens: List[str]      # SHA-256 hex hashes
blocked_user_ids: List[int]
session_timeout_minutes: Optional[int]  # defined but never consumed
```

**Current Go consumer (`go/internal/epconfig/epconfig.go`):**
```go
type appRuntimeConfig struct {
    MaxConcurrentSessions int    `json:"max_concurrent_sessions"`
    RateLimitRPM          int    `json:"rate_limit_rpm"`
    BlockedTokens         []string `json:"blocked_tokens"`
    BlockedUserIDs        []int  `json:"blocked_user_ids"`
    // session_timeout_minutes: parsed but unused
}
```

**Decision: Go `PUT /{id}/runtime` handler uses the same five-field schema. Include
`session_timeout_minutes` as a passthrough field (accept, persist, do not enforce). Do
not remove it — the field is defined in Python API and may be in stored JSONB already.**

The Go `AppRuntimeConfig` struct for the handler should mirror the Python schema exactly:

```go
type AppRuntimeConfig struct {
    MaxConcurrentSessions *int     `json:"max_concurrent_sessions"`
    RateLimitRPM          *int     `json:"rate_limit_rpm"`
    BlockedTokens         []string `json:"blocked_tokens"`
    BlockedUserIDs        []int    `json:"blocked_user_ids"`
    SessionTimeoutMinutes *int     `json:"session_timeout_minutes"`
}
```

Use pointer types for nullable integers so that `null` and `0` are distinguishable in JSON.
After UPDATE, publish `them:ep:config:changed {app_id}` to trigger epconfig cache
invalidation.

---

### Q11: What is the bulk-delete contract?

**Current Python implementation (`bulk_delete_applications` lines 617-644):**
1. Accept `{"ids": ["<uuid>", ...]}` body.
2. Pre-load `app_orchestrators.name` for all apps in the list (for cache flush).
3. Hard-delete: `DELETE FROM them.applications WHERE id IN (...)`.
4. DB CASCADE deletes `app_orchestrators`, `entry_points`, `middleware_wirings`.
5. Flush orch caches for each deleted orch name.
6. Publish `them:ep:config:changed` for each app UUID.

**Note:** `runs` reference `orchestrators(id)` NOT `applications` directly. Deleting an
application does NOT delete its runs. This is intentional — runs are immutable audit
records.

**Decision: Go bulk-delete follows the Python contract exactly. Hard-delete (no soft
delete). Must flush caches after delete, not before.**

The Go handler must:
- Pre-load orch names before DELETE (to know what to flush).
- Execute DELETE with the app UUID list and tenant_id filter.
- Loop flush for each deleted orch (DEL orch config keys + DEL agent registry).
- Publish ep:config:changed for each deleted app_id.

---

### Q12: What is the export format for middleware wirings (and its known limitation)?

**Current behavior:** `export_graph` emits a middleware node for each `MiddlewareWiring`
but does NOT emit edges from middleware to agents. Only orchestrator→agent edges are
emitted. Middleware→agent edges are not recoverable from the relational model alone
(the wiring encodes `agent_id` directly but not the chain topology).

**Decision: Accept this limitation in Wave 8 and Wave 9. Do not redesign the
middleware-edge encoding now.**

The practical impact is low: middleware is currently unused in production. The
round-trip fidelity for export/import of apps without middleware is 100%. Apps with
middleware will lose the visual edge-to-agent connections after import/restore, but the
relational `middleware_wirings` rows will be correctly reconstructed by `compile_graph`
since the wiring table stores `agent_id` directly.

**Document this in the export response as a known limitation of v1 format.**

---

## 3. Decision Table: Wave 8 vs Wave 9

| Endpoint | Decision | Blocker removed | Wave |
|---|---|---|---|
| `PUT /{id}/runtime` | Migrate to Go | None — pure JSONB UPDATE + cache flush | 8 |
| `POST /bulk-delete` | Migrate to Go | None — DELETE + cache flush | 8 |
| `GET /{id}/export` | Migrate to Go | `export_graph` is pure relational→JSON | 8 |
| `POST /import` | Defer | `compile_graph` port required | 9 |
| `PUT /{id}/restore` | Defer | `compile_graph` port required | 9 |

---

## 4. Infrastructure Required for Wave 8

### 4a. New Traefik rule for sub-path write routes

Existing `them-go-apps-update` anchors at `^/api/v1/admin/applications/[^/]+$` (no
sub-paths). `PUT /{id}/runtime` is at `/{id}/runtime`. Add to both
`docker-compose.traefik.yml` and `docker-compose.yml`:

```yaml
- "traefik.http.routers.them-go-apps-subroutes.rule=PathRegexp(`^/api/v1/admin/applications/[^/]+/.+$`) && (Method(`PUT`) || Method(`POST`) || Method(`PATCH`) || Method(`DELETE`))"
- "traefik.http.routers.them-go-apps-subroutes.entrypoints=web"
- "traefik.http.routers.them-go-apps-subroutes.priority=115"
- "traefik.http.routers.them-go-apps-subroutes.service=them-go-svc"
```

Note: `GET /{id}/export` is already covered by `them-go-admin-reads` (`PathPrefix +
Method(GET)`, priority 110). Only write sub-paths need the new rule.

### 4b. FlushApplicationOrchCaches helper

Python's `_flush_orch_caches()` must have a Go equivalent. The exact Redis keys:
```
DEL  them:app:{app_id}:orch:{name}   (per orch name — per-app orch config cache)
DEL  them:orch:loc:{name}            (per orch name — location cache for Temporal)
DEL  them:agents:registry            (global agent registry)
PUBLISH them:ep:config:changed {app_id}  (triggers epconfig.Invalidate)
```

Add `FlushApplicationOrchCaches(ctx context.Context, appID string, orchNames []string) error`
to `go/internal/admin/cache/` (or extend the existing cache invalidation helper).

### 4c. DAL additions

In `go/internal/admin/dal/applications.go`:

```
// For export
GetApplicationForExport(ctx, tenantID, appID string) → (Application, []AppOrchestrator, []EntryPoint, []MiddlewareWiring, error)

// For runtime update  
UpdateRuntimeConfig(ctx, tenantID, appID string, configJSON []byte) error

// For bulk delete (pre-fetch + delete)
ListAppOrchestratorNames(ctx, appID string) ([]string, error)
BulkDeleteApplications(ctx, tenantID string, ids []string) (int64, error)
```

The export query must JOIN across four tables in a single transaction to avoid
partial reads during concurrent saves:

```sql
SELECT ao.id, ao.node_id, ao.name, ao.display_name, ao.system_prompt,
       ao.llm_provider, ao.llm_model, ao.max_iterations, ao.max_parallel_tools,
       ao.history_window, ao.delegatable, ao.kind, ao.budget_tokens,
       ao.allowed_agent_ids, ao.transcription_provider, ao.transcription_model,
       ao.tts_provider, ao.tts_voice, ao.enabled
FROM them.app_orchestrators ao
WHERE ao.application_id = $1::uuid AND ao.tenant_id = $2::uuid;

SELECT ep.id, ep.slug, ep.entry_point_type, ep.access_policy,
       ep.conversation_token_limit, ep.max_concurrent_sessions,
       ep.queue_timeout_seconds, ep.queue_message, ep.app_orchestrator_id
FROM them.entry_points ep
WHERE ep.application_id = $1::uuid;

SELECT mw.id, mw.node_id, mw.def_id, mw.agent_id, mw.position,
       mw.config_override, mw.enabled
FROM them.middleware_wirings mw
WHERE mw.application_id = $1::uuid
ORDER BY mw.position;
```

---

## 5. Infrastructure Required for Wave 9

### 5a. Go compile_graph port

`compile_graph` (~265 lines, async) must be ported to Go as a synchronous service
function:

```go
func CompileGraph(ctx context.Context, db *pgxpool.Pool, appID string,
    graph AppGraph, existingOrchNames []string) (touchedOrchNames []string, err error)
```

The port must preserve:
- Slug conflict check (other apps within tenant)
- Agent existence check
- `app_orchestrators` upsert keyed by `node_id`
- `delegatable` and `allowed_agent_ids` derived from graph edges
- Fernet encryption of LLM API keys via `go/internal/crypto/fernet.go`
- `middleware_wirings` full replacement

### 5b. Go import handler

`POST /import` creates a new Application and calls compile_graph. No ID in path.
Pre-validation: `validate_graph` (pure structural) before any DB writes.

### 5c. Go restore handler

`PUT /{id}/restore` loads existing Application and calls compile_graph in update mode
(existing rows passed in, allowing upsert-vs-insert discrimination). Wrapped in a
single transaction — the caller's existing rows are replaced atomically.

---

## 6. Known Risks and Open Decisions

### Risk 1: Python `Application` model missing `tenant_id` mapping

Python `app/models.py:Application` (line 345) has no `tenant_id` column mapped. New
applications created via Python admin API will have `tenant_id = NULL` in DB unless
the default tenant is hardcoded in the INSERT. Migration 026 sets all existing rows to
the bootstrap tenant UUID, but future Python-created rows will be NULL.

**Impact:** Go DAL filters by `tenant_id` — it will not find Python-created apps that
have no `tenant_id`. Until Python is removed, there is a dual-path risk.

**Recommended fix before Wave 9:** Add `tenant_id` to the Python `Application` SQLAlchemy
model and default it to the bootstrap tenant UUID on insert. This is a two-line change.
Not blocking for Wave 8 since Wave 8 Go handlers read existing apps (which have
`tenant_id` from the backfill).

### Risk 2: Deprecated `orchestrator_id NOT NULL` column

`them.applications.orchestrator_id` is `NOT NULL REFERENCES them.orchestrators(id)`.
`015_phase12_drop_deprecated.sql` exists but is not applied. Python creates new
applications without setting this column, which means Python's INSERT fails unless
the column has a DEFAULT or is set. **This is actually fine: the column exists but
Python's SQLAlchemy model doesn't map it, and PG would reject the INSERT if it's NOT
NULL with no default.** The fact that Python app creation works today means this column
either has a default or was already dropped in the live DB.

**Verify this before Wave 9** by running:
```sql
SELECT column_name, column_default, is_nullable
FROM information_schema.columns
WHERE table_schema='them' AND table_name='applications'
ORDER BY ordinal_position;
```

### Risk 3: Middleware edge fidelity in export/import round-trip

As noted in Q12, `export_graph` emits middleware nodes but not middleware→agent edges.
An export/import cycle correctly restores the `middleware_wirings` table (because
`compile_graph` reads `def_id` and `agent_id` from the node data), but the React Flow
canvas will not show middleware-to-agent edges after import.

**Impact:** Low (middleware not currently used in production). Document it.

### Risk 4: Agent UUID portability in export/import

Export payloads reference agents by UUID. Import will fail if the agent does not exist
in the target environment. This is by design but must be documented clearly in the
import API response when validation fails.

### Risk 5: Fernet key portability in export/import

Encrypted API keys are deliberately absent from export payloads. Users must re-enter API
keys after import. The Go import handler must not attempt to store empty-string keys as
Fernet-encrypted values (would produce an invalid ciphertext for a zero-length plaintext).

---

## 7. Next Migration Scope

### Wave 8 (approved)

Implement in Go:
1. `PUT /{id}/runtime` — AppRuntimeConfig update + cache flush + ep:config invalidation
2. `POST /bulk-delete` — hard-delete + orch cache flush + ep:config invalidation
3. `GET /{id}/export` — relational→graph JSON using export_graph logic

Infrastructure prerequisites (in Wave 8, before handlers):
- Add `them-go-apps-subroutes` Traefik rule (both compose files)
- Add `FlushApplicationOrchCaches` to Go cache layer
- Add DAL: `UpdateRuntimeConfig`, `BulkDeleteApplications`, `ListAppOrchestratorNames`,
  `GetApplicationForExport`

Test requirements:
- Unit tests for all three handlers (mock DAL)
- `go/TEST_INDEX.md` updated in same commit as new tests
- `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md` updated after cutover

### Wave 9 (deferred)

Implement in Go:
1. Port `compile_graph` as Go service function
2. `POST /import` — validate + create application + call compile_graph
3. `PUT /{id}/restore` — load app + call compile_graph in update mode
4. Fix Python `tenant_id` gap (add mapping to Python model)

---

## 8. Summary of Decisions

| Question | Decision |
|---|---|
| Source of truth | Relational rows. Graph is derived on export, compiled on save. |
| Versioning | None. Not needed for current use case. Revisit for CI/CD pipeline use case. |
| Export format | Application Definition v1: `{schema_version:1, name, presentation, graph, canvas}` |
| Compile-on-save | Preserved. Go compile_graph port must follow Python model exactly. |
| ADK compatibility | No schema changes. Agent UUID FK is sufficient. ADK agents registered as `them.agents` rows. |
| Orchestrator_id FK | Dead. Do not populate in Go. Do not join on it. |
| Wave 8 scope | runtime + bulk-delete + export. No compile_graph port. |
| Wave 9 scope | compile_graph port + import + restore + Python tenant_id fix. |
| Middleware wirings in Wave 8 | Read-only (export only). No upsert/delete in Wave 8. |
| runtime_config Go schema | Five fields, pointer types for nullable ints. session_timeout_minutes: accept + persist, do not enforce. |
| Bulk-delete contract | Hard-delete. Flush caches after delete. Runs not deleted (intentional). |
| Middleware edge fidelity | Known v1 limitation. Accept and document. |
