# Agentic Application Studio — Canvas Builder Progress

**Feature:** Visual canvas for composing agentic applications (drag-and-drop orchestrators, agents, entry points)  
**Started:** 2026-07-11  
**Design reference:** `docs/architecture/screen.png` + `docs/architecture/code.html` + `docs/architecture/DESIGN.md`  
**Main file:** `frontend/src/app/admin/applications/page.tsx`

---

## Status Overview

| Phase | Description | Status |
|---|---|---|
| 1 | Canvas foundation — single-level graph, drag-drop, save | ✅ Complete |
| 2 | Sub-orchestrator composition — hierarchical graphs | ⏳ Pending |
| 3 | Live execution — monitor tab, real-time node highlighting | ⏳ Pending |

---

## Phase 1 — Canvas Foundation ✅

**Completed:** 2026-07-11  
**Commits:** `b985fa7`, `de7570c`, `b004eac`

### What was built

**Stack:**
- `@xyflow/react` v12.11.2 — added to `frontend/package.json`
- React Flow canvas with three custom node types
- Full-screen builder view (takes over the page, back arrow returns to list)

**Node types** (all with inline SVG icons, hover scale animation, glow shadow):
- `EntryPointNode` — cyan, lightning bolt icon, source handle at bottom only
- `OrchestratorNode` — purple, crown icon, target + source handles (top + bottom)
- `AgentNode` — green, robot face icon, target handle at top only

**Node Library panel** (280px, left side):
- Three collapsible sections: Entry Points, Orchestrators, Agents
- All items draggable onto canvas via `dataTransfer`
- Populates from live DB data (orchestrators + agents fetched on page load)

**Properties Panel** (320px, right side):
- Context-sensitive: shows fields for whichever node is selected
- EntryPoint: editable name, type (WS/SSE), access policy, slug + live URL preview
- Orchestrator: read-only info + link to Orchestrators admin page
- Agent: read-only info + link to Agents admin page
- Tabs: Properties / Configuration

**Canvas toolbar** (floating, centered top):
- Zoom out SVG button → `zoomOut()`
- Range slider (10–200, step 10) → `setViewport({ zoom: v/100 })`
- Live zoom % display (polls `getZoom()` every 250ms)
- Zoom in SVG button → `zoomIn()`
- Fit-to-screen SVG button → `fitView({ padding: 0.15 })`

**Animated edges:** cyan dashed, `strokeDasharray: '5,3'`, animated

**Save logic** (`handleSave`):
1. Find EntryPoint node → get slug, epType, accessMode
2. Find Orchestrator connected to EntryPoint via edges
3. Find all Agent nodes connected to Orchestrator via edges → collect agentIds
4. `updateOrchestrator(orchId, { allowed_agent_ids: agentIds })`
5. `createApplication` or `updateApplication` with name/slug/entry_point_type/orchestrator_id/access_policy

**List view** (card grid):
- `display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr))`
- Each card: colored top border (cyan=enabled, muted=disabled), icon + name + badges + orchestrator info + action strip
- URLs button → modal overlay (not inline expand)
- Action buttons: URLs, Enable/Disable (toggle icon), Builder (edit icon), Delete

**Bug fixed:** `Sidebar` uses `position: fixed` — BuilderView was hidden behind it.  
Fix: `marginLeft: 260` on the builder wrapper div in `ApplicationsPage`.

**CSS animations:**
- `@keyframes handlePulse` — selected node handles pulse with cyan glow
- `font-family: inherit` injected into `.react-flow__node *` to fix React Flow CSS reset overriding inline styles

### Layout structure

```
[Sidebar 260px fixed] | [marginLeft:260 wrapper]
                            [BuilderView — flex column, 100vh]
                              [Top bar 56px — back / app name / Save / Deploy]
                              [Builder area — flex row, flex:1]
                                [NodeLibrary 280px]
                                [Canvas — flex:1, ReactFlow]
                                [PropertiesPanel 320px]
                              [Status bar 28px — node/edge counts]
```

### Design tokens (from Stitch design system)

```typescript
bg: '#051424'          // deep navy canvas
cyan: '#00f0ff'        // entry points, primary actions
purple: '#d0bcff'      // orchestrators
green: '#4ade80'       // agents, enabled status
text: '#d4e4fa'        // primary text
textMuted: '#b9cacb'   // secondary text
glass: backdrop-blur(12px) panels with rgba(15,23,42,0.7) background
```

### Relevant files

| File | Purpose |
|---|---|
| `frontend/src/app/admin/applications/page.tsx` | Entire canvas — nodes, library, properties, list view, save logic |
| `frontend/package.json` | `@xyflow/react: ^12.11.2` dependency |
| `app/routers/admin_applications.py` | Backend CRUD for applications |
| `app/routers/apps.py` | Entry point routing (`/apps/{slug}/ws`, `/apps/{slug}/sse`) |
| `docs/architecture/screen.png` | Stitch design reference screenshot |
| `docs/architecture/code.html` | Stitch component HTML reference |
| `docs/architecture/DESIGN.md` | Stitch design system spec (colors, typography, spacing) |

