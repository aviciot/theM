# Cycle Node — Design & Implementation Plan
# Last updated: 2026-10-02
# Status: Planning — Phase 1 ready to build

---

## What is a Cycle node?

A **Cycle** is a canvas-level container that wraps a sub-section of an AppFlow graph and
repeats it until an exit condition is met. It is the the-M equivalent of ADK's `LoopAgent`
and Dify's Loop/Iteration container — a first-class primitive, not a workaround using backward
edges.

**Key properties:**
- The canvas stays a DAG — no backward edges, no cycle in the graph topology
- The "loop" is expressed as a Temporal `for` loop compiled from the container's contents
- Exit is driven by a flow var condition (`break when: <var> = <value>`) or a max-iteration cap
- Flow vars set inside a Cycle are visible on the next iteration (state passes forward)

**Why "Cycle" not "Loop":**
Loop is overloaded (for-loop, while-loop, event loop). Cycle is deliberate, memorable, and
matches the ↻ icon. Users say "wrap this in a Cycle."

---

## Prior art summary

| Platform | Approach | Notes |
|---|---|---|
| **Dify** | Container/frame on canvas — drag nodes inside | Cleanest UX, no backward edges |
| **ADK** | `LoopAgent` wraps child agents, `escalate` signal breaks loop | Code-only, no canvas |
| **LangGraph** | Real backward edges in cycle-aware renderer | Works but requires custom renderer |
| **n8n** | IF node wired backward — messy | Not a model to follow |

**We follow Dify's container approach** — it avoids backward edges, maps naturally to
Temporal's `for` loop, and fits our existing ReactFlow canvas without a renderer rewrite.

---

## Phase 1 — Cycle container (no true graph cycle)

**Scope:** Cycle as a visual frame/container on the App Canvas. Nodes inside repeat until exit
condition or max iterations. The canvas graph remains acyclic. No backward edges.

**Effort:** L  
**Dependency:** Existing AppFlow compiler, flow vars system

---

### 1A — Canvas UI

**New node type: `cycle`**

