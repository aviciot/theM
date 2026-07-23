# the-M — Go Runtime Migration Plan (Python → Go Gateway)

> **Status:** implementation-ready. Derived from source, not docs. Where this
> plan and the inventory diverge, this plan wins (it re-verified the seam code).
> **Companion doc:** `API_RUNTIME_MIGRATION_INVENTORY.md` (route-by-route surface).
> **Baseline commit:** `823a77a` already pushed. **Repo root Python = `app/`.**
> **Go module:** `github.com/aviciot/them`, source at `go/`, port **8002**.

---

## 0. Source-verified corrections to the inventory

Two facts the inventory got *directionally* right but that must be nailed down
because the whole plan pivots on them. Both were re-verified against the code:

1. **The Go→Python `run_id` handoff is BROKEN in the current repo-root code.**
   - Go passes `RunID: runID` (a fresh Go UUID) in
     `temporal.PythonOrchestrationInput` (`go/internal/ws/handler.go:451`,
     `go/internal/sse/handler.go:459`) and sets `wfOpts.ID = runID`
     (`handler.go:457`).
   - Go's own comment (`go/internal/temporal/python_input.go:7-11`) claims
     "Python uses it verbatim … Python falls back to `workflow.uuid4()` when
     RunID is absent."
   - **This is not true today.** `app/temporal/shared.py:16` `OrchestrationInput`
     has **no `run_id` field**, and `app/temporal/workflows.py:134`
     **unconditionally** does `generated_run_id = str(workflow.uuid4())`. The
     Go-supplied run_id is silently dropped. Python then publishes events on
     `them:dash:run:{python_uuid}:tokens` while Go subscribes to
     `them:dash:run:{go_uuid}:tokens`. **Go receives nothing.**
   - There is a Go-side integration test that *expects* the handoff to work
     (`go/internal/temporal/hybrid_integration_test.go` T1
     "GoProvidedRunIDPreservedEndToEnd") — it will FAIL against the current
     Python worker. This is the canary for Prerequisite P1.

2. **Redis Streams (`:stream`) has no producer in repo-root `app/`.**
   `grep -rn "xadd\|XADD\|stream_publish" app/temporal/` returns nothing.
   `app/temporal/stream_publish.lua` and `db/025_events_transport.sql` are
   referenced by `theM_gateway/CLAUDE.md`'s trigger map but **do not exist** in
   `app/` yet. So Go's `dual`/`streams` mode reads an empty stream. Today only
   Pub/Sub `:tokens` carries events. This is why Prerequisite P2 recommends
   **staying on Pub/Sub** for Wave 4.

Everything below is written to survive both facts.

---

## Pre-Migration Prerequisites (do before any wave)

### P0. Baseline & safety net (done / verify)
- Baseline commit `823a77a` pushed. ✔
- Confirm rollback lever exists: all product routes are Python via Traefik
  labels in `theM_gateway/docker-compose.yml:198-217` + file provider
  `theM_gateway/traefik/dynamic.yml`. Reverting any Go router = deleting one
  label block. Keep `dynamic.yml` and the compose labels under version control
  so a `git revert` of a single hunk is the rollback.
- Bring Go into the routable mesh **read-only** first: it already answers
  `/go-health/*` (`docker-compose.traefik.yml:49-52`). No new exposure yet.

### P1. Workflow-ID scheme — **DECISION: Option (b), Python accepts Go's run_id.**

**Options considered:**
- (a) Go adopts Python's `ctx-{context_id}` as the workflow ID.
- (b) Python worker accepts a caller-supplied `run_id` (and keeps deriving the
  Pub/Sub channel from it); Python still owns the *workflow ID* as `ctx-…`.
- (c) Add a `run_id → context_id` lookup table (extra DB round-trip per start).

**Recommendation: (b), with a precise scope.** Rationale:
- The seam that actually breaks is **not** the Temporal workflow ID — it is the
  **Redis channel key**, which Python derives from the *run_id* it invents in
  `init_run` (`activities.py`). If Python honors the caller's `run_id`, the
  channel `them:dash:run:{run_id}:tokens` lines up and Go receives events. The
  Temporal workflow ID can stay `ctx-{context_id}` on the Python side — Go does
  not need to know it (Go only subscribes to Redis and calls `wfRun.Get`).
- Option (a) forces a large rewrite of Go's WS/SSE handlers (they are built end
  to end around a run-UUID workflow ID and stamp `run.ID = runID`), and it makes
  Go responsible for the `ctx-` dedup/resume semantics that live in Python's
  `bridge_client.start_orchestration_workflow` (the `WorkflowAlreadyStartedError`
  / `DeadContextError` path, `bridge_client.py:105-146`). High risk, no payoff.
- Option (c) adds a DB query on the hottest path and a new table (the task
  constraints forbid schema changes anyway). Rejected.
- Option (b) is the smallest, most local change and is exactly what Go's code +
  its integration test *already assume*. It makes the existing (failing) Go test
  pass rather than inventing a new contract.

**Exact change (Python side, P1):**
- `app/temporal/shared.py` — add to `OrchestrationInput` (after
  `entry_point_slug`, line ~33):
  ```python
  run_id: Optional[str] = None  # caller-supplied (Go bridge); None → workflow generates
  ```
- `app/temporal/workflows.py:134` — replace
  ```python
  generated_run_id = str(workflow.uuid4())
  ```
  with
  ```python
  generated_run_id = inp.run_id or str(workflow.uuid4())
  ```
  (`workflow.uuid4()` stays the deterministic fallback for Python-native
  callers, preserving replay determinism.)
- **No change to `init_run_activity`** — it already receives `generated_run_id`
  as the `run_id` arg (`workflows.py:146`) and publishes on
  `them:dash:run:{actual_run_id}:tokens` (`activities.py:327,349`). Because
  `start_run` may re-mint the UUID (see the `actual_run_id` TODO at
  `activities.py:302-304`), **P1 also requires** making `run_recorder.start_run`
  honor a pre-set run_id: pass `generated_run_id` through and `INSERT … id =
  :run_id` instead of DB-default. Verify `actual_run_id == generated_run_id`
  after the call; if they differ, the channel breaks. Add an assertion + log.
