# Observability Storage Design — logs, activity, chat, tool calls
# Status: PROPOSAL (not started) — 2026-10-18
# Related: LLM_GATEWAY_DESIGN.md (§12 bodies), adr-003-redis-streams-event-delivery.md, SCHEMA.md

---

## 1. Goal

the-M is meant to be the AI control tower of an organization: every LLM call, tool call,
chat, and user action must be recorded, searchable, and retained per policy — from a small
team to a large enterprise. At large scale this data is high-volume and append-only, which
is a poor fit for the operational Postgres.

## 2. Today

Verified against the code on 2026-10-18 (not just docs). Everything below is Postgres; there is
**no** ClickHouse, object store, Loki, or OTel in the stack. Operational logs are `slog` JSON
(`internal/telemetry`) plus Prometheus metrics (`internal/metrics`).

| Data | Table | Written by (sync, in the request path) | Tenant isolation | Gaps |
|---|---|---|---|---|
| Gateway LLM calls | `gateway_requests` (`db/100`) | `llmgateway.DAL.WriteRequest` (`llmgateway/dal.go:79`), called from a `defer` at 3 sites in `llmgateway/handler.go` (appflow sync ~193, appflow stream ~264, plain ~446) via the `DALWriter` interface | `tenant_id` + FORCE RLS (`app.tenant_id`) | metadata only; no bodies; no retention; write uses `r.Context()` (verify a client disconnect can't cancel the metering insert) |
| Gateway bodies | `gateway_request_bodies` (`db/100`) | **nothing writes it yet** — table exists, Phase 4 of LLM_GATEWAY_DESIGN | `tenant_id` + FORCE RLS | not built |
| Per-LLM-call usage (runs) | `run_usage` | `runrecorder.RecordUsage` (`runrecorder/recorder.go:170`) | RLS via `EXISTS` on `runs` (no own `tenant_id`) | no retention; `user_id` is an int, no app id |
| Agent steps / tool calls (runs) | `run_steps` | `runrecorder.RecordAgentStep` (`:191`), `appflow/activities.go:1195` | RLS via `runs` | holds `tool_call_id`, `input`, `output`; **no dedicated MCP/tool-call log** found |
| Conversation | `task_messages` (JSONB `parts`) | `history/pgx.go:160,229` | RLS via parent task | unbounded growth, no retention |
| Middleware / guard decisions | `middleware_audit` | `middleware/job.go:356` | has `application_id`, **no `tenant_id`**; RLS via parent | cannot be filtered by tenant without a join |
| Admin actions | `audit_logs` | `admin/dal/audit_logs.go:67` — admin CRUD only | `tenant_id` (added in `db/026`), `them_app` is INSERT-only | not tamper-evident; not transactional with the action |
| User activity (login, logout, SSO, access) | **none** | `authserver` only emits `slog` lines; `user_sessions`; last SSO outcome in Redis (24 h TTL) | — | **no durable user-activity record at all** |
| Run events | Redis Streams (ADR-003) | `runstream` | per-run key | delivery transport, not storage |

Retention: no retention/cleanup job exists for any of these (only tenant-delete cascades in
`admin/dal/tenants.go`). Every table above grows forever today.

Existing seams that make this cheap to add:
- `llmgateway.DALWriter` is already an interface, injected in tests — the first producer to
  switch to an `EventSink`.
- `runrecorder.Recorder` already wraps a `DBQuerier` and is the single choke point for
  usage / steps / tasks.
- `history/pgx.go` is the single writer of `task_messages`.
- Redis Streams are already operated (ADR-003) — the queue tech is not new to the team.

## 3. Decision (summary)

1. **Postgres stays the control plane**: config, tenants, apps, runs, budgets, current-state
   rows. Anything the product must read transactionally or join with config.
2. **ClickHouse is the event store**: gateway requests, tool calls, user activity, run
   steps — append-only, analytics and search.
3. **Payload storage is pluggable (`BodyStore` interface)**: prompt/completion text, files,
   tool results. The event row keeps only a pointer + size + hash. Start with text bodies
   in a separate compressed ClickHouse table (no extra container); add **S3 / MinIO as an
   optional backend** when files or very large payloads appear, or when a customer needs
   separate encryption/retention/deletion of content. Switching is a config change —
   callers only see the pointer.
4. **A queue sits between producers and ClickHouse**, behind a Go interface. Start with
   Redis Streams; swap to NATS JetStream or Kafka for very large deployments without
   touching producers.
5. **One architecture for all sizes, backend behind an interface.** Writes go through
   `EventSink`, reads through a narrow `EventStore` (§4.1). Postgres (today's queries, moved
   behind the interface) and ClickHouse are the two implementations. The Postgres one is
   not a second product: it is the existing code, kept as the migration/rollback path and
   not extended with new features. Small installs run the same containers as large ones.

## 4. Architecture

```
gateway / agent-runtime / admin API / dag-worker
        │  Emit(event)                      (non-blocking, in-memory buffer)
        ▼
   EventSink interface ──► Redis Streams (separate instance, AOF on)
                                  │
                       event-writer (consumer group, N replicas)
                          │ batch 1–5 s / 10k rows
              ┌───────────┴────────────┐
              ▼                        ▼
         ClickHouse            BodyStore (pluggable)
   (events, aggregates)     default: ClickHouse bodies table
                            optional: S3 / MinIO (files, large payloads)
```

- Producers never wait on ClickHouse. `Emit` appends to a bounded in-memory buffer that a
  goroutine flushes to the queue.
- The writer batches into ClickHouse (large inserts, which is what it is built for).
  `async_insert=1, wait_for_async_insert=1` is the fallback if batching client-side is
  impractical.
- Redis for the queue is a **separate instance** from sessions / rate limits
  (`them:session:*`, `rl:them:*`) so log load and maintenance cannot affect auth.

### 4.1 Storage abstraction (switch backends without touching callers)

No handler, activity, or screen writes or reads event tables directly. Two interfaces in
`go/internal/events/`:

```go
// Write side — fire and forget, tenant required.
type EventSink interface {
    Emit(ctx context.Context, e Event) error
}

// Read side — one method per product use case, tenant always an explicit argument.
type EventStore interface {
    ListGatewayRequests(ctx context.Context, tenant TenantID, f RequestFilter) ([]GatewayRequest, error)
    UsageByDay(ctx context.Context, tenant TenantID, r TimeRange, by GroupBy) ([]UsagePoint, error)
    ToolCallsForRun(ctx context.Context, tenant TenantID, runID string) ([]ToolCall, error)
    UserActivity(ctx context.Context, tenant TenantID, f ActivityFilter) ([]Activity, error)
}
```

Implementations, chosen by config (`THE_M_EVENTS_BACKEND=postgres|clickhouse|dual`):

| Backend | What it is | Role |
|---|---|---|
| `postgres` | Today's `INSERT`/`SELECT` code moved behind the interfaces (tenant GUC via `BeginTenantTx` unchanged) | Default until Phase 2; rollback path afterwards. Frozen — no new features |
| `clickhouse` | Queue → writer → ClickHouse; reads via the tenant-injecting query package (§7.1) | Target backend |
| `dual` | Writes to both, reads from a configured primary; a comparison job counts mismatches | Used during migration only |

Rules that keep this cheap:
- **Shape the interface by product use cases, not by SQL.** If a screen needs a new query, add
  a method; never expose a generic `Query(sql)`.
- **Narrow on purpose.** Only what the product uses today. Free-text search or ad-hoc
  analytics over content is *not* promised on the Postgres backend — those methods return
  `ErrNotSupported` there, and the UI hides the feature.
- **One contract test suite** runs against every backend (same fixtures, same expected
  results, plus tenant-isolation cases), so backends cannot drift.
- **Metering/budget stays out of this interface.** Enforcement counters stay in
  Postgres/Redis as today (§5).
- Event types are plain Go structs with stable field names; the ClickHouse schema and the
  Postgres tables are both mappings of those structs.

## 5. Event classes — durability differs

| Class | Examples | If the pipeline is down |
|---|---|---|
| **Audit** (must not lose) | admin actions, policy changes, access to bodies, auth events | Fail closed for the action, or write to the Postgres `audit_logs` outbox in the same transaction, then relay |
| **Operational** (best effort, bounded loss) | gateway requests, tool calls, run steps, user activity | Buffer in memory + queue; on overflow drop oldest and increment a `dropped_events` metric |
| **Content** (opt-in, sensitive) | prompts, completions, tool results, files | Same as operational, plus encryption and separate read permission |

Billing/budget totals must **not** depend on this pipeline. Gateway budget enforcement keeps
using its existing Postgres/Redis counters (see LLM_GATEWAY_DESIGN §9, §16); ClickHouse is
for reporting, not enforcement.

## 6. What happens when a component is down

| Down | Effect | Recovery |
|---|---|---|
| Redis (log instance) | Producers keep buffering in memory (bounded); operational events beyond the buffer are dropped and counted; audit events use the Postgres outbox | Buffer flushes on recovery; outbox relay drains |
| ClickHouse | Queue grows; writer retries with backoff; no user impact | Writer drains the backlog — nothing lost while the queue holds |
| Writer | Queue grows; consumer group resumes from last ack | Restart; at-least-once delivery |
| S3 / MinIO | Content events retry; metadata events still land with `body_status=pending` | Retry job back-fills |

Delivery is **at-least-once**, so every event carries a unique `event_id` and ClickHouse
dedupes (ReplacingMergeTree on `event_id`) or queries tolerate duplicates.

Queue length and consumer lag are alerting signals: alarm on lag > N minutes and on any
`dropped_events` > 0.

### 6.1 Event-writer failure cases

| Case | What goes wrong | Design answer |
|---|---|---|
| **Writer down for a long time** | Queue grows until `them-redis-events` hits `maxmemory` | Per-stream `MAXLEN ~` cap and `maxmemory` with `noeviction` (Redis rejects writes rather than silently evicting). Producers see the rejection and apply the §5 rules: operational events are dropped and counted; audit events go to the Postgres outbox and are relayed later. Alert at 70 % of the cap, well before it fills. Audit events live in their own stream so operational traffic cannot push them out |
| **Poison event** | One malformed or oversized event fails every retry and blocks its batch | On batch failure, retry with bisection to isolate the bad row; after N attempts move it to a dead-letter stream (`them:events:dlq`) with the error, ack it, and continue. DLQ count > 0 alerts; a small admin tool can inspect and replay |
| **Crash mid-batch** | Rows may be inserted but not yet acked, so the batch is delivered again | Ack only after a successful ClickHouse insert. Duplicates are expected and harmless: `event_id` dedupe (ReplacingMergeTree) plus idempotent insert tokens where ClickHouse supports them |
| **Catching up after an outage** | Backlog is large; naive replay overloads ClickHouse or never catches up | Writer drains in large batches with bounded concurrency and a rate limit; drain rate must exceed peak ingest rate (checked in the load test); metric `backlog_eta_seconds` |
| **Several writers** | Double processing, or events stuck with a dead consumer | One consumer group, N replicas; each event goes to one consumer. A reaper uses `XAUTOCLAIM` to take over events idle longer than a timeout |
| **Writer up but ClickHouse rejecting** (schema drift, disk full, auth) | Looks healthy but nothing lands | Separate "last successful insert" gauge; alert when it is older than N minutes even if the writer process is up |
| **Silent failure** | Nobody notices any of the above | Required alerts: queue length, consumer lag, DLQ count, `dropped_events`, last successful insert age, writer restarts. Wire into the existing Prometheus metrics (`internal/metrics`) |
| **Version skew** | Producer emits a new event field the writer doesn't know (rolling deploys) | Events carry `schema_version`; the writer ignores unknown fields and sends events with an unknown version to the DLQ rather than guessing |

Stage A's kill tests (§11) must cover each row above, not only "ClickHouse down".

## 7. Tenancy (hard rule)

- Every event carries `tenant_id`, `application_id` (and `entry_point_id` when applicable),
  `user_id`, `trace_id` / `request_id` (matches GATEWAY_REQUEST_TRACING).
- ClickHouse has no Postgres RLS. Isolation is enforced in the **query layer**: the Go
  read API injects `tenant_id` into every query — there is no direct ClickHouse access from
  the UI or tenants. Use ClickHouse row policies as a second layer for any user that is
  ever given direct SQL access.
- Table `ORDER BY (tenant_id, application_id, toDate(ts), ...)` so per-tenant queries and
  per-tenant deletes are cheap.
- Redis keys for the queue are prefixed `them:events:` and carry tenant in the payload
  (single stream per event class; per-tenant streams only if a noisy tenant needs
  isolation). Document in `REDIS.md` when built.

### 7.1 Mapping from today's Postgres tenancy

How tenant separation works now, and what it means for the event store:

- **Today:** roles `them_app` (RLS enforced), `them_admin` (BYPASSRLS, cross-tenant reads
  such as slug resolution), `them_owner`. Tenant-scoped tables are `FORCE ROW LEVEL SECURITY`
  keyed on `app.tenant_id`, which Go sets per transaction via `db.Pools.BeginTenantTx`
  (`internal/db/db.go:137`). A plain pool query without the GUC fails silently (returns no
  rows). Tenant comes from the verified token / `epconfig`, never from request input.
- **Event-store rule:** the tenant id on an event must come from the same trusted source
  (token → `tenantctx` / `epconfig`), set by the **producer's Go code**, never from payload
  or headers. `EventSink.Emit` takes a typed `TenantID` that is required — an empty value is
  rejected (same pattern as `runrecorder.ErrMissingTenantID`).
- **Denormalize `tenant_id` onto every event.** Today `run_usage`, `run_steps`,
  `task_messages` and `middleware_audit` have no own `tenant_id` and rely on parent-table RLS
  or a join. That does not work in ClickHouse (no RLS, no cheap joins) — the writer or the
  producer must resolve and stamp `tenant_id` (and `application_id`) at emit time.
- **Read path = the only isolation boundary.** One Go package (`internal/events/query`)
  builds all ClickHouse SQL and always appends `tenant_id = ?` from the request's tenant
  context; no handler builds ClickHouse SQL itself, and ClickHouse is not reachable from the
  frontend or from tenants. Cross-tenant ("platform") reports use a separate, explicitly
  named code path and permission, mirroring `them_admin`.
- **ClickHouse credentials:** two users — `events_writer` (INSERT only) and `events_reader`
  (SELECT only, row policy `tenant_id = {tenant}` optional second layer). Neither can DDL.
- **Test (mirrors `internal/db/rls_integration_test.go`):** emit events for tenant A and B,
  assert every query function returns zero rows of the other tenant, and that an
  `Emit` with empty tenant is rejected.
- **Tenant deletion:** `admin/dal/tenants.go` already cascades deletes across tables; add the
  ClickHouse delete (`ALTER ... DELETE WHERE tenant_id`) and body-store prefix delete to that
  flow, or tenant offboarding will leave data behind.

## 8. Retention, privacy, compliance

- Retention is **per tenant, per event class**: e.g. operational 90 d, audit 1–7 y,
  content 30 d. Implemented as ClickHouse TTL plus a lifecycle rule on the bucket,
  parameterized per tenant (partition by `(tenant_id, month)` or a TTL expression reading
  a per-tenant setting).
- Content capture is opt-in per client / app (already specified in LLM_GATEWAY_DESIGN §12):
  encrypted at rest, separate read permission, reads are themselves audit events.
- PII middleware output (redacted text) is what gets stored when the guard is on — never
  store pre-redaction text from a guarded path.
- Right-to-erasure: delete by `(tenant_id, user_id)` — S3 objects keyed
  `tenant/app/date/event_id` so deletion is a prefix + index lookup.
- Audit events are tamper-evident: hash-chain per tenant (each row stores the hash of the
  previous) or periodic signed checkpoints written to object storage with Object Lock.

## 9. App export/import

Event data is **runtime data, not configuration** — it is not part of `AppDefinitionDoc`
and is not copied by Deploy to Tenant. Retention and capture **settings** are
configuration: store them in `AppDefinitionDoc` (or a registered `application_id` table
per `APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md`) so export/import stays complete.

## 10. Data model sketch (ClickHouse)

```
events_gateway_request
  event_id UUID, ts DateTime64(3), tenant_id, application_id, client_id, user_id,
  request_id, trace_id, provider, model, status, latency_ms, ttfb_ms,
  tokens_in, tokens_out, cost_usd, error_code,
  body_ref String  -- s3 key or ''
ENGINE ReplacingMergeTree  ORDER BY (tenant_id, application_id, toDate(ts), event_id)

events_tool_call     (… tool_name, mcp_server, args_ref, result_ref, duration_ms, status)
events_user_activity (… action, resource_type, resource_id, ip, user_agent)
events_audit         (… action, before_ref, after_ref, prev_hash, hash)
```

`gateway_requests` in Postgres stays during migration (see phases) — it serves the
existing Requests screen and budget totals.

## 11. Phases

### Where to start (concrete) — two stages

The work is split so that **nothing running is touched until everything new is built and
proven**. Stage A is purely additive; Stage B integrates one area at a time.

#### Stage A — build and deploy the new pieces, wired to nothing (no impact on the live app)

Rule: no existing service imports the new code, and no existing container or compose file
changes behavior. Phases 0–1 of the table below are Stage A.

1. **New containers in a separate compose file** (`docker-compose.events.yml`, profile
   `events`) so a normal `up` does not start them: `them-clickhouse`, `them-redis-events`
   (dedicated Redis, AOF on, `noeviction` — the main `them-redis` runs `allkeys-lru` with 256 MB,
   which would silently evict queued events), `them-event-writer`
   (new `go/cmd/event-writer`). `them-minio` **already exists** in the stack (FileGate quarantine + artifacts), so the optional S3 `BodyStore` backend (§3) needs no new container — only new buckets; it is not wired in Stage A.
2. **Scripts:** ClickHouse DDL under `db/clickhouse/` (tables from §10, TTL, `events_writer`
   INSERT-only and `events_reader` SELECT-only users), an init script like the existing
   `docker cp ... psql` flow, `generate-env.sh` / `.ps1` additions that derive the new
   secrets from `secrets.local` (never committed), healthchecks, and a runbook section in
   `docs/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md`.
3. **New Go code, unused by live services:** `go/internal/events/` — `Event` types,
   `EventSink` / `EventStore` (§4.1), in-memory sink, Redis Streams sink, ClickHouse backend,
   a **copy** of today's gateway-request queries as the `postgres` backend (copied, not moved —
   the live handler keeps using its own code), tenant-injecting query package, metrics.
