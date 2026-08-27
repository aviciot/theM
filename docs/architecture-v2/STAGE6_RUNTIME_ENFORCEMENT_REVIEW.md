# Stage 6 Runtime Enforcement — Architecture Review

**Date:** 2026-08-27
**Scope:** Runtime enforcement of data-flow contracts on the interpreter execution loop
**Codebase revision:** Steps 1–6 complete. `TransformStepConfig.ExposedVars` removed (fa879b7). Transform outputs derived authoritatively from `functions[].output_var`. Do not reintroduce any exposed_vars or parallel output declaration mechanism.

---

## Q1: Is Scoped Visibility Over Shared ExecutionState the Right Model?

**Finding: Adopt the input-scoping half now. Defer the return-value half.**

The current interpreter (`interpreter.go` `executeStep`) calls `def.Execute(..., vars, result)` passing the global `PipelineVars` map directly. Every Execute implementation mutates it in place: `vars[cfg.OutputVar] = out` (LLM), `vars[ext.Var] = val` (HTTP), `tvars` copy-then-merge-back (transform).

The input-scoping half is independently valuable and low-risk. Before calling Execute, build a scoped map containing only the keys named in `step.Inputs`. Pass the scoped map as `vars`. The node physically cannot read undeclared vars — they are absent. This does not require changing the Execute signature. The scoped map IS `PipelineVars`; passing it is a one-line substitution in `executeStep`.

The output-merging half — having Execute return a `NodeResult` — requires changing the signature of all 6 registered Execute closures simultaneously. They are anonymous function literals on `NodeDef` structs in the `init` block of `nodes.go`; they cannot be partially migrated. There is no incremental path. The good news: the interpreter can enforce output contracts without a signature change, by reading `step.Outputs` after Execute returns and promoting only declared keys from the scoped map back to global state. This gives 80% of the benefit at near-zero refactoring cost.

**Verdict:** Adopt scoped input resolution + post-Execute output-only promotion. Defer the NodeResult signature change until parallel execution makes it structurally necessary.

---

## Q2: Are There Go Map/Slice/Reference Mutation Risks?

**Finding: The risk is real but does not require deep-copy today. Convention is sufficient; document the known gap.**

`PipelineVars` is `map[string]any`. Values can be `map[string]any` (HTTP response body, stored at `interpreter.go` `execHTTP` merge loop) or `[]any` (transform functions like `split`, `json_keys`). Shallow-copying a subset of the global map into a scoped NodeInputs map creates two map variables pointing to the same underlying inner maps. If a node mutates an inner object (e.g., adds a key to `vars["http_response"].(map[string]any)`), that mutation is visible in global state even though the node received a "scoped" copy.

The transform executor is the most exposed path: `transform.Execute` receives `transform.Vars` built by shallow-copying `vars`. All registered transform functions currently return `string` outputs, so no inner-map mutation occurs in practice. The risk is prospective for future transform functions that return structured objects.

Deep-copy (reflection or JSON round-trip per step) is not justified today — there is no parallel execution, and the cost would be paid on every step for every agent run. The right trigger is `StepParallel` execution, at which point concurrent goroutines accessing the same map would cause data races that the race detector would catch immediately.

**Verdict:** Convention only: Execute functions MUST NOT mutate values retrieved from their input vars; they may only write to declared output keys. Document this explicitly. Add deep-copy only when `StepParallel` is implemented.

---

## Q3: Should Execute Evolve Toward `NodeInputs -> NodeResult`?

**Finding: Premature. The runtime context is not decomposable. Only the vars parameter needs scoping.**

Current signature:
```go
func(ctx context.Context, interp *Interpreter, ic *InvocationContext, step *StepSpec, vars PipelineVars, result *ExecutionResult) error
```

