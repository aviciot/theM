# Event Delivery — Remaining Gaps and Fix Plan
# Date: 2026-10-21
# Status: INCOMPLETE — Fix requires verification and backfill

---

## Summary of Previous Fix (Commits 19998d8c, 5a89eda6, 2e0e5a54)

**What was claimed:** Fixed event-writer stall by disabling Redis `stop-writes-on-bgsave-error`
**What was verified:** Health endpoint works, lag=0 after restart
**What was NOT verified:**
1. **100% delivery under stress** (restarts, CH outage)
2. **Root cause of permissions** (Redis running as root, AOF files owned by uid=999)
3. **Why 11,272 events were lost** (not from retention, not from poison messages)
4. **Backfill plan for missing events** (11,272 PG records not in CH)

---

## Critical Gaps Found (2026-10-21 Review)

### Gap 1: Permissions Issue NOT Fixed

**Problem:** Redis runs as root (uid=0) but data files owned by uid=999 (redis user)
**Evidence:**
```bash
$ docker exec them-redis-events id
uid=0(root) gid=0(root) ...

$ ls -ln /opt/docker/them/data/them-redis-events/
drwxrwsr-x+ 2 999 1006 4096 Oct 18 12:33 appendonlydir
-rw-rw-r--+ 1 999 1006  300 Oct 21 06:36 dump.rdb
```

**Impact:**
- RDB saves will continue to fail with "Permission denied"
- `stop-writes-on-bgsave-error no` masks the problem but doesn't fix it
- AOF works for now, but inconsistent ownership is a ticking time bomb

**Fix Required:**
1. Either run Redis as `user: "999:1006"` in docker-compose.events.yml, OR
2. Fix bind mount ownership to match root (not recommended), OR
3. Disable RDB entirely and rely only on AOF (document this choice)

### Gap 2: Missing Events Distribution Misunderstood

**Original claim:** "11,272 events lost from 2026-10-20 18:37-18:56"
**Actual truth:** 11,272 events spread across Oct 15-20, NOT just the recent window

**Evidence:**
```
PG total: 14,426
CH total: 3,154
Missing: 11,272

PG by hour:
2026-10-20 18:00 → 3,151  (CH has 3,151 ✓ delivered 100%)
2026-10-20 07:00 → 2,418  (CH has 0 ✗ missing)
2026-10-20 06:00 → 2,100  (CH has 0 ✗ missing)
2026-10-19 08:00 → 600    (CH has 0 ✗ missing)
2026-10-18 17:00 → 1,200  (CH has 0 ✗ missing)
2026-10-18 16:00 → 1,829  (CH has 0 ✗ missing)
... (older events)
```

**Impact:**
- The recent 18:37-18:56 window delivered 100% successfully
- ALL missing events are from Oct 15-20 06:00-10:00 windows
- This suggests DUAL mode was NOT enabled during those hours, OR events were never emitted to Redis

**Root cause hypothesis:**
1. Gateway was running in `THE_M_EVENTS_GATEWAY=postgres` mode (not dual), OR
2. Event emission code had a bug that silently failed, OR
3. Redis was down/unreachable during those hours

**Fix Required:**
1. Check git history for when DUAL mode was enabled
2. Check gateway logs for Oct 15-20 to see event emission errors
3. Determine if backfill is possible (PG → CH direct load)

### Gap 3: No Regression Test for Retention Safety

**Problem:** Retention manager trims "safely" but safety is NOT tested
**Code claim:** "Never trim pending or unread messages" (retention.go:20-21)
**Actual test:** NONE — no test verifies retention respects pending/unread

**Risk:**
- Retention runs every 15 minutes with 24h maxAge
- If `lastDeliveredID` parsing is wrong, could trim undelivered events
- If `XPENDING` check has a race condition, could trim pending events

**Fix Required:**
1. Write integration test: generate events, stop writer, wait 24h+15min, verify stream NOT trimmed
2. Write integration test: generate events, deliver some, verify only delivered portion is trimmed
3. Add safety margin (e.g., only trim events older than 48h, not 24h)

### Gap 4: Backfill Not Implemented

**Problem:** 11,272 events exist in PG but not in CH
**User expectation:** ClickHouse mirror should be complete after "fix"
**Actual state:** Historical data has 78% gap, no backfill plan

