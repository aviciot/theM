# Phase R-2 Planning Complete — Handover

**Date:** 2026-07-28
**Branch:** main
**HEAD:** f5ef287 docs(planning): Phase R-2 Temporal Go Worker plan
**Session model:** claude-opus-4 (planning session)

---

## Current objective

Phase R-2 planning is complete.
Next task: Phase R-2A — Orchestrator Feature Parity (start in a fresh Sonnet session).

---

## Work completed this session

### R-2 Architecture Plan

Full plan written to `docs/architecture-v2/R2_TEMPORAL_GO_WORKER_PLAN.md`.

Key decisions recorded in the plan:
1. **Inline path identified and condemned** — `ws/handler.go` and `sse/handler.go` contain
   an inline Go orchestrator path (`temporalEnabled=false` branch) that runs the entire
   agentic loop outside Temporal. This violates the mandatory constraint. R-2B removes it.
2. **R-2 split into R-2A and R-2B** — R-2A is orchestrator feature parity (7 gaps);
   R-2B is Go worker wiring + inline path removal. R-2B is blocked on R-2A.
3. **Atomic cutover plan** — switching from `PythonOrchestrationInput` to `WorkflowInput`
   breaks Python-worker compatibility; must be a coordinated deploy (Go worker up →
   bridge updated → Python worker stopped).
4. **Documents to correct** — gate doc §1.4 and §1.7 describe the inline path as valid;
   both must be corrected in the R-2B commit.

---

## Deployed / live state

- Go bridge: NOT rebuilt since R-1 — still running pre-R-1 binary
- Temporal path: Python worker still active (used when TEMPORAL_ENABLED=true)
- Stack: them-postgres, them-redis, them-auth-service, them-bridge, them-traefik (healthy)
- Go bridge container: stale (does not have R-0 or R-1 code live)

---

## Working tree state

Clean after commit. Untracked `go/them` binary (correct — not committed).

---

## Architecture decisions made this session

1. **Inline path is eliminated in R-2B.** No LLM call, agent call, or orchestration
   iteration may execute outside Temporal after R-2B is complete.
2. **Phase naming clarification:**
   - R-1 (delivered) = Prometheus metrics + structured logging (HEAD 39d505c)
   - R-2A = Orchestrator feature parity (7 gaps from gate doc §1.5)
   - R-2B = Go Temporal Worker wiring + inline path removal
3. **In-process event bus remains correct after R-2.** When the Go worker runs in the
   same binary as the bridge, the bus transports events produced inside a Temporal activity
   to the bridge handler. This is event delivery, not an execution bypass.
4. **Memory injection deferred.** Of the 8 orchestrator gaps, memory injection is the
   only one deferred to R-3. All other 7 gaps are required before R-2B.

---

## Hard constraints remaining in force

- Temporal is the single durable owner of every run — no exceptions after R-2B
- Never log token values, API keys, request bodies, or any secret
- Never use `session_id`, `run_id`, `request_id`, `user_id`, `tenant_id` as Prometheus label names
- All Go changes require `go test ./...` to pass before commit
- Workflow ID scheme `ctx-{contextID}` must be preserved — HITL signal routing depends on it
- Task queue name `them-orchestration` is shared with Python during transition — do not change it

---

## Known bugs / risks

- Go bridge Docker image is stale — rebuild needed before any live testing
- `PythonOrchestrationInput` → `WorkflowInput` cutover is a breaking change for Python worker
  compatibility — must be an atomic deploy, not a rolling one
- `tenant_id` field in session info is still empty string (pre-existing, tracked in gate doc)

---

## Files most relevant to next task

| File | Relevance |
|---|---|
| `go/internal/orchestrator/orchestrator.go` | Starting point for R-2A — add 7 missing features |
| `docs/architecture-v2/R2_TEMPORAL_GO_WORKER_PLAN.md` | Full R-2 plan — read before starting |
| `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.5 | Gap table for orchestrator features |
| `docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md` OD-3, OD-4, OD-5 | Resolved decisions for R-2A features |
| `go/internal/temporal/activities.go` | Activity skeleton — will be fully implemented in R-2B |
| `go/internal/temporal/workflow.go` | Correct as-is — do not change |
| `go/internal/ws/handler.go` lines 481–561 | Inline path to remove in R-2B |

---

## Exact next single focused task

**Phase R-2A, Task 1: Implement in-memory message accumulation + durable checkpoints (OD-3)**

In `go/internal/orchestrator/orchestrator.go`:
1. Add `HistoryLoader` and `CheckpointWriter` injection to `Orchestrator.New()`
2. Call `HistoryLoader.LoadHistory(ctx, contextID, historyWindow)` at start of `Run()`
   if `history` param is nil/empty
3. After each LLM iteration: write assistant turn + tool results to `task_messages`
   via a `CheckpointWriter` interface (new interface in `internal/orchestrator` or
   a new package — your call, keep it simple)
4. Write `TestOrchestrator_CheckpointRecovery` (required by OD-3)
5. `go test ./...` — zero failures
6. Commit `internal/orchestrator/`, `go/TEST_INDEX.md`

---

## Exact first prompt for next session

```
Read first:
1. docs/architecture-v2/R2_TEMPORAL_GO_WORKER_PLAN.md   (the full R-2 plan)
2. go/CLAUDE.md
3. go/TEST_INDEX.md
4. go/internal/orchestrator/orchestrator.go              (current state ~303 lines)
5. docs/architecture-v2/CRITICAL_RUNTIME_BLOCKING_DECISIONS.md (OD-3, OD-4, OD-5)

We are on branch main. Phase R-1 (Prometheus metrics) is complete (HEAD 39d505c or newer).
Phase R-2 planning is complete — plan is in R2_TEMPORAL_GO_WORKER_PLAN.md.

Start R-2A, Task 1: Implement in-memory message accumulation + durable checkpoints
in go/internal/orchestrator/orchestrator.go.

See R2_TEMPORAL_GO_WORKER_PLAN.md §9.5 for the exact task description.

Do not implement R-2B (worker wiring / inline path removal) yet.
Do not change Temporal, Python worker, DB schema, Redis, Docker, or Traefik.
```