- Python still starts the workflow with `id = f"ctx-{context_id}"`
  (`bridge_client.py:72`). **Go does not set the Temporal ID to match** — Go's
  `wfOpts.ID = runID` only applies when *Go* starts the workflow. In the
  Go→Python hybrid path, **Go must start the workflow with a `ctx-{context_id}`
  ID too**, or the two paths will collide on IDs when both are live. See Wave 4a.

**Test gate for P1:** `go test -tags=integration ./internal/temporal/...` T1–T4
(`hybrid_integration_test.go`) pass against a live Python worker; Python full
suite (`python3.12 scripts/tests/run_tests.py`) 0 new failures; restart
`them-worker` after editing activities/workflows.

### P2. Redis event transport for Wave 4 — **DECISION: Option A first (stay on Pub/Sub `:tokens`).**

- **Option A:** keep Pub/Sub `them:dash:run:{run_id}:tokens` as Go's consumer.
  Go already reads it (`go/internal/runstream/stream.go:124`), Python already
  produces it (`activities.py:349,398,485,…`). **Zero new producer code.**
- **Option B:** add XADD to the Python worker, then switch Go to Streams.

**Recommendation: do Option A for the entire Wave 4 cutover.** Reasons:
- The Streams producer **does not exist** (see §0.2). Building it is net-new
  Lua + activity work with its own test burden and is orthogonal to moving
  traffic to Go.
- Go's dispatcher already falls back to Pub/Sub when
  `RUN_EVENTS_MODE=pubsub` (the prod default; `main.go:185`,
  `runstream.NewDispatcher`). Set Go's `RUN_EVENTS_MODE=pubsub` for the cutover.
- Streams (Option B) becomes **Wave 4-optional / Phase 11c-D**: build the XADD
  producer, flip `RUN_EVENTS_MODE=dual` to validate parity, then `streams`.
  Sequenced *after* live traffic is on Go so replay/`Last-Event-ID` is an
  enhancement, not a cutover dependency.

**Cutover env:** `them-go-bridge` `RUN_EVENTS_MODE: "pubsub"` (override the
`dual` currently set in `docker-compose.integration.yml:78`).

### P3. Dual-invalidation channel unification

Python busts the agent registry on `them:agents:changed`
(`app/services/agent_registry.py` listener); Go's `internal/agentregistry`
subscribes to `them:agents:invalidate` (**different name**). Both must converge
on one channel or a write on one runtime won't invalidate the other.

**Exact change (pick Python's name `them:agents:changed` as canonical, since it
is the one currently in production):**
- Go: in `go/internal/agentregistry/registry.go`, change the subscribed channel
  constant from `them:agents:invalidate` to `them:agents:changed`. Grep:
  `grep -rn "agents:invalidate\|agents:changed" go/internal/agentregistry/`.
  Update the publish side too if the registry ever publishes.
- Go admin writes (Wave 2) must **PUBLISH `them:agents:changed`** (not
  `invalidate`) after agent create/update/delete so Python pods bust their
  registry. Add to `go/internal/admin/agents.go` write handlers.
- Update `go/TEST_INDEX.md` agentregistry test rows + `docs/REDIS.md`.
- **Test gate:** `go test ./internal/agentregistry/...`; manual: write an agent
  via Python admin → confirm Go registry reloads (log line) and vice-versa.

