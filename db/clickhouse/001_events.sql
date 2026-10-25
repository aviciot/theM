-- the-M events store — ClickHouse schema (database: them_events)
-- Design: docs/_archive/OBSERVABILITY_STORAGE_DESIGN.md §10
--
-- Runs once, on first start of an empty data dir (docker-entrypoint-initdb.d).
-- Conventions:
--   * every table carries tenant_id (and application_id) — isolation is enforced by the
--     Go query layer (§7.1); ClickHouse has no Postgres-style RLS
--   * event_id is unique per event; delivery is at-least-once, so ReplacingMergeTree
--     dedupes on it (queries that need exact counts use FINAL or tolerate duplicates)
--   * schema_version lets the writer route unknown versions to the DLQ (§6.1)
--   * TTL here is the platform default (90 d); per-tenant retention comes later (§8)

CREATE DATABASE IF NOT EXISTS them_events;

CREATE TABLE IF NOT EXISTS them_events.events_gateway_request
(
    event_id         UUID,
    schema_version   UInt16,
    ts               DateTime64(3, 'UTC'),
    tenant_id        UUID,
    application_id   UUID,
    client_id        UUID,
    user_id          String,
    request_id       String,
    trace_id         String,
    provider         LowCardinality(String),
    model_requested  String,
    model_served     String,
    status           LowCardinality(String),
    http_status      UInt16,
    error_code       LowCardinality(String),
    tokens_in        UInt32,
    tokens_out       UInt32,
    cost_usd         Decimal(12, 8),
    latency_ms       UInt32,
    ttfb_ms          UInt32,
    streamed         Bool,
    body_ref         String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, application_id, toDate(ts), event_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS them_events.events_tool_call
(
    event_id         UUID,
    schema_version   UInt16,
    ts               DateTime64(3, 'UTC'),
    tenant_id        UUID,
    application_id   UUID,
    run_id           String,
    request_id       String,
    trace_id         String,
    user_id          String,
    tool_name        LowCardinality(String),
    mcp_server       LowCardinality(String),
    status           LowCardinality(String),
    duration_ms      UInt32,
    args_ref         String,
    result_ref       String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, application_id, toDate(ts), event_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS them_events.events_user_activity
(
    event_id         UUID,
    schema_version   UInt16,
    ts               DateTime64(3, 'UTC'),
    tenant_id        UUID,
    application_id   UUID,
    user_id          String,
    action           LowCardinality(String),
    resource_type    LowCardinality(String),
    resource_id      String,
    outcome          LowCardinality(String),
    ip               String,
    user_agent       String,
    trace_id         String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, toDate(ts), user_id, event_id)
TTL toDateTime(ts) + INTERVAL 180 DAY DELETE;

-- Audit: longer retention, hash-chained per tenant (prev_hash/hash filled by the writer).
CREATE TABLE IF NOT EXISTS them_events.events_audit
(
    event_id         UUID,
    schema_version   UInt16,
    ts               DateTime64(3, 'UTC'),
    tenant_id        UUID,
    user_id          String,
    action           LowCardinality(String),
    resource_type    LowCardinality(String),
    resource_id      String,
    before_ref       String,
    after_ref        String,
    prev_hash        FixedString(64),
    hash             FixedString(64)
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, ts, event_id)
TTL toDateTime(ts) + INTERVAL 7 YEAR DELETE;

-- Default BodyStore backend (§3): prompt/completion text, tool args/results.
-- Compressed with ZSTD; event rows reference it via body_ref / args_ref / result_ref.
-- Encryption is applied by the Go layer before insert (Fernet, per LLM_GATEWAY_DESIGN §12).
CREATE TABLE IF NOT EXISTS them_events.events_bodies
(
    body_id          UUID,
    ts               DateTime64(3, 'UTC'),
    tenant_id        UUID,
    application_id   UUID,
    kind             LowCardinality(String),
    encrypted        Bool,
    size_bytes       UInt32,
    sha256           FixedString(64),
    body             String CODEC(ZSTD(3))
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, application_id, toDate(ts), body_id)
TTL toDateTime(ts) + INTERVAL 30 DAY DELETE;
