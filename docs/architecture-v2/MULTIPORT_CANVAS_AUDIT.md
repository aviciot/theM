# Multi-Port Canvas Architecture Audit
# Produced: 2026-08-28
# Purpose: Design input for generic multi-port / bundled-cable canvas implementation

---

## 1. What the Backend NodeDef Currently Provides (field-by-field)

`GET /api/v1/admin/node-types` returns `[]NodeTypeInfo`. Every field below is serialised from `noderegistry.go:NodeTypeInfo`.

### Identity & Display
| Field | Type | Example | Notes |
|---|---|---|---|
| `type` | string | `"http"` | StepType constant — stable key |
| `version` | int | 1 | schema version, not used by frontend yet |
| `label` | string | `"HTTP"` | human name for palette |
| `description` | string | `"Make an HTTP request…"` | tooltip |
| `emoji` | string | `"🌐"` | icon on node card |
| `color` | string | `"rgba(20,184,166,0.5)"` | accent color |
| `bg_color` | string | `"rgba(20,184,166,0.07)"` | card background |

### Topology / Structure
| Field | Type | Example | Notes |
|---|---|---|---|
| `is_source` | bool | true for `input` | no incoming edges allowed |
| `is_sink` | bool | true for `response`/`stream_out` | no outgoing edges allowed |
| `single_input` | bool | true for `llm` | max 1 incoming control edge |
| `output_arity` | string | `"single"`, `"multi"`, `"none"` | loosely describes output cardinality |
| `edges` | EdgeRules | `{min_in:1, max_in:1, min_out:1, max_out:1}` | **authoritative** for compiler and connection guard |
| `accepts_dynamic_inputs` | bool | true for `http`, `transform`, `llm` | user can drag a data-out port onto this node |
| `dynamic_outputs` | bool | true for `transform` only | output port names are derived from config (functions[].output_var), not static |

### Static Port Declarations
| Field | Type | Notes |
|---|---|---|
| `input_ports` | `[]PortDef` | Static named input data ports (nil = dynamic or none) |
| `output_ports` | `[]PortDef` | Static named output data ports (nil = dynamic or no data outputs) |

**PortDef fields:**
| Field | Notes |
|---|---|
| `id` | stable handle ID (e.g. `"output"`, `"from_var"`) |
| `label` | human-readable (e.g. `"LLM response"`) |
| `required` | input must be wired; output always produced |
| `multi` | input accepts multiple bindings (fan-in) — currently unused in practice |
| `type_hint` | `"text"` \| `"json"` \| `"any"` — informational only |

### Other Fields
| Field | Notes |
|---|---|
| `input_field` | config key to auto-fill when a control edge connects |
| `executable` | whether Execute is implemented (vs stub) |
| `app_params` | runtime params the node can consume (HTTP auth keys etc.) |
| `allowed_successors` | valid next-hop step types (for LLM copilot only — not enforced by frontend) |
| `config_fields`, `usage_notes`, `examples` | LLM copilot knowledge — not used by canvas |

### Current Port Population (which nodes have static ports)
| Node | input_ports | output_ports | dynamic_outputs |
|---|---|---|---|
| `input` | none | none (DeriveOutputs computes from config) | false |
| `llm` | `[{id:"input", label:"Prompt input"}]` | `[{id:"output", label:"LLM response", required:true}]` | false |
| `http` | none | none (extractions are dynamic) | false |
| `transform` | none | none (functions[].output_var drives ports) | **true** |
| `response` | `[{id:"from_var", label:"Response value", required:true}]` | none | false |
| `branch` | none | none (control routing only) | false |
| `mcp_call` | none | none | false |
| All stubs | none | none | false |

---

## 2. What the Frontend Adds on Top

### `frontend/src/lib/nodeRegistry.ts`

