# Implementation Status — Go Gateway

**Last updated:** 2026-08-15 (Agent actions migrated; Python auth removed; AdminTenantMiddleware)

---

## Current State

Phase R-4d complete (2026-08-01): Runtime tenant propagation — TenantID from EPConfig written to them.runs.tenant_id and WorkflowInput; activity boundary validation (non-retryable errors for empty TenantID/ApplicationID/RunID). 13 new tests. 452 total Go tests. HEAD pending commit.
Compose consolidation complete (2026-08-01): Production Compose deployment migrated from `theM_gateway/` to canonical root `/home/avi/them`. All 15 services running. Go Workers now Compose-managed under `project=them_gateway`. See `COMPOSE_CONSOLIDATION_EXECUTION_REPORT.md`.
Phase R-4c2 complete (2026-08-01): BearerTenantMiddleware wired to tenant-scoped admin routes; bootstrapTenantID shim removed. 480 total Go tests. HEAD `3efb097`.
Phase R-4c1 complete (2026-07-31): Tenant-scoped DAL and service layers. 468 total Go tests. HEAD `09c5665`.
Phase R-4b complete (2026-07-31): Authenticated tenant identity foundation — `tenantctx` package, TenantID in `Claims`/`TokenInfo`/`TokenRow`, `pgx_querier` fetches `tenant_id`, `BearerTenantMiddleware`/`HS256TenantMiddleware`, `RuntimeIdentity` struct. 23 new tests. 447 total. HEAD `a95e859`.
Phase R-4a complete (2026-07-31): Tenant database foundation — `them.tenants` table, `tenant_id UUID NOT NULL` on 7 tables, bootstrap tenant, constraint migration, `them.run_artifacts` with tenant_id. DB-only change; no Go/Python application code changed. HEAD `0056d95`.
Phase R-3 complete (2026-07-29): File artifact delivery — DB schema, recorder, handler, orchestrator wiring. 22 new tests. HEAD `ac12082`.
Phase R-2 complete (2026-07-28): Go Temporal worker registered; inline execution path removed; orchestrator feature parity achieved. 11 new tests. HEAD `029bf8c`.
Phase R-1 complete (2026-07-28): Prometheus metrics + structured logging in WS/SSE handlers + drain observability. 12 new tests in `internal/metrics/`.
Wave 7 complete (2026-07-26): llm-providers CRUD (GET/POST/PATCH/DELETE) now served by Go.
Wave 6 complete (2026-07-25): monitoring-config + llm-providers/routing/config now served by Go.
Wave 5 complete (2026-07-24): tokens + sessions admin routes served by Go.
Phase 11c-C validation complete (2026-07-21). 229 unit tests pass. Race detector clean in Linux CI.

---

## Package Inventory

