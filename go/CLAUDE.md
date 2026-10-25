# THEM Go Gateway — Claude Session Guide
# Last updated: 2026-10-25

---

The project-wide session lifecycle, handover, Git, secrets, migration goal, model selection,
and tenant-aware design rules in `../CLAUDE.md` are mandatory. Read that file first.

---

## Read These First (Go-specific)

Before touching any Go code, read:

| Doc | When to read |
|---|---|
| `TEST_INDEX.md` | Before adding or changing any test |
| `../docs/INDEX.md` | Find the architecture doc that owns the package you are changing |
| `../docs/lessons/` | Before any judgment call — one file per area (`auth-tenancy-rls`, `appflow-canvas`, `temporal-workers`, `infra-docker`, …) |
| `../docs/CURRENT.md` | Current state, next task, hard constraints |

---

## This Module

**the-M Go Gateway** is the Go rewrite of the THEM AI orchestration platform.
Module: `github.com/aviciot/them` — source at `go/` in the repo root.
Port: **8002** (sole active API gateway, behind Traefik on 8088; Python bridge permanently removed).

Language rules: UI/docs say **the-M**. Code identifiers use **them** / **THE_M_**.

---

## Package Map

| Package | Purpose | Key files |
|---|---|---|
| `cmd/them/` | Binary entrypoint, full wiring | `main.go` |
| `cmd/auth-server/` | Go auth service binary (replaces Python them-auth-service) | `main.go` |
| `internal/authserver/` | User-facing auth: HS256 JWT issuance, bcrypt verify, login/me/refresh/logout, auth_service-schema DAL | `config.go`, `jwt.go`, `password.go`, `store.go`, `pgx.go`, `service.go`, `handlers.go`, `router.go` |
| `internal/config/` | Env loading, startup validation | `config.go` |
| `internal/db/` | pgxpool wrapper | `db.go` |
| `internal/cache/` | rueidis wrapper | `cache.go` |
| `internal/telemetry/` | slog JSON/console handler | `telemetry.go` |
| `internal/health/` | /health/live + /health/ready | `health.go` |
| `internal/server/` | chi router, middleware, graceful shutdown | `server.go` |
| `internal/auth/` | Local RS256 JWT + bearer token cache + pub/sub revocation | `jwt.go`, `token_cache.go`, `middleware.go` |
| `internal/gate/` | Runtime admission gate. Check (SADD + ReservationTTL 10s) → Register (Hash in session) → Confirm (extend to 90s). Rollback on Register failure. Queue via BLPop signal channel; wake is a re-compete not a guarantee. | `gate.go` |
| `internal/session/` | Session lifecycle — Hash (state) only. Atomic Lua SREM+DEL shadow on End. | `session.go` |
| `internal/event/` | In-process fan-out event bus | `bus.go` |
| `internal/events/` | Observability events — EventSink (Redis Streams → ClickHouse), EventsMode validation, RecoverPending with periodic recovery, retention policy | `redis.go`, `mode.go`, `clickhouse.go` |
| `cmd/event-writer/` | Event consumer — reads Redis Streams (consumer group), writes ClickHouse, handles recovery + retention | `main.go`, `recovery_test.go`, `retention.go` |
| `internal/domain/` | Canonical Message/Run types, status enums | `domain.go` |
| `internal/runrecorder/` | Run persistence to them.runs / run_steps / run_usage | `recorder.go` |
| `internal/llm/` | Provider interface, AnthropicProvider, MockProvider | `provider.go`, `anthropic.go`, `mock.go` |
| `internal/llmresolve/` | Shared LLM provider key/base_url/pricing resolution (app → tenant → platform precedence); used by `workerconfig`, `cmd/dag-worker`, and `internal/llmgateway` | `llmresolve.go` |
| `internal/llmgateway/` | LLM Gateway Phase 1 — OpenAI-compatible proxy for closed agents; observe + meter. POST /{tenant_slug}/llm/v1/chat/completions | `handler.go`, `service.go`, `dal.go` |
| `internal/governance/` | Multi-scope governance policy (limits + logging): `Policy` doc, `Validate`, `Resolve` across tenant/app/client/user. Pure logic; design in `docs/_archive/GOVERNANCE_POLICY_DESIGN.md` | `policy.go`, `resolve.go` |
| `internal/orchestrator/` | Agentic loop, DB-level history LIMIT, cost estimation (default rate card + optional DB-sourced `CostEstimator`) | `orchestrator.go`, `pricing.go` |
| `internal/temporal/` | Workflow, activity, HITL signal, client | `workflow.go`, `activities.go`, `client.go`, `signaler.go` |
| `internal/ws/` | WebSocket handler | `handler.go` |
| `internal/sse/` | Server-Sent Events handler | `handler.go` |
| `internal/a2a/` | JSON-RPC 2.0 A2A server + agent card | `server.go` |
| `internal/agentregistry/` | A2A invocation, two-level Redis cache | `registry.go` |
| `internal/admin/` | CRUD API for agents/orchestrators/apps/runs — thin HTTP handlers only | `agents.go`, `orchestrators.go`, `applications.go`, `runs.go` |
| `internal/admin/dal/` | Admin Data Access Layer — all SQL query strings + row-scan helpers; handlers call these | `dal.go`, `agents.go`, `orchestrators.go`, `applications.go`, `runs.go` |
| `internal/transport/` | Shared interfaces (Authenticator, SessionStore, GateStore, EPConfigLoader, TemporalClientExecutor) + TokenHash used by both ws and sse | `transport.go` |
| `internal/ratelimit/` | Redis INCR rate limiter per-token + per-app | `limiter.go` |
| `cmd/worker/` | Temporal worker for orchestrator runs (queue `them-orchestration-go`); publishes run events to Redis Streams | `main.go` |
| `cmd/dag-worker/` | Temporal worker: canvas agent DAG (`canvas-dag-nodes`) and AppFlow (`appflow-dag`, `appflow-dag-debug`) — see `docs/architecture/app-canvas-appflow.md` | `main.go` |
| `cmd/agent-runtime/` | Generic A2A server for canvas-built agents (reads AgentSpec from Postgres) — `docs/architecture/a2a-agents-agentgen.md` | `main.go`, `runtime.go`, `hitl.go` |
| `cmd/mcp-service/` | MCP server supervisor + tool executor — `docs/architecture/mcp-service.md` | `main.go` |
| `cmd/middleware-worker/` | Artifact scan jobs (ClamAV) from `them.middleware_jobs` | `main.go` |
| `internal/execution/` | Shared admission → start → release pipeline for WS/SSE/A2A (auth modes, quotas, gate, session, CreateRun) — `docs/architecture/run-lifecycle.md` | `lifecycle.go`, `roles.go`, `errors.go` |
| `internal/epconfig/` | Entry-point/app runtime config loader (30 s cache, pub/sub invalidation) and access checks | `epconfig.go`, `pgx.go` |
| `internal/runstream/` | Run events over Redis Streams: publisher (workers) + replaying reader (bridge) | `publisher.go`, `streamer.go` |
| `internal/reconciler/` | Sweeps stuck `running` runs against Temporal (dry-run by default; see backlog F-08) | `reconciler.go` |
| `internal/appflow/` | App Canvas compiler, validator, `AppFlowWorkflow` and activities | `compiler.go`, `validate.go`, `workflow.go`, `activities.go` |
| `internal/agentgen/` | Canvas agent builder: AgentSpec compiler, node registry, interpreter, local executor, HITL/task stores | `compiler.go`, `interpreter.go`, `noderegistry.go` |
| `internal/nodedefs/` | Portable node-type metadata shared by agentgen and appflow (served by `/admin/node-types`) | `nodedefs.go` |
| `internal/debugcred/` | Per-run Debug Mode credential store in Redis | `store.go` |
| `internal/roles/` | Tenant role gate (claim/header → role → app grants) | `roles.go`, `service.go` |
| `internal/quota/` | Per-tenant run/API/token quota enforcement | `enforcer.go` |
| `internal/jwks/` | RS256 verification against a JWKS endpoint (SSO and external JWT) | `jwks.go` |
| `internal/idpcrypto/` | AES-256-GCM for stored IdP client secrets | `idpcrypto.go` |
| `internal/tenantctx/` | Tenant identity in request context + slug→tenant resolver | `tenantctx.go`, `resolver.go` |
| `internal/dbtype/` | Typed query interfaces (`TenantQuerier` vs `AdminQuerier`) | `querier.go` |
| `internal/dashboard/` | `/ws/dashboard` live monitoring WebSocket (tails run streams, tenant-checked) | `handler.go` |
| `internal/appliveness/` | Probes entry points every 30 s and publishes app status | `liveness.go` |
| `internal/middleware/` | A2A artifact middleware pipeline: File Guard, text guards (PII, prompt-injection), job queue | `gate.go`, `textgate.go` |
| `internal/artifacts/` | HTTP download of run file artifacts | `handler.go` |
| `internal/storage/` | S3-compatible object store client (MinIO) | `storage.go` |
| `internal/crypto/` | Fernet-compatible encryption for stored secrets | `fernet.go` |
| `internal/history/` | Persistent conversation history in Postgres | `pgx.go` |
| `internal/summarizer/` | LLM conversation summarizer for memory | `summarizer.go` |
| `internal/voice/` | STT/TTS for voice entry points | `handler.go`, `service.go` |
| `internal/registry/` | Design-time component-definition resolver (publish) | `resolver.go` |
| `internal/metrics/` | Prometheus metrics + Redis daily usage counters | `recorder.go` |

