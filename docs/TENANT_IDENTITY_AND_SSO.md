# Tenant Identity & SSO in the-M
# Last updated: 2026-09-07

This document explains how tenants work in the-M, how their users are managed, and how
SSO/OIDC integrations operate end-to-end. It also describes what the automation test
script verifies and what still requires manual testing.

---

## What is a Tenant?

A **tenant** is the isolation boundary in the-M. Every agent, application, run, audit log,
and access token belongs to exactly one tenant. PostgreSQL Row-Level Security (RLS) enforces
this at the database level — a tenant-scoped DB session can only read and write its own rows,
regardless of the application code.

Key fields in `them.tenants`:

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | Primary key, gen_random_uuid |
| `slug` | TEXT UNIQUE | URL-safe identifier, `^[a-z0-9_-]{1,64}$` |
| `display_name` | TEXT | Human-readable name, required |
| `idp_config` | JSONB NULL | OIDC provider config — NULL means no SSO |
| `email_domain` | TEXT NULL UNIQUE | Used for email-first tenant discovery |
| `enabled` | BOOLEAN | Disabled tenants cannot initiate OIDC flows |
| `is_bootstrap` | BOOLEAN | True only for the built-in `default` tenant |

The `default` tenant (id `00000000-0000-0000-0000-000000000001`) is seeded at DB init.
New tenants are created by a super-admin via `POST /api/v1/admin/tenants`.

---

## Two-Pool Architecture

| Pool | DB Role | RLS | Used by |
|---|---|---|---|
| Admin pool | `them_admin` | BYPASSRLS | Super-admin routes; cross-tenant reads |
| App pool | `them_app` | Enforced | All tenant-scoped operations |

Every tenant request goes through `BeginTenantTx`, which sets `app.tenant_id` in the
PostgreSQL session before any query runs. RLS policies then restrict all reads and writes
to rows matching that tenant ID. The `tenant_id` comes from the JWT claim — never from a
request header or query parameter.

---

## User Model

Users live in the `auth_service` schema. Every user has **two independent roles**:

### Platform role
Stored in `auth_service.users.role_id` → `auth_service.roles`.
Values: `super_admin`, `developer`, `analyst`, `viewer`.
Controls platform-level capabilities (e.g. can this user see all tenants?).
**Not included in the JWT.**

### Membership role (tenant role)
Stored in `auth_service.tenant_memberships.role` — one row per user per tenant.
Values: `super_admin`, `admin`, `member`, `viewer`.
This is the `role` claim in the JWT and controls per-tenant access:

| Membership role | What it can do |
|---|---|
| `super_admin` | All routes including cross-tenant admin |
| `admin` | All tenant routes (agents, apps, orchestrators, settings) |
| `member` | Read/write tenant resources (no admin-only routes) |
| `viewer` | Read-only tenant resources |

A user can have memberships in multiple tenants. At login, the membership is selected
based on the tenant context (either `tenant_slug` in the login body, or the user's
primary membership if they only belong to one tenant).

### Creating a tenant user (super-admin only)

```bash
POST /auth/api/v1/admin/users
Authorization: Bearer <super-admin-token>

{
  "username":    "alice@acme.com",
  "name":        "Alice Smith",
  "password":    "SecurePass1!",
  "role":        "viewer",        # platform role
  "tenant_role": "admin",         # membership role for this tenant
  "tenant_id":   "<tenant-uuid>"
}
```

Login is blocked if the user has no membership row for any tenant.

---

## SSO / OIDC Integration

### How it works

Each tenant can have one OIDC provider configured. The config is stored encrypted in
`them.tenants.idp_config` JSONB:

```json
{
  "discovery_url": "https://idp.example.com/realm",
  "client_id":     "them-m",
  "client_secret": "...",
  "redirect_uri":  "https://your-host/auth/api/v1/auth/oidc/callback"
}
```

The full flow uses **PKCE (S256)** with a signed state token:

