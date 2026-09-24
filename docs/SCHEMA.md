# the-M Database Schema
# Last updated: 2026-09-04
# Source of truth: db/001_schema.sql + migrations db/003 through db/077

Schema: `them` (owned by them-bridge)
Auth schema: `auth_service` (owned by them-auth-service — never access directly from bridge; use `app/services/auth_client.py`)

---

## them.llm_providers ⭐
LLM provider-level config per tenant (or platform default). Encrypted API keys via `crypto.py`/`internal/crypto`.
Migration 057 adds per-tenant override support: `tenant_id IS NULL` = platform default; non-NULL = tenant override.
Migration 105 adds `allowed_models` — the tenant's own key management moves to the child table
`them.llm_provider_keys` (multiple named keys per provider); `api_key_encrypted` on this table is frozen to
legacy/platform-row use going forward.
Unique constraints: `llm_providers_name_platform_uq` (name WHERE tenant_id IS NULL) and `llm_providers_name_tenant_uq` (name, tenant_id WHERE tenant_id IS NOT NULL).
Migration 107 seeds platform-default rows for `gemini` and `groq` (both `enabled=false` by default) —
they were fully supported in code (dispatch, model lists, probing) since before this table existed in
its current form, but never had a row, so they never appeared in the LLM Providers tab. Platform-default
rows today: `anthropic` (enabled), `openai`, `gemini`, `groq` (all three disabled by default — same
`enabled=false` precedent `openai` set in `003_phase8.sql`), plus the unrelated dev-only `mock` provider
(instant reply, no API key, enabled — not real-provider config, still shows up in the same list).
Queried exclusively via the admin pool (`them_admin`, BYPASSRLS) — tenant isolation is enforced by an
application-level `WHERE tenant_id = $1` predicate (`tenantctx.MustTenantIDFromCtx`), not by RLS/GUC. RLS
policies below exist for defense-in-depth only.

| Column | Type | Purpose |
|---|---|---|
| id | SERIAL PK | referenced by `them.llm_provider_keys.llm_provider_id` |
| name | TEXT | provider slug: `"anthropic"`, `"openai"` |
| display_name | TEXT | UI label |
| api_key_encrypted | TEXT | `enc:` Fernet ciphertext — legacy/platform-row key only (added 057) |
| base_url | TEXT | for openai_compat providers |
| default_model | TEXT | e.g. `"claude-sonnet-4-6"` |
| model_pricing | JSONB | `{model: {input: float, output: float}}` per million tokens |
| enabled | BOOL | provider on/off for this tenant — independent of whether any key exists |
| tenant_id | UUID FK→them.tenants(id) | NULL = platform default; non-NULL = per-tenant override (added 057) |
| allowed_models | JSONB | array of model ID strings the tenant permits for this provider, e.g. `["claude-sonnet-4-6"]` (added 105) |

---

## them.llm_provider_keys ⭐
Multiple named API keys per `(llm_provider_id, tenant_id)` — added 105 because `llm_providers` can only
hold one key per provider per tenant. A tenant can save several keys under one provider (e.g.
"Key_for_april", "Key_for_QA") and mark one as the default used by "general settings" consumers
(classifier, card_synthesizer, security_scanner, and future LLM-consuming features). Usage/cost per
key is *not* stored here — it's computed from `them.run_usage` grouped by `llm_provider_key_id`, to
avoid double bookkeeping. Same admin-pool / application-level tenant-isolation posture as
`them.llm_providers` (see above); RLS present for defense-in-depth only.

