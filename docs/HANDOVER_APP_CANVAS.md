# Handover — Application Canvas Upgrade
# Created: 2026-09-16
# Last updated: 2026-09-16 (Phase 1 complete)
# Use this doc when starting a fresh Claude session to continue this work.

---

## First prompt for new session

```
Read docs/HANDOVER_APP_CANVAS.md and docs/APP_CANVAS_UPGRADE_PLAN.md, then implement Phase 2: Router + HIL nodes in the application canvas (topology only). The full plan is in APP_CANVAS_UPGRADE_PLAN.md. No scope creep — Phase 2 only.
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
HEAD: `c3d5444d  docs(current): update CURRENT.md for Phase 1 canvas upgrade completion`

Recent work completed (this feature):
- **Phase 1 complete** (commit `61a3d915`) — middleware node registry: emoji/color/bg_color from DB
- Step 38 — File Guard (all 6 steps): per-agent file scanning via canvas wiring, guard events in run history, per-app health card in RuntimeView
- Auth service (Python) deleted from repo — Go auth service (`them-auth-go`) is sole auth

Active stack: `docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d`

---

## The 4-phase plan

Full details in `docs/APP_CANVAS_UPGRADE_PLAN.md`.

| Phase | What | Effort | Status |
|---|---|---|---|
| 1 | Middleware node registry — emoji/color/bg from DB, not hardcoded | S | ✅ **DONE** (commit `61a3d915`) |
| **2** | Router + HIL nodes in app canvas (topology only) | M | **START HERE** |
| 3 | App canvas DAG execution — local loop + Temporal | L | Pending |
| 4 | Full node unification (optional/future) | L | Future |

---

## Phase 1 — COMPLETE

Everything shipped in commit `61a3d915`. Summary of what was built:

- `db/097_middleware_defs_visual.sql` — `emoji`/`color`/`bg_color` columns added to `middleware_defs`; File Guard seeded. **Applied to DB.**
- `go/internal/admin/dal/middleware_wirings.go` — `MiddlewareDefSummary` extended; `ListMiddlewareDefs` SELECT updated.
- `go/internal/admin/middleware_wirings.go` — new `ListDefs` handler.
- `go/internal/admin/router.go` — `GET /admin/middleware-defs` registered.
- Frontend: `MiddlewareDef`, `MiddlewareData`, `MwNodeData` all have `emoji?`/`color?`/`bg_color?`.
- `CanvasBuilderView.tsx` — fetches `middlewareDefs`, builds `mwVisualById` map, applies on drop + load.
- `CanvasHelpers.ts` (`docToCanvas`) — new optional `mwVisualById` param.
- `CanvasNodes.tsx` (`MiddlewareNode`) — reads emoji/color/bg_color from node data; plain `<div>` render.
- `NodeLibrary.tsx` — `m.emoji ?? fallback`; passes visual fields on drag.
- Test S1-111 added (`TestMiddlewareWirings_ListDefs`). Full suite: 1164 tests, 0 failures.

---

## Phase 2 — what to do (concise)

**Goal:** Router and HIL nodes appear as draggable items in the application canvas node library, can be placed on the canvas, and persist through save/reload. **No execution changes** — topology only.

### Step 1 — Frontend types

File: `frontend/src/app/admin/applications/types.ts`

Add a new interface:
```ts
export interface FlowControlNodeData {
  _kind: 'flow_control';
  instance_id: string;
  node_type: 'router' | 'hil';
  display_name: string;
  config: Record<string, unknown>;
  _error?: boolean;
  _shake?: boolean;
  _errorMsg?: string;
}
```

Update `CanvasNodeData` union to include `FlowControlNodeData`.

### Step 2 — New canvas node component

File: `frontend/src/app/admin/applications/components/CanvasNodes.tsx`

Add `FlowControlNode` component. Reuse `StepNode` visual pattern — round node, emoji from `node_type`:
- `router` → `🔀`, color `#06b6d4` (cyan)
- `hil` → `✋`, color `#a855f7` (purple)

Register in `NODE_TYPES` map: `{ ..., flowControl: FlowControlNode }`.

### Step 3 — Node library

File: `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx` (the active inline palette)

Add a "Flow Control" section below the Middleware section. Two static entries (no DB needed — these are always available):
- **Router** — `🔀` — "Routes to one of multiple agents based on message intent"
- **HIL** — `✋` — "Pauses flow for human decision before continuing"

Drag data: `{ nodeType: 'flow_control', nodeData: { node_type: 'router' | 'hil' } }`

### Step 4 — Drop handler

File: `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx` (`handleDropOnCanvas`)

Add a `flow_control` branch:
```ts
} else if (nodeType === 'flow_control' && payload.node_type) {
  const id = genInstanceId('flow_control', payload.node_type, existingIds);
  const newNode: Node = { id, type: 'flowControl', position: pos, data: {
    _kind: 'flow_control', instance_id: id,
    node_type: payload.node_type, display_name: payload.node_type === 'router' ? 'Router' : 'Human-in-Loop',
    config: {},
  } as unknown as Record<string, unknown> };
  setNodes(ns => [...ns, newNode]);
}
```

Also update `genInstanceId` in `CanvasHelpers.ts` to handle `'flow_control'` kind → base `'fc_' + sanitize(defName)`.

### Step 5 — canvasToDoc / docToCanvas

File: `frontend/src/app/admin/applications/components/CanvasHelpers.ts`

`canvasToDoc`: serialize `flow_control` nodes into `doc.components` with `definition_ref.kind = 'flow_control'` and `config.node_type`.

`docToCanvas`: restore `flow_control` nodes from `definition_ref.kind === 'flow_control'`, reading `config.node_type`.

### Step 6 — Canvas validation (optional but good)

In `CanvasInner.tsx` or the existing `validateConnection` helper: warn if a Router node has no outgoing edges.

### Verification
- Router and HIL entries appear in the node library under "Flow Control"
- Drag-drop places a node with correct emoji
- Save → reload restores the node at the same position
- No backend changes needed

---

## Key infrastructure to reuse

| What | Where | Notes |
|---|---|---|
| Phase 1 pattern | `CanvasNodes.tsx` `MiddlewareNode` | Same round-node structure — copy for `FlowControlNode` |
| `genInstanceId` | `CanvasHelpers.ts` line ~102 | Add `'flow_control'` case |
| `canvasToDoc` / `docToCanvas` | `CanvasHelpers.ts` | Both need a `flow_control` branch |
| `NODE_TYPES` map | `CanvasNodes.tsx` bottom | Add `flowControl: FlowControlNode` |
| Agent builder node types | `GET /admin/node-types` | Phase 2 does NOT need this — flow control nodes are static in the app canvas |
| ExecutionBackend concept | `go/internal/agentgen/compiler.go` | Phase 3 only |
| Temporal workflow | `go/internal/temporal/workflow.go` | Phase 3 only |

---

## Rules to follow

- Read `CLAUDE.md` and `go/CLAUDE.md` at session start
- Phase 2 is **frontend-only** — no Go changes needed, no DB migration
- If any Go file is touched, run `docker run --rm -v /opt/docker/them/go:/src -w /src golang:1.25-alpine go test ./...` before committing
- `TEST_INDEX.md` updated in same commit as any Go tests
- Never commit `.env` or `secrets.local`
- One phase at a time — do not start Phase 3 in the same session as Phase 2
- After Phase 2 is complete and tested, update `docs/CURRENT.md` and this doc
- TypeScript must compile clean (`npx tsc --noEmit`) before committing frontend changes
- When context tokens drop below ~2M, stop, commit current state, update `docs/CURRENT.md` and this doc, and hand over
