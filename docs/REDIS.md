# the-M Redis Key Space
# Last updated: 2026-09-02
# Redis: them-redis container (fully isolated). DB index: 0 (the-M owns this Redis entirely).

## Key Patterns

| Key Pattern | TTL | Owner | Replica-safe | Purpose |
|---|---|---|---|---|
| `them:session:token:{sha256(token)}` | 300s | token_cache.py | Yes | L2 token cache (user context) |
| `them:session:user:{user_id}` | 300s | token_cache.py | Yes | Reverse index for per-user invalidation |
| `them:agents:registry` | 600s | agent_registry.py (LEGACY — Python permanently retired) | Yes | **LEGACY Python agent registry key — no longer written or read. See `them:agents:registry:{tenant_id}` below.** |
| `them:agents:registry:{tenant_id}` | 600s | go/internal/agentregistry/registry.go | Yes | Per-tenant serialized enabled agents list (SEC-03 fix). Keyed by tenant UUID. L1 in-process by `"{tenantID}:{slug}"`. Invalidation is per-tenant only — publishing to `them:agents:changed` with tenantID as payload. |
| `them:orch:tmpl:{name}` | 600s | loaders.py / task_runner.py | Yes | Serialized shared orchestrator template (them.orchestrators) |
| `them:app:{app_id}:orch:{name}` | 600s | loaders.py / task_runner.py | Yes | Serialized app-owned orchestrator instance (them.app_orchestrators) |
| `them:orch:loc:{name}` | 600s | loaders.py / task_runner.py | Yes | Locator pointer: `"tmpl"` or `"app:{app_id}"` — tells readers which namespace holds the config |
| `rl:them:{user_id}:{hour_slot}` | 7200s | rate_limiter.py | Yes | Per-user rate limit counter (INCR) |
| `rl:them:app:{app_id}:{hour_slot}` | 7200s | runtime_manager.py | Yes | App-level rate limit counter (INCR). Separate from per-user counter. Enforced in runtime_gate() step 3. |
| `rl:them:{tenant_id}:token:{hash}:{minute}` | 90s | go/internal/gate/gate.go (luaAdmit) + go/internal/ratelimit/limiter.go | Yes | **Tenant-scoped** per-token rate limit bucket (INCR). Minute bucket = Unix/60. Tenant prefix prevents cross-tenant token collisions. Used by gate admission Lua script (KEYS[5]) and Limiter.CheckToken. |
| `them:bridge:{instance_id}:heartbeat` | 30s | heartbeat bg task | Yes | Per-replica liveness |
| `them:sess:{session_id}` | 90s | session_manager.py | Yes | Per-session metadata Hash (refreshed by touch(); expires if pod dies) |
| `them:ep:{ep_slug}:sessions` | none | runtime_manager.py (reserve) + session_manager.py (meta/remove) | Yes | Set of active session_ids for one entry point. Slot reserved atomically by Lua EVAL in runtime_gate(); session_manager.end() is the sole remover. |
| `them:app:{app_id}:sessions` | none | session_manager.py | Yes | Set of active session_ids for one application |
| `them:pod:{pod_id}` | 30s | session_manager.py + main.py | Yes | Pod liveness + session count Hash (written every 15s by heartbeat loop) |
| `them:pods` | none | session_manager.py | Yes | Set of live pod instance_ids |
| `them:dash:sessions:state:{app_id}` | 120s | dashboard_broadcaster.py | Yes | Session state Hash (session_id → JSON) for snapshot delivery to new WS subscribers |
| `them:{tenant_id}:scan:state:{agent_id}` | 300s (started/step), 30s (failed), no TTL (complete) | go/internal/admin/scanjob.go | Yes | **Tenant-scoped** agent security scan result Hash (type, score, risk, summary, findings, scanned_at). Written by scan goroutine; read by Go dashboard WS on connect to deliver snapshot. Key includes tenantID to prevent cross-tenant leakage. |
| `them:ctx:{context_id}:heads` | 300s | context_service.py | Yes | Hot cache of recent artifacts for a context (Phase 5) |
| `them:ctx:{context_id}:summary` | 3600s | memory_service.py | Yes | Latest context summary text for injecting into agent messages (Phase 8.4) |
| `them:{tenant_id}:mcp:manifest:{slug}` | 300s | go/internal/mcp/registry.go | Yes | **Tenant-scoped** cached MCP tool manifest JSON for one server. Written by supervisor on discovery; read by executor on tool call. Includes tenantID to prevent cross-tenant slug collision. |
| `them:{tenant_id}:mcp:health:{slug}` | 90s | go/internal/mcp/registry.go | Yes | **Tenant-scoped** latest health probe result JSON for one server. Short TTL — absence implies unknown/unreachable. |
| `them:mcp:leader` | 30s | go/internal/mcp/leader.go | No | Leader election lock for `them-mcp-service` supervisor. SET NX PX 30000; renewed every 20s by the current leader. Only one pod runs the reconciler/supervisor at a time. |
| `them:hitl:{task_id}` | 24h | go/internal/agentgen/hitl_store.go (written by agent-runtime on HITL submit) | Yes | HITL handle: `{workflow_id, run_id, tenant_id, step_id, wait_token, state}` — maps an A2A task ID to its paused Temporal workflow. State machine: "submitted" → "waiting" (when workflow reaches hw node, with deterministic wait_token) → "signalled" (after TrySignal CAS) → deleted (MarkDone on completion). Signal endpoint at `/admin/canvas-tasks/{task_id}/signal` reads this to verify tenant, validate wait_token, and route human responses. |
| `them:agent:a2atask:{task_id}` | 24h | go/internal/agentgen/a2a_task_store.go (RedisA2ATaskStore) | Yes | Full A2A SDK Task JSON blob — implements `taskstore.Store` for the A2A server SDK. Enables durable task state across pod restarts; read by GetTask and SubscribeToTask. |

