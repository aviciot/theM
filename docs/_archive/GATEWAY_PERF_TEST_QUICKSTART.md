# Gateway Performance Test — Quick Start

**Goal:** Systematically identify why 183/200 users failed with 502 Bad Gateway

---

## Quick Start (Phase A: 30 minutes)

### Step 1: Test 1a — Baseline (10 users)
**Expected:** All pass, establish baseline latency

```bash
cd /opt/docker/them

# Scenario is already set to 10 users
# Go to: http://10.55.125.43:3210/scenarios
# Click: "Test-Policy-Mock"
# Click: "Run"
# Copy the run_id when test completes

# Then run:
./scripts/perf-test.sh 1a 10
# Paste run_id when prompted
```

**Success:** 10/10 users pass, latency <5s  
**If fails:** Problem is NOT worker slots — investigate DB/Redis/mock provider

---

### Step 2: Test 2 — Latency Breakdown
**Expected:** Identify where time is spent

```bash
# After Test 1a completes:
./scripts/latency-breakdown.sh
```

**Look for:**
- Pre-Temporal time (should be <500ms)
- Temporal Workflow time (currently ~10s — this is the bottleneck)

---

### Step 3: Test 1b — Scale to 25 users
**Expected:** Still all pass (within 100 worker slots)

```bash
# Update scenario to 25 users:
sed -i 's/"n_users": 10/"n_users": 25/' test-runner/data/scenarios/test-policy-mock.json

# Run test via UI: http://10.55.125.43:3210/scenarios
# Then:
./scripts/perf-test.sh 1b 25
```

**Success:** 25/25 users pass  
**If fails:** Worker saturation happening earlier than expected

---

### Step 4: Test 1c — Find Saturation Point (50 users)
**Expected:** Start to see failures (50 users × 3 msgs = 150 slots > 100 available)

```bash
sed -i 's/"n_users": 25/"n_users": 50/' test-runner/data/scenarios/test-policy-mock.json

# Run test via UI
./scripts/perf-test.sh 1c 50
```

**Expected:** ~33% failure rate (50 over capacity)  
**If 100% pass:** Bottleneck is NOT worker slots  
**If 100% fail:** Something else broke

---

## Phase B: Worker Tuning (1 hour)

### Step 5: Check Current Worker Config

```bash
docker logs them-dag-worker --tail 50 | grep "max_concurrent_activities"
# Should show: "max_concurrent_activities":25
```

### Step 6: Increase Worker Concurrency

**Edit:** `go/cmd/dag-worker/main.go`

Find:
```go
MaxConcurrentActivityExecutionSize: 25,
```

Change to:
```go
MaxConcurrentActivityExecutionSize: 100,
```

**Rebuild:**
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  build them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4
```

**Restart:**
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4
```

**Verify:**
```bash
docker logs them-dag-worker --tail 10 | grep "max_concurrent"
# Should now show: "max_concurrent_activities":100
```

---

### Step 7: Re-test with Higher Concurrency

**Test 1d (75 users):**
```bash
sed -i 's/"n_users": 50/"n_users": 75/' test-runner/data/scenarios/test-policy-mock.json
# Run via UI
./scripts/perf-test.sh 1d 75
```

**Expected:** 75/75 pass (75 × 3 = 225 slots, have 400)

**Test 1e (100 users):**
```bash
sed -i 's/"n_users": 75/"n_users": 100/' test-runner/data/scenarios/test-policy-mock.json
# Run via UI
./scripts/perf-test.sh 1e 100
```

**Expected:** 100/100 pass (300 slots, have 400)

**Test Original (200 users):**
```bash
sed -i 's/"n_users": 100/"n_users": 200/' test-runner/data/scenarios/test-policy-mock.json
# Run via UI
./scripts/perf-test.sh 1f 200
```

**Expected:** 200/200 pass (600 slots, have 400 — may need Phase C)

---

## Phase C: Horizontal Scale (if Phase B insufficient)

### Step 8: Add 2 More Workers

**Edit:** `docker-compose.dev.yml` — add at end:

```yaml
  them-dag-worker-5:
    image: them_gateway-them-dag-worker:latest
    container_name: them-dag-worker-5
    hostname: dag-worker-5
    environment:
      - POSTGRES_HOST=them-postgres
      - POSTGRES_PORT=5432
      - POSTGRES_USER=them
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=them
      - REDIS_HOST=them-redis
      - REDIS_PORT=6379
      - TEMPORAL_HOST_PORT=them-temporal:7233
      - TEMPORAL_NAMESPACE=default
    networks:
      - them
    depends_on:
      - them-postgres
      - them-redis
      - them-temporal
    profiles:
      - temporal

  them-dag-worker-6:
    image: them_gateway-them-dag-worker:latest
    container_name: them-dag-worker-6
    hostname: dag-worker-6
    environment:
      - POSTGRES_HOST=them-postgres
      - POSTGRES_PORT=5432
      - POSTGRES_USER=them
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=them
      - REDIS_HOST=them-redis
      - REDIS_PORT=6379
      - TEMPORAL_HOST_PORT=them-temporal:7233
      - TEMPORAL_NAMESPACE=default
    networks:
      - them
    depends_on:
      - them-postgres
      - them-redis
      - them-temporal
    profiles:
      - temporal
```

**Start:**
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  --profile temporal up -d them-dag-worker-5 them-dag-worker-6
```

**Verify:**
```bash
docker ps | grep dag-worker
# Should show 6 workers (them-dag-worker through them-dag-worker-6)
```

**New Capacity:** 6 workers × 100 = **600 concurrent slots**

**Re-test 200 users:**
```bash
# Run via UI
./scripts/perf-test.sh 1g 200
```

**Expected:** 200/200 pass

---

## Decision Tree

```
Start: 183/200 failed (91.5% failure)
  ↓
Test 1a (10 users)
  ├─ All pass → Worker slots OK at low load
  └─ Some fail → DB/Redis/Network issue (investigate Phase C alternative)
  ↓
Test 1b (25 users)
  ├─ All pass → Saturation point is 25-50 users
  └─ Some fail → Earlier saturation (check worker health)
  ↓
Test 1c (50 users)
  ├─ Some fail → CONFIRMED: Worker slot bottleneck (proceed to Phase B)
  └─ All pass → Bottleneck is elsewhere (check DB connections)
  ↓
Increase worker concurrency (25→100)
  ↓
Test 1d-1e (75-100 users)
  ├─ All pass → Phase B solved it! ✅
  └─ Some fail → Need Phase C (add workers)
  ↓
Add workers (4→6)
  ↓
Test 1g (200 users)
  ├─ All pass → Success! 🎉
  └─ Some fail → Different bottleneck (DB/Redis/Temporal server)
```

---

## Expected Outcome

**After Phase A:**
- Know exact saturation point (likely 25-50 users)
- Confirmed worker slots are bottleneck
- Baseline latency metrics

**After Phase B:**
- 100-150 users supported (4× improvement)
- May still need Phase C for 200 users

**After Phase C:**
- 200+ users supported
- <5s latency per request
- >95% success rate

---

## Data Files

Results saved to:
```
test-results/
  test-1a-20261019-HHMMSS.json
  test-1b-20261019-HHMMSS.json
  test-1c-20261019-HHMMSS.json
  ...
```

---

## Full Test Plan

See: `docs/GATEWAY_PERFORMANCE_TEST_PLAN.md`
