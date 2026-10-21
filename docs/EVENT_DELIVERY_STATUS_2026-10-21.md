# Event Delivery — Final Status Report
# Date: 2026-10-21 07:15 UTC
# Session: Background job investigation

---

## Executive Summary

**Status: NOT PRODUCTION READY**

Event delivery system has multiple critical gaps:
1. ✅ Redis permissions fixed (RDB saves work)
2. ✅ AOF persistence verified (survives restart)
3. ❌ Event-writer crashes on valid RFC3339 events ("nil gateway request event")
4. ❌ 0/100 comprehensive test delivery (100 messages stuck in pending)
5. ✅ Historical "missing" 11,272 events explained (created before DUAL mode existed)

---

## What Was Fixed

### 1. Redis Permissions (Commit pending)

**Problem:** Redis ran as root, data files owned by uid=999
**Fix:** Added `user: "999:1006"` to docker-compose.events.yml + fixed ownership
**Verification:**
```bash
$ docker exec them-redis-events redis-cli BGSAVE
Background saving started

$ docker logs them-redis-events --tail 3
20:C 21 Oct 2026 07:12:20.255 * DB saved on disk
20:C 21 Oct 2026 07:12:20.256 * Fork CoW for RDB: current 0 MB, peak 0 MB, average 0 MB
1:M 21 Oct 2026 07:12:20.303 * Background saving terminated with success
```

**Result:** ✅ RDB saves now work, no more "Permission denied"

### 2. Test Script Timestamp Format (Commit pending)

**Problem:** Test used `"2026-10-21 06:42:15"`, event-writer expects RFC3339
**Fix:** Changed to `$(date -u +"%Y-%m-%dT%H:%M:%SZ")`
**Result:** ✅ Timestamp parsing works, but uncovered deeper schema issue

### 3. Historical Events Gap Explained

**Problem:** 11,272 events in PG but not CH (Oct 15-20)
**Root cause:** Events system didn't exist until Oct 20 10:08 UTC

**Evidence:**
```
3947b27e 2026-10-20 10:39:59 fix(events): dual mode working
20bf4ef1 2026-10-20 10:08:15 feat(events): Stage B1 - wire gateway requests

PG events by date:
2026-10-20: 7,678  (3,151 after 10:08 delivered 100% ✓)
2026-10-19: 600    (before events system existed)
2026-10-18: 5,443  (before events system existed)
```

**Conclusion:** NOT a delivery gap — these are historical records from before DUAL mode.
No backfill needed (events system wasn't running).

---

## What's Still Broken

### 1. Event-Writer Schema Validation Failure

**Error:** `clickhouse sink: nil gateway request event`
**Symptoms:**
- 100 test events generated with valid RFC3339 timestamps
- All 100 stuck in Redis pending (lag=0 but pending=100)
- Event-writer retries 3x per message then gives up
- No messages move to DLQ (validation fails before poison check)
- Health endpoint crashes (Connection refused)

**Root cause (hypothesis):** Test events missing required fields for ClickHouse schema
The test generates a generic `Event` JSON, but ClickHouse sink expects typed fields.

**Required investigation:**
1. Compare test event JSON to real gateway event structure
2. Check ClickHouse schema vs Event struct fields
3. Fix test to match production event format

### 2. No Regression Tests for Retention Safety

**Gap:** Retention manager has safety logic but zero tests
**Code claims:** "Never trim pending or unread messages"
**Actual tests:** NONE

**Risk:** Retention runs every 15min, could trim undelivered events if logic is wrong

**Required:**
- Test: generate events, stop writer, wait 24h+15min, verify NOT trimmed
- Test: generate events, deliver some, verify only delivered portion trimmed
- Test: empty consumer group handles trimming safely

### 3. No End-to-End Delivery Proof

**Required (per feedback):**
- Fix test event schema to match production
- Prove 100/100 events reach ClickHouse with matching IDs and fields
- Test during writer restart (in-progress events)
- Test during Redis restart (AOF recovery)
- Test during ClickHouse outage (retry + backpressure)
- Require 100% eventual delivery, not >95%

---

## Test Results Summary

| Test | Status | Evidence |
|---|---|---|
| Redis permissions | ✅ PASS | RDB saves succeed |
| AOF persistence | ✅ PASS | Stream survives restart (100 → 100) |
| RFC3339 timestamps | ✅ PASS | No more parse errors |
| Event delivery | ❌ FAIL | 0/100 delivered, 100 pending |
| Writer restart | ❌ FAIL | Events stuck after restart |
| Schema validation | ❌ FAIL | "nil gateway request event" |
| Health endpoint | ❌ FAIL | Connection refused (writer crashed) |

**Overall:** 3/7 pass — infrastructure works, event handling broken

---

## Next Steps (Required)

### Immediate (Blocking)

1. **Debug schema mismatch:**
   - Read real gateway event from Redis
   - Compare to test event structure
   - Fix test to match production format

2. **Prove 100% delivery:**
   - Generate 100 events via REAL gateway (not test script)
   - Wait for delivery
   - Verify all 100 in ClickHouse with matching request_id
   - Check field integrity (10 random samples)

3. **Test restart resilience:**
   - Send 50 events
   - Restart writer mid-processing
   - Send 50 more events
   - Verify all 100 delivered (no loss, no duplicates)

4. **Test Redis restart:**
   - Send 100 events
   - Restart Redis (AOF recovery)
   - Restart writer
   - Verify all 100 delivered

### After Delivery Proven

5. **Add retention safety tests:**
   - `go/internal/events/retention_test.go`
   - Test pending message protection
   - Test unread message protection
   - Test empty group handling

6. **Add ClickHouse outage test:**
   - Stop ClickHouse
   - Send 100 events
   - Verify writer pauses (doesn't drop)
   - Start ClickHouse
   - Verify all 100 delivered

7. **Document SLA:**
   - Delivery guarantee: at-least-once
   - Latency: <10s for 95th percentile
   - Outage handling: pause + retry (no data loss)

---

## Files Changed (Uncommitted)

```
M  docker-compose.events.yml          # user: "999:1006" + removed stop-writes-on-bgsave-error no
M  scripts/test_event_delivery_comprehensive.sh  # RFC3339 timestamp
```

---

## Recommendation

**DO NOT merge or deploy event-writer changes yet.**

The permissions fix is good, but event handling is broken. Need to:
1. Fix test event schema mismatch
2. Prove 100% delivery with real events
3. Add regression tests for retention safety

**Estimated time:** 2-4 hours to debug schema, fix test, prove delivery.

---

## Evidence Links

- Test output: `/tmp/claude-1006/.../tasks/bcn0jk27s.output`
- Writer logs: `docker logs them-event-writer --since 5m`
- Redis state: `docker exec them-redis-events redis-cli XINFO GROUPS them:events`
- Gap analysis: `docs/EVENT_DELIVERY_GAPS_AND_PLAN.md`

---

**Status:** Permissions fixed, delivery broken, investigation paused.
