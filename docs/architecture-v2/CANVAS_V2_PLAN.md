# Canvas V2 — Application Builder Rebuild Plan

**Status:** Planning (Phase E)
**Author:** Architect session, 2026-08-17
**Scope:** Rebuild the ReactFlow canvas in `frontend/src/app/admin/applications/page.tsx` on top of the v2 `AppDefinitionDoc` model. Replace the current form-based `DefinitionView` center panel with the visual canvas experience while reusing its API wiring.

> This is a frontend-only plan **plus one small backend gap that must be closed** (canvas layout persistence — see §4 and §11). No orchestration or Temporal changes.

---

## 0. Ground truth (verified against code, 2026-08-17)

Read and confirmed:

- `page.tsx` — node components (EntryPointNode/OrchestratorNode/AgentNode/MiddlewareNode) at lines 335–541; `NODE_TYPES` map at 536; `makeId()` at 551; `applyDagreLayout` at 557; `buildNodesFromApp` (OLD loader) at 573; `NodeLibrary` at 717; `PropertiesPanel` at 956; `CanvasLogo`/`LogoState` at 1865–2006; `AdvisorPanel` at 2104; `CanvasInner` at 2262; connection-rule engine (`NODE_PORTS`, `validateConnection`, `CANVAS_RULES`, `styledEdges`) at 2433–2680; `DefinitionView` at 2729; `ApplicationsPage` at 4829.
- `lib/api.ts` — v2 types at 404–485 (`ComponentDefinitionSummary`, `DefinitionRef`, `ComponentInstance`, `EPInstance`, `ConnectionDef`, `AppDefinitionDoc`, `AppDefinition`, `ValidationReport`), API methods at 585–600 (`listComponentDefinitions`, `listDefinitions`, `createDefinition`, `updateDefinition`, `deleteDefinition`, `validateDefinition`, `publishDefinition`). `CanvasLayout` type at 217. `Application.canvas` at 237.
- `go/internal/admin/service/publish.go` — validator + compiler. Reads orchestrator config keys: `max_iterations` (def 10), `max_parallel_tools` (def 5), `history_window` (def 20), `llm_provider`, `llm_model`, `system_prompt`, `budget_tokens`. `delegatable` derived from being a **target** of a `delegation` connection. `AllowedAgentIDs` derived from `tool` connections (source=orch → target=agent). `EPInstance.Root` → `app_orchestrators` UUID via `orchUUIDs[ep.Root]`. Only `definition_ref.Kind == orchestrator` components compile into `app_orchestrators`.
- `go/internal/admin/dal/registry.go` — `ComponentDefinitionSummary{ID, Kind, Namespace, Name, Version, DisplayName, Description, ImplementationType, Scope, Status, Enabled}`.

### Backend gap (CRITICAL — see §11)
`go/internal/admin/applications.go` `Update` (line 101) reads only `Name` + `Enabled` from `ApplicationInput`. **`app.canvas` is silently dropped on PATCH.** The current OLD builder relied on the Python bridge to persist `canvas`, but applications routes are now Go-owned (`docker-compose.yml:1006`, route ownership inventory lines 110–112). Canvas layout persistence through `PATCH /admin/applications/{id}` **does not work today on Go**. The plan defers layout persistence (uses auto-layout on every open) for v1 and specifies the exact backend change needed to enable it later.

---

## 1. Architecture decision — Node ID strategy

### Decision
Node IDs **are** `instance_id`. There is no separate ReactFlow-id vs instance-id. The ReactFlow `Node.id` === `ComponentInstance.instance_id` (or `EPInstance.instance_id`). This keeps serialization trivial (no id-translation table) and means validation errors (keyed by `instance_id`) map 1:1 onto canvas nodes for red-ring highlighting.

Rationale: `instance_id` is meaningful in v2 — it is the `EPInstance.root` foreign key, the connection `source`/`target`, the `app_orchestrators.instance_id` / `.name` stable Temporal key, and the key in `ValidationError.instance_id`. Reusing it as the node id removes an entire class of mapping bugs.

### Format
Human-readable, prefix by kind + a monotonic per-kind counter, plus a slug fragment for agents/middleware so IDs stay legible in validation output:

```
orch_1, orch_2, ...
agent_<defname>_1        e.g. agent_vision_1
mw_<defname>_1           e.g. mw_ratelimit_1
ep_<protocol>_1          e.g. ep_websocket_1, ep_sse_2
```

Where `<defname>` = `ComponentDefinitionSummary.name` sanitized to `[a-z0-9_]` (lowercase, non-alnum → `_`, collapse repeats, trim). For orchestrators the def name is generic ("standard") so we omit it and use `orch_N`.

### Generation + uniqueness
Add a single generator that scans the current draft for collisions:

```ts
// Signature
function genInstanceId(
  kind: 'orchestrator' | 'agent' | 'middleware' | 'ep',
  defName: string | undefined,   // ComponentDefinitionSummary.name (or protocol for ep)
  existingIds: Set<string>,       // all instance_ids currently on the canvas
): string
```

