# 10 — Sequence Diagrams

> Source of truth: `app/routers/apps.py`, `app/temporal/activities.py`,
> `app/temporal/workflows.py`, `app/services/session_manager.py`,
> `app/adapters/a2a_async_adapter.py`.

---

## 1. WebSocket Request Lifecycle (Primary Path)

**Channel design note:** The edge pre-allocates `run_id` (UUID) before calling `StartWorkflow` and subscribes to `them:dash:run:{run_id}:tokens` immediately. `run_id` is passed as part of `WorkflowInput` so the worker publishes directly to that channel. There is no context channel and no channel-switch — this eliminates the ready-bootstrap race entirely (see §7 for the race that this replaces).

```mermaid
sequenceDiagram
    participant C as Client
    participant T as Traefik
    participant GA as GoApp (edge layer)
    participant Auth as Auth (bearer cache)
    participant Gate as RuntimeGate (Lua)
    participant SessMgr as SessionManager
    participant TC as TemporalClient
    participant Redis as Redis
    participant TW as TemporalWorker
    participant LLM as LLMProvider
    participant A2A as A2AAgent

    C->>T: WS /apps/{slug}/ws?token=...
    T->>GA: Route (sticky session header)
    GA->>GA: Accept WebSocket
    GA->>Auth: Validate Bearer Token (L1 in-process cache → L2 Redis → PostgreSQL — opaque token)
    Note over Auth: RS256 is used only for JWT validation (user session tokens from auth service)
    Auth-->>GA: TokenInfo {user_id, token_id}
    GA->>GA: Receive first WS message {content, context_id}

    Note over GA,Gate: Gate.Check() — single atomic Lua script, one round-trip
    Note over Gate: Gate is the SOLE owner of Set membership (them:ep:*:sessions, them:app:*:sessions)
    GA->>Gate: Gate.Check(ep_slug, app_id, user_id, session_id, token_hash, ep_max_concurrent, rate_limit_rpm)
    Gate->>Redis: Lua (atomic): prune ghosts (SREM members with no shadow key)
    Gate->>Redis: Lua (atomic): SCARD them:ep:{slug}:sessions (cap check)
    Gate->>Redis: Lua (atomic): INCR rl:them:token:{hash}:{minute} (rate limit)
    Gate->>Redis: Lua (atomic): SADD them:ep:{slug}:sessions {session_id}
    Gate->>Redis: Lua (atomic): SADD them:app:{app_id}:sessions {session_id}
    Gate->>Redis: Lua (atomic): SET them:ep:{slug}:shadow:{session_id} 1 EX 10 (ReservationTTL — short)
    Gate->>Redis: Lua (atomic): SET them:app:{app_id}:shadow:{session_id} 1 EX 10
    Redis-->>Gate: Admitted
    Gate-->>GA: Result{Status: Admitted}

    GA->>SessMgr: session.Register(session_id, instance_id, user_id, orch_name, context_id, ep_slug, app_id)
    Note over SessMgr: SessionManager owns the Hash ONLY — never writes to Set index keys
    SessMgr->>Redis: HSET them:sess:{session_id} {...}, EXPIRE 90s
    SessMgr-->>GA: OK

    GA->>Gate: Gate.Confirm(session_id, ep_slug, app_id)
    Gate->>Redis: SET them:ep:{slug}:shadow:{session_id} 1 EX 90 (extend ReservationTTL → ShadowTTL)
    Gate->>Redis: SET them:app:{app_id}:shadow:{session_id} 1 EX 90
    Note over Gate: If Register() had failed, Gate.Rollback() would SREM + DEL shadow + Release instead

    GA->>GA: run_id = uuid.New() (pre-allocated by edge — no context channel needed)
    Note over GA,Redis: Subscribe to run token channel BEFORE StartWorkflow — no channel-switch race
    GA->>Redis: SUBSCRIBE them:dash:run:{run_id}:tokens

    GA->>TC: StartWorkflow(OrchestrationWorkflow, WorkflowInput{run_id, context_id, ...})
    TC-->>GA: workflowHandle, workflow_id

    TW->>TW: Pick up workflow task
    TW->>TW: execute load_orchestration_context_activity
    TW->>TW: Load orch config + agents from DB/Redis
    TW->>TW: execute init_run_activity (uses run_id from WorkflowInput)
    TW->>TW: INSERT them.runs with pre-allocated run_id, INSERT them.tasks
    TW->>Redis: PUBLISH them:dash:run:{run_id}:tokens {type:"ready", run_id, task_id}

    Redis-->>GA: ready event (already subscribed — no events lost)

    Note over TW,LLM: plan_turn_activity — LLM streaming
    TW->>LLM: POST /messages (stream=true)
    LLM-->>TW: SSE token stream
    TW->>Redis: PUBLISH them:dash:run:{run_id}:tokens {type:"token", text}
    Redis-->>GA: Token event
    GA->>C: WS send {type:"token", text}

    Note over TW,A2A: invoke_agent_activity — A2A call
    TW->>A2A: POST / {jsonrpc:2.0, method:SendMessage}
    A2A-->>TW: {result: {task: {id: remote_task_id}}}
    TW->>Redis: PUBLISH ..:tokens {type:"tool_start", tool}
    Redis-->>GA: tool_start event
    GA->>C: WS send {type:"tool_start", ...}

    loop Poll GetTask (1s interval)
        TW->>A2A: POST / {method:GetTask, id:remote_task_id}
        A2A-->>TW: {result: {status:{state:"working"}}}
        TW->>Redis: PUBLISH ..:tokens {type:"agent_status", state:"working"}
    end

    TW->>Redis: PUBLISH ..:tokens {type:"tool_done", latency_ms}
    Redis-->>GA: tool_done event
    GA->>C: WS send {type:"tool_done"}

    Note over TW: finalize_run_activity
    TW->>TW: UPDATE them.runs status=completed
    TW->>Redis: PUBLISH them:dash:run:{run_id}:tokens {type:"done", run_id, iterations}
    Redis-->>GA: done event
    GA->>C: WS send {type:"done", run_id, iterations}

    GA->>SessMgr: session.End(session_id, ep_slug, app_id)
    SessMgr->>Redis: Lua (atomic): DEL them:sess:{session_id}
    SessMgr->>Redis: Lua (atomic): SREM them:ep:{slug}:sessions {session_id}, DEL them:ep:{slug}:shadow:{session_id}
    SessMgr->>Redis: Lua (atomic): SREM them:app:{app_id}:sessions {session_id}, DEL them:app:{app_id}:shadow:{session_id}
    GA->>Gate: Gate.Release(ep_slug, app_id)
    Gate->>Redis: LPush them:ep:gate:queue:{slug} "1" (wakes one queued waiter)
    GA->>C: WebSocket close (normal)
```