---

## Rules — Architecture

- **Handler → Service → DAL** — no SQL in handlers, no business rules in handlers
- All PostgreSQL access goes through `internal/admin/dal/` (admin) or the equivalent DAL/repository adapter for each package — never inline SQL in handlers
- Shared WS/SSE behavior (auth, gate, session, token hash) goes in `internal/transport/`
- No third-party JWT library — local RS256 via stdlib `crypto/rsa` only
- No ORM — pgx/v5 direct queries only
- No indirect dep pseudo-versions pinned in go.mod — let `go mod tidy` resolve
- Context cancellation must propagate from WS disconnect → Temporal cancel → LLM HTTP
- Event bus subscribe BEFORE Temporal workflow start (ready bootstrap handshake)
- Admin routes require JWT middleware (`RequireSuperAdmin`)
- All list endpoints return `[]` not `null` when empty
- Secrets never appear in log output — use `cfg.SafeString()`
- `writeServiceError` must handle **every** typed sentinel that any service method it covers can return — if a new sentinel is added (e.g. `ErrConflict`), update `writeServiceError` in the same commit
- 500 responses must use static strings — never `err.Error()` from service or DAL layers

---

## Rules — File Size & Structure

- Keep files focused and preferably under 400 lines.
- If a file approaches 500 lines, stop and propose a logical split before adding more code.
- Split by clear responsibility (e.g. handlers vs service vs DAL), not arbitrary line count.
- Avoid oversized files that mix HTTP, business logic, and SQL — respect Handler → Service → DAL.
- Do not over-fragment into many tiny files.
- Before creating or expanding a large file, show the proposed structure and wait for approval.

