# Event Delivery — Required Next Steps
# Date: 2026-10-21 07:30 UTC
# Status: Permissions fixed, delivery unproven, test infrastructure incomplete

---

## What Must Be Done (In Order)

### 1. Use Real Gateway Requests (BLOCKING)

**Problem:** Synthetic test generates invalid events (schema mismatch)
**Solution:** Use existing Performance Test Runner or real gateway calls

**Steps:**
```bash
# Option A: Use existing app with gateway entry point
# Check for existing gateway-enabled apps:
docker exec them-postgres psql -U them -d them -c "
  SELECT a.id, a.slug, ep.slug as entry_point, ep.entry_point_type
  FROM applications a
  JOIN entry_points ep ON ep.application_id = a.id
  WHERE ep.entry_point_type IN ('rest', 'llm_gateway')
  LIMIT 5;
"

# Option B: Create test app with LLM gateway entry point
# (Requires admin JWT - use scripts/tests/run_tests.py auto-fetch pattern)

# Option C: Send direct gateway requests (simplest)
# Find an existing app with gateway access
docker exec them-postgres psql -U them -d them -c "
  SELECT DISTINCT application_slug, COUNT(*)
  FROM gateway_requests
  WHERE created_at > NOW() - INTERVAL '1 day'
  GROUP BY application_slug
  ORDER BY COUNT(*) DESC
  LIMIT 3;
"
# Use one of these app slugs
```

**Expected real event structure** (from production):
```json
{
  "kind": "gateway_request",
  "event_id": "<uuid>",
  "timestamp": "2026-10-21T07:00:00Z",
  "tenant_id": "<uuid>",
  "application_id": "<uuid>",
  "client_id": "<uuid>",
  "user_id": "",
  "request_id": "<uuid>",
  "trace_id": "",
  "provider": "anthropic",
  "model_requested": "claude-sonnet-4-5",
  "model_served": "claude-sonnet-4-5",
  "status": "ok",
  "http_status": 200,
  "error_code": "",
  "tokens_in": 100,
  "tokens_out": 200,
  "cost_usd": "0.00150000",
  "latency_ms": 1500,
  "ttfb_ms": 250
}
```

### 2. Prove 100% Delivery (BLOCKING)

**Test:**
1. Clear existing test data:
   ```bash
   # Mark timestamp for test start
   TEST_START=$(date -u +"%Y-%m-%d %H:%M:%S")
   
   # Send 100 real gateway requests (using real app)
   # (Implementation depends on Step 1 solution)
   
   # Wait for delivery
   sleep 30
   
   # Reconcile
   PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "
     SELECT COUNT(*) FROM gateway_requests 
     WHERE created_at > '$TEST_START';
   ")
   
   CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "
     SELECT COUNT(*) FROM events_gateway_request 
     WHERE ts > '$TEST_START';
   ")
   
   echo "PG: $PG_COUNT, CH: $CH_COUNT"
   ```

2. Verify field integrity (10 random samples):
   ```bash
   # Compare request_id, provider, model, tokens between PG and CH
   docker exec them-postgres psql -U them -d them -c "
     SELECT id, provider, model_served, tokens_in, tokens_out, cost_usd
     FROM gateway_requests
     WHERE created_at > '$TEST_START'
     ORDER BY RANDOM()
     LIMIT 10;
   "
   
   # Match against CH (by request_id)
   ```

**Acceptance:** 100/100 delivered, all fields match

### 3. Fix Poison Event Handling (BLOCKING)

**Problem:** Malformed events block processing (writer crashes)
**Current behavior:** Event-writer crashes on "nil gateway request event"
**Expected behavior:** Malformed events → DLQ, valid events continue processing

**Investigation:**
```bash
# Check if writer is actually crashed or just health endpoint down
docker ps --filter name=them-event-writer --format "{{.Status}}"

# Check process status inside container
docker exec them-event-writer ps aux

# Check full logs for crash/panic
docker logs them-event-writer --since 10m | grep -E "panic\|fatal\|crash"
```

**Fix (if writer crashes):**
- Add error handling in `go/internal/events/consumer.go`
- Ensure poison events move to DLQ without blocking batch
- Add test: malformed event + 10 valid events → 10 delivered, 1 in DLQ

### 4. Add Regression Tests (BLOCKING)

**Required tests in `go/internal/events/retention_test.go`:**

