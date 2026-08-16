# R5 — Wave 9 Implementation Readiness Review
# Registry-Backed Application Component Model
# Date: 2026-08-16
# HEAD at review: ca29acd
# Architecture source: docs/architecture-v2/REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md

---

## 1. Executive Conclusion — Is the Architecture Still Sound?

**Yes. The architecture is sound and should be adopted as written.**

The document (`REGISTRY_BACKED_APPLICATION_COMPONENT_MODEL.md`, hereafter "the spec") defines a
four-concept model: Component Definitions (registry) → Application Definition v2 (instances +
connections) → Compiled Runtime Projection (relational rows) → Temporal Execution. Every runtime
invariant the platform depends on is explicitly preserved: `app_orchestrators.name` stays immutable
on the *instance* as the Temporal lookup key; `entry_points.slug` stays on the *instance* as the
epconfig hot-path key; the registry is *never* read at runtime.

The architecture is not over-built for the platform's current size. It solves three concrete problems
that exist today: (1) the Canvas's `ApplicationCreate/Update` API is a bespoke format whose
orchestrator fields are embedded inside entry points, making multi-orchestrator apps inexpressible
without a graph payload; (2) there is no schema-driven validation path — config errors surface only
at Temporal runtime; (3) export/import uses UUID FKs that do not survive environment boundaries.
All three are real defects. The registry-backed model fixes them without adding a runtime layer.

**One pre-condition is unmet and is a hard blocker**: `app_orchestrators` has a cluster-global
`UNIQUE(name)` constraint (`app_orchestrators_name_key`). The spec requires this to become a
per-application constraint (`UNIQUE(application_id, name)`) so that two applications can use the same
orchestrator name. This migration must happen before any Wave 9 code writes new `app_orchestrators`
rows under the v2 model.

**No other architectural issues found.** All fifteen review areas below confirm the spec is
implementable from current HEAD.

---

## 2. Architecture Issues and Changes Recommended

### 2.1 Blocker: `app_orchestrators.name` Global Uniqueness

**Current constraint** (live DB, verified 2026-08-16):
```
"app_orchestrators_name_key" UNIQUE CONSTRAINT, btree (name)
```

**Required constraint**: `UNIQUE(application_id, name)` — the spec correctly states the name is
immutable *per application*, not globally unique.

**Impact**: Without this change, the first two applications that both use the `llm-orchestrator`
builtin definition will collide on name allocation. The current `_generate_orch_name` function in
`app/services/app_compiler.py` (line 534) already passes a set of `db_names` to avoid collisions,
but it does so globally — this must become per-app scoped after the constraint change.

**Migration path** (safe, non-destructive):
```sql
ALTER TABLE them.app_orchestrators DROP CONSTRAINT app_orchestrators_name_key;
ALTER TABLE them.app_orchestrators
  ADD CONSTRAINT uq_app_orch_app_name UNIQUE (application_id, name);
```
The existing `uq_app_orch_app_node UNIQUE(application_id, node_id)` constraint already proves the
pattern. `idx_app_orchestrators_name` btree index should remain for lookup performance.

### 2.2 Required: `application_definitions` Table

The spec's Wave 9 creates `application_definitions` to store versioned definition JSONB with
`revision`, `status` (draft/published), and `definition_hash`. This table does not exist. It is the
centerpiece of Wave 9 and must be created before any draft/publish API can be implemented.

### 2.3 Required: `component_definitions` Base Table

The `component_definitions` base table (spec §7.1) does not exist. It is needed for the registry
resolver before `CompileDefinition` can work. The adoption of `agents` and `middleware_defs` as
subtypes is done via ALTER + backfill, not relocation — existing FKs to `agents.id` are unaffected.

### 2.4 Clarification: `app_orchestrators.edges` Column

The `app_orchestrators` table currently has `edges text[] NOT NULL DEFAULT '{websocket}'`. The spec
deprecates this column in favour of `connections[]` in the Application Definition JSON, but it is
*not* dropped in Wave 9 (the spec calls it a cleanup wave). The new Python write path should stop
populating `edges` for v2 apps and the Go compile path should treat it as always empty. The column
is kept until after the `orchestrators` table and the legacy `GET /ws/orchestrate/{name}` path are
removed in Wave 15.

### 2.5 Clarification: Tenant Scoping of `app_orchestrators`

The live `app_orchestrators` table has no `tenant_id` column. Tenant isolation today is achieved
via `application_id → applications.tenant_id`. The spec does not add `tenant_id` to
`app_orchestrators` — it correctly carries tenant identity through the application FK. This is
confirmed correct and requires no change.

### 2.6 Minor: `middleware_defs.kind` CHECK constraint

The live `middleware_defs` has `kind_check CHECK (kind IN ('guard','cache'))`. The spec's Component
Definition `kind` on the base table uses a different vocabulary (`agent|orchestrator|middleware|tool|
entry_point`). When `middleware_defs` is adopted as the `middleware` subtype, its internal `kind`
column stays as-is (guard/cache sub-classification) and the base `component_definitions.kind` is
always `'middleware'`. The two `kind` fields operate at different levels — no conflict, no migration
needed, but the naming difference should be documented to avoid confusion.

---

## 3. Current Implementation Map

### What Go already owns (live at HEAD)

| Area | Go coverage | Notes |
|---|---|---|
| Applications CRUD | Create, List, Get, Update (name+enabled only), Delete (soft), BulkDelete | Missing: canvas, presentation, graph payload, app_orchestrators write |
| Entry Points CRUD | Create, List (via Get app), Update (slug+type+enabled only), Delete | Missing: access_policy, limits, queue, app_orchestrator_id binding |
| App Runtime Config | `PUT /{app_id}/runtime` | Complete (runtime_config JSONB) |
| Application Definition v2 | None | Does not exist in Go |
| Component Registry | None | Does not exist in Go |
| Graph Compiler | None | Lives entirely in Python `app/services/app_compiler.py` |
| Export / Import / Restore | None in Go | Python-only routes (still on them-bridge) |
| Middleware Wirings | None in Go | Python-only (`PUT /{id}/middleware-wirings`) |
| AppOrchestrator write path | None in Go | No `AppOrchestrator` type exists in Go at all |

