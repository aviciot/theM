# Events B1-B3 Reliability Review Handover
# Branch: reliability/events-b1-b3-fixes
# Date: 2026-10-20
# Status: Finding 1 implemented (unverified), Findings 2-7 open

---

## Context

Performing focused reliability review of observability events pipeline (Stages B1-B3) per user request. Working on separate branch `reliability/events-b1-b3-fixes` from commit 250c172b.

Goal: Fix confirmed issues with minimal changes. Do NOT proceed to Stage B4. Do NOT mark production-ready based solely on successful event insertion.

---

## Finding 1: Dedicated Redis ✅ IMPLEMENTED (UNVERIFIED)

**Commit**: 53de7688

**Problem**: All producers (bridge/auth/mcp) and event-writer used them-redis, whose `allkeys-lru` policy can evict the event stream or application keys.

**Fix**:
- Added `REDIS_EVENTS_*` config fields to all three services
- Each producer creates dedicated `redisEvents` client
- Startup fails fast if events mode (clickhouse/dual) requires Redis events but `REDIS_EVENTS_HOST` unset
- Dev overlay passes `REDIS_EVENTS_HOST/PORT/PASSWORD=${THE_M_EVENTS_REDIS_PASSWORD}` to all services
- Documented stream retention policy: ACK preserves entries, XPENDING check required before trim

**Files Changed**:
- `go/internal/config/config.go` (bridge config)
- `go/internal/authserver/config.go` (auth config)
- `go/internal/mcp/config.go` (mcp config)
- `go/cmd/them/main.go` (bridge wiring)
- `go/cmd/auth-server/main.go` (auth wiring)
- `go/cmd/mcp-service/main.go` (mcp wiring)
- `docker-compose.dev.yml` (env vars)
- `redis/config/redis-events.conf` (retention docs)

**Verification Needed**:
1. Compile all three services
2. Verify redisEvents client creation
3. Check startup validation (fail if REDIS_EVENTS_HOST unset)
4. Verify event-writer connects to same Redis instance
5. Test credentials flow
6. Check shutdown ordering (drain events before closing Redis client)

**Known Gaps**:
- Retention documented but NOT implemented (no XTRIM logic)
- Event-writer still needs update to use them-redis-events

---

## Finding 2: Consumer Failure and Recovery (OPEN - HIGH PRIORITY)

**Current Issues**:
- Consumer blocks forever on first poison event (observed in B3 testing with invalid UUID)
- No bounded batch processing on ClickHouse failure
- No retry with backoff
- No XAUTOCLAIM for pending recovery across restarts
- No ACK failure retention

**Required**:
1. Bounded batch processing — stop ingesting if ClickHouse unavailable
2. Retry with exponential backoff (preserve event IDs + timestamps)
3. XAUTOCLAIM on startup to recover pending messages from dead consumers
4. Poison event handling:
   - Explicit durable disposition (DLQ or dead-letter stream)
   - ACK only after disposition succeeds
   - Logging alone insufficient
5. ACK failure retention — if ACK fails, keep message pending

**Files to Modify**:
- `cmd/event-writer/main.go` (consumer loop)
- Likely need new `internal/events/consumer.go` package

---

## Finding 3: Producer Delivery Guarantees (OPEN - HIGH PRIORITY)

**Current Issues**:
- `RedisSink.writeBatch` discards failed batches without retry
- No accurate drop/failure counters
- `Close()` doesn't drain local batch or queued events
- No deadline honor in Close
- Emit accepted after Close

**Required**:
1. Bounded retries in `writeBatch` (e.g., 3 attempts with backoff)
2. Observable counters:
   - `events_dropped_total` (buffer full)
   - `events_failed_total` (Redis write failed after retries)
3. Graceful Close:
   - Drain local batch
   - Drain channel (`buffer`)
   - Honor deadline
   - Set closed flag, reject subsequent Emit calls
4. Shutdown ordering: drain sinks after HTTP requests finish, before Redis clients close

**Files to Modify**:
- `go/internal/events/redis.go` (RedisSink)
- `go/cmd/them/main.go` (shutdown order)
- `go/cmd/auth-server/main.go` (shutdown order)
- `go/cmd/mcp-service/main.go` (shutdown order)

---

## Finding 4: Gateway Mode Validation (OPEN - MEDIUM PRIORITY)

