# Handover: Events Reliability Fixes + Testing
## For Fresh Session

**Branch**: `reliability/events-b1-b3-fixes`  
**HEAD**: `51123a98`  
**Status**: Implementation complete but **TWO CRITICAL BUGS** found during verification  
**Next**: Fix bugs, then execute tests

---

## Context Summary

Implemented Findings 1-3 (dedicated Redis, consumer reliability, producer reliability). During verification phase, discovered two correctness bugs that BLOCK testing. Fresh session should fix both issues, then execute the prepared test plan.

---

## Critical Bug #1: Shutdown Ordering Wrong

### Current Broken Behavior
```
1. preDrainHook() → closes event sinks  ← BUG: happens BEFORE HTTP drain
2. httpServer.Shutdown() → drains HTTP
3. closers.Close() → closes Redis
```

**Problem**: In-flight HTTP handlers can `Emit()` to closed sink → events lost

### Root Cause
- `WithPreDrainHook()` accepts only ONE hook
- Second call at line 518 REPLACES first call (runCancel)
- Both goroutine cleanup AND event sink closing are broken

### Required Fix

**Files to modify**:
- `go/cmd/them/main.go:518-527`
- `go/cmd/auth-server/main.go:150-163`  
- `go/cmd/mcp-service/main.go:133-151`

**Solution**:

1. Remove the `srv.WithPreDrainHook(event sink closer)` calls
2. Create adapter implementing `server.Closer` interface:

```go
// Near end of main.go, before the helper types
type eventSinkCloser struct {
    sink events.EventSink
    logger *slog.Logger
}

func (e *eventSinkCloser) Close() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := e.sink.Close(ctx); err != nil {
        e.logger.Error("failed to drain event sink", "error", err)
    } else {
        e.logger.Info("event sink drained")
    }
}
```

3. **For Go bridge** (`cmd/them/main.go`):
   - Server created at line 200 with closers: `database, redisCache`
   - Need to pass event sink closer BETWEEN these
   - Problem: server created BEFORE event sink exists (line 514)
   - Solution: Create server with `database` only, add closers later:
   
   ```go
   // At line 200:
   srv := server.NewWithBus(addr, healthHandler, authMW, bus, drainDuration, log, database)
   
   // Later, after event sink created (around line 530):
   if gwEventSink != nil {
       srv.AddCloser(&eventSinkCloser{sink: gwEventSink, logger: log})
   }
   srv.AddCloser(redisCache)
   srv.AddCloser(redisEvents) // if not nil
   ```
   
   **BUT**: `server.Server` doesn't have `AddCloser()` method. Need to add it to `internal/server/server.go`:
   
   ```go
   // Add this method to Server
   func (s *Server) AddCloser(c Closer) {
       s.closers = append(s.closers, c)
   }
   ```

4. **For auth-server** (`cmd/auth-server/main.go:150-163`):
   - Currently drains sink explicitly before `srv.Shutdown()`
   - Already correct order! Just needs cleanup
   - Keep current code, just improve logging

5. **For MCP service** (`cmd/mcp-service/main.go:133-151`):
   - Same as auth-server
   - Already correct order
   - Keep current code

### Verification
After fix, shutdown sequence should be:
1. Signal received
2. preDrainHook() → runCancel (background goroutines stop)
3. httpServer.Shutdown() → HTTP handlers finish
4. closers[0].Close() → database
5. closers[1].Close() → event sink (drains buffer)
6. closers[2].Close() → redisCache
7. closers[3].Close() → redisEvents (if exists)

---

## Critical Bug #2: Retention Deletes Unread Messages

### Current Broken Behavior
```go
// retention.go:85-120 trimStream()
pendingInfo = XPENDING stream group
if pendingInfo.count > 0:
    skip trim  // has pending
else:
    trim everything older than 24h  // ← BUG: ignores UNREAD
```

**Problem**: Stopped consumer with 48h-old unread messages → **silently deleted**

### Attack Scenario
1. Consumer processes 100 events, then stops (crashes/maintenance)
2. Producer adds 1000 new events (age: 25 hours)
3. Consumer group still exists, zero pending (nothing claimed)
4. Retention runs: `XPENDING` empty → trims all 25h-old messages
5. **Result**: 1000 undelivered messages LOST

### Required Fix

**File**: `go/cmd/event-writer/retention.go:85-120`

**Current code**:
```go
func (r *RetentionManager) trimStream(ctx context.Context, stream, group string) (int, error) {
    cmd := r.redis.B().Xpending().Key(stream).Group(group).Start("-").End("+").Count(1).Build()
    result, err := r.redis.Do(ctx, cmd).ToMessage()
    if err != nil {
        if rueidis.IsRedisNil(err) {
            return r.trimToNow(ctx, stream)  // ← BUG
        }
        return 0, fmt.Errorf("XPENDING failed: %w", err)
    }
    r.logger.Debug("skipping trim: pending messages exist", "stream", stream)
    return 0, nil
}
```

