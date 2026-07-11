# the-M Database Schema
# Last updated: 2026-07-11
# Source of truth: db/001_schema.sql + db/003_phase8.sql + db/004_phase9.sql + db/008_debate_stack.sql

Schema: `them` (owned by them-bridge)
Auth schema: `auth_service` (owned by them-auth-service — never access directly from bridge; use `app/services/auth_client.py`)

---

## them.llm_providers
LLM provider credentials and config. Encrypted API keys via `crypto.py`.

| Column | Type | Purpose |
|---|---|---|
| id | SERIAL PK | |
| name | TEXT UNIQUE | provider slug: `"anthropic"`, `"openai"` |
| display_name | TEXT | UI label |
| api_key_encrypted | TEXT | `enc:` Fernet ciphertext |
| base_url | TEXT | for openai_compat providers |
| default_model | TEXT | e.g. `"claude-sonnet-4-6"` |
| model_pricing | JSONB | `{model: {input: float, output: float}}` per million tokens |
| enabled | BOOL | |

---

## them.config
Key→JSONB config store. Key rows: `llm_routing`.

| Column | Type | Purpose |
|---|---|---|
| config_key | TEXT PK | e.g. `"llm_routing"` |
| config_value | JSONB | e.g. `{"provider":"anthropic","model":"claude-sonnet-4-6"}` |

---

## them.agents ⭐
The agent registry. Each enabled row = one LLM tool named `agent__<slug>`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| slug | TEXT UNIQUE | `^[a-z0-9_]{1,48}$` — used in tool name |
| display_name | TEXT | UI label |
| description | TEXT | **LLM tool description** — critical for routing decisions |
| transport | TEXT | `"omni_ws"` or `"a2a"` (or `"a2a_async"` alias) |
| endpoint_url | TEXT | base URL for the agent |
| auth_token_encrypted | TEXT | `enc:` bearer token sent to agent |
| input_schema | JSONB | JSON Schema for tool input (overrides agent card if set) |
| timeout_seconds | INT | per-call timeout |
| max_concurrency | INT | max parallel calls to this agent |
| enabled | BOOL | |
| tags | TEXT[] | grouping/filtering |
| **agent_card** | JSONB | cached agent card fetched via `GET {endpoint}/.well-known/agent-card.json` |
| **supports_streaming** | BOOL | agent declared SSE streaming support |
| **input_modes** | TEXT[] | MIME types agent accepts (e.g. `{"application/json"}`) |
| **last_scan_at** | TIMESTAMPTZ | Timestamp of the most recent security scan (NULL = never scanned) |
| **last_scan_result** | JSONB | Latest scan result — `{score, risk, summary, findings[], http_probes, scanned_at}` (see shape below) |

**Note:** `agent_card`, `supports_streaming`, `input_modes` are populated by the Discover button in the admin UI (or `_ensure_agent_skills` in the task runner). They drive typed A2A input (`_build_parts()` in the adapter).

**`last_scan_result` shape** (written by `_run_scan_job` in `admin_agents.py`):
```json
{
  "score": 72,
  "risk": "low|medium|high",
  "summary": "One-sentence plain-English finding",
  "findings": [{ "id": "tls", "label": "TLS Enforcement", "status": "pass|warn|fail", "risk": "low|medium|high", "detail": "...", "recommendation": "..." }],
  "http_probes": { "tls": "pass|fail", "auth_required": "pass|fail", "reachable": true },
  "scanned_at": "2026-07-11T10:00:00Z"
}
```

---

## them.orchestrators ⭐
Named orchestrator configs. One row per WS endpoint `/ws/orchestrate/{name}`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| name | TEXT UNIQUE | in URL path |
| display_name | TEXT | UI label |
| system_prompt | TEXT | LLM system prompt |
| allowed_agent_ids | UUID[] | empty = all enabled agents |
| llm_provider | TEXT | NULL = use `them.config['llm_routing']` |
| llm_model | TEXT | NULL = use provider default |
| llm_api_key_encrypted | TEXT | NULL = use provider key from `them.llm_providers` |
| llm_base_url | TEXT | optional override for openai_compat providers |
| max_iterations | INT | agentic loop bound |
| max_parallel_tools | INT | concurrent agent calls per iteration |
| rate_limit_rpm | INT | per-user rate limit |
| daily_budget_usd | NUMERIC | 0 = unlimited |
| enabled | BOOL | |
| voice_enabled | BOOL | enable STT transcription |
| transcription_provider | TEXT | e.g. `"openai"`, `"groq"` |
| transcription_model | TEXT | e.g. `"whisper-1"` |
| transcription_api_key_encrypted | TEXT | optional override |
| tts_enabled | BOOL | enable text-to-speech |
| tts_provider | TEXT | e.g. `"openai"` |
| tts_voice | TEXT | e.g. `"nova"` |
| tts_api_key_encrypted | TEXT | optional override |
| memory_enabled | BOOL | enable context summarization (Phase 8.4) |
| summarize_every_n_calls | INT | trigger summary after N agent calls (default 3) |
| memory_raw_fallback_n | INT | raw artifact fallback count (default 5) |
| summarizer_provider | TEXT | NULL = env default (`anthropic`/Haiku) |
| summarizer_model | TEXT | NULL = env default |
| summarizer_api_key_encrypted | TEXT | optional key override for summarizer |
| **history_window** | INT | max prior turns to load in `_load_context_history` (default 20) |
| **a2a_exposed** | BOOL | expose this orchestrator as an A2A agent via `/.well-known/agent-card.json` |
| **budget_tokens** | INT | NULL = no token budget; if set, workflow aborts when tokens_used_so_far exceeds this |