### What Python still owns (relevant to Wave 9)

| Route | Handler | Notes |
|---|---|---|
| `GET /{id}/export` | `admin_applications.py:807` | Uses `export_graph()` |
| `POST /import` | `admin_applications.py:828` | Uses `import_application()` |
| `PUT /{id}/restore` | `admin_applications.py:867` | Uses `restore_application()` |
| `PUT /{id}/middleware-wirings` | `admin_applications.py:974` | Uses `put_middleware_wirings()` |
| `POST /{id}/orchestrators/{ao_id}/test-llm` | `admin_applications.py:1022` | LLM smoke test |
| `POST /{id}/orchestrators/{ao_id}/test-voice` | `admin_applications.py:1055` | Voice test |
| `POST /{id}/orchestrators/{ao_id}/test-tts` | `admin_applications.py:1096` | TTS test |
| `PATCH /{id}` (with graph payload) | `admin_applications.py:720` | Calls `compile_graph()` |
| `POST /` (with graph payload) | `admin_applications.py:668` | Calls `compile_graph()` |

### Python app_compiler.py (651 lines — must be ported to Go)

| Function | Lines | What it does |
|---|---|---|
| `validate_graph(graph)` | 64–201 | Validates node/edge structure before compile |
| `compile_graph(app_id, graph, canvas, db, fernet)` | 202–533 | Full projection write: upserts `app_orchestrators`, `entry_points`, `middleware_wirings`; allocates names; resolves agent IDs; encrypts secrets |
| `_generate_orch_name(proposed, hint, db_names, ...)` | 534–555 | Name allocation with uniqueness check |
| `export_graph(app, db)` | 556–651 | Serialises the projection back to portable graph JSON |

---

## 4. DB Gap Analysis

### Tables that do not exist and must be created in Wave 9

| Table | Spec ref | DDL work |
|---|---|---|
| `them.component_definitions` | §7.1 | New table; full DDL in spec |
| `them.application_definitions` | §18 (Option C Wave 9) | New table; full DDL from APPLICATION_EXECUTION_ARCHITECTURE_REVIEW.md |

### Tables that exist and need column additions (ALTER + backfill)

| Table | What to add | Spec ref |
|---|---|---|
| `them.agents` | `namespace`, `version`, `capabilities`, `credential_schema`, `scope`, `status`, `content_hash`, `implementation_type` (aligned from `transport`) | §18, §5 |
| `them.middleware_defs` | same shared-contract columns + `default_config` rename from `config` | §18, §7.2 |
| `them.app_orchestrators` | `component_definition_id UUID`, `component_version INT`, `source_definition_hash TEXT` | §18, §8 |
| `them.entry_points` | `component_definition_id UUID`, `component_version INT`, `source_definition_hash TEXT` | §18, §8 |
| `them.middleware_wirings` | `component_definition_id UUID`, `component_version INT`, `source_definition_hash TEXT` | §18, §8 |
| `them.applications` | `active_definition_id UUID`, `source_definition_hash TEXT` | §18 (Option C) |
| `them.runs` | `definition_id UUID` | §18 (Option C) |

### Constraint changes required

| Table | Current | Target | Action |
|---|---|---|---|
| `them.app_orchestrators` | `UNIQUE(name)` global | `UNIQUE(application_id, name)` | DROP + ADD; see §2.1 above |

### Seed data required

| Object | What | Where |
|---|---|---|
| Builtin `llm-orchestrator` | 1 row in `component_definitions` (kind=`orchestrator`, namespace=`them.builtin`, name=`llm-orchestrator`, version=1, scope=`builtin`, status=`published`) | Migration seed script |
| 5 optional EP palette rows | kind=`entry_point`, one per protocol string | Optional; needed for Canvas; can defer |

### Current migration level

Latest applied migration: `025_events_transport`. Wave 9 requires `026` (component_definitions +
application_definitions tables) and `027` (ALTER existing tables). Both are new. Neither `026` nor
`027` files exist in `db/` yet.

---

## 5. Go Gap Analysis

### Missing packages (must be created)

| Package | Purpose | Spec ref |
|---|---|---|
| `go/internal/admin/registry/` | Component-definition resolver (UUID fast-path + portable-identity fallback), `configuration_schema` JSON Schema validator, capability/connection compatibility checker | §18, §14 |
| `go/internal/admin/definition/` | `CompileDefinition` (16-step publish pipeline), draft/validate/publish/activate, export/import/restore/clone/rollback over v2 format | §16, §18 |
| `go/internal/admin/dal/component_definitions.go` | Base CRUD for `component_definitions`; agent/middleware subtype reads | §18 |
| `go/internal/admin/dal/definitions.go` | `application_definitions` CRUD + full projection writers for `app_orchestrators`, `entry_points`, `middleware_wirings` | §18 |

### Gaps in existing packages

| Package | Gap | Impact |
|---|---|---|
| `go/internal/admin/dal/dal.go` | `ApplicationInput` has only `Name + Enabled`; `EntryPointInput` has only `Slug + Type + Enabled`; no `Application` fields for canvas/presentation/graph | All rich application writes go to Python; Go can only do name/enabled updates |
| `go/internal/admin/dal/dal.go` | No `AppOrchestrator` type at all | Go cannot write or read the orchestrator projection |
| `go/internal/admin/dal/applications.go` | `CreateApplication`, `UpdateApplication`, `CreateEntryPoint`, `UpdateEntryPoint` are all minimal; no projection writes | Cannot replace Python save path |
| `go/internal/admin/applications.go` | Handlers have no graph/canvas body parsing, no orchestrator sub-resources | Cannot accept v2 `components[]/connections[]` payloads |
| `go/internal/admin/service/` | No `AppService` equivalent for compile/publish | Python `app_compiler.py` has no Go counterpart |