Algorithm:
1. Build the base: `orchestrator → 'orch'`; `ep → 'ep_' + protocol`; `agent → 'agent_' + sanitize(defName)`; `middleware → 'mw_' + sanitize(defName)`.
2. Starting at `n = 1`, return `${base}_${n}` for the first `n` where `${base}_${n} ∉ existingIds`.

`existingIds` is computed at drop time from `new Set([...draft.components.map(c=>c.instance_id), ...draft.entry_points.map(e=>e.instance_id)])`. Because components + entry points share one namespace (Go validator enforces uniqueness across both — publish.go:129/173), the generator must check against the union.

> Replaces `makeId()` (page.tsx:551) and the `${cd.kind}_${Date.now()}` / `ep_${Date.now()}` generators in the current `DefinitionView` (page.tsx:2855, 2868), which are opaque and (Date.now collision) technically non-unique on fast double-add.

`instance_id` is **immutable after creation**. The properties panel shows it read-only.

---

## 2. Node type → v2 mapping (exact)

There are 4 canvas node types. Three map to `ComponentInstance` (orchestrator/agent/middleware), one maps to `EPInstance` (entryPoint). The ReactFlow node keeps a small `data` payload; the authoritative record is always the `AppDefinitionDoc` reconstructed by `canvasToDoc` (§4). To avoid drift, **the node `data` carries the full instance object plus render-only fields**, and `canvasToDoc` reads straight from it.

Node `data` shape (new — replaces the OLD `OrchestratorData`/`AgentData`/etc.):

```ts
type CanvasNodeData =
  | ({ _kind: 'orchestrator' } & OrchNodeData)
  | ({ _kind: 'agent' }        & AgentNodeData)
  | ({ _kind: 'middleware' }   & MwNodeData)
  | ({ _kind: 'ep' }           & EpNodeData);

// render/validation-only fields present on every node
interface NodeCommon { _error?: boolean; _shake?: boolean; _errorMsg?: string; }
```

### 2.1 Orchestrator node  (`type: 'orchestrator'`)
- Maps to: `ComponentInstance` with `definition_ref.kind === 'orchestrator'`.
- `definition_ref` populated from the orchestrator `ComponentDefinitionSummary` (see §11): `{ kind:'orchestrator', namespace, name, version }`; `definition_id = cd.id`.
- `ComponentInstance.name` = the `instance_id` (v2 uses instance_id as the stable Temporal key; publish.go:301 sets `Name = InstanceID`). Set `name === instance_id` at create time.
- `config` (keys the Go compiler reads — publish.go:310–320): `system_prompt`, `llm_provider`, `llm_model`, `max_iterations`, `max_parallel_tools`, `history_window`, `budget_tokens`. Plus voice keys carried but **not compiled by publish.go today** (see §6 "defer"): `transcription_provider`, `transcription_model`, `tts_provider`, `tts_voice`. Store them in config so they round-trip; they become active when the compiler learns them.
- `delegatable` is **not** stored in config — it is derived on publish from being the target of a `delegation` edge. The panel toggle (§5) exists only to make the node a delegation target; toggling it ON with no incoming delegation edge is a no-op at publish. (Design note: v1 renders `delegatable` from "is target of any delegation edge" so the toggle and the canvas stay consistent — see §5.)
- Node `data` (`OrchNodeData`): `{ instance_id, display_name, definition_ref, definition_id, config: {system_prompt, llm_provider, llm_model, max_iterations, max_parallel_tools, history_window, budget_tokens, transcription_provider?, transcription_model?, tts_provider?, tts_voice?} }`.
- `display_name` is a **UI-only** label (v2 doc has no per-orch display name field on ComponentInstance). Store it in `config.display_name` so it round-trips through save/publish; publish.go ignores unknown keys.
- Reconstruction from doc: for each `component` with `definition_ref.kind==='orchestrator'`, create node `{ id: c.instance_id, type:'orchestrator', data:{ _kind:'orchestrator', instance_id:c.instance_id, display_name: (c.config.display_name as string) ?? c.instance_id, definition_ref:c.definition_ref, definition_id:c.definition_id, config:c.config } }`.

### 2.2 Agent node  (`type: 'agent'`)
- Maps to: `ComponentInstance` with `definition_ref.kind === 'agent'`.
- `definition_ref`/`definition_id` from the agent `ComponentDefinitionSummary`. The palette entry IS a `ComponentDefinitionSummary` (kind `agent`), not a raw `Agent` row — v2 agents are component definitions.
- `config`: agent config overrides (default `{}`). `secret_bindings` optional.
- Node `data` (`AgentNodeData`): `{ instance_id, display_name, description, definition_ref, definition_id, config, secret_bindings? , icon? }`. `display_name`/`description`/`icon` are read-only, copied from the `ComponentDefinitionSummary` at drop time (registry is the source of truth). They are NOT written back to the doc (they live only on the node for rendering); on reconstruction they are re-derived by matching `definition_id`/`definition_ref` against the loaded `componentDefs`.
- Reconstruction: for each `component` with kind `agent`, look up its `ComponentDefinitionSummary` in `componentDefs` (match by `definition_id === cd.id`, fallback to `ref` tuple match), copy `display_name`/`description` for render; `config`/`secret_bindings` from the instance.