---

## 2. WebSocket Termination Paths (Three Concurrent Goroutines)

```mermaid
sequenceDiagram
    participant C as Client
    participant GA as GoApp
    participant Redis as Redis
    participant TC as TemporalClient

    Note over GA: Three goroutines start concurrently after StartWorkflow

    par stream_goroutine
        loop Until done/error/cancel
            GA->>Redis: Read from them:dash:run:{run_id}:tokens (pub/sub)
            Redis-->>GA: Event (token, tool_start, done, error...)
            GA->>C: WS send event
        end
    and cancel_goroutine
        loop Until cancel or disconnect
            GA->>C: WS receive_json() (blocking read)
            C-->>GA: {type:"cancel"} message
            GA->>GA: cancel_evt.Set()
        end
    and control_goroutine
        loop Until admin signal
            GA->>Redis: SUBSCRIBE them:sess:control:{session_id}
            Redis-->>GA: Admin disconnect signal published
            GA->>GA: return "admin_terminate"
        end
    end

    Note over GA: asyncio.wait(FIRST_COMPLETED) — whichever goroutine finishes first wins

    alt stream_goroutine completes first (done event received)
        GA->>GA: Cancel pending cancel_goroutine and control_goroutine
        Note over GA: Normal completion — no Temporal cancel needed
        GA->>GA: session_end() in finally block
    else cancel_goroutine completes first (cancel message)
        GA->>GA: cancel_evt.Set()
        GA->>GA: Cancel pending stream_goroutine and control_goroutine
        GA->>TC: cancel_workflow(workflow_id)
        TC-->>GA: OK
        GA->>GA: session_end() in finally block
    else control_goroutine completes first (admin terminate)
        GA->>GA: Cancel pending stream_goroutine and cancel_goroutine
        GA->>TC: cancel_workflow(workflow_id)
        TC-->>GA: OK
        GA->>C: WS send {type:"error", message:"Session terminated by administrator"}
        GA->>C: WS close(code=4000)
        GA->>GA: session_end() in finally block
    end

    Note over GA: finally block always runs
    GA->>GA: session.End(session_id, ep_slug, app_id)
    GA->>GA: gate.Release(ep_slug, app_id)
```