**Legitimate frontend-only additions (presentation only):**
- `bg` / `border` — CSS shorthand aliases for `bg_color` / `color`
- `summary(cfg)` — subtitle rendered from live config data (the only truly frontend-owned rule)
- In-memory cache (`_cache`, `_byType`, `setCachedNodeTypes`, `getCachedNodeTypes`)
- Convenience helpers: `isSingleInput`, `isSource`, `isSink`, `outputArity`, `acceptsDynamicInputs`, `hasDynamicOutputs`, `canAddIncoming`, `canAddOutgoing` — all delegate to backend data. **No duplication of logic — good.**

### `frontend/src/app/admin/agents/builder/nodeVars.ts`

**Substantial logic duplication here.** `extractNodeVars()` re-implements the DeriveInputs/DeriveOutputs logic in TypeScript, per node type:
- `input` → reads binding config, writes `bindVar`
- `llm` → reads template vars from user_prompt/system_prompt, writes output_var
- `transform` → walks functions[], reads input_var, writes output_var
- `http` → reads url_template/body_template vars, writes http_response + extractions
- `response` → reads from_var
- `branch` → reads expression vars

**This is the largest structural duplication.** The Go compiler already computes identical derivations via `DeriveInputs`/`DeriveOutputs`. The TypeScript version exists for live pre-validate UX (edge labels, port highlights) because the backend derivation is only available after calling the validate API.

---

## 3. Hardcoded Node-Type Conditionals Found

### `StepNode.tsx`
| Line | Conditional | Purpose |
|---|---|---|
| 201 | `data.step_type === 'branch'` | Renders two named source handles (`source-true`, `source-false`) with green/red colors and T/F labels |
| 214 | `dynOutputs && transformOutputs.length > 0` | Renders per-var named source handles for transform outputs (pixel-positioned by function index) |
| 237 | else clause | Generic single source handle for all other types |

This 3-way branch is the primary structural hardcode that prevents generic multi-port rendering.

### `page.tsx`
| Lines | Conditional | Purpose |
|---|---|---|
| 450, 693–698 | `step_type === 'branch'` | Source handle routing: maps `source-true`/`source-false` to true/false next step IDs in canvas JSON |
| 644–652 | `isBranch`, `isTransform && sh` | When loading edges from canvas JSON, applies special sourceHandle logic for branch and transform |
| 698 | `step_type === 'transform' && ctrlOut.some(e => e.sourceHandle)` | Preserve named sourceHandle for transform edges in buildDefinitionDoc |
| 1035 | `srcData.step_type === 'input'` | Auto-fill heuristic: input step uses bindings.text or 'input'; all others use output_var |
| 1120–1121 | `srcType === 'branch' \|\| srcType === 'transform'` | Degree check: count outgoing edges PER handle (not total) for branch and transform |
| 1156, 1185 | `step_type === 'llm'`, `step_type === 'http'` | Debug setup: determines which debug params to show |
| 1264–1377 | Full `if/else if` chain for each step type | Debug execution: re-implements all node semantics in TypeScript |

### `nodeVars.ts`
| Lines | Conditional | Purpose |
|---|---|---|
| 48–123 | Full `if` chain: `input`, `llm`, `transform`, `http`, `response`, `branch` | Duplicates DeriveInputs/DeriveOutputs in TypeScript for live edge label computation |

---

## 4. Current Handle/Port Model Limitations for Multi-Port Use Case

**The target design (from image):** Many ports arranged vertically on source and target sides, multiple edges between the same node pair fanning into a bundle with a count badge in the center.

**Current limitations:**

1. **Static 1:1 control edge model.** Each node has exactly one source handle and one target handle. There is no concept of a named source port per output variable, except for `branch` (2 named handles) and `transform` (N named handles from functions).

2. **Data-port model is there but hidden.** `data-in-{portID}` / `data-out-{portID}` handles exist for the explicit binding system (orange squares, dashed indigo edges). But:
   - Data edges are kept visually separate from control edges (different color, dashed)
   - Only `transform` has multiple data-out handles (one per function output_var)
   - The current UI does NOT fan out multiple data-out ports on the source side — it shows them as small squares stacked inside the node, not as a port-rail design

