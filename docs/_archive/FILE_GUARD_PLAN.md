# File Guard — Implementation Plan
# Phase 1: Per-Agent File Scanning via Canvas Guard Node
# Last updated: 2026-09-15

---

## Overview

Today file scanning is a single on/off toggle on `them.applications.security_config` — it applies to all agents in the app or none. Users cannot enable scanning for one specific agent while leaving others unguarded.

This plan moves file scanning to **per-agent-in-app** granularity by wiring the existing (unused) `middleware_wirings` table into `FileGate`. A "File Guard" node on the canvas can be dropped onto any A2A agent node. Only that agent's file artifacts are scanned using that guard's config.

The `middleware_defs` + `middleware_wirings` schema is already in place. The `FileGate` pipeline is fully implemented. This is primarily a wiring + frontend task.

---

## Steps

| # | Area | What |
|---|---|---|
| 1 | Schema | Seed builtin `file_guard` row in `middleware_defs` |
| 2 | Go — gate | `FileGate` reads wiring first, falls back to `applications.security_config` |
| 3 | Go — API | CRUD endpoints for `middleware_wirings` |
| 4 | Frontend | File Guard canvas node + properties panel |
| 5 | Frontend | Run history — surface quarantine events inline |
| 6 | Frontend | Security aggregate view per agent |

---

## Step 1 — Schema Migration

### New migration file: `db/094_file_guard_seed.sql`

```sql
-- Seed the builtin file-guard middleware definition.
-- middleware_wirings rows reference this def_id.
INSERT INTO them.middleware_defs (
    slug, kind, display_name, description, config, is_builtin, enabled
) VALUES (
    'file-guard',
    'guard',
    'File Guard',
    'Intercepts file artifacts produced by A2A agents, quarantines them, and runs the configured processor pipeline before delivery.',
    '{
        "enabled": false,
        "processors": {
            "av_scan": {
                "enabled": true,
                "max_bytes": 5242880,
                "block_on_infected": true
            },
            "audit_capture": {
                "enabled": true
            }
        },
        "mode": "block",
        "max_file_size_mb": 5,
        "allowed_types": [],
        "blocked_types": ["exe", "sh", "bat", "ps1", "cmd"],
        "notify_on_fail": true
    }'::jsonb,
    true,
    true
)
ON CONFLICT (slug) DO NOTHING;
```

**No new columns needed.** `middleware_wirings` already has:
- `application_id` + `agent_id` — scope
- `def_id` → points to `file-guard` row
- `config_override JSONB` — per-agent overrides merged over def defaults
- `enabled BOOLEAN` — per-wiring on/off
- `node_id TEXT` — canvas node ID (already there)

Add this migration to `docs/CURRENT.md` migration list.

---

## Step 2 — Gate Logic Change

### Files changed: `go/internal/middleware/gate.go`, `go/internal/middleware/config.go`

#### New `GateQuerier` interface method

Add to the `GateQuerier` interface in `gate.go`:

```go
// LoadWiringConfig returns the merged SecurityConfig for (appID, agentID).
// Returns (cfg, true, nil) if a wiring exists; (zero, false, nil) if not found.
LoadWiringConfig(ctx context.Context, appID, agentID string) (SecurityConfig, bool, error)
```

#### New DB query implementation (in `gate.go` or a new `gate_dal.go`)

```go
func (g *FileGate) loadWiringCfg(ctx context.Context, appID, agentID string) (SecurityConfig, bool, error) {
    const q = `
        SELECT
            COALESCE(d.config, '{}') || COALESCE(w.config_override, '{}') AS effective_cfg,
            w.enabled
        FROM them.middleware_wirings w
        JOIN them.middleware_defs d ON d.id = w.def_id
        WHERE w.application_id = $1::uuid
          AND w.agent_id       = $2::uuid
          AND d.slug           = 'file-guard'
          AND d.enabled        = true
        LIMIT 1`
    // scan into SecurityConfig + enabled bool
    // if no rows → return zero, false, nil
}
```

#### `loadSecCfg` change — wiring-first with fallback

Replace the current `loadSecCfg` (which reads only `applications.security_config`) with:

```go
func (g *FileGate) loadSecCfg(ctx context.Context, appID, agentID string) (SecurityConfig, error) {
    // 1. Check per-agent wiring (new path)
    if agentID != "" {
        if cfg, ok, err := g.loadWiringCfg(ctx, appID, agentID); err != nil {
            return SecurityConfig{}, err
        } else if ok {
            return cfg, nil  // wiring found — use it, ignore app-level config
        }
    }
    // 2. Fallback: app-level security_config (backwards compat)
    return g.loadAppSecCfg(ctx, appID)
}
```

