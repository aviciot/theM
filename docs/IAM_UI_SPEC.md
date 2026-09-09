# IAM UI — Design & Implementation Spec
# Last updated: 2026-09-09
# Status: Stage 1 IMPLEMENTED + DEPLOYED (2026-09-09)

This spec defines the IAM UI work needed to make identity and access management
usable in the-M. It is organized into three bounded stages. It is written for a
developer picking this up in a new session.

---

## Stage 1 — Tenant creation → SSO → bank employee login → Members
**Status: IMPLEMENTED + DEPLOYED (2026-09-09, HEAD 86b0886+)**

### What was delivered

| Item | File | Status |
|---|---|---|
| `PATCH /api/v1/tenant/members/{user_id}` backend | `go/internal/admin/tenant_self_service.go`, `go/internal/admin/dal/tenants.go` | Done |
| Members page Save uses tenant-scoped endpoint | `frontend/src/app/tenant/members/page.tsx` | Done |
| SSO fields populated from saved config on load | `frontend/src/app/tenant/settings/page.tsx` | Done |
| ProvisionWizard Done step → SSO setup callout | `frontend/src/app/admin/tenants/ProvisionWizard.tsx` | Done |
| Login page org-code fallback | `frontend/src/app/login/page.tsx` | Done |

### Stage 1 verification
- Go build: all packages pass (`ok internal/admin`, `ok internal/admin/dal`)
- Tests: TSS-09, TSS-10, TSS-11 covering `PatchMyMember` all pass
- Frontend: TypeScript compiles, container restarted

### What Stage 1 enables
A super-admin can:
1. Create a tenant via ProvisionWizard (Steps 1–3)
2. Be directed to SSO tab immediately after (Done step callout)
3. Configure SSO — fields now pre-populate when re-opening the settings page
4. Bank employees can log in via SSO or via org-code fallback on login page
5. Tenant admin can view and update member roles from `/tenant/members` — no longer requires super-admin

---

## Stage 2 — Group-to-role mapping, tenant-admin membership editing, developer permissions
**Status: NOT STARTED**

### What Stage 2 adds
1. Group mapping UI in SSO tab — lets SSO users auto-receive roles without manual promotion
2. Admin/Users Membership tab — super-admin can change tenant_role per user
3. Admin/Tenants Members tab — super-admin sees member list per tenant

### Backend needed for Stage 2
- `PATCH /auth/api/v1/admin/users/{id}` — add `tenant_role` to update request (Change 1 backend)
- `GET /api/v1/tenant/group-mappings` + `PUT /api/v1/tenant/group-mappings` — tenant-scoped group mapping endpoints
- Add `groups_claim` and `unmatched_action` to `idp_config` JSONB + enforce at OIDC login

### Frontend needed for Stage 2
- **Change 1 frontend**: Membership tab in `/admin/users` side panel
- **Change 2 frontend**: Members tab in `/admin/tenants` side panel + member count badge
- **Change 4 frontend**: Group mapping UI in SSO settings tab (see full spec below)

---

## Stage 3 — End-customer authentication, application permissions, usage
**Status: NOT STARTED — requires Phase 4 (JWKS) as prerequisite**

### What Stage 3 adds
1. JWKS-validated external JWT bearer tokens for end-customer auth
2. Per-EP allowed_principals enforcement (already built — `allowed_principals` column + `CheckPrincipal`)
3. Application-level usage/quota UI visible to tenant admins
4. End-customer session and run scoping visible in the Members/Activity view

### Prerequisite: Phase 4 (JWKS)
Phase 4 adds the `JWKSAuthenticator` and `tenant_runtime_config` table. Until that lands,
Stage 3 cannot be started. Phase 3 (`allowed_principals`) is complete and gating is live.

---

---

## Context — What Exists Today

### Backend APIs (all working, no changes needed)

**Super-admin user management** — served by `them-auth-go` at `/auth/api/v1/admin/`:

