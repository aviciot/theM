# Current State — the-M
# Last Updated: 2026-10-22 00:00 UTC
# Branch: main
# HEAD: 9c8ceb5f

---

## Quick Status

✅ **Performance Testing Complete**  
✅ **Delivery Verification Complete**  
✅ **Dual mode functional (delivery proven)**  
🔄 **Next**: Performance cost evaluation (optional) or Run producer implementation

---

## Latest Work (2026-10-21)

### Performance Testing & Delivery Verification - COMPLETE

**Session Duration**: 12:40 - 17:14 UTC (4.5 hours)  
**Commits**: 80ab9842, 3e60cff3, bf8c5a76, 9c8ceb5f

**What Was Accomplished**:

1. ✅ **Gateway performance benchmark executed** (corrected methodology)
   - Fixed FLUSHDB issue (preserved consumer groups)
   - Fixed RPS calculation (actual completed / elapsed time)
   - Scenario 40: 3 runs per mode (postgres, dual)
   - Results: +13% median latency preliminary (needs more runs for confidence)

2. ✅ **Schema fully documented** (`docs/SCHEMA_GATEWAY_EVENTS.md`)
   - PostgreSQL `gateway_requests` (17 columns)
   - ClickHouse `events_gateway_request` (22 columns)
   - ID mapping: PG `id` (UUID) → CH `request_id` (String)
   - Verification methodology documented

3. ✅ **Delivery question resolved** (`docs/DELIVERY_VERIFICATION_CONCLUSION.md`)
   - **Finding**: Events deliver successfully in dual mode
   - "10+ minute delay" was verification timing error, not failure
   - Event-writer healthy, 300/300 events delivered
   - Actual delivery: 5-30s typical, up to 2 min under load

**Key Findings**:
- **Delivery**: ✅ VERIFIED FUNCTIONAL - all events reach ClickHouse
- **Performance**: ⚠️ PRELIMINARY - +13% median latency (+18ms absolute)
- **RPS**: No throughput degradation (300 req/sec both modes)
- **Resources**: Negligible (CPU +0.03%, RAM -1.5MB)

---

## Current HEAD

```
9c8ceb5f docs: delivery verification conclusion - events deliver successfully
bf8c5a76 docs: schema documentation + ID verification script fixed
3e60cff3 docs: performance test next steps - verification methodology needed
80ab9842 perf: corrected Gateway benchmark - scenario 40, postgres vs dual
90a817aa docs: prepare performance testing handover
```

---

## Deployment State

### Infrastructure Status
- **Gateway mode**: `dual` (THE_M_EVENTS_GATEWAY=dual)
- **Event-writer**: Healthy (processing batches, no errors)
- **ClickHouse**: Running, receiving events (delivery verified)
- **Redis events**: Consumer group healthy (lag=0, pending=0)
- **Branch**: main (clean, pushed)

### Container Health
All core + events containers running:
- ✅ them-traefik (reverse proxy)
- ✅ them-postgres (DB)
- ✅ them-redis (cache)
- ✅ them-clickhouse (events store, profile: events)
- ✅ them-redis-events (event queue, profile: events)
- ✅ them-event-writer (consumer, profile: events)
- ✅ them-auth-go (UI auth)
- ✅ them-go-bridge (API gateway)
- ✅ them-frontend (Next.js dashboard)

---

## Performance Testing Results

### Documentation
- `docs/PERFORMANCE_TEST_RESULTS_FOCUSED.md` - Full test results
- `docs/PERFORMANCE_TEST_NEXT_STEPS.md` - Further testing plan (optional)
- `docs/DELIVERY_VERIFICATION_CONCLUSION.md` - Delivery verification closed
- `docs/SCHEMA_GATEWAY_EVENTS.md` - Schema reference

### Scripts
- `scripts/perf_focused_test.py` - Corrected benchmark runner
- `scripts/verify_delivery_by_id.py` - ID-based verification tool
- `scripts/verify_delivery_timing.sh` - Delivery timing test

### Data Files
- `perf_focused_results_raw.json` - Raw measurements (6 runs)
- `perf_focused_summary.json` - Aggregated results

---

## Dual Mode Status

### Delivery (Production-Ready)
- ✅ Event-writer functional
- ✅ Consumer groups stable
- ✅ All events delivered (verified)
- ✅ Timing acceptable (5-30s typical, 2min max)
- ✅ No errors or failures