### 2.3 Middleware node  (`type: 'middleware'`)  — DEFERRED to v1.1 (see §6)
- Maps to: `ComponentInstance` with `definition_ref.kind === 'middleware'`.
- `config` = config override (default `{}`).
- Node `data` (`MwNodeData`): `{ instance_id, display_name, definition_ref, definition_id, config }`. `display_name` read-only from registry.
- Reconstruction: same pattern as agent.
- NOTE: publish.go only compiles `kind==='orchestrator'` into `app_orchestrators`; middleware instances are carried in the doc and validated (registry resolution + uniqueness) but not projected. Middleware chaining rules are out of scope for v1.

### 2.4 Entry Point node  (`type: 'entryPoint'`)
- Maps to: `EPInstance` `{ instance_id, slug, protocol, root }`. **No `definition_ref`** — entry points are not registry components.
- `protocol` ∈ `websocket|sse|webrtc|a2a|voice` (matches Go validProtocols publish.go:165 and api.ts EPInstance union).
- `root` = the `instance_id` of the orchestrator connected via the EP→Orch edge. Not editable in the panel; set by drawing the edge (§3).
- `slug` editable, validated `^[a-z0-9_-]{1,64}$` (existing EP_SLUG_FORMAT rule, page.tsx:2538).
- Node `data` (`EpNodeData`): `{ instance_id, slug, protocol, label }`. `label` is a UI-only display string (keep existing `EP_META[protocol].title` default). `root` is NOT stored in node data — it is derived from edges at serialization time so the edge is the single source of truth.
- Reconstruction: for each `ep` in `doc.entry_points`, create node `{ id: ep.instance_id, type:'entryPoint', data:{ _kind:'ep', instance_id:ep.instance_id, slug:ep.slug, protocol:ep.protocol, label: EP_META[ep.protocol]?.title ?? ep.protocol } }`, then create the EP→root edge from `ep.root` (§4).

---

## 3. Edge type → v2 mapping (exact)

ReactFlow edges are typed by the pair of node types they connect. We do NOT store an explicit edge kind on the edge object beyond what's derivable; instead `canvasToDoc` classifies each edge by its endpoints' node types.

| Canvas edge (source → target) | v2 target | Notes |
|---|---|---|
| `entryPoint` → `orchestrator` | sets `EPInstance.root = orchestrator.instance_id` | **NOT** added to `connections[]`. There is at most one such edge per EP. |
| `orchestrator` → `agent` | `ConnectionDef { source: orch.id, target: agent.id, type: 'tool' }` | compiles to `allowed_agent_ids` (publish.go:287). |
| `orchestrator` → `orchestrator` | `ConnectionDef { source, target, type: 'delegation' }` | target orch becomes `delegatable` (publish.go:279). |

> The Go validator/compiler never emits or requires a `type:'entry'` ConnectionDef — EP→root is expressed purely via `EPInstance.root`. (`ConnectionDef.type` still allows `'entry'` in the TS union, but the canvas does not produce it. Leave it out.)

### Connection rules to enforce (extend the existing `NODE_PORTS` engine, page.tsx:2442)
Rewrite `NODE_PORTS` for v2 semantics:

```ts
const NODE_PORTS = {
  entryPoint:   { accepts: [],               emits: ['entry'],  maxOutgoing: 1 },
  orchestrator: { accepts: ['entry','deleg'], emits: ['tool','deleg'] },
  agent:        { accepts: ['tool'],          emits: [],         maxIncoming: undefined },
  middleware:   { accepts: [],                emits: [] },       // v1: not connectable
};
```

Valid connections (enforced in `onConnect` via `validateConnection`, page.tsx:2450):
- `entryPoint → orchestrator` — allowed. `maxOutgoing:1` on EP means each EP has exactly one root. Re-drawing replaces (delete old edge first) — see below.
- `orchestrator → agent` — allowed (tool). No cardinality cap.
- `orchestrator → orchestrator` — allowed (delegation). Reject self-loop (`source === target`). Reject if it would create a delegation cycle (optional for v1; at minimum block direct A→B + B→A duplication via the existing duplicate-edge check page.tsx:2465).
- Everything else — rejected with a toast (reuse the existing shake/error-ring: set `_shake` on the source node for 600ms).

### EP→Orch edge is single (replace semantics)
When the user draws a second EP→Orch edge from an EP that already has one, `validateConnection` returns the "already has an orchestrator" message (existing `maxOutgoing` path). v1 behavior: reject the new edge (user must delete the old one first). This matches current UX and keeps `EPInstance.root` unambiguous.

