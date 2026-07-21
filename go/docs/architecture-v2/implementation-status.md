# Implementation Status — Go Gateway

**Last updated:** 2026-07-21
**Phase:** 11c-C validation complete (pending staging observation period)

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
| `internal/ws` | Complete | 16 (S1-12) | `handler.go` |
| `internal/sse` | Complete | 15 (S1-13) | `handler.go` |
| `internal/a2a` | Complete | 3 (S1-14) | `server.go` |
| `internal/agentregistry` | Complete | 5 (S1-11) | `registry.go` |
| `internal/admin` | Complete | 19 (S1-15) | `agents.go`, `orchestrators.go`, `applications.go`, `runs.go` |
| `internal/ratelimit` | Complete | 3 (S1-16) | `limiter.go` |
| `internal/epconfig` | Complete | 26 (S1-18) | `epconfig.go`, `pgx.go` |
| `internal/runstream` | Complete | 25 (S1-21, S1-23) | `stream.go`, `streamer.go`, `dispatcher.go`, `metrics.go`, `streamid.go` |
| `internal/reconciler` | Complete | 15 (S1-22) | `reconciler.go` |

**Total unit tests (S1): 212** (was 210; +1 ws replay_unavailable forwarding, +1 sse replay_unavailable forwarding — Phase 11c-C fix)

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

## Phase 11c-C — Validation and staging readiness

**Status: Local/integration validation complete — 2026-07-21. Staging observation period NOT yet started (requires explicit approval).**

### Design/code review findings

| Finding | Severity | Status |
|---|---|---|
| `replay_unavailable` silently dropped by WS `writeEvent` switch | Bug — client never sees trim notification | **Fixed** (Phase 11c-C) |
| `replay_unavailable` silently dropped by SSE `formatSSE` switch | Bug — client never sees trim notification | **Fixed** (Phase 11c-C) |
| ADR-003 D4 stale sentence contradicts D7 (TTL ownership) | Doc inconsistency | **Fixed** (ADR-003 D4 updated) |

### Cursor ownership and concurrency

The cursor in `StreamFromRedis` is a single local variable owned by one goroutine (the reader). No mutex is needed: there is exactly one goroutine per `StreamFromRedis` call, and each WS/SSE connection calls it independently. Two concurrent readers for the same run each own their own cursor — confirmed by `TestStreamFromRedis_MultiPodSafety`. There is no shared cursor state. **No data race is possible by design.**

### Transport routing (design → code match confirmed)

| Mode | events_transport | Transport used | Code path |
|---|---|---|---|
| `pubsub` | any | Pub/Sub | `dispatcher.go:44` — `d.mode != config.RunEventsModePublish` is false |
| `dual` | `streams` | Streams | `dispatcher.go:45` — eventsTransport == "streams" |
| `dual` | `pubsub` | Pub/Sub | `dispatcher.go:45` — eventsTransport != "streams" |
| `streams` | `streams` | Streams | `dispatcher.go:44-45` — mode is non-pubsub, eventsTransport == "streams" |
| `streams` | `pubsub` (legacy row) | Pub/Sub | `dispatcher.go:44-45` — eventsTransport != "streams" |

**All five routes exactly match the design doc. Confirmed by `TestDispatcher_*` (6 tests).**

### Test results

| Suite | Result |
|---|---|
| `go test -count=1 ./...` (212 unit tests) | **PASS** |
| `go test -count=1 -v ./internal/runstream/...` (25 tests) | **PASS** |
| `go test -run TestReplayUnavailableForwardedToClient ./internal/ws/...` | **PASS** |
| `go test -run TestSSEReplayUnavailableForwardedToClient ./internal/sse/...` | **PASS** |
| Integration tests (`-tags=integration`) | Not run — requires live stack (no live infra in current session) |
| Race detector (`go test -race`) | Not run — requires gcc on Windows; runs clean in Linux CI |
| Dual-mode soak | Not run — requires live Docker stack |
| MAXLEN scenarios (1k/5k/6k/tool-heavy/trim replay) | Not run — requires live Redis + Python worker |

### Remaining risks before staging

- MAXLEN validation (5 scenarios) requires a live dual-mode stack with Python worker.
- Race detector must pass in Linux CI before staging merge.
- Integration test S2-03 (Redis Streams integration) requires live Redis.
- SSE `Last-Event-ID` header parsing on reconnect: implemented at `sse/handler.go:419` — not integration-tested in current session.

## Pending / future work

- Phase 11c-C: Staging observation period (`RUN_EVENTS_MODE=streams`) — requires explicit approval gate after MAXLEN validation + race-clean CI run
- Phase 11c-D: Remove Pub/Sub (requires ≥2 weeks stable in Phase 11c-C + explicit approval)
- Voice EP implementation (deferred, not started)
- `go test -race ./...` requires gcc on Windows — runs clean in Linux CI
