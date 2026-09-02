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
| Step 6 | Managed Apps foundation | Complete | 7c056fc |

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

## Step 6 — COMPLETE

### What Step 6 built

- `db/055_managed_apps.sql` — adds `app_type TEXT DEFAULT 'tenant'`, `version TEXT DEFAULT '1.0.0'`, `changelog TEXT` to `them.applications`; creates `them.managed_app_params` (parameter manifest) and `them.managed_app_bindings` (per-tenant activation); indexes on `(tenant_id, enabled)` and `(app_id, sort_order)`
- `go/internal/admin/dal/managed_apps.go` — 8 DAL methods: `ListManagedApps`, `CreateManagedApp`, `GetManagedApp`, `ListManagedAppParams`, `UpsertManagedAppParams` (DELETE+INSERT), `ListBindingsForTenant`, `GetBinding`, `UpsertBinding` (ON CONFLICT DO UPDATE)
- `go/internal/admin/managed_apps.go` — `ManagedAppsHandler` with:
  - Platform routes (no tenant scope): `GET /admin/managed-apps`, `POST /admin/managed-apps`, `GET /admin/managed-apps/{id}`, `PUT /admin/managed-apps/{id}/params`
  - Tenant routes (inside AdminTenantMiddleware): `GET /admin/managed-app-bindings`, `PUT /admin/managed-app-bindings/{app_id}`
  - TenantID always from `tenantctx.TenantIDFromCtx` — never from headers
  - All 500 responses use static strings only
- `go/internal/admin/router.go` — wires `PlatformRoutes` in the platform-global group and `TenantRoutes` in the tenant-scoped group
- `go/internal/admin/managed_apps_test.go` — 10 tests (MA-01..MA-10)
- `go/TEST_INDEX.md` — adds S1-92, updates trigger map, total 989 → 999

All 46 packages pass (`go test ./...`).

### Migration note for live DB
`db/055_managed_apps.sql` has not yet been applied to the live DB. Run before next feature work:
```bash
docker cp db/055_managed_apps.sql them-postgres:/tmp/them_055.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_055.sql
```

---

## Step 7 — Runtime parameter injection (next task)

### Goal
Wire the Managed App binding into the agent/orchestrator invocation path so that when a run
is created under a managed-app entry point, the binding's `config` values are injected into
the `InvocationContext` — making `{{PARAMS.KEY}}` substitution available to agent prompts.

### Design summary (from `docs/architecture/MULTI_TENANCY_DESIGN.md` §19)
- `InvocationContext` gains `ManagedAppParams *ManagedAppParams` field (non-nil only for managed-app runs)
- `ManagedAppParams.Config map[string]any` — plain config values from the binding
- `ManagedAppParams.Secrets map[string]string` — decrypted secrets (never logged, never in Temporal history)
- At run start: load the application → check `app_type`; if `managed`, look up binding for `(app_id, tenant_id)`, populate `ManagedAppParams`
- Orchestrator uses `ManagedAppParams.Config` for `{{PARAMS.KEY}}` substitution in system prompts

### Files to read before starting
- `docs/architecture/MULTI_TENANCY_DESIGN.md` §19 (Runtime injection section)
- `go/internal/orchestrator/orchestrator.go` — where InvocationContext is built
- `go/internal/admin/dal/managed_apps.go` — GetBinding method (already implemented)
- `go/internal/domain/domain.go` — InvocationContext definition

### What NOT to do in Step 7
- Do not add secret encryption/decryption (secrets_enc BYTEA) — leave secrets_enc NULL for now; encryption requires a KMS decision (deferred per design doc)
- Do not add OIDC JWKS signature verification
- Do not add SAML 2.0 or SCIM

---

## Startup commands for next session

```bash
cd /opt/docker/them

# Verify stack is healthy
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml ps

# Apply Step 6 migration if not yet done
docker cp db/055_managed_apps.sql them-postgres:/tmp/them_055.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_055.sql

# Read before starting
cat docs/HANDOVER.md
cat docs/architecture/MULTI_TENANCY_DESIGN.md  # §19 (Runtime injection)

# Run tests to confirm baseline (zero failures required)
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 7: Runtime parameter injection.

Current state: Steps 1–6 are complete and merged to main.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (4ccb4c4)
- Step 2: All Redis keys tenant-scoped (97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (98ccf03)
- Step 4: Tenant CRUD API — GET/POST /admin/tenants, GET /admin/tenants/{id} (a534a54)
- Step 5: OIDC login flow — /auth/oidc/start + /auth/oidc/callback with PKCE + signed state (2de98f5)
- Step 6: Managed Apps foundation — catalog CRUD + tenant binding activation (7c056fc)
All 46 Go packages pass (go test ./..., 999 tests).

Read docs/HANDOVER.md fully before starting — it contains all rules, constraints, and the
exact scope for Step 7. The HANDOVER.md is the source of truth for this session.

Constraints (all in HANDOVER.md):
- go test ./... must be zero failures before every commit
- TenantID comes only from JWT claims via tenantctx typed key — never from headers
- 500 responses use static strings only — never err.Error()
- Go runs inside Docker: docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
- Handler → Service → DAL — no SQL in handlers
- TEST_INDEX.md updated in same commit as any new test
- Do NOT add secret encryption (secrets_enc) — leave NULL, KMS decision deferred

After each change run go test ./... before committing. Report: files changed, tests passed, commit hash.
Update docs/HANDOVER.md at the end.
```
