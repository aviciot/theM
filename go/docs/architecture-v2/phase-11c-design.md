# Phase 11c Design: Durable Event Delivery via Redis Streams

**Status:** Design only — not yet implemented
**Author:** aviciot
**Date:** 2026-07-20

---

## Problem

The current run event delivery uses Redis Pub/Sub (`PUBLISH` / `SUBSCRIBE`). This is
at-most-once: a client that disconnects and reconnects misses all events published
during the gap. For long-running LLM orchestrations this means a reconnecting browser
sees a blank stream, forcing the user to reload. There is no replay mechanism.

---

## Approaches considered

### 1. Keep Redis Pub/Sub with reconnect-only (current)

The `runstream.Stream` implementation already handles reconnect with bounded exponential
backoff (6 attempts, 100ms base → 3200ms max). It emits a synthetic `error` event after
exhaustion.

**Pros:** Zero migration cost, already in production.
**Cons:** At-most-once. Events missed during reconnect gap are gone. Unacceptable UX
for runs that take 30+ seconds.

### 2. Redis Streams (XADD / XREAD)

Redis Streams provide an append-only log. Each event is a stream entry with a
monotonic ID (`{millis}-{seq}`). Readers use `XREAD COUNT n STREAMS key $` for live
consumption, or `XRANGE key id +` for replay from a known position.

**Pros:**
- Durable: entries survive client disconnects.
- Replay: client sends `last_event_id` on reconnect; server replays from that position.
- Ordering guaranteed by entry IDs.
- No consumer groups needed for WS/SSE — each connection is its own reader.

**Cons:**
- Memory footprint: each stream entry has overhead (~100 bytes). A run with 1000 token
  events = ~100KB per run. With MAXLEN 10000 per stream, worst case ~1MB per run key.
- Python worker currently uses `PUBLISH`; must be updated or dual-published.
- Key cleanup required: streams do not expire unless `EXPIRE` or `MAXLEN` is set.

### 3. Dual-publish migration

During the transition, the Python worker publishes to **both** the Pub/Sub channel and
the Stream key simultaneously. The Go gateway subscribes to both, deduplicates by
entry ID. Once all clients have migrated to Stream-based replay, the Pub/Sub publish
is removed.

**Pros:** Zero client downtime during migration. Backward compatible.
**Cons:** Double the Redis writes during transition. Code complexity in both Python and Go.

### 4. Client reconnect protocol

Client sends `run_id` and `last_event_id` on reconnect. The server performs:
1. `XRANGE them:dash:run:{runID}:stream {last_event_id+1} +` to replay missed events.
2. Then `XREAD BLOCK 0 STREAMS them:dash:run:{runID}:stream $` for live events.

**Duplicate handling:** Replayed events have the same `id` field as originally sent.
The client should deduplicate on `id` before rendering. A simple JS `Set<string>` of
seen IDs is sufficient.

### 5. Stream retention and TTL

Two options:
- **MAXLEN**: `XADD key MAXLEN ~ 10000 * ...` caps each stream at ~10000 entries.
  For token-heavy runs this may trim early events. Safe default.
- **EXPIRE**: `EXPIRE them:dash:run:{runID}:stream 86400` (24h). Simpler, bounded cost,
  but a long-running run whose stream expires mid-run loses history.

Recommendation: use `MAXLEN ~ 5000` (trim to 5000 entries) per stream key. This bounds
memory while preserving the last ~5000 tokens — more than enough for any replay window.

### 6. Multi-client consumption

Each WS or SSE connection is its own consumer. There is no consumer group. The
connection reads directly with `XRANGE` (replay) then `XREAD BLOCK` (live). This is
intentional — consumer groups add coordination overhead that is not needed here since
each connection is independent.

---

## Recommendation

**Implement Redis Streams with dual-publish, then cut over.**

The safest incremental path:

1. **Phase 11c-A** — Python worker adds dual-publish: continues `PUBLISH` on the
   existing channel AND adds `XADD` to the new stream key with the same payload.
   Stream key: `them:dash:run:{runID}:stream`. Use `MAXLEN ~ 5000`.
   No Go changes yet. Streams are being written but not read.

2. **Phase 11c-B** — Go `runstream.Stream` adds stream-first read path: on new
   connection, attempts `XREAD` from the stream key. Falls back to Pub/Sub if the
   stream key does not exist (backward compat for Python-native runs).
   On reconnect, accepts `last_event_id` from the WS/SSE client and replays via `XRANGE`.

3. **Phase 11c-C** — Remove Pub/Sub publish from Python worker. Remove Pub/Sub
   subscribe from Go gateway. Stream is now the sole delivery mechanism.

4. **Phase 11c-D** — Add stream cleanup job: a goroutine in the reconciler (or a
   separate cron) that runs `DEL them:dash:run:{runID}:stream` for completed runs
   older than 24h. This is a soft cleanup — `MAXLEN` handles the memory bound during
   the run lifetime.

**Why not consumer groups?** Each WS/SSE connection reads independently and has
different last-seen positions. Consumer group semantics (one delivery per message per
group) do not match this pattern.

**Why not Pub/Sub-only with a longer backoff?** Reconnect-only cannot recover events
missed during a network partition or a slow GC pause on the server side. The only
correct solution is a persistent store with replay.

**Python backward compatibility:** During 11c-A and 11c-B, both delivery mechanisms
co-exist. The Go gateway falls back gracefully when a stream key is absent. No
coordination window, no flag day.

**Rollback:** If streams cause unexpected memory pressure, remove `XADD` from Python
and the stream-read path from Go. Both sides revert to Pub/Sub independently.

---

## Operational cost estimate

| Parameter | Value |
|---|---|
| Avg token events per run | ~500 |
| Avg bytes per stream entry | ~150 bytes |
| Memory per active run stream | ~75KB |
| Max concurrent runs (est.) | 100 |
| Peak stream memory footprint | ~7.5MB |

With MAXLEN 5000 and 24h EXPIRE on completed runs, total Redis memory impact is well
within the 384MB Redis container limit.
