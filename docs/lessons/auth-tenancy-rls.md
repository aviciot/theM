---
title: Lessons — Auth, Tenancy & RLS
type: lessons
status: current
last_verified: unverified
---

> Append-only. Format: symptom → root cause → fix → watch for next time. Keep newest entries at the top of each area file. Split from the single LESSONS file in docs-reboot tracker #12; entries are verbatim.

## 2026-10-15 — App created via admin JWT goes under platform tenant, not target tenant

**Symptom:** Created an app via `POST /api/v1/admin/applications` with `tenant_id: <payops_ai>` in the body, but the app and its entry_points ended up under the platform (`default`) tenant. Gateway calls returned 502 "profile workflow failed to start".
**Root cause:** The handler uses `tenantctx.MustTenantIDFromCtx` (from the JWT) to set the tenant, ignoring the `tenant_id` field in the request body. Admin JWT = platform tenant. The `entry_points.tenant_id` is also set from the JWT context, not the body.
**Fix:** To create an app under a specific tenant via API: after creating via admin JWT, run SQL to update both `them.applications.tenant_id` AND `them.entry_points.tenant_id` to the target tenant UUID. Or log in as a tenant-scoped user to create the app correctly.
**Watch for:** Any app creation via admin JWT will land under `default` tenant. The `epconfig` loader queries `WHERE ep.tenant_id = $1` — if tenant mismatches, `AdmitDebug` returns `ErrNotFound` and gateway returns 502.

---

---

## 2026-09-10 — Bank SSO login fails: "IdP token exchange failed"

**Symptom:** Logging in via bank SSO returns `{"detail":"IdP token exchange failed"}`. Keycloak logs show `error="invalid_client_credentials"` for the `them-m` client in the `bank` realm.
**Root cause:** The `them.tenants` row for the `bank` tenant had `client_secret = "them-m-bank-secret"` (set manually from a different machine/IP), but `keycloak/bank-realm.json` defines the `them-m` client with secret `"them-m-secret"`. The DB and Keycloak were never in sync.
**Fix:** `UPDATE them.tenants SET idp_config = jsonb_set(idp_config, '{client_secret}', '"them-m-secret"') WHERE slug = 'bank';`
**Watch for:** When configuring a new tenant's SSO via the UI, the secret entered must exactly match what is in the Keycloak realm. The realm JSON is the source of truth for secrets — the DB row must match it.

---

---

## 2026-09-07 — POST /admin/tokens returns 500 (FK violation on user_id)

**Symptom:** `POST /admin/tokens` returns 500 for any user when no `user_id` is in the request body. Bridge logs show no error (silently swallowed).
**Root cause:** `tokenCreateBody.UserID` is `int64` with Go zero value `0`. The INSERT passed `0` for `user_id`, violating the FK to `auth_service.users` (no user with id=0 exists). The scan path also failed when reading back a row with NULL user_id into `int64`.
**Fix:** `NULLIF($4, 0)::integer` in the INSERT so 0→NULL. `COALESCE(user_id, 0)` in `tokenSelectCols` and `pgx_querier.go` so NULL scans safely into int64. Added `slog.Error` in the handler so future failures appear in logs.
**Watch for:** Any nullable integer FK column that Go reads as `int64` (not `*int64`) needs `COALESCE` in SELECT and `NULLIF` in INSERT/UPDATE to avoid FK violations and scan panics.

---

---

## 2026-09-07 — ListApplications returns empty entry_points via RLS path

**Symptom:** `GET /admin/applications` returns applications with `entry_points: []` when going through the TenantTx (App pool / RLS) path. `GET /admin/applications/{id}` returns entry_points correctly.
**Root cause:** `ListApplications` in the DAL opened a `pgx.Rows` cursor for the main applications query, then called `ListEntryPoints` (which opens another `Query`) for each app while the first cursor was still iterating. pgx transactions do not support interleaved result sets on a single connection — the inner query silently returned empty results. Additionally, `AppService.List` was calling `ListEntryPoints` a second time after the DAL already populated it (double fetch).
**Fix:** Scan all app rows into memory first (close cursor), then enrich with `listAppOrchSummaries` + `ListEntryPoints` after the main cursor is closed. Removed the redundant `ListEntryPoints` call in the service layer.
**Watch for:** Never run sub-queries (even `QueryRow`) inside a `rows.Next()` iteration loop on the same `pgx.Tx` — the connection is busy with the first cursor. Collect results first, then enrich.

---

---

## 2026-09-05 — All tenant-scoped admin Create endpoints return 500 (transaction aborted)

