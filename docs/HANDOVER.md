# Handover — Multi-Tenancy Step 5
**Date:** 2026-09-02
**Branch:** main
**HEAD:** 9d51765 (docs: update HANDOVER.md with Step 4 completion)
**Steps complete:** 1 → 4 (all 46 Go packages pass)

---

## Rules for the new session (read this before touching code)

### Standing constraints — non-negotiable
- **Go runs inside Docker only** — no host `go` binary. Always use:
  ```bash
  docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
  ```
- **TenantID is NEVER read from HTTP headers or query params** — only from JWT claims via the typed `tenantctx` context key.
- **500 responses use static strings only** — never `err.Error()` from service or DAL layers.
- **Secrets never appear in logs** — use `cfg.SafeString()`.
- **Never commit `.env` or `secrets.local`**.
- **Never use DB name `odin` or schema `odin`** — everything is `them`.
- **Never query `auth_service.*` tables directly from the bridge** — Go uses `internal/auth/`.
- **Never skip git hooks** (`--no-verify`).
- **Never use `git add .` or `git add -A`** — add only the files relevant to the current task.
- **`go test ./...` must be zero failures before every commit** — no exceptions.
- **`TEST_INDEX.md` must be updated in the same commit as any new or changed test.**
- **Every code change to `internal/` or `cmd/` MUST have a test** — new behavior = new test.
- **Files must stay under 400 lines** — propose a split before adding more code to a file that is approaching 500 lines.
- **Handler → Service → DAL** — no SQL in handlers, no business rules in handlers.
- **All list endpoints return `[]` not `null` when empty.**

### Session workflow
1. **Plan** — read relevant docs, confirm scope before writing code.
2. **Implement** — one focused subsystem; do not widen scope mid-task.
3. **Test** — run `go test ./...`; zero new failures before committing.
4. **Commit** — all changed files in one commit with a clear message.
5. **Report** — files changed, tests passed, commit hash.

### When to recommend a new session
- After each step is complete and committed.
- When 5–8 meaningful commits have been made.
- When context reliability is uncertain (re-reading same files, conflicting statements).
- Before a major architecture decision.
- **When recommending a new session: update this HANDOVER.md first, then say so.**

---

## Completed steps

| Step | Description | Status | Commit |
|---|---|---|---|
| Step 1 | JWT + tenant membership foundation | Complete | 4ccb4c4 |
| Step 2 | Redis key hardening | Complete | 97c9d71 |
| Step 3 | Temporal workflow ID prefix with `{tenant_id}:` | Complete | 98ccf03 |
| Step 4 | Tenant CRUD API (`GET/POST /admin/tenants`, `GET /admin/tenants/{id}`) | Complete | a534a54 |
| Step 5 | OIDC login flow | **Next** | — |
| Step 6 | Managed Apps foundation | Not started | — |

---

## What Step 4 built

- `go/internal/admin/dal/tenants.go` — `Tenant`, `TenantInput` types; `ListTenants`, `GetTenant`, `CreateTenant` DAL methods
- `go/internal/admin/dal/dal.go` — added `IsCheckViolation` (SQLSTATE 23514)
- `go/internal/admin/tenants.go` — `TenantsHandler` with `GET /tenants`, `POST /tenants`, `GET /tenants/{id}`
- `go/internal/admin/tenants_test.go` — 8 handler tests (TN-01 to TN-08)
- `go/internal/admin/router.go` — `TenantsHandler` wired into platform-global group
- **No migration needed** — `them.tenants` table already existed from `026_tenant_foundation.sql`
- Table columns: `id UUID PK, slug TEXT UNIQUE, display_name TEXT, enabled BOOL, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ`

---

## Known constraints and surprises

1. **Go 1.25 is only available inside Docker** — host has no `go` binary (see run command above).

2. **`ratelimit.Limiter.CheckToken` is defined but currently unused** — `main.go` assigns `_ = limiter`. Signature is correct (tenant-scoped). Will be wired when per-token rate limiting moves out of the Lua gate script.

3. **Dashboard `sendScanSnapshot`** — `scan:{artifactID}` channel and `them:{tenant_id}:scan:state:{artifactID}` are distinct from the agent scan flow. Both now use tenant-scoped keys but remain separate flows.

4. **MCP server `TenantID` field** — `w.server.TenantID` flows from the DB query in the supervisor. Verify this field is populated in `go/internal/mcp/dal.go` if adding a new MCP server.

5. **Bootstrap tenant UUID** — `00000000-0000-0000-0000-000000000001`, slug `default`. Used in all tests as `testTenantID`.

6. **`them.tenants` table** — `is_bootstrap` column does NOT exist (the design doc is aspirational). Live columns: `id, slug, display_name, enabled, created_at, updated_at`. Verify before adding columns.

---

## Step 5 — OIDC login flow (next task)

### Goal
Allow tenants to authenticate via external OIDC providers (Google, GitHub, Azure AD, Okta, Auth0).
After this step: customers with SSO requirements can use the system.