3. **No bundled edge rendering.** Multiple edges between the same source and target node are rendered independently. There is no grouping, no count badge, no hover-expand mechanic.

4. **Port spacing is absolute pixel math**, not driven by port count or node geometry. Adding more ports expands node padding but does not trigger Dagre re-layout.

5. **`branch` source handles are hardcoded** in StepNode.tsx lines 201–212 with type-specific colors, position fractions (30%/70%), and T/F labels. This cannot be generalised without a per-port handle descriptor.

6. **`useUpdateNodeInternals()` is NOT called anywhere.** When dynamic handles change (e.g., transform outputs when config changes), React Flow's internal geometry can become stale. This is a latent bug.

---

## 5. Gap Analysis — Missing Backend Fields

### 5.1 Port Direction
**Current:** `InputPorts` / `OutputPorts` arrays imply direction by which array they're in. This is sufficient if we read them correctly.
**Gap:** None — direction is implicitly encoded by array membership.

### 5.2 Port Cardinality (max connections per port)
**Current:** `PortDef.multi` exists (`bool`) but means "fan-in allowed", not a max count.  
**Gap:** No per-port max connection count. The `edges.max_in`/`max_out` applies to the node as a whole, not per port.  
**Need:** For multi-port design, each output port on a source might allow exactly 1 connection, or unlimited. `PortDef.multi` covers the input case; output cardinality is unspecified.

**Proposed addition to PortDef:**
```go
MaxConnections int `json:"max_connections,omitempty"` // 0 = unlimited, 1 = exclusive
```

### 5.3 Port Type (control vs data)
**Current:** There are two entirely separate systems:
- Control edges (no sourceHandle/targetHandle) → execution order
- Data edges (sourceHandle=`data-out-*`, targetHandle=`data-in-*`) → variable binding

The control handles (center of node) are anonymous React Flow defaults. Data handles are rendered as small colored squares.  
**Gap:** Backend has no concept of "control port" vs "data port" — they're inferred by the presence/absence of a sourceHandle string.  
**Need:** For a unified multi-port rail design, control-flow ports need to be declared alongside data ports so they can be positioned in the same rail.

**Proposed addition to NodeDef:**
```go
// ControlPorts declares named control-flow output ports (for multi-output control nodes like branch).
// Empty for most nodes (they have one anonymous control output).
// When populated, the generic handle renderer places each in the output rail.
ControlOutputPorts []PortDef `json:"control_output_ports,omitempty"`
```
For `branch`, this would be `[{id:"true", label:"True", color:"#4ade80"}, {id:"false", label:"False", color:"#f87171"}]`.

### 5.4 Dynamic Port Creation Rules
**Current:** `dynamic_outputs: true` on `transform` tells the frontend "compute port names from config". But the rule itself (look at `functions[].output_var`) is hardcoded in `StepNode.tsx` (`computeFinalOutputs`) and `nodeVars.ts`.  
**Gap:** The backend knows this (DeriveOutputs does exactly this), but the frontend re-implements it.  
**Need:** A `dynamic_output_source` field on NodeDef to tell the frontend which config path to traverse for dynamic port names.

**Proposed addition:**
```go
// DynamicOutputSource, when set, tells the frontend which config field path
// contains the list of output variable names. Used when dynamic_outputs=true.
// Format: "functions[].output_var" (JSONPath-like notation interpreted by frontend).
// Frontend uses this to derive port names generically without per-type conditionals.
DynamicOutputSource string `json:"dynamic_output_source,omitempty"`
```

