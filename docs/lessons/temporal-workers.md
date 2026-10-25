---
title: Lessons — Temporal & Workers
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-09-27 — Rebuilding "them-dag-worker" silently left debug runs on stale code

**Symptom:** a trace-detail fix (adding a "File Guard: pending/clean/blocked" suffix to the agent
node's debug-log line) compiled clean, passed `go test ./...`, and was confirmed live via a direct
API debug run — except the debug log kept showing the OLD output with no File Guard mention at
all, run after run, even after rebuilding and restarting `them-dag-worker`.
**Root cause:** `them-dag-worker`, `them-dag-worker-2`, and `them-dag-worker-debug` all build from
the same `Dockerfile.dag-worker`, but each is a **separate compose service** and therefore gets its
own separately-tagged image (`them_gateway-them-dag-worker`, `...-2`, `...-debug`). Running
`docker compose build them-dag-worker` only rebuilds that one image tag — `them-dag-worker-debug`'s
image is untouched. Every App Canvas **debug** run (`debug/start`) uses Temporal task queue
`appflow-dag-debug`, which only `them-dag-worker-debug` polls — so all debug-mode testing was
silently running the pre-fix binary while production traffic (`appflow-dag`, served by the other
two, which HAD been rebuilt) would have shown the fix correctly.
**Fix:** always rebuild all 3 images explicitly by naming all 3 services:
`docker compose build them-dag-worker them-dag-worker-2 them-dag-worker-debug`, then restart all 3.
`go/CLAUDE.md`'s trigger map updated to say this explicitly instead of implying one rebuild covers
all replicas.
**Watch for:** "same Dockerfile" does NOT mean "same image" in Docker Compose — each service name is
its own build target/tag by default. When debugging "my fix isn't showing up" after a rebuild,
check `docker inspect <container> --format '{{.State.StartedAt}}'` AND confirm which task
queue/container actually served the specific request (grep logs for the request's own run_id or a
unique marker) before assuming the binary is current — a healthy, recently-restarted container can
still be running stale code if it was rebuilt from the wrong service name.

---

---

## 2026-07-11 — Temporal activity heartbeat timeout fires before first poll event

**Symptom:** `invoke_agent_activity` fails with "activity Heartbeat timeout" after exactly 30 seconds. Agent is slow but functional. Two orphaned `run_steps` rows (one per retry attempt) remain stuck in `running` state.

**Root cause:** Two issues compounded:
1. `heartbeat_timeout=timedelta(seconds=30)` in `workflows.py` — the activity heartbeats only on `status` events from the adapter. But the adapter's `stream_invoke` first does `await self.submit()` (POST SendMessage) which can take 10-40+ seconds depending on agent load, before yielding any event. During that window, **no heartbeat fires**, so Temporal kills the activity.
2. The adapter yields a `task_created` event after submit, but `invoke_agent_activity` didn't handle this event type — so no heartbeat on submit completion.

**Fix:**
1. In `workflows.py`: set `heartbeat_timeout=timedelta(seconds=agent_timeout + 30)` where `agent_timeout = int(agent_dict.get("timeout_seconds", 30))`. This gives the activity enough time for the full submit + poll cycle.
2. In `invoke_agent_activity`: add `activity.heartbeat({"phase": "submitted"})` on the `task_created` event — fires immediately after the POST completes.

**Watch for:**
- Any Temporal activity that calls a slow external HTTP endpoint before its first heartbeat: add an explicit heartbeat immediately after the call returns.
- Always set `heartbeat_timeout >= max possible time between external I/O calls`, not a fixed small value.
- On activity retry (RetryPolicy.maximum_attempts > 1), each attempt creates new DB rows (child_task, run_step) — orphaned "running" rows accumulate on timeout. Consider idempotency keys or cleanup on retry if this becomes a problem.

---

---

## 2026-07-11 — Temporal workflow cancel stores status="failed" not "canceled"

**Symptom:** User clicks Stop in the playground. Run appears canceled in the UI but DB shows `status=failed, error=Cancelled`.

**Root cause:** When Temporal cancels a workflow mid-activity, the Python SDK raises `ActivityError` (not `CancelledError` directly) — the cancellation is wrapped. The `except ActivityError` block set `run_status = "failed"` unconditionally, instead of inspecting whether the cause was a cancellation.

**Fix:** In `except ActivityError`, check `isinstance(exc.cause, CancelledError)` and set `run_status = "canceled"` in that case.

**Watch for:** When adding new except blocks in workflows, always check both `CancelledError` (direct) and `ActivityError` wrapping a `CancelledError` (mid-activity cancellation).

---

---

## 2026-07-14 — Multi-replica deploy: always restart ALL bridge replicas

**Symptom:** One WS session works, the other is silent — but no error is logged on the bridge you're watching.

**Root cause:** Traefik load-balances WS connections across all running bridge replicas. When only one replica was restarted with new code, half of connections (routed by Traefik to the old replica) silently ran the old code — without the pre-subscribe fix or `DeadContextError` — and hung.

**Fix:** Always restart ALL replicas when deploying code changes:
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml --profile replica restart them-bridge them-bridge-2
```

**Watch for:** If parallel sessions behave differently (one works, one silent), check `docker logs them-bridge-2` — the affected session may be on a replica you didn't touch.

---

---

## 2026-08-23 — Temporal load-balances across ALL worker replicas — rebuild all after code changes

**Symptom:** After rebuilding `them-go-worker` and testing a streaming run, the run still failed. Checking Temporal UI showed the task was picked up by `them-go-worker-2` (started Aug 20, old binary).

**Root cause:** Temporal load-balances randomly across all active workers registered on the same task queue. If you rebuild only one replica, the other replica(s) still run the old binary. Approximately 50% of runs will land on the stale worker.

**Fix:** Always rebuild ALL workers in a single operation:
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  build them-go-worker them-go-worker-2
docker rm -f them-go-worker them-go-worker-2
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d them-go-worker them-go-worker-2
```

**Watch for:** Any code change to `go/` that affects worker behavior — always rebuild both worker replicas simultaneously. Verify container start timestamps with `docker ps` to confirm both are fresh.

---

---

## `activity.GetLogger(ctx)` Panics in Unit Tests (2026-09-09)

`go.temporal.io/sdk/activity.GetLogger(ctx)` panics when called with a plain `context.Background()` (used in unit tests). The Temporal SDK expects an activity-specific context with internal state injected.

**Fix:** Use `slog.InfoContext(ctx, ...)` or any stdlib logger inside activities — these work with any context.

**Watch for:** Any log call inside an `activity.*` function that uses `activity.GetLogger(ctx)`. Replace with `slog.InfoContext` for portability across test and production contexts.

---

## `workflow.WaitGroup` Must Be Created With `workflow.NewWaitGroup(ctx)` (2026-09-17)

`var wg workflow.WaitGroup` gives a zero-value struct. Calling `.Add(1)` on it panics with a nil pointer dereference inside the Temporal runtime.

**Fix:** Always use `wg := workflow.NewWaitGroup(ctx)` to obtain a properly initialized WaitGroup.

**Watch for:** Any parallel fork pattern in Temporal workflows. `sync.WaitGroup` (stdlib) is NOT usable in workflows — use `workflow.NewWaitGroup(ctx)` everywhere.