---

## Rules — Route Ownership

- **Never claim a route is migrated based only on Traefik labels or dead code removal.**
- A route is owned by Go only when: the handler exists in `go/internal/`, the route is registered in `cmd/them/main.go`, the Traefik label is applied in the compose file, AND a live request through Traefik reaches the Go handler (verified from logs).
- Verify route ownership by checking Go bridge logs for actual request hits, not by inspecting labels alone.

---

## Rules — Tests (non-negotiable)

1. **Every code change to `internal/` or `cmd/` MUST have a test** — new behavior = new test, changed behavior = updated test.
2. **`TEST_INDEX.md` MUST be updated in the same commit** — add the test row, update the count, update the trigger map.
3. **`go test ./...` must pass before every commit** — zero new failures.
4. **`go test -race ./...` must pass before every PR merge** — no data races.
5. **Never delete a test to make the suite pass** — fix the code or fix the test.
6. **Integration tests** (build tag `integration`) require a live PostgreSQL and Redis — run them against the real stack before marking any schema-dependent code complete.

---

## Rules — Documentation (mandatory)

| Change | Update |
|---|---|
| New test | `TEST_INDEX.md` (same commit) |
| New package | `TEST_INDEX.md` + the owning `docs/architecture/` doc (`owns:` list) + package map above |
| New Redis key | `docs/reference/redis-generated.md` (key + TTL + purpose) |
| Bug fix or non-obvious behavior | `docs/lessons/<area>.md` |
| New route | the owning `docs/architecture/` doc |
| Architectural decision | owning `docs/architecture/` doc; trade-offs → ADR in `docs/decisions/` |

---

## Common Commands

