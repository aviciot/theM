---
title: What data the-M writes while it is used
type: guide
status: current
last_verified: 2026-11-01   # row counts measured live (before/after one run); code-only items marked "from code"
---

# What data the-M writes while it is used

## Purpose
Answers "which tables, Redis keys and ClickHouse tables fill up when someone uses the App Canvas, the orchestrator (chat) or the LLM Gateway, and how do the logging levels change that?". Use it to understand growth, to clean an environment safely, and to know where to look when debugging.
Related: `docs/plans/e2e-clean-env-purge-investigation.md` (sizes, purge commands), `docs/plans/run-lifecycle-hardening-plan.md` (cleanup work), `docs/architecture/events-pipeline.md`, `docs/architecture/redis-usage.md`.

**How the numbers were obtained (2026-11-01):** row counts of every table in `them` and `auth_service` before and after one run, on the dev stack with the mock LLM. **Measured** = seen in that diff. **From code** = written by code that the measured run did not exercise (needs an agent, a real LLM, a file or an approval).

## The big picture
```
 user / client                      writes (Postgres unless noted)
 ─────────────                      ──────────────────────────────────────────────────────────────
 login (the-M or SSO)           ──► auth_service.user_sessions ............ +1 per login/refresh
                                    ClickHouse events_user_activity ....... +1 (login event)

 App Canvas run (AppFlow)       ──► runs ................................. +1
   WS/SSE entry point →             run_steps ............................. +1 per node  (see "canvas trace level")
   Temporal AppFlowWorkflow         hil_approvals ......................... +1 per HIL node reached
                                    run_usage ............................. per LLM call that reports tokens (from code)
                                    run_artifacts/artifacts + MinIO ....... only when a node returns a file (from code)
                                    middleware_jobs/_audit/quarantine ..... only for File Guard scans (from code)
                                    Redis: run stream, rl:* counters, gate keys

 Orchestrator (chat) run        ──► runs ................................. +1
   WS/SSE entry point →             tasks ................................. +1 root task (+1 per delegated agent call)
   OrchestrationWorkflow            task_messages ......................... +2 (user message + answer)
                                    run_usage, run_steps, artifacts ....... when agents/LLM report them (from code)

 LLM Gateway call               ──► gateway_requests ..................... +1 per call (every logging level)
   POST /{tenant}/llm/v1/...        gateway_request_bodies ................ +1 per call ONLY at debug/audit
                                    ClickHouse events_gateway_request ..... +1 per call (events mode dual)

 admin changes (create/delete app…) ► audit_logs ......................... +1 per audited action (from code)
```
Config tables (applications, entry_points, definitions, roles, keys, quotas…) are written only by admin actions and publish, never by running an app.

## 1. App Canvas run
Measured: one completed run of `smart-refund-handler` (10 nodes, mock LLM) = `runs` +1, `run_steps` +10. Nothing else.

**Canvas trace level** (per application, `app_debug_config.log_verbosity`, `GET/PUT /admin/applications/{id}/log-verbosity`, default `status`; Debug-mode runs always use `full`):
| Level | `runs` | `run_steps` | what a step row contains | Measured |
|---|---|---|---|---|
| `off` | yes | **none** | — | runs +1, run_steps +0 |
| `status` (default) | yes | one per node | node, kind, status, latency — **no** output/error text | run_steps +10, `output` empty |
| `full` | yes | one per node | the above **plus** the node's output / error text | run_steps +10, `output` filled |
The live Redis stream (`them:dash:run:{id}:stream`) is published at every level, including `off`.

## 2. Orchestrator (chat) run
Measured: one WS message on `debator` (mock LLM) = `runs` +1, `tasks` +1 (root task), `task_messages` +2. Redis: `rl:them:{tenant}:runs:{minute}` (rate counter, ~90 s TTL) and the entry point's gate shadow key.
`runs.application_id` is never filled for these runs by design; the application is found through `entry_point_slug` → `entry_points`.

## 3. LLM Gateway call
Measured (migration 125 applied): every call writes one `gateway_requests` row; `gateway_request_bodies` rows appear only at `debug` and `audit`.

