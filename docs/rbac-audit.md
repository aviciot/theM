# RBAC Audit — Tenant-Role Authorization Model
# Audit date: 2026-09-06

---

## 1. Router Middleware Map

Source: `go/internal/admin/router.go` lines 138–254, `go/internal/admin/middleware.go`.

| Route prefix | Middleware chain | Resources included | Verdict |
|---|---|---|---|
| `/admin/agents`, `/admin/orchestrators`, `/admin/applications`, `/admin/tokens`, `/admin/agent-definitions`, `/admin/component-definitions`, `/admin/mcp-servers`, `/admin/audit-logs`, `/admin/security-config`, `/admin/managed-apps` (tenant routes), `/admin/canvas-tasks` | JWT → **RequireSuperAdmin** → **AdminTenantMiddleware** | All tenant-scoped CRUD | **Tenant admins get 403** |
| `/runs`, `/runs/*` | JWT → **RequireSuperAdmin** → **AdminTenantMiddleware** | Runs list/detail/cancel/delete/tasks/artifacts/signal/stats/bulk-delete | **Tenant admins get 403** |
| `/admin/llm-providers`, `/admin/monitoring-config`, `/admin/llm-routing`, `/admin/system-agents`, `/admin/tenants`, `/admin/observability`, `/admin/managed-apps` (platform routes), `/admin/sessions`, `/admin/services-stats` | JWT → **RequireSuperAdmin** (no AdminTenantMiddleware) | Platform-global resources | Correctly super_admin only |
| `/tenant/settings`, `/tenant/quota` | JWT → **RequireTenantAdmin** → **AdminTenantMiddleware** | Tenant self-service settings + quota | Correctly available to admin + super_admin |
| `/admin/debug-proxy` | JWT → **RequireSuperAdmin** | Debug proxy | Correctly super_admin only |
| `/admin/transform-functions`, `/admin/transform-test`, `/admin/transform-assist`, `/admin/node-types` | None | Static/compute | Public (no auth) |

**Critical finding:** Every tenant-scoped resource — Applications, Agents, Orchestrators, MCP Servers, Agent Definitions, Runs, Audit Logs — is wrapped in `RequireSuperAdmin`. There is no middleware path that allows `role=admin` or `role=member` to reach these routes.

---

## 2. Middleware Definitions

Source: `go/internal/admin/middleware.go`.

### RequireSuperAdmin (lines 77–108)
- Reads `*Claims` from context (set by jwtMiddleware).
- Iterates `claims.Roles []string`; passes if any element == `"super_admin"`.
- Returns 401 if no claims in context, 403 if role check fails.
- **No tenant scope action** — tenant scoping is left to the next middleware.

### AdminTenantMiddleware (lines 114–130)
- Reads `*Claims` from context.
- Rejects with 403 if `claims.TenantID == ""`.
- Writes `tenantctx.WithTenantID(ctx, claims.TenantID)` into the request context.
- **This is the RLS gate.** The GUC `app.tenant_id` is set from this context value in every DB call. Removing AdminTenantMiddleware would break row-level security.

### RequireTenantAdmin (lines 134–162)
- Reads `*Claims` from context.
- Passes if any element of `claims.Roles` is `"admin"` OR `"super_admin"`.
- Returns 401/403 otherwise.
- Used only on `/tenant/*` self-service routes.

### No RequireMember / RequireAuthenticated middleware
There is no middleware that accepts any authenticated JWT (member, viewer, admin, super_admin). The only role-gating primitives are RequireSuperAdmin and RequireTenantAdmin.

---

## 3. JWT Claims

Source: `go/internal/auth/jwt.go`.

The auth service issues **HS256** tokens. The raw payload shape is `hs256RawClaims` (lines 66–75):

| Claim | JSON key | Type | Notes |
|---|---|---|---|
| User ID | `sub` | string | Converted to int64 in Claims struct |
| Username | `username` | string | |
| Display name | `name` | string | Not in Claims struct (dropped at normalisation) |
| Role | `role` | string | **Single string** — normalised to `Roles []string{"<value>"}` |
| Email | `email` | string | |
| Tenant ID | `tenant_id` | string (UUID) | Populated from `tenant_memberships` at login; empty if no membership |
| Expiry | `exp` | int64 Unix | |
| Issued at | `iat` | int64 Unix | |

