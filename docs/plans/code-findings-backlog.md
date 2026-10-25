---
title: Code Findings Backlog (from the docs reboot)
type: plan
status: current
last_verified: 2026-10-24
---

# Code Findings Backlog

Bugs, risks and inefficient code found while writing docs from the code. **Not fixed yet** — we fix them after the docs reboot is complete. All found by reading code, none reproduced live.

**Rule:** every session that finds a new bug/risk/inefficiency adds a row here (same commit). When one is fixed, set Status to `fixed (<commit>)` and update the owning architecture doc. Never delete rows.

Severity: **H** security / data loss / silent wrong result · **M** wrong behaviour · **L** cleanup.

| ID | Sev | Area | Finding | Where | Status |
|---|---|---|---|---|---|
| F-01 | H | Canvas app | PII / prompt-injection / file checks do not run inside fork branches | `appflow/graph.go` `walkBranch` | open |
| F-02 | H | Canvas app | A failed fork branch is ignored; run can finish `completed` | `appflow/workflow.go` fork case | open |
| F-03 | M | Canvas app | Fork branches only support agent/llm/condition/orchestrator/middleware/join; other nodes fail (and F-02 hides it) | `appflow/graph.go` | open |
| F-04 | M | Canvas app | Agent-only flows may send no answer over WS/SSE (`done.final_text` dropped; only LLM nodes emit `token`) | `ws/handler.go`, `sse/handler.go`, `appflow/activities.go` | open |
| F-05 | M | Canvas app | `node_*` events with output detail go to end-user WS/SSE clients, even outside debug — confirm intended | `appflow/activities.go` `emitTrace` | open |
| F-06 | L | Canvas app | `Validate` has no checks for http nodes, router labels vs edges, unknown kinds | `appflow/validate.go` | open |
| F-07 | L | Canvas app | HIL rejection stored as `failed`; rejection comment lost | `appflow/activities.go` Finalize | open |
| F-08 | H | Run lifecycle | Reconciler never matches runs (looks up run ID, real workflow IDs differ); also dry-run by default | `reconciler/reconciler.go` | open |
| F-09 | M | Run lifecycle | `session.Store.Touch` never called; gate/session keys expire after 90s, caps undercount long sessions | `session/session.go`, `cmd/them/main.go` | open |
| F-10 | M | Run lifecycle | Failures before `orchestrator.Run` send no `error` event and leave run `running` | `temporal/activities.go` | open |
| F-11 | M | Run lifecycle | Bridge cannot start without Temporal (nil client panics `NewLifecycle`); log says "inline path" | `cmd/them/main.go` | open |
| F-12 | M | Run lifecycle | WS drops `file` + `canceled/terminated/timed_out`; SSE drops the 3 terminal types; only WS supports follow-up messages | `ws/handler.go`, `sse/handler.go` | open |
| F-13 | L | Run lifecycle | Per-user daily cost ignores gateway spend (`GetUserDailySpendFromGateway` unused) | `execution/lifecycle.go` | open |
| F-14 | L | Docs/code | Stale comments (Python-era, in-process bus, "agents rejected", `local` = goroutine loop) | ws, sse, appflow, publish | open |
| F-15 | L | Docs | `REDIS.md` has Python-era keys, lacks `them:events`, `them:events:dlq`, `them:ep:gate:queue:{slug}` | `docs/_archive/REDIS.md` | fixed (generated reference `docs/reference/redis-generated.md`, old file archived) |
| F-16 | L | Config | Root `CLAUDE.md` has Python-era rules and a wrong container map; says 2 dag workers (compose has 4 + debug) | `CLAUDE.md` | open |
| F-17 | L | Ops | `version:` still in `docker-compose.yml`; many stray status/result files in repo root | repo root | open |
| F-18 | H | Auth | Logout only blacklists inside `authserver`; bridge/admin/user-mgmt/`user_jwt` EPs never check the blacklist, so a logged-out token works until expiry | `auth/jwt.go`, `auth/middleware.go`, `authserver/service.go` | open |
| F-19 | H | Auth | SSO upsert `ON CONFLICT (email) DO UPDATE … active=true` has no `password_hash IS NULL` guard: SSO login can take over/re-activate an existing (local or disabled) account; no `email_verified` check | `authserver/oidc_store.go` | open |
| F-20 | M | Auth | ID token not checked for `iss`, `aud`, `exp`, `nonce` | `authserver/oidc_jwks.go` | open |
| F-21 | M | Auth | No brute-force/rate limit on `/login` and `/runtime-login` | `authserver/handlers.go` | open |
| F-22 | M | Auth | Auth cookies have no `Secure` flag | `authserver/handlers.go`, `oidc.go` | open |
| F-23 | M | Auth | Refresh token not rotated or revoked on use (7d replay); refresh skips the `dashboard_access` check | `authserver/service.go` | open |
| F-24 | M | Auth | Login without `tenant_slug` uses `LIMIT 1` with no `ORDER BY` (random tenant for multi-tenant users) | `authserver/pgx.go` | open |
| F-25 | M | Auth | `external_jwt`: `SubClaim` config ignored; empty `Audience` skips aud check | `auth/external_jwt.go` | open |
| F-26 | M | Auth | Role gate and `AppHasGrants` DB errors fail open (app treated as open) | `roles/roles.go` | open |
| F-27 | L | Auth | HS256 validation accepts tokens with no `type`; unparsable `sub` becomes user 0 silently | `auth/jwt.go` | open |
| F-28 | L | Auth | `Cache.Revoke` has no production caller (admin tokens service re-implements it inline) | `auth/token_cache.go`, `admin/service/tokens.go` | open |
| F-29 | L | Auth | `JWTMiddleware` returns `err.Error()` to client; `RuntimeLogin` and `Login` duplicate logic | `auth/middleware.go`, `authserver/service.go` | open |
| F-30 | L | Docs | Legacy `AUTH.md` contradicted code (refresh claims, bootstrap tenant fallback) | `docs/_archive/AUTH.md` | fixed (archived in docs reboot #11; replaced by architecture/auth-and-roles.md) |
| F-31 | H | Tenancy | Gate/session Redis keys use only the EP slug (`them:ep:{slug}:*`, queue key): tenants with same EP slug share one cap and queue | `gate/gate.go`, `session/session.go` | open |
| F-32 | H | Tenancy | Superuser `them` is the DB user for auth-go, mcp-service, middleware-worker, agent-runtime and the bridge's legacy pool (bypasses RLS/FORCE) | compose env, `cmd/*/main.go` | open |
| F-33 | M | Tenancy | Only ~8 admin handlers use `BeginTenantTx`; ~36 Admin-pool (BYPASSRLS) sites rely on hand-written `WHERE tenant_id` | `internal/admin`, workers | open |
| F-34 | M | Tenancy | No RLS on `hil_approvals` (has tenant_id), `app_debug_config`, `app_flow_llm_overrides`, `app_temporal_config`, `gateway_profile_steps`, while `them_app` has DML; `tenants`/`tenant_quotas` readable by `them_app` | `db/` | open |
| F-35 | M | DB | `db/001_schema.sql` is missing many live tables (gateway_*, governance_policies, tenant_runtime_config, hil_approvals, …); fresh install per README does not match live schema | `db/001_schema.sql`, `db/README.md` | open |
| F-36 | M | DB | No migration runner; `schema_migrations` has 52 of 125 migrations (latest recorded 111) | `db/` | open |
| F-37 | L | DB | `tenant_roles`, `tenant_role_mappings`, `tenant_role_grants` RLS not FORCE; integration test CV-02 expects all FORCE; some tables owned by `them` not `them_owner` | `db/095_tenant_roles.sql` | open |
| F-38 | L | Tenancy | Tenant slug resolver caches 5 min: disabled tenant still serves runtime traffic | `tenantctx/resolver.go` | open |
| F-39 | L | Tenancy | `llm_providers_read` exposes bootstrap-tenant (platform) providers to every tenant; confirm no secret columns are readable | `db/110_*.sql` | open |
| F-40 | L | Docs | `db/README.md` is Python-era (`them-bridge (app/)`, init skips migrations 003+) | `db/README.md` | open |
| F-41 | L | Tests | `rls_integration_test.go` CV-01 expects ≥28 RLS tables / all FORCE — verify it passes; add `hil_approvals` etc. to the catalog test | `internal/db/rls_integration_test.go` | open |
| F-42 | H | Gateway | `WriteRequest` inserts `user_id` but live `gateway_requests` has no such column (migration 125 not applied): usage writes fail on this DB | `llmgateway/dal.go`, `db/125_*.sql` | open |
| F-43 | H | Gateway | Profile-app path skips model allow-list, API-RPM quota, tenant run quotas and gate (AdmitDebug); caller's `model` picks provider | `llmgateway/handler.go` | open |
| F-44 | M | Gateway | Usage write in `defer` uses `r.Context()` (cancelled on client disconnect) → usage/cost lost | `llmgateway/handler.go` | open |
| F-45 | M | Gateway | Cost from static rate card with stale model IDs; unknown Anthropic models billed at Sonnet rate; DB `model_pricing` ignored | `llmgateway/service.go` | open |
| F-46 | M | Gateway | Only monthly-USD budget enforced, tenant-wide (not per client), check-then-spend race; other budgets, `warn_at`, `sample_rate`, `capture_bodies`, `retention_days` stored but not enforced | `llmgateway/policy_gov.go` | open |
| F-47 | M | Gateway | Request/response bodies stored plaintext at debug/audit; `retain_until` never purged | `llmgateway/dal.go` | open |
| F-48 | H | Gateway | Provider `base_url` set by tenant admin is not validated → SSRF from bridge and workers; ollama/mock need no key | `admin/service/llm_providers.go`, `llm/openai.go` | open |
| F-49 | L | Gateway | `system` messages rewritten to `user`; array content/tools/n/stop/top_p unsupported | `llmgateway/service.go` | open |
| F-50 | L | Gateway | No request body size cap or message-count limit | `llmgateway/handler.go` | open |
| F-51 | L | Gateway | `X-User-JWT` tenant not checked against client tenant (attribution/quota spoof) | `llmgateway/auth.go` | open |
| F-52 | L | Gateway | `gateway_clients` has no expiry/revoked/enabled; `last_seen` never updated; no throttling of bad tokens | `llmgateway/dal.go`, `db/100_*.sql` | open |
| F-53 | L | Gateway | Policy errors fail open on direct path but closed on profile-app path; nil policy never cached (DB hit per request) | `llmgateway/service.go`, `dal.go` | open |
| F-54 | L | Gateway | `modelToProvider` has no gemini (providers seeded in db/107); profile-app "streaming" is one chunk | `llmgateway/service.go` | open |
| F-55 | L | Docs | `llmresolve` header claims platform-default fallback (code never uses it); `them:gateway:policy:v2:*` missing from `REDIS.md` | `llmresolve/llmresolve.go`, `docs/reference/redis-generated.md` | open |
| F-56 | H | MCP | `/internal/execute` and `/internal/probe` have no auth; body-supplied `application_id`/`test_credential` trusted; any container on the network can use any app's MCP credentials | `mcp/server.go`, `mcp/executor.go` | open |
| F-57 | H | MCP | MCP server `url` not validated (any scheme/host/internal IP) → SSRF; `MCP_ALLOW_HTTP`/`MCP_ALLOW_STDIO` never enforced | `admin/service/mcp_servers.go`, `mcp/config.go` | open |
| F-58 | H | MCP | Admin `POST /mcp-servers/{id}/probe` skips tenant ownership check (IDOR by UUID) and service loads by ID only | `admin/mcp_servers.go`, `mcp/dal.go` | open |
| F-59 | M | MCP | Orchestrator `tools` allowlist / attachment not enforced at execution (only filters what LLM sees) | `orchestrator/tools.go`, `mcp/executor.go` | open |
| F-60 | M | MCP | Workers keep the Server struct they started with; URL/probe-token edits are ignored until disable/enable or restart | `mcp/supervisor.go` | open |
| F-61 | M | MCP | Tool-count-drop (>20%) marks `degraded` and never updates the manifest → stale tools after legit removal | `mcp/health.go` | open |
| F-62 | M | MCP | Client: unbounded response read, only first SSE `data:` line read, fixed 10s tool timeout, redirects keep auth header, full `initialize` on every call | `mcp/client.go`, `mcp/executor.go` | open |
| F-63 | L | MCP | Admin transport error text wrong (stdio); `sse` accepted but unsupported by client; slug unvalidated (`__` breaks tool-name parsing) | `admin/service/mcp_servers.go`, `orchestrator/mcp.go` | open |
| F-64 | L | MCP | Leader renew is GET-then-PEXPIRE (not atomic, no fencing); probe auth always `Authorization` (per-server header name not stored) | `mcp/leader.go`, `mcp/health.go` | open |
| F-65 | L | MCP | Upstream error bodies (≤512B) are forwarded into LLM context and `tool_result` events seen by end users | `mcp/client.go`, `orchestrator/tools.go` | open |
| F-66 | L | MCP | `tool_call` events only emitted in dual mode (no Postgres table); `ToolsManifest` ignores `outputSchema`/annotations | `cmd/mcp-service/main.go` | open |
| F-67 | H | A2A | agent-runtime identity = plain `X-Them-*` headers, `Authorization` ignored; anyone on the network can run any tenant's agent with that app's credentials | `cmd/agent-runtime/runtime.go` | open |
| F-68 | H | A2A | Task store keys by task ID only (no tenant/app/user check); inbound A2A skips admission for GetTask/CancelTask/push-config (no auth); ownership-checking store is unused | `agentgen/a2a_task_store.go`, `a2a/server.go` | open |
| F-69 | M | A2A | HITL handle has TenantID but runtime GetTask/Subscribe/Cancel never compare it with the caller | `cmd/agent-runtime/hitl.go` | open |
| F-70 | H | A2A | HTTP step: URL/body templated from caller data, no allow-list (SSRF), redirects followed, app credential injected into rendered URL (exfil), unbounded response, non-JSON replies silently ignored | `agentgen/interpreter.go` `execHTTP` | open |
| F-71 | M | A2A | Runtime agent-card endpoint unauthenticated; spec looked up by slug only (duplicate slugs across tenants pick one arbitrarily) | `cmd/agent-runtime/spec.go` | open |
| F-72 | M | A2A | Inbound A2A: bearer token tenant overrides URL slug tenant (opposite of WS/SSE); card `agent.json` vs runtime `agent-card.json`; card URL from untrusted `X-Forwarded-*` when `publicURL` unset | `a2a/server.go`, `a2a/card.go` | open |
| F-73 | L | Docs | `.claude/skills/a2a.md` is stale (hand-rolled dispatch, SDK "not adopted", unimplemented step types, `a2a_async`, ownership-checked store) | `.claude/skills/a2a.md` | open |
| F-74 | L | A2A | HITL `TrySignal` is GET-then-SET (not atomic); `hitlStore.Store` failure only warns, leaving a running workflow with no reconnect handle | `agentgen/hitl_store.go`, `runtime.go` | open |
| F-75 | L | A2A | Outbound A2A/registry: unbounded response reads, streaming client `Timeout: 0`, depth guard relies on caller-supplied header | `agentregistry/registry.go`, `agentgen/a2a_caller.go` | open |
| F-76 | L | A2A | 60 s spec cache delays unpublish/disable; legacy plaintext `provider_keys` and `plain:` prefixed secrets still accepted | `cmd/agent-runtime/spec.go` | open |
| F-77 | L | A2A | Runtime output is one text artifact only (no file/data parts) while AppFlow expects file parts from A2A agents | `cmd/agent-runtime/runtime.go` | open |
| F-78 | H | Export | Tenant-scoped `export-own` uses `legacyDAL` = Admin (BYPASSRLS) pool and `ExportApplication` has no tenant filter: any tenant admin can export any tenant's app by UUID (comment wrongly says `them_app`) | `admin/applications.go`, `dal/app_export.go`, `cmd/them/main.go` | open |
| F-79 | H | Export | `app_agent_bindings.agent_params` secret entries (`{ct,hint}`) copied verbatim by deploy and written to export files (same class as fixed `app_params` leak); target tenant silently uses source secrets | `dal/app_deploy.go`, `dal/app_export.go` | open |
| F-80 | M | Export | Deploy/import not functionally identical: entry points lose `access_policy`, `max_concurrent_sessions`, `queue_*`, `conversation_token_limit`, `required_role`, `agent_card`; orchestrators lose component pin; deploy drops `security_config`/`conversation_token_limit`; `presentation` never copied; no warning to re-publish | `dal/app_deploy.go`, `dal/app_export.go` | open |
| F-81 | M | Export | `_resolved_agent_ids` in the copied definition JSON keeps source-tenant agent UUIDs (never remapped) → AppFlow agent nodes fail until re-publish | `dal/app_deploy.go`, `dal/app_export.go` | open |
| F-82 | M | Export | Tenant-scoped import runs without a transaction → partial app on failure | `admin/applications.go` | open |
| F-83 | M | Export | Import looks up `mcp_servers` by slug with no tenant filter, silently skips missing; import returns no checklist (comment promises one) | `dal/app_export.go` | open |
| F-84 | M | Export | Import trusts file contents (agent `endpoint_url`, spec, definition, `content_hash`); only `export_version` validated | `dal/app_export.go` | open |
| F-85 | L | Export | `appflow_debug_presets` and `tenant_role_grants` have `application_id` but are not in `appScopedConfigTables` (rule in CLAUDE.md) | `dal/app_deploy.go` | open |
| F-86 | L | Export | No audit entries visible for deploy/export/import; deploy slug suffix is random so repeat deploys create duplicates | `admin/applications.go` | open |
| F-87 | L | Docs | ~206 comments/strings in ~135 source files (Go, TS, scripts, compose) point at old `docs/<NAME>.md` paths that were archived to `docs/_archive/`; comment-only fix, plus a link check in docs-lint (tracker #13) | repo-wide | mostly fixed 2026-10-25 (≈300 path-only comment edits; 12 refs left: 4 inside string literals, others point at nested archived dirs) |
| F-88 | H | Redis | Main Redis is `allkeys-lru` (256 MB) yet stores non-rebuildable state (run streams, HITL handles, `them:wait:*`, gate sets, MCP leader lock); LRU can evict them silently under pressure | `redis/config/redis.conf` | open |
| F-89 | M | Redis | Main Redis password only if `THE_M_REDIS_PASSWORD` set, events Redis has none, no TLS (relies on network isolation) | `docker-compose*.yml`, `redis/config/*.conf` | open |
| F-90 | M | Redis | Debug-mode LLM keys/secrets stored unencrypted in Redis for ~3h40m (deleted on finalize) | `go/internal/debugcred/store.go` | open |
| F-91 | H | Secrets | Real gateway bearer tokens are committed in git: 3 root scripts (now deleted, still in history), `test-runner/data/scenarios/*.json` (several), `go/scripts/soak_setup_db.sh`. Rotate/revoke those tokens (delete the gateway client rows) and scrub or template the remaining files | `test-runner/data/scenarios/`, `go/scripts/soak_setup_db.sh`, git history | open |
| F-92 | M | Tests | Live E2E `test_40/41/42` fail at validate/publish with `component_not_found` for the `a2a_echo` agent although the component exists and is listed by `/admin/component-definitions` (same kind/namespace/name/version, tenant default). Either a stale test fixture (hard-coded agent id/namespace) or a resolver bug in the validate path — investigate | `scripts/tests/test_4[0-2]*.py`, `admin/service/publish.go`, `registry/` | open |
| F-93 | L | Tests | `test_37_sso.py` had a SyntaxError (fixed 2026-10-25) and still fails 2/11 (`tenant-lookup` by email domain returns 404 — fixture likely assumes a `bank.com` email-domain mapping); the E2E scripts duplicate their HTTP/login helpers and hard-code agent IDs | `scripts/tests/` | open |
| F-94 | H | Secrets | The `origin` remote URL in `.git/config` embeds a GitHub personal access token in plain text (printed once into a Claude session on 2026-10-25): revoke/rotate it and switch to a credential helper or SSH | `.git/config` | open |
| F-95 | M | Redis | Run streams `them:dash:run:{id}:stream` get only `MAXLEN ~5000`, **no TTL** (ADR-003's 48 h safety / 24 h final TTL were Python-Lua and never ported): finished runs' streams stay until LRU evicts them (which can also evict live ones) | `internal/cache/runstreamer_writer_adapter.go`, `runstream/publisher.go` | open |
| F-96 | L | Runs | `Lifecycle` models an `admitted` run state but `CreateRun` inserts `running` and the CHECK constraint forbids `admitted` (and `input_required`); the admitted/orphan-run logic in comments does not match the DB | `execution/lifecycle.go`, `runrecorder/recorder.go`, `domain/domain.go` | open |
