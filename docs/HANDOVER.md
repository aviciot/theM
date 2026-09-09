# Handover — End-User Auth Phase 2 + Closure Fixes
# Date: 2026-09-08
# HEAD: (see git log — after Phase 2 closure commit)

---

## What was completed

**Phase 2 of `docs/END_USER_AUTH_PLAN.md`** — the-M user JWT at entry points, runtime-only role, and internal history isolation.

### Migrations (not yet applied to live DB)

| File | What it does |
|---|---|
| `db/086_phase2_user_history.sql` | `them.tasks.user_id INT` + `them.runs.user_id INT` + indexes |
| `db/087_end_user_role.sql` | Seed `end_user` role with `dashboard_access='none'` |

**Apply before restarting containers:**
```bash
docker cp db/086_phase2_user_history.sql them-postgres:/tmp/them_086.sql
docker cp db/087_end_user_role.sql them-postgres:/tmp/them_087.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_086.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_087.sql
```

### Go changes

All changes compile and all 54 packages pass (`go test ./...` — 0 failures).

| File | Change |
|---|---|
| `go/internal/epconfig/epconfig.go` | `AccessModeUser = "user_jwt"` constant |
| `go/internal/domain/domain.go` | `UserID int64` on `Run` |
| `go/internal/execution/request.go` | `UserID int64` on `ExecutionRequest` + `ExecutionHandle` |
| `go/internal/execution/lifecycle.go` | Step 3.5: HS256 JWT validation + tenant check for `AccessModeUser` EPs; `jwtSecret` field; `WithJWTSecret()` |
| `go/cmd/them/main.go` | `execLifecycle.WithJWTSecret(userJWTSecret)` wired at startup |
| `go/internal/runrecorder/recorder.go` | `user_id` ($10) in `CreateRun` INSERT |
| `go/internal/history/pgx.go` | All 5 functions gain `userID int64`; dual-column SQL filter; `resolveRootTaskID` gains `user_id` column |
| `go/internal/orchestrator/orchestrator.go` + `summary.go` | Interface + call site updates for new signatures |
| `go/internal/temporal/workflow.go` + `activities.go` | `UserID int64` in `WorkflowInput` + `RunContext` |
| `go/internal/ws/handler.go` + `go/internal/sse/handler.go` | `UserID: handle.UserID` in `WorkflowInput` |

### Tests added / updated

| File | Tests |
|---|---|
| `go/internal/execution/lifecycle_test.go` | 6 new: `TestAccessModeUser_*` — valid JWT admitted, invalid rejected, cross-tenant forbidden, no token, no secret, UserID stored on run |
| `go/internal/history/pgx_test.go` | 3 new: `TestHistory_UserA_CannotReadUserB`, `TestHistory_InternalCannotReadExternalUser`, `TestHistory_LegacyRows_NotLeakedToUser`; 2 updated: `TestHistory_CrossUser_Denied`, `TestHistory_ServiceToken_ExternalUserIsolation` |
| `go/internal/runrecorder/recorder_test.go` | `TestCreateRun_callsCorrectSQL` updated: arg count 9→10, new `user_id` assert |
| `go/internal/orchestrator/orchestrator_test.go` | `fakeHistoryLoader.LoadHistory` + `fakeCheckpointWriter.WriteMessage` signatures updated |

### Docs updated

- `docs/SCHEMA.md`: `them.runs.user_id` + `them.tasks.user_id` descriptions updated; migrations 086+087 added to migration table
- `docs/END_USER_AUTH_PLAN.md`: gap table updated (Phase 2 rows all ✅); Phase 2 section marked COMPLETE; `AccessModeUser` authorization rule documented
- `docs/CURRENT.md`: Phase 2 completion section added; next-task options updated
- `go/TEST_INDEX.md`: S1-35 (lifecycle) + S1-46 (history) test tables updated with new tests

---

## AccessModeUser authorization rule (explicit)

