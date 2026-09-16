# Application Canvas Evolution
# Proposal: Unify with Agent Builder node/registry model
# Last updated: 2026-09-16

---

## Current State

### Agent Builder Canvas

The agent builder (`/admin/agents/builder/`) is built around a **server-driven node registry**:

- `GET /admin/node-types` returns `NodeTypeInfo[]` — each entry has `type`, `emoji`, `label`, `description`, `color`, `bg_color`, `edges` (connection rules), `input_ports`, `output_ports`, `control_output_ports`, `executable`, `default_policy`, etc.
- The frontend registry (`src/lib/nodeRegistry.ts`) holds only UI-only supplements (summary rendering). All structural/visual metadata comes from the backend.
- A single generic `StepNode` component reads `meta.emoji`, `meta.bg`, `meta.border` from the registry and renders any node type. No hardcoded per-type components.
- The node library panel reads `getNodeDef(type)` to get colors, emoji, and description for each draggable item.
- Result: adding a new node type means adding a backend registry entry. The frontend requires zero changes.

Current step types: `input`, `response`, `llm`, `transform`, `http`, `branch`, `loop`, `parallel`, `a2a_call`, `human_wait`, `stream_out`, plus Skills.

### Application Canvas

The application canvas (`/admin/applications/`) is built around **hardcoded node types**:

- 4 fixed React components: `EntryPointNode`, `OrchestratorNode`, `AgentNode`, `MiddlewareNode` — each with its own colors, icons, and shape logic baked in.
- `MiddlewareData` has a `kind: 'guard' | 'cache'` enum — extending to new middleware kinds requires editing TypeScript source.
- Visual metadata (emoji for shield/bolt) is hardcoded in component JSX. No registry lookup.
- Node library (`NodeLibrary.tsx`) renders each section with hand-written logic per type.
- Execution model: **linear agentic loop only** — one orchestrator → one agent → LLM turns. The canvas topology defines routing/wiring, not execution order.
- DAG execution: not supported. Middleware edges weren't even serialized to the definition doc until a recent fix.

---

## The Three Gaps to Close

### Gap 1 — Visual/registry model
Middleware (and future app-level nodes like Router, HIL, Cache) have hardcoded visuals. Every new node type requires editing `CanvasNodes.tsx`, `NodeLibrary.tsx`, `types.ts`, and `CanvasHelpers.ts` in sync.

**Target**: Application canvas reads from the same `GET /admin/node-types` registry (or a parallel `GET /admin/app-node-types`) to drive node rendering, library listing, and drag metadata. `MiddlewareNode` becomes a generic `AppStepNode` reading emoji/color from the registry, exactly like `StepNode` does.

### Gap 2 — DAG execution in the application layer
Today the orchestrator runs a **single-agent linear loop**. The canvas can express a multi-agent topology but the runtime ignores inter-agent edges.

**Target**: App definitions compile to a Temporal workflow graph where:
- Each agent node becomes a Temporal activity
- Orchestrator node becomes the workflow root / LLM router deciding which activity to invoke next
- Middleware nodes become interceptor activities in the chain
- Edges define sequencing/branching

This also enables **local vs Temporal** execution mode per-app: local = in-process sequential execution of activities (fast, no Temporal overhead, suitable for simple 1-2 agent flows); Temporal = durable, retryable, HITL-capable, for complex multi-agent DAGs.

### Gap 3 — Reuse of agent builder nodes at application level
Router, HIL (`human_wait`), `branch`, `parallel` all make sense at the application canvas level, not just inside an agent's step graph. Currently there is no path to place these on the application canvas — they live only in the agent builder world.

**Target**: A subset of agent builder node types (those that operate at the routing/orchestration level) can be placed on the application canvas. The application canvas gets its own node library section: "Flow Control" with Router, HIL, Branch nodes sourced from the same backend registry.

---

## Phased Approach

### Phase 1 — Middleware registry (2–3 days, low risk)
*Immediate value, no execution changes.*

- Add `emoji`, `color`, `bg_color`, `description` fields to `middleware_defs` table.
- Extend `GET /admin/middleware-defs` to return these fields (already returned to the canvas).
- Create `APP_NODE_DEFS` constant (or small fetch hook) on the frontend that maps middleware slug → `{ emoji, color, bgColor, label }`.
- Replace the hardcoded `kind === 'guard' ? '🛡️' : '⚡'` in `MiddlewareNode` and `NodeLibrary` with a registry lookup.
- `MiddlewareNode` becomes visually driven by registry data — no more font-family hacks.

