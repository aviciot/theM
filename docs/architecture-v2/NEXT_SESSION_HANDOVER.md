# Phase R-0 Complete — Handover to Phase R-1

**Date:** 2026-07-26  
**Branch:** main  
**HEAD:** (update after commit)  
**Session model used:** claude-sonnet-4-6

---

## Current objective

Phase R-0 (Critical Runtime Gate) is complete and committed. Next task is Phase R-1.

---

## Work completed this session

### Phase R-0: Critical Runtime Gate — ALL 5 ITEMS DONE

| Item | Description | Files |
|---|---|---|
| L-1 / OD-1 | Terminal event guarantee via `termCh` (cap 1) | `go/internal/event/bus.go`, `ws/handler.go`, `sse/handler.go`, `a2a/server.go` |
| T-2 / OD-2 | `sessInfo.AppID` + `sessInfo.TenantID` populated from EP config | `go/internal/session/session.go`, `go/internal/epconfig/epconfig.go`, `ws/handler.go`, `sse/handler.go` |
| OD-7 | Shutdown drain 5s→30s with `SHUTDOWN_DRAIN_SECONDS` env var | `go/internal/config/config.go`, `go/internal/server/server.go` |
| L-2 | Heartbeat goroutine uses `runCtx` not `context.Background()` | `go/cmd/them/main.go` |
| L-3 | `runCancel` called as pre-drain hook before `httpServer.Shutdown` | `go/cmd/them/main.go`, `go/internal/server/server.go` |

### Tests added
- 3 new event bus tests (terminal event guarantee — `TestBus_TerminalEventDeliveredOnFullBuffer`, `TestBus_TerminalEventDroppedIfTermChFull`, `TestBus_TerminalEventAlsoRoutedToEvCh`)
- 4 new config tests (drain parsing — `TestShutdownDrain_Default`, `TestShutdownDrain_Valid`, `TestShutdownDrain_BelowMin_Clamped`, `TestShutdownDrain_Invalid_Clamped`)

### Test results
- `go test -race ./...`: ALL PASS, 0 failures, 0 data races
- Python sanity (01 02 03 04 15): 55 passed, 0 failed

---

## Deployed / live state

- Go bridge is NOT rebuilt/redeployed this session — code changes are committed but the running Docker container still has the previous binary
- To redeploy: `docker compose --profile go build them-go-bridge && docker compose --profile go up -d them-go-bridge`
- Stack is running: them-postgres, them-redis, them-auth-service, them-bridge, them-traefik (all healthy)

---

## Architecture decisions made

1. **Terminal events route to BOTH evCh AND termCh** (not termCh only) — ensures both the normal drain loop and the terminal guarantee path work when evCh has capacity
2. **Pre-drain hook pattern** — `srv.WithPreDrainHook(runCancel)` cancels subscriber goroutines before HTTP drain, so the 30s window is not wasted waiting for stuck goroutines
3. **TenantID is empty string until Phase R-4** — `EPConfig.TenantID` and `SessionInfo.TenantID` are wired but empty because `applications.tenant_id` column does not exist yet. This is explicitly acceptable per OD-2.
4. **isTerminal checks `ev.Type` not `ev.Topic`** — orchestrator publishes `Topic=contextID, Type="done"`. Test events must set `Type="done"` to trigger the terminal path.

---

## Known bugs and blockers

None. All Phase R-0 acceptance criteria met.

---

## Temporary compatibility code in place

- `EPConfig.TenantID` and `SessionInfo.TenantID` are always empty string until Phase R-4 adds `applications.tenant_id` column to PostgreSQL schema.

---

## Hard constraints that must remain in force

- `go test -race ./...` must pass before every commit
- `go/TEST_INDEX.md` must be updated in the same commit as test changes
- Do NOT start Phase R-2, R-3, R-4, or R-5 without completing R-1 first
- Never use `context.Background()` for long-lived goroutines — use `runCtx`
- Terminal events must always be delivered — never drop on a full buffer
- Tenant is a security boundary — `AppID` must flow through all tenant-scoped operations

---

## Files most relevant to Phase R-1

Phase R-1 is Observability & Metrics. Relevant files:
- `go/internal/server/server.go` — Prometheus handler already mounted at `/metrics`
- `go/cmd/them/main.go` — startup wiring
- `go/internal/event/bus.go` — may need Prometheus counters for publish/drop rates
- `go/internal/session/session.go` — session count metrics
- `go/internal/gate/gate.go` — admission/rejection counters
- `go/internal/runstream/metrics.go` — existing runstream metrics pattern to follow

---

## Exact next single focused task: Phase R-1

**Scope (read the full R-1 definition before starting):**
- Structured logging improvements (request IDs, session IDs, tenant context in log fields)
- Prometheus metric coverage for: session count, gate admission/rejection, event bus drop rate, active WS/SSE connections
- Expose drain timeout in startup log
- Do NOT redesign the tenant schema (that is R-4)

**First commands for next session:**
```bash
# Verify HEAD and clean working tree
git log --oneline -3
git status

# Read the relevant docs
cat docs/architecture-v2/NEXT_SESSION_HANDOVER.md
cat docs/architecture-v2/implementation-status.md
cat docs/architecture-v2/lessons-learned.md
```

**First prompt for next session:**
"Read NEXT_SESSION_HANDOVER.md, implementation-status.md, and lessons-learned.md. Then implement Phase R-1 (Observability & Metrics) as defined in the architecture gate docs. Do not start R-2, R-3, R-4, or R-5."
