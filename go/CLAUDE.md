# THEM Go Gateway — Claude Session Guide
# Last updated: 2026-07-19

---

## Read These First

Before touching any Go code, read:

| Doc | When to read |
|---|---|
| `TEST_INDEX.md` | Before adding or changing any test |
| `docs/architecture-v2/README.md` | Any architectural question |
| `docs/architecture-v2/lessons-learned.md` | Before any judgment call — what burned us before |
| `docs/architecture-v2/implementation-status.md` | Package inventory + which findings each package fixes |
| `DEPLOY_AND_TEST.md` | Before running a live deploy verification |

---

## This Project

**the-M Go Gateway** is a Go rewrite of the THEM AI orchestration platform.
Module: `github.com/aviciot/them` — source at `go/` in the repo root.
Port: **8002** (runs alongside Python bridge on 8001).

Language rules: UI/docs say **the-M**. Code identifiers use **them** / **THE_M_**.

---

## Package Map

| Package | Purpose | Key files |
|---|---|---|
| `cmd/them/` | Binary entrypoint, full wiring | `main.go` |
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
| `internal/domain/` | Canonical Message/Run types, status enums | `domain.go` |
| `internal/runrecorder/` | Run persistence to them.runs / run_steps / run_usage | `recorder.go` |
| `internal/llm/` | Provider interface, AnthropicProvider, MockProvider | `provider.go`, `anthropic.go`, `mock.go` |
| `internal/orchestrator/` | Agentic loop, DB-level history LIMIT | `orchestrator.go` |
| `internal/temporal/` | Workflow, activity, HITL signal, client | `workflow.go`, `activities.go`, `client.go`, `signaler.go` |
| `internal/ws/` | WebSocket handler | `handler.go` |
| `internal/sse/` | Server-Sent Events handler | `handler.go` |
| `internal/a2a/` | JSON-RPC 2.0 A2A server + agent card | `server.go` |
| `internal/agentregistry/` | A2A invocation, two-level Redis cache | `registry.go` |
| `internal/admin/` | CRUD API for agents/orchestrators/apps/runs | `agents.go`, `orchestrators.go`, `applications.go`, `runs.go` |
| `internal/ratelimit/` | Redis INCR rate limiter per-token + per-app | `limiter.go` |

---

## Go binary

```powershell
$env:PATH = "$env:USERPROFILE\go-sdk\go\bin;$env:PATH"
$env:GOPATH = "$env:USERPROFILE\gopath"
$env:GOCACHE = "$env:USERPROFILE\gocache"
```

---

## Common commands

```powershell
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

# Docker — build Go image
docker compose --profile go build them-go-bridge

# Docker — start Go bridge
docker compose --profile go up -d them-go-bridge

# Docker — watch logs
docker compose --profile go logs -f them-go-bridge
```

---

## Rules — Tests (non-negotiable)

1. **Every code change to `internal/` or `cmd/` MUST have a test** — new behavior = new test, changed behavior = updated test.
2. **`TEST_INDEX.md` MUST be updated in the same commit** — add the test row, update the count, update the trigger map.
3. **`go test ./...` must pass before every commit** — zero new failures.
4. **`go test -race ./...` must pass before every PR merge** — no data races.
5. **Never delete a test to make the suite pass** — fix the code or fix the test.

---

## Rules — Code

- No third-party JWT library — local RS256 via stdlib `crypto/rsa` only
- No ORM — pgx/v5 direct queries only
- No indirect dep pseudo-versions pinned in go.mod — let `go mod tidy` resolve
- Context cancellation must propagate from WS disconnect → Temporal cancel → LLM HTTP
- Event bus subscribe BEFORE Temporal workflow start (ready bootstrap handshake)
- Admin routes require JWT middleware (`RequireSuperAdmin`)
- All list endpoints return `[]` not `null` when empty
- Secrets never appear in log output — use `cfg.SafeString()`

---

## Rules — Documentation (mandatory)

