---
title: Events Pipeline
type: architecture
status: current
owns:
  - go/internal/events/
  - go/cmd/event-writer/
  - db/clickhouse/
  - docker-compose.events.yml
  - redis/config/redis-events.conf
last_verified: 2026-10-24   # by reading code; not re-run live this session
---

# Events Pipeline

## Purpose
Move high-volume observability data (gateway requests, tool calls, user activity, audit) out of
Postgres into ClickHouse without slowing the request path. Postgres stays the control plane
and, for the gateway, the authoritative record.

## How it works

```
producer (gateway / auth / mcp-service)
  └─ RedisSink.Emit()  non-blocking, in-memory buffer (10000)
       └─ flushLoop: batches of ≤100 or every 1s, XADD to stream `them:events`
            (3 attempts, 100ms·n backoff; then dropped + counted)
them-redis-events  (separate Redis, AOF, noeviction)
  └─ event-writer (consumer group `event-writers`, XREADGROUP COUNT 10)
       ├─ bad JSON / missing field / missing kind  → DLQ `them:events:dlq`, then ACK
       ├─ events.ValidateEvent fails               → DLQ, then ACK
       ├─ ClickHouseSink.Emit per event (≤3 attempts) → ACK only after success
       └─ all retries fail → leave pending, pause ingestion after 3 consecutive failures
ClickHouse `them_events`  (ReplacingMergeTree, dedup by event_id)
```

- **Envelope:** `events.Event` (`events.go`): `EventID` (dedup key), `SchemaVersion`, `Timestamp`,
  `TenantID`, `Kind`, and exactly one payload (`GatewayRequest`, `ToolCall`, `UserActivity`, `Audit`).
- **Delivery is at-least-once.** A retried batch can re-insert events already written; ClickHouse
  collapses them by `event_id` at merge time (queries may briefly see duplicates).
- **Recovery:** `RecoverPending` (XAUTOCLAIM, idle >30s) runs at startup and every 60s, pushing
  reclaimed messages through the same validate → write/DLQ → ACK path.
- **Retention:** `retention.go` trims `them:events` every 15 min, only up to the group's
  last-delivered id AND older than 24h, and never while any message is pending or no consumer
  exists. DLQ is trimmed by age only (7 days). ClickHouse TTLs: gateway/tool 90d, user activity
  180d, audit 7y, bodies 30d (`db/clickhouse/001_events.sql`).
- **Health:** event-writer serves `GET /health` on `:8080`; 503 when lag >1000 or pending >100.

## Modes (`THE_M_EVENTS_<AREA>`, `internal/events/mode.go`)

| Value | Meaning |
|---|---|
| `postgres` (default / empty) | Gateway: no event emission, Postgres only. |
| `dual` | Gateway: Postgres write (authoritative) + event emitted to Redis → ClickHouse. Emit errors are ignored (`_ =`) so the request never fails. Requires `REDIS_EVENTS_HOST` (`RequireRedisForMode`). |
| `clickhouse` | **Rejected for the gateway** (`ValidateGatewayEventsMode`: no budget queries / body storage yet). Allowed by `ValidateEventsMode` for auth and tools. |

Areas wired today: `GATEWAY` (`cmd/them/main.go`, `internal/llmgateway/dal.go`),
`AUTH` (`cmd/auth-server/main.go`), `TOOLS` (`cmd/mcp-service/main.go`, `internal/mcp/executor.go`).
Unknown values fail startup.

## Key invariants
- Every event carries `TenantID`; `ValidateEvent` rejects empty `TenantID`/`EventID`/`Kind`.
- A message is ACKed only after ClickHouse confirms the write, or after it is safely in the DLQ.
- Retention must never trim pending or unread messages.
- Producers must never block or fail a user request because of an event (`Emit` is best-effort).
- Event Redis is `noeviction`: if it fills (512mb), XADD fails and events are dropped at the producer.

## Failure modes
| Failure | Effect |
|---|---|
| ClickHouse down | Messages stay pending; writer backs off then pauses; no loss until Redis fills or retention (24h, but never past unACKed) |
| Event Redis down | Producer retries 3×, then drops the batch (`FailedCount`) |
| Producer buffer full (10000) | `Emit` returns error, event dropped (`DroppedCount`) |
| Process crash before flush | Buffered events lost (in-memory only) |
| Writer crash mid-batch | Messages pending → reclaimed after 30s (duplicates possible, deduped by `event_id`) |
| Poison message | Goes to DLQ with a reason; DLQ is not auto-replayed |

## Known gaps (verified in code, 2026-10-24)
- **Read side is unused.** `EventStore` implementations (`PostgresStore`, `ClickHouseStore`) have no
  production callers; only tests use them. `ToolCallsForRun`/`UserActivity` on `PostgresStore` are unimplemented.
- **`Audit` kind has no producer** (sink writes it, nothing emits it). `ValidateEvent` does not check the audit payload.
- Producer drop/fail counters exist but nothing exports them (no metric/log surfaced from `main.go`).
- (fixed) `them:events` / `them:events:dlq` are now found by the generated Redis reference `docs/reference/redis-generated.md`.
- The ClickHouse sink inserts one row per event (the "batch" is a loop, not a bulk insert).
- `retention_test.go.disabled` — retention has no active unit test.
- `GatewayRequestEvent.UserID/ApplicationID` are not set by `llmgateway/dal.go`'s `WriteRequest`.

## Where to change things
| To… | Edit |
|---|---|
| Add an event kind | `events.go` (type + `EventKind`), `validate.go`, `clickhouse.go` (`write*`), `db/clickhouse/001_events.sql`, `postgres.go` if mirrored |
| Change retry/batch/recovery timing | constants at top of `cmd/event-writer/main.go` |
| Change retention | `NewRetentionManager(...)` call in `main.go`, logic in `retention.go` |
| Add a producer | build `events.Event`, `NewRedisSink(client, "them:events", 10000, 1*time.Second)`, gate on `ValidateEventsMode` |
| Run the stack | `docker-compose.events.yml`, profile `events`; secrets via `scripts/events-env.sh` |

Tests: `go test ./internal/events/... ./cmd/event-writer/...` (see `go/TEST_INDEX.md`).
After changing `internal/events/` or `cmd/event-writer/`: **rebuild + restart `them-event-writer`**.

## Related
- Archived history (hints only): `docs/_archive/OBSERVABILITY_STORAGE_DESIGN.md`, `adr-003-redis-streams-event-delivery.md`.
