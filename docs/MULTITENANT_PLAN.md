# Multi-Tenant Plan — the-M
# Last updated: 2026-09-06 (pre-Step-34 architecture sync)

## Executive Summary

The-M supports multiple isolated tenants (companies/customers) on a single instance. Data isolation is enforced at the DB layer via Row-Level Security — no application-layer filtering is trusted. The infrastructure layer (RLS, quota, user management, OIDC backend) is now complete. What remains is the **experience layer**: role-aware frontend nav, a guided provisioning flow, and wiring the existing OIDC backend to a test IdP + login page.

**OIDC clarification:** The OIDC authorization-code flow with PKCE, RS256 id_token verification, JWKS key caching, group-claim→role mapping, and HS256 JWT issuance are all implemented (Steps 5, 8, 9, 17, 18). Gap 4 is frontend-only: connect the login page email-first flow to the tenant-lookup endpoint and add a Keycloak test IdP in Docker.

---

## Current State

| Capability | Status | Notes |
|---|---|---|
| DB row-level security (RLS) | ✅ Complete | All 28 tables, FORCE RLS, two-pool architecture |
| Tenant model + quotas | ✅ Complete | `them.tenants`, `them.tenant_quotas` |
| Quota enforcement at run-admit | ✅ Complete | 5 limits: concurrent, RPM, monthly runs, API RPM, token budget |
| Quota enforcement at create | ✅ Complete | max_agents, max_apps, max_mcp_servers, max_users |
| Tenant self-service API | ✅ Complete | `GET/PATCH /tenant/settings`, `GET /tenant/quota` |
| Tenant self-service UI | ✅ Complete | `/tenant/settings` page (General + Quota tabs) |
| User management API | ✅ Complete | `GET/POST/PATCH/DELETE /api/v1/admin/users` (super_admin only) |
| User management UI | ✅ Complete | `/admin/users` page — create, assign to tenant, reset password |
| JWT tenant_id claim | ✅ Complete | Populated from `tenant_memberships` at login; login blocked if no membership |
| RLS activated from JWT | ✅ Complete | Bridge sets `app.tenant_id` GUC via TenantTx on every request |
| Tenant login chain | ✅ Complete | CreateUser contract fixed, /me returns JWT role, UM-13/14 regression tests |
| OIDC backend flow | ✅ Complete | Steps 5/8/9/17/18 — PKCE, RS256 JWKS, group mappings, HS256 JWT issuance |
| Email-domain → tenant routing | ✅ Complete | `GET /auth/tenant-lookup?email=` live; `tenants.email_domain` indexed |
| OIDC group claims → tenant role | ✅ Complete | `them.tenant_group_mappings`; `GetGroupRole` in OIDCCallback |
| Tenant-scoped dashboard | ✅ Complete | Role-based nav (Step 34); super_admin route guards on tenants/users/observability |
| Tenant provisioning UX | ✅ Complete | 4-step wizard: tenant → admin user → quota → done (Step 35) |
| SSO / OIDC frontend wiring | ⚠️ Partial | Login page email-first flow done; super-admin IdP config UI done; **tenant-admin self-service IdP form missing** (Step 37); Keycloak test IdP not yet in Docker |
| Multi-tenant refresh (tenant preserved) | ✅ Complete | Refresh token carries tenant_id; issuePairByTenantID re-validates membership; OIDC callback also preserves tenant (OIDC-30) |
| Live two-tenant API E2E test | ❌ Not built | UM-13/14 are unit tests (fakeStore); no live test across auth→bridge→RLS |
| Tenant onboarding flow | ❌ Not built | No guided "set up your first app" flow for new tenant admins |

---

## Gaps — Ranked by Importance

### Gap 1 — Tenant Login Chain — **COMPLETE** (2026-09-06)

Fixed and verified end-to-end across commits 80924a1 + 5b2e283:
1. `CreateUser`: `role` → `RoleName` (platform: super_admin/developer/analyst/viewer); `tenant_role` → `TenantRole` (membership: admin/member/viewer).
2. `Me()` returns `claims.Role` (JWT membership role) instead of `user.Role` (global DB role).
3. Regression tests UM-13 (two-tenant isolation + /me role) and UM-14 (refresh carries tenant) added.

Live smoke test: create user → assign to tenant A → login → JWT has `tenant_id=tenantA, role=admin` → bridge returns 403 on super_admin routes (correct).

---

### Gap 2 — Tenant-Scoped Dashboard (medium) — Step 34

**What:** The frontend nav is built entirely for super_admin. A tenant admin logging in sees Tenants/Users/Observability nav items they cannot use and will get 403 errors on those routes.

**Why it matters:** RLS already isolates the data correctly. The nav is a UX problem, not a security problem — but it makes multi-tenant untestable from the UI.