### What is NOT missing

- Fernet crypto: `go/internal/crypto/` already provides `EncryptStored`/`DecryptStored` (used by agents scan job). The `CompileDefinition` secret resolution can use these directly.
- Redis cache invalidation: already in `go/internal/admin/service/agents.go` (`invalidate()` pattern). The same Redis key flush logic from `_flush_orch_caches` / `_flush_mw_chain_cache` (Python) maps to the same Redis keys — just needs implementing in Go.
- Auth middleware: `RequireSuperAdmin` is already wired on all admin routes.
- JSON Schema validation: no existing Go library in go.mod. Must add (e.g. `santhosh-tekuri/jsonschema/v6` or `xeipuuv/gojsonschema`) — one dependency to add for `configuration_schema` validation.

---

## 6. Frontend Gap Analysis

### What the Canvas UI currently sends

The current `ApplicationCreate`/`ApplicationUpdate` API shape (Python, still the live write path)
uses:
```json
{
  "name": "...",
  "presentation": {...},
  "graph": {
    "nodes": [{"id": "node_id", "type": "...", "data": {...}}],
    "edges": [{"source": "...", "target": "...", "data": {...}}]
  },
  "canvas": {"layout": {...}}
}
```

This is the **v1 graph format** (node/edge React Flow model). The spec defines a **v2 format**
(`components[]/connections[]`). The frontend currently produces v1; Wave 9 must either:
- (a) Accept v1 from the frontend and translate to v2 in the Go handler (bridge/adapter layer), or
- (b) Migrate the frontend to produce v2 directly.

**Recommendation: option (a) for Wave 9, option (b) in a follow-up Canvas wave.** The v1→v2
translation is isomorphic (it is the same data, different structure), and migrating the canvas
React Flow component is out of Wave 9's scope. The Wave 9 Go compiler should internally normalize
to v2 but accept v1 from the frontend.

### Frontend types that need extension

| File | What to add |
|---|---|
| `frontend/src/lib/api.ts` | `ApplicationDefinition`, `ComponentDefinition`, `DefinitionRef`, `ComponentInstance`, `Connection` types |
| `frontend/src/app/admin/applications/page.tsx` | Definition status indicator, "newer version available" badge, publish/validate/activate buttons |
| `frontend/src/app/admin/applications/page.tsx` | `definition_id`, `revision`, `status` fields on ApplicationOut response |

### Frontend changes that are NOT required in Wave 9

- Rewriting the canvas drag-drop model (React Flow stays as-is; backend translation layer bridges v1→v2)
- Component palette showing registry definitions (nice to have, not blocking publish)
- Per-definition config schema form generation (deferred to Wave 14 Canvas wave)

---

## 7. Canonical Application Definition Example

The full v2 example is in the spec (§9). For implementation reference, here is the minimal valid
v2 definition for a single-entry-point app with one orchestrator and one agent:

```jsonc
{
  "schema_version": 2,
  "application_id": "<app-uuid>",
  "revision": 1,
  "status": "draft",
  "name": "Simple Support Bot",
  "presentation": { "color": "#6366f1" },

  "components": [
    {
      "instance_id": "orch_main",
      "name": "support_main",
      "definition_ref": {
        "kind": "orchestrator",
        "namespace": "them.builtin",
        "name": "llm-orchestrator",
        "version": 1
      },
      "config": {
        "system_prompt": "You are a helpful support agent.",
        "llm": { "provider": "anthropic", "model": "claude-sonnet" },
        "max_iterations": 10
      },
      "secret_bindings": { "llm_api_key": "secret://tenant/main-llm" }
    },
    {
      "instance_id": "agent_kb",
      "definition_ref": {
        "kind": "agent",
        "namespace": "them.tenant.00000000-0000-0000-0000-000000000001",
        "name": "knowledge-base",
        "version": 1
      },
      "config": {}
    }
  ],

  "entry_points": [
    {
      "instance_id": "ep_ws",
      "slug": "support-ws",
      "protocol": "websocket",
      "root": "orch_main",
      "access_policy": { "mode": "token" },
      "limits": { "max_concurrent_sessions": 50 }
    }
  ],

  "connections": [
    { "source": "ep_ws",     "target": "orch_main", "type": "entry" },
    { "source": "orch_main", "target": "agent_kb",  "type": "tool"  }
  ],

  "canvas": { "viewport": { "x": 0, "y": 0, "zoom": 1 }, "layout": {} },
  "runtime_config": {}
}
```

Key rules:
- `name` on an orchestrator instance is the Temporal lookup key — must match `app_orchestrators.name` exactly after compile.
- `slug` on an entry point is the epconfig lookup key — must match `entry_points.slug` exactly after compile.
- `secret_bindings` carries only `secret://` references; values are resolved at publish.

---

## 8. Concrete DB Design

### 8.1 Migration 026 — New tables

