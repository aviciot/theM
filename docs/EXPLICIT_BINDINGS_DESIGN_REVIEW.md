# Explicit Canvas Data Bindings — Architecture Design Review

**Date:** 2026-08-27
**Status:** Pre-implementation review — decision pending
**Scope:** Moving from implicit PipelineVars name-matching to explicit source-output → target-input port bindings in the canvas

---

## Context: What Is Already Done

Stages 1–6 are fully implemented:
- All 12 node types have `DeriveInputs`/`DeriveOutputs` on their `NodeDef` (`nodes.go`)
- `StepSpec.Inputs []VarRef` and `StepSpec.Outputs []VarRef` populated by compiler (`compiler.go` `deriveStepVars`)
- `validateDataFlow` — path-sensitive reachability lattice running as Stage 5
- Stage 6 runtime enforcement: `executeStep` builds scoped `PipelineVars`, checks required inputs, calls `Execute`, promotes only declared outputs back
- `TransformStepConfig` has no `ExposedVars`. `ErrContractViolation` is the typed runtime error.
- Existing canvas JSON and all saved agents carry **no binding data whatsoever**.

The current model communicates via shared key names in `PipelineVars`. A node writes `vars["summary"]`; the next reads `vars["summary"]` via a template. Nothing in canvas JSON encodes that relationship explicitly.

---

## Q1: Where Should Bindings Be Stored Canonically?

### Options evaluated

**(a) On StepSpec.Inputs as `{port_id, source_step_id, source_port_id}`**

The compiled `StepSpec.Inputs []VarRef` already exists and is persisted in `agent_runtime_specs.spec`. Adding `SourceStepID` and `SourcePortID` to `VarRef` extends the existing shape. The compiler populates these from canvas binding data; the runtime reads them for scoped input resolution without any structural change to `executeStep`.

Upside: single canonical location, runtime already reads `step.Inputs`. Downside: `VarRef` currently lives in spec.go and is a compile-time artifact — adding source references couples canvas authoring state into the compiled spec.

**(b) On canvas edges as data-annotated edges distinct from control edges**