| Package | Status | Tests | Key files |
|---|---|---|---|
| `cmd/them` | Complete | — (no unit tests; wired in integration) | `main.go` |
| `internal/config` | Complete | 14 (S1-01) | `config.go`, `config_test.go` |
| `internal/db` | Complete | — | `db.go` |
| `internal/cache` | Complete | 2 (S1-19, S1-20) | `cache.go`, auth/runstream/runstreamer adapters |
| `internal/telemetry` | Complete | — | `telemetry.go` |
| `internal/health` | Complete | 5 (S1-02) | `health.go` |
| `internal/server` | Complete | 4 (S1-03) | `server.go` |
| `internal/auth` | Complete (R-4b) | 37 (S1-04, S1-05, S1-31) | `jwt.go`, `token_cache.go`, `middleware.go`, `pgx_querier.go` — TenantID in Claims/TokenInfo, tenant middleware |
| `internal/tenantctx` | Complete (R-4b) | 8 (S1-32) | `tenantctx.go` — typed context key, ErrNoTenant, ErrInvalidTenant |
| `internal/gate` | Complete | 16 (S1-17) | `gate.go` |
| `internal/session` | Complete | 7 (S1-06) | `session.go` |
| `internal/event` | Complete | 6 (S1-07) | `bus.go` |
| `internal/domain` | Complete | 3 (S1-08) | `domain.go` |
| `internal/runrecorder` | Complete (R-4d) | 21 (S1-09) | `recorder.go` — events_transport, RecordArtifact (1MiB limit), GetArtifact, sanitizeFilename, tenant_id propagation |
| `internal/artifacts` | Complete (R-3) | 9 (S1-30) | `handler.go` — bearer-token auth, RFC 5987 Content-Disposition, safe content-type allow-list |
| `internal/llm` | Complete | 6 (S1-10) | `provider.go`, `anthropic.go`, `mock.go` |
| `internal/orchestrator` | Complete (R-3) | 10 (S1-28) | `orchestrator.go` — checkpoints, budget, parallel fan-out, A2A discovery, artifact recording + metadata-only event emission |
| `internal/temporal` | Complete (R-4d) | 10 (S2-02, S1-29) | `workflow.go`, `activities.go`, `client.go`, `signaler.go`, `worker_test.go` — TenantID+ApplicationID in WorkflowInput, activity boundary validation |
| `internal/ws` | Complete (R-4d) | 19 (S1-12) | `handler.go` — inline path removed, Temporal unconditional, tenant propagation from EPConfig |
| `internal/sse` | Complete (R-4d) | 18 (S1-13) | `handler.go` — inline path removed, Temporal unconditional, tenant propagation from EPConfig |
| `internal/a2a` | Complete | 3 (S1-14) | `server.go` |
| `internal/agentregistry` | Complete | 5 (S1-11) | `registry.go` |
| `internal/admin` | Complete | 40 (S1-15) | `agents.go`, `orchestrators.go`, `applications.go`, `runs.go`, `monitoring.go`, `llm_routing.go` |
| `internal/admin/dal` | Complete | — (covered by admin tests) | `dal.go`, `agents.go`, `orchestrators.go`, `applications.go`, `runs.go`, `tokens.go`, `config.go` |
| `internal/metrics` | Complete (Phase R-1) | 12 (S1-27) | `metrics.go` — all 10 Prometheus metrics + cardinality enforcement |
| `internal/admin/service` | Complete | 10 (S1-25 config) | `service.go`, `tokens.go`, `sessions.go`, `config.go` |
| `internal/transport` | Complete (R-4b) | — (covered by ws/sse tests) | `transport.go` — `RuntimeIdentity` struct added |
| `internal/ratelimit` | Complete | 3 (S1-16) | `limiter.go` |
| `internal/epconfig` | Complete | 26 (S1-18) | `epconfig.go`, `pgx.go` |
| `internal/runstream` | Complete | 25 (S1-21, S1-23) | `stream.go`, `streamer.go`, `dispatcher.go`, `metrics.go`, `streamid.go` |
| `internal/reconciler` | Complete | 15 (S1-22) | `reconciler.go` |

**Total unit tests (S1): 212** (was 210 at Phase 11c-B; +1 ws replay_unavailable forwarding, +1 sse replay_unavailable forwarding — Phase 11c-C fix; Wave 5 handler tests tracked separately in go/TEST_INDEX.md)

---

## Route Map (post-Wave 5, as routed through Traefik)

For full Traefik priority details see `REMAINING_ROUTE_OWNERSHIP_INVENTORY.md`.

