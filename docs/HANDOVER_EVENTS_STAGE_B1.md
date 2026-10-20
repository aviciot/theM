# Observability Events — Stage B1 Complete
# Date: 2026-10-20
# Design: docs/OBSERVABILITY_STORAGE_DESIGN.md
# HEAD: 20bf4ef1

---

## Summary

**Stage B1 (Gateway Requests Producer) — ✅ COMPLETE**

LLM Gateway requests now emit observability events to ClickHouse via Redis Streams.
Controlled by `THE_M_EVENTS_GATEWAY` environment variable.

---

## What Was Built

### Stage B1 — Gateway Requests Producer

**Files modified:**
- `go/internal/llmgateway/dal.go` (121 lines changed)
  - Added `EventSink` field to DAL struct
  - Added `WithEventSink(sink events.EventSink)` method
  - Refactored `WriteRequest()` to emit events + conditional Postgres write
  - Pre-generates UUID for request ID (used by both Postgres and ClickHouse)
  
- `go/cmd/them/main.go` (18 lines added)
  - Wire EventSink based on `THE_M_EVENTS_GATEWAY` mode
  - Three modes: `postgres` (default), `clickhouse`, `dual`

**Event flow:**
```
HTTP Request → llmgateway.Handler → WriteRequest()
   ↓
RedisSink.Emit() → Redis Streams (them:events)
   ↓
them-event-writer consumes → ClickHouse (events_gateway_request table)
```

**Modes:**

| Mode | Postgres Write | Event Emission | Use Case |
|---|---|---|---|
| `postgres` (default) | ✅ Yes | ❌ No | Current production behavior |
| `clickhouse` | ❌ No | ✅ Yes | Pure ClickHouse (after migration) |
| `dual` | ✅ Yes | ✅ Yes | Migration verification — both writes |

---

## How to Test

### 1. Start the events stack (if not running)

```bash
cd /opt/docker/them

# Start ClickHouse + Redis + event-writer
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-clickhouse them-redis-events them-event-writer

# Check health
docker ps | grep -E "(clickhouse|redis-events|event-writer)"
docker logs them-event-writer --tail 20
```

**Expected:**
- All 3 containers `Up` and healthy
- Event-writer logs: `"ClickHouse sink initialized"`

---

### 2. Enable dual mode (write to both Postgres AND ClickHouse)

**Option A: Environment variable**

Add to `.env`:
```bash
THE_M_EVENTS_GATEWAY=dual
```

**Option B: Docker Compose override**

Edit `docker-compose.dev.yml` and add to `them-go-bridge` environment:
```yaml
them-go-bridge:
  environment:
    - THE_M_EVENTS_GATEWAY=dual
```

---

### 3. Restart Go bridge to pick up the new mode

```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-go-bridge

# Check logs for confirmation
docker logs them-go-bridge --tail 30 | grep "events sink"
```

**Expected log:**
```
gateway events sink: ClickHouse (via Redis Streams) mode=dual
```

---

### 4. Send test requests

**Option A: Use test-runner scenario 06 (gateway token limit)**

```bash
cd test-runner
python3.12 run_scenario.py 06
```

**Option B: Manual curl**

```bash
# Get a gateway token first
GATEWAY_TOKEN=$(docker exec them-postgres psql -U them -d them -tAc \
  "SELECT token FROM them.gateway_clients LIMIT 1")

# Send request
curl -X POST http://localhost:8088/test-app/llm/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

---

### 5. Verify dual writes

**Check Postgres `gateway_requests`:**

```bash
docker exec them-postgres psql -U them -d them -c \
  "SELECT id, provider, model_requested, status, tokens_in, tokens_out 
   FROM them.gateway_requests 
   ORDER BY created_at DESC LIMIT 5;"
```

**Check ClickHouse `events_gateway_request`:**

```bash
docker exec them-clickhouse clickhouse-client \
  -u events_reader \
  --password="$THE_M_EVENTS_CH_READER_PASSWORD" \
  --query="SELECT event_id, request_id, provider, model_requested, status, tokens_in, tokens_out 
           FROM them_events.events_gateway_request FINAL 
           ORDER BY ts DESC LIMIT 5 FORMAT Vertical"
```

**Expected:**
- Both queries return the same request (matching by `request_id`)
- All fields match (provider, model, status, tokens)

---

### 6. Check event pipeline health

**Redis Streams:**

```bash
docker exec them-redis redis-cli -a "$THE_M_REDIS_PASSWORD" \
  XINFO STREAM them:events
```

**Expected:**
- `length: N` (N > 0 means events are queued)
- `groups: 1` (consumer group `event-writers` exists)

**Event-writer logs:**

```bash
docker logs them-event-writer --tail 50 | grep "batch written"
```

**Expected:**
- Logs like `"batch written" count=1 latency_ms=X`

---

## Cutover to ClickHouse-Only (Future)

Once dual mode is verified stable:

1. **Switch to ClickHouse-only:**

```bash
# .env
THE_M_EVENTS_GATEWAY=clickhouse