### 5.5 Connection Validity Rules (allowed source-port → target-port pairs)
**Current:** `allowed_successors` lists valid next-hop node types. Nothing at the port level.  
**Gap:** No way to express "this output port can only connect to input ports of type X".  
**Assessment:** For the current node set, this is NOT needed — all connections are semantically valid if the node type is allowed. The `type_hint` on PortDef is informational but not enforced.  
**Verdict:** Skip for now. Add when needed.

### 5.6 Port Ordering / Grouping
**Current:** `InputPorts` and `OutputPorts` are arrays — order is implicit (registration order).  
**Gap:** No explicit ordering field, no grouping.  
**Assessment:** Array order in Go maps is non-deterministic for the registry (fixed at AllNodeTypeInfos time). The NodeTypeInfo serialised slice maintains the order from `nodes.go` registration. This is sufficient for static ports. For dynamic ports, order follows DeriveOutputs output order (also stable for current nodes).  
**Verdict:** `PortDef.Order int` is not needed yet — array index is sufficient.

---

## 6. Minimal Backend Schema Extension Proposal

Three small additions to `PortDef` and `NodeDef`. All backwards-compatible (omitempty).

### PortDef extension (in `noderegistry.go`)
```go
type PortDef struct {
    ID             string `json:"id"`
    Label          string `json:"label"`
    Required       bool   `json:"required"`
    Multi          bool   `json:"multi,omitempty"`
    TypeHint       string `json:"type_hint,omitempty"`
    // NEW: max connections on this specific port (0 = unlimited)
    MaxConnections int    `json:"max_connections,omitempty"`
    // NEW: accent color override for this port (overrides node color for the handle)
    Color          string `json:"color,omitempty"`
}
```

### NodeDef extension (in `noderegistry.go`)
```go
// NEW: Named control-flow output ports. Populated only for nodes with multiple
// control paths (branch). Empty means single anonymous control output.
ControlOutputPorts []PortDef `json:"control_output_ports,omitempty"`

// NEW: JSONPath-like expression to derive output port names from step config.
// Only meaningful when dynamic_outputs=true.
// Example: "functions[].output_var" → iterate cfg.functions, collect output_var values.
DynamicOutputSource string `json:"dynamic_output_source,omitempty"`
```

### nodes.go registration changes
For `branch`:
```go
ControlOutputPorts: []PortDef{
    {ID: "true",  Label: "True path",  Color: "#4ade80"},
    {ID: "false", Label: "False path", Color: "#f87171"},
},
```
For `transform`:
```go
DynamicOutputSource: "functions[].output_var",
```

### What this replaces
- `if data.step_type === 'branch'` in StepNode.tsx → read `control_output_ports` instead
- `dynOutputs && transformOutputs.length > 0` → read `dynamic_output_source` + `dynamic_outputs`
- `computeFinalOutputs()` helper in StepNode.tsx → generic `resolvePortsFromConfig(nodeDef, cfg)` using the source path

---

## 7. Frontend Model Recommendation

### Port resolution (generic)

```typescript
interface ResolvedPort {
  id: string;        // stable handle ID
  label: string;     // truncated to ~7-8 chars for display
  kind: 'control' | 'data';   // determines handle color and edge type
  direction: 'in' | 'out';
  color?: string;    // port-specific color override
  required?: boolean;
}

function resolveInputPorts(nodeDef: NodeDef, cfg: Record<string, unknown>): ResolvedPort[] {
  // 1. Dynamic input ports from node data (committed bindings)
  // 2. Static registry input_ports
  // Both yield data-kind ports.
  // Control-in: always one anonymous port (unless is_source).
}

function resolveOutputPorts(nodeDef: NodeDef, cfg: Record<string, unknown>): ResolvedPort[] {
  // 1. control_output_ports (if any) → control-kind ports
  // 2. If no control_output_ports and not is_sink: one anonymous control-out
  // 3. If dynamic_outputs && dynamic_output_source: evaluate path against cfg → data-kind ports
  // 4. static output_ports → data-kind ports
}
```

