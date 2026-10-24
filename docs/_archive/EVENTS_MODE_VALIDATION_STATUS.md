# Events Mode Validation Status
**Date:** 2026-10-21  
**Branch:** main (commit 950e689c)  

## Summary

Event mode validation has been added to all three event-emitting services. The validation correctly blocks unknown/unsupported modes at startup, but the current `.env` configuration has Auth and Tools set to `clickhouse` mode, which is now blocked.

## Current State

### Validation Code (✓ Complete)
- **File:** `go/internal/events/mode.go`
- **Functions:** `ValidateEventsMode()`, `RequireRedisForMode()`
- **Behavior:**
  - Accepts: `postgres` (default), `dual`
  - Rejects: `clickhouse` (blocked), unknown values
  - Error messages explain why and suggest alternatives

### Service Integration (✓ Complete)

| Service | Main File | Validation Call | Build Status |
|---------|-----------|-----------------|--------------|
| Gateway | `go/cmd/them/main.go` | Line 122-130 | ✓ Running |
| Auth | `go/cmd/auth-server/main.go` | Line 111-125 | ✓ Built, crashing on start |
| MCP Tools | `go/cmd/mcp-service/main.go` | Line 81-95 | ✓ Running (old image) |

### Container Runtime Status

| Container | Env Var | Current Value | Status | Issue |
|-----------|---------|---------------|--------|-------|
| them-go-bridge | THE_M_EVENTS_GATEWAY | `dual` | Running | None |
| them-auth-go | THE_M_EVENTS_AUTH | `clickhouse` | Crash loop | Validation blocks startup |
| them-mcp-service | THE_M_EVENTS_TOOLS | `clickhouse` | Running | Old image (no validation yet) |

## The Configuration Gap

**Problem:** The validation code blocks `clickhouse` mode with this error:
```
events mode 'clickhouse' is not yet supported: missing budget accounting queries 
and request body storage. Use 'postgres' (default) or 'dual' (migration verification 
mode) instead.
```

**Analysis:** The blocking reason ("budget accounting + request body storage") is **Gateway-specific**:
- **Gateway** needs budget queries (`SumMonthlySpend`) and body storage (`gateway_request_bodies`)
- **Auth** events (login/logout) have no budget or body dependencies
- **Tools** events (tool calls) have no budget or body dependencies

However, during migration/verification phase, **all three services should use the same mode** for consistency.

## Required Fix

### Step 1: Update `.env`
Change:
```bash
THE_M_EVENTS_AUTH=clickhouse    # current
THE_M_EVENTS_TOOLS=clickhouse   # current
```

To:
```bash
THE_M_EVENTS_AUTH=dual
THE_M_EVENTS_TOOLS=dual
```

### Step 2: Rebuild MCP Service
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  build them-mcp-service
```

### Step 3: Restart Services
```bash
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml \
  restart them-auth-go them-mcp-service
```

### Step 4: Verify
```bash
docker logs them-auth-go --tail 10    # should see "auth events sink: dual mode"
docker logs them-mcp-service --tail 10 # should see "tool events sink: dual mode"
```

## Effective Write Destinations After Fix

With all three services in `dual` mode:

| Event Type | Service | Postgres Write | ClickHouse Write | Authoritative |
|------------|---------|----------------|------------------|---------------|
| Gateway request | them-go-bridge | ✓ gateway_requests | ✓ via Redis Streams | Postgres |
| User activity | them-auth-go | ✗ (no PG table yet) | ✓ via Redis Streams | ClickHouse only |
| Tool call | them-mcp-service | ✗ (no PG table yet) | ✓ via Redis Streams | ClickHouse only |

**Note:** Auth and Tools currently have **no Postgres tables** for their events, so "dual" mode effectively means "ClickHouse-only via Redis Streams" for them. The dual mode is correct because it preserves the event delivery path while allowing future PG tables to be added without code changes.

## Design Question: Should Validation Be Service-Specific?

**Current:** `ValidateEventsMode()` is universal — blocks `clickhouse` for all services.

**Alternative:** Add a `restrictions` parameter to allow Gateway-specific blocking:
```go
ValidateEventsMode(envVar string, restrictions ...string) (EventsMode, error)
```

**Decision:** Keep it simple. Use `dual` mode consistently during migration/verification. When Phase 2 is complete (budget queries + body storage implemented), remove the `clickhouse` block universally.

## Status

- [x] Validation code written and tested
- [x] Gateway integrated and running
- [x] Auth integrated and built
- [x] MCP Tools integrated (not yet rebuilt)
- [ ] `.env` updated to use `dual` mode for Auth/Tools
- [ ] MCP service rebuilt with validation
- [ ] All services restarted and verified

## Next Steps

1. User updates `.env` (permission-restricted file)
2. Rebuild them-mcp-service
3. Restart them-auth-go and them-mcp-service
4. Verify all three services log their effective mode at startup
5. Continue with auth identity fix (verifiedClaims compilation error)