**In Go**, the three goroutines map directly to a `select{}` over three channels:

```go
select {
case <-streamDone:      // stream goroutine finished
case <-cancelRequested: // client sent cancel
case <-controlSignal:   // admin disconnect
}
```

The `finally block` maps to a `defer` registered immediately after `Gate.Confirm()`:
```go
defer func() {
    session.End(ctx, sessionID, epSlug, appID)
    gate.Release(ctx, cfg)
}()
```

---

## 3. HITL (Human-in-the-Loop) Signal Flow

```mermaid
sequenceDiagram
    participant C1 as Client (WS)
    participant GA as GoApp
    participant TW as TemporalWorker
    participant TS as TemporalServer
    participant C2 as Human (REST)

    Note over TW,TS: Inside invoke_agent_activity
    TW->>TW: A2A adapter receives input-required status
    TW->>GA: Redis PUBLISH them:dash:run:{run_id}:tokens\n{type:"input_required", tool_call_id, elapsed_ms}
    GA->>C1: WS send {type:"input_required", tool_call_id}

    Note over TW,TS: invoke_agent_activity returns InvokeAgentResult(status="input-required")
    TW->>TS: Activity completes with status="input-required"
    TW->>TW: Workflow: detect input_required result
    TW->>TW: _human_response = None
    TW->>TS: workflow.wait_condition(lambda: _human_response is not None, timeout=10min)
    Note over TS: 10-minute timer starts

    Note over C2,GA: Human submits response (may be a different person/session)
    C2->>GA: POST /api/v1/runs/{run_id}/signal\n{human_response: "Yes, proceed with option A"}
    GA->>GA: Validate Bearer token
    GA->>TS: temporal.SignalWorkflow(workflow_id, "human_response",\n{content: "Yes, proceed with option A"})
    TS-->>GA: OK
    GA-->>C2: 202 Accepted

    TS->>TW: Deliver signal to OrchestrationWorkflow
    TW->>TW: submit_human_response({content: "Yes, proceed with option A"})
    TW->>TW: _human_response = {content: "..."}
    TW->>TW: wait_condition satisfied
    TW->>TW: Inject human text as tool result for input-required slot
    TW->>TW: _human_response = None (reset)
    TW->>TW: Loop continues to next planning turn

    Note over TW,TS: Timeout path (10 min elapsed with no signal)
    TS->>TW: wait_condition timeout
    TW->>TW: _human_response is None → run_status = "failed"
    TW->>TW: run_error = "Human response timeout (10 minutes)"
    TW->>TW: break → finalizing state
    TW->>TW: finalize_run_activity(status="failed", error=timeout_msg)
    TW->>GA: Redis PUBLISH them:dash:run:{run_id}:tokens {type:"error", message:"..."}
    GA->>C1: WS send {type:"error", message:"Human response timeout (10 minutes)"}
```

---

## 4. Token Revocation Broadcast (Multi-Pod)

**Actual guarantee:** Redis Pub/Sub is at-most-once. A pod that misses the revocation message (restart, transient Redis disconnect) will continue accepting the token from its L1 cache until the L1 entry expires. The L2 key is deleted at revocation time, but L1 is not backed by L2 on reads — an L1 hit returns without consulting L2. The real worst-case window is the **L1 TTL** (60s), not sub-second.

**Design decision:** L1 TTL is set to 60s (not 300s). The pub/sub path provides fast-path eviction in the common case. The 60s TTL is the hard upper bound for pods that miss the event. For use cases that require stronger guarantees, set `AUTH_BEARER_L1_TTL_SECONDS=0` to disable L1 caching entirely — all validation falls through to L2 Redis (one Redis GET per request, ~0.5ms, no L1 stale window).

