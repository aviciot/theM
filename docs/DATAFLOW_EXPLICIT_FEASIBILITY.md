# Explicit Data Flow — Architecture Feasibility Review (v2)
**Date:** 2026-08-27 (revised)
**Status:** Decision pending — no implementation yet
**Scope:** Canvas node input/output contracts; data-flow from heuristic to authoritative

---

## 1. Current State (grounded in code)

### The shared-store model

```
PipelineVars = map[string]any   // flat, unscoped, single instance per execution
```

Every step in a skill execution shares one map. Steps read from it via Go `text/template`
rendering (LLM, HTTP) or explicit `InputVar` fields (transform, a2a_call). Steps write to it
by setting named keys (`OutputVar`, `Extractions[i].Var`, `Bindings["text"]`, etc.).

Edges (`StepSpec.Next []string`) encode only execution order. No variable name, type, or
data-pipe annotation exists on any edge in the compiled spec or canvas JSON.

### Save vs publish vs validate — the actual pipeline

Understanding the three paths is essential before designing any change:

**SAVE** (`PUT /agent-definitions/{id}`):
The frontend POSTs raw canvas JSON (`AgentDefinitionDoc`) as-is. The Go backend runs only a
lightweight structural check (valid JSON object, `display_name` present, no duplicate IDs, no
secret values). **`agentgen.Validate` is never called at save time.** The definition is stored
verbatim in `agent_definitions.definition` (JSONB).

**VALIDATE** (`POST /agent-definitions/{id}/validate`):
Called by the frontend on a 1200 ms debounce after every canvas change. Posts the live
`AgentDefinitionDoc` body. Go calls `agentgen.Validate(...)`, returns `[]Issue` (stub nodes →
warning). The resulting `*AgentSpec` is **discarded**. Nothing is persisted.

**PUBLISH** (`POST /agent-definitions/{id}/publish`):
Loads the last-saved DB definition, calls `agentgen.CompileForPublish(...)`. The resulting
`*AgentSpec` is marshalled to JSON and written to `agent_runtime_specs.spec` (JSONB) — this is
the only persisted compiled form. Stub nodes → error; any error → 422, nothing persisted.

```
SAVE ──────────────── raw canvas JSON ──────────────── agent_definitions.definition
                           │
VALIDATE (debounced) ──────┤ agentgen.Validate() → []Issue (AgentSpec discarded)
                           │
PUBLISH ────────────────── ┘ agentgen.CompileForPublish() → AgentSpec → agent_runtime_specs.spec
```

The key implication: the Go compiler (`agentgen.CompileForPublish`) is already **the sole
authority** for what gets persisted as the executable spec. There is no competing
TypeScript-side compilation. The frontend is a canvas authoring tool; the compiler is the truth.

### What the compiled AgentSpec contains today

`AgentSpec.Skills[].Steps` is a `[]StepSpec`, topologically sorted by the compiler. Each
`StepSpec` carries `{ID, Type, Config json.RawMessage, Next []string, Branches []BranchArm}`.
`Config` is the opaque per-type config blob — identical to what the canvas stored. No data-flow
metadata is derived or stored today.

### NodeDef — what exists, what is missing

**Go `NodeDef`** already has runtime-only hook fields (`json:"-"`):
```go
Validate func(step canvasStep) []Issue          // nil on all 11 registered types today
Execute  func(ctx, interp, ic, step, vars, result) error
```

Adding `DeriveInputs / DeriveOutputs` as additional `json:"-"` hook fields follows the
identical pattern. The `canvasStep` (unexported) is the `Validate` hook's argument; a cleaner
signature for derivation hooks takes only `json.RawMessage` (the config), which is callable from
outside the package.

**What is missing from NodeDef:** no `inputs []VarDecl` or `outputs []VarDecl` — no
declaration of what variable names a node type reads from or writes to `PipelineVars`. The
frontend `nodeVars.ts` derives this heuristically from live instance config at render time; it
is explicitly annotated as "NOT authoritative."