```bash
# Run all unit tests
go test ./...

# Run with race detector
go test -race ./...

# Run integration tests (requires live Postgres + Redis)
go test -tags=integration -v ./...

# Build binary
go build ./cmd/them/

# Run locally (set env vars first)
go run ./cmd/them/

# Docker — build Go image (them-go-bridge is a default service, no profile needed)
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge

# Docker — start Go bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml --profile temporal up -d them-go-bridge

# Docker — watch logs
docker compose --project-name them_gateway logs -f them-go-bridge
```

---

## Trigger Map — which tests to run after changing what

| Changed | Run |
|---|---|
| `internal/db/db.go` (`Pools`, `BeginTenantTx`, `BeginAdminTx`) or any `db/0*_*.sql` RLS policy migration | `go test ./internal/db/...` + `go test -tags=integration ./internal/db/...` (needs live Postgres — role-attribute, GUC-isolation, two-tenant, catalog, and Platform-as-Tenant RLS checks) |
| `internal/config/config.go` | `go test ./internal/config/...` |
| `internal/health/health.go` | `go test ./internal/health/...` |
| `internal/server/server.go` | `go test ./internal/server/...` |
| `internal/auth/jwt.go` | `go test ./internal/auth/...` |
| `internal/auth/token_cache.go` | `go test ./internal/auth/...` |
| `internal/authserver/` (any file) or `cmd/auth-server/main.go` | `go test ./internal/authserver/...` |
| `internal/session/session.go` | `go test ./internal/session/...` |
| `internal/event/bus.go` | `go test ./internal/event/...` |
| `internal/domain/domain.go` | `go test ./internal/domain/...` |
| `internal/runrecorder/recorder.go` | `go test ./internal/runrecorder/...` |
| `internal/runrecorder/pgx.go` | `go test ./internal/runrecorder/...` |
| `internal/artifacts/handler.go` | `go test ./internal/artifacts/...` |
| `internal/llm/` (any file) | `go test ./internal/llm/...` |
| `internal/llmresolve/` (any file) | `go test ./internal/llmresolve/...` + `go test -tags=integration ./internal/llmresolve/...` (app→tenant→platform precedence needs a live DB) |
| `internal/llmgateway/` (any file), `cmd/them/main.go` (gateway wiring) | `go test ./internal/llmgateway/...` |
| `internal/governance/` (any file), `internal/admin/dal/gateway_policy*.go`, `internal/admin/policy_cache.go` | `go test ./internal/governance/... ./internal/llmgateway/... ./internal/admin/...` |
| `internal/agentregistry/registry.go` | `go test ./internal/agentregistry/...` |
| `internal/orchestrator/orchestrator.go` or `internal/orchestrator/pricing.go` | `go test ./internal/orchestrator/...` |
| `internal/ws/handler.go` | `go test ./internal/ws/... ./internal/execution/...` |
| `internal/sse/handler.go` | `go test ./internal/sse/... ./internal/execution/...` |
| `internal/epconfig/epconfig.go` or `internal/epconfig/pgx.go` | `go test ./internal/epconfig/...` |
| `internal/execution/lifecycle.go` or `internal/execution/roles.go` or `internal/execution/quotas.go` or `internal/ratelimit/user_limiter.go` | `go test ./internal/execution/... ./internal/ratelimit/... ./internal/ws/... ./internal/sse/...` |
| `internal/a2a/server.go` | `go test ./internal/a2a/...` |
| `internal/temporal/temporal_executor.go` | `go test ./internal/temporal/...` |
| `internal/appflow/` (any file) | `go test ./internal/appflow/...` |
| `internal/appflow/validate.go` | `go test ./internal/appflow/...` — Validate must stay pure (no DB, no I/O); a new node kind needs its rules here plus AF-* tests |
| `internal/appflow/workflow.go`, `nodes.go`, `graph.go` | `go test ./internal/appflow/...` — workflow code: must stay deterministic across replays (no I/O, no wall clock, no randomness) |
| `internal/appflow/activities.go` | `go test ./internal/appflow/...` + **rebuild and restart ALL dag workers** (`them-dag-worker`, `-2`, `-3`, `-4`, `-debug`; activities register at startup on each) — and no credential may be added to an activity INPUT type (persisted in Temporal history; guarded by AF-WF-14) |
| `internal/appflow/inline.go` | `go test ./internal/appflow/...` — and if a condition-expression example was added to the canvas properties panel, add a matching AF-IN-02b case |
| `cmd/dag-worker/main.go` | `go test ./... + go build ./cmd/dag-worker/` + **rebuild ALL dag-worker images (`docker compose build them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4 them-dag-worker-debug`), then recreate all with `up -d`.** They share one Dockerfile but each compose service builds its own separate image tag — rebuilding only `them-dag-worker` does NOT update `them-dag-worker-debug`'s image, so debug-mode runs (task queue `appflow-dag-debug`) silently keep running the old binary. Found live: a trace-detail fix compiled and passed tests, but debug runs kept showing stale output until all 3 images were rebuilt explicitly. |
| `internal/middleware/gate.go` (FileGate — scans A2A file artifacts against `middleware_wirings`) | `go test ./internal/middleware/...` — if the `resolveSecCfg`/`loadWiringCfg` scoping precedence changes, also `go test ./internal/appflow/...` (its `FileGateActivity` depends on this behavior) |
| `internal/admin/` (any file) | `go test ./internal/admin/...` |
| `internal/admin/dal/` (any file) | `go test ./internal/admin/...` |
| `internal/transport/transport.go` | `go test ./internal/ws/... ./internal/sse/...` |
| `internal/gate/gate.go` | `go test ./internal/gate/...` |
| `internal/ratelimit/limiter.go` | `go test ./internal/ratelimit/...` |
| `cmd/them/main.go` | `go test ./...` (full suite) |
| `go.mod` or `go.sum` | `go test ./...` (full suite) |
| `Dockerfile.go` | rebuild image + `go test -tags=integration ./...` |
| `docker-compose.yml` (Go labels) | `go test -tags=integration ./...` + live smoke test |
| Any `internal/` change | `go test ./...` before commit |
| Before any production deploy | `go test -race ./...` + `go test -tags=integration ./...` + live smoke tests |