**Current rule:** Any authenticated member of the EP's tenant can invoke any `AccessModeUser` entry point. JWT validity + `claims.TenantID == EP.TenantID` is the only gate.

**Scheduled fix:** Phase 3 adds `allowed_principals` column to `them.entry_points`, letting operators restrict which principal types may call each EP. Until then, all tenant members have equal access to `AccessModeUser` EPs.

---

## History isolation invariant

SQL dual-column filter in `LoadHistory`, `LoadSummary`, `SaveSummary`, `WriteMessage`, `resolveRootTaskID`:

```sql
AND ($3 = '' OR t.external_user_id = $3)       -- external/backend runs: filter by external_user_id
AND ($3 != '' OR $4 = 0 OR t.user_id = $4)     -- internal runs: filter by user_id when set
```

Isolation properties:
- External user A (`externalUserID="alice"`) cannot read external user B (`externalUserID="bob"`) ✓
- Internal user A (`userID=42`) cannot read internal user B (`userID=99`) ✓  
- Internal session (`userID=42`) cannot read external-user rows (`user_id=NULL`, NULL≠42 in SQL) ✓
- External-user session (`externalUserID="alice"`) cannot read internal-user rows (`external_user_id=NULL`) ✓
- Legacy rows (both NULL) excluded from all user-scoped queries by SQL NULL semantics ✓

---

## E2E verification gate — CLOSED (2026-09-09)

**Status: PASSED.** Both `runs.user_id` and `tasks.user_id` confirmed via live WebSocket runs with JOIN assertions. All prior evidence was invalidated (see bug fix below).

### Bug fix: `CreateTask` missing `user_id` column

**File:** `go/internal/runrecorder/recorder.go`, function `CreateTask`

**Bug:** The INSERT for delegated tasks did not include `user_id` in the column list. Only root tasks (via `resolveRootTaskID` in `go/internal/history/pgx.go`) were storing `user_id`. The `TaskRecorder` interface and orchestrator call site were also missing the parameter.

**Fix:**
- `go/internal/orchestrator/orchestrator.go`: `TaskRecorder.CreateTask` interface: added `userID int64` param; call site passes `rctx.UserID`
- `go/internal/runrecorder/recorder.go`: INSERT now includes `user_id` column with `NULLIF($5, 0)::integer`
- `go/internal/runrecorder/recorder_test.go`: `TestCreateTask_insertsChildTaskRow` updated to assert `user_id` in SQL and arg at index 4

**Docker worker cache fix:** `them-go-worker` and `them-go-worker-2` build to **different image names** (both use `Dockerfile.go-worker` but have separate image tags). Rebuilding one does not update the other. Fix: explicitly name both services in `docker compose build` and `--force-recreate` both containers.

### Live run details (2026-09-09)

- App: `e2e-test-app`, EP: `e2e-ep` (mode: `user_jwt`), orchestrator delegates to `a2a_echo`
- User A: `e2e_ua` (auth_service.users.id=63), User B: `e2e_ub` (auth_service.users.id=64)

**Run 1 — delegation check:**
- Run A: `af687e08-9442-4be5-a48a-ed03e718f595`, context: `0263b839-...`
- User A sends "Please delegate to a2a_echo and echo back: verify-delegation"

**Run 2 — isolation check:**
- Run B: `04d1ca7f-6dea-4920-8fb0-c458f63f45d9`, **same** context_id: `0263b839-...`
- User B sends "what did the previous user say?" with A's context_id

### Verified assertions (all directly queried from DB after runs completed)

**Assertion 1 — Delegated task `user_id` matches `runs.user_id`:**

```sql
SELECT r.id, r.user_id AS run_uid, t.kind, t.user_id AS task_uid,
       (r.user_id IS NOT DISTINCT FROM t.user_id) AS consistent
FROM them.runs r JOIN them.tasks t ON t.run_id = r.id
WHERE r.id = 'af687e08-9442-4be5-a48a-ed03e718f595'::uuid
ORDER BY t.kind;

-- Result:
-- af687e08-... | 63 | delegated | 63 | t   ← CreateTask fix verified
-- af687e08-... | 63 | root      | 63 | t
```

