# Tenant Identity & SSO in the-M
# Last updated: 2026-09-07

This guide is written for someone with no prior SSO experience.
It covers what tenants are, how users are managed, how SSO login works,
and how to test it end-to-end — including exactly what the automation
script covers and what you still need to do manually in a browser.

---

## Part 1 — Tenants

### What is a tenant?

A **tenant** is a completely isolated workspace. Think of it like separate offices in
a shared building — people in office A cannot see or touch anything in office B.

In the-M, each tenant has its own:
- Agents
- Applications
- Runs and history
- Users

This isolation is enforced at the **database level**, not just in the code. Even if
there were a bug in the API, the database itself would block data from crossing tenant
boundaries.

### How tenants are created

Only a **super-admin** can create tenants. There is no self-service tenant signup.

```
Super-admin calls:
POST /api/v1/admin/tenants
{ "slug": "acme-corp", "display_name": "Acme Corporation" }
```

The `slug` is a short identifier used in URLs (`acme-corp`).
The `display_name` is the human-readable name shown in the UI.

A built-in `default` tenant exists for the platform operator. It cannot be deleted.

---

## Part 2 — Users and Roles

### Every user has two roles

This is the most important thing to understand, and it trips people up.

**Role 1 — Platform role** (what you can do across the whole system):

| Role | Meaning |
|---|---|
| `super_admin` | Can create tenants, see all tenants, manage everything |
| `developer` | Platform-level developer access |
| `analyst` | Read-only platform access |
| `viewer` | Minimal platform access |

This role is **never shown in your login token**. It controls things like "can this
person create new tenants?"

**Role 2 — Membership role** (what you can do inside one specific tenant):

| Role | Meaning |
|---|---|
| `admin` | Manage agents, apps, orchestrators, settings for this tenant |
| `member` | Create and edit resources inside the tenant |
| `viewer` | Read-only access inside the tenant |

This role **is** in your login token and is checked on every API call. When you log
in, the token says something like: "this person is an `admin` in tenant `acme-corp`."

A user can belong to multiple tenants, with a different membership role in each.

### Creating a user (super-admin only)

Users are created via the Users section in the admin UI, or via API:

```http
POST /auth/api/v1/admin/users
Authorization: Bearer <super-admin-token>
Content-Type: application/json

{
  "username":    "alice",
  "name":        "Alice Smith",
  "password":    "SecurePass1!",
  "role":        "viewer",         ← platform role
  "tenant_role": "admin",          ← membership role inside the tenant
  "tenant_id":   "<tenant-uuid>"
}
```

A user with no tenant membership **cannot log in at all**.

---

## Part 3 — SSO (Single Sign-On)

### What SSO means in plain language

Without SSO, Alice has a username and password stored in the-M. She types them on the
the-M login page.

With SSO, Alice's company already has an identity system — maybe Google Workspace,
Microsoft, Okta, or Keycloak. Instead of a separate the-M password, Alice clicks
"Log in with SSO", gets taken to her company's login page, authenticates there, and
lands back in the-M — already logged in. The-M never sees her password.

The-M supports any identity provider that implements the **OpenID Connect** standard
(Google, Microsoft, Okta, Keycloak, Auth0, and many others).

### SSO is configured per-tenant

Each tenant can have **one** SSO provider configured. A tenant either uses SSO or
username/password — not both at the same time.

The SSO configuration lives on the tenant's settings page (Settings → SSO / Identity
Provider tab). It has four fields:

| Field | What to put here |
|---|---|
| Discovery URL | The base URL of your identity provider's realm/tenant. The-M fetches configuration from this URL automatically. |
| Client ID | The application ID you registered in your identity provider |
| Client Secret | The secret key from your identity provider registration |
| Redirect URI | Where the identity provider sends the user back after login — always `https://your-host/auth/api/v1/auth/oidc/callback` |

### What "JIT provisioning" means

JIT stands for **Just-In-Time**. It means: the-M creates the user's account
automatically the first time they log in via SSO.

You do not need to pre-create every user. The flow is:

1. Alice logs in via SSO for the first time
2. The identity provider confirms she is `alice@acme.com`
3. The-M automatically creates an account for Alice with `viewer` membership in the tenant
4. Alice is now logged in

