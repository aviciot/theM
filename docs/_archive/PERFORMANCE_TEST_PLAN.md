# Gateway Performance Testing Plan
# postgres vs dual Mode Comparison
# Date: 2026-10-21
# Status: Ready to Execute

---

## Objective

Compare Gateway performance between `postgres` (write to PG only) and `dual` (write to PG + emit to ClickHouse via Redis) modes to measure the actual performance impact of event emission.

**Key Question**: Does dual mode add measurable latency or reduce throughput compared to postgres-only mode?

---

## Current State

### Infrastructure
- **Gateway mode**: `dual` (THE_M_EVENTS_GATEWAY=dual)
- **Event-writer**: Running, processing events
- **ClickHouse**: Running, receiving events
- **Redis events**: Running, healthy
- **HEAD commit**: 2540e295
- **Branch**: main

### Verification Status
- ✅ Events infrastructure production-ready
- ✅ DLQ failure handling tested (mock tests)
- ✅ Retention safety verified
- ✅ Resilience tests passed (4000 events, 100% delivery)

---

## Test Scenarios

### Load Tests (50/200/400 users)
1. **10-perf-50-users-direct.json** - 50 concurrent users
2. **11-perf-200-users-direct.json** - 200 concurrent users
3. **12-perf-400-users-direct.json** - 400 concurrent users

### Logging Level Tests (100 users, different capture levels)
4. **40-logging-perf-minimal.json** - MINIMAL (no body capture)
5. **41-logging-perf-metrics.json** - METRICS (metadata only)
6. **42-logging-perf-debug.json** - DEBUG (full body capture)
7. **43-logging-perf-audit.json** - AUDIT (full body capture)

**Location**: `/opt/docker/them/test-runner/data/scenarios/`

---

## Test Procedure

### Phase 1: Baseline (Current Dual Mode)
1. Verify current mode: `docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY`
2. Clear existing data:
   ```bash
   docker exec them-postgres psql -U them -d them -c "TRUNCATE gateway_requests CASCADE;"
   docker exec them-clickhouse clickhouse-client --query "TRUNCATE TABLE them_events.events_gateway_request;"
   docker exec them-redis-events redis-cli FLUSHDB
   ```
3. Run each scenario 3 times (warmup + 2 measured runs)
4. Collect metrics (see below)

### Phase 2: Switch to Postgres Mode
1. Update environment:
   ```bash
   cd /opt/docker/them
   # Edit .env: THE_M_EVENTS_GATEWAY=postgres (was dual)
   sed -i 's/THE_M_EVENTS_GATEWAY=dual/THE_M_EVENTS_GATEWAY=postgres/' .env
   ```

2. **Recreate bridge** (environment changes require container recreation):
   ```bash
   docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml stop them-go-bridge
   docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge
   ```

3. **Verify mode changed**:
   ```bash
   docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY
   # Expected: THE_M_EVENTS_GATEWAY=postgres
   ```

4. Wait 10 seconds for bridge readiness
5. Clear data (same as Phase 1)
6. Run same scenarios 3 times each

### Phase 3: Alternating Runs (Reduce Bias)
For statistical validity, alternate mode order:
- Run 1: postgres → dual
- Run 2: dual → postgres  
- Run 3: postgres → dual

Change mode between each test using steps from Phase 2.

---

## Metrics to Collect

### Client-Side Metrics (from Test Runner)
**Primary**: These measure actual user-experienced latency
- **p50 latency** (median response time)
- **p95 latency** (95th percentile)
- **p99 latency** (99th percentile)
- **Requests/sec** (throughput)
- **Request errors** (count/percentage)
- **Test duration** (total time)

**How to collect**: Test runner output or scenario results file

### Server-Side Metrics

#### Gateway Metrics
```bash
# CPU/RAM during test
docker stats them-go-bridge --no-stream

# Recorded latency (WARNING: calculated BEFORE WriteRequest, not full path)
docker exec them-postgres psql -U them -d them -c "
  SELECT 
    percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms) as p50,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) as p95,
    percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms) as p99
  FROM gateway_requests 
  WHERE created_at > NOW() - INTERVAL '5 minutes';"
```

#### Postgres Load
```bash
# Connection count
docker exec them-postgres psql -U them -d them -c "SELECT count(*) FROM pg_stat_activity WHERE datname='them';"

# Query duration
docker exec them-postgres psql -U them -d them -c "
  SELECT query, state, wait_event_type, wait_event
  FROM pg_stat_activity 
  WHERE datname='them' AND state='active';"
```

#### Event System (Dual Mode Only)
```bash
# Redis backlog
docker exec them-redis-events redis-cli XLEN them:events:gateway

# Redis pending
docker exec them-redis-events redis-cli XPENDING them:events:gateway event-writers - + 10

# Event-writer throughput
docker logs them-event-writer --since 5m 2>&1 | grep "batch written" | tail -10

# ClickHouse count
docker exec them-clickhouse clickhouse-client --query "
  SELECT count() FROM them_events.events_gateway_request 
  WHERE ts > now() - INTERVAL 5 MINUTE;"
```

### Dual Mode Verification
**Critical**: After each dual-mode run, verify ALL request IDs reached ClickHouse:

```bash
# Get request count from Postgres
PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM gateway_requests;")

# Wait for event-writer to drain (check lag=0, pending=0)
sleep 30

# Get request count from ClickHouse
CH_COUNT=$(docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request FORMAT TabSeparated;")

# Compare
if [ "$PG_COUNT" = "$CH_COUNT" ]; then
  echo "✅ All requests in ClickHouse"
else
  echo "⚠️  Missing: $((PG_COUNT - CH_COUNT)) events"
fi
```

