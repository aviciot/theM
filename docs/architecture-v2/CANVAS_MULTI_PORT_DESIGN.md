# Canvas Multi-Port Wiring — Design & Implementation Guide

**Date:** 2026-08-27  
**Status:** Active design — partially implemented, gaps documented below

---

## 1. Current State (verified from code)

### What works
- Dragging a `data-out-*` handle from a Transform node triggers `onConnectStart`
- Every target node gets `_draggingVar` + `_dragAccept` injected — deduplicated per-node (if `cat_fact` exists on node B, it gets `cat_fact_2`)
- Ghost orange port appears on accept nodes (green ring), red ring on reject nodes
- On drop: `data.inputs[portID] = { from_step, from_port }` is written + a `dataEdge` is created
- `onDeleteInput` removes the binding from `data.inputs` and removes the edge
- Runtime: each step receives **only** vars declared in its `inputs` map — confirmed in `interpreter.go`

### Known gaps
1. **Load path does not restore `data.inputs`** — after save/reload, `data.inputs` on nodes is empty. Dynamic input port dots are invisible. Edges exist but the Handle elements (`data-in-*`) are not rendered, so ReactFlow can't connect to them.
2. **Single source of truth is edges** — `buildDefinitionDoc` derives `inputs` from the edge list, not from `data.inputs`. This is consistent but undocumented — any code that reads `data.inputs` before save will see stale data.
3. **Node types that can't accept data inputs** (`input`, `branch`) reject in `onConnectStart` but there is no registry-driven enforcement — it's a hardcoded set in page.tsx.

---

## 2. UI Design — Connecting Multiple Ports

### Visual model
```
LR layout (horizontal flow):
  ┌─────────────┐         ┌─────────────┐
  │  Transform  │──────→──│   Response  │  (control flow: left → right)
  │             │         │             │
  │  [cat_fact] ○─────────○ cat_fact    │  (data wire: named orange dot)
  │  [length]   ○─────────○ length      │
  └─────────────┘         └─────────────┘

TB layout (vertical flow):
  ┌─────────────┐
  │  Transform  │   (control flow: top → bottom)
  │ [cat_fact]  │
  │ [length]    │
  └──────┬──────┘
         │
  ○──────╯  ← data wires exit from bottom? or right?
```

### Constraints

| Constraint | Rule |
|---|---|
| Source of data wire | Must be a `data-out-*` handle (Transform output, static registry output) |
| Target of data wire | Any step node except `input` and `branch` |
| Port name collision | Auto-deduplicate: `varName` → `varName_2` → `varName_3` |
| Multiple ports on same target | Allowed — each gets its own named handle |
| Same var to multiple targets | Allowed — fan-out is valid |
| Delete | Removing edge must also remove `data.inputs` entry and the handle element |
| Load round-trip | After save/reload, all port handles must be re-rendered |

### What must be fixed for load round-trip
`loadDefinitionDoc` must populate `data.inputs` on each node from `step.inputs` in the doc — mirroring what `onPipeConnect` does at drag time:

```ts
// In loadDefinitionDoc, after building stepNodes:
for (const step of skill.steps) {
  if (step.inputs) {
    const node = stepNodes.find(n => n.id === `step-${step.id}`);
    if (node) node.data.inputs = step.inputs;
  }
}
```

This makes `data.inputs` always in sync with edges and the handles always render correctly.

---

## 3. Tech Stack

