---
name: reuse-map
description: "Mapping of Omni source files to Odin destination files"
metadata:
  type: project
---

## COPY verbatim (no changes)
| Omni source | Odin destination |
|---|---|
| app/services/providers/ (entire dir) | app/services/providers/ |
| app/services/ws_connection_manager.py | app/services/ws_connection_manager.py |
| auth_service/ (entire dir) | auth_service/ (port 8701, title changed) |

## ADAPT (copy then change marked parts)
| Omni source | Odin destination | What changes |
|---|---|---|
| app/config.py | app/config.py | Remove Omni-specific vars, add ODIN_INSTANCE_ID, REDIS_DB=1 |
| app/database.py | app/database.py | Redis DB from settings, schema odin |
| app/utils/crypto.py | app/utils/crypto.py | Verbatim (imports from app.config already work) |
| app/utils/logger.py | app/utils/logger.py | app name "odin" |
| app/services/auth_client.py | app/services/auth_client.py | URL to 8701 |
| app/services/mcp_gateway_session_cache.py | app/services/token_cache.py | Redis key prefix odin:session:, DB 1 |
| app/services/rate_limiter.py | app/services/rate_limiter.py | Key prefix rl:odin: |
| app/services/websocket_broadcaster.py | app/services/dashboard_broadcaster.py | Named channels for dashboard multiplex |
| auth_service/config/settings.py | auth_service/config/settings.py | PORT=8701, APP_TITLE=Odin Auth Service |

## NEW (write fresh)
- app/services/orchestrator_service.py (adapt from llm_service.py — adapters replace MCP)
- app/services/agent_registry.py
- app/services/run_recorder.py
- app/adapters/base.py, omni_ws_adapter.py, a2a_adapter.py, factory.py
- app/routers/ws_orchestrator.py, ws_dashboard.py, admin_agents.py, admin_orchestrators.py, admin_tokens.py, runs.py, _deps.py
