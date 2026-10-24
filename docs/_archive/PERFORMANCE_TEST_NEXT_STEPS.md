# Performance Test - Next Steps
# Date: 2026-10-21
# Status: PRELIMINARY RESULTS PUSHED, VERIFICATION NEEDED

---

## Current Status

**Commit**: 80ab9842 (pushed to main)
**Test**: Scenario 40, 3 runs per mode (postgres vs dual)
**Results**: +13% median latency in dual mode, but requires verification

---

## Critical Issues to Resolve

### 1. Delivery Verification Methodology (CRITICAL)

**Problem**: Current test uses count-based verification, which cannot confirm delivery of specific requests.

**Evidence**:
- Test reported "214 missing events" after 30s drain
- Manual check 10+ minutes later showed 300/300 events in ClickHouse
- Unclear if events arrived late or if verification method was flawed

**Required Fix**:
```python
# Current (WRONG): Compare counts
pg_count = SELECT count(*) FROM gateway_requests
ch_count = SELECT count() FROM them_events.events_gateway_request
missing = pg_count - ch_count  # NOT RELIABLE

# Correct: Track specific request IDs
# 1. Get list of IDs written during test window
pg_ids = SELECT id FROM gateway_requests WHERE created_at BETWEEN start AND end
# 2. Check each ID exists in ClickHouse
ch_ids = SELECT id FROM them_events.events_gateway_request WHERE request_id IN (...)
# 3. Report missing specific IDs
missing_ids = set(pg_ids) - set(ch_ids)
```

**Blocker**: `gateway_requests` table schema unclear - need to identify the request ID column.

**Action**:
1. Document `gateway_requests` schema (all columns)
2. Identify unique request identifier
3. Rewrite `verify_clickhouse_delivery()` to track specific IDs
4. Rerun test with ID-based verification

---

### 2. RPS Calculation Verification (MEDIUM)

**Problem**: All tests show exactly `rps=300.0` and `duration_sec=1.0`, which seems too uniform.

**Evidence from raw data**:
```json
{
  "rps": 300.0,
  "duration_sec": 1.0,
  "total_requests": 300
}
```

**Timestamp verification**:
- Test-runner history: Started `16:54:12Z`, Ended `16:54:13Z` (1 second)
- PostgreSQL: 300 requests created between `16:55:32.064` and `16:55:32.458` (0.4 seconds)
- Per-user timing: Each user completed in ~900ms

**Analysis**:
- 100 concurrent users, 3 messages each = 300 total requests
- All users run in parallel (WebSocket)
- Wall-clock time: ~1 second (correct)
- **Actual RPS**: 300 requests / 1 second = 300 (calculation is correct)

**Conclusion**: RPS measurement is valid. The uniform 300.0 value is expected for fixed-size scenario.

**No action required** - RPS is correctly measured.

---

### 3. Delivery Delay Investigation (CRITICAL)

**Problem**: Test reported 10+ minute delivery delay, but this may be verification error.

**Evidence**:
- 30s post-test: 86/300 events in ClickHouse (29%)
- 10+ minutes post-test: 300/300 events in ClickHouse (100%)

**Possible causes**:
1. **Verification error**: Count-based check during active test (other events mixed in)
2. **ClickHouse buffering**: Inserts batched, not immediately visible
3. **Event-writer delay**: Processes in batches (100/batch), may have backlog
4. **Redis Streams lag**: Events queued, slow drain

**Required investigation**:
```bash
# Check event-writer batch processing rate
docker logs them-event-writer --since 5m | grep "batch written"

# Check Redis queue stats DURING test
docker exec them-redis-events redis-cli XLEN them:events:gateway
docker exec them-redis-events redis-cli XPENDING them:events:gateway event-writers - + 10

# Check ClickHouse insert timing
docker exec them-clickhouse clickhouse-client --query \
  "SELECT toStartOfMinute(ts) as minute, count() FROM them_events.events_gateway_request \
   WHERE ts > now() - INTERVAL 1 HOUR GROUP BY minute ORDER BY minute DESC"
```

