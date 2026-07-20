# Implementation Status — Go Gateway

**Last updated:** 2026-07-21
**Phase:** 11c-B complete (Go stream-read/replay behind RUN_EVENTS_MODE)

---

## Package inventory

| Package | Status | Tests | Key files |
|---|---|---|---|
| `cmd/them` | Complete | — (no unit tests; wired in integration) | `main.go` |
| `internal/config` | Complete | 14 (S1-01) | `config.go`, `config_test.go` |
| `internal/db` | Complete | — | `db.go` |
| `internal/cache` | Complete | 2 (S1-19, S1-20) | `cache.go`, auth/runstream/runstreamer adapters |
| `internal/telemetry` | Complete | — | `telemetry.go` |
| `internal/health` | Complete | 5 (S1-02) | `health.go` |
| `internal/server` | Complete | 4 (S1-03) | `server.go` |
| `internal/auth` | Complete | 14 (S1-04, S1-05) | `jwt.go`, `token_cache.go`, `middleware.go` |
| `internal/gate` | Complete | 16 (S1-17) | `gate.go` |
| `internal/session` | Complete | 7 (S1-06) | `session.go` |
| `internal/event` | Complete | 6 (S1-07) | `bus.go` |
| `internal/domain` | Complete | 3 (S1-08) | `domain.go` |
| `internal/runrecorder` | Complete | 8 (S1-09) | `recorder.go` (events_transport per RUN_EVENTS_MODE) |
| `internal/llm` | Complete | 6 (S1-10) | `provider.go`, `anthropic.go`, `mock.go` |
| `internal/orchestrator` | Complete | — | `orchestrator.go` |
| `internal/temporal` | Complete | 8 (S2-02) | `workflow.go`, `activities.go`, `client.go`, `signaler.go` |
| `internal/ws` | Complete | 15 (S1-12) | `handler.go` |
| `internal/sse` | Complete | 14 (S1-13) | `handler.go` |
| `internal/a2a` | Complete | 3 (S1-14) | `server.go` |
| `internal/agentregistry` | Complete | 5 (S1-11) | `registry.go` |
| `internal/admin` | Complete | 19 (S1-15) | `agents.go`, `orchestrators.go`, `applications.go`, `runs.go` |
| `internal/ratelimit` | Complete | 3 (S1-16) | `limiter.go` |
| `internal/epconfig` | Complete | 26 (S1-18) | `epconfig.go`, `pgx.go` |
| `internal/runstream` | Complete | 25 (S1-21, S1-23) | `stream.go`, `streamer.go`, `dispatcher.go`, `metrics.go`, `streamid.go` |
| `internal/reconciler` | Complete | 15 (S1-22) | `reconciler.go` |

**Total unit tests (S1): 210** (was 192; +1 config RUN_EVENTS_MODE, +2 runrecorder events_transport, +15 runstream streamer/dispatcher)

---

## Phase 11b — Temporal run reconciler

**Status: Complete — controlled write activation validated 2026-07-20**

The reconciler is fully operational:

- DryRun mode controlled by `RECONCILER_DRY_RUN` env var (default `true`)
- Config field `ReconcilerDryRun bool` wired through `config.Load()`
- 4 new unit tests cover: missing env var, `"true"`, `"false"`, invalid value → all fail-safe to `true`
- Controlled write activation on 2026-07-20:
  - 37 stale running rows in DB before
  - 30 rows reconciled to `completed` in first sweep
  - 0 errors, 0 invalid statuses, 0 idempotency violations
  - Rollback confirmed: `updated_total` stopped increasing after revert to `true`

---

## Route map

| Route | Handler | Auth |
|---|---|---|
| `GET /health/live` | `internal/health` | None |
| `GET /health/ready` | `internal/health` | None |
| `GET /metrics` | prometheus | None |
| `GET /ws/apps/{slug}/{ep_slug}` | `internal/ws` | Token or public |
| `GET /sse/apps/{slug}/{ep_slug}` | `internal/sse` | Token or public |
| `POST /a2a/message` | `internal/a2a` | Bearer token |
| `GET /.well-known/agent-card.json` | `internal/a2a` | None |
| `GET /api/v1/admin/*` | `internal/admin` | JWT super-admin |
| `GET /api/v1/runs/*` | `internal/admin` | JWT super-admin |

---

## Critical findings addressed