### Performance (Needs Decision)
- ⚠️ +13.4% median latency (+18ms: 132ms → 149ms)
- ✅ No throughput impact (RPS unchanged)
- ✅ Negligible resource overhead
- ⚠️ Small sample size (only 3 runs per mode)
- ⚠️ Statistical significance not confirmed

**Trade-off Decision Required**:
- Is +18ms median latency acceptable for ClickHouse observability?
- Current finding is PRELIMINARY (needs 5-10 runs for confidence)

---

## Next Tasks (Priority Order)

### 1. Performance Cost Evaluation (OPTIONAL)

**If dual mode deployment is being considered**:
- Run 5-10 iterations of scenario 40 per mode
- Calculate 95% confidence intervals
- Run t-test for statistical significance
- Decide if +13% latency is acceptable

**If staying in postgres mode**:
- Skip additional testing
- Document decision rationale
- Move to next priority task

### 2. Run Usage/Steps Producer (DEFERRED FROM PREVIOUS)

**Status**: BLOCKED until performance testing complete (NOW UNBLOCKED)  
**Location**: `go/internal/events/` (Run/RunStep event producers)  
**Requirement**: Implement Run and RunStep event emission in dual mode

### 3. Optional Test Improvements

**Low Priority** - only if needed:
- Fix test script for real Gateway endpoint (not mock)
- Test additional scenarios (11, 12) with corrected methodology
- Implement request-ID tracking in production

---

## Known Issues

### 1. Performance Test Sample Size
**Issue**: Only 3 runs per mode (low statistical confidence)  
**Impact**: +13% latency finding is preliminary  
**Status**: Documented in next steps, additional testing optional

### 2. Test Scenario Compatibility
**Issue**: Scenario 40 uses mock provider, doesn't write to `gateway_requests`  
**Impact**: ID-based verification couldn't be tested end-to-end  
**Status**: Verification script ready, needs production scenario  
**Priority**: Low (delivery already verified via count + event-writer health)

---

## Files Changed (since 90a817aa)

### New Documentation
- `docs/PERFORMANCE_TEST_RESULTS_FOCUSED.md` - Test results and analysis
- `docs/PERFORMANCE_TEST_NEXT_STEPS.md` - Further testing plan
- `docs/SCHEMA_GATEWAY_EVENTS.md` - Complete schema reference
- `docs/DELIVERY_VERIFICATION_CONCLUSION.md` - Delivery question closed
- `docs/PERFORMANCE_TEST_IN_PROGRESS.md` - Test execution log

### New Scripts
- `scripts/perf_focused_test.py` - Performance benchmark runner (Python)
- `scripts/verify_delivery_by_id.py` - ID-based verification (Python)
- `scripts/verify_delivery_timing.sh` - Timing verification (Bash)
- `scripts/check_perf_progress.sh` - Progress monitor
- `scripts/perf_comparison.py` - Multi-scenario runner (unused)

### Data Files
- `perf_focused_results_raw.json` - Raw test data
- `perf_focused_summary.json` - Aggregated summary
- `perf_comparison_log.txt` - Old test log
- `perf_test_results_raw.json` - Old test data (invalid)
- `perf_test_summary.json` - Old test summary (invalid)

---

## Context for Next Session

**What was accomplished**:
- Performance testing complete (corrected methodology)
- Delivery verification complete (events deliver successfully)
- Schema documented
- Verification tools created

**What's next**:
1. **Decision**: Evaluate +13% latency cost for dual mode (optional)
2. **OR**: Move to Run usage/steps producer (main priority)
3. **Optional**: Additional performance testing for confidence

**Current mode**: `dual` (delivery verified, performance cost documented)

---

## Startup Commands (for next session)

### Start Infrastructure
```bash
cd /opt/docker/them

# Core + Events (current production setup)
docker compose --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  -f docker-compose.events.yml \
  --profile events up -d

# Verify Gateway mode
docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY
# Expected: THE_M_EVENTS_GATEWAY=dual

# Check event-writer health
docker logs them-event-writer --tail 10
# Expected: "batch written" messages, no errors
```

### Health Check
```bash
# All containers
docker compose --project-name them_gateway \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  -f docker-compose.events.yml \
  --profile events ps

# Event system specifically
docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request WHERE ts > now() - INTERVAL 1 HOUR;"
docker exec them-redis-events redis-cli XINFO GROUPS them:events:gateway
```

---

**Updated**: 2026-10-22 00:00 UTC  
**Session**: Performance testing complete  
**Next**: Performance cost decision or Run producer implementation
