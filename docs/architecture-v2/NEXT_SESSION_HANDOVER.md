# Phase R-2 Complete — Handover

**Date:** 2026-07-28
**Branch:** main
**HEAD:** 029bf8c feat(r2b): remove inline execution path — Temporal is unconditional
**Session model:** claude-sonnet-4-6 (implementation session)

---

## Current objective

Phase R-2 is complete and pushed.
Next task: Phase R-3 — File Artifact Delivery.

---

## Work completed this session

### R-2A — Orchestrator Feature Parity (`cf51b44`)

Added 7 missing features to `go/internal/orchestrator/orchestrator.go`:
- History loading from DB at run start (HistoryLoader already existed, now called)
- Budget token enforcement (`BudgetTokens`, `ErrBudgetExceeded`)
- Parallel agent fan-out with `semaphore.Weighted` (`MaxParallelTools`)
- A2A agent card auto-discovery (`CardDiscoverer` interface, graceful fallback)
- `run_usage` persistence (`UsageRecorder` interface — non-fatal)
- Child task lifecycle (`TaskRecorder.CreateTask/CompleteTask` — non-fatal)
- Per-iteration budget checkpoint (`BudgetStore.UpdateTokensUsed` — non-fatal)

Memory injection is deferred to R-3 per plan decision.

### R-2B.1 — Go Temporal Worker (`a245f11`)

Registered Go Temporal worker in `go/cmd/them/main.go`:
- Same binary as bridge (same process, separate goroutine)
- Task queue: `them-orchestration` (shared with Python worker during transition)
- Registers `OrchestrationWorkflow` + `RunOrchestratorActivity`
- Gated on `TEMPORAL_ENABLED=true`

### R-2B.2 — Inline Path Removed (`029bf8c`)

- Deleted `// 11b. Go-inline path` branches from `ws/handler.go` and `sse/handler.go`
- When `temporalClient == nil`: returns 503 error, marks run failed — NO fallback
- `PythonOrchestrationInput` → `WorkflowInput` in WS/SSE `ExecuteWorkflow` calls
- `temporalEnabled bool` field gone from both Handler structs
- Architecture docs corrected per R2_TEMPORAL_GO_WORKER_PLAN.md §7

---

## Deployed / live state

- Go bridge Docker image: **STALE** — was not rebuilt; running pre-R-1 binary
- Temporal path: Python worker still active (normal during R-2 transition period)
- Stack: them-postgres, them-redis, them-auth-service, them-bridge, them-traefik (healthy)
- Go bridge container: stale binary (does not have R-0/R-1/R-2 code live)

**To rebuild Go bridge:**
```bash
docker compose --profile go build them-go-bridge
docker compose --profile go up -d them-go-bridge
docker logs them-go-bridge | grep "temporal worker"  # verify Go worker polling
```

---

## Working tree state

Clean after all commits. Untracked `go/them` binary (correct — not committed).
Branch `main` is synchronized with `origin/main`.

---

## Test results

| Run | Passed | Failed | Races |
|---|---|---|---|
| `go test ./...` after R-2A | 353 | 0 | — |
| `go test ./...` final | 390 | 0 | — |
| `go test -race ./...` | 390 | 0 | 0 |

New tests added: 11 (S1-28: orchestrator ×7, S1-12/S1-13 updated ×2, S1-29: temporal worker ×2)

---

## Architecture decisions made this session

1. **Inline path fully removed.** No execution may happen outside Temporal after R-2B.2.
   When Temporal is unavailable, WS/SSE return 503 — no degraded fallback.
2. **Optional orchestrator features via `With*` methods.** All new orchestrator
   capabilities (checkpointer, card discoverer, usage recorder, etc.) are injected
   via nil-safe option methods so existing tests and callers are unaffected.
3. **Go worker in same binary for R-2.** The worker runs alongside the bridge. A
   separate worker binary is R-3+ scope.
4. **Python worker runs in parallel** during the transition. Cutover (stop Python
   worker) is a manual coordinated deploy after the Go worker is validated under load.

---

## Hard constraints remaining in force

