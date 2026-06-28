---
name: conventions
description: "Odin naming conventions, Redis prefix rules, code rules"
metadata:
  type: feedback
---

## Naming
- Tool names: `agent__<slug>` (double underscore, same convention as Omni's `mcp__tool`)
- Redis keys: always `odin:` prefix (never `omni:`, never bare)
- Rate limit keys: `rl:odin:{user_id}:{hour_slot}`
- DB schema: always `odin.` prefix in queries
- Auth schema: `auth_service.` — never query from bridge

## Redis
- DB index 1 ALWAYS. Never DB 0 (Omni's).
- New key → add to docs/REDIS.md immediately

## Copy vs adapt from Omni
- COPY verbatim: providers/, crypto.py, ws_connection_manager.py, rate_limiter.py
- ADAPT (change key prefixes, URLs, schema names): token_cache.py, auth_client.py, websocket_broadcaster.py
- Write fresh: orchestrator_service.py, agent_registry.py, adapters/, run_recorder.py

## Never
- Touch /opt/docker/omni-stack/
- Use Redis DB 0
- Query auth_service.* from bridge (use auth_client.py)
- Set transport='a2a' in real agents (stub only)
- Use Opus for coding — Opus for planning/architecture, Sonnet for coding