The canvas already has an `edges` array in the React Flow state (persisted in canvas JSON as part of each step's `next[]` array after compilation). A data edge would carry `{source_step, source_port, target_step, target_port, kind: "data"}` alongside control edges `{kind: "control"}`.

Upside: clean separation of concerns; the canvas JSON carries both control topology and data wiring. Downside: the current canvas JSON does NOT have an `edges` top-level array — edges are implicit in `step.next[]`. Adding an explicit edge list changes the canvas JSON schema and requires migration.

**(c) As a separate top-level `bindings: []` array on SkillSpec**

```json
{
  "bindings": [
    {"source_step": "llm1", "source_port": "output", "target_step": "http1", "target_port": "body_var"}
  ]
}
```

Upside: zero impact on existing step objects; trivially backward-compatible (absent `bindings` → heuristic fallback). Downside: the binding relationship is decoupled from the steps it connects, making the JSON harder to reason about locally.

**(d) On AgentStepDoc.inputs as a map (current canvas step shape)**

The canvas step currently has a `config` object. Add an `inputs` map to the canvas step:
```json
{"step_id": "http1", "inputs": {"body_var": {"from_step": "llm1", "from_port": "output"}}}
```

### Recommended: Option (d) — per-step `inputs` map on canvas step

**Rationale:** It is the option that requires the least structural change to everything else.
- The canvas step already has a `config` object. Adding an `inputs` map alongside it is a single optional field.
- Absent → backward-compatible heuristic path. Present → binding-aware compile path.
- The compiler reads `step.inputs` bindings and resolves them into the existing `StepSpec.Inputs []VarRef`. The runtime does not change at all.
- The canvas JSON shape stays step-centric — each step declares what feeds into it, which mirrors how users think ("this step reads from that step's output").
- Fan-out is natural: multiple steps can each declare `inputs.body_var.from_step = "llm1"` independently.

**What "stable canvas JSON" means for migration:** All existing canvas steps simply have no `inputs` field. The compiler detects absence and falls back to the current `DeriveInputs` heuristic. This is the same fallback that Stage 6 already uses for steps with empty `Inputs/Outputs`. No existing agent breaks; no re-save is required before next publish.

---

## Q2: What Should a PortDef Look Like?

### Current state

`NodeDef` has `DeriveInputs(cfg json.RawMessage) []VarRef` and `DeriveOutputs(cfg json.RawMessage) []VarRef` — functions that derive variable names from instance config. They are per-instance (called with the step's specific config), not per-type.

`VarRef` = `{Name string, Required bool}`. It names a PipelineVars key.

### PortDef proposal

```go
type PortDef struct {
    ID          string // stable identifier, used in binding references
    Label       string // human-readable ("Output text", "Body variable")
    Required    bool   // for inputs: must be wired; for outputs: always produced
    Multi       bool   // for inputs: accepts multiple bindings (fan-in); for outputs: can feed multiple (fan-out, always true)
    TypeHint    string // optional loose tag: "text", "json", "any" — informational only, not enforced
}
```

### How PortDef relates to VarRef

`PortDef` is a **type-level** declaration (same for every instance of a node type). `VarRef` is an **instance-level** artifact (name derived from instance config). For most node types, the port ID is fixed (`"output"`, `"input"`) but the PipelineVars key name varies per instance (LLM's `output_var` field). The compiler maps: `PortDef.ID → VarRef.Name` using the instance config.

### How NodeDef grows

Add optional `InputPorts []PortDef` and `OutputPorts []PortDef` alongside the existing `DeriveInputs`/`DeriveOutputs`. When `InputPorts`/`OutputPorts` are present, the frontend uses them to render wiring UI (port sockets, labels). When absent (stub nodes, mcp_call), fall back to `DeriveInputs`/`DeriveOutputs` heuristics.

`DeriveInputs`/`DeriveOutputs` are NOT removed — they remain the source of truth for `StepSpec.Inputs/Outputs` population, and they handle the dynamic case (transform, where output count depends on instance config).

### Port count per node type

| Node type | Input ports | Output ports |
|---|---|---|
| `input` | 0 (pipeline start — receives text externally) | 1: `"output"` (the bound var, e.g. `"raw"`) |
| `llm` | 1: `"input"` (user_prompt or fallback) | 1: `"output"` (output_var) |
| `http` | 1: `"input"` (URL/body templates) | N: one per extraction + 1 `"http_response"` |
| `transform` | N: one per `functions[].input_var` | N: one per `functions[].output_var` (derived from config) |
| `response` | 1: `"from_var"` | 0 (sink) |
| `branch` | 1: `"expression"` (vars used in expression) | 0 data outputs; 2 **control** outputs (true/false) |
| `a2a_call` | 1: `"input_var"` | 1: `"output_var"` |
| `human_wait` | 0 | 1: `"reply_var"` |

**Branch true/false are control outputs, not data ports.** They must be rendered and stored differently from data ports — they carry routing decisions, not values. In canvas JSON they stay in `BranchStepConfig.true_next`/`false_next` (control topology), not in `inputs` bindings.

---

## Q3: How Should Control Edges and Data Bindings Coexist in the Canvas UX?

### Current canvas JSON edge model

Edges are implicit: each step has `next: ["step-id-a", "step-id-b"]`. Branch routing lives in `BranchStepConfig.true_next`/`false_next`. The React Flow `edges` array (used for visual rendering) is derived from `next[]` at load time and converted back at save time.

### Two edge categories

**Control edges** (execution order): `step.next[]`, `BranchStepConfig.true_next/false_next`. These already work. Do not change them.

**Data bindings** (value flow): the new `step.inputs` map on each canvas step. These are directional: they declare where each input port's value comes from.

### Canvas JSON coexistence

```json
{
  "step_id": "http-step",
  "type": "http",
  "config": { ... },
  "next": ["response-step"],
  "inputs": {
    "body_content": { "from_step": "llm-step", "from_port": "output" }
  }
}
```

Control edges (`next`) and data bindings (`inputs`) live on the same step object, in separate fields. No ambiguity, no structural conflict.

### Canvas UX distinction

In the React Flow canvas:
- **Control edges**: thin grey arrows (existing style). Drawn from step bottom to next step top.
- **Data edges**: colored wires, drawn from a named output port socket on the source node to a named input port socket on the target node. Visually distinct (different color, port sockets, labels).

The user explicitly drags from a named output port to a named input port to create a data binding. The current implicit name-matching UI (where the user types the same variable name in both places) is replaced by a visual wire.

**Key UX invariant**: the canvas must remain operable without data bindings (for backward compatibility). If no bindings are wired, the agent still compiles and runs via the heuristic path.

---

## Q4: How Should Compiled StepSpec.Inputs/Outputs Evolve?

### Current VarRef

```go
type VarRef struct {
    Name     string `json:"name"`
    Required bool   `json:"required"`
}
```

### Proposed extension

```go
type VarRef struct {
    Name       string `json:"name"`
    Required   bool   `json:"required"`
    PortID     string `json:"port_id,omitempty"`     // which port this ref was derived from
    SourceStep string `json:"source_step,omitempty"` // step ID that produces this value (from explicit binding)
    SourcePort string `json:"source_port,omitempty"` // output port on that step
}
```

When the canvas has explicit bindings, the compiler populates `SourceStep`/`SourcePort` in addition to `Name`. When bindings are absent, `SourceStep`/`SourcePort` are empty (heuristic path, same as today).

### Stage 6 runtime: no change required

The runtime `executeStep` only reads `step.Inputs[i].Name` to build the scoped vars map and check required inputs. `SourceStep`/`SourcePort` are compile-time verification artifacts — the runtime does not need them because by execution time the value has already been written to global `PipelineVars` under its name. The scoped enforcement remains exactly as implemented.

### Validator evolution

`validateDataFlow` currently checks: "is there a step upstream that writes a var with this name on every execution path?" With explicit bindings, the validator can additionally check: "does the declared source step actually have the declared source port?" This is stronger and catches type-level wiring errors at compile time, not just name-collision errors.

---

## Q5: Migration and Fallback Behavior

### Three options

**(a) Compile-time fallback via DeriveInputs/DeriveOutputs when no bindings present**

When `step.inputs` is absent or empty on a canvas step, the compiler falls back to calling `DeriveInputs(cfg)` exactly as today. The compiled `VarRef` gets `Name` but empty `SourceStep`/`SourcePort`. The runtime handles this identically to today.

**This is the recommended option.** Zero risk to existing agents. No re-save required.

**(b) Require re-save with bindings before next publish**

Reject publish of any agent that has no binding data. Forces users to re-wire everything.

**Risk:** Every existing canvas agent breaks at publish. The team must re-wire every canvas agent manually. Given that there are currently 6 agent_definitions rows in the live DB, this is feasible in principle, but it creates a forced migration cliff with no rollback.

**(c) Auto-generate bindings on builder open**

When the builder loads a canvas with no `inputs` bindings, run the existing heuristic (`DeriveInputs`) to infer bindings and pre-populate the `inputs` map. Present these as "suggested bindings" in the UI.

**Risk:** The heuristic can produce incorrect inferences (e.g., a step reading "output" might be inferring from the wrong upstream source when multiple steps write "output"). Auto-generated bindings that don't match user intent are worse than no bindings.

### Recommended: (a) compile-time fallback

The compiler checks each step's `inputs` map first. If present and non-empty, use explicit bindings. If absent, call `DeriveInputs(cfg)` and emit a validation warning: "step has no explicit data bindings — using heuristic derivation." Publish still succeeds. The warning nudges users toward explicit wiring without forcing it.

---

## Q6: Changes Required

### Go — `go/internal/agentgen/spec.go`

- Add `PortID`, `SourceStep`, `SourcePort` fields to `VarRef` (omitempty, backward-compatible)
- Add `Binding` struct: `{FromStep, FromPort string}` — the raw canvas binding reference
- Add `Inputs map[string]Binding` to `canvasStep` (the canvas JSON model used by compiler) — `omitempty`, so all existing canvas JSON remains valid

### Go — `go/internal/agentgen/noderegistry.go`

- Add `InputPorts []PortDef` and `OutputPorts []PortDef` to `NodeDef` (both omitempty)
- Add `PortDef` struct: `{ID, Label string; Required, Multi bool; TypeHint string}`
- `NodeTypeInfo` (the JSON sent to frontend) gains `InputPorts` and `OutputPorts`

### Go — `go/internal/agentgen/nodes.go`

- Register `InputPorts`/`OutputPorts` on the 6 implemented node types (input, llm, http, transform, response, branch)
- Transform ports are dynamic (depend on config) — for transform, `InputPorts`/`OutputPorts` remain nil; the frontend derives them from `cfg.functions[].input_var/output_var` live (same as today)
- Branch: `OutputPorts` = nil (control-only outputs, not data ports)

### Go — `go/internal/agentgen/compiler.go`

- In `deriveStepVars` (the function that builds `step.Inputs`/`step.Outputs`): check the canvas step's `Inputs map[string]Binding` first. If a binding is present for an input port, resolve `VarRef.Name` from the source step's declared output port and populate `SourceStep`/`SourcePort`. If absent, fall back to `DeriveInputs(cfg)` as today.
- In `validateDataFlow`: add a binding-coherence check — if `VarRef.SourceStep` is set, verify that source step exists and has the declared source port in its `DeriveOutputs`. This is a new Issue type: `BROKEN_BINDING`.
- Emit `UNBOUND_INPUT` warning (not error) when a step has no explicit bindings and falls back to heuristic derivation.

### Go — `go/internal/agentgen/interpreter.go`

**No changes required.** The runtime reads only `VarRef.Name` from `step.Inputs`. Stage 6 enforcement remains identical.

### Frontend — `go/internal/agentgen/` (no change) + `frontend/src/`

- `api.ts`: add `inputs?: Record<string, {from_step: string, from_port: string}>` to the canvas step type
- `nodeVars.ts`: `extractNodeVars` remains unchanged (used for pre-validate heuristic). Post-validate, the RightPanel already uses `StepContracts` from the compiled spec.
- `nodeRegistry.ts`: add `inputPorts`/`outputPorts` from `GET /admin/node-types` response
- `page.tsx`: distinguish control edges (drawn from `step.next`) and data edges (drawn from `step.inputs` bindings). Implement port socket rendering on nodes. On connect: if source handle is a data port and target handle is a data port, create a data binding (write to `step.inputs`). If source handle is the step body (control), create a `next` edge.
- `RightPanel.tsx`: in the READS/WRITES panel, show whether each input is explicitly bound or heuristic
- `StepNode.tsx`: render named port sockets for declared `inputPorts`/`outputPorts`

### Tests

- New compiler tests: explicit binding resolves to correct `VarRef.SourceStep/SourcePort`; `BROKEN_BINDING` issue for bad source step reference; `UNBOUND_INPUT` warning when no bindings
- New interpreter tests: none needed (Stage 6 runtime unchanged)
- New noderegistry tests: `PortDef` presence on the 6 implemented types; `NodeTypeInfo` includes ports

---

## Q7: What Should NOT Be in This Stage

- **Type enforcement at runtime** — `TypeHint` on `PortDef` is informational only. No coercion, no runtime type checks. Defer type system entirely.
- **Fan-in (multiple bindings to one input port)** — `Multi: false` on most input ports. Do not implement merge semantics; the PipelineVars model is single-value per key. Defer until StepParallel makes fan-in meaningful.
- **Multi-skill bindings** — bindings only within one skill. Cross-skill data flow does not exist today and must not be introduced.
- **Temporal / ADK integration** — the binding model must be Temporal-compatible in shape but must not implement Temporal in this stage.
- **Blob/large-payload references** — PipelineVars values remain in-process `any`. No lazy-load or content-addressed references.
- **Loop / Parallel node binding semantics** — these stub nodes have `Execute: nil`. Do not define their port semantics yet.
- **Auto-migration of existing agents** — do not auto-rewrite canvas JSON on builder open. Heuristic fallback is sufficient.
- **Binding validation as a hard publish error** — `UNBOUND_INPUT` is a warning, not an error. Agents without explicit bindings must still be publishable.

---

## Recommended Model (Summary)

| Decision | Choice |
|---|---|
| Binding location | Per-step `inputs` map on canvas step object (`step.inputs: {port_id: {from_step, from_port}}`) |
| PortDef shape | `{ID, Label string; Required, Multi bool; TypeHint string}` on NodeDef |
| Control/data coexistence | `step.next[]` for control; `step.inputs` for data — same step object, separate fields |
| VarRef evolution | Add `PortID`, `SourceStep`, `SourcePort` (omitempty) — runtime reads only `Name`, unchanged |
| Migration | Heuristic fallback when `inputs` absent; `UNBOUND_INPUT` warning; no forced re-save |
| Transform ports | Dynamic — derived from `functions[].output_var` per instance; no static PortDefs on NodeDef |
| Branch outputs | Control-only — not data ports, not in `inputs` bindings |

---

## Risks

**Risk 1: Frontend complexity spike**
The port socket rendering, drag-to-wire UX, and distinguishing control vs data edge handles in React Flow is the most complex part. React Flow handles have a fixed model; adding named port handles per node requires significant StepNode.tsx rework.
*Mitigation:* Implement port sockets as a separate render pass on top of the existing node card. Keep the existing `next`-edge control wires unchanged. Land the canvas JSON schema change and compiler support first; the UX can be iterative.

**Risk 2: Heuristic fallback creates a two-tier agent population**
Agents with explicit bindings get strong compile-time `BROKEN_BINDING` checks. Agents without bindings (the current population) get only the existing `UNRESOLVED_INPUT` reachability check. This bifurcation persists indefinitely unless there is a deprecation path.
*Mitigation:* After binding UI ships, add a builder UI prompt: "This agent uses implicit variable names. Wire it explicitly for stronger validation." Track `binding_coverage` in the compiled spec (0.0–1.0) to quantify progress.

**Risk 3: Port ID stability**
If the `PortDef.ID` for a registered port changes (e.g., `"output"` → `"text_output"`), all existing canvas JSON that references that port ID in `inputs` bindings breaks silently (compiler would emit `BROKEN_BINDING`).
*Mitigation:* Treat `PortDef.ID` as a permanent stable identifier — same stability contract as step IDs. Document this in `go/CLAUDE.md`. Never rename a registered port ID without a migration.

**Risk 4: Transform ports remain heuristic**
Transform nodes have dynamic ports (N input vars, M output vars, derived from `cfg.functions`). They cannot have static `PortDef` entries on NodeDef. The frontend must derive transform port sockets from the live config.
*Mitigation:* This is already the case in `nodeVars.ts` and `StepNode.tsx`. The change is: instead of displaying port names in the node card, render them as interactive sockets. No new logic needed — just a rendering change.

**Risk 5: Canvas JSON forward-compatibility**
Adding `inputs` to canvas steps changes the schema. Old compiler versions (if any were cached or in use) would silently ignore the field. New compiler versions reading old canvas JSON would see no `inputs` and fall back to heuristics.
*Mitigation:* The `inputs` field is `omitempty` in both directions. The fallback is by design. There is no compatibility break — only a capability gap between old and new canvas JSON.

---

## Staged Implementation Plan

### Stage A — Schema + compiler (Go only, no UI)
**Files:** `spec.go`, `noderegistry.go`, `nodes.go`, `compiler.go`
1. Add `PortDef` struct to `noderegistry.go`; add `InputPorts`/`OutputPorts` to `NodeDef`
2. Add `PortID`, `SourceStep`, `SourcePort` (omitempty) to `VarRef` in `spec.go`
3. Add `Binding` struct and `Inputs map[string]Binding` to `canvasStep` in `compiler.go` (or a new `canvas_types.go`)
4. Update `deriveStepVars` in `compiler.go`: check `step.Inputs` bindings first; fall back to `DeriveInputs` if absent
5. Register `InputPorts`/`OutputPorts` on the 6 implemented node types in `nodes.go`
6. Add `BROKEN_BINDING` and `UNBOUND_INPUT` issue types to `validateDataFlow`
7. Tests: compiler binding resolution, BROKEN_BINDING, UNBOUND_INPUT warning

### Stage B — Frontend canvas JSON wiring (no UX change yet)
**Files:** `api.ts`, `nodeRegistry.ts`
1. Add `inputs` field to canvas step TypeScript types
2. Update `GET /admin/node-types` response parsing to include `inputPorts`/`outputPorts`
3. Update save/load: ensure `inputs` round-trips correctly through canvas JSON
4. Tests: TypeScript type coverage

### Stage C — Port socket rendering + drag-to-wire UX
**Files:** `StepNode.tsx`, `page.tsx`, `RightPanel.tsx`
1. Render named port sockets on nodes for declared `inputPorts`/`outputPorts`
2. Distinguish control handles (step body top/bottom) from data handles (named port sockets)
3. On data-edge connect: write binding to `step.inputs` in canvas state
4. On control-edge connect: write to `step.next[]` as today
5. RightPanel READS/WRITES: annotate each input as "explicitly bound" or "heuristic"
6. Tests: manual E2E — wire LLM output to HTTP body, publish, invoke

### Stage D — Binding coverage nudge (deferred)
- Add `binding_coverage float64` to compiled AgentSpec
- Builder UI prompt for agents with `binding_coverage < 1.0`

---

## Go / No-Go Recommendation

**GO — with one prerequisite.**

The design is sound. The canonical binding location (per-step `inputs` map) is the smallest schema change that is fully backward-compatible, integrates cleanly with the existing compiler and Stage 6 runtime, and supports the fan-out and control/data separation requirements.

**The one condition that would flip this to NO-GO:**

If the React Flow port socket rendering in Stage C turns out to require replacing the existing node rendering architecture entirely (not just adding port sockets to existing cards), the frontend scope becomes a multi-week effort that should be scoped and staffed separately. Before starting Stage C, build a prototype of named port sockets on one node type (e.g., LLM) and confirm that React Flow's custom handle model supports named handles per node without requiring a full node component rewrite. If it does (which is expected — React Flow supports this natively), proceed. If it doesn't, implement Stage A + B first and deliver compiler-level binding support without the UX, then revisit Stage C.

**The implementation risk is in the frontend, not the Go backend.** Stage A (Go only) is low-risk and high-value on its own — it enables binding-aware compilation and validation even before the UX is complete, and can be shipped independently.
