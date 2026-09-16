# Handover — Application Canvas Upgrade
# Created: 2026-09-16
# Use this doc when starting a fresh Claude session to continue this work.

---

## First prompt for new session

```
Read docs/HANDOVER_APP_CANVAS.md and docs/APP_CANVAS_UPGRADE_PLAN.md, then implement Phase 1: Middleware node registry. The full plan is in APP_CANVAS_UPGRADE_PLAN.md. Start with the DB migration, then extend the Go DAL, then update the frontend. No scope creep — Phase 1 only.
```

---

## Strategic framing (read this first)

The-M is an AI governance and control platform for organizations. It manages agents and exposes them with governance, security, RBAC, and observability.

Two distinct canvases:
- **Agent builder** — defines *what* an agent does (internal DAG of steps)
- **Application canvas** — defines *how* agents are governed and exposed (entry points, middleware, routing, runtime config)

These stay separate. The application canvas is the governance shell — not a replacement for the agent builder.

---

## Current HEAD and state

Branch: `main`
HEAD (as of 2026-09-16): See `docs/CURRENT.md` for exact commit hash.

Recent work completed:
- Step 38 — File Guard (all 6 steps): per-agent file scanning via canvas wiring, guard events in run history, per-app health card in RuntimeView
- Auth service (Python) deleted from repo — Go auth service (`them-auth-go`) is sole auth
- Canvas bug fixes: middleware edges now persisted in `canvasToDoc`, emoji font fix applied, runtime file scan toggle removed

Active stack: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`

---

## The 4-phase plan (all phases pending)

Full details in `docs/APP_CANVAS_UPGRADE_PLAN.md`.

| Phase | What | Effort | Status |
|---|---|---|---|
| **1** | Middleware node registry — emoji/color/bg from DB, not hardcoded | S | **START HERE** |
| 2 | Router + HIL nodes in app canvas (topology only) | M | Pending |
| 3 | App canvas DAG execution — local loop + Temporal | L | Pending |
| 4 | Full node unification (optional/future) | L | Future |

---

## Phase 1 — what to do (concise)

**Goal:** Middleware visual metadata (emoji, color, bg_color) comes from the DB, not hardcoded frontend code. Same pattern as agent builder's node registry.

### Step 1 — DB migration
File: `db/097_middleware_defs_visual.sql`

```sql
ALTER TABLE them.middleware_defs
  ADD COLUMN IF NOT EXISTS emoji     TEXT,
  ADD COLUMN IF NOT EXISTS color     TEXT,
  ADD COLUMN IF NOT EXISTS bg_color  TEXT;

UPDATE them.middleware_defs
SET emoji = '🛡️', color = '#f59e0b', bg_color = 'rgba(245,158,11,0.08)'
WHERE slug = 'file-guard';
```

Apply: `docker cp db/097_middleware_defs_visual.sql them-postgres:/tmp/ && docker exec them-postgres psql -U them -d them -f /tmp/097_middleware_defs_visual.sql`

### Step 2 — Go DAL
File: `go/internal/admin/dal/` — wherever `ListMiddlewareDefs` is (check `middleware_wirings.go` or `applications.go`).
- Add `Emoji`, `Color`, `BgColor` fields to the `MiddlewareDef` struct
- Extend the SELECT query to include these columns

### Step 3 — Frontend
Files to change:
- `frontend/src/lib/apiTypes.ts`: Add `emoji?: string; color?: string; bg_color?: string` to `MiddlewareDef` interface
- `frontend/src/app/admin/applications/types.ts`: Add same fields to `MiddlewareData` and `MwNodeData`
- `frontend/src/app/admin/applications/components/CanvasHelpers.ts` (`docToCanvas`): Pass `emoji`, `color`, `bg_color` from the component def through to node data on canvas load
- `frontend/src/app/admin/applications/components/NodeLibrary.tsx`: Replace `const emoji = m.kind === 'guard' ? '🛡️' : '⚡'` with `m.emoji ?? (m.kind === 'guard' ? '🛡️' : '⚡')` (fallback for safety)
- `frontend/src/app/admin/applications/components/CanvasNodes.tsx` (`MiddlewareNode`): Read `data.emoji`, `data.color`, `data.bg_color` from node data instead of deriving from `data.kind`; render emoji in plain `<div style={{ fontSize: 26, lineHeight: 1 }}>` (no material-symbols, no forced font-family — same as `StepNode` line 411)

### Verification
- File Guard node in library shows 🛡️ from DB
- File Guard node on canvas shows 🛡️ after drag-drop
- Adding a second middleware def with custom emoji requires only a DB UPDATE, no frontend change

---

## Key infrastructure to reuse

| What | Where | Notes |
|---|---|---|
| Node registry pattern | `go/internal/agentgen/nodes.go` + `frontend/src/lib/nodeRegistry.ts` | Model Phase 1 on this — emoji/color/label per type, served from backend |
| StepNode emoji rendering | `frontend/src/app/admin/agents/builder/components/StepNode.tsx` line 411 | `<div style={{ fontSize: '26px', lineHeight: 1 }}>{meta.emoji}</div>` — copy this exactly |
| ExecutionBackend concept | `go/internal/agentgen/compiler.go` lines 64–67 + `spec.go` lines 20–24 | Phase 3: app canvas gains same `execution_backend: "local" | "temporal"` |
| Temporal workflow | `go/internal/temporal/workflow.go` | Phase 3: extend `CanvasAgentWorkflow` for app-level agent invocations |
| dag-worker | `go/cmd/dag-worker/` | Phase 3: register new `ExecuteAgent` activity here |

---

## Rules to follow

- Read `CLAUDE.md` and `go/CLAUDE.md` at session start
- Every Go change needs a test; run `docker run --rm -v /opt/docker/them/go:/src -w /src golang:1.25-alpine go test ./...` before committing
- `TEST_INDEX.md` updated in same commit as tests
- Never commit `.env` or `secrets.local`
- One phase at a time — do not start Phase 2 in the same session as Phase 1
- After Phase 1 is complete and tested, update `docs/CURRENT.md` with HEAD and next recommended task
