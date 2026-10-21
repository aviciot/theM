# Poison Event Fix — Implementation Status
# Date: 2026-10-21 07:58 UTC
# Session: Background job continuing from context compaction

---

## Implementation Complete (Partial)

###  Completed
1. ✅ `go/internal/events/errors.go` — Error types (ErrMalformedEvent, ErrTransientFailure)
2. ✅ `go/internal/events/validate.go` — ValidateEvent() checks required fields
3. ✅ `go/internal/events/clickhouse.go` — Emit() validates before write, wraps connection errors as transient
4. ✅ `go/cmd/event-writer/main.go` RecoverPending — Distinguishes malformed vs transient, moves poison to DLQ
5. ✅ Built and restarted them-event-writer with poison fix

### ❌ Incomplete
- **Main Run loop flush() function** (line ~436-562 in `go/cmd/event-writer/main.go`) — Has complex nested retry logic that needs simplification
- **Regression test** — Test created (`scripts/test_poison_event_isolation.sh`) but FAILED due to incomplete main loop fix

---

## Problem Found

The main event processing loop (`flush()` function starting line 436) has overly complex nested retry logic:

1. Iterates through batch
2. Calls `Emit()` on each event
3. If ANY event fails with validation error, enters transient retry path (line 456-524)
4. Transient retry path RE-EMITS all events including poison
5. This causes poison to block valid events

**Root cause**: The flush logic mixes validation detection with batch writing. When the first event in a batch is poison, it enters the "transient retry" path which retries all 3 events together.

---

## Correct Implementation Strategy

**Two-pass approach**:

```go
// Pass 1: Validate all events BEFORE writing any
validBatch := []Event{}
validMsgIDs := []string{}
poisonBatch := []PoisonEvent{}

for i, event := range batch {
    if err := ValidateEvent(event); err != nil {
        poisonBatch = append(poisonBatch, ...)
    } else {
        validBatch = append(validBatch, event)
        validMsgIDs = append(validMsgIDs, messageIDs[i])
    }
}

// Pass 2: Write only valid events with retry
for attempt := 1; attempt <= maxRetries; attempt++ {
    for _, event := range validBatch {
        if err := c.sink.Emit(ctx, event); err != nil {
            // Transient error → retry
        }
    }
}

// Pass 3: Move poison to DLQ
for _, poison := range poisonBatch {
    moveToDLQ(...)
    ackMessages(...)
}
```

This ensures poison events never enter the write+retry loop.

---

## Files Modified

| File | Status | Changes |
|---|---|---|
| `go/internal/events/errors.go` | ✅ Complete | Error types added |
| `go/internal/events/validate.go` | ✅ Complete | ValidateEvent() added |
| `go/internal/events/clickhouse.go` | ✅ Complete | Emit() validates first, wraps connection errors |
| `go/cmd/event-writer/main.go` RecoverPending (line ~221-380) | ✅ Complete | Poison → DLQ, transient → retry |
| `go/cmd/event-writer/main.go` flush() (line ~436-562) | ❌ Incomplete | Needs two-pass rewrite |
| `scripts/test_poison_event_isolation.sh` | ⚠️  Test created | FAILS due to incomplete main loop |

---

## Test Results

**Test**: `scripts/test_poison_event_isolation.sh`

**Scenario**: Send 3 events: valid → malformed (missing GatewayRequest) → valid

**Expected**:
- CH count: 2 (both valid events delivered)
- DLQ count: 1 (malformed event)
- Redis lag: 0
- Redis pending: 0

**Actual**:
- CH count: 0
- DLQ count: 2
- Redis lag: 0
- Redis pending: 3

**Diagnosis**: All 3 events stuck in pending because poison triggered transient retry path, which retried all 3 together and failed.

**Logs**:
```
ERROR failed to write event clickhouse sink: nil gateway request event
WARN retrying after backoff attempt=1 backoff_ms=1000
ERROR failed to write event clickhouse sink: nil gateway request event
WARN retrying after backoff attempt=2 backoff_ms=2000
ERROR failed to write event clickhouse sink: nil gateway request event
ERROR batch write failed after retries count=3
```

This confirms the poison event blocked the entire batch.

---

## Next Steps (for next session or continuation)

1. **Rewrite flush() function** (line ~436-562 in `go/cmd/event-writer/main.go`):
   - Use two-pass approach (validate → write valid → DLQ poison)
   - Remove nested retry logic
   - Simplify to ~80 lines

2. **Re-run regression test**:
   ```bash
   /opt/docker/them/scripts/test_poison_event_isolation.sh
   ```
   - Should pass: 2 valid events in CH, 1 malformed in DLQ