```mermaid
sequenceDiagram
    participant Admin as AdminClient
    participant PodA_HTTP as Pod_A HTTP
    participant PodA_DB as Pod_A DB Layer
    participant PodA_L1 as Pod_A L1 Cache (in-process, TTL 60s)
    participant Redis as Redis (shared)
    participant PodB_L1 as Pod_B L1 Cache (in-process, TTL 60s)
    participant PodB_Auth as Pod_B Auth handler

    Admin->>PodA_HTTP: DELETE /api/v1/tokens/{id}
    PodA_HTTP->>PodA_DB: token_store.Revoke(token_hash)
    PodA_DB->>PodA_DB: UPDATE them.access_tokens SET enabled=false

    Note over PodA_DB,Redis: Revocation — L2 delete + pub/sub broadcast
    PodA_DB->>Redis: DEL them:token:{token_hash} (L2 cache key)
    PodA_DB->>Redis: PUBLISH them:token:revoked {token_hash: "<hash>"}
    PodA_DB-->>PodA_HTTP: OK
    PodA_HTTP-->>Admin: 204 No Content

    Note over Redis,PodB_L1: Fast path — pub/sub delivery (at-most-once)
    Redis-->>PodA_L1: Message on them:token:revoked channel
    PodA_L1->>PodA_L1: Delete {token_hash} from in-process map

    Redis-->>PodB_L1: Same message (IF Pod_B is connected and subscribed)
    PodB_L1->>PodB_L1: Delete {token_hash} from in-process map

    Note over PodB_L1: Slow path — if Pod_B missed the pub/sub message
    Note over PodB_L1: L1 entry expires after 60s (L1 TTL upper bound)

    Note over PodB_Auth: Next request to Pod_B with revoked token (after eviction)
    PodB_Auth->>PodB_L1: L1 lookup(token_hash)
    PodB_L1-->>PodB_Auth: Miss (evicted by pub/sub or expired)
    PodB_Auth->>Redis: GET them:token:{token_hash}
    Redis-->>PodB_Auth: Miss (L2 deleted at revocation)
    PodB_Auth->>PodB_Auth: DB lookup them.access_tokens WHERE token_hash = ...
    PodB_Auth-->>PodB_Auth: Row found, enabled=false → 401 Unauthorized
```

**Revocation guarantee summary:**

| Scenario | Window |
|---|---|
| Pod receives pub/sub message | ~1ms (pub/sub latency) |
| Pod misses pub/sub (restart / transient disconnect) | ≤60s (L1 TTL) |
| L1 disabled (`AUTH_BEARER_L1_TTL_SECONDS=0`) | ~0.5ms per request (L2 Redis GET) |

**What the L2 TTL does NOT protect:** If L1 holds an entry and L2 is already deleted, L1 does not fall through to L2 on the next read. L1 hit = return immediately. Only pub/sub eviction or L1 TTL expiry clears a stale L1 entry.

⚠️ **Python note:** The current Python implementation does NOT publish `them:token:revoked`. The Python path relies solely on L1/L2 TTL expiry (300s window). The sequence above documents the Go implementation.

---

## 5. Agent Registry Invalidation (Multi-Pod)

```mermaid
sequenceDiagram
    participant Admin as AdminClient
    participant PodA as Pod_A (HTTP)
    participant DB as PostgreSQL
    participant Redis as Redis
    participant PodA_Reg as Pod_A AgentRegistry (in-process)
    participant PodB_Reg as Pod_B AgentRegistry (in-process)

    Admin->>PodA: PUT /api/v1/admin/agents/{id}
    PodA->>DB: UPDATE them.agents SET ... WHERE id = {id}
    DB-->>PodA: OK

    Note over PodA,Redis: Cache invalidation
    PodA->>Redis: DEL them:agents:registry (L2 key)
    PodA->>Redis: PUBLISH them:agents:changed ""

    Redis-->>PodA_Reg: Message on them:agents:changed
    PodA_Reg->>PodA_Reg: Clear in-process agent list

    Redis-->>PodB_Reg: Same message
    PodB_Reg->>PodB_Reg: Clear in-process agent list

    PodA-->>Admin: 200 OK

    Note over PodA_Reg: Next orchestration load on Pod_A
    PodA_Reg->>Redis: GET them:agents:registry
    Redis-->>PodA_Reg: Miss
    PodA_Reg->>DB: SELECT * FROM them.agents WHERE enabled=true
    DB-->>PodA_Reg: Fresh agent list
    PodA_Reg->>Redis: SETEX them:agents:registry 600 {serialized}
    PodA_Reg->>PodA_Reg: Populate in-process cache
```