4. **Prove it with synthetic data:** a test tool emits fake multi-tenant events; the contract
   suite runs against `postgres` and `clickhouse`; tenant-isolation tests; load test; then
   kill each component (Redis, ClickHouse, writer) and confirm the §6 behavior.
5. **Docs:** `REDIS.md` (new keys), CLAUDE.md container map, `go/TEST_INDEX.md`, INDEX.

**Stage A progress:** A1 (compose overlay, Redis conf, ClickHouse DDL + users, secrets script, runbook) written 2026-10-18, started and tested live the same day (tables, TTLs, user privileges, Redis auth/AOF/noeviction; see runbook §10). A2 (`internal/events` package), A3 (`event-writer` + its container), A4 (synthetic data, contract and failure tests) pending.

**Stage A exit gate:** stack with the `events` profile healthy; synthetic events flow end to
end; contract + isolation + failure tests green; `go test ./...` and the existing compose
health smoke show **zero change** for the live stack.

#### Stage B — integrate one area at a time

Each area gets its own flag, `THE_M_EVENTS_<AREA>=off|dual|clickhouse`, **default `off`**, so
deploying the code is a no-op until the flag is flipped, and rollback is a flag flip.
Per area: (1) swap the producer to the sink/interface behind the flag, (2) flip to `dual`,
verify counts and contents match over ~7 days, (3) flip reads to `clickhouse`, (4) freeze or
trim the Postgres table and add retention. Finish one area before starting the next.

