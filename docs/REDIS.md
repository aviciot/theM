# Odin Redis Key Space
# Last updated: 2026-07-03
# Redis: odin-redis container (fully isolated). DB index: 0 (Odin owns this Redis entirely).

## Key Patterns

| Key Pattern | TTL | Owner | Replica-safe | Purpose |
|---|---|---|---|---|
| `odin:session:token:{sha256(token)}` | 300s | token_cache.py | Yes | L2 token cache (user context) |
| `odin:session:user:{user_id}` | 300s | token_cache.py | Yes | Reverse index for invalidation |
| `odin:agents:registry` | 600s | agent_registry.py | Yes | Serialized enabled agents list |
| `odin:orchestrators:{name}` | 600s | orchestrator_service.py | Yes | Serialized orchestrator config |
| `rl:odin:{user_id}:{hour_slot}` | 7200s | rate_limiter.py | Yes | Rate limit counter (INCR) |
| `odin:bridge:{instance_id}:heartbeat` | 30s | heartbeat bg task | Yes | Per-replica liveness |

## Pub/Sub Channels

| Channel | Publisher | Subscribers | Purpose |
|---|---|---|---|
| `odin:agents:changed` | admin_agents.py on write | agent_registry.py | Invalidate agent cache on all replicas |
| `odin:orchestrators:changed` | admin_orchestrators.py on write | orchestrator_service.py | Invalidate orchestrator config cache |
| `odin:dash:runs` | orchestrator_service.py per run event | ws_dashboard.py (channel: runs) | Lightweight summary of every run event (no tool inputs) |
| `odin:dash:agents` | (reserved) | ws_dashboard.py (channel: agents) | Agent registry change events |
| `odin:dash:metrics` | (reserved) | ws_dashboard.py (channel: metrics) | System metrics |
| `odin:dash:run:{run_id}` | orchestrator_service.py per run event | ws_dashboard.py (channel: run:{uuid}) | Full per-run trace: tool inputs/outputs, token usage, iteration events |

## Naming Rules
- All keys MUST start with `odin:` or `rl:odin:`
- Hash tokens before storing: `hashlib.sha256(token.encode()).hexdigest()`

## Adding a New Key
1. Choose a key pattern following the `odin:{subsystem}:{identifier}` convention
2. Add it to this table with TTL and owner
3. Verify it doesn't overlap with Omni's keys (Omni uses `omni:`, `session:`, `gateway:`, `rl:`, `flow:`, etc.)