What each parameter actually provides:
- `ctx`: cancellation to HTTP and LLM calls. Non-negotiable.
- `interp`: carries `httpClient`, `llmFactory`, `platformAPIKey`, and `nextStepOverride`. The `nextStepOverride` field is the branch side-channel — a mutation on the interpreter struct that the interpreter reads after Execute returns. This is the one genuinely awkward coupling, but it works correctly in a single-goroutine sequential interpreter and is self-clearing.
- `ic *InvocationContext`: carries `AppAPIKey`, `AgentParams`, `AppGlobalParams`, `NodeLLMOverrides` — credentials and per-request config that must never appear in PipelineVars or logs. Must remain as runtime context.
- `step *StepSpec`: the node reads its own `Config json.RawMessage`. Unavoidable.
- `vars PipelineVars`: the mutation surface. This is the only parameter that needs to change (scoped instead of global).
- `result *ExecutionResult`: only `execResponse` uses this.

A `NodeInputs -> NodeResult` model could encapsulate `vars` and `result`, but `interp`, `ic`, and `ctx` cannot go into NodeInputs — they carry credentials and HTTP/LLM clients. A signature change is a big-bang refactor of all 6 closures, adds two new types, and delivers no new capability for sequential execution. The `nextStepOverride` side-channel would need to move to `NodeResult.NextStepID`, which is the right fix long-term but not needed now.

**Verdict:** Do not change the Execute signature in Stage 6. The only productive change at the call site is passing a scoped vars map. `interp` and `ic` must remain as runtime context.

---

## Q4: Which Improvements Are Worth Doing Now vs. Postponing?

**a. Scoped input resolution — DO NOW**
Resolve `step.Inputs` from global state before calling Execute. Build `scopedVars` from declared input names only. ~15 lines in `executeStep`. Zero signature change. Immediately enables contract checking. Required input absent → structured error.

**b. Output-only promotion — DO NOW**
After Execute returns, iterate `step.Outputs` and copy only declared output keys from scoped map back to global state. Undeclared mutations are silently dropped. Requires updating `execTransform` to write into the received (scoped) vars instead of a separate `tvars` copy — `transform.Execute` already writes to the passed-in `Vars` map, so after scoping this becomes the scoped map automatically. ~15 lines in `executeStep`, ~5-line change to `execTransform`.

Pre-implementation safety audit (all 6 Execute functions vs. their DeriveOutputs):
- `execInput`: writes `vars[varName]` where varName = `cfg.Bindings["text"]` or "input" — matches DeriveOutputs ✓
- `execLLM`: writes `vars[cfg.OutputVar]` or `vars["output"]` — matches DeriveOutputs ✓
- `execHTTP`: writes `vars[ext.Var]` per extraction + `vars["http_response"]` — matches DeriveOutputs ✓
- `execTransform`: writes to `tvars` (all `output_var` fields) then merges back — DeriveOutputs captures all function output_vars ✓
- `execBranch`: writes nothing to vars, writes to `interp.nextStepOverride` — DeriveOutputs returns nil ✓
- `execResponse`: writes nothing to vars, writes to `result *ExecutionResult` — DeriveOutputs returns nil ✓

Output promotion is safe to enable for all 6 types.

**c. Immutability enforcement on node inputs — DEFER**
True immutability requires deep-copy. See Q2. Convention only for now.

**d. Structured contract errors — DO NOW (zero cost)**
When scoped input resolution finds a Required input var absent from global state, return a structured `ErrContractViolation{StepID, VarName, Kind: "missing_required_input"}`. ~8 lines. Surfaces runtime contract violations as actionable errors rather than silent template zero-values.

**e. Structured read/write trace events — DEFER**
Requires a trace sink (where do events go — SSE? log buffer? run recorder?), a format, and integration. The transform subsystem already has `TraceResult` in `transform/executor.go` covering transform-level tracing. Generalizing to all node types is a separate feature. Defer to a dedicated tracing stage.

**f. Credential/secret redaction from traces — PARTIALLY NOW**
`InvocationContext` is already marked never-logged; redaction is already in place. No new action needed in Stage 6 beyond documenting the guarantee. If trace events (item e) are implemented later, make redaction a prerequisite before that stage.

