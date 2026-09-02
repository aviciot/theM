# Handover — Multi-Tenancy Step 15
**Date:** 2026-09-02
**Branch:** main
**HEAD:** 24ff822 (feat(multi-tenancy): Step 15 — Per-tenant LLM provider key management)
**Steps complete:** 1 → 15 (all 47 Go packages pass, 1056 S1 tests, 1008 go test ./...)

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
| Step 7 | Runtime parameter injection | Complete | 0bbfa28 |
| Step 8 | OIDC JWKS RS256 id_token signature verification | Complete | 99fc33c |
| Step 9 | OIDC JWKS key caching (TTL-based, rotation-aware) | Complete | 2056550 |
| Step 10 | Tenant provisioning UI — PATCH /admin/tenants/{id} + frontend Tenants page | Complete | 441f9e7 |
| Step 11 | Binding management UI — platform-level binding API + frontend Managed Apps page | Complete | 59105c4 |
| Step 12 | Tenant quota management — them.tenant_quotas + GET/PUT /admin/tenants/{id}/quota + frontend Quotas tab | Complete | 293fe26 |
| Step 13 | Quota enforcement at run start — max_concurrent_runs (DB COUNT) + runs_per_minute (Redis INCR) | Complete | cfaef99 |
| Step 14 | Monthly run limit enforcement — monthly_runs quota (Redis INCR keyed by YYYY-MM, 48h-past-month TTL) | Complete | 828739b |
| Step 15 | Per-tenant LLM provider key management — tenant_id on llm_providers, merged list, upsert override, run-time resolution | Complete | 24ff822 |

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

### Step 5 limitation — resolved in Step 8
ID token signature is now verified against the IdP's JWKS using RS256 (stdlib only). See Step 8 below.

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

## Step 7 — COMPLETE

### What Step 7 built

- `go/internal/temporal/workerconfig/loader.go` — Added:
  - `ManagedAppParams` struct: `Config map[string]any` (Secrets field deferred — KMS decision pending)
  - `ApplyParamSubstitution(prompt string, params *ManagedAppParams) string` — replaces `{{PARAMS.KEY}}` placeholders; leaves unmatched keys unchanged; nil-safe
  - `ManagedAppParams *ManagedAppParams` field on `RunConfig`
  - `tenantID string` added as 5th parameter to `Loader` interface and `PgxLoader.LoadRunConfig`
  - `loadAppType(ctx, applicationID)` — queries `applications.app_type`; non-fatal (returns "tenant" on error)
  - `loadBindingParams(ctx, appID, tenantID)` — queries `managed_app_bindings.config` WHERE `enabled = true`; non-fatal (returns nil on miss or error)
  - At run start: if `app_type = 'managed'` and binding found, `RunConfig.ManagedAppParams` is populated and `{{PARAMS.KEY}}` is substituted into the system prompt
- `go/internal/temporal/activities.go` — `LoadRunConfig` call passes `input.TenantID`
- `go/internal/voice/handler.go` — both `LoadRunConfig` calls pass `cfg.TenantID`
- `go/internal/temporal/worker_test.go` — fake `LoadRunConfig` updated for new signature
- `go/internal/temporal/workerconfig/loader_test.go` — 3 new tests (MAP-01..03): substitution, nil-safe, zero-value
- `go/TEST_INDEX.md` — S1-93 added; totals 999 → 1002 (S1), 951 → 954 (go test ./...)

### Step 7 constraints and notes

- Secrets NOT populated: `ManagedAppParams` has no `Secrets` field — KMS/encryption decision deferred
- Non-fatal binding lookup: binding not found or DB error → `ManagedAppParams = nil`, run proceeds normally
- Voice handler passes tenant from resolved EP config (`cfg.TenantID`)

---

## Step 8 — COMPLETE

### What Step 8 built

- `go/internal/authserver/oidc_jwks.go` — JWKS-based RS256 id_token signature verification (stdlib only):
  - `jwk` struct + `rsaPublicKey()` — parses JWK RSA fields, rejects keys < 2048 bits
  - `httpJWKSFetcher` — fetches JWKS from IdP over HTTP (injectable for tests)
  - `jwksFetcher` interface — injectable in `OIDCHandlers` for test isolation
  - `verifyRS256IDToken(ctx, fetcher, jwksURI, idToken)` — decodes JWT header, verifies `alg=RS256`, fetches JWKS, selects key by `kid`, verifies PKCS1v15 RS256 signature, then parses claims