### Dynamic outputs — why NodeDef alone is insufficient

Every deployed step type has **instance-configurable** output variable names:

| Step type | Dynamic output field | Fixed output |
|---|---|---|
| `input` | `bindings.text` (def: `"input"`) | — |
| `llm` | `output_var` (def: `"output"`) | — |
| `http` | `extractions[].var` | `http_response` (always) |
| `transform` | `functions[].output_var` (per fn) | — |
| `a2a_call` | `output_var` | — |
| `human_wait` | `reply_var` | — |
| `loop` | `accum_var` | — |
| `parallel` | `merge_var` | — |

A static port registry on NodeDef cannot describe output variable names without reading the
live instance config. This is the central structural constraint that shapes the entire design.

---

## 2. The Three Questions Challenged

### Q1 — Should the Go backend be the sole derivation authority?

**Yes.**

The current architecture already makes this the natural answer: the Go compiler
(`agentgen.CompileForPublish`) is the only path that produces a persisted executable artifact.
The validate endpoint calls the same compiler path. Save bypasses it intentionally (lightweight
structural check only). There is no competing derivation in TypeScript that reaches the backend.

Having the frontend derive `Inputs`/`Outputs` and POST them as part of the save body introduces
a second derivation engine that can drift from Go semantics. Every time a node type's derivation
logic changes, it must be updated in both TypeScript and Go. The Go compiler would need to
either trust the frontend-derived metadata (wrong — the compiler cannot verify it is correct)
or re-derive it anyway (making the frontend derivation redundant).

**The correct boundary:**
- **Frontend (`nodeVars.ts`):** live heuristic analysis for canvas UX — READS/WRITES hints,
  edge labels, unresolved-var warnings. Runs locally, never sent to backend. Explicitly
  heuristic, may be approximate.
- **Go compiler (`DeriveInputs`/`DeriveOutputs` hooks on NodeDef):** authoritative derivation
  called during Validate and CompileForPublish. The only derivation that produces Issues or
  populates the compiled AgentSpec.

The frontend does not need to know that its heuristic matches the Go derivation exactly —
it is display-only. Discrepancies surface at validate/publish time as compiler Issues, not
as save-time frontend errors.

### Q2 — Do Inputs/Outputs need to be persisted on StepSpec?

**No — derive on demand during Validate/Compile; do not persist.**

Three options compared:

**Option 2A — Persist on StepSpec (in saved canvas JSON):**
Frontend populates `inputs`/`outputs` at save time and sends them. Backend stores them verbatim.
- Source of truth: split — canvas JSON is the config authority; derived fields are a cached view
  of what the backend would compute anyway.
- Problem: the stored metadata can drift from what the compiler would compute if derivation
  logic changes after save. The compiler must re-derive to validate, making the stored form
  redundant.
- Problem: forces dual derivation (TypeScript at save time, Go to verify at compile time).
- Backward compat: new `omitempty` fields — old agents load fine.
- **Verdict: avoid.** Two engines, cached stale state, no correctness benefit.

**Option 2B — Derive during Validate/Compile; include in compiled AgentSpec only:**
Canvas JSON stays clean (config only). The compiler derives `Inputs`/`Outputs` for each step
during Validate and CompileForPublish. The derived form is part of the `AgentSpec`/`StepSpec`
that gets persisted in `agent_runtime_specs.spec`.
- Source of truth: canvas JSON (config) → Go compiler (derivation) → AgentSpec (computed
  contract). Single derivation path.
- Validation: compiler stage 5 has full access to all steps' derived vars simultaneously.
- Debugger: the compiled spec (available post-publish) contains the authoritative contract;
  the debug runner can read it from the spec.
- Backward compat: add `Inputs`/`Outputs` to `StepSpec` as `omitempty` — old published specs
  lack them but runtime ignores them. Old canvas definitions are re-derived on next publish.