**Fix Required:**
1. Export missing events from PG (`scripts/backfill_gateway_events.sql` created but not tested)
2. Load into CH with idempotent upsert (CH doesn't have native upsert, need `ReplacingMergeTree`)
3. Verify no duplicates (event_id primary key collision)
4. Document that backfill is one-time, not continuous

---

## Comprehensive Test Gaps

### What Was NOT Tested

1. **100% delivery rate** — only checked lag=0, not actual record counts
2. **Writer restart mid-batch** — does batch boundary respect atomicity?
3. **Redis restart under load** — does AOF recovery preserve pending messages?
4. **ClickHouse outage** — does writer pause/retry correctly without data loss?
5. **Poison message handling** — does DLQ work? Does it prevent delivery stall?
6. **Retention during backlog** — does trimSafe protect pending events?

### Test Created But Not Verified

`scripts/test_event_delivery_comprehensive.sh` — tests:
- 100 events generated (PG + Redis)
- Writer restart resilience
- Redis restart + AOF survival
- 100% eventual delivery (CH count == PG count)
- Field integrity sampling

**Status:** Running in background (task blrq0kvbp)

---

## Recommended Fix Sequence

### Phase 1: Verify Current State (Immediate)

1. ✅ Run comprehensive test and capture full output
2. Check test results:
   - Did 100% of events reach CH?
   - Did writer survive restart without losing events?
   - Did Redis survive restart with AOF intact?
3. Document actual delivery rate and failure modes

### Phase 2: Fix Permissions (High Priority)

1. Choose Redis user strategy:
   - **Option A:** Run as `user: "999:1006"` (redis:gid-1006)
   - **Option B:** Disable RDB, rely only on AOF, document choice
2. Test chosen fix:
   - Restart Redis
   - Verify AOF writes succeed
   - Verify RDB disabled OR saves succeed
3. Add health check for Redis persistence in docker-compose.events.yml

### Phase 3: Identify Missing Events Root Cause (High Priority)

1. Check gateway env history:
   ```bash
   git log --all -p -- docker-compose.yml docker-compose.dev.yml | grep THE_M_EVENTS_GATEWAY
   ```
2. Check gateway logs for Oct 15-20:
   ```bash
   # If logs exist
   docker logs them-go-bridge --since 2026-10-15T00:00:00 | grep -i "event\|emit"
   ```
3. Determine if backfill is needed:
   - If DUAL was never enabled → backfill is expected historical gap
   - If DUAL was enabled but events not emitted → code bug, backfill recovers data

### Phase 4: Implement Backfill (Medium Priority)

1. Test backfill script on 10 records first
2. Run full backfill (11,272 records)
3. Verify:
   - CH count matches PG count
   - No duplicates (event_id collisions)
   - Field integrity (sample 100 random records)
4. Document backfill in EVENTS_RECONCILIATION_RESULTS.md

### Phase 5: Add Retention Safety Tests (Medium Priority)

1. Integration test: retention respects pending messages
2. Integration test: retention respects unread messages
3. Integration test: retention handles empty consumer group
4. Add to `go/internal/events/retention_test.go`

### Phase 6: Production Hardening (Low Priority)

1. Add docker healthcheck for them-event-writer (lag < 1000)
2. Add alerting for Redis persistence failures
3. Add alerting for CH delivery rate < 95%
4. Document runbook for event delivery issues

---

## Acceptance Criteria for "Fixed"

### Must Have (Blocking)

- [ ] Comprehensive test passes with 100% delivery (including restarts)
- [ ] Redis permissions fixed (RDB saves succeed OR explicitly disabled)
- [ ] Root cause of 11,272 missing events identified and documented
- [ ] Backfill plan approved (implement OR document why not needed)

### Should Have (Non-Blocking)

- [ ] Retention safety regression tests added
- [ ] Health checks added to docker-compose.events.yml
- [ ] Runbook documented for future delivery issues

### Nice to Have

- [ ] Alerting configured for delivery lag/rate
- [ ] Performance test: 10,000 events under load
- [ ] CH outage resilience test (pause CH, verify writer retries)

---

## Current Status

- ✅ Health endpoint added (lag/pending visibility)
- ✅ Write-lock disabled (Redis accepts writes despite RDB failure)
- ⚠️ Permissions NOT fixed (RDB still fails, AOF works by luck)
- ⚠️ Missing events NOT backfilled (11,272 gap remains)
- ⚠️ 100% delivery NOT verified (comprehensive test running)
- ❌ Retention safety NOT tested (no regression tests)

**Verdict:** NOT SAFE for production DUAL mode yet. Works for new events, but historical gaps unaddressed and permissions fragile.

---

## Files to Update After Fix

1. `docker-compose.events.yml` — user directive OR RDB disable
2. `docs/EVENTS_CURRENT_STATE.md` — backfill results, delivery SLA
3. `docs/EVENT_DELIVERY_FIX_2026-10-21.md` — mark as "partial, gaps remain"
4. `docs/CURRENT.md` — update status from "fixed" to "in progress"
5. `go/internal/events/retention_test.go` — add safety tests (new file)

---

**Next action:** Wait for comprehensive test results, then decide on permissions fix strategy and backfill approach.
