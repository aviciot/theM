# Events B1-B3 Reliability Review - Final Status
# Branch: reliability/events-b1-b3-fixes  
# Date: 2026-10-20
# HEAD: 14f1dcf6

---

## Implementation Status

### ✅ Finding 1: Dedicated Redis (COMPLETE)
- **Commits**: 53de7688, 7344cc85
- **Status**: Implemented + Compile-Verified
- All producers use them-redis-events (noeviction + AOF)
- Event-writer fixed to correct endpoint
- Startup validation: fails fast if config missing

### ✅ Finding 2: Consumer Reliability (COMPLETE)
- **Commit**: 93abe1b1
- **Status**: Implemented + Compile-Verified
- XAUTOCLAIM on startup (30s idle threshold)
- Poison events → DLQ before ACK
- ClickHouse retry (3 attempts, exp backoff)
- Bounded processing (pause if CH down)
- Observable counters

### ✅ Finding 3: Producer Reliability (COMPLETE)  
- **Commits**: 93abe1b1, 324d8eb0, 14f1dcf6
- **Status**: Implemented + Compile-Verified
- RedisSink bounded retries (3 attempts)
- Atomic counters: dropped, failed
- Graceful Close with deadline
- **Shutdown ordering**: sinks drain before Redis closes
- **Stream retention**: safe trim protecting pending/unread

---

## Shutdown Ordering (324d8eb0)

**Sequence**:
1. Signal received (SIGTERM/SIGINT)
2. Event sinks Close() with 5s deadline
   - Stop accepting new Emit calls
   - Drain buffered events
   - Flush to Redis
3. HTTP server Shutdown() with 20s deadline
4. Redis clients close (via defer)

**Implementation**:
- **Go bridge**: WithPreDrainHook
- **Auth server**: explicit drain before srv.Shutdown  
- **MCP service**: explicit drain before srv.Shutdown

---

## Stream Retention (14f1dcf6)

**RetentionManager** runs every 15 minutes:
- Trims events older than 24h (main stream)
- Trims events older than 7 days (DLQ)
- **Safety**: checks XPENDING before trim
- **Conservative**: skips trim if ANY pending messages exist
- Uses XTRIM MINID (timestamp-based)

**Protection**:
- ✅ Pending messages (claimed, not ACKed) protected
- ✅ Unread messages (not yet claimed) protected
- ✅ DLQ separate retention (audit trail)

---

## Runtime Testing Status: ⚠️ NOT PERFORMED

The following tests are **required** but **not yet executed**:

### Critical Tests
1. ❌ Normal delivery + deduplication
2. ❌ ClickHouse outage + recovery
3. ❌ Consumer restart + XAUTOCLAIM recovery
4. ❌ Poison events → DLQ → ACK
5. ❌ DLQ write failure (original stays recoverable)
6. ❌ ACK failure + recovery
7. ❌ Redis outage / buffer full + counter accuracy
8. ❌ SIGTERM with queued events + deadline expiry
9. ❌ Concurrent Emit/Close race detection
10. ❌ 30s reclaim threshold validation

### Verification Commands Prepared

```bash
# Start full stack
cd /opt/docker/them
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  -f docker-compose.events.yml --profile events --profile temporal up -d

# Watch logs
docker logs them-event-writer -f
docker logs them-go-bridge -f

# Check ClickHouse
docker exec them-clickhouse clickhouse-client --query \
  "SELECT count(*), kind FROM them_events.events_gateway_request GROUP BY kind"

# Check DLQ
docker exec them-redis-events redis-cli XLEN them:events:dlq

# Check pending
docker exec them-redis-events redis-cli XPENDING them:events event-writers

# Race detection
cd go && go test -race ./internal/events/... ./cmd/event-writer/...
```

---

## Known Limitations

### Best-Effort Guarantees Remain
- Producer drops on buffer-full (counted)
- Consumer exhausts retries → events lost (counted in failedCtr)
- No persistent outbox for guaranteed delivery

### Testing Gaps
- **No runtime tests executed**
- No integration tests
- No failure scenario tests
- No race detection run
- No load testing

### Missing Production Features
- No /metrics endpoint (counters in logs only)
- No alerting on Redis events memory
- No body capture to ClickHouse
- No tenant isolation tests
- Retention is conservative (may retain too much if pending stuck)

---

## Findings 4-6: OPEN

**Finding 4**: Gateway mode validation + dual-write safety (HIGH PRIORITY)  
**Finding 5**: Auth event identity (TenantID, verified logout)  
**Finding 6**: MCP IsError check + gateway streaming race

Per user directive: kept explicitly open, not addressed in this branch.

---

## Next Steps

### IMMEDIATE (Required Before Merge)
1. **Run runtime tests** (see commands above)
2. **Fix any discovered issues**
3. **Run go test -race**
4. **Document exact test results**
5. **Verify 30s XAUTOCLAIM threshold is safe**

### BEFORE PRODUCTION
1. **Address Finding 4** (gateway mode validation) - data integrity risk
2. **Add integration tests** to CI/CD
3. **Implement /metrics endpoint** for observable counters
4. **Set up alerting** on Redis events memory usage
5. **Load test** retention manager

### OPTIONAL IMPROVEMENTS
- Make retention less conservative (trim up to oldest pending, not skip entirely)
- Add retention metrics (events_trimmed_total)
- Implement body capture to ClickHouse
- Add tenant isolation tests

---

## Commit Summary

```
14f1dcf6 feat(events): implement safe stream retention with pending protection
324d8eb0 fix(events): implement proper shutdown ordering for event sinks
93abe1b1 fix(events): Findings 2+3 - consumer+producer reliability
7344cc85 fix(events): event-writer must use them-redis-events, not them-redis
e23f4518 docs: add events reliability review handover document
53de7688 fix(events): Finding 1 - wire producers to dedicated them-redis-events
12e82e5b docs: add comprehensive reliability review summary report
```

**Files Modified**: 15
**Lines Changed**: ~500

---

## Rollback

```bash
cd /opt/docker/them
git checkout main
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-go-bridge them-auth-go them-mcp-service
```

---

## Decision Point

**Current State**:
- ✅ Code complete for Findings 1-3
- ✅ All services compile successfully
- ❌ Runtime tests NOT performed
- ❌ Findings 4-6 remain open

**Options**:
1. **Run tests now** → fix issues → merge (RECOMMENDED)
2. **Merge untested** → test in staging (RISKY)
3. **Continue with Finding 4** → test all together (SCOPE CREEP)

**Recommendation**: Run tests now. The code is ready, tests are defined, commands are prepared. Testing will likely expose issues that should be fixed before merge.

---

## ⚠️ WARNING

**This branch is NOT production-ready** until:
- Runtime tests pass
- Race detection clean
- Finding 4 (gateway validation) addressed
- Integration tests exist
- Metrics/alerting configured

Do NOT deploy based solely on "code compiles and looks good."
