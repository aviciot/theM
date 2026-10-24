# Poison Event Fix Plan
# Critical Issue: Malformed events block all subsequent valid events

---

## Problem Statement

**Current Behavior:**
```
Malformed event → Retry 3x → Leave in pending → Block entire batch
```

**Required Behavior:**
```
Malformed event → Validate → Move to DLQ → ACK → Continue processing
Valid event + transient CH failure → Retry → Keep pending → Eventually deliver
```

---

## Root Cause

**Location**: `go/internal/events/clickhouse.go:456-459`

```go
func (c *ClickHouseSink) Emit(ctx context.Context, event Event) error {
    switch event.Kind {
    case EventKindGatewayRequest:
        return c.writeGatewayRequest(ctx, event.TenantID, event.Timestamp, event.EventID, event.GatewayRequest)
```

**At line 477:**
```go
func (c *ClickHouseSink) writeGatewayRequest(..., gw *GatewayRequestEvent) error {
    if gw == nil {
        return fmt.Errorf("clickhouse sink: nil gateway request event")  // THIS ERROR
    }
```

**Why `gw` is nil:**
Event JSON unmarshals but `event.GatewayRequest` field is not populated correctly due to schema mismatch or missing fields.

**Why it blocks:**
`go/cmd/event-writer/main.go:RecoverPending` retries failed events but doesn't distinguish between:
- **Validation errors** (malformed event → should go to DLQ)
- **Transient errors** (ClickHouse down → should retry)

---

## Fix Strategy

### 1. Add Event Validation (BEFORE ClickHouse write)

**File**: `go/internal/events/events.go` or new `validate.go`

```go
// ValidateEvent checks if an event has required fields for its kind
func ValidateEvent(e Event) error {
    if e.Kind == "" {
        return fmt.Errorf("missing event kind")
    }
    if e.EventID == "" {
        return fmt.Errorf("missing event_id")
    }
    if e.TenantID == "" {
        return fmt.Errorf("missing tenant_id")
    }
    
    switch e.Kind {
    case EventKindGatewayRequest:
        if e.GatewayRequest == nil {
            return fmt.Errorf("gateway_request event has nil GatewayRequest payload")
        }
        // Validate required fields
        if e.GatewayRequest.RequestID == "" {
            return fmt.Errorf("gateway_request missing request_id")
        }
        // Add other critical field checks
        
    case EventKindToolCall:
        if e.ToolCall == nil {
            return fmt.Errorf("tool_call event has nil ToolCall payload")
        }
        
    case EventKindUserActivity:
        if e.UserActivity == nil {
            return fmt.Errorf("user_activity event has nil UserActivity payload")
        }
    }
    
    return nil
}
```

### 2. Distinguish Error Types

**File**: `go/internal/events/errors.go` (new)

```go
package events

import "errors"

// ErrMalformedEvent indicates the event failed validation (poison → DLQ)
var ErrMalformedEvent = errors.New("malformed event")

// ErrTransientFailure indicates a temporary issue (ClickHouse down → retry)
var ErrTransientFailure = errors.New("transient failure")

// IsMalformedEvent checks if an error is due to malformed event
func IsMalformedEvent(err error) bool {
    return errors.Is(err, ErrMalformedEvent)
}

// IsTransientFailure checks if error is transient (should retry)
func IsTransientFailure(err error) bool {
    return errors.Is(err, ErrTransientFailure)
}
```

### 3. Update ClickHouseSink.Emit

**File**: `go/internal/events/clickhouse.go:456`

```go
func (c *ClickHouseSink) Emit(ctx context.Context, event Event) error {
    // Validate BEFORE attempting write
    if err := ValidateEvent(event); err != nil {
        return fmt.Errorf("%w: %v", ErrMalformedEvent, err)
    }
    
    switch event.Kind {
    case EventKindGatewayRequest:
        err := c.writeGatewayRequest(ctx, event.TenantID, event.Timestamp, event.EventID, event.GatewayRequest)
        if err != nil {
            // Check if it's a CH connection error (transient)
            if isClickHouseConnectionError(err) {
                return fmt.Errorf("%w: %v", ErrTransientFailure, err)
            }
            return err
        }
        return nil
    // ...
    }
}

func isClickHouseConnectionError(err error) bool {
    // Check for connection refused, timeout, etc.
    errStr := err.Error()
    return strings.Contains(errStr, "connection refused") ||
           strings.Contains(errStr, "timeout") ||
           strings.Contains(errStr, "dial tcp")
}
```

### 4. Fix Consumer RecoverPending Logic

**File**: `go/cmd/event-writer/main.go:169` (RecoverPending method)