**Fixed code**:
```go
func (r *RetentionManager) trimStream(ctx context.Context, stream, group string) (int, error) {
    // Check 1: Any pending messages?
    pendingCmd := r.redis.B().Xpending().Key(stream).Group(group).Start("-").End("+").Count(1).Build()
    _, err := r.redis.Do(ctx, pendingCmd).ToMessage()
    if err == nil {
        r.logger.Debug("skipping trim: pending messages exist", "stream", stream)
        return 0, nil
    }
    if !rueidis.IsRedisNil(err) {
        return 0, fmt.Errorf("XPENDING failed: %w", err)
    }
    
    // Check 2: Get last-delivered-id from group info
    groupCmd := r.redis.B().XinfoGroups().Key(stream).Build()
    groups, err := r.redis.Do(ctx, groupCmd).AsXInfoGroups()
    if err != nil {
        return 0, fmt.Errorf("XINFO GROUPS failed: %w", err)
    }
    
    // Find our consumer group
    var lastDeliveredID string
    var consumersCount int64
    for _, g := range groups {
        if g.Name == group {
            lastDeliveredID = g.LastDeliveredID
            consumersCount = g.Consumers
            break
        }
    }
    
    // Safety checks
    if consumersCount == 0 {
        r.logger.Debug("skipping trim: no active consumers", "stream", stream, "group", group)
        return 0, nil
    }
    
    if lastDeliveredID == "" || lastDeliveredID == "0-0" {
        r.logger.Debug("skipping trim: no messages delivered yet", "stream", stream, "group", group)
        return 0, nil
    }
    
    // Safe to trim: only messages OLDER than last-delivered-id AND older than maxAge
    return r.trimUpTo(ctx, stream, lastDeliveredID)
}

// Add new method
func (r *RetentionManager) trimUpTo(ctx context.Context, stream, maxID string) (int, error) {
    // Parse maxID to get timestamp
    parts := strings.Split(maxID, "-")
    if len(parts) != 2 {
        return 0, fmt.Errorf("invalid message ID: %s", maxID)
    }
    
    deliveredMs, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil {
        return 0, fmt.Errorf("parse message ID timestamp: %w", err)
    }
    
    // Apply maxAge limit
    cutoff := time.Now().Add(-r.maxAge)
    cutoffMs := cutoff.UnixMilli()
    
    // Use earlier of: delivered timestamp or age cutoff
    trimMs := deliveredMs
    if cutoffMs < deliveredMs {
        trimMs = cutoffMs
    }
    
    trimID := fmt.Sprintf("%d-0", trimMs)
    
    // Count before
    beforeCmd := r.redis.B().Xlen().Key(stream).Build()
    beforeLen, err := r.redis.Do(ctx, beforeCmd).AsInt64()
    if err != nil {
        return 0, fmt.Errorf("XLEN before: %w", err)
    }
    
    // Trim
    trimCmd := r.redis.B().Xtrim().Key(stream).Minid().Threshold(trimID).Build()
    if err := r.redis.Do(ctx, trimCmd).Error(); err != nil {
        return 0, fmt.Errorf("XTRIM: %w", err)
    }
    
    // Count after
    afterCmd := r.redis.B().Xlen().Key(stream).Build()
    afterLen, err := r.redis.Do(ctx, afterCmd).AsInt64()
    if err != nil {
        return 0, fmt.Errorf("XLEN after: %w", err)
    }
    
    return int(beforeLen - afterLen), nil
}
```

### Verification Test Case

After fix, create this test scenario:
1. Start event-writer, let it process 100 events
2. Stop event-writer (consumer group remains)
3. Manually add events with 48h-old timestamps:
   ```bash
   for i in {1..1000}; do
     OLD_TS=$(($(date +%s%3N) - 172800000))  # 48h ago
     redis-cli XADD them:events $OLD_TS-0 event '{"EventID":"test-'$i'","Kind":"test",...}'
   done
   ```
4. Check stream length: `redis-cli XLEN them:events` → should be 1100
5. Wait 20 minutes for retention run
6. Check again: `redis-cli XLEN them:events` → should still be 1100 (unread protected)
7. Start consumer, let it process all 1000
8. Wait 20 minutes
9. Check again: → should be ~100 (old processed events trimmed)

---

## After Fixes: Runtime Test Plan

Once both bugs fixed and code compiles, execute these tests:

### Test 1: Normal Delivery
```bash
cd /opt/docker/them
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  -f docker-compose.events.yml --profile events up -d

# Wait for services
sleep 10

# Check all healthy
docker compose ps | grep -i unhealthy && echo "FAIL: unhealthy services"

# Emit test events (use internal endpoint or trigger via HTTP)
# Verify delivery to ClickHouse
docker exec them-clickhouse clickhouse-client --query \
  "SELECT count(*) FROM them_events.events_gateway_request" 
  
# Should see events arriving
```

### Test 2: Shutdown with Buffered Events
```bash
# Buffer 100 events (don't let them flush)
# Send SIGTERM
docker kill -s TERM them-go-bridge

# Check logs for drain messages
docker logs them-go-bridge 2>&1 | grep "event sink drained"

# Verify events reached Redis
docker exec them-redis-events redis-cli XLEN them:events
```

