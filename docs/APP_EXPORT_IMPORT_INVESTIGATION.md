# Full Application Export/Import — Investigation

Last updated: 2026-09-28. Research only — no code changes made. Scope: full 1:1 export/import
of a single `them.applications` row and everything that constitutes its configured behavior,
excluding secrets.

---

## 1. Inventory — tables with `application_id` (or equivalent app-scoping FK)

Source: `grep -rl application_id db/*.sql` (excluding `.claude/worktrees/*` copies) +
`go/internal/admin/dal/`. No table uses `entry_point_id` as a foreign key anywhere in the
codebase — `entry_points` is always reached via `application_id`.

### Configured behavior — MUST be exported

| Table | File | Represents | Export notes |
|---|---|---|---|
| `them.applications` | `db/001_schema.sql:250`, `db/048_application_slug.sql`, `db/032_app_provider_keys.sql`, `db/045_app_global_params.sql`, `db/055_managed_apps.sql`, `db/023_app_runtime.sql`, `db/017_canvas_layout.sql`, `db/050_middleware_pipeline.sql` | The app row itself: `name`, `slug`, `enabled`, `runtime_config`, `app_params` (secrets embedded, see §5), `security_config`, `canvas` (ReactFlow layout JSON), `active_definition_id`, `provider_keys` (JSONB, legacy/secret-bearing — see §5), `app_type`/`version`/`changelog` | Core row. `canvas` (visual node positions) is currently NOT copied by `DeployApplication` — confirmed gap, see §4. |
| `them.application_definitions` | `db/029_component_registry_foundation.sql:42` | Published/draft app-definition JSON (schema_version 2 shape) + revision history | Only the **active** definition is copied by `DeployApplication` (see §4) — draft/older revisions are not. Decide if export needs full revision history or just the active one. |
| `them.entry_points` | `db/001_schema.sql:264`, `db/010_app_entrypoints.sql`, `db/021_voice_ep.sql`, `db/024_ep_queue.sql`, `db/028_entry_points_tenant_scoped_slug.sql`, `db/032_ep_memory_config.sql`, `db/047_ep_llm.sql`, `db/049_ep_agent_card.sql`, `db/098_hil_approvals.sql` | One row per WS/SSE/WebRTC/A2A/voice door: slug, `access_policy`, memory/summarizer config, `allowed_principals`, `app_orchestrator_id` FK | Exported/copied today by `DeployApplication`. `access_policy` JSONB needs checking for embedded token/secret material (see §5). |
| `them.app_orchestrators` | `db/014_app_orchestrators.sql`, `db/027`, `db/044_mcp_orchestrator.sql`, `db/098_hil_approvals.sql` | One row per orchestrator node on canvas: system prompt, `allowed_agent_ids`, LLM/voice/TTS/memory config, `mcp_servers` JSONB | Exported/copied today. Contains several `*_api_key_encrypted` columns — secrets, excluded (see §5). `mcp_servers` JSONB references MCP servers **by slug**, not ID — no rewrite needed, but target env must have matching slugs. |
| `them.middleware_wirings` | `db/001_schema.sql:360`, `db/013_agentic_middleware.sql`, `db/114_middleware_wirings_agent_optional.sql` | Guard/cache/etc. wiring per node (File Guard, PII Guard, Prompt-Injection Guard): `def_id`, `agent_id` (nullable), `node_id`, `config_override`, `position`, `enabled` | **Confirmed gap — NOT copied by `DeployApplication` at all.** Must be part of any real export. |
| `them.app_agent_bindings` | `db/036_canvas_a2a_runtime.sql:28` | Per-app credential/config overrides for an agent used in this specific app: `credential_bindings` (secrets, Fernet), `config_overrides`, `policies` | `config_overrides`/`policies` are configured behavior; `credential_bindings` must be excluded (§5). **Not copied by `DeployApplication`.** |
| `them.app_mcp_credentials` | `db/042_mcp_app_credentials.sql` | Per-app MCP server credential binding (which MCP servers this app uses + encrypted creds) | The *binding* (which MCP servers, `auth_header_name`) is configured behavior; `credential_encrypted` must be excluded. **`DeployApplication` only reads this to build a post-deploy checklist (`ListAppMCPCredentials`, `applications.go:828`) — it does not create rows in the target.** |
| `them.app_flow_llm_overrides` | `db/101_app_flow_llm_overrides.sql` | Per-canvas-node (`node_id`) provider/model override for inline `llm` nodes, set in the Runtime tab outside the published definition | Configured behavior (which model an `llm` node actually runs), keyed by the canvas-JSON `node_id` (not a DB UUID — no rewrite needed). **Not copied by `DeployApplication`.** |
| `them.app_temporal_config` | `db/099_temporal_config.sql` | Per-app Temporal execution overrides (concurrency, timeouts, retries) | Configured behavior. **Not copied by `DeployApplication`.** |
| `them.app_debug_config` | `db/104_app_log_verbosity.sql` | Per-app debug trace log-verbosity setting | Minor but is configured behavior. **Not copied.** |
| `them.tenant_role_grants` | `db/001_schema.sql:389` (part of migration 095) | Which tenant roles grant access to this application | Arguably app-adjacent, but it's really a *role* config that references the app, not app config that references a role. Cross-cutting — see §2. |