The normalisation at lines 192–204 sets `claims.Roles = []string{raw.Role}` — so `claims.Roles` always has exactly one element when `role` is non-empty.

**Role values by actor:**

| Actor | `role` claim value | `Roles` slice |
|---|---|---|
| Platform admin | `super_admin` | `["super_admin"]` |
| Tenant admin | `admin` | `["admin"]` |
| Tenant member | `member` | `["member"]` |
| Tenant viewer | `viewer` | `["viewer"]` |
| OIDC user | `viewer` (platform role always viewer — `go/internal/authserver/oidc_store.go`) | `["viewer"]` |

**tenant_id presence:**
- Populated if and only if a `tenant_memberships` row exists for this user.
- Login is **blocked** if no membership row exists (LoginService.Login returns error).
- Super_admin users who were created before tenant support may have an empty `tenant_id`. Current code rejects any admin route token with empty tenant_id (AdminTenantMiddleware line 122).

---

## 4. Role × Resource Matrix

`✓` = allowed, `403` = forbidden, `—` = not applicable

| Resource | super_admin | admin (tenant) | member | viewer |
|---|---|---|---|---|
| **Applications** — GET list | ✓ | **403** | **403** | **403** |
| **Applications** — POST create | ✓ | **403** | **403** | **403** |
| **Applications** — PUT/DELETE | ✓ | **403** | **403** | **403** |
| **Agents** — GET list | ✓ | **403** | **403** | **403** |
| **Agents** — POST create | ✓ | **403** | **403** | **403** |
| **Agents** — PUT/DELETE | ✓ | **403** | **403** | **403** |
| **Orchestrators** — full CRUD | ✓ | **403** | **403** | **403** |
| **MCP Servers** — full CRUD | ✓ | **403** | **403** | **403** |
| **Runs** — GET list/detail | ✓ | **403** | **403** | **403** |
| **Runs** — PATCH cancel / DELETE | ✓ | **403** | **403** | **403** |
| **Audit Logs** — GET | ✓ | **403** | **403** | **403** |
| **Agent Definitions** — CRUD | ✓ | **403** | **403** | **403** |
| **Tokens** — CRUD | ✓ | **403** | **403** | **403** |
| **LLM Providers** — CRUD | ✓ | **403** | **403** | **403** |
| **Tenants** — CRUD | ✓ | **403** | **403** | **403** |
| **Users** — CRUD | ✓ (auth-service routes) | **403** | **403** | **403** |
| **Observability** | ✓ | **403** | **403** | **403** |
| **Sessions** — list/disconnect | ✓ | **403** | **403** | **403** |
| `/tenant/settings` — GET | ✓ | ✓ | **403** | **403** |
| `/tenant/settings` — PATCH | ✓ | ✓ | **403** | **403** |
| `/tenant/quota` — GET | ✓ | ✓ | **403** | **403** |

**Summary:** Only `super_admin` can perform any operation on tenant-scoped resources (Applications, Agents, Runs, etc.) and all platform-global resources. Tenant `admin`, `member`, and `viewer` roles are blocked (403) on every resource except the `/tenant/*` self-service routes.

---

## 5. Step 34 / Step 36 Functional Assessment

### Step 34 — Role-based nav + frontend route guards
**Visually correct, functionally broken for tenant admins.**

Step 34 correctly hides `Tenants`, `Users`, and `Observability` nav items for non-super_admin users. It also adds `useRequireSuperAdmin()` guards on those three pages.

However, the remaining nav items visible to tenant admins — **Applications, Agents, Orchestrators, MCP Servers, Runs** — all make backend API calls that hit routes wrapped with `RequireSuperAdmin`. A tenant admin (role=`admin`) logging in will:
- See the Applications page in the nav ✓
- Click into it → the page calls `GET /api/v1/admin/applications` → **403 Forbidden**
- Empty state or error will be shown instead of data

The same is true for Agents, Orchestrators, Runs, and any page that fetches from `/admin/*` or `/runs/*`.

### Step 36 — Tenant Onboarding Banner (GetStartedBanner)
**Visually correct, CTA broken for tenant admins.**