Suggested order (lowest risk first):

| # | Area | Producer(s) to change | Why this position |
|---|---|---|---|
| 1 | Gateway requests | 3 `WriteRequest` defers in `llmgateway/handler.go` → helper that calls the sink; use `context.WithoutCancel` for the write | Highest volume, already behind `DALWriter`, tenant-keyed |
| 2 | User activity | new producers in `authserver` (login/logout/SSO) | Greenfield — nothing to migrate or compare |
| 3 | Tool calls | new event at the MCP / agent tool-execution point | Greenfield |
| 4 | Run usage + steps | `runrecorder.RecordUsage` / `RecordAgentStep`, `appflow/activities.go` | Touches the run path; dag-worker restart needed (trigger map) |
| 5 | Conversation messages | `history/pgx.go` | Large, sensitive; needs content rules (§8) |
| 6 | Middleware audit | `middleware/job.go` | Stamp `tenant_id` at emit time (§7.1) |
| 7 | Admin audit | `admin/dal/audit_logs.go` + outbox | Strictest durability — last |
| 8 | Bodies | new `BodyStore` writer | Needs retention shipped with it |

Heads-up: `go/internal/llmgateway/dal.go` and `middleware.go` currently have uncommitted
changes in the working tree — land or stash those before starting Stage B area 1.

