# Application Canvas Upgrade Plan
# Last updated: 2026-09-16
# Status: Planning — no phases started

---

## Strategic framing

The application canvas is the **governance shell** around agents — it controls who can reach which agent, how, with what security in between, and under what execution model. It is not the same as the agent builder (which defines what an agent does internally).

The two canvases have distinct jobs:
- **Agent builder** → defines agent DAG steps (internal, technical)
- **Application canvas** → composes and governs exposed agents (external-facing, business)

They should stay separate but share infrastructure where it makes sense: node registry pattern, visual metadata, and execution backend selection.

---

## What exists today

### Agent builder (already done)
- `agentgen.Compile` / `CompileForPublish` — takes canvas JSON → produces `AgentSpec`
- `AgentSpec.ExecutionBackend`: `""` / `"local"` → in-process goroutines; `"temporal"` → Temporal DAG
- Node registry: each node type has `emoji`, `bg`, `border`, `label`, port definitions, execution policy
- `StepNode` React component renders any node type from registry — no hardcoding
- `NodeLibraryPanel` lists node types by category from the backend registry

### Application canvas (current gaps)
- Canvas definition (`AppDefinitionDoc`) is topology-only — nodes + edges saved as JSON but never compiled to an execution plan
- Node types are hardcoded: `EntryPointNode`, `OrchestratorNode`, `AgentNode`, `MiddlewareNode` in `CanvasNodes.tsx`
- `MiddlewareData` has hardcoded `kind: 'guard' | 'cache'` — no registry lookup
- Middleware visual metadata (`emoji`, colors) hardcoded in component code
- No execution mode toggle — always uses the legacy agentic loop
- No Flow Control nodes (Router, HIL) at the application level

---

## Phase 1 — Middleware node registry
**Scope:** Visual/metadata only. No execution change. No new DB runtime.
**Effort:** S
**Dependency:** None

### What changes

**DB (`middleware_defs` table):**
- Add columns: `emoji TEXT`, `color TEXT`, `bg_color TEXT`
- Migration: `db/097_middleware_defs_visual.sql`
- Seed: File Guard → `emoji='🛡️'`, `color='#f59e0b'`, `bg_color='rgba(245,158,11,0.08)'`

**Go (`go/internal/admin/dal/middleware_wirings.go` or new `dal/middleware_defs.go`):**
- `ListMiddlewareDefs` response includes `emoji`, `color`, `bg_color`

**Frontend:**
- `apiTypes.ts`: `MiddlewareDef` gets `emoji?: string; color?: string; bg_color?: string`
- `MiddlewareData` / `MwNodeData` (`types.ts`): add `emoji?: string; color?: string; bg_color?: string`
- `CanvasNodes.tsx` `MiddlewareNode`: read `data.emoji` / `data.color` / `data.bg_color` — same pattern as `StepNode` reads `meta.emoji`
- `NodeLibrary.tsx`: read `m.emoji` from the def — no more hardcoded `m.kind === 'guard' ? '🛡️' : '⚡'`
- `docToCanvas` in `CanvasHelpers.ts`: pass emoji/color through to node data on load
- Drop the `fontFamily: 'Apple Color Emoji...'` workaround — emoji comes from DB, renders in plain div

### What can be reused
- Existing `ListMiddlewareDefs` API endpoint — just extend the query and response shape
- `StepNode` pattern — exactly the same approach, applied to `MiddlewareNode`

### Success criteria
- File Guard shows 🛡️ in node library and on canvas node, sourced from DB
- Adding a new middleware def with a custom emoji requires only a DB row insert, no frontend code change
- Color/bg_color drive node styling — no hardcoded amber everywhere

---

## Phase 2 — Flow Control nodes in application canvas
**Scope:** Router and HIL as droppable nodes in the application canvas. Topology only — no execution.
**Effort:** M
**Dependency:** Phase 1 pattern established (registry-driven nodes)

### What changes

**Frontend — node library:**
- Add "Flow Control" section to `NodeLibrary.tsx` alongside Agents and Middleware
- Initial node types: **Router** (intent-based routing to different agents) and **HIL** (pause for human decision)
- These reuse node type definitions from the agent builder's node registry (`GET /admin/node-types`)
- New `FlowControlNodeData` interface in `types.ts`

**Frontend — canvas:**
- New `FlowControlNode` React component in `CanvasNodes.tsx` — or reuse/adapt `StepNode` directly
- `NODE_TYPES` map extended: `{ ..., flowControl: FlowControlNode }`
- `canvasToDoc` / `docToCanvas` in `CanvasHelpers.ts`: serialize/restore `flow_control` connection type

**Backend — no changes yet** (topology is saved as JSON, execution not wired up)

### What can be reused
- Agent builder's `GET /admin/node-types` already serves Router and HIL node definitions with full metadata
- `StepNode` component can be imported directly into the application canvas for these node types — avoids a duplicate component