Rendered as a rounded rectangle (frame) that contains child nodes. Uses ReactFlow's
[`NodeResizer`](https://reactflow.dev/docs/api/nodes/node-resizer/) + parent/child node
grouping (same pattern as React Flow's sub-flow / group node examples).

**Visual design:**
- Color: indigo/violet — distinct from the blue/green/amber/red palette of existing nodes
- Header badge: `↻ Cycle` label
- Header controls (inline, small):
  - `break when:` → dropdown of current flow vars + operator + value
  - `max:` → integer input (default 10, range 1–100)
- One **entry handle** on the top (flow enters here)
- One **exit handle** on the bottom (flow continues here when loop ends)
- Nodes inside the frame connect to each other normally
- The last node inside connects back to the Cycle's internal entry handle (visual only — the
  compiler understands this means "next iteration")

**Node library panel:**
- New "Control Flow" section (alongside Agents, Middleware, AppFlow nodes)
- `↻ Cycle` listed there

**Canvas interactions:**
- Drag a Cycle node from the library → empty frame appears, resizable
- Drag existing nodes into the frame → they become children (ReactFlow parent assignment)
- Right-click a selection of nodes → "Wrap in Cycle" → frame created around selection
- Delete Cycle frame → children are ejected back to canvas root (not deleted)

---

### 1B — AppDefinitionDoc shape

Cycle is a component like any other — stored in `components[]` in the `AppDefinitionDoc` JSON.
Children store their `parentId` pointing to the Cycle's `instance_id`.

```json
{
  "instance_id": "cycle_1",
  "definition_ref": { "kind": "flow_control", "name": "cycle" },
  "config": {
    "break_when_var": "accepted",
    "break_when_op": "eq",
    "break_when_value": "true",
    "max_iterations": 10
  }
}
```

Child nodes carry `"parent_instance_id": "cycle_1"` in their component entry.

**No new DB table** — config lives in `AppDefinitionDoc` JSON (export/import covered
automatically). This satisfies the App Export/Import completeness rule (CLAUDE.md hard rule).

---

### 1C — Component definition (registry)

New builtin component definition row in `db/002_seed.sql` (or next migration):

```sql
INSERT INTO component_definitions
  (id, kind, namespace, name, version, label, description, enabled, status, config_schema, port_defs, scope)
VALUES
  (gen_random_uuid(), 'flow_control', 'builtin', 'cycle', '1.0.0',
   'Cycle', 'Repeats inner nodes until exit condition or max iterations',
   true, 'stable',
   '{"type":"object","properties":{"break_when_var":{"type":"string"},"break_when_op":{"type":"string","enum":["eq","neq","gt","lt","truthy"]},"break_when_value":{"type":"string"},"max_iterations":{"type":"integer","minimum":1,"maximum":100,"default":10}}}',
   '[{"id":"in","type":"target","label":""},{"id":"out","type":"source","label":"done"}]',
   'builtin');
```

---

### 1D — Compiler (`go/internal/appflow/compiler.go`)

**New compile step: `compileCycleNodes`**

Called after the flat node list is built, before `buildEdgeMap`.

1. Find all `cycle` kind components
2. Collect all child components (those with matching `parent_instance_id`)
3. For each Cycle, produce a `CycleSpec`:

```go
type CycleSpec struct {
    NodeID        string        `json:"node_id"`
    BreakWhenVar  string        `json:"break_when_var"`
    BreakWhenOp   string        `json:"break_when_op"`   // "eq"|"neq"|"gt"|"lt"|"truthy"
    BreakWhenVal  string        `json:"break_when_val"`
    MaxIterations int           `json:"max_iterations"`
    Body          []AppFlowNode `json:"body"`            // ordered sub-graph
    BodyEdges     []AppFlowEdge `json:"body_edges"`
    EntryNodeID   string        `json:"entry_node_id"`   // first node in body
}
```

4. The Cycle's entry handle in the outer graph becomes a single `AppFlowNode` of kind `cycle`
   with config = `CycleSpec` serialized. From the outer workflow's perspective the Cycle is
   one opaque node.

**Edge compilation:**
- Outer edges that enter the Cycle frame → point to the Cycle node's ID
- Outer edges that exit the Cycle frame → point from the Cycle node's ID (`out` handle)
- Inner edges are stored in `CycleSpec.BodyEdges` and never appear in the outer edge map

**Validation (`go/internal/appflow/validate.go`):**
- Cycle body must have at least one node
- `break_when_var` must be non-empty
- `max_iterations` must be between 1 and 100
- Body must form a valid DAG (no nested cycles in Phase 1 — error if detected)
- Entry node must be reachable from the Cycle's in-handle

---

### 1E — Workflow execution (`go/internal/appflow/`)

**New file: `go/internal/appflow/cycle.go`**

`execCycleNode` — called from the main dispatch loop when a node of kind `cycle` is reached:

```go
func execCycleNode(
    ctx workflow.Context,
    node *AppFlowNode,
    input AppFlowWorkflowInput,
    vars FlowVars,
    // ... activity options etc
) (FlowVars, error) {
    var spec CycleSpec
    _ = json.Unmarshal(node.Config, &spec)

    for i := 0; i < spec.MaxIterations; i++ {
        // Run the body sub-graph (same dispatch loop, scoped to body nodes/edges)
        vars, err = runSubGraph(ctx, spec.Body, spec.BodyEdges, spec.EntryNodeID, input, vars)
        if err != nil {
            return vars, fmt.Errorf("cycle %q iteration %d: %w", node.ID, i+1, err)
        }
        // Check exit condition
        if evalBreakCondition(vars, spec.BreakWhenVar, spec.BreakWhenOp, spec.BreakWhenVal) {
            traceNode(ctx, input.RunID, node.ID, "cycle", "node_done",
                fmt.Sprintf("broke after %d iteration(s)", i+1), input.LogVerbosity)
            return vars, nil
        }
    }
    // Max iterations exhausted — not an error, just exit
    traceNode(ctx, input.RunID, node.ID, "cycle", "node_done",
        fmt.Sprintf("max iterations (%d) reached", spec.MaxIterations), input.LogVerbosity)
    return vars, nil
}
```

`runSubGraph` is a helper that runs the existing node-dispatch logic over a scoped node/edge
set — extracts the loop from `workflow.go` so both the top-level flow and a Cycle body share
the same dispatcher. This is the main refactor in Phase 1.

`evalBreakCondition` evaluates `vars[BreakWhenVar] <op> BreakWhenVal` — pure function, no I/O.

**Flow var scoping:**
- Flow vars are **shared** between the outer flow and the Cycle body — same `FlowVars` map
  passed in and out. Vars set inside the body persist after the loop exits.
- This matches ADK's `output_key` / shared context model.

---

### 1F — Debug & Inspector

**Debug panel (step mode):**
- Each Cycle iteration appears as a collapsible group in the step log:
  `↻ Cycle cycle_1 — iteration 2/10`
- Inner node steps are indented under it
- Exit reason shown: `broke after 3 iterations` or `max iterations (10) reached`

**Run Inspector:**
- `run_steps` rows for inner nodes carry `node_id = "cycle_1:iter:2:llm_1"` (prefixed with
  cycle ID and iteration number) so the inspector can group them
- The Cycle node itself gets a summary step: status = `completed`, output = iteration count

---

### 1G — Export / Import

No extra work — Cycle config lives in `AppDefinitionDoc` JSON. Export and import handle it
automatically (satisfies CLAUDE.md hard rule).

---

### 1H — Tests

Following `go/CLAUDE.md` — every changed behavior needs a test.

| Test | Location |
|---|---|
| `TestCompileCycleNode` — cycle compiles to CycleSpec correctly | `go/internal/appflow/compiler_test.go` |
| `TestCompileCycleValidation` — body empty / no break_var / nested cycle errors | `go/internal/appflow/validate_test.go` |
| `TestExecCycleBreaksOnCondition` — loop exits after var set | `go/internal/appflow/cycle_test.go` |
| `TestExecCycleMaxIterations` — loop exits after N even if condition never met | `go/internal/appflow/cycle_test.go` |
| `TestExecCycleVarsShared` — var set in body iteration 1 visible in iteration 2 | `go/internal/appflow/cycle_test.go` |
| `TestRunSubGraph` — sub-graph dispatcher matches top-level dispatcher | `go/internal/appflow/workflow_test.go` |

---

## Phase 2 — True cycle (deferred)

**What this adds:** an edge inside the Cycle body that points backward to an earlier inner
node, creating a genuine sub-cycle within the container. Useful for "keep asking follow-up
questions until intent is clear" flows where the re-entry point is mid-body, not at the top.

**Why deferred:**
- Phase 1 covers 90% of real-world loop use cases
- True cycles inside the body require a cycle-aware sub-graph dispatcher and a more complex
  canvas edge validation pass
- The canvas renderer needs a way to draw the backward edge legibly inside the frame — this
  is a non-trivial UX problem

**Design sketch (not final):**
- A `↩ Jump` node inside the body, configured with "jump to: <inner node id>"
- Compiler detects the Jump node and emits a backward reference in `BodyEdges`
- Sub-graph dispatcher handles it as a `goto` — only legal inside a Cycle body

---

## Phase 3 — Iteration over lists (deferred)

**What this adds:** Cycle body runs once per item in a flow var that holds a list
(e.g. process each order line, send each notification).

**Config addition:**
- `iterate_over_var: "order_lines"` — if set, `break_when` is ignored; loop runs `len(list)` times
- Each iteration receives `cycle_item` flow var = current element, `cycle_index` = current index

---

## Success criteria (Phase 1)

- Cycle node appears in the App Canvas node library
- User can drag nodes into a Cycle frame and configure exit condition + max iterations
- Publish compiles a flow with a Cycle to a valid `AppFlowSpec`
- Debug run executes the body repeatedly, exits on condition or max cap, flow vars persist
- Inspector shows per-iteration steps grouped under the Cycle node
- Export → Import round-trip preserves Cycle config with no manual re-click required
- All new tests pass; `go test ./...` zero failures

---

## Files that will change

| File | Change |
|---|---|
| `db/002_seed.sql` (or next migration) | Add `cycle` component definition row |
| `go/internal/appflow/compiler.go` | `compileCycleNodes`, `CycleSpec` type |
| `go/internal/appflow/validate.go` | Cycle validation rules |
| `go/internal/appflow/cycle.go` | New — `execCycleNode`, `runSubGraph`, `evalBreakCondition` |
| `go/internal/appflow/workflow.go` | Extract sub-graph dispatcher into `runSubGraph` |
| `go/internal/appflow/compiler_test.go` | `TestCompileCycleNode` |
| `go/internal/appflow/validate_test.go` | `TestCompileCycleValidation` |
| `go/internal/appflow/cycle_test.go` | New — execution tests |
| `frontend/src/components/canvas/AppCanvas.tsx` | Cycle frame rendering |
| `frontend/src/components/canvas/NodeLibrary.tsx` | Add Cycle to Control Flow section |
| `frontend/src/components/canvas/CanvasNodes.tsx` | `CycleNode` component |
| `frontend/src/lib/canvasHelpers.ts` | `docToCanvas` / `canvasToDoc` for cycle parent/child |
| `frontend/src/types/canvas.ts` | `CycleNodeData` type |
| `go/TEST_INDEX.md` | Add new cycle tests |
| `docs/CURRENT.md` | Update state after implementation |
