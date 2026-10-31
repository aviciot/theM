---
title: Run Lifecycle (WS/SSE → admission → Temporal → agents)
type: architecture
status: current
owns:
  - go/internal/ws/
  - go/internal/sse/
  - go/internal/execution/
  - go/internal/transport/
  - go/internal/gate/
  - go/internal/session/
  - go/internal/epconfig/
  - go/internal/runstream/
  - go/internal/reconciler/
  - go/internal/orchestrator/
  - go/internal/temporal/workflow.go
  - go/internal/temporal/activities.go
  - go/internal/temporal/client.go
  - go/internal/temporal/signaler.go
  - go/cmd/worker/
last_verified: 2026-10-31   # required_role now read from runtime_config instead of entry_points column
prev_verified: 2026-10-25   # F-31 fixed (gate key tenant isolation); tests pending (Go not installed)
---

# Run Lifecycle

## Purpose
How one end-user message becomes a run: the WS/SSE door, the admission pipeline (auth, access,
quotas, gate, session), the Temporal workflow that does the work, and the Redis Stream that
carries events back to the client. Covers the **legacy orchestrator path** end to end. The
**AppFlow path** (`execution_backend="temporal"`) shares the front half (admission, dispatch,
event delivery); its workflow internals are for `app-canvas` (tracker item #4). A2A reuses
`execution.Lifecycle` too but is not described here (item #9).

## How it works

```
client ──WS /{tenant}/apps/{app}/{ep}/ws  or  SSE .../sse   (also legacy /ws|/sse/orchestrate/{app}/{ep})
  │  1 extract token, resolve tenant (URL slug > token claim > bootstrap tenant)
  ▼
execution.Lifecycle.Admit            ← BEFORE the WS upgrade / SSE headers: errors are plain HTTP
  │  auth → epconfig.Load → access-mode JWT checks → CheckAccess/CheckPrincipal → role gate
  │  → tenant quotas → gate.Check → session.Register → gate.Confirm → CreateRun (row inserted as `running`)
  ▼
upgrade (WS) / write headers (SSE)
  │  WS: wait ≤30s for first {"type":"message"}   SSE: message comes from ?message= or POST body
  │  runstream.StreamFromRedis(runID)             ← subscribe BEFORE starting the workflow
  ▼
dispatch
  ├─ EPConfig.ExecutionBackend=="temporal" → compile+validate AppFlow spec → Lifecycle.StartAppFlow
  │      queue `appflow-dag` (`appflow-dag-debug` for debug), workflow ID `appflow:{tenant}:{runID}`
  └─ else → Lifecycle.Start → OrchestrationWorkflow
         queue `them-orchestration-go`, workflow ID `{tenant}:ctx-{contextID}`
  │  Start calls UpdateRunStatus(running) — a no-op in practice, see invariants — (3 tries, then metric `RunStatusUpdateFailed`)
  ▼
send `ready` {run_id, context_id}; WS pings every 15s
  ▼
worker (cmd/worker) RunOrchestratorActivity → orchestrator.Run (LLM loop, tool calls → agents/MCP)
  │  events → worker's in-process bus → wildcard subscriber → runstream.PublishEvent
  │  → XADD `them:dash:run:{runID}:stream` (MAXLEN ~5000)
  ▼
bridge StreamFromRedis (XRANGE replay from last_event_id, then XREAD BLOCK on the same cursor)
  → handler writeEvent/formatSSE → client; stops on done/error (or channel close)
  ▼
deferred Lifecycle.Release: if Start never succeeded mark run `failed`; session.End; gate.Release
```

### Admission order (`execution/lifecycle.go` `Admit`)
1. Validate token if present (failure is not fatal here; step 4 decides).
2. `epLoader.Load(tenantID, appSlug, epSlug)` — cached in-process 30s, evicted on Redis channel `them:ep:config:changed`. Not found → 404, DB error → 503.
3. Access mode: `user_jwt` (HS256 dashboard JWT, tenant must match the EP's tenant; then optional app-level `required_role` from `runtime_config`, per-user rpm and daily-cost limits) · `external_jwt` (RS256 via tenant JWKS; sets `ExternalUserID` from `sub`) · `token` (opaque bearer, required) · `public`.
4. `CheckAccess` (EP/app enabled, blocked tokens/users), `CheckPrincipal` (backend vs end-user tokens), role gate (external_jwt only).
5. `quota.CheckQuota(billingTenantID)` — concurrent runs, runs/min, monthly runs, API rpm, monthly LLM tokens. Fail-open if no enforcer is wired.
6. Generate run/context/session UUIDs. `gate.Check` → `session.Register` → `gate.Confirm` → `CreateRun`. Any failure after `Check` rolls the gate/session back.

Error → HTTP: 401 unauthorized · 402 daily user cost · 403 forbidden · 404 not found · 429 caps/rate/quotas · 501 not implemented · 503 queue full / DB down · 500 otherwise. `AdmitError.Error()` is a static string; causes are only logged.

### Admission gate (`gate/gate.go`)
One Lua script (`luaAdmit`) atomically: prunes ghosts (set members with no shadow key), checks EP cap then app cap, increments the per-token minute counter, then `SADD` + shadow key with a **10s reservation TTL**. `Confirm` extends the shadow to 90s. `Rollback` removes it. When the EP cap is full and `QueueTimeout>0`, the caller `BLPOP`s `them:ep:gate:queue:{appID}:{slug}`; `Release` pushes a token. A wake is a re-compete, never a guarantee, and a loser is not re-queued.
Redis keys (all app-scoped for tenant isolation): `them:ep:{appID}:{slug}:sessions|shadow:{sid}`, `them:app:{appID}:sessions|shadow:{sid}`, `rl:them:{tenantID}:token:{hash}:{minute}`, `them:ep:gate:queue:{appID}:{slug}`. Session Hash: `them:sess:{sid}` (90s).

### Events and terminal states
- Wire types forwarded: `token`, `tool_call`, `tool_result` (WS only), `done`, `error`, `replay_unavailable`, AppFlow `node_start|node_done|node_error|node_paused`; SSE also maps `file` → `artifact-update`.
- `StreamFromRedis` treats `done`, `error`, `canceled`, `terminated`, `timed_out` as terminal and closes the channel. Resume: client sends `last_event_id` (WS first message field, SSE `Last-Event-ID` header). If the cursor was trimmed it emits `replay_unavailable` and replays from the oldest entry.
- WS only: later client messages call `UserInputSignaler.SignalUserInput`, which signals a paused AppFlow `wait_for_input` node (key `them:wait:{runID}`). SSE has no such path.

### Legacy orchestration workflow
`OrchestrationWorkflow` runs `RunOrchestratorActivity` once (`MaximumAttempts: 1`, 10 min start-to-close, 15s heartbeat timeout). If the activity returns an `ApplicationError` of type `TaskInputRequired`, the workflow waits on signal `submit_human_response` and re-runs with the reply appended to history. The activity refuses to run with empty `TenantID`, `ApplicationID` or `RunID` (non-retryable). With `AppOrchestratorID` set, it loads per-run config (`workerconfig`) and builds an orchestrator via the factory; otherwise it uses the static runner.

`orchestrator.Run` loop (max 10 iterations by default): load history (DB `LIMIT`, skipped if `HistoryWindow==0`) → optional summarize → checkpoint user message → stream LLM → publish `token`/`tool_call` → record usage/cost → if tool calls: `executeTools` (agents via registry, MCP via `them-mcp-service`, bounded by `MaxParallelTools`) → repeat. Stops on stop/`end_turn`/`max_tokens`/no tool calls/token budget. Success: status `completed`, root task completed, final output saved, `done` published. Failure: `error` published, status `failed`.

### Signalling a running run
The admin run-signal endpoint calls `RunService.Signal`, which looks up the run's `contextID` and signals `{tenant}:ctx-{contextID}` with `submit_human_response`. AppFlow signals use `Signaler.SignalNamedWorkflow` / `cache.UserInputSignaler` with workflow ID `appflow:{tenant}:{runID}`.

## Key invariants
- **Tenant and app identity come from the server**, never the request: `Start`/`StartAppFlow` overwrite `RunID`, `ContextID`, `TenantID`, `ApplicationID`, `EntryPointSlug` from the handle. `X-External-User` is honoured only for backend tokens (`is_backend=true`).
- **Admit before upgrade; subscribe before start.** Otherwise early events are lost or errors become WS frames.
- **Billing tenant** = the caller's tenant (`req.TenantID`); owning tenant = `EPConfig.TenantID`. Quotas and the run row use the billing tenant.
- `Release` must run exactly once and uses its own 5s context (the request context is already cancelled). `Release(nil)` is safe.
- Run status: `Lifecycle` models `admitted` → `running` → terminal, but `runrecorder.CreateRun` **always inserts `running`** (it ignores the handle's `admitted` status, and the `runs.status` CHECK constraint does not allow `admitted`). So the DB never holds `admitted`; a run whose `Start` never succeeded goes `running` → `failed` via `Release`. (Verified against code and live constraint 2026-10-25; backlog F-96.)
- Orchestrator unbound to the EP (`OrchestratorName==""`) is a config error, not a fallback to the URL slug (SEC-04).
- Cancelling the client context cancels `Start`'s context; the Temporal workflow is **not** cancelled on disconnect (WS watches completion on a background context).

## Failure modes
| What fails | Effect |
|---|---|
| Redis | Admission fails closed (500/503); `StreamFromRedis` emits a synthetic `error` and closes |
| Postgres at admit | EP load → 503; `CreateRun` failure → 500 after rolling back gate+session |
| Temporal start fails | Client gets `failed to start workflow`; run marked `failed` by `Release` |
| Worker down / queue unserved | Run stays `running`; client sees `ready` then nothing (WS pings keep the socket open) |
| Status update to `running` fails 3× | Workflow still runs; metric `RunStatusUpdateFailed` only |
| Client disconnects | Streams stop; `Release` frees gate+session; workflow keeps running to completion |
| Stream trimmed (>5000 entries) | `replay_unavailable`, replay from oldest retained |

## Known gaps (found while writing, 2026-10-24; from reading code, not reproduced live)
1. **Temporal is mandatory for the bridge.** `main.go` passes a nil `temporalCli` to `execution.NewLifecycle`, which panics on nil. The log line "Temporal disabled — using Go-inline orchestration path" and the "optional Temporal client" wording in `platform-overview.md` are stale. Unreachable Temporal also fails startup (`client.Dial`).
2. **Reconciler cannot match runs.** It calls `DescribeWorkflowExecution(runID, "")`, but real workflow IDs are `{tenant}:ctx-{contextID}` and `appflow:{tenant}:{runID}`. Every lookup is `NotFound` → left unchanged. It is also dry-run by default (`RECONCILER_DRY_RUN`). Net effect: stuck `running` rows are never repaired. The package doc still claims "Workflow ID is exactly the run ID".
3. **`session.Store.Touch` has no production caller.** Only the pod heartbeat runs. Session Hash and gate shadow keys expire 90s after `Confirm`, so `luaAdmit` can prune live long-lived sessions and the EP/app caps undercount.
4. **Failures before `orchestrator.Run` are silent to the client.** Config-load/build errors in `RunOrchestratorActivity` return without publishing an `error` event or updating the run row. WS/SSE just end; the run stays `running` (see gap 2).
5. **WS/SSE are not at parity.** WS drops `file` events and `canceled/terminated/timed_out` frames (the stream closes without telling the client); SSE drops the same three terminal types; only WS supports follow-up messages; SSE waits on the workflow with the request context, WS with a background one.
6. **Stale comments:** WS/SSE package docs mention the in-process bus and a "Python platform"; the live path is Redis Streams. `Handler.bus` is still carried but events do not flow through it. `RunService.Signal` doc mentions Python's workflow.
7. (fixed) The old hand-written `REDIS.md` had Python-era session/gate keys; it is archived (`docs/_archive/REDIS.md`) and replaced by the generated `docs/reference/redis-generated.md`.
8. `Lifecycle.Admit` per-user daily cost uses `GetUserDailySpend` (canvas `run_usage`) only; `GetUserDailySpendFromGateway` exists but is not called here.
9. `go/CLAUDE.md` package map has no rows for `execution/`, `epconfig/`, `runstream/`, `reconciler/` (they only appear in its trigger map). Root `CLAUDE.md` still has Python-era rules (item #5).

## Where to change things
| To… | Edit |
|---|---|
| Add an admission check | `execution/lifecycle.go` `Admit` (+ new `AdmitErrorKind` and HTTP status in `errors.go`) |
| Change caps/queueing | `gate/gate.go` (`luaAdmit`), EP/app limits come from `epconfig/` |
| Add a WS/SSE event type | `ws/handler.go` `writeEvent`, `sse/handler.go` `formatSSE`; producers use `runstream.PublishEvent` or `XAdd` |
| Change run-start behaviour / workflow IDs | `execution/lifecycle.go` `Start`/`StartAppFlow`, `temporal/workflow.go` (`WorkflowIDForContext`) — then fix gap 2 |
| Change the agent loop | `orchestrator/orchestrator.go`, `tools.go`; activity wiring in `temporal/activities.go` and `cmd/worker/main.go` |
| Change HITL pause/resume | `temporal/workflow.go` (signal `submit_human_response`), `temporal/signaler.go` |

Worker code changes: restart `them-go-worker` and `-2` (see `platform-overview.md`).
Tests: `go test ./internal/ws/... ./internal/sse/... ./internal/execution/... ./internal/gate/... ./internal/runstream/... ./internal/reconciler/... ./internal/orchestrator/... ./internal/temporal/...`; index in `go/TEST_INDEX.md` (S1-12 WS, S1-13 SSE, S1-17 gate, S1-35 lifecycle).

## Related
- `platform-overview.md` — containers, routes, startup order
- `events-pipeline.md` — separate observability stream (`them:events`), not the run stream
- `docs/decisions/002-reconciler-status-mapping.md` — Temporal→DB status mapping
- `docs/decisions/001-canonical-run-id.md`, `docs/reference/redis-generated.md`
