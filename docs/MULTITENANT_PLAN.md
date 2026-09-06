# Multi-Tenant Plan — the-M
# Last updated: 2026-09-06

## Executive Summary

The-M supports multiple isolated tenants (companies/customers) on a single instance. Data isolation is enforced at the DB layer via Row-Level Security — no application-layer filtering is trusted. The infrastructure layer (RLS, quota, user management) is now complete. What remains is the **experience layer**: tenant users need to be able to log in and see only their data, the frontend needs to render a tenant-scoped view, and super_admins need a guided provisioning flow.

---

## Current State

| Capability | Status | Notes |
|---|---|---|
| DB row-level security (RLS) | ✅ Complete | All 28 tables, FORCE RLS, two-pool architecture |
| Tenant model + quotas | ✅ Complete | `them.tenants`, `them.tenant_quotas` |
| Quota enforcement at run-admit | ✅ Complete | 5 limits enforced: concurrent, RPM, monthly runs, API RPM, token budget |
| Quota enforcement at create | ✅ Complete | max_agents, max_apps, max_mcp_servers, max_users |
| Tenant self-service API | ✅ Complete | `GET/PATCH /tenant/settings`, `GET /tenant/quota` |
| Tenant self-service UI | ✅ Complete | `/tenant/settings` page (General + Quota tabs) |
| User management API | ✅ Complete | `GET/POST/PATCH/DELETE /api/v1/admin/users` (super_admin only) |
| User management UI | ✅ Complete | `/admin/users` page — create, assign to tenant, reset password |
| JWT tenant_id claim | ✅ Complete | Populated from `tenant_memberships` at login |
| RLS activated from JWT | ✅ Complete | Bridge sets `app.tenant_id` GUC via TenantTx on every request |
| Tenant login (verify it works) | ⚠️ Unverified | JWT has tenant_id, but no end-to-end test with a non-super_admin user |
| Tenant-scoped dashboard | ❌ Not built | Frontend shows all data; no role-based view filtering |
| Tenant provisioning UX | ❌ Not built | Multi-step manual process; no guided flow for super_admin |
| SSO / OIDC | ❌ Not built | Username+password works; external IdP not integrated |
| Tenant onboarding flow | ❌ Not built | No guided "set up your first app" flow for new tenant admins |

---

## Gaps — Ranked by Importance

### Gap 1 — Tenant Login Verification (small)

**What:** Confirm a non-super_admin user created via `/admin/users` can log in, get a tenant-scoped JWT, and have RLS correctly restrict their DB access.

**Why it matters:** Everything else depends on this. If the JWT → GUC → RLS chain is broken for regular users, no amount of UI work will produce real isolation.

**What to build:**
- Write an integration test: create user → assign to tenant → login → verify JWT has `tenant_id` → verify a DB query through the bridge returns only that tenant's data
- Fix any gaps found (likely: `them-auth-go` login handler may not query `tenant_memberships` at all, or may not include `tenant_id` in the token claims)
- Files: `go/internal/authserver/handlers.go` (login handler), `go/internal/authserver/pgx.go` (`GetTenantMembership`), token claims struct

**Scope:** Small (1–2 days). Pure backend verification + fix.

---

### Gap 2 — Tenant-Scoped Dashboard (medium)

**What:** The frontend is built entirely for super_admin. A tenant admin or member logging in sees the super_admin nav (Tenants, Users, Observability) and all platform data. They need to see only:
- Their own applications, agents, runs, MCP servers
- Their own tenant settings (already built — `/tenant/settings`)
- No cross-tenant admin UI

**Why it matters:** Without this, multi-tenant is backend-only. A real customer logging in sees a confusing UI with admin controls they can't use and data that's already correctly filtered by RLS (the data is right, the nav is wrong).

**What to build:**
- Read JWT role from the frontend session cookie
- In `Sidebar.tsx`: show/hide nav items based on role (`super_admin` sees Tenants/Users/Observability; `admin`/`member`/`viewer` do not)
- Guard admin pages (`/admin/tenants`, `/admin/users`, `/admin/observability`) — redirect non-super_admin to `/admin/applications`
- Add a "My Tenant" landing page or redirect tenant users to their relevant section
- Files: `frontend/src/components/Sidebar.tsx`, `frontend/src/lib/auth.ts` (role reading), individual page auth guards

**Scope:** Medium (2–3 days). Purely frontend — no new Go work.

---

### Gap 3 — Tenant Provisioning UX (medium)

**What:** Creating a new tenant customer currently requires:
1. POST `/api/v1/admin/tenants` (create tenant)
2. POST `/api/v1/admin/users` (create user, assign tenant)
3. PATCH `/api/v1/admin/tenants/{id}/quota` (set quota)

No single guided flow. Easy to miss a step.

**Why it matters:** Super_admins onboarding new customers need a reliable, fast path. Errors (user with no tenant, tenant with no users) create broken states.