| Change | Update |
|---|---|
| New test | `TEST_INDEX.md` (same commit) |
| New package | `TEST_INDEX.md` + `implementation-status.md` |
| New Redis key | `docs/architecture-v2/` (note the key + TTL + purpose) |
| Bug fix or non-obvious behavior | `docs/architecture-v2/lessons-learned.md` |
| New route | `implementation-status.md` route map |
| Architectural decision | `docs/architecture-v2/` (new or updated doc) |

---

## Trigger map — which tests to run after changing what

| Changed | Run |
|---|---|
| `internal/config/config.go` | `go test ./internal/config/...` |
| `internal/health/health.go` | `go test ./internal/health/...` |
| `internal/server/server.go` | `go test ./internal/server/...` |
| `internal/auth/jwt.go` | `go test ./internal/auth/...` |
| `internal/auth/token_cache.go` | `go test ./internal/auth/...` |
| `internal/session/session.go` | `go test ./internal/session/...` |
| `internal/event/bus.go` | `go test ./internal/event/...` |
| `internal/domain/domain.go` | `go test ./internal/domain/...` |
| `internal/runrecorder/recorder.go` | `go test ./internal/runrecorder/...` |
| `internal/llm/` (any file) | `go test ./internal/llm/...` |
| `internal/agentregistry/registry.go` | `go test ./internal/agentregistry/...` |
| `internal/ws/handler.go` | `go test ./internal/ws/...` |
| `internal/sse/handler.go` | `go test ./internal/sse/...` |
| `internal/a2a/server.go` | `go test ./internal/a2a/...` |
| `internal/admin/` (any file) | `go test ./internal/admin/...` |
| `internal/gate/gate.go` | `go test ./internal/gate/...` |
| `internal/ratelimit/limiter.go` | `go test ./internal/ratelimit/...` |
| `cmd/them/main.go` | `go test ./...` (full suite) |
| `go.mod` or `go.sum` | `go test ./...` (full suite) |
| `Dockerfile.go` | rebuild image + `go test -tags=integration ./...` |
| `docker-compose.yml` | `go test -tags=integration ./...` + T-01..T-05 from DEPLOY_AND_TEST.md |
| Any `internal/` change | `go test ./...` before commit |
| Before any production deploy | `go test -race ./...` + `go test -tags=integration ./...` + full DEPLOY_AND_TEST.md |

---

## Key architectural decisions (quick ref)

| Decision | Choice | Where documented |
|---|---|---|
| Architecture | Monolith-first Go service | `03b-alternatives-considered.md` |
| JWT auth | Local RS256 signature validation (no HTTP call) — user session tokens from auth service | `internal/auth/jwt.go` |
| Bearer token auth | Opaque token: L1 in-process sync.Map → L2 Redis `them:token:{sha256}` → PostgreSQL `them.access_tokens` | `internal/auth/token_cache.go` |
| Token revocation | Redis pub/sub `them:token:revoked` — cross-pod L1 eviction | `internal/auth/token_cache.go` |
| Session model | Atomic Lua + shadow TTL keys; Hash owned by session, Set membership owned by gate | `internal/session/session.go`, `internal/gate/gate.go` |
| Admission gate | Reservation pattern: Check (10s TTL) → Register → Confirm (90s TTL). Rollback on failure. Queue wake = re-compete. | `internal/gate/gate.go` |
| History loading | DB-level `LIMIT` not Python full-scan | `internal/orchestrator/orchestrator.go` |
| LLM cancellation | `context.Context` propagated to HTTP | `internal/llm/anthropic.go` |
| Temporal | Retained (Go SDK), HITL via Signal | `internal/temporal/workflow.go` |
| Message format | Canonical domain types in DB | `internal/domain/domain.go` |
| Tenant boundary | Application is the tenant | `06-domain-model.md` |
| Bus subscribe timing | Subscribe BEFORE StartWorkflow | `internal/ws/handler.go` line ~154 |
