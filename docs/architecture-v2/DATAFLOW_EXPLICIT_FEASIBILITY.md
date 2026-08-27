# Explicit Data Flow — Architecture Feasibility Review
**Date:** 2026-08-27  
**Status:** Decision pending — no implementation yet  
**Scope:** Canvas node input/output contracts; data-flow from heuristic to authoritative

---

## 1. Current State (grounded in code)

### The shared-store model

```
PipelineVars = map[string]any   // flat, unscoped, single instance per execution
```

Every step in a skill execution shares one map. Steps read from it via Go `text/template` rendering (LLM, HTTP) or explicit `InputVar` fields (transform, a2a_call). Steps write to it by setting named keys (`OutputVar`, `Extractions[i].Var`, `Bindings["text"]`, etc.).

Edges (`StepSpec.Next []string`) encode only execution order. No variable name, type, or data-pipe annotation exists on any edge in the compiled spec or canvas JSON.

### What exists for metadata today

**Go `NodeDef`** has: `OutputArity`, `IsSource`, `IsSink`, `SingleInput`, `EdgeRules`, `InputField` (single key hint), `AppParams`. **Nothing** about variable names.

**StepSpec configs** are opaque `json.RawMessage` per type. The interpreter casts each to its typed config struct and reads data-binding fields from there (e.g., `LLMStepConfig.OutputVar`, `HTTPStepConfig.Extractions`).

**Frontend `nodeVars.ts`** does static heuristic analysis of live instance config JSON to derive `reads[]`/`writes[]`. Explicitly annotated "NOT authoritative." Added 2026-08-26.

### Dynamic outputs

Every currently-deployed step type has instance-configurable output variable names:

| Step type | Dynamic output field | Fixed output |
|---|---|---|
| `input` | `bindings.text` | — |
| `llm` | `output_var` (def: `"output"`) | — |
| `http` | `extractions[].var` | `http_response` (always) |
| `transform` | `functions[].output_var` (per fn) | — |
| `a2a_call` | `output_var` | — |
| `human_wait` | `reply_var` | — |
| `loop` | `accum_var` | — |
| `parallel` | `merge_var` | — |

This is the central challenge: output variable names are not knowable from the node type alone — they require the instance config.

---

## 2. Approaches Evaluated

Three viable approaches, from least to most invasive:

---

### Approach A — Instance-level Var Declarations on StepSpec (preferred)

Add optional `inputs` and `outputs` fields to `StepSpec` / `canvasStep` that enumerate concrete variable names for each instance. These are populated by the canvas UI at save time and validated by the compiler; the runtime ignores them (they are advisory). Migration to runtime enforcement is a separate later step.

```go
// StepSpec with optional data-flow declarations
type StepSpec struct {
    ID       string          `json:"id"`
    Type     StepType        `json:"type"`
    Config   json.RawMessage `json:"config"`
    Next     []string        `json:"next"`
    Branches []BranchArm     `json:"branches,omitempty"`
    // New: advisory data-flow declarations (ignored by interpreter v1)
    Inputs   []VarRef        `json:"inputs,omitempty"`
    Outputs  []VarRef        `json:"outputs,omitempty"`
}

type VarRef struct {
    Name     string `json:"name"`       // variable name in PipelineVars
    PortKey  string `json:"port_key"`   // which config field declares this (e.g. "output_var", "extractions.0.var")
    Required bool   `json:"required"`   // missing writer → error vs warning
}
```

**NodeDef changes needed:** Add a `DeriveOutputs(cfg json.RawMessage) []VarRef` hook (optional, called by compiler). For nodes with fully static outputs (branch, response), the NodeDef can declare them statically. For dynamic nodes (llm, http, transform), the hook parses `cfg` and returns the instance-specific list. The compiler calls this to populate `Outputs` at save time and cross-reference with the `Inputs` of downstream nodes.

**Runtime:** No change to interpreter. `PipelineVars` shared store continues to be the authority. `Inputs`/`Outputs` on StepSpec are compiler/validator metadata only (like `ExposedVars` is today, but used).

**Validation in compiler stage 5 (new):**
- Walk the topo-sorted step list, accumulating a `writes: map[string]stepID`.
- For each step's `Inputs`, check each `VarRef.Name` is in `writes`. Missing → error if `Required`, warning if not.
- Detect duplicate producers (two steps write the same var before any reader) → warning.
- Detect declared write that nothing downstream reads → info/suppressed.

**Branch edges:** No change needed. `TrueNext`/`FalseNext` stay in BranchStepConfig. Branch carries no data.

**Parallel/Join/Loop:** These already have `AccumVar`/`MergeVar` in their configs — they map cleanly to `Outputs`.

**Pros:**
- Minimal runtime change. Interpreter untouched.
- Compiler gains data-flow validation without a rewrite.
- Frontend can use `Outputs` from the compiled spec instead of re-deriving via `nodeVars.ts`.
- Backward compatible: `inputs`/`outputs` are `omitempty` — old agents load and run unchanged.
- `DeriveOutputs` hook on NodeDef gives per-type encapsulation without a full port registry.
- Staged: advisory first, enforced later.