- `go/internal/authserver/oidc.go` changes:
  - `oidcDiscovery` gains `JWKSURI string` (`jwks_uri` JSON field); discovery now fails if `jwks_uri` is missing
  - `OIDCHandlers` gains `jwks jwksFetcher` field, wired to `httpJWKSFetcher` in `NewOIDCHandlers`
  - `OIDCCallback` calls `verifyRS256IDToken` instead of the removed `parseIDTokenClaims`
  - `parseIDTokenClaims` (unsigned, Step 5 legacy) removed
- `go/internal/authserver/oidc_test.go` — mock IdP upgraded:
  - Generates a real RSA 2048-bit test key at `init()` time (`testRSAKey`)
  - Mock IdP now serves `/jwks` endpoint with `testJWKS()` JWKS document
  - Discovery document now includes `jwks_uri`
  - Token endpoint signs id_tokens with `testRSAKey` (RS256, kid=`test-key-1`)
  - All 12 existing OIDC tests (OIDC-01..12) pass with real signatures
- `go/internal/authserver/oidc_jwks_test.go` — 5 new unit tests (OIDC-13..OIDC-17):
  - OIDC-13: valid RS256 token accepted
  - OIDC-14: tampered signature rejected
  - OIDC-15: unknown kid rejected (no matching JWKS key)
  - OIDC-16: non-RS256 alg rejected before JWKS fetch
  - OIDC-17: JWKS fetch failure propagated
- `go/TEST_INDEX.md` — S1-40 updated (50→55 tests); totals 1002→1007 S1, 954→959 `go test ./...`

All 46 packages pass (`go test ./...`).

## Step 9 — COMPLETE

### What Step 9 built

- `go/internal/authserver/oidc_jwks.go` — Added TTL-based JWKS cache:
  - `jwksCacheEntry` struct: cached `*jwksDocument` + `expiresAt time.Time`
  - `jwksCache` struct: `sync.Map` keyed by `jwks_uri`, configurable TTL, default 5 minutes
  - `newJWKSCache(inner jwksFetcher, ttl time.Duration) *jwksCache` — constructor
  - `FetchJWKS` returns cached entry if still fresh, else fetches from upstream and stores
  - `fetchFresh` bypasses cache and re-fetches (called on unknown kid — key rotation path)
  - `findKey` helper extracted (replaces inline loop in `verifyRS256IDToken`)
  - `verifyRS256IDToken` updated: on kid-not-found with a `*jwksCache` fetcher, calls `fetchFresh` once before failing
- `go/internal/authserver/oidc.go` — `NewOIDCHandlers` wires `newJWKSCache(httpJWKSFetcher, defaultJWKSCacheTTL)` instead of raw `httpJWKSFetcher`
- `go/internal/authserver/oidc_jwks_test.go` — 3 new tests (OIDC-18..20):
  - OIDC-18: second verify call within TTL → only 1 upstream fetch (cache hit)
  - OIDC-19: TTL=1ns → expires before second call → 2 upstream fetches
  - OIDC-20: cached doc has "old-key", token carries "new-key" → 1 re-fetch, succeeds

All 46 packages pass.

## Step 10 — COMPLETE

### What Step 10 built

**Go backend:**
- `go/internal/admin/dal/tenants.go`:
  - `TenantIDPConfig` — OIDC IdP config struct (`discovery_url`, `client_id`, `client_secret` write-only, `redirect_uri`)
  - `TenantPatch` — patch body with custom `UnmarshalJSON` to distinguish "idp_config absent" vs "explicit null" (clears config) via `SetIDP bool`
  - `TenantDetail` — extends with `IDPConfigured bool` (`idp_config IS NOT NULL`)
  - `PatchTenant` — COALESCE-based UPDATE with `CASE WHEN $4 THEN $5::jsonb ELSE idp_config END` for idp_config
