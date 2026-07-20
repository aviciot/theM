# the-M Redis Key Space
# Last updated: 2026-07-21
# Redis: them-redis container (fully isolated). DB index: 0 (the-M owns this Redis entirely).

## Key Patterns

| Key Pattern | TTL | Owner | Replica-safe | Purpose |
|---|---|---|---|---|
| `them:session:token:{sha256(token)}` | 300s | token_cache.py | Yes | L2 token cache (user context) |
| `them:session:user:{user_id}` | 300s | token_cache.py | Yes | Reverse index for per-user invalidation |
| `them:agents:registry` | 600s | agent_registry.py | Yes | Serialized enabled agents list |
| `them:orch:tmpl:{name}` | 600s | loaders.py / task_runner.py | Yes | Serialized shared orchestrator template (them.orchestrators) |
| `them:app:{app_id}:orch:{name}` | 600s | loaders.py / task_runner.py | Yes | Serialized app-owned orchestrator instance (them.app_orchestrators) |
| `them:orch:loc:{name}` | 600s | loaders.py / task_runner.py | Yes | Locator pointer: `"tmpl"` or `"app:{app_id}"` — tells readers which namespace holds the config |
| `rl:them:{user_id}:{hour_slot}` | 7200s | rate_limiter.py | Yes | Per-user rate limit counter (INCR) |
| `rl:them:app:{app_id}:{hour_slot}` | 7200s | runtime_manager.py | Yes | App-level rate limit counter (INCR). Separate from per-user counter. Enforced in runtime_gate() step 3. |
| `them:bridge:{instance_id}:heartbeat` | 30s | heartbeat bg task | Yes | Per-replica liveness |
| `them:sess:{session_id}` | 90s | session_manager.py | Yes | Per-session metadata Hash (refreshed by touch(); expires if pod dies) |
| `them:ep:{ep_slug}:sessions` | none | runtime_manager.py (reserve) + session_manager.py (meta/remove) | Yes | Set of active session_ids for one entry point. Slot reserved atomically by Lua EVAL in runtime_gate(); session_manager.end() is the sole remover. |
| `them:app:{app_id}:sessions` | none | session_manager.py | Yes | Set of active session_ids for one application |
| `them:pod:{pod_id}` | 30s | session_manager.py + main.py | Yes | Pod liveness + session count Hash (written every 15s by heartbeat loop) |
| `them:pods` | none | session_manager.py | Yes | Set of live pod instance_ids |
| `them:dash:sessions:state:{app_id}` | 120s | dashboard_broadcaster.py | Yes | Session state Hash (session_id → JSON) for snapshot delivery to new WS subscribers |
| `them:ctx:{context_id}:heads` | 300s | context_service.py | Yes | Hot cache of recent artifacts for a context (Phase 5) |
| `them:ctx:{context_id}:summary` | 3600s | memory_service.py | Yes | Latest context summary text for injecting into agent messages (Phase 8.4) |
| `them:dash:run:{runID}:stream` | 48h (safety) / 24h (final) | activities.py `stream_publish()` | Yes | **Phase 11c+** Redis Stream for durable run event replay. MAXLEN ~5000 entries (~75KB/run). Safety TTL 48h set on first XADD; final TTL 24h set atomically when terminal event (`done`/`error`/`canceled`/`terminated`/`timed_out`) is published. Written by Python worker via Lua script. Read by Go gateway for replay + live delivery (Phase 11c-B+). |

## Pub/Sub Channels

| Channel | Publisher | Subscribers | Purpose |
|---|---|---|---|
| `them:agents:changed` | admin_agents.py on write | agent_registry.py | Invalidate agent cache on all replicas |
| `them:orchestrators:changed` | admin_orchestrators.py on write | (no subscriber — reserved for future in-process L1 cache) | Invalidate orchestrator template cache signal |
| `them:dash:runs` | task_runner.py per run event | ws_dashboard.py (channel: runs) | Lightweight summary of every run event (no tool inputs) |
| `them:dash:agents` | (reserved) | ws_dashboard.py (channel: agents) | Agent registry change events |
| `them:dash:metrics` | (reserved) | ws_dashboard.py (channel: metrics) | System metrics |
| `them:dash:apps` | main.py `_app_liveness_loop` every 30s | ws_dashboard.py (channel: apps) | App liveness probe results: `{type: "app_status", statuses: {slug: {reachable, latency_ms}}}` |
| `them:dash:run:{run_id}` | task_runner.py / activities.py `_publish_dash()` per run event | ws_dashboard.py (channel: run:{uuid}) | Full per-run trace: tool inputs/outputs, token usage, iteration events |
| `them:dash:run:{run_id}:tokens` | activities.py `stream_publish()` via Lua (dual-publish mode) | Go gateway `internal/runstream` (legacy Pub/Sub path) | **Legacy bridge stream channel.** Receives same payload as `:stream` key when `dual_publish=True`. Removed in Phase 11c-D once all Go handlers use Streams. |
| `them:dash:agent:{agent_id}` | admin_agents.py `_run_scan_job` | ws_dashboard.py (channel: agent:{id}) | Per-agent events: `scan_started`, `scan_complete`, `scan_failed`. Transient pub/sub — no TTL, no persistence. |
| `them:tasks:{task_id}:events` | task_store.py on every state transition | ws_orchestrator.py subscribers | Task lifecycle events (created, state, artifact) |
| `them:dash:sessions:{app_id}` | dashboard_broadcaster.py publish_session_event | ws_dashboard.py (channel: sessions:\<app_id\>) | Per-app session_start / session_end events; snapshot on subscribe |
| `them:sess:control:{session_id}` | runtime_manager.py signal_disconnect (via admin_sessions router) | apps.py + ws_orchestrator.py per-session `_control_listener` | Cross-replica admin session termination. One message closes the WS with code 4000. Best-effort pub/sub — no persistence, no TTL. |

## Naming Rules
- All keys MUST start with `them:` or `rl:them:`
- Hash tokens before storing: `hashlib.sha256(token.encode()).hexdigest()`
- Never use the old `odin:` prefix — that name is retired

## Adding a New Key
1. Choose a key pattern following the `them:{subsystem}:{identifier}` convention
2. Add it to this table with TTL, owner file, replica-safety, and purpose
3. Document it in this file before merging