**What to build:**
- "New Tenant" wizard modal on `/admin/tenants` page: Step 1 (tenant name + slug + email domain) → Step 2 (initial admin user credentials) → Step 3 (quota defaults) → Confirm → creates all three in one flow
- Backend: a single `POST /api/v1/admin/tenants/provision` endpoint that creates tenant + user + quota atomically in a transaction (or the frontend calls the 3 existing endpoints in sequence — simpler)
- Files: `frontend/src/app/admin/tenants/page.tsx` (add wizard modal), optionally `go/internal/admin/tenants.go` (provision endpoint)

**Scope:** Medium (1–2 days). Frontend wizard + optional Go atomic endpoint.

---

### Gap 4 — SSO / OIDC (large)

**What:** Enterprise customers authenticate via their company IdP (Google Workspace, Azure AD, Okta, Keycloak). The login page needs email-domain detection → IdP redirect → OIDC callback → issue tenant-scoped JWT.

**Why it matters:** Enterprise customers will not create individual username/password accounts. SSO is a commercial requirement, but it's NOT needed for initial multi-tenant demo or for customers comfortable with username/password.

**What to build:**
- `them.tenant_idp_configs` table (or use existing `idp_config` JSONB on `them.tenants`): maps email domain → OIDC provider URL + client_id + client_secret
- Login page: email input → domain lookup → redirect to IdP or show password form
- OIDC callback handler in `them-auth-go`: exchange code → validate ID token → upsert user → issue the-M JWT with tenant_id
- Test IdP: Keycloak in Docker (`docker-compose.dev.yml` profile `sso`)
- Files: `go/internal/authserver/oidc.go` (new), `go/cmd/auth-server/main.go`, `frontend/src/app/login/page.tsx`

**Scope:** Large (5–7 days). Requires Keycloak setup, OIDC flow, DB changes, frontend login page redesign.

---

### Gap 5 — Tenant Onboarding Flow (small)

**What:** A brand-new tenant admin logs in for the first time and sees an empty dashboard. They need to: set an LLM API key, create their first application, create their first entry point. Currently nothing guides them.

**Why it matters:** Without onboarding guidance, tenant admins hit a blank screen and don't know what to do first.

**What to build:**
- "Get started" banner on `/admin/applications` when `applications.length === 0` with 3-step checklist: 1. Set LLM key → 2. Create app → 3. Connect entry point
- No new backend needed — all the APIs exist

**Scope:** Small (0.5 days). Frontend only.

---

## Proposed Build Order

| Step | Name | Rationale |
|---|---|---|
| **33** | Tenant login verification + fix | Must confirm the chain works before any UX work has meaning |
| **34** | Tenant-scoped dashboard (role-based nav) | Makes multi-tenant visible and testable from the UI |
| **35** | Tenant provisioning wizard | Unblocks super_admin workflow; needed before real customer demos |
| **36** | Tenant onboarding flow | Small; improves first-login UX once login works |
| **37** | SSO / OIDC (Keycloak) | Large; defer until Steps 33–36 are proven |

---

## Key Design Decisions

**One user = one tenant (current):** `tenant_memberships` has `UNIQUE(user_id, tenant_id)` — one membership per user. This is the right constraint for now. Multi-tenant-per-user (e.g. a consultant) would require a tenant-switch UX and is deferred.

**Username/password first, SSO later:** Steps 33–36 use bcrypt passwords (already working). SSO is Gap 4 and requires Keycloak. No dependency between them — both paths issue the same JWT format.

**RLS is the isolation guarantee:** The application layer does NOT filter by tenant_id in queries — it sets a PG GUC (`app.tenant_id`) and lets RLS policies enforce isolation. This means a bug in the frontend nav (Gap 2) leaks no data — it's a UX problem, not a security problem.

**Tenant_id always from JWT, never URL/header:** `AdminTenantMiddleware` reads tenant_id from JWT claims only. This invariant must never be violated in new code.

---

## Working Demo Checklist

After Steps 33–35 are complete, the following must work:

- [ ] Super_admin logs in at `/login` → sees full admin nav (Tenants, Users, Observability)
- [ ] Super_admin creates tenant "Acme Corp" via New Tenant wizard
- [ ] Super_admin creates user `alice@acme.com`, assigns to "Acme Corp" as `admin`
- [ ] Alice logs in → JWT contains `tenant_id = <acme-uuid>`
- [ ] Alice sees only her tenant's nav (Applications, Runs, MCP, My Tenant — no Tenants/Users/Observability)
- [ ] Alice creates an application → it's created under Acme's tenant_id
- [ ] Super_admin logs in → sees Acme's application in observability summary
- [ ] Bob from a different tenant logs in → cannot see Acme's application (RLS blocks it)
- [ ] Alice's runs are quota-gated by Acme's quota limits, not the platform default