- `go/internal/admin/tenants.go`: `Patch` handler + `r.Patch("/tenants/{id}", h.Patch)` route
- `go/internal/admin/tenants_test.go`: `tenantDetailFakeRow` (7-col), extended `tenantDB.patchRow`; TN-09..12 (Patch_Success, Patch_NotFound, Patch_BadJSON, Patch_IDPConfigured) — 8→12 tests; S1-94 covers all 12

**Frontend:**
- `frontend/src/lib/apiTypes.ts`: `IDPConfig` (with `client_secret?: string` write-only), `TenantRecord`, `TenantPatch` types
- `frontend/src/lib/api.ts`: `listTenants`, `createTenant`, `patchTenant` methods
- `frontend/src/app/admin/tenants/page.tsx`: full Tenants admin page with:
  - Card grid: slug, display_name, enabled badge, IdP badge, created date
  - Side panel with two tabs — General (display_name + enabled toggle) and Identity Provider (OIDC discovery_url/client_id/client_secret/redirect_uri + Clear button)
  - Create modal: slug + display_name validation
- `frontend/src/components/Sidebar.tsx`: "Tenants" nav entry (`domain` icon) added to ADMIN_NAV

All 46 Go packages pass (`go test ./...`); 1022 S1 tests, 974 `go test ./...` total. TypeScript: zero new errors.

## Step 11 — COMPLETE

### What Step 11 built

**Go backend:**
- `go/internal/admin/managed_apps.go`:
  - `PlatformRoutes` extended with two new endpoints:
    - `GET /admin/tenants/{tenant_id}/managed-app-bindings` — list all bindings for any tenant (by path param)
    - `PUT /admin/tenants/{tenant_id}/managed-app-bindings/{app_id}` — upsert binding for any tenant (by path param)
  - `ListBindingsByTenant` + `UpsertBindingByTenant` handlers — tenant_id from path, no AdminTenantMiddleware, reuse existing DAL methods
- `go/internal/admin/managed_apps_test.go`: MA-11..14 (ListBindingsByTenant, ListBindingsByTenant_Empty, UpsertBindingByTenant, UpsertBindingByTenant_MissingConfig)
- `go/TEST_INDEX.md`: S1-92 updated (10→14 tests); totals 1022→1026 S1, 974→978 go test ./...

**Frontend:**
- `frontend/src/lib/apiTypes.ts`: `ManagedApp`, `ManagedAppParam`, `ManagedAppDetail`, `ManagedAppBinding`, `ManagedAppBindingInput` types
- `frontend/src/lib/api.ts`: `listManagedApps`, `getManagedApp`, `listManagedAppBindings`, `upsertManagedAppBinding`
- `frontend/src/app/admin/managed-apps/page.tsx`: Binding management page:
  - Tenant selector dropdown in header (loads all tenants, switches binding context)
  - Managed app catalog grid — each card shows app name/slug/version + binding status (active/inactive/not bound)
  - `BindingPanel` side panel: active toggle, per-param inputs (text/password/enum), save button
  - `BindingPanel` pre-fills from existing binding config; inline create on first save
- `frontend/src/components/Sidebar.tsx`: "Managed Apps" nav entry (`extension` icon); removes pre-existing duplicate Tenants entry

## Step 12 — COMPLETE

### What Step 12 built

- `db/056_tenant_quotas.sql` — creates `them.tenant_quotas` with `tenant_id PK`, `plan TEXT CHECK(...)`, and 9 nullable limit columns (max_agents, max_apps, max_mcp_servers, max_concurrent_runs, max_users, monthly_llm_tokens, monthly_runs, api_requests_per_minute, runs_per_minute); inserts bootstrap tenant default row (plan='enterprise')
- `go/internal/admin/dal/tenants.go` — added `TenantQuota` struct (nullable int/int64 pointer fields, 11 columns), `GetQuota`, `UpsertQuota` (ON CONFLICT DO UPDATE RETURNING) DAL methods
- `go/internal/admin/tenants.go` — added `GetQuota` (GET /admin/tenants/{id}/quota) and `UpsertQuota` (PUT /admin/tenants/{id}/quota) handlers; plan validation (trial/starter/pro/enterprise); routes registered in `TenantsHandler.Routes`
- `go/internal/admin/tenants_test.go` — TN-13..TN-17: GetQuota_NotFound, GetQuota_Found, UpsertQuota_Success, UpsertQuota_BadPlan, UpsertQuota_BadJSON; `quotaFakeRow` + `quotaRow` field on `tenantDB`
- `go/TEST_INDEX.md` — S1-94 updated 12→17 tests; totals 1026→1031 S1, 978→983 go test ./...
- `frontend/src/lib/apiTypes.ts` — `TenantQuota` interface, `QuotaPlan` type
- `frontend/src/lib/api.ts` — `getTenantQuota`, `upsertTenantQuota` API methods; types re-exported
- `frontend/src/app/admin/tenants/page.tsx` — "Quotas" tab added to `TenantPanel`; lazy-loads quota on tab switch; plan `<select>` + 9 nullable number inputs (blank = unlimited); Save Quotas button

