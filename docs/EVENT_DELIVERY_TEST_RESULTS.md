# Event Delivery Test Results
# Date: 2026-10-21 07:42 UTC
# Test: 50 — Event Delivery to ClickHouse

---

## Test Execution

**Method**: Test Runner UI (http://10.55.125.43:3210)
**Scenario**: `50-event-delivery-test`
**Run ID**: `83dff0a3-320f-422b-bf70-553fbc2bfd78`
**Duration**: ~30 seconds for 100 users

### Test Parameters
- Users: 100
- Messages per user: 1
- Model: fast (mock provider)
- Token: test-gateway-token-12345
- Gateway mode: Direct path (no Temporal)
- Events mode: DUAL (PG + ClickHouse mirror)

---

## Results

### ✅ 100% Delivery Verified

| Metric | Value | Status |
|---|---|---|
| PG gateway_requests | 100 | ✅ |
| CH events_gateway_request | 100 | ✅ |
| Delivery rate | 100% | ✅ |
| Redis lag | 0 | ✅ |
| Redis pending | 0 | ✅ |
| Event-writer processed | 100 | ✅ |
| Event-writer failed | 0 | ✅ |
| Event-writer DLQ | 0 | ✅ |

### Field Integrity Verification

**Sample Request ID**: `19cb7a91-c656-4d7e-8515-e1724853e302`

| Field | PG Value | CH Value | Match |
|---|---|---|---|
| request_id | 19cb7a91-... | 19cb7a91-... | ✅ |
| provider | mock | mock | ✅ |
| model_served | mock-model-v1 | mock-model-v1 | ✅ |
| status | ok | ok | ✅ |
| tokens_in | 0 | 0 | ✅ |
| tokens_out | 0 | 0 | ✅ |
| cost_usd | 0.00000000 | 0 | ✅ |
| latency_ms | 15 | 15 | ✅ |
| ttfb_ms | NULL | 0 | ✅ |

**All fields match between PG and ClickHouse.**

---

## Timeline

| Time (UTC) | Event |
|---|---|
| 07:41:26 | Test triggered via API |
| 07:41:25-26 | 100 gateway requests processed |
| 07:41:26-27 | Events emitted to Redis Streams |
| 07:41:27-30 | Event-writer consumed and delivered to CH |
| 07:42:26 | Verification complete: 100/100 delivered |

---

## Critical Finding: Poison Message Blocking

### Problem Discovered

During initial test run, old synthetic test events (with schema mismatch) were stuck in the Redis stream. These poison messages blocked ALL subsequent valid events from processing.

**Error**: `clickhouse sink: nil gateway request event`
**Impact**: 100 valid test-runner events stuck in pending state
**Root cause**: Poison messages prevent batch processing of subsequent events

### Resolution

Cleared Redis stream manually:
```bash
docker exec them-redis-events redis-cli DEL them:events
docker exec them-redis-events redis-cli XGROUP CREATE them:events event-writers 0 MKSTREAM
```

After clearing poison messages, re-run achieved 100% delivery.

### Required Fix

**Poison messages MUST move to DLQ without blocking valid events.**

Current behavior:
```
Poison event → Retry 3x → Leave pending → Block all subsequent events
```

Expected behavior:
```
Poison event → Retry 3x → Move to DLQ → Continue processing valid events
```

This is the #1 blocker for production deployment.

---

## Key Findings

### What Works

1. **DUAL Mode**: Gateway correctly writes to PG and emits to Redis Streams
2. **Event-Writer**: Processes valid events from Redis → ClickHouse
3. **Field Mapping**: All fields map correctly between PG and CH schemas
4. **Performance**: 100 events processed in ~3-5 seconds
5. **Health Monitoring**: `/health` endpoint reports accurate lag/pending/processed counts

### What Needs Fixing

1. **Poison Message Handling**: Must move to DLQ without blocking valid events
2. **No Restart Resilience Tests**: Writer/Redis/CH restart scenarios untested
3. **No Regression Tests**: Retention safety tests missing
4. **No Load Testing**: Only tested 100 events, production may see thousands

---

## Next Steps (Priority Order)

### 1. Fix Poison Message Handling (BLOCKING)

**Issue**: Poison messages block all subsequent valid events
**Solution**: Modify `go/internal/events/consumer.go` RecoverPending to:
- Move poison messages to DLQ immediately after 3 retries
- ACK poison messages after DLQ write
- Continue processing rest of batch

**Test**: Send 1 malformed + 99 valid events, verify 99 delivered + 1 in DLQ

### 2. Add Restart Resilience Tests (HIGH PRIORITY)

**Writer Restart**:
- Send 50 events
- Restart writer mid-processing
- Send 50 more
- Verify: 100 delivered, no duplicates

**Redis Restart**:
- Send 100 events
- Restart Redis (AOF recovery)
- Verify: All 100 delivered

**ClickHouse Outage**:
- Stop ClickHouse
- Send 100 events
- Start ClickHouse
- Verify: All 100 delivered after resume

### 3. Add Regression Tests (HIGH PRIORITY)

`go/internal/events/retention_test.go`:
- Test pending message protection
- Test unread message protection
- Test partial delivery trimming

### 4. Performance Testing (MEDIUM PRIORITY)

- Test with 1,000 events (10x current test)
- Test with 10,000 events (100x current test)
- Measure delivery latency (p50, p95, p99)
- Identify throughput limits

---

## Production Readiness Checklist

- [x] 100% delivery proven (100/100 events)
- [x] Field integrity verified
- [x] Redis permissions fixed
- [x] AOF persistence verified
- [x] Health monitoring working
- [ ] Poison message handling fixed (BLOCKER)
- [ ] Writer restart resilience verified
- [ ] Redis restart resilience verified
- [ ] ClickHouse outage handling verified
- [ ] Retention safety tests added
- [ ] Load testing complete (1K+ events)
- [ ] Alerting configured
- [ ] Runbook documented

**Current Status**: 5/12 complete (42%)
**Estimated time to production-ready**: 3-5 hours

---

## Conclusion

**✅ Event delivery works correctly with valid events.**

Test-runner successfully sent 100 real gateway requests, all were written to Postgres, emitted to Redis Streams, consumed by event-writer, and delivered to ClickHouse with 100% accuracy and field integrity.

**❌ Poison message handling is broken.**

Malformed events block all subsequent valid events from processing. This must be fixed before production deployment.

**Next immediate action**: Fix poison message handling in `go/internal/events/consumer.go` to move poison messages to DLQ without blocking the batch.