### Handle IDs (stable, backwards-compatible)
```
Control target:  "ctrl-in"           (anonymous)
Control source:  "ctrl-out"          (anonymous, or "ctrl-out-{portID}" for named control ports)
Data target:     "data-in-{portID}"  (matches existing format — no migration needed)
Data source:     "data-out-{portID}" (matches existing format — no migration needed)
```

For `branch`, the two control output handles become `"ctrl-out-true"` and `"ctrl-out-false"`.  
For `transform`, data output handles remain `"data-out-{varName}"` (unchanged).

### Edge type determination
```typescript
function classifyEdge(e: Edge): 'control' | 'data' {
  return e.data?.kind === 'data'
    || (e.sourceHandle?.startsWith('data-out-') && e.targetHandle?.startsWith('data-in-'))
    ? 'data' : 'control';
}
```

---

## 8. How Bundled Edges + Dynamic Ports Work with React Flow

### Port-rail / bundled-cable design
The image shows source ports stacked vertically on the right side of the source node and target ports stacked vertically on the left side of the target node, with fan-out wires converging on a bundle count badge.

**Implementation:**

1. **Port-rail handles:** Each data output port on a source node gets a Handle at a calculated Y-position in a rail on the right edge. Each data input port on a target node gets a Handle in a rail on the left edge. Port positions use `top: PORT_START + idx * PORT_STEP`.

2. **Bundle edge component:** When N edges share the same source+target node pair, a custom React Flow edge (`BundleEdge`) renders:
   - Individual fan-out lines from source port positions to a center convergence point
   - A count badge at the midpoint
   - Individual fan-in lines from convergence to target port positions
   - On hover: expand to show individual wires with port labels

3. **useUpdateNodeInternals:** Call this hook whenever the resolved port list changes (config edit, dynamic port add/remove). This is mandatory — React Flow caches handle positions and renders stale connection paths without it.

```typescript
const updateNodeInternals = useUpdateNodeInternals();
useEffect(() => {
  updateNodeInternals(nodeId);
}, [portList.length, portList.map(p=>p.id).join(',')]);
```

4. **Bundle detection in edge rendering:**
```typescript
// Group edges by (source, target) pair
const edgeGroups = groupBy(edges, e => `${e.source}::${e.target}`);
// In edgeTypes, BundleEdge receives the group as data
```

React Flow does not natively support bundled edges. We render them by:
- Using a custom edge component that reads `data.bundleIndex` and `data.bundleTotal`
- Computing the convergence point as the midpoint between source node right edge and target node left edge
- Drawing bezier curves from each source port to the convergence, then from convergence to each target port

5. **Bundle expand on hover:** The bundle edge component tracks hover state. When hovered, individual wires spread out with full labels; when not hovered, they converge into a tight bundle with the count badge.

### Dynamic port changes without re-running Dagre
- Dagre assigns positions to **nodes**, not handles
- Adding a port only changes handle positions WITHIN a node (CSS absolute positioning inside the node div)
- Dagre must NOT re-run on port changes — it runs only on: initial load, explicit "Auto Layout" button press, skill-switch
- Handle positions update via `useUpdateNodeInternals()` — this is React Flow internal only, not a layout operation
- Node size must grow to accommodate more ports: either use auto-resize with `NodeResizer` or compute `minHeight = PORT_START + portCount * PORT_STEP + FOOTER_PX` and apply it to the node div style

---

## 9. Dagre Impact

### Current Dagre usage (page.tsx:310-324)
```javascript
g.setNode(n.id, { width: 120, height: 80 });  // hardcoded dimensions
```

**Problem:** Dagre uses fixed 120×80 for ALL nodes regardless of actual rendered size or port count. With multi-port nodes that may be 200px tall, this creates edge routing collisions.

