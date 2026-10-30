---
title: App Export / Import & Deploy-to-Tenant
type: architecture
status: current
owns:
  - go/internal/admin/dal/app_deploy.go
  - go/internal/admin/dal/app_export.go
  - go/internal/admin/applications.go
last_verified: 2026-10-30   # code read + admin API additions; required_role added to entry_points travel list
---

# App Export / Import & Deploy-to-Tenant

## Purpose
Move an application between tenants/environments or back it up, **without secrets**. The project's hard
rule (root `CLAUDE.md`, `docs/plans/app-canvas-config-completeness-plan.md`): everything configurable in the
App Canvas must have a working path through export/import and deploy, and the result must be
functionally identical. This doc records what actually travels today.

## Three mechanisms
| Mechanism | Where | What it moves | Who |
|---|---|---|---|
| **Canvas JSON** download/upload | frontend only (per the plan doc; frontend not reviewed here) | `AppDefinitionDoc` (components, entry points, connections) | any canvas user |
| **Deploy to Tenant** (DB→DB clone) | `POST /admin/applications/{id}/deploy` → `dal/app_deploy.go` | rows of one app into another tenant, in one Admin-pool transaction | super admin |
| **Export file / Import file** | `GET …/{id}/export`, `POST …/import` (platform-global, super admin, any tenant) and `GET …/{id}/export-own`, `POST …/import-own` (tenant admin, own tenant) → `dal/app_export.go` | the same data as a versioned JSON envelope (`export_version = 1`) | super admin / tenant admin |

## How it works

### What travels (Deploy and Export/Import share the same registry)
- **application**: `name, enabled, runtime_config, canvas, app_params` (secret entries — objects with a `ct` key — are stripped; scalars kept); export/import also `conversation_token_limit`, `security_config`. `provider_keys` is never copied. New slug = old slug (or sanitized name) + random 6-hex suffix.
- **active definition** (`application_definitions` row, its JSON as-is) and `active_definition_id` re-linked.
- **app_orchestrators**: prompts, limits, memory, voice, `edges`, `mcp_servers`, `allowed_agent_ids` (remapped to new agent IDs); all `*_api_key_encrypted` set NULL.
- **entry_points**: `slug, type, enabled, memory/summarizer/history, llm_provider/model, allowed_principals, required_role`, link to the new orchestrator by `node_id`.
- **agents** referenced by orchestrators or bindings: `component_definitions` + `agents`; canvas agents also `agent_definitions` + `agent_runtime_specs`. Reuse by `(kind, namespace, name, version)` in the target tenant; **content-hash mismatch aborts** (`ErrDeployConflict`, HTTP 409). Agent auth tokens/keys are not copied.
- **Scoped config** — one registry, `appScopedConfigTables` in `app_deploy.go` (adding a table = one entry; export has a matching `exportSQL` and a `switch` case in `exportScopedConfig`): `middleware_wirings` (Guards; export carries `def_slug`), `app_agent_bindings` (without `credential_bindings`), `app_mcp_credentials` (binding by server slug, **no credential**), `app_flow_llm_overrides`, `app_temporal_config`, `app_debug_config`, `app_flow_http_params` (without `value_encrypted`).
- Deploy returns a checklist (LLM keys required, MCP server names, agents copied/reused/conflict). Import returns only the application.

### Flows
- **Deploy** (`DeployApplication` handler): `BeginAdminTx` → `CopyAgentsForDeploy` (conflict check first, then writes) → one CTE inserting application/definition/orchestrators/entry points → remap `allowed_agent_ids` → `copyAppScopedConfigForDeploy` → commit. Atomic.
- **Export**: `to_jsonb` selects per table into `ExportedApp{application, application_definition, entry_points, app_orchestrators, scoped_config, agents[]}`; secrets columns omitted or NULLed.
- **Import**: reject unknown `export_version`; `importAgents` (same reuse/conflict rule) → insert application → definition → orchestrators → entry points → `importScopedConfig`. Super-admin import runs in an Admin transaction; **tenant-scoped import does not** (see gaps).

