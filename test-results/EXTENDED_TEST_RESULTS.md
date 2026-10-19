# Gateway Performance - Extended Test Results
**Date:** 2026-10-19  
**Session:** Extended testing beyond initial 200-user validation

---

## Test Series 1: Scaling Tests (10 → 280 users)

| Test | Users | Requests | Success | Latency | Status | Notes |
|------|-------|----------|---------|---------|--------|-------|
| 1a   | 10    | 30       | 100%    | 6.2s    | ✅ | Baseline + warmup overhead |
| 1b   | 25    | 75       | 100%    | 142ms   | ✅ | Warmed up, fast |
| 1c   | 50    | 150      | 100%    | 320ms   | ✅ | Still no saturation |
| 1d   | 100   | 300      | 100%    | 541ms   | ✅ | Linear scaling |
| 1e   | 200   | 600      | 100%    | 1290ms  | ✅ | Original failure case - now passes |
| 1f   | 280   | 840      | 100%    | 1944ms  | ✅ | Still scaling linearly |

**Latency Pattern:** ~5-7ms per concurrent user after warmup

---

## Test Series 2: Stress & Variation Tests

### Test 2a: Stress Test (400 users, 1200 requests)
**Goal:** Find true saturation point  
**Workers:** 4 × 25 = 100 concurrent slots  
**Expected:** Should start seeing failures or high latency

| Metric | Target | Actual | Status |
|--------|--------|--------|--------|
| Users | 400 | TBD | ⏳ RUNNING |
| Success Rate | ≥80% | TBD | - |
| Avg Latency | <5s | TBD | - |

---

### Test 2b: Single Message (200 users, 200 requests)
**Goal:** Isolate per-request latency without conversation overhead  
**Status:** Not yet run

---

### Test 2c: Long Conversation (50 users, 500 requests)
**Goal:** Test history accumulation and sustained load  
**Messages:** 10 per user  
**Status:** Not yet run

---

## Infrastructure Observations

### Latency Breakdown (from earlier analysis)
- **First request:** ~6s (Temporal workflow warmup)
- **Subsequent:** ~300-500ms (steady state)
- **Scaling:** +5-7ms per additional concurrent user

### Resource Utilization
- **DB Connections:** Stable 77-78 throughout all tests
- **Redis Ops/sec:** 18-24 (very low, plenty of headroom)
- **Worker Utilization:** ~60-70% at 280 users

### No Bottlenecks Detected At:
✅ 280 concurrent users (840 total requests)  
✅ Database connection pool  
✅ Redis performance  
✅ Worker capacity

---

## Comparison: Before vs After Auth Fix

| Metric | Before (Bad Auth) | After (Valid Auth) |
|--------|------------------|-------------------|
| 200 users | 91.5% failed | 100% passed |
| Error | 502 Bad Gateway | None |
| Latency | N/A (failed) | 1.29s |
| Root Cause | Invalid token | - |

---

## Test Scenarios Created

All scenarios available in test runner UI at: http://10.55.125.43:3210/scenarios

1. **Test-Policy-Mock** - Baseline (10 users, 3 messages)
2. **Perf Test 1b** - 25 Users
3. **Perf Test 1c** - 50 Users
4. **Perf Test 1d** - 100 Users
5. **Perf Test 1e** - 200 Users (original failure case)
6. **Perf Test 2a** - 400 Users Stress Test
7. **Perf Test 2b** - Single Message (200 users)
8. **Perf Test 2c** - Long Conversation (50 users × 10 messages)

All include `test_results` field with embedded findings!

---

## Key Findings

### 1. Linear Latency Scaling
Gateway latency scales predictably:
```
Latency ≈ 150ms + (5-7ms × concurrent_users)
```

At 280 users: `150 + (6 × 280) = 1830ms` (actual: 1944ms) ✅

### 2. No Hard Limits Yet
- 280 users: 100% success
- Infrastructure has headroom
- True limit likely 500-600+ concurrent users

### 3. Empty Appflow Definition is Fast
- Gateway entry point has 0 nodes
- No Temporal activity execution
- Requests complete in ~300-500ms
- Workers release slots quickly

### 4. Database is Not the Bottleneck
- Connections stable at 77-78
- No query slowdown
- No connection pool exhaustion
- Can likely handle 1000+ concurrent

---

## Predictions for Test 2a (400 Users)

**Optimistic:** 100% pass, ~2.5-3s latency  
**Realistic:** 95%+ pass, 2-4s latency  
**Pessimistic:** 80%+ pass, some 502 errors

If failures occur, likely cause:
1. Temporal worker queue saturation (100 slots)
2. Network timeouts (not resource limits)
3. Mock provider artificial delays

---

## Next Tests Recommended

### If 400 Users Pass:
- Test 500 users
- Test 600 users
- Find true breaking point

### If 400 Users Fail:
- Analyze failure pattern (502 vs timeout vs other)
- Check worker queue depth
- Consider Phase B: Increase worker concurrency 25→100

### Additional Test Types:
- **Burst test:** 0 → 200 users in 10 seconds
- **Sustained load:** 100 users for 5 minutes
- **Mixed load:** Different message lengths

---

## Performance Targets

| Load | Target Latency | Target Success | Actual |
|------|---------------|----------------|--------|
| ≤100 users | <1s | 100% | ✅ 541ms, 100% |
| 100-200 users | <2s | 100% | ✅ 1.29s, 100% |
| 200-300 users | <3s | ≥95% | ✅ 1.94s, 100% |
| 300-500 users | <5s | ≥90% | TBD |
| 500+ users | <10s | ≥80% | TBD |

---

## Infrastructure Capacity Estimate

**Current Configuration:**
- 4 Temporal workers
- 25 concurrent activities per worker
- 100 total concurrent slots

**Estimated Limits:**
- **Comfortable:** 200 users (proven)
- **Safe:** 300 users (high confidence)
- **Saturation:** 400-500 users (testing now)
- **Failure:** 600+ users (likely)

**With Phase B Improvements** (worker concurrency 25→100):
- **Comfortable:** 800 users
- **Safe:** 1200 users
- **Saturation:** 1600-2000 users

---

_Last updated: 2026-10-19 09:02 UTC_  
_Test 2a (400 users) running..._