#### `GateInput` — add `AgentID`

```go
type GateInput struct {
    ApplicationID string
    AgentID       string  // NEW — empty string triggers app-level fallback
    RunID         string
    SessionID     string
    TenantID      string
    FileURL       string
    Filename      string
    ContentType   string
    SizeHint      int64
}
```

Update all callers in `go/internal/a2a/executor.go` to pass `EPConfig.AgentID` (or equivalent).

#### Redis cache invalidation — extend key

Current key: `them:security_config:invalidated:{appID}`
Add per-wiring key: `them:middleware_wiring:invalidated:{appID}:{agentID}`

Update `go/cmd/them/main.go` subscription to also subscribe `them:middleware_wiring:invalidated:*` and call `gate.InvalidateCache(appID)` (or a new `gate.InvalidateWiringCache(appID, agentID)`).

---

## Step 3 — Admin API (middleware_wirings CRUD)

### New file: `go/internal/admin/middleware_wirings.go`

#### Routes (register in the admin router)

```
GET    /api/v1/admin/applications/{appID}/middleware-wirings
POST   /api/v1/admin/applications/{appID}/middleware-wirings
PUT    /api/v1/admin/applications/{appID}/middleware-wirings/{wiringID}
DELETE /api/v1/admin/applications/{appID}/middleware-wirings/{wiringID}
```

#### Request/Response types

```go
type MiddlewareWiringRequest struct {
    AgentID        string          `json:"agent_id"`
    DefSlug        string          `json:"def_slug"`        // "file-guard"
    Position       int             `json:"position"`
    ConfigOverride json.RawMessage `json:"config_override"` // partial SecurityConfig
    Enabled        bool            `json:"enabled"`
    NodeID         string          `json:"node_id"`         // canvas node ID
}

type MiddlewareWiringResponse struct {
    ID             string          `json:"id"`
    ApplicationID  string          `json:"application_id"`
    AgentID        string          `json:"agent_id"`
    DefSlug        string          `json:"def_slug"`
    Position       int             `json:"position"`
    ConfigOverride json.RawMessage `json:"config_override"`
    Enabled        bool            `json:"enabled"`
    NodeID         string          `json:"node_id"`
    CreatedAt      time.Time       `json:"created_at"`
    UpdatedAt      time.Time       `json:"updated_at"`
}
```

After any write, publish Redis invalidation:
```go
redis.Publish("them:middleware_wiring:invalidated:"+appID+":"+agentID, "1")
```

### New file: `go/internal/admin/dal/middleware_wirings.go`

Standard DAL — insert, list, update, delete by ID. All queries scoped to `application_id`.

---

## Step 4 — Canvas Frontend

### New node type: `FileGuardNode`

**File:** `frontend/src/components/canvas/nodes/FileGuardNode.tsx`

- Rendered as a shield icon node
- Only valid drop target: an existing **A2A agent node** edge
- When dropped: POST to `/api/v1/admin/applications/{appID}/middleware-wirings` with `def_slug: "file-guard"`, `agent_id: <target agent id>`, `node_id: <reactflow node id>`
- Visual state: green (enabled + clean) / yellow (warn hits) / red (blocked files)

**File:** `frontend/src/components/canvas/panels/FileGuardPanel.tsx`

Properties panel fields:

| Field | Type | Default |
|---|---|---|
| `enabled` | Toggle | off |
| `mode` | Select: `warn` / `block` | block |
| `max_file_size_mb` | Number input | 5 |
| `allowed_types` | Tag input (comma-sep) | empty = all |
| `blocked_types` | Tag input | exe, sh, bat, ps1, cmd |
| `notify_on_fail` | Toggle | on |

On change: PATCH `config_override` via PUT wiring endpoint + publish Redis invalidation.

**Canvas node registration:** add `FileGuardNode` to the node type map in the canvas root component.

**Drag constraint:** enforce in the canvas drop handler — `FileGuardNode` can only be connected to nodes where `data.agentCard` is present (A2A agents). Show tooltip "File Guard requires an A2A agent" otherwise.

---

## Step 5 — Run History Surfacing

### Backend

New endpoint (or extend existing run detail):
```
GET /api/v1/admin/runs/{runID}/guard-events
```

Query:
```sql
SELECT id, filename, content_type, size, storage_key, created_at,
       -- status from middleware_jobs
       mj.status, mj.result
FROM them.quarantine_artifacts qa
LEFT JOIN them.middleware_jobs mj ON mj.quarantine_artifact_id = qa.id
WHERE qa.run_id = $1
ORDER BY qa.created_at
```

Returns list of guard events: `{ filename, size, content_type, status: "clean"|"infected"|"pending", scanned_at }`.