| Method | Path | What it does |
|---|---|---|
| `GET` | `/auth/api/v1/admin/users` | List all users (returns `ManagedUser[]`) |
| `POST` | `/auth/api/v1/admin/users` | Create user with tenant membership |
| `GET` | `/auth/api/v1/admin/users/{id}` | Get single user |
| `PATCH` | `/auth/api/v1/admin/users/{id}` | Update name, email, active (NOT membership role — see gap below) |
| `DELETE` | `/auth/api/v1/admin/users/{id}` | Delete user |
| `POST` | `/auth/api/v1/admin/users/{id}/reset-password` | Reset password |

**Super-admin tenant membership** — served by `them-go-bridge` at `/api/v1/admin/`:

| Method | Path | What it does |
|---|---|---|
| `GET` | `/api/v1/admin/tenants/{id}/members` | List all members of a tenant (`TenantMember[]`) |
| `POST` | `/api/v1/admin/tenants/{id}/members` | Add a user to a tenant |
| `GET` | `/api/v1/admin/tenants/{id}/group-mappings` | List IdP group → role mappings |
| `PUT` | `/api/v1/admin/tenants/{id}/group-mappings` | Set/replace group mappings |
| `DELETE` | `/api/v1/admin/tenants/{id}/group-mappings/{mapping_id}` | Delete one mapping |

**No tenant-scoped member API exists yet.** A tenant-admin has no backend endpoint
to list or manage users in their own tenant. This must be added (see Change 3).

### Key response shapes

```ts
// GET /auth/api/v1/admin/users → ManagedUser[]
type ManagedUser = {
  id: number;
  username: string;
  name: string;
  email?: string;
  role: string;           // platform role: super_admin | developer | analyst | viewer
  active: boolean;
  created_at: string;
  last_login_at?: string;
  tenant_id?: string;
  tenant_slug?: string;
  tenant_role?: string;   // membership role: admin | member | viewer
}

// GET /api/v1/admin/tenants/{id}/members → TenantMember[]
type TenantMember = {
  id: string;
  user_id: number;
  tenant_id: string;
  role: string;           // membership role: admin | member | viewer
  username: string;
  email: string;
  created_at: string;
  // Note: no last_login_at — comes from auth_service.users, not memberships
}

// PATCH /auth/api/v1/admin/users/{id} — updateUserRequest
type UserUpdateInput = {
  name?: string;
  email?: string;
  active?: boolean;
  // ⚠️ membership role (tenant_role) is NOT patchable via this endpoint today
  // To change tenant_role, a new endpoint is needed or POST /members can replace
}
```

### Frontend type definitions — `frontend/src/lib/api.ts`

`ManagedUser`, `UserCreateInput`, `UserUpdateInput`, `TenantSummary` are already
defined and exported. The API client methods are at:
```ts
themApi.listUsers()
themApi.createUser(input)
themApi.updateUser(id, input)
themApi.deleteUser(id)
themApi.listTenantsForUsers()   // returns TenantSummary[]
```

### Existing pages

| Page | Route | Who sees it | Status |
|---|---|---|---|
| Users | `/admin/users` | super_admin | Exists — needs enhancement |
| Tenants | `/admin/tenants` | super_admin | Exists — needs Members tab |
| My Tenant | `/tenant/settings` | tenant_admin | Exists |
| Members | `/tenant/members` | tenant_admin | **Does not exist** |

### Sidebar — `frontend/src/components/Sidebar.tsx`

Three nav arrays:
- `NAV` — visible to all authenticated users
- `ADMIN_NAV` — visible to all (but routes are auth-gated at page level)
- `SUPER_ADMIN_NAV` — rendered only when `user?.role === 'super_admin'`

Current entries relevant to IAM:
```ts
// NAV (all users)
{ href: '/tenant/settings', icon: 'manage_accounts', label: 'My Tenant' }

// SUPER_ADMIN_NAV
{ href: '/admin/tenants', icon: 'domain',  label: 'Tenants' }
{ href: '/admin/users',   icon: 'group',   label: 'Users' }
```

### Design language
- Background: `var(--tm-bg)`, border: `var(--tm-border)`, accent: `#818cf8` (`ACCENT`)
- Active badge: `#34d399` green / `#f87171` red (see `badge()` helper in users page)
- Panels: dark card `background: '#0f1117'` or similar, `borderRadius: '14px'`
- All pages follow: full-width table on left, slide-in side panel on right on row click