### How to rebuild after changes

```bash
# Frontend runs in Docker — edit page.tsx then:
docker compose -f docker-compose.yml -f docker-compose.local.yml build them-frontend
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d them-frontend

# Verify healthy
docker ps --filter name=them-frontend --format "{{.Status}}"
# Should show: Up X seconds (healthy)

# App at: http://localhost:8088/admin/applications
```

---

## Phase 2 — Sub-orchestrator Composition ⏳

### Goal

Allow orchestrators to connect to other orchestrators (not just agents), creating hierarchical multi-agent pipelines like:

```
Entry Point (WebSocket)
        ↓
  Main Orchestrator
     ↓         ↓
Research     Payments
Orchestrator  Orchestrator
  ↓    ↓       ↓      ↓
Ag-A  Ag-B   Ag-C   Ag-D
```

### What needs to change

**Frontend (`page.tsx`):**
- `OrchestratorNode` already has both target + source handles — the canvas can already draw these connections
- `handleSave()` needs to traverse the full graph recursively instead of assuming one flat level
- Need to detect when an orchestrator's source connects to another orchestrator vs an agent
- The `allowed_agent_ids` update needs to walk all orchestrator nodes in the graph

**Backend:**
- `app/routers/admin_orchestrators.py` — `allowed_agent_ids` currently only allows agent IDs; needs to support sub-orchestrator IDs too (or a separate `allowed_orchestrator_ids` field)
- `app/services/task_runner.py` — the agentic loop builds tools from `agent_registry`; sub-orchestrators would need to be exposed as tools too (they already can via A2A `a2a_exposed` flag)
- `db/001_schema.sql` — may need a graph/topology storage table if we want to persist the full visual layout

**Save logic rewrite (pseudocode):**
```
function saveGraph(nodes, edges):
  ep = find entryPoint node
  traverse from ep via edges:
    for each orchestrator found:
      children = edges from this orchestrator
      agentChildren = children where target.type === 'agent'
      orchChildren = children where target.type === 'orchestrator'
      updateOrchestrator(orch.id, { allowed_agent_ids: agentChildren.map(id) })
      recurse into orchChildren
  createOrUpdateApplication(ep data + root orchestrator id)
```

### Considerations
- Circular connection guard (prevent orch→orch loops)
- The backend task_runner needs to know the sub-orchestrator topology at runtime — currently it only knows one orchestrator per run
- Possibly store the canvas JSON (nodes + edges positions) in `them.applications` as a `canvas_layout JSONB` column for round-trip fidelity

---

## Phase 3 — Live Execution & Monitor Tab ⏳

### Goal

From the reference design (`screen.png`), the builder has three tabs: **Design / Monitor / Settings**.

**Monitor tab** shows the canvas with live execution overlaid:
- Nodes highlight as the run flows through them (cyan pulse on active node)
- Edges animate in the direction of data flow
- Bottom panel: Execution Log (timestamped events), Issues, Comments
- Stats bar: Nodes, Connections, Entry Points, Agents counts
- "Test Run" button that triggers a real run through the application's entry point

### What needs to change

**Frontend:**
- Tab switcher in the top bar (Design / Monitor / Settings)
- Monitor view: same canvas but read-only, with a WebSocket connection to `ws://localhost:8088/ws/dashboard`
- Subscribe to `them:dash:run:{run_id}` channel events; on each `tool_start` event highlight the matching node
- Node highlight: add a `highlighted` prop to node data, update it reactively during the run
- Execution log panel below the canvas (collapsible, 200px)

**Backend:**
- The `run_id` needs to be tied to an `application.slug` so Monitor knows which run to subscribe to
- `POST /apps/{slug}` (REST entry point) or `WS /apps/{slug}/ws` — already exists in `app/routers/apps.py`
- Dashboard WS already broadcasts per-run events — Monitor just needs to subscribe

### Relevant existing code
- `app/routers/ws_dashboard.py` — broadcasts `them:dash:run:{run_id}` events
- `app/routers/apps.py` — existing entry point handlers
- `frontend/src/app/admin/playground/page.tsx` — reference for how the frontend subscribes to dashboard WS and renders streaming events

---

## Design Reference Notes

From `screen.png` (Stitch reference design):

- **Node Library** is a named left panel, not just a sidebar — has a search box at top, sections: Entry Points / Orchestrators / Agents / Utilities
- Each library item has: colored icon box + name + subtitle + drag handle (⋮⋮)
- **Canvas** has a mini toolbar row at top: Select / Pan / Connect / Comment / Note mode buttons
- **Properties panel** (right) has a "Node" header with icon + type label, then General / Execution / Observability sections
- The node cards on canvas have a type label below the name (e.g. "ENTRY POINT", "ORCHESTRATOR", "SUB-ORCHESTRATOR")
- Connected nodes use a smooth curved edge with dot handles at connection points
- **Bottom strip** below canvas: Execution Log | Issues (n) | Comments (n) tabs + Workflow Summary stats on right