Phases 0–1 below are **Stage A** (additive, wired to nothing); phase 2 onward is **Stage B**,
applied per area behind its flag.

| Phase | Scope | Exit criteria |
|---|---|---|
| 0 | `EventSink` + `EventStore` interfaces (§4.1), event schema, a **copy** of today's gateway-request queries as the `postgres` backend, in-memory sink, metrics (`emitted`, `dropped`, `lag`), shared contract test suite — **no live service imports it** | Contract + tenant-isolation tests green on `postgres`; live stack unchanged |
| 1 | `events` compose profile: Redis Streams sink (dedicated instance), `event-writer`, ClickHouse container, DDL + users + env scripts, `clickhouse` backend, synthetic-data load and failure tests | Stage A exit gate; contract suite green on `clickhouse` |
| 2 | Per area (gateway requests first): swap producer behind `THE_M_EVENTS_<AREA>` → `dual` (counts match ~7 days) → flip reads to `clickhouse` → trim Postgres + retention. Rollback = flip flag back | UI parity; contract suite green on `clickhouse`; tenant-isolation tests |
| 3 | Tool calls, user activity, run steps as events | Activity feed + per-user history screens |
| 4 | Content capture via `BodyStore` — default ClickHouse bodies table, **S3/MinIO optional** backend (replaces Postgres `gateway_request_bodies` plan); retention job ships in the same phase | Encryption + permission + retention tests on the default backend; S3 backend added only when files/large payloads or customer requirements need it |
| 5 | Audit outbox + hash chain; per-tenant retention settings in app JSON | Compliance export works |
| 6 | Optional: NATS/Kafka sink for large deployments | Same producers, config switch |