| Route | Handler | Auth | Owner |
|---|---|---|---|
| `GET /health/live` | `internal/health` | None | Go |
| `GET /health/ready` | `internal/health` | None | Go |
| `GET /metrics` | prometheus | None | Go |
| `GET /ws/apps/{slug}/{ep_slug}` | `internal/ws` | Token or public | Go |
| `GET /sse/apps/{slug}/{ep_slug}` | `internal/sse` | Token or public | Go |
| `GET,POST /ws/orchestrate/{app}/{ep}` | `internal/ws` | Token or public | Go |
| `GET,POST /sse/orchestrate/{app}/{ep}` | `internal/sse` | Token or public | Go |
| `POST /a2a/message` | `internal/a2a` | Bearer token | Go |
| `GET /.well-known/agent-card.json` | `internal/a2a` | None | Go |
| `GET /api/v1/admin/agents*` | `internal/admin` | JWT super-admin | Go |
| `POST/PUT/DELETE /api/v1/admin/agents*` | `internal/admin` | JWT super-admin | Go |
| `GET /api/v1/admin/orchestrators*` | `internal/admin` | JWT super-admin | Go |
| `POST/PUT/DELETE /api/v1/admin/orchestrators*` | `internal/admin` | JWT super-admin | Go |
| `GET /api/v1/admin/applications*` | `internal/admin` | JWT super-admin | Go |
| `POST/PUT/DELETE /api/v1/admin/applications*` | `internal/admin` | JWT super-admin | Go |
| `ALL /api/v1/admin/tokens*` | `internal/admin` (Wave 5) | JWT super-admin | Go |
| `ALL /api/v1/admin/sessions*` | `internal/admin` (Wave 5) | JWT super-admin | Go |
| `GET,PUT /api/v1/admin/monitoring-config` | `internal/admin` (Wave 6) | JWT super-admin | Go |
| `GET,PUT /api/v1/admin/llm-providers/routing/config` | `internal/admin` (Wave 6) | JWT super-admin | Go |
| `ALL /api/v1/admin/llm-providers*` (excl. /routing/config) | `internal/admin` (Wave 7) | JWT super-admin | Go |
| `GET /api/v1/runs*` | `internal/admin` | JWT super-admin | Go |
| `POST /api/v1/runs/{run_id}/signal` | `internal/admin` | JWT | Go |
| `GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` | `internal/artifacts` (R-3) | Bearer token | Go |
| All other `/api/v1/*` | Python bridge | — | Python |

**Python still owns:** orchestrator test-llm/voice/tts, application import/export/restore/middleware-wirings, per-app orchestrator test-llm/voice/tts, runs stats/contexts/tasks/artifacts/cancel/delete/bulk-delete, dashboard WS `/ws/dashboard`, one-segment `/ws/orchestrate/{name}`, A2A server `/a2a/*`.

**Go now owns (as of 2026-08-15):** all of `/api/v1/auth/*` (via `them-auth-go`), agent discover + test + security-scan (p=116), agents/orchs/apps/entry-points full CRUD, tokens, sessions, LLM providers, monitoring-config, runtime + bulk-delete, WS/SSE.

**AdminTenantMiddleware:** Admin routes use JWT-based tenant resolution (not opaque bearer tokens). Super_admin users without `tenant_id` claim fall back to bootstrap tenant `00000000-0000-0000-0000-000000000001`.

---

## Phase 11b — Temporal run reconciler

**Status: Complete — controlled write activation validated 2026-07-20**

- DryRun mode controlled by `RECONCILER_DRY_RUN` env var (default `true`)
- Config field `ReconcilerDryRun bool` wired through `config.Load()`
- 4 unit tests cover: missing env var, `"true"`, `"false"`, invalid value → all fail-safe to `true`
- Controlled write activation on 2026-07-20: 37 stale running rows → 30 reconciled to `completed`, 0 errors

---

## Phase 11c-A — Python atomic dual-publish (Python-only, no Go changes)

**Status: Complete — 2026-07-21**

| Artifact | Description |
|---|---|
| `db/025_events_transport.sql` | Adds `events_transport TEXT NOT NULL DEFAULT 'pubsub'` to `them.runs` |
| `app/temporal/stream_publish.lua` | Lua script: atomic XADD + PUBLISH + EXPIRE in single round-trip |
| `app/temporal/activities.py` | Replaced all `:tokens` PUBLISH calls with `stream_publish()`; added `TERMINAL_EVENT_TYPES` frozenset |
| `scripts/tests/run_tests.py` | test_36 — structural + unit tests via fakeredis |

Python publishes to both the Redis Stream (`them:dash:run:{runID}:stream`) and legacy Pub/Sub (`them:dash:run:{runID}:tokens`) atomically. Go continues to read from Pub/Sub as before. Default `events_transport='pubsub'` until Phase 11c-B.

---

## Phase 11c-B — Go stream-read/replay behind RUN_EVENTS_MODE

**Status: Complete — 2026-07-21**

Go gateway reads run events from Redis Streams (XRANGE replay + live XREAD) behind `RUN_EVENTS_MODE` flag. Pub/Sub is the default.

