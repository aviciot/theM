# Events System — Current State
**Date:** 2026-10-21  
**Branch:** main  
**HEAD:** 69d87eb9

## Summary

Event observability system is operational with service-specific mode validation and verified identity attribution. Auth and Tools services emit to ClickHouse, Gateway emits to both Postgres (authoritative) and ClickHouse (mirror) in dual mode.

## Configuration

### Event Modes (THE_M_EVENTS_* environment variables)

| Mode | Behavior | Restrictions |
|------|----------|--------------|
| `postgres` (default) | Postgres writes only, no event emission to Redis Streams | None |
| `dual` | Postgres (authoritative) + ClickHouse (mirror via Redis Streams) | Requires REDIS_EVENTS_HOST |
| `clickhouse` | ClickHouse only via Redis Streams | **Gateway-only restriction:** Blocked until budget queries + request body storage complete. Auth/Tools: allowed (no PG tables yet) |

### Current Service Modes

| Service | Env Var | Mode | Write Destination | Status |
|---------|---------|------|-------------------|--------|
| Gateway | THE_M_EVENTS_GATEWAY | `dual` | PG `gateway_requests` (authoritative) + CH (mirror) | ✓ Running |
| Auth | THE_M_EVENTS_AUTH | `clickhouse` | CH only (no PG table) | ✓ Running |
| MCP Tools | THE_M_EVENTS_TOOLS | `clickhouse` | CH only (no PG table) | ✓ Running |

### Event Infrastructure

| Component | Role | Config | Status |
|-----------|------|--------|--------|
| them-redis-events | Event queue (Redis Streams), AOF, noeviction | Port 6379 (internal), `profiles: [events]` | ✓ Running (orphan warning, safe) |
| them-clickhouse | Event store | Port 8123/9000 (internal), `profiles: [events]` | ✓ Running (orphan warning, safe) |
| them-event-writer | Consumer: reads Redis Streams → writes ClickHouse, handles recovery + retention | `profiles: [events]` | ✓ Running (orphan warning, safe) |

**Orphan warning:** These containers use `profile: events` which isn't in the current compose command, so Docker warns they're "orphan". They're actually running and functional — warning is cosmetic.

## Event Types and Schema

### Gateway Request Events
- **Kind:** `gateway_request`
- **Postgres:** `them.gateway_requests` (tenant_id, client_id, provider, model_requested, model_served, status, tokens_in, tokens_out, cost_usd, latency_ms, ttfb_ms, streamed)
- **ClickHouse:** `events.events` table (same fields in `gateway_request` JSON column)
- **Mode:** `dual` — Postgres authoritative, ClickHouse mirror
- **Safety:** PG write failure blocks request; CH mirror failure logged only

### User Activity Events (Auth)
- **Kind:** `user_activity`
- **Postgres:** None (no table yet)
- **ClickHouse:** `events.events` table (user_id, action, resource_type, outcome, ip, user_agent in `user_activity` JSON column)
- **Mode:** `clickhouse` — CH only
- **Actions:** `login`, `logout`, `token_refresh`
- **Identity:** Verified from JWT (user_id, tenant_id populated from signature-verified claims)
- **Security:** Forged tokens recorded as `user_id="unknown"`, `outcome="unverified"`

### Tool Call Events (MCP)
- **Kind:** `tool_call`
- **Postgres:** None (no table yet)
- **ClickHouse:** `events.events` table (tool_name, server_slug, status, latency_ms, error in `tool_call` JSON column)
- **Mode:** `clickhouse` — CH only

## Validation and Safety

### Mode Validation (go/internal/events/mode.go)
- **ValidateEventsMode()**: General validation — allows `postgres`, `dual`, `clickhouse`
- **ValidateGatewayEventsMode()**: Gateway-specific — **blocks** `clickhouse` until budget queries + request body storage complete
- **RequireRedisForMode()**: Requires REDIS_EVENTS_HOST for `dual` and `clickhouse` modes
- **Effect:** Services reject unknown modes at startup; Gateway explicitly blocks incomplete clickhouse-only mode

### Auth Identity Verification (go/internal/authserver/)
- **service.go Logout()**: Returns `*verifiedClaims` (nil when token invalid/forged/expired)
- **handlers.go emitUserActivityWithIdentity()**: Uses verified user_id and tenant_id from JWT
- **Security guarantee:** Forged tokens cannot claim arbitrary identity in event logs
- **Test coverage:** 5 tests (valid, expired, forged, malformed, empty tokens) — all pass

