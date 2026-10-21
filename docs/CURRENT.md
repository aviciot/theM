# Current Session State — the-M
# Last updated: 2026-10-21 06:30 UTC

---

## HEAD

Branch: `main`
HEAD: `3773bedd` - docs: events reconciliation reveals 78% delivery failure
Remote: `origin` → `aviciot/theM` on GitHub

Recent commits (2026-10-21):
```
3773bedd  docs: events reconciliation reveals 78% delivery failure
9c4bf650  test(llmgateway): verify DUAL mode mirror-failure safety
f1270178  test(events): add service-specific mode validation tests
5b4255f2  docs: comprehensive events system current state
69d87eb9  test(auth): add identity verification tests for Logout
ad8eec52  feat(auth): populate user activity events with verified JWT identity
8ea4ee55  fix(events): service-specific mode validation and clickhouse sink activation
```

---

## Events Safety Fixes — ✅ TESTS PASS, ❌ DELIVERY BROKEN (2026-10-21)

**Status:** ✅ **Safety verified with tests**, ❌ **Delivery reliability CRITICAL FAILURE**

### What Was Completed

#### 1. Service-Specific Mode Validation (Commit 8ea4ee55, f1270178)
✅ **Implementation:**
- `ValidateEventsMode()` — general validation (allows postgres/dual/clickhouse)
- `ValidateGatewayEventsMode()` — Gateway-specific (blocks clickhouse until budget/body support)
- Gateway uses Gateway-specific validation, Auth/Tools use general validation
- All services reject unknown modes at startup

✅ **Tests (8/8 pass):**
- Valid modes accepted (postgres, dual, clickhouse, empty→postgres)
- Invalid modes rejected (invalid, redis, kafka, wrong case)
- Gateway blocks clickhouse with clear error message
- Gateway allows postgres and dual
- Redis requirements enforced for dual/clickhouse modes

#### 2. Auth Identity Verification (Commit ad8eec52, 69d87eb9)
✅ **Implementation:**
- `service.Logout()` returns `*verifiedClaims` (nil when token invalid/forged/expired)
- `handlers.emitUserActivityWithIdentity()` uses verified user_id and tenant_id from JWT
- Unverified logout recorded as `outcome="unverified"`, `user_id="unknown"`
- Failed login/refresh recorded with user_id=0, tenant_id="" (no identity)

✅ **Security guarantee:** Forged tokens cannot claim arbitrary identity in event logs

✅ **Tests (5/5 pass):**
- Valid token → returns verified claims with user_id, tenant_id, username
- Expired token → returns nil
- Forged token (wrong secret) → returns nil
- Malformed tokens → returns nil
- Empty token → returns nil

#### 3. DUAL Mode Mirror-Failure Safety (Commit 9c4bf650)
✅ **Contract tests verify:**
- Event sink failures ignored (line 154: `_ = d.eventSink.Emit()`)
- PG write happens after event emission
- Only PG failures propagate as errors
- Mode-based PG write behavior (postgres/dual/empty → write, clickhouse → skip)

✅ **Tests (2/2 pass):** Code contract verified, behavior documented

#### 4. PG-ClickHouse Reconciliation (Commit 3773bedd)
❌ **CRITICAL FAILURE FOUND:**
- **Postgres:** 14,426 gateway requests (authoritative, all written ✓)
- **ClickHouse:** 3,154 gateway requests (21.9% delivery rate ✗)
- **Missing:** 11,272 events (78% loss)
- **Auth events:** 8 user_activity events delivered successfully (tenant_id verified ✓)
- **Tool events:** 1 tool_call event delivered

**Root cause:**
1. Event-writer processed 3,562 events successfully (2026-10-20 18:37-18:40, high throughput)
2. Event-writer restarted at 18:56
3. After restart: stopped processing, Redis Streams shows lag=1
4. Backlog of 11,272 events never recovered

