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
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed','cancelled','stopped')),
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
    agent_id UUID REFERENCES them.agents(id),
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
    agent_id        UUID REFERENCES them.agents(id),
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
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS a2a_exposed BOOLEAN NOT NULL DEFAULT FALSE;

-- A2A agent card cache + capability flags
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS agent_card JSONB;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS agent_card_url TEXT;
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