- Migration cost: low. No canvas JSON migration needed. Existing definitions work unchanged.
- **Verdict: correct.** Single engine, no drift, clean separation of config vs contract.

**Option 2C — Derive on demand; do not persist at all:**
Derivation runs at validate/compile time for issue generation only. Nothing is written into
AgentSpec or StepSpec.
- Source of truth: canvas JSON only.
- Validation: works for publish-time issue generation.
- Debugger: cannot read a persisted contract; must re-derive from config at debug time, which
  means running Go derivation logic in TypeScript again — back to the dual-engine problem.
- Runtime: if the interpreter ever needs to know a step's declared outputs (e.g., for
  partial enforcement), it must re-parse config at execution time rather than reading a
  pre-compiled contract.
- **Verdict: acceptable short-term, but a dead end.** Forces dual derivation to serve the
  debugger and any future runtime enforcement. Option 2B costs little more and avoids
  regressions.

**Decision: Option 2B.** Derive during Validate/Compile; store in compiled `AgentSpec.StepSpec`
only. Canvas JSON (save path) carries config only and is never augmented with derived fields.

### Q3 — Is VarRef.PortKey necessary?

**No — remove it in this phase.**

The original proposal included `PortKey string` with values like `"extractions.0.var"`, coupling
the identity of an output port to its position in the config JSON array. This is fragile:
reordering `extractions` would change the port identity of every extraction var.

What is actually needed in this phase:

```go
type VarRef struct {
    Name     string `json:"name"`      // variable name in PipelineVars — the identity
    Required bool   `json:"required"`  // missing upstream writer → error vs warning
}
```

`Name` is the stable identity: it is the string key in `PipelineVars`. If two steps both write
`"city1_lat"`, the collision is detectable by name alone. If an extraction var is renamed in
config, the old name disappears from `Outputs` and the new name appears — which is correct
behaviour, not a breakage.

`PortKey` would matter only if the model needed to distinguish *which config location* declares
a given output (e.g., to drive UI highlighting of the exact config field that produces a named
var). That is a future UX concern, not a validation or runtime concern. Defer it.

If a stable port identity independent of variable name is ever needed (for true explicit
bindings where a port can be re-bound to a different variable), that is the moment to introduce
a `PortID string` — a stable UUID-like identifier assigned at canvas authoring time. That
is explicitly a future concern and belongs in the explicit-bindings phase.

---

## 3. Refined Architecture: Approach A (v2)

### Core principle

> The Go compiler is the single source of derivation truth.
> Canvas JSON carries config only.
> Derived data-flow contracts are computed during Validate/Compile and stored only in the
> compiled AgentSpec.
> The frontend uses local heuristics for live UX; it is never the authority.

### New types

```go
// VarRef describes one variable a step reads from or writes to PipelineVars.
// Computed by NodeDef.DeriveInputs / DeriveOutputs from instance config;
// stored in StepSpec after compilation. Not present in canvas JSON.
type VarRef struct {
    Name     string `json:"name"`     // PipelineVars key
    Required bool   `json:"required"` // if true, missing upstream writer = publish error
}

// StepSpec gains two optional fields (omitempty — backward compatible)
type StepSpec struct {
    ID       string          `json:"id"`
    Type     StepType        `json:"type"`
    Config   json.RawMessage `json:"config"`
    Next     []string        `json:"next"`
    Branches []BranchArm     `json:"branches,omitempty"`
    // Derived by compiler — absent in canvas JSON, present in compiled AgentSpec
    Inputs   []VarRef        `json:"inputs,omitempty"`
    Outputs  []VarRef        `json:"outputs,omitempty"`
}
```

```go
// NodeDef gains two optional derivation hooks (json:"-", same pattern as Validate/Execute)
type NodeDef struct {
    // ... existing fields unchanged ...
    Validate      func(step canvasStep) []Issue
    Execute       func(ctx, interp, ic, step, vars, result) error
    // New — nil means "no static derivation available for this type"
    DeriveInputs  func(cfg json.RawMessage) []VarRef
    DeriveOutputs func(cfg json.RawMessage) []VarRef
}
```