On Alice's second login, the-M updates her display name if it changed — but she is
not re-created.

**Alice always gets at least `viewer` access.** SSO login is never rejected because of
missing role configuration — the worst case is read-only access.

If you want Alice to have `admin` or `member` access, you have two options:
- After her first login, a super-admin promotes her via the Users API
- Or use group mappings (see below)

### Group mappings — what they are and how to set them

Your identity provider can send a list of **groups** that Alice belongs to (e.g.
`["engineers", "team-leads"]`). The-M can automatically translate those groups into
tenant membership roles.

**Important: there is no UI for this yet.** Group mappings are configured via API
only, by a super-admin.

```http
PUT /api/v1/admin/tenants/{tenant-id}/group-mappings
Authorization: Bearer <super-admin-token>
Content-Type: application/json

[
  { "group_claim": "team-leads", "role": "admin",  "priority": 1 },
  { "group_claim": "engineers",  "role": "member", "priority": 2 }
]
```

When Alice logs in and her identity provider says she's in `team-leads`, she gets
`admin` membership. If she's only in `engineers`, she gets `member`. If she's in
neither, she gets `viewer`.

`priority` matters when a user is in multiple matching groups — the lowest number wins.

Super-admin can never be granted via group mapping — that role can only be assigned
manually.

If you don't configure any group mappings, all SSO users get `viewer` and a super-admin
can promote them individually afterward.

---

## Part 4 — The SSO Login Flow Step by Step

This is what actually happens when Alice clicks "Log in with SSO":

```
1. Alice's browser → the-M:  "Start SSO for tenant acme-corp"
   URL: GET /auth/oidc/start?tenant=acme-corp

2. The-M → Identity Provider:  "Give me your configuration"
   (fetches the discovery document — a JSON file at the identity provider's URL
    that lists all the endpoints the-M needs)

3. The-M → Alice's browser:  "Go log in here"
   (redirects to the identity provider's login page, along with a one-time
    security token so the callback can't be faked)

4. Alice logs in at the identity provider (the-M never sees this)

5. Identity Provider → Alice's browser:  "Here's a one-time code, go back to the-M"
   (redirects to /auth/oidc/callback?code=ONE_TIME_CODE&state=SECURITY_TOKEN)

6. Alice's browser → the-M:  "Here's my code"

7. The-M → Identity Provider:  "Exchange this code for identity information"
   (uses the code to get Alice's verified identity: email, name, groups)

8. The-M:
   - Verifies the identity (cryptographic signature check)
   - Creates or updates Alice's account in the database (JIT provisioning)
   - Assigns her tenant membership role (from group mappings, or viewer by default)
   - Issues a the-M session cookie

9. The-M → Alice's browser:  "You're in, go to the dashboard"
```

**The identity provider's tokens are never exposed to Alice or stored long-term.**
The-M immediately converts them into its own short-lived session cookie (`them_access_token`).

---

## Part 5 — Testing SSO End-to-End

### What you need

The local dev stack includes **Keycloak** as a test identity provider. Start it with:

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  --profile sso up -d them-keycloak

# Verify it's running:
curl http://localhost:8088/auth/keycloak/realms/them
# Should return JSON with "realm": "them"
```

Keycloak test credentials:
| Item | Value |
|---|---|
| Test user email | `testuser@example.com` |
| Test user password | `testpass` |

### Step 1 — Create a tenant

In the UI: go to Admin → Tenants → Create, or via API:
```bash
# Get a super-admin token first
TOKEN=$(curl -s -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

# Create the tenant
curl -s -X POST http://localhost:8088/api/v1/admin/tenants \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"acme-corp","display_name":"Acme Corporation"}'
```

Note the `id` in the response — you'll need it.

### Step 2 — Create a tenant admin user

```bash
curl -s -X POST http://localhost:8088/auth/api/v1/admin/users \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "username":    "alice",
    "name":        "Alice Smith",
    "password":    "AlicePass1!",
    "role":        "viewer",
    "tenant_role": "admin",
    "tenant_id":   "<tenant-id-from-step-1>"
  }'
