# Authentication — the-M
# Last updated: 2026-09-06
# Source of truth: go/internal/authserver/, go/internal/auth/

---

## Overview

Two distinct auth paths exist. They use different token formats and serve different actors.

---

## 1. Human UI auth (JWT)

**Service:** `them-auth-go` (`go/cmd/auth-server`) — internal port **8703**
**Algorithm:** HS256, signed with `THE_M_JWT_SECRET`
**Python `them-auth-service` is removed from deployment.**

### Flow

```
Browser → Next.js API route (/api/auth/*) → them-auth-go:8703 → auth_service schema (PostgreSQL)
                                                      ↓
                                              httpOnly cookies set:
                                              them_access_token (1h)
                                              them_refresh_token (7d)
```

### Endpoints (them-auth-go)

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/auth/login` | Username+password → JWT pair + Set-Cookie |
| GET | `/api/v1/auth/me` | Verify access token → user profile (role = membership role from JWT) |
| POST | `/api/v1/auth/refresh` | Refresh token → new JWT pair (re-queries tenant_memberships) |
| POST | `/api/v1/auth/logout` | Blacklist access token + clear cookies |
| POST | `/api/v1/auth/verify` | Service-to-service JWT validation |
| GET | `/api/v1/auth/validate` | Traefik forwardAuth compatible |
| GET | `/api/v1/auth/tenant-lookup?email=` | Domain → tenant lookup (public, no auth) |
| GET | `/health` | Compose healthcheck |

### User management endpoints (super_admin only)

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/admin/users` | List all users with tenant membership info |
| POST | `/api/v1/admin/users` | Create user + optional tenant membership |
| GET | `/api/v1/admin/users/{id}` | Get single user |
| PATCH | `/api/v1/admin/users/{id}` | Update name / email / active |
| DELETE | `/api/v1/admin/users/{id}` | Hard delete (cascades memberships) |
| POST | `/api/v1/admin/users/{id}/reset-password` | Set new password |
| GET | `/api/v1/admin/tenants` | List tenants (for user assignment dropdowns) |

### JWT claims (access token)

```json
{
  "sub": "1",
  "username": "admin",
  "name": "Administrator",
  "role": "super_admin",
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "permissions": [],
  "exp": 1234567890,
  "iat": 1234567890,
  "type": "access"
}
```

`role` carries the **membership role** from `auth_service.tenant_memberships` (admin/member/viewer for tenant users; super_admin for bootstrap admin). `tenant_id` is always present — login is blocked if the user has no membership row.

### Two-role model

Every user has two roles that serve different purposes:

| Role type | Source | Field in JWT | Values |
|---|---|---|---|
| Platform role | `auth_service.roles.name` via `users.role_id` | — (not in JWT) | super_admin, developer, analyst, viewer |
| Membership role | `auth_service.tenant_memberships.role` | `role` claim | super_admin, admin, member, viewer |

At login, `issuePair()` always queries `tenant_memberships` and puts the **membership role** in the JWT `role` claim. The platform role is stored in the DB but not currently included in the token. The bridge uses the JWT `role` claim for all access control decisions (RequireSuperAdmin, RequireTenantAdmin).

When creating a user via `POST /api/v1/admin/users`:
- `role` field → platform role (`auth_service.roles.name`). Default: `"viewer"`.
- `tenant_role` field → membership role written to `tenant_memberships.role`. Default: `"member"`.

### Single-vs-multiple tenant membership

Current design: **one active membership per user** per tenant (`UNIQUE(user_id, tenant_id)` in `tenant_memberships`). A user may theoretically have memberships in multiple tenants; at login, `issuePair()` picks the first row unless `tenant_slug` is provided in the login request.

To log into a specific tenant: `POST /api/v1/auth/login` with `{"tenant_slug": "acme"}`. The frontend does not yet expose this field.

Multi-tenant-per-user (consultant use case) is deferred to a later step.

### Refresh token behavior

Refresh tokens carry only `user_id` (no tenant or role claim). On refresh, `issuePair()` re-queries `tenant_memberships` and issues a fresh access token with the current membership role and tenant_id. If the user's membership changes between refresh cycles, the next refresh picks up the new membership automatically.

### AdminTenantMiddleware

All Go admin routes use `AdminTenantMiddleware` (not `BearerTenantMiddleware`).

Behavior:
1. Reads JWT claims from context (set by HS256 middleware)
2. If `claims.TenantID` is non-empty: use it
3. If empty: fall back to bootstrap tenant `00000000-0000-0000-0000-000000000001`

This means UI admin users always resolve to their JWT tenant. Machine tokens (below) carry their own tenant.

### Database