## Key invariants
- Secrets never travel: provider keys, agent/MCP/HTTP credentials, orchestrator API keys, app-param secrets.
- Agent identity across tenants is by portable ref `(kind, namespace, name, version)`, never by UUID; `agent_id` columns are remapped through `agentIDMap`.
- A different agent with the same ref but different `content_hash` in the target aborts the whole operation.
- New app-scoped tables must be added to `appScopedConfigTables` (+ export case) or they silently do not travel.

## Failure modes
| What fails | Effect |
|---|---|
| Agent content conflict | 409, nothing written (deploy: transaction; import: see gaps) |
| Source canvas agent never published | Deploy aborts ("agent must be published before deploy") |
| MCP server slug missing in target | Binding silently skipped on import |
| Required platform guard def missing in target | Import fails with a message naming the def slug |
| Unsupported `export_version` | Import rejected |

## Known gaps (found while writing, 2026-10-24; code + live catalog; nothing executed)
See `docs/plans/code-findings-backlog.md` F-78…F-86. Highlights:
1. **Tenant-scoped export is cross-tenant.** `export-own` calls `ExportApplication(appID)` from the URL on `legacyDAL`, which is built on the **Admin (BYPASSRLS) pool** (`NewPgxQuerier(rlsPools.Admin)`, despite the comment saying `them_app`), and the SQL has no tenant filter. Any tenant admin who knows another tenant's app UUID can download its canvas, prompts, agent definitions/specs and wiring.
2. **Agent-param secrets are copied.** `app_agent_bindings.agent_params` holds secret-type params as `{ct, hint}` ciphertext; deploy copies them verbatim and the export file contains them — the same leak class as the already-fixed `app_params` bug. On a shared platform key the target tenant silently runs with the source tenant's secret.
3. **Not functionally identical after deploy/import (violates the hard rule):** entry points lose `access_policy` (a `public`/`user_jwt`/`external_jwt` EP comes back as default `token`), `max_concurrent_sessions`, `queue_*`, `conversation_token_limit`, `required_role`, `agent_card`; orchestrators lose their component pin; deploy drops `security_config`/`conversation_token_limit`; `presentation` is never copied. Re-publishing the definition re-projects most of this, but nothing tells the user to.
4. **Stale agent IDs inside the copied definition.** The published definition JSON carries `_resolved_agent_ids` (source-tenant agent UUIDs); it is never remapped, so AppFlow agent nodes in the new app cannot resolve their agents until it is re-published.
5. **Tenant-scoped import is not transactional** — a failure midway leaves a partial application.
6. **Import resolves MCP servers by slug with no tenant filter** (can bind to another tenant's server) and silently skips missing ones; no checklist is returned for import.
7. **Import trusts the file:** agent `endpoint_url`, spec, definition and `content_hash` are inserted as given (only `export_version` is validated).
8. Coverage gaps vs. the rule: `appflow_debug_presets` and app role grants (`tenant_role_grants`) have an `application_id` but are not in the registry.

## Where to change things
| To… | Edit |
|---|---|
| Make a new app-scoped table travel | `appScopedConfigTables` (`copySQL` + `exportSQL`), `ExportedScopedConfig` field + `exportScopedConfig` case, `importScopedConfig` loop, tests in `go/TEST_INDEX.md` |
| Change what an entry point/orchestrator copies | CTE in `DeployApplication`, `epQ`/`orchQ` in `ExportApplication`, insert in `ImportApplication` — keep all three in sync |
| Change authorization | `admin/router.go` (platform-global vs tenant-scoped groups), handlers in `admin/applications.go` |

Tests: `go test ./internal/admin/...` (+ integration tests in `internal/admin/dal/*integration_test.go`).

## Related
- `app-canvas-appflow.md` — publish projection and `_resolved_agent_ids`
- `multi-tenancy-rls.md` — Admin pool vs RLS; `auth-and-roles.md`
- `docs/plans/app-canvas-config-completeness-plan.md` — the rule and phase plan (legacy doc)