### Test 3: Poison Event → DLQ
```bash
# Inject poison event (invalid JSON)
docker exec them-redis-events redis-cli XADD them:events \* event "not-json"

# Check event-writer logs
docker logs them-event-writer | grep -i "poison"

# Verify DLQ
docker exec them-redis-events redis-cli XLEN them:events:dlq
# Should be 1

# Verify original stream moved forward (not blocked)
docker exec them-redis-events redis-cli XLEN them:events
```

### Test 4: ClickHouse Outage
```bash
# Stop ClickHouse
docker stop them-clickhouse

# Emit 50 events
# Check event-writer logs for retry + backoff
docker logs them-event-writer -f | grep -i "retry\|backoff\|pause"

# Verify pending count grows
docker exec them-redis-events redis-cli XPENDING them:events event-writers

# Restart ClickHouse
docker start them-clickhouse

# Wait for recovery
sleep 30

# Verify events delivered
docker exec them-clickhouse clickhouse-client --query \
  "SELECT count(*) FROM them_events.events_gateway_request"
```

### Test 5: Consumer Restart + XAUTOCLAIM
```bash
# Let consumer claim 10 messages
# Kill consumer hard (SIGKILL - no ACK)
docker kill -s KILL them-event-writer

# Check pending
docker exec them-redis-events redis-cli XPENDING them:events event-writers
# Should show 10 pending

# Wait 35 seconds (past 30s idle threshold)
sleep 35

# Restart consumer
docker start them-event-writer

# Check logs for XAUTOCLAIM recovery
docker logs them-event-writer | grep -i "recovered pending"

# Verify pending cleared
docker exec them-redis-events redis-cli XPENDING them:events event-writers
# Should be 0 or low
```

### Test 6: Race Detection
```bash
cd /opt/docker/them/go
go test -race -run=. ./internal/events/... ./cmd/event-writer/...
```

### Test 7: Retention Unread Protection
See "Verification Test Case" in Bug #2 section above.

---

## Success Criteria

All tests must pass:
- ✅ Normal delivery working
- ✅ Shutdown drains events (no loss)
- ✅ Poison events → DLQ (queue not blocked)
- ✅ ClickHouse outage → retry → recovery (no loss)
- ✅ Consumer restart → XAUTOCLAIM → events recovered
- ✅ Race detection clean
- ✅ Retention protects unread messages

---

## Commit and Report

After all tests pass:

```bash
git add -A
git commit -m "fix(events): correct shutdown ordering + retention safety

Bug 1 - Shutdown ordering:
- Event sinks now close AFTER HTTP drain completes
- Added Server.AddCloser() method for dynamic registration
- Verified: in-flight handlers finish before sink closes

Bug 2 - Retention safety:
- Check XINFO GROUPS last-delivered-id before trim
- Protect unread messages (not just pending)
- Only trim messages older than delivered AND maxAge

All runtime tests passing:
- Normal delivery: PASS
- Shutdown drain: PASS  
- Poison→DLQ: PASS
- CH outage: PASS
- XAUTOCLAIM: PASS
- Race detection: PASS
- Retention unread: PASS

Findings 1-3 now VERIFIED and production-ready."
```

Create final report in `docs/EVENTS_RELIABILITY_VERIFIED.md` documenting:
- Test commands used
- Actual results (with timestamps/logs)
- Any remaining limitations
- Production deployment recommendations

---

## If Tests Fail

Document exact failure:
- Which test failed
- Actual vs expected behavior
- Error messages / logs
- Root cause analysis

Do NOT merge until all tests pass.

---

## Important Notes

1. **Do not skip fixes** - testing broken code wastes time
2. **Fix Bug #1 first** - simpler, unblocks testing
3. **Fix Bug #2 second** - requires retention test scenario
4. **Run tests in order** - early failures may block later tests
5. **Document everything** - actual commands run, results observed

---

## Files Modified This Session

- `go/cmd/them/main.go` - event sink wiring
- `go/cmd/auth-server/main.go` - event sink wiring  
- `go/cmd/mcp-service/main.go` - event sink wiring
- `go/cmd/event-writer/main.go` - consumer + retention
- `go/cmd/event-writer/retention.go` - NEW FILE
- `go/internal/events/redis.go` - RedisSink reliability
- `go/internal/server/server.go` - shutdown logging
- `go/internal/config/config.go` - REDIS_EVENTS_* fields
- `go/internal/authserver/config.go` - REDIS_EVENTS_* fields
- `go/internal/mcp/config.go` - REDIS_EVENTS_* fields
- `docker-compose.dev.yml` - REDIS_EVENTS_* env vars
- `docker-compose.events.yml` - event-writer endpoint fix
- `redis/config/redis-events.conf` - retention docs
- Multiple docs files

---

## Questions to Resolve

None - path forward is clear:
1. Fix bug #1
2. Fix bug #2  
3. Run tests
4. Document results
5. Merge or iterate

---

## Estimated Time

- Bug #1 fix: 30-45 min
- Bug #2 fix: 45-60 min
- Testing: 60-90 min
- Documentation: 30 min

**Total: 3-4 hours for fresh session**

Good luck! 🚀
