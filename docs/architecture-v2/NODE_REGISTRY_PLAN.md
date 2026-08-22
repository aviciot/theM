# Canvas Node Definition Registry — Implementation Plan
# Created: 2026-08-22

## Problem

Node type logic is scattered across four places today:

| Location | What it hardcodes |
|---|---|
| `go/internal/agentgen/compiler.go` | `knownStepTypes` map, per-type credential slot validation |
| `go/internal/agentgen/interpreter.go` | `executeStep` switch, per-type exec funcs |
| `go/internal/agentgen/spec.go` | `StepType` constants, per-type config structs |
| `frontend/.../builder/page.tsx` | `STEP_META`, `STEP_INPUT_FIELD`, `SINGLE_INPUT_TYPES`, config panels, connection rules, debug execution |

Adding a new node type (e.g. `branch`) requires touching all four independently, with no central contract.

---

## Solution

One `NodeDef` struct per type that declares everything about it. Two registries — Go (runtime authority) and TypeScript (UI authority) — using the same type names and field names.

---

## Go Registry — `go/internal/agentgen/noderegistry.go`

### NodeDef struct

```go
type NodeDef struct {
    Type        StepType
    // Validate checks config fields and slot references for this step type.
    // Returns CompileErrors (empty = valid).
    Validate    func(step canvasStep, knownSlots map[string]bool) []CompileError
    // Execute runs the step at runtime. nil = "not implemented".
    Execute     func(ctx context.Context, interp *Interpreter, ic *InvocationContext,
                     step *StepSpec, vars PipelineVars, result *ExecutionResult) error
    // OutputArity: "single" (one next), "multi" (named arms/branches), "none" (terminal)
    OutputArity string
    // IsSink: true means this node terminates the pipeline (response, stream_out)
    IsSink      bool
    // IsSource: true means this node is a valid pipeline start (input)
    IsSource    bool
}
```

### Registry

```go
var nodeRegistry = map[StepType]*NodeDef{}

func RegisterNode(def NodeDef) {
    nodeRegistry[def.Type] = &def
}

func LookupNode(t StepType) (*NodeDef, bool) {
    d, ok := nodeRegistry[t]
    return d, ok
}
```

### Registration — `go/internal/agentgen/nodes/`

Each node type gets its own file:
- `nodes/input.go` — registers `input`
- `nodes/llm.go` — registers `llm`
- `nodes/http.go` — registers `http`
- `nodes/transform.go` — registers `transform`
- `nodes/response.go` — registers `response`
- `nodes/branch.go` — registers `branch` (Execute=nil for now)
- `nodes/loop.go`, `nodes/parallel.go`, `nodes/a2a_call.go`, `nodes/human_wait.go`, `nodes/stream_out.go`

Each file calls `agentgen.RegisterNode(agentgen.NodeDef{...})` in an `init()` function, or all registrations are collected in a `nodes/register.go` that `agentgen` imports via blank import.

### Compiler changes

`knownStepTypes` map → `_, ok := LookupNode(step.Type)`
Per-type credential validation switch → each type's `NodeDef.Validate` is called

### Interpreter changes

`executeStep` switch → lookup `NodeDef`, call `def.Execute`. If `Execute == nil`, return `"not implemented"` error.

---

## Frontend Registry — `frontend/src/lib/nodeRegistry.ts`

```ts
export interface NodeDef {
  type: string;
  label: string;
  emoji: string;
  bg: string;
  border: string;
  // outputArity: "single" | "multi" | "none"
  outputArity: 'single' | 'multi' | 'none';
  isSource: boolean;   // can be pipeline start
  isSink: boolean;     // terminates pipeline
  // inputField: the variable name field in config (for connection hints)
  inputField?: string;
  // singleInput: true = only one incoming edge allowed
  singleInput?: boolean;
  // summary: function to derive the node subtitle shown in the canvas
  summary?: (cfg: Record<string, unknown>) => string;
  // ConfigPanel: React component to render config UI (null = not yet supported)
  ConfigPanel: React.ComponentType<ConfigPanelProps> | null;
}
```

Each node type is declared once:

```ts
export const NODE_REGISTRY: Record<string, NodeDef> = {
  input:      { type: 'input',     label: 'Input',      emoji: '📥', outputArity: 'single', isSource: true,  isSink: false, ... },
  llm:        { type: 'llm',       label: 'LLM',        emoji: '🧠', outputArity: 'single', isSource: false, isSink: false, singleInput: true, ... },
  http:       { type: 'http',      label: 'HTTP',       emoji: '🌐', outputArity: 'single', isSource: false, isSink: false, ... },
  transform:  { type: 'transform', label: 'Transform',  emoji: '⚙️', outputArity: 'single', isSource: false, isSink: false, singleInput: true, ... },
  response:   { type: 'response',  label: 'Response',   emoji: '📤', outputArity: 'none',   isSource: false, isSink: true,  singleInput: true, ... },
  branch:     { type: 'branch',    label: 'Branch',     emoji: '🔀', outputArity: 'multi',  isSource: false, isSink: false, ... },
  // ... remaining types
};
```

### Builder changes

- `STEP_META` → derived from `NODE_REGISTRY`
- `STEP_INPUT_FIELD` → derived from `NODE_REGISTRY`
- `SINGLE_INPUT_TYPES` → derived from `NODE_REGISTRY`
- Connection validation → use `def.outputArity`, `def.isSink`, `def.singleInput`
- Config panels → moved to separate components, referenced via `def.ConfigPanel`
- `StepNode` subtitle → `def.summary(cfg)` 
- Debug execution → calls per-type handler looked up from registry

---

## What does NOT change

- `spec.go` — `StepType` constants and config structs stay; they are the data model, not behavior
- `AgentSpec`, `SkillSpec`, `StepSpec` — unchanged
- Compiler's topo-sort, cycle detection, dangling-edge checks — unchanged (structural, not per-type)
- DB schema — nothing changes

---

## File plan

### New Go files
- `go/internal/agentgen/noderegistry.go` — `NodeDef`, `RegisterNode`, `LookupNode`
- `go/internal/agentgen/nodes/input.go`
- `go/internal/agentgen/nodes/llm.go`
- `go/internal/agentgen/nodes/http.go`
- `go/internal/agentgen/nodes/transform.go`
- `go/internal/agentgen/nodes/response.go`
- `go/internal/agentgen/nodes/stubs.go` — branch, loop, parallel, a2a_call, human_wait, stream_out (Execute=nil)

### Modified Go files
- `go/internal/agentgen/compiler.go` — use registry for type check + validation
- `go/internal/agentgen/interpreter.go` — use registry for dispatch

### New TS files
- `frontend/src/lib/nodeRegistry.ts` — full registry
- `frontend/src/app/admin/agents/builder/configPanels.tsx` — extracted config panel components

### Modified TS files
- `frontend/src/app/admin/agents/builder/page.tsx` — replace hardcoded maps with registry lookups

---

## Test plan

- All existing `go test ./internal/agentgen/...` tests must pass unchanged
- Add `noderegistry_test.go`: verify all 11 types are registered, `LookupNode` returns correct `OutputArity`/`IsSink`/`IsSource`, compiler rejects unknown type via registry
- Frontend: TypeScript compilation must pass (no new type errors)

---

## Scope boundary

This plan covers the registry scaffolding and wiring only — it does NOT implement `branch`, `loop`, `parallel` execution (those remain `Execute=nil` stubs, same as today). The registry makes adding them a single-file change in the future.