### Frontend

In the run detail / run history view, add a "Guard Events" section that lists quarantine results inline. Show:
- File name + size
- Status badge: `clean` (green) / `infected` / `blocked` (red) / `pending` (grey)
- Which agent produced the file (from `agent_id` on quarantine row — may need adding, see note below)

> **Note:** `quarantine_artifacts` currently has `application_id` but not `agent_id`. Add `agent_id UUID REFERENCES them.agents(id)` in the migration, populated from `GateInput.AgentID`.

---

## Step 6 — Security Aggregate View

New endpoint:
```
GET /api/v1/admin/applications/{appID}/guard-summary
```

Query per agent with a wiring:
```sql
SELECT
    a.id, a.name, a.slug,
    COUNT(qa.id)                                          AS total_files,
    COUNT(qa.id) FILTER (WHERE mj.status = 'clean')      AS clean,
    COUNT(qa.id) FILTER (WHERE mj.status = 'infected')   AS infected,
    MAX(qa.created_at)                                    AS last_event
FROM them.middleware_wirings mw
JOIN them.agents a ON a.id = mw.agent_id
LEFT JOIN them.quarantine_artifacts qa ON qa.agent_id = a.id AND qa.application_id = mw.application_id
LEFT JOIN them.middleware_jobs mj ON mj.quarantine_artifact_id = qa.id
WHERE mw.application_id = $1
GROUP BY a.id, a.name, a.slug
```

Frontend: add a "Guard Health" card to the application security tab showing a per-agent table of scanned / clean / blocked counts.

---

## Backwards Compatibility

| Scenario | Behaviour |
|---|---|
| App has `security_config.enabled=true`, no wirings | Unchanged — gate falls back to app-level config, all agents scanned as before |
| App has wirings for agent X only | Agent X uses wiring config; agents Y, Z fall back to app-level config |
| App has `security_config.enabled=false`, wiring for agent X with `enabled=true` | Agent X is scanned; Y, Z are not |
| Wiring `enabled=false` | Gate skips scanning for that agent even if app-level is on |

The Runtime toggle in the app card remains as the app-level default. Canvas guard nodes are per-agent overrides that take precedence.

---

## Testing Requirements

### Go tests

| File | Tests to add |
|---|---|
| `go/internal/middleware/gate_test.go` | `TestLoadSecCfg_WiringTakesPrecedence`, `TestLoadSecCfg_FallsBackToApp`, `TestLoadSecCfg_WiringDisabled` |
| `go/internal/admin/middleware_wirings_test.go` (new) | CRUD round-trip, config merge, Redis invalidation published |
| `go/internal/middleware/job_test.go` | Verify `agent_id` stored in `quarantine_artifacts` |

Run after changes: `cd go && go test ./internal/middleware/... ./internal/admin/...`

### Frontend tests

- `FileGuardNode` renders correctly with all prop states
- Properties panel calls PUT on change
- Drag constraint rejects non-A2A nodes

### Manual smoke

1. Enable file guard on agent X in canvas
2. Run a session through agent X that returns a file
3. Confirm quarantine row created with correct `agent_id`
4. Confirm run history shows guard event
5. Run through agent Y (no wiring) — confirm no quarantine if app-level is off

---

## Files Changed Summary

| File | Change |
|---|---|
| `db/094_file_guard_seed.sql` | New — seed `middleware_defs` row |
| `db/001_schema.sql` | Add `agent_id` column to `quarantine_artifacts` |
| `go/internal/middleware/gate.go` | `loadSecCfg` wiring-first logic, `GateInput.AgentID`, new DB query |
| `go/internal/middleware/config.go` | No struct changes needed |
| `go/internal/middleware/gate_test.go` | New wiring precedence tests |
| `go/internal/admin/middleware_wirings.go` | New — HTTP handlers |
| `go/internal/admin/dal/middleware_wirings.go` | New — DAL |
| `go/internal/admin/middleware_wirings_test.go` | New — CRUD tests |
| `go/internal/a2a/executor.go` | Pass `AgentID` in `GateInput` |
| `go/cmd/them/main.go` | Subscribe to per-wiring Redis invalidation channel |
| `frontend/src/components/canvas/nodes/FileGuardNode.tsx` | New — canvas node |
| `frontend/src/components/canvas/panels/FileGuardPanel.tsx` | New — properties panel |
| `frontend/src/components/canvas/` (root) | Register `FileGuardNode` type |
| `docs/CURRENT.md` | Add migration 094, update next task |
| `docs/SCHEMA.md` | Document `middleware_defs`, `middleware_wirings`, `quarantine_artifacts.agent_id` |
| `go/TEST_INDEX.md` | Add new test entries |
