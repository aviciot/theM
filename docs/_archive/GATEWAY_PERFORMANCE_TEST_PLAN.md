# Gateway Performance Test Plan
**Date:** 2026-10-19  
**Goal:** Identify bottleneck causing 183/200 failures (91.5% failure rate)  
**Context:** Post-logging-overhead minimization (governance slice 1c complete)

---

## Current State (Baseline)

**Test Results (23e69428-53bd-43f9-bd90-76237d85460b):**
- **Load:** 200 concurrent users, 3 messages each = 600 requests
- **Success:** 17/200 users (8.5%)
- **Failed:** 183/200 users (91.5%)
- **Error:** HTTP 502 Bad Gateway
- **Latency (successful):** ~10,000ms per request

**Infrastructure:**
- **Workers:** 4 (them-dag-worker, them-dag-worker-2, them-dag-worker-3, them-dag-worker-4)
- **Worker Concurrency:** 25 concurrent activities per worker = 100 total slots
- **Go Bridge:** 1 replica (them-go-bridge)
- **Postgres:** 1 instance (them-postgres)
- **Redis:** 1 instance (them-redis)
- **Mock Provider:** Internal (them-go-bridge embedded)

**Known Observations:**
- Every gateway request launches a Temporal workflow (600 workflows total)
- Gateway definition has 0 nodes (empty appflow)
- Requests complete with "(appflow sync)" — synchronous wait for Temporal
- No 502 errors in Go bridge logs (502 comes from upstream timeout)

---

## Test Series

### Test 1: Load Scaling (Find Saturation Point)
**Hypothesis:** Worker concurrency (100 slots) is the bottleneck

| Test | Users | Total Reqs | Expected Slots Needed | Expected Result |
|------|-------|------------|----------------------|-----------------|
| 1a   | 10    | 30         | 30                   | ✅ All pass     |
| 1b   | 25    | 75         | 75                   | ✅ All pass     |
| 1c   | 50    | 150        | 150                  | ⚠️  Some fail   |
| 1d   | 75    | 225        | 225                  | ❌ Many fail    |
| 1e   | 100   | 300        | 300                  | ❌ Most fail    |

**Run:**
```bash
# Edit test-runner/data/scenarios/test-policy-mock.json: set n_users
# Run each test, record:
# - Pass/fail count
# - Average latency
# - 502 count
```

**Success Criteria:**
- If 1a-1b pass 100%, 1c starts failing → confirms 100-slot bottleneck
- If 1a fails → different bottleneck (DB, Redis, mock provider)

**Next Step if Confirmed:**
- Increase worker concurrency to 100/worker (400 total slots)
- Re-run Test 1d/1e

---

### Test 2: Component Latency Breakdown
**Hypothesis:** Temporal workflow startup overhead dominates latency

**Method:** Add detailed timing logs to identify where time is spent

**Measure (from logs):**
1. `execution: starting appflow workflow` → Temporal workflow start
2. First activity execution (if any nodes exist)
3. `llmgateway: request complete (appflow sync)` → Total E2E

**Calculate:**
- Temporal startup overhead
- Node execution time
- Mock LLM call time
- Workflow complete → HTTP response time

**Run:** Single user, 3 messages
```bash
# Check timestamps:
docker logs them-go-bridge --since 2m | grep "request_id.*fd29.*" | grep -E "started|complete|workflow"
```

**Success Criteria:**
- If >80% time in Temporal startup → optimize workflow launch
- If >50% time in mock LLM → check mock provider implementation
- If >30% time waiting after workflow complete → bridge response path slow

---

### Test 3: Worker Concurrency Increase
**Hypothesis:** Increasing slots from 100 → 400 will handle 200 users

**Change:** Modify `cmd/dag-worker/main.go`:
```go
MaxConcurrentActivityExecutionSize: 100  // was 25
```

**Rebuild & Restart:**
```bash
docker compose build them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4
docker compose restart them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4
```

**Run:** Test 1e (100 users) and Test-Policy-Mock (200 users)

**Success Criteria:**
- 100 users: >95% success
- 200 users: >95% success
- Latency: <5s per request

**If Still Fails:** Different bottleneck (DB connections, Redis, Temporal server)

---

### Test 4: Horizontal Scale (Add Workers)
**Hypothesis:** 4 workers insufficient; need 6-8 workers

**Method:** Add 2 more worker replicas to docker-compose.yml

