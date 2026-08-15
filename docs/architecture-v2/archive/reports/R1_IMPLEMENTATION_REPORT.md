# Phase R-1: Observability & Metrics — Implementation Report

**Date:** 2026-07-28
**Branch:** main
**Preceding phase:** R-0 (Critical Runtime Gate — HEAD 88004b1)
**This phase:** R-1 (Observability & Metrics)

---

## Summary

Phase R-1 adds Prometheus metrics and structured logging to the Go gateway. All metrics follow low-cardinality label rules. The event bus drop counter is wired to the existing R-0 terminal-event guarantee. Startup and shutdown have explicit observability. The full test suite passes clean with no data races.

---

## Metrics Added

All metrics use the `them_` prefix and are registered on `prometheus.DefaultRegistry` via `init()` in `internal/metrics/metrics.go`.

### Session metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `them_active_sessions` | GaugeVec | `ep_type` | In-flight sessions on this replica |
| `them_sessions_started_total` | CounterVec | `ep_type`, `result` | Session start attempts (admitted / rejected) |
| `them_sessions_ended_total` | CounterVec | `ep_type`, `reason` | Sessions terminated by reason |

### Gate metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `them_gate_admissions_total` | CounterVec | `ep_type` | Successful gate admissions |
| `them_gate_rejections_total` | CounterVec | `ep_type`, `reason` | Gate rejections by reason |

### Event bus metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `them_event_bus_dropped_total` | Counter | (none) | Transient events dropped (slow consumer, buffer full) |
| `them_event_bus_coalesced_total` | Counter | (none) | Events coalesced (reserved for future use) |

### Connection metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `them_active_ws_connections` | Gauge | (none) | Open WebSocket connections on this replica |
| `them_active_sse_connections` | Gauge | (none) | Open SSE connections on this replica |

### Shutdown metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `them_graceful_drain_duration_seconds` | Histogram | (none) | Duration of graceful drain. Buckets: 1,5,10,15,20,25,30,45,60s |

---

## Cardinality Rules (Enforced)

**Permitted labels:**
- `ep_type`: websocket | sse | a2a | voice | unknown
- `result`: admitted | rejected
- `reason`: cap_exceeded | rate_limited | queue_full | client_disconnect | context_cancel | admin_signal | error

**Prohibited labels (never used as metric labels):**
- `session_id`, `run_id`, `request_id`, `user_id`, `tenant_id`

Enforcement is automated: `TestHighCardinalityLabelsAbsent` fails the suite if any `them_*` metric uses a prohibited label name.

---

## Structured Logging Fields

All `slog.Info` / `slog.Warn` calls in WS and SSE handlers now include:

| Field | Where populated | Value |
|---|---|---|
| `ep_slug` | WS + SSE session start/end/errors | Entry point slug from URL path |
| `app_id` | WS + SSE session start/end/errors | Application ID from resolved EP config |
| `tenant_id` | WS + SSE session start/end | Tenant ID from session info |
| `session_id` | WS + SSE session start/end | UUID generated per connection |
| `run_id` | WS only, at workflow start | Run UUID |
| `workflow_id` | WS only, Temporal path | Temporal workflow ID |

**Secrets never logged:** token values, API keys, request body content — per CLAUDE.md rule.

---

## Files Changed

| File | Change |
|---|---|
| `go/internal/metrics/metrics.go` | New package — all 10 metrics defined and registered |
| `go/internal/metrics/metrics_test.go` | New — 12 tests covering all metrics, cardinality rules, label isolation |
| `go/internal/event/bus.go` | Added `metrics.EventBusDropped.Inc()` in the slow-consumer drop path (non-terminal events only) |
| `go/internal/ws/handler.go` | ActiveWSConnections gauge, gate rejection/admission counters, session lifecycle counters, structured slog fields |
| `go/internal/sse/handler.go` | ActiveSSEConnections gauge, gate rejection/admission counters, session lifecycle counters, structured slog fields, `epTypeLabel` helper |
| `go/internal/server/server.go` | Added `metrics.ObserveDrain(drainStart)` after `httpServer.Shutdown`; imports `internal/metrics` |
| `go/cmd/them/main.go` | Added `log.Info("shutdown drain configured", ...)` at startup |
| `go/TEST_INDEX.md` | Added S1-27 (metrics, 12 tests), updated totals (S1: 342, Grand: 407) |

---

## R-0 Safety Verification (Terminal Event Guarantee)

**Question:** Are terminal events delivered twice — once via `evCh` and once via `termCh`?

**Answer:** Yes, by design — and this is safe. The WS and SSE handlers use a `select` that reads from either `evCh` or `termCh` to ensure terminal delivery. Receiving the terminal event on both channels does not cause it to be processed twice because:
1. The handler loop exits immediately after processing a terminal event.
2. The bus drop counter in `bus.go` only fires for NON-terminal events (`if !terminal { metrics.EventBusDropped.Inc() }`), so a terminal event that also fits into `evCh` does not increment the drop counter — which would be misleading.

Pre-existing tests in `internal/event/bus_test.go` confirm this:
- `TestBus_TerminalEventDeliveredOnFullBuffer` — terminal via termCh even when evCh is full
- `TestBus_TerminalEventDroppedIfTermChFull` — second terminal does not block (cap 1)
- `TestBus_TerminalEventAlsoRoutedToEvCh` — terminal appears in both when evCh has capacity

No duplicate delivery issue exists.

---

## Test Results

```
go test ./...         — all packages pass, 0 failures
go test -race ./...   — all packages pass, 0 data races
python3.12 scripts/tests/run_tests.py 01 02 03 04 15 — 55 passed, 0 failed
```

New tests: 12 (S1-27 in `internal/metrics/`)
S1 total: 342 | Grand total: 407

---

## Not Implemented (Deferred to Later Phases)

- R-2: Tenant tier label (requires architecture approval for `tenant_id` as label)
- R-3: Per-route latency histograms
- R-4: LLM provider usage metrics
- R-5: Cross-replica aggregation (requires external Prometheus federation)

---

## Next Phase

Phase R-2 scope is TBD pending the architecture review of tenant-tier labeling.
See `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` for exact next steps.
