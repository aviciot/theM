# Authentication — the-M
# Last updated: 2026-08-15
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
| GET | `/api/v1/auth/me` | Verify access token → user profile |
| POST | `/api/v1/auth/refresh` | Refresh token → new JWT pair |
| POST | `/api/v1/auth/logout` | Blacklist access token + clear cookies |
| POST | `/api/v1/auth/verify` | Service-to-service JWT validation |
| GET | `/api/v1/auth/validate` | Traefik forwardAuth compatible |
| GET | `/health` | Compose healthcheck |

### JWT claims (access token)

```json
{
  "sub": "1",
  "username": "admin",
  "name": "Administrator",
  "role": "super_admin",
  "permissions": [],
  "exp": 1234567890,
  "iat": 1234567890,
  "type": "access"
}
```

`tenant_id` is NOT present in user JWTs — Go admin routes handle this via `AdminTenantMiddleware`.

### AdminTenantMiddleware

All Go admin routes use `AdminTenantMiddleware` (not `BearerTenantMiddleware`).

Behavior:
1. Reads JWT claims from context (set by HS256 middleware)
2. If `claims.TenantID` is non-empty: use it
3. If empty (all UI super_admin users): fall back to bootstrap tenant `00000000-0000-0000-0000-000000000001`

This means UI admin users always resolve to the bootstrap tenant. Machine tokens (below) carry their own tenant.

### Database

Auth data lives in the `auth_service` schema (separate from `them` schema):
- `auth_service.users` — credentials, bcrypt password hash, role
- `auth_service.roles` — role definitions + token_expiry
- `auth_service.user_sessions` — refresh token tracking
- `auth_service.blacklisted_tokens` — logout/revocation records

**Never query `auth_service.*` tables directly from bridge code.** Python bridge uses `app/services/auth_client.py` (HTTP to them-auth-go). Go bridge uses `go/internal/auth/`.

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
| `them_access_token` | HS256 access JWT | yes | Lax |
| `them_refresh_token` | HS256 refresh JWT | yes | Lax |

---

## 4. Auth CRUD not yet migrated

`them-auth-service` (Python) is **removed from deployment** as of August 2026.
`them-auth-go` handles only the UI-facing session auth (login/me/refresh/logout).

The following admin CRUD from the old Python service is **not yet implemented** in Go and is therefore **not currently exposed**:
- Users CRUD
- Roles CRUD
- Teams CRUD
- Permissions
- API keys
- MCP tokens

These are future Go migration targets. The source code remains in `auth_service/` for reference.