**Action**:
1. Run test with real-time monitoring of Redis lag and event-writer logs
2. Track ClickHouse insert timestamps (not just counts)
3. Measure actual end-to-end delivery latency (Gateway write → ClickHouse visible)

---

### 4. Latency Variation Analysis (MEDIUM)

**Problem**: +13% median latency reported, but per-run variation is high.

**Per-run p50 values**:
- postgres: 120ms, 111ms, 164ms (mean: 132ms, std dev: ±28ms)
- dual: 116ms, 180ms, 152ms (mean: 149ms, std dev: ±32ms)

**Observation**:
- Run 1 dual (116ms) was **faster** than run 1 postgres (120ms)
- Run 2 dual (180ms) was **much slower** than run 2 postgres (111ms)
- High variance suggests system noise dominates

**Statistical concern**:
- Sample size: only 3 runs per mode
- Variance: ±30ms (23% of mean)
- Difference: 17ms (13% of postgres mean)
- **Confidence**: LOW - need more runs to confirm difference is real

**Action**:
1. Increase runs to 5-10 per mode
2. Calculate confidence intervals (95% CI)
3. Run t-test to determine if difference is statistically significant
4. Report per-run variation, not just means

---

### 5. Test Scenario Coverage (LOW)

**Current**: Only scenario 40 tested (100 users, MINIMAL logging)

**Missing**:
- Scenario 11: 200 users
- Scenario 12: 400 users
- Different logging levels: METRICS, DEBUG, AUDIT

**Action** (optional):
1. Run scenarios 11, 12 with corrected methodology
2. Compare latency overhead across load levels (does it scale?)
3. Test different logging levels to isolate event emission cost

**Priority**: Low if decision is to stay in postgres mode, High if deploying dual

---

## Immediate Actions (Priority Order)

### 1. Schema Documentation (5 minutes)
```bash
docker exec them-postgres psql -U them -d them -c "\d gateway_requests"
docker exec them-clickhouse clickhouse-client --query "DESCRIBE them_events.events_gateway_request"
```

Document columns, identify unique request ID field.

### 2. ID-Based Verification Script (30 minutes)
Rewrite `verify_clickhouse_delivery()` to:
- Extract actual request IDs from test window
- Query ClickHouse for those specific IDs
- Report missing IDs (not just count)

### 3. Rerun Test with Monitoring (1 hour)
- Run scenario 40, 5 runs per mode
- Monitor Redis lag in real-time
- Monitor event-writer logs
- Track ClickHouse insert timing
- Use ID-based delivery verification

### 4. Statistical Analysis (30 minutes)
- Calculate 95% confidence intervals for latency
- Run t-test (postgres vs dual p50)
- Report: "dual mode is X% slower with Y% confidence"

---

## Decision Framework

**Do NOT decide on dual mode until**:

1. ✅ Delivery verification proves 100% of request IDs reach ClickHouse
2. ✅ Delivery timing is understood (seconds? minutes? acceptable?)
3. ✅ Latency difference confirmed statistically significant (t-test p < 0.05)
4. ✅ Sample size increased (5-10 runs minimum)

**If latency difference is real (+13%)**:

Decision depends on:
- Latency budget (is 18ms acceptable?)
- Observability value (worth the cost?)
- Load profile (does overhead scale with load?)

**If delivery is delayed (minutes)**:

This is **unacceptable** for production observability. Investigate:
- Event-writer batch timing
- ClickHouse insert buffering
- Optimize or reject dual mode

---

## Files

- **Corrected test script**: `scripts/perf_focused_test.py`
- **Raw results**: `perf_focused_results_raw.json`
- **Summary**: `perf_focused_summary.json`
- **Report**: `docs/PERFORMANCE_TEST_RESULTS_FOCUSED.md`

---

## Current Mode

**Gateway**: `dual` (THE_M_EVENTS_GATEWAY=dual)
**Event-writer**: Healthy
**Consumer group**: Intact

---

**Status**: Pushed preliminary results for review  
**Next**: Complete verification methodology before making deployment decision