---

## 6. Graceful Shutdown Sequence

```mermaid
sequenceDiagram
    participant OS as OS
    participant Main as main.go
    participant HTTP as HTTP Server
    participant WS as WS Edge (all sessions)
    participant TW as Temporal Worker
    participant DB as DB Pool
    participant R as Redis Client
    participant OTel as OTel Exporter

    OS->>Main: SIGTERM
    Main->>Main: Capture signal, start shutdown context (30s deadline)

    Note over Main,HTTP: Phase 1 — Stop accepting new connections
    Main->>HTTP: server.Shutdown(ctx) — stop accepting new HTTP/WS

    Note over Main,WS: Phase 2 — Drain active WebSocket sessions
    Main->>WS: Broadcast {type:"draining", message:"Server shutting down"}\nto all active session channels
    Note over WS: Sessions have up to 20s to complete their current run
    WS->>WS: Wait for active sessions to finish\n(or 20s timeout, whichever first)

    Note over Main,TW: Phase 3 — Drain Temporal worker
    Main->>TW: worker.Stop() — stop polling for new tasks
    Note over TW: In-flight activities complete naturally\n(Temporal heartbeat keeps them alive)
    TW->>TW: Wait for in-flight activities to finish
    TW-->>Main: Worker drained

    Note over Main,DB: Phase 4 — Close infrastructure
    par Close DB pool
        Main->>DB: db.Close()
        DB-->>Main: OK
    and Close Redis subscriptions
        Main->>R: Unsubscribe all pub/sub channels
        Main->>R: redis.Close()
        R-->>Main: OK
    end

    Note over Main,OTel: Phase 5 — Flush telemetry
    Main->>OTel: tracerProvider.Shutdown(ctx) — flush pending spans
    OTel-->>Main: Flushed

    Main->>OS: os.Exit(0)
```

**Shutdown timeline notes:**

---

## 7. Ready Bootstrap Race — Why the Context Channel Was Removed

The previous design used two Redis channels with a channel-switch between them:

```
Edge subscribes to: them:dash:run:{context_id}:ctx
Edge calls StartWorkflow(WorkflowInput without run_id)
Worker runs init_run_activity, allocates run_id, publishes to context channel
Edge receives {type:"ready", run_id}
Edge UNSUBSCRIBES context channel          ← gap opens here
Edge SUBSCRIBES them:dash:run:{run_id}:tokens  ← gap closes here
Worker immediately starts plan_turn_activity, publishes token events
```

**The race:** Between UNSUBSCRIBE and SUBSCRIBE, the worker may already be executing `plan_turn_activity` and publishing token events. These events are published to `them:dash:run:{run_id}:tokens` before the edge has subscribed. Redis Pub/Sub is at-most-once with no message buffering — events published during the gap are silently lost. The client never sees the first token(s) of the response.

**The fix (§1 above):**

```
Edge pre-allocates run_id = uuid.New()
Edge SUBSCRIBES them:dash:run:{run_id}:tokens  ← subscribed before workflow starts
Edge calls StartWorkflow(WorkflowInput{run_id: run_id, ...})
Worker runs init_run_activity using the pre-allocated run_id
Worker publishes all events to them:dash:run:{run_id}:tokens
Edge receives all events — no gap, no channel switch
```

The context channel (`them:dash:run:{context_id}:ctx`) is removed entirely. The worker no longer needs to publish a `ready` signal to a separate channel; the single token channel carries the `ready` event as well.

**WorkflowInput change required:** `run_id uuid.UUID` must be added to `WorkflowInput` (and the Python `OrchestrationInput` dataclass during Phase 5.4 overlap). `init_run_activity` uses the provided `run_id` for the `INSERT INTO them.runs` row instead of allocating one internally.

- Total shutdown budget: 30 seconds (configurable via `SHUTDOWN_TIMEOUT_SECONDS`)
- Active sessions have 20 of those 30 seconds to complete
- Temporal worker drain is concurrent with the 20-second session wait
- A session that takes longer than 20 seconds is disconnected with `{type:"error", message:"Server shutting down"}`
- The Temporal workflow is NOT cancelled on graceful shutdown — it continues on the next available worker
- Only on SIGKILL (forced) would in-flight activities be interrupted