Both root and **delegated** tasks carry `user_id=63`, matching `runs.user_id=63`.

**Assertion 2 — User B using A's `context_id` does NOT load A's history:**

```sql
-- All messages in the shared context_id, grouped by owner
SELECT t.user_id, t.kind, t.run_id, tm.role, LEFT(tm.parts::text, 80)
FROM them.task_messages tm
JOIN them.tasks t ON tm.task_id = t.id
WHERE t.context_id = '0263b839-d896-45d4-960e-4cdcf241f499'::uuid
ORDER BY t.user_id, tm.seq;

-- Result: 8 rows — 4 with user_id=63 (A's run), 4 with user_id=64 (B's run)
-- B's agent reply: "what did the previous user say?" — echoed back verbatim
-- → B's LLM received empty history (no knowledge of A's prior message)
```

LoadHistory SQL with `userID=64` applied to shared context:
```sql
WHERE t.context_id = '0263b839-...'::uuid
  AND ('' = '' OR t.external_user_id = '')          -- no external_user_id filter
  AND ('' != '' OR 64 = 0 OR t.user_id = 64)        -- filter: only user_id=64

-- Result: 4 rows, ALL user_id=64 — zero of A's (user_id=63) rows returned
```

B's task writes preserve isolation: B's task has `user_id=64`, A's messages `user_id=63` are physically present in the same `context_id` but excluded by the per-user SQL filter.

| Check | Result | Evidence |
|---|---|---|
| User A `runs.user_id=63` | ✅ PASS | Live query |
| `delegated` task `user_id=63` == `runs.user_id` | ✅ PASS | JOIN consistent=`t`, `CreateTask` fix verified |
| User B `runs.user_id=64` | ✅ PASS | Live query |
| B's root task `user_id=64` | ✅ PASS | Live query |
| B uses A's `context_id` → 0 of A's messages loaded | ✅ PASS | LoadHistory SQL returns 0 user_id=63 rows for B |
| B's LLM response shows no knowledge of A's content | ✅ PASS | B echoed its own message; A's "verify-delegation" not visible |

**`user_jwt` EPs are safe to enable for production end-users.** The full attribution chain is verified: bridge lifecycle → `runs.user_id` → Temporal `WorkflowInput.UserID` → worker root task `user_id` + delegated task `user_id`. History isolation holds: same `context_id`, different `user_id` → zero cross-user message leakage.

---

## Identity propagation bug fix (commit 6843934)

**Bug:** In WS and SSE `ServeHTTP`, the `else-if` that populated `tokenInfo` was skipped whenever `resolved_tenant_id` was set by the slug resolver. Consequence: `tokenInfo.IsBackend` was always `false` on slug-routed paths (`/{tenant_slug}/apps/...`), so `X-External-User` was silently dropped even for valid backend tokens.

**Fix:** Token validation is now unconditionally attempted before the tenant priority block. The slug-derived UUID still takes precedence for tenant resolution, but `tokenInfo` is always populated.

**Regression tests added (commit 6843934, all 54 packages pass):**

| Test | What it proves |
|---|---|
| `TestWS_SlugPath_BackendToken_ExternalUserPropagated` | Backend token on `AppsWSRoute` slug path → X-External-User IS propagated |
| `TestWS_SlugPath_NonBackendToken_ExternalUserIgnored` | Non-backend token on slug path → X-External-User is ignored |
| `TestSSE_SlugPath_BackendToken_ExternalUserPropagated` | Backend token on `AppsSSERoute` slug path → X-External-User IS propagated |
| `TestSSE_SlugPath_NonBackendToken_ExternalUserIgnored` | Non-backend token on slug path → X-External-User is ignored |

---

