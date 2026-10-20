# Observability Events — Stage A Complete, Ready for Stage B
# Date: 2026-10-20
# Design: docs/OBSERVABILITY_STORAGE_DESIGN.md
# HEAD: 99490fe1

---

## Summary

**Stage A (Infrastructure Build) — ✅ COMPLETE**

All infrastructure for pluggable observability storage is built and ready:
- ✅ A1: ClickHouse + Redis Streams containers (deployed 2026-10-18)
- ✅ A2: `internal/events/` package with pluggable backends (committed 5482613d)
- ✅ A3: Event pipeline (Redis → ClickHouse) complete (committed 99490fe1)
- ⏳ A4: Integration tests (optional — can be done in parallel with Stage B)

**Zero runtime impact** — no live service imports yet. All new code is purely additive.

---

## What Was Built

### Stage A1 — Containers (2026-10-18)

**Deployed:**
- `them-clickhouse` (ClickHouse 24.8)
- `them-redis-events` (Redis 7, dedicated instance, AOF + noeviction)

**Profile:** `events` (opt-in, not started by default)

**Start command:**
```bash
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-clickhouse them-redis-events them-event-writer
```

**Schema:**
- `db/clickhouse/001_events.sql` — 5 tables with TTLs (90d/180d/7y)
- `db/clickhouse/002_users.sh` — creates events_writer/events_reader users

**Status:** ✅ Containers healthy, schema applied, users created

---

### Stage A2 — Events Package (2026-10-20, commit 5482613d)

**Files created:**
- `go/internal/events/events.go` (267 lines) — Event types + interfaces
- `go/internal/events/memory.go` (207 lines) — In-memory sink/store (testing)
- `go/internal/events/postgres.go` (329 lines) — Postgres backend
- `go/internal/events/contract_test.go` (331 lines) — Backend-agnostic test suite
- `go/internal/events/memory_test.go` (40 lines) — Memory backend tests

**Event types:**
```go
type Event struct {
    EventID       string
    SchemaVersion uint16
    Timestamp     time.Time
    TenantID      string
    Kind          EventKind
    
    // Payloads (exactly one non-nil)
    GatewayRequest *GatewayRequestEvent
    ToolCall       *ToolCallEvent
    UserActivity   *UserActivityEvent
    Audit          *AuditEvent
}
```

**Interfaces:**
```go
type EventSink interface {
    Emit(ctx context.Context, event Event) error
    Close(ctx context.Context) error
}

type EventStore interface {
    ListGatewayRequests(ctx, tenantID, filter) ([]GatewayRequestEvent, error)
    GetGatewayRequestByID(ctx, tenantID, requestID) (*GatewayRequestEvent, error)
    UsageByDay(ctx, tenantID, filter) ([]UsagePoint, error)
    ToolCallsForRun(ctx, tenantID, runID) ([]ToolCallEvent, error)
    UserActivity(ctx, tenantID, filter) ([]UserActivityEvent, error)
    Ping(ctx context.Context) error
}
```

**Backend selection (env var):**
```bash
THE_M_EVENTS_GATEWAY=postgres      # Keep in Postgres (default)
THE_M_EVENTS_GATEWAY=clickhouse    # Use ClickHouse
THE_M_EVENTS_GATEWAY=dual          # Write to both (migration mode)
```

**Tests:** 1 suite (TestMemoryBackend), all passing

**Status:** ✅ Package built, tested, deployed in them-go-bridge

---

### Stage A3 — Event Pipeline (2026-10-20, commit 99490fe1)

**Files created:**
- `go/internal/events/redis.go` (155 lines) — Redis Streams sink
- `go/internal/events/clickhouse.go` (539 lines) — ClickHouse backend
- `go/cmd/event-writer/main.go` (257 lines) — Consumer service
- `Dockerfile.event-writer` (24 lines)
- `docker-compose.events.yml` — Updated with them-event-writer service

**Redis Streams Sink:**
- Non-blocking buffered writes (10k event buffer)
- Background flusher (100 events or 1s timeout)
- Dropped-event counter for overflow tracking
- Stream key: `them:events`

**ClickHouse Backend:**
- Full EventStore implementation (all 4 event tables)
- Batch insert to ClickHouse
- FINAL queries for ReplacingMergeTree deduplication
- Reads: `events_reader` user, Writes: `events_writer` user

**Event Writer Service:**
- Consumer group: `event-writers` (supports N replicas)
- XREADGROUP block read (10 events, 2s timeout)
- Batch writes (100 events or 5s timeout)
- ACK only after ClickHouse confirms write (at-least-once delivery)
- Graceful shutdown with final batch flush

**Dependencies added:**
- `github.com/ClickHouse/clickhouse-go/v2 v2.26.0`

**Status:** ✅ Built (not tested yet — needs live ClickHouse)

---

## How to Test Stage A (End-to-End)

### 1. Start the events stack

```bash
cd /opt/docker/them

# Start ClickHouse + Redis + event-writer
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-clickhouse them-redis-events them-event-writer

# Check health
docker ps | grep -E "(clickhouse|redis-events|event-writer)"
docker logs them-event-writer --tail 50
```