All 46 Go packages pass.

### Migration note
`db/056_tenant_quotas.sql` must be applied to the live DB before the quota routes are used:
```bash
docker cp db/056_tenant_quotas.sql them-postgres:/tmp/them_056.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_056.sql
```

---

## Step 13 — COMPLETE

### What Step 13 built

- `go/internal/admin/dal/runs.go` — `CountActiveRuns(ctx, tenantID)` — `COUNT(*)` of runs with status `admitted`, `running`, or `input_required` for the tenant. Used by quota enforcement.
- `go/internal/quota/enforcer.go` — new package:
  - `RunCounter` interface (DB) + `RedisIncrementer` interface (Redis)
  - `Quota` struct: `MaxConcurrentRuns *int`, `RunsPerMinute *int`
  - `Enforcer.Check(ctx, tenantID, quota)` — enforces both limits; nil limit → skip
  - `ErrConcurrentRunsExceeded`, `ErrRunsRateLimited` sentinels
  - Redis key: `rl:them:{tenant_id}:runs:{minute}`, TTL 90s (same pattern as ratelimit)
  - Fail-open on DB error; returns wrapped error
- `go/internal/quota/enforcer_test.go` — 6 tests (QE-01..06)
- `go/internal/execution/errors.go` — `AdmitErrQuotaConcurrentRuns`, `AdmitErrQuotaRunsPerMinute` (both HTTP 429; static error strings)
- `go/internal/execution/lifecycle.go`:
  - `QuotaEnforcer` interface with `CheckQuota(ctx, tenantID) error`
  - `ErrQuotaConcurrentRuns`, `ErrQuotaRunsPerMinute` package-level sentinels
  - `Lifecycle.quota QuotaEnforcer` field (nil = fail-open)
  - `WithQuotaEnforcer(qe) *Lifecycle` setter (chained after constructor)
  - Enforcement in `Admit` step 5b (after CheckAccess, before gate.Check)
- `go/internal/execution/lifecycle_test.go` — 3 tests (LC-QE-01..03)
- `go/cmd/them/main.go`:
  - `tenantQuotaAdapter` struct: loads quota row from DB via `dal.DB.GetQuota`; maps errors to execution sentinels; fails-open when no quota row exists
  - Wired in section 16a: `execLifecycle.WithQuotaEnforcer(quotaAdapter)`
  - Imports: `admin/dal`, `quota`, `errors`

All 47 packages pass. S1: 1040, go test ./...: 992.

### Step 13 design decisions

- **Fail-open on missing quota row** — `GetQuota` returning `pgx.ErrNoRows` → no enforcement; allows bootstrapping tenants without a quota row
- **Fail-closed on quota hit** — returns 429 before consuming a gate slot
- **Fail-open on DB error counting runs** — the quota enforcer propagates the DB error; `tenantQuotaAdapter` returns it to `Lifecycle` which maps it to `AdmitErrInternal` (500) not 429
- **Redis error on RPM** — propagated, mapped to `AdmitErrInternal` (500); acceptable for rare Redis unavailability
- **No schema migration needed** — `them.tenant_quotas` already created in Step 12

## Step 14 — COMPLETE

### What Step 14 built

- `go/internal/quota/enforcer.go`:
  - `ErrMonthlyRunsExceeded` sentinel
  - `MonthlyRuns *int` field on `Quota` struct
  - `checkMonthly(ctx, tenantID, limit)` — Redis INCR keyed by `rl:them:{tenant_id}:runs:monthly:{YYYY-MM}`; TTL = seconds remaining in current month + 48 h buffer (only set on first increment); returns `ErrMonthlyRunsExceeded` when limit exceeded
  - `Check()` updated to call `checkMonthly` after existing checks