---

## Change 1 — `/admin/users` — Add Membership Role editing

**File:** `frontend/src/app/admin/users/page.tsx`

### What to add

The existing side panel has tabs **Info** / **Password** / **Delete**.

Add a fourth tab: **Membership**.

**Membership tab content:**
- Shows current `tenant_slug` (read-only text)
- Shows current `tenant_role` (editable dropdown: `viewer` / `member` / `admin`)
- Save button → calls the membership update endpoint (see backend gap below)
- If user has no tenant membership → shows "No tenant assigned"

### Backend gap — membership role update

`PATCH /auth/api/v1/admin/users/{id}` does NOT update `tenant_role` today.
Two options (pick one):

**Option A (simpler):** Add `tenant_role` to `UserUpdateInput` in Go and handle it
in `UpdateUser` handler → updates `auth_service.tenant_memberships` row.

**Option B:** Use the existing `POST /api/v1/admin/tenants/{id}/members` with the
user's current tenant to replace/upsert the membership role.

**Recommendation: Option A.** Changes needed:
1. `go/internal/authserver/store.go` — add `TenantRole *string` to `UserUpdateInput`
2. `go/internal/authserver/pgx.go` — `UpdateUser()` → if `TenantRole != nil`, update `auth_service.tenant_memberships` row
3. `go/internal/authserver/user_mgmt_handlers.go` — add `TenantRole *string` to `updateUserRequest` struct
4. `frontend/src/lib/api.ts` — add `tenant_role?: string` to `UserUpdateInput` type

### Also add: Role filter to the table

Add a `<select>` filter above the table:
```
Filter by role: [All ▾] [super_admin] [admin] [member] [viewer] [inactive]
```
Filter is client-side (data is already loaded). Filter on `user.tenant_role` or
`user.active === false` for inactive.

---

## Change 2 — `/admin/tenants` — Add Members tab to side panel

**File:** `frontend/src/app/admin/tenants/page.tsx`

### What to add

The existing side panel has tabs **General** / **Identity Provider** / **Quotas**.

Add a fourth tab: **Members**.

**Members tab content:**
- On tab open: fetch `GET /api/v1/admin/tenants/{id}/members`
- Shows a compact table: Username / Email / Membership Role / Joined
- Read-only. No edit actions here — direct super-admin to `/admin/users` to change roles.
- If no members → "No members yet"

**Also add to tenant card:** member count badge
- Fetch member count from the existing members endpoint when the tenant list loads,
  OR add a `member_count` field to the `GET /api/v1/admin/tenants` response (simpler).
- Display as a small stat: `3 members` next to the enabled/disabled badge on each card.

### Frontend API call to add to `frontend/src/lib/api.ts`

```ts
listTenantMembers: (tenantId: string) =>
  bridge.get<TenantMember[]>(`admin/tenants/${tenantId}/members`),
```

Add `TenantMember` type to `api.ts`:
```ts
export type TenantMember = {
  id: string;
  user_id: number;
  tenant_id: string;
  role: string;
  username: string;
  email: string;
  created_at: string;
}
```

---

## Change 3 — `/tenant/members` — New page (highest priority)

This is the biggest gap. Tenant-admins have zero visibility into who is in their tenant.

### Sidebar change

**File:** `frontend/src/components/Sidebar.tsx`

Add to `NAV` array (visible to all authenticated users — the page itself guards on role):
```ts
{ href: '/tenant/members', icon: 'group', label: 'Members' }
```
Place it directly below `{ href: '/tenant/settings', ... }`.

### New page

**File:** `frontend/src/app/tenant/members/page.tsx` ← create this file

**Auth guard:** `useRequireSuperAdmin` is wrong here — use the existing pattern from
`tenant/settings/page.tsx` which checks `user?.role !== 'super_admin'` and redirects
if the user is not at least an admin of their tenant. Or create `useRequireTenantAdmin`.

**Data source:** There is no tenant-scoped member list endpoint today.
Two options:

**Option A (no backend change):** Use `GET /api/v1/admin/tenants/{id}/members` with
the tenant_id from the user's JWT. This endpoint requires `super_admin` today.
Would need middleware loosened to allow `admin` role scoped to the same tenant.

**Option B (new endpoint — recommended):** Add to the Go bridge:
```
GET /api/v1/tenant/members
```
Reads `tenant_id` from JWT, queries `auth_service.tenant_memberships` for that tenant.
Returns `TenantMember[]`. Requires `admin` or `super_admin` membership role.

**Go implementation for Option B:**
- Handler: `go/internal/admin/tenants.go` — add `ListMyMembers` handler
- Route: `go/cmd/them/main.go` — register `GET /api/v1/tenant/members` with `RequireTenantAdmin` middleware
- DAL: reuse `dal.ListMembers(ctx, tenantID)` — already exists

### Page layout

```
Members                                    [← no create button]

┌─────────────────────────────────────────────────────────────┐
│ Name              Email                Role        Joined   │
│ ─────────────────────────────────────────────────────────── │
│ Alice Smith       alice@acme.com       admin       Jan 2026 │
│ Bob Jones         bob@acme.com         member      Feb 2026 │
│ Carol White       carol@acme.com       viewer      Mar 2026 │
└─────────────────────────────────────────────────────────────┘

Click row → side panel slides in:

┌─────────────────────────┐
│  Alice Smith            │
│  alice@acme.com         │
│                         │
│  [Profile] [Membership] │
│  ───────────────────    │
│  Profile tab:           │
│  Username: alice        │
│  Email: alice@acme.com  │
│  Joined: Jan 2026       │
│  (all read-only)        │
│                         │
│  Membership tab:        │
│  Role: [admin ▾]        │
│  Active: [toggle]       │
│  [Save]                 │
└─────────────────────────┘
```

**Actions a tenant-admin can do:**
- Change membership role (viewer / member / admin — never super_admin)
- Deactivate/reactivate a member (sets `active` flag via PATCH user, but this is a
  super-admin-only call today — may need a separate tenant-scoped endpoint)

**Actions a tenant-admin CANNOT do:**
- Create new users (super-admin only — users arrive via SSO JIT or super-admin creates them)
- Delete users from the system (super-admin only)
- See users from other tenants

**What viewers see:** If `user.role === 'viewer'`, show the Members page as read-only
(no edit panel, just the table). Check role from JWT.

---

## Implementation Order

1. **Change 3 backend** — `GET /api/v1/tenant/members` endpoint (small Go change)
2. **Change 3 frontend** — `/tenant/members` page (new file, ~200 lines following the pattern from `admin/users/page.tsx`)
3. **Change 1 backend** — add `tenant_role` to `PATCH /users/{id}` (small Go change)
4. **Change 1 frontend** — Membership tab in `/admin/users` side panel
5. **Change 2 frontend** — Members tab in `/admin/tenants` side panel + member count on cards

---

## Files to Touch

| File | Change |
|---|---|
| `go/internal/authserver/store.go` | Add `TenantRole *string` to `UserUpdateInput` |
| `go/internal/authserver/pgx.go` | Update `UpdateUser()` to patch tenant_memberships |
| `go/internal/authserver/user_mgmt_handlers.go` | Add `tenant_role` to `updateUserRequest` |
| `go/internal/admin/tenants.go` | Add `ListMyMembers` handler |
| `go/cmd/them/main.go` | Register `GET /api/v1/tenant/members` |
| `frontend/src/lib/api.ts` | Add `TenantMember` type, `listTenantMembers`, `listMyMembers`, update `UserUpdateInput` |
| `frontend/src/components/Sidebar.tsx` | Add Members nav entry |
| `frontend/src/app/tenant/members/page.tsx` | **Create new file** |
| `frontend/src/app/admin/users/page.tsx` | Add Membership tab + role filter |
| `frontend/src/app/admin/tenants/page.tsx` | Add Members tab to side panel |

---

## Change 4 — Group Mapping UI in SSO Settings

**File:** `frontend/src/app/tenant/settings/page.tsx` (SSO tab)