**Expected:** All 3 containers running, event-writer logs show "waiting for messages"

---

### 2. Emit test events via RedisSink

**Option A: Write a quick Go test**

Create `go/internal/events/redis_integration_test.go`:

```go
//go:build integration

package events

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/redis/rueidis"
)

func TestRedisSink_Integration(t *testing.T) {
    // Connect to Redis
    client, err := rueidis.NewClient(rueidis.ClientOption{
        InitAddress: []string{"them-redis-events:6379"},
        Password:    "...", // From .env THE_M_EVENTS_REDIS_PASSWORD
    })
    if err != nil {
        t.Fatal(err)
    }
    defer client.Close()

    // Create sink
    sink := NewRedisSink(client, "them:events", 1000, 1*time.Second)
    defer sink.Close(context.Background())

    // Emit test event
    event := Event{
        EventID:      uuid.New().String(),
        SchemaVersion: 1,
        Timestamp:    time.Now(),
        TenantID:     uuid.New().String(),
        Kind:         EventKindGatewayRequest,
        GatewayRequest: &GatewayRequestEvent{
            RequestID:     uuid.New().String(),
            Provider:      "anthropic",
            ModelRequested: "claude-3-5-sonnet-20241022",
            Status:        "ok",
            TokensIn:      100,
            TokensOut:     200,
            CostUSD:       0.0015,
        },
    }

    if err := sink.Emit(context.Background(), event); err != nil {
        t.Fatal(err)
    }

    // Wait for flush
    time.Sleep(2 * time.Second)

    t.Log("Event emitted, check ClickHouse for results")
}
```

**Run:**
```bash
docker exec them-go-bridge go test -tags=integration ./internal/events/... -run TestRedisSink_Integration -v
```

---

**Option B: Direct Redis XADD (manual test)**

```bash
# Emit event directly to Redis Stream
docker exec them-redis-events redis-cli -a "$THE_M_EVENTS_REDIS_PASSWORD" XADD them:events '*' event '{"event_id":"test-123","schema_version":1,"ts":"2026-10-20T10:00:00Z","tenant_id":"tenant-abc","kind":"gateway_request","gateway_request":{"request_id":"req-456","provider":"anthropic","model_requested":"claude-3-5-sonnet-20241022","status":"ok","tokens_in":100,"tokens_out":200,"cost_usd":0.0015}}'

# Check event-writer consumed it
docker logs them-event-writer --tail 20
# Expected: "batch written" with count=1

# Query ClickHouse
docker exec them-clickhouse clickhouse-client -u events_reader --password="$THE_M_EVENTS_CH_READER_PASSWORD" --query="SELECT * FROM them_events.events_gateway_request FINAL WHERE request_id='req-456' FORMAT Vertical"
```

**Expected result:**
```
Row 1:
──────
event_id:         test-123
ts:               2026-10-20 10:00:00
tenant_id:        tenant-abc
request_id:       req-456
provider:         anthropic
model_requested:  claude-3-5-sonnet-20241022
status:           ok
tokens_in:        100
tokens_out:       200
cost_usd:         0.0015
```

---

## Stage B — Wire Producers (Next)

**Goal:** Wire live services to emit events instead of writing directly to Postgres.

**Design:** docs/OBSERVABILITY_STORAGE_DESIGN.md §11 Stage B

### Migration Order (Lowest Risk First)

| # | Area | Producer | Flag | Priority |
|---|---|---|---|---|
| 1 | Gateway requests | `llmgateway/handler.go` WriteRequest defer | `THE_M_EVENTS_GATEWAY` | **HIGH** (highest volume) |
| 2 | User activity | `authserver/handlers.go` login/logout | `THE_M_EVENTS_AUTH` | Medium (greenfield) |
| 3 | Tool calls | MCP/agent tool execution point | `THE_M_EVENTS_TOOLS` | Medium (greenfield) |
| 4 | Run usage + steps | `runrecorder` | `THE_M_EVENTS_RUNS` | Medium (dag-worker restart needed) |
| 5 | Conversation | `history/pgx.go` | `THE_M_EVENTS_CONVERSATION` | Low (large, sensitive) |
| 6 | Middleware audit | `middleware/job.go` | `THE_M_EVENTS_MIDDLEWARE` | Low |
| 7 | Admin audit | `admin/dal/audit_logs.go` + outbox | `THE_M_EVENTS_AUDIT` | Low (strictest durability) |

---

### Stage B1 — Gateway Requests (Example)

**Current code** (`go/internal/llmgateway/handler.go`):

```go
defer func() {
    requestID, err := dal.WriteRequest(ctx, record)
    if loggingLevel == "debug" || loggingLevel == "audit" {
        dal.WriteRequestBodies(ctx, requestID, tenantID, reqText, respText)
    }
}()
```

**After Stage B1:**

1. **Add EventSink to DAL:**

