# Handover: Gateway Performance Testing
# Date: 2026-10-21 13:30 UTC
# From: Events verification session
# To: Performance testing session

---

## Session Summary (Completed)

### What Was Accomplished
1. ✅ **Events infrastructure verification complete**
   - DLQ failure: Mock tests added, all passed
   - Retention safety: Runtime verified (pending protected)
   - Resilience: 4/4 tests passed (4000 events, 100% delivery)
   - MCP: Code verified, 1 event in CH, runtime pending agent usage

2. ✅ **Production readiness confirmed**
   - Infrastructure solid, monitoring ready
   - Deploy-ready with conditional MCP tracking

3. ✅ **Documentation complete**
   - Verification status: `docs/VERIFICATION_FINAL_STATUS.md`
   - Test plan: `docs/PERFORMANCE_TEST_PLAN.md`

### Commits
- `85d98833` - DLQ mock tests (passed)
- `2540e295` - Verification complete, production-ready

### Current State
- **Branch**: main (clean, pushed)
- **HEAD**: 2540e295
- **Gateway mode**: dual (THE_M_EVENTS_GATEWAY=dual)
- **Infrastructure**: Running, healthy

---

## Next Task: Performance Testing

### Objective
Compare Gateway performance between `postgres` (PG only) and `dual` (PG + ClickHouse) modes to measure actual overhead of event emission.

### Key Question
**Does dual mode add measurable latency or reduce throughput?**

---

## Test Scenarios

Run these 7 scenarios in BOTH modes (postgres, dual):
1. **10-perf-50-users-direct.json** - 50 users
2. **11-perf-200-users-direct.json** - 200 users
3. **12-perf-400-users-direct.json** - 400 users
4. **40-logging-perf-minimal.json** - MINIMAL logging
5. **41-logging-perf-metrics.json** - METRICS logging
6. **42-logging-perf-debug.json** - DEBUG logging
7. **43-logging-perf-audit.json** - AUDIT logging

**Runs**: 3 iterations per scenario/mode, alternating order (postgres→dual→postgres→dual→...)

**Total**: 42 test runs (7 scenarios × 2 modes × 3 runs)

---

## Critical Requirements

### 1. Mode Switching Procedure
**IMPORTANT**: Environment variable changes require container recreation:

```bash
# Change mode in .env
sed -i 's/THE_M_EVENTS_GATEWAY=dual/THE_M_EVENTS_GATEWAY=postgres/' /opt/docker/them/.env

# Recreate bridge (required for env changes)
cd /opt/docker/them
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml stop them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml up -d them-go-bridge

# Verify mode changed
docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY
# Must show: THE_M_EVENTS_GATEWAY=postgres

# Wait for ready
sleep 10
```

### 2. Metrics to Collect

**Primary (Client-side)**:
- p50, p95, p99 latency (from test runner)
- Requests/sec
- Error count/rate

**Secondary (Server-side)**:
- CPU/RAM: `docker stats them-go-bridge --no-stream`
- PG connections: `psql -c "SELECT count(*) FROM pg_stat_activity WHERE datname='them';"`
- Redis lag/pending (dual mode only)
- ClickHouse count (dual mode only)

### 3. Dual Mode Verification
**After each dual-mode run**, verify ALL request IDs reached ClickHouse:

```bash
PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM gateway_requests;")
sleep 30  # Wait for event-writer drain
CH_COUNT=$(docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request FORMAT TabSeparated;")

if [ "$PG_COUNT" = "$CH_COUNT" ]; then
  echo "✅ All requests delivered"
else
  echo "⚠️  Missing: $((PG_COUNT - CH_COUNT))"
fi
```

### 4. Data Clearing Between Tests
```bash
docker exec them-postgres psql -U them -d them -c "TRUNCATE gateway_requests CASCADE;"
docker exec them-clickhouse clickhouse-client --query "TRUNCATE TABLE them_events.events_gateway_request;"
docker exec them-redis-events redis-cli FLUSHDB
```

---

## Important Warnings

### Gateway Latency Calculation
**⚠️ WARNING**: `gateway_requests.latency_ms` is calculated **BEFORE WriteRequest**, NOT after.

It does NOT include:
- Postgres write time
- Event emission time
- Response marshaling