- Temporal is the single durable owner of every run — no exceptions
- Never log token values, API keys, request bodies, or any secret
- Never use `session_id`, `run_id`, `request_id`, `user_id`, `tenant_id` as Prometheus labels
- All Go changes require `go test ./...` to pass before commit
- Workflow ID scheme `ctx-{contextID}` must be preserved
- Task queue name `them-orchestration` shared with Python during transition
- `PythonOrchestrationInput` Go type: deprecated but retained until Python worker off

---

## Known bugs / risks

- Go bridge Docker image stale — rebuild needed before any live testing
- Python worker cutover is manual — must be coordinated deploy, not rolling
- `tenant_id` field in session info is still empty string (pre-existing, tracked in gate doc)
- `PythonOrchestrationInput` dead code still in `go/internal/temporal/python_input.go`
- Memory injection (`MemoryStore.Inject`) deferred to R-3 — orchestrator config `memory_enabled` field not yet wired

---

## Files most relevant to next task (R-3: File Artifact Delivery)

| File | Relevance |
|---|---|
| `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.11 | Artifact delivery spec |
| `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §9 Phase R-2 | R-3 scope description |
| `go/internal/orchestrator/orchestrator.go` | Add artifact recording in executeTools |
| `go/internal/runrecorder/recorder.go` | Add `RecordArtifact` method |
| `db/001_schema.sql` | `them.artifacts` table definition |

---

## Python Worker Cutover Procedure (execute manually after Go worker validated)

```bash
# 1. Build and start Go bridge with R-2 code
docker compose --profile go build them-go-bridge
docker compose --profile go up -d them-go-bridge

# 2. Verify Go worker is polling
docker compose --profile go logs -f them-go-bridge | grep "temporal worker"

# 3. Verify Go worker picks up a test run
# (create a test run via admin API, verify it completes via Go worker logs)

# 4. Stop Python worker
docker compose --profile temporal stop them-worker

# 5. Monitor Temporal UI (localhost:3111) for any workflow failures
# 6. If failures: restart Python worker immediately
docker compose --profile temporal start them-worker
```

---

## Exact next single focused task

**Phase R-3: File Artifact Delivery**

Per gate document §1.11 and OD-6 decision:
1. Artifact DB row creation in `them.artifacts` (id, run_id, filename, media_type, data BYTEA)
2. `RecordArtifact(ctx, runID, filename, mediaType string, data []byte) (string, error)` in `internal/runrecorder`
3. `{"type":"file","artifact_id":"...","filename":"...","media_type":"..."}` event emission in orchestrator
4. Size gate: inline if `len(data) < 4096`, by-reference otherwise
5. `GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` endpoint in admin router
6. `ErrArtifactTooLarge` sentinel for >1MB artifacts

---

## Exact commands for next session startup

```bash
cd /opt/docker/them
git log --oneline -3   # verify HEAD is 029bf8c
git status             # verify clean tree
# Read in order:
# docs/architecture-v2/NEXT_SESSION_HANDOVER.md
# docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md §1.11 and §4.5
# go/internal/orchestrator/orchestrator.go
# go/internal/runrecorder/recorder.go
# db/001_schema.sql (them.artifacts table)
```

First prompt for next session:
```
Read docs/architecture-v2/NEXT_SESSION_HANDOVER.md and the artifact-delivery sections of
CRITICAL_RUNTIME_ARCHITECTURE_GATE.md (§1.11, §4.5, OD-6).

Phase R-2 is complete (HEAD 029bf8c). The inline orchestration path is removed.
Temporal is the unconditional execution owner.

Implement Phase R-3: File Artifact Delivery.
- RecordArtifact in internal/runrecorder/recorder.go
- artifact event emission in internal/orchestrator/orchestrator.go
- GET /api/v1/runs/{run_id}/artifacts/{artifact_id} admin endpoint
- 1MB size limit, ErrArtifactTooLarge sentinel
- go test ./... must pass before commit
```

---

## Push and repository status

All R-2 commits are pushed to `origin/main`.
Working tree: clean (go/them binary untracked — correct).

Close this Claude session. Open a fresh Sonnet session for R-3.