```

### Step 3 — Log in as the tenant admin and verify

```bash
ALICE=$(curl -s -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"AlicePass1!"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

# Check what's in the token (decode the JWT payload)
echo $ALICE | cut -d. -f2 | base64 -d 2>/dev/null | python3 -m json.tool
# Should show: "role": "admin", "tenant_id": "<your-tenant-id>"
```

### Step 4 — Configure SSO on the tenant

Log in to the UI as Alice (or as super-admin), go to Settings → SSO / Identity Provider,
and fill in:

| Field | Value for local dev |
|---|---|
| Discovery URL | `http://them-keycloak:8080/auth/keycloak/realms/them` |
| Client ID | `them-m` |
| Client Secret | `them-m-secret` |
| Redirect URI | `http://localhost:8088/auth/api/v1/auth/oidc/callback` |

> **Why the internal URL?** The-M's auth service runs inside Docker. When it fetches
> the discovery document, it goes to `them-keycloak:8080` — the Docker-internal
> hostname. If you put `localhost:8088` here, it would try to connect to itself
> (the container), not to Keycloak, and fail with a 502 error.

Or via API using Alice's token:
```bash
curl -s -X PATCH http://localhost:8088/api/v1/tenant/settings \
  -H "Authorization: Bearer $ALICE" \
  -H "Content-Type: application/json" \
  -d '{
    "idp_config": {
      "discovery_url": "http://them-keycloak:8080/auth/keycloak/realms/them",
      "client_id":     "them-m",
      "client_secret": "them-m-secret",
      "redirect_uri":  "http://localhost:8088/auth/api/v1/auth/oidc/callback"
    }
  }'
# Response should include: "idp_configured": true
```

### Step 5 — Verify the redirect works (no browser needed)

```bash
curl -v "http://localhost:8088/auth/oidc/start?tenant=acme-corp" 2>&1 | grep -E "< HTTP|Location:"
# Should show: HTTP/1.1 302
# Location: http://...keycloak.../realms/them/protocol/openid-connect/auth?...
```

If you see 302 with a Keycloak URL in Location, the SSO config is working correctly.

### Step 6 — Do the actual browser login

This part cannot be automated — it requires a real browser.

1. Open: `http://localhost:8088/auth/oidc/start?tenant=acme-corp`
2. You'll be redirected to a Keycloak login page
3. Enter: `testuser@example.com` / `testpass`
4. You should land back on the the-M dashboard

**To confirm it worked:**

Open browser DevTools (F12) → Application tab → Cookies → find `them_access_token`.
It should be set.

Then open the browser console and run:
```javascript
// Decode and read your session token
JSON.parse(atob(document.cookie.match(/them_access_token=([^;]+)/)[1].split('.')[1]))
```
You should see your `role` and `tenant_id`.

Or call the /me endpoint from the browser address bar (it uses the cookie automatically):
```
http://localhost:8088/auth/api/v1/auth/me
```

You should see:
```json
{
  "id": 5,
  "username": "testuser@example.com",
  "name": "Test User",
  "role": "viewer",
  "tenant_id": "<your-tenant-id>"
}
```

Role is `viewer` because no group mappings are configured for the Keycloak test user.
A super-admin can promote the user afterward if needed.

### Step 7 — Optionally set email domain routing

If you want users at `@acme.com` to be automatically routed to the Acme SSO login:

```bash
curl -s -X PATCH http://localhost:8088/api/v1/tenant/settings \
  -H "Authorization: Bearer $ALICE" \
  -H "Content-Type: application/json" \
  -d '{"email_domain": "acme.com"}'
```

Then test the lookup:
```bash
curl "http://localhost:8088/auth/api/v1/auth/tenant-lookup?email=anyone@acme.com"
# Returns: {"slug":"acme-corp","display_name":"Acme Corporation","idp_configured":true}
```

The login page uses this to decide: show the SSO button, or show the password form.

---

## Part 6 — The Automation Script

Run: `python3.12 scripts/tests/test_38_multitenant.py`

The script runs **74 checks** and exits with code 0 if everything passes.

### What it tests automatically

| Section | What it checks |
|---|---|
| S0 | Auth service and Go bridge are reachable |
| S1 | Super-admin login works; JWT has `role=super_admin`; admin APIs accessible |
| S2 | Create tenant + user + quota via API — all succeed |
| S3 | Tenant-admin gets a token with `role=admin` scoped to their tenant; calling super-admin APIs returns 403 |
| S4 | Tenant-admin can create agents and apps; they appear in the list |
| S5 | Quota is enforced — creating a 4th agent when limit is 3 gets rejected |
| S6 | Super-admin sees all tenants in the observability dashboard |
| S7 | Tenant can update its own settings (email domain); slug cannot be changed |
| S8 | Refreshing a token preserves the same `role` and `tenant_id` |
| S9 | Tenant-admin cannot see agents that belong to a different tenant |
| S10 | After tenant deletion: tenant gone, former user cannot log in |
| S11 | SSO: IdP config saves correctly; `/oidc/start` returns a redirect to Keycloak; email-domain lookup works; config can be cleared |

### What the script cannot test (browser required)

The actual SSO login requires a real browser because:

- The browser must carry a one-time security cookie (`them_oidc_state`) from step 1 through to the callback. A script making two separate HTTP calls cannot do this.
- The identity provider's login page requires user interaction.

| Browser-only step | Why |
|---|---|
| Typing credentials on the Keycloak login page | Real user interaction required |
| The SSO callback completing | Needs the browser's security cookie |
| Verifying `them_access_token` is set | Cookie is HttpOnly — not readable by scripts |
| Confirming `/me` returns the correct user after SSO login | Depends on the above |
| Verifying JIT provisioning created the user row in the DB | Depends on the above |

**For the browser test:** follow Step 6 above. It takes about 2 minutes.

---

## Quick Reference

### Local Keycloak

| Item | Value |
|---|---|
| Admin console | `http://localhost:8088/auth/keycloak/admin` |
| Admin credentials | `admin` / `admin123` |
| Client ID | `them-m` |
| Client secret | `them-m-secret` |
| External URL (browser / curl from host) | `http://localhost:8088/auth/keycloak/realms/them` |
| Internal URL (use in `discovery_url` field) | `http://them-keycloak:8080/auth/keycloak/realms/them` |
| Start command | `docker compose ... --profile sso up -d them-keycloak` |

### Test users — all passwords are `admin123`

**realm: bank**

| Username | Email | Notes |
|---|---|---|
| `bankadmin` | bankadmin@bank.com | Primary bank tenant admin |
| `avi2` | avi2@bank.com | Regular bank user |

**realm: rnd**

| Username | Email | Notes |
|---|---|---|
| `dev` | dev@rnd.com | Developer user |
| `qa-user` | qauser@rnd.com | QA user |

**realm: them** (main/default realm)

| Username | Email | Notes |
|---|---|---|
| `bankadmin` | bankadmin@bank.com | Bank admin in main realm |
| `avi` | avi@bank.com | Test user |
| `avi1` | avi1@bank.com | Test user |
| `avi2` | avi2@bank.com | Test user |
| `avi3` | avi3@bank.com | Test user |
| `dev` | dev@bank.com | Developer test user |
| `qa-user` | qa@bank.com | QA test user |
| `testuser` | testuser@bank.com | Generic test user |

**realm: master** (Keycloak internal — do not use for app login)

| Username | Notes |
|---|---|
| `admin` | Keycloak master admin — password `admin123` |

### Key API endpoints

| Endpoint | Who can call it | What it does |
|---|---|---|
| `POST /api/v1/admin/tenants` | super-admin | Create a tenant |
| `GET /api/v1/admin/tenants` | super-admin | List all tenants |
| `POST /auth/api/v1/admin/users` | super-admin | Create a user with tenant membership |
| `PATCH /api/v1/tenant/settings` | tenant-admin | Update tenant settings including SSO config |
| `GET /api/v1/tenant/settings` | tenant-admin | Read tenant settings |
| `GET /auth/oidc/start?tenant=<slug>` | anyone | Start SSO login for a tenant |
| `GET /auth/api/v1/auth/me` | logged-in user (via cookie) | Read your own identity |
| `GET /auth/api/v1/auth/tenant-lookup?email=<email>` | anyone | Find which tenant an email belongs to |
| `PUT /api/v1/admin/tenants/{id}/group-mappings` | super-admin | Configure IdP group → tenant role mappings (API only, no UI yet) |