**Cons:**
- Two sources of truth during transition: `Config` (runtime authority) and `Inputs`/`Outputs` (compiler metadata). Must stay in sync.
- Canvas must populate `Inputs`/`Outputs` correctly at save time — UI regression risk.
- Does not prevent a step from reading a variable it didn't declare (runtime still permissive).

**Migration cost:** Medium. New Go types + compiler stage + frontend save-path changes + new validation stage. No interpreter change.

---

### Approach B — Typed Port Registry on NodeDef

NodeDef declares static port schemas: `Inputs []PortDef` and `Outputs []PortDef` at registration time, where dynamic ports are represented by a special `"variadic"` / `"derived"` kind. Bindings on StepSpec edges carry variable name assignments that wire ports to PipelineVars keys.

```go
type PortDef struct {
    Key      string `json:"key"`         // logical port name (e.g. "prompt_input", "result")
    VarHint  string `json:"var_hint"`    // default variable name
    Kind     string `json:"kind"`        // "static" | "dynamic" | "variadic"
    Required bool   `json:"required"`
    DataType string `json:"data_type"`   // "string" | "json" | "any"
}

type NodeDef struct {
    // ... existing fields ...
    Inputs  []PortDef
    Outputs []PortDef
}
```

Edges carry an optional `VarName string` binding: `{ source, target, sourcePort, targetPort, varName }`.

**Pros:**
- Richer tooling possible: type checking, IDE-style autocomplete, explicit pipe visualization.
- Port identity is stable even if variable names change.
- Aligns with industry data-pipeline models (Airflow XCom, Prefect artifacts).

**Cons:**
- **Large rewrite.** Every registered NodeDef needs Inputs/Outputs. Every StepSpec needs edge bindings. The frontend ReactFlow edge model needs custom data. The interpreter must resolve ports → variable names before execution.
- Dynamic outputs (`http.extractions`, `transform.functions`) cannot be described statically by type — requires the `"variadic"` escape hatch, which defeats the purpose for the most complex cases.
- The existing canvas (`Next []string` implicit edges) has no port concept at all — migration must be coordinated across Go spec, compiler, interpreter, and all frontend components simultaneously.
- Published agents break unless migrated; migration script needed for every stored definition.

**Migration cost:** Very high. 3–4 weeks minimum, multiple coordinated changes across the full stack.

---

### Approach C — Pure Frontend Contracts (extend `nodeVars.ts`)

Keep runtime untouched. Promote `nodeVars.ts` from heuristic to authoritative by:
- Making `extractNodeVars` the canonical source of `reads`/`writes` for each node instance.
- Adding a `DeriveVars(cfg) → NodeVars` hook to each frontend `NodeDef` (in `nodeRegistry.ts`), so individual node types can declare their own analysis logic instead of the central switch.
- Running `extractNodeVars` at save time and embedding the result in a `_derived` metadata block in the canvas JSON (not in StepSpec, not in the Go spec).
- Validation runs client-side before save (and/or in the Go compiler via a parallel JSON analysis).

**Pros:**
- Zero Go runtime changes.
- No migration: `_derived` is `omitempty`, old agents unaffected.
- Fast to implement. Frontend-only.
- Consistent with how `ExposedVars` already works (declared in config, not enforced at runtime).

**Cons:**
- Frontend is the authority for a property that the backend ultimately executes. The Go runtime can always diverge.
- TypeScript analysis cannot be shared with Go-side validation in `compiler.go` — two parallel implementations required to get Go-side issue reporting.
- Heuristic quality ceiling: no escape from regex-based template parsing. Template functions, computed variable names, conditional configs all remain unanalysable.
- Does not enable Go-side data-flow errors at publish time without duplicating the logic in Go.

**Migration cost:** Low for frontend. Medium for any Go validation parity.

---

## 3. Specific Questions Answered

### Control flow vs data flow — should they be separate?

**Yes, keep them separate.** Control flow (which step runs next) and data flow (which variables a step reads/writes) are orthogonal concepts with different granularity. `Next []string` edges are adequate for control flow and should not be overloaded with data semantics. Branch `TrueNext`/`FalseNext` remain in BranchStepConfig (already correct). Parallel/Join topology is already expressed in `Branches [][]string`/`MergeVar`.

Future nodes (Human Wait, Loop, A2A) all have clear control-flow topology. Their data-flow can be declared via `Inputs`/`Outputs` on StepSpec (Approach A) without touching edge structure.

### Dynamic outputs — how should the model represent them?

Static NodeDef registration cannot describe variable names for dynamic outputs. The cleanest model:

1. NodeDef registers a `DeriveOutputs(cfg json.RawMessage) []VarRef` hook.
2. The compiler calls this hook at save/publish time, passing the instance config.
3. The result is stored as `StepSpec.Outputs` (Approach A).

For the most dynamic case (transform with N function steps each producing a different variable), the hook parses `functions[]` and returns one `VarRef` per `output_var`. This is exact and does not require a special "variadic" escape hatch.

### BuildValidator validation logic

With Approach A, the new compiler stage 5 (data-flow validation):