The `GetStartedBanner` renders correctly when the applications list is empty (frontend only). But:
- The banner is shown because the API call returned 403 (empty/error state), not because the tenant genuinely has no applications.
- When the tenant admin clicks "Create Application" → `POST /api/v1/admin/applications` → **403 Forbidden**
- The banner's primary CTA is non-functional for tenant admins.

**The demo checklist item "Alice creates an application → created under Acme's tenant_id" (MULTITENANT_PLAN.md line 190) will fail at the API level.**

---

## 6. Root Cause + Recommended Fix

### Root cause
`BuildRouter` (router.go line 138) wraps the entire `/admin` sub-tree in a single `adminGroup` with `RequireSuperAdmin` applied at the group level. Tenant-scoped resources (agents, orchestrators, applications, tokens, runs) are inside this group. There is no mechanism to let `admin`, `member`, or `viewer` roles through to those routes.

The CLAUDE.md comment at line 35–37 of router.go says "fall back to bootstrap tenant covers all UI-authenticated admin users" — but this refers only to the `tenant_id` source (JWT vs bootstrap), not to role gating. The comment describes AdminTenantMiddleware's behaviour, not RequireSuperAdmin's.

### Recommended fix
**Do not weaken platform-global route protection.** The fix is to split the tenant-scoped sub-group out from the RequireSuperAdmin umbrella and apply a lighter middleware (RequireTenantMember — new, accepts any authenticated role with a non-empty tenant_id) instead.

**Preserve AdminTenantMiddleware on all tenant-scoped routes.** AdminTenantMiddleware is the RLS gate — it sets `tenantctx.TenantID` which the DAL uses to set `app.tenant_id` GUC. Removing it would break row-level security. The fix keeps AdminTenantMiddleware everywhere tenant-scoped data is read or written.

**New middleware to add: `RequireTenantMember`**
- Accepts any JWT with a non-empty role: `super_admin`, `admin`, `member`, `viewer`
- Rejects unauthenticated requests (401) and authenticated requests with empty Roles (403)
- Does not do any tenant scoping — AdminTenantMiddleware handles that

**Route groupings after fix:**

| Route group | New middleware chain |
|---|---|
| Applications, Agents, Orchestrators, MCP Servers, Agent Definitions, Tokens, Audit Logs, Canvas Tasks (read-only ops) | JWT → **RequireTenantMember** → AdminTenantMiddleware |
| Applications, Agents (mutating ops — POST/PUT/DELETE) | JWT → **RequireTenantAdmin** → AdminTenantMiddleware |
| Runs (read: GET list/detail/tasks/artifacts/stats) | JWT → **RequireTenantMember** → AdminTenantMiddleware |
| Runs (mutating: cancel/delete/signal/bulk-delete) | JWT → **RequireTenantAdmin** → AdminTenantMiddleware |
| LLM Providers, Monitoring, Routing, System Agents, Tenants, Observability, Sessions, Managed Apps (platform) | JWT → **RequireSuperAdmin** (no change) |
| /tenant/settings, /tenant/quota | JWT → **RequireTenantAdmin** → AdminTenantMiddleware (no change) |

**Alternative simpler approach (recommended for now):**
If role-based write protection is not yet needed within tenant scope, apply `RequireTenantAdmin` (already exists, allows admin + super_admin) to all tenant-scoped routes. This unblocks tenant admins immediately with minimal new code — just replace RequireSuperAdmin with RequireTenantAdmin for the tenant-scoped sub-group in router.go.

Whether `member` and `viewer` should get read-only access is a product decision. The minimum viable fix to make Steps 34/36 functional is: **tenant admins (role=admin) can use Applications, Agents, Runs**. This is satisfied by changing the tenant-scoped group to use RequireTenantAdmin.

---

## 7. Minimum Changes Required

### `go/internal/admin/router.go`

**Current structure (lines 138–221):**
```
r.Group(func(adminGroup) {
    adminGroup.Use(jwtMiddleware)
    adminGroup.Use(RequireSuperAdmin)          ← gates everything
    adminGroup.Route("/admin", func(a) {
        a.Group(func(tenantScoped) {
            tenantScoped.Use(AdminTenantMiddleware)
            // agents, orchs, apps, tokens, defs, mcp, audit, ...
        })
        // llm-providers, monitoring, tenants, sessions (platform-global)
    })
    adminGroup.Group(func(runsGroup) {
        runsGroup.Use(AdminTenantMiddleware)
        runs.Routes(runsGroup)
    })
})
```