### Tests Written
✓ Auth identity verification (5 tests, all pass):
  - Valid token → returns verified claims
  - Expired token → returns nil
  - Forged token (wrong secret) → returns nil
  - Malformed tokens → returns nil
  - Empty token → returns nil

## Current State Checklist

- [x] Mode validation implemented and tested
- [x] Gateway blocks clickhouse-only mode
- [x] Auth/Tools allow clickhouse mode
- [x] Auth identity uses verified JWT claims
- [x] Unverified logout recorded explicitly
- [x] Event sinks active for all three services
- [x] Services built and running
- [x] Auth identity tests written and passing
- [ ] Mode validation tests (pending — task #15)
- [ ] Mirror failure test (pending — task #16)
- [ ] Tenant attribution test (pending — task #17)
- [ ] Forged token event test (pending — task #18)
- [ ] PG-ClickHouse reconciliation (pending — task #9)

## Known Gaps

### Missing Tests
1. **Mode validation tests** — verify startup rejection of invalid modes
2. **Mirror failure test** — confirm PG write succeeds when CH sink fails in dual mode
3. **Tenant attribution test** — verify tenant_id populated from verified JWT
4. **Forged token event test** — verify event records `user_id="unknown"` for forged logout

### Missing Verification
5. **PG-ClickHouse reconciliation** — Use Performance Test Runner to:
   - Generate auth events (login/logout/refresh)
   - Generate gateway requests (if dual mode)
   - Compare PG and CH records by request_id
   - Field-level comparison (tenant_id, tokens_in, tokens_out, cost_usd)
   - Report missing, unexpected, and differing records

### Future Work (Phase 2 — Not Blocking)
6. **Gateway budget queries** — SumMonthlySpend() and related queries must work with CH-only data
7. **Request body storage** — DEBUG/AUDIT logging levels need CH equivalent of `gateway_request_bodies`
8. **Auth events PG table** — Add `user_activity` table to Postgres for dual-mode support
9. **Tool events PG table** — Add `tool_calls` table to Postgres for dual-mode support

## File Locations

| File | Purpose |
|------|---------|
| `go/internal/events/mode.go` | Mode validation functions |
| `go/cmd/them/main.go` | Gateway event sink (dual mode) |
| `go/cmd/auth-server/main.go` | Auth event sink (clickhouse mode) |
| `go/cmd/mcp-service/main.go` | MCP event sink (clickhouse mode) |
| `go/internal/authserver/service.go` | Logout() returns verified claims |
| `go/internal/authserver/handlers.go` | emitUserActivityWithIdentity() |
| `go/internal/authserver/auth_identity_test.go` | Identity verification tests |
| `docker-compose.events.yml` | Event infrastructure (CH, Redis, event-writer) |
| `Dockerfile.go` | Bridge build (excludes event-writer tests) |

## Recent Commits

| Commit | Summary |
|--------|---------|
| 69d87eb9 | test(auth): add identity verification tests for Logout |
| ad8eec52 | feat(auth): populate user activity events with verified JWT identity |
| 8ea4ee55 | fix(events): service-specific mode validation and clickhouse sink activation |
| 950e689c | fix: add missing log/slog import in auth-server |
| fe27894a | Merge reliability/events-safety-fixes |
| 42fdcc16 | docs: events safety fixes status |

## Next Steps

1. Write remaining tests (mode validation, mirror failure, tenant attribution, forged event)
2. Run all tests and verify pass
3. Generate test events (login/logout/refresh, gateway requests if dual)
4. Reconcile PG-ClickHouse records using Performance Test Runner
5. Report actual results with evidence (missing/unexpected/differing records)
6. Update this document with reconciliation results
7. Commit and push final verification report

## Commands

### Start Event Stack
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml -f docker-compose.events.yml --profile events up -d
```

### Check Event Services
```bash
docker logs them-event-writer --tail 50
docker logs them-auth-go | grep "events sink"
docker logs them-mcp-service | grep "events sink"
```

### Verify Auth Health
```bash
docker exec them-auth-go wget -qO- http://localhost:8703/health
```

### Check Redis Streams
```bash
docker exec them-redis-events redis-cli XINFO STREAM them:events
docker exec them-redis-events redis-cli XLEN them:events
```

### Query ClickHouse Events
```bash
docker exec them-clickhouse clickhouse-client --query "SELECT COUNT(*) FROM events.events"
docker exec them-clickhouse clickhouse-client --query "SELECT kind, COUNT(*) FROM events.events GROUP BY kind"
```
