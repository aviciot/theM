# Critical Runtime Blocking Decisions

**Status:** All decisions resolved — Phase R-0 implementation authorized  
**Prepared:** 2026-07-26  
**Gate document:** `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md`

---

## Blocker Set Correction

The gate document listed two inconsistent sets of Phase R-0 blockers. The correct sets are:

| Phase | Blocking decisions |
|---|---|
| R-0 | OD-1, OD-2, OD-7 (OD-8 verified clean — not a blocker) |
| R-1 | OD-3, OD-4, OD-5 |
| R-2 | OD-6 |

OD-3 blocks Phase R-1, not R-0. OD-8 is resolved by inspection: context propagation already
correct in both `invokeA2A` (line 236) and `invokeHTTP` (line 293) in
`internal/agentregistry/registry.go`.

---

## OD-1 — Terminal event must-deliver guarantee

**Status: RESOLVED**  
**Blocks: Phase R-0** (fixes finding L-1)

**Selected:** Separate `termCh chan event.Event` with buffer 1.

The existing `evCh` (capacity 256) carries all transient events. A separate `termCh`
(capacity 1) is added to each subscriber record. `Publish` in `internal/event/bus.go`
routes events whose `Type` is `"done"` or `"error"` to `termCh` via a non-blocking send;
all other events go to `evCh`. `streamEvents` in `ws/handler.go` and `sse/handler.go`
adds a `case ev := <-termCh` as the highest-priority drain case, checked after `evCh`
is exhausted in the same select.

**Why not blocking send with timeout:** holds the publish mutex for up to 50ms, stalling
all other subscribers.  
**Why not priority flag:** every consumer read loop must change; `termCh` changes only
`Subscribe` return type and `streamEvents` select.

**Failure behavior:** if the subscriber goroutine exits before draining `termCh`, the
channel is garbage-collected — safe.

**Required test:** `TestBus_TerminalEventDeliveredOnFullBuffer` — fill the 256-event buffer
with transient events, publish `done`; subscriber must receive `done` despite full `evCh`.

---

## OD-2 — ApplicationID and TenantID in SessionInfo

**Status: RESOLVED**  
**Blocks: Phase R-0** (fixes findings T-1 and T-2)

**Selected:** Populate both `AppID` and `TenantID` in `SessionInfo` in Phase R-0. No Redis
migration required.

In `ws/handler.go` and `sse/handler.go`, after `epconfig.Load` resolves `EPConfig`, set:

```go
sessInfo.AppID    = resolvedCfg.AppID
sessInfo.TenantID = resolvedCfg.TenantID
```

`SessionInfo` gains both fields. The Redis Hash serialization adds the two new fields;
sessions created before this change will not have them and will read back as empty string —
not an error. The shadow TTL is 90s; all pre-migration sessions expire naturally. No Lua
migration script needed.

Adding `AppID` without `TenantID` would be a half-fix: the corrected §2 tenant boundary
requires the full five-element RuntimeIdentity at session creation. Both fields must be
added in the same commit.

**Failure behavior:** if `resolvedCfg` is nil (test paths without EP config wired), both
remain empty string — safe for tests.

**Required test:** `TestSession_AppIDAndTenantIDPopulated` — register a session with a
mock `EPConfig`; verify the Redis Hash contains `app_id` and `tenant_id`.

---

## OD-3 — Orchestration state model

**Status: RESOLVED**  
**Blocks: Phase R-1**

**Selected:** In-memory accumulation with durable per-iteration checkpoints.

**Analysis summary:**

| Criterion | Full DB rebuild (Python) | In-memory + checkpoints | Event-sourced |
|---|---|---|---|
| Per-LLM-call DB reads | 1 per iteration | 0 during normal execution | 0 |
| DB load at 100 concurrent runs × 10 iter | ~1000 extra round-trips/min | ~0 | ~0 |
| Crash recovery | Lossless — full rebuild | To last checkpoint (one iteration max loss) | Same |
| Temporal durability | Redundant — activity is re-run anyway | Correct — Temporal owns outer durability | Same |
| Reconnect behavior | Full DB load at connection | Same — HistoryLoader reads from DB | Same |
| Implementation complexity | Lowest | Low | High |

**Checkpoint schedule:**
1. **Run start:** `HistoryLoader` reads `task_messages` from DB (existing, with LIMIT)
2. **After each LLM response:** write assistant turn to `task_messages` (durable)
3. **After each tool batch:** write tool results to `task_messages` (durable)
4. **Budget counter:** in-process, written to `tasks.tokens_used` each checkpoint

**Crash recovery:** Temporal re-executes the activity; `HistoryLoader` reads the last
checkpoint; run continues from the last complete iteration.

**Why the Python per-iteration rebuild is wrong for Go:** Python rebuilds on every iteration
because async Python cannot hold per-run in-process state across event loop ticks without
complex coordination. Go goroutines trivially hold per-run state for the run's lifetime.
The rebuild is a Python workaround, not a desirable property.

**Required test:** `TestOrchestrator_CheckpointRecovery` — simulate crash after iteration 2
of a 5-iteration run; verify recovered run loads correct history from DB and continues from
iteration 3 without re-invoking the LLM for iterations 1–2.

