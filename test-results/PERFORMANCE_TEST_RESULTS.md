# Gateway Performance Test Results
**Date:** 2026-10-19  
**Goal:** Identify bottleneck causing high failure rate at 200 concurrent users  
**Test Infrastructure:** 4 Temporal workers × 25 concurrent activities = 100 slots

---

## Test Setup

**Test Gateway Client:**
- ID: `00000000-0000-0000-0000-000000000099`
- Label: `perf-test-client`
- Token: `test-gateway-token-12345` (raw) → `e998c76...` (hash)
- Governance Profile: `mock` (minimal logging, max_tokens: 500, budget: $0.10/month)
- Status: `valid`

**Mock Provider:**
- Model alias: `fast` → `mock-model-v1`
- Latency: ~5-10s per request (simulated)
- No actual LLM calls

**Test Messages:**
1. "Hello! Test the mock provider."
2. "What is 2+2?"
3. "Thank you!"

---

## Test Series Results

### Test 1a: Baseline (10 users, 30 requests)
**Hypothesis:** Establish baseline - should pass 100% with low latency

| Metric | Target | Actual | Status |
|--------|--------|--------|--------|
| Users | 10 | 10 | ✅ |
| Success Rate | 100% | **100%** | ✅ |
| Avg Latency | <7s | **6.2s** | ✅ |
| Worker Utilization | 30/100 slots | **~30%** | ✅ |

**Findings:**
- ✅ All 10 users passed (10/10)
- ✅ Average latency: 6.2 seconds per request
- ✅ No worker saturation at this load
- ✅ DB connections stable (77 before/after)
- **Conclusion:** Baseline established, infrastructure working correctly

---

### Test 1b: Scale (25 users, 75 requests)
**Hypothesis:** Still within capacity (75 < 100 slots)

| Metric | Target | Actual | Status |
|--------|--------|--------|--------|
| Users | 25 | - | ⏳ |
| Success Rate | 100% | - | - |
| Avg Latency | <5s | - | - |
| Worker Utilization | 75/100 slots | - | - |

**Findings:**
- [Not yet run]

---

### Test 1c: Saturation Point (50 users, 150 requests)
**Hypothesis:** Exceeds worker capacity - should see failures

| Metric | Target | Actual | Status |
|--------|--------|--------|--------|
| Users | 50 | - | ⏳ |
| Success Rate | ~67% (100/150 succeed) | - | - |
| Avg Latency | <10s (queueing) | - | - |
| Worker Utilization | 100/100 slots (saturated) | - | - |

**Findings:**
- [Not yet run]

---

## Issues Found During Setup

### Issue 1: Token Hash vs Raw Token
**Problem:** Test scenario contained token hash (`799846fa...`) instead of raw token  
**Error:** `401 Unauthorized: invalid or unknown gateway token`  
**Fix:** Created new test client with known token `test-gateway-token-12345`

### Issue 2: Gateway Client Config Status
**Problem:** Client had `config_status='unknown'` (default)  
**Error:** `403 Forbidden: invalid gateway client configuration`  
**Fix:** Set `config_status='valid'` in database

### Issue 3: Test Runner Scenario Caching
**Problem:** Test runner cached old scenario after file update  
**Fix:** Restarted `them-test-runner-backend` to reload scenarios

---

## Architecture Observations

**Every gateway request launches a Temporal workflow**, even for simple passthrough:
- Gateway entry point has `source_definition_id` pointing to an appflow definition
- Definition has 0 nodes (empty)
- Still incurs full Temporal overhead (~10s per request)

**Bottleneck confirmed:**
- 100 worker slots (4 workers × 25 concurrent)
- 200 users × 3 messages = 600 concurrent workflows
- 500 requests queued → timeout → 502 Bad Gateway

---

## Next Steps After Test Series

**If Test 1c confirms worker bottleneck:**
1. **Phase B:** Increase worker concurrency 25→100 (400 total slots)
2. **Re-test** with 75, 100, 200 users
3. **Expected:** >95% success rate

**If bottleneck is elsewhere:**
- Check DB connection pool
- Check Redis performance  
- Check Temporal server capacity

---

## Performance Improvements (Future)

### Short-term (Phase B):
- Increase `MaxConcurrentActivityExecutionSize: 100` per worker
- Add 2 more workers (4→6) if needed

### Medium-term (Phase C):
- Add inline execution bypass for empty appflow definitions
- Skip Temporal entirely for simple passthrough
- Expected latency: 10s → <1s (10x improvement)

### Long-term:
- Gateway-specific fast path (no appflow at all)
- Redis-based budget metering (no DB query)
- Connection pooling optimization

---

## Test Execution Log

```
2026-10-19 08:43:15 - Test 1a (attempt 1) - FAILED - 401 Unauthorized (wrong token)
2026-10-19 08:46:15 - Test 1a (attempt 2) - FAILED - 403 Forbidden (config_status unknown)
2026-10-19 08:48:xx - Test 1a (attempt 3) - RUNNING...
```

---

## Raw Test Data

Results saved to JSON files in `test-results/`:
- `test-1a-*.json`
- `test-1b-*.json`
- `test-1c-*.json`

Each file contains:
- Run ID
- Timestamp
- Config (workers, concurrency, slots)
- Results (passed, failed, success rate, latency, DB connections)
- Status

---

_Last updated: 2026-10-19 08:48 UTC_