### Runtime / historical data — must NOT be exported

| Table | Represents |
|---|---|
| `them.runs`, `them.run_steps`, `them.run_usage` | Run history — per-invocation data, entirely transient/historical |
| `them.run_artifacts`, `them.quarantine_artifacts` | Uploaded/generated files tied to specific runs |
| `them.middleware_jobs`, `them.middleware_audit` | Async guard-scan job queue + audit trail for specific artifacts |
| `them.tasks`, `them.task_messages`, `them.artifacts` | Task-runner execution state |
| `them.appflow_debug_presets` | Personal, per-user debug-panel presets — not app behavior, explicitly user-scoped (`tenant_id, user_id, application_id`) |
| `them.audit_logs` | Audit trail |
| Anything under `them.hil_approvals` (db/098) keyed to a specific run | Human-in-the-loop approval instances, not config |

### Ambiguous / needs a decision

| Table | Question |
|---|---|
| `them.app_agent_bindings.policies` | Is a "policy" (e.g. per-app rate limit override for an agent) config or could it encode something environment-specific that shouldn't travel? Needs a content review before committing to "always inline." |
| `them.applications.provider_keys` (JSONB, `db/032_app_provider_keys.sql`) | Legacy column — check whether it's dead (superseded by `llm_provider_keys` table) or still read anywhere; if dead, exclude entirely and flag for cleanup rather than exporting. |

---

## 2. Cross-Cutting / Shared References

An app's canvas or config can point at things that are NOT app-scoped — they're tenant-scoped or
platform-scoped and potentially shared by other apps in the same tenant.