```sql
-- 026_wave9_registry.sql

-- Component Definitions (base table — Wave 9 registry)
CREATE TABLE them.component_definitions (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind                 TEXT NOT NULL CHECK (kind IN ('agent','orchestrator','middleware','tool','entry_point')),
  namespace            TEXT NOT NULL,
  name                 TEXT NOT NULL,
  version              INTEGER NOT NULL,
  display_name         TEXT NOT NULL,
  description          TEXT,
  implementation_type  TEXT NOT NULL,
  configuration_schema JSONB NOT NULL DEFAULT '{}'::jsonb,
  default_config       JSONB NOT NULL DEFAULT '{}'::jsonb,
  capabilities         JSONB NOT NULL DEFAULT '[]'::jsonb,
  input_schema         JSONB,
  output_schema        JSONB,
  credential_schema    JSONB NOT NULL DEFAULT '[]'::jsonb,
  scope                TEXT NOT NULL CHECK (scope IN ('builtin','tenant')),
  tenant_id            UUID,
  status               TEXT NOT NULL CHECK (status IN ('draft','published','deprecated')),
  content_hash         TEXT NOT NULL,
  enabled              BOOLEAN NOT NULL DEFAULT true,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at         TIMESTAMPTZ,
  UNIQUE (kind, namespace, name, version)
);
CREATE INDEX idx_component_defs_kind_namespace ON them.component_definitions(kind, namespace);
CREATE INDEX idx_component_defs_tenant ON them.component_definitions(tenant_id) WHERE tenant_id IS NOT NULL;

-- Application Definitions (versioned design-source JSONB — Wave 9)
CREATE TABLE them.application_definitions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  application_id   UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  revision         INTEGER NOT NULL,
  status           TEXT NOT NULL CHECK (status IN ('draft','published','archived')),
  definition       JSONB NOT NULL,
  definition_hash  TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at     TIMESTAMPTZ,
  UNIQUE (application_id, revision)
);
CREATE INDEX idx_app_defs_application ON them.application_definitions(application_id);

-- Seed: builtin llm-orchestrator Component Definition
INSERT INTO them.component_definitions
  (kind, namespace, name, version, display_name, description, implementation_type,
   configuration_schema, default_config, capabilities, credential_schema,
   scope, tenant_id, status, content_hash, enabled, published_at)
VALUES
  ('orchestrator', 'them.builtin', 'llm-orchestrator', 1,
   'LLM Orchestrator', 'Standard plan-act-observe orchestrator using an LLM',
   'llm_loop',
   '{"type":"object","properties":{"system_prompt":{"type":"string"},"llm":{"type":"object"},"max_iterations":{"type":"integer"},"max_parallel_tools":{"type":"integer"},"history_window":{"type":"integer"},"budget_tokens":{"type":"integer"}}}',
   '{"max_iterations":10,"max_parallel_tools":3,"history_window":20}',
   '["delegation.target","tool.delegator"]',
   '[{"name":"llm_api_key","required":true,"description":"LLM provider API key"}]',
   'builtin', NULL, 'published',
   'sha256:builtin-llm-orchestrator-v1',
   true, now());
```

### 8.2 Migration 027 — ALTER existing tables

```sql
-- 027_wave9_alter.sql

-- Fix the blocker: make app_orchestrators.name per-application unique
ALTER TABLE them.app_orchestrators DROP CONSTRAINT app_orchestrators_name_key;
ALTER TABLE them.app_orchestrators
  ADD CONSTRAINT uq_app_orch_app_name UNIQUE (application_id, name);

-- Stamp columns on projection tables (nullable; backfilled by Wave 9 compile)
ALTER TABLE them.app_orchestrators
  ADD COLUMN component_definition_id UUID,
  ADD COLUMN component_version       INTEGER,
  ADD COLUMN source_definition_hash  TEXT;

ALTER TABLE them.entry_points
  ADD COLUMN component_definition_id UUID,
  ADD COLUMN component_version       INTEGER,
  ADD COLUMN source_definition_hash  TEXT;

ALTER TABLE them.middleware_wirings
  ADD COLUMN component_definition_id UUID,
  ADD COLUMN component_version       INTEGER,
  ADD COLUMN source_definition_hash  TEXT;

-- Option C columns on applications (required for active_definition_id)
ALTER TABLE them.applications
  ADD COLUMN active_definition_id  UUID REFERENCES them.application_definitions(id),
  ADD COLUMN source_definition_hash TEXT;

-- definition_id on runs (audit trail)
ALTER TABLE them.runs
  ADD COLUMN definition_id UUID REFERENCES them.application_definitions(id);

-- Shared-contract columns on agents (agents becomes the 'agent' subtype)
ALTER TABLE them.agents
  ADD COLUMN namespace              TEXT NOT NULL DEFAULT 'them.tenant.default',
  ADD COLUMN version                INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN capabilities           JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN credential_schema      JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN scope                  TEXT NOT NULL DEFAULT 'tenant',
  ADD COLUMN def_status             TEXT NOT NULL DEFAULT 'published',
  ADD COLUMN content_hash           TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN component_def_id       UUID;

-- Shared-contract columns on middleware_defs (becomes the 'middleware' subtype)
ALTER TABLE them.middleware_defs
  ADD COLUMN namespace              TEXT NOT NULL DEFAULT 'them.builtin',
  ADD COLUMN version                INTEGER NOT NULL DEFAULT 1,
  ADD COLUMN default_config         JSONB,
  ADD COLUMN capabilities           JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN credential_schema      JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN scope                  TEXT NOT NULL DEFAULT 'builtin',
  ADD COLUMN def_status             TEXT NOT NULL DEFAULT 'published',
  ADD COLUMN content_hash           TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN component_def_id       UUID;
-- Backfill default_config from existing config
UPDATE them.middleware_defs SET default_config = config WHERE default_config IS NULL;
```

### 8.3 Backfill jobs (run after 027)

1. **Agent base rows**: INSERT one `component_definitions` row per existing agent (kind=`agent`,
   namespace=`them.tenant.<tenant_id>`, name=agent.slug, version=1, scope=`tenant`); set
   `agents.component_def_id = component_definitions.id`.

2. **Middleware base rows**: same for each `middleware_defs` row (kind=`middleware`, scope=`builtin`
   where `is_builtin`, else `tenant`).

3. **Application_definitions backfill**: synthesize a revision=1 `application_definitions` JSONB
   from each app's current projection (call `export_graph()` and reformat to v2 schema).

4. **Stamp projection rows**: once app_definition rows exist, set `applications.active_definition_id`
   and `app_orchestrators.component_definition_id` / `component_version` = 1 (llm-orchestrator).

Backfills 1–4 are one-time idempotent scripts, safe to run while the platform is live (no locks
beyond row-level).

