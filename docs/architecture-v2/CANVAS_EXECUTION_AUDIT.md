# Canvas Ports UI & Execution Audit
# Date: 2026-08-29
# Branch: origin/main (HEAD b5767b0 + canvas fixes)
# Scope: read-only inspection — no code changed

---

## 1. Canvas Ports UI

### A. Which nodes get the ⊕ button?
**Verdict: OK**

`StepNode.tsx:439` — unconditional for all nodes rendered as `StepNode`.
`BuilderCanvas.tsx:22–25` — `nodeTypes = { agentRoot: AgentRootNode, skill: SkillNode, step: StepNode }`.
All 12 Go step types are registered with `type: 'step'` in `useSkillPipeline.ts:58–72`, so all receive `StepNode` and therefore the ⊕ button.

---

### B. Do all Go Step types use `StepNode`?
**Verdict: OK**

All 12 `StepType` constants (`StepInput`, `StepLLM`, `StepHTTP`, `StepTransform`, `StepResponse`, `StepBranch`, `StepLoop`, `StepParallel`, `StepA2ACall`, `StepHumanWait`, `StepStreamOut`, `StepMCPCall`) are added to the ReactFlow graph with `type: 'step'` → all render via `StepNode`.

---

### C. Should `AgentRootNode` / `SkillNode` expose ports?
**Verdict: OK — no ports needed**

- `AgentRootNode.tsx:11` — one unnamed source Handle; structural only (connects agent card to skill).
- `SkillNode.tsx:11–12` — one unnamed source + one target Handle; structural layout node.

Neither carries pipeline variables. PortsPopover is not appropriate here.

---

### D. Duplicate Handle IDs between StepNode and PortsPopover
**Verdict: CRITICAL BUG**

`StepNode.tsx:82–111` renders `data-in-{portID}` target Handles directly on the node.
`StepNode.tsx:384–393` renders `data-out-{portID}` source Handles directly on the node.
`PortsPopover.tsx:116–129` renders source Handles with `id = data-out-{varname}`.
`PortsPopover.tsx:143–154` renders target Handles with `id = data-in-{varname}`.

**When PortsPopover is open, both StepNode and PortsPopover emit the same Handle IDs on the same node.** ReactFlow requires unique handle IDs per node — duplicates cause silent edge misrouting (edges snap to whichever handle ReactFlow finds first).

Fix: remove the invisible `data-in-*`/`data-out-*` Handles from StepNode (lines 82–111, 384–393) and let PortsPopover be the sole source. Or remove them from PortsPopover and keep them only in StepNode (making PortsPopover handle-free except for the real drag source).

---

### E. Card height incorrectly includes ctrl-port count
**Verdict: MEDIUM — cosmetic**

`StepNode.tsx:280–283`:
```ts
const maxPorts = Math.max(inputPorts.length, outputPorts.length);
const railH    = maxPorts > 0 ? PORT_START + maxPorts * PORT_STEP + PORT_START : 0;
const cardH    = Math.max(80, railH);
```

`inputPorts` always includes `ctrl-in` (1 entry); `outputPorts` includes all ctrl-out ports. For branch: `outputPorts = [ctrl-out-true, ctrl-out-false]` → `maxPorts=2` → `cardH=100`. Ctrl handles are positioned absolute at the card edge and don't consume rail space — only data ports need vertical rail. Branch is ~20px taller than necessary.

Fix: count only data ports: `Math.max(inputPorts.filter(p => p.kind==='data').length, outputPorts.filter(p => p.kind==='data').length)`.

---

### F. PortsPopover never auto-closes
**Verdict: HIGH BUG**

`StepNode.tsx:175` — `showPortsPanel` is local state, toggled only by ⊕ click.
`BuilderCanvas.tsx:128,130,154,156` — `onNodeClick`/`onPaneClick` update `selectedNode` and close the context menu but do **not** close any node's PortsPopover.

Result: clicking a different node or the canvas background leaves the previous popover visible. Multiple popovers can be open simultaneously.

Fix: lift close signal to canvas level — broadcast via a React context or a `closeAllPopovers` callback passed into `StepNode` from `BuilderCanvas`.

Also: `StepNode.tsx:182–185` computes `hasCtrlIn`/`hasCtrlOut` but never uses them — dead code, should be removed.

---

## 2. Execution and Temporal

### A. Is `CompileExecutionPlan` used in production?
**Verdict: OK**

`go/cmd/agent-runtime/main.go:294`:
```go
plan := agentgen.CompileExecutionPlan(skill)
execResult, err := agentgen.NewLocalExecutor(rt.interp).Execute(ctx, ic, plan, initial)
```
All other callsites are `_test.go` only.

---

### B. Is `LocalExecutor` wired into agent-runtime?
**Verdict: OK**