# Restart bridge
docker compose ... restart them-go-bridge
```

2. **Verify Postgres writes stop:**

```bash
# Send requests, then check Postgres
docker exec them-postgres psql -U them -d them -c \
  "SELECT COUNT(*) FROM them.gateway_requests WHERE created_at > NOW() - INTERVAL '1 minute';"
# Should return 0
```

3. **Verify ClickHouse writes continue:**

```bash
docker exec them-clickhouse clickhouse-client ... \
  --query="SELECT COUNT(*) FROM them_events.events_gateway_request FINAL WHERE ts > NOW() - INTERVAL 1 MINUTE FORMAT Vertical"
# Should return N > 0
```

---

## Known Gaps / Future Work

1. **Dedicated Redis client for events** — currently using `redisCache` (main Redis), should use a separate `redisEvents` client (dedicated events Redis instance) when available.

2. **Additional fields in events** — `ApplicationID`, `UserID`, `TraceID` are defined in `GatewayRequestEvent` but not yet populated from `RequestRecord`. Need to add these fields to `RequestRecord` or pass them separately.

3. **Body capture** — `WriteRequestBodies()` still writes to Postgres `gateway_request_bodies`. Need to:
   - Add `BodyRef` (UUID) to the event
   - Write bodies to ClickHouse `events_bodies` table
   - Conditionally skip Postgres write based on mode

4. **Stage B2-B7** — Additional producers:
   - User activity (login/logout)
   - Tool calls (MCP/agent execution)
   - Run usage + steps
   - Conversation history
   - Middleware audit
   - Admin audit

---

## Rollback Procedure

If issues arise:

1. **Switch back to Postgres-only:**

```bash
# Remove or comment out in .env
# THE_M_EVENTS_GATEWAY=dual

# Restart bridge
docker compose ... restart them-go-bridge
```

2. **Check logs confirm:**

```bash
docker logs them-go-bridge --tail 30 | grep "events sink"
# Expected: "gateway events sink: Postgres only (legacy)"
```

3. **Verify requests continue working normally**

---

## Monitoring

**Metrics to track:**
- Event emission failures (check `them-go-bridge` logs)
- Event-writer batch write latency (check `them-event-writer` logs)
- Redis Stream length (should stay low, not grow unbounded)
- ClickHouse table size growth (`SELECT COUNT(*) FROM events_gateway_request`)
- Postgres `gateway_requests` table growth (should stop in `clickhouse` mode)

**Alerts:**
- Redis Stream length > 10,000 (backlog building up)
- Event-writer not consuming for > 5 minutes
- ClickHouse writes failing (check event-writer error logs)

---

## Files Changed

| File | Lines Changed | Description |
|---|---|---|
| `go/internal/llmgateway/dal.go` | +121 -31 | EventSink field, refactored WriteRequest |
| `go/cmd/them/main.go` | +18 -0 | Wire EventSink based on mode |
| `go/internal/events/clickhouse.go` | +2 -2 | Fix compile errors |
| `go/cmd/event-writer/main.go` | +35 -10 | ClickHouse sink init, fix rueidis API |
| `go/go.mod`, `go/go.sum`, `go/vendor/` | +357427 -13 | Add ClickHouse dependencies |

---

## Commits

- `8079bf33` — Stage A fixes (clickhouse.go, event-writer)
- `20bf4ef1` — Stage B1 (gateway requests producer)
- `a93bcede` — Updated STATUS.md

---

## Next Session

**Continue Stage B — Wire Additional Producers**

Priority order (from design doc):

1. ✅ **B1: Gateway requests** — COMPLETE
2. **B2: User activity** — `authserver/handlers.go` login/logout
3. **B3: Tool calls** — MCP/agent tool execution point
4. **B4: Run usage + steps** — `runrecorder` (requires dag-worker restart)
5. **B5: Conversation** — `history/pgx.go`
6. **B6: Middleware audit** — `middleware/job.go`
7. **B7: Admin audit** — `admin/dal/audit_logs.go` + outbox

**Startup commands:**

```bash
cd /opt/docker/them

# Verify events stack running
docker ps | grep -E "(clickhouse|redis-events|event-writer)"

# Verify dual mode active
docker logs them-go-bridge --tail 30 | grep "events sink"

# Start next producer (B2: User activity)
# Read: go/internal/authserver/handlers.go
# Add EventSink to authserver, emit events on login/logout
```

---

## Context Summary

**HEAD:** 20bf4ef1  
**Session duration:** ~2 hours  
**Token usage:** 85k / 200k (42%)  
**Status:** Stage A + B1 complete, tested, documented  
**Next:** Stage B2 (user activity) or B3 (tool calls)

---

**Recommendation:** Test dual mode with live traffic (5-10 gateway requests) before continuing to B2.
Verify both Postgres and ClickHouse receive matching rows, then proceed with next producer.