**Current**:
```go
// Write to ClickHouse with retry
var lastErr error
success := false
for attempt := 1; attempt <= maxRetries; attempt++ {
    if err := c.sink.Emit(ctx, event); err != nil {
        lastErr = err
        // Retries regardless of error type
    } else {
        success = true
        break
    }
}

if !success {
    // Leaves message pending → BLOCKS BATCH
    continue
}
```

**Fixed**:
```go
// Validate and write event
var lastErr error
success := false

for attempt := 1; attempt <= maxRetries; attempt++ {
    if err := c.sink.Emit(ctx, event); err != nil {
        // Check if malformed (validation error)
        if events.IsMalformedEvent(err) {
            // Don't retry validation errors
            c.logger.Warn("poison event during recovery", "msg_id", msg.ID, "event_id", event.EventID, "error", err)
            lastErr = err
            break  // Stop retrying
        }
        
        // Transient error → retry
        lastErr = err
        if attempt < maxRetries {
            time.Sleep(time.Second * time.Duration(attempt))
        }
    } else {
        success = true
        break
    }
}

// Handle result
if success {
    // ACK after successful write
    if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
        c.logger.Error("failed to ACK recovered event", "error", err, "msg_id", msg.ID)
        totalFailed++
    } else {
        totalRecovered++
    }
} else if events.IsMalformedEvent(lastErr) {
    // Move poison to DLQ
    if err := c.moveToDLQ(ctx, msg.ID, fmt.Sprintf("validation_failed: %v", lastErr)); err != nil {
        c.logger.Error("failed to move poison event to DLQ", "error", err, "msg_id", msg.ID)
        totalFailed++
        continue  // Leave pending for manual intervention
    }
    // ACK poison event after DLQ write
    if err := c.ackMessages(ctx, []string{msg.ID}); err != nil {
        c.logger.Error("failed to ACK poison event", "error", err, "msg_id", msg.ID)
        totalFailed++
    } else {
        totalPoison++
    }
} else {
    // Transient failure after retries → leave pending for next recovery
    c.logger.Error("recovered event write failed after retries (transient)", "error", lastErr, "msg_id", msg.ID)
    totalFailed++
    // Don't ACK - will retry in next RecoverPending call
}
```

### 5. Same Fix for Main Consumer Loop

**File**: `go/cmd/event-writer/main.go:449` (Run method)

Apply same validation + error distinction logic in the main event processing loop.

---

## Regression Test

**File**: `go/internal/events/validation_test.go` (new)

```go
func TestPoisonEventDoesNotBlockValid(t *testing.T) {
    // Setup: Redis + ClickHouse (integration test)
    
    // Send 3 events in order:
    // 1. Valid event A
    // 2. Malformed event (missing GatewayRequest payload)
    // 3. Valid event B
    
    // Wait for processing
    time.Sleep(5 * time.Second)
    
    // Verify:
    // - ClickHouse has 2 events (A and B)
    // - DLQ has 1 event (malformed)
    // - Redis pending = 0
    // - Redis lag = 0
}
```

---

## Implementation Steps

1. Create `go/internal/events/errors.go`
2. Create `go/internal/events/validate.go` with `ValidateEvent()`
3. Update `go/internal/events/clickhouse.go` Emit() to validate first
4. Update `go/cmd/event-writer/main.go` RecoverPending() with error distinction
5. Update `go/cmd/event-writer/main.go` Run() main loop with same logic
6. Add regression test `go/internal/events/validation_test.go`
7. Run test: send valid→poison→valid, verify 2 delivered + 1 DLQ
8. Update `go/TEST_INDEX.md` with new test

---

## Testing Checklist

- [ ] Unit test: ValidateEvent() catches nil payloads
- [ ] Integration test: Poison event goes to DLQ
- [ ] Integration test: Valid events after poison are processed
- [ ] Integration test: Transient CH failure keeps pending (no DLQ)
- [ ] E2E test via test-runner: 100 valid events = 100% delivery
- [ ] E2E test: 1 poison + 99 valid = 99 delivered + 1 DLQ

---

## Estimated Time

- Implementation: 1-2 hours
- Testing: 1 hour
- Documentation: 30 minutes

**Total: 2.5-3.5 hours**

---

## Current Status

- [x] Problem diagnosed (poison blocks batch)
- [x] Root cause identified (no validation before write)
- [x] Fix strategy designed
- [ ] Implementation (next step)
- [ ] Testing
- [ ] Documentation update

**Files to modify:**
1. `go/internal/events/errors.go` (new)
2. `go/internal/events/validate.go` (new)
3. `go/internal/events/clickhouse.go` (modify Emit)
4. `go/cmd/event-writer/main.go` (modify RecoverPending + Run)
5. `go/internal/events/validation_test.go` (new)
6. `go/TEST_INDEX.md` (update)
