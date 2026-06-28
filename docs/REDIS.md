# Odin Redis Key Space
# Last updated: 2026-06-28
# Redis DB index: 1 (Omni uses 0 — NEVER use DB 0 from Odin)

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
| `odin:dash:{channel}` | various | ws_dashboard.py | Dashboard event fan-out (named channels) |

## Naming Rules
- All keys MUST start with `odin:` or `rl:odin:`
- Never use Redis DB 0 (Omni's namespace)
- Hash tokens before storing: `hashlib.sha256(token.encode()).hexdigest()`

## Adding a New Key
1. Choose a key pattern following the `odin:{subsystem}:{identifier}` convention
2. Add it to this table with TTL and owner
3. Verify it doesn't overlap with Omni's keys (Omni uses `omni:`, `session:`, `gateway:`, `rl:`, `flow:`, etc.)
