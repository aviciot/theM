# AppFlow Phase A — Fork/Join Parallel Execution
# Created: 2026-09-17
# Status: COMPLETE (2026-09-17)

---

## Goal

Add Fork and Join nodes to the application canvas so a Temporal AppFlow workflow
can split into N parallel branches, execute agents concurrently, and merge results
before continuing.

This is the first step toward full parallel agent orchestration in Temporal mode.

---

## Background

Current AppFlow DAG (Phase 3) supports:
- Linear execution: EP → Agent
- LLM routing: EP → Router → Agent A or Agent B (one branch only)
- HIL pause/resume

It does NOT support:
- Running multiple branches in parallel
- Merging results from parallel branches
- Tree-like task graphs where branches continue independently

Fork/Join fills this gap using Temporal's native parallel activity execution
(`workflow.Go` goroutines inside the workflow).

---

## Architecture

```
EP → Fork → Branch A → Agent A → Join → continue
          → Branch B → Agent B ↗
```

- **Fork node**: splits flow into N parallel branches (one outgoing edge per branch)
- **Join node**: waits for all incoming branches to complete, merges results, continues

Fork/Join is pure workflow logic — no new Temporal activities needed.
Temporal's `workflow.Go` + channels handle the parallel execution.

---

## Progress Tracker

### Backend — Go

| Task | Status | Notes |
|---|---|---|
| `compiler.go`: add `fork` and `join` node kinds to `compileNode` | ✅ | |
| `compiler.go`: validate fork has ≥2 outgoing edges | ✅ | |
| `compiler.go`: validate join has ≥2 incoming edges | ✅ | |
| `workflow.go`: fork handling — launch parallel `workflow.Go` per branch | ✅ | |
| `workflow.go`: join handling — wait for all branches, collect results | ✅ | |
| `workflow.go`: pass merged results as context to next node after join | ✅ | |
| Tests: compiler fork/join topology | ✅ | AF-11..14 |
| Tests: workflow parallel branch execution | ✅ | AF-WF-07..09 |
| `go test ./internal/appflow/...` passes | ✅ | 29/29 |
| `go test ./...` full suite passes | ✅ | zero failures |
| TEST_INDEX.md updated | ✅ | S1 total: 1193 |

### Frontend

| Task | Status | Notes |
|---|---|---|
| `apiTypes.ts`: add `'fork'` and `'join'` to flow_control connection type union | ✅ | handled via `types.ts` union (no separate apiTypes.ts change needed) |
| `types.ts`: extend `FlowControlNodeData` with fork/join variants | ✅ | |
| `CanvasNodes.tsx`: `FC_META` entries for fork (⑂) and join (⊕) | ✅ | |
| `CanvasNodes.tsx`: `ForkNode` / `JoinNode` visual components | ✅ | reuse `FlowControlNode` via FC_META |
| `CanvasBuilderView.tsx`: Fork + Join in Flow Control palette | ✅ | |
| `CanvasHelpers.ts`: `canvasToDoc` handles fork/join serialization | ✅ | existing flowControl branch handles all node_type values |
| `CanvasHelpers.ts`: `docToCanvas` restores fork/join nodes | ✅ | display name lookup table added |
| `CanvasHelpers.ts`: `genInstanceId` handles `'fork'` and `'join'` | ✅ | `fc_` prefix used for all flow_control kinds |
| TypeScript compiles clean (`npx tsc --noEmit`) | ✅ | |
| Frontend rebuilt and running | ✅ | container restarted |

### E2E Test

| Task | Status | Notes |
|---|---|---|
| `scripts/tests/test_41_appflow_fork_join.py` written | ✅ | |
| Test: fork → 2 agents in parallel → join → run completes | ✅ | run.status==completed check |
| Test: verify both agents were called (check run steps or artifacts) | ✅ | run_steps COUNT ≥ 3 check |
| All checks pass | ✅ | 13/13 pass |

### Docs & Cleanup

| Task | Status | Notes |
|---|---|---|
| `docs/CURRENT.md` updated with Phase A complete | ✅ | |
| `docs/HANDOVER_APP_CANVAS.md` updated | ✅ | covered in CURRENT.md |
| Committed with clear message | ✅ | HEAD 67c2cd6b |

---

## Key Files

| File | What changes |
|---|---|
| `go/internal/appflow/compiler.go` | Add fork/join node kinds + validation |
| `go/internal/appflow/workflow.go` | Parallel branch execution logic |
| `go/internal/appflow/compiler_test.go` | Fork/join compiler tests |
| `go/internal/appflow/workflow_test.go` | Parallel execution tests |
| `go/TEST_INDEX.md` | Update test count + entries |
| `frontend/src/lib/apiTypes.ts` | flow_control type union |
| `frontend/src/app/admin/applications/types.ts` | FlowControlNodeData |
| `frontend/src/app/admin/applications/components/CanvasNodes.tsx` | FC_META + node components |
| `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx` | Palette |
| `frontend/src/app/admin/applications/components/CanvasHelpers.ts` | Serialize/restore |
| `scripts/tests/test_41_appflow_fork_join.py` | E2E test |

---

## Implementation Notes

### Workflow fork/join pattern (Go pseudocode)

```go
case "fork":
    branches := outgoingEdges(node.ID)
    results := make([]string, len(branches))
    var wg workflow.WaitGroup
    for i, branch := range branches {
        i, branch := i, branch
        wg.Add(1)
        workflow.Go(ctx, func(ctx workflow.Context) {
            results[i] = walkBranch(ctx, branch.Target, until="join")
            wg.Done()
        })
    }
    wg.Wait(ctx)
    // continue from join's outgoing edge with merged results

case "join":
    // reached by each branch goroutine — signal completion
    // main goroutine waits for all branches via WaitGroup
```

### Canvas wire format

Fork node in definition JSON:
```json
{
  "instance_id": "fork_1",
  "definition_ref": {"kind": "flow_control", "name": "fork"},
  "config": {}
}
```

Connection from fork to each branch (type = "flow_control"):
```json
{"source": "fork_1", "target": "agent_a", "type": "flow_control"},
{"source": "fork_1", "target": "agent_b", "type": "flow_control"}
```

Join node collects all branches:
```json
{"source": "agent_a", "target": "join_1", "type": "flow_control"},
{"source": "agent_b", "target": "join_1", "type": "flow_control"}
```

---

## What comes after Phase A

- **Phase B**: Temporal execution controls (max concurrent workflows, timeouts, retries)
  - Two-level: platform defaults + per-app override
  - UI: platform admin settings + app Runtime "Temporal" tab
- **Phase C**: OrchestratorNode (multi-turn LLM loop inside Temporal) — on hold