| Referenced thing | Table | How it's referenced from an app | Recommendation |
|---|---|---|---|
| **Agents** | `them.agents` (+ `them.component_definitions`, `them.agent_definitions`, `them.agent_runtime_specs` for canvas agents) | `app_orchestrators.allowed_agent_ids[]` (UUID array), canvas JSON `definition_ref.kind:"agent"` by `(name, namespace)` | **(a) Inline a full copy.** This is exactly what `CopyAgentsForDeploy` already does (see §4) — agents are copied into the target tenant if not already present (matched by `kind+namespace+name+version`), reused if already there with matching `content_hash`, and conflict-flagged if a same-name agent exists with different content. This precedent should be reused, not reinvented. |
| **MCP servers** | `them.mcp_servers` | `app_orchestrators.mcp_servers` JSONB `[{slug, tools[]}]`, `app_mcp_credentials.mcp_server_id` | **(b) Reference by slug, expect it to exist.** `mcp_servers` JSONB already stores slugs, not IDs — no rewrite needed for that column. But `app_mcp_credentials.mcp_server_id` is a real FK UUID that WOULD need remapping to the target environment's matching-slug MCP server row, or the row must be dropped from export entirely (it's excluded anyway per §5, credentials). Net: export should emit the MCP **slugs** used (already surfaced today as the deploy checklist's `MCPServers []string`, `applications.go:738`) and let the importer resolve/require them in the target tenant — do not try to clone `mcp_servers` rows themselves (they're platform/tenant registrations, often environment-specific URLs). |
| **LLM providers / keys** | `them.llm_providers`, `them.llm_provider_keys` | `app_orchestrators.llm_provider`/`llm_model` (TEXT, provider slug — not an FK), `app_flow_llm_overrides.provider`/`model` (TEXT) | **(b) Reference by name, expect it to exist.** These are stored as plain provider-name strings, never as FK UUIDs into `llm_providers`. No rewrite needed. But the target tenant must have that provider enabled with a usable key — this is exactly the existing `deployChecklist.LLMKeysRequired` flag (`applications.go:737`), already surfaced by `DeployApplication` today. |
| **Orchestrator templates** | `them.orchestrators` | `app_orchestrators.orchestrator_id` (nullable FK, "seed template") | **(c) Open question.** `DeployApplication`'s CTE (`app_deploy.go:339`) copies `ao.orchestrator_id` through unchanged — i.e. the cloned `app_orchestrators` row still points at the **same** `them.orchestrators` template row, which may not exist (or may mean something different) in the target tenant/environment, since `them.orchestrators` rows are not tenant-scoped the same way. This looks like an existing latent bug in `DeployApplication` itself, not just an export/import gap — worth flagging separately. |
| **Component definitions (middleware defs)** | `them.middleware_defs` / `them.component_definitions` | `middleware_wirings.def_id` | **(b) Reference by slug, expect it to exist.** These are `is_builtin=true` platform rows (File Guard, PII Guard, Prompt-Injection Guard, cache, etc.) — effectively fixed platform infrastructure, present in every environment via migrations. Export should resolve `def_id` → slug and re-resolve slug → `def_id` on import, not inline a copy of the guard definition itself. |
| **Tenant** | `them.tenants` | `applications.tenant_id` | Implicit in "moving between tenants" — the whole point of export/import is to let the target tenant differ from the source. Not a gap, just the defining parameter of the operation. |

---

## 3. ID Rewrite Problem

Every table above uses `gen_random_uuid()` PKs. On import, every UUID must be regenerated and
every FK to it rewritten. The actual depth of the chain, confirmed from code:

**Needs rewriting (real DB UUID FKs):**
- `applications.id` → new UUID (root)
- `application_definitions.application_id` → new app ID
- `applications.active_definition_id` → new definition ID
- `entry_points.application_id` → new app ID
- `entry_points.app_orchestrator_id` → new orchestrator ID
- `app_orchestrators.application_id` → new app ID
- `app_orchestrators.allowed_agent_ids[]` → **array of UUIDs**, each must be remapped through the agent old→new ID map (confirmed: `applications.go:397-417`, `DeployApplication` already does exactly this remap pass after the CTE)
- `middleware_wirings.application_id` → new app ID
- `middleware_wirings.agent_id` → new agent ID (nullable; only rewrite when non-NULL — an `llm`-node wiring has no agent at all, confirmed `db/114_middleware_wirings_agent_optional.sql`)
- `middleware_wirings.def_id` → **not rewritten as ID** — should be resolved by slug (see §2), since these are fixed platform rows expected to exist identically in every environment
- `app_agent_bindings.application_id`, `app_agent_bindings.agent_id`, `app_agent_bindings.definition_id` → all need remapping through the same maps
- `app_mcp_credentials.application_id`, `app_mcp_credentials.mcp_server_id` → app ID remap + MCP-server resolve-by-slug in target env (credential itself excluded)
- `agent_definitions.id`, `agent_runtime_specs.definition_id`/`agent_id` → already handled inside `CopyAgentsForDeploy`'s `copyCanvasAgentDeps` (`app_deploy.go:205-227`)