### Deleting an EP→Orch edge
Because `root` is derived from edges (not stored on the node), **deleting the edge automatically clears `root`** in the next `canvasToDoc` pass — no extra bookkeeping. Just remove the edge from `edges` state and mark dirty. The EP node then fails the `EP_HAS_ORCH` rule (page.tsx:2545) and shows a red ring until reconnected.

### Deleting a node
On node delete (reuse `deleteNodeRef` mechanism, page.tsx:67): remove the node AND all edges touching it. If an orchestrator that was some EP's root is deleted, that EP's `root` clears automatically (edge gone).

---

## 4. Canvas ↔ AppDefinitionDoc serialization

### 4.1 `canvasToDoc`

```ts
function canvasToDoc(nodes: Node[], edges: Edge[], name?: string): AppDefinitionDoc
```

Algorithm:
1. `components: ComponentInstance[] = []`, `entry_points: EPInstance[] = []`, `connections: ConnectionDef[] = []`.
2. Build `rootByEp: Map<epId, orchId>` from edges where `source.type==='entryPoint' && target.type==='orchestrator'` (take the first if somehow >1 — cardinality guarantees at most one).
3. For each node:
   - `orchestrator`: push `{ instance_id: id, name: id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: { ...d.config, display_name: d.display_name } }`.
   - `agent`: push `{ instance_id: id, definition_ref: d.definition_ref, definition_id: d.definition_id, config: d.config, secret_bindings: d.secret_bindings }` (omit `secret_bindings` if empty).
   - `middleware` (v1.1): push `{ instance_id: id, definition_ref, definition_id, config }`.
   - `entryPoint`: push `{ instance_id: id, slug: d.slug, protocol: d.protocol, root: rootByEp.get(id) ?? '' }`.
4. For each edge, classify by endpoint node types:
   - `entryPoint→orchestrator`: skip (already captured in `root`).
   - `orchestrator→agent`: `connections.push({ source, target, type:'tool' })`.
   - `orchestrator→orchestrator`: `connections.push({ source, target, type:'delegation' })`.
5. Return `{ schema_version: 2, name, components, entry_points, connections }`.

> Order of `components`/`entry_points` is not significant to the Go validator (it maps by id). Keep a stable order (nodes creation order) for diff-friendliness.

### 4.2 `docToCanvas`

```ts
function docToCanvas(
  doc: AppDefinitionDoc,
  componentDefs: ComponentDefinitionSummary[],
  layout: Record<string, { x: number; y: number }>,   // keyed by instance_id; {} if none
): { nodes: Node[]; edges: Edge[] }
```

Algorithm:
1. `defById = new Map(componentDefs.map(cd => [cd.id, cd]))`; also a ref-tuple index for fallback: key `${kind}:${namespace}:${name}:${version}`.
2. `nodes = []`, `edges = []`.
3. For each `component`:
   - resolve its `cd` (by `definition_id` then ref-tuple) for `display_name`/`description`/`icon` render fields (may be undefined if the def was removed — fall back to `instance_id`).
   - map `definition_ref.kind` → node `type` (`orchestrator`/`agent`/`middleware`).
   - push node with `data` per §2, `position: layout[c.instance_id] ?? {x:0,y:0}` (dagre fills in later).
   - if orchestrator: `display_name` from `c.config.display_name ?? cd?.display_name ?? c.instance_id`.
4. For each `ep`:
   - push entryPoint node (`position: layout[ep.instance_id] ?? {x:0,y:0}`).
   - if `ep.root`: push edge `{ id: 'e_'+ep.instance_id+'_'+ep.root, source: ep.instance_id, target: ep.root, type:'default' }`.
