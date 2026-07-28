# Phase R-1 Complete — Handover

**Date:** 2026-07-28
**Branch:** main
**HEAD:** 39d505c feat(observability): Phase R-1 — Prometheus metrics + structured logging
**Session model:** claude-sonnet-4-6

---

## Current objective

Phase R-1 (Observability & Metrics) is complete and committed.
Next task: Phase R-2 — define scope with Opus before implementation.

---

## Work completed this session

### Phase R-1: Observability & Metrics — ALL ITEMS DONE

| Item | Description | Files |
|---|---|---|
| New `internal/metrics` package | 10 Prometheus metrics registered on default registry via `init()` | `go/internal/metrics/metrics.go` |
| Metrics tests | 12 tests covering all metrics, label isolation, cardinality enforcement | `go/internal/metrics/metrics_test.go` |
| Event bus drop counter | `EventBusDropped.Inc()` in slow-consumer drop path (non-terminal only) | `go/internal/event/bus.go` |
| WS handler metrics | ActiveWSConnections gauge, gate rejection/admission counters, session lifecycle counters | `go/internal/ws/handler.go` |
| SSE handler metrics | ActiveSSEConnections gauge, gate rejection/admission counters, session lifecycle counters | `go/internal/sse/handler.go` |
| Structured logging | `ep_slug`, `app_id`, `tenant_id`, `session_id`, `run_id` logged at session start/end and errors | `go/internal/ws/handler.go`, `go/internal/sse/handler.go` |
| Drain observation | `metrics.ObserveDrain(drainStart)` after `httpServer.Shutdown`; imports `internal/metrics` | `go/internal/server/server.go` |
| Startup visibility | `log.Info("shutdown drain configured", "drain_seconds", ...)` at startup | `go/cmd/them/main.go` |
| TEST_INDEX.md | Added S1-27 (metrics, 12 tests); totals: S1=342, Grand=407 | `go/TEST_INDEX.md` |

### Test results
- `go test ./...`: ALL PASS, 0 failures
- `go test -race ./...`: ALL PASS, 0 data races
- Python sanity (01 02 03 04 15): 55 passed, 0 failed

### R-0 safety verified
Terminal events are NOT delivered twice in a harmful way. They are routed to both `evCh` (best-effort) and `termCh` (guaranteed) by design. The handler loop exits after the first terminal event it processes. The drop counter guards `if !terminal` so terminal events never increment the drop counter.

---

## Deployed / live state

- Go bridge is NOT rebuilt/redeployed — code committed but Docker container still has pre-R-1 binary
- To redeploy: `docker compose --profile go build them-go-bridge && docker compose --profile go up -d them-go-bridge`
- Stack running: them-postgres, them-redis, them-auth-service, them-bridge, them-traefik (all healthy)

---

## Working tree state

After commit, all Phase R-1 files should be clean. Untracked `go/them` binary is the local compiled binary (not committed — correct).

---

## Architecture decisions made this session

1. **Cardinality rule enforced by test** — `TestHighCardinalityLabelsAbsent` fails if any `them_*` metric uses `session_id`, `run_id`, `request_id`, `user_id`, or `tenant_id` as a label name. This is the automated guard.
2. **`epTypeLabel` duplicated in ws and sse handlers** — not moved to `internal/transport` to keep each handler file self-contained. Both definitions are identical.
3. **`Describe` not `Gather` for registration check** — `Gather()` skips vec families with no observed series; `Describe()` always returns all registered descriptors.

---

## Hard constraints remaining in force

- Never log token values, API keys, request bodies, or any secret — per CLAUDE.md
- Never use `session_id`, `run_id`, `request_id`, `user_id`, or `tenant_id` as Prometheus label names (enforced by test)
- Never start R-2 through R-5 in the same session as R-1 (already observed)
- All Go changes require `go test ./...` to pass before commit

---

## Known bugs / risks

- `tenant_id` field in session info is empty string — `EPConfig.TenantID` is populated but `applications.tenant_id` DB column doesn't exist yet. This is acceptable per R-0 OD-2 decision.
- Go bridge Docker image is stale — rebuild needed to pick up R-0 + R-1 changes before metrics are observable via `/metrics` endpoint.

---

## Files most relevant to next task

| File | Relevance |
|---|---|
| `go/internal/metrics/metrics.go` | All metric definitions — starting point for R-2 |
| `go/internal/ws/handler.go` | WS handler with R-1 instrumentation — reference for R-2 patterns |
| `go/internal/sse/handler.go` | SSE handler with R-1 instrumentation |
| `go/internal/server/server.go` | Drain observation wired here |
| `docs/architecture-v2/R1_IMPLEMENTATION_REPORT.md` | Full R-1 implementation summary |

---

## Exact next single focused task

**Phase R-2: Tenant Tier Metrics** (or a different R-2 scope — TBD with Opus).

Before implementing R-2, start a new session with **Opus** to define scope:
- Decide whether to add `tenant_tier` as a label (requires architecture approval per current cardinality rules)
- OR pivot to a different R-2 priority (per-route latency histogram, LLM usage metrics, etc.)
- Write the scope decision to `docs/architecture-v2/` before returning to Sonnet for implementation

---

## Exact first prompt for next session

```
Read first:
- docs/architecture-v2/NEXT_SESSION_HANDOVER.md
- go/TEST_INDEX.md
- docs/architecture-v2/R1_IMPLEMENTATION_REPORT.md
- go/CLAUDE.md

We are on branch main. Phase R-1 (Observability & Metrics) is complete.

Next task: Define Phase R-2 scope. Review what metrics are already in place from R-1 (see R1_IMPLEMENTATION_REPORT.md) and propose the single most valuable next observability addition. Options include:
- Tenant tier label (requires architecture approval — cardinality rule currently prohibits tenant_id)
- Per-route latency histograms (http_request_duration_seconds by route_group)
- LLM provider usage counters (tokens in/out, errors)
- Other

Output: a short proposal (1-2 paragraphs) with your recommendation and the key trade-off. Do not implement yet.
```
