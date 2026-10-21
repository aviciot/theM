# Event Delivery Verification Guide
# Using Test Runner for End-to-End Validation
# Date: 2026-10-21

---

## Quick Start

### 1. Run the Test (via UI)

1. **Open Test Runner**: http://10.55.125.43:3210/scenarios
2. **Find Test**: "50 — Event Delivery to ClickHouse"
3. **Click**: ▶ Run
4. **Wait**: ~30 seconds for 100 users to complete
5. **Verify**: Run `./scripts/verify_event_delivery.sh`

### 2. Verify Results

```bash
cd /opt/docker/them
./scripts/verify_event_delivery.sh
```

**Expected Output:**
```
✅ TEST PASSED
  • All 100 events delivered to ClickHouse
  • Delivery rate: 100%
  • Redis lag: 0 (fully processed)
  • Redis pending: 0
```

---

## What This Tests

### Real Gateway Requests
- Uses test-runner to send 100 actual gateway requests
- Model: `fast` (mock provider, ~200ms latency)
- Token: `test-gateway-token-12345` (exists in DB)
- NO app assigned → Direct path (no Temporal overhead)

### Dual-Mode Event Flow
```
Gateway Request
  ↓
PG: gateway_requests (authoritative write)
  ↓
Redis Streams: them:events (emit event)
  ↓
Event-Writer: consumer (XREADGROUP)
  ↓
ClickHouse: events_gateway_request (mirror)
```

### Verification Points
1. **Count Match**: PG count == CH count
2. **Redis Lag**: Should be 0 (all processed)
3. **Redis Pending**: Should be 0 (no stuck messages)
4. **Field Integrity**: 10 random samples match (manual check)

---

## Prerequisites

### 1. DUAL Mode Enabled

Check:
```bash
docker exec them-go-bridge printenv | grep THE_M_EVENTS_GATEWAY
```

Expected: `THE_M_EVENTS_GATEWAY=dual`

If not set:
```bash
# Add to docker-compose.dev.yml under them-go-bridge:
environment:
  - THE_M_EVENTS_GATEWAY=dual

# Restart bridge
docker restart them-go-bridge
```

### 2. Event Stack Running

```bash
# Start events stack
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml -f docker-compose.events.yml \
  --profile events up -d

# Verify containers
docker ps | grep -E "clickhouse|redis-events|event-writer"

# Check health
docker exec them-event-writer wget -qO- http://localhost:8080/health
# Expected: {"status":"healthy","lag":0,"pending":0,...}
```

### 3. Test Runner Available

```bash
# Check test runner
docker ps | grep test-runner

# Backend should be running
docker logs them-test-runner-backend --tail 5

# Verify scenario loaded
docker exec them-test-runner-backend wget -qO- http://localhost:8090/scenarios | \
  python3 -c "import sys,json; scenarios=[s['name'] for s in json.load(sys.stdin)]; print('50 — Event Delivery' in str(scenarios))"
```

---

## Manual Verification Steps

### 1. Field-Level Reconciliation

After running the test, verify specific fields match:

**Get PG sample:**
```bash
docker exec them-postgres psql -U them -d them -c "
  SELECT 
    id AS request_id,
    provider,
    model_served,
    tokens_in,
    tokens_out,
    cost_usd::text,
    latency_ms,
    ttfb_ms
  FROM gateway_requests
  WHERE model_served = 'mock-model-v1'
  ORDER BY created_at DESC
  LIMIT 5;
"
```

**Check CH has same records:**
```bash
# Copy a request_id from above, then:
docker exec them-clickhouse clickhouse-client --database=them_events --query "
  SELECT 
    request_id,
    provider,
    model_served,
    tokens_in,
    tokens_out,
    cost_usd,
    latency_ms,
    ttfb_ms
  FROM events_gateway_request
  WHERE request_id = '<paste-uuid-here>'
  FORMAT Pretty;
"
```

**Expected**: All fields match exactly

### 2. Check Event-Writer Logs

```bash
docker logs them-event-writer --since 5m | grep -E "batch written|ERROR|dlq"
```

**Expected**: 
- Several "batch written" messages (e.g., "batch written count=100")
- NO "ERROR" messages
- NO "dlq" messages (no poison events)

### 3. Verify No Poison Messages

```bash
# Check DLQ is empty
docker exec them-redis-events redis-cli XLEN them:events:dlq
```

**Expected**: `0` (no poison messages)

---

## Troubleshooting

### Issue: 0% Delivery (All Events Stuck)

**Symptoms:**
- PG has 100 events
- CH has 0 events
- Redis lag = 0, pending = 100