---

## OD-4 — Parallel agent fan-out

**Status: RESOLVED**  
**Blocks: Phase R-1**

**Selected:** Orchestrator owns goroutines via `sync.WaitGroup` + dual semaphore.

For each batch of tool calls, spawn one goroutine per call. Two semaphores control
concurrency: `parallel_sem` (global, bounded by `max_parallel_tools`) and `agent_sem`
(per-agent, bounded by the agent's `max_concurrency`). Both must be acquired before an
HTTP call begins. Results written to a pre-allocated `[]result` slice (indexed by position,
no mutex needed). `WaitGroup.Wait()` called after all goroutines are started.

`AgentInvoker.Invoke` stays synchronous — pushing concurrency into the registry would
make the registry own scheduling logic that belongs in the orchestrator.

**Required test:** `TestOrchestrator_ParallelFanOut` — 5 tool calls, `max_parallel_tools=2`;
verify at most 2 agent HTTP calls in-flight at any time; verify all 5 results returned.

---

## OD-5 — Memory injection placement

**Status: RESOLVED**  
**Blocks: Phase R-1**

**Selected:** Inline in orchestrator loop.

Memory injection (context summarization) runs inside the orchestrator loop, controlled by
`memory_enabled` and `summarize_every_n_calls` from the orchestrator config. The orchestrator
calls `MemoryStore.Inject(ctx, contextID)` when the threshold is reached. A
middleware/interceptor approach would require restructuring the loop around a hook-chain —
added complexity for a single optional feature.

No separate required test beyond the existing memory service tests; the orchestrator test
for this feature is `TestOrchestrator_MemoryInjectionAtThreshold`.

---

## OD-6 — Artifact storage backend

**Status: RESOLVED**  
**Blocks: Phase R-2**

**Selected:** DB `BYTEA` column with 1MB hard limit. Object store deferred.

`them.artifacts.data BYTEA` with a 1MB limit enforced at the Go service layer before the
INSERT. Artifacts larger than 1MB: service returns `ErrArtifactTooLarge`; handler maps to
413; orchestrator records an error in the tool result.

S3/MinIO integration is a future wave — not blocked on this gate.

---

## OD-7 — Shutdown drain timeout

**Status: RESOLVED**  
**Blocks: Phase R-0**

**Selected:** Increase to 30s. Make configurable via `SHUTDOWN_DRAIN_SECONDS` (default 30,
minimum 5).

Anthropic claude-3-5-sonnet responses can take 20–60s for long outputs. A 5s drain forces
active LLM streams to cancel, breaking the client's run. 30s covers the large majority of
responses.

Add `them:bridge:active_runs:{instanceID}` counter key (published by the pod-heartbeat loop)
for Kubernetes `preStop` hook polling.

**Failure behavior:** if a run does not complete within 30s, the process exits and Temporal
re-executes the activity on another instance. Client may see a disconnect; on reconnect,
Redis Streams replay provides missed events when `events_transport=streams`.

**Required test:** `TestServer_GracefulShutdownWith30sDrain` — start a mock LLM that delays
25s; issue SIGTERM; verify the response completes before the server exits.

---

## OD-8 — Agent HTTP context propagation

**Status: RESOLVED (verified clean — no code change required)**  
**Was listed as blocking Phase R-0 — no longer blocking.**

Inspection of `internal/agentregistry/registry.go` (2026-07-26):

- `invokeA2A` line 236: `http.NewRequestWithContext(ctx, http.MethodPost, ...)` ✓
- `invokeHTTP` line 293: `http.NewRequestWithContext(ctx, http.MethodPost, ...)` ✓
- `httpClient.Timeout = 60s` (line 68) — per-call ceiling independent of context ✓

Context cancellation from a client disconnect propagates correctly to in-flight agent HTTP
calls. No code change required. Closed.

---

## Phase R-0 Implementation Checklist

All decisions resolved. A fresh Sonnet session may begin Phase R-0 immediately.

| Item | Decision | File(s) |
|---|---|---|
| Add `termCh` to event bus | OD-1 / L-1 | `internal/event/bus.go`, `internal/ws/handler.go`, `internal/sse/handler.go` |
| Populate AppID + TenantID in SessionInfo | OD-2 / T-1 / T-2 | `internal/ws/handler.go`, `internal/sse/handler.go`, `internal/session/session.go` |
| Increase shutdown drain to 30s | OD-7 | `internal/server/server.go`, `internal/config/config.go` |
| Fix heartbeat goroutine context (L-2) | — | `internal/health/health.go` |
| Stop Subscribe goroutines before Redis close (L-3) | — | `internal/auth/token_cache.go` |
| `go test -race ./...` | — | All packages |
| Goroutine leak test | — | At minimum `internal/event/`, `internal/ws/`, `internal/sse/` |
| Python sanity `01 02 03 04 15` | — | Zero regressions |
| Update `go/TEST_INDEX.md` | — | Same commit |

**Phase R-1 gate opens** when Phase R-0 is complete and `go test -race ./...` passes.
