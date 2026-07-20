# ADR-003: Redis Streams for Durable Run Event Delivery

**Status:** Accepted — Phase 11c-A implemented 2026-07-21
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

### D7: Terminal TTL owned by Python publisher inside Lua — not by Go handler

**Rationale:** The original draft had the Go handler call `EXPIRE` with the final
24-hour TTL when it forwarded the terminal event to the client. This is incorrect.
There are three failure scenarios where the Go handler never executes that EXPIRE:
(a) no Go client is connected when the terminal event is published; (b) the client
disconnects before the terminal event arrives; (c) the bridge pod restarts between
terminal event publish and the EXPIRE call.

The final 24-hour TTL must be applied by the Lua script in the Python publisher,
atomically with the XADD, regardless of whether any Go client is connected. This
is the only path that is guaranteed to execute when the terminal event is published.

The Go handler does not call EXPIRE at all.

### D8: Both TTL operations (safety + final) are inside the same Lua script as XADD

**Rationale:** A process crash or network failure after a successful XADD but
before a separate EXPIRE call leaves the stream key with no expiry — a permanent
Redis memory leak. The EXPIRE must be atomic with the XADD.

Both the safety TTL (48h, on first event) and the final TTL (24h, on terminal
event) are applied inside the same Lua script execution as the XADD. There is no
window between XADD and EXPIRE.

### D9: Transport selection via `events_transport` column on `them.runs` — no timeout fallback

**Rationale:** The original draft used a 2-second `XREAD BLOCK` timeout to check
whether a stream key existed before deciding which transport to use. This is
incorrect: a valid LLM orchestration may take 30+ seconds before its first token.
The timeout would incorrectly lock the connection to Pub/Sub for the entire run.

The correct approach stores `events_transport` as a column on `them.runs` (values:
`'pubsub'` or `'streams'`). The Go handler reads this value once when the run is
created; the column value is stable for the run's entire lifetime. This eliminates
the race window entirely.

Migration: `db/025_events_transport.sql` adds the column with default `'pubsub'`.
The Go bridge sets `'streams'` when `RUN_EVENTS_MODE` is `dual` or `streams`
(Phase 11c-B). Python and the DB default remain `'pubsub'` until Phase 11c-B.

### D10: Centralized terminal event set — single frozenset, single authoritative source

**Rationale:** The set of event types that trigger the final 24-hour TTL must be
defined exactly once in the codebase. Duplicating the list (e.g., once in a
comment, once in a condition, once in a test) creates divergence risk.

The authoritative set is `TERMINAL_EVENT_TYPES` in `app/temporal/activities.py`:

```python
TERMINAL_EVENT_TYPES = frozenset({'done', 'error', 'canceled', 'terminated', 'timed_out'})
```

This is the only place in the Python code where terminal event types are defined.
The Lua script receives `is_terminal` as a pre-computed flag from Python (not the
event type string directly), so the type-to-flag mapping happens in one Python
function (`stream_publish`) that reads from this constant. Tests explicitly cover
all five terminal types and verify no non-terminal type is treated as terminal.

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