**What to build:**
- Read JWT `role` from `/api/auth/me` response (already called on page load)
- `Sidebar.tsx`: hide Tenants/Users/Observability for non-super_admin
- Frontend route guards on `/admin/tenants`, `/admin/users`, `/admin/observability` — redirect non-super_admin to `/admin/applications`
- **Do NOT remove backend `RequireSuperAdmin` checks** — frontend guards are UX only; backend authorization remains mandatory

**Files:** `frontend/src/components/Sidebar.tsx`, `frontend/src/hooks/useAuth.ts` (or existing auth util), 3 admin page files.

**Scope:** Medium (1–2 days). Purely frontend — no new Go work.

---

### Gap 3 — Tenant Provisioning Wizard (medium) — Step 35

**What:** Creating a new tenant customer currently requires 3 separate API calls. No guided flow.

**What to build:**
- "New Tenant" wizard modal on `/admin/tenants`: tenant → user → quota → confirm
- Frontend calls the 3 existing endpoints in sequence (simpler than a new atomic backend endpoint)

**Scope:** Medium (1–2 days).

---

### Gap 4 — SSO / OIDC Frontend Wiring (small–medium) — Step 37

**Audit date:** 2026-09-06 — see `docs/sso-access-audit.md` for full findings.

**What was already complete before Step 37:**
- Login page email-first flow: email blur → tenant-lookup → `showSSO` flag → "SSO Login" button → `window.location.href = /api/auth/oidc/start?tenant={slug}` — **already wired in `frontend/src/app/login/page.tsx`**
- Backend OIDC flow: PKCE, RS256/JWKS, group-claim mapping, HS256 JWT issuance — complete (Steps 5, 8, 9, 17, 18)
- Super-admin IdP config UI: `/admin/tenants` TenantPanel "Identity Provider" tab — complete (Step 10)

**Access model (hybrid — both paths use same DB call, different auth levels):**
- `PATCH /admin/tenants/{id}` — `RequireSuperAdmin` — platform admin configures any tenant's IdP
- `PATCH /tenant/settings` — `RequireTenantAdmin` — tenant admin configures own IdP (JWT-scoped, cannot escape their tenant)
- Both paths accept `idp_config`; backend enforces scope; no Go changes needed

**What Step 37 must build:**
1. **`/tenant/settings` SSO form** — add "SSO / Identity Provider" section to `frontend/src/app/tenant/settings/page.tsx`:
   - Fields: `discovery_url`, `client_id`, `redirect_uri` (text), `client_secret` (write-only password input — show "already configured" placeholder when `idp_configured=true`)
   - Save button → `PATCH /tenant/settings` with `idp_config`; Clear button → sends `idp_config: null`
   - Uses `themApi.patchTenantSettings` (extend to accept `idp_config` field)
2. **Keycloak test IdP** — `docker-compose.dev.yml`: `them-keycloak` service (`quay.io/keycloak/keycloak`, profile `sso`), pre-configured realm export in `keycloak/`; Traefik route at `/auth/keycloak/` (internal only)
3. **End-to-end SSO smoke test** — configure tenant IdP via both paths (tenant-admin self-service and super-admin override), log in via OIDC flow, verify JWT contains correct `tenant_id` and role

**What Step 37 must NOT do:**
- Rebuild the login page (already done)
- Add SSO config to the Provision Wizard (optional advanced config; done post-provisioning)
- Encrypt `client_secret` at rest (see production-readiness blocker below)

**⚠️ Production-readiness blocker (NOT in Step 37 scope):**
`client_secret` is stored as **plaintext** in `them.tenants.idp_config` JSONB. Mitigations in place:
- Never returned in any GET response or audit log (write-only; audit redacted to `client_secret_changed=true`)
- DB access requires privileged credentials
- Precedent: LLM keys in `applications.provider_keys` are AES-GCM encrypted before DB write

**Encryption design (Step 37-S — do before any production OIDC deployment):**
- Add `THEM_IDP_ENCRYPTION_KEY` env var (or derive from existing `secrets.local` HMAC seed)
- `PatchTenant` encrypts `client_secret` before JSON marshal → write
- `GetTenantIDPConfig` in `oidc_store.go` decrypts after read, before OAuth2 code exchange
- Data migration: one-time script to re-encrypt any existing plaintext rows
- Touches `go/internal/admin/dal/tenants.go` + `go/internal/authserver/oidc_store.go` + both binary entrypoints
- **Block any production OIDC enablement on this step being complete**

**Scope:** Medium (2–3 days). No Go changes for the IdP config write path.

---

### Gap 5 — Tenant Onboarding Flow (small) — Step 36

**What:** New tenant admin logs in to empty dashboard with no guidance.