## Pub/Sub Channels

| Channel | Publisher | Subscribers | Purpose |
|---|---|---|---|
| `them:agents:changed` | go/internal/admin/service/agents.go on write | go/internal/agentregistry/registry.go | Per-tenant agent cache invalidation. Payload is tenantID UUID string. Empty payload = ignored (guards against global eviction). |
| `them:agents:registry:{tenant_id}` | — (pub/sub signal only) | go/internal/admin/service/agent_definitions_publish.go on canvas publish | Published after a canvas agent is published to runtime. Payload is agentID UUID. Triggers registry cache refresh in agentregistry. |
| `them:orchestrators:changed` | admin_orchestrators.py on write | (no subscriber — reserved for future in-process L1 cache) | Invalidate orchestrator template cache signal |
| `them:dash:runs` | task_runner.py per run event | go/internal/dashboard (channel: runs) | Lightweight summary of every run event (no tool inputs) |
| `them:dash:agents` | (reserved) | go/internal/dashboard (channel: agents) | Agent registry change events |
| `them:dash:metrics` | (reserved) | go/internal/dashboard (channel: metrics) | System metrics |
| `them:dash:apps` | main.py `_app_liveness_loop` every 30s | go/internal/dashboard (channel: apps) | App liveness probe results: `{type: "app_status", statuses: {slug: {reachable, latency_ms}}}` |
| `them:dash:run:{run_id}` | task_runner.py per run event | go/internal/dashboard (channel: run:{uuid}) | Full per-run trace: tool inputs/outputs, token usage, iteration events |
| `them:dash:agent:{agent_id}` | go/internal/admin security-scan goroutine | go/internal/dashboard (channel: agent:{id}) | Per-agent events: `scan_started`, `scan_complete`, `scan_failed`. On connect, Go bridge delivers snapshot from `them:{tenant_id}:scan:state:{agent_id}` hash. Transient pub/sub — no TTL, no persistence. |
| `them:dash:services:stats` | go/cmd/middleware-worker/main.go after each scan job completes | go/internal/dashboard (channel: services:stats) | Lightweight invalidation signal (payload `{}`) — Services page frontend re-fetches stats on receipt. No payload data. |
| `them:dash:services:health` | go/cmd/middleware-worker/main.go every 10s (TTL 30s) | go/internal/admin/services_stats.go GET /admin/services/stats | Worker liveness key. Presence = worker up; absence (TTL expired) = worker down. Value: `{"status":"up"}`. Also pushed as `{type:services_health,worker_up}` snapshot on dashboard WS subscribe. |
| `them:tasks:{task_id}:events` | task_store.py on every state transition | ws_orchestrator.py subscribers | Task lifecycle events (created, state, artifact) |
| `them:dash:sessions:{app_id}` | dashboard_broadcaster.py publish_session_event | go/internal/dashboard (channel: sessions:\<app_id\>) | Per-app session_start / session_end events; snapshot from `them:dash:sessions:state:{app_id}` delivered on connect |
| `them:sess:control:{session_id}` | runtime_manager.py signal_disconnect (via admin_sessions router) | apps.py + ws_orchestrator.py per-session `_control_listener` | Cross-replica admin session termination. One message closes the WS with code 4000. Best-effort pub/sub — no persistence, no TTL. |
| `them:mcp:manifest:changed` | go/internal/mcp/registry.go `PublishManifestChanged` | (reserved — future Go bridge real-time update) | Signals that a server's tool manifest was updated. Payload is server slug. |

## Naming Rules
- All keys MUST start with `them:` or `rl:them:`
- Hash tokens before storing: `hashlib.sha256(token.encode()).hexdigest()`
- Never use the old `odin:` prefix — that name is retired

## Adding a New Key
1. Choose a key pattern following the `them:{subsystem}:{identifier}` convention
2. Add it to this table with TTL, owner file, replica-safety, and purpose
3. Document it in this file before merging
