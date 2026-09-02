# Handover — Multi-Tenancy Step 6
**Date:** 2026-09-02
**Branch:** main
**HEAD:** 2de98f5 (feat(multi-tenancy): Step 5 — OIDC login flow)
**Steps complete:** 1 → 5 (all 46 Go packages pass, 50 authserver tests)

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
| Step 5 | OIDC login flow | Complete | 2de98f5 |
| Step 6 | Managed Apps foundation | Not started | — |

---

## What Step 5 built

- `db/054_tenant_idp_config.sql` — adds `idp_config JSONB DEFAULT NULL` to `them.tenants`
- `go/internal/authserver/oidc_store.go` — `OIDCStore` interface + `pgxOIDCStore`:
  - `GetTenantIDPConfig(slug)` → tenant UUID + `IDPConfig` (discovery_url, client_id, client_secret, redirect_uri) or `ErrTenantNotFound`/`ErrNoIDPConfig`
  - `UpsertOIDCUser(tenantID, email, name)` → idempotent ON CONFLICT(email) user upsert + `tenant_memberships` row
- `go/internal/authserver/oidc.go` — `OIDCHandlers`:
  - `GET /auth/oidc/start?tenant={slug}` — PKCE code_verifier generated, S256 challenge computed, HMAC-signed state (slug + nonce), cookie set, redirect to IdP
  - `GET /auth/oidc/callback?code=...&state=...` — state HMAC verified, PKCE cookie read and cleared, code exchanged with IdP, ID token parsed, user upserted, internal HS256 JWT issued with `tenant_id`, auth cookies set, redirect to `/`
  - OIDC routes registered under `/oidc/start`, `/oidc/callback` AND Traefik mirror `/auth/oidc/start`, `/auth/oidc/callback`
- `go/internal/authserver/jwt.go` — exported `NewTokenSigner(cfg)` for cmd wiring
- `go/internal/authserver/oidc_test.go` — 12 tests (OIDC-01 to OIDC-12) with mock IdP HTTP server
- Migration applied to live DB: `them.tenants.idp_config` column live

### Known Step 5 limitation (to harden in Step 6+)
ID token signature is not verified against the IdP's JWKS — trust is anchored to the code exchange over HTTPS. JWKS-based RS256 signature verification should be added before production use.

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

6. **`them.tenants` table** — `is_bootstrap` column does NOT exist in the live DB (was removed). Live columns after Step 5: `id, slug, display_name, enabled, idp_config, created_at, updated_at`. Verify before adding columns.

---

## Step 5 — COMPLETE (see "What Step 5 built" above)

---

## Step 6 — Managed Apps foundation (next task)

### Goal
Allow platform operators to define "Managed Apps" — pre-configured Application templates
that can be instantiated per tenant. After this step: onboarding a new tenant automatically
provisions their Applications from a Managed App catalog.

### Design summary (from `docs/architecture/MULTI_TENANCY_DESIGN.md` §19)
- A `managed_apps` table in `them` schema, owned by the platform (not tenants)
- A `tenant_managed_apps` binding table (tenant + managed_app + overrides)
- Admin CRUD for managed app catalog (`/admin/managed-apps`)
- Tenant provisioning creates binding rows when apps are assigned

### Files to read before starting
- `docs/architecture/MULTI_TENANCY_DESIGN.md` §19 (Managed Apps)
- `go/internal/admin/` — existing CRUD pattern to follow
- `go/internal/admin/dal/` — DAL pattern
- `db/001_schema.sql` — current table list (check no collision with proposed tables)

### What NOT to do in Step 6
- Do not add OIDC JWKS signature verification (hardening, future step)
- Do not add SAML 2.0
- Do not add SCIM

---

## Startup commands for next session

```bash
cd /opt/docker/them

# Verify stack is healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Read before starting
cat docs/HANDOVER.md
cat docs/architecture/MULTI_TENANCY_DESIGN.md  # §19 (Managed Apps)

# Run tests to confirm baseline (zero failures required)
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...

# Check next available migration number
ls db/*.sql | sort | tail -5
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 6: Managed Apps foundation.

Current state: Steps 1–5 are complete and merged to main.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (4ccb4c4)
- Step 2: All Redis keys tenant-scoped (97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (98ccf03)
- Step 4: Tenant CRUD API — GET/POST /admin/tenants, GET /admin/tenants/{id} (a534a54)
- Step 5: OIDC login flow — /auth/oidc/start + /auth/oidc/callback with PKCE + signed state (2de98f5)
All 46 Go packages pass (go test ./..., 50 authserver tests).

Read docs/HANDOVER.md fully before starting — it contains all rules, constraints, and the
exact scope for Step 6. The HANDOVER.md is the source of truth for this session.

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