```
Browser                     the-M (auth-go)                IdP (Keycloak / etc.)
  │                               │                               │
  │  GET /auth/oidc/start         │                               │
  │    ?tenant=acme               │                               │
  │ ──────────────────────────► │                               │
  │                               │  Fetch discovery doc          │
  │                               │ ────────────────────────────► │
  │                               │ ◄──────────────────────────── │
  │                               │  Generate PKCE verifier        │
  │                               │  Sign state token              │
  │                               │  Set them_oidc_state cookie    │
  │  302 → IdP /authorize          │                               │
  │ ◄──────────────────────────── │                               │
  │                                                               │
  │  User logs in at IdP                                          │
  │ ──────────────────────────────────────────────────────────── ►│
  │                                                               │
  │  302 → /auth/oidc/callback?code=AUTH_CODE&state=SIGNED        │
  │ ◄──────────────────────────────────────────────────────────── │
  │                               │                               │
  │  GET /auth/oidc/callback      │                               │
  │ ──────────────────────────► │                               │
  │                               │  Verify state HMAC            │
  │                               │  Read code_verifier cookie    │
  │                               │  Exchange code at token EP    │
  │                               │ ────────────────────────────► │
  │                               │ ◄──────────────────────────── │
  │                               │  Verify id_token (RS256/JWKS) │
  │                               │  JIT provision user           │
  │                               │  Issue internal HS256 JWT     │
  │                               │  Set them_access_token cookie │
  │  302 → /                      │                               │
  │ ◄──────────────────────────── │                               │
```

### JIT (just-in-time) user provisioning

On a successful OIDC callback, the auth-go service **automatically creates or updates
the user** — no pre-provisioning required:

```sql
-- UpsertOIDCUser
INSERT INTO auth_service.users (email, name, username, ...)
ON CONFLICT (email) DO UPDATE SET name=..., active=true;

INSERT INTO auth_service.tenant_memberships (user_id, tenant_id, role)
ON CONFLICT (user_id, tenant_id) DO UPDATE SET role=<mapped-role>;
```

Platform role is always `viewer` for OIDC-provisioned users. The **membership role** is
determined by OIDC group claims:

- The IdP must return a `groups` claim (list of strings)
- `them.tenant_group_mappings` maps group names → membership roles
- `super_admin` mapping is rejected (OIDC cannot escalate to super_admin)
- If no group matches, the user gets `viewer` membership

### JWT issued after OIDC login

```json
{
  "sub":       "42",
  "username":  "alice@acme.com",
  "name":      "Alice Smith",
  "role":      "admin",
  "tenant_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "exp":       1234567890,
  "type":      "access"
}
```

The external OIDC token is **never exposed**. The browser only ever sees the internal
HS256 `them_access_token` cookie.

### Configuring SSO for a tenant

**Step 1 — Start Keycloak (local dev only):**
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile sso up -d them-keycloak
```

**Step 2 — Write IdP config to the tenant (tenant-admin or super-admin):**
```bash
PATCH /api/v1/tenant/settings
Authorization: Bearer <tenant-admin-token>

{
  "idp_config": {
    "discovery_url": "http://them-keycloak:8080/auth/keycloak/realms/them",
    "client_id":     "them-m",
    "client_secret": "them-m-secret",
    "redirect_uri":  "http://localhost:8088/auth/api/v1/auth/oidc/callback"
  }
}
```

> **Important:** Use the **internal Docker hostname** (`them-keycloak:8080`) for
> `discovery_url` when running locally. The auth-go container fetches this URL from
> inside Docker — `localhost:8088` is not reachable from inside a container.

Response includes `idp_configured: true` when the config was saved.

**Step 3 — Verify redirect works:**
```bash
curl -v "http://localhost:8088/auth/oidc/start?tenant=<your-slug>"
# Expect: HTTP 302 → IdP /authorize URL
```

**Step 4 — Browser SSO login (manual):**
Open `http://localhost:8088/auth/oidc/start?tenant=<your-slug>` in a browser.
Local Keycloak test credentials: `testuser@example.com` / `testpass`.

**To clear SSO config:**
```bash
PATCH /api/v1/tenant/settings
Authorization: Bearer <tenant-admin-token>

{ "idp_config": null }
```

### Tenant discovery by email domain

Set `email_domain` on the tenant to allow the login UI to route users automatically:
```bash
PATCH /api/v1/tenant/settings
{ "email_domain": "acme.com" }
```