**Use client-side metrics** (test runner) as primary measurement, not DB latency.

### Blocked Mode
**DO NOT** test `clickhouse`-only mode:
- BLOCKED for Gateway (missing budget/body queries)
- Only test: `postgres` vs `dual`

### Claims
**DO NOT** claim "faster" or "no overhead" without evidence:
- Report actual percentage differences
- Latency: postgres vs dual (e.g., "+6.7% p50")
- Throughput: postgres vs dual (e.g., "-2.0% rps")
- Statistical significance: 3+ runs, alternating order

---

## Test Execution Steps

### Phase 1: Baseline (Current Dual)
1. Verify mode: `docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY`
2. Clear data
3. Run scenarios 10-12, 40-43 (3 times each)
4. Collect metrics
5. Verify ClickHouse delivery (100%)

### Phase 2: Postgres Mode
1. Switch mode (see procedure above)
2. Verify mode changed
3. Clear data
4. Run same scenarios (3 times each)
5. Collect metrics

### Phase 3: Alternating Runs
For statistical validity:
- Run 1: postgres → dual
- Run 2: dual → postgres
- Run 3: postgres → dual

Change mode between each test.

### Phase 4: Analysis
1. Calculate mean + std dev per scenario
2. Calculate percentage differences
3. Assess overhead acceptability
4. Document results

### Phase 5: Restore
1. Switch back to dual mode
2. Verify mode changed
3. Confirm infrastructure healthy

---

## Results Template

Create `docs/PERFORMANCE_COMPARISON_RESULTS.md`:

```markdown
# Performance Comparison: postgres vs dual

## Test Environment
- Date: [date]
- Commit: 2540e295
- Server: [specs]
- Runs: 3 per scenario/mode

## Results

### Scenario 10 (50 users)
| Mode | p50 | p95 | p99 | RPS | Errors |
|---|---|---|---|---|---|
| postgres | 45ms | 120ms | 180ms | 250 | 0 |
| dual | 48ms | 125ms | 185ms | 245 | 0 |
| **Diff** | **+6.7%** | **+4.2%** | **+2.8%** | **-2.0%** | **0** |

[Repeat for scenarios 11, 12, 40-43]

## Summary
- Average latency increase: X%
- Average throughput decrease: Y%
- ClickHouse delivery: 100% verified
- Resource usage: [CPU/RAM differences]

## Conclusion
[Overhead acceptable? Deploy dual mode? Or optimize?]
```

---

## Success Criteria

- [ ] All 7 scenarios tested in both modes
- [ ] 3 runs per scenario/mode completed
- [ ] Client-side metrics collected
- [ ] Dual mode: 100% ClickHouse delivery verified
- [ ] Statistical analysis: mean + std dev
- [ ] Original configuration restored (dual)
- [ ] Results documented with percentage differences
- [ ] Results committed to main

---

## Estimated Time

- Per scenario: 5-10 minutes
- Mode switches: 2 minutes each (~14 total)
- Data collection: Automated
- Analysis: 30 minutes
- **Total: 3-4 hours**

---

## Post-Test Commit

```bash
git add docs/PERFORMANCE_COMPARISON_RESULTS.md
git commit -m "perf: Gateway postgres vs dual mode comparison

Tested: scenarios 10/11/12/40/41/42/43
Runs: 3 per scenario/mode (alternating)

Results:
- Latency: +X% (postgres → dual)
- Throughput: -Y%
- ClickHouse delivery: 100%
- Conclusion: [overhead acceptable/needs optimization]

See docs/PERFORMANCE_COMPARISON_RESULTS.md"
git push origin main
```

---

## Files Reference

- **Test plan**: `docs/PERFORMANCE_TEST_PLAN.md` (detailed procedures)
- **Current status**: `docs/VERIFICATION_FINAL_STATUS.md`
- **Test scenarios**: `test-runner/data/scenarios/`
- **Test runner**: `scripts/tests/run_tests.py`

---

## Contact Points

- **Current HEAD**: 2540e295
- **Branch**: main
- **Mode**: dual (restore to this after testing)
- **Next task**: Run usage/steps producer (after perf testing)

---

**Prepared**: 2026-10-21 13:30 UTC  
**Ready for**: Fresh session with focus on performance measurement