---

## 9. API Design

### 9.1 Component Definition CRUD

```
GET    /api/v1/admin/component-definitions
         ?kind=agent|orchestrator|middleware|tool
         ?namespace=them.builtin|them.tenant.<id>
         ?status=published|draft|deprecated
POST   /api/v1/admin/component-definitions
PUT    /api/v1/admin/component-definitions/{id}
GET    /api/v1/admin/component-definitions/{id}
DELETE /api/v1/admin/component-definitions/{id}       (deprecate, not hard-delete)
POST   /api/v1/admin/component-definitions/{id}/publish
PATCH  /api/v1/admin/component-definitions/{id}/deprecate
GET    /api/v1/admin/component-definitions/{id}/versions
```

**Response shape** for a single definition:
```json
{
  "id": "<uuid>",
  "kind": "agent",
  "namespace": "them.tenant.abc",
  "name": "fraud-agent",
  "version": 2,
  "display_name": "Fraud Detection Agent",
  "description": "...",
  "implementation_type": "a2a_async",
  "configuration_schema": {...},
  "default_config": {...},
  "capabilities": ["tool.callable"],
  "credential_schema": [{"name":"api_token","required":true}],
  "scope": "tenant",
  "tenant_id": "<tenant-uuid>",
  "status": "published",
  "content_hash": "sha256:...",
  "enabled": true,
  "created_at": "...",
  "published_at": "..."
}
```

### 9.2 Application Definition APIs

```
GET    /api/v1/admin/applications/{id}/definition        — get current definition (active or draft)
PUT    /api/v1/admin/applications/{id}/definition/draft  — save draft (cheap, no compile)
POST   /api/v1/admin/applications/{id}/validate          — validate without publishing (returns errors)
POST   /api/v1/admin/applications/{id}/publish           — compile + publish (full 16-step pipeline)
POST   /api/v1/admin/applications/{id}/activate/{rev}   — activate a specific published revision
GET    /api/v1/admin/applications/{id}/revisions         — list all revisions with status
GET    /api/v1/admin/applications/{id}/export            — export portable v2 JSON (no secret values)
POST   /api/v1/admin/applications/import                 — create new app from portable v2 JSON
PUT    /api/v1/admin/applications/{id}/restore           — overwrite app from portable v2 JSON
POST   /api/v1/admin/applications/{id}/clone             — clone app to new name
POST   /api/v1/admin/applications/{id}/rollback/{rev}   — roll back to a previous published revision
```

**Save draft body** (v2 format, as defined in spec §9):
```json
{
  "components": [...],
  "entry_points": [...],
  "connections": [...],
  "canvas": {...},
  "runtime_config": {...},
  "policies": {...}
}
```

**Validate response** (422 if invalid):
```json
{
  "valid": false,
  "errors": [
    {"type": "unresolved_definition", "instance_id": "orch_main", "ref": {"kind":"orchestrator","namespace":"them.builtin","name":"llm-orchestrator","version":99}},
    {"type": "config_invalid", "instance_id": "agent_kb", "fields": [{"path": "timeout_seconds", "message": "must be integer"}]},
    {"type": "connection_incompatible", "source": "orch_main", "target": "mw_pii", "reason": "target lacks capability tool.callable"}
  ]
}
```

### 9.3 Route backward-compatibility notes

The existing `PATCH /api/v1/admin/applications/{id}` with inline `entry_points[]` and `graph{}` is
the v1 write path. It must remain live during Wave 9 (the frontend still uses it). Wave 9 adds the
`/definition/*` sub-resource routes as the v2 path. Both co-exist until the Canvas is migrated.

---

## 10. Save / Validate / Publish Design

### Three distinct operations

| Operation | Trigger | Side effects | Fail behavior |
|---|---|---|---|
| **Save Draft** | `PUT /definition/draft` | Write `application_definitions` row (status=draft); no projection change; no cache flush | None — persists whatever the user saved |
| **Validate** | `POST /validate` | Run steps 1–6 of CompileDefinition in read-only mode (no DB writes); return error list | Returns 422 with structured errors; app state unchanged |
| **Publish** | `POST /publish` | Full 16-step compile (§16); writes all projection tables atomically; flushes caches | Transaction rollback if any step fails; returns 422 with errors; projection unchanged |

### Idempotency

- Saving the same draft content twice produces the same `definition_hash` and must be a no-op (UPDATE WHERE hash != new_hash).
- Publishing the same definition twice (same hash as the current active revision) is a no-op; return 200 with the existing revision.

### Version allocation

On each Publish that succeeds: `revision = MAX(revision WHERE application_id = ?) + 1`. Never
reuse revision numbers. Drafts use `revision = 0` or `NULL` until published.

---

## 11. Compiler Design

The `CompileDefinition` function in `go/internal/admin/definition/` is the Go port of Python's
`compile_graph` (651-line `app_compiler.py`) extended with registry resolution.

### Step sequence (spec §16)

```
1. Load draft definition from application_definitions
2. Resolve each components[].definition_ref (UUID cache → portable-identity lookup)
3. Pin definition_id + version onto each instance
4. Validate each instance config against definition.configuration_schema (JSON Schema)
5. Validate connections[] compatibility (capabilities + input/output schemas)
6. Check secret_bindings: required slots must have a secret:// reference that resolves
7. Merge config: default_config ⊕ tenant/env ⊕ instance.config (deep merge, right-wins)
8. Write projection rows (inside single DB transaction):
   a. Upsert app_orchestrators keyed by instance name (immutable)
   b. Upsert entry_points keyed by slug (immutable)
   c. Replace middleware_wirings in via[] position order
   d. Derive allowed_agent_ids[] from tool connections
   e. Set delegatable from delegation connections
   f. Carry component_definition_id + component_version pins
9. Encrypt secret values: Fernet-encrypt plaintext → write ciphertext to projection column
10. Stamp definition_hash on applications.source_definition_hash
11. Set applications.active_definition_id; flip definition status draft→published
12. Flush caches (same keys as today):
    DEL them:app:{app_id}:orch:{name} (per name)
    DEL them:orch:loc:{name} (per name)
    DEL them:agents:registry
    PUBLISH them:ep:config:changed {app_id}
```