**Diagnosis:**
```bash
# Check if events are poison (schema mismatch)
docker logs them-event-writer --since 2m | grep -E "ERROR|poison|nil"

# Check if writer crashed
docker ps --filter name=them-event-writer --format "{{.Status}}"
```

**Solutions:**
- If "nil gateway request event": Schema mismatch, events are malformed
- If writer "Restarting": Writer crashed, check for panic in logs
- If pending > 0 after 2 minutes: Move poison messages to DLQ manually

### Issue: Partial Delivery (e.g., 80/100)

**Symptoms:**
- PG: 100 events
- CH: 80 events
- Redis lag: 0, pending: 0

**Diagnosis:**
```bash
# Check if some events went to DLQ
docker exec them-redis-events redis-cli XLEN them:events:dlq

# Check DLQ messages
docker exec them-redis-events redis-cli XRANGE them:events:dlq - + COUNT 5
```

**Interpretation:**
- If DLQ has 20 messages: 20 were poison, 80 valid delivered ✓
- If DLQ is empty: 20 events lost (data loss ✗)

### Issue: Redis Lag Growing

**Symptoms:**
- Redis lag increases over time
- Events not being processed

**Diagnosis:**
```bash
# Check ClickHouse is reachable
docker exec them-event-writer wget -qO- http://them-clickhouse:8123/ping

# Check event-writer is alive
docker exec them-event-writer ps aux

# Check event-writer logs for ClickHouse errors
docker logs them-event-writer --since 5m | grep clickhouse
```

**Solutions:**
- ClickHouse down: Restart `docker restart them-clickhouse`
- Writer paused: ClickHouse was unavailable, will resume when back up
- Writer crashed: Restart `docker restart them-event-writer`

---

## Restart Resilience Tests

### Test 1: Writer Restart Mid-Processing

```bash
# Clear test data
docker exec them-postgres psql -U them -d them -c "DELETE FROM gateway_requests WHERE model_served = 'mock-model-v1';"

# Run test (50 users) in UI
# While running, restart writer:
docker restart them-event-writer

# Run test again (50 more users)
# Verify: 100 events in both PG and CH
```

### Test 2: Redis Restart (AOF Recovery)

```bash
# Send 100 events via test runner
# Restart Redis
docker restart them-redis-events

# Check stream survived
docker exec them-redis-events redis-cli XLEN them:events

# Wait for delivery
sleep 30

# Verify: All events delivered
```

### Test 3: ClickHouse Outage

```bash
# Stop ClickHouse
docker stop them-clickhouse

# Send 100 events via test runner

# Check Redis backlog (should be 100)
docker exec them-redis-events redis-cli XLEN them:events

# Start ClickHouse
docker start them-clickhouse

# Wait for delivery
sleep 60

# Verify: All 100 delivered
```

---

## Success Criteria

### Must Pass

- [ ] 100/100 events delivered to ClickHouse
- [ ] PG count matches CH count exactly
- [ ] Redis lag = 0 after delivery completes
- [ ] Redis pending = 0 (no stuck messages)
- [ ] All 10 sampled fields match between PG and CH
- [ ] No ERROR messages in event-writer logs
- [ ] DLQ is empty (no poison messages from valid test)

### Should Pass

- [ ] Writer survives restart (no event loss)
- [ ] Redis survives restart (AOF recovery)
- [ ] ClickHouse outage handled gracefully (retry + deliver)

---

## Next Steps After 100% Delivery Proven

1. **Add Retention Safety Tests**
   - `go/internal/events/retention_test.go`
   - Test pending message protection
   - Test unread message protection

2. **Add Poison Event Regression Test**
   - Send 1 malformed + 99 valid events
   - Verify: 99 delivered, 1 in DLQ
   - Verify: Valid events not blocked by poison

3. **Performance Test**
   - Run with 200 users (200 concurrent requests)
   - Verify: All delivered within 2 minutes
   - Measure: p50, p95, p99 latency for event delivery

4. **Production Checklist**
   - [ ] Delivery proven (100/100)
   - [ ] Restart resilience verified
   - [ ] Retention safety tests added
   - [ ] Health monitoring configured
   - [ ] Alerting set up (lag > 1000)
   - [ ] Runbook documented

---

## Files

- **Test Scenario**: `test-runner/data/scenarios/50-event-delivery-test.json`
- **Verification Script**: `scripts/verify_event_delivery.sh`
- **This Guide**: `docs/EVENT_DELIVERY_VERIFICATION_GUIDE.md`
- **Test Runner Skill**: `test-runner/.claude/skills/test-runner-guide.md`

---

**Status**: Ready for execution. Run test via UI, then execute verification script.