**Does NOT need rewriting — confirmed:**
- `middleware_wirings.node_id` — this is the canvas-JSON `instance_id` string (e.g. `"llm_1"`), the same node-identity space used throughout the app definition JSON (`definitions.go`/`compiler.go`, confirmed in `.claude/skills/app-canvas.md` §2 and `go/internal/admin/dal/middleware_wirings.go:119-157`'s `uq_mw_wiring_app_node` constraint). It is independent of any DB UUID and is stable across export/import as long as the app definition JSON's node `instance_id`s are preserved verbatim. **This is the one part of the "deep rewrite chain" that turns out to be shallow** — node_id travels as plain text, no map needed.
- `app_flow_llm_overrides.node_id` — same canvas `instance_id` space, same non-UUID stability.
- `app_orchestrators.node_id` — same; used for save-reconciliation against canvas JSON, not a DB FK.
- Provider/model/MCP-slug string fields (§2) — plain text, resolved by name at runtime, not FKs.

**Net assessment:** the rewrite chain is two levels deep in practice — `application_id` and
`agent_id` (plus the derived `agent_definition_id`/`agent_runtime_spec_id`/`definition_id` for
canvas agents) are the only UUID families that actually need an old→new map propagated across
inserts. Everything else is either resolved by human-readable slug/name (MCP servers, LLM
providers, middleware defs) or is already string-stable (`node_id`). This is a two-map problem
(`appIDMap` single entry + `agentIDMap`), not an N-deep graph walk.

---

## 4. Existing Precedent

**`go/internal/admin/dal/app_deploy.go`** already implements cross-tenant application cloning
with ID remapping — this is the closest existing precedent and should be extended, not
reinvented.

- `CopyAgentsForDeploy(ctx, sourceAppID, targetTenantID)` (`app_deploy.go:35`) — copies every
  agent referenced by the source app's `app_orchestrators.allowed_agent_ids` into the target
  tenant. Matches existing target-tenant agents by `(kind, namespace, name, version)` and reuses
  them if `content_hash` matches; aborts the whole deploy with `ErrDeployConflict` if a
  same-identity agent exists with **different** content (`app_deploy.go:196-199`). For
  `canvas_a2a` agents, also copies `agent_definitions` and `agent_runtime_specs`
  (`copyCanvasAgentDeps`, `app_deploy.go:205-227`). Explicitly does NOT copy
  `auth_token_encrypted` or any `*_api_key_encrypted` column (see doc comment, `app_deploy.go:30-31`).
  Returns an `IDMap` (old→new agent UUID).
- `DeployApplication(ctx, sourceAppID, targetTenantID, agentIDMap)` (`app_deploy.go:291`) — one
  big CTE that clones, in order: `applications` (new slug gets a random 6-char suffix appended to
  avoid collision, `app_deploy.go:303`), `application_definitions` (**active definition only**),
  `app_orchestrators` (name also gets a collision-avoiding suffix), `entry_points`. Then a
  second pass remaps `allowed_agent_ids[]` using `agentIDMap` (`app_deploy.go:397-417`).
- HTTP entry point: `POST /api/v1/admin/applications/{id}/deploy` → `ApplicationsHandler.DeployApplication`
  (`go/internal/admin/applications.go:749`), `RequireSuperAdmin`-gated, registered in
  `go/internal/admin/router.go:266`. Runs inside a single Admin-pool (BYPASSRLS) transaction —
  `CopyAgentsForDeploy` + `DeployApplication` + commit, with rollback on any failure.
  Tested: `go/internal/admin/deploy_application_test.go`.

**Confirmed gaps in this existing feature** (i.e. `DeployApplication` today is NOT a complete
export/import — it's a narrower "deploy to another tenant" feature that predates the guard/MCP/
Temporal-config additions):
- `middleware_wirings` — not copied at all. A deployed app silently loses all File Guard / PII
  Guard / Prompt-Injection Guard configuration. This matches the gap already confirmed earlier
  this session.
- `app_agent_bindings` — not copied (config_overrides/policies lost, though credentials
  correctly wouldn't travel anyway).
- `app_mcp_credentials` — only read to build a checklist (`applications.go:828`), never inserted
  into the target. The *binding* (which MCP servers this app expects) is lost, not just the
  credential.
- `app_flow_llm_overrides` — not copied. Inline `llm` node provider/model overrides set in the
  Runtime tab are lost; nodes would fall back to whatever default applies in the target.
- `app_temporal_config`, `app_debug_config` — not copied (lower-impact, but still silently lost).
- `applications.canvas` (visual layout JSONB) — not copied; the app would still *function*
  identically but the canvas would re-layout from scratch, which may or may not matter depending
  on how strictly "1:1" is interpreted.
- `app_orchestrators.orchestrator_id` (seed-template FK) copied verbatim without remap — a
  latent correctness issue in the existing feature, independent of export/import scope, worth a
  separate ticket.
- Only the **active** `application_definitions` revision is copied — draft/historical revisions
  are lost. For a "backup/restore" use case (as opposed to "promote to prod"), this may not be
  acceptable.

---

## 5. Secrets/Credentials Exclusion List

Every place a credential/secret is reachable from an app, confirmed by column-name grep across
the schema files:

| Table.column | What it holds | Confirmed excluded by existing code? |
|---|---|---|
| `them.agents.auth_token_encrypted` | Bearer token sent to an A2A agent endpoint | Yes — explicitly excluded, `app_deploy.go:30` doc comment + not in `insertAgent` param list for the token itself (endpoint_url is copied, token is not) |
| `them.app_orchestrators.llm_api_key_encrypted` | Per-orchestrator LLM key override | Yes — `DeployApplication` CTE passes `NULL` explicitly (`app_deploy.go:343`) |
| `them.app_orchestrators.transcription_api_key_encrypted` | STT key override | Yes — `NULL` explicitly (`app_deploy.go:344`) |
| `them.app_orchestrators.tts_api_key_encrypted` | TTS key override | Yes — `NULL` explicitly (`app_deploy.go:345`) |
| `them.app_orchestrators.summarizer_api_key_encrypted` | Summarizer LLM key override | Yes — `NULL` explicitly (`app_deploy.go:347`) |
| `them.app_agent_bindings.credential_bindings` | Fernet ciphertext, per-credential-slot values for an agent used in this app | **Not addressed** — table isn't copied at all today; when built, must exclude this JSONB column specifically while keeping `config_overrides`/`policies` |
| `them.app_mcp_credentials.credential_encrypted` | Fernet ciphertext MCP server credential | **Not addressed** — table isn't copied; must exclude this column, keep `auth_header_name` + the binding's existence |
| `them.llm_providers.api_key_encrypted` | Legacy/platform-row LLM key | Not app-scoped (tenant/platform-scoped) — out of an app-export's direct responsibility, but note if any per-app snapshot ever inlines provider config, this must be dropped |
| `them.llm_provider_keys.api_key_encrypted` | Named per-tenant LLM key | Same as above — tenant-scoped, not app-scoped; excluded by virtue of apps referencing providers by name only (§2) |
| `them.mcp_servers.probe_credential_encrypted` | Platform-level MCP health-probe credential | Tenant-scoped registry row, not app-scoped — excluded by virtue of apps referencing MCP servers by slug only |
| `them.applications.provider_keys` (JSONB) | Legacy column, `db/032_app_provider_keys.sql` | Ambiguous — verify whether any secret material still lands here before deciding; `DeployApplication` currently hardcodes it to `'{}'::jsonb` on the cloned row (`app_deploy.go:301`), which is actually the safe default already |
| `them.applications.app_params` (JSONB) | Per-app named parameters; secrets stored as `{"name": {"ct": "enc:...", "hint": "..."}}`, non-secrets as `{"name": "value"}` (confirmed comment, `db/045_app_global_params.sql:15-16`) | **Not addressed** — `DeployApplication` copies `app_params` through unchanged (`app_deploy.go:294` SELECT, `app_deploy.go:305` INSERT) — **this looks like it currently leaks encrypted secret ciphertext into the target tenant**, since the shape distinguishes secret vs non-secret entries within the same JSONB blob. Any export/import (and arguably the existing deploy feature right now) must filter this JSONB to drop every key whose value has a `"ct"` field, keeping only plain non-secret entries. |
| `them.appflow_debug_presets.llm_overrides[*].api_key_encrypted` | Saved debug-panel per-node LLM key | N/A — this table is user-scoped debug state, excluded entirely from export regardless (§1) |

**New finding worth flagging on its own:** `them.applications.app_params` is copied verbatim by
the existing `DeployApplication`, and per its own documented shape it can contain Fernet-encrypted
secret values inline. This looks like a real secret leak in the *existing* deploy feature, not
just a future export/import gap — worth a decision on whether to fix now or track as a
pre-existing bug to fix alongside export/import.

---

## 6. Recommendation

**Recommended approach: extend the existing `DeployApplication`/`CopyAgentsForDeploy` DB-CTE
pattern (option (b)-adjacent, not a JSON envelope, not pg_dump) rather than building a new JSON
export format from scratch.**

Rationale:
- The hard part of export/import — cross-tenant ID remapping for `application_id` and
  `agent_id`/canvas-agent dependents — is **already built, tested, and live** in
  `app_deploy.go`/`applications.go:749`. A JSON-envelope approach (option a) would have to
  re-solve the exact same remap problem in a different shape (marshal → unmarshal → remap → 
  insert) for no benefit, since the DB is the source of truth either way.
- A pg_dump-style export (option b, literal) is the wrong tool here: `application_id` scoping
  cuts across many tables with different, non-uniform relationships (some 1:1, some 1:N, some
  array-of-UUID like `allowed_agent_ids`), and secret columns must be scrubbed at the row level
  before any bytes leave the source — pg_dump doesn't have a natural "redact this column" mode
  scoped to one tenant's one app.
- The real gap is **coverage, not architecture**: `DeployApplication`'s CTE needs to grow to also
  clone `middleware_wirings`, `app_agent_bindings` (minus `credential_bindings`),
  `app_mcp_credentials` (as a binding-only row, minus `credential_encrypted`),
  `app_flow_llm_overrides`, `app_temporal_config`, `app_debug_config`, and `applications.canvas`.
  All of these hang off `application_id` alone (no additional ID-map needed beyond the existing
  `appIDMap`/`agentIDMap` pair) — this is additive work on a proven pattern, not a redesign.
- Today's endpoint conflates "deploy to another tenant" with "export/import" — for the stated use
  cases (move between teams/tenants, backup/restore) this is actually fine: "export" = call
  `DeployApplication` targeting a snapshot/backup tenant or export it as the returned JSON;
  "import" = the same call targeting the real destination tenant. A thin wrapper that serializes
  the *result* of a (extended) `DeployApplication` call to a portable JSON file, and a
  corresponding "restore from JSON" path that replays the same insert logic from a file instead
  of a live source app, gets you true backup/restore without inventing a second code path.

**Does this need a schema refactor (stable logical ID)?** No — not required to build this
correctly. The rewrite chain is shallow (§3): exactly two ID families
(`application_id`, `agent_id`+dependents) need remapping, and everything else is already either
slug/name-resolved (MCP servers, LLM providers, middleware defs) or string-stable
(`node_id` = canvas `instance_id`, never a DB UUID). Introducing a "logical ID" layer would mean
migrating every one of the ~10 app-scoped tables to carry a second stable identifier column,
which is a much larger and riskier change than the two-map remap this problem actually needs.
**Flag, don't block:** if a future requirement emerges for *live* cross-environment references
(e.g. two apps in different tenants staying in sync, not just point-in-time export/import), that
would be a different and harder problem justifying a logical-ID refactor — but it is not required
for the stated use cases (one-time move, backup/restore).

**Two things that need a human decision before implementation starts** (beyond the "biggest
open question" flagged in the summary):
1. Should export capture only the **active** `application_definitions` revision (matches
   current `DeployApplication` behavior) or the full revision history (needed for a strict
   backup/restore reading of "100% of the app")?
2. Should `them.applications.app_params`'s existing secret-leak-on-deploy behavior be fixed as
   part of this work (filter out `{"ct": ...}` entries) or tracked as a separate pre-existing bug
   ticket? It's in scope either way since export/import must not repeat it.