## IAM UI Stage 1 — IMPLEMENTED + DEPLOYED (2026-09-09)

Bank-team onboarding flow: tenant creation → SSO → bank employee login → Members.

### Backend added

| File | Change |
|---|---|
| `go/internal/admin/dal/tenants.go` | `UpdateMemberRole(ctx, tenantID, userID, role)` — UPDATE with RETURNING for no-rows detection |
| `go/internal/admin/tenant_self_service.go` | `PatchMyMember` handler + route `PATCH /api/v1/tenant/members/{user_id}` |

**Auth:** `RequireTenantAdmin` middleware (already applied to all `/tenant/` routes) — only `admin` or `super_admin` membership role can update member roles.

**Role validation:** `viewer` | `member` | `admin` only — `super_admin` is rejected at handler level.

### Frontend fixed

| File | What changed |
|---|---|
| `frontend/src/app/tenant/members/page.tsx` | `handleSave` calls `themApi.patchMyMember(user_id, role)` → `PATCH /api/v1/tenant/members/{id}` instead of super-admin-only `updateUser` |
| `frontend/src/app/tenant/settings/page.tsx` | `useEffect` populates `idpDiscoveryUrl`, `idpClientId`, `idpRedirectUri` from `t.idp_config` on load |
| `frontend/src/app/admin/tenants/ProvisionWizard.tsx` | Done step (Step 4) shows SSO setup callout with "Open tenant panel → SSO" button |
| `frontend/src/app/login/page.tsx` | "Sign in with organization code" toggle — text input for tenant slug, triggers `GET /api/auth/oidc/start?tenant=<slug>` without email lookup |

### Tests

TSS-09, TSS-10, TSS-11 for `PatchMyMember` and TSS-12 for `GetSettings` IDP config response.
All in `go/internal/admin/tenant_self_service_test.go`. All admin tests pass.

Also: `GetSettings` now calls `GetTenantDetail` (not `GetTenant`), so the response
includes `idp_config.{discovery_url,client_id,redirect_uri}` when SSO is configured.
`client_secret` is always omitted (`""` in the struct → omitempty in JSON). This is the
backend prerequisite for the SSO fields pre-population fix in the settings page.

### Live API verification (2026-09-09)

- `GET /api/v1/tenant/settings` (avi-test tenant, SSO-configured):
  - `idp_configured: true` ✅
  - `idp_config.discovery_url`, `client_id`, `redirect_uri` present ✅
  - `client_secret` absent ✅
- `PATCH /api/v1/tenant/members/24` (role: viewer → member):
  - HTTP 204 ✅
  - DB confirmed: `auth_service.tenant_memberships.role = 'member'` for user_id=24 ✅

### Stage 2 — IMPLEMENTED + DEPLOYED (2026-09-09) — commit 2c930cd

**Group Mappings tab in admin tenant panel + Keycloak groups mapper**

#### Frontend changes

| File | Change |
|---|---|
| `frontend/src/lib/apiTypes.ts` | Added `GroupMapping`, `GroupMappingInput` interfaces |
| `frontend/src/lib/api.ts` | Added `listGroupMappings`, `upsertGroupMapping`, `deleteGroupMapping` API methods |
| `frontend/src/app/admin/tenants/page.tsx` | Added `'groups'` tab to `TenantPanel` — lists existing mappings, add/update form (group claim, role dropdown, priority), delete button |

No backend Go changes — group mapping API already existed at `GET/PUT/DELETE /api/v1/admin/tenants/{id}/group-mappings` (super_admin only).

#### Keycloak configuration

- `them-m` client: **Group Membership** protocol mapper added — claim name `groups`, full path `false`, included in ID + access + userinfo tokens
- Group `bank-admins` created in `them` realm
- User `bankadmin` created (email: `bankadmin@test.com`, password: `admin123`) and added to `bank-admins` group
- `avi1` has no groups — stays `viewer`

#### Mapping configured