5. For each `connection`:
   - `type:'tool'` or `type:'delegation'` → push edge `{ id: 'e_'+source+'_'+target, source, target }`. (`type:'entry'` never appears, but if present, skip — it's redundant with root.)
6. If `Object.keys(layout).length === 0`, run `applyDagreLayout(nodes, edges)` (page.tsx:557, unchanged) and use its positions. Otherwise use stored positions.
7. Return `{ nodes, edges }`.

### 4.3 Layout storage
- Layout positions are **NOT** part of `AppDefinitionDoc` (the doc is the semantic contract; positions are cosmetic). Store them on `Application.canvas.layout` keyed by `instance_id`:
  ```ts
  app.canvas = { layout: { [instance_id]: {x, y}, ... } }   // CanvasLayout, api.ts:217
  ```
- **v1 does NOT persist layout** (see §6 defer + §11 backend gap). On every open, `docToCanvas` receives `layout = {}` and dagre auto-arranges. This is acceptable and matches the OLD builder's "no saved positions → dagre" fallback (page.tsx:690).
- **v1.1 (after backend fix):** persist layout by calling `PATCH /admin/applications/{id}` with `{ canvas: { layout } }` on save/blur, once the Go `Update` handler accepts+stores `canvas` (§11). Save layout at the same moment as `saveDraft()` (debounced), reading positions from ReactFlow node state.

---

## 5. Properties Panel redesign

The right-hand panel replaces the OLD `PropertiesPanel` (page.tsx:956). It renders per `selectedNode.type`. Every edit calls `onUpdateNode(id, patch)` which mutates node `data` and sets `isDirty = true`. `instance_id` is read-only everywhere.

Shared field style: reuse `fieldStyle` from `DefinitionView` (page.tsx:2751).

### Orchestrator panel
| Field | Control | Writes to |
|---|---|---|
| Instance ID | read-only text | — |
| Display name | text input | `data.display_name` (→ `config.display_name` on serialize) |
| System prompt | textarea (6 rows) | `data.config.system_prompt` |
| LLM provider | select (from providers list, reuse existing provider fetch) | `data.config.llm_provider` |
| LLM model | text input | `data.config.llm_model` |
| Max iterations | number input (default 10) | `data.config.max_iterations` |
| Max parallel tools | number input (default 5) | `data.config.max_parallel_tools` |
| History window | number input (default 20) | `data.config.history_window` |
| Budget tokens | number input (nullable) | `data.config.budget_tokens` |
| Delegatable | read-only badge: "Delegation target" if this node is the target of a delegation edge; else "Not delegatable" | derived from edges (see §2.1). No stored toggle in v1. |

> Rationale for read-only delegatable: `delegatable` is computed at publish from delegation edges (publish.go:277). A separate stored toggle would desync from the canvas. Show it as a derived indicator.

Voice fields (`transcription_provider`, `transcription_model`, `tts_provider`, `tts_voice`) — **deferred** (§6). When added, they write to `data.config.*` and round-trip.

### Agent panel
| Field | Control | Source |
|---|---|---|
| Instance ID | read-only | — |
| Display name | read-only text | registry `cd.display_name` |
| Description | read-only text | registry `cd.description` |
| Config overrides | key/value editor OR raw JSON textarea | `data.config` |
| Secret bindings | key/value editor (name → secret ref) | `data.secret_bindings` |

For v1, a JSON textarea for `config` is acceptable (validate JSON on blur; revert + toast on parse error). Key/value editor is a v1.1 nicety.

### Middleware panel (v1.1)
| Field | Control | Source |
|---|---|---|
| Instance ID | read-only | — |
| Display name | read-only | registry |
| Config override | JSON textarea | `data.config` |

### Entry Point panel
| Field | Control | Writes to |
|---|---|---|
| Instance ID | read-only | — |
| Slug | text input, validated `^[a-z0-9_-]{1,64}$` (live), amber border if invalid | `data.slug` |
| Protocol | select (`websocket|sse|webrtc|a2a|voice`) | `data.protocol` |
| Root orchestrator | read-only text: shows connected orch's `display_name` or "— draw an edge from this entry point to an orchestrator" | derived from edges |

### Save wiring
`onUpdateNode(id, patch)`:
```ts
setNodes(ns => ns.map(n => n.id === id ? { ...n, data: { ...n.data, ...patch } } : n));
setIsDirty(true);
```
For nested config edits, patch the whole `config` object: `onUpdateNode(id, { config: { ...node.data.config, max_iterations: v } })`.

---

## 6. Must-have vs deferred for canvas v1

### MUST HAVE (v1)
1. Drag orchestrator/agent from palette (NodeLibrary) onto canvas → creates node with generated `instance_id` and `definition_ref` from the `ComponentDefinitionSummary`.
2. Drag entry-point protocols onto canvas → creates EP node.
3. Draw edges with §3 connection rules enforced (EP→Orch single, Orch→Agent tool, Orch→Orch delegation; invalid rejected with shake).
4. Properties panel for orchestrator / agent / entry point (§5).
5. **Save draft**: `canvasToDoc(nodes, edges, draft.name)` → `updateDefinition(app.id, activeDef.id, { definition })`.
6. **Validate**: `validateDefinition` → map `ValidationReport.errors[].instance_id` onto nodes (`_error` + `_errorMsg`), show banner (reuse DefinitionView banner page.tsx:3002).
7. **Publish**: `publishDefinition`, gated on `!isDirty && validationReport?.valid`.
8. **Load** existing definition via `docToCanvas` (with dagre auto-layout).
9. **Revision selector** dropdown (reuse DefinitionView's, page.tsx:3078) + **New Draft** button.
10. **Auto-layout (dagre)** on first open and via toolbar button (`applyDagreLayout`, page.tsx:557, `CanvasInner` already has the button at 2361).
11. **CanvasLogo** wired to canvas state (§7).
12. Node + edge deletion (delete key / node ✕ / edge double-click — `CanvasInner` already wires `onEdgeDoubleClick` at 2408 and `deleteNodeRef` at 67).

### DEFER (v1.1+)
- **AI Advisor panel** (`AdvisorPanel` page.tsx:2104) — keep the dead code; do not wire. Needs backend advisor endpoint + proposal-apply against `config`.
- **Voice/STT/TTS orchestrator fields** — publish.go does not compile them yet; carry in config but hide in UI.
- **MiniMap** — `CanvasInner` renders it (2417); keep or hide behind a toggle, low priority.
- **Canvas layout persistence** — v1 always auto-layouts (§4.3 + §11 backend gap).
- **Middleware nodes** — palette entry + node exist but defer connectable middleware chains.
- **Delegatable as an editable toggle** — v1 shows it derived-only.
- Key/value config editors (JSON textarea suffices for v1).

---

## 7. CanvasLogo state mapping

`LogoState` = `'idle'|'dirty'|'error'|'success'|'thinking'|'warning'` (page.tsx:1865). Compute a single `logoState` in the canvas container from these inputs: `{ activeDef, draft loaded, isDirty, validating, saving, publishing, validationReport }`.

```ts
function computeLogoState(s: {
  loaded: boolean; isDirty: boolean; busy: boolean;      // validating||saving||publishing
  lastResult: 'none'|'valid'|'invalid'|'warn';           // set after validate/publish returns
}): LogoState {
  if (s.busy) return 'thinking';
  if (s.lastResult === 'invalid') return 'error';
  if (s.lastResult === 'warn')    return 'warning';
  if (s.lastResult === 'valid')   return 'success';   // caller resets to idle after ~1.8s
  if (!s.loaded)  return 'idle';
  if (s.isDirty)  return 'dirty';
  return 'idle';
}
```

Transitions:
- No definition loaded → `idle`.
- Draft loaded, no changes → `idle`.
- User edits (node add/move/edit, edge add/remove) → `dirty`.
- validate/save/publish in flight → `thinking`.
- validate returns valid → `success`, then a `setTimeout(1800ms)` sets `lastResult='none'` → back to `idle`/`dirty`.
- validate returns errors → `error` (persists until next edit clears `lastResult` to `'none'`).
- validate returns valid-with-client-warnings (from local `CANVAS_RULES` warn-severity, page.tsx:2555) → `warning`.

Client-side warnings: run `runRules(nodes, edges, 'save')` (page.tsx:2615) locally; if it returns `warnings.length>0` and server says valid, prefer `warning`. Note the OLD `CANVAS_RULES` reference `EntryPointData.epType`/`OrchestratorData.transcriptionProvider` — update those rule bodies to read the new node `data` shape (`d.protocol`, `d.config.transcription_provider`) as part of Step 4 below.

`success` uses the explode animation (page.tsx:1988) — a nice publish/validate-pass moment.

---

## 8. Implementation sequence (atomic, each commits green)

Each step keeps the app compiling and the page working. Tests: this is a client component with no unit-test harness in the repo for `page.tsx`; verification is `npm run build` (typecheck) + manual smoke through Traefik. Where the plan touches Go (Step 8), follow `go/CLAUDE.md` test rules.

**Step 1 — Serialization core (pure functions, no UI change).**
Add `genInstanceId`, `canvasToDoc`, `docToCanvas`, `sanitize`, and the new `CanvasNodeData` types near the existing helpers (page.tsx ~550). Add a `NEW_NODE_TYPES` map only if node components change; initially reuse existing node components. No behavior change yet. Typecheck passes.

**Step 2 — New `CanvasBuilderView` component skeleton.**
Create `CanvasBuilderView` (new function in page.tsx) that copies `DefinitionView`'s state + API wiring verbatim (`defs, activeDef, draft, isDirty, componentDefs, validating, publishing, saving, validationReport, toast`) and its functions (`loadDef, reloadDefs, newDraft, saveDraft, validate, publish`, §9). Render the SAME 3-column DefinitionView body for now (so nothing breaks), but drive it from the new component. Switch `ApplicationsPage` `view==='definition'` (page.tsx:4945) to render `CanvasBuilderView`. Commit — identical behavior, new host.

**Step 3 — Swap center panel to ReactFlow canvas.**
Inside `CanvasBuilderView`, replace the center column with `<ReactFlowProvider><CanvasInner .../></ReactFlowProvider>`. Wire `nodes/edges` via `useNodesState/useEdgesState`. On `loadDef`, run `docToCanvas(def.definition, componentDefs, {})` and `setNodes/setEdges`. Keep left palette as-is (Definition's palette works). No save-from-canvas yet (Save still writes `draft`). Commit — canvas renders existing defs read-only-ish.

**Step 4 — Palette drop + node creation + connection rules.**
Replace left column with the OLD `NodeLibrary` (page.tsx:717) fed by `componentDefs` (map `ComponentDefinitionSummary` → drag payload) instead of raw agents. Implement `onDrop` (create node via `genInstanceId`), `onConnect` (validate via updated `NODE_PORTS`/`validateConnection`), node/edge delete. Update `CANVAS_RULES` bodies to the new `data` shape (§7). Commit — full canvas editing, in-memory only.

**Step 5 — Save from canvas.**
Change `saveDraft` to `const doc = canvasToDoc(nodes, edges, draftName); await updateDefinition(app.id, activeDef.id, { definition: doc })`. Set dirty on any node/edge change. Commit — round-trip save works.

**Step 6 — Validate + error mapping.**
On `validate()`, after report returns, set `_error`/`_errorMsg` on nodes whose `instance_id` is in `errors[].instance_id`; render banner. Clear on next edit. Commit.

**Step 7 — Publish + revision selector + logo state.**
Wire `publish()` gating (`canPublish`), revision `<select>` + New Draft (from DefinitionView), and `computeLogoState` (§7) into `CanvasInner`'s `logoState` prop (already a prop, page.tsx:2277). Commit — feature-complete v1 (auto-layout every open).

**Step 8 — (Backend, separate task) canvas layout persistence.**
Only after v1 ships. Extend Go `ApplicationInput` + `Update` + DAL to accept/store `canvas jsonb` (§11). Add Go test. Then flip frontend to send/read `app.canvas.layout` in save/load. Follow `go/CLAUDE.md` (test + TEST_INDEX + route ownership verification).

**Step 9 — Delete dead code.**
Once `CanvasBuilderView` is the only editor, remove the OLD `DefinitionView` form body and any now-unused helpers (`buildNodesFromApp` at 573 is OLD-format — remove; `AdvisorPanel` — KEEP for v1.1). Keep node components, `CanvasLogo`, `CanvasInner`, rule engine. Commit.

---

## 9. What to keep from DefinitionView

Reuse verbatim (lift into `CanvasBuilderView`):
- State: `defs`, `activeDef`, `draft` (still used as the name holder + fallback), `isDirty`, `componentDefs`, `validating`, `publishing`, `saving`, `validationReport`, `toast`.
- Functions: `loadDef` (page.tsx:2762), `reloadDefs` (2770), `newDraft` (2794), `saveDraft` (2808 — modify per Step 5), `validate` (2822), `publish` (2837), `showToast` (2757).
- The **top bar** (Back / status pill / Save / Validate / Publish buttons, page.tsx:2949–2999) — reuse; add canvas toolbar inside `CanvasInner` (already present).
- The **validation errors banner** (page.tsx:3002).
- The **revision selector** `<select>` + New Draft button (page.tsx:3078).
- Toast notifications block.

Note on `draft`: v1 keeps `draft: AppDefinitionDoc | null` as the loaded snapshot, but the **canvas node/edge state is the live source of truth**. `saveDraft` serializes from nodes/edges (not from `draft`). Keep `draft.name` synced via a name input in the top bar; on `loadDef`, seed both `draft` and the canvas.

Changes vs DefinitionView:
- Left column: form list → `NodeLibrary` (drag source).
- Center column: form editor → ReactFlow canvas.
- Right column: none currently → new `PropertiesPanel` (§5).

---

## 10. File structure decision

**Recommendation: keep everything in `page.tsx` for v1; extract only after it stabilizes.**

Rationale:
- The file is already 5000+ lines but self-contained; the node components, `CanvasLogo` (with its 14-polygon SVG + keyframes), rule engine, and `CanvasInner` are tightly coupled through the module-level `C` design tokens, `glass`, `CANVAS_STYLES`, and `deleteNodeRef`. Extracting mid-rebuild multiplies churn and import-cycle risk while the shapes are still moving.
- All the canvas machinery ALREADY lives in this file (it was the OLD builder). We are re-pointing it at v2, not importing it fresh. Cross-file extraction now = large diff with no functional gain.
- Next.js `'use client'` + a single default export is simplest here; there are no SSR boundaries to gain from splitting.

**After v1 ships and is stable**, extract in this order (each a pure move, no logic change) into `frontend/src/app/admin/applications/canvas/`:
1. `logo.tsx` — `CanvasLogo`, `LogoState`, `LOGO_*` constants, keyframes.
2. `nodes.tsx` — the 4 node components + `NODE_TYPES` + `InternalMBadge`.
3. `rules.ts` — `NODE_PORTS`, `validateConnection`, `CANVAS_RULES`, `runRules`, `styledEdges`, `analyzeChain`.
4. `serialize.ts` — `genInstanceId`, `canvasToDoc`, `docToCanvas`, `applyDagreLayout`.
5. `CanvasBuilderView.tsx` — the host.
Shared tokens (`C`, `glass`, `CANVAS_STYLES`) move to `canvas/tokens.ts`. This keeps `page.tsx` as just `ApplicationsPage` + `ListView`.

Do NOT extract during the rebuild.

---

## 11. Backend compatibility check

Serialization produces `AppDefinitionDoc`; verify against `publish.go` validator (`validateDoc`, line 101) and compiler:

1. **`instance_id` unique across components + entry_points?** — YES. `genInstanceId` checks the union set (§1). Validator enforces this (publish.go:129/173, `duplicate_instance_id`). ✔
2. **`EPInstance.root` references a valid component instance_id?** — YES. `root` is derived from the EP→Orch edge target, which is an orchestrator node whose id is that orch's `instance_id`. If the EP has no root edge, `root=''` — publish.go tolerates empty root (orchID stays nil, EP gets null `app_orchestrator_id`, publish.go:355–360) but the client `EP_HAS_ORCH` block rule (page.tsx:2545) prevents publishing with a dangling EP. Note: the Go validator does NOT currently error on empty root, so client rules are the gate — keep them. ✔ (with client-side enforcement)
3. **Connection source/target valid instance_ids?** — YES. Both endpoints are node ids = instance_ids. Validator flags `dangling_connection` otherwise (publish.go:194–212); this can only happen if a node is deleted without its edges — our delete path removes touching edges (§3), so it won't. ✔
4. **`definition_ref` populatable from `ComponentDefinitionSummary`?** — YES. `definition_ref = { kind: cd.kind, namespace: cd.namespace, name: cd.name, version: cd.version }` and `definition_id = cd.id`. The compiler resolves via `ResolveForPublish(ref, definition_id)` (publish.go:266). Because we pass `definition_id` (the UUID fast-path), resolution is exact. ✔
5. **Orchestrator config keys** — the panel writes exactly the keys publish.go reads: `system_prompt, llm_provider, llm_model, max_iterations, max_parallel_tools, history_window, budget_tokens` (publish.go:310–320). Extra keys (`display_name`, voice) are ignored by the compiler. `budget_tokens` handled via `toInt` (accepts number/json.Number). Send numbers as JS numbers. ✔
6. **`tool` connections → `allowed_agent_ids`** — publish.go:287 maps `type:'tool'` source→target; then resolves each target agent instance to its component-definition UUID (publish.go:336–340). This requires the agent component to resolve in the registry (it will, via `definition_id`). ✔
7. **`delegation` → delegatable** — publish.go:279 marks the delegation **target** delegatable. Our Orch→Orch edge sets `type:'delegation'` source→target accordingly. ✔
8. **`name` on orchestrator ComponentInstance** — we set `name = instance_id`; publish.go overrides `row.Name = comp.InstanceID` anyway (301), so consistent. ✔
9. **Protocols** — `websocket|sse|webrtc|a2a|voice` match publish.go:165 exactly. ✔

### The one real gap — canvas layout persistence
`app.canvas` (layout positions) **cannot be saved today**: Go `ApplicationsHandler.Update` (applications.go:101) reads only `Name`+`Enabled` from `ApplicationInput`, and `svc.Update(ctx, tenantID, id, name, enabled)` has no canvas param. Layout is therefore NOT round-tripped by the current Go backend.

**v1 mitigation:** do not persist layout; auto-layout with dagre on every open (§4.3, §6). Fully functional, positions just reset each session.

**v1.1 backend change (Step 8, separate task, `go/CLAUDE.md` rules):**
- `ApplicationInput`: add `Canvas json.RawMessage` (`json:"canvas,omitempty"`).
- `svc.Update` + DAL `UpdateApplication`: add a `canvas` param, `UPDATE them.applications SET canvas = $canvas ... `. (Column already implied by the OLD Python path / `applications.canvas` — confirm the column exists via `\d them.applications`; add migration if missing.)
- Return `canvas` in the `Get`/list projection (frontend already types `Application.canvas`).
- Add Go test in `internal/admin/...`; update `TEST_INDEX.md`; verify route ownership per inventory.
- Frontend: on save/blur, `updateApplication(app.id, { canvas: { layout } })`; on load, pass `app.canvas.layout` to `docToCanvas`.

No other backend change is required for canvas v1.

---

## Appendix — exact new type + signature reference

```ts
// IDs
function sanitize(s: string): string;                                  // → [a-z0-9_], collapsed
function genInstanceId(kind: 'orchestrator'|'agent'|'middleware'|'ep',
                       defName: string|undefined, existing: Set<string>): string;

// Node data (new)
interface OrchNodeData { _kind:'orchestrator'; instance_id:string; display_name:string;
  definition_ref:DefinitionRef; definition_id?:string; config:Record<string,unknown>; }
interface AgentNodeData { _kind:'agent'; instance_id:string; display_name:string; description:string;
  definition_ref:DefinitionRef; definition_id?:string; config:Record<string,unknown>;
  secret_bindings?:Record<string,string>; icon?:string; }
interface MwNodeData { _kind:'middleware'; instance_id:string; display_name:string;
  definition_ref:DefinitionRef; definition_id?:string; config:Record<string,unknown>; }
interface EpNodeData { _kind:'ep'; instance_id:string; slug:string;
  protocol:'websocket'|'sse'|'webrtc'|'a2a'|'voice'; label:string; }

// Serialization
function canvasToDoc(nodes:Node[], edges:Edge[], name?:string): AppDefinitionDoc;
function docToCanvas(doc:AppDefinitionDoc, componentDefs:ComponentDefinitionSummary[],
                     layout:Record<string,{x:number;y:number}>): { nodes:Node[]; edges:Edge[] };

// Logo
function computeLogoState(s:{loaded:boolean; isDirty:boolean; busy:boolean;
  lastResult:'none'|'valid'|'invalid'|'warn'}): LogoState;
```