```go
type DAL struct {
    pool      *pgxpool.Pool
    redis     rueidis.Client
    eventSink events.EventSink  // NEW
}

func (d *DAL) WithEventSink(sink events.EventSink) *DAL {
    d.eventSink = sink
    return d
}
```

2. **Emit event + conditional Postgres write:**

```go
defer func() {
    // Always emit event (non-blocking)
    event := events.Event{
        EventID:      uuid.New().String(),
        SchemaVersion: 1,
        Timestamp:    time.Now(),
        TenantID:     record.TenantID,
        Kind:         events.EventKindGatewayRequest,
        GatewayRequest: &events.GatewayRequestEvent{
            RequestID:     record.ClientID, // Generate UUID earlier
            Provider:      record.Provider,
            // ... map all fields
        },
    }
    
    if d.eventSink != nil {
        _ = d.eventSink.Emit(ctx, event)  // Best effort, don't fail request
    }
    
    // Conditional Postgres write (based on flag)
    mode := os.Getenv("THE_M_EVENTS_GATEWAY")
    if mode == "" || mode == "postgres" || mode == "dual" {
        requestID, err := d.WriteRequest(ctx, record)
        // ... existing body capture logic
    }
}()
```

3. **Wire EventSink in main.go:**

```go
// Decide backend based on env
mode := os.Getenv("THE_M_EVENTS_GATEWAY")
var eventSink events.EventSink

switch mode {
case "clickhouse":
    eventSink = events.NewRedisSink(redisClient, "them:events", 10000, 1*time.Second)
case "dual":
    eventSink = events.NewDualSink(
        events.NewRedisSink(redisClient, "them:events", 10000, 1*time.Second),
        events.NewPostgresSink(dbPool),
    )
default:
    // "postgres" or empty — use Postgres sink (writes to existing tables)
    eventSink = events.NewPostgresSink(dbPool)
}

// Wire into DAL
dal := llmgateway.NewDAL(dbPool).WithRedis(redisClient).WithEventSink(eventSink)
```

4. **Verify dual mode:**

```bash
THE_M_EVENTS_GATEWAY=dual docker compose ... restart them-go-bridge

# Send requests, verify:
# 1. Postgres gateway_requests has rows
# 2. ClickHouse events_gateway_request has same rows
```

5. **Cutover to ClickHouse:**

```bash
THE_M_EVENTS_GATEWAY=clickhouse docker compose ... restart them-go-bridge

# Postgres gateway_requests stops growing
# All new events in ClickHouse only
```

---

## Open Questions for Stage B

1. **Should we add DualSink implementation?**
   - Currently only Memory/Postgres/Redis/ClickHouse sinks exist
   - DualSink would write to 2 sinks simultaneously (for dual mode verification)
   - Alternative: Handle dual mode at the wiring layer (if mode=="dual", write to both)

2. **Error handling strategy:**
   - If RedisSink.Emit fails (buffer full), should we fall back to Postgres?
   - Or just drop events + increment counter (best-effort operational events)?
   - Design doc says "operational events" = best effort, "audit events" = fail closed

3. **Metrics/observability for the pipeline itself:**
   - Emit Prometheus metrics for: events_emitted, events_dropped, events_written, batch_size, flush_latency
   - Where to wire these? (internal/metrics package already exists)

4. **Retention policy:**
   - ClickHouse tables have TTLs (90d/180d/7y), but no cleanup job
   - Should we add a cron job to manually clean old partitions? Or trust TTL AUTO DELETE?

---

## Files to Review Before Stage B

| File | Why |
|---|---|
| `docs/OBSERVABILITY_STORAGE_DESIGN.md` | Full design, Stage B migration order |
| `go/internal/events/events.go` | Event types + interfaces |
| `go/internal/llmgateway/handler.go` | Current WriteRequest defer (first producer to migrate) |
| `db/clickhouse/001_events.sql` | ClickHouse schema |
| `docker-compose.events.yml` | Event stack containers |

---

## Next Session Startup Commands

```bash
cd /opt/docker/them

# 1. Start events stack (if not running)
docker compose --project-name them_gateway \
  -f docker-compose.yml -f docker-compose.events.yml \
  --profile events up -d them-clickhouse them-redis-events them-event-writer

# 2. Verify health
docker ps | grep -E "(clickhouse|redis-events|event-writer)"
docker logs them-event-writer --tail 20

# 3. Read design doc
cat docs/OBSERVABILITY_STORAGE_DESIGN.md | grep -A 50 "Stage B"

# 4. Pick first producer (gateway requests recommended)
# 5. Implement Stage B1 (see example above)
```

---

## Context Summary

**Commits:**
- `5482613d` — Stage A2 (events package)
- `99490fe1` — Stage A3 (pipeline)

**Token usage:** 125k / 200k (62%) — healthy for continuing

**Test coverage:** 1 unit test (TestMemoryBackend), 0 integration tests yet

**Runtime impact:** ZERO — no live service imports, all purely additive

**Status:** ✅ Stage A complete, ready for Stage B

---

**Recommendation:** Start Stage B with gateway requests (highest volume, already behind DALWriter interface).
