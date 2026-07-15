-- the-M database schema — idempotent (safe to re-run)
-- Apply: docker exec them-postgres psql -U them -d them -f /tmp/001_schema.sql

CREATE SCHEMA IF NOT EXISTS them;

CREATE TABLE IF NOT EXISTS them.llm_providers (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    api_key_encrypted TEXT,
    base_url TEXT,
    default_model TEXT NOT NULL,
    model_pricing JSONB NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.config (
    config_key TEXT PRIMARY KEY,
    config_value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_]{1,48}$'),
    display_name TEXT NOT NULL,
    description TEXT NOT NULL,
    transport TEXT NOT NULL DEFAULT 'omni_ws' CHECK (transport IN ('omni_ws', 'a2a')),
    endpoint_url TEXT NOT NULL,
    auth_token_encrypted TEXT,
    input_schema JSONB NOT NULL DEFAULT '{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}',
    timeout_seconds INTEGER NOT NULL DEFAULT 120,
    max_concurrency INTEGER NOT NULL DEFAULT 4,
    max_retries INTEGER NOT NULL DEFAULT 2,
    enabled BOOLEAN NOT NULL DEFAULT true,
    tags TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_slug ON them.agents(slug);
CREATE INDEX IF NOT EXISTS idx_agents_enabled ON them.agents(enabled);

CREATE TABLE IF NOT EXISTS them.orchestrators (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    allowed_agent_ids UUID[] NOT NULL DEFAULT '{}',
    llm_provider TEXT,
    llm_model TEXT,
    llm_api_key_encrypted TEXT,
    llm_base_url TEXT,
    max_iterations INTEGER NOT NULL DEFAULT 10,
    max_parallel_tools INTEGER NOT NULL DEFAULT 4,
    rate_limit_rpm INTEGER NOT NULL DEFAULT 30,
    daily_budget_usd NUMERIC(10,4) NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS them.access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    orchestrator_id UUID REFERENCES them.orchestrators(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_access_tokens_user ON them.access_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_access_tokens_hash ON them.access_tokens(token_hash);

CREATE TABLE IF NOT EXISTS them.runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    orchestrator_id UUID NOT NULL REFERENCES them.orchestrators(id),
    orchestrator_name TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    session_id UUID NOT NULL,
    goal TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed','canceled','cancelled','stopped')),
    final_output TEXT,
    error TEXT,
    iterations INTEGER NOT NULL DEFAULT 0,
    total_tokens_in INTEGER NOT NULL DEFAULT 0,
    total_tokens_out INTEGER NOT NULL DEFAULT 0,
    total_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_runs_user_started ON them.runs(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_orchestrator ON them.runs(orchestrator_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status ON them.runs(status);

CREATE TABLE IF NOT EXISTS them.run_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES them.runs(id) ON DELETE CASCADE,
    iteration INTEGER NOT NULL,
    agent_id UUID REFERENCES them.agents(id) ON DELETE SET NULL,
    agent_slug TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    input JSONB NOT NULL,
    output TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','timeout')),
    error TEXT,
    latency_ms INTEGER,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_run_steps_run ON them.run_steps(run_id, iteration);
CREATE INDEX IF NOT EXISTS idx_run_steps_agent ON them.run_steps(agent_id);

CREATE TABLE IF NOT EXISTS them.run_usage (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES them.runs(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    tokens_input INTEGER NOT NULL DEFAULT 0,
    tokens_output INTEGER NOT NULL DEFAULT 0,
    cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_run_usage_run ON them.run_usage(run_id);
CREATE INDEX IF NOT EXISTS idx_run_usage_user_created ON them.run_usage(user_id, created_at);

CREATE TABLE IF NOT EXISTS them.audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id INTEGER,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT,
    details JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON them.audit_logs(created_at DESC);

-- ── Phase 2: Task graph ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID REFERENCES them.runs(id) ON DELETE SET NULL,
    parent_task_id  UUID REFERENCES them.tasks(id) ON DELETE CASCADE,
    orchestrator_id UUID REFERENCES them.orchestrators(id),
    agent_id        UUID REFERENCES them.agents(id) ON DELETE SET NULL,
    context_id      UUID NOT NULL,
    state           TEXT NOT NULL DEFAULT 'submitted'
                    CHECK (state IN ('submitted','working','input-required',
                                     'completed','failed','canceled','rejected')),
    kind            TEXT NOT NULL DEFAULT 'root'
                    CHECK (kind IN ('root','delegated')),
    remote_task_id  TEXT,
    push_url        TEXT,
    status_message  JSONB,
    input_message   JSONB NOT NULL DEFAULT '{}',
    budget_tokens   INTEGER,
    deadline        TIMESTAMPTZ,
    max_depth       INTEGER NOT NULL DEFAULT 5,
    tokens_used     INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tasks_context   ON them.tasks(context_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_parent    ON them.tasks(parent_task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_state     ON them.tasks(state)
    WHERE state IN ('submitted','working','input-required');
CREATE INDEX IF NOT EXISTS idx_tasks_remote    ON them.tasks(remote_task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_run       ON them.tasks(run_id);

CREATE TABLE IF NOT EXISTS them.artifacts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    context_id    UUID NOT NULL,
    artifact_id   TEXT NOT NULL,
    name          TEXT,
    parts         JSONB NOT NULL,
    append_index  INTEGER NOT NULL DEFAULT 0,
    last_chunk    BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, artifact_id, append_index)
);
CREATE INDEX IF NOT EXISTS idx_artifacts_ctx  ON them.artifacts(context_id, created_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON them.artifacts(task_id);

CREATE TABLE IF NOT EXISTS them.task_messages (
    id          BIGSERIAL PRIMARY KEY,
    task_id     UUID NOT NULL REFERENCES them.tasks(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('user','agent','system')),
    parts       JSONB NOT NULL,
    seq         INTEGER NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_task_messages ON them.task_messages(task_id, seq);

-- A2A server support
-- DEPRECATED: a2a_exposed pending drop in Phase 12 (015_contract_orchestrator_split.sql)
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS a2a_exposed BOOLEAN NOT NULL DEFAULT FALSE;

-- A2A agent card cache + capability flags
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS agent_card JSONB;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS agent_card_url TEXT;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS card_fetched_at TIMESTAMPTZ;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS skills JSONB NOT NULL DEFAULT '[]';
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS supports_streaming BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS supports_push BOOLEAN NOT NULL DEFAULT FALSE;

-- Phase 8.1: collapse to a2a_async only (migrate rows first, then tighten)
-- Note: 003_phase8.sql handles the live migration. This reflects final desired state.
ALTER TABLE them.agents DROP CONSTRAINT IF EXISTS agents_transport_check;
ALTER TABLE them.agents ADD CONSTRAINT agents_transport_check
    CHECK (transport IN ('a2a_async'));

-- Voice transcription
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS voice_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS transcription_provider VARCHAR(32);
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS transcription_model VARCHAR(64);
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS transcription_api_key_encrypted TEXT;

-- TTS
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS tts_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS tts_provider TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS tts_voice TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS tts_api_key_encrypted TEXT;

-- Memory / context threading
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarize_every_n_calls INTEGER NOT NULL DEFAULT 3;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_raw_fallback_n INTEGER NOT NULL DEFAULT 5;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_provider TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_model TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_api_key_encrypted TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS edges TEXT[] NOT NULL DEFAULT ARRAY['websocket'];
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS budget_tokens INTEGER;

-- Phase 11: history_window
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS history_window INTEGER NOT NULL DEFAULT 20;

-- Phase 14: delegatable replaces a2a_exposed for internal sub-orch delegation
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS delegatable BOOLEAN NOT NULL DEFAULT FALSE;

-- ── Phase 9: Applications ─────────────────────────────────────────────────────
-- DEPRECATED column: orchestrator_id pending drop in Phase 12 (015_contract_orchestrator_split.sql)

CREATE TABLE IF NOT EXISTS them.applications (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL,
    orchestrator_id  UUID        NOT NULL REFERENCES them.orchestrators(id) ON DELETE CASCADE, -- DEPRECATED: pending drop in Phase 12 (015_contract_orchestrator_split.sql)
    presentation     JSONB       NOT NULL DEFAULT '{}',
    enabled          BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE them.applications ADD COLUMN IF NOT EXISTS conversation_token_limit INTEGER;

-- ── Phase 10: Entry Points ────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.entry_points (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id           UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    slug                     TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_-]{1,64}$'),
    entry_point_type         TEXT        NOT NULL CHECK (entry_point_type IN ('websocket', 'sse', 'webrtc', 'a2a')),
    access_policy            JSONB       NOT NULL DEFAULT '{"mode":"token"}',
    conversation_token_limit INTEGER,
    enabled                  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_entry_points_application_id ON them.entry_points(application_id);
CREATE INDEX IF NOT EXISTS idx_entry_points_slug           ON them.entry_points(slug);

ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS entry_point_slug TEXT;
CREATE INDEX IF NOT EXISTS idx_runs_entry_point_slug ON them.runs(entry_point_slug);

ALTER TABLE them.runs ADD COLUMN IF NOT EXISTS parent_run_id UUID NULL
    REFERENCES them.runs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_runs_parent_run_id ON them.runs(parent_run_id);

-- ── Phase 14: app_orchestrators ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.app_orchestrators (
    id                              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id                  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    orchestrator_id                 UUID        REFERENCES them.orchestrators(id) ON DELETE SET NULL,
    name                            TEXT        NOT NULL,
    node_id                         TEXT,
    kind                            TEXT        NOT NULL DEFAULT 'standard',
    delegatable                     BOOLEAN     NOT NULL DEFAULT FALSE,
    -- ── Config columns (cloned from them.orchestrators) ──────────────────────
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
    voice_enabled                   BOOLEAN     NOT NULL DEFAULT FALSE,
    transcription_provider          VARCHAR(32),
    transcription_model             VARCHAR(64),
    transcription_api_key_encrypted TEXT,
    tts_enabled                     BOOLEAN     NOT NULL DEFAULT FALSE,
    tts_provider                    TEXT,
    tts_voice                       TEXT,
    tts_api_key_encrypted           TEXT,
    memory_enabled                  BOOLEAN     NOT NULL DEFAULT FALSE,
    summarize_every_n_calls         INTEGER     NOT NULL DEFAULT 3,
    memory_raw_fallback_n           INTEGER     NOT NULL DEFAULT 5,
    summarizer_provider             TEXT,
    summarizer_model                TEXT,
    summarizer_api_key_encrypted    TEXT,
    edges                           TEXT[]      NOT NULL DEFAULT '{websocket}',
    history_window                  INTEGER     NOT NULL DEFAULT 20,
    budget_tokens                   INTEGER,
    enabled                         BOOLEAN     NOT NULL DEFAULT TRUE,
    -- ── Timestamps ───────────────────────────────────────────────────────────
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- ── Constraints ──────────────────────────────────────────────────────────
    CONSTRAINT uq_app_orchestrators_name        UNIQUE (name),
    CONSTRAINT ck_app_orchestrators_name_slug   CHECK  (name ~ '^[a-z0-9_-]{1,64}$'),
    CONSTRAINT ck_app_orchestrators_kind        CHECK  (kind IN ('standard', 'router', 'voice'))
);
CREATE INDEX IF NOT EXISTS idx_app_orchestrators_application_id ON them.app_orchestrators(application_id);
CREATE INDEX IF NOT EXISTS idx_app_orchestrators_name           ON them.app_orchestrators(name);

-- Phase 14: FK from entry_points to app_orchestrators
ALTER TABLE them.entry_points
    ADD COLUMN IF NOT EXISTS app_orchestrator_id UUID
        REFERENCES them.app_orchestrators(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_entry_points_app_orchestrator_id ON them.entry_points(app_orchestrator_id);

-- ── Phase 13: Agentic Middleware ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_defs (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          TEXT        NOT NULL UNIQUE,
    kind          TEXT        NOT NULL,
    display_name  TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    config        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    is_builtin    BOOLEAN     NOT NULL DEFAULT false,
    enabled       BOOLEAN     NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_mw_defs_kind CHECK (kind IN ('guard', 'cache'))
);
CREATE INDEX IF NOT EXISTS idx_mw_defs_kind ON them.middleware_defs(kind);

CREATE TABLE IF NOT EXISTS them.middleware_wirings (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    agent_id        UUID        NOT NULL REFERENCES them.agents(id) ON DELETE CASCADE,
    def_id          UUID        NOT NULL REFERENCES them.middleware_defs(id) ON DELETE RESTRICT,
    position        INTEGER     NOT NULL DEFAULT 0,
    config_override JSONB       NOT NULL DEFAULT '{}'::jsonb,
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    node_id         TEXT        NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_mw_wiring_app_agent_pos UNIQUE (application_id, agent_id, position)
);
CREATE INDEX IF NOT EXISTS idx_mw_wirings_app_agent ON them.middleware_wirings(application_id, agent_id);

-- ── Phase 15: Canvas graph storage ───────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.app_nodes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    node_id         TEXT        NOT NULL,
    node_type       TEXT        NOT NULL,
    ref_id          UUID,
    position_x      DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_y      DOUBLE PRECISION NOT NULL DEFAULT 0,
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_nodes_app_node UNIQUE (application_id, node_id),
    CONSTRAINT ck_app_nodes_type CHECK (node_type IN ('entry_point','orchestrator','agent','middleware'))
);
CREATE INDEX IF NOT EXISTS idx_app_nodes_application_id ON them.app_nodes(application_id);

CREATE TABLE IF NOT EXISTS them.app_edges (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    edge_id         TEXT        NOT NULL,
    source_node_id  TEXT        NOT NULL,
    target_node_id  TEXT        NOT NULL,
    data            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_edges_app_edge UNIQUE (application_id, edge_id)
);
CREATE INDEX IF NOT EXISTS idx_app_edges_application_id ON them.app_edges(application_id);