This is the feature that makes SSO actually usable for real companies. Without it,
every SSO user lands as `viewer` and must be manually promoted.

### How group mapping works (background)

When a user logs in via SSO, the identity provider puts a list of groups in their
token, for example:
```json
"groups": ["engineering", "team-leads", "all-staff"]
```
The-M reads this list and matches it against the tenant's mapping table. If
`team-leads` is mapped to `admin` → the user gets `admin` membership automatically.
No manual work per user. The admin only configures mappings once.

**Key facts:**
- The admin cannot fetch groups from the IdP — only groups that appear in a login token
  are visible. Every IdP (Okta, Azure, Google, Keycloak) requires admin-level credentials
  to list all groups — a SaaS app should never ask for that.
- The admin types group names manually, exactly as they appear in their IdP.
- Azure AD sends group GUIDs by default (e.g. `a8f3c2d1-...`), not names — note this in the UI.
- The claim name is usually `groups` but varies: Keycloak can use `roles`, some IdPs use
  custom claim names. The admin must be able to configure which claim to read.

### Fail open vs fail closed

A critical design decision: what happens when a user logs in via SSO but their groups
don't match any mapping?

| Mode | Behavior |
|---|---|
| **Fail closed** (default) | Login is denied — user sees an error. Recommended for enterprise. |
| **Fail open** | User gets `viewer` membership — read-only access. |

This must be configurable per tenant. Default: **fail closed**.

### UI — add to the SSO tab after the IdP config form

```
── Group Mappings ─────────────────────────────────────────────

Groups claim name: [ groups ]
  (The JWT field your IdP uses for groups. Usually "groups".
   Keycloak may use "roles". Azure AD users: your IdP sends
   GUIDs by default — configure a "Group Name" attribute mapper
   in Azure to get names instead.)

If user's groups don't match any mapping:
  ● Deny login   ○ Grant viewer access

Mappings:
┌────────────────────────────────┬──────────────┬──────┐
│ IdP Group Name (exact match)   │ Role         │      │
├────────────────────────────────┼──────────────┼──────┤
│ [ engineering                ] │ [ member ▼ ] │ [×]  │
│ [ team-leads                 ] │ [ admin  ▼ ] │ [×]  │
└────────────────────────────────┴──────────────┴──────┘
[ + Add mapping ]                              [ Save ]

ⓘ  Groups are matched against claims in the SSO token at login time.
   To verify your group names: log in via SSO once, then check
   your token at /auth/api/v1/auth/me (the 'groups' field will
   appear if your IdP is sending them).
```

Role choices: `viewer` / `member` / `admin` — never `super_admin`.

### Backend changes needed

**1. Add `groups_claim` and `unmatched_action` to idp_config:**

The `them.tenants.idp_config` JSONB already exists. Extend it:
```json
{
  "discovery_url": "...",
  "client_id": "...",
  "client_secret": "...",
  "redirect_uri": "...",
  "groups_claim": "groups",           ← new: which JWT field to read
  "unmatched_action": "deny"          ← new: "deny" | "viewer"
}
```

Changes:
- `go/internal/authserver/oidc_store.go` — add `GroupsClaim string` and `UnmatchedAction string` to `IDPConfig` struct
- `go/internal/authserver/oidc.go` — in `UpsertOIDCUser`: if no group matches and `UnmatchedAction == "deny"` → return error causing 403; if `"viewer"` → assign viewer

**2. Expose group-mappings API to tenant-admin (not just super-admin):**

Today `PUT /api/v1/admin/tenants/{id}/group-mappings` requires `super_admin`.
Add a tenant-scoped version:
```
GET  /api/v1/tenant/group-mappings
PUT  /api/v1/tenant/group-mappings
```
These read `tenant_id` from the JWT (same pattern as `/api/v1/tenant/settings`).
Require `admin` or `super_admin` membership role.

Go handler location: `go/internal/admin/tenants.go`
Route registration: `go/cmd/them/main.go`

