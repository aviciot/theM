# ADR-003: Redis Streams for Durable Run Event Delivery

**Status:** Accepted (design phase)
**Date:** 2026-07-21
**Deciders:** aviciot

---

## Context

Run events (LLM tokens, tool calls, done/error) are delivered from the Python
Temporal worker to Go WS/SSE handlers via Redis Pub/Sub
(`them:dash:run:{runID}:tokens`). Pub/Sub is at-most-once: a client that
disconnects and reconnects misses every event published during the gap. There is
no replay mechanism. This produces blank or partial responses for any run longer
than a network hiccup.

---

## Decision

Replace Redis Pub/Sub with Redis Streams (`XADD` / `XRANGE` / `XREAD`) as the
durable event transport, migrated incrementally via a dual-publish phase.

---

## Consequences

### Positive

- Reconnecting clients replay from `last_event_id` — no events lost.
- Ordering guaranteed by monotonic stream entry IDs.
- No new infrastructure: Redis Streams are built into the existing Redis instance.
- Each phase is independently rollbackable.

### Negative / tradeoffs

- ~75 KB Redis memory per active run stream (bounded by MAXLEN ~5000).
- Python worker requires a new Lua script call for atomic dual-publish.
- Go `runstream` package requires a new `Streamer` interface and a mode flag.
- Additional complexity: TTL management, trim detection, mode configuration.

---

## Key decisions and rationale

### D1: Continuous cursor — no gap at replay-to-live transition

After XRANGE replay, continue XREAD from the last stream entry ID actually
processed, not from `$`. Using `$` creates a window where events written between
the end of XRANGE and the start of XREAD are dropped silently. A single advancing
cursor eliminates this race entirely.

### D2: Explicit mode flag (`RUN_EVENTS_MODE`) — not key-existence check

The stream key does not exist before the first event is published. Selecting the
transport based on key existence means a handler that starts first would fall back
to Pub/Sub for the entire run, defeating the migration. An explicit configuration
flag (`pubsub` | `dual` | `streams`) is the only correct gate.

### D3: Atomic dual-publish via Lua script

XADD and PUBLISH in separate calls have an observable failure window. A Lua script
executing on the Redis server ensures both succeed or neither does. The script
returns the stream entry ID, making it the authoritative event cursor. The Stream
is the durable source of truth; PUBLISH is a real-time optimisation removable in
Phase D.

### D4: Redis EXPIRE for stream lifetime — no in-process timers

An in-process goroutine scheduling DEL after 24 hours is lost on pod restart. Redis
`EXPIRE` is durable at the Redis layer. Two TTLs are applied:

- **Safety TTL (48h)**: set on first XADD; prevents orphaned keys if no terminal
  event is ever published.
- **Final TTL (24h)**: set when the Go handler forwards the terminal event;
  starts the 24-hour retention clock from run completion.

### D5: `replay_unavailable` event on trim detection

When `last_event_id` falls before the oldest retained entry (trimmed by MAXLEN),
the server sends a `{"type":"replay_unavailable",...}` event rather than silently
returning a partial replay. The client can display a user-facing message and decide
whether to fetch the run status from the REST API instead.

### D6: Pub/Sub removal requires explicit approval — no automatic cutover

Phase D (removing Pub/Sub) is not triggered by a calendar timer. It requires:
- ≥2 weeks of Phase C running in staging with `RUN_EVENTS_MODE=streams`
- Soak validation showing zero replay failures, zero stream errors
- Explicit approval following the same process as the DryRun=false activation

---

## Rejected alternatives

**Pub/Sub with longer reconnect backoff:** Reconnect-only cannot recover events
published during the gap regardless of backoff duration. The fundamental limitation
is at-most-once semantics, not reconnect speed.

**Consumer groups:** Each WS/SSE connection has its own read position and reads
independently. Consumer group semantics (deliver each message once per group) do
not match this pattern and add coordination overhead without benefit.

**External message broker (Kafka, NATS):** No new infrastructure dependency
justified for this use case. Redis Streams provide sufficient durability and replay
semantics within the existing Redis instance.

---

## Rollout sequence

| Phase | Scope | Gate to next phase |
|---|---|---|
| A | Python: atomic dual-publish Lua script | Go bridges unaffected; observe XADD metrics |
| B | Go: stream-read/replay behind `RUN_EVENTS_MODE=dual` | Staging soak with dual mode |
| C | Staging: `RUN_EVENTS_MODE=streams`; MAXLEN validation tests | ≥2 weeks stable + explicit approval |
| D | Remove Pub/Sub from Python + Go | Post-approval only |

---

## Rollback

Each phase rolls back independently:

- **Rollback A:** remove Lua script from Python worker; Pub/Sub unchanged.
- **Rollback B:** set `RUN_EVENTS_MODE=pubsub`; restart Go bridges.
- **Rollback C:** set `RUN_EVENTS_MODE=dual`; restart Go bridges.
- **Rollback D:** re-add PUBLISH in Python + Subscribe in Go; streams remain
  as backup.

No shared state makes any rollback risky.

---

## Related

- `phase-11c-design.md` — full design with all implementation details
- `runbook-reconciler.md` — Phase 11b reconciler (preceding phase)
- `docs/REDIS.md` — Redis key registry (stream keys to be added on implementation)
