# Performance Test In Progress
# Date: 2026-10-21 12:40 UTC
# Status: RUNNING

---

## Test Configuration

**Objective**: Compare Gateway performance between `postgres` (write to PG only) and `dual` (write to PG + emit to ClickHouse) modes.

**Test Script**: `scripts/perf_comparison.py`
**Output Log**: `perf_comparison_log.txt`
**Background Task ID**: bol8hgxpm
**Output File**: `/tmp/claude-1006/-opt-docker-them/6150ade6-7d9e-48fe-a2ac-1e8acf619e12/tasks/bol8hgxpm.output`

---

## Scenarios

1. **10-perf-50-users-direct** - 50 concurrent users, 3 messages each (150 total requests)
2. **11-perf-200-users-direct** - 200 concurrent users, 3 messages each (600 total requests)
3. **12-perf-400-users-direct** - 400 concurrent users, 2 messages each (800 total requests)
4. **40-logging-perf-minimal** - 100 users, MINIMAL logging (no body capture)
5. **41-logging-perf-metrics** - 100 users, METRICS logging (metadata only)
6. **42-logging-perf-debug** - 100 users, DEBUG logging (full body capture)
7. **43-logging-perf-audit** - 100 users, AUDIT logging (full body capture)

---

## Test Execution Plan

**Runs**: 3 iterations per scenario/mode
**Mode Switching**: Alternating order to reduce bias
- Run 1: postgres → dual
- Run 2: dual → postgres
- Run 3: postgres → dual

**Total Tests**: 42 (7 scenarios × 2 modes × 3 runs)
**Estimated Time**: 3-4 hours

---

## Metrics Collected

### Client-Side (Primary)
- **p50/p95/p99 latency** - From test-runner results (actual user-experienced latency)
- **Requests/sec** - Throughput
- **Error count** - Failed requests

### Server-Side
- **PG count** - Total requests in gateway_requests table
- **CH count** - Total events in ClickHouse (dual mode only)
- **Missing events** - PG count - CH count (dual mode verification)
- **CPU/RAM** - Gateway container resource usage
- **PG connections** - Active database connections
- **Redis lag** - Pending events in Redis (dual mode only)

---

## Mode Switching Procedure

1. Update `docker-compose.dev.yml` (THE_M_EVENTS_GATEWAY=<mode>)
2. Stop them-go-bridge
3. Start them-go-bridge
4. Wait 10 seconds for readiness
5. Verify mode via `docker exec them-go-bridge env | grep THE_M_EVENTS_GATEWAY`

---

## Data Clearing Between Tests

Before each test run:
1. `TRUNCATE gateway_requests CASCADE;` (Postgres)
2. `TRUNCATE TABLE them_events.events_gateway_request;` (ClickHouse)
3. `FLUSHDB` (Redis events)

---

## Expected Results

**Hypothesis**: Dual mode adds minimal overhead because:
- Event emission is non-blocking (`_ = eventSink.Emit()`)
- Events buffered in-memory, written to Redis asynchronously
- Gateway writes to PG synchronously in BOTH modes
- ClickHouse write happens in separate event-writer process

**Expected Overhead**: <5% latency increase, <10% throughput decrease

---

## Progress (as of 12:41 UTC)

Run 1 - postgres mode:
- ✅ 10-perf-50-users: p50=194ms p95=633ms p99=636ms rps=150.0
- ✅ 11-perf-200-users: p50=221ms p95=290ms p99=334ms rps=600.0
- ✅ 12-perf-400-users: p50=567ms p95=714ms p99=776ms rps=600.0
- 🔄 40-logging-perf-minimal (running...)

---

## Output Files (Generated After Completion)

- `perf_test_results_raw.json` - Raw results for all 42 runs
- `perf_test_summary.json` - Aggregated comparison summary
- `docs/PERFORMANCE_COMPARISON_RESULTS.md` - Final report with analysis

---

## Notes

- **Gateway latency_ms calculation**: Calculated BEFORE WriteRequest, NOT after - doesn't include PG write time or event emission time
- **Primary measurement source**: Client-side metrics from test-runner (actual user-experienced latency)
- **Dual mode verification**: After each dual-mode run, verify 100% ClickHouse delivery (wait 30s for drain)
- **Mode restore**: After testing, restore to `dual` mode (current production setting)

---

**Started**: 2026-10-21 12:40 UTC  
**Estimated Completion**: 2026-10-21 16:00 UTC (approx 3.5-4 hours)