### Where derivation runs

Both `agentgen.Validate` and `agentgen.CompileForPublish` call a new internal function
`deriveDataFlow(steps []canvasStep) map[string]StepVarInfo` that:

1. Looks up `NodeDef` for each step type.
2. Calls `nd.DeriveInputs(step.Config)` and `nd.DeriveOutputs(step.Config)` if non-nil.
3. Returns per-step `Inputs`/`Outputs`.

For `Validate`: derivation results feed into the new stage 5 (validation only; `*AgentSpec`
is discarded as today).
For `CompileForPublish`: derivation results are embedded into the compiled `StepSpec.Inputs` /
`StepSpec.Outputs` before the spec is marshalled and persisted.

The canvas JSON (save path) is never touched. The `PUT /agent-definitions/{id}` handler does
not change.

### New compiler stage 5 — data-flow validation

Called after stage 4 (executability check). Operates on the topo-sorted step list:

```
writers := map[string]string{}    // varName → stepID that last wrote it

for each step in topo order:
    for each VarRef in step.Inputs:
        if VarRef.Required and varName not in writers:
            emit error UNRESOLVED_INPUT (step, varName)
        elif varName not in writers:
            emit warning UNRESOLVED_INPUT (step, varName)

    for each VarRef in step.Outputs:
        if varName already in writers:
            emit warning DUPLICATE_WRITER (step, varName)   // overwrite, not necessarily wrong
        writers[varName] = step.ID
```

Issue severity policy:

| Condition | Validate severity | Publish severity |
|---|---|---|
| Required input var has no upstream writer | warning | **error** |
| Optional input var has no upstream writer | warning | warning |
| Two steps write same var (overwrite) | info (suppressed) | info |
| Step declares output that nothing downstream reads | — (not checked) | — |

"Nothing downstream reads it" is deliberately not an error — the shared PipelineVars model
means a var may be a legitimate side-effect written for inspection/debugging. Flag it only if
an explicit "no-read" lint mode is requested in the future.

### Boundary: derived contract vs explicit bindings

**Approach A (this phase) provides:**
- An authoritative derived list of what each step instance reads and writes.
- Compiler validation that required reads have an upstream writer.
- Debugger access to the compiled contract (read from spec, no re-derivation in TypeScript).
- No change to PipelineVars semantics — the runtime remains permissive.

**Approach A does NOT provide:**
- Enforced data pipes: the runtime does not check that `http_response` actually came from the
  HTTP node immediately upstream vs. some earlier HTTP node that also wrote `http_response`.
- Port-to-port wiring: there is no model for "HTTP node port 'extractions.token' →
  LLM node port 'context_input'". Variables are still matched by name in PipelineVars.
- Type checking on variable values.

**How Approach A is a safe stepping stone to explicit bindings (future phase):**

The compiled `StepSpec.Inputs`/`Outputs` establish the vocabulary and schema for a future
binding model. When explicit bindings are added:

1. An `Edge` struct gains a `VarName string` field that names the variable crossing it.
2. `StepSpec.Inputs` gains a `SourceStepID string` field (populated from the edge binding).
3. The interpreter reads `StepSpec.Inputs` at execution time and verifies that the named variable
   was written by the declared source step, not just "any upstream step."
4. The compiler checks that every declared binding refers to a real output port of the source step.

This is additive: `VarRef.SourceStepID` is empty in Approach A (advisory), populated in the
explicit-binding phase (enforced). No rewrite of the interpreter is needed — only a new
pre-execution check gate. No backward-compatibility break — old specs without bindings remain
permissive; new specs with bindings get enforcement.

### Frontend interaction (unchanged authoring; improved feedback)

**Canvas authoring (unchanged):**
- `PUT /agent-definitions/{id}` body: raw canvas JSON only. No `inputs`/`outputs` fields.
- `nodeVars.ts` continues to run locally for live READS/WRITES hints and edge labels.
  Its output is never serialised. This is explicitly heuristic — approximate, fast, display-only.

