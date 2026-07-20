﻿# Phase 5 — Migration Strategy

### Constraints Recap

- Same PostgreSQL schema throughout (no schema changes until Go is sole writer)
- Same Redis key namespace (`them:` prefix shared)
- Traefik is the routing toggle (labels control which container receives which routes)
- Go binary deployable alongside Python binary from day one
- MCP token format remains unchanged (opaque bearer, `them.access_tokens` table)

### Phase Toggle Convention

Traefik route priority determines which binary handles each path. Go routes have `traefik.http.routers.gateway-go.priority=100`; Python routes keep `priority=50`. Adding a route to the Go router automatically wins over the Python fallback for that path.

---

## Phase 5.1 — Foundation

**Objective**: The Go binary exists, passes all health checks, and connects to every dependency. Nothing routes production traffic to it yet.

**What runs in Go**: Health endpoints (`/healthz`, `/readyz`), startup validation (DB, Redis, Temporal connections), migration runner (reads goose version, runs zero migrations).

**What remains in Python**: Everything. All production traffic.

**Expected outcome**: `docker compose up them-gateway-go` starts cleanly, `/readyz` returns 200 with all dependencies healthy, logs appear in structured JSON.

**Validation strategy**:
- CI: build the Go binary, run `go vet ./...` and `go test ./internal/config/... ./internal/db/... ./internal/cache/...`
- Staging: run Go binary alongside Python, verify both `/readyz` endpoints return 200
- Smoke test: `curl https://staging-host/healthz` against Go service returns 200

**Rollback strategy**: Stop `them-gateway-go` container. No Traefik routes point to it yet. Zero impact on Python service.

**Implementation complexity**: Low

**Estimated duration**: 1 person-week

**Risks**:
- DB connection string format differs between Python (asyncpg DSN) and Go (pgx DSN) — requires config mapping
- Goose migration scan may fail if `them.goose_db_version` table doesn't exist yet (handled: Goose creates its table on first run)

---

## Phase 5.2 — Auth

**Objective**: The Go binary handles all token validation. Python no longer calls the auth service for JWT validation.

**What runs in Go**: All JWT validation (local RS256), all bearer token validation (three-layer cache, L1 TTL 60s), token revocation pub/sub listener, MCP token validation endpoint, admin token CRUD endpoints (`/api/v1/tokens/*`).

**What remains in Python**: All orchestration, all session management, all LLM calls, all agent calls.

**Implementation note — temporary Python-to-Go HTTP hop**: Python's `auth_client.py` currently calls `them-auth-service:8701` on every JWT validation request. In this phase, `them-auth-service` is replaced by the Go binary. Python config `AUTH_SERVICE_URL` is updated to point to `them-gateway-go:8001`. The Go binary exposes `/api/v1/auth/verify` and `/api/v1/mcp/tokens/validate` with the same response shape as the original auth service.

This introduces a **Python → Go HTTP call** on every authenticated Python request. This is intentional and temporary: it consolidates auth in Go while Python still handles orchestration. The hop adds ~1ms of loopback latency. This hop is eliminated in Phase 5.4 when Python no longer handles any entry-point connections and Go validates tokens locally at the edge.

**Revocation guarantee in this phase**: Go pods receive pub/sub revocations in ~1ms. Python pods rely on Go forwarding revocations — Python calls Go's `/api/v1/auth/verify` which checks L1→L2→DB, so Python is always authoritative (no Python-side L1 stale window).

