# SSO Configuration — End-to-End Access Audit
# Last updated: 2026-09-06

---

## 1. Scope

This document audits the current state of SSO (OIDC) configuration access across the backend,
frontend, and planned roadmap. It answers:

- Which roles can read/write IdP config (both API and UI)?
- What does the login page do today?
- What are the security properties and gaps?
- What product model should Step 37 implement?

---

## 2. Backend — API Access Model

### 2a. PATCH /admin/tenants/{id} (super_admin only)

**Route:** `go/internal/admin/tenants.go` — `TenantHandler.Patch`
**Middleware chain:** JWT → `RequireSuperAdmin` (401/403 if not super_admin) → handler
**What it accepts:** Full `TenantPatch` including `idp_config` (SetIDP sentinel + IDPConfig struct)
**What it returns:** `TenantDetail` with `idp_configured bool` only — `idp_config` JSONB is **never** returned
**Secret handling:** `client_secret` is redacted from audit log; `client_secret_changed=true` sentinel written instead
**Storage:** `idp_config` written as plaintext JSONB to `them.tenants` — no DB-level encryption

### 2b. PATCH /tenant/settings (admin OR super_admin)

**Route:** `go/internal/admin/tenant_self_service.go` — `TenantSelfServiceHandler.PatchSettings`
**Middleware chain:** JWT → `RequireTenantAdmin` (admin OR super_admin passes) → `AdminTenantMiddleware` (reads tenant from JWT, never URL)
**What it accepts:** Same `TenantPatch` — slug and enabled are dropped server-side, but `idp_config` is **fully accepted**
**What it returns:** Same `TenantDetail` with `idp_configured bool` — no secrets exposed
**Secret handling:** Same audit-log redaction pattern as super_admin route (lines 70–78 of tenant_self_service.go)
**Scope gate:** Tenant admin can only configure their own tenant (tenant_id from JWT claim)

**Finding:** The backend already implements a **hybrid model** — both super_admin and tenant admin can configure the IdP. The two routes converge on the same DAL call (`db.PatchTenant`) with the same write semantics.

### 2c. GET /admin/tenants / GET /admin/tenants/{id}

**Route:** `go/internal/admin/tenants.go`
**Access:** `RequireSuperAdmin` only
**Returns:** `TenantDetail` — `idp_configured bool` only, no raw `idp_config` JSONB, no `client_secret`

### 2d. GET /tenant/settings

**Route:** `go/internal/admin/tenant_self_service.go` — `GetSettings`
**Access:** `RequireTenantAdmin`
**Returns:** Same `TenantDetail` shape — `idp_configured bool` only, no raw config

### 2e. OIDC flow — no auth required

**Routes:** `go/internal/authserver/oidc.go`
- `GET /auth/oidc/start?tenant={slug}` — **no authentication required**, public endpoint
- `GET /auth/oidc/callback` — validates HMAC state parameter, exchanges code, verifies RS256 id_token
- External OIDC access_token is **never exposed** outside the oidc package; the callback issues an internal HS256 JWT

**Platform role assignment:** `UpsertOIDCUser` in `oidc_store.go` always sets `platform_role = "viewer"` — regardless of IdP claims or group mappings. OIDC users can only get elevated membership roles (admin/member/viewer) via `tenant_group_mappings`, and `super_admin` is explicitly rejected.

**Group mapping privilege escalation — closed:** `validMemberRoles = {"admin","member","viewer"}` (migration 081, 2026-09-06). DB CHECK constraint enforces the same set. `super_admin` is not mappable via OIDC.

---

## 3. Frontend — Current State

### 3a. /tenant/settings (tenant admin self-service page)

**File:** `frontend/src/app/tenant/settings/page.tsx`
**Access:** Any authenticated user (guarded by `AuthGuard` only — no role check)
**What it shows:**
- Tenant slug (read-only)
- Status badge (read-only, text says "Status can only be changed by a platform admin")
- Display name (editable)
- Email domain (editable, labeled "for SSO routing")
- `IdP configured` badge (read-only boolean — shows Configured/Not configured)

**What is MISSING:** No form to input/update `idp_config` (discovery_url, client_id, client_secret, redirect_uri). The UI shows that SSO is configured/not, but gives the tenant admin no way to configure it.

**API call on save:** `themApi.patchTenantSettings({ display_name, email_domain })` — does not pass `idp_config`.

### 3b. /admin/tenants (super_admin only)

**File:** `frontend/src/app/admin/tenants/page.tsx` + `ProvisionWizard.tsx`
**What it exposes:** TenantPanel includes an "IdP" tab (from Step 10). That tab is the only current UI for writing `idp_config` — super_admin only.

### 3c. Login page

**File:** `frontend/src/app/login/page.tsx`
**Current behavior:**
- On email field blur, calls `/api/auth/tenant-lookup?email=...` (frontend proxy → `GET /auth/api/v1/auth/tenant-lookup`)
- If response has `idp_configured === true`, sets `showSSO = true` and renders an "SSO Login" button
- Clicking SSO Login: `window.location.href = /api/auth/oidc/start?tenant={slug}` (redirect to OIDC flow)
- The tenant-lookup → SSO redirect path **already works** — the login page email-first flow is already wired

**What Step 37 adds:** A Keycloak test IdP in Docker to exercise the full flow end-to-end. The login page code itself needs no changes (it already handles the SSO path).