- `go/internal/execution/errors.go`:
  - `AdmitErrQuotaMonthlyRuns` constant; static string `"monthly run limit exceeded"`; HTTP 429 mapping
- `go/internal/execution/lifecycle.go`:
  - `ErrQuotaMonthlyRuns` sentinel; `Check()` switch case maps it to `AdmitErrQuotaMonthlyRuns`
- `go/cmd/them/main.go`:
  - `tenantQuotaAdapter` populates `MonthlyRuns: q.MonthlyRuns` in `quota.Quota`
  - Maps `quota.ErrMonthlyRunsExceeded → execution.ErrQuotaMonthlyRuns`
- `go/internal/quota/enforcer_test.go`: QE-07..09 (MonthlyNilLimit, MonthlyBelowLimit, MonthlyExceeded)
- `go/internal/execution/lifecycle_test.go`: LC-QE-04 (QuotaMonthlyRunsExceeded → AdmitErrQuotaMonthlyRuns)
- `go/TEST_INDEX.md`: S1-95 updated 6→9 tests; S1-35 updated 21→22; totals 1040→1044 S1, 992→996 go test

No schema migration needed — `monthly_runs` column already exists in `them.tenant_quotas` from Step 12.

### Step 14 design decisions
- **Redis key per calendar month** — `rl:them:{tenant_id}:runs:monthly:{YYYY-MM}` avoids precision loss from UNIX-second month boundaries
- **TTL set only on first increment** — avoids race where a late Expire call resets the TTL of an already-expiring key
- **48 h buffer on TTL** — month end is computed from UTC; 48 h absorbs DST surprises and minor clock skew without leaving stale keys indefinitely
- **Fail-open on Redis error** — same policy as `checkRPM`; Redis unavailability returns a wrapped error that `tenantQuotaAdapter` propagates as `AdmitErrInternal` (500), not a false 429

---

## Step 15 — COMPLETE

### What Step 15 built

- `db/057_tenant_llm_providers.sql` — adds `tenant_id UUID FK→them.tenants(id) ON DELETE CASCADE` (nullable) to `them.llm_providers`; drops old `llm_providers_name_key` UNIQUE; adds partial unique indexes `llm_providers_name_platform_uq` (WHERE tenant_id IS NULL) and `llm_providers_name_tenant_uq` (WHERE tenant_id IS NOT NULL); adds lookup index `llm_providers_tenant_id_idx`
- `go/internal/admin/dal/llm_providers.go`:
  - `LLMProvider` struct gains `TenantID *string`
  - `scanProvider` updated to scan 9 columns (adds tenant_id)
  - `ListProviders` now filters `WHERE tenant_id IS NULL` (platform defaults only)
  - `ListProvidersForTenant(ctx, tenantID)` — merged view: tenant overrides + platform defaults not overridden
  - `GetProviderByNameForTenant(ctx, name, tenantID)` — tenant override by name+tenantID
  - `GetProviderByNamePlatform(ctx, name)` — platform default by name
  - `UpsertTenantProvider(ctx, tenantID, in)` — ON CONFLICT(name, tenant_id) WHERE tenant_id IS NOT NULL DO UPDATE
  - `CreateProvider` INSERT unchanged (no tenant_id column set = NULL = platform)
  - `UpdateProvider` and `DeleteProvider` RETURNING updated to scan 9 columns
- `go/internal/admin/service/service.go` — Dal interface: 4 new methods (`ListProvidersForTenant`, `GetProviderByNameForTenant`, `GetProviderByNamePlatform`, `UpsertTenantProvider`)
- `go/internal/admin/service/llm_providers.go`:
  - `LLMProviderOut` gains `TenantID *string` field
  - `toOut` maps `TenantID`
  - `ListForTenant(ctx, tenantID)` — calls `ListProvidersForTenant`
  - `UpsertForTenant(ctx, tenantID, name, body)` — validates, inherits display_name from platform row, encrypts key, delegates to `UpsertTenantProvider`
