---
name: architecture
description: "Odin core mental model, key files, agentic loop, adapter abstraction"
metadata:
  type: project
---

## Core Mental Model
Each enabled `odin.agents` row = ONE LLM tool named `agent__<slug>`.
The `description` column is the LLM tool description — critical for routing.

## The one difference from Omni's loop
Omni: LLM ToolCall → MCP registry → fastmcp.Client.call_tool()
Odin: LLM ToolCall → adapter factory → AgentAdapter.stream_invoke()

## Key files (not yet built — Phase 3+)
- `app/services/orchestrator_service.py` — agentic loop (adapt from Omni's llm_service.py)
- `app/services/agent_registry.py` — loads agents, builds NeutralTool list, Redis cache
- `app/adapters/omni_ws_adapter.py` — Omni WS protocol adapter
- `app/services/token_cache.py` — L1+L2 bearer token cache
- `app/routers/ws_orchestrator.py` — WS endpoint

## Parallel tool calls
When LLM returns multiple ToolCalls in one iteration:
asyncio.gather() bounded by orchestrator.max_parallel_tools + per-agent asyncio.Semaphore(max_concurrency)

## Scalability
Multi-replica from day 1. All shared state in Redis DB 1. See CLAUDE.md scalability table.