| Layer | Technology |
|---|---|
| Canvas renderer | [React Flow](https://reactflow.dev/) v11 (`@xyflow/react`) |
| Layout engine | [Dagre](https://github.com/dagrejs/dagre) — directed graph auto-layout |
| Node types | Custom React components (`StepNode`, `AgentRootNode`, `SkillNode`) |
| Edge types | Custom `DataEdge` (dashed indigo), `DebugEdge` (animated) |
| State | React `useState` + `useNodesState` / `useEdgesState` from React Flow |
| Persistence format | JSON canvas doc — `AgentDefinitionDoc` → `step.inputs` map |
| Runtime | Go `interpreter.go` — scoped var delivery per step |

---

## 4. Making UI Development Easier — NodeDef as Single Source of Truth

### The problem
Currently two separate places define what a node can do:

1. **Go** — `go/internal/agentgen/nodedef.go` — the authoritative NodeDef (step types, input/output ports, edge rules, descriptions)
2. **Frontend** — `frontend/src/lib/nodeRegistry.ts` — a partial mirror (UI supplements: emoji, color, summary function) with no enforcement from Go

Any time Go adds a new step type or changes port rules, the frontend must be manually updated. This is error-prone.

### What exists today
`GET /api/v1/admin/node-types` already returns `NodeDef[]` from Go — the frontend fetches this on builder load via `fetchNodeTypes()`. The returned fields include:
- `type`, `label`, `description`, `emoji`
- `output_arity`, `is_source`, `is_sink`, `single_input`, `executable`
- `edges` — `{ min_in, max_in, min_out, max_out }`
- `input_ports[]` — `{ id, label, description }` — **static declared input data ports**
- `output_ports[]` — **static declared output data ports**

### What to add to NodeDef in Go

```go
type NodeDef struct {
    // existing fields ...
    
    // New: whether this node can accept dynamic data input ports (drag-to-wire)
    AcceptsDynamicInputs bool `json:"accepts_dynamic_inputs"`
    
    // New: whether this node produces named output vars (like Transform)
    // If true, output port names are derived at runtime from config, not statically known
    DynamicOutputs bool `json:"dynamic_outputs"`
    
    // New: summary template for the canvas node subtitle (replaces UI_SUPPS hardcode)
    // e.g. "from {{.from_var}}" or "→ {{.output_var}}"
    SummaryTemplate string `json:"summary_template"`
}
```

### What the frontend stops hardcoding

| Currently hardcoded in frontend | Replaced by NodeDef field |
|---|---|
| `DATA_INPUT_REJECT = new Set(['input', 'branch'])` | `!nodeDef.accepts_dynamic_inputs` |
| `isTransform = data.step_type === 'transform'` guard for named outputs | `nodeDef.dynamic_outputs` |
| `UI_SUPPS` color/emoji per type | already in NodeDef |
| `input_field` per type for auto-fill heuristic | already partially in NodeDef |

### Implementation plan

1. **Go** — add `AcceptsDynamicInputs` and `DynamicOutputs` bool fields to `NodeDef`, set correctly per type, expose via existing `/api/v1/admin/node-types` endpoint
2. **Frontend `nodeRegistry.ts`** — remove `DATA_INPUT_REJECT` set from page.tsx, instead read `nodeDef.accepts_dynamic_inputs` in `onConnectStart`
3. **Frontend `StepNode.tsx`** — remove `isTransform` special case; use `nodeDef.dynamic_outputs` to know whether to render named output handles from config
4. **Frontend `loadDefinitionDoc`** — add the `data.inputs` restoration (one-liner fix for the load gap)

### Result
- One NodeDef in Go drives port rendering, validation, drag rules, and edge rules
- Adding a new step type requires only a Go change — frontend picks it up via the API
- No frontend deploy needed for new node types that follow existing patterns

---

## 5. Priority Fix List

| Priority | Fix | File |
|---|---|---|
| P0 | Restore `data.inputs` on load | `page.tsx` `loadDefinitionDoc` |
| P1 | Add `AcceptsDynamicInputs` to Go NodeDef | `go/internal/agentgen/nodedef.go` |
| P1 | Drive `DATA_INPUT_REJECT` from NodeDef | `page.tsx` `onConnectStart` |
| P2 | Add `DynamicOutputs` to NodeDef | Go + `StepNode.tsx` |
| P2 | Add `SummaryTemplate` to NodeDef | Go + `nodeRegistry.ts` |