---

## them.access_tokens
Opaque bearer tokens for WS orchestrator / A2A access. Token stored as SHA-256 hash.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| token_hash | TEXT UNIQUE | SHA-256 hex of plaintext token |
| label | TEXT | human label |
| user_id | INT | auth_service user ID |
| orchestrator_id | UUID FK→orchestrators | NULL = any orchestrator |
| enabled | BOOL | |
| expires_at | TIMESTAMPTZ | NULL = no expiry; enforced at API layer (not just DB) |
| last_used_at | TIMESTAMPTZ | updated on each use |

---

## them.runs ⭐
One row per orchestrator invocation (user goal → final answer).

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Temporal workflow run_id |
| orchestrator_id | UUID FK | |
| orchestrator_name | TEXT | denormalized for fast queries |
| user_id | INT | |
| session_id | UUID | WS connection session |
| context_id | UUID | conversation thread — shared across multi-turn runs |
| goal | TEXT | user's input message |
| status | TEXT | `running/completed/failed/canceled` |
| final_output | TEXT | assembled final answer |
| iterations | INT | actual iterations used |
| total_tokens_in | INT | aggregate input tokens across all LLM calls |
| total_tokens_out | INT | aggregate output tokens |
| total_cost_usd | NUMERIC | aggregate cost in USD |
| error | TEXT | error string on failure |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

---

## them.run_steps
One row per agent (tool) invocation within a run. Kept for backward compatibility; new runs also create `them.tasks` child rows.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| run_id | UUID FK→runs | parent run |
| agent_id | UUID FK→agents ON DELETE SET NULL | NULL if agent deleted |
| iteration | INT | which loop iteration (1-indexed) |
| agent_slug | TEXT | denormalized — survives agent deletion |
| tool_call_id | TEXT | LLM-provided tool_use ID |
| input | JSONB | tool input arguments |
| output | TEXT | agent response text |
| status | TEXT | `pending/running/completed/failed/timeout` |
| latency_ms | INT | adapter round-trip time |
| created_at | TIMESTAMPTZ | |

---

## them.run_usage
Per-LLM-call token and cost tracking.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| run_id | UUID FK→runs | |
| iteration | INT | which loop iteration |
| provider | TEXT | e.g. `"anthropic"` |
| model | TEXT | e.g. `"claude-haiku-4-5-20251001"` |
| input_tokens | INT | |
| output_tokens | INT | |
| cost_usd | NUMERIC | |
| created_at | TIMESTAMPTZ | |

---

## them.audit_logs
Admin actions: agent/orchestrator/token CRUD operations.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| user_id | INT | acting user |
| action | TEXT | e.g. `"create_agent"`, `"delete_token"` |
| resource_type | TEXT | |
| resource_id | TEXT | |
| details | JSONB | before/after snapshot |
| created_at | TIMESTAMPTZ | |

---

## them.tasks ⭐ (A2A Phase 3+)
Durable task graph. One row per A2A task (root or child).

State machine: `submitted → working → completed/failed/canceled/rejected`

`input-required` is a pause state: workflow waits for HITL signal.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| run_id | UUID FK→runs | NULL for inbound A2A tasks (orchestrator-as-agent) |
| parent_task_id | UUID FK→tasks | set for child tasks |
| orchestrator_id | UUID FK→orchestrators | which orchestrator owns this task |
| agent_id | UUID FK→agents ON DELETE SET NULL | set for child tasks |
| context_id | UUID | shared across all tasks in one conversation thread |
| state | TEXT | `submitted/working/input-required/completed/failed/canceled/rejected` |
| kind | TEXT | `"root"` or `"subtask"` |
| input_message | JSONB | A2A message parts (initial input — historical; use task_messages for multi-turn) |
| status_message | JSONB | agent status message (error detail) |
| remote_task_id | TEXT | task ID on the child A2A agent |
| error | TEXT | error string on failure |
| budget_tokens | INT | token budget for this task |
| tokens_used | INT | running total |
| deadline | TIMESTAMPTZ | reaper collects hung tasks past this (default: created_at + 30 min) |
| max_depth | INT | recursion depth limit (fork-bomb guard) |
| user_id | INT FK→auth_service.users | task owner (Phase 9) — NULL for legacy rows |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

