# Poison Event Fix — COMPLETE ✅
# Date: 2026-10-21 08:07 UTC
# Regression Test: PASSED

---

## Summary

**Problem**: Malformed events blocked all subsequent valid events from processing.

**Solution**: Two-pass validation — validate all events before writing any, then process valid and poison separately.

**Result**: 100% isolation — poison events move to DLQ without blocking valid events in the same batch.

---

## Test Results

**Test**: `scripts/test_poison_event_isolation.sh`

**Scenario**: Send 3 events in sequence:
1. Valid gateway request
2. Malformed (missing `gateway_request` payload)
3. Valid gateway request

**Expected**:
- ClickHouse: 2 events (both valid)
- DLQ: 1 event (malformed)
- Redis lag: 0
- Redis pending: 0

**Actual** ✅:
```
✅ TEST PASSED
  • 2 valid events delivered to ClickHouse
  • Malformed event moved to DLQ
  • No blocking occurred (lag=0)
```

**Logs**:
```json
{"level":"WARN","msg":"poison event in batch","msg_id":"1792570036382-0","event_id":"886a8ce6-3195-429e-b302-27967796a83f","error":"gateway_request event has nil GatewayRequest payload"}
{"level":"INFO","msg":"batch written","count":2}
{"level":"INFO","msg":"poison events to DLQ","count":1}
```

**Field verification**:
- ✅ Event 1 delivered (request_id: 88759f94-9fbf-43bd-a3de-629eacb07e81)
- ✅ Event 3 delivered (request_id: 39679780-fdce-41ea-8f4c-3c781aa0bf57)

---

## Implementation

### Files Modified

| File | Changes |
|---|---|
| `go/internal/events/errors.go` | Added `ErrMalformedEvent`, `ErrTransientFailure` |
| `go/internal/events/validate.go` | Added `ValidateEvent()` — checks required fields for each kind |
| `go/internal/events/clickhouse.go` | `Emit()` validates first, wraps connection errors as transient |
| `go/internal/events/events.go` | Added JSON struct tags (`event_id`, `gateway_request`, etc.) |
| `go/cmd/event-writer/main.go` RecoverPending | Distinguishes malformed vs transient, moves poison to DLQ |
| `go/cmd/event-writer/main.go` flush() | Two-pass logic: validate → write valid → DLQ poison |
| `scripts/test_poison_event_isolation.sh` | Regression test (valid → poison → valid) |

### Key Design: Two-Pass Flush

**Before** (broken):
```go
for _, event := range batch {
    if err := Emit(event); err != nil {
        if IsMalformedEvent(err) {
            // poison → should go to DLQ
        } else {
            // transient → retry ENTIRE BATCH including poison
        }
    }
}
```

**After** (fixed):
```go
// Pass 1: Validate (no writes yet)
for _, event := range batch {
    if err := ValidateEvent(event); err != nil {
        poisonBatch = append(poisonBatch, ...)
    } else {
        validBatch = append(validBatch, event)
    }
}

// Pass 2: Write only valid events with retry
for attempt := 1; attempt <= maxRetries; attempt++ {
    for _, event := range validBatch {
        Emit(event) // Only valid events, no poison
    }
}

// Pass 3: Move poison to DLQ
for _, poison := range poisonBatch {
    moveToDLQ(...)
    ackMessages(...)
}
```

### RecoverPending Fix

```go
for attempt := 1; attempt <= maxRetries; attempt++ {
    if err := Emit(event); err != nil {
        if IsMalformedEvent(err) {
            // Don't retry validation errors
            break
        }
        // Transient error → retry
    }
}

if IsMalformedEvent(lastErr) {
    moveToDLQ(...)
    ackMessages(...) // ACK after DLQ write
} else {
    // Transient failure → leave pending for next recovery
}
```

---

## What Works Now

1. ✅ **Validation before write** — `ValidateEvent()` checks required fields for each event kind
2. ✅ **Error type distinction** — Malformed (→ DLQ) vs Transient (→ retry)
3. ✅ **Batch isolation** — Poison events don't block valid events in same batch
4. ✅ **RecoverPending** — Poison messages from previous runs moved to DLQ on recovery
5. ✅ **JSON unmarshaling** — Struct tags allow proper parsing of snake_case field names
6. ✅ **DLQ persistence** — Poison events ACKed only after DLQ write succeeds
7. ✅ **Regression test** — Automated test proves isolation (valid → poison → valid)

---

## Validation Rules

### Event-level (all kinds)
- `kind` must be non-empty
- `event_id` must be non-empty
- `tenant_id` must be non-empty

### Gateway Request
- `GatewayRequest` payload must be non-nil
- `request_id` must be non-empty

### Tool Call
- `ToolCall` payload must be non-nil

### User Activity
- `UserActivity` payload must be non-nil

---

## Error Flow

```
Event arrives
  ↓
ValidateEvent()
  ↓
Valid? → Write to ClickHouse
  ↓
  Success? → ACK
  ↓
  Transient failure? → Retry (max 3x)
    ↓
    Still failing? → Leave pending (RecoverPending will retry later)

Malformed? → Move to DLQ
  ↓
  DLQ write success? → ACK
  ↓
  DLQ write failed? → Leave pending (manual intervention)
```

---

## Production Readiness Checklist

- [x] 100% delivery proven (test-runner: 100/100 events)
- [x] Field integrity verified
- [x] Redis permissions fixed
- [x] AOF persistence verified
- [x] Health monitoring working
- [x] **Poison message handling fixed (COMPLETE)**
- [x] **Regression test passing (valid → poison → valid)**
- [ ] Writer restart resilience verified
- [ ] Redis restart resilience verified
- [ ] ClickHouse outage handling verified
- [ ] Retention safety tests added
- [ ] Load testing complete (1K+ events)
- [ ] Alerting configured
- [ ] Runbook documented

**Current Status**: 7/13 complete (54%)

**Next Steps**:
1. Add Go unit tests (`go/internal/events/validate_test.go`)
2. Add integration tests (restart, outage, retention)
3. Update `go/TEST_INDEX.md`
4. Run load test (1K+ events)
5. Configure alerting (lag > 1000)
6. Document runbook

---

## Commits

```
a47a3554 fix(events): complete poison event isolation - 2-pass validation
111fbe81 wip(events): poison event fix partial implementation
4852d135 docs: poison event fix plan with detailed implementation strategy
```

---

## Related Docs

- `docs/POISON_EVENT_FIX_PLAN.md` — Original design plan
- `docs/POISON_FIX_STATUS.md` — WIP status (superseded by this doc)
- `docs/EVENT_DELIVERY_TEST_RESULTS.md` — 100% delivery verification
- `docs/EVENT_DELIVERY_VERIFICATION_GUIDE.md` — Test runner usage
- `scripts/test_poison_event_isolation.sh` — Regression test
- `scripts/verify_event_delivery.sh` — Count reconciliation

---

**Status**: ✅ COMPLETE AND TESTED

Poison event isolation is now production-ready for the tested scenario (valid events interleaved with malformed events). Remaining work is resilience testing (restart, outage) and load testing.
