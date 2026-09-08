# Handover — End-User Auth Phase 2 Complete
# Date: 2026-09-08
# HEAD: 77bb7d0

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

## What was NOT done (remaining phases)

| Phase | What | Why deferred |
|---|---|---|
| Phase 3 | `allowed_principals` guard on EPs | Separate DB migration + CheckAccess change; not needed for Phase 2 correctness |
| Phase 4 | Bank JWT / JWKS validation | Requires `JWKSAuthenticator` + `tenant_runtime_config` table — significant new surface |
| Phase 5 | Managed app runtime routing | Requires `epConfigQuery` JOIN on `managed_app_bindings`; consuming-tenant data+quota attribution |

---

## Next session startup

```bash
# 1. Get token
curl -s -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])"

# 2. Apply Phase 2 migrations (if not yet done)
docker cp db/086_phase2_user_history.sql them-postgres:/tmp/them_086.sql
docker cp db/087_end_user_role.sql them-postgres:/tmp/them_087.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_086.sql
docker exec them-postgres psql -U them -d them -f /tmp/them_087.sql

# 3. Rebuild and restart the Go bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal restart them-go-bridge them-go-worker them-dag-worker
```

**First prompt for next session:**

> Read `docs/CURRENT.md` and `docs/END_USER_AUTH_PLAN.md`. Phase 2 is complete. Next task: implement Phase 3 — `allowed_principals` guard on `them.entry_points`. See Phase 3 spec in `docs/END_USER_AUTH_PLAN.md`. Run `go test ./...` before committing. Confirm plan before writing code.

---

## Known pending items

- **Migration 081** (`db/081_tenant_group_mappings_safe_roles.sql`) — not confirmed applied to live DB. Apply before enabling OIDC group mapping.
- **Phase 2 migrations** (086, 087) — written but not yet applied to live DB. Apply + rebuild before first AccessModeUser EP test.
- **`THEM_DB_URL_APP`/`THEM_DB_URL_ADMIN`** must be in `.env` — run `./generate-env.sh` if missing.