| Finding | Package | Status |
|---|---|---|
| #1 Ghost-set bug | `internal/session` | Fixed (atomic Lua SREM+DEL) |
| #5 DB-level history LIMIT | `internal/orchestrator` | Fixed |
| #8 Typed tool definitions | `internal/llm` | Fixed |
| #9 Streaming cancellation | `internal/llm` | Fixed |
| #10 Temporal HITL signal | `internal/temporal` | Fixed |
| #11 Reconciler safe NotFound | `internal/reconciler` | Fixed |
| #12 RECONCILER_DRY_RUN env var | `internal/config` | Fixed (Phase 11b) |

---

## Phase 11c-A — Python atomic dual-publish (Python-only, no Go changes)

**Status: Complete — 2026-07-21**

Python-only infrastructure for Redis Streams event delivery. No Go changes.

| Artifact | Description |
|---|---|
| `theM_gateway/db/025_events_transport.sql` | Adds `events_transport TEXT NOT NULL DEFAULT 'pubsub'` to `them.runs` |
| `theM_gateway/app/temporal/stream_publish.lua` | Lua script: atomic XADD + PUBLISH + EXPIRE in single round-trip |
| `theM_gateway/app/temporal/activities.py` | Replaced all `:tokens` PUBLISH calls with `stream_publish()`; added `TERMINAL_EVENT_TYPES` frozenset |
| `theM_gateway/scripts/tests/run_tests.py` | test_36 — structural + unit tests via fakeredis |

**Default behavior:** `events_transport='pubsub'` (unchanged) until Go is updated in Phase 11c-B. The Python worker now writes to both the Redis Stream (`them:dash:run:{runID}:stream`) and the legacy Pub/Sub channel (`them:dash:run:{runID}:tokens`) atomically. Go continues to read from Pub/Sub as before.

---

## Phase 11c-B — Go stream-read/replay behind RUN_EVENTS_MODE

**Status: Complete — 2026-07-21**

Go gateway now reads run events from Redis Streams (with XRANGE replay + live XREAD)
behind a new `RUN_EVENTS_MODE` flag. Pub/Sub is untouched and remains the default.

| Artifact | Description |
|---|---|
| `internal/config/config.go` | `RunEventsMode` type + `RUN_EVENTS_MODE` parsing (`dual`/`streams`/else→`pubsub`); added to `SafeString` |
| `internal/domain/domain.go` | `Run.EventsTransport` field |
| `internal/runrecorder/recorder.go` | `WithRunEventsMode`; `CreateRun` writes `events_transport` (`streams` in dual/streams, else `pubsub`) |
| `internal/runstream/streamer.go` | `StreamFromRedis` — XRANGE replay → continuous-cursor XREAD live; trim detection → `replay_unavailable` |
| `internal/runstream/dispatcher.go` | `Dispatcher` — picks Pub/Sub vs Streams from mode × `events_transport` |
| `internal/runstream/metrics.go` | 6 Prometheus metrics + `SetModeGauge` |
| `internal/runstream/streamid.go` | stream-ID compare / exclusive-predecessor helpers |
| `internal/cache/runstreamer_adapter.go` | rueidis-backed `RedisStreamer` (XRange/XRangeN/XRead) |
| `internal/ws/handler.go`, `internal/sse/handler.go` | `WithRunEvents`; stamp `events_transport` on new run; route via dispatcher; WS `last_event_id` / SSE `Last-Event-ID` resume |
| `cmd/them/main.go` | build dispatcher once, inject into WS+SSE, `SetModeGauge`, log mode |
| `theM_gateway/docker-compose.integration.yml`, `docker-compose.soak.yml` | `RUN_EVENTS_MODE=dual` (production compose left at default `pubsub`) |

**Transport selection (mode × events_transport):**
- `pubsub` mode → always Pub/Sub (events_transport ignored)
- `dual`/`streams` mode → per-run `events_transport`: `streams`→Streams, `pubsub`→Pub/Sub (legacy rows never forced onto Streams)

**Replay→live cursor:** cursor starts at `last_event_id` (or `0-0`), XRANGE replays from `(cursor` to `+`, then XREAD BLOCK resumes from the last replayed entry ID (never `$`) — no gap, no overlap.

---

## Pending / future work

- Phase 11c-C: Staging cutover to `RUN_EVENTS_MODE=streams` + MAXLEN validation (requires explicit approval gate)
- Phase 11c-D: Remove Pub/Sub (requires ≥2 weeks stable in Phase 11c-C + explicit approval)
- Voice EP implementation (deferred, not started)
- `go test -race ./...` requires gcc on Windows — runs clean in Linux CI