---

## Key Architectural Decisions (quick ref)

| Decision | Choice | Where documented |
|---|---|---|
| Architecture | Monolith-first Go service | Package Map above |
| JWT auth | Local RS256 signature validation (no HTTP call) — user session tokens from auth service | `internal/auth/jwt.go` |
| Bearer token auth | Opaque token: L1 in-process sync.Map → L2 Redis `them:token:{sha256}` → PostgreSQL `them.access_tokens` | `internal/auth/token_cache.go` |
| Token revocation | Redis pub/sub `them:token:revoked` — cross-pod L1 eviction | `internal/auth/token_cache.go` |
| Session model | Atomic Lua + shadow TTL keys; Hash owned by session, Set membership owned by gate | `internal/session/session.go`, `internal/gate/gate.go` |
| Admission gate | Reservation pattern: Check (10s TTL) → Register → Confirm (90s TTL). Rollback on failure. Queue wake = re-compete. | `internal/gate/gate.go` |
| History loading | DB-level `LIMIT` not Python full-scan | `internal/orchestrator/orchestrator.go` |
| LLM cancellation | `context.Context` propagated to HTTP | `internal/llm/anthropic.go` |
| Temporal | Retained (Go SDK), HITL via Signal | `internal/temporal/workflow.go` |
| Message format | Canonical domain types in DB | `internal/domain/domain.go` |
| Tenant boundary | Application is the tenant | `docs/_archive/implementation-status.md` (history only) |
| Bus subscribe timing | Subscribe BEFORE StartWorkflow | `internal/ws/handler.go` line ~154 |
| ExecLua return type | Use `res.ToAny()` for Lua scripts that may return non-integer results (e.g. arrays) | `docs/_archive/lessons-learned.md` (history only; new entries go in `docs/lessons/<area>.md`) |
| AT TIME ZONE parsing | PG `AT TIME ZONE 'UTC'` on timestamptz returns timestamp without tz suffix — parseTS must handle both formats | `go/internal/admin/dal/tokens.go` |

---

## Go Binary Path (Linux/Mac)

```bash
export PATH="$HOME/go/bin:$PATH"
```

Windows PowerShell:
```powershell
$env:PATH = "$env:USERPROFILE\go-sdk\go\bin;$env:PATH"
$env:GOPATH = "$env:USERPROFILE\gopath"
$env:GOCACHE = "$env:USERPROFILE\gocache"
```
