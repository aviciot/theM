# Roles Clarification — Two Separate Role Systems
# Date: 2026-10-22
# Status: Documentation

---

## TL;DR — You Were Right!

**There ARE existing roles in the UI** — but they're **DIFFERENT** from the ones we're planning to use.

**Two systems**:
1. **`auth_service.roles`** — Platform-level user roles (what we're adding quota enforcement for)
2. **`them.tenant_roles`** — Tenant-level app access roles (already in UI, different purpose)

---

## System 1: Platform User Roles (auth_service.roles)

**Location**: `auth_service.roles` table  
**Scope**: **Platform-wide** (across all tenants)  
**UI**: ❌ **No UI exists** (only DB seeded data)  
**Purpose**: User identity + quota limits

### Schema

```sql
-- auth_service.roles
id                  | INTEGER PK
name                | VARCHAR(50) UNIQUE  -- 'developer', 'analyst', 'viewer'
description         | TEXT
mcp_access          | TEXT[]              -- Allowed MCP servers
tool_restrictions   | JSONB
dashboard_access    | VARCHAR(20)         -- 'admin', 'view', 'none'
rate_limit          | INTEGER             -- 5000 rpm
cost_limit_daily    | NUMERIC(10,2)       -- $100.00/day
token_expiry        | INTEGER             -- 3600 seconds
```

### Current Roles (Seeded)

| Name | Rate Limit | Cost Limit | Dashboard | Users Link |
|---|---|---|---|---|
| super_admin | 10k rpm | $1k/day | admin | auth_service.users.role_id |
| developer | 5k rpm | $100/day | view | ↑ |
| analyst | 1k rpm | $50/day | view | ↑ |
| viewer | 100 rpm | $10/day | view | ↑ |
| end_user | 1k rpm | $10/day | none | ↑ |

### What It's For

- **User identity**: "Alice is a developer"
- **Quota limits**: "Developers get 5000 rpm, $100/day"
- **Dashboard access**: "Analysts can view, end_users cannot"
- **Assigned to users**: `auth_service.users.role_id → roles.id`

### Where It's Used

- ✅ JWT token claims (`role: "developer"`)
- ✅ Token expiry (`roles.token_expiry`)
- ❌ **NOT enforced in Gateway** (no quota checks)
- ❌ **NOT enforced in Canvas** (no role requirements)
- ❌ **NO UI to edit** (must use SQL)

**This is what our implementation plan adds enforcement for.**

---

## System 2: Tenant Roles (them.tenant_roles)

**Location**: `them.tenant_roles` table  
**Scope**: **Per-tenant** (each tenant has own roles)  
**UI**: ✅ **Full UI exists** (`/admin/settings` → "Roles" tab)  
**Purpose**: Application access control + JWT mapping

### Schema

```sql
-- them.tenant_roles
id           | UUID PK
tenant_id    | UUID FK → tenants(id)
name         | TEXT                    -- 'premium-customer', 'basic-user'
display_name | TEXT                    -- 'Premium Customer'
description  | TEXT
created_at   | TIMESTAMPTZ
updated_at   | TIMESTAMPTZ

-- them.tenant_role_grants (which apps this role can access)
id             | UUID PK
role_id        | UUID FK → tenant_roles(id)
application_id | UUID FK → applications(id)

-- them.tenant_role_mappings (how to assign role from JWT/headers)
id      | UUID PK
role_id | UUID FK → tenant_roles(id)
source  | TEXT                     -- 'jwt_claim' or 'header'
field   | TEXT                     -- 'tier' or 'X-User-Tier'
value   | TEXT                     -- 'premium' or 'gold'
```

### Example Tenant Roles

**Tenant**: PayOps AI

| Role Name | Display Name | Granted Apps | Mapping Rule |
|---|---|---|---|
| premium-customer | Premium Customer | App A, App B | `JWT.tier = "premium"` |
| basic-user | Basic User | App A | `JWT.tier = "basic"` |
| admin | Administrator | All apps | `JWT.role = "admin"` |

### What It's For

- **Application access**: "Premium customers can use App A and App B"
- **JWT mapping**: "If JWT has `tier=premium`, assign `premium-customer` role"
- **M2M auth**: "If header `X-User-Tier: gold`, assign `premium-customer` role"
- **Tenant-specific**: Each tenant defines their own roles

### Where It's Used

- ✅ **App access grants** (role → app access)
- ✅ **JWT claim mapping** (JWT field → role assignment)
- ✅ **M2M header mapping** (header → role assignment)
- ✅ **Full UI** (`RolesTab.tsx`)

**This system is already complete and working.**

---

## Key Differences

| Feature | auth_service.roles | them.tenant_roles |
|---|---|---|
| **Scope** | Platform-wide | Per-tenant |
| **Purpose** | User identity + quotas | App access control |
| **Who defines** | Platform admin (seeded) | Tenant admin (via UI) |
| **Assigned to** | Users (`auth_service.users.role_id`) | Dynamic (JWT/headers) |
| **Quota enforcement** | ❌ Not yet | N/A (no quotas) |
| **UI exists** | ❌ No | ✅ Yes (`/admin/settings`) |
| **Examples** | developer, analyst, viewer | premium-customer, admin |

---

## Where the Confusion Came From

**You asked**: "Is there a Roles tab under Organization settings?"

**Answer**: **YES**, but it's for **tenant roles** (app access), not **user roles** (quotas).

**What you see in UI**:
- `/admin/settings` → "Roles" tab
- Create role: `premium-customer`
- Grant apps: App A, App B
- Add mapping: `JWT.tier = "premium"`

**What you DON'T see in UI**:
- Edit `auth_service.roles` (developer, analyst, viewer)
- Set rate_limit or cost_limit_daily
- Assign users to roles

---

## How They Work Together (Example)

**User**: Alice  
**Platform role**: `developer` (from `auth_service.users.role_id`)  
**Tenant**: PayOps AI  
**Tenant role**: `premium-customer` (mapped from `JWT.tier = "premium"`)

**What Alice can do**:
1. **Platform quota**: 5000 rpm, $100/day (from `auth_service.roles.developer`)
2. **App access**: Can use App A and App B (from `tenant_roles.premium-customer` grants)
3. **Dashboard**: Can view dashboard (from `auth_service.roles.dashboard_access = "view"`)

**Flow**:
```
1. Alice logs in → JWT issued
2. JWT contains:
   - role: "developer" (from auth_service.users.role_id)
   - tier: "premium" (custom claim)
3. Canvas checks tenant_roles:
   - JWT.tier = "premium" → maps to tenant_role "premium-customer"
   - premium-customer has grants for App A, App B → access allowed
4. Gateway checks auth_service.roles:
   - JWT.role = "developer"
   - developer.rate_limit = 5000 rpm → enforce quota
   - developer.cost_limit_daily = $100 → enforce quota
```

---

## What Our Plan Adds

**Existing**: Tenant roles (app access) ✅  
**Missing**: Platform roles (quota enforcement) ❌

**Our implementation plan adds**:

### Phase 1-4: Backend Enforcement
- Enforce `auth_service.roles.rate_limit` (Canvas + Gateway)
- Enforce `auth_service.roles.cost_limit_daily` (Canvas + Gateway)
- Add `entry_points.required_role` (restrict by platform role)

### UI Changes Needed

**Do NOT touch**: `/admin/settings` → "Roles" tab (tenant roles, already works)

**Do ADD**:
1. **User Profile** — Show user's platform role (`developer`) + quotas
2. **Entry Point Form** — Add "Required Role" dropdown (platform roles only)
3. **Quota Usage** — Show current vs. limit for rate/cost
4. **Error Dialogs** — Handle 429/402 errors

**Optional**: New page `/admin/platform-roles` to edit `auth_service.roles`

---

## Updated UI Plan

### Keep Existing (NO CHANGES)
- ✅ `/admin/settings` → "Roles" tab (tenant roles)
- ✅ Role grants (app access)
- ✅ Role mappings (JWT/header → tenant role)

### Add New
1. **User Profile Dialog** (Phase 2)
   - Show: "Role: Developer"
   - Show: "Rate Limit: 5000 rpm"
   - Show: "Cost Limit: $100/day"
   - **NO edit** (platform roles are seeded, not tenant-editable)

2. **Entry Point Form** (Phase 1)
   - Add: "Required Role" dropdown
   - **Options from `auth_service.roles`** (developer, analyst, viewer)
   - **Not from `tenant_roles`** (those are for app grants)

3. **Quota Usage** (Phase 3)
   - Show: "230 / 5000 rpm" (progress bar)
   - Show: "$45.32 / $100.00" (progress bar)

4. **Error Handling** (Phase 4)
   - 429 Rate Limited → "Your developer quota is 5000 rpm"
   - 402 Quota Exceeded → "Your daily limit is $100.00"

---

## SQL Examples to Clarify

### Platform Roles (What We're Adding Enforcement For)

```sql
-- List all platform roles
SELECT * FROM auth_service.roles;

-- Alice's platform role
SELECT r.name, r.rate_limit, r.cost_limit_daily
FROM auth_service.users u
JOIN auth_service.roles r ON u.role_id = r.id
WHERE u.username = 'alice';

-- Result: developer, 5000, 100.00
```

### Tenant Roles (What Already Has UI)

```sql
-- List tenant roles for PayOps AI
SELECT * FROM them.tenant_roles WHERE tenant_id = '<payops-uuid>';

-- What apps can premium-customer role access?
SELECT a.name
FROM them.tenant_role_grants g
JOIN them.applications a ON g.application_id = a.id
WHERE g.role_id = (SELECT id FROM them.tenant_roles WHERE name = 'premium-customer');

-- How is premium-customer role assigned?
SELECT source, field, value
FROM them.tenant_role_mappings
WHERE role_id = (SELECT id FROM them.tenant_roles WHERE name = 'premium-customer');

-- Result: jwt_claim, tier, premium
```

---

## API Clarification

### Existing API (Tenant Roles — Don't Touch)
```
GET    /admin/roles               → List tenant roles
POST   /admin/roles               → Create tenant role
PUT    /admin/roles/:id           → Update tenant role
DELETE /admin/roles/:id           → Delete tenant role
GET    /admin/roles/:id/grants    → List app grants
POST   /admin/roles/:id/grants    → Add app grant
DELETE /admin/roles/:id/grants/:grantId → Remove grant
GET    /admin/roles/:id/mappings  → List JWT/header mappings
POST   /admin/roles/:id/mappings  → Add mapping
DELETE /admin/roles/:id/mappings/:mappingId → Remove mapping
```

**Used by**: `/admin/settings` → "Roles" tab (`RolesTab.tsx`)

### New API Needed (Platform Roles — What We're Adding)

**Option A: Read-only** (recommended for MVP)
```
GET /auth/me → Include user's platform role + quotas
GET /auth/quota-usage → Current rate/cost usage
```

**Option B: Admin editable** (optional, Phase 5)
```
GET    /admin/platform-roles     → List auth_service.roles
PUT    /admin/platform-roles/:id → Update rate_limit, cost_limit_daily
GET    /admin/users/:id          → Include role_id
PUT    /admin/users/:id/role     → Change user's platform role
```

---

## Recommendations

### 1. Keep Existing Tenant Roles UI As-Is ✅
- Already works
- Different purpose (app access, not quotas)
- No changes needed

### 2. Add Platform Role Display (Not Edit) ✅
- Show in user profile: "Role: Developer"
- Show quotas: "5000 rpm, $100/day"
- Show usage: progress bars
- **Don't allow editing** (platform roles are seeded)

### 3. Entry Point "Required Role" Uses Platform Roles ✅
- Dropdown: developer, analyst, viewer (from `auth_service.roles`)
- **NOT** premium-customer, admin (from `tenant_roles`)
- Purpose: "Only developers can access this EP"

### 4. Consider Renaming for Clarity 🤔
- Current: "Roles" tab → confusing (which roles?)
- Better: "App Access Roles" or "Tenant Roles"
- Or: Keep as-is, add tooltip: "Control which apps each role can access"

### 5. Optional: Platform Roles Admin Page (Phase 5) 🔜
- New page: `/admin/platform-roles`
- List `auth_service.roles`
- Edit rate_limit, cost_limit_daily
- Assign users to roles
- **Only for super_admin users**

---

## Migration Path

### Step 1: No UI Changes (Backend Only)
- Implement quota enforcement (Phases 1-4)
- Use existing `auth_service.roles` data (seeded)
- Users assigned via SQL: `UPDATE auth_service.users SET role_id = X`

### Step 2: Display-Only UI (MVP)
- Show user's platform role in profile
- Show quotas + usage
- Show errors (429/402)
- **No editing** (roles are seeded)

### Step 3: Full Admin UI (Optional)
- New page: `/admin/platform-roles`
- Edit limits per role
- Assign users to roles
- **Only if needed** (most deployments: seeded roles are fine)

---

## Conclusion

**You were right** — there IS a Roles tab under Organization settings!

**But**: It's for **tenant roles** (app access), not **platform roles** (quotas).

**Our plan**:
- ✅ Keep tenant roles UI as-is (no changes)
- ✅ Add platform role **display** (profile, quotas)
- ✅ Add platform role enforcement (backend)
- 🔜 Optionally add platform role **editing** (admin page)

**Files to update**:
- ✅ User profile UI (show platform role)
- ✅ Entry point form (required_role dropdown)
- ❌ Do NOT touch `/admin/settings` "Roles" tab (tenant roles)

---

**Created**: 2026-10-22  
**Status**: Clarification complete  
**Action**: Update UI plan to preserve existing tenant roles UI
