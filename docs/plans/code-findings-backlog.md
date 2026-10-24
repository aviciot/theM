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
| F-15 | L | Docs | `REDIS.md` has Python-era keys, lacks `them:events`, `them:events:dlq`, `them:ep:gate:queue:{slug}` | `docs/REDIS.md` | open |
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
| F-30 | L | Docs | Legacy `docs/AUTH.md` contradicts code (refresh claims, bootstrap tenant fallback) | `docs/AUTH.md` | open |
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
| F-55 | L | Docs | `llmresolve` header claims platform-default fallback (code never uses it); `them:gateway:policy:v2:*` missing from `REDIS.md` | `llmresolve/llmresolve.go`, `docs/REDIS.md` | open |