### Success criteria
- Router and HIL nodes appear in the node library under "Flow Control"
- Dragging them onto the canvas places a node that persists through save/reload
- Canvas validation warns if Router has no outgoing edges or HIL has no resume path

---

## Phase 3 — Application DAG execution
**Scope:** The application canvas gains a real runtime — local loop or Temporal DAG.
**Effort:** L
**Dependency:** Phase 2 (Router/HIL topology must be serializable before it can be executed)

### The core new piece: `AppCanvasCompiler`

The agent builder has `agentgen.Compile` which takes agent canvas JSON → `AgentSpec` (steps within one agent).

The application needs an analogous compiler that takes `AppDefinitionDoc` → an executable plan where **agents are the units** (not steps). Call it `AppFlowSpec`.

```go
// go/internal/appflow/compiler.go (new package)
type AppFlowSpec struct {
    ExecutionBackend string        // "local" | "temporal"
    EntryPoints      []EPFlow
}
type EPFlow struct {
    Slug     string
    Protocol string
    Nodes    []AppFlowNode   // ordered/graph — agents, middleware, flow-control
    Edges    []AppFlowEdge
}
type AppFlowNode struct {
    ID       string          // instance_id from canvas
    Kind     string          // "agent" | "middleware" | "flow_control"
    AgentID  string          // for kind=agent: resolved agents.id
    NodeType string          // for kind=flow_control: "router" | "hil"
    Config   json.RawMessage
}
```

**Reuse from agentgen:**
- `ExecutionBackend` field and semantics — identical concept, copy the pattern
- Graph validation helpers (`validateGraph` logic) — adapt for agent-level graph instead of step-level
- Temporal workflow dispatch — `CanvasAgentWorkflow` already exists in `go/internal/temporal/`; the app-level compiler generates a compatible workflow input where each activity = one agent invocation

**Local mode:**
- Compiler produces a topological order of agent nodes
- Orchestrator walks the order, calling each agent, passing output as input to next
- Router node = evaluate condition, pick branch
- HIL node = emit `hitl_required` event, pause, wait for signal (already implemented in Temporal)

**Temporal mode:**
- Compiler produces a workflow definition JSON
- `them-dag-worker` receives it, maps each node to an activity
- Agent invocations become `ExecuteAgent` activities (new activity type)
- Router = Temporal `SideEffect` or workflow decision
- HIL = existing `HumanWaitActivity`

**Frontend:**
- `AppDefinitionDoc` gains `execution_backend?: 'local' | 'temporal'`
- Canvas toolbar gets an execution mode toggle (simple select or toggle button)
- On save, `execution_backend` is persisted in the definition JSON

**DB:**
- No schema change — `execution_backend` is stored inside the definition JSON blob

### What can be reused
- `AgentSpec.ExecutionBackend` semantics — identical, same values
- `CanvasAgentWorkflow` in `go/internal/temporal/` — extend to accept `AppFlowSpec` input
- `them-dag-worker` — already running, register new activity types there
- HIL signal handling — already implemented for agent builder workflows

### Success criteria
- Canvas with two agents in sequence (EP → Orch → AgentA → AgentB) executes in the right order
- Local mode: both agents called, second receives first agent's output
- Temporal mode: workflow visible in Temporal UI with two activity nodes
- Router node correctly routes to one of two agents based on message content
- HIL node pauses flow, resumes after human approval in the run UI

---

## Phase 4 — Full node unification (future / optional)
**Scope:** Application canvas and agent builder share one node component and one library.
**Effort:** L
**Dependency:** Phases 1–3

This phase eliminates the parallel `CanvasNodes.tsx` / `StepNode.tsx` implementations. Both canvases render all node types through `StepNode` with context-aware filtering (agent builder sees step-level nodes; application canvas sees agent/middleware/flow-control nodes). One node library, one registry endpoint drives both.

Not prioritized until Phases 1–3 are stable.

---

## Effort summary

| Phase | Description | Effort | Status |
|---|---|---|---|
| 1 | Middleware node registry (visual) | S | Pending |
| 2 | Router + HIL in app canvas (topology) | M | Pending |
| 3 | App canvas DAG execution (local + Temporal) | L | Pending |
| 4 | Full node unification | L | Future |

---

## Key files per phase

| Phase | Go | Frontend |
|---|---|---|
| 1 | `dal/middleware_defs.go` (extend query) | `types.ts`, `CanvasNodes.tsx`, `NodeLibrary.tsx`, `CanvasHelpers.ts` |
| 2 | none | `CanvasNodes.tsx`, `NodeLibrary.tsx`, `CanvasHelpers.ts`, `types.ts` |
| 3 | `internal/appflow/compiler.go` (new), `internal/temporal/workflow.go` (extend) | `CanvasHelpers.ts` (`canvasToDoc`), canvas toolbar |
| 4 | none | `CanvasNodes.tsx` → merged into agent builder's `StepNode` |
