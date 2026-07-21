-- =============================================================================
-- the-M — Canonical Schema Snapshot
-- Version: 25 (matches migration 025_events_transport.sql)
-- Date: 2026-07-21
--
-- PURPOSE: Fresh Linux installations apply THIS FILE ONLY to create the full
-- current schema in a single pass. Do NOT replay individual migration files
-- (001..025) on a fresh database — this snapshot supersedes them.
--
-- WHAT THIS FILE DOES:
--   1. Creates them.schema_migrations for tracking applied migrations
--   2. Creates both schemas (them, auth_service)
--   3. Creates all tables at their current final shape (no ALTER chains)
--   4. Creates all indexes and constraints
--   5. Records migrations 001–025 as applied (no data, no user accounts)
--
-- WHAT THIS FILE DOES NOT DO:
--   - Does NOT insert demo/test agents or orchestrators (see db/seed_demo.sql)
--   - Does NOT create user accounts (see db/seed_users.sql for dev; use IAM for prod)
--   - Does NOT replay ALTER TABLE history
--
-- MIGRATION TRACKING:
--   After applying this snapshot, them.schema_migrations will contain versions
--   001 through 025 marked as applied. Future migrations (026+) will check this
--   table and apply only what is missing, under a PostgreSQL advisory lock.
--
-- USAGE (called by scripts/linux-db-init.sh on fresh install):
--   docker exec -i them-postgres psql -U them -d them < db/schema_current.sql
-- =============================================================================

BEGIN;

-- Require at least PostgreSQL 14 (gen_random_uuid() is built-in from pg14+)
DO $$
BEGIN
  IF current_setting('server_version_num')::integer < 140000 THEN
    RAISE EXCEPTION 'the-M requires PostgreSQL 14 or later (found %)', version();
  END IF;
END;
$$;

-- =============================================================================
-- 0. SCHEMA MIGRATION TRACKING
-- =============================================================================

-- Create the them schema first so schema_migrations lives there
CREATE SCHEMA IF NOT EXISTS them;
CREATE SCHEMA IF NOT EXISTS auth_service;

CREATE TABLE IF NOT EXISTS them.schema_migrations (
    version         TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    checksum        TEXT,
    CONSTRAINT pk_schema_migrations PRIMARY KEY (version),
    CONSTRAINT ck_schema_migrations_version CHECK (version ~ '^\d{3}[a-z]?(_[a-z0-9_]+)?$')
);

COMMENT ON TABLE them.schema_migrations IS
  'Tracks applied schema migrations. Fresh installs pre-populate this for all '
  'versions covered by schema_current.sql. Upgrade scripts insert one row per '
  'new migration after applying it, under pg_try_advisory_lock(987654321).';

-- =============================================================================
-- 1. AUTH SERVICE SCHEMA
-- =============================================================================