Auth data lives in the `auth_service` schema (separate from `them` schema):
- `auth_service.users` — credentials, bcrypt password hash, role_id
- `auth_service.roles` — role definitions + token_expiry
- `auth_service.tenant_memberships` — user_id → tenant_id + role (UNIQUE per user+tenant)
- `auth_service.user_sessions` — refresh token tracking
- `auth_service.blacklisted_tokens` — logout/revocation records

**Never query `auth_service.*` tables directly from bridge code.** Go bridge uses `go/internal/auth/`.

---

## 2. Machine / data-plane auth (opaque bearer token)

**Owner:** Go bridge `go/internal/auth/token_cache.go`
**Format:** opaque random token, stored as `sha256(token)` in `them.access_tokens`

### Flow

```
Client → Authorization: Bearer <token>
       → L1 in-process sync.Map
       → L2 Redis them:token:{sha256}  (TTL 300s)
       → PostgreSQL them.access_tokens
```

### Usage

Used by:
- WS orchestration endpoints (`/ws/orchestrate/`, `/apps/{slug}/ws`)
- SSE endpoints (`/apps/{slug}/sse`)
- A2A server (`/a2a/message`)
- Runs data-plane (`/api/v1/runs/*`) — uses `BearerTenantMiddleware`

**Admin CRUD routes use JWT (AdminTenantMiddleware), not opaque tokens.**

### Token lifecycle

| Action | Effect |
|---|---|
| `POST /api/v1/admin/tokens` | Generate random token, store sha256 in DB, return plaintext once |
| `DELETE /api/v1/admin/tokens/{id}` | Delete from DB, publish to `them:token:revoked` pub/sub |
| Redis TTL | 300s — revoked tokens may work up to 5min (by design) |
| Cross-pod invalidation | Redis pub/sub `them:token:revoked` → L1 eviction on all pods |

---

## 3. Cookie names

| Cookie | Content | HttpOnly | SameSite |
|---|---|---|---|
| `them_access_token` | HS256 access JWT | yes | Strict |
| `them_refresh_token` | HS256 refresh JWT | yes | Strict |

---

## 4. OIDC / SSO flow

**Status: Backend COMPLETE (Steps 5, 8, 9, 17, 18). Frontend wiring pending (Step 37).**

The full authorization-code flow with PKCE is implemented in `go/internal/authserver/`:

| File | Purpose |
|---|---|
| `oidc.go` | `OIDCHandlers.Start` + `OIDCCallback` — PKCE, HMAC state, code exchange, JWT issuance |
| `oidc_jwks.go` | RS256 id_token verification; JWKS key caching with rotation awareness |
| `oidc_store.go` | `GetTenantIDPConfig`, `UpsertOIDCUser`, `GetGroupRole` |

### Login flow

```
1. GET /auth/oidc/start?tenant={slug}
   → generate PKCE code_verifier + S256 challenge
   → HMAC-sign state (slug + nonce), set cookie
   → redirect to IdP /authorize

2. GET /auth/oidc/callback?code=&state=
   → verify HMAC state, read PKCE cookie
   → exchange code with IdP → id_token
   → verify RS256 signature via JWKS
   → UpsertOIDCUser (upsert user + tenant_membership)
   → GetGroupRole: lookup them.tenant_group_mappings for role
   → issuePair → HS256 JWT pair (same cookie format as password login)
   → Set-Cookie them_access_token + them_refresh_token
   → redirect to /
```

### Email-first tenant discovery

`GET /api/v1/auth/tenant-lookup?email=alice@acme.com`

Returns `{"tenant_slug":"acme","idp_configured":true}` or `{"idp_configured":false}`.

Frontend should call this on email entry: if `idp_configured=true`, redirect to `/auth/oidc/start?tenant={slug}`; otherwise show password form.

### Group mapping privilege escalation

`them.tenant_group_mappings.role` CHECK allows `'super_admin'`. If mapped, `UpsertOIDCUser` resolves `auth_service.roles WHERE name = role` — sets platform super_admin. Only super_admin can write mappings (API-layer mitigation). Guardrail (Step 37): tighten CHECK to `('admin','member','viewer')`.

---

## 5. Auth CRUD migration status

| Capability | Status |
|---|---|
| Login / me / refresh / logout | ✅ Go (`them-auth-go`) |
| Users CRUD + tenant assignment | ✅ Go (`them-auth-go`, Step 32) |
| Tenant lookup by email domain | ✅ Go (`them-auth-go`, Step 17) |
| OIDC backend (PKCE, RS256, group mappings) | ✅ Go (`them-auth-go`, Steps 5/8/9/17/18) |
| OIDC frontend wiring + Keycloak test IdP | ⚠️ Partial (Step 37) |
| Roles CRUD | ❌ Not exposed (data seeded via SQL) |
| Teams CRUD | ❌ Not migrated |
| API keys / MCP tokens | ❌ Not migrated |
