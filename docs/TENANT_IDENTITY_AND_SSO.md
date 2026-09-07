# Tenant Identity & SSO in the-M
# Last updated: 2026-09-07

---

## Start here — the plain-language version

**Tenant** = a company or team that uses the-M. Each tenant has its own agents,
applications, and users. One tenant cannot see another tenant's data — ever.

**User** = a person who logs into the-M. Every user belongs to at least one tenant
and has a role that controls what they can do inside that tenant.

**SSO (Single Sign-On)** = instead of remembering a password in the-M, a user logs in
through their company's existing identity system (Google, Microsoft, Okta, Keycloak, etc.).
The-M never sees their password — it just receives a "yes, this person is who they say
they are" confirmation from the company's identity system.

---

## What is a Tenant?

Think of each tenant as a completely separate workspace. If you're Acme Corp, your
agents, applications, and run history are invisible to everyone outside Acme Corp —
even to other tenants on the same the-M installation.

This isolation is enforced at the **database level** (PostgreSQL Row-Level Security),
not just in the application. Even if there were a bug in the API code, the database
itself would block cross-tenant data leaks.

Every tenant has:
- A **slug** — a short, URL-safe ID (e.g. `acme-corp`)
- A **display name** — human-readable (e.g. `Acme Corporation`)
- Optionally: an **SSO configuration** (see below)
- Optionally: an **email domain** (so users at `@acme.com` are automatically routed to the right tenant's login page)

A built-in `default` tenant exists for the platform super-admin. New tenants are
created by the super-admin via the admin API.

---

## Users and Roles

Every user in the-M has **two separate roles** that control two different things:

### 1. Platform role — what you can do across the whole system

| Role | Who it's for |
|---|---|
| `super_admin` | Platform operator — can create tenants, see everything |
| `developer` | Platform-level developer access |
| `analyst` | Read-only platform access |
| `viewer` | Minimal platform access |

This role is **not** in the login token. It controls things like "can this user
create new tenants?"

### 2. Membership role — what you can do inside a specific tenant

| Role | What it can do |
|---|---|
| `super_admin` | Everything, including cross-tenant admin |
| `admin` | Manage agents, apps, orchestrators, settings for this tenant |
| `member` | Create and edit resources inside the tenant |
| `viewer` | Read-only access inside the tenant |

This role **is** in the login token and is checked on every API call. When you log
into a tenant, the system issues you a token that says "this person is an `admin` in
tenant `acme-corp`".

A single user can belong to multiple tenants with different roles in each.

### Creating a user manually (super-admin only)

```http
POST /auth/api/v1/admin/users
Authorization: Bearer <super-admin-token>
Content-Type: application/json

{
  "username":    "alice@acme.com",
  "name":        "Alice Smith",
  "password":    "SecurePass1!",
  "role":        "viewer",      <- platform role
  "tenant_role": "admin",       <- role inside the tenant
  "tenant_id":   "the-tenant-uuid-here"
}
```

If a user has no tenant membership, they cannot log in at all.

---

## SSO — How It Works (Plain Language)

Without SSO, a user logs in with a username + password stored in the-M.

With SSO, the flow is:

1. User clicks "Log in with SSO" on the-M's login page
2. The-M redirects the browser to the company's identity provider (e.g. Keycloak, Google, Okta)
3. The user logs in there — the-M never sees the password
4. The identity provider tells the-M: "yes, this is alice@acme.com, and she's in the `engineers` group"
5. The-M looks up or creates Alice's account, assigns her the right tenant role (based on her group), and logs her in

From Alice's perspective: she just clicked one button and ended up logged into the-M.
She doesn't have a separate the-M password to remember.

### What "just-in-time provisioning" means

The-M does **not** require you to pre-create users before they can log in via SSO.
The first time Alice logs in through SSO, the-M automatically creates her account.
On subsequent logins it updates her name if it changed. This is called JIT provisioning.

### Group → Role mapping

If the identity provider sends a list of groups (e.g. `["engineers", "admins"]`),
the-M can map those to tenant roles automatically. You configure this mapping in the
`them.tenant_group_mappings` table. If no group matches, the user gets `viewer` access.

Note: no IdP group can ever grant `super_admin` — that role can only be assigned manually.

---

## The Full SSO Login Flow (Technical Detail)

This section is for developers who want to understand what happens under the hood.
You can skip this if you just want to use or test SSO.

```
Browser                    the-M (auth-go)              Identity Provider
  │                              │                              │
  │ Open /auth/oidc/start        │                              │
  │   ?tenant=acme-corp          │                              │
  │ ───────────────────────────► │                              │
  │                              │ Load tenant's SSO config     │
  │                              │ Fetch IdP discovery doc      │
  │                              │ ──────────────────────────── ►│
  │                              │ ◄────────────────────────────│
  │                              │ Generate random PKCE secret  │
  │                              │ Sign a state token (HMAC)    │
  │                              │ Set one-time cookie          │
  │ 302 → IdP login page         │                              │
  │ ◄─────────────────────────── │                              │
  │                                                             │
  │ User types password at IdP                                  │
  │ ──────────────────────────────────────────────────────────► │
  │                                                             │
  │ 302 → /auth/oidc/callback?code=XXX&state=YYY               │
  │ ◄────────────────────────────────────────────────────────── │
  │                              │                              │
  │ GET /auth/oidc/callback      │                              │
  │ ───────────────────────────► │                              │
  │                              │ Verify state signature       │
  │                              │ Exchange code for tokens     │
  │                              │ ──────────────────────────── ►│
  │                              │ ◄────────────────────────────│
  │                              │ Verify identity token        │
  │                              │ Create/update user in DB     │
  │                              │ Issue the-M session cookie   │
  │ 302 → / (dashboard)          │                              │
  │ ◄─────────────────────────── │                              │
```

Key security properties:
- **PKCE** prevents code interception attacks — the one-time secret proves the browser that started the flow is the same one completing it
- **Signed state** prevents CSRF — the state token is HMAC-signed so the callback cannot be forged
- **The external IdP token is never exposed** — the-M immediately converts it to its own internal session cookie (`them_access_token`). The browser only ever sees the-M's own cookie.

---

## What's in the Login Token (JWT)

After login — whether via password or SSO — the browser receives a `them_access_token`
cookie. It's a signed JWT (JSON Web Token) that contains:

```json
{
  "sub":       "42",                                   <- internal user ID
  "username":  "alice@acme.com",
  "name":      "Alice Smith",
  "role":      "admin",                               <- membership role in this tenant
  "tenant_id": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", <- which tenant
  "exp":       1234567890,                             <- expiry timestamp
  "type":      "access"
}
```

Every API call the-M makes uses this token to know: who is this person, which tenant
do they belong to, and what are they allowed to do.

---

## Setting Up SSO for a Tenant

### Step 1 — Start Keycloak (local dev only)

Keycloak is the test identity provider included with the dev stack:

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile sso up -d them-keycloak
```

For production, you'd point to your real IdP (Google, Okta, Azure AD, etc.) instead.

### Step 2 — Save the SSO config on the tenant

A tenant admin (or super-admin) calls:

```http
PATCH /api/v1/tenant/settings
Authorization: Bearer <tenant-admin-token>
Content-Type: application/json

{
  "idp_config": {
    "discovery_url": "http://them-keycloak:8080/auth/keycloak/realms/them",
    "client_id":     "them-m",
    "client_secret": "them-m-secret",
    "redirect_uri":  "http://localhost:8088/auth/api/v1/auth/oidc/callback"
  }
}
```

> **Local dev gotcha:** Use `http://them-keycloak:8080/...` (the Docker-internal hostname)
> for `discovery_url` — NOT `http://localhost:8088/...`. The auth-go service fetches
> this URL from inside the Docker network, where `localhost` refers to the container
> itself, not your laptop.

The response will include `"idp_configured": true` to confirm it worked.

### Step 3 — Test the redirect

```bash
curl -v "http://localhost:8088/auth/oidc/start?tenant=your-tenant-slug"
# You should see: HTTP/1.1 302 Found
# Location: http://...keycloak.../auth?response_type=code&...
```

### Step 4 — Do the browser login

Open this URL in your browser:
```
http://localhost:8088/auth/oidc/start?tenant=your-tenant-slug
```

You'll be taken to Keycloak. Log in with:
- Email: `testuser@example.com`
- Password: `testpass`

You should end up back at the-M's dashboard, logged in as that user.

To confirm it worked:
1. Open browser DevTools → Application → Cookies
2. Check that `them_access_token` is set
3. Call `GET http://localhost:8088/auth/api/v1/auth/me` — it should return the user's
   name, role, and tenant_id

### Step 5 — Remove SSO config (if needed)

```http
PATCH /api/v1/tenant/settings
Authorization: Bearer <tenant-admin-token>

{ "idp_config": null }
```

---

## Email Domain Routing

If you set an email domain on a tenant, the login page can automatically detect which
tenant a user belongs to and show them the right login option (SSO vs password):

```http
PATCH /api/v1/tenant/settings
{ "email_domain": "acme.com" }
```

Then the login page can call:
```
GET /auth/api/v1/auth/tenant-lookup?email=alice@acme.com
```

Response:
```json
{
  "slug": "acme-corp",
  "display_name": "Acme Corporation",
  "idp_configured": true
}
```

If `idp_configured` is true, redirect the user to `/auth/oidc/start?tenant=acme-corp`.
If false, show the password form.

---

## What the Test Script Checks

Run the full automation script:
```bash
python3.12 scripts/tests/test_38_multitenant.py
```

It runs **74 checks** across 11 sections and exits 0 on full pass.

### What it proves automatically

| Section | What it verifies |
|---|---|
| S0 | Auth service and API are reachable |
| S1 | Super-admin can log in; JWT has `role=super_admin`; admin-only APIs accessible |
| S2 | Create a tenant, create a tenant-admin user, set quota — all work |
| S3 | Tenant-admin gets a token scoped to their tenant; cannot access super-admin APIs (403) |
| S4 | Tenant admin can create agents and apps; they appear in their list |
| S5 | Quota is enforced — creating a 4th agent when limit=3 is rejected |
| S6 | Super-admin sees all tenants in the observability summary |
| S7 | Tenant settings (email_domain) can be updated; slug cannot be changed |
| S8 | Refreshing the token preserves the same tenant and role |
| S9 | A tenant-admin cannot see agents created in a different tenant |
| S10 | Deleting a tenant: tenant disappears, former user cannot log in |
| S11 | SSO config save/clear; Keycloak redirect works; email-domain lookup works |

### What the script cannot test (must be done manually in a browser)

The script cannot drive a browser, so these steps require a human:

| What | Why it needs a browser |
|---|---|
| Clicking through the Keycloak login page | Requires real browser interaction |
| The full SSO callback completing | The browser carries a one-time security cookie that curl cannot replicate |
| Verifying `them_access_token` is set in the browser | Cookie is HttpOnly — invisible to scripts |
| Verifying the UI loads correctly after SSO login | Front-end rendering |
| Checking that a new user was auto-created in the DB after first SSO login | Depends on the full callback completing |

**For the browser test:** follow Step 4 in "Setting Up SSO" above. The whole thing takes
about 2 minutes.

---

## Keycloak Reference (Local Dev)

| Item | Value |
|---|---|
| Realm | `them` |
| Client ID | `them-m` |
| Client secret | `them-m-secret` |
| Test user | `testuser@example.com` / `testpass` |
| External URL (use from browser / host) | `http://localhost:8088/auth/keycloak/realms/them` |
| Internal URL (use in `discovery_url` config) | `http://them-keycloak:8080/auth/keycloak/realms/them` |
| Start command | `docker compose ... --profile sso up -d them-keycloak` |
| Verify it's up | `curl http://localhost:8088/auth/keycloak/realms/them` → should return JSON with `realm: "them"` |