Do the analogous unification for the two other cross-runtime gaps (both handled
in Wave 2, listed here so they aren't forgotten):
- **Token revocation:** Python admin token delete/update does direct
  `DEL them:session:token:{sha256}` only (no pub/sub). Go evicts L1 via
  `them:token:revoked` (`go/internal/auth/token_cache.go`). Python must **also**
  `PUBLISH them:token:revoked {sha256}` on token invalidation so Go pods evict.
- **EP-config invalidation:** Python `admin_applications` does direct DEL; Go
  subscribes to `them:ep:config:changed` (`main.go:176`). Python must **also**
  publish `them:ep:config:changed {app_id}` on app/EP writes.

---

## Wave 1 — Stateless reads + health (zero risk, first) — ~10h

Goal: move read-only, no-Redis, no-Temporal endpoints to Go behind Traefik with
identical paths, so a bad response can only affect GET traffic and is revertible
in seconds.

### 1.1 Routes that ALREADY exist in Go — only need a Traefik rule change

| Route | Go handler | Notes |
|---|---|---|
| `GET /health/live`, `GET /health/ready` | `health.Live/Ready` (`go/internal/health/health.go:53,63`) | exist; today only reachable via `/go-health/*` rewrite |
| `GET /api/v1/admin/agents`, `/agents/{id}` | `go/internal/admin/agents.go` | path-identical to Python |
| `GET /api/v1/admin/orchestrators` | `go/internal/admin/orchestrators.go` | **keyed by `{name}` for get — do not route GET-by-id yet** |
| `GET /api/v1/admin/applications`, `/applications/{id}` | `go/internal/admin/applications.go` | path-identical |
| `GET /api/v1/runs`, `/runs/{id}` | `go/internal/admin/runs.go` | **auth divergence: Go = jwt-only, no ownership scoping** |

### 1.2 New Go code required in Wave 1 (still read-only)

| Route | New Go file | Why not yet in Go |
|---|---|---|
| `GET /health` (bare, no `/api/v1`, no auth) | add `Bare()` to `go/internal/health/health.go`; mount in `go/internal/server/server.go` | frontend `/api/bridge/health` hits `/health` with no auth; Go only has `/health/live|ready`. Must return `{status,db,redis,redis_db,instance_id}` shape. |
| `GET /api/v1/admin/orchestrators/{id}` (by **id**) | extend `orchestrators.go` with an id-keyed lookup | Go keys by `{name}`; frontend sends `{id}`. Add id route **in addition** (do not remove name route). |
| `GET /api/v1/runs/stats`, `/runs/contexts`, `/runs/{id}/tasks`, `/runs/{id}/artifacts`, `/runs/context/{context_id}/artifacts` | new handlers in `go/internal/admin/runs.go` | absent in Go |
| `GET /apps/{slug}` (catalogue/liveness ping) | new `go/internal/apps/catalogue.go` + mount `/apps` | absent; frontend `/api/apps/{slug}` liveness |
| `GET /.well-known/agent-card.json` | alias in `go/internal/a2a/server.go` (Go serves `/.well-known/agent.json`) | filename mismatch; add the `-card.json` alias route |

**Auth-scoping fix (Wave 1, required before routing `/runs`):** Go `runs.go`
must replicate Python ownership scoping (`runs.py`: non-admin → `user_id ==
Run.user_id`; admin/superadmin → all). Without it, a non-admin user sees every
run. Add role check mirroring `RequireSuperAdmin` + per-row `user_id` filter.

### 1.3 Exact Traefik label changes (add to `them-go-bridge`, route to `them-go-svc`)

Add these routers to `theM_gateway/docker-compose.traefik.yml` (labels) **and**
mirror in `theM_gateway/traefik/dynamic.yml`. **Higher priority than the Python
`them-*` routers (100) so Go wins the match; use `Method(...)` guards to keep
writes on Python during Wave 1.**

```yaml
# Bare health (no auth) — highest, exact prefix
- "traefik.http.routers.them-go-health-bare.rule=Path(`/health`)"
- "traefik.http.routers.them-go-health-bare.priority=130"
- "traefik.http.routers.them-go-health-bare.entrypoints=web"
- "traefik.http.routers.them-go-health-bare.service=them-go-svc"

# Read-only admin + runs GETs (Method guard keeps writes on Python)
- "traefik.http.routers.them-go-reads.rule=(PathPrefix(`/api/v1/admin/agents`) || PathPrefix(`/api/v1/admin/orchestrators`) || PathPrefix(`/api/v1/admin/applications`) || PathPrefix(`/api/v1/runs`)) && Method(`GET`)"
- "traefik.http.routers.them-go-reads.priority=110"
- "traefik.http.routers.them-go-reads.entrypoints=web"
- "traefik.http.routers.them-go-reads.service=them-go-svc"

# /apps catalogue GET only
- "traefik.http.routers.them-go-apps-read.rule=PathPrefix(`/apps`) && Method(`GET`)"
- "traefik.http.routers.them-go-apps-read.priority=110"
- "traefik.http.routers.them-go-apps-read.entrypoints=web"
- "traefik.http.routers.them-go-apps-read.service=them-go-svc"
```

> Note: `/apps/{slug}/ws` is an HTTP GET upgrade — the `Method(GET)` guard would
> catch it. In Wave 1 the frontend WS still targets `/apps/{slug}/ws`; to avoid
> Go intercepting WS before Wave 4, **scope the apps-read router to the exact
> catalogue path** instead: `Path(\`/apps/{slug}\`)` is not expressible, so use
> ``PathRegexp(`^/apps/[^/]+$`) && Method(`GET`)`` which excludes `/apps/{slug}/ws`.

### 1.4 Test gate (must pass before AND after the Wave 1 cutover)
- **Python:** `01 02 03 04 15` (sanity: DB/Redis/auth/bridge/compose health),
  `05` (agents CRUD — the list GET), `06` (orchestrators), `12` (runs auth),
  `20` (Traefik routing + multi-replica), `22` (applications).
- **Go:** `go test ./internal/health/... ./internal/admin/... ./internal/apps/...`
  + `go test -tags=integration ./...` for the new routes.
- **Manual contract check:** `curl :8088/health` returns Python-shape JSON;
  `curl :8088/api/v1/admin/agents` (Go) vs the same on a reverted config returns
  byte-comparable JSON. Non-admin JWT sees only own runs.

---

## Wave 2 — Admin writes + missing admin entities — ~40h

Every write touches Postgres + a cache-invalidation pub/sub. During the
transition **both runtimes must dual-publish** the canonical invalidation
channels (§P3) so a write served by Go busts Python's cache and vice-versa.

**Global Wave-2 rule — verb parity:** frontend sends **PATCH** for updates
(`admin_agents.py` etc.); Go currently uses **PUT**. **Go adds PATCH routes**
(keep PUT as an alias for Go-native callers). Do **not** change the frontend.
Add `r.Patch(...)` next to each `r.Put(...)` in the Go admin routers, both
dispatching the same handler.

**Global Wave-2 rule — id vs name keying:** frontend keys orchestrators by
`{id}`; Go keys by `{name}`. Add id-keyed PATCH/DELETE routes to
`go/internal/admin/orchestrators.go` alongside the name routes.

**Global Wave-2 rule — soft vs hard delete:** Python DELETE is a **hard**
`DELETE` for agents (`admin_agents.py:477`) and cascade for apps; Go does
`enabled=false` soft-delete. Match Python: Go DELETE must hard-delete (agents)
and cascade (applications) to keep list results identical. Preserve FKs — cascade
via existing FK `ON DELETE`, **do not drop any FK**.

### 2.1 tokens — **new** — `go/internal/admin/tokens.go`
- **Routes:** base `/api/v1/admin/tokens`; `GET /` (q `user_id?`), `POST /`
  (201, plaintext once), `GET /{id}`, `PATCH /{id}`, `DELETE /{id}`.
- **DB:** `them.access_tokens` (+ `them.orchestrators` for scope validation on
  create). SQLx/pgx: `SELECT … FROM them.access_tokens WHERE …`; INSERT returns
  the row; the plaintext token is generated in Go, `sha256` stored.
- **Redis invalidation:** on PATCH/DELETE → `DEL them:session:token:{sha256}`
  **and PUBLISH `them:token:revoked` {sha256}** (so both runtimes evict L1).
- **Struct mapping (Pydantic `TokenCreate/TokenUpdate/TokenOut`
  `admin_tokens.py` → Go):**
  | Pydantic | Go struct field | json |
  |---|---|---|
  | `name: str` | `Name string` | `name` |
  | `user_id: int` | `UserID int64` | `user_id` |
  | `orchestrator_id: UUID?` | `OrchestratorID *string` | `orchestrator_id` |
  | `expires_at: datetime?` | `ExpiresAt *time.Time` | `expires_at` |
  | `enabled: bool` | `Enabled bool` | `enabled` |
  | (out) `token: str` (once) | `Token string` | `token` |
- **PATCH vs PUT:** add PATCH (frontend). **Test gate:** Go `admin` package
  tests + Python `08 09` (tokens CRUD + cache).

### 2.2 llm-providers — **new** — `go/internal/admin/llm_providers.go`
- **Routes:** `/api/v1/admin/llm-providers` GET/POST/`{id}` GET/PATCH/DELETE;
  `GET/PUT /routing/config`.
- **DB:** `them.llm_providers`; routing config in `them.config` key
  `llm_routing` (JSON blob GET/PUT).
- **Redis:** none for provider CRUD (Python has none). Routing config PUT: no
  pub/sub in Python — match (no invalidation).
- **Struct mapping** (`admin_llm_providers.py` `ProviderCreate/Out`): `name`,
  `provider_type`, `base_url`, `api_key_encrypted`, `models: []`, `enabled`,
  `priority`. Keep field names verbatim in json tags.
- **Test gate:** Go admin tests; Python has no dedicated test — add a Go unit
  test mirroring the Python schema and update `go/TEST_INDEX.md`.

### 2.3 middleware-defs — **new** — `go/internal/admin/middleware.go`
- **Routes:** `/api/v1/admin/middleware-defs` GET/POST/`{id}` GET/PATCH/DELETE.
- **DB:** `them.middleware_defs`.
- **Redis:** on create/update/delete → `SCAN + DEL them:mw:chain:*` (all apps),
  matching `admin_middleware.py:57`. Implement a SCAN-based delete helper in Go
  `internal/cache`.
- **Also:** `PUT /api/v1/admin/applications/{id}/middleware-wirings`
  (`admin_applications.py:966`) → `them.middleware_wirings`; invalidate
  `SCAN + DEL them:mw:chain:{app_id}:*`. Add to `applications.go`.
- **Struct mapping** (`MiddlewareDefIn/Out`): `name`, `mw_type`, `config: json`,
  `enabled`.
- **Test gate:** Go admin tests; add mw-chain invalidation test.

### 2.4 system-agents — **new** — `go/internal/admin/system_agents.go`
- **Routes:** `GET/PUT /api/v1/admin/system-agents`;
  `POST /api/v1/admin/system-agents/{role}/test-llm` (external LLM probe).
- **DB:** `them.config` key `system_agents` (JSON GET/PUT).
- **Redis:** none.
- **Struct mapping:** free-form JSON object of `{role: {provider, model, …}}`;
  model as `json.RawMessage` to avoid over-specifying.
- **Test gate:** Go admin tests.

### 2.5 monitoring-config — **new** — `go/internal/admin/monitoring.go`
- **Routes:** `GET/PUT /api/v1/admin/monitoring-config`.
- **DB:** `them.config` key `monitoring` (JSON GET/PUT).
- **Redis:** none.
- **Test gate:** Go admin tests + Python `32` (monitoring config CRUD).

### 2.6 runs writes — extend `go/internal/admin/runs.go`
- `POST /api/v1/runs/bulk-delete` (admin, ≤500), `DELETE /api/v1/runs/{id}`
  (admin, cascade), `PATCH /api/v1/runs/{id}/cancel` (own/admin). No Redis.
  Cancel just UPDATEs status (Temporal cancel is Wave 4). **Test gate:** Python
  `12`.

### 2.7 agents/orchestrators/applications writes — extend existing Go routers
- Add PATCH aliases, id-keyed routes, hard/cascade delete, graph-compile parity
  for `POST/PATCH applications` (Python compiles a graph via
  `app/services/app_compiler.py`; Go creates plain rows). **Graph compile is the
  biggest single item** — port `compile_graph`/`export_graph` to
  `go/internal/appcompiler/`. Also add `export`/`import`/`restore`/`bulk-delete`
  and `PUT /{id}/runtime` (feeds Wave 3 gate).
- **Redis (dual-publish during transition):** on any agent write →
  `DEL them:agents:registry` + `PUBLISH them:agents:changed`; on orchestrator
  write → `DEL them:orch:tmpl:{name}` + `them:orch:loc:{name}` + `PUBLISH
  them:orchestrators:changed`; on app/EP write → `_flush_orch_caches` DELs +
  `PUBLISH them:ep:config:changed {app_id}`.

### 2.8 Dual-pub/sub mechanism during the transition window
While admin writes are split across runtimes (some GETs on Go, writes on
Python, or mid-cutover), **every write handler in both runtimes publishes the
canonical channel**:
- Agents → `them:agents:changed`
- Orchestrators → `them:orchestrators:changed`
- Apps/EPs → `them:ep:config:changed`
- Tokens → `them:token:revoked`
- Middleware → SCAN+DEL `them:mw:chain:*` (no pub/sub; direct DEL is shared
  since both hit the same Redis)

Both runtimes **subscribe** to all of the above. Cutover order: land the Go
publishers + subscribers **and** the Python `them:token:revoked` /
`them:ep:config:changed` publishers (§P3) **before** flipping any write route to
Go. Then flip writes entity-by-entity via the `Method` Traefik guard (drop the
`&& Method(GET)` restriction per entity).

**Wave 2 test gate:** full Python suite (`python3.12 scripts/tests/run_tests.py`)
0 new failures + Go `go test ./... && go test -race ./...`. Contract-diff each
migrated write against Python (create→get→list→patch→delete round-trip).

---

## Wave 3 — Runtime governance wiring — ~16h

All packages exist; they are constructed-then-discarded in `main.go`. Wire them.
**Must land before Go serves any `/apps/*` or `/ws/orchestrate/*` traffic
(Wave 4)** or caps/blocks/rate-limits silently stop enforcing.

### 3.1 Exact `go/cmd/them/main.go` changes

- **Rate limiter (line 116):** replace `_ = limiter` with wiring into the gate.
  The gate needs the limiter; build the gate here:
  ```go
  gateRedis := cache.NewGateRedisClient(redisCache.Client()) // add if not present
  admissionGate := gate.New(gateRedis, limiter, log)
  ```
  (Confirm `gate.New` signature in `go/internal/gate/gate.go`; adapt.)

- **Agent registry (line 111):** the orchestrator is built with `nil` agents.
  Construct the registry and pass it:
  ```go
  regRedis := cache.NewAgentRegistryRedisClient(redisCache.Client())
  agentReg := agentregistry.New(adminDB /* or dedicated querier */, regRedis, log)
  go agentReg.Subscribe(ctx) // listens on them:agents:changed (per §P3)
  orch := orchestrator.New(orchCfg, llmProvider, agentReg, recorder, bus, log)
  ```
  (`orchestrator.New`'s 3rd arg is the agents source; replace `nil`.)

- **Gate into WS + SSE (lines 188-200):** add `.WithGate(admissionGate)` to both
  builder chains:
  ```go
  wsHandler := ws.NewHandler(...).WithGate(admissionGate).WithEPConfig(epLoader).
      WithTemporal(...).WithRunEvents(...)
  sseHandler := sse.NewHandler(...).WithGate(admissionGate).WithEPConfig(epLoader).
      WithTemporal(...).WithRunEvents(...)
  ```
  (`WithGate` already exists — `ws/handler.go:154`; the WS handler already calls
  Check→Register→Confirm→Rollback/Release when `gateStore != nil`,
  `handler.go:307-385`.)

- **Pod heartbeat:** after the session store is built (line 86), start a
  heartbeat goroutine calling `session.WriteHeartbeat`:
  ```go
  go func() {
      t := time.NewTicker(15 * time.Second); defer t.Stop()
      for { sessionStore.WriteHeartbeat(ctx); select { case <-ctx.Done(): return; case <-t.C: } }
  }()
  ```
  (Confirm `WriteHeartbeat` exists on the store; if it lives on a sub-type, wire
  accordingly.)

- **SubscribeControl (admin disconnect):** start the control listener so admin
  "terminate session" (`them:sess:control:{session_id}`) closes the WS:
  ```go
  go sessionStore.SubscribeControl(ctx) // fans out disconnect signals
  ```
  and in `ws/handler.go` add a `_control_listener` equivalent per session that
  subscribes to `them:sess:control:{sessionID}` and cancels the run context on a
  `"disconnect"` message (mirror Python `ws_orchestrator.py:267`).

### 3.2 Key-format alignment with Python (critical)
- Python session manager uses **`them:pod:{instance_id}` (Hash, 30s TTL) +
  `them:pods` (Set)** (`session_manager.py:264-270`). CLAUDE.md/REDIS.md's
  `them:bridge:{instance_id}:heartbeat` is stale — **code wins**. Go's
  `WriteHeartbeat` MUST write `them:pod:{instance_id}` + `SADD them:pods`, TTL
  30s, refreshed every 15s. Fix the Go key constant if it differs.
- Session keys Go must match exactly: `them:sess:{id}` (Hash, 90s),
  `them:ep:{slug}:sessions` (Set), `them:app:{app_id}:sessions` (Set),
  `them:sess:{id}:active` (Set, 90s). The gate's atomic Lua on
  `them:ep:{slug}:sessions` must reproduce Python's prune+cap+SADD
  (`runtime_manager.py` gate step 6).

### 3.3 Test gate
- **Go:** `go test ./internal/gate/... ./internal/ratelimit/...
  ./internal/session/... ./internal/agentregistry/...` + `go test -race ./...`.
  New wiring test in `cmd/them` (or an integration test) proving: 3rd concurrent
  session on a cap-2 EP gets `503 session cap exceeded`; blocked token → 403;
  admin disconnect closes the WS with 4000.
- **Python:** `31 33 34 35` (session/control/runtime/queue) — run against a Go
  instance holding sessions concurrently to prove key-format compat (Python's
  admin sessions list reads the same `them:ep:*`/`them:app:*` sets).
- **Do not proceed to Wave 4 until Go enforces caps correctly** (verified by the
  concurrent-session integration test above).

---

## Wave 4 — Live orchestration (WS + SSE) — ~48h (hardest)

### 4a — Resolve the workflow-ID + event-transport seam (do first in Wave 4)

Per **P1 (Option b)** and **P2 (Option A / Pub/Sub)**:

**Python side (already specified in P1):** `OrchestrationInput.run_id` added;
`workflows.py:134` uses `inp.run_id or workflow.uuid4()`; `start_run` honors the
pre-set id. Restart `them-worker`.

**Go side — WS `go/internal/ws/handler.go`:**
- Keep `runID := newID()` (line 356) and `RunID: runID` in the input (line 451)
  — Python now honors it, so `them:dash:run:{runID}:tokens` lines up. **No change
  needed to the run_id passing.**
- **Change the Temporal workflow ID (line 457)** from `ID: runID` to
  `ID: "ctx-" + contextID` so Go and Python agree on the workflow-ID namespace
  and Go can attach/dedupe the same conversation Python would:
  ```go
  wfOpts := temporalclient.StartWorkflowOptions{
      ID:        "ctx-" + contextID,
      TaskQueue: temporal.TaskQueue,
      WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY, // match Python resume semantics
  }
  ```
  and ensure the input carries `ContextID: contextID` (already at line 458 SSE /
  the WS `input` — verify `PythonOrchestrationInput.ContextID` is set in WS too;
  add it if missing).
- Set Go `RUN_EVENTS_MODE=pubsub` (env) so the dispatcher uses Pub/Sub
  (`runEvents` → `dispatcher.Stream` → pubsub). Event key derivation already
  matches (`runstream/stream.go:124`).
- **SSE `go/internal/sse/handler.go`:** identical change at line 465
  (`ID: runID` → `ID: "ctx-" + contextID`); `ContextID`/`RunID` already at
  lines 458-459.

**Result:** Go starts `OrchestrationWorkflow` with ID `ctx-{context_id}`, passes
`run_id`, Python honors it and publishes on the matching Pub/Sub channel Go
subscribes to. One workflow-ID namespace, one transport.

> If (and only if) Streams is later built (Phase 11c-D): add
> `app/temporal/stream_publish.lua` (Lua XADD, `MAXLEN ~ 5000`, field `data`),
> call it in `activities.py` **immediately after each `_publish_dash` call**
> (lines 327, 398, 485, 503, 512, 595, 599, 652, 667, 745, 749, 897, 916),
> stream key `them:dash:run:{run_id}:stream`. Prefer **Lua** (atomic XADD +
> MAXLEN in one round-trip, matches the documented contract Go's streamer reads).
> Then flip Go `RUN_EVENTS_MODE=dual` → validate → `streams`. Not required for
> the Wave 4 cutover.

### 4b — Python `/ws/orchestrate/{name}` → Go

Go has `/ws/orchestrate/{app_slug}/{entry_point_slug}`. Python's `{name}` is a
**bare orchestrator name** (no EP). The frontend uses this for the
`workflow_advisor` (`applications/page.tsx:2954`).

**Decision: add a new Go route `/ws/orchestrate/{name}`** that resolves the
orchestrator by name directly (no EP lookup), rather than a shim. Reasons: the
frontend URL is fixed and must not change (`.../ws/orchestrate/workflow_advisor`);
a name-only path is a distinct handler shape (no `epconfig.Load`, gate uses
`ep_slug=None` / rate-limit-only like Python `ws_orchestrator.py`).

- In `go/internal/ws/handler.go` `Routes()` (line 209-213), add:
  ```go
  r.Get("/orchestrate/{name}", h.ServeOrchestratorByName)
  ```
  New `ServeOrchestratorByName` mirrors `ServeHTTP` but: loads the orchestrator
  by name (validate `edges` contains `"websocket"`, else close 4003, per
  `ws_orchestrator.py:120`), skips EP access checks, runs the gate with
  EP-less config (rate-limit only), then the same Temporal/stream path as 4a.
- **Chi routing caveat:** `/orchestrate/{name}` and
  `/orchestrate/{app_slug}/{entry_point_slug}` do not conflict (different segment
  counts). Register both.

**Traefik:** `/ws` is already routed to Python (priority 100). Add a higher-prio
Go router:
```yaml
- "traefik.http.routers.them-go-ws.rule=PathPrefix(`/ws/orchestrate`)"
- "traefik.http.routers.them-go-ws.priority=110"
- "traefik.http.routers.them-go-ws.entrypoints=web"
- "traefik.http.routers.them-go-ws.service=them-go-svc"
```
This intercepts `/ws/orchestrate/*` (incl. `workflow_advisor`) to Go while
leaving `/ws/dashboard` on Python (priority-100 `them-ws` still matches it).
**Rollback:** delete the `them-go-ws` router block → `/ws/*` falls back to
Python (`them-ws`, prio 100) in seconds.

### 4c — `/apps/{slug}/ws` → Go

Go's `/ws/orchestrate/{app_slug}/{entry_point_slug}` is the EP-based analog.
**Decision: add a Go route `/apps/{slug}/ws` that maps to the EP-based handler**,
rather than changing the frontend WS URL (frontend hardcodes
`ws://<host>:8088/apps/${slug}/ws`, `applications/page.tsx:1229`,`4917`).

- In Go, mount an `/apps` router (shared with the Wave-1 catalogue) with
  `r.Get("/{slug}/ws", wsHandler.ServeAppWS)`. `ServeAppWS` resolves the EP by
  the app slug's **default/only WS entry point** (Python `apps.py:502` binds the
  slug to its orchestrator via `entry_points⋈applications`), then delegates to
  the existing EP-based logic. Public-EP → synthetic `user_id=0`; reject `a2a`
  EP type — mirror `apps.py:_resolve_bearer_ws:94`.
- Reuse the full gate path (Wave 3) — this is the endpoint that most needs it.

**Traefik:** add a Go router for the app WS path, higher prio than `them-apps`:
```yaml
- "traefik.http.routers.them-go-apps-ws.rule=PathRegexp(`^/apps/[^/]+/ws$`)"
- "traefik.http.routers.them-go-apps-ws.priority=115"
- "traefik.http.routers.them-go-apps-ws.entrypoints=web"
- "traefik.http.routers.them-go-apps-ws.service=them-go-svc"
```
**Rollback:** delete `them-go-apps-ws` → falls back to Python `them-apps`
(prio 100).

### 4d — SSE `/apps/{slug}/sse` → Go

**Path compatibility:** same as 4c — add a Go route `/apps/{slug}/sse` mapping
to the EP-based SSE handler (do not change frontend; the frontend has no
`EventSource` today but the URL is advertised in the UI and used by external
clients).

**SSE frame format:** Python `SSEEdge` emits `token` events as **bare `data:`
frames** and non-token events (`ready`/`tool_start`/`tool_done`/`done`/`error`)
as **`event: <type>\ndata: <json>`**, with a terminal `event: done\ndata: {}`
sentinel (`apps.py:408`, `sse_edge.py`). Go SSE emits
`token`/`tool_call`/`done`/`error`/`replay_unavailable`.
**Adjustments required in `go/internal/sse/handler.go`:**
1. Emit `token` as a **bare `data:` frame** (no `event:` line) to match Python.
2. Rename Go's `tool_call` → emit `event: tool_start` / `event: tool_done`
   (Python uses those two names, not `tool_call`). Map orchestrator tool events
   accordingly.
3. Emit the terminal `event: done\ndata: {}` sentinel after the run's `done`.
4. Keep `error` as `event: error\ndata: <json>`.

**Traefik:**
```yaml
- "traefik.http.routers.them-go-apps-sse.rule=PathRegexp(`^/apps/[^/]+/sse$`)"
- "traefik.http.routers.them-go-apps-sse.priority=115"
- "traefik.http.routers.them-go-apps-sse.entrypoints=web"
- "traefik.http.routers.them-go-apps-sse.service=them-go-svc"
```
Note: the current gateway also overlays `/sse` → Python; that router is
untouched. **Rollback:** delete `them-go-apps-sse`.

### 4e — HITL signal reconciliation

Broken today: Python worker's `@workflow.signal def submit_human_response`
(`workflows.py:67-68`) listens for **`submit_human_response`**; Go's signaler
sends **`human_input`** (`go/internal/temporal/workflow.go:25`,
`signaler.go:33`). Go signals the **run UUID**; Python signals
**`ctx-{context_id}`** via a direct client (`runs.py:413`).

**Decision: change Go to match Python (signal name + target).** Python is the
worker that actually receives the signal, so Go must speak its protocol.
- `go/internal/temporal/workflow.go:25` — change
  `SignalHumanInput = "human_input"` → `SignalHumanInput = "submit_human_response"`.
- `go/internal/temporal/signaler.go:33` — target the workflow by
  **`"ctx-" + contextID`**, not `runID`. The admin `/signal` route provides a
  run id; resolve its `context_id` from `them.runs` (or accept context_id).
  Python's own signal path finds the root context via `tasks`
  (`runs.py:406-416`); replicate: `SELECT context_id FROM them.runs WHERE
  id=$1`, then `SignalWorkflow(ctx, "ctx-"+contextID, "", SignalHumanInput, payload)`.
- Payload shape: Python's signal takes a `dict` (`submit_human_response(payload:
  dict)`). Go currently sends a `domain.Message`; send a JSON object matching
  Python's expected `payload` keys (inspect `workflows.py:342` input-required
  handling for the exact field, typically `{"response": "<text>"}`).
- Update Go `TEST_INDEX.md` signaler tests.

### 4f — Dashboard WS

No Go equivalent exists; `/ws/dashboard` is the fan-in consumer of every
`them:dash:*` channel (`ws_dashboard.py:159`).

**Decision: (b) leave dashboard WS in Python permanently for now.** Traefik
routes `/ws/orchestrate/*` and `/apps/{slug}/ws` to Go (4b/4c) while
`/ws/dashboard` stays on Python via the existing prio-100 `them-ws` router
(`PathPrefix(/ws)` still matches `/ws/dashboard`). Reasons: it consumes 9
`them:dash:*` channels plus HGETALL snapshots; porting it adds large surface for
zero user-facing latency benefit; Python publishers (`them:dash:runs`,
`them:dash:sessions:{app_id}`, security scan, app liveness) still run. Revisit
in a later wave once all publishers are Go.

> This means `them-bridge` **stays alive after Wave 4** specifically for
> `/ws/dashboard` + media + webrtc + A2A `GetTask/CancelTask` (see Wave 7).

### Wave 4 test gate
- **Python:** `10 11` (run recorder + WS orchestrate live), `19` (edges), `22`
  (EP WS/SSE), `31 33 34 35` (session/runtime), `13` (dashboard WS still works),
  `test_multiturn.py`, `scripts/test_temporal_workflow.py` (inside `them-worker`),
  `scripts/test_temporal_phase5.py` (signal). **Restart `them-worker`** after the
  P1 activity/workflow edits.
- **Go:** `go test ./internal/ws/... ./internal/sse/...
  ./internal/temporal/... ./internal/runstream/...` + `-tags=integration` hybrid
  tests (T1–T4) + `-race`.
- **End-to-end manual:** open a WS to `/apps/{slug}/ws` through Traefik → tokens
  stream; trigger HITL → signal resumes; admin terminate closes 4000; dashboard
  still receives run/session events.

---

## Wave 5 — A2A — ~16h

Go supports only `message/send` at `POST /a2a/{app_slug}` with no auth; Python
supports `SendMessage`/`GetTask`/`CancelTask` at `POST /a2a` with bearer + 10rpm
(`a2a_server.py:612-626`), plus `POST /a2a/push/{task_id}` and agent card at
`/.well-known/agent-card.json`.

**Changes to reach parity (in `go/internal/a2a/server.go`):**
1. **Method names:** accept Python's CamelCase JSON-RPC methods
   `SendMessage`/`GetTask`/`CancelTask` (dispatch table). Keep `message/send` as
   an alias for existing Go clients.
2. **Path:** add `POST /a2a` (no app_slug — resolve the exposed orchestrator
   from body/context) alongside `POST /a2a/{app_slug}`.
3. **Auth + rate limit:** add `_resolve_bearer` equivalent (opaque or admin-JWT)
   + 10rpm limit (`rl:them:a2a:*`) + body ≤512KB + batch ≤10.
4. **Agent card filename:** serve `/.well-known/agent-card.json` (alias of Go's
   `/.well-known/agent.json`) — already added in Wave 1.
5. **GetTask/CancelTask:** implement against `them.tasks`/`them.artifacts`
   (inline, no Temporal — matches Python).

**Remain in Python (explicitly):** `POST /a2a/push/{task_id}` (push
notifications) unless a Go client needs it — mark deferred. Move the `/a2a`
Traefik router to Go only after 1–5 land:
```yaml
- "traefik.http.routers.them-go-a2a.rule=PathPrefix(`/a2a`)"
- "traefik.http.routers.them-go-a2a.priority=110"
```
Keep `/a2a/push` on Python by a more specific higher-prio Python router if push
stays Python.

**Test gate:** Python `16 18 21 23 24 25` (A2A suite); Go `internal/a2a` tests +
a parity test per method. Invoke the `/a2a` skill before touching A2A code
(project rule).

---

## Wave 6 — Pub/Sub elimination — ~8h (only after ALL consumers moved)

Do **not** start until dashboard WS + all edges are Go (i.e. after Wave 4f is
revisited and dashboard is ported, and Streams (Option B) is live if chosen).

**Checklist:**
1. **Channels eliminable:** `them:dash:run:{run_id}:tokens` and the
   `them:dash:run:{context_id}:ctx` bootstrap channel — **only** once (a) the
   Streams `:stream` transport is the sole path (`RUN_EVENTS_MODE=streams`
   everywhere) and (b) dashboard is Go. All other `them:dash:*` fan-out channels
   **survive** (dashboard needs them) but publisher+subscriber become Go.
2. **Producers to retire:** Python worker `_publish_dash` + inline `:tokens`
   publishes in `activities.py` (lines 349, 398, 485, 503, 512, 595, 599, 652,
   667, 745, 749, 897, 916) — retire the `:ctx`/`:tokens` publishes once the Go
   worker (or Streams) owns run events.
3. **Consumers to retire:** Python `bridge_client.stream_run_events`
   (`:ctx`→`:tokens`) once no Python edge serves live runs; Go
   `runstream/stream.go` Pub/Sub path once `RUN_EVENTS_MODE=streams`.
4. **Tests that prove safety:** Go `runstream` streamer/dispatcher tests in
   `streams` mode; Python `10 11 13`; a soak run
   (`docker-compose.soak.yml`, two Go bridges) with `RUN_EVENTS_MODE=streams`
   showing zero dropped terminal events.
5. **Config change that removes the final Pub/Sub path:** set
   `RUN_EVENTS_MODE=streams` on all Go bridges (env in compose) and delete the
   Pub/Sub fallback branch in `runstream.NewDispatcher`. Remove the `:ctx`
   bootstrap by having Go subscribe directly to `:stream` keyed by the known
   pre-generated run_id (no ctx bootstrap needed once run_id is caller-supplied,
   per P1).

---

## Wave 7 — Python cleanup — ~6h

Retire Python surface incrementally. **What keeps `them-bridge` alive:**

| After wave | `them-bridge` still needed for |
|---|---|
| Wave 1 | everything except read GETs + `/health` |
| Wave 2 | all live traffic (`/ws`, `/sse`, `/a2a`), media, dashboard |
| Wave 3 | same as Wave 2 (governance is internal wiring, no route move) |
| **Wave 4** | `/ws/dashboard`, media (`/orchestrators/{name}/tts|transcribe`, `/apps/{slug}/voice/*`), webrtc token, A2A `GetTask/CancelTask/push` (until Wave 5), `/apps/{slug}` catalogue if not on Go |
| **Wave 5** | `/ws/dashboard`, media, webrtc, A2A `push` (if deferred) |
| **Wave 6** | `/ws/dashboard` (if not ported), media, webrtc |

**`them-worker` (Temporal worker) stays until Go owns a Temporal worker** — in
the recommended plan Go remains a Temporal *client* dispatching to the Python
worker (P1/4a), so **`them-worker` is required for all orchestration
indefinitely** under this plan. Retiring it is a separate future effort (build a
Go worker registering `OrchestrationWorkflow` + activities) explicitly out of
scope here.

**Safe removals per step:**
- After Wave 4 verified stable (≥1 week): remove Python `/ws/orchestrate`,
  `/apps/{slug}/ws`, `/apps/{slug}/sse` route registrations from `app/main.py`
  (leave the code; just unmount) — Traefik already bypasses them.
- After Wave 5: unmount Python `/a2a` (except `/a2a/push` if deferred).
- **Media/webrtc/dashboard remain Python** until explicitly migrated (out of the
  7-wave scope). `them-bridge` cannot be fully decommissioned until then.

---

## Rollback design (per wave)

Every cutover is a Traefik router priority flip. Reverting = deleting the Go
router label block (compose) / stanza (`dynamic.yml`) and reloading Traefik.
Traefik file+label providers hot-reload — **time to effect ≈ 5–10s**, no restart.

| Wave | Router to delete to revert | Time to effect | Data-loss risk |
|---|---|---|---|
| 1 | `them-go-health-bare`, `them-go-reads`, `them-go-apps-read` | ~5–10s | none (reads) |
| 2 | per-entity Go write router (re-add `&& Method(GET)` guard) | ~5–10s | none (writes idempotent; both runtimes dual-publish invalidation, so caches stay coherent through the flip) |
| 3 | n/a — no route moves; revert by removing `.WithGate()` and redeploying Go | ~30s (Go restart) | none |
| 4b | `them-go-ws` | ~5–10s | in-flight WS runs on Go drop; workflow continues in Temporal, client reconnects to Python; **no persisted data loss** (run state in `them.runs`) |
| 4c | `them-go-apps-ws` | ~5–10s | same as 4b |
| 4d | `them-go-apps-sse` | ~5–10s | in-flight SSE drops; re-request replays from DB |
| 4e | revert `signaler.go` + `workflow.go` signal name; redeploy Go | ~30s | a signal sent mid-flip may be lost — retry HITL |
| 5 | `them-go-a2a` | ~5–10s | in-flight A2A request fails; client retries (idempotent-ish) |
| 6 | set `RUN_EVENTS_MODE=dual` (re-enable Pub/Sub); redeploy Go | ~30s | events during the flip may gap; terminal event guaranteed from `workflow.result()` |

**Golden rule:** never delete Python route code until the Go router has been
stable in production for one full validation cycle; keep both mounted so a label
flip is always available.

---

## Test gates (overall) — what must be green before each Traefik cutover

| Cutover | Python tests (`python3.12 scripts/tests/run_tests.py`) | Go tests |
|---|---|---|
| **Prereq P1** | full suite + `test_temporal_workflow.py` (restart worker) | `-tags=integration ./internal/temporal/...` (hybrid T1–T4) |
| **P3** | `05 06 08 09` | `./internal/agentregistry/... ./internal/auth/...` |
| **Wave 1** | `01 02 03 04 15 05 06 12 20 22` | `./internal/health/... ./internal/admin/... ./internal/apps/...` + `-tags=integration` |
| **Wave 2** | full suite + `14` (needs `ADMIN_JWT`) + `test_multi_ep.py` | `./... && -race ./...` |
| **Wave 3** | `31 33 34 35` (against a live Go holding sessions) | `./internal/gate/... ./internal/ratelimit/... ./internal/session/... -race` |
| **Wave 4** | `10 11 19 22 31 33 34 35 13` + `test_multiturn.py` + `test_temporal_workflow.py` + `test_temporal_phase5.py` (restart worker) | `./internal/ws/... ./internal/sse/... ./internal/temporal/... ./internal/runstream/...` + `-tags=integration` + `-race` |
| **Wave 5** | `16 18 21 23 24 25` | `./internal/a2a/...` |
| **Wave 6** | `10 11 13` + soak (`docker-compose.soak.yml`) | `./internal/runstream/...` in `streams` mode + soak |
| **Every cutover** | `20` (Traefik routing + multi-replica) | — |

Project rule (both `CLAUDE.md`s): **no code change without a test + INDEX/
TEST_INDEX update**; `go test ./...` and the Python full suite must be green
with zero new failures before any commit or cutover.

---

## Effort summary

| Phase | Hours | Gate to advance |
|---|---|---|
| Prereqs (P0–P3) | ~12 | hybrid integration T1–T4 green; agent invalidation cross-runtime verified |
| Wave 1 | ~10 | read contract-diff clean; `/health` shape matches |
| Wave 2 | ~40 | full Python suite + Go `-race` green; write round-trips match |
| Wave 3 | ~16 | Go enforces caps/blocks/disconnect (integration test) |
| Wave 4 | ~48 | live WS/SSE + HITL + dashboard-on-Python all green |
| Wave 5 | ~16 | A2A method parity tests green |
| Wave 6 | ~8 | Streams soak zero terminal-event loss |
| Wave 7 | ~6 | Python routes unmounted, stack stable |
| **Total** | **~156h** | single engineer, several sessions |

**Single most important thing to do first:** Prerequisite **P1** — make the
Python worker honor the caller-supplied `run_id` (`shared.py` + `workflows.py:134`
+ `start_run`). Until that lands, *every* Go orchestration run subscribes to a
Redis channel Python never publishes to, so Go's entire WS/SSE path silently
receives no events. It is a ~1-hour change that unblocks all of Wave 4, and its
green light is the existing `hybrid_integration_test.go` T1.