Then `GET /auth/api/v1/auth/tenant-lookup?email=alice@acme.com` returns the tenant's
slug and whether SSO is configured — the login page uses this to decide whether to show
the password form or redirect to SSO.

---

## What the Test Script Covers

Run: `python3.12 scripts/tests/test_38_multitenant.py`

The script has 74 automated checks across 11 sections.

### SSO-specific checks (S11) — all automated

| Check | What it proves |
|---|---|
| Keycloak discovery endpoint reachable | Traefik proxies `/auth/keycloak` to Keycloak correctly |
| `PATCH /tenant/settings` with `idp_config` → 200, `idp_configured=true` | IdP config is stored and the flag is returned |
| Re-PATCH confirms `idp_configured=true` persists | Config survives a second write |
| `GET /auth/oidc/start?tenant=slug` returns 302 to Keycloak | PKCE generation + discovery fetch work; the redirect URL points to Keycloak |
| `GET /auth/tenant-lookup?email=...@example.com` returns tenant with `idp_configured=true` | Email-domain routing works |
| `PATCH /tenant/settings` with `idp_config=null` → `idp_configured=false` | Config can be cleared |

### What the script does NOT cover (manual only)

| Step | Why it can't be automated |
|---|---|
| Clicking through Keycloak login UI | Requires a real browser session |
| OIDC callback (`/oidc/callback?code=...&state=...`) | Requires the browser to carry the `them_oidc_state` cookie set by `/oidc/start` |
| Verifying `them_access_token` cookie is set after callback | Cookie is HttpOnly; only a browser can observe this flow end-to-end |
| Verifying `/me` returns the JIT-provisioned OIDC user | Depends on the callback having completed |
| Group mapping (IdP groups → membership role) | Requires IdP to be configured to return `groups` claim |
| JIT user creation in `auth_service.users` | Depends on the full callback flow completing |

### Manual test procedure for the browser OIDC flow

1. Start the SSO profile: `docker compose ... --profile sso up -d them-keycloak`
2. Configure IdP on a test tenant (see above, or run the script which does this in S11)
3. Open in a browser: `http://localhost:8088/auth/oidc/start?tenant=mt-test-sso`
4. You should be redirected to Keycloak's login page
5. Log in with `testuser@example.com` / `testpass`
6. You should be redirected back and land on the the-M UI (`/`)
7. Open DevTools → Application → Cookies — verify `them_access_token` is set
8. Call `GET /auth/api/v1/auth/me` (cookie is sent automatically) — verify:
   - `role` matches the group mapping (or `viewer` if no groups configured)
   - `tenant_id` matches your test tenant
9. Optionally: check `auth_service.users` in psql — the OIDC user row should exist

---

## Other Tenant-Scoped Checks (S0–S10)

Beyond SSO, the script verifies the full multi-tenant isolation story:

| Section | What it proves |
|---|---|
| S2 | Tenant create, user create, quota set |
| S3 | Tenant-admin JWT has correct `role` and `tenant_id`; super-admin routes return 403 |
| S4 | Agents and apps created by TA are visible only to TA (RLS); SA cannot see them by default |
| S5 | Quota enforcement — 4th agent rejected when `max_agents=3`; 3rd app rejected when `max_apps=2` |
| S6 | Super-admin sees all tenants in observability summary |
| S7 | Tenant self-service: `PATCH /tenant/settings` updates `email_domain`; slug is read-only |
| S8 | Refreshed token carries same `tenant_id` and `role` |
| S9 | Cross-tenant isolation: TA cannot see SA's agents; unauthenticated/bad JWT rejected |
| S10 | Tenant deletion cascades: tenant gone, TA login fails |

---

## Keycloak Local Dev Reference

| Parameter | Value |
|---|---|
| Realm | `them` |
| Client ID | `them-m` |
| Client secret | `them-m-secret` |
| Test user email | `testuser@example.com` |
| Test user password | `testpass` |
| External discovery URL | `http://localhost:8088/auth/keycloak/realms/them` |
| Internal discovery URL | `http://them-keycloak:8080/auth/keycloak/realms/them` |
| Use internal URL in | `idp_config.discovery_url` (stored in DB; fetched by auth-go from inside Docker) |
| Use external URL for | Browser navigation, smoke-testing from host |