`bank-admins → admin, priority 1` added for `avi-test` via the backend API (`PUT /api/v1/admin/tenants/d20402c8-.../group-mappings`). Visible in the new Group Mappings tab.

#### SSO flow for bankadmin

1. Login at `http://<host>:8088` → "Sign in with organization code" → slug: `avi-test`
2. Keycloak login: `bankadmin` / `admin123`
3. Keycloak emits `groups: ["bank-admins"]` in ID token
4. `OIDCCallback` calls `GetGroupRole` → `bank-admins → admin` → `UpsertOIDCUser(role="admin")`
5. `bankadmin` gets `admin` membership in `avi-test`; platform role stays `viewer`

#### Verification done

- `GET /api/v1/admin/tenants/{id}/group-mappings` returns `[]` before mapping, correct JSON after ✅
- `PUT` returns 200 with the created mapping row ✅
- DB: `them.tenant_group_mappings` has 1 row: `bank-admins | admin | 1` for `avi-test` ✅
- `bankadmin` Keycloak user is in `bank-admins` group ✅
- `avi1` has no groups ✅
- `go test ./internal/admin/...` and `./internal/authserver/...` — both pass ✅
- Frontend rebuilt and running ✅

#### Browser test instructions

1. Open `http://<host>:8088/admin/tenants`
2. Log in as `admin` / `admin123`
3. Click `avi-test` → open **Group Mappings** tab → should show `bank-admins / admin / 1`
4. Open new private tab → log in with organization code `avi-test` → use `bankadmin` / `admin123` at Keycloak → expect landing in tenant member panel with `admin` role
5. Open another new private tab → log in with organization code `avi-test` → use `avi1` / `admin123` at Keycloak → expect `viewer` access (dashboard only)
6. Confirm `bankadmin` can see Members panel; `avi1` cannot reach admin screens

#### Remaining unchecked (live browser test)

- Actual Keycloak → THE-M callback for `bankadmin` (SSO E2E not run — requires browser session)
- `bankadmin` email `bankadmin@test.com` will create a new THE-M user on first SSO login (not pre-provisioned)

---

## What was NOT done (remaining phases)

| Phase | What | Why deferred |
|---|---|---|
| Phase 3 | `allowed_principals` guard on EPs | ✅ COMPLETE (2026-09-08) — migration 088, `CheckPrincipal` in lifecycle, 17 new tests |
| Phase 4 | Bank JWT / JWKS validation | Requires `JWKSAuthenticator` + `tenant_runtime_config` table — significant new surface |
| Phase 5 | Managed app runtime routing | Requires `epConfigQuery` JOIN on `managed_app_bindings`; consuming-tenant data+quota attribution |

---

## Phase 2 closure fixes (same session, after review)

Three issues found in code review of ef51f6b:

### Fix 1 — `end_user` runtime-login endpoint

**Problem:** Migration 087 comment referenced "runtime-login" but no such endpoint existed. `end_user`-role users could not authenticate at all — `Login` rejects `dashboard_access='none'` and `RuntimeLogin` was absent.

**Fix:**
- `authserver/service.go`: `RuntimeLogin()` — authenticates username+password, skips `dashboard_access` gate, calls `issuePair`. No API-key path (password-only for runtime users).
- `authserver/handlers.go`: `RuntimeLogin()` handler — does NOT set dashboard cookies (runtime-only tokens must not create dashboard sessions).
- `authserver/router.go`: registered at `POST /api/v1/auth/runtime-login` (and `/auth/` mirror).

**Tests added:** `TestRuntimeLogin_EndUserRoleAdmitted`, `TestRuntimeLogin_DashboardLoginStillDenied`, `TestRuntimeLogin_TokenIsAccessType`, `TestRuntimeLogin_RefreshWorks`, `TestRuntimeLogin_WrongPassword`, `TestRuntimeLogin_NoAPIKey`.

### Fix 2 — Refresh token rejection in `ValidateHS256JWT`