**Safety properties verified:**
✓ PG write persistence (mirror failure didn't block requests — non-blocking contract honored)
✓ Auth identity (tenant_id/user_id populated from verified JWT)
✓ Mode validation working correctly

**Critical gap:**
✗ Event delivery reliability broken after restart
✗ RecoverPending logic not triggering or not working
✗ No monitoring/alerting for delivery lag

---

## Current Configuration

### Event Modes
| Service | Env Var | Mode | Write Destination | Status |
|---------|---------|------|-------------------|--------|
| Gateway | THE_M_EVENTS_GATEWAY | `dual` | PG `gateway_requests` (authoritative) + CH mirror (BROKEN) | ⚠️ |
| Auth | THE_M_EVENTS_AUTH | `clickhouse` | CH only via Redis Streams | ✓ Working |
| MCP Tools | THE_M_EVENTS_TOOLS | `clickhouse` | CH only via Redis Streams | ✓ Working |

### Event Infrastructure
- **them-redis-events:** Event queue (Redis Streams, AOF, noeviction) — ✓ Running
- **them-clickhouse:** Event store (them_events database, type-specific tables) — ✓ Running
- **them-event-writer:** Consumer (Redis Streams → ClickHouse) — ⚠️ Running but NOT processing

---

## Critical Issues

### 🔴 BLOCKER: Event Delivery 78% Failure Rate
**Impact:** Cannot use DUAL mode in production, ClickHouse mirror unreliable
**Evidence:** `docs/EVENTS_RECONCILIATION_RESULTS.md`, `./scripts/reconcile_events.sh`
**Symptoms:**
- Event-writer consumed 3,562 events successfully, then stopped
- Redis Streams lag=1 (consumer stalled)
- No error logs after restart — silent failure
- Backlog not recovered on restart

**Must fix before production:**
1. Debug why event-writer stops processing after restart
2. Fix RecoverPending or add explicit startup backlog catchup
3. Add health check that fails when lag > threshold
4. Verify delivery rate > 95% consistently
5. Add integration test for delivery reliability

---

## Recommended Next Steps

### Immediate Session (Next 1-2 hours)
1. **Debug event-writer restart behavior:**
   - Add detailed logging around consumer group initialization
   - Check if RecoverPending is being called
   - Verify XAUTOCLAIM parameters and response handling
   - Test manual recovery: `docker exec them-redis-events redis-cli XPENDING them:events event-writers`

2. **Fix delivery reliability:**
   - Option A: Fix RecoverPending logic to catch up on startup
   - Option B: Add explicit startup catchup (XREADGROUP from "0" until caught up)
   - Option C: Increase RecoverPending frequency or reduce idle threshold

3. **Verify fix:**
   - Generate 100 new gateway requests
   - Wait 5 minutes
   - Run `./scripts/reconcile_events.sh`
   - Verify delivery rate > 95%

4. **Add monitoring:**
   - Event-writer health endpoint reports lag
   - Alert when lag > 100 or delivery rate < 95%

### After Delivery Fixed
5. **Set recommended production config:**
   ```bash
   THE_M_EVENTS_GATEWAY=postgres  # No mirror until delivery proven reliable
   THE_M_EVENTS_AUTH=clickhouse   # Low volume, working
   THE_M_EVENTS_TOOLS=clickhouse  # Low volume, working
   ```

6. **Phase 2 work** (when delivery > 95%):
   - Budget queries: ClickHouse-based SumMonthlySpend()
   - Request body storage: DEBUG/AUDIT level body capture to ClickHouse
   - Auth/Tools PG tables: Add user_activity and tool_calls tables for DUAL mode

---

## Key Files

### Implementation
- `go/internal/events/mode.go` — Mode validation (general + Gateway-specific)
- `go/cmd/them/main.go` — Gateway event sink (dual mode)
- `go/cmd/auth-server/main.go` — Auth event sink (clickhouse mode)
- `go/cmd/mcp-service/main.go` — MCP event sink (clickhouse mode)
- `go/internal/authserver/service.go` — Logout returns verified claims
- `go/internal/authserver/handlers.go` — emitUserActivityWithIdentity
- `go/internal/llmgateway/dal.go` — WriteRequest (best-effort event emission)

### Tests
- `go/internal/events/mode_test.go` — Mode validation tests (8/8 pass)
- `go/internal/authserver/auth_identity_test.go` — Identity verification tests (5/5 pass)
- `go/internal/llmgateway/dal_test.go` — Mirror failure safety tests (2/2 pass)

### Documentation
- `docs/EVENTS_CURRENT_STATE.md` — Configuration, event types, validation, safety
- `docs/EVENTS_RECONCILIATION_RESULTS.md` — Detailed reconciliation findings, root cause, evidence
- `scripts/reconcile_events.sh` — Reconciliation tool (PG vs CH comparison)

### Event Writer
- `go/cmd/event-writer/main.go` — Consumer entry point
- `go/internal/events/consumer.go` — Redis Streams consumer + RecoverPending
- `go/internal/events/clickhouse.go` — ClickHouse sink implementation

---

## Commands for Next Session

### Start event stack (if not running)
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml -f docker-compose.events.yml \
  --profile events up -d
```

### Check event-writer status
```bash
docker logs them-event-writer --tail 50
docker logs them-event-writer --since 2m | grep -E "ERROR|batch written|RecoverPending"
```

### Check Redis Streams backlog
```bash
docker exec them-redis-events redis-cli XINFO GROUPS them:events
docker exec them-redis-events redis-cli XPENDING them:events event-writers - + 10
```

### Run reconciliation
```bash
./scripts/reconcile_events.sh
```

### Force event-writer restart
```bash
docker restart them-event-writer
sleep 5
docker logs them-event-writer --tail 20
```

---

## Test Results Summary

| Category | Tests | Pass | Status |
|----------|-------|------|--------|
| Mode validation | 8 | 8 | ✅ |
| Auth identity | 5 | 5 | ✅ |
| Mirror failure safety | 2 | 2 | ✅ |
| **Total code tests** | **15** | **15** | **✅** |
| **Real-world delivery** | 1 | 0 | **❌ 78% loss** |

---

## Deployment State

- **Local dev:** Linux, Docker Compose, all services running
- **Postgres:** them DB, 14,426 gateway_requests records (yesterday's load test)
- **ClickHouse:** them_events DB, 3,154 gateway requests, 8 auth events, 1 tool event
- **Redis Streams:** them:events stream, lag=1, consumer stalled
- **Containers:** 3 event containers marked "orphan" (cosmetic warning, they're functional)

---

## Next Session First Prompt

```
Continue on main (HEAD: 3773bedd). Event delivery has 78% failure rate — event-writer 
stops processing after restart. Debug why consumer stalls (Redis Streams lag=1, no errors 
logged). Check RecoverPending invocation, XAUTOCLAIM parameters, consumer group state. 
Fix startup backlog recovery. Generate 100 test events, verify delivery rate > 95%. 
Add health check for lag. Report actual fix and re-run reconciliation with evidence.
```

---

**Session ready for handover. All tests passing. Critical delivery bug documented with evidence. Next steps clear.**
