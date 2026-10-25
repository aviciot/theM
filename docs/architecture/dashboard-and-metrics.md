---
title: Dashboard Live Feed and Metrics
type: architecture
status: current
owns:
  - go/internal/dashboard/
  - go/internal/metrics/
  - go/internal/admin/observability.go
  - go/internal/admin/services_stats.go
last_verified: 2026-10-25
---

# Dashboard Live Feed and Metrics

## Purpose
Three separate "what is happening" mechanisms. Do not mix them up:
1. **`/ws/dashboard`** — live feed for the admin UI (sessions, scans, run traces).
2. **Prometheus `/metrics`** — process/gate/connection counters for operators.
3. **Daily per-tenant usage counters in Redis** — feed the Observability pages.

## How it works

### 1. `/ws/dashboard` (`go/internal/dashboard/`)
- Auth: dashboard HS256 JWT only (`auth.ValidateHS256JWT`); the tenant comes from the token. Any role passes.
- The client's first message is `{"type":"subscribe","channels":[...]}`. Valid channels (`IsValidChannel`): static `runs`, `agents`, `metrics`, `apps`, `services:stats`, plus prefixes `run:{id}`, `agent:{id}`, `sessions:{appId}`, `scan:{id}`.
- Each channel maps to Redis pub/sub `them:dash:{channel}`. On subscribe the server first sends a **snapshot** (agent/scan state `them:{tenantId}:scan:state:{id}`, sessions hash `them:dash:sessions:state:{appId}`), then relays live events; a `ping` is sent every 30 s.
- `run:{id}` is special: nothing publishes it. The server tails the run's Redis **Stream** (`them:dash:run:{id}:stream`, see `run-lifecycle.md`) with replay + live, after `RunOwnershipChecker` confirms the run belongs to the caller's tenant (refused if no checker is configured).
- `SessionPublisher` (`publisher.go`) publishes session start/end and keeps the sessions state hash.

### 2. Prometheus (`go/internal/metrics/metrics.go`)
Served unauthenticated at `/metrics` by the bridge (`server.go`). Series: `them_active_sessions`, `them_sessions_started/ended_total`, `them_gate_admissions/rejections_total`, `them_event_bus_dropped/coalesced_total`, `them_active_ws/sse_connections`, `them_run_status_update_failed_total`, `them_graceful_drain_duration_seconds`, `them_audit_write_errors_total`.

### 3. Daily usage counters (`metrics/recorder.go`)
`RedisRecorder` writes, with a 32-day TTL: hash `them:metrics:{tenant}:{date}` and `them:metrics:{tenant}:app:{app}:{date}` (fields `runs`, `tokens_in`, `tokens_out`, `mcp_calls`) and HyperLogLog `them:metrics:{tenant}:users:{date}` (unique users). Writers: `runrecorder` (runs, tokens), `mcp/executor.go` (MCP calls), `ws`/`sse` handlers (users). Readers: `admin/observability.go` (`/observability/summary`, `/observability/tenant/{id}/apps`) merge **30-day DB aggregates** with **today's Redis counters**. `GET /services/stats` (`services_stats.go`) reports security-service/worker status.

## Key invariants
- Metrics writes are fire-and-forget; a failure only logs a warning and never fails a run.
- Redis counters are today-only and expire; history comes from Postgres.
- Run traces are tenant-checked before tailing.

## Failure modes
- Redis down → no live feed and no today numbers; DB history still works.
- The main Redis is `allkeys-lru` (see `redis-usage.md`): counters and session state can be evicted under memory pressure.
- Pub/sub is fire-and-forget: a client that connects late only sees the snapshot, not earlier events.

## Known gaps
Backlog F-114…F-117: live pub/sub channels are not tenant-scoped, `CheckOrigin` allows any origin, `/metrics` is unauthenticated, and session/usage counters sit in an evictable Redis.

## Where to change things
New channel → `IsValidChannel` + `sendSnapshots` in `dashboard/handler.go` (add a tenant check). New counter → `metrics.go` (Prometheus) or `recorder.go` (daily Redis, then `redis-generated.md`). Tests: `go test ./internal/dashboard/... ./internal/metrics/...`.