### Key invariants the compiler must enforce

1. **Name immutability**: if an `app_orchestrators` row with this `(application_id, instance_id)` already exists, the `name` column MUST NOT be updated — only config fields change. Changing the name would break in-flight Temporal workflows. Use `node_id`/`instance_id` as the upsert key, not `name`.

2. **Slug immutability**: same for `entry_points.slug`. Upsert key is `(application_id, instance_id)` where instance_id maps to the EP's `instance_id`. The slug cannot change once created.

3. **Secret values never touch the definition JSONB**: the compiler reads `secret://` references, resolves them from the secret store, Fernet-encrypts them, and writes only ciphertext to projection columns. The reference stays in the definition; the value never enters it.

4. **Atomicity**: steps 8–11 are a single DB transaction. If step 9 (encrypt) fails for any instance, the entire transaction rolls back; the projection is unchanged.

5. **Orphan cleanup**: after upserting, DELETE any `app_orchestrators` / `entry_points` / `middleware_wirings` rows for this application that have no corresponding instance in the new definition. This handles component removal.

### Config merge implementation (Go)

```go
// deepMerge merges src into dst (right-wins on conflict, deep for maps).
func deepMerge(dst, src map[string]any) map[string]any {
    result := make(map[string]any, len(dst))
    for k, v := range dst {
        result[k] = v
    }
    for k, v := range src {
        if srcMap, ok := v.(map[string]any); ok {
            if dstMap, ok := result[k].(map[string]any); ok {
                result[k] = deepMerge(dstMap, srcMap)
                continue
            }
        }
        result[k] = v
    }
    return result
}

// ResolveConfig computes the 3-layer merge for one component instance.
func ResolveConfig(defDefault, tenantEnv, instanceOverride map[string]any) map[string]any {
    merged := deepMerge(defDefault, tenantEnv)   // L1 ⊕ L2
    return deepMerge(merged, instanceOverride)   // ⊕ L3
}
```

### v1→v2 translation (bridge, accept from frontend)

Until the Canvas is migrated, the handler must translate the incoming v1 `graph{nodes,edges}` format
into a v2 `components[]/connections[]` structure before calling `CompileDefinition`. The translation
is mechanical:
- Each `nodes[]` entry with `type="orchestrator"` → a component instance with
  `definition_ref={kind:orchestrator, namespace:them.builtin, name:llm-orchestrator, version:1}`.
- Each `nodes[]` entry with `type="agent"` → look up the agent row by `data.agent_id` and synthesize
  a portable ref from `agents.namespace+name+version` (after the subtype adoption columns exist).
- `edges[]` with `type="delegation"` → `connections[]{type:delegation}`; `type="tool"` →
  `connections[]{type:tool}`; `type="middleware_chain"` → the tool/delegation edge plus a `via[]`
  list.

---

## 12. Migration Strategy

### Sequencing within Wave 9

```
Phase 1: Schema + Seed
  (a) Write and apply migration 026 (new tables: component_definitions, application_definitions)
  (b) Write and apply migration 027 (ALTER existing tables + constraint fix)
  (c) Run backfill scripts (agent + middleware base rows; app_definitions synthesis)
  (d) Verify: all agents have component_def_id; applications.active_definition_id set

Phase 2: Registry DAL + Resolver
  (a) go/internal/admin/dal/component_definitions.go (CRUD)
  (b) go/internal/admin/registry/ (resolver: UUID fast-path + portable-identity fallback)
  (c) go/internal/admin/registry/ (JSON Schema validator for configuration_schema)
  (d) Tests: resolver tests with synthetic fixture data

Phase 3: CompileDefinition
  (a) go/internal/admin/definition/ (deepMerge, ResolveConfig)
  (b) go/internal/admin/dal/definitions.go (application_definitions CRUD + all projection writers)
  (c) go/internal/admin/definition/compile.go (full 16-step pipeline)
  (d) Tests: compile with test app — verify projection rows match expected shape

Phase 4: Draft/Validate/Publish API
  (a) Handler: PUT /definition/draft (cheap save)
  (b) Handler: POST /validate (read-only compile, return errors)
  (c) Handler: POST /publish (full compile + transaction)
  (d) Handler: POST /activate/{rev}, GET /revisions
  (e) Tests: end-to-end publish creates correct projection; rollback on error

Phase 5: Export/Import/Restore/Clone/Rollback
  (a) Port export_graph() → Go (serialize projection to v2 JSON)
  (b) Import: create app from v2 JSON; fail-closed on missing refs; fail-open on secrets
  (c) Restore: overwrite app from v2 JSON (same as import but into existing app_id)
  (d) Clone, Rollback
  (e) Tests: export→import→publish round-trip produces identical projection

Phase 6: Component Definition CRUD APIs (Canvas palette)
  (a) GET/POST/PUT/DELETE /component-definitions handlers
  (b) Tests: CRUD + publish/deprecate lifecycle

Phase 7: Python cutover
  (a) Traefik labels: move /definition/*, /validate, /publish, /export, /import, /restore to Go
  (b) Remove Python routes from them-bridge (export, import, restore, middleware-wirings)
  (c) Update Python /admin/applications PATCH/POST to call Go publish internally or keep for legacy v1
```

### Backward compatibility during migration