**Required**:
- Validate `THE_M_EVENTS_GATEWAY` at startup (fail on unknown values)
- Keep modes limited to `postgres` and `dual` (not `clickhouse` alone) until accounting/readers support cutover
- Preserve Postgres as authority in dual mode — mirror failure must NOT remove PG record
- Use bounded persistence context detached from client cancellation

**Files to Check**:
- `go/internal/llmgateway/dal.go` (WriteRequest defer)
- `go/cmd/them/main.go` (mode validation)

---

## Finding 5: Auth Event Identity (OPEN - MEDIUM PRIORITY)

**Issues**:
- Login/refresh emit empty `TenantID`
- Logout doesn't distinguish verified from invalid-token attempts

**Required**:
- Populate TenantID from verified login/refresh results
- Logout: only attribute successful action to verified JWT sub
- Define handling when tenant cannot be resolved

**Files to Modify**:
- `go/internal/authserver/handlers.go` (emitUserActivity calls)

---

## Finding 6: MCP Status + Gateway Race (OPEN - MEDIUM PRIORITY)

**Issues**:
- MCP executor doesn't check `result.IsError` before marking tool call as "ok"
- `llmgateway.Service.Stream` goroutine mutates `callRes` while returning it (race)

**Files to Modify**:
- `go/internal/mcp/executor.go` (check IsError)
- `go/internal/llmgateway/service.go` (Stream method race)

---

## Finding 7: Verification Tests (REQUIRED BEFORE MERGE)

**Test Coverage Needed**:
1. Real Redis → event-writer → ClickHouse integration (actual schema + permissions)
2. Dual-write correlation (same request_id in Postgres + ClickHouse)
3. PG preservation when mirror fails
4. Tenant isolation (events don't leak across tenants)
5. Redis outage (producer behavior)
6. ClickHouse outage (consumer behavior)
7. ACK failure
8. Poison event (invalid UUID, malformed JSON, unsupported kind)
9. Consumer restart with pending entries (XAUTOCLAIM recovery)
10. SIGTERM with buffered events
11. MCP tool call with `IsError=true`
12. `go test -race` for affected packages

---

## Delivery Guarantees Statement

Events remain **best-effort** until Findings 2-3 complete:
- Producer drops on buffer-full (counted but not retried)
- Consumer blocks on first bad event (no DLQ)
- No pending-message recovery across restarts
- Graceful shutdown may lose buffered events
- No ClickHouse retry with backoff

---

## Rollback Instructions

### Full rollback to main:
```bash
cd /opt/docker/them
git checkout main
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml restart them-go-bridge them-auth-go them-mcp-service
```

### Adopt Finding 1 only (dedicated Redis):
```bash
cd /opt/docker/them
git checkout reliability/events-b1-b3-fixes

# Ensure secrets exist
grep THE_M_EVENTS_REDIS_PASSWORD .env || echo "Run scripts/events-env.sh first"

# Start them-redis-events
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-redis-events

# Rebuild and restart producers
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  build them-go-bridge them-auth-go them-mcp-service

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-go-bridge them-auth-go them-mcp-service

# Rebuild and restart event-writer
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events build them-event-writer

docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events restart them-event-writer
```

---

## Next Session Priority

**Focus: Findings 2 and 3 only**

1. Verify Finding 1 (compile, startup validation, credentials)
2. Implement Finding 2 (consumer reliability)
3. Implement Finding 3 (producer reliability)
4. Run failure tests
5. Report exact results + remaining limitations
6. Commit focused change
7. Keep Findings 4-6 explicitly open

Do NOT proceed to Stage B4. Do NOT mark production-ready.

---

## Notes

- Poison event blocking observed during B3 test: event `test-b3-002` with invalid UUID blocked consumer until manually ACKed
- Event-writer had to be restarted after deleting bad events
- Current event-writer location: `cmd/event-writer/main.go` (separate binary, not in main services)
- Current HEAD: 53de7688 on branch `reliability/events-b1-b3-fixes`
- Parent branch: `main` at 250c172b

---

## Task Status

- [x] Finding 1: Dedicated Redis
- [ ] Finding 2: Consumer failure and recovery (HIGH)
- [ ] Finding 3: Producer delivery guarantees (HIGH)
- [ ] Finding 4: Gateway mode validation (MEDIUM)
- [ ] Finding 5: Auth event identity (MEDIUM)
- [ ] Finding 6: MCP + gateway races (MEDIUM)
- [ ] Finding 7: Verification tests (REQUIRED)