CREATE TABLE IF NOT EXISTS auth_service.roles (
    id                  SERIAL      PRIMARY KEY,
    name                VARCHAR(50) UNIQUE NOT NULL,
    description         TEXT,
    mcp_access          TEXT[]      DEFAULT '{}',
    tool_restrictions   JSONB       DEFAULT '{}',
    dashboard_access    VARCHAR(20) DEFAULT 'none',
    rate_limit          INTEGER     DEFAULT 1000,
    cost_limit_daily    DECIMAL(10,2) DEFAULT 100.00,
    token_expiry        INTEGER     DEFAULT 3600,
    created_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS auth_service.users (
    id                  SERIAL      PRIMARY KEY,
    username            VARCHAR(100) UNIQUE NOT NULL,
    name                VARCHAR(255) NOT NULL,
    email               VARCHAR(255) UNIQUE,
    role_id             INTEGER     REFERENCES auth_service.roles(id),
    password_hash       VARCHAR(255),
    api_key_hash        VARCHAR(255) UNIQUE,
    active              BOOLEAN     NOT NULL DEFAULT true,
    rate_limit_override INTEGER,
    last_login_at       TIMESTAMP,
    created_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_role_id ON auth_service.users(role_id);
CREATE INDEX IF NOT EXISTS idx_users_email   ON auth_service.users(email);

CREATE TABLE IF NOT EXISTS auth_service.teams (
    id                  SERIAL      PRIMARY KEY,
    name                VARCHAR(100) UNIQUE NOT NULL,
    description         TEXT,
    mcp_access          TEXT[]      DEFAULT '{}',
    resource_access     JSONB       DEFAULT '{}',
    team_rate_limit     INTEGER,
    team_cost_limit     DECIMAL(10,2),
    created_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS auth_service.team_members (
    team_id     INTEGER     REFERENCES auth_service.teams(id) ON DELETE CASCADE,
    user_id     INTEGER     REFERENCES auth_service.users(id) ON DELETE CASCADE,
    role        VARCHAR(50) DEFAULT 'member',
    joined_at   TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS auth_service.user_overrides (
    user_id             INTEGER     PRIMARY KEY REFERENCES auth_service.users(id) ON DELETE CASCADE,
    mcp_restrictions    TEXT[]      DEFAULT '{}',
    tool_restrictions   JSONB       DEFAULT '{}',
    custom_rate_limit   INTEGER,
    custom_cost_limit   DECIMAL(10,2),
    created_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS auth_service.auth_audit (
    id          SERIAL      PRIMARY KEY,
    user_id     INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    username    VARCHAR(255),
    action      VARCHAR(100) NOT NULL,
    status      VARCHAR(50)  NOT NULL,
    ip_address  VARCHAR(45),
    user_agent  TEXT,
    details     TEXT,
    created_at  TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_auth_audit_user_id    ON auth_service.auth_audit(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_audit_created_at ON auth_service.auth_audit(created_at);

CREATE TABLE IF NOT EXISTS auth_service.user_sessions (
    id                  SERIAL      PRIMARY KEY,
    user_id             INTEGER     REFERENCES auth_service.users(id) ON DELETE CASCADE,
    access_token_hash   VARCHAR(255) UNIQUE NOT NULL,
    refresh_token_hash  VARCHAR(255) UNIQUE NOT NULL,
    expires_at          TIMESTAMP   NOT NULL,
    created_at          TIMESTAMP   DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id    ON auth_service.user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON auth_service.user_sessions(expires_at);

CREATE TABLE IF NOT EXISTS auth_service.blacklisted_tokens (
    token_hash      VARCHAR(255) PRIMARY KEY,
    blacklisted_at  TIMESTAMP   DEFAULT CURRENT_TIMESTAMP,
    expires_at      TIMESTAMP   NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_blacklisted_tokens_expires_at ON auth_service.blacklisted_tokens(expires_at);

-- Seed mandatory roles (no user accounts — see db/seed_users.sql for dev)
INSERT INTO auth_service.roles (name, description, mcp_access, tool_restrictions, dashboard_access, rate_limit, cost_limit_daily, token_expiry)
VALUES
  ('super_admin', 'Full system access',    ARRAY['*'], '{}', 'admin', 10000, 1000.00, 7200),
  ('developer',   'Developer access',      ARRAY['database_mcp','macgyver_mcp','informatica_mcp'],
   '{"database_mcp":["analyze_full_sql_context","compare_query_plans"],"macgyver_mcp":["*"],"informatica_mcp":["*"]}',
   'view', 5000, 100.00, 7200),
  ('analyst',     'Data analyst access',   ARRAY['database_mcp'],
   '{"database_mcp":["analyze_full_sql_context","get_top_queries"]}',
   'view', 1000, 50.00, 3600),
  ('viewer',      'Read-only access',      ARRAY[]::text[], '{}', 'view', 100, 10.00, 3600)
ON CONFLICT (name) DO NOTHING;

-- =============================================================================
-- 2. THEM SCHEMA — CORE TABLES (current final shape, no ALTER chains)
-- =============================================================================

CREATE TABLE IF NOT EXISTS them.llm_providers (
    id                  SERIAL      PRIMARY KEY,
    name                TEXT        NOT NULL UNIQUE,
    display_name        TEXT        NOT NULL DEFAULT '',
    api_key_encrypted   TEXT,
    base_url            TEXT,
    default_model       TEXT,
    model_pricing       JSONB,
    enabled             BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.config (
    config_key      TEXT        PRIMARY KEY,
    config_value    JSONB       NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.agents (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                    TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_]{1,48}$'),
    display_name            TEXT        NOT NULL DEFAULT '',
    description             TEXT        NOT NULL DEFAULT '',
    transport               TEXT        NOT NULL DEFAULT 'a2a_async'
                                        CHECK (transport IN ('a2a_async')),
    endpoint_url            TEXT,
    auth_token_encrypted    TEXT,
    input_schema            JSONB       NOT NULL DEFAULT '{}',
    timeout_seconds         INTEGER     NOT NULL DEFAULT 30,
    max_concurrency         INTEGER     NOT NULL DEFAULT 5,
    max_retries             INTEGER     NOT NULL DEFAULT 2,
    enabled                 BOOLEAN     NOT NULL DEFAULT true,
    agent_card              JSONB,
    agent_card_url          TEXT,
    card_fetched_at         TIMESTAMPTZ,
    skills                  JSONB       NOT NULL DEFAULT '[]',
    supports_streaming      BOOLEAN     NOT NULL DEFAULT false,
    supports_push           BOOLEAN     NOT NULL DEFAULT false,
    tags                    TEXT[]      NOT NULL DEFAULT '{}',
    icon                    TEXT,
    category                TEXT,
    last_scan_at            TIMESTAMPTZ,
    last_scan_result        JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_slug    ON them.agents(slug);
CREATE INDEX        IF NOT EXISTS idx_agents_enabled ON them.agents(enabled);

CREATE TABLE IF NOT EXISTS them.orchestrators (
    id                              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                            TEXT        NOT NULL UNIQUE,
    display_name                    TEXT        NOT NULL DEFAULT '',
    system_prompt                   TEXT        NOT NULL DEFAULT '',
    allowed_agent_ids               UUID[]      NOT NULL DEFAULT '{}',
    llm_provider                    TEXT,
    llm_model                       TEXT,
    llm_api_key_encrypted           TEXT,
    llm_base_url                    TEXT,
    max_iterations                  INTEGER     NOT NULL DEFAULT 10,
    max_parallel_tools              INTEGER     NOT NULL DEFAULT 3,
    rate_limit_rpm                  INTEGER,
    daily_budget_usd                NUMERIC(10,4),
    voice_enabled                   BOOLEAN     NOT NULL DEFAULT false,
    transcription_provider          VARCHAR(32),
    transcription_model             VARCHAR(64),
    transcription_api_key_encrypted TEXT,
    tts_enabled                     BOOLEAN     NOT NULL DEFAULT false,
    tts_provider                    TEXT,
    tts_voice                       TEXT,
    tts_api_key_encrypted           TEXT,
    memory_enabled                  BOOLEAN     NOT NULL DEFAULT false,
    summarize_every_n_calls         INTEGER     NOT NULL DEFAULT 3,
    memory_raw_fallback_n           INTEGER     NOT NULL DEFAULT 5,
    summarizer_provider             TEXT,
    summarizer_model                TEXT,
    summarizer_api_key_encrypted    TEXT,
    edges                           TEXT[]      NOT NULL DEFAULT '{websocket}',
    history_window                  INTEGER     NOT NULL DEFAULT 20,
    budget_tokens                   INTEGER,
    delegatable                     BOOLEAN     NOT NULL DEFAULT false,
    enabled                         BOOLEAN     NOT NULL DEFAULT true,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.access_tokens (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      TEXT        NOT NULL UNIQUE,
    label           TEXT        NOT NULL DEFAULT 'default',
    user_id         INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    orchestrator_id UUID        REFERENCES them.orchestrators(id) ON DELETE CASCADE,
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_access_tokens_user ON them.access_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_access_tokens_hash ON them.access_tokens(token_hash);

CREATE TABLE IF NOT EXISTS them.applications (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT        NOT NULL,
    presentation            JSONB       NOT NULL DEFAULT '{}',
    enabled                 BOOLEAN     NOT NULL DEFAULT true,
    conversation_token_limit INTEGER,
    runtime_config          JSONB       NOT NULL DEFAULT '{}',
    canvas                  JSONB,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN them.applications.runtime_config IS
  'App-level runtime policy: {max_concurrent_sessions, rate_limit_rpm, blocked_tokens[], blocked_user_ids[], session_timeout_minutes}. {} = unlimited.';

CREATE TABLE IF NOT EXISTS them.app_orchestrators (
    id                              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id                  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    orchestrator_id                 UUID        REFERENCES them.orchestrators(id) ON DELETE SET NULL,
    name                            TEXT        NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9_-]{1,64}$'),
    node_id                         TEXT        NOT NULL,
    kind                            TEXT        NOT NULL DEFAULT 'standard'
                                                CHECK (kind IN ('standard','router','voice')),
    delegatable                     BOOLEAN     NOT NULL DEFAULT false,
    display_name                    TEXT,
    system_prompt                   TEXT,
    allowed_agent_ids               UUID[]      NOT NULL DEFAULT '{}',
    llm_provider                    TEXT,
    llm_model                       TEXT,
    llm_api_key_encrypted           TEXT,
    llm_base_url                    TEXT,
    max_iterations                  INTEGER     NOT NULL DEFAULT 10,
    max_parallel_tools              INTEGER     NOT NULL DEFAULT 3,
    rate_limit_rpm                  INTEGER,
    daily_budget_usd                NUMERIC(10,4),
    voice_enabled                   BOOLEAN     NOT NULL DEFAULT false,
    transcription_provider          VARCHAR(32),
    transcription_model             VARCHAR(64),
    transcription_api_key_encrypted TEXT,
    tts_enabled                     BOOLEAN     NOT NULL DEFAULT false,
    tts_provider                    TEXT,
    tts_voice                       TEXT,
    tts_api_key_encrypted           TEXT,
    memory_enabled                  BOOLEAN     NOT NULL DEFAULT false,
    summarize_every_n_calls         INTEGER     NOT NULL DEFAULT 3,
    memory_raw_fallback_n           INTEGER     NOT NULL DEFAULT 5,
    summarizer_provider             TEXT,
    summarizer_model                TEXT,
    summarizer_api_key_encrypted    TEXT,
    edges                           TEXT[]      NOT NULL DEFAULT '{websocket}',
    history_window                  INTEGER     NOT NULL DEFAULT 20,
    budget_tokens                   INTEGER,
    enabled                         BOOLEAN     NOT NULL DEFAULT true,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_orch_app_node UNIQUE (application_id, node_id)
);

CREATE INDEX IF NOT EXISTS idx_app_orchestrators_application_id ON them.app_orchestrators(application_id);
CREATE INDEX IF NOT EXISTS idx_app_orchestrators_name           ON them.app_orchestrators(name);

CREATE TABLE IF NOT EXISTS them.entry_points (
    id                          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id              UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    app_orchestrator_id         UUID        REFERENCES them.app_orchestrators(id) ON DELETE CASCADE,
    slug                        TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_-]{1,64}$'),
    entry_point_type            TEXT        NOT NULL
                                            CHECK (entry_point_type IN ('websocket','sse','webrtc','a2a','voice')),
    access_policy               JSONB       NOT NULL DEFAULT '{"mode":"token"}',
    conversation_token_limit    INTEGER,
    max_concurrent_sessions     INTEGER,
    queue_timeout_seconds       INTEGER,
    queue_message               TEXT,
    enabled                     BOOLEAN     NOT NULL DEFAULT true,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN them.entry_points.max_concurrent_sessions IS
  'Max simultaneous active sessions for this entry point. NULL = unlimited.';
COMMENT ON COLUMN them.entry_points.queue_timeout_seconds IS
  'Seconds to wait for a slot before rejecting. NULL = immediate reject (no queue).';
COMMENT ON COLUMN them.entry_points.queue_message IS
  'Message sent to client while waiting for a slot. NULL = default.';

CREATE INDEX IF NOT EXISTS idx_entry_points_application_id      ON them.entry_points(application_id);
CREATE INDEX IF NOT EXISTS idx_entry_points_slug                ON them.entry_points(slug);
CREATE INDEX IF NOT EXISTS idx_entry_points_app_orchestrator_id ON them.entry_points(app_orchestrator_id);

CREATE TABLE IF NOT EXISTS them.runs (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    orchestrator_id     UUID        REFERENCES them.orchestrators(id) ON DELETE SET NULL,
    orchestrator_name   TEXT,
    user_id             INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    session_id          UUID,
    goal                TEXT,
    status              TEXT        NOT NULL DEFAULT 'running'
                                    CHECK (status IN ('running','completed','failed','canceled','cancelled','stopped')),
    final_output        TEXT,
    error               TEXT,
    iterations          INTEGER     NOT NULL DEFAULT 0,
    total_tokens_in     INTEGER     NOT NULL DEFAULT 0,
    total_tokens_out    INTEGER     NOT NULL DEFAULT 0,
    total_cost_usd      NUMERIC(12,8) NOT NULL DEFAULT 0,
    entry_point_slug    TEXT,
    parent_run_id       UUID        REFERENCES them.runs(id) ON DELETE SET NULL,
    events_transport    TEXT        NOT NULL DEFAULT 'pubsub'
                                    CHECK (events_transport IN ('pubsub','streams')),
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at            TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ
);

COMMENT ON COLUMN them.runs.events_transport IS
  'Event delivery transport for this run. pubsub = legacy Redis Pub/Sub (at-most-once). streams = Redis Streams with replay (Phase 11c+).';

CREATE INDEX IF NOT EXISTS idx_runs_user_started   ON them.runs(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_orchestrator   ON them.runs(orchestrator_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status         ON them.runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_entry_point_slug ON them.runs(entry_point_slug);
CREATE INDEX IF NOT EXISTS idx_runs_parent_run_id  ON them.runs(parent_run_id);

CREATE TABLE IF NOT EXISTS them.run_steps (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      UUID        NOT NULL REFERENCES them.runs(id) ON DELETE CASCADE,
    iteration   INTEGER     NOT NULL DEFAULT 0,
    agent_id    UUID        REFERENCES them.agents(id) ON DELETE SET NULL,
    agent_slug  TEXT,
    tool_call_id TEXT,
    input       JSONB,
    output      TEXT,
    status      TEXT        NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','running','completed','failed','timeout')),
    error       TEXT,
    latency_ms  INTEGER,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at    TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_run_steps_run   ON them.run_steps(run_id, iteration);
CREATE INDEX IF NOT EXISTS idx_run_steps_agent ON them.run_steps(agent_id);

CREATE TABLE IF NOT EXISTS them.run_usage (
    id              BIGSERIAL   PRIMARY KEY,
    run_id          UUID        REFERENCES them.runs(id) ON DELETE CASCADE,
    user_id         INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    provider        TEXT,
    model           TEXT,
    tokens_input    INTEGER     NOT NULL DEFAULT 0,
    tokens_output   INTEGER     NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12,8) NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_run_usage_run         ON them.run_usage(run_id);
CREATE INDEX IF NOT EXISTS idx_run_usage_user_created ON them.run_usage(user_id, created_at);

CREATE TABLE IF NOT EXISTS them.audit_logs (
    id          BIGSERIAL   PRIMARY KEY,
    user_id     INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,
    entity_type TEXT,
    entity_id   TEXT,
    details     JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON them.audit_logs(created_at DESC);

CREATE TABLE IF NOT EXISTS them.tasks (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID        REFERENCES them.runs(id) ON DELETE SET NULL,
    parent_task_id  UUID        REFERENCES them.tasks(id) ON DELETE CASCADE,
    orchestrator_id UUID        REFERENCES them.orchestrators(id) ON DELETE SET NULL,
    agent_id        UUID        REFERENCES them.agents(id) ON DELETE SET NULL,
    user_id         INTEGER     REFERENCES auth_service.users(id) ON DELETE SET NULL,
    context_id      UUID,
    state           TEXT        NOT NULL DEFAULT 'submitted'
                                CHECK (state IN ('submitted','working','input-required','completed','failed','canceled','rejected')),
    kind            TEXT        NOT NULL DEFAULT 'root'
                                CHECK (kind IN ('root','delegated')),
    remote_task_id  TEXT,
    push_url        TEXT,
    status_message  JSONB,
    input_message   JSONB,
    budget_tokens   INTEGER,
    deadline        TIMESTAMPTZ,
    max_depth       INTEGER     NOT NULL DEFAULT 5,
    tokens_used     INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tasks_context ON them.tasks(context_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_parent  ON them.tasks(parent_task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_state   ON them.tasks(state)
    WHERE state IN ('submitted','working','input-required');
CREATE INDEX IF NOT EXISTS idx_tasks_remote  ON them.tasks(remote_task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_run     ON them.tasks(run_id);
CREATE INDEX IF NOT EXISTS idx_tasks_user_id ON them.tasks(user_id);

CREATE TABLE IF NOT EXISTS them.artifacts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID        NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    context_id  UUID,
    artifact_id TEXT        NOT NULL,
    name        TEXT,
    parts       JSONB,
    append_index INTEGER,
    last_chunk  BOOLEAN,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_artifacts_task_artifact_idx UNIQUE (task_id, artifact_id, append_index)
);

CREATE INDEX IF NOT EXISTS idx_artifacts_ctx  ON them.artifacts(context_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON them.artifacts(task_id);

CREATE TABLE IF NOT EXISTS them.task_messages (
    id      BIGSERIAL   PRIMARY KEY,
    task_id UUID        NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    role    TEXT        NOT NULL CHECK (role IN ('user','agent','system')),
    parts   JSONB,
    seq     INTEGER     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_task_messages_task_seq UNIQUE (task_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_task_messages ON them.task_messages(task_id, seq);

CREATE TABLE IF NOT EXISTS them.middleware_defs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT        NOT NULL UNIQUE,
    kind            TEXT        NOT NULL CHECK (kind IN ('guard','cache')),
    display_name    TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    config          JSONB       NOT NULL DEFAULT '{}',
    is_builtin      BOOLEAN     NOT NULL DEFAULT false,
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mw_defs_kind ON them.middleware_defs(kind);

CREATE TABLE IF NOT EXISTS them.middleware_wirings (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    agent_id        UUID        NOT NULL REFERENCES them.agents(id) ON DELETE CASCADE,
    def_id          UUID        NOT NULL REFERENCES them.middleware_defs(id) ON DELETE RESTRICT,
    position        INTEGER     NOT NULL DEFAULT 0,
    config_override JSONB       NOT NULL DEFAULT '{}',
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    node_id         TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_mw_wiring_app_agent_pos UNIQUE (application_id, agent_id, position)
);

CREATE INDEX        IF NOT EXISTS idx_mw_wirings_app_agent ON them.middleware_wirings(application_id, agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_mw_wiring_app_node
    ON them.middleware_wirings(application_id, node_id)
    WHERE node_id IS NOT NULL AND node_id != '';

-- =============================================================================
-- 3. SEED REQUIRED SYSTEM DATA (minimal — no demo agents, no user accounts)
-- =============================================================================

-- LLM provider defaults: Anthropic + OpenAI stubs. No api_key — operators set those.
INSERT INTO them.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES
  ('anthropic', 'Anthropic Claude', 'claude-sonnet-4-6',
   '{"claude-sonnet-4-6":{"input":3.00,"output":15.00},"claude-opus-4-8":{"input":15.00,"output":75.00},"claude-haiku-4-5-20251001":{"input":0.80,"output":4.00}}',
   true),
  ('openai', 'OpenAI', 'gpt-4o-mini',
   '{"gpt-4o-mini":{"input":0.15,"output":0.60},"gpt-4o":{"input":2.50,"output":10.00},"gpt-4.1":{"input":2.00,"output":8.00}}',
   false)
ON CONFLICT (name) DO NOTHING;

-- LLM routing default
INSERT INTO them.config (config_key, config_value)
VALUES ('llm_routing', '{"provider":"anthropic","model":"claude-sonnet-4-6","max_tokens":4096}')
ON CONFLICT (config_key) DO NOTHING;

-- Summarizer default
INSERT INTO them.config (config_key, config_value)
VALUES ('summarizer.default', '{"provider":"anthropic","model":"claude-haiku-4-5-20251001"}')
ON CONFLICT (config_key) DO NOTHING;

-- System agents config stub
INSERT INTO them.config (config_key, config_value)
VALUES ('system_agents', '{"roles":{"classifier":{"enabled":false,"provider":null,"model":null,"base_url":null,"system_prompt":null,"api_key_encrypted":null}}}')
ON CONFLICT (config_key) DO NOTHING;

-- Built-in middleware definitions
INSERT INTO them.middleware_defs (slug, kind, display_name, description, config, is_builtin, enabled)
VALUES
  ('guard_default', 'guard', 'Guard (PII + Prompt Injection)',
   'In-process PII detection and prompt-injection detection. Block or redact.',
   '{"mode":"redact","checks":["pii","prompt_injection"],"pii_entities":["EMAIL","PHONE","CREDIT_CARD","SSN"],"on_block_message":"This request was blocked by a safety guard."}',
   true, true),
  ('cache_default', 'cache', 'Exact-Match Cache',
   'Redis-backed exact-match response cache. Scopes: global, app, session, user.',
   '{"ttl_seconds":300,"scope":"global","key_fields":["message"],"max_result_chars":100000}',
   true, true)
ON CONFLICT (slug) DO NOTHING;

-- =============================================================================
-- 4. RECORD THIS SNAPSHOT IN schema_migrations
-- All versions 001–025 are considered applied. Future migrations (026+) will
-- insert their own row after running under the advisory lock.
-- =============================================================================

INSERT INTO them.schema_migrations (version, description, applied_at) VALUES
  ('001', 'base schema',                              now()),
  ('002', 'seed data (moved to separate seed files)', now()),
  ('003_phase8',           'phase 8: transport consolidation, memory, edges',   now()),
  ('003_users_seed',       'dev user accounts (applied separately)',            now()),
  ('004_phase9',           'phase 9: tasks.user_id, applications table',        now()),
  ('005_phase10',          'phase 10: SSE edge, drop entry_point_type check',   now()),
  ('006_phase11',          'phase 11: history_window on orchestrators',         now()),
  ('007_docu_stack',       'documentation stack agents + orchestrator',         now()),
  ('008_debate_stack',     'debate stack agents + orchestrator',                now()),
  ('009_security_scan',    'security scanner agent + scan columns',             now()),
  ('010_agent_icon',       'agent icon column',                                 now()),
  ('010_agent_retry',      'per-agent temporal retry policy',                   now()),
  ('010_app_entrypoints',  'split applications into parent+child entry_points', now()),
  ('010_workflow_advisor', 'workflow advisor agent + orchestrator',             now()),
  ('011_conversation_budget', 'per-conversation token budget on applications',  now()),
  ('012_sub_orchestrator', 'sub-orchestrator parent_run_id on runs',            now()),
  ('013_agentic_middleware','agentic middleware tables + built-in defs',         now()),
  ('014_app_orchestrators','app-scoped orchestrator instances',                  now()),
  ('015_drop_deprecated',  'drop a2a_exposed and applications.orchestrator_id', now()),
  ('016_graph_storage',    'canvas app_nodes/app_edges tables',                 now()),
  ('017_canvas_layout',    'replace app_nodes/edges with canvas JSONB',         now()),
  ('018_graph_compiler',   'graph compiler: node_id unique index',              now()),
  ('019_system_agents',    'system agents config stub',                         now()),
  ('020_agent_category',   'agent category column',                             now()),
  ('021_voice_ep',         'voice entry point type',                            now()),
  ('022_runtime_limits',   'per-EP max_concurrent_sessions',                   now()),
  ('023_app_runtime',      'app-level runtime_config JSONB',                    now()),
  ('024_ep_queue',         'entry point queue config columns',                  now()),
  ('025_events_transport', 'events_transport column on runs (Phase 11c)',        now())
ON CONFLICT (version) DO NOTHING;

COMMIT;
