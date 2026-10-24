# Handover — AppFlow Phase A: Fork/Join Parallel Execution
# Created: 2026-09-17
# Last updated: 2026-09-17 (not started — plan complete, implementation pending)

---

## First prompt for new session

```
Read docs/HANDOVER_APPFLOW_FORK_JOIN.md and docs/APPFLOW_FORK_JOIN_PLAN.md, then implement Phase A — Fork/Join parallel execution for the AppFlow canvas. Check the progress tracker in APPFLOW_FORK_JOIN_PLAN.md for what's done and what's next.
```

---

## Context

The-M is a multi-agent orchestration platform. The application canvas lets users build flows
that execute via Temporal (AppFlow DAG). Phase 3 of the canvas upgrade is complete — it
supports linear execution, LLM routing (Router node), and HIL pause/resume.

Phase A adds Fork/Join: parallel branch execution inside the Temporal AppFlow workflow.

---

## Current HEAD

Branch: `main`
HEAD: `4b3c55c7` — fix(appflow): A2A v1.0 wire format in dag-worker; add full canvas E2E test (17/17)

---

## What's already built (do not rebuild)

- `go/internal/appflow/compiler.go` — compiles canvas JSON → AppFlowSpec (agent/router/hil nodes)
- `go/internal/appflow/workflow.go` — Temporal workflow: linear walk + Router (LLM routing) + HIL (pause/approve)
- `go/cmd/dag-worker/main.go` — registers AppFlowWorkflow on `appflow-dag` task queue; `pgxAgentA2ACaller` calls agents via A2A `SendMessage`
- `go/internal/admin/hil_approvals.go` — HIL approval API
- Frontend: Router + HIL nodes in canvas, execution backend toggle, edge labels
- E2E: `scripts/tests/test_40_appflow_canvas_e2e.py` (17/17 pass), `test_39_appflow_hil.py` (4/4 pass)

---

## What Phase A adds

Fork node: splits flow into N parallel branches.
Join node: waits for all branches, merges results, continues.

Temporal executes branches as parallel goroutines (`workflow.Go`) — no new activities needed.

Full plan and progress tracker: `docs/APPFLOW_FORK_JOIN_PLAN.md`

---

## Implementation order

1. **Backend first** — compiler + workflow (Go)
   - Add `fork`/`join` to `compileNode` in `compiler.go`
   - Add validation (fork needs ≥2 outgoing, join needs ≥2 incoming)
   - Add parallel branch execution to `workflow.go` using `workflow.Go` + `workflow.WaitGroup`
   - Run `cd go && go test ./internal/appflow/...` — must pass before frontend work

2. **Frontend second**
   - Add Fork + Join to `FC_META`, `FlowControlNodeData`, `CanvasNodes.tsx`
   - Add to palette in `CanvasBuilderView.tsx`
   - Update `CanvasHelpers.ts` serialize/restore

3. **E2E test last**
   - `scripts/tests/test_41_appflow_fork_join.py`
   - Fork → 2 agents in parallel → Join → run completes
   - Verify both agents were called

4. **Rebuild containers**
   ```bash
   docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge them-dag-worker them-frontend
   docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-dag-worker them-go-bridge them-frontend
   ```

---

## Key design decisions

**Fork/Join is workflow logic, not activities.**
The parallel branches run as `workflow.Go` goroutines inside the workflow function.
Each branch walks the DAG until it reaches the Join node. The Join node waits for
all branches via `workflow.WaitGroup`, collects results, then continues from Join's
outgoing edge.

**Merged results.**
After Join, the user message passed to the next node is the concatenation of all
branch results (or the first non-empty result — decide at implementation time based
on what makes sense for the use case).

**No new Temporal activities.**
Agent invocation reuses `AppFlowInvokeAgentActivity`. Router and HIL remain unchanged.
Fork and Join are handled entirely inside `AppFlowWorkflow`.

---

## Wire format (canvas definition JSON)

Fork node:
```json
{
  "instance_id": "fork_1",
  "definition_ref": {"kind": "flow_control", "name": "fork"},
  "config": {}
}
```

Connections:
```json
{"source": "fork_1", "target": "agent_a", "type": "flow_control"},
{"source": "fork_1", "target": "agent_b", "type": "flow_control"},
{"source": "agent_a", "target": "join_1", "type": "flow_control"},
{"source": "agent_b", "target": "join_1", "type": "flow_control"},
{"source": "join_1", "target": "agent_c", "type": "flow_control"}
```

---

## Stack commands

```bash
# Run Go tests (appflow package)
cd go && go test ./internal/appflow/...

# Run full Go suite
cd go && go test ./...

# Restart dag-worker after Go changes
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-dag-worker

# Run E2E test
python3.12 scripts/tests/test_41_appflow_fork_join.py

# Stack status
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal ps
```

---

## What comes after Phase A

- **Phase B**: Temporal execution controls (max concurrent workflows, timeouts, retries)
  - Platform-level defaults in `config` table
  - Per-app overrides in new `app_temporal_config` table
  - UI: platform admin settings page + app Runtime "Temporal" tab
- **Phase C**: OrchestratorNode (multi-turn LLM loop inside Temporal) — on hold