This is the immediate fix for the emoji issue and sets the pattern for Gap 1.

### Phase 2 — Unified app-level node component (1 week)
*No execution changes, frontend refactor only.*

- Introduce a generic `AppNode` component replacing `MiddlewareNode` (and eventually `OrchestratorNode` and `AgentNode` for their visual layer).
- `AppNode` reads `{ emoji, color, bgColor, label }` from a node-type registry lookup, identical to how `StepNode` works in the agent builder.
- `NodeLibrary.tsx` sections are generated from registry data instead of hardcoded.
- `CanvasNodes.tsx` reduces from 4 bespoke components to 2: one for topology boundary nodes (EP, Orch) and one generic `AppNode` for all others.
- `types.ts` no longer needs `kind: 'guard' | 'cache'` — each middleware def is just its own registry entry.

### Phase 3 — App canvas DAG + execution mode (2–3 weeks, significant)
*Runtime + backend changes required.*

- Add `execution_mode: 'local' | 'temporal'` to `app_orchestrators` (or app definition doc).
- `canvasToDoc` serializes the full topology as a typed connection graph (not just `tool`/`delegation`/`middleware`).
- Go service compiles the app definition into a Temporal workflow spec: agents → activities, edges → sequence/fork/join.
- Local execution mode: Go orchestrator walks the DAG sequentially in-process (no Temporal dependency).
- Canvas gains a toolbar toggle: **Local / Temporal** with a visual indicator.
- Middleware nodes become activities with pre/post hooks in the DAG execution path.

### Phase 4 — Flow control nodes on application canvas (1 week, after Phase 3)
*Depends on Phase 3's DAG execution model being in place.*

- Backend marks a subset of node types as `scope: 'app_canvas'` in the registry.
- Application canvas library shows a "Flow Control" section with: Router, Branch, HIL, Parallel.
- These nodes place as `StepNode` instances on the application canvas.
- The DAG compiler handles them the same way as in the agent builder.

---

## Reuse Opportunities

| What | Reuse | What's new |
|---|---|---|
| `StepNode` visual component | Direct reuse for middleware/flow nodes on app canvas | App canvas–specific handles (EP→Orch fixed topology) |
| `nodeRegistry` / `getNodeDef` | Direct reuse for all app canvas nodes | May need `app_canvas` scope filter |
| `NodeLibraryPanel` structure | Pattern reuse (sections, drag data format) | Different sections, different node types |
| DAG layout (dagre) | Direct reuse — already in both canvases | Nothing |
| Temporal workflow engine | Extension (add app-level workflow compiler) | App workflow spec → Temporal workflow definition |
| `human_wait` node | Direct reuse on app canvas | App-level HITL signal routing |

---

## Risks / Tradeoffs

**Registry approach adds a backend round-trip.** App canvas currently needs no backend call for node metadata. With a registry, it fetches `GET /admin/middleware-defs` (already done) or `GET /admin/app-node-types`. Minor — one cached fetch at canvas load.

**DAG execution is a significant runtime change.** The current linear loop is simple and well-tested. A DAG compiler touching Temporal workflows carries real risk of regression in existing apps. Phase 3 must be gated: existing apps default to `local` (same linear behavior), `temporal` DAG is opt-in per-app.

**Two canvases sharing node types could create confusion.** An agent's step graph (micro-level: LLM calls, transforms, HTTP requests) vs an application's routing graph (macro-level: which agents handle which flows) have different semantics. Reusing node types across both canvases requires clear visual and UX distinction — e.g. a Router on the app canvas routes between agents, not between LLM outputs. Backend registry should use `scope` to clearly separate which nodes appear where.

**Phase 1 and 2 are safe and high-value** — they fix current pain (emoji, hardcoded node types) without touching execution. Start there. Phases 3 and 4 are the architectural leap and warrant a separate design review before implementation begins.

---

## Recommendation

Start with **Phase 1** (middleware registry metadata) immediately — it fixes the current emoji problem correctly and establishes the pattern. **Phase 2** (generic AppNode component) can follow in the next session. **Phases 3 and 4** should be discussed as a separate milestone once Phase 2 is stable, since they touch the runtime execution model and require backend schema changes.