### Design summary (from `docs/architecture/MULTI_TENANCY_DESIGN.md` §4)
- **Generic OIDC authorization code flow** in `them-auth-go` (`go/internal/authserver/`)
- Per-tenant IdP config stored in `tenants.idp_config JSONB` column (needs a migration to add this column)
- Email-domain → tenant routing on the login page
- The-M auth service remains the internal JWT issuer — it exchanges the OIDC token for an internal HS256 JWT with `tenant_id` claim. The rest of the system never sees an OIDC token.
- One OIDC config per tenant (discovery URL, client_id, client_secret)
- Standard OIDC authorization code flow:
  1. Browser hits `/auth/oidc/start?tenant={slug}` → auth service redirects to IdP
  2. IdP redirects to `/auth/oidc/callback?code=...&state=...`
  3. Auth service exchanges code for ID token, validates claims
  4. Maps external user email to `auth_service.users` (create on first login)
  5. Issues internal JWT with `tenant_id`, sets cookie, redirects to UI

### Files to read before starting
- `docs/architecture/MULTI_TENANCY_DESIGN.md` §4 (Identity and Authentication) and §4.3 (Tenant routing)
- `go/internal/authserver/` — all files (this is where the OIDC flow goes)
- `go/internal/authserver/handlers.go` — existing login/logout/me/refresh handlers
- `go/internal/authserver/store.go` — user store and session management
- `go/internal/authserver/jwt.go` — JWT issuance (OIDC callback will call into this)
- `auth_service/SCHEMA.sql` — `auth_service.users` table schema (tenant_id is NOT there yet)
- `db/001_schema.sql` — `them.tenants` table (will need `idp_config JSONB` column)

### Implementation scope (Step 5 only — do not start Step 6)
1. **Migration**: add `idp_config JSONB DEFAULT NULL` column to `them.tenants`
   - Migration file: `db/027_tenant_idp_config.sql` (check the next available number)
2. **Migration**: add `tenant_id UUID` to `auth_service.users` and create `auth_service.tenant_memberships` table
   - File: `auth_service/migrations/XXX_tenant_identity.sql`
3. **OIDC handler** in `go/internal/authserver/`:
   - `GET /auth/oidc/start` — load tenant IdP config, redirect to IdP authorize endpoint
   - `GET /auth/oidc/callback` — exchange code, validate ID token, upsert user, issue JWT
   - PKCE required (code_verifier/code_challenge) for security
   - State parameter must be signed (HMAC) to prevent CSRF
4. **DAL**: tenant IdP config lookup in `go/internal/authserver/store.go` or a new `go/internal/authserver/oidc_store.go`
5. **Tests**: handler tests for start/callback flows (use a mock IdP HTTP server)
6. **Update `TEST_INDEX.md`** in the same commit

### What NOT to do in Step 5
- Do not start SAML 2.0 (Step 6+ scope)
- Do not start Managed Apps (Step 6)
- Do not change anything in `go/internal/admin/` (that's Step 4, done)
- Do not add SCIM

---

## Startup commands for next session

```bash
cd /opt/docker/them

# Verify stack is healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Read before starting
cat docs/HANDOVER.md
cat docs/architecture/MULTI_TENANCY_DESIGN.md  # §4 and §4.3

# Run tests to confirm baseline (zero failures required)
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...

# Check next available migration number
ls db/*.sql | sort | tail -5
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 5: OIDC login flow.

Current state: Steps 1–4 are complete and merged to main.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (4ccb4c4)
- Step 2: All Redis keys tenant-scoped (97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (98ccf03)
- Step 4: Tenant CRUD API — GET/POST /admin/tenants, GET /admin/tenants/{id} (a534a54)
All 46 Go packages pass (go test ./...).

Read docs/HANDOVER.md fully before starting — it contains all rules, constraints, and the
exact scope for Step 5. The HANDOVER.md is the source of truth for this session.

Step 5 scope only:
1. Migration: add idp_config JSONB column to them.tenants
2. Migration: add tenant_id to auth_service.users + create tenant_memberships table
3. OIDC handlers in go/internal/authserver/: GET /auth/oidc/start and GET /auth/oidc/callback
4. PKCE + signed state parameter (CSRF protection)
5. User upsert on first OIDC login → issue internal HS256 JWT with tenant_id claim
6. Tests for start and callback flows (mock IdP HTTP server)
7. Update TEST_INDEX.md in the same commit

Constraints (all in HANDOVER.md):
- go test ./... must be zero failures before every commit
- TenantID comes only from JWT claims via tenantctx typed key — never from headers
- 500 responses use static strings only — never err.Error()
- Go runs inside Docker: docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
- Handler → Service → DAL — no SQL in handlers
- TEST_INDEX.md updated in same commit as any new test

After each change run go test ./... before committing. Report: files changed, tests passed, commit hash.
Update docs/HANDOVER.md at the end.
```