---

## 4. Gap Summary

| Layer | Gap | Severity |
|---|---|---|
| Backend — write path | Tenant admin can write `idp_config` via `PATCH /tenant/settings` (backend supports hybrid model) | None — this is intended behavior |
| Backend — read path | `idp_config` JSONB is never returned; only `idp_configured bool` is exposed | None — correct; client_secret is write-only |
| Backend — storage | `client_secret` stored as plaintext JSONB in `them.tenants` | Medium — no DB-level encryption; mitigated by: never returned in responses, audit log redaction, DB access is already privileged |
| Frontend — /tenant/settings | No UI to write IdP config — tenant admin can't self-configure SSO through the UI (backend allows it, UI does not expose it) | Medium — missing feature for the hybrid model |
| Frontend — /admin/tenants | IdP tab exists (Step 10) for super_admin to configure tenant IdP | Complete |
| Login page | email-first flow + SSO redirect already wired (`showSSO` + `handleSSOLogin`) | Complete — needs Keycloak test IdP only |
| OIDC callback | platform_role always "viewer"; super_admin not mappable | Complete |
| Group mapping escalation | DB CHECK + app-layer guard close the privilege-escalation path | Complete (migration 081) |

---

## 5. Security Properties

**What is safe today:**
- `client_secret` is write-only — it is never returned by GET /admin/tenants, GET /tenant/settings, or audit log entries
- `admin` tenant users are scope-gated: they can only configure their own tenant's IdP (tenant_id from JWT, enforced by AdminTenantMiddleware)
- OIDC users cannot become super_admin via group mappings (double-guarded: app layer + DB CHECK)
- External OIDC tokens are never exposed outside the auth server

**What is a known risk:**
- `client_secret` is stored as plaintext in the `idp_config` JSONB column. Anyone with direct DB read access can extract it. This is acceptable for the current maturity level but should be noted as a future hardening item (encrypt at application layer before write, decrypt before use in oidc_store.go).

---

## 6. Recommended Product Model

**Hybrid model — already implemented in the backend; UI gap is on the tenant-admin side.**

The cleanest product model is:

| Actor | Configures | Via |
|---|---|---|
| super_admin | Any tenant's IdP | `/admin/tenants/{id}` → TenantPanel IdP tab (already complete) |
| tenant admin | Their own tenant's IdP | `/tenant/settings` → new "SSO / Identity Provider" section (to be built in Step 37) |

This hybrid model is the right choice because:
1. The backend already supports it — no Go changes needed
2. Enterprise customers expect to self-configure SSO (they hold the client_secret)
3. Platform admins can override/clear IdP config if a tenant misconfigures it
4. Scope is already enforced: tenant admin is JWT-scoped to their own tenant

**Step 37 should include** (in addition to Keycloak test IdP):
- Add an "SSO / Identity Provider" section to `/tenant/settings` with fields for `discovery_url`, `client_id`, `client_secret` (write-only input — show placeholder if already configured), `redirect_uri`
- Add a "Test SSO" button (initiates the OIDC flow in a new tab) so tenant admins can verify before going live
- Keep the `/admin/tenants` IdP tab as-is for super_admin override

**Step 37 should NOT:**
- Move IdP config out of `/tenant/settings` — the backend already handles it there
- Add a wizard step for SSO — the Provisioning Wizard (Step 35) is already complete and SSO is optional/advanced setup done after initial provisioning
- Encrypt client_secret at DB level in Step 37 — scope it as a separate hardening task if needed

---

## 7. Roadmap Documentation Changes Needed

| Doc | Change |
|---|---|
| `docs/MULTITENANT_PLAN.md` Gap 4 | Update Step 37 scope to include the `/tenant/settings` SSO form (tenant-admin self-service IdP config UI) in addition to Keycloak test IdP |
| `docs/MULTITENANT_PLAN.md` Gap 4 | Note that login page email-first flow is **already complete** — Step 37 needs Keycloak + tenant-settings SSO form only |
| `docs/STATUS.md` | Add: client_secret plaintext storage — known risk, not a blocker, future hardening item |

---

## 8. Files Audited

| File | Role |
|---|---|
| `go/internal/authserver/oidc.go` | OIDC start/callback — public, no auth required |
| `go/internal/authserver/oidc_store.go` | idp_config read; UpsertOIDCUser (platform_role always viewer) |
| `go/internal/admin/router.go` | Middleware chain: RequireSuperAdmin vs RequireTenantAdmin |
| `go/internal/auth/middleware.go` | RequireSuperAdmin + RequireTenantAdmin definitions |
| `go/internal/admin/tenants.go` | PATCH /admin/tenants/{id} — super_admin only |
| `go/internal/admin/dal/tenants.go` | PatchTenant; client_secret never returned; plaintext JSONB storage |
| `go/internal/admin/tenant_self_service.go` | PATCH /tenant/settings — RequireTenantAdmin; accepts idp_config |
| `frontend/src/app/tenant/settings/page.tsx` | Tenant admin UI — shows idp_configured badge, no write form |
| `frontend/src/app/login/page.tsx` | Email-first flow + SSO redirect already wired |
| `frontend/src/lib/api.ts` | themApi surface — patchTenantSettings does not pass idp_config |
| `docs/MULTITENANT_PLAN.md` | Gap 4 / Step 37 scope |
