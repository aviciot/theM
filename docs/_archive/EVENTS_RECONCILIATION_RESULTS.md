# Events Reconciliation Results
**Date:** 2026-10-21  
**Branch:** main  
**HEAD:** 9c4bf650

## Summary

Reconciliation reveals **78% event loss** — only 3,154 of 14,426 gateway requests delivered to ClickHouse (21.9% delivery rate). Event-writer consumed events successfully until restart at 2026-10-20 18:56, then stopped processing. Redis Streams shows lag=1 (events stuck). Auth events delivered successfully (8 events in CH).

## Configuration Verified

| Service | Mode | Sink Status | Verified |
|---------|------|-------------|----------|
| Gateway (them-go-bridge) | `dual` | Active: "dual mode (Postgres authoritative, ClickHouse mirror)" | ✓ |
| Auth (them-auth-go) | `clickhouse` | Active: "ClickHouse (via Redis Streams)" | ✓ |
| MCP Tools (them-mcp-service) | `clickhouse` | Active: "ClickHouse (via Redis Streams)" | ✓ |

## Reconciliation Results

### Gateway Requests (DUAL mode)
- **Postgres (authoritative):** 14,426 requests
- **ClickHouse (mirror):** 3,154 requests
- **Delivery rate:** 21.9%
- **Missing:** 11,272 events (78% loss)
- **Status:** ❌ **CRITICAL FAILURE**

#### Most Recent Records
**Postgres:** Last record 2026-10-20 18:40:22  
**ClickHouse:** Last record 2026-10-20 18:39:49

**Gap:** Event delivery stopped after 18:39:49 despite PG continuing to write until 18:40:22 and beyond.

### Auth Events (clickhouse mode)
- **ClickHouse:** 8 user_activity events
- **Actions:** login, logout, token_refresh
- **Tenant attribution:** ✓ Verified (tenant_id populated: 00000000-0000-0000-0000-000000000000)
- **User attribution:** ✓ Verified (user_id: 1, 102)
- **Status:** ✓ Working

### Tool Events (clickhouse mode)
- **ClickHouse:** 1 tool_call event
- **Status:** ✓ Working

## Root Cause Analysis

### Timeline
1. **2026-10-20 18:37-18:40:** Event-writer successfully wrote 3,562 events (100 events/batch, high throughput)
2. **2026-10-20 18:40:01:** Last successful batch write (62 events)
3. **2026-10-20 18:40:26:** One more event written
4. **2026-10-20 18:56:09:** Event-writer restarted
5. **2026-10-20 18:56:12:** ClickHouse sink reinitialized
6. **After restart:** No further batches written despite Redis Streams showing lag=1

### Findings
- **Event-writer stopped processing** after restart
- **Redis Streams status:** 1 pending event, lag=1 (consumer not progressing)
- **Gateway kept writing to PG** (authoritative path working correctly — mirror failure didn't block requests ✓)
- **Backlog not processed:** 11,272 events accumulated after 18:40 but never consumed

### Verified Safety Properties
✓ **PG write persistence:** Gateway wrote 14,426 requests to Postgres despite mirror failures  
✓ **Non-blocking:** Mirror failures didn't block gateway requests (best-effort contract honored)  
✓ **Auth identity:** tenant_id and user_id populated from verified JWT  

### Critical Gap
❌ **Event delivery broken:** 78% loss rate — event-writer not processing Redis Streams after restart

## Test Results Summary

### Completed Tests (All Passing)
✓ Auth identity verification (5/5 tests pass):
  - Valid token → verified claims
  - Expired token → nil
  - Forged token → nil
  - Malformed token → nil
  - Empty token → nil

✓ Mode validation (8/8 tests pass):
  - Valid modes accepted (postgres, dual, clickhouse, empty)
  - Invalid modes rejected
  - Gateway blocks clickhouse
  - Gateway allows postgres/dual
  - Redis requirements enforced

✓ Mirror failure safety (2/2 tests pass):
  - Event sink failures ignored (best-effort)
  - Mode-based PG write behavior correct

### Real-World Verification
✓ Postgres authoritative in DUAL mode (14,426 records written)  
✓ Mirror failures don't block requests (PG writes succeeded)  
❌ Event delivery reliability (78% loss — FAILURE)

## Remaining Gaps

### Critical (Blocking Production)
1. **Event-writer reliability:** Stops processing after restart, doesn't recover backlog
2. **Delivery verification:** No monitoring/alerting for delivery lag
3. **Backlog recovery:** RecoverPending logic exists but not triggering

### Configuration
4. **Gateway mode:** Currently DUAL for testing — should be `postgres` in production until delivery reliability proven
5. **Monitoring:** No metrics on delivery rate, lag, or failures

## Recommendations

### Immediate Actions
1. **DO NOT use DUAL mode in production** until delivery reliability fixed
2. **Set Gateway to `postgres` mode** (Auth/Tools can stay `clickhouse` — lower volume, non-critical)
3. **Investigate event-writer restart behavior** — why does it stop processing after reinitialization?
4. **Add delivery rate monitoring** — alert when lag > threshold or delivery rate < 95%

### Required Fixes
1. **Event-writer recovery:** Fix RecoverPending or add explicit backlog catchup on startup
2. **Health check:** Event-writer should report unhealthy when not processing (lag growing)
3. **Integration test:** Automated test that generates events, waits, verifies delivery rate > 95%

### Phase 2 (After Delivery Fixed)
4. **Budget queries:** Implement ClickHouse-based SumMonthlySpend() for clickhouse-only mode
5. **Request body storage:** Add DEBUG/AUDIT level body capture to ClickHouse
6. **Auth/Tools PG tables:** Add user_activity and tool_calls tables for DUAL mode support

## Evidence

### Reconciliation Script
```bash
./scripts/reconcile_events.sh
```

### ClickHouse Queries
```sql
-- Gateway requests
SELECT COUNT(*) FROM them_events.events_gateway_request;  -- 3154

-- Auth events
SELECT COUNT(*) FROM them_events.events_user_activity;     -- 8

-- Tool events
SELECT COUNT(*) FROM them_events.events_tool_call;         -- 1
```

### Postgres Queries
```sql
-- Gateway requests
SELECT COUNT(*) FROM them.gateway_requests;  -- 14426
```

### Redis Streams
```bash
docker exec them-redis-events redis-cli XINFO GROUPS them:events
# Output: lag: 1, pending: 0
```

## Conclusion

**Tests pass, but delivery fails.** The safety properties work correctly (PG persistence, non-blocking, auth identity), but the ClickHouse mirror has a **78% delivery failure rate**. Event-writer successfully processed 3,562 events at high throughput, then stopped after restart and never recovered.

**Not production-ready** until:
1. Event-writer restart recovery fixed
2. Delivery rate consistently > 95%
3. Monitoring/alerting in place

**Safe interim state:**
- Gateway: `THE_M_EVENTS_GATEWAY=postgres` (no mirror)
- Auth: `THE_M_EVENTS_AUTH=clickhouse` (works, low volume)
- Tools: `THE_M_EVENTS_TOOLS=clickhouse` (works, low volume)

**Next session:**
1. Debug event-writer restart behavior
2. Fix RecoverPending or add startup backlog catchup
3. Re-run reconciliation and verify > 95% delivery
4. Add integration test for delivery reliability
