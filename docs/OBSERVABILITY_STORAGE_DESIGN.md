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

| Data | Where | Notes |
|---|---|---|
| Gateway LLM calls | `gateway_requests` (Postgres) | metering + Requests screen + budget totals |
| Gateway bodies (opt-in) | `gateway_request_bodies` (Postgres, Fernet) | designed in LLM_GATEWAY_DESIGN §12, not built |
| Per-call usage | `run_usage` | tokens/cost per run |
| Admin actions | `audit_logs` | INSERT-only for `them_app`, RLS |
| Run events | Redis Streams (ADR-003) | delivery transport, not storage |

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
5. **One architecture for all sizes.** No "small mode in Postgres" — two backends means
   double the code and double the tests. Small installs just run the same containers.

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

| Phase | Scope | Exit criteria |
|---|---|---|
| 0 | `EventSink` Go interface + in-memory implementation + event schema + metrics (`emitted`, `dropped`, `lag`) | Producers compile against interface; no behavior change |
| 1 | Redis Streams sink (dedicated instance) + `event-writer` service + ClickHouse container + `events_gateway_request` | Dual-write: gateway writes Postgres **and** emits events; counts match over 7 days |
| 2 | Read API in Go (tenant-injected) + Requests screen reads from ClickHouse; Postgres `gateway_requests` becomes metering-only with short retention | UI parity; tenant-isolation tests |
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
