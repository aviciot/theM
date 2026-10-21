# Resilience Test Actual Results
# Date: 2026-10-21 09:10 UTC
# Status: Partially Executed - JSON Marshaling Issue Found

---

## Test Execution Summary

**Tests Attempted**: 4/4
**Tests Completed Successfully**: 0/4
**Tests Blocked**: 4/4 (same root cause)

**Root Cause**: JSON struct tag mismatch between gateway emission (PascalCase) and event-writer parsing (snake_case then PascalCase)

---

## Executed Test: Writer Restart Mid-Processing

**Run ID**: `4ad4f46b-8af6-4df0-b75a-53261ba59144`
**Timestamp**: 2026-10-21 09:06 UTC

### Test Procedure
1. ✅ Cleared old test data (PG and CH)
2. ✅ Triggered 1000-user test via API (`POST http://10.55.125.43:8093/run`)
3. ✅ Waited 10 seconds
4. ✅ Restarted event-writer (`docker restart them-event-writer`)
5. ✅ Waited 120 seconds for processing
6. ❌ Verification failed

### Results

**Postgres (Gateway)**:
- Count: 1000 events
- Status: ✅ All requests written

**ClickHouse (Events)**:
- Count: 0 events
- Status: ❌ No delivery

**Redis**:
- Lag: 0
- Pending: 0
- DLQ: 2001 events

**Event-Writer Logs**:
```
{"level":"INFO","msg":"consumer stopped","processed":2,"failed":0,"dlq":2001}
```

### Root Cause Analysis

**Problem**: All 1000 test events + some prior events went to DLQ with error:
```
validation_failed: malformed event: missing event_id
```

**Investigation**:

1. Checked actual event in Redis stream:
```json
{
  "EventID":"d8c956ae-a2fe-46a5-898b-0f1cfb3990a4",
  "SchemaVersion":1,
  "Timestamp":"2026-10-21T09:06:09.479187322Z",
  "TenantID":"ad7d567d-b222-40be-acb6-7929cf6ae168",
  "Kind":"gateway_request",
  "GatewayRequest":{
    "ApplicationID":"",
    "ClientID":"00000000-0000-0000-0000-000000000099",
    "UserID":"",
    "RequestID":"fe4a1de5-16e3-45e9-a354-859e1eaca8cb",
    "Provider":"mock",
    "ModelServed":"mock-model-v1",
    ...
  }
}
```

2. **Gateway emits PascalCase** (`EventID`, `GatewayRequest`)
3. **Event struct had snake_case tags** (`event_id`, `gateway_request`) — added in commit a47a3554
4. **Unmarshal fails** because field names don't match
5. **Validation fails** because EventID field is empty after unmarshal
6. **All events → DLQ**

### Fix Applied

Changed struct tags in `go/internal/events/events.go` from snake_case to PascalCase:
- `json:"event_id"` → `json:"EventID"`
- `json:"gateway_request"` → `json:"GatewayRequest"`
- All GatewayRequestEvent fields similarly updated

**Build Status**: ❌ Failed due to vendor directory inconsistency
```
go: inconsistent vendoring in /build:
	github.com/redis/rueidis@v1.0.79: is explicitly required in go.mod
	but not marked as explicit in vendor/modules.txt
```

**Next Step**: Run `go mod vendor` and rebuild

---

## Test Blockers

### Blocker 1: JSON Struct Tags (IDENTIFIED)
- **Impact**: All 4 tests blocked
- **Status**: Fix applied, rebuild pending
- **Resolution**: Update vendor directory + rebuild event-writer

### Blocker 2: Go Test Environment (WORKAROUND ATTEMPTED)
- **Issue**: Retention tests require Go 1.25
- **Attempted**: `docker run golang:1.25 go test`
- **Result**: Vendor directory mismatch
- **Resolution**: Need `go mod vendor` first

---

## What Was Actually Tested

### API Integration ✅
- Test runner backend endpoint: `http://10.55.125.43:8093/run`
- JSON payload format: `{"scenario_id":"50-event-delivery-test"}`
- Response format: `{"run_id":"..."}`
- Status: **Working**

### Test Runner Execution ✅
- Scenario: 50-event-delivery-test
- n_users: 1000 (updated from 100)
- Run ID: 4ad4f46b-8af6-4df0-b75a-53261ba59144
- Status: **Executed successfully**

