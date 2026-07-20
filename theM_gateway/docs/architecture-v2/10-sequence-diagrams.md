# 10 — Sequence Diagrams

> Source of truth: `app/routers/apps.py`, `app/temporal/activities.py`,
> `app/temporal/workflows.py`, `app/services/session_manager.py`,
> `app/adapters/a2a_async_adapter.py`.

---

## 1. WebSocket Request Lifecycle (Primary Path)

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

    Note over GA,Gate: Runtime Gate — single atomic Lua script, one round-trip
    Note over Gate: Gate is the SOLE owner of Set membership (them:ep:*:sessions, them:app:*:sessions)
    GA->>Gate: runtime_gate(ep_slug, app_id, user_id, session_id, token_hash, runtime_config, ep_max_concurrent, rate_limit_rpm)
    Gate->>Redis: Lua (atomic): SCARD them:ep:{slug}:sessions (cap check)
    Gate->>Redis: Lua (atomic): INCR rl:them:{user_id} (rate limit)
    Gate->>Redis: Lua (atomic): SADD them:ep:{slug}:sessions {session_id}
    Gate->>Redis: Lua (atomic): SADD them:app:{app_id}:sessions {session_id}
    Gate->>Redis: Lua (atomic): SET them:ep:{slug}:sess:{session_id}:shadow 1 EX 90 (shadow TTL key)
    Redis-->>Gate: OK
    Gate-->>GA: Admitted

    GA->>SessMgr: session_register(session_id, instance_id, user_id, orch_name, context_id, ep_slug, app_id)
    Note over SessMgr: SessionManager owns the Hash (state) ONLY — never writes to Set index keys
    SessMgr->>Redis: HSET them:sess:{session_id} {...}, EXPIRE 90s

    Note over GA,Redis: Subscribe BEFORE StartWorkflow (ready race fix)
    GA->>Redis: SUBSCRIBE them:dash:run:{context_id}:ctx

    GA->>TC: StartWorkflow(OrchestrationWorkflow, OrchestrationInput)
    TC-->>GA: workflowHandle, workflow_id

    TW->>TW: Pick up workflow task
    TW->>TW: execute load_orchestration_context_activity
    TW->>TW: Load orch config + agents from DB/Redis
    TW->>TW: execute init_run_activity
    TW->>TW: INSERT them.runs, them.tasks
    TW->>Redis: PUBLISH them:dash:run:{context_id}:ctx {type:"ready", run_id, task_id}
    TW->>Redis: PUBLISH them:dash:run:{run_id}:tokens {type:"ready", ...}

    Redis-->>GA: Message on context channel: {type:"ready", run_id}
    GA->>Redis: UNSUBSCRIBE them:dash:run:{context_id}:ctx
    GA->>Redis: SUBSCRIBE them:dash:run:{run_id}:tokens

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

    GA->>SessMgr: session_end(session_id, ep_slug, app_id)
    SessMgr->>Redis: DEL them:sess:{session_id}
    SessMgr->>Redis: Lua (atomic): SREM them:ep:{slug}:sessions, DEL shadow key
    SessMgr->>Redis: Lua (atomic): SREM them:app:{app_id}:sessions, DEL shadow key
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
    GA->>GA: session_end(session_id, ep_slug, app_id)
```

**In Go**, the three goroutines map directly to a `select{}` over three channels:

```go
select {
case <-streamDone:      // stream goroutine finished
case <-cancelRequested: // client sent cancel
case <-controlSignal:   // admin disconnect
}
```

The `finally block` maps to a `defer session_end()` registered immediately after `session_register()`.

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

```mermaid
sequenceDiagram
    participant Admin as AdminClient
    participant PodA_HTTP as Pod_A HTTP
    participant PodA_DB as Pod_A DB Layer
    participant PodA_L1 as Pod_A L1 Cache (in-process)
    participant Redis as Redis (shared)
    participant PodB_L1 as Pod_B L1 Cache (in-process)
    participant PodB_Auth as Pod_B Auth handler

    Admin->>PodA_HTTP: DELETE /api/v1/tokens/{id}
    PodA_HTTP->>PodA_DB: token_store.Revoke(token_hash)
    PodA_DB->>PodA_DB: UPDATE them.access_tokens SET enabled=false\n(or expires_at = now)

    Note over PodA_DB,Redis: Multi-step revocation
    PodA_DB->>Redis: DEL them:session:token:{token_hash} (L2 cache key)
    PodA_DB->>Redis: PUBLISH them:token:revoked {token_hash: "<hash>"}
    PodA_DB-->>PodA_HTTP: OK

    Redis-->>PodA_L1: Message on them:token:revoked channel
    PodA_L1->>PodA_L1: Delete {token_hash} from in-process map

    Redis-->>PodB_L1: Same message (same channel, all subscribers)
    PodB_L1->>PodB_L1: Delete {token_hash} from in-process map

    PodA_HTTP-->>Admin: 204 No Content

    Note over PodB_Auth: Next request to Pod_B with revoked token
    PodB_Auth->>PodB_L1: L1 lookup(token_hash)
    PodB_L1-->>PodB_Auth: Miss (just deleted)
    PodB_Auth->>Redis: GET them:session:token:{token_hash}
    Redis-->>PodB_Auth: Miss (just deleted)
    PodB_Auth->>PodB_Auth: DB lookup them.access_tokens WHERE token_hash = ...
    PodB_Auth-->>PodB_Auth: Row found, enabled=false
    PodB_Auth-->>PodB_Auth: 401 Unauthorized
```

⚠️ **Correction:** The current Python implementation does NOT publish `them:token:revoked`. The L1 cache is per-replica and only expires via TTL (300s). The sequence above documents the **required Go implementation**. The Python path relies solely on L2 TTL expiry — revoked tokens may be accepted for up to 5 minutes on replicas that hold a valid L1/L2 cache entry.

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

- Total shutdown budget: 30 seconds (configurable via `SHUTDOWN_TIMEOUT_SECONDS`)
- Active sessions have 20 of those 30 seconds to complete
- Temporal worker drain is concurrent with the 20-second session wait
- A session that takes longer than 20 seconds is disconnected with `{type:"error", message:"Server shutting down"}`
- The Temporal workflow is NOT cancelled on graceful shutdown — it continues on the next available worker
- Only on SIGKILL (forced) would in-flight activities be interrupted