Each phase needs Go tests and `go/TEST_INDEX.md` updates; new Redis keys go in `REDIS.md`,
new containers in the CLAUDE.md container map and compose runbook.

## 12. Why not the alternatives

- **Everything in Postgres (partitioned):** fine to ~tens of millions of rows; beyond that,
  write amplification, vacuum, and slow aggregate queries compete with the control plane.
  Also couples log maintenance to the DB that runs the product.
- **Elasticsearch/OpenSearch:** strong full-text search, but heavier to run and costlier
  per GB than ClickHouse for analytic queries. Revisit only if free-text search over
  content becomes a core feature.
- **Write directly to ClickHouse from producers:** acceptable at moderate scale with
  async inserts, but gives no durable buffer when ClickHouse is slow or down.
- **Local disk spool instead of a queue:** simple, but harder to operate across replicas
  and not a standard pattern; rejected in favor of an interface-backed queue.
- **Kafka from day one:** the right answer at very large scale, but another system to run;
  the `EventSink` interface keeps that door open.

## 13. Open questions

1. Expected peak requests/day and chat-content retention for the first target customers
   (sizes the queue and ClickHouse hardware).
2. Is ClickHouse acceptable in the on-prem footprint of the target customers, or do some
   need a managed/alternative store?
3. Do any customers require WORM / Object Lock audit storage from day one?
4. Single stream per event class vs per-tenant streams (decide after Phase 1 load test).
