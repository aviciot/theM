# THEM Go Rewrite — Implementation Status

Last updated: 2026-07-20

All 8 phases complete + Phase 9 (gate package) + Phase 5.4 full EP config wiring + Phase 10 Temporal execution path (canonical run_id, ADR-001) + live-stack validation + Phase 11a (runstream reconnect with backoff) + Phase 11b (run reconciler). `go build ./...` and `go test ./...` pass. Race detector clean on all packages.

**Phase 11a (runstream reconnect):** `internal/runstream/stream.go` now holds the output channel open across transient Redis disconnects. Reconnects use bounded exponential backoff (100ms→3200ms, max 6 attempts). Terminal events (`done`/`error`) close the output immediately without waiting for the source channel. Exhausted reconnects emit a single synthetic error event. Four Prometheus counters: `them_runstream_{disconnects,reconnect_attempts,reconnect_success,reconnect_failure}_total`. Integration tests pass under `go test -tags=integration ./...`.

**Phase 11b (run reconciler):** `internal/reconciler/` sweeps `them.runs` for rows stuck in `status='running'` and reconciles them against Temporal's authoritative state via `DescribeWorkflowExecution`. PostgreSQL advisory lock prevents duplicate sweeps across pods. Dry-run mode is on by default — set `DryRun=false` in `main.go` to enable writes. Safe NotFound policy: no DB write on 404; warns and increments metric instead. Status mapping: COMPLETED→completed, FAILED→failed, CANCELED→canceled, TERMINATED→stopped, TIMED_OUT→failed (ADR-002). Six Prometheus counters: `them_reconciler_{scanned,unchanged,updated,notfound,errors,dryrun}_total`. 14 unit tests pass.

---

## Package Inventory