- `go/internal/admin/llm_providers.go`:
  - `TenantProviderRoutes(r)` — mounts `GET /tenants/{id}/llm-providers` and `PUT /tenants/{id}/llm-providers/{name}`
  - `ListForTenant` and `UpsertForTenant` handler methods
- `go/internal/admin/router.go` — `llmProviders.TenantProviderRoutes(a)` wired into platform-global admin group
- `go/internal/temporal/workerconfig/loader.go`:
  - `loadTenantProviderKey(ctx, tenantID, provider)` — prefers tenant override in llm_providers, falls back to platform default
  - `lookupLLMProviderKey(ctx, provider, tenantID*)` — single-row lookup helper (nil tenantID = platform)
  - `LoadRunConfig`: main LLM key resolution now calls `loadTenantProviderKey` first, then falls back to `loadProviderKey` (per-app key)
  - Summarizer key resolution updated the same way
- Tests: S1-96 (6 service tests), S1-97 (5 handler tests), S1-93 updated (+1 workerconfig test)
- `docs/SCHEMA.md` — `them.llm_providers` updated with `tenant_id` column and constraint notes
- `go/TEST_INDEX.md` — S1-93 updated (3→4), S1-96 and S1-97 added; totals 1044→1056 S1, 996→1008 go test

All 47 Go packages pass.

### Migration note
`db/057_tenant_llm_providers.sql` must be applied to the live DB before the tenant LLM override endpoints are used:
```bash
docker cp db/057_tenant_llm_providers.sql them-postgres:/tmp/them_057.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_057.sql
```

### Step 15 design decisions
- **NULL = platform default** — existing rows remain valid; no backfill needed
- **Two partial unique indexes** — avoids complications with NULL equality in standard UNIQUE constraints
- **Fail-open tenant key lookup** — if tenant has no override, falls back to platform row, then falls back to per-app key in applications.provider_keys; run is never blocked by a missing tenant override
- **UpsertForTenant requires platform row** — enforces that tenant overrides can only name providers that exist at the platform level (prevents typos creating orphaned rows)
- **Summarizer key inherits same resolution chain** — tenant override → per-app key → global env; consistent with main LLM key

---

## Step 16 — (next task)

### Goal
Per-tenant RBAC — tenant-level role assignments and JWT claims:
- `auth_service.tenant_memberships` table already exists (from Step 5: `UpsertOIDCUser`). Verify columns.
- Extend `GET /auth/me` (auth-go) to return `tenant_id` and `role` from the membership row
- Update `POST /auth/login` to accept optional `tenant_slug` param and embed `tenant_id` + `role` in the issued JWT
- Add `GET /admin/tenants/{id}/members` and `POST /admin/tenants/{id}/members` endpoints for managing memberships
- Enforce tenant_id claim in `AdminTenantMiddleware` — remove bootstrap fallback for users who have an explicit membership

### Files to read before starting
- `docs/HANDOVER.md` (this file)
- `go/internal/authserver/handlers.go` — login + me endpoints
- `go/internal/authserver/store.go` — `OIDCStore.UpsertOIDCUser` (shows tenant_memberships schema)
- `go/internal/admin/middleware.go` — `AdminTenantMiddleware` (the bootstrap fallback to remove)
- `go/internal/authserver/jwt.go` — JWT claims struct (add tenant_id + role fields)
- `auth_service/SCHEMA.sql` — tenant_memberships table definition

---

## Step 7 — Runtime parameter injection (original plan, now complete)

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

# Apply pending migrations if not yet applied
docker cp db/055_managed_apps.sql them-postgres:/tmp/them_055.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_055.sql
docker cp db/056_tenant_quotas.sql them-postgres:/tmp/them_056.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_056.sql
docker cp db/057_tenant_llm_providers.sql them-postgres:/tmp/them_057.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_057.sql

# Read before starting
cat docs/HANDOVER.md

# Run tests to confirm baseline (zero failures required)
docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
```

---

## First prompt for next session

```
Continue multi-tenancy implementation — Step 16.