---

## them.task_messages ⭐ (Phase 11 + Temporal)
Durable per-turn message history for multi-turn conversations. Used by `_load_context_history`
to reconstruct the full conversation when a new turn starts.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| task_id | UUID FK→tasks | root task this message belongs to |
| role | TEXT | `"user"` or `"assistant"` |
| parts | JSONB | provider-native message dict `{role, content: [...]}` |
| seq | INT | ordering within this task (0 = initial user message) |
| created_at | TIMESTAMPTZ | |

**Key invariant:** For every assistant turn with tool_use blocks (role='assistant', content includes
`{type: "tool_use", id: "toolu_..."}` entries), there MUST be a corresponding 'user' row with
`{type: "tool_result", tool_use_id: "toolu_..."}` entries at `seq = assistant_seq + 1`.
This invariant is maintained by `record_tool_results_activity` in `app/temporal/activities.py`.

**Typical sequence within one root task:**
```
seq=0  role=user      {content: "User message"}
seq=1  role=assistant {content: [{type: "tool_use", id: "toolu_abc", name: "agent__coder", input: {...}}]}
seq=2  role=user      {content: [{type: "tool_result", tool_use_id: "toolu_abc", content: "..."}]}
seq=3  role=assistant {content: [{type: "text", text: "Final answer"}]}
```

---

## them.artifacts (A2A Phase 3+)
Output artifacts produced by agent tasks.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| task_id | UUID FK→tasks | producing task |
| context_id | UUID | inherited from task (for cross-context queries) |
| artifact_id | TEXT | agent-assigned artifact identifier (used for dedup) |
| name | TEXT | human label (e.g. `"argument-round-1"`, `"summary-{timestamp}"`) |
| parts | JSONB | A2A part list `[{text: "..."}, {data: {...}}, ...]` |
| append_index | INT | chunk ordering for streaming artifacts |
| last_chunk | BOOL | true = final chunk (artifact is complete) |
| created_at | TIMESTAMPTZ | |

---

## them.applications ⭐ (Phase 9)
User-composable agentic applications. Each row is one deployable entry point bound to an orchestrator.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| name | TEXT | display name |
| slug | TEXT UNIQUE | URL-safe ID `^[a-z0-9_-]{1,64}$` |
| entry_point_type | TEXT | `"websocket"` / `"sse"` / `"webrtc"` |
| orchestrator_id | UUID FK→orchestrators ON DELETE CASCADE | target orchestrator |
| access_policy | JSONB | `{"mode":"token"}` or `{"mode":"public"}` |
| presentation | JSONB | UI metadata (title, theme, icon, etc.) |
| enabled | BOOL | |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

---

## auth_service schema (read-only reference)
Owned by `them-auth-service`. **Never query directly from the bridge** — use `app/services/auth_client.py`.

Tables: `roles`, `users`, `teams`, `team_members`, `user_overrides`, `auth_audit`, `user_sessions`, `blacklisted_tokens`

Key relationships:
- `users.id` (INT) is the `user_id` FK stored in `them.runs`, `them.tasks`, `them.access_tokens`
- JWT subject (`sub`) = `users.id` as string
- JWT `role` claim = `roles.name` for the user's assigned role

---

## Migration Files

| File | Contents |
|---|---|
| `db/001_schema.sql` | Base schema: all `them.*` tables |
| `auth_service/SCHEMA.sql` | Auth schema: all `auth_service.*` tables |
| `db/002_seed.sql` | Seed data: default orchestrators, mock agents, access tokens |
| `db/003_phase8.sql` | Memory columns on `them.orchestrators` |
| `db/004_phase9.sql` | `them.tasks.user_id` column + `them.applications` table |
| `db/005_phase10.sql` | `entry_point_type` column updates for SSE edge |
| `db/006_phase11.sql` | `them.task_messages` table; `history_window` + `budget_tokens` + `a2a_exposed` on orchestrators; `agent_card` + `supports_streaming` + `input_modes` on agents |
| `db/007_docu_stack.sql` | `docu_writer` agent seed + orchestrator config |
| `db/008_debate_stack.sql` | Debate agents (evidence, logic, creative, judge) + `debate_flow` orchestrator |