**Symptom:** `GET /admin/agents` returns 200; `POST /admin/agents` returns 500 with no structured log output. Response time ~10ms (DB contact made). Similarly for orchestrators, applications, MCP servers.
**Root cause 1:** `agentSelectCols` in `dal/agents.go` had `LEFT JOIN auth_service.users u ON u.id = a.created_by`. `them_app` (the App pool / RLS role) has no USAGE grant on the `auth_service` schema. Every List/Get call via the App pool returned `ERROR: permission denied for schema auth_service` — PostgreSQL aborts the transaction. This caused all GET requests through the TenantTx path to fail with HTTP 500.
**Root cause 2:** `checkResourceQuota` (called by agents/apps/mcp Create) runs `SELECT FROM them.tenant_quotas`. `them_app` had no SELECT grant on `tenant_quotas`. PostgreSQL returned permission denied, aborting the transaction. The subsequent INSERT (`CreateAgent` CTE) then fails with SQLSTATE 25P02 "transaction is aborted". `checkResourceQuota` is fail-open on errors, so the quota denial was swallowed, but the aborted TX killed the real INSERT.
**Fix:** (1) Removed `LEFT JOIN auth_service.users` from `agentSelectCols` and `agent_definitions.go` queries — `them_app` must never cross into `auth_service` schema. `CreatedByUsername` field is now empty (omitempty, UI shows `—`). (2) Added migration 080 to `GRANT SELECT ON them.tenant_quotas TO them_app`.
**Watch for:** Any new query run via the App pool (TenantTx / `them_app` role) must not JOIN into `auth_service.*` — that schema is auth-admin only. Any new table that the App pool needs to read must have an explicit GRANT. When adding quota checks or cross-table reads to a service that runs inside TenantTx, verify `them_app` has SELECT on the target table before deploying.

---

---

## 2026-09-05 — Cross-tenant agent token exfiltration via agents/discover

**Symptom:** A tenant with an `agent_id` in their request body could cause the bridge to fetch a token from a different tenant's agent and forward it to a user-controlled endpoint via `Authorization: Bearer <stolen_token>`.
**Root cause:** `GetAgentTokenEncrypted` in the legacy DAL queried `agents` by `id` only, with no `tenant_id` filter. Any authenticated user who knew another tenant's agent UUID could extract its decrypted token.
**Fix:** Added `GetAgentTokenEncryptedForTenant(ctx, id, tenantID)` that filters by both `id` AND `tenant_id`. The Discover handler now uses this tenant-scoped lookup; if the agent doesn't belong to the requesting tenant, no token is returned. Test `TestDiscover_CrossTenantTokenNotForwarded` verifies the Authorization header is empty when the lookup returns no rows.
**Watch for:** Any place that fetches a secret (token, key, credential) by a single ID must include the tenant's `tenant_id` in the WHERE clause when the secret is tenant-scoped.

---

---

## 2026-08-19 — "Failed to load user" after login on HTTP

**Symptom:** Login returns 200, but the page immediately shows "Failed to load user". Auth-go logs show no `/me` request ever arriving.
**Root cause:** The Next.js login route sets auth cookies with `Secure=true` when `NODE_ENV=production`. Browsers silently discard `Secure` cookies on plain HTTP — so the cookie set by `/api/auth/login` is never sent on the immediately following `/api/auth/me` call, which returns 401.
**Fix:** Added `COOKIE_SECURE` env var to the frontend. When set to `"false"`, cookies are issued without the `Secure` flag. `docker-compose.yml` sets `COOKIE_SECURE=false` for local/HTTP deployments. Remove it (or set to `"true"`) for HTTPS production (Hetzner with TLS).
**Watch for:** Any new environment that serves over plain HTTP — must have `COOKIE_SECURE=false` set. Any environment that moves to HTTPS must remove it. See `docs/guides/local-dev-and-deploy.md` for the full variable table.

---

---

## 2026-07-06 — JWT "Invalid or disabled token" in playground WS

**Symptom:** Playground shows "Error: Invalid or disabled token" immediately when trying to connect the WebSocket, even though the user was logged in.
**Root cause:** `/api/auth/token` returned the JWT from the httpOnly cookie without checking expiry. JWTs have a 2-hour TTL. A user who logged in and left the tab open would silently get an expired token passed to the WS `?token=` parameter.
**Fix:** Added `jwtExpiresIn()` in `frontend/src/app/api/auth/token/route.ts` — if the token has < 30s left, call `/api/v1/auth/refresh`, set fresh cookies, and return the new token. Expired tokens are never returned to JS.
**Watch for:** Any place that hands a JWT to client-side JS for WS auth must check expiry first. The browser cannot refresh JWTs on its own because they live in httpOnly cookies.

---

---

## 2026-07-07 — TOCTOU: orchestrator scope check must be inside the same DB session as task creation

**Symptom:** `_handle_send_message` did an orchestrator lookup in one `async with` block, then a task creation in another. A race condition could allow a token scoped to orchestrator A to create a task for orchestrator B if the orchestrator was swapped between the two sessions.