**g. Large-payload reference model — DEFER (explicitly)**
No evidence of payload size pressure. Values are passed in-process with no serialization boundary. A reference model requires a storage backend and changes to every node. Belongs to a Temporal activity boundary design, not Stage 6.

**h. Temporal Activity boundary alignment — DEFER (explicitly)**
The interpreter runs the full pipeline in one in-process call. Temporal activities live in `internal/temporal/activities.go` (separate package). Re-entrant step execution across activity calls requires state serialization — a major architectural change. `StepSpec.Inputs/Outputs` compiled contracts are forward-compatible with this future model (already persisted in the spec), but the interpreter redesign is out of scope for Stage 6.

---

## Q5: Unnecessary Abstraction or Backward-Compatibility Risk?

**Risk 1: Execute signature change — highest risk, explicitly exclude**
Changing `Execute` to `NodeInputs -> NodeResult` is a big-bang refactor touching 4 files simultaneously (`nodes.go`, `noderegistry.go`, `interpreter.go`, `agentgen_test.go`) with no new capability for sequential execution. The `nextStepOverride` side-channel would need to move to `NodeResult`, which is a valid long-term fix but unnecessary now. **Explicitly defer.**

**Risk 2: Deep-copy in input scoping — medium risk**
If added "for safety," reflection-based or JSON-round-trip deep-copy on every step execution adds latency proportional to payload size. A large HTTP response body deep-copied into every downstream scoped vars map is expensive. **Convention only; no deep-copy.**

**Risk 3: Output promotion stripping undeclared writes — low risk, mitigated by audit**
If any existing agent relies on a node writing a var not in its DeriveOutputs list, that agent silently breaks after Stage 6. The audit above confirms all 6 implemented Execute functions match their DeriveOutputs exactly. The risk is zero for current code; future node implementations must be required to keep DeriveOutputs in sync (enforce in code review, document in `go/CLAUDE.md`).

**Non-risk: `type NodeInputs = PipelineVars` alias**
A type alias costs nothing and breaks nothing. Provides a named documentation hook for the scoped-input concept. Benign.

---

## Recommended Stage 6 Design and Implementation Boundary

**Implement:**

1. **Scoped input resolution** in `executeStep`: build `scopedVars PipelineVars` from `step.Inputs` before calling `def.Execute`. Pass `scopedVars` instead of global `vars`. Required input var absent → return `ErrContractViolation{StepID, VarName, Kind: "missing_required_input"}`.

2. **Output-only promotion** in `executeStep`: after Execute returns, iterate `step.Outputs` and copy only declared output keys from `scopedVars` into global `vars`. Update `execTransform`: remove the manual `tvars` copy-then-merge-back; write directly to the received (now scoped) vars, then let the interpreter promote declared outputs.

3. **`ErrContractViolation` error type**: `StepID string`, `VarName string`, `Kind string` ("missing_required_input"). Returned from the interpreter, not from node Execute functions.

4. **New tests**: contract enforcement for each of the 6 node types, missing-required-input error, undeclared-output-dropped behavior, fan-out (two downstream steps both reading the same upstream var from scoped copies).

5. **`go/CLAUDE.md` update**: add rule — Execute functions MUST NOT mutate values retrieved from input vars; future parallel nodes depend on this convention.

**Explicitly exclude:**

- Execute signature change to `NodeInputs -> NodeResult`
- Deep-copy of `any` values in scoped vars
- Structured per-var trace events
- Temporal activity boundary alignment
- Large-payload reference model
- `nextStepOverride` refactor
- Any form of `exposed_vars` or parallel output declaration for transform nodes

**Estimated scope:** ~60 lines net new/changed in `interpreter.go`, ~5 lines in `nodes.go` (`execTransform`), 1 new error type in `spec.go` or a new `errors.go`, new test file `dataflow_runtime_test.go`. No new packages. No signature changes. Backward-compatible for all deployed agents.