| Condition | Severity | Blocking |
|---|---|---|
| `Required` input var has no reachable upstream writer | `error` | Publish |
| Non-required input var has no reachable upstream writer | `warning` | No |
| Two steps write the same var before any reader between them | `warning` | No |
| Step declares an output that nothing downstream reads | info (suppressed) | No |
| Output var name collides with a reserved key (`http_response`, `input`) | `warning` | No |
| Declared write does not match config's actual `output_var` (drift) | `error` | Save |

Only the last one (drift between declared and config) is a save-blocking error — it means the canvas saved stale data. All data-flow topology issues are warnings at save time, errors at publish time.

### Frontend transition: heuristic → explicit

With Approach A:
1. Canvas save path calls `DeriveOutputs` / `DeriveInputs` (frontend mirrors of Go hooks) and embeds results in StepSpec JSON as `inputs`/`outputs`.
2. `RightPanel.tsx` READS/WRITES section reads from `node.data.inputs` / `node.data.outputs` (the declared lists) instead of calling `extractNodeVars` at render time.
3. Edge labels read from `node.data.outputs` of the source filtered by target `node.data.inputs` — same logic as today but against declared data instead of heuristically derived data.
4. `nodeVars.ts` is retained as the derivation engine (called once at save, not at every render) and possibly simplified to per-type hooks.

### Backward compatibility

`StepSpec.Inputs`/`Outputs` are `omitempty` in both Go and TypeScript. Published agents without these fields continue to load and execute identically — the interpreter ignores them. The new compiler validation stages are gated on the fields being present: if absent, no data-flow validation runs (same behavior as today). A background job can backfill `Inputs`/`Outputs` on existing published agents by running the `DeriveOutputs` hooks, but this is not required for correctness.

### Source of truth question

**Neither NodeDef alone nor a separate port registry is the right answer for this codebase.**

- NodeDef is the right place for type-level metadata (static ports for nodes that have them: branch, response, input).
- Per-instance `Inputs`/`Outputs` on StepSpec is the right place for dynamic/config-dependent declarations.
- `DeriveOutputs` hooks on NodeDef bridge the two: they are type-registered functions that produce instance-specific declarations by reading config JSON.

The Go runtime (`PipelineVars` shared store) remains the execution authority. The compiler/validator is the contract-checking authority. The canvas UI is the authoring surface.

---

## 4. Recommendation

**Approach A — Instance-level Var Declarations on StepSpec** is the right path.

**Reason:** It is the smallest change that makes data-flow contracts explicit and compiler-checkable without rewriting the runtime. It matches the existing pattern (`ExposedVars` is already an advisory instance-level declaration on TransformStepConfig — `Inputs`/`Outputs` is the same concept made cross-cutting and compiler-used). It is fully backward compatible. It enables Go-side data-flow validation at publish time. The frontend heuristic (`nodeVars.ts`) can transition from an always-on renderer to a save-time derivation engine.

**Approach B** (port registry) is the correct long-term destination if the platform needs type-checked pipelines, visual port wiring, or reusable component composition — but the migration cost is prohibitive relative to the current scale and the benefit of getting there now. Revisit after Approach A is stable and the team has validated that per-instance declarations are the right user-facing model.

**Approach C** (pure frontend) is a dead end for validation quality: it cannot produce Go-side publish errors, requires duplicating analysis in two languages, and leaves the authoritative runtime perpetually divergable from the frontend contract.

### Proposed implementation order (not for this session)

1. Add `VarRef` type and `Inputs`/`Outputs []VarRef` to `StepSpec` (Go + frontend TypeScript).
2. Add `DeriveOutputs(cfg json.RawMessage) []VarRef` hook to Go `NodeDef`; implement per registered type.
3. Compiler stage 5: topo-walk, accumulate `writes` map, validate `Inputs[].Name` against it, emit issues.
4. Canvas save path: call derivation hooks, embed into StepSpec JSON before POST.
5. RightPanel READS/WRITES: read from `node.data.inputs`/`node.data.outputs` instead of calling `extractNodeVars` at render time.
6. (Later) Mark required inputs enforced at interpret time; emit runtime error if var missing at step entry.

Total estimated scope: 2 focused sessions. No interpreter rewrite. No backward-compatibility break.

---

## 5. Risks and Open Questions

- **Template-based reads are unanalysable without parsing Go templates in TypeScript.** The regex approach in `extractTemplateVars` covers `{{.varname}}` but not Go template functions, conditionals, or computed variable names. This is acceptable for advisory derivation but means `Required: false` should be the default for template-derived reads.
- **`ExposedVars` on TransformStepConfig is dead code in the Go interpreter.** If `Outputs` on StepSpec supersedes it, `ExposedVars` should be formally deprecated and removed from TransformStepConfig to avoid two declarations of the same information.
- **Branch edges carry no data** and should never carry data (they select paths, not variable values). If Approach B is ever pursued, branch edges must be explicitly tagged as control-only to prevent confusion.
- **The `buildvalidator/` directory referenced in some docs does not exist yet.** The current validation lives in `compiler.go`. Stage 5 should be added to `compiler.go` in the same pattern as the existing four stages, not in a new package.