**Required change — split tenant-scoped routes out of the RequireSuperAdmin group:**

1. **Lines 138–221:** Remove `RequireSuperAdmin` from the outer group. Instead:
   - Create a new sub-group for platform-global routes with `RequireSuperAdmin` only
   - Create a new sub-group for tenant-scoped routes with `RequireTenantAdmin` + `AdminTenantMiddleware`
   - The runs group (lines 217–220) needs `RequireTenantAdmin` + `AdminTenantMiddleware` instead of inheriting `RequireSuperAdmin`

2. The comment at lines 35–37 in the file must be updated to reflect the new split.

**Exact groups to change:**
- Lines 149–191: `tenantScoped` group — change its parent from RequireSuperAdmin to RequireTenantAdmin (the group just needs AdminTenantMiddleware, with RequireTenantAdmin on the outer group)
- Lines 217–220: runs group — change from RequireSuperAdmin (inherited) to RequireTenantAdmin + AdminTenantMiddleware
- Lines 193–211: platform-global routes — keep RequireSuperAdmin, move them to a separate inner group

### `go/internal/admin/middleware.go` (optional — add RequireTenantMember)
If member/viewer read access is desired: add a new `RequireTenantMember` function (lines after 162) that accepts any non-empty role. Not required for the minimum viable fix.

### No DAL changes required
The DAL already uses `tenantctx.TenantIDFromCtx` to set the `app.tenant_id` GUC. AdminTenantMiddleware sets the tenantctx value. As long as AdminTenantMiddleware stays on all tenant-scoped routes, RLS is preserved correctly.

### No JWT changes required
The `tenant_id` claim is already present in tokens issued for tenant admins (fixed in Step 33, UM-13/14 confirmed).

---

## 8. Tests Required

### New tests in `go/internal/admin/` (router_test.go or middleware_test.go)

1. **TestRequireTenantAdmin_allows_admin_role** — JWT with role=admin passes RequireTenantAdmin
2. **TestRequireTenantAdmin_allows_super_admin_role** — JWT with role=super_admin passes RequireTenantAdmin  
3. **TestRequireTenantAdmin_blocks_member_role** — JWT with role=member gets 403
4. **TestRequireTenantAdmin_blocks_viewer_role** — JWT with role=viewer gets 403
5. **TestBuildRouter_tenant_admin_can_list_applications** — role=admin JWT reaches GET /admin/applications (200 or empty list, not 403)
6. **TestBuildRouter_tenant_admin_can_create_application** — role=admin JWT reaches POST /admin/applications (no 403)
7. **TestBuildRouter_tenant_admin_can_list_runs** — role=admin JWT reaches GET /runs (no 403)
8. **TestBuildRouter_super_admin_still_required_for_tenants** — role=admin JWT on GET /admin/tenants gets 403
9. **TestBuildRouter_super_admin_still_required_for_observability** — role=admin JWT on GET /admin/observability gets 403

Tests 1–4 are unit tests on the middleware function directly.  
Tests 5–9 are integration-style router tests using httptest.NewRecorder with BuildRouter (same pattern as existing router tests in the package — pass nil for redis/temporal/etc., use fakeDB).

### Update `go/TEST_INDEX.md`
Add the new test functions and update the count in the same commit as the router change.

---

## Files Audited

| File | Purpose |
|---|---|
| `go/internal/admin/router.go` | Route registration and middleware chains |
| `go/internal/admin/middleware.go` | RequireSuperAdmin, RequireTenantAdmin, AdminTenantMiddleware definitions |
| `go/internal/auth/middleware.go` | JWTMiddleware, HS256Middleware, BearerMiddleware definitions |
| `go/internal/auth/jwt.go` | Claims struct, hs256RawClaims, ValidateHS256JWT normalisation |
| `docs/MULTITENANT_PLAN.md` | Step status, working demo checklist |
| `docs/sso-access-audit.md` | Prior SSO audit (confirms /tenant/* middleware chain) |