**Problem:** The authserver signs access tokens with `type="access"` and refresh tokens with `type="refresh"` using the **same** HMAC-SHA256 key. `auth.ValidateHS256JWT` did not check the `type` claim — a refresh token was a valid bearer credential at any `AccessModeUser` EP.

**Fix:** `auth/jwt.go`: added `Type string` field to `hs256RawClaims`; after expiry check, returns `ErrTokenMalformed` if `raw.Type != "" && raw.Type != "access"`.

**Tests added:** `TestValidateHS256JWT_RefreshTokenRejected` (refresh token → `ErrTokenMalformed`), `TestValidateHS256JWT_AccessTokenAccepted` (type="access" passes).

### Fix 3 — History isolation tests replaced with integration tests

**Problem:** The three Phase 2 history isolation tests (`TestHistory_UserA_CannotReadUserB`, `TestHistory_InternalCannotReadExternalUser`, `TestHistory_LegacyRows_NotLeakedToUser`) inspected SQL string constants copied into the test body — they did not exercise the actual Store queries against PostgreSQL.

**Fix:** Removed the three SQL-constant tests. Added `internal/history/pgx_integration_test.go` (build tag `integration`) with 5 tests that call actual `Store.WriteMessage` and `Store.LoadHistory` against a live DB. Each test uses a unique `contextID` + `tenantID` to avoid cross-test interference.

Run: `go test -tags=integration ./internal/history/... ` (requires `DATABASE_PASSWORD`).

---

## Phase 2 deployment verification (2026-09-08)

All services rebuilt and recreated against the live stack. Full smoke test run.

### Rebuild scope

| Image | Dockerfile | Rebuilt |
|---|---|---|
| `them-auth-go` | `Dockerfile.auth-go` | ✅ |
| `them-go-bridge` | `Dockerfile.go` | ✅ |
| `them-go-worker` | `Dockerfile.go-worker` | ✅ |
| `them-dag-worker` | `Dockerfile.dag-worker` | ✅ |

Note: `Dockerfile.go-worker` is separate from `Dockerfile.go`. Both worker images were rebuilt and recreated. (Prior deployment documentation only listed auth + bridge.)

Vendor directory was out of sync — `go mod vendor` run before rebuild.

### Smoke test results

| Step | Result | Notes |
|---|---|---|
| `POST /auth/runtime-login` | ✅ PASS | Returns access + refresh tokens |
| Dashboard denied with `access_token` | ✅ PASS | 403 — membership role=member, dashboard_access=none |
| Normal `POST /auth/login` blocked | ✅ PASS | 403 — `dashboard_access='none'` rejects at login |
| `POST /auth/refresh` issues new tokens | ✅ PASS | Bearer header accepted |
| Refresh token rejected as bearer | ✅ PASS | 401 — `type="refresh"` blocked by `ValidateHS256JWT` |
| Dashboard denied after refresh (bearer) | ✅ PASS | 403 |
| Dashboard denied after refresh (cookie) | ✅ PASS | 401 — cookie alone is not sufficient |
| JWT `sub` matches `auth_service.users.id` | ✅ PASS | DB confirmed |

### /refresh sets dashboard cookies — boundary verified

`POST /auth/refresh` calls `h.setAuthCookies(w, pair)` which sets `them_access_token` and `them_refresh_token` cookies. This is the standard response for all refresh flows (login and runtime-login share the same handler).

**Why this is safe:** The bridge's dashboard authorization checks the JWT `role` claim against `dashboard_access` in `auth_service.roles`. An `end_user`-role JWT has `dashboard_access='none'` — the bearer check returns 403 regardless of how the token was delivered (header or cookie). Verified: cookie-bearing request after refresh returns 401 (no cookie-only auth path exists on admin routes).

**Residual note for Phase 3+:** If a future session adds cookie-to-JWT exchange for the frontend, the `dashboard_access` check must be applied there too. Not a current gap.

### Integration history tests

