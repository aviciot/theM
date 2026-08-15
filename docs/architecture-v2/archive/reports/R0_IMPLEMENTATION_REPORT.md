# Phase R-0 — Critical Runtime Gate: Implementation Report

Date: 2026-07-26

---

## Summary

Five focused fixes to the Go bridge runtime layer. All tests pass; zero regressions.

---

## Fixes Implemented

### L-1 / OD-1 — Terminal event guarantee

- **File:** `go/internal/event/bus.go`
- **Change:** `Bus.Subscribe` now returns `(<-chan Event, <-chan Event, func())` — third return is `termCh` (capacity 1)
- Terminal events (`Type="done"` or `Type="error"`) are routed to BOTH `evCh` (best-effort) AND `termCh` (guaranteed, non-blocking drop if already full)
- Transient events only go to `evCh`, dropped non-blocking if full
- All three call sites updated: `ws/handler.go`, `sse/handler.go`, `a2a/server.go`
- WS and SSE `streamEvents` functions now receive `termCh` and select on it
- A2A discards `termCh` (`_, unsub := ...`) since it uses synchronous aggregation
- `publishToEntries` sends to `evCh` first (best-effort), then to `termCh` if terminal

### T-2 / OD-2 — RuntimeIdentity population

- **Files:** `go/internal/epconfig/epconfig.go`, `go/internal/session/session.go`, `go/internal/ws/handler.go`, `go/internal/sse/handler.go`
- `EPConfig` and `EPConfigRow` now carry `TenantID string` (empty until Phase R-4 adds the `applications.tenant_id` column)
- `SessionInfo` now carries `TenantID string` (persisted to Redis Hash as `tenant_id`)
- WS and SSE handlers populate `sessInfo.AppID` and `sessInfo.TenantID` from `resolvedCfg` after EP config load
- Both handlers also capture `appID` for the deferred `session.End` call (previously passed empty string)

### OD-7 — Configurable shutdown drain

- **Files:** `go/internal/config/config.go`, `go/internal/server/server.go`
- New config field: `ShutdownDrainSeconds int` (default 30, minimum 5, clamped)
- New env var: `SHUTDOWN_DRAIN_SECONDS`
- `server.NewWithBus` signature extended: `drainSeconds time.Duration` parameter added
- `server.Server.WithPreDrainHook(fn func())` method added
- `ListenAndServe` calls `preDrainHook()` before `httpServer.Shutdown`
- `main.go` passes `time.Duration(cfg.ShutdownDrainSeconds) * time.Second` to server

### L-2 — Heartbeat uses derived context

- **File:** `go/cmd/them/main.go`
- Created `runCtx, runCancel := context.WithCancel(context.Background())` with `defer runCancel()`
- Heartbeat goroutine now uses `runCtx` (previously used uncontrolled `ctx = context.Background()`)
- All other long-lived goroutines also migrated to `runCtx`: `tokenCache.Subscribe`, `agentReg.Subscribe`, `reconciler.Run`, `epLoader.Subscribe`

### L-3 — Subscribe goroutines stopped before HTTP drain

- **File:** `go/cmd/them/main.go`
- `srv.WithPreDrainHook(runCancel)` called after server construction
- On SIGTERM/SIGINT: `runCancel()` fires → all subscriber goroutines exit → `httpServer.Shutdown` drains remaining HTTP connections
- `runCancel` is idempotent; `defer runCancel()` in main is a safety net

---

## Tests Added

- 3 new tests in `internal/event/bus_test.go` (terminal event guarantee)
- 4 new tests in `internal/config/config_test.go` (drain parsing)
- `go/TEST_INDEX.md` updated: S1-07 count 6→9, S1-01 count 14→18, S1 total 323→330, grand total 388→395

---

## Test Results

- `go test -race ./...`: all packages PASS, 0 failures, 0 data races
- Python sanity (01 02 03 04 15): 55 passed, 0 failed

---

## Boundary Check

No R-1, R-2, R-3, R-4, or R-5 work was started. The `applications.tenant_id` column is absent from the DB schema — `TenantID` will be empty string until Phase R-4 adds it.