| Package | Path | Purpose | Test count | Architectural finding fixed |
|---|---|---|---|---|
| config | `internal/config` | Env loading, startup validation, JWT PEM, ANTHROPIC_API_KEY | 9 | Centralised config — no scattered os.Getenv |
| telemetry | `internal/telemetry` | slog JSON logger with instance ID | 0 (no logic to test) | Structured logging from day 1 |
| db | `internal/db` | pgxpool wrapper, Ping, Pool accessor | 0 (integration only) | Connection pooling, graceful close |
| cache | `internal/cache` | rueidis wrapper + session/admin/ratelimit/auth/runstream adapters | 2 | Redis abstraction, adapter isolation |
| health | `internal/health` | /health/live + /health/ready with DB+Redis probes | 5 | Kubernetes-compatible health endpoints |
| server | `internal/server` | chi router, middleware, graceful shutdown, event bus, mount methods | 4 | Single router — no scattered http.HandleFunc |
| auth | `internal/auth` | RS256 JWT validation, bearer token two-level cache (L1+L2+pub/sub revocation), PgxQuerier for them.access_tokens | 14 | Local JWT validation (no auth service round-trip on hot path); bearer token wired to real DB in production |
| session | `internal/session` | Atomic Lua scripts, shadow TTL keys, ghost pruning, pod heartbeat | 6 | Fixes ghost session accumulation (Critical finding #1) |
| event | `internal/event` | In-process fan-out topic event bus, wildcard subscriber | 6 | Fixes race condition: subscribe-before-publish |
| domain | `internal/domain` | Canonical Message/Run types, role + status enums | 3 | Provider format never leaks into DB (High finding #7) |
| runrecorder | `internal/runrecorder` | Run persistence, DBQuerier interface, pgx adapter | 9 | DB-backed run state — survives pod crash |
| llm | `internal/llm` | Provider interface, AnthropicProvider, MockProvider | 6 | Provider swap without code changes |
| orchestrator | `internal/orchestrator` | Agentic loop, DB-level history LIMIT, context cancellation | 0 (tested via ws/sse) | DB-level LIMIT on history (Medium finding #3) |
| temporal | `internal/temporal` | Temporal workflow/activity, HITL signal channel, Signaler adapter, PythonOrchestrationInput (wire-format struct for Python worker) | 0 (integration) | Durable execution, HITL, pod-crash resilience |
| runstream | `internal/runstream` | Redis pub/sub subscriber for `them:dash:run:{runID}:tokens`. Reconnects on transient drops with bounded exponential backoff (100ms→3200ms, max 6 attempts). Terminal events close output immediately. Exhaustion emits one synthetic error event. Four Prometheus counters. At-most-once delivery — no replay during reconnect gap. | 10 | Phase 11a: output channel survives Redis hiccups without tearing down client connection. |
| reconciler | `internal/reconciler` | Sweeps `them.runs` for stuck `running` rows and reconciles against Temporal via `DescribeWorkflowExecution`. PostgreSQL advisory lock for single-pod sweep coordination. Dry-run mode on by default. Safe NotFound policy (no DB write). Status mapping per ADR-002. Six Prometheus counters. | 15 | Phase 11b: reconciles runs that completed in Temporal but remain `running` in DB after connection loss. |
| agentregistry | `internal/agentregistry` | A2A JSON-RPC 2.0 invocation, two-level cache, pub/sub invalidation | 5 | Agent config cache with cross-replica invalidation |
| epconfig | `internal/epconfig` | EP + App runtime config resolver — DB JOIN, 30s TTL cache, CheckAccess (fail-closed), Subscribe for cross-pod Redis pub/sub invalidation, shared by WS + SSE | 26 | Single typed config model; no duplication between handlers |
| ws | `internal/ws` | WebSocket handler, lazy auth (EP config checked first), public EP support, anonymous session identity (UserID=0, TokenHash="" to gate), Gate contract (Check→Register→Confirm/Rollback/Release), bus subscription before workflow, voice EP rejection (501), Temporal execution path (TEMPORAL_ENABLED=true) with single-phase subscribe-before-start (subscribe `:tokens` → start workflow), Go-inline fallback. `newID()` uses `github.com/google/uuid` — UUID v4 format required for Python Temporal worker | 15 | Subscribe-before-start bootstrap pattern + Gate contract enforcement + real EP limits + public EP access + anonymous session safety + voice EP guard + Temporal coexistence + UUID ID format |
| sse | `internal/sse` | SSE handler (GET+POST), lazy auth (EP config checked first), public EP support, anonymous session identity, same Gate contract + subscribe-before-start pattern as WS, voice EP rejection (501), Temporal execution path (TEMPORAL_ENABLED=true) with single-phase subscribe-before-start (subscribe `:tokens` → start workflow), Go-inline fallback. `newID()` uses `github.com/google/uuid` — UUID v4 format required for Python Temporal worker | 14 | SSE as a first-class entry point + Gate contract enforcement + real EP limits + public EP access + anonymous session safety + voice EP guard + Temporal coexistence + UUID ID format |
| a2a | `internal/a2a` | JSON-RPC 2.0 A2A server, /.well-known/agent.json agent card | 3 | Orchestrator-as-agent pattern |
| admin | `internal/admin` | REST CRUD for agents/orchestrators/applications/entry-points/runs, HITL signal, JWT+super_admin middleware (fail-closed for anonymous requests); EP config invalidation on all EP/App mutations including slug renames (old+new slug both published); ep_type validation (websocket/sse/voice only, 422 on invalid) | 19 | Admin API with proper auth, cache invalidation; cross-pod EP config eviction; slug rename evicts both old and new cache entries; anonymous requests rejected with 401; invalid EP type rejected at API boundary |
| ratelimit | `internal/ratelimit` | Redis INCR rate limiting, per-token + per-app, 1-minute windows | 3 | Redis-backed rate limiting (replica-safe) |
| gate | `internal/gate` | Runtime admission gate — SOLE owner of Set membership. Reservation TTL pattern: Check (SADD + short shadow TTL 10s) → Register (Hash) → Confirm (extend to 90s). Rollback on Register failure. Queue: BLPop signal channel, re-compete on wake. | 16 | Eliminates duplicate-SADD failure window; reservation TTL bounds ghost window to ≤10s even on crash; queue wake-up is a compete not a guarantee |

**Total packages with tests: 24**
**Total test count: 188 unit + 12 integration = 200 automated**

---

## Route Map (as mounted in main.go)

| Route | Package | Auth |
|---|---|---|
| GET /health/live | health | None |
| GET /health/ready | health | None |
| GET /metrics | server (promhttp) | None |
| GET /ws/orchestrate/{app_slug}/{ep_slug} | ws | Bearer token (or none for public EPs) |
| GET /sse/orchestrate/{app_slug}/{ep_slug} | sse | Bearer token (or none for public EPs) |
| POST /sse/orchestrate/{app_slug}/{ep_slug} | sse | Bearer token (or none for public EPs) |
| POST /a2a/{app_slug} | a2a | None (caller auth is up to app) |
| GET /.well-known/agent.json | a2a | None |
| GET /api/v1/admin/agents | admin | JWT + super_admin |
| POST /api/v1/admin/agents | admin | JWT + super_admin |
| GET /api/v1/admin/agents/{id} | admin | JWT + super_admin |
| PUT /api/v1/admin/agents/{id} | admin | JWT + super_admin |
| DELETE /api/v1/admin/agents/{id} | admin | JWT + super_admin |
| GET /api/v1/admin/orchestrators | admin | JWT + super_admin |
| POST /api/v1/admin/orchestrators | admin | JWT + super_admin |
| GET /api/v1/admin/orchestrators/{name} | admin | JWT + super_admin |
| PUT /api/v1/admin/orchestrators/{name} | admin | JWT + super_admin |
| DELETE /api/v1/admin/orchestrators/{name} | admin | JWT + super_admin |
| GET /api/v1/admin/applications | admin | JWT + super_admin |
| POST /api/v1/admin/applications | admin | JWT + super_admin |
| GET /api/v1/admin/applications/{id} | admin | JWT + super_admin |
| PUT /api/v1/admin/applications/{id} | admin | JWT + super_admin |
| DELETE /api/v1/admin/applications/{id} | admin | JWT + super_admin |
| POST /api/v1/admin/applications/{id}/entry-points | admin | JWT + super_admin |
| PUT /api/v1/admin/applications/{id}/entry-points/{ep_id} | admin | JWT + super_admin |
| DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id} | admin | JWT + super_admin |
| GET /api/v1/runs | admin | JWT |
| GET /api/v1/runs/{run_id} | admin | JWT |
| POST /api/v1/runs/{run_id}/signal | admin | JWT |

---

## Architectural Findings Fixed

From the original architecture review:

| Finding | Severity | Fixed in | How |
|---|---|---|---|
| Ghost session accumulation — Set members without TTL | Critical | Phase 4 (session) + Phase 9 (gate) | Atomic Lua shadow-key pattern: every SADD paired with SET…EX shadow key; luaPruneAndCount runs inside gate's admission Lua on every request; SREM+DEL shadow on End |
| Duplicate SADD failure window — both Gate and SessionManager wrote to Set | Architecture fix | Phase 9 (gate) | Gate is the sole owner of Set membership at admission. SessionManager owns Hash only. Clear ownership boundary with no failure window. |
| Subscribe-after-publish race (events lost) | Critical | Phase 5 (ws, sse) | Subscribe to bus BEFORE starting orchestrator goroutine |
| Provider format leaks into DB | High | Phase 5 (domain) | Canonical domain.Message type; all providers translate at the boundary |
| Hardcoded 0 session count in pod heartbeat | High | Phase 4 (session) | atomic.LoadInt32(&activeSessions) — accurate count |
| No DB-level LIMIT on history load | Medium | Phase 6 (orchestrator) | HistoryLoader interface with DB-level LIMIT parameter |
| Single-level token cache (no Redis L2) | Medium | Phase 2 (auth) | Two-level cache: in-process L1 + Redis L2 + pub/sub eviction |
| Rate limiting not replica-safe | Medium | Phase 8 (ratelimit) | Redis INCR with minute-bucket keys — all replicas share same counter |
| Admin mutations don't invalidate caches | Medium | Phase 8 (admin) | Every mutation calls CacheInvalidator.Del on affected keys |
| No HITL support | Low | Phase 7 (temporal) | Temporal SignalWorkflow via admin runs signal endpoint |

---

## Build and Test Status

```
go build ./...     PASS
go test ./...      PASS (24 packages, 188 unit tests)
go test -tags=integration ./...   PASS (4 stack integration + 8 hybrid Temporal integration = 12 integration tests)
go test -race ./...                NOTE: requires Linux/GCC — run in CI only
```

**Hybrid Temporal integration tests** (`internal/temporal/hybrid_integration_test.go`):
- Require live Temporal server, PostgreSQL, Redis, and Python Temporal worker
- Start with: `cd theM_gateway && ./scripts/run-go-integration-tests.sh`
  (or manually: `docker compose -f docker-compose.yml -f docker-compose.local.yml -f docker-compose.integration.yml --profile temporal up -d`)
- Non-standard host ports (avoid local conflicts): Postgres=15432, Redis=16379, Temporal=17233, Go bridge=8002
- All IDs (runID, sessionID, contextID) are UUID v4 — required by Python's `uuid.UUID()` parser
- Verify: canonical run_id preservation end-to-end, direct single-channel subscription, no context-channel handshake, no lost events, full wire format, cancel propagation, Python-native backward compat, channel key match

**Manual smoke tests** (`scripts/smoke_test_go_gateway.py`):
- Start full hybrid stack then run: `python3 scripts/smoke_test_go_gateway.py --token <tok> --app <slug> --ep <slug>`
- Tests: `/health/live`, `/health/ready`, WS orchestrate, SSE orchestrate
