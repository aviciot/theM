# Events B1-B3 Reliability Review - Summary Report
# Branch: reliability/events-b1-b3-fixes
# Date: 2026-10-20
# Final Status: Findings 1-3 IMPLEMENTED (compile-verified, runtime tests pending)

---

## Executive Summary

Implemented critical reliability fixes for the observability events pipeline (Stages B1-B3). Focus: **dedicated Redis + consumer/producer reliability**. Findings 4-6 remain open per user directive.

**Commits**:
- `53de7688` Finding 1: Wire producers to dedicated them-redis-events
- `7344cc85` Fix: event-writer Redis endpoint correction
- `93abe1b1` Findings 2+3: Consumer + producer reliability

**Delivery Guarantees**: Events remain **best-effort** but now have:
- ✅ Dedicated Redis (no eviction risk)
- ✅ Poison event DLQ (no queue blocking)
- ✅ ClickHouse retry with backoff
- ✅ Pending recovery (XAUTOCLAIM)
- ✅ Observable counters
- ✅ Graceful shutdown with drain

---

## Finding 1: Dedicated Redis ✅ IMPLEMENTED + VERIFIED

**Problem**: Producers and event-writer used them-redis (`allkeys-lru`), risking event stream eviction.

**Solution**:
- All three producers (bridge/auth/mcp) create dedicated `redisEvents` client
- Event-writer connects to `them-redis-events:6379` (not `them-redis`)
- Startup fails fast if events mode requires Redis but `REDIS_EVENTS_HOST` unset
- Dev overlay passes `REDIS_EVENTS_HOST/PORT/PASSWORD` to all services
- Redis events config: `noeviction` + AOF + documented retention policy

**Verification**: ✅
- All services compile successfully
- Startup validation present (fail-fast if config missing)
- Event-writer correctly points to them-redis-events

**Files Modified**:
- `go/internal/config/config.go` (bridge config + RedisEventsAddr helper)
- `go/internal/authserver/config.go` (auth config + RedisEventsAddr helper)
- `go/internal/mcp/config.go` (mcp config + RedisEventsAddr helper)
- `go/cmd/them/main.go` (create redisEvents client, wire to gwEventSink)
- `go/cmd/auth-server/main.go` (create redisEventsClient, wire to authEventSink)
- `go/cmd/mcp-service/main.go` (create redisEvents client, wire to toolEventSink)
- `docker-compose.dev.yml` (pass REDIS_EVENTS_* to all three services)
- `docker-compose.events.yml` (event-writer → them-redis-events, correct password var)
- `redis/config/redis-events.conf` (retention policy documentation)

**Remaining Gaps**:
- Retention documented but NOT implemented (no XTRIM logic in event-writer)
- No monitoring/alerting on Redis events memory usage

---

## Finding 2: Consumer Failure and Recovery ✅ IMPLEMENTED

**Problem**:
- Consumer blocked forever on first poison event (observed: invalid UUID)
- No ClickHouse retry
- No pending recovery
- No bounded processing

**Solution** (`go/cmd/event-writer/main.go`):

1. **XAUTOCLAIM on startup** - recovers pending messages from dead consumers (30s idle threshold)
2. **Poison event handling**:
   - Three types detected: missing event field, invalid JSON, missing kind
   - Moved to DLQ stream (`them:events:dlq`) with reason + timestamp
   - ACKed only after successful DLQ write (not just logging)
3. **ClickHouse retry**:
   - Max 3 attempts with exponential backoff (1s base)
   - Preserves event IDs + timestamps
4. **Bounded processing**:
   - Tracks consecutive failures
   - Pauses ingestion if ClickHouse unavailable (3+ failures, max 30s pause)
5. **ACK failure retention**:
   - If ACK fails, message stays pending for recovery
   - Logged but not silently dropped
6. **Observable counters**:
   - `processedCount` - successful writes
   - `failedCount` - exhausted all retries
   - `dlqCount` - poison events

**Verification**: ✅ Compile successful
**Runtime Testing**: Pending (see Test Plan below)

---

## Finding 3: Producer Delivery Guarantees ✅ IMPLEMENTED

**Problem**:
- `RedisSink.writeBatch` discarded failed batches without retry
- No accurate failure counters
- `Close()` didn't drain buffer
- Emit accepted after Close

**Solution** (`go/internal/events/redis.go`):

1. **Bounded retries**:
   - `writeBatchWithRetry`: max 3 attempts, 100ms base backoff
   - Preserves event batch across retries
2. **Observable counters** (atomic for concurrency):
   - `droppedCtr` - buffer full (best-effort drop)
   - `failedCtr` - Redis write failed after all retries
   - Exposed via `DroppedCount()` and `FailedCount()`
3. **Graceful Close**:
   - Sets `closed` flag (atomic bool)
   - Drains remaining buffered events (channel + local batch)
   - Honors deadline from context
   - Rejects Emit calls after Close
4. **Shutdown behavior**:
   - On context cancel, drains buffer before exit
   - Flushes final batch
   - Returns error if deadline exceeded during Close

**Verification**: ✅ Compile successful
**Runtime Testing**: Pending (see Test Plan below)

**Shutdown Ordering** (not yet implemented):
- Producers should close sinks after HTTP drain, before Redis clients close
- Currently: sinks close via defer, but order not guaranteed
- Required: explicit shutdown sequence in main.go files

---

## Findings 4-6: OPEN (Per User Directive)

**Finding 4**: Gateway mode validation + dual-write safety
**Finding 5**: Auth event identity (TenantID population, verified logout)
**Finding 6**: MCP status check (IsError) + gateway streaming race

User explicitly requested focus on Findings 2-3 only. These remain high priority for next session.

---

## Runtime Test Plan (REQUIRED BEFORE MERGE)