Confirmed at `go/cmd/agent-runtime/main.go:295`. `Interpreter.Execute` (the sequential loop) is NOT called from production code — only from tests. Production path: `LocalExecutor.Execute` → `runBranch` → `execNode` → `interp.executeStep` (single-node dispatch).

---

### C. Does production still fall back to sequential Next[0]?
**Verdict: OK — dead in production**

`go/internal/agentgen/interpreter.go:85` — `Interpreter.Execute` exists and is exported but is called only from `agentgen_test.go`. The sequential loop (which takes only `Next[0]` and cannot fan out) is effectively dead in production.

---

### D. Is `TemporalExecutor` implemented?
**Verdict: MISSING**

`grep -rn "TemporalExecutor" go/` — zero hits in production code. `go/internal/agentgen/executor.go` defines the `ExecutionBackend` interface with a comment noting TemporalExecutor as planned. `LocalExecutor` is the sole implementation.

---

### E. How does Temporal currently execute canvas agents?
**Verdict: OK for current architecture — Temporal ≠ DAG executor**

`go/cmd/worker/main.go:184–190` registers `OrchestrationWorkflow` + `RunOrchestratorActivity`.
`activities.go:116` calls `runner.Run(...)` → `orchestrator.Orchestrator` (multi-turn LLM tool loop).

Canvas agents (`canvas_a2a` transport) are invoked via: `OrchestrationWorkflow` → `RunOrchestratorActivity` → `orchestrator.Run()` → LLM tool call → `agentregistry.InvokeForRun()` → **HTTP POST to `them-agent-runtime`** → `LocalExecutor`.

**Temporal runs the whole orchestrator LLM loop as one long-running activity, not individual DAG nodes.** These are separate concerns — Temporal owns the multi-turn conversation loop; LocalExecutor owns the intra-agent DAG. This is the intended architecture.

---

### F. Any DB/UI setting for `execution_backend`?
**Verdict: MISSING — acceptable**

No column, env var, config field, or UI toggle exists. `LocalExecutor` is hardcoded at `main.go:295`. Not a problem until `TemporalExecutor` is implemented.

---

## Blockers by severity

| Severity | ID | Finding | File |
|---|---|---|---|
| **CRITICAL** | UI-D | Duplicate Handle IDs: PortsPopover + StepNode emit same `data-in-*`/`data-out-*` IDs simultaneously — silent ReactFlow edge misrouting | `StepNode.tsx:82–111,384–393`, `PortsPopover.tsx:116–154` |
| **HIGH** | UI-F | PortsPopover never auto-closes; multiple popovers can be open simultaneously | `StepNode.tsx:175`, `BuilderCanvas.tsx:128–156` |
| **MEDIUM** | UI-E | Card height counts ctrl ports → branch is ~20px taller than needed | `StepNode.tsx:280–283` |
| **LOW** | UI-F2 | `hasCtrlIn`/`hasCtrlOut` dead vars in StepNode | `StepNode.tsx:182–185` |
| **LOW** | EX-D | `TemporalExecutor` not implemented; `ExecutionBackend` has one implementation | `go/internal/agentgen/executor.go` |

---

## Recommended implementation order

1. **Fix duplicate Handle IDs (CRITICAL)** — Remove invisible `data-in-*`/`data-out-*` Handles from `StepNode` (they are 1×1 opacity-0); let `PortsPopover` be the sole source of those handles. The StepNode handles were originally added so edges could land without the popover being open — instead, keep them in StepNode and remove them from PortsPopover (PortsPopover becomes display-only for IN ports, drag-only for OUT via real Handle).

2. **Auto-close PortsPopover on canvas/node click (HIGH)** — Add a `closePortsPanel` prop (or a `PortsPanelContext`) to `StepNode`. `BuilderCanvas.tsx` `onNodeClick` and `onPaneClick` broadcast close. Alternatively: close on `onMouseLeave` with a 300ms delay (simpler but less precise).

3. **Fix card height formula (MEDIUM)** — Count only data ports in `maxPorts`; ctrl handles are absolute-positioned and don't need rail space.

4. **Remove `hasCtrlIn`/`hasCtrlOut` dead code (LOW)** — Clean up `StepNode.tsx:182–185`.

5. **TemporalExecutor (LOW, future session)** — Implement `internal/agentgen/temporal_executor.go` as a second `ExecutionBackend`; each `PlanNode` schedules as a Temporal child activity. Requires new task queue, workflow shape, and an `execution_backend` config on `AgentSpec`. Separate session — significant scope.

6. **`execution_backend` toggle (LOW, after #5)** — DB column on `agent_runtime_specs`, surfaced in the builder publish panel; defaults to `"local"`.