- The v1 write path (`PATCH /{id}` with `graph{}`) stays on Python (or Go with translation) throughout Wave 9. The frontend is not broken.
- `app_orchestrators`, `entry_points`, and `middleware_wirings` shapes are **unchanged** — the Temporal worker loads these rows and will not notice the new `component_definition_id` nullable columns.
- All existing apps keep working. The `applications.active_definition_id` column is nullable; apps without a backfilled definition simply have `NULL` there — they still load from the live projection.
- The `app_orchestrators.name` constraint change (§2.1) is safe: existing rows have unique names across the table, so the DROP/ADD is non-conflicting.

---

## 13. Temporal Impact

**No Temporal code changes required in Wave 9.**

The worker's load path reads `app_orchestrators` by `name` then falls back to the global
`orchestrators` table. Both paths continue to work:
- Primary path: `app_orchestrators` by `name` — shape unchanged; `component_definition_id` /
  `component_version` columns are nullable and ignored by the loader.
- Fallback path: `orchestrators` table — deprecated but not dropped in Wave 9; no change.

The `ResolvedOrchestrator` Go struct in the worker gains two fields for auditing
(`ComponentDefID`, `ComponentVersion`) but these are `omitempty` — they do not affect workflow
determinism because they are never passed to an LLM or used in branching.

**Temporal replay safety**: the worker loads config from the projection at workflow start and caches
the snapshot in Event History. A Wave 9 publish (which rewrites projection rows for *new* runs)
cannot alter an in-flight workflow. The snapshot in history is deterministic. No changes to
`go/internal/temporal/` or `app/temporal/` are needed.

**One deferred coupling to clean up (Wave 15, not Wave 9)**: the `agent__orch__<name>` double-prefix
string parsing in the Temporal loader (`app/temporal/loaders.py`). The spec calls for replacing this
with an explicit `ref_kind`/`transport` field from the projection. This requires a new column on
`app_orchestrators` and a loader change — deferred to Wave 14/15 when the legacy `orchestrators`
table fallback is removed.

---

## 14. Implementation Phases

### Wave 9 phases (detailed in §12 above)

| Phase | Deliverable | Blocking | Estimated size |
|---|---|---|---|
| 9.1 Schema + Seed | Migrations 026+027 + backfill scripts | None | 1–2 days |
| 9.2 Registry DAL + Resolver | `registry/` package + `dal/component_definitions.go` | 9.1 | 2 days |
| 9.3 CompileDefinition | `definition/compile.go` + `dal/definitions.go` | 9.2 | 3–4 days |
| 9.4 Draft/Validate/Publish API | Handlers + routes + tests | 9.3 | 2 days |
| 9.5 Export/Import/Restore/Clone | Port from Python | 9.3 | 2–3 days |
| 9.6 Component Definition CRUD | Palette APIs | 9.2 | 1–2 days |
| 9.7 Python cutover | Traefik + route removal | 9.4+9.5 | 1 day |

**Total Wave 9 estimate**: 12–16 focused implementation days.

### Wave sequencing (unchanged from spec §19)

| Wave | Domain | Depends on |
|---|---|---|
| **9** | Application Definition + Component Registry | Wave 8 complete ✓ |
| **10** | Runs read tail | Run recorder ✓ |
| **11** | Run control | Go Temporal signaler ✓ |
| **12** | Apps runtime surface (/apps) | Wave 9 (compile/publish) |
| **13** | A2A server | Wave 9 registry |
| **14** | Admin ops: middleware-defs = component-def CRUD; AO test-llm/voice/tts | Wave 9 |
| **15** | Voice + legacy deprecation + Python removal | Waves 8–14 |

---

## 15. Risks

### High severity

| Risk | Likelihood | Mitigation |
|---|---|---|
| `app_orchestrators.name` constraint change breaks existing data | Low (existing names are already unique across the table — no conflicts) | Run `SELECT name, COUNT(*) FROM them.app_orchestrators GROUP BY name HAVING COUNT(*) > 1` before migration to verify zero conflicts |
| `CompileDefinition` transaction writes wrong `app_orchestrators.name` on re-compile | Medium (easy to miss the immutability invariant) | Enforce in DAL: `INSERT ... ON CONFLICT (application_id, instance_id) DO UPDATE SET ... WHERE name = EXCLUDED.name` — name column excluded from UPDATE |
| Secret resolver reads plaintext and it leaks into a log or error message | Medium | Never log the resolved secret value; only log the `secret://` reference; use `cfg.SafeString()` pattern from the Go auth service |
| Definition JSONB stores a secret value accidentally (user sets config: {api_key: "sk-..."}) | Low but catastrophic | `configuration_schema` for the `llm-orchestrator` definition must mark `api_key` fields as excluded from instance config (they are `secret_bindings` only). The compiler must reject any config key that appears in `credential_schema`. |
| JSON Schema validator library adds unacceptable transitive dependencies | Low | Evaluate `santhosh-tekuri/jsonschema/v6` (no CGO, minimal deps) before committing |

### Medium severity

| Risk | Likelihood | Mitigation |
|---|---|---|
| Python `_generate_orch_name` is still called on v1 PATCH after 027 migration changes constraint | Medium | After 027, update Python to scope the uniqueness check to `(application_id, name)` instead of global |
| Frontend sends a v1 `graph{}` payload to a Go handler that only understands v2 | Medium | Implement the v1→v2 translation layer in Wave 9 before cutting Traefik over |
| Backfill synthesizes a wrong `application_definitions` JSONB that fails on the first publish | Low | Validate each synthesized definition via `validate_graph` before inserting; skip and log apps that cannot be synthesized |
| `middleware_defs.kind` check constraint (`guard|cache`) conflicts with base table `kind` (`middleware`) | None — they are different columns at different levels | Document clearly; see §2.6 |

### Low severity

| Risk | Mitigation |
|---|---|
| `go test ./...` fails after adding JSON Schema library | Run `go mod tidy` + full test suite before committing any dependency |
| Portable ref lookup is slow under load | Registry is never on the runtime hot path; only called at publish; acceptable |

---

## 16. Recommended First Implementation Phase

**Start with Phase 9.1 (Schema + Seed) and Phase 9.2 (Registry DAL + Resolver) in the same session.**