**Fix:** Moved orchestrator lookup + scope check + task creation into a single `async with db_module.AsyncSessionLocal() as db:` block. Both the scope check and the `create_task` call now use the same session snapshot.

**Watch for:** Any multi-step operation that checks a permission then acts on it — always do both in the same DB transaction or session to avoid TOCTOU gaps.

---

---

## 2026-07-07 — Token expiry not checked at API layer — token_cache payload must include expires_at

**Symptom:** Access tokens with `expires_at` set in the DB were accepted by `/a2a` even after expiry, because `_row_to_payload` in `token_cache.py` didn't serialize `expires_at` into the cached dict.

**Fix:** Added `"expires_at": row.expires_at.isoformat() if row.expires_at else None` to `_row_to_payload`. Added an expiry check in `_resolve_bearer` that parses the ISO string and rejects if `expires_at < now(UTC)`.

**Watch for:** Any new token payload fields needed for policy enforcement must be added to `_row_to_payload` — the cache is the sole source of truth once a token is cached. If a field is missing from the payload dict, it cannot be enforced without a DB round-trip.

---

---

## OIDC Role Separation — Platform Role vs Membership Role (2026-09-06)

`UpsertOIDCUser` previously used the group-mapping `role` as both:
1. The key to look up `auth_service.roles WHERE name = role` (platform role → users.role_id)
2. The value stored in `tenant_memberships.role` (tenant role)

This is wrong because `auth_service.roles` only has `super_admin/developer/analyst/viewer` (platform roles), not `admin/member/viewer` (tenant membership roles). Looking up `"admin"` in `auth_service.roles` returned no rows, causing a 500 cascade to last-resort fallback.

**Fix:** Platform role for all OIDC users is always `"viewer"` (hard-coded constant). Group mapping result goes only to `tenant_memberships.role`. `validMemberRoles` map rejects `super_admin` before it reaches the DB.

**Watch for:** If a new role type is added, always check whether it's a platform role (`auth_service.roles`) or a tenant role (`tenant_memberships.role`) — they are independent enumerations.

---

## Refresh Token Must Carry Tenant ID (2026-09-06)

A refresh token that carries only `user_id` always picks the first membership row on refresh, silently losing the tenant selected at login for multi-membership users.

**Fix:** `IssueRefreshToken(userID, tenantID)` — tenant embedded in claims. `Refresh()` calls `issuePairByTenantID` when present, re-validating the specific membership row (membership may have been revoked). Old tokens without `tenant_id` fall back to first-row for backwards compatibility.

**Watch for:** Any new token issuance path (e.g., OAuth device flow) must also embed `tenant_id` in the refresh token.

---

## `/ws/dashboard`'s `run:*` channel had no tenant-ownership check — a real cross-tenant IDOR (found 2026-09-23)

While fixing the live-delivery gap above, a second, separate bug was found in the same code path:
`IsValidChannel(ch)` only validates that a channel name is well-formed (`"run:" + non-empty
suffix`) — it never checks whether the caller's own tenant actually owns the run UUID in that
channel name. Any authenticated user of any tenant could subscribe to `run:{any-uuid}` and, if that
run's Redis Stream still existed, receive another tenant's complete live trace — including LLM
prompts and outputs — with no authorization check at all.

**Why it wasn't caught earlier:** `tenantID` (from the JWT) is already passed into
`sendSnapshots(ctx, cw, pubsubChannels, tenantID)` and used there for `agent:`/`scan:` channel
authorization — so a reviewer skimming `ServeHTTP` could reasonably assume every channel type gets
the same treatment. `run:*` channels were split out of `pubsubChannels` into their own
`tailRunChannel` path (the live-delivery fix above) with no equivalent check carried over — the
`tenantID` variable was right there in scope, just never referenced by the new code.

**Fix:** new `dashboard.RunOwnershipChecker` interface + `PgxRunOwnershipChecker`
(`go/internal/dashboard/run_ownership.go`) — a single `SELECT EXISTS(... WHERE id = $1 AND
tenant_id = $2)` query against `them.runs`, checked before `tailRunChannel` is ever called for a
given channel. Fails **closed**: a nil checker, a query error, or `owns=false` all refuse to tail
that channel (silent — logged as a warning, no distinguishable error sent to the client, so a
tenant probing run IDs can't tell "wrong tenant" apart from "doesn't exist" or "server
misconfigured").

**Watch for:** any handler that threads a caller's `tenantID` through some code paths for
authorization but not others in the same function — especially when a new branch is added that
looks structurally similar to existing authorized branches (same function, same loop, same
variable in scope) but was written without an explicit ownership check because "it's just like the
other channels" was assumed rather than verified. Grep for every place a resource ID from client
input is used to build a lookup key, and confirm each one is scoped by the caller's own tenant, not
just well-formed.