```go
func TestRetention_RespectsPendingMessages(t *testing.T) {
    // 1. Generate 100 events
    // 2. Stop event-writer (messages become pending)
    // 3. Wait 24h + 15min (trigger retention)
    // 4. Verify stream NOT trimmed (pending protected)
}

func TestRetention_RespectsUnreadMessages(t *testing.T) {
    // 1. Generate 100 events
    // 2. Don't start consumer (messages unread)
    // 3. Wait 24h + 15min
    // 4. Verify stream NOT trimmed (unread protected)
}

func TestRetention_OnlyTrimsDelivered(t *testing.T) {
    // 1. Generate 100 events
    // 2. Deliver 50 (ACK 50)
    // 3. Wait 24h + 15min
    // 4. Verify only 50 trimmed (delivered portion only)
}
```

### 5. Test Restart Resilience (HIGH PRIORITY)

**Test writer restart:**
```bash
# Send 50 events
# Restart writer mid-processing
# Send 50 more events
# Verify all 100 delivered (no loss, no duplicates)
```

**Test Redis restart:**
```bash
# Send 100 events
# Restart Redis (AOF recovery)
# Restart writer
# Verify all 100 delivered
```

### 6. Test ClickHouse Outage (MEDIUM PRIORITY)

**Test:**
```bash
# Stop ClickHouse
docker stop them-clickhouse

# Send 100 events
# Verify writer pauses (doesn't drop)

# Check Redis backlog
docker exec them-redis-events redis-cli XLEN them:events
# Should be 100

# Start ClickHouse
docker start them-clickhouse

# Wait for delivery
sleep 60

# Verify all 100 delivered
```

**Acceptance:** Writer retries, no events lost during outage

---

## Critical Fixes Applied (Already Committed)

### ✅ Redis Permissions
- **Fixed:** `user: "999:1006"` in docker-compose.events.yml
- **Verified:** RDB saves work
- **Verified:** AOF persists (stream survives restart 100→100)

### ✅ Historical Gap Explained
- **Root cause:** Events system didn't exist before Oct 20 10:08 UTC
- **Evidence:** Git history shows DUAL mode enabled in commit 3947b27e (Oct 20 10:39)
- **Conclusion:** 11,272 "missing" events are from before DUAL mode existed
- **No backfill needed:** These records were never emitted (PG-only writes)

---

## Current Blockers

### ❌ Event Schema Mismatch
- Synthetic test events don't match production format
- Writer rejects with "nil gateway request event"
- Need to use real gateway requests instead

### ❌ Health Endpoint Unavailable
- `wget: can't connect to remote host: Connection refused`
- Unknown if writer crashed or just health server down
- Need to check process status

### ❌ No Delivery Proof
- 0/100 synthetic events delivered
- Need 100/100 real gateway events delivered
- Need field-by-field reconciliation

---

## Files Modified (Committed: b598335a)

```
M docker-compose.events.yml                    # user: "999:1006", permissions fixed
M scripts/test_event_delivery_comprehensive.sh # RFC3339 timestamps
A docs/EVENT_DELIVERY_STATUS_2026-10-21.md    # Status report
A docs/EVENT_DELIVERY_GAPS_AND_PLAN.md        # Gap analysis
A docs/EVENT_DELIVERY_FIX_2026-10-21.md       # Initial fix report (incomplete)
A scripts/backfill_gateway_events.sql         # Backfill query (not needed, historical gap explained)
```

---

## Estimated Time

| Task | Time | Blocker? |
|---|---|---|
| Use real gateway requests | 30min | YES |
| Prove 100% delivery | 20min | YES |
| Fix poison handling | 1-2h | YES |
| Add regression tests | 2-3h | YES |
| Test restart resilience | 30min | HIGH |
| Test CH outage | 30min | MEDIUM |

**Total:** 5-7 hours to production-ready

---

## Success Criteria

- [ ] 100/100 real gateway requests delivered to ClickHouse
- [ ] All request IDs match between PG and CH
- [ ] All key fields match (10 random sample verification)
- [ ] Poison events move to DLQ without blocking valid events
- [ ] Retention tests added (pending, unread, partial delivery)
- [ ] Writer survives restart (no event loss)
- [ ] Redis survives restart (AOF recovery)
- [ ] CH outage handled gracefully (pause + retry, no loss)

---

## Reference

- Performance Test Runner: `scripts/tests/run_tests.py` (uses real apps)
- Gateway test scripts: `test-trace.sh`, `test-uuid.sh`, `governance_smoke_simple.sh`
- Event reconciliation: `scripts/reconcile_events.sh`
- Current status: `docs/EVENT_DELIVERY_STATUS_2026-10-21.md`

---

**Next immediate action:** Use real gateway requests from existing app instead of synthetic test events. Identify why event-writer rejects valid events.
