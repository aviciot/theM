# Role Management — Design Document
# Last updated: 2026-09-14

---

## Goal

Allow tenant admins to define roles for their tenant and grant those roles access to specific applications. End-users carry a role (from SSO JWT claims, M2M headers, or manual assignment) and the-M gates their access at the application boundary.

---

## Hierarchy

```
Tenant
  └── Role (e.g. "customer", "analyst", "manager")
        └── App Grant (which apps this role can access)
```

Gate check on every WS/SSE/A2A request:
1. Resolve the end-user's role (see "Role Resolution" below)
2. Look up: does this role have a grant for this application?
3. Yes → allow. No → 403.

If an application has **no grants configured** → it is open to all authenticated users of that tenant (backward-compatible default).

---

## Database Schema

```sql
-- Role definitions per tenant
CREATE TABLE them.tenant_roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,           -- e.g. "customer", "analyst"
    display_name text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

-- Which apps a role can access
CREATE TABLE them.tenant_role_grants (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    role_id         uuid NOT NULL REFERENCES them.tenant_roles(id) ON DELETE CASCADE,
    application_id  uuid NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (role_id, application_id)
);

-- Role claim mapping rules (how to extract the role from a JWT or M2M header)
CREATE TABLE them.tenant_role_mappings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    source      text NOT NULL CHECK (source IN ('jwt_claim', 'header')),
    field       text NOT NULL,   -- JWT claim name (e.g. "roles", "groups") or header name
    value       text NOT NULL,   -- claim/header value to match (e.g. "analyst")
    role_id     uuid NOT NULL REFERENCES them.tenant_roles(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source, field, value)
);
```

---

## Role Resolution

On each request, the-M resolves the end-user's role in this priority order:

### 1. External JWT (SSO passthrough — `access_mode: external_jwt`)
- Validate the Keycloak/Okta JWT
- Walk the tenant's `tenant_role_mappings` rows where `source = 'jwt_claim'`
- Match claim field + value → resolve `role_id`
- Example mapping: `field=roles, value=analyst → role "analyst"`

### 2. M2M service token with header (`access_mode: token`, `allowed_principals: external`)
- Walk mappings where `source = 'header'`
- Match header name + value → resolve `role_id`
- Example: `X-End-User-Role: analyst → role "analyst"`

### 3. No role resolved
- If the application has no grants configured → allow (open to all tenant users)
- If the application has grants configured but user has no matching role → 403

---

## Access Gate Logic

```
request arrives at EP
  │
  ├─ resolve role_id from JWT claims or headers
  │
  ├─ check: does application have any tenant_role_grants rows?
  │     NO  → allow (no role gating on this app)
  │     YES → check: does user's role_id have a grant for this application_id?
  │               YES → allow
  │               NO  → 403 Forbidden
```

---

## UI — Tenant Admin

Tenant admins manage roles in: **Admin → Tenant Settings → Roles**

### Roles list page
- Table: role name, display name, apps granted, actions (edit, delete)
- Button: "New Role"

### Role detail / edit page
- Name, display name, description
- **App grants** — multi-select from tenant's applications
- **Claim mappings** — list of mapping rules:
  - Source: JWT claim / M2M header
  - Field name (e.g. `roles`)
  - Value to match (e.g. `analyst`)
  - Add / remove rows

### Application page (existing)
- Badge showing "Role-gated" if the app has any grants, "Open" if not

---

## API Routes (Go — Admin)

All under `/admin/tenants/{tenant_id}/roles` — requires tenant-admin JWT.

| Method | Path | Description |
|---|---|---|
| GET | `/roles` | List roles for tenant |
| POST | `/roles` | Create role |
| GET | `/roles/{role_id}` | Get role + grants + mappings |
| PUT | `/roles/{role_id}` | Update role |
| DELETE | `/roles/{role_id}` | Delete role (cascades grants + mappings) |
| GET | `/roles/{role_id}/grants` | List app grants for role |
| POST | `/roles/{role_id}/grants` | Add app grant |
| DELETE | `/roles/{role_id}/grants/{grant_id}` | Remove app grant |
| GET | `/roles/{role_id}/mappings` | List claim mappings |
| POST | `/roles/{role_id}/mappings` | Add mapping rule |
| DELETE | `/roles/{role_id}/mappings/{mapping_id}` | Remove mapping rule |

---

## Runtime Gate (Go — epconfig / lifecycle)

Added to the existing `CheckPrincipal` flow in `go/internal/epconfig/`:

```
CheckPrincipal (existing — checks allowed_principals)
  └── CheckRoleGrant (new — checks tenant_role_grants)
```

`CheckRoleGrant(ctx, applicationID, roleID)`:
- If `roleID == ""` → check if app has any grants; if yes → 403, if no → allow
- If `roleID != ""` → query `tenant_role_grants` for `(role_id, application_id)` match

Role resolution happens in lifecycle before `CheckRoleGrant` is called, using the resolved JWT claims or headers.

---

## Implementation Order

1. **DB migration** — `tenant_roles`, `tenant_role_grants`, `tenant_role_mappings`
2. **Runtime gate** — role resolution + `CheckRoleGrant` in lifecycle
3. **Admin API** — CRUD routes for roles, grants, mappings
4. **UI** — Tenant Settings → Roles pages

---

## What Is NOT in Scope (v1)

- EP-level gating (app-level only for now)
- Per-role rate limits
- Role hierarchy / inheritance
- User-level manual assignment (roles come from JWT/headers only)
