# Odin Request Flows
# Last updated: 2026-06-28

## Multi-Agent Orchestrator Run (full sequence)

```
1. Client: WS connect to ws://odin/ws/orchestrate/my-orchestrator
           Authorization: Bearer <opaque_token>

2. Bridge: validate_token(token)
   a. L1 cache hit → use cached user_context
   b. L1 miss → check Redis odin:session:token:{sha256(token)} (TTL 300s)
   c. Redis miss → SELECT from odin.access_tokens WHERE token_hash=... AND enabled
   d. Cache result in L2 Redis + L1 in-process

3. Bridge: load_orchestrator(name)
   a. Check Redis odin:orchestrators:{name} (TTL 600s)
   b. Miss → SELECT from odin.orchestrators WHERE name=... AND enabled

4. Bridge: build_tools()
   a. SELECT agents WHERE id = ANY(allowed_agent_ids) AND enabled
      (empty array → all enabled agents)
   b. Each agent → NeutralTool(name=f"agent__{slug}", description=..., schema=input_schema)

5. Bridge: rate_limit check → Redis INCR rl:odin:{user_id}:{hour_slot}

6. Bridge: run_recorder.start_run() → INSERT odin.runs (status=running)

7. WS: accept, send {"type": "ready", "run_id": "..."}

8. Client: send {"content": "Analyze our payments and check for anomalies"}

9. Orchestrator loop (iteration 1..max_iterations):
   a. Build messages: [system_prompt, history, user_message]
   b. provider.stream_call(messages, tools) → stream LLM response
   c. Stream tokens to client: {"type": "token", "text": "..."}
   d. LLM emits ToolCalls → e.g. [agent__paywatch, agent__analytics]
   e. Fan out: asyncio.gather(
        adapter_paywatch.stream_invoke(...),
        adapter_analytics.stream_invoke(...)
      ) bounded by max_parallel_tools semaphore
   f. Each adapter call → run_recorder.record_step() odin.run_steps
   g. Collect results → feed to LLM as tool_results
   h. run_recorder.record_usage() → odin.run_usage

10. LLM emits final answer (no more tool calls)
    → stream remaining tokens to client
    → {"type": "done", "run_id": "..."}

11. run_recorder.complete_run() → UPDATE odin.runs status=completed
```

## Dashboard Event Flow

```
Dashboard WS connects → subscribe to channels: ["runs", "agents", "metrics"]
Bridge publishes to Redis pub/sub odin:dash:{channel}
dashboard_broadcaster.py relays to subscribed WS clients
```
