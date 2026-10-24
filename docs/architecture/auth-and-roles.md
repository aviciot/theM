---
title: Auth & Roles
type: architecture
status: current
owns:
  - go/internal/auth/
  - go/internal/authserver/
  - go/cmd/auth-server/
  - go/internal/jwks/
  - go/internal/idpcrypto/
  - go/internal/roles/
  - go/internal/execution/roles.go
  - go/internal/admin/middleware.go
last_verified: 2026-10-24   # by reading code; nothing run live this session
---

# Auth & Roles

## Purpose
Who can call what. Four separate credential types, one auth service (`them-auth-go`) that issues
dashboard tokens, and the checks the bridge runs on every request. Supersedes the legacy
`docs/AUTH.md` (parts of it contradict the code; see Known gaps).

## The four credentials

| Credential | Issued by | Validated by | Used for |
|---|---|---|---|
| **Dashboard JWT** (HS256, `type=access`, 1h default; refresh 7d) | `authserver` (login, OIDC) | Bridge locally: `auth.ValidateHS256JWT` with the shared `JWT_SECRET` | Admin API (`/api/v1`), and runtime EPs with `access_mode=user_jwt` |
| **Opaque bearer token** (sha256 stored in `them.access_tokens`) | Admin API (tokens) | `auth.Cache`: L1 in-process (300s) → L2 Redis `them:session:token:{hash}` (300s) → Postgres | Runtime EPs with `access_mode=token` (machine callers); `is_backend=true` tokens may assert `X-External-User` |
| **External JWT** (RS256 from the tenant's own IdP) | Customer IdP | `auth.ExternalJWTValidator` via tenant JWKS (`jwks.VerifyRS256`) | Runtime EPs with `access_mode=external_jwt` |
| **Public** | — | — | EPs with `access_mode=public` |

Claims in the dashboard JWT: `sub` (user id), `username`, `name`, `role` (the **tenant membership role**: `super_admin|admin|member|viewer`), `tenant_id`, `type`, `exp`, `iat`. Bridge middleware rejects tokens with no `tenant_id` (403) and refuses `type=refresh` as a bearer.

## How it works

### Login (`authserver/service.go`)
1. `POST /api/v1/auth/login` (also mirrored under `/auth/...`): username/email + bcrypt password, or API key hash. `dashboard_access` of the user's platform role must not be `none`.
2. `POST .../runtime-login`: same but skips the dashboard gate, for end-user roles that only need runtime tokens.
3. `issuePair`: looks up the tenant membership (by `tenant_slug` if given, else the first row), puts the **membership role** and `tenant_id` in the access token; refresh token carries `sub` + `tenant_id`. Session hash is recorded best-effort; cookies `them_access_token` / `them_refresh_token` are set HttpOnly + SameSite=Strict.
4. `Refresh`: verifies the refresh token (type, blacklist), re-checks the user is active and the membership for the token's tenant still exists, issues a new pair.
5. `Logout`: blacklists the access token hash until its expiry (`auth_service.blacklisted_tokens`).
6. `Me`/`Verify`/`Validate`: verify signature + blacklist, then reload the user.
7. SSO (`oidc.go`): `GET /oidc/start?tenant=` builds PKCE + HMAC-signed state (slug + nonce, signed with `JWT_SECRET`), verifier in cookie `them_oidc_state` (10 min). Callback verifies state, exchanges the code, verifies the ID token signature (RS256 via JWKS, with one forced re-fetch on unknown `kid`), maps IdP groups → tenant role via `them.tenant_group_mappings` (priority order; `super_admin` mappings rejected; unmatched → `viewer` or deny per tenant `unmatched_action`), upserts the user and membership, issues the same JWT pair. IdP `client_secret` is AES-256 encrypted (`idpcrypto`, key `IDP_ENCRYPTION_KEY`).
8. Super-admin user CRUD (`/api/v1/admin/users`, `/tenants`) lives in `authserver/user_mgmt_handlers.go` and checks `role == super_admin` on the access token itself.

### Admin API authorization (`internal/admin/middleware.go`, `admin/router.go`)
`HS256Middleware(JWT_SECRET)` (or RS256 if `JWT_PUBLIC_KEY_PEM` is set) → then per group:
- **Tenant-scoped routes**: `RequireTenantAdmin` (role `admin` or `super_admin`) + `AdminTenantMiddleware` (tenant from the JWT claim only, 403 if empty; this later sets the RLS GUC).
- **Platform-global routes** (tenants, platform LLM providers, …): `RequireSuperAdmin` only.
Roles are read from `claims.Roles`, which is just the single `role` claim.

### Runtime authorization (`execution.Lifecycle.Admit`; see `run-lifecycle.md`)
Per EP `access_mode`: token / user_jwt / external_jwt / public. Then `CheckAccess` (EP/app enabled, blocked token hashes and user IDs), `CheckPrincipal` (`allowed_principals`: internal|external|both vs `is_backend`), and the checks below. `validateEPAuthCombination` at publish rejects dead combos (external_jwt+internal, user_jwt+external, public+external).

- **Platform role** (`auth_service.roles`, via `PlatformRoleChecker`): `required_role` on the EP (user_jwt only), per-user `rate_limit` rpm and `cost_limit_daily`.
- **Tenant role gate** (`internal/roles`, external_jwt only): `tenant_role_mappings` map a JWT claim or header value → `tenant_roles`; `tenant_role_grants` list which roles may use an app. An app with **no grants is open**; a DB error also fails open.

### Two role systems (don't mix them up)
| | Platform role | Membership role |
|---|---|---|
| Table | `auth_service.roles` (via `users.role_id`) | `auth_service.tenant_memberships.role` |
| In JWT? | No | Yes (`role`) |
| Controls | dashboard access, rate/cost limits, `required_role` on EPs | admin API access (`RequireTenantAdmin` / `RequireSuperAdmin`) |
| OIDC users | always `viewer` | mapped from IdP groups (never `super_admin`) |

## Key invariants
- Tenant identity for admin calls comes **only** from the signed JWT claim; for runtime it comes from the EP/URL slug, never from headers or body.
- `HS256` verification accepts only `alg=HS256`; refresh tokens must never work as bearers (`type` check).
- No third-party JWT library; stdlib crypto only. External/OIDC JWTs: RS256 only, keys ≥2048 bits.
- OIDC can never grant `super_admin`; membership role is validated against `admin|member|viewer`.
- Secrets (JWT secret, IdP client secret, API keys) never appear in logs; error bodies to clients are static strings.
- Opaque-token revocation: admin revoke deletes the Redis L2 key and publishes the hash on `them:token:revoked`; every pod evicts L1.

## Failure modes
| What fails | Effect |
|---|---|
| `them-auth-go` down | No new logins/refresh/SSO; existing access tokens keep working (checked locally by the bridge) |
| Postgres down at login | 5xx; at runtime, opaque-token L1/L2 hits still pass until TTL |
| Redis down | Opaque-token cache falls through to Postgres; revocation broadcast is lost (L1 may stay up to 300s) |
| IdP JWKS unreachable | OIDC login fails (502); external_jwt EPs reject (401) |
| `JWT_SECRET` differs between auth service and bridge | Every dashboard token is rejected |
| Role gate DB error | App treated as open (fail-open) |

## Known gaps (found while writing, 2026-10-24; from reading code, not reproduced live)
See `docs/plans/code-findings-backlog.md` F-18…F-30. Highlights:
1. **Logout does not log out at the bridge.** The blacklist is only checked inside `authserver`. The bridge, admin API, user-mgmt routes and `user_jwt` EPs validate the signature locally, so a logged-out access token works until `exp` (1h).
2. **SSO can take over / re-activate existing accounts.** `UpsertOIDCUser` uses `ON CONFLICT (email) DO UPDATE … active = true` without the `password_hash IS NULL` guard its own comment promises. No `email_verified` check.
3. **ID token claims not validated**: no `iss`, `aud`, `exp` or `nonce` check (only signature, `sub`, `email`).
4. **No brute-force protection on `/login`** (no rate limit or lockout) and cookies lack the `Secure` flag.
5. **Refresh doesn't rotate** (old refresh token stays valid for 7d) and doesn't re-check `dashboard_access`.
6. Login without `tenant_slug` picks `LIMIT 1` with no `ORDER BY` — non-deterministic for multi-tenant users.
7. `external_jwt`: `SubClaim` config is ignored (always `sub`); empty `Audience` silently skips the audience check.
8. Legacy `docs/AUTH.md` is wrong on: refresh tokens (they do carry `tenant_id`), and `AdminTenantMiddleware` (no bootstrap fallback any more).

## Where to change things
| To… | Edit |
|---|---|
| Change token claims or lifetimes | `authserver/jwt.go`, `config.go` (`ACCESS_TOKEN_EXPIRY`, `REFRESH_TOKEN_EXPIRY`) **and** `auth/jwt.go` (must stay compatible) |
| Add an auth route | `authserver/router.go` (+ handler), Traefik routes `/auth` |
| Change SSO behaviour | `authserver/oidc.go`, `oidc_store.go`, `oidc_jwks.go` |
| Change admin role rules | `admin/middleware.go`, `admin/router.go` group wiring |
| Change runtime role/limit checks | `execution/lifecycle.go` (Admit), `execution/roles.go`, `roles/` |
| Change opaque-token validation | `auth/token_cache.go` (and `admin/service/tokens.go` for revoke) |

Tests: `go test ./internal/auth/... ./internal/authserver/... ./internal/roles/... ./internal/execution/...`; index in `go/TEST_INDEX.md`. Live check: login/me/refresh/logout smoke (see root `CLAUDE.md`).

## Related
- `run-lifecycle.md` — where runtime auth is enforced
- `platform-overview.md` — `them-auth-go` container and routes
- `docs/plans/code-findings-backlog.md` — F-18…F-30
- `docs/AUTH.md` — legacy; superseded by this doc
