# Phase R-2C Implementation Report — Go Worker Separation

**Date:** 2026-07-28
**Phase:** R-2C
**Commit:** b98fcea (Phases 1–3) + Phase 4 fix commit
**Status:** COMPLETE

---

## Summary

Phase R-2C separated the Go Temporal Worker from the Go Bridge into a distinct binary and container pair. Previously the worker goroutine was co-located in `cmd/them/main.go`. It now lives in `cmd/worker/main.go` and runs as `them-go-worker` / `them-go-worker-2`, independently scalable and independently deployable.

---

## Phase 1 — Dedicated Worker Binary

**New file:** `go/cmd/worker/main.go`

- No HTTP, WS, or SSE listeners — polls Temporal only.
- Registers all activities and workflows against `them-orchestration-go`.
- `cmd/them/main.go` (Bridge) had all worker registration code removed; Bridge is now a client-only Temporal caller.

**Supporting changes:**

| File | Change |
|---|---|
| `go/internal/temporal/workflow.go` | Added `GoTaskQueue = "them-orchestration-go"` constant |
| `go/internal/config/config.go` | Added `WorkerTaskQueue` field (env `WORKER_TASK_QUEUE`, default `them-orchestration-go`) |
| `go/internal/config/config_test.go` | Tests for new field |
| `go/internal/temporal/worker_test.go` | Queue distinctness tests |

---

## Phase 2 — Task Queue Separation

Two workers, two queues, zero cross-talk:

| Worker | Queue | Changed? |
|---|---|---|
| `them-worker` (Python) | `them-orchestration` | No |
| `them-go-worker` (Go) | `them-orchestration-go` | New |

The Bridge WS and SSE handlers previously sent `PythonOrchestrationInput` structs to the Go queue — wrong type for the Go workflow. Both were changed to send `temporal.WorkflowInput` to `GoTaskQueue`.

**Files changed:**
- `go/internal/ws/handler.go` — `WorkflowInput` instead of `PythonOrchestrationInput`
- `go/internal/sse/handler.go` — same fix

---

## Phase 3 — Cross-process Redis Streams

With the worker running in a separate process, the in-process event bus no longer reaches the Bridge. Events must travel over Redis Streams.

**New infrastructure:**

| File | Purpose |
|---|---|
| `go/internal/runstream/publisher.go` | `StreamPublisher` interface + `PublishEvent()` function |
| `go/internal/runstream/publisher_test.go` | 6 publisher unit tests |
| `go/internal/cache/runstreamer_writer_adapter.go` | rueidis `XAdd` adapter implementing `StreamPublisher` |

**Stream format:**

- Key: `them:dash:run:{runID}:stream`
- Command: `XADD`
- Fields: `data` = JSON object with `type`, `run_id`, `context_id`, and event-specific payload fields

**Event routing:**

- Worker subscribes to the internal bus wildcard `"*"` and forwards every event to Redis Streams in a goroutine.
- Bridge reads from Redis Streams via the existing `StreamFromRedis` / `decodeEntry` path — no Bridge changes required.
- `RUN_EVENTS_MODE=streams` on the Worker; `RUN_EVENTS_MODE=dual` on the Bridge.

---

## Phase 4 — Deployment

**New file:** `Dockerfile.go-worker` — builds only the `cmd/worker` binary; does not include any HTTP listener.

**docker-compose changes:**

- `theM_gateway/docker-compose.integration.yml` — added `them-go-worker` and `them-go-worker-2` services.
- `theM_gateway/docker-compose.yml` and `docker-compose.yml` — fixed `temporal-frontend` healthcheck that was bound to `127.0.0.1:7233` (unreachable inside the container). Changed to:

  ```sh
  nc -z $(hostname -i | awk '{print $1}') 7233
  ```

---

## E2E Validation Results

| Check | Result |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-go-worker (×2) | Healthy, polling `them-orchestration-go` |
| them-worker (Python) | Healthy, polling `them-orchestration` only |
| 5 E2E runs end-to-end | All succeeded (tokens streamed, done event received) |
| Load distribution | Worker-1: 2 runs, Worker-2: 3 runs — Temporal distributed correctly |
| Redis Streams | `them:dash:run:{runID}:stream` contained `token` + `done` events from Worker |
| Python worker new runs | 0 — queue separation confirmed |

---

## Test Results

```
go test ./...   →   413 passed, 0 failed
```

New tests added in commit b98fcea:

| Suite | Tests added | Coverage |
|---|---|---|
| S1-01 (config) | +2 | `WorkerTaskQueue` field and default |
| S1-23 (runstream) | +6 | `StreamPublisher` interface, `PublishEvent`, error paths |
| S1-29 (temporal queue) | +2 | Go vs Python queue distinctness |

`go/TEST_INDEX.md` updated.

---

## Files Changed

```
go/cmd/worker/main.go                             (new — dedicated worker binary)
go/cmd/them/main.go                               (removed worker registration)
go/internal/temporal/workflow.go                  (GoTaskQueue constant)
go/internal/temporal/worker_test.go               (queue distinctness tests)
go/internal/config/config.go                      (WorkerTaskQueue field)
go/internal/config/config_test.go                 (tests for new field)
go/internal/runstream/publisher.go                (new — StreamPublisher + PublishEvent)
go/internal/runstream/publisher_test.go           (new — 6 publisher tests)
go/internal/cache/runstreamer_writer_adapter.go   (new — rueidis XAdd adapter)
go/internal/ws/handler.go                         (WorkflowInput fix)
go/internal/sse/handler.go                        (WorkflowInput fix)
Dockerfile.go-worker                              (new — worker Docker image)
theM_gateway/docker-compose.integration.yml       (Go worker services)
theM_gateway/docker-compose.yml                   (temporal-frontend healthcheck fix)
docker-compose.yml                                (temporal-frontend healthcheck fix)
go/TEST_INDEX.md                                  (updated)
```
