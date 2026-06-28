# Odin Database Schema
# Last updated: 2026-06-28
# Source of truth: db/001_schema.sql

Schema: `odin` (owned by odin-bridge)
Auth schema: `auth_service` (owned by odin-auth-service — never touch from bridge)

## odin.llm_providers
LLM provider credentials and config. Encrypted API keys via crypto.py.
| Column | Type | Purpose |
|---|---|---|
| id | SERIAL PK | |
| name | TEXT UNIQUE | provider slug: "anthropic", "openai" |
| display_name | TEXT | UI label |
| api_key_encrypted | TEXT | `enc:` Fernet ciphertext |
| base_url | TEXT | for openai_compat providers |
| default_model | TEXT | e.g. "claude-sonnet-4-6" |
| model_pricing | JSONB | `{model: {input: float, output: float}}` per million tokens |
| enabled | BOOL | |

## odin.config
Key→JSONB config store. Key rows: `llm_routing`.
| Column | Type | Purpose |
|---|---|---|
| config_key | TEXT PK | e.g. "llm_routing" |
| config_value | JSONB | e.g. `{"provider":"anthropic","model":"claude-sonnet-4-6"}` |

## odin.agents ⭐
The agent registry. Each row = one LLM tool `agent__<slug>`.
| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| slug | TEXT UNIQUE | `^[a-z0-9_]{1,48}$` — used in tool name |
| display_name | TEXT | UI label |
| description | TEXT | **LLM tool description** — critical for routing |
| transport | TEXT | `omni_ws` or `a2a` |
| endpoint_url | TEXT | WebSocket URL for the agent |
| auth_token_encrypted | TEXT | `enc:` bearer token sent to agent |
| input_schema | JSONB | JSON Schema for tool input |
| timeout_seconds | INT | per-call timeout |
| max_concurrency | INT | max parallel calls to this agent |
| enabled | BOOL | |
| tags | TEXT[] | grouping/filtering |

## odin.orchestrators ⭐
Named orchestrator configs. One row per WS endpoint `/ws/orchestrate/{name}`.
| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| name | TEXT UNIQUE | in URL path |
| display_name | TEXT | UI label |
| system_prompt | TEXT | LLM system prompt |
| allowed_agent_ids | UUID[] | empty = all enabled agents |
| llm_provider | TEXT | NULL = use odin.config['llm_routing'] |
| llm_model | TEXT | NULL = use default |
| max_iterations | INT | agentic loop bound |
| max_parallel_tools | INT | concurrent agent calls per iteration |
| rate_limit_rpm | INT | per-user rate limit |
| daily_budget_usd | NUMERIC | 0 = unlimited |
| enabled | BOOL | |

## odin.access_tokens
Opaque bearer tokens for WS orchestrator access. Token stored as SHA-256 hash.
| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| token_hash | TEXT UNIQUE | SHA-256 hex of plaintext token |
| label | TEXT | human label |
| user_id | INT | auth_service user ID |
| orchestrator_id | UUID FK→orchestrators | NULL = any orchestrator |
| enabled | BOOL | |
| expires_at | TIMESTAMPTZ | NULL = no expiry |
| last_used_at | TIMESTAMPTZ | updated on each use |

## odin.runs ⭐
One row per orchestrator session (user goal → final answer).
| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| orchestrator_id | UUID FK | |
| orchestrator_name | TEXT | denormalized for fast queries |
| user_id | INT | |
| session_id | UUID | WS connection session |
| goal | TEXT | user's input |
| status | TEXT | running/completed/failed/cancelled |
| final_output | TEXT | assembled final answer |
| iterations | INT | actual iterations used |
| total_tokens_in/out | INT | aggregate across all LLM calls |
| total_cost_usd | NUMERIC | aggregate cost |

## odin.run_steps
One row per agent (tool) invocation within a run.
| Column | Type | Purpose |
|---|---|---|
| run_id | UUID FK | parent run |
| iteration | INT | which loop iteration |
| agent_slug | TEXT | which agent was called |
| tool_call_id | TEXT | LLM-provided ID |
| input | JSONB | tool input arguments |
| output | TEXT | agent response |
| status | TEXT | pending/running/completed/failed/timeout |
| latency_ms | INT | adapter round-trip time |

## odin.run_usage
Per-LLM-call token and cost tracking.

## odin.audit_logs
Admin actions: agent/orchestrator/token CRUD.