3. **Add Go unit test** (`go/internal/events/validate_test.go`):
   - Test ValidateEvent() catches nil payloads
   - Test each event kind validation

4. **Add integration test** (`go/cmd/event-writer/poison_test.go`):
   - valid → poison → valid sequence
   - Verify 2 delivered, 1 DLQ, 0 pending

5. **Update docs**:
   - `go/TEST_INDEX.md` — add new tests
   - `docs/POISON_EVENT_FIX_PLAN.md` — mark complete
   - `docs/EVENT_DELIVERY_VERIFICATION_GUIDE.md` — add poison test instructions

6. **Commit and push**:
   - All code changes
   - Test results
   - Documentation updates

---

## Code Snippet (for continuation)

The flush() function needs this replacement (starting line ~436):

```go
// Write valid events to ClickHouse with retry
if len(batch) > 0 {
    // First pass: separate valid from malformed (validation only)
    validBatch := []events.Event{}
    validMsgIDs := []string{}
    poisonBatch := []struct {
        msgID string
        err   error
    }{}

    for i, event := range batch {
        if err := events.ValidateEvent(event); err != nil {
            c.logger.Warn("poison event in batch", "msg_id", messageIDs[i], "event_id", event.EventID, "error", err)
            poisonBatch = append(poisonBatch, struct {
                msgID string
                err   error
            }{messageIDs[i], fmt.Errorf("%w: %v", events.ErrMalformedEvent, err)})
        } else {
            validBatch = append(validBatch, event)
            validMsgIDs = append(validMsgIDs, messageIDs[i])
        }
    }

    // Second pass: write valid events with retry
    if len(validBatch) > 0 {
        var lastErr error
        for attempt := 1; attempt <= maxRetries; attempt++ {
            success := true
            for _, event := range validBatch {
                if err := c.sink.Emit(ctx, event); err != nil {
                    c.logger.Error("failed to write valid event", "error", err, "event_id", event.EventID, "attempt", attempt)
                    lastErr = err
                    success = false
                    break
                }
            }

            if success {
                if err := c.ackMessages(ctx, validMsgIDs); err != nil {
                    c.logger.Error("failed to ack valid messages", "error", err)
                    return
                }
                c.processedCount += uint64(len(validBatch))
                c.logger.Info("batch written", "count", len(validBatch))
                consecutiveFailures = 0
                backoff = 1 * time.Second
                break
            }

            if attempt < maxRetries {
                sleep := backoff * time.Duration(attempt)
                c.logger.Warn("retrying after backoff", "attempt", attempt, "backoff_ms", sleep.Milliseconds())
                time.Sleep(sleep)
            }
        }

        if lastErr != nil {
            c.logger.Error("batch write failed after retries (transient)", "error", lastErr, "count", len(validBatch))
            c.failedCount += uint64(len(validBatch))
            consecutiveFailures++
            batch = batch[:0]
            messageIDs = messageIDs[:0]

            if consecutiveFailures >= 3 {
                pauseDuration := min(backoff*time.Duration(consecutiveFailures), 30*time.Second)
                c.logger.Warn("ClickHouse unavailable, pausing ingestion", "pause_ms", pauseDuration.Milliseconds())
                time.Sleep(pauseDuration)
            }
            return
        }
    }

    // Third pass: move poison to DLQ
    for _, poison := range poisonBatch {
        if err := c.moveToDLQ(ctx, poison.msgID, fmt.Sprintf("validation_failed: %v", poison.err)); err != nil {
            c.logger.Error("failed to move poison to DLQ", "error", err, "msg_id", poison.msgID)
            continue
        }
        if err := c.ackMessages(ctx, []string{poison.msgID}); err != nil {
            c.logger.Error("failed to ACK poison event", "error", err, "msg_id", poison.msgID)
        } else {
            c.dlqCount++
        }
    }

    if len(poisonBatch) > 0 {
        c.logger.Info("poison events to DLQ", "count", len(poisonBatch))
    }

    batch = batch[:0]
    messageIDs = messageIDs[:0]
}
```

---

## Current HEAD

```
4852d135 docs: poison event fix plan with detailed implementation strategy
```

---

## Session Note

Context approaching limit. Recommend:
1. Commit current progress with this status doc
2. Continue in fresh session with `docs/POISON_FIX_STATUS.md` as the starting point
3. Apply the code snippet above to complete the flush() rewrite
4. Run regression test and verify success
5. Add unit/integration tests
6. Update all docs and commit final working fix

**Estimated time to complete**: 30-45 minutes in fresh session