**Gateway logging level** (governance policy `logging.level`; per gateway client/profile; default `minimal`):
| Level | `gateway_requests` | `gateway_request_bodies` (request + response text) | ClickHouse event | Measured (300 calls) |
|---|---|---|---|---|
| `minimal` | yes | no | yes | requests +300 |
| `metrics` | yes | no | yes | requests +300 (**same as minimal**) |
| `debug` | yes | **yes** | yes | requests +300, bodies +300 |
| `audit` | yes | **yes** | yes | requests +300, bodies +300 (**same as debug**) |
In the current code the level only decides whether bodies are stored (`llmgateway/handler.go`: `debug`/`audit`). `minimal` = `metrics` and `debug` = `audit` in behaviour. The other logging fields a policy can carry (`sample_rate`, `capture_bodies`, `capture_pii_decisions`, `capture_file_guard`, `retention_days`) are resolved by `governance/resolve.go` but nothing applies them yet (backlog F-136).

**Events mode** (`THE_M_EVENTS_GATEWAY`): `postgres` = Postgres only; `dual` (dev default) = Postgres **and** ClickHouse; `clickhouse` is blocked. ClickHouse `events_gateway_request` has no bodies.

## 4. Other writers (from code)
| Source | Tables / stores |
|---|---|
| Login, refresh, logout | `auth_service.user_sessions`, `auth_service.blacklisted_tokens` (logout/revoke), ClickHouse `events_user_activity` |
| HIL node | `hil_approvals` (`pending` → `approved`/`rejected`/`timed_out`); no FK, no RLS |
| Agent returns a file | `run_artifacts` (+ MinIO object when S3 is configured), `artifacts` |
| File Guard scan | `middleware_jobs`, `middleware_audit`, `quarantine_artifacts` (+ MinIO `them-quarantine`) |
| LLM usage reported | `run_usage` (feeds per-user daily cost cap and monthly token quota) |
| Admin actions (create/delete app, config changes) | `audit_logs` (INSERT-only for `them_app`) |
| MCP tool calls | ClickHouse `events_tool_call` |
| Playground / test-runner | `access_tokens` (temporary tokens) |

## 5. What happens when a run is deleted
Deleting a run (admin `DELETE /runs/{id}`, bulk delete) removes the `runs` row; since 2026-11-01 (db/127) this is what follows:
| Child table | When its run is deleted |
|---|---|
| `run_steps`, `run_usage` | deleted too (cascade) |
| `run_artifacts` (+ `middleware_jobs`/`_audit`) | deleted too (cascade). If the artifact points at a stored file, a trigger queues the file in `storage_deletions` and the middleware-worker janitor deletes it within ~5 min |
| `hil_approvals` | deleted too (cascade) |
| `tasks` + `task_messages` (+ `artifacts` under them) | **kept on purpose**: `tasks.run_id` is set to NULL. These rows are the conversation history, so deleting a run record does not erase the user's chat memory |
| `gateway_requests` | not tied to runs; removed with the tenant only |
Deleting a tenant (`?force=true`) removes everything above including the chat history, and queues its stored files.
Runs left in `running` after a crash used to keep counting against `max_concurrent_runs` (counted live from `runs`); since 2026-11-01 the reconciler closes them.

## 6. Retention
Since 2026-11-01 (plan P3, `docs/architecture/data-retention.md`): expired auth sessions and blacklist rows are purged by the auth service, and the platform job removes expired Admin-Playground/test-runner tokens after 7 days. `runs`, chat history (`tasks`/`task_messages`), `gateway_requests` and `audit_logs` are kept until `THE_M_RETENTION_{RUNS,CHAT,GATEWAY,AUDIT}_DAYS` is set; the job is dry-run until `THE_M_RETENTION_DRY_RUN=false`. ClickHouse event tables have TTLs (90 d / 180 d / 7 y / 30 d); ClickHouse system logs do not (backlog F-130). Redis run streams have no TTL and can be evicted.

## Where to change things
Gateway capture: `go/internal/llmgateway/handler.go` (body capture), `dal.go` (`WriteRequest`), `governance/resolve.go`. Canvas trace: `go/internal/appflow/activities.go` (`persistTrace`), `go/internal/admin/dal/log_verbosity.go`. Run rows: `go/internal/runrecorder/`. Delete paths: `go/internal/admin/dal/runs.go`, `tenants.go`.