**Change:**
```yaml
them-dag-worker-5:
  <<: *dag-worker-common
  container_name: them-dag-worker-5
  hostname: dag-worker-5

them-dag-worker-6:
  <<: *dag-worker-common
  container_name: them-dag-worker-6
  hostname: dag-worker-6
```

**Run:** Test-Policy-Mock (200 users)

**Success Criteria:**
- 200 users: >95% success with 6 workers × 100 = 600 slots

---

### Test 5: Database Connection Pool
**Hypothesis:** Postgres connection exhaustion under load

**Check Current:**
```bash
docker exec them-postgres psql -U them -d them -c "SHOW max_connections;"
docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM pg_stat_activity;"
```

**Monitor During Test:**
```bash
# Run 200-user test, watch connections in real-time:
watch -n 1 'docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM pg_stat_activity;"'
```

**Success Criteria:**
- Active connections should stay well below max_connections
- If nearing limit → increase max_connections or add connection pooler (PgBouncer)

---

### Test 6: Redis Performance
**Hypothesis:** Redis cache lookups slow under high concurrency

**Monitor:**
```bash
# During 200-user test:
docker exec them-redis redis-cli --latency
docker exec them-redis redis-cli INFO stats | grep -E "instantaneous_ops|keyspace"
```

**Success Criteria:**
- Latency: <5ms p99
- Ops/sec: Should handle 10,000+ easily
- If slow → Redis maxmemory policy, eviction, or network issue

---

### Test 7: Inline Execution Bypass (Optimization)
**Hypothesis:** Empty appflow definitions should skip Temporal

**Method:** Add fast-path in gateway handler:
```go
// In llmgateway handler:
if definition has 0 nodes {
    // Skip Temporal, call mock provider directly
    return directMockCall(...)
}
```

**Expected Impact:**
- Latency: 10s → <1s (eliminate Temporal overhead)
- Throughput: 10x improvement

**Implementation Effort:** Medium (2-3 hours)

---

## Execution Order

### Phase A: Quick Wins (30 min)
1. **Test 1a-1b** (10-25 users) — confirm baseline works
2. **Test 2** (latency breakdown) — identify where time goes
3. **Test 5** (DB connections check) — rule out DB exhaustion

### Phase B: Worker Tuning (1 hour)
4. **Test 3** (increase concurrency 25→100) — likely biggest win
5. **Test 1c-1e** (re-test scaling) — validate improvement

### Phase C: Horizontal Scale (if needed, 1 hour)
6. **Test 4** (add workers 4→6) — if Test 3 insufficient
7. **Test 6** (Redis perf) — if still slow

### Phase D: Optimization (future)
8. **Test 7** (inline bypass) — eliminate Temporal overhead for simple cases

---

## Success Metrics

**Target:** 200 concurrent users, >95% success, <2s latency

**Current:**
- ❌ 8.5% success
- ❌ ~10s latency
- ❌ 502 errors

**After Phase A+B (expected):**
- ✅ 95%+ success
- ✅ <5s latency
- ✅ No 502 errors

**After Phase D (stretch):**
- ✅ 99%+ success
- ✅ <1s latency
- ✅ 500+ concurrent users capable

---

## Data Collection

For each test, record:
```json
{
  "test_id": "1a",
  "timestamp": "2026-10-19T08:45:00Z",
  "config": {
    "users": 10,
    "workers": 4,
    "worker_concurrency": 25,
    "total_slots": 100
  },
  "results": {
    "total_users": 10,
    "passed": 10,
    "failed": 0,
    "avg_latency_ms": 1200,
    "p95_latency_ms": 1500,
    "p99_latency_ms": 1800,
    "error_502_count": 0,
    "duration_seconds": 15
  },
  "observations": "All requests succeeded. No queueing observed."
}
```

Save to: `test-results/gateway-perf-YYYYMMDD-HHMMSS.json`

---

## Next Steps After Findings

Based on results:
- **If Test 3 solves it:** Document worker tuning, update defaults, close
- **If Test 4 needed:** Add horizontal scale playbook to runbook
- **If Test 7 needed:** Create task for inline bypass optimization
- **If DB/Redis bottleneck:** Investigate connection pooling, caching strategy

---

## Notes

- All tests use same scenario: `Test-Policy-Mock` (payops_ai tenant, mock provider)
- Gateway token: `799846fa95d95a259d33a56e0da4961bff927f48c8ed0c9737ed45e55e38286c`
- Model alias: `fast` → `mock-model-v1`
- Logging level: `minimal` (post-slice 1c optimization)