**3. Frontend API additions to `frontend/src/lib/api.ts`:**
```ts
export type GroupMapping = {
  id: string;
  group_claim: string;
  role: 'admin' | 'member' | 'viewer';
  priority: number;
}

getMyGroupMappings: () => bridge.get<GroupMapping[]>('tenant/group-mappings'),
putMyGroupMappings: (mappings: Omit<GroupMapping, 'id'>[]) =>
  bridge.put<GroupMapping[]>('tenant/group-mappings', mappings),
```

---

## Testing Group Mappings with Keycloak (local dev)

### Why Keycloak works as a stand-in for Okta / Azure / Google

Keycloak implements the same **OpenID Connect** standard that Okta, Microsoft, and
Google use. When you configure the-M to use Keycloak, the SSO flow is identical to
what a real company would experience. The only difference is who runs the IdP.

For testing group mappings specifically, Keycloak is perfect — you can create groups,
assign users to them, and configure Keycloak to include groups in the JWT token, all
from its admin UI or CLI.

### Step-by-step: test group mapping with Keycloak

**Step 1 — Create groups in the Keycloak realm**

```bash
docker exec them-keycloak /opt/keycloak/bin/kcadm.sh config credentials \
  --server http://localhost:8080/auth/keycloak \
  --realm master --user admin --password admin123

# Create two groups in the "them" realm
docker exec them-keycloak /opt/keycloak/bin/kcadm.sh create groups \
  -r them -s name=engineering

docker exec them-keycloak /opt/keycloak/bin/kcadm.sh create groups \
  -r them -s name=team-leads
```

**Step 2 — Add testuser to a group**

```bash
# Get the user ID
USER_ID=$(docker exec them-keycloak /opt/keycloak/bin/kcadm.sh get users \
  -r them -q username=testuser --fields id | python3 -c \
  "import sys,json; print(json.load(sys.stdin)[0]['id'])")

# Get the group ID
GROUP_ID=$(docker exec them-keycloak /opt/keycloak/bin/kcadm.sh get groups \
  -r them | python3 -c \
  "import sys,json; [print(g['id']) for g in json.load(sys.stdin) if g['name']=='engineering']")

# Add user to group
docker exec them-keycloak /opt/keycloak/bin/kcadm.sh update \
  users/$USER_ID/groups/$GROUP_ID -r them -s realm=them
```

**Step 3 — Configure Keycloak to include groups in the JWT token**

This is the critical step — by default Keycloak does NOT include groups in the token.
You must add a "Group Membership" mapper to the `them-m` client:

```bash
CLIENT_ID=$(docker exec them-keycloak /opt/keycloak/bin/kcadm.sh get clients \
  -r them --fields clientId,id | python3 -c \
  "import sys,json; [print(c['id']) for c in json.load(sys.stdin) if c['clientId']=='them-m']")

docker exec them-keycloak /opt/keycloak/bin/kcadm.sh create \
  clients/$CLIENT_ID/protocol-mappers/models -r them \
  -s name=groups \
  -s protocolMapper=oidc-group-membership-mapper \
  -s protocol=openid-connect \
  -s 'config."claim.name"=groups' \
  -s 'config."full.path"=false' \
  -s 'config."id.token.claim"=true' \
  -s 'config."access.token.claim"=true' \
  -s 'config."userinfo.token.claim"=true'
```

**Step 4 — Configure group mapping in the-M**

In the SSO tab of your tenant settings, set:
- Groups claim name: `groups`
- Unmatched action: `Deny login`
- Mappings: `engineering` → `member`

**Step 5 — Test**

Open `http://localhost:8088/auth/oidc/start?tenant=your-slug` in a browser.
Log in as `testuser@example.com` / `testpass`.

After login, call:
```
GET http://localhost:8088/auth/api/v1/auth/me
```

You should see `"role": "member"` — the group mapping worked.

To test fail-closed: remove testuser from all groups, try logging in again.
You should see a 403 / error page — login denied because no group matched.

---

## What NOT to Build (keep it clean)

- No "invite user" flow in the tenant context — super-admin creates users, SSO provisions them
- No CSV import / bulk operations
- No separate "IAM" top-level nav section — Members lives under My Tenant
- No cross-tenant user search from the tenant-admin view
- No custom role creation — the three roles (admin / member / viewer) cover all real use cases