**Post-validate feedback (improved):**
- The validate endpoint already returns `[]Issue` to the frontend on debounce.
- New stage 5 issues (`UNRESOLVED_INPUT`) appear in the canvas as red node annotations,
  same as today's structural issues. No new frontend work for the validation display path.

**Post-publish (new capability):**
- The compiled `AgentSpec` (in `agent_runtime_specs.spec`) now contains `StepSpec.Inputs` /
  `StepSpec.Outputs`. A new endpoint (or extension of the existing get-definition endpoint)
  can return the compiled contract to the frontend.
- The canvas debugger can read the compiled contract for the running spec instead of
  re-deriving from raw config via `nodeVars.ts`. This eliminates the dual-derivation problem
  for the debug path. **`nodeVars.ts` becomes a live preview only; the compiled spec is truth.**

### Branch, Parallel, Loop, Human Wait, A2A

**Branch:** `DeriveInputs` returns `[{Name: expressionVars..., Required: false}]`.
`DeriveOutputs` returns `[]` (branch writes nothing to PipelineVars).
Branch edges remain control-flow only. This is correct and needs no change.

**Parallel:** `DeriveOutputs` returns `[{Name: cfg.MergeVar, Required: false}]`.
Parallel body steps are sub-lists in `Branches [][]string` — the compiler already validates
their IDs. Data-flow validation within parallel branches follows the same topo-walk, treating
each branch as a sub-graph that inherits the parent's `writers` map.

**Loop:** `DeriveOutputs` returns `[{Name: cfg.AccumVar, Required: false}]`.
Loop body steps are validated similarly. Condition template vars derive `DeriveInputs`.

**Human Wait:** `DeriveOutputs` returns `[{Name: cfg.ReplyVar, Required: false}]`.

**A2A Call:** `DeriveInputs` returns `[{Name: cfg.InputVar, Required: true}]`.
`DeriveOutputs` returns `[{Name: cfg.OutputVar, Required: false}]`.

### `ExposedVars` deprecation

`TransformStepConfig.ExposedVars []string` is confirmed dead code in the Go interpreter —
`execTransform` never reads it. With `DeriveOutputs` on `NodeDef`, the transform type can
derive its outputs from `functions[].output_var` directly (more precise: it lists only the vars
actually produced by the function chain, not a manually maintained parallel list).

`ExposedVars` should be **formally deprecated in this same implementation pass**:
- Add a compiler warning `DEPRECATED_EXPOSED_VARS` when a step config contains non-empty
  `ExposedVars` (for existing agents that set it).
- Remove it from `TransformStepConfig` in a subsequent pass once no stored definitions use it
  (can check `agent_definitions.definition` for any that set it before removing).

---

## 4. Comparison of All Approaches (updated)

| Dimension | A (v2) — derive in compiler | B — port registry on NodeDef | C — frontend only |
|---|---|---|---|
| Single derivation engine | ✓ Go compiler only | ✓ Go registry | ✗ TS + Go |
| Persisted derived state | In compiled AgentSpec only | In compiled AgentSpec | In canvas JSON or not at all |
| Canvas JSON stays clean | ✓ | ✓ | ✗ (if 2A) or ✓ |
| Runtime change | None | Interpreter reads port bindings | None |
| Dynamic output support | DeriveOutputs hook per type | "variadic" escape hatch | heuristic regex |
| Data-flow validation at publish | New compiler stage 5 | Full port binding validation | None (or TS-only) |
| Debugger uses compiled contract | ✓ (post-publish) | ✓ | ✗ (must re-derive in TS) |
| Backward compatibility | Full (omitempty) | Requires migration script | Full |
| Path to explicit bindings | Additive (VarRef.SourceStepID) | Natural (port wiring model) | Rewrite needed |
| Migration cost | Low–Medium | Very high | Low |