**Expected outcome**: Python JWT and bearer validation calls route to Go. Token revocation propagates to all Go pods in ~1ms (pub/sub); Python validation is always current (hits Go's cache which was just updated).

**Validation strategy**:
- Unit tests: auth package with `testcontainers-go` (Redis for L2 and revocation pub/sub)
- Integration test: start Python + Go in staging, issue a JWT, validate via Python â†’ Go path, revoke the JWT, verify immediate rejection on the next call
- Comparison test: shadow mode — Python calls both the old auth service and the new Go endpoint, compares responses for 1000 requests

**Rollback strategy**: Update `AUTH_SERVICE_URL` to point back to the original auth service. No DB changes involved.

**Implementation complexity**: Medium

**Estimated duration**: 2 person-weeks

**Risks**:
- RS256 public key sourcing: the existing auth service may use a different key format (PKCS#1 vs PKCS#8). Must verify key format before cutover.
- L1 cache cold start: on Go startup, L1 is empty. First validations hit L2/DB. Under traffic spike at startup, this is acceptable.
- Bearer token `enabled` flag: Python checks `enabled` column; Go must also check it. Must verify the DB query includes this condition.

---

## Phase 5.3 — Admin APIs

**Objective**: All `/api/v1/*` CRUD operations (agents, orchestrators, LLM providers, applications, entry points, middleware, monitoring, tokens, sessions) are handled by Go. Python admin routes are removed from Traefik.

**What runs in Go**: All admin REST endpoints, all admin authentication (via Phase 5.2 auth), audit logging, pub/sub invalidation publishing on every write operation.

**What remains in Python**: All WS/SSE/A2A/REST entry points, all orchestration.

**Implementation note**: The admin API is the lowest-risk migration target — it is read/write CRUD with no streaming, no Temporal, no complex state. It is also the highest-frequency source of bugs (the Python admin API has several JSONB serialization inconsistencies). The Go implementation fixes these as part of the port.

**Expected outcome**: Admin dashboard operations (create agent, update orchestrator, view sessions) all route to Go. JSONB serialization is consistent. Audit log entries appear for all write operations.

**Validation strategy**:
- End-to-end test: exercise every admin API endpoint against Go in staging, compare responses to Python using a recorded request/response corpus
- Database state: after each write operation, verify the DB row matches the request payload exactly (no serialization loss)
- Audit log: verify `them.audit_log` entries created for every POST/PUT/DELETE
- Load test: 100 concurrent admin reads (agent list) against Go endpoint, verify P99 < 20ms

**Rollback strategy**: Update Traefik labels to route `/api/v1/*` back to Python. DB state is compatible (same schema, additive writes only).

**Implementation complexity**: Medium

**Estimated duration**: 3 person-weeks

**Risks**:
- JSONB round-trip fidelity: Python stores some JSONB with Python-specific types (e.g., UUID as string in some fields, integer in others). Go must match the existing serialization exactly to avoid breaking Python consumers of the same data.
- Pagination format: Python returns different pagination shapes across endpoints. Go must match the existing shapes (not fix them yet).
- `agent_registry` invalidation: Go admin writes must publish `them:agents:changed`. Python's `agent_registry.py` must receive and process these signals — verify the existing Python pub/sub listener handles signals from a Go publisher.

---

## Phase 5.4 — Entry Points

**Objective**: Go handles all entry-point connections (WebSocket, SSE, A2A server, REST `/runs`, Voice). Orchestration calls are forwarded from Go edge adapters to the Python Temporal workflow starter (or directly to Temporal). Python no longer handles any inbound client connections.

**What runs in Go**: All WebSocket connections, all SSE streams, A2A server (receives incoming A2A requests), REST runs endpoint, Voice STT/TTS endpoint, WebRTC endpoint. All session management (using the Go `session.Store`). All gate enforcement (rate limit, session cap).

**What remains in Python**: All Temporal workflow execution (activities, LLM calls, agent calls). Python Temporal worker remains running.

**Implementation detail**: Go edge adapters start Temporal workflows directly using the Go Temporal client — the same Temporal namespace, same workflow type name (`OrchestrationWorkflow`). Python Temporal worker continues executing workflows. This works because Temporal is language-agnostic: the Go client starts workflows that the Python worker picks up, and vice versa. Workflow definitions must be kept in sync between Go and Python during this overlap phase.

**Expected outcome**: All client connections terminate at Go. Session counts are accurate (atomic counter). Gate enforcement is correct (no ghost sessions). Temporal workflows execute in Python.

**Validation strategy**:
- Session accuracy: connect 10 concurrent WebSocket clients, verify `them:pod:{go_pod_id}:sess_count` reports exactly 10, verify `/api/v1/sessions` admin endpoint shows all 10
- Gate enforcement: configure EP with `max_concurrent_sessions = 3`, attempt 5 simultaneous connections, verify 3 succeed and 2 receive the queue response (`{type:"waiting"}`)
- Gate contract: for each admitted session, verify Redis state after each step:
  - After `Gate.Check()`: EP set contains session_id, shadow key exists with TTL ≤ 10s
  - After `session.Register()`: Hash exists with TTL ~90s; shadow still at ≤ 10s
  - After `Gate.Confirm()`: shadow TTL refreshed to ~90s
  - After `Gate.Rollback()` (inject Register failure): EP set does NOT contain session_id, shadow key deleted, queue receives "1" signal
  - After session end: EP set does NOT contain session_id, Hash deleted, shadow deleted, queue receives "1" signal
- Ready bootstrap: verify no token events are lost — connect client, start a run, assert the first token event is received (tests the pre-allocated run_id + single-channel design)
- A2A parity: send A2A `SendMessage` requests to Go endpoint, verify same JSON-RPC response format as Python
- Cancellation: connect WS client, start a run, send `{"type":"cancel"}`, verify Temporal workflow cancelled within 2s

**Rollback strategy**: Update Traefik to route all WS/SSE/A2A/REST paths back to Python. Active WebSocket connections will be dropped (clients reconnect to Python). Active Temporal workflows continue running in Python worker. Sessions in Go's Redis keys expire within 90s.

**Implementation complexity**: High

**Estimated duration**: 4 person-weeks

**Risks**:
- Temporal workflow start signature: the Go `WorkflowInput` struct must serialize identically to the Python `start_orchestration_workflow()` kwargs for the Python worker to accept the workflow. Use `json.RawMessage` for the token payload field to avoid type mismatch.
- WebSocket protocol parity: Go must emit identical JSON event shapes (`{"type":"token","text":"..."}` etc.) as Python. Any deviation breaks existing clients.
- Voice endpoint: STT/TTS involves external service calls (Deepgram, ElevenLabs). These are straightforward HTTP proxies in Python; verify the Go implementation handles binary audio data correctly.
- WebRTC/LiveKit: LiveKit token generation (Go SDK available) must produce tokens with identical claims structure to the Python implementation.

---

## Phase 5.5 — Orchestration

**Objective**: Go Temporal worker executes all orchestration workflows and activities. Python Temporal worker is stopped. This is the highest-complexity phase.

**What runs in Go**: Go Temporal worker (all activities: `LoadContextActivity`, `InitRunActivity`, `PlanTurnActivity`, `InvokeAgentActivity`, `RecordToolResultsActivity`, `SummarizeContextActivity`, `FinalizeRunActivity`). All LLM provider calls. All A2A agent calls.

**What remains in Python**: Nothing (Python process stopped after validation).

**Implementation note**: Temporal workflows that were started by Go in Phase 5.4 and are mid-execution in the Python worker must complete in Python before the Python worker is stopped. Strategy: enable the Go worker in Temporal with a new `task_queue = "them-go"`. Route new workflow starts to `them-go`. Wait for all workflows on `them-py` task queue to drain (monitor Temporal UI for pending workflows). Stop Python Temporal worker. Switch new workflow starts to `them-go` only.

**Expected outcome**: Full Go execution stack. LLM streaming, agent invocations, HITL signals, sub-orchestrators, continue_as_new all execute in Go. Python process is not running.

**Validation strategy**:
- Functional parity test suite: replay 50 recorded production runs against Go orchestration, compare final outputs, token counts, and tool call sequences
- Streaming: verify token-by-token streaming works end-to-end (WS client receives `token` events in real time)
- HITL: trigger a human-response signal via REST, verify workflow resumes correctly
- Sub-orchestrators: run a workflow that delegates to a sub-orchestrator, verify delegation completes
- continue_as_new: run a workflow that exceeds `max_iterations_before_continue`, verify it continues correctly
- Budget: set a `daily_budget_usd`, exceed it, verify workflow rejects new runs with the correct error

**Rollback strategy**: Re-enable Python Temporal worker on `them-py` task queue. Route new workflow starts back to `them-py`. Active Go workflows complete in Go (Go worker stays running temporarily). Once Go workflows drain, stop Go worker. This rollback requires the Python worker binary to still be available — do not remove Python deployable until Phase 5.6 validation period ends.

**Implementation complexity**: High

**Estimated duration**: 5 person-weeks

**Risks**:
- LLM streaming accuracy: Anthropic's Go SDK streaming may emit different event shapes than the Python SDK. Must validate token accumulation, tool_use detection, and stop_reason handling match expected behavior.
- `cache_control` placement: the Anthropic cache_control injection strategy must produce cache hits at the expected rate. Validate by monitoring `cache_creation_input_tokens` and `cache_read_input_tokens` in Anthropic usage metadata.
- A2A task state machine: the Go A2A adapter must handle all terminal states (completed/failed/canceled/rejected and their lowercase aliases) correctly. Missing a state causes infinite polling.
- Message format migration: if any existing `them.task_messages` rows contain Python-specific serialization in the `parts` JSONB, the Go history loader must handle both the old and new formats. Add a format version field to `parts` or use a migration query.

---

## Phase 5.6 — Full Cutover

**Objective**: Python binary is removed. Go binary is the sole service. Alembic is decommissioned (Goose takes over schema management).

**What runs in Go**: Everything.

**What remains in Python**: Nothing.

**Steps**:
1. Confirm no Python Temporal worker activity for 72 hours (monitor Temporal UI)
2. Remove Python container from `docker-compose.yml`
3. Remove Python Traefik fallback routes
4. Archive Python codebase (do not delete — retain for reference)
5. Update `alembic_version` to a terminal state (last migration version)
6. Transfer schema authority to Goose: first Goose migration creates `them.goose_db_version` with the equivalent of the final Alembic state

**Expected outcome**: Single binary deployment. No Python dependencies in the runtime environment.

**Validation strategy**:
- Full production smoke test: exercise every endpoint type (WS, SSE, A2A, REST, Voice) against production Go service
- 24-hour soak test: monitor error rates, P99 latency, and session counts in production for 24 hours post-cutover
- Compare error rates to the Python baseline (previous 7-day average)

**Rollback strategy**: Re-deploy Python container from the retained image tag. Update Traefik labels. This rollback path exists for 14 days after cutover (retain Python image in registry).

**Implementation complexity**: Low

**Estimated duration**: 1 person-week

**Risks**:
- Silent behavioral differences in long-tail edge cases not covered by the parity test suite. Mitigation: maintain the Python binary in the registry for the 14-day rollback window.
- Third-party integrations (LiveKit, Deepgram, ElevenLabs) may behave differently when called from Go vs Python. Validate these integrations explicitly in Phase 5.4 before removing Python.

---

## Phase 5.7 — Post-Migration

**Objective**: Clean up technical debt, transfer schema ownership, harden observability, and implement the improvements that were deferred to avoid risk during migration.

**Work items**:

1. **Schema cleanup**: remove `them.goose_db_version` vs `them.alembic_version` coexistence. Write Goose migration that drops `alembic_version`. Normalize JSONB column serialization inconsistencies (e.g., unified pagination format across endpoints).

2. **Session stickiness elimination**: implement WS reconnect-on-any-pod protocol. Client sends `context_id` in reconnect message; Go pod re-subscribes to `them:run:{context_id}:events` and resumes event relay. Remove Traefik sticky session cookie.

3. **`express` orchestrator type**: single-turn orchestrators (no iteration, no tool use) bypass Temporal and execute directly in the edge handler. Reduces single-turn latency from ~300ms (Temporal workflow start) to ~50ms.

4. **OTel trace-to-Temporal correlation**: implement search attribute propagation so Temporal UI links to OTel trace, and OTel traces link to Temporal workflow history.

5. **`sqlc` code generation**: optionally add `sqlc` for query type-safety. The hand-written queries from Phase 5.3-5.5 become the baseline. `sqlc.yaml` is configured against the final Go-owned schema.

6. **Load testing**: run `k6` against production-like staging with 500 concurrent WebSocket sessions, validate P99 latency, session counter accuracy, and graceful drain behavior.

7. **Secrets rotation automation**: implement key rotation for the `SECRET_KEY` (re-encrypt all stored agent tokens and LLM API keys in a background migration job) and for the JWT RS256 key pair (automate the rotation watcher test).

8. **Remove dead Python infrastructure**: remove `them-auth-service` container (replaced by Go auth), remove Python-specific Traefik labels, remove `asyncpg`/`aioredis` from any remaining infrastructure documentation.

**Estimated duration**: 3 person-weeks

**Risks**: Low. This phase makes no user-visible changes. All items are internal improvements with independent rollback paths.

---

## Phase Summary Table

| Phase | Go Handles | Python Handles | Complexity | Duration |
|---|---|---|---|---|
| 5.1 Foundation | Health endpoints, dependency checks | Everything | Low | 1 week |
| 5.2 Auth | JWT/bearer validation, token CRUD | Everything else | Medium | 2 weeks |
| 5.3 Admin APIs | All `/api/v1/*` CRUD | Entry points, orchestration | Medium | 3 weeks |
| 5.4 Entry Points | WS/SSE/A2A/REST connections, sessions, gate | Temporal worker, LLM, agents | High | 4 weeks |
| 5.5 Orchestration | Full Temporal worker + LLM + agents | Nothing | High | 5 weeks |
| 5.6 Full Cutover | Everything | Nothing | Low | 1 week |
| 5.7 Post-Migration | Schema, observability, WS stickiness | — | Low | 3 weeks |
| **Total** | | | | **~19 person-weeks** |

The total estimate assumes one senior Go engineer owning the work with periodic review from a second engineer. Phases 5.4 and 5.5 can be parallelized across two engineers (entry point adapters vs. Temporal activity ports) reducing the critical path by ~3 weeks.