Current state: Steps 1–15 are complete and pushed to main (HEAD: 80c296f).
- Step 1: JWT carries tenant_id; bootstrap fallback removed (4ccb4c4)
- Step 2: Redis key hardening (97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (98ccf03)
- Step 4: Tenant CRUD API (a534a54)
- Step 5: OIDC login flow — PKCE + signed state (2de98f5)
- Step 6: Managed Apps foundation — catalog CRUD + binding activation (7c056fc)
- Step 7: Runtime parameter injection — {{PARAMS.KEY}} substitution in system prompts (0bbfa28)
- Step 8: OIDC JWKS RS256 id_token signature verification — stdlib only (99fc33c)
- Step 9: OIDC JWKS key caching — TTL-based, rotation-aware (2056550)
- Step 10: Tenant provisioning UI + PATCH /admin/tenants/{id} (441f9e7)
- Step 11: Binding management UI — platform-level binding API + frontend Managed Apps page (59105c4)
- Step 12: Tenant quota management — them.tenant_quotas + GET/PUT /admin/tenants/{id}/quota (293fe26)
- Step 13: Quota enforcement at run start — max_concurrent_runs + runs_per_minute (cfaef99)
- Step 14: Monthly run limit enforcement — monthly_runs quota Redis INCR (828739b)
- Step 15: Per-tenant LLM provider key management — tenant_id on llm_providers, merged list API,
           upsert override API, run-time resolution in workerconfig (24ff822)
All 47 Go packages pass (go test ./..., 1056 S1 tests, 1008 go test ./... total).

Read docs/HANDOVER.md fully before starting — it is the source of truth.

Goal for Step 16: Per-tenant RBAC — see Step 16 section in HANDOVER.md.

Constraints (all in HANDOVER.md):
- go test ./... must be zero failures before every commit
- TenantID comes only from JWT claims via tenantctx typed key — never from headers
- 500 responses use static strings only — never err.Error()
- Go runs inside Docker: docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
- Handler → Service → DAL — no SQL in handlers
- TEST_INDEX.md updated in same commit as any new test
```

---

Current state: Steps 1–14 are complete and pushed to main.
- Step 1: JWT carries tenant_id; bootstrap fallback removed (4ccb4c4)
- Step 2: Redis key hardening (97c9d71)
- Step 3: Temporal workflow IDs tenant-prefixed (98ccf03)
- Step 4: Tenant CRUD API (a534a54)
- Step 5: OIDC login flow — PKCE + signed state (2de98f5)
- Step 6: Managed Apps foundation — catalog CRUD + binding activation (7c056fc)
- Step 7: Runtime parameter injection — {{PARAMS.KEY}} substitution in system prompts (0bbfa28)
- Step 8: OIDC JWKS RS256 id_token signature verification — stdlib only, no third-party (99fc33c)
- Step 9: OIDC JWKS key caching — TTL-based, rotation-aware (2056550)
- Step 10: Tenant provisioning UI + PATCH /admin/tenants/{id} (441f9e7)
- Step 11: Binding management UI — platform-level binding API + frontend Managed Apps page (59105c4)
- Step 12: Tenant quota management — them.tenant_quotas + GET/PUT /admin/tenants/{id}/quota + frontend Quotas tab (293fe26)
- Step 13: Quota enforcement at run start — max_concurrent_runs (DB COUNT) + runs_per_minute (Redis INCR) wired into Lifecycle.Admit (cfaef99)
- Step 14: Monthly run limit enforcement — monthly_runs quota (Redis INCR per YYYY-MM, TTL past month end) wired into quota.Enforcer and Lifecycle.Admit
All 47 Go packages pass (go test ./..., 1044 S1 tests, 996 go test ./... total).

Read docs/HANDOVER.md fully before starting — it is the source of truth.

Goal for Step 15: Per-tenant LLM provider key management — see Step 15 section in HANDOVER.md.

Constraints (all in HANDOVER.md):
- go test ./... must be zero failures before every commit
- TenantID comes only from JWT claims via tenantctx typed key — never from headers
- 500 responses use static strings only — never err.Error()
- Go runs inside Docker: docker run --rm -v "$(pwd)/go":/src -w /src golang:1.25-alpine go test ./...
- Handler → Service → DAL — no SQL in handlers
- TEST_INDEX.md updated in same commit as any new test
```

---

## ARCHIVED: First prompt for Step 7 session

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