**What to build:** "Get started" banner on `/admin/applications` when `applications.length === 0`. Frontend only.

**Scope:** Small (0.5 days).

---

### Gap 6 — Live Two-Tenant API E2E Test (small) — Step 38

**What:** UM-13/14 use a fakeStore (no DB, no RLS). `TestRLS_TwoTenantFullIsolation` (integration tag, `go/internal/db/`) tests DB-level isolation only, not the HTTP stack. No test covers the full path: login → JWT → bridge API → RLS → response.

**What to build:** A Python test script (using urllib — curl blocked by shell permissions) that:
1. Logs in as super_admin, creates tenant A + user A and tenant B + user B
2. Logs in as user A, creates an application
3. Logs in as user B, verifies the application is NOT visible (empty list)
4. Verifies user B gets 403 on super_admin routes

**Scope:** Small (0.5–1 day).

---

## Build Order

| Step | Name | Scope | Status |
|---|---|---|---|
| **33** | Tenant login chain + contract alignment | Small | ✅ COMPLETE (2026-09-06, 5b2e283) |
| **34** | Role-based nav + frontend route guards | Medium | ✅ COMPLETE (2026-09-06, 82a8f22) |
| **35** | Tenant provisioning wizard | Medium | ✅ COMPLETE (2026-09-06, c95976d) |
| **36** | Tenant onboarding (first-login guidance) | Small | ✅ COMPLETE (2026-09-06, 34687ad) |
| **37** | SSO: tenant-admin self-service IdP form + Keycloak test IdP + E2E smoke | Medium | **Next** |
| **37-S** | client_secret encryption at rest (production-readiness blocker) | Small–Medium | Before any production OIDC deployment |
| **38** | Live two-tenant API E2E test | Small | Can be done any time after 33 |
| **—** | Group mapping super_admin guardrail | Small | ✅ COMPLETE (2026-09-06) — migration 081, validMemberRoles guard, OIDCCallback rejection |

---

## Key Design Decisions

**Multi-membership support:** The DB schema allows one user in multiple tenants (`UNIQUE(user_id, tenant_id)` permits multiple rows per user, one per tenant). `issuePair()` picks the first row when no `tenant_slug` is given at login. To log into a specific tenant: `POST /api/v1/auth/login` with `{"tenant_slug":"acme"}`. The frontend does not yet expose this field.

**Refresh preserves tenant selection:** Refresh tokens carry `tenant_id` in claims. `Refresh()` calls `issuePairByTenantID` when the claim is present, re-validating the specific membership row. Old tokens without `tenant_id` fall back to first-row behaviour for backwards compatibility. OIDC callback also embeds the tenant UUID in the refresh token it issues.

**OIDC group mapping privilege escalation — closed:** `them.tenant_group_mappings.role` is now constrained by a DB CHECK (`('admin','member','viewer')` — migration 081). App-layer rejection in OIDCCallback and `validMemberRoles` guard in `UpsertOIDCUser` provide defence-in-depth. Platform role for OIDC users is always `"viewer"` regardless of group mapping.

**UM-13/14 are unit tests, not live RLS tests:** They use a fakeStore (in-memory, no DB, no RLS). The real live two-tenant RLS test is `TestRLS_TwoTenantFullIsolation` (integration tag, `go/internal/db/`). A live auth→bridge→RLS HTTP E2E test does not yet exist (Gap 6, Step 38).

**RLS is the isolation guarantee:** The application layer does NOT filter by tenant_id in queries — it sets a PG GUC (`app.tenant_id`) and lets RLS policies enforce isolation. A bug in the frontend nav (Gap 2) leaks no data — it's a UX problem, not a security problem.

**Tenant_id always from JWT, never URL/header:** `AdminTenantMiddleware` reads tenant_id from JWT claims only. This invariant must never be violated in new code.

---

## Working Demo Checklist

After Steps 33–35 are complete, the following must work:

- [ ] Super_admin logs in → sees full admin nav (Tenants, Users, Observability)
- [ ] Super_admin creates tenant "Acme Corp" via New Tenant wizard
- [ ] Super_admin creates user `alice@acme.com`, assigns to "Acme Corp" as `admin`
- [ ] Alice logs in → JWT contains `tenant_id = <acme-uuid>`, `role = "admin"`
- [ ] Alice sees tenant-scoped nav (Applications, Runs, MCP, My Tenant — no Tenants/Users/Observability)
- [ ] Navigating directly to `/admin/users` as Alice → redirected to `/admin/applications`
- [ ] Alice creates an application → created under Acme's tenant_id (RLS scopes it)
- [ ] Super_admin sees Acme's application in observability summary
- [ ] Bob (different tenant) logs in → cannot see Acme's application (RLS blocks it)
- [ ] Alice's runs are quota-gated by Acme's quota limits