Migration 108 made `tenant_id` nullable — `NULL` = platform-owned key (the-M's own named keys,
mirroring `them.llm_providers.tenant_id`'s existing NULL-means-platform convention), non-NULL =
tenant-owned. Replaced the single `UNIQUE(llm_provider_id, tenant_id, name)` constraint and single
partial default-index with four indexes split by NULL/non-NULL:
`llm_provider_keys_name_platform_uq` (name WHERE tenant_id IS NULL),
`llm_provider_keys_name_tenant_uq` (name, tenant_id WHERE tenant_id IS NOT NULL),
`llm_provider_keys_default_platform_uq` (WHERE tenant_id IS NULL AND is_default),
`llm_provider_keys_default_tenant_uq` (tenant_id WHERE tenant_id IS NOT NULL AND is_default). RLS
policy unaffected — `tenant_id = <GUC>` never matches NULL, so platform rows were already correctly
invisible to `them_app` without any policy change.

| Column | Type | Purpose |
|---|---|---|
| id | BIGSERIAL PK | referenced by `them.run_usage.llm_provider_key_id` |
| llm_provider_id | INTEGER FK→them.llm_providers(id) ON DELETE CASCADE | which provider this key belongs to |
| tenant_id | UUID FK→them.tenants(id) ON DELETE CASCADE, NULLABLE | NULL = platform-owned (added 108); non-NULL = owning tenant |
| name | TEXT | user-chosen label, e.g. `"Key_for_april"` — unique per (provider, tenant), separately unique per (provider) among platform-owned rows |
| api_key_encrypted | TEXT | `enc:` Fernet ciphertext |
| is_default | BOOL | the key used by "general settings" mode for this provider+tenant (or provider+platform); at most one true per (provider, tenant) or per (provider) among platform rows |
| last_tested_at | TIMESTAMPTZ | set by the Test button's real probe call |
| last_test_ok | BOOL | result of the last test |

---

## them.config
Key→JSONB config store. Key rows: `llm_routing`, `system_agents` (platform-global classifier/
card_synthesizer/security_scanner config — see `them.tenant_system_agent_config` below for the
per-tenant override). Each role in `system_agents.roles` now also carries `mode` ("" or "custom" =
today's provider/model/api_key_encrypted/base_url/system_prompt fields; "general" = `provider` +
`key_id` resolved against the-M's own platform-owned `them.llm_provider_keys` rows, mirroring the
per-tenant general/custom pattern — see `docs/TENANT_LLM_PROVIDERS_PLAN.md`'s "the-M admin gets the
same General/Custom parity" section).

| Column | Type | Purpose |
|---|---|---|
| config_key | TEXT PK | e.g. `"llm_routing"` |
| config_value | JSONB | e.g. `{"provider":"anthropic","model":"claude-sonnet-4-6"}` |

---

## them.tenant_system_agent_config (migration 106)
Per-tenant mode (general/custom) for system-agent roles (`classifier`, `card_synthesizer`). Added
because `them.config` has no `tenant_id` column at all — the platform-global `system_agents` row
above is shared by every tenant. This table lets a tenant opt each role into either:
- `mode='general'` — resolve through the tenant's own `them.llm_providers` +
  `them.llm_provider_keys` config (the same screen from step 4), via `provider_name` + `key_id`
  (`key_id` NULL = that provider's default key).
- `mode='custom'` — its own standalone provider/model/key/base_url/system_prompt, same shape as
  the platform-global row.

No row (or `mode='custom'` with unset custom fields) falls back to the platform-global
`them.config['system_agents']` row — unchanged prior behavior. **Hard rule:** `mode='general'`
with no usable tenant key never falls back to a platform key — resolution simply fails and the
caller degrades exactly like "role disabled" (see `resolveSystemAgentRole`,
`go/internal/admin/system_agent_resolve.go`).

Same admin-pool / application-level tenant-isolation posture as `them.llm_providers` /
`them.llm_provider_keys` (RLS present for defense-in-depth only).

| Column | Type | Purpose |
|---|---|---|
| tenant_id | UUID FK→them.tenants(id) ON DELETE CASCADE | part of PK |
| role | TEXT | `"classifier"` or `"card_synthesizer"` — part of PK |
| mode | TEXT CHECK IN ('general','custom') | default `'custom'` |
| provider_name | TEXT | `mode='general'` — which of the tenant's `them.llm_providers` rows |
| key_id | BIGINT FK→them.llm_provider_keys(id) ON DELETE SET NULL | `mode='general'`; NULL = use that provider's default key |
| custom_provider | TEXT | `mode='custom'` |
| custom_model | TEXT | `mode='custom'` |
| custom_api_key_encrypted | TEXT | `mode='custom'` — `enc:` Fernet ciphertext |
| custom_base_url | TEXT | `mode='custom'` |
| custom_system_prompt | TEXT | `mode='custom'` — empty = caller's own built-in default prompt |

Admin routes: `GET`/`PUT /admin/my/system-agents/{role}/config` (tenant self-service only, mirrors
the `/admin/my/llm-providers` naming pattern — `go/internal/admin/tenant_system_agent_config.go`).

---

## them.appflow_debug_presets (migration 111)
Per-(tenant, user, application) saved presets for the AppFlow debug panel
(`go/internal/admin/appflow_debug.go`'s `/debug/start` body shape) — lets a user save the entry
point, test message, step mode, and per-node LLM overrides they typed into the debug panel and
reload them next time instead of re-entering everything. Presets are personal: scoped to
`(tenant_id, user_id, application_id)`, never visible to other users even within the same
tenant/app (deliberately not folded into the tenant Role model — see
`docs/UNIFIED_ROLE_GOVERNANCE_DESIGN.md`).

`llm_overrides` is a JSONB map keyed by canvas `node_id`, mirroring `debugStartBody.LLMOverrides`
plus one change: any Custom-mode `api_key` is Fernet-encrypted (`api_key_encrypted`, same scheme as
`them.tenant_system_agent_config.custom_api_key_encrypted`) before storage — the plaintext key is
never written to this table, and the service layer only ever returns a masked hint, never the
decrypted value, on read.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| tenant_id | UUID FK→them.tenants(id) ON DELETE CASCADE | |
| user_id | BIGINT | the-M internal user id (not a FK — mirrors `them.audit_logs`' pattern) |
| application_id | UUID FK→them.applications(id) ON DELETE CASCADE | |
| name | TEXT | unique per (tenant_id, user_id, application_id, name) |
| entry_point_slug | TEXT | |
| user_message | TEXT | |
| step_mode | BOOLEAN | |
| llm_overrides | JSONB | keyed by node_id; `api_key` never stored — see `api_key_encrypted` above |

Admin routes (tenant-scoped, under `/admin/applications/{id}/debug/presets`):
`GET`/`POST /debug/presets`, `DELETE /debug/presets/{preset_id}` — all scoped to the caller's own
`user_id` (`go/internal/admin/appflow_debug_presets.go`).

---

## them.agents ⭐
The agent registry. Each enabled row = one LLM tool named `agent__<slug>`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| slug | TEXT UNIQUE | `^[a-z0-9_]{1,48}$` — used in tool name |
| display_name | TEXT | UI label |
| description | TEXT | **LLM tool description** — critical for routing decisions |
| transport | TEXT | `"a2a_async"` (external A2A agent) or `"canvas_a2a"` (canvas-generated agent — spec served from `agent_runtime_specs`) |
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
| **a2a_exposed** | BOOL | **deprecated** — use `delegatable` on `app_orchestrators`; kept for backward compat (Phase 12 drops) |
| **delegatable** | BOOL | allow this orchestrator to be used as a sub-orchestrator tool (`orch__<name>`) — backfilled from `a2a_exposed` |
| **budget_tokens** | INT | NULL = no token budget; if set, workflow aborts when tokens_used_so_far exceeds this |

---

## them.app_orchestrators ⭐
Per-application orchestrator instances. Each row is one orchestrator node on the canvas, owned by one application. Runtime key = `name` (globally unique slug). Resolved by `loaders.load_orchestrator_row()` before `them.orchestrators` fallback.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| application_id | UUID FK→applications CASCADE | owning app |
| orchestrator_id | UUID FK→orchestrators SET NULL | seed template (nullable) |
| name | TEXT UNIQUE `^[a-z0-9_-]{1,64}$` | **runtime key** — Redis cache `them:orchestrators:{name}`, Temporal routing, A2A skill id |
| node_id | TEXT | canvas node identity (for save reconciliation) |
| kind | TEXT | `standard`, `router`, `voice` |
| delegatable | BOOL | allow use as sub-orchestrator tool (`orch__<name>`) |
| display_name | TEXT | UI label (does not affect routing) |
| system_prompt | TEXT | LLM system prompt |
| allowed_agent_ids | UUID[] | agents this orchestrator can invoke |
| llm_provider / llm_model / llm_api_key_encrypted / llm_base_url | TEXT | same semantics as `them.orchestrators` |
| max_iterations | INT | agentic loop bound (default 10) |
| max_parallel_tools | INT | concurrent agent calls (default 3) |
| rate_limit_rpm | INT | per-user rate limit |
| daily_budget_usd | NUMERIC | 0 = unlimited |
| voice_enabled / transcription_provider / transcription_model / transcription_api_key_encrypted | — | voice/STT config |
| tts_enabled / tts_provider / tts_voice / tts_api_key_encrypted | — | TTS config |
| memory_enabled / summarize_every_n_calls / memory_raw_fallback_n / summarizer_* | — | context summarization config |
| history_window | INT | max prior turns (default 20) |
| budget_tokens | INT | NULL = no token budget |
| enabled | BOOL | |
| created_at / updated_at | TIMESTAMPTZ | |

**Name immutability:** `name` is the Temporal workflow key, Redis cache key, A2A skill id, and `orch__<name>` delegation slug. Never change it after creation — doing so orphans in-flight workflows, stale Redis cache entries, and A2A skill references.

**Resolution order in `load_orchestrator_row(name)`:**
1. Redis `them:orchestrators:{name}` (TTL 600s) → proxy object
2. `them.app_orchestrators WHERE name = name AND enabled = true`
3. `them.orchestrators WHERE name = name AND enabled = true` (legacy fallback)

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
| tenant_id | UUID NOT NULL | owning tenant (migration R-4a) |
| is_backend | BOOL DEFAULT false | when true, caller may assert X-External-User; never set on mobile/browser tokens (migration 084) |

---

## them.runs ⭐
One row per orchestrator invocation (user goal → final answer).

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Temporal workflow run_id |
| orchestrator_id | UUID FK | |
| orchestrator_name | TEXT | denormalized for fast queries |
| user_id | INT | internal THE-M user ID: set from the JWT `sub` claim on AccessModeUser (user_jwt) EPs. NULL for service-token or external-user runs. Enables per-user history isolation for dashboard users. (migration 086) |
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
| external_user_id | TEXT | end-user identity: set from X-External-User (is_backend=true) or JWKS JWT sub. NULL for internal/service-token runs. (migration 085) |

---

## them.run_steps
One row per agent (tool) invocation within an orchestrator-mode run, OR one row
per DAG node execution within an AppFlow (Graph-mode) run (migration 103 —
App Canvas Debug Mode Phase 3, `docs/APP_CANVAS_DEBUG_PLAN.md`). The two shapes
never mix within one run: orchestrator-mode rows populate `agent_slug`/
`iteration`; AppFlow rows populate `node_id`/`node_kind` instead and use
`iteration=0` as a not-applicable sentinel. Kept for backward compatibility;
new orchestrator-mode runs also create `them.tasks` child rows.

For an AppFlow node, the row is inserted (`status='running'`) when the node's
`node_start` trace event fires, then updated in place — not a second row — to
`completed`/`failed` when its `node_done`/`node_error` event fires. Keyed by a
partial unique index on `(run_id, node_id) WHERE node_id IS NOT NULL`, so this
never constrains orchestrator-mode rows (`node_id` always NULL there, and
`run_id` legitimately repeats across many of them).

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| run_id | UUID FK→runs | parent run |
| agent_id | UUID FK→agents ON DELETE SET NULL | NULL if agent deleted; orchestrator-mode only, never populated today |
| iteration | INT, default 0 | orchestrator-mode: which loop iteration (1-indexed). AppFlow-mode: always 0 (not applicable) |
| agent_slug | TEXT | orchestrator-mode only — denormalized, survives agent deletion |
| node_id | TEXT, nullable | AppFlow-mode only — the canvas node's ID (migration 103) |
| node_kind | TEXT, nullable | AppFlow-mode only — `router`/`hil`/`agent`/`llm`/`condition`/`fork`/`join` (migration 103) |
| input | JSONB | orchestrator-mode: tool input arguments. Unused by AppFlow-mode today |
| output | TEXT | orchestrator-mode: agent response text. AppFlow-mode: short kind-specific detail (e.g. `branch=true`), never a full prompt/response |
| status | TEXT | `pending/running/completed/failed/timeout` |
| error | TEXT | failure detail (both modes) |
| latency_ms | INT | orchestrator-mode: adapter round-trip time. AppFlow-mode: node_start → node_done/node_error wall time |
| started_at | TIMESTAMPTZ | |
| ended_at | TIMESTAMPTZ, nullable | |

`tool_call_id` (formerly `TEXT NOT NULL`) was dropped by migration 103 — it was
never populated by the only writer (`internal/runrecorder.RecordAgentStep`),
so every insert should have been failing the NOT NULL constraint against a
real Postgres instance. Not caught earlier because the unit test mocked the
DB. No reader depended on it for anything beyond a always-empty display field.

---

## them.run_usage
Per-LLM-call token and cost tracking. (Corrected 2026-09-23 — previous version of this section did not
match `db/001_schema.sql`.)

| Column | Type | Purpose |
|---|---|---|
| id | BIGSERIAL PK | |
| run_id | UUID FK→them.runs(id) ON DELETE CASCADE | |
| user_id | INTEGER | |
| provider | TEXT | e.g. `"anthropic"` — free text, no FK |
| model | TEXT | e.g. `"claude-haiku-4-5-20251001"` |
| tokens_input | INTEGER | |
| tokens_output | INTEGER | |
| cost_usd | NUMERIC(12,8) | |
| created_at | TIMESTAMPTZ | |
| llm_provider_key_id | BIGINT FK→them.llm_provider_keys(id) ON DELETE SET NULL | which named key backed this call, nullable (added 105) |

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
| user_id | INT FK→auth_service.users | task owner: set from JWT sub on AccessModeUser EPs (migration 086). Dual-column filter with external_user_id ensures cross-identity isolation: external rows have user_id=NULL, internal rows have external_user_id=NULL. Legacy rows with both NULL are excluded from user-scoped queries. |
| tenant_id | UUID | owning tenant for history isolation |
| external_user_id | TEXT | end-user identity scoping this task's history. NULL = internal or service-token run. (migration 084) |
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
| entry_point_type | TEXT | `"websocket"` / `"sse"` / `"webrtc"` / `"voice"` / `"a2a"` |
| orchestrator_id | UUID FK→orchestrators ON DELETE CASCADE | target orchestrator |
| access_policy | JSONB | `{"mode":"token"}` or `{"mode":"public"}` |
| presentation | JSONB | UI metadata (title, theme, icon, etc.) |
| agent_card | JSONB | synthesized A2A agent card (populated by `POST .../entry-points/{ep_id}/discover`) |
| card_synthesized_at | TIMESTAMPTZ | timestamp of last card synthesis |
| enabled | BOOL | |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

---

## them.agent_definitions (Phase 2 Canvas A2A Agent Builder)
Design-time table for canvas-authored agent definitions. Separate from the runtime registry `them.agents`. Migration: `db/035_agent_definitions.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Auto-generated |
| tenant_id | UUID NOT NULL | Tenant scoping |
| agent_slug | TEXT NOT NULL | kebab-case agent identifier |
| revision | INTEGER NOT NULL | Version within (tenant_id, agent_slug) |
| definition | JSONB NOT NULL | AgentDefinitionDoc canvas JSON — slot NAMES only, never values |
| definition_hash | TEXT NOT NULL | sha256 of canonical JSON |
| status | TEXT | 'draft' or 'published' — Phase 2 only writes 'draft' |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

Unique constraint: `(tenant_id, agent_slug, revision)`.
Indexes: `agent_definitions_tenant_slug (tenant_id, agent_slug)`, `agent_definitions_tenant_status (tenant_id, status)`.

---

## them.agent_runtime_specs (Phase 3 Canvas A2A — compiled spec)
Compiled AgentSpec produced from `agent_definitions` at publish time. One row per definition revision. Migration: `db/036_canvas_a2a_runtime.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Auto-generated |
| tenant_id | UUID NOT NULL | Tenant scoping |
| definition_id | UUID NOT NULL FK→agent_definitions | Source definition |
| agent_id | UUID NOT NULL FK→agents | Runtime agent row (same UUID as definition_id) |
| spec | JSONB NOT NULL | Compiled AgentSpec — slot NAMES only, never credential values |
| spec_hash | TEXT NOT NULL | sha256 of spec JSON |
| deployed_at | TIMESTAMPTZ | When this spec became active |
| created_at | TIMESTAMPTZ | |

Unique constraint: `(definition_id)` — one compiled spec per definition revision.

---

## them.app_agent_bindings (Phase 3 Canvas A2A — per-app credential bindings)
Per-application binding of a canvas agent with encrypted credentials. Migration: `db/036_canvas_a2a_runtime.sql`.

**Security**: `credential_bindings` stores AES-256-GCM ciphertext (base64url). Responses return `{slot_name: bool}` only — never ciphertext or plaintext.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Auto-generated |
| application_id | UUID NOT NULL FK→applications | Application that owns this binding |
| agent_id | UUID NOT NULL FK→agents | Canvas agent |
| definition_id | UUID FK→agent_definitions | Pinned definition revision (nullable during drafting) |
| credential_bindings | JSONB NOT NULL | AES-256-GCM ciphertext per credential slot — NEVER plaintext |
| agent_params | JSONB NOT NULL DEFAULT '{}' | App-level runtime params. Secrets: `{key: {ct: "enc:...", hint: "XXXX"}}` (Fernet). Non-secrets: `{key: "value"}`. Never returned as plaintext. Migration: `db/038_app_agent_params.sql`. |
| config_overrides | JSONB NOT NULL | Per-app configuration overrides |
| policies | JSONB NOT NULL | Invocation policy overrides |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

Unique constraint: `(application_id, agent_id)`.

---

## them.mcp_servers
MCP (Model Context Protocol) server registry. Tenant-scoped. Health and manifest fields are owned by `them-mcp-service` — admin CRUD owns name/slug/transport/url/auth_type/enabled only. Migration: `db/041_mcp_servers.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| tenant_id | UUID NOT NULL | Tenant isolation boundary |
| name | TEXT NOT NULL | Human display name |
| slug | TEXT NOT NULL | Unique per-tenant identifier |
| description | TEXT | Optional human description |
| transport | TEXT NOT NULL DEFAULT 'http' | `'http'` \| `'sse'` \| `'stdio'` |
| url | TEXT | Server endpoint URL |
| auth_type | TEXT NOT NULL DEFAULT 'none' | `'none'` \| `'bearer'` \| `'header'` \| `'oauth2'` |
| health_status | TEXT NOT NULL DEFAULT 'unknown' | `'unknown'` \| `'healthy'` \| `'degraded'` \| `'unreachable'` — written by `them-mcp-service` only |
| last_checked_at | TIMESTAMPTZ | Timestamp of last health probe — written by `them-mcp-service` only |
| last_error | TEXT | Last probe error message — written by `them-mcp-service` only |
| tools_manifest | JSONB NOT NULL DEFAULT '[]' | Tool list from last successful discovery — written by `them-mcp-service` only |
| capabilities | JSONB NOT NULL DEFAULT '{}' | Server capability block — written by `them-mcp-service` only |
| enabled | BOOL NOT NULL DEFAULT true | Admin toggle |

Unique constraint: `(tenant_id, slug)`.

---

## them.app_mcp_credentials
Per-application encrypted credentials for MCP servers. One row per (application, mcp_server) pair. Credential value is Fernet-encrypted using the same scheme as `llm_providers.api_key_encrypted`. Migration: `db/042_mcp_app_credentials.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| application_id | UUID FK→them.applications | ON DELETE CASCADE |
| mcp_server_id | UUID FK→them.mcp_servers | ON DELETE CASCADE |
| credential_encrypted | TEXT | `enc:` Fernet ciphertext — never returned to clients |
| auth_header_name | TEXT NOT NULL DEFAULT 'Authorization' | HTTP header for credential injection |

Unique constraint: `(application_id, mcp_server_id)`.
**Security:** `GET` credential endpoints return `credential_set: bool` only — never the decrypted value.

---

## them.hil_approvals (migration 098)
Human-in-the-loop approval requests written by AppFlowWorkflow before it pauses.
One row per HIL gate per run. The Temporal workflow waits for a `hil_approval:<nodeID>` signal.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| tenant_id | UUID NOT NULL | tenant owning this run |
| application_id | UUID NOT NULL | application being executed |
| run_id | UUID NOT NULL | parent run |
| node_id | TEXT NOT NULL | canvas node ID of the HIL gate |
| approver_role | TEXT NOT NULL | minimum RBAC role required to approve (default: 'admin') |
| prompt | TEXT | message shown to the approver |
| fallback_action | TEXT NOT NULL | 'reject'/'approve'/'abort' on timeout |
| status | TEXT NOT NULL | 'pending'/'approved'/'rejected'/'timed_out' |
| decided_at | TIMESTAMPTZ | when status was set to non-pending |
| comment | TEXT | optional approver comment |

---

## them.run_artifacts (Phase R-3 binary files)
Stores binary file artifacts produced by the Go orchestrator/worker. Source of truth: `db/025_run_artifacts.sql` + `db/050_middleware_pipeline.sql`. Contains the file bytes directly (`data BYTEA NOT NULL`).

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| run_id | UUID NOT NULL | parent run |
| application_id | UUID | parent application (nullable) |
| session_id | UUID | parent session (nullable) |
| tenant_id | UUID NOT NULL | tenant (from R-4a) |
| filename | TEXT NOT NULL | sanitized filename |
| content_type | TEXT NOT NULL | MIME type |
| size | BIGINT NOT NULL | byte size |
| data | BYTEA NOT NULL | file bytes — never log |
| scan_status | TEXT | `'disabled'`/`'pending'`/`'scanning'`/`'clean'`/`'infected'`/`'flagged'`/`'error'`/`'failed'` (from `db/050_middleware_pipeline.sql`) |
| scan_result | JSONB | per-processor results from the middleware pipeline |
| scanned_at | TIMESTAMPTZ | timestamp of last scan completion |
| created_at | TIMESTAMPTZ | |

**scan_status lifecycle:** `disabled` (scanning not enabled) → `pending` (job enqueued) → `scanning` (worker claimed) → `clean` (OK) | `infected` (blocked) | `flagged` | `error` (scanner unavailable) | `failed` (max retries exceeded)

---

## them.app_flow_llm_overrides (migration 101 — Node Registry Phase 1)
Runtime-screen provider+model override for one inline LLM node on an app canvas.
Mirrors `app_agent_bindings.config_overrides.llm_nodes` for the agent builder, but stored in its
own table because these nodes live directly on the app canvas (no `agent_id`). Stored outside the
published definition so changing a model needs no re-publish. `Lifecycle.StartAppFlow` applies
these via `appflow.ApplyLLMOverrides` before the workflow starts (fail-open: DB error → no
overrides applied, canvas-compiled value used).

| Column | Type | Purpose |
|---|---|---|
| application_id | UUID NOT NULL, FK → applications(id) ON DELETE CASCADE | |
| node_id | TEXT NOT NULL | canvas instance_id of the inline LLM node |
| provider | TEXT NOT NULL | override provider slug |
| model | TEXT NOT NULL | override model identifier |
| updated_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

PRIMARY KEY (application_id, node_id). No RLS (same as `app_temporal_config`) — isolation is via
the `application_id` FK and TenantTx-scoped admin routes, not a row policy on this table.

---

## them.app_debug_config (migration 104 — App Canvas Debug Mode Phase 4)

Per-app AppFlow trace log-verbosity setting. Controls how much `persistTrace`
(`internal/appflow/activities.go`) writes to `them.run_steps` for Graph-mode runs. No row = default
`'status'`. `Lifecycle.StartAppFlow` resolves this once per run into
`AppFlowWorkflowInput.LogVerbosity`, fail-open to the default on load error; a debug-mode run always
uses `'full'` regardless of this setting.

| Column | Type | Purpose |
|---|---|---|
| application_id | UUID NOT NULL, FK → applications(id) ON DELETE CASCADE | |
| log_verbosity | TEXT NOT NULL DEFAULT 'status', CHECK IN ('off','status','full') | see semantics below |
| updated_at | TIMESTAMPTZ NOT NULL DEFAULT now() | |

PRIMARY KEY (application_id). No RLS (same as `app_temporal_config`) — isolation is via the
`application_id` FK and TenantTx-scoped admin routes.

**Verbosity semantics** (enforced in `persistTrace`, not in the DB):
- `off` — no `them.run_steps` write at all. The run's live Redis stream (`them:dash:run:{run_id}:stream`)
  still publishes `node_start`/`node_done`/`node_error` while a viewer is connected; only durable
  persistence is skipped. The Run History "Flow" tab stays empty for these runs.
- `status` — the row is written/updated (`node_id`, `node_kind`, `status`, `latency_ms`), but
  `output`/`error` detail is never persisted (`NULL`), whether the node succeeded or failed.
- `full` — today's Phase 3 behavior: status plus `output`/`error` detail.

Admin routes: `GET`/`PUT /admin/applications/{id}/log-verbosity` (`internal/admin/log_verbosity.go`).
Frontend: Runtime → Trace Logging tab (`RuntimeLogVerbosityTab.tsx`).

---

## them.middleware_defs

Registry of middleware component types (builtin and tenant-scoped). Migration: `db/001_schema.sql`; visual columns added by `db/097_middleware_defs_visual.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| slug | TEXT UNIQUE NOT NULL | e.g. `file-guard` |
| kind | TEXT NOT NULL | `guard` or `cache` |
| display_name | TEXT NOT NULL | |
| description | TEXT | |
| config | JSONB | default config schema |
| is_builtin | BOOLEAN | true for system-provided defs |
| scope | TEXT | `builtin` or `tenant` |
| enabled | BOOLEAN | |
| emoji | TEXT | visual emoji for canvas node (e.g. `🛡️`) — added by `db/097` |
| color | TEXT | accent hex color for canvas node (e.g. `#f59e0b`) — added by `db/097` |
| bg_color | TEXT | background color for canvas node (e.g. `rgba(245,158,11,0.08)`) — added by `db/097` |
| tenant_id | UUID FK→auth_service.users | NULL for builtins |
| created_at / updated_at | TIMESTAMPTZ | |

---

## them.middleware_jobs (Phase 3 middleware pipeline)
Job queue for artifact processing pipeline. Workers claim rows using `SELECT FOR UPDATE SKIP LOCKED`. Migration: `db/050_middleware_pipeline.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| artifact_id | UUID FK→run_artifacts | ON DELETE CASCADE |
| application_id | UUID FK→applications | ON DELETE CASCADE |
| run_id | UUID | parent run |
| session_id | UUID | parent session |
| processors | TEXT[] | ordered processor names (e.g. `['av_scan']`) |
| status | TEXT | `'pending'`/`'claimed'`/`'done'`/`'failed'` |
| attempt_count | INT | incremented on failure |
| max_attempts | INT DEFAULT 3 | |
| claimed_at | TIMESTAMPTZ | |
| retry_after | TIMESTAMPTZ | for backoff |
| result | JSONB | final pipeline result |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

---

## them.middleware_audit (Phase 3 middleware pipeline)
Per-processor per-artifact audit trail. Migration: `db/050_middleware_pipeline.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | |
| artifact_id | UUID FK→run_artifacts | ON DELETE CASCADE |
| application_id | UUID | |
| session_id | UUID | |
| run_id | UUID | |
| processor | TEXT | processor name (e.g. `'av_scan'`) |
| outcome | TEXT | `'clean'`/`'infected'`/`'flagged'`/`'skipped'`/`'error'` |
| detail | JSONB | processor-specific details (e.g. threat name) |
| duration_ms | INT | processing time |
| created_at | TIMESTAMPTZ | |

---

## them.applications — security_config column (Phase 3)
Added by `db/050_middleware_pipeline.sql`:

| Column | Type | Purpose |
|---|---|---|
| security_config | JSONB NOT NULL DEFAULT '{}' | per-app middleware pipeline config (see `SecurityConfig` struct in `go/internal/middleware/config.go`) |

Example:
```json
{
  "enabled": true,
  "processors": {
    "av_scan": {"enabled": true, "max_file_mb": 5, "block_on_infected": true}
  }
}
```

---

## them.tenants (Multi-tenancy Phase 2)
Tenant registry. Migration: `db/053_tenants.sql` + `db/058_tenant_email_domain.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Stable tenant identifier |
| slug | TEXT UNIQUE NOT NULL | URL-safe name (e.g. `"acme"`) |
| display_name | TEXT NOT NULL | Human-readable label |
| enabled | BOOL NOT NULL DEFAULT true | Soft disable without deletion |
| idp_config | JSONB | OIDC IdP config (null = no SSO). Keys: `issuer`, `client_id`, `client_secret` |
| email_domain | TEXT UNIQUE (partial) | Domain for auto-routing (e.g. `"acme.com"`); null = disabled. Partial UNIQUE INDEX WHERE NOT NULL. Always stored lowercase. |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

Bootstrap tenant: `id = 00000000-0000-0000-0000-000000000001`, slug = `"platform"`.

**Partial unique index:** `tenants_email_domain_uq ON tenants (email_domain) WHERE email_domain IS NOT NULL`  
Multiple rows with `email_domain = NULL` are allowed; at most one row per non-null domain value.

---

## them.tenant_group_mappings (Multi-tenancy Phase 3 — Step 18)
OIDC group claim → tenant role mapping. Migration: `db/059_tenant_group_mappings.sql`.

| Column | Type | Purpose |
|---|---|---|
| id | UUID PK | Stable mapping identifier |
| tenant_id | UUID FK→them.tenants(id) ON DELETE CASCADE | Owning tenant |
| group_claim | TEXT NOT NULL | Exact value from the OIDC `groups` claim (e.g. `"OktaAdmins"`, `"EntraID-Admin"`) |
| role | TEXT NOT NULL CHECK (admin\|member\|viewer) | Tenant role assigned when this group matches. `super_admin` is explicitly forbidden at DB and app layers (migration 081). |
| priority | INT NOT NULL DEFAULT 0 | Lower integer = higher priority. Ties broken by group_claim ASC. |
| created_at | TIMESTAMPTZ | |
| updated_at | TIMESTAMPTZ | |

**UNIQUE:** `(tenant_id, group_claim)` — one role per group per tenant.  
**Index:** `tenant_group_mappings_tenant_id_idx ON tenant_group_mappings (tenant_id)`

The OIDC callback resolves the role by querying `WHERE tenant_id = $1 AND group_claim = ANY($2) ORDER BY priority ASC, group_claim ASC LIMIT 1`. Non-fatal: no match → default "viewer" role used.

**Security:** `super_admin` is blocked at three layers: DB CHECK (`admin|member|viewer` only, migration 081), `validMemberRoles` guard in `UpsertOIDCUser`, and explicit rejection in `OIDCCallback` before `UpsertOIDCUser` is called. Platform role for all OIDC users is always `"viewer"` regardless of group mapping — group mappings only affect tenant membership role.

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
| `db/035_agent_definitions.sql` | `them.agent_definitions` table (canvas agent design-time store) |
| `db/036_canvas_a2a_runtime.sql` | `them.agent_runtime_specs` + `them.app_agent_bindings` tables |
| `db/037_agents_transport_canvas.sql` | Extend `agents_transport_check` to include `'canvas_a2a'` transport |
| `db/041_mcp_servers.sql` | `them.mcp_servers` table (MCP-1 registry) |
| `db/042_mcp_app_credentials.sql` | `them.app_mcp_credentials` table (per-app encrypted credentials) |
| `db/050_middleware_pipeline.sql` | `run_artifacts.scan_status/scan_result/scanned_at`; `them.middleware_jobs`; `them.middleware_audit`; `applications.security_config` |
| `db/053_tenants.sql` | `them.tenants` table (multi-tenancy Phase 2 foundation) |
| `db/054_tenant_members.sql` | `them.tenant_members` table |
| `db/055_tenant_llm_provider_keys.sql` | Per-tenant LLM provider key overrides |
| `db/056_tenant_rbac.sql` | Per-tenant RBAC (roles, grants) |
| `db/057_llm_providers_tenant.sql` | `llm_providers.tenant_id` FK (per-tenant overrides) |
| `db/058_tenant_email_domain.sql` | `tenants.email_domain` — nullable; partial UNIQUE INDEX WHERE NOT NULL |
| `db/059_tenant_group_mappings.sql` | `them.tenant_group_mappings` — OIDC group claim → tenant role mapping (Step 18) |
| `db/070_rls_roles.sql` | DB roles: `them_owner` (table owner, NOBYPASSRLS), `them_admin` (BYPASSRLS), `them_app` (app pool). Grants for all `them.*` tables. |
| `db/071_rls_phase_b.sql` | RLS Phase B: enable RLS on `mcp_servers`, `tenant_group_mappings`, `agent_definitions`, `agent_runtime_specs` |
| `db/072_rls_phase_c.sql` | RLS Phase C: enable RLS on `tenants`, `tenant_members`, `tenant_rbac_roles`, `tenant_rbac_grants`, `mcp_server_tools`, `app_mcp_credentials` |
| `db/073_rls_phase_d.sql` | RLS Phase D: enable RLS on `agents`, `orchestrators`, `applications`, `entry_points`, `access_tokens`, `agent_runtime_specs` |
| `db/074_tasks_tenant_backfill.sql` | E0: backfill `tasks.tenant_id` from orchestrators, set NOT NULL constraint |
| `db/075_rls_phase_e.sql` | RLS Phase E: enable RLS on `runs`, `tasks`, `run_artifacts` (direct tenant_id policies) |
| `db/076_rls_phase_f.sql` | RLS Phase F: enable RLS on `run_steps`, `run_usage`, `artifacts`, `task_messages`, `middleware_audit` (EXISTS-based via parent) |
| `db/077_rls_phase_g.sql` | RLS Phase G: enable RLS on `llm_providers` (split policy: own+NULL for SELECT), `middleware_jobs` (EXISTS via applications), `audit_logs` |
| `db/078_rls_phase_h2.sql` | RLS Phase H2: FORCE RLS on 4 remaining tables — `application_definitions`, `managed_app_bindings`, `quarantine_artifacts` (direct tenant_id), `component_definitions` (split: SELECT own+NULL, DML revoked from `them_app`). All 28 them-schema tables now have RLS. |
| `db/079_component_definitions_grant.sql` | Restore `GRANT INSERT, DELETE ON them.component_definitions TO them_app` — 078 over-revoked (Agent Create CTE + Delete run via TenantTx). Apply 078+079 together. |
| `db/080_grant_tenant_quotas_to_app.sql` | `GRANT SELECT ON them.tenant_quotas TO them_app` — required by `checkResourceQuota` in Create paths (quota check runs in TenantTx). |
| `db/081_tenant_group_mappings_safe_roles.sql` | ADD CHECK on `them.tenant_group_mappings.role` restricting values to `('admin','member','viewer')` — closes OIDC privilege escalation via `super_admin` group mappings. |
| `db/086_phase2_user_history.sql` | Phase 2 user history isolation: `them.tasks.user_id INT` + `them.runs.user_id INT`. Dual-column SQL filter (external_user_id / user_id) isolates internal vs external identity. Legacy NULL rows excluded from user-scoped queries by SQL NULL semantics. |
| `db/087_end_user_role.sql` | Seed `auth_service.roles` with `end_user` role (`dashboard_access='none'`, rate_limit=1000, cost_limit_daily=$10, token_expiry=3600s). Runtime-only access — no dashboard permissions. |
| `db/088_allowed_principals.sql` | Phase 3 principal guard: `them.entry_points.allowed_principals TEXT NOT NULL DEFAULT 'internal' CHECK (IN ('internal','external','both'))`. Controls which principal types (internal token/user_jwt, external backend+X-External-User, or both) may call each EP. Default 'internal' is safe for all existing EPs. |
| `db/097_middleware_defs_visual.sql` | Phase 1 middleware node registry: adds `emoji TEXT`, `color TEXT`, `bg_color TEXT` to `them.middleware_defs`. Seeds File Guard with `🛡️` / `#f59e0b` / `rgba(245,158,11,0.08)`. |
| `db/101_app_flow_llm_overrides.sql` | Node Registry Phase 1: `them.app_flow_llm_overrides` table — per-app, per-node provider+model override for app-canvas inline LLM nodes, applied at workflow start ahead of the canvas-compiled value. |
| `db/103_run_steps_appflow_trace.sql` | App Canvas Debug Mode Phase 3: `them.run_steps` gains `node_id`/`node_kind` (nullable) for AppFlow DAG node traces + a partial unique index on `(run_id, node_id) WHERE node_id IS NOT NULL`. Also drops the broken `tool_call_id TEXT NOT NULL` column (never populated by its writer — every insert should have been failing). |

---

## Row-Level Security (Step 19)

All `them.*` tables are protected by Postgres RLS. Three DB roles are in use:

| Role | BYPASSRLS | Purpose |
|---|---|---|
| `them_owner` | No (NOBYPASSRLS) | Table owner — DDL only, not used at runtime |
| `them_admin` | Yes | Admin pool for long-lived workers (recorder, reconciler, middleware, history store). Bypasses RLS; these callers embed explicit `tenant_id` in every INSERT. |
| `them_app` | No | App pool for per-request tenant context. GUC `app.tenant_id` set by `BeginTenantTx`. |

**GUC pattern:** `set_config('app.tenant_id', $1, true)` in `BeginTenantTx`. Policies read via `NULLIF(current_setting('app.tenant_id', true), '')::uuid`.

### RLS status per table

| Table | RLS | Policy type | Notes |
|---|---|---|---|
| `tenants` | ✅ | Direct `tenant_id` | Only own tenant visible |
| `tenant_members` | ✅ | Direct `tenant_id` | |
| `tenant_rbac_roles` | ✅ | Direct `tenant_id` | |
| `tenant_rbac_grants` | ✅ | EXISTS via `tenant_rbac_roles` | |
| `mcp_servers` | ✅ | Direct `tenant_id` | |
| `mcp_server_tools` | ✅ | EXISTS via `mcp_servers` | |
| `app_mcp_credentials` | ✅ | EXISTS via `applications` | |
| `tenant_group_mappings` | ✅ | Direct `tenant_id` | |
| `agent_definitions` | ✅ | Direct `tenant_id` | |
| `agent_runtime_specs` | ✅ | Direct `tenant_id` | |
| `agents` | ✅ | Direct `tenant_id` | |
| `orchestrators` | ✅ | Direct `tenant_id` | |
| `applications` | ✅ | Direct `tenant_id` | |
| `entry_points` | ✅ | EXISTS via `applications` | |
| `access_tokens` | ✅ | Direct `tenant_id` | |
| `runs` | ✅ | Direct `tenant_id` | Admin pool used by recorder |
| `tasks` | ✅ | Direct `tenant_id` | `tenant_id` NOT NULL after migration 074 |
| `run_artifacts` | ✅ | Direct `tenant_id` | |
| `run_steps` | ✅ | EXISTS via `runs` | |
| `run_usage` | ✅ | EXISTS via `runs` | |
| `artifacts` | ✅ | EXISTS via `tasks` | |
| `task_messages` | ✅ | EXISTS via `tasks` | |
| `middleware_audit` | ✅ | EXISTS via `applications` | INSERT-only for `them_app`; reads via admin pool only |
| `llm_providers` | ✅ | Split: own OR NULL for SELECT; own-only for write | NULL = platform defaults, always readable by all tenants |
| `middleware_jobs` | ✅ | EXISTS via `applications` | Worker uses admin pool (cross-tenant by design) |
| `audit_logs` | ✅ | Direct `tenant_id` | INSERT-only for `them_app` |
| `hil_approvals` | ✅ | Direct `tenant_id` | Written by AppFlow HIL activity; pending rows read by approval API |
| `config` | ❌ | No RLS — global config, not tenant-scoped | |
| `schema_migrations` | ❌ | No RLS — DDL tracking table | |
