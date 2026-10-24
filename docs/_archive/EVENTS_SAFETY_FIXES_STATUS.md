# Events Safety Fixes Status

**Date**: 2026-10-20  
**Branch**: `reliability/events-safety-fixes`  
**Status**: ⚠️ **Partial - Auth Identity Fix Incomplete**

---

## Completed (Commits `b99f1907`, `5e76cd20`)

### ✅ Event Mode Validation
- Created `internal/events/mode.go` with `ValidateEventsMode()` and `RequireRedisForMode()`
- Rejects unknown event modes at startup (prevents silent misconfiguration)
- **Blocks clickhouse-only mode** until Phase 2 features complete:
  - Missing: budget accounting queries (monthly spend tracking)
  - Missing: request body storage (DEBUG/AUDIT logging levels)
- Only allows: `postgres` (default, fully supported), `dual` (Postgres authoritative + ClickHouse mirror)
- Empty env var defaults to `postgres` (safe default)
- Updated: `cmd/them/main.go`, `cmd/auth-server/main.go`, `cmd/mcp-service/main.go`

### ✅ Documented Dual Mode Safety
- Event emission is best-effort (errors logged, don't block requests)
- Postgres write is authoritative (failure blocks the request)
- Mirror failures preserve the Postgres record
- Updated: `internal/llmgateway/dal.go` with safety documentation

### ✅ Documentation Updates
- Added `them-event-writer` to container map in `CLAUDE.md`
- Added `internal/events/` and `cmd/event-writer/` to package map in `go/CLAUDE.md`
- Added trigger map entry: event changes require rebuild + restart
- Added event stack commands with `--profile events`
- Documented event modes and clickhouse blocking

---

## Incomplete (Not Committed)

### ❌ Auth Event Attribution Using Verified Identity

**Status**: Implementation started, compilation errors

**Goal**: Auth events (login, logout, refresh) must use verified JWT claims for tenant_id and user_id attribution. Never attribute logout success to forged/unverified tokens.

**Changes Made** (not committed due to errors):
1. Modified `Service.Logout()` to return `(*verifiedClaims, error)` - verified identity or nil
2. Added `Service.VerifyToken()` method to expose JWT verification for event attribution
3. Created `Handlers.emitUserActivityWithClaims()` - uses verified claims or records "unknown" for unverified
4. Updated `Login`, `Logout`, `Refresh` handlers to use verified claims

**Compilation Errors**:
- `go/internal/authserver/service.go:214:9`: type mismatch with `verifiedClaims` pointer
- `go/internal/authserver/service.go:269:9`: same type mismatch in second location
- Issue: `signer.Verify()` returns `verifiedClaims` (value), but we're trying to return `*verifiedClaims` (pointer)
- Need to check `jwt.go` to understand the actual return type of `Verify()`

**Files Modified** (unstaged):
- `go/internal/authserver/handlers.go`
- `go/internal/authserver/service.go`

**Preserved Behavior**:
- Logout still succeeds with invalid tokens (fail-open for UX)
- Authentication behavior unchanged
- Only event attribution changed

---

## Remaining Work

### 1. Fix Auth Identity Compilation Errors
**Blocker**: Need to resolve `verifiedClaims` type mismatch
- Check `internal/authserver/jwt.go` for `tokenSigner.Verify()` return type
- May need to change method signature or handling
- Run `go test ./internal/authserver/...` to verify after fix

### 2. Write Tests (Not Started)
Per original requirements:
- **Test invalid event modes**: Unknown values, clickhouse blocking
- **Test mirror failure in dual mode**: Mock event sink fails, PG write succeeds
- **Test tenant attribution from JWT**: Verified claims used for events
- **Test forged logout token rejection**: nil claims recorded as "unknown"

### 3. Run Regression Tests
- Auth server tests: `go test ./internal/authserver/...`
- Events tests: `go test ./internal/events/...`
- Gateway tests: `go test ./internal/llmgateway/...`

### 4. PG-ClickHouse Reconciliation (Critical)
Per user requirement: "total event counts alone do not prove 100% delivery"

**Must verify**:
1. **Request-level reconciliation**: Every `gateway_requests.id` in PG has matching `request_id` in ClickHouse `events_gateway_request`
2. **Field-level reconciliation**: tenant_id, tokens_in, tokens_out, cost_usd match between PG and CH
3. **Gap detection**: Identify any missing events, not just count totals
4. **Query**: 
   ```sql
   -- PostgreSQL
   SELECT id as request_id, tenant_id, tokens_in, tokens_out, cost_usd 
   FROM them.gateway_requests 
   WHERE created_at >= NOW() - INTERVAL '1 hour'
   ORDER BY created_at;
   
   -- ClickHouse
   SELECT request_id, tenant_id, tokens_in, tokens_out, cost_usd
   FROM them_events.events_gateway_request
   WHERE ts >= now() - INTERVAL 1 HOUR
   ORDER BY ts;
   
   -- Compare: JOIN on request_id, check for mismatches or missing rows
   ```

---

## Summary for Next Session

**Completed and Safe**:
- Mode validation (blocks bad configs, clickhouse-only)
- Documentation updates
- Branch: `reliability/events-safety-fixes` (2 commits, pushed: no)

**Blocked on Compilation**:
- Auth identity fix (Go type error needs resolution)
- Files: `go/internal/authserver/{handlers,service}.go` (unstaged)

**Next Steps**:
1. Debug `verifiedClaims` type mismatch in auth code
2. Complete auth identity fix and test
3. Write focused tests for mode validation and auth attribution
4. Perform proper PG-ClickHouse reconciliation (not just count totals)
5. Report evidence: matching request_ids, field comparisons, gap analysis

**Do Not Merge**: Per user instructions, deliver branch for review, not merge to main.
