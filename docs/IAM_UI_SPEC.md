# IAM UI — Design & Implementation Spec
# Last updated: 2026-09-07
# Status: PLANNED — not yet implemented

This spec defines the three IAM UI changes needed to make identity and access
management usable in the-M. It is written for a developer picking this up in a
new session.

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

## What NOT to Build (keep it clean)

- No "invite user" flow in the tenant context — super-admin creates users, SSO provisions them
- No CSV import / bulk operations
- No separate "IAM" top-level nav section — Members lives under My Tenant
- No group mapping UI yet — it remains a super-admin API-only feature
- No cross-tenant user search from the tenant-admin view