### Gateway Event Emission ✅
- 1000 events written to Postgres
- 1000 events emitted to Redis Streams
- Format: PascalCase JSON
- Status: **Working**

### Event-Writer Consumption ✅
- Events read from Redis Streams
- Validation executed
- DLQ write executed
- Status: **Working** (but validating incorrectly due to unmarshal failure)

### Poison Event Isolation ✅ (Unintentional Test)
- 1000 "poison" events (unmarshal failures)
- All moved to DLQ without blocking
- No pending messages
- Status: **Poison isolation working as designed**

---

## Tests NOT Executed

❌ Test 1: Writer restart (blocked by JSON issue)
❌ Test 2: Redis restart (not attempted)
❌ Test 3: ClickHouse outage (not attempted)
❌ Test 4: Load test (blocked by JSON issue)
❌ Retention unit tests (vendor + Go environment)
❌ Request ID reconciliation (no events in CH)

---

## Code Review vs Executed Tests

### Previously Claimed "Passing"

**Claim**: "9/9 automated tests passing"

**Reality**:
1. ✅ Poison event isolation regression test (`scripts/test_poison_event_isolation.sh`) - **EXECUTED** and passed
2. ⚠️ DLQ write failure handling - **CODE REVIEW ONLY**, not executed
3. ⚠️ Retention safety - **CODE WRITTEN**, not executed
4. ❌ Writer restart - **NOT EXECUTED** (JSON issue)
5. ❌ Redis restart - **NOT EXECUTED**
6. ❌ ClickHouse outage - **NOT EXECUTED**
7. ❌ Load test - **NOT EXECUTED** (JSON issue)

**Corrected Count**:
- **Executed and passing**: 1 (poison isolation)
- **Code review only**: 1 (DLQ failure handling)
- **Code written, not executed**: 3 (retention tests)
- **Infrastructure ready, not executed**: 4 (resilience tests)

---

## Required Actions

### Immediate (Unblock Tests)

1. **Fix vendor directory**:
```bash
cd /opt/docker/them/go
docker run --rm -v $(pwd):/workspace -w /workspace golang:1.25 go mod vendor
```

2. **Rebuild event-writer**:
```bash
docker compose --project-name them_gateway -f docker-compose.yml \
  -f docker-compose.dev.yml -f docker-compose.events.yml \
  --profile events build them-event-writer
```

3. **Restart event-writer**:
```bash
docker compose --project-name them_gateway -f docker-compose.yml \
  -f docker-compose.dev.yml -f docker-compose.events.yml \
  --profile events up -d them-event-writer
```

4. **Re-run resilience tests**:
```bash
/opt/docker/them/scripts/execute_all_resilience_tests.sh
```

### Short-Term (Complete Test Coverage)

5. **Run retention tests**:
```bash
cd /opt/docker/them/go
docker run --rm -v $(pwd):/workspace -w /workspace golang:1.25 go test ./internal/events/retention_test.go -v
```

6. **Verify request ID reconciliation**:
- After tests pass, manually verify 10-20 random request IDs between PG and CH

---

## Findings

### Positive
1. ✅ Test infrastructure works (API, runner, verification logic)
2. ✅ Poison isolation works (unintentionally tested with 1000 events)
3. ✅ Gateway → Redis emission works
4. ✅ Event-writer → DLQ path works

### Issues Found
1. ❌ JSON struct tags were wrong (snake_case instead of PascalCase)
2. ❌ No integration test caught this before production-scale testing
3. ❌ Vendor directory management needed for new test dependencies

### Lessons
1. **Always test with real production data flow** before claiming tests pass
2. **Code review ≠ executed test**
3. **Wire format must match struct tags exactly**

---

## Files Modified

- `go/internal/events/events.go` - Changed JSON tags from snake_case to PascalCase
- `scripts/execute_all_resilience_tests.sh` - Fixed API endpoint (8093) and JSON field names

---

## Current State

**HEAD**: (pending commit)

**Event System Status**:
- Gateway emission: ✅ Working
- Redis Streams: ✅ Working
- Event-writer: ⚠️ Needs rebuild with PascalCase tags
- ClickHouse delivery: ❌ Blocked by JSON unmarshal issue
- DLQ: ✅ Working (2001 events, all "missing event_id")

**Test Execution**: 1/9 actually executed and passing

**Next**: Fix vendor + rebuild + re-run all 4 resilience tests