**Recommendation:**
- Compute estimated height per node before layout: `estimateNodeHeight(nodeDef, cfg, portCount)`
- Pass dynamic dimensions to Dagre: `g.setNode(n.id, { width: 140, height: estimatedH })`
- Dagre runs ONLY on: initial load (skill enter), explicit "Auto Layout" press
- Never re-run Dagre on port add/change — only call `useUpdateNodeInternals(nodeId)`

### Minimum viable Dagre fix
```typescript
function estimateNodeHeight(nodeDef: NodeDef, cfg: Record<string, unknown>): number {
  const inputPorts = resolveInputPorts(nodeDef, cfg);
  const outputPorts = resolveOutputPorts(nodeDef, cfg);
  const portCount = Math.max(inputPorts.length, outputPorts.length);
  return Math.max(80, PORT_START + portCount * PORT_STEP + 20);
}
```

---

## 10. Summary — What to Change vs What to Keep

### Backend (Go) — small additions only
- Add `ControlOutputPorts []PortDef` to `NodeDef` + `NodeTypeInfo`
- Add `DynamicOutputSource string` to `NodeDef` + `NodeTypeInfo`
- Add `Color string` and `MaxConnections int` to `PortDef`
- Populate `ControlOutputPorts` on `branch` node
- Set `DynamicOutputSource: "functions[].output_var"` on `transform` node
- Update `ToInfo()`, `AllNodeTypeInfos()` — trivial
- Zero compiler/interpreter changes needed

### Frontend — what to add
- `resolveInputPorts` / `resolveOutputPorts` generic helpers (driven by NodeDef)
- `BundleEdge` custom edge component
- `useUpdateNodeInternals()` call in StepNode on port list changes
- Bundle grouping logic in ReactFlow edge pass
- Dynamic height estimation for Dagre

### Frontend — what to REMOVE (hardcodes to eliminate)
- `if (data.step_type === 'branch')` block in StepNode → replace with generic `control_output_ports` rendering
- `dynOutputs && transformOutputs.length > 0` block in StepNode → replace with generic `dynamic_output_source` rendering
- `computeFinalOutputs()` helper → replace with generic port resolver
- `isBranch`/`isTransform` checks in buildDefinitionDoc and loadDefinitionDoc → replace with `control_output_ports` presence check
- `nodeVars.ts` long-term: post-validate, use `StepContracts` from backend instead of re-deriving

### What MUST NOT change
- Handle ID formats: `data-in-{portID}`, `data-out-{portID}` — existing canvas JSON has these embedded
- The two-tier edge system (control vs data) — the semantic distinction is real and correct
- Dagre running on skill-switch — it must still run to lay out newly loaded nodes
- `canAddIncoming`/`canAddOutgoing` helpers — they correctly read backend EdgeRules

---

## 11. Duplication Risk Assessment

| Area | Current state | Risk |
|---|---|---|
| `nodeVars.ts` extractNodeVars | Full re-implementation of DeriveInputs/DeriveOutputs in TS | HIGH — any Go interpreter change needs parallel TS update |
| StepNode branch/transform blocks | 3-way type switch for handle rendering | MEDIUM — breaks on any new multi-output node type |
| page.tsx isBranch/isTransform | Scattered in buildDefinitionDoc and loadDefinitionDoc | MEDIUM — fragile round-trip serialisation |
| Debug execution (executeStep) | Complete node-by-node re-implementation in browser | LOW — acceptable: debug mode is an approximation, not production |
| constants.ts STEP_TYPES | Hardcoded palette list | LOW — informational only, easily extended |

**The `nodeVars.ts` duplication is the highest-priority structural debt.** The `StepContracts` response from the validate endpoint already provides compiled input/output lists per step — once the validate response is cached, `extractNodeVars()` can be replaced by reading `stepContracts[stepId]` directly. This removes the entire parallel Go/TS derivation and makes the `READS/WRITES` panel 100% backend-authoritative for post-validate state (already partially done in RightPanel for the validate path).