---

## Expected Results

### Dual Mode Overhead
**Hypothesis**: Dual mode adds minimal overhead because:
1. Event emission is **non-blocking** (`_ = eventSink.Emit()`)
2. Events buffered in-memory, written to Redis asynchronously
3. Gateway writes to PG synchronously in BOTH modes
4. ClickHouse write happens in separate event-writer process

**Potential overhead sources**:
1. Redis XADD latency (~1-2ms)
2. JSON marshaling (event structure)
3. Memory allocation for event buffers

**Expected difference**: <5% latency increase, <10% throughput decrease

### Logging Level Impact (40-43)
**Hypothesis**: DEBUG/AUDIT modes (body capture) add overhead:
1. Body serialization to JSON
2. Larger PG writes (gateway_request_bodies table)
3. Not related to ClickHouse emission

---

## Test Execution Commands

### Run Single Scenario
```bash
cd /opt/docker/them
ADMIN_JWT=$(curl -s -X POST http://localhost:8088/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")

ADMIN_JWT=$ADMIN_JWT python3.12 scripts/tests/run_tests.py 10
```

### Automated Test Loop
```bash
#!/bin/bash
SCENARIOS=(10 11 12 40 41 42 43)
MODES=("postgres" "dual")

for scenario in "${SCENARIOS[@]}"; do
  for mode in "${MODES[@]}"; do
    # Change mode (see Phase 2)
    # Run test
    ADMIN_JWT=$ADMIN_JWT python3.12 scripts/tests/run_tests.py $scenario
    # Collect metrics
    # Sleep between runs
    sleep 10
  done
done
```

---

## Data Collection Template

### Results CSV Format
```csv
scenario,mode,run,p50_ms,p95_ms,p99_ms,rps,errors,pg_count,ch_count,missing,cpu_pct,ram_mb,pg_conn,redis_lag,redis_pending
10,postgres,1,45,120,180,250,0,12500,0,0,25,512,10,0,0
10,dual,1,48,125,185,245,0,12500,12500,0,28,550,10,0,0
...
```

### Summary Report Format
```
Scenario 10 (50 users):
  postgres: p50=45ms p95=120ms p99=180ms rps=250
  dual:     p50=48ms p95=125ms p99=185ms rps=245
  Difference: +6.7% p50, +4.2% p95, +2.8% p99, -2.0% rps
  
Scenario 11 (200 users):
  ...
```

---

## Post-Test Actions

### 1. Restore Original Configuration
```bash
cd /opt/docker/them
# Restore to dual mode
sed -i 's/THE_M_EVENTS_GATEWAY=postgres/THE_M_EVENTS_GATEWAY=dual/' .env

# Recreate bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml stop them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge

# Verify
docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY
```

### 2. Document Results
Create `docs/PERFORMANCE_COMPARISON_RESULTS.md` with:
- Measured latency differences (percentage)
- Throughput differences (percentage)
- Resource usage differences
- ClickHouse delivery verification (dual mode)
- Conclusion: Is overhead acceptable?

### 3. Commit Results
```bash
git add docs/PERFORMANCE_COMPARISON_RESULTS.md
git commit -m "perf: Gateway postgres vs dual mode comparison results

Scenarios tested: 10/11/12/40/41/42/43
Runs per scenario: 3 (alternating mode order)

Results:
- Latency difference: X%
- Throughput difference: Y%
- ClickHouse delivery: 100% verified
- Conclusion: [overhead acceptable/not acceptable]

See docs/PERFORMANCE_COMPARISON_RESULTS.md for full data."
git push origin main
```

---

## Important Notes

### Gateway Latency Calculation
**WARNING**: Gateway's `latency_ms` in `gateway_requests` table is calculated **BEFORE** the `WriteRequest` call, NOT after. It does NOT include:
- Postgres write time
- Event emission time (in dual mode)
- Response marshaling time

**Use client-side metrics** (from test runner) as primary measurement.

### Blocked Mode
**DO NOT** enable `THE_M_EVENTS_GATEWAY=clickhouse` mode:
- This mode is BLOCKED for Gateway (missing budget/body support)
- Only test `postgres` vs `dual`

### Claims
**DO NOT** claim "faster" or "no overhead" without measured evidence showing:
- Latency difference <5% (or report actual percentage)
- Throughput difference <10% (or report actual percentage)
- Statistical significance (3+ runs, alternating order)

---

## Prerequisites

- ✅ Test runner operational
- ✅ Admin credentials (username: admin, password: admin123)
- ✅ Scenarios 10/11/12/40-43 exist
- ✅ Sufficient disk space for test data
- ✅ Stable network (no other load on system)

---

## Estimated Time

- Setup/teardown: 30 min
- Per scenario per mode: 5-10 min (depends on user count)
- Total scenarios: 7
- Runs per scenario: 6 (3 postgres + 3 dual)
- Mode switches: ~14 (2 min each)
- **Total estimated time**: 3-4 hours

---

## Success Criteria

1. ✅ All 7 scenarios tested in both modes
2. ✅ 3 runs per scenario/mode completed
3. ✅ Client-side metrics collected for all runs
4. ✅ Dual mode: 100% ClickHouse delivery verified
5. ✅ Statistical analysis: mean + std dev per scenario
6. ✅ Original configuration restored
7. ✅ Results documented and committed

---

**Date**: 2026-10-21 13:30 UTC  
**Status**: Ready for execution  
**Next session**: Performance testing
