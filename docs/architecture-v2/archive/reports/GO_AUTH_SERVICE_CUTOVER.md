# Go Auth Service — Design, Contract, and Cutover

Status: implementation in progress
Replaces: Python `them-auth-service` (FastAPI, port 8701) for the UI-facing auth contract.
New service: `them-auth-go` (Go binary `go/cmd/auth-server`), internal port 8703.

---

## 1. UI-facing contract (source of truth = Python auth_service/routes/auth.py + frontend/src/app/api/auth/*)

The Next.js server-side API routes proxy to `THE_M_AUTH_URL` (container DNS, NOT through Traefik).
Only these five endpoints are exercised by the UI and MUST be preserved bit-for-bit:

| UI action | endpoint | method | request body | response body | token/claim usage | cutover |
|---|---|---|---|---|---|---|
| Login | `/api/v1/auth/login` | POST | `{username,password}` OR `{api_key}` | `{access_token, refresh_token, expires_in}` + Set-Cookie them_access_token/them_refresh_token | mints HS256 access+refresh | required |
| Me | `/api/v1/auth/me` | GET | cookie `them_access_token` | `{id,email,name,username,role}` | verifies access JWT | required |
| Refresh | `/api/v1/auth/refresh` | POST | cookie `them_refresh_token` (or Bearer) | `{access_token, refresh_token, expires_in}` + Set-Cookie | verifies refresh JWT, mints new pair | required |
| Logout | `/api/v1/auth/logout` | POST | cookie `them_access_token` (or Bearer) | `{message:"Logged out successfully"}` + delete cookies | blacklists access token | required |
| Health | `/health` | GET | — | `{status,service,version,...}` | none | required (compose healthcheck) |

Service-to-service endpoints kept for compatibility (bridge `AUTH_SERVICE_URL` uses `/verify`):
| `/api/v1/auth/verify` | POST | Bearer access | `{sub,user_id,username,name,role,email}` | validates JWT |
| `/api/v1/auth/validate` | GET | Bearer access | 200 + X-User-* headers | Traefik forwardAuth compat |

NOT migrated this session (no UI dependency, not in mandate): users/roles/teams/permissions/api_keys/mcp_tokens admin CRUD. The Python container source is retained; only the UI-facing auth contract moves to Go.

## 2. JWT — algorithm and claims (MUST match go/internal/auth/jwt.go ValidateHS256JWT + Python token_service.py)

- Algorithm: **HS256**, signed with the platform secret `JWT_SECRET` (compose: `${THE_M_JWT_SECRET}`).
  This is the SAME secret the Go bridge already validates with (`cfg.JWTSecret`). No algorithm change.
- Access token claims: `sub` (string user id), `username`, `name`, `role` (string), `permissions` ([]),
  `exp`, `iat`, `type="access"`.
- Refresh token claims: `sub`, `exp`, `iat`, `type="refresh"`.
- `exp` derived from `roles.token_expiry` (fallback ACCESS_TOKEN_EXPIRY=3600). Refresh exp = 604800.
- Token hashing for session/blacklist rows: lowercase hex SHA-256 of the raw token (matches Python utils/hashing.hash_token).

## 3. Machine / opaque token behaviour

- Human UI JWT: HS256, issued here, validated by the Go bridge `HS256Middleware`.
- Machine opaque bearer token (`them.access_tokens`, `ak_`/random): UNCHANGED. Still validated by the Go
  bridge's `internal/auth/token_cache.go` (L1→L2 Redis→PostgreSQL). The auth server does NOT issue or own
  these; API-key login (`{api_key}`) resolves a user row via `users.api_key_hash` and mints a JWT, exactly
  as Python did.
- `internal/auth/middleware.go` already accepts BOTH: `HS256Middleware` (user JWT) and `BearerMiddleware`
  (opaque). No change required for dual acceptance; the bridge already wires HS256 for admin JWT and Bearer
  for WS/SSE/A2A. Phase 4 = confirm + document, no code change to the acceptance matrix.

## 4. Go packages

- `go/cmd/auth-server/main.go` — binary entrypoint, wiring, graceful shutdown.
- `go/internal/authserver/` — the service:
  - `config.go` — env load + validate (JWT_SECRET, DATABASE_*, PORT).
  - `jwt.go` — HS256 issue + verify (mirrors Python claim shape). Reuses no third-party lib.
  - `password.go` — bcrypt verify (golang.org/x/crypto/bcrypt).
  - `store.go` + `pgx.go` — DAL over `auth_service` schema (users/roles/user_sessions/blacklisted_tokens).
  - `service.go` — Login/Me/Refresh/Logout/Verify business logic + sentinel errors.
  - `handlers.go` — chi HTTP handlers, cookie handling, JSON shapes.
  - `router.go` — chi router: /api/v1/auth/* + /auth/api/v1/auth/* mirror + /health.

## 5. Compose + Traefik

- Add `them-auth-go` to `docker-compose.yml` base (port 8703 internal). No new compose file.
- Point `THE_M_AUTH_URL` (frontend) and `AUTH_SERVICE_URL` (bridge) at `http://them-auth-go:8703`.
- Traefik: add `/auth` prefix router → them-auth-go (Python had a `/auth/*` mirror; preserved for parity).
  The primary UI path is server-to-server via THE_M_AUTH_URL, so Traefik label is for external `/auth` parity.

## 6. Cutover verification checklist — see final report.

## 7. Rollback

- Revert `THE_M_AUTH_URL` / `AUTH_SERVICE_URL` to `http://them-auth-service:8701`.
- `./scripts/deploy.sh up` (or `docker compose ... up -d them-auth-service`) to restart Python auth.
- Remove the `them-auth-go` service + its Traefik router. Python source was never deleted.
</content>
</invoke>