### Exact tasks for the first implementation session

1. **Fix the blocker first**: write and apply migration `027` with the `app_orchestrators.name`
   constraint change. Verify:
   ```sql
   SELECT name, COUNT(*) FROM them.app_orchestrators GROUP BY name HAVING COUNT(*) > 1;
   -- expect 0 rows before running
   ```
   Then apply:
   ```sql
   ALTER TABLE them.app_orchestrators DROP CONSTRAINT app_orchestrators_name_key;
   ALTER TABLE them.app_orchestrators ADD CONSTRAINT uq_app_orch_app_name UNIQUE (application_id, name);
   ```

2. **Write and apply migration `026`**: `component_definitions` table + seed `llm-orchestrator` row.
   Write `application_definitions` table. File: `db/026_wave9_registry.sql`.

3. **Write and apply migration `027_alter`**: ALTER `agents`, `middleware_defs`, `app_orchestrators`,
   `entry_points`, `middleware_wirings`, `applications`, `runs`. File: `db/027_wave9_alter.sql`.

4. **Backfill agents**: one-time script — insert `component_definitions` row per agent, set
   `agents.component_def_id`. Run inside `them-postgres`.

5. **Create `go/internal/admin/dal/component_definitions.go`**: `ListComponentDefinitions`,
   `GetComponentDefinition`, `GetComponentDefinitionByRef(kind, namespace, name, version)`,
   `CreateComponentDefinition`, `UpdateComponentDefinition`.

6. **Create `go/internal/admin/registry/resolver.go`**: `Resolve(ctx, ref) (ComponentDefinition, error)`
   implementing UUID fast-path + portable-identity fallback.

7. **Run `go test ./...`** — all existing tests must still pass.

8. **Update `go/TEST_INDEX.md`** with the new registry package tests.

9. **Commit**: `feat(wave9): component_definitions schema + registry resolver`.

10. **Update `docs/architecture-v2/CURRENT.md`** to note Wave 9 Phase 1+2 complete.

This first session delivers the schema foundation and the registry resolver that all later compile
and publish work depends on. No application behavior is changed; the Temporal worker is unaffected;
the frontend sees no difference. It is safe, testable, and incremental.

---

## Appendix A: Current Go `ApplicationInput` / `EntryPointInput` Gap

For reference, the current Go DAL types at HEAD (need full expansion in Phase 9.3):

```go
// Current (go/internal/admin/dal/dal.go:186)
type ApplicationInput struct {
    Name    string `json:"name"`
    Enabled *bool  `json:"enabled,omitempty"`
}

// Required (Wave 9 target)
type ApplicationInput struct {
    Name               string          `json:"name"`
    Enabled            *bool           `json:"enabled,omitempty"`
    Presentation       map[string]any  `json:"presentation,omitempty"`
    Canvas             map[string]any  `json:"canvas,omitempty"`
    RuntimeConfig      map[string]any  `json:"runtime_config,omitempty"`
    ActiveDefinitionID *string         `json:"active_definition_id,omitempty"`
    SourceDefHash      *string         `json:"source_definition_hash,omitempty"`
}

// Current (go/internal/admin/dal/dal.go:192)
type EntryPointInput struct {
    Slug           string `json:"slug"`
    EntryPointType string `json:"entry_point_type"`
    Enabled        *bool  `json:"enabled,omitempty"`
}

// Required (Wave 9 target)
type EntryPointInput struct {
    Slug                   string         `json:"slug"`
    EntryPointType         string         `json:"entry_point_type"`
    Enabled                *bool          `json:"enabled,omitempty"`
    AppOrchestratorID      *string        `json:"app_orchestrator_id,omitempty"`
    AccessPolicy           map[string]any `json:"access_policy,omitempty"`
    ConversationTokenLimit *int           `json:"conversation_token_limit,omitempty"`
    MaxConcurrentSessions  *int           `json:"max_concurrent_sessions,omitempty"`
    QueueTimeoutSeconds    *int           `json:"queue_timeout_seconds,omitempty"`
    QueueMessage           *string        `json:"queue_message,omitempty"`
}
```

---

## Appendix B: Files to Create / Modify in Wave 9

| File | Action | Notes |
|---|---|---|
| `db/026_wave9_registry.sql` | Create | component_definitions + application_definitions + seed |
| `db/027_wave9_alter.sql` | Create | ALTER existing tables + constraint fix |
| `go/internal/admin/dal/component_definitions.go` | Create | Base CRUD |
| `go/internal/admin/dal/definitions.go` | Create | application_definitions + projection writers |
| `go/internal/admin/dal/dal.go` | Modify | Extend ApplicationInput, EntryPointInput; add AppOrchestratorInput, AppOrchestrator type |
| `go/internal/admin/dal/applications.go` | Modify | Extend Create/Update to write canvas, presentation, graph |
| `go/internal/admin/registry/resolver.go` | Create | Portable-ref resolver |
| `go/internal/admin/registry/validator.go` | Create | JSON Schema config validator |
| `go/internal/admin/definition/compile.go` | Create | 16-step CompileDefinition |
| `go/internal/admin/definition/export.go` | Create | Serialize projection → v2 JSON |
| `go/internal/admin/definition/import.go` | Create | v2 JSON → draft + validate |
| `go/internal/admin/applications.go` | Modify | Add definition sub-resource handlers |
| `go/internal/admin/router.go` | Modify | Register new routes |
| `go/cmd/them/main.go` | Modify | Wire new handlers |
| `go/TEST_INDEX.md` | Modify | Add new test rows |
| `docs/architecture-v2/CURRENT.md` | Modify | Update after each phase |
| `docs/SCHEMA.md` | Modify | Document new tables + columns |

---

*Review written 2026-08-16. Architecture source committed at 55ad66e + this session's doc.
All DB facts verified against live `them-postgres` at HEAD ca29acd.*