**Transport selection (mode × events_transport):**
- `pubsub` mode → always Pub/Sub
- `dual`/`streams` mode → per-run `events_transport`: `streams`→Streams, `pubsub`→Pub/Sub (legacy rows never forced onto Streams)

**Replay→live cursor:** XRANGE replays from `(cursor` to `+`, then XREAD BLOCK resumes from the last replayed ID — no gap, no overlap.

---

## Phase 11c-C — Validation and staging readiness

**Status: Full local + live integration validation complete — 2026-07-21. Staging observation period NOT yet started (requires explicit approval).**

Bugs fixed in this phase:
- `replay_unavailable` silently dropped by WS `writeEvent` switch — **Fixed**
- `replay_unavailable` silently dropped by SSE `formatSSE` switch — **Fixed**
- ADR-003 D4 doc inconsistency — **Fixed**

All five transport routing combinations confirmed by `TestDispatcher_*` (6 tests). Full MAXLEN and reconnect validation passed. See `docs/archive/migrations/phase-11c-design.md` for detailed validation results (historical record).

---

## Wave 5 — Admin Tokens + Sessions (complete, 2026-07-24)

Routes `/api/v1/admin/tokens*` and `/api/v1/admin/sessions*` migrated to Go.
Detailed implementation: archived in `archive/migrations/WAVE5_PLAN.md`. Current state: `CURRENT.md`.

---

## Architectural Findings Fixed

| Finding | Severity | Fixed in | How |
|---|---|---|---|
| Ghost session accumulation — Set members without TTL | Critical | Phase 4 (session) + Phase 9 (gate) | Atomic Lua shadow-key pattern: SADD + SET…EX shadow; luaPruneAndCount on each admission; SREM+DEL on End |
| Duplicate SADD failure window | Architecture | Phase 9 (gate) | Gate is sole owner of Set membership; SessionManager owns Hash only |
| Subscribe-after-publish race (events lost) | Critical | Phase 5 (ws, sse) | Subscribe to bus BEFORE starting orchestrator goroutine |
| Provider format leaks into DB | High | Phase 5 (domain) | Canonical domain.Message type; all providers translate at boundary |
| Hardcoded 0 session count in pod heartbeat | High | Phase 4 (session) | `atomic.LoadInt32(&activeSessions)` |
| No DB-level LIMIT on history load | Medium | Phase 6 (orchestrator) | HistoryLoader interface with DB-level LIMIT parameter |
| Single-level token cache (no Redis L2) | Medium | Phase 2 (auth) | Two-level cache: in-process L1 + Redis L2 + pub/sub eviction |
| Rate limiting not replica-safe | Medium | Phase 8 (ratelimit) | Redis INCR with minute-bucket keys |
| Admin mutations don't invalidate caches | Medium | Phase 8 (admin) | Every mutation calls CacheInvalidator.Del on affected keys |
| No HITL support | Low | Phase 7 (temporal) | Temporal SignalWorkflow via admin runs signal endpoint |
| #11 Reconciler safe NotFound | — | Phase 11b | No DB write on Temporal 404; warn + metric instead |
| #12 RECONCILER_DRY_RUN env var | — | Phase 11b | `getEnvBoolSafe` defaults to true; any invalid value is safe |

---

## Build and Test Status

```
go build ./...                          PASS
go test ./...                           PASS (212 unit tests across all packages)
go test -tags=integration ./...        PASS (live Postgres + Redis required)
go test -race ./...                     PASS in Linux CI (requires GCC — not available on Windows)
```

**Integration test non-standard ports** (to avoid conflicts with local dev services):
- Postgres: `15432:5432`, Redis: `16379:6379`, Temporal: `17233:7233`, Go bridge: `8002`

**Smoke test:** `scripts/smoke_test_go_gateway.py --token <tok> --app <slug> --ep <slug>`

---

## Pending / Future Work

- Phase 11c-C: Staging observation period (`RUN_EVENTS_MODE=streams`) — requires explicit approval gate
- Phase 11c-D: Remove Pub/Sub (requires ≥2 weeks stable in Phase 11c-C + explicit approval)
- Voice EP implementation (deferred, not started)
- Wave 6: next batch of Python admin routes to migrate (scope TBD)