All 5 isolation tests pass against the live DB (after fixing `resolveRootTaskID findQ` UUID cast + FK-safe `seedUser` helper). HEAD: `ac7c118`.

---

## Phase 2 E2E — user_id on runs (confirmed 2026-09-07)

**Verification method:** Created a fresh `user_jwt` EP directly in DB (bypasses 30s cache), created `end_user` account via API, runtime-login'd, connected via WS, queried `them.runs` by `entry_point_slug` **before** deleting the test user (avoiding FK ON DELETE SET NULL cascade).

**Result: PASS**

```
EP: check-uid-a321e8c9
User id=38  JWT uid=38
  <- ready  run_id=d0e50a33-fef5-424a-b81e-17fa107579ac

runs by slug: d0e50a33-fef5-424a-b81e-17fa107579ac|38||check-uid-a321e8c9
run by id:   d0e50a33-fef5-424a-b81e-17fa107579ac|38|

PASS: user_id in runs
```

**What this confirms:**
- `user_id=38` (from JWT `sub=38`) is written to `them.runs.user_id` by the bridge recorder
- The bridge session lifecycle correctly extracts `UserID` from the HS256 JWT and threads it through `ExecutionHandle → RunRecorder → CreateRun INSERT`
- The FK ON DELETE SET NULL (`runs_user_id_fkey`) was masking previous verification attempts — querying after user deletion always shows NULL

**Why `them.tasks.user_id` is empty:** The run status was `failed` — the orchestrator started but the Temporal worker didn't complete any activity (no LLM agent running during smoke test). Task creation happens inside the Temporal workflow; `tasks.user_id` is written by the worker at task-creation time. The bridge half of Phase 2 is verified. Task-level attribution will appear in any successful orchestration run.

**Phase 2 is CLOSED.** All three closure fixes (RuntimeLogin, refresh-token guard, integration history tests) plus E2E `runs.user_id` persistence are confirmed.

---

## Next session startup

```bash
# 1. Apply Phase 2 migrations (if not yet done)
docker cp db/086_phase2_user_history.sql them-postgres:/tmp/them_086.sql
docker cp db/087_end_user_role.sql them-postgres:/tmp/them_087.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_086.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_087.sql

# 2. Rebuild the Go auth server and bridge (IMPORTANT: must build before recreate;
#    docker compose restart does not deploy a newly built image)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  build them-auth-go them-go-bridge

# 3. Recreate affected containers (not restart — restart reuses the old image)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d --force-recreate them-auth-go them-go-bridge them-go-worker them-dag-worker

# 4. Verify
docker logs them-auth-go --tail 5
docker logs them-go-bridge --tail 5

# 5. Smoke test runtime-login
curl -s -X POST http://localhost:8088/auth/api/v1/auth/runtime-login \
  -H "Content-Type: application/json" \
  -d '{"username":"end_user_test","password":"..."}' | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('access_token','ERROR:',d))"
```

**First prompt for next session:**

> Read `docs/CURRENT.md` and `docs/END_USER_AUTH_PLAN.md`. Phase 2 + closure fixes are complete. Next task: implement Phase 3 — `allowed_principals` guard on `them.entry_points`. See Phase 3 spec in `docs/END_USER_AUTH_PLAN.md`. Run `go test ./...` before committing. Confirm plan before writing code.

---

## Known pending items

- **Migration 081** (`db/081_tenant_group_mappings_safe_roles.sql`) — not confirmed applied to live DB. Apply before enabling OIDC group mapping.
- **Phase 2 migrations** (086, 087) — ✅ applied to live DB (2026-09-08).
- **All 4 Go service images rebuilt and recreated** — ✅ complete (2026-09-08): auth-go, go-bridge, go-worker, dag-worker.
- **Integration history tests** — ✅ all 5 pass against live DB (2026-09-08, HEAD ac7c118).
- **`THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN`** must be in `.env` — run `./generate-env.sh` if missing.
