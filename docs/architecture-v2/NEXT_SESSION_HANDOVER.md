# Phase R-2C Complete — Handover to R-3

**Date:** 2026-07-28
**Branch:** main
**HEAD:** (Phase 4 fix commit — run `git log --oneline -1` to confirm)
**Prepared by:** Phase R-2C session

---

## Current Objective

Phase R-2C (Go Worker Separation) is **complete**. The next task is **Phase R-3: File Artifact Delivery** per gate doc §1.11.

---

## What Was Completed This Session (R-2C)

1. Dedicated Go worker binary at `go/cmd/worker/main.go` — no HTTP, polls `them-orchestration-go` only.
2. Bridge (`cmd/them/main.go`) stripped of worker registration — client-only Temporal caller.
3. `GoTaskQueue = "them-orchestration-go"` constant in `go/internal/temporal/workflow.go`.
4. `WorkerTaskQueue` config field added (`go/internal/config/config.go`).
5. WS and SSE handlers fixed: send `WorkflowInput` (not `PythonOrchestrationInput`) to Go queue.
6. Cross-process Redis Streams pipeline: `runstream.StreamPublisher`, `PublishEvent()`, rueidis XAdd adapter.
7. `Dockerfile.go-worker` — separate Docker image.
8. `theM_gateway/docker-compose.integration.yml` — `them-go-worker` + `them-go-worker-2` services added.
9. `temporal-frontend` healthcheck fixed in both `docker-compose.yml` and `theM_gateway/docker-compose.yml`.
10. E2E validated: 5 runs, 2-worker load distribution, Redis Streams confirmed, Python worker queued 0 new runs.

Full details: `docs/architecture-v2/R2C_IMPLEMENTATION_REPORT.md`

---

## Stack State

| Container | Status |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-go-worker (×2) | Healthy, polling `them-orchestration-go` |
| them-worker (Python) | Healthy, polling `them-orchestration` |
| them-temporal-* | Healthy |
| them-postgres | Healthy |
| them-redis | Healthy |

---

## Test State

```
go test ./...   →   413 passed, 0 failed
```

Python suite last clean run: 390 passed (Phase R-2 baseline).

---

## Hard Constraints — Carry Forward

- **Temporal is the single durable owner of every run.** No inline execution path exists in Go or Python.
- **Never log token values, API keys, or secrets** at any log level.
- **All Go changes require `go test ./...` before commit.** Zero regressions allowed.
- **Workflow ID scheme `ctx-{contextID}`** must be preserved — changing it orphans in-flight runs.
- **`PythonOrchestrationInput` in `go/internal/temporal/python_input.go`** is dead code — retained until the Python worker is decommissioned. Do not delete it yet.
- **Python worker** remains running on `them-orchestration`. It is not decommissioned in R-2C. The two workers run in parallel on separate queues.
- **`RUN_EVENTS_MODE`**: Worker must be `streams`; Bridge must be `dual`. Do not change without understanding the Bridge read path.
- **Tenant-aware design**: all new DB queries and Redis keys must include `application_id` or entry point slug.
- DB name and schema: `them` only. Never `odin`.

---

## Known Issues and Blockers

| Issue | Severity | Notes |
|---|---|---|
| `context_id` column missing from `them.runs` | Non-fatal | Run is created but the context_id insert fails silently. Will be fixed as part of R-3 run-recording work. |
| `go/them` and `go/worker` binaries in repo root | Cosmetic | Local build artifacts, covered by `.gitignore`. Not committed. |

---

## Next Task: Phase R-3 — File Artifact Delivery

**Scope:** Implement file artifact delivery per gate doc §1.11. The Go orchestrator must be able to record artifacts (file blobs or references) produced during a run, persist them to `them.artifacts`, and make them retrievable via the runs API.

**Gate doc reference:** `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §1.11

**Files most relevant to R-3:**

| File | Why |
|---|---|
| `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` | Gate requirements §1.11 — read first |
| `go/internal/orchestrator/orchestrator.go` | Agentic loop — where artifact events originate |
| `go/internal/runrecorder/recorder.go` | Run recording layer — where artifact rows are written |
| `db/001_schema.sql` | `them.artifacts` table definition |
| `go/internal/runstream/publisher.go` | Redis Streams publisher (artifact events may flow here) |

**Do not touch** the Python worker, Python adapters, or auth service in R-3.

---

## Starting the Next Session

```bash
# Confirm HEAD and stack health first
git log --oneline -5
docker compose -f docker-compose.yml -f docker-compose.local.yml --profile temporal ps

# Run Go tests to confirm clean baseline
cd /opt/docker/them && go test ./go/...

# Open next session
tmux new-session -s r3 -d
tmux send-keys -t r3 'cd /opt/docker/them' Enter
```

**First prompt for the next session:**

> Phase R-2C is complete (HEAD confirmed, 413 Go tests passing, 2 Go workers healthy). Start Phase R-3: File Artifact Delivery per gate doc §1.11. Read docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md §1.11, go/internal/runrecorder/recorder.go, go/internal/orchestrator/orchestrator.go, and db/001_schema.sql (artifacts table) before writing any code. Plan the implementation, confirm scope, then implement.

---

## Commits This Session

- b98fcea feat(r2c): phases 1–3 — dedicated worker binary, queue separation, Redis Streams pipeline
- (Phase 4 fix commit — confirm hash with `git log --oneline -1`)

Push status: confirm with `git status` and push if credentials available.