### Integration Tests

1. **Redis → ClickHouse**:
   - Emit 100 events across all three producers
   - Verify all reach ClickHouse with correct schema
   - Check event_id, timestamps preserved

2. **Dual-write correlation**:
   - Send gateway request with THE_M_EVENTS_GATEWAY=dual
   - Verify same request_id in Postgres + ClickHouse
   - Confirm PG record exists even if ClickHouse write fails

3. **Poison event DLQ**:
   - Inject invalid UUID event
   - Verify moved to `them:events:dlq`
   - Confirm ACK only after DLQ write
   - Verify queue not blocked

4. **ClickHouse retry**:
   - Stop ClickHouse mid-stream
   - Emit events, observe retry logs
   - Restart ClickHouse, verify events arrive
   - Check bounded processing (pause after 3 failures)

5. **Pending recovery**:
   - Start consumer, let it claim 10 messages
   - Kill consumer (SIGKILL) before ACK
   - Start new consumer, verify XAUTOCLAIM recovery

6. **Redis outage**:
   - Stop them-redis-events
   - Emit events, verify producer drops counted
   - Restart Redis, verify failedCtr increments

7. **ACK failure**:
   - Simulate ACK error (Redis readonly mode)
   - Verify message stays pending
   - Restore Redis, verify recovery

8. **Graceful shutdown**:
   - Buffer 1000 events
   - Send SIGTERM
   - Verify all buffered events flushed
   - Check Close deadline honored

9. **MCP IsError**:
   - Call MCP tool that returns error
   - Verify event status != "ok"

10. **Race detection**:
    ```bash
    cd go && go test -race ./internal/events/... ./cmd/event-writer/...
    ```

### Manual Verification Commands

```bash
# Start full stack with events
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  -f docker-compose.events.yml --profile events --profile temporal up -d

# Check all services healthy
docker compose --project-name them_gateway ps

# Watch event-writer logs
docker logs them-event-writer -f

# Check ClickHouse
docker exec them-clickhouse clickhouse-client --query \
  "SELECT count(*), kind FROM them_events.events_gateway_request GROUP BY kind"

# Check DLQ
docker exec them-redis-events redis-cli XLEN them:events:dlq

# Check counters (would need /metrics endpoint or logs)
docker logs them-go-bridge | grep "events_dropped\|events_failed"
```

---

## Remaining Limitations

### Best-Effort Guarantees
- Producer drops on buffer-full (counted but not retried beyond Redis write retries)
- Consumer exhausts retries = events lost (counted in failedCtr)
- No persistent outbox for guaranteed delivery

### Missing Features
- No XTRIM implementation (stream grows unbounded)
- No /metrics endpoint for counters (only internal logs)
- No alerting on Redis events memory usage
- No body capture to ClickHouse (refs remain empty)
- No integration tests yet

### Known Issues
- Shutdown ordering not enforced (sinks may close before HTTP drain complete)
- No tenant isolation tests
- Finding 4-6 fixes not implemented

---

## Rollback Instructions

### Full rollback to main:
```bash
cd /opt/docker/them
git checkout main
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-go-bridge them-auth-go them-mcp-service
```

### Adopt these fixes:
```bash
cd /opt/docker/them
git checkout reliability/events-b1-b3-fixes

# Ensure secrets exist
scripts/events-env.sh  # or generate-env.ps1 on Windows

# Start them-redis-events
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-redis-events them-clickhouse

# Rebuild + restart all affected services
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  build them-go-bridge them-auth-go them-mcp-service

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events build them-event-writer

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-go-bridge them-auth-go them-mcp-service

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events restart them-event-writer
```

---

## Next Session Priorities

1. **Run runtime tests** (see Test Plan above)
2. **Fix any discovered issues**
3. **Implement shutdown ordering** (drain HTTP → close sinks → close Redis clients)
4. **Consider Finding 4** (gateway mode validation) - high priority for production safety
5. **Document exact test results** in this file
6. **Decision**: merge to main OR continue with Findings 4-6

DO NOT proceed to Stage B4 until:
- Runtime tests pass
- Findings 4-6 addressed or explicitly deferred with rationale
- Production readiness criteria met (not just successful insertion)

---

## Files Modified Summary

- `go/internal/config/config.go` - Added REDIS_EVENTS_* fields
- `go/internal/authserver/config.go` - Added REDIS_EVENTS_* fields
- `go/internal/mcp/config.go` - Added REDIS_EVENTS_* fields
- `go/cmd/them/main.go` - Create redisEvents client, fail-fast validation
- `go/cmd/auth-server/main.go` - Create redisEventsClient, fail-fast validation
- `go/cmd/mcp-service/main.go` - Create redisEvents client, fail-fast validation
- `go/cmd/event-writer/main.go` - XAUTOCLAIM, DLQ, retry, bounded processing, counters
- `go/internal/events/redis.go` - Bounded retries, atomic counters, graceful Close
- `docker-compose.dev.yml` - Pass REDIS_EVENTS_* to producers
- `docker-compose.events.yml` - Fix event-writer Redis endpoint + password
- `redis/config/redis-events.conf` - Document retention policy
- `docs/EVENTS_RELIABILITY_HANDOVER.md` - Handover document
- `docs/EVENTS_RELIABILITY_SUMMARY.md` - This file

**Total**: 13 files modified, 3 new docs

---

## Commit Log

```
93abe1b1 fix(events): Findings 2+3 - consumer+producer reliability
7344cc85 fix(events): event-writer must use them-redis-events, not them-redis
e23f4518 docs: add events reliability review handover document
53de7688 fix(events): Finding 1 - wire producers to dedicated them-redis-events
```

HEAD: `93abe1b1` on branch `reliability/events-b1-b3-fixes`
Parent: `main` at `250c172b`