**Recommendation: Approach A (v2).** Approach B remains the correct long-term destination if
type-checked port wiring becomes a product requirement. The migration cost of B is justified
only when the team has validated that name-matched PipelineVars is insufficient at product scale.
Approach A's `Inputs`/`Outputs` on `StepSpec` and the `DeriveOutputs` hooks on `NodeDef` are
explicit stepping stones toward B — the vocabulary and schema are already right; only
enforcement and port identity are deferred.

---

## 5. Implementation Sequence (when approved)

Each step is a focused, independently testable change. No step requires completing a later one.

**Step 1 — Types only** (Go)
Add `VarRef` struct. Add `Inputs`/`Outputs []VarRef` to `StepSpec` as `omitempty`. Add
`DeriveInputs`/`DeriveOutputs func(json.RawMessage) []VarRef` to `NodeDef` as `json:"-"`.
No behaviour change. All existing tests pass. Commit.

**Step 2 — Derivation hooks per node type** (Go)
Implement `DeriveInputs`/`DeriveOutputs` for all 11 registered node types in `nodes.go`.
Wire the derivation call into an internal `deriveDataFlow` helper called from both `Validate`
and `CompileForPublish`. Write unit tests per type (same pattern as existing `transform_test.go`).
Deprecate `ExposedVars` with a compiler warning. Commit.

**Step 3 — Embed in compiled spec** (Go)
`CompileForPublish` sets `step.Inputs` / `step.Outputs` on each `StepSpec` before marshalling
to `agent_runtime_specs.spec`. `Validate` uses the derived form for stage 5 only (not
persisted). Existing published specs in the DB are unaffected (they lack the fields; old
runtime ignores them). Commit.

**Step 4 — Compiler stage 5** (Go)
Add data-flow validation: topo-walk, accumulate `writers` map, emit `UNRESOLVED_INPUT` issues.
Update `go/TEST_INDEX.md` and `scripts/tests/INDEX.md`. Run full test suite. Commit.

**Step 5 — Frontend debugger reads compiled contract** (TypeScript)
Extend the agent definition or debug endpoint to return the compiled spec's `Inputs`/`Outputs`
for each step. Canvas debugger reads these instead of calling `extractNodeVars` live. `nodeVars.ts`
is retained as the live-preview engine only. Commit.

**Step 6 — `ExposedVars` removal** (Go + DB migration)
After confirming no stored definitions use `ExposedVars` (query `agent_definitions.definition`),
remove the field from `TransformStepConfig`. Commit.

---

## 6. Risks and Open Questions

- **Template-based reads are partially unanalysable.** Go `text/template` supports functions,
  conditionals, and computed variable names that the regex-based derivation cannot parse.
  `DeriveInputs` for LLM and HTTP nodes will miss template-function-derived reads. Accept this:
  these are declared `Required: false` and produce warnings, not errors.

- **The save/publish gap.** Because publish reads the last-saved DB definition (not the current
  canvas state), a user can have a validated canvas that produces different issues at publish
  time if they changed derivation logic (e.g., upgraded a node type) between save and publish.
  This gap exists today and is not introduced by Approach A.

- **`buildvalidator/` does not exist yet.** Stage 5 should be added to `compiler.go` following
  the existing four-stage pattern. Do not create a new package for it.

- **The AI Transform Assistant UI stub** in `TransformPanel.tsx` (line 339–352) is dead code.
  If a Canvas Copilot feature is built in the future, it will need access to the compiled
  data-flow contract (Approach A Step 5) to suggest correct variable bindings. Ensure the
  compiled spec API is designed with that consumer in mind.

- **Parallel branch sub-graph validation** (stage 5) needs careful treatment: parallel branches
  run concurrently but share PipelineVars. A variable written in branch A is visible to branch
  B — this is a known shared-store race condition that Approach A does not resolve (it is a
  runtime semantics issue, not a compiler issue). Flag it as a known limitation in the
  eventual implementation doc.
