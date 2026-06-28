-- Odin database schema — idempotent (safe to re-run)
-- Apply: docker exec omni-postgres psql -U odin -d odin -f /tmp/001_schema.sql

CREATE SCHEMA IF NOT EXISTS odin;

CREATE TABLE IF NOT EXISTS odin.llm_providers (
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

CREATE TABLE IF NOT EXISTS odin.config (
    config_key TEXT PRIMARY KEY,
    config_value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS odin.agents (
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_slug ON odin.agents(slug);
CREATE INDEX IF NOT EXISTS idx_agents_enabled ON odin.agents(enabled);

CREATE TABLE IF NOT EXISTS odin.orchestrators (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    allowed_agent_ids UUID[] NOT NULL DEFAULT '{}',
    llm_provider TEXT,
    llm_model TEXT,
    max_iterations INTEGER NOT NULL DEFAULT 10,
    max_parallel_tools INTEGER NOT NULL DEFAULT 4,
    rate_limit_rpm INTEGER NOT NULL DEFAULT 30,
    daily_budget_usd NUMERIC(10,4) NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS odin.access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    orchestrator_id UUID REFERENCES odin.orchestrators(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_access_tokens_user ON odin.access_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_access_tokens_hash ON odin.access_tokens(token_hash);

CREATE TABLE IF NOT EXISTS odin.runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    orchestrator_id UUID NOT NULL REFERENCES odin.orchestrators(id),
    orchestrator_name TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    session_id UUID NOT NULL,
    goal TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','failed','cancelled')),
    final_output TEXT,
    error TEXT,
    iterations INTEGER NOT NULL DEFAULT 0,
    total_tokens_in INTEGER NOT NULL DEFAULT 0,
    total_tokens_out INTEGER NOT NULL DEFAULT 0,
    total_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_runs_user_started ON odin.runs(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_orchestrator ON odin.runs(orchestrator_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status ON odin.runs(status);

CREATE TABLE IF NOT EXISTS odin.run_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES odin.runs(id) ON DELETE CASCADE,
    iteration INTEGER NOT NULL,
    agent_id UUID REFERENCES odin.agents(id),
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
CREATE INDEX IF NOT EXISTS idx_run_steps_run ON odin.run_steps(run_id, iteration);
CREATE INDEX IF NOT EXISTS idx_run_steps_agent ON odin.run_steps(agent_id);

CREATE TABLE IF NOT EXISTS odin.run_usage (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES odin.runs(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    tokens_input INTEGER NOT NULL DEFAULT 0,
    tokens_output INTEGER NOT NULL DEFAULT 0,
    cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_run_usage_run ON odin.run_usage(run_id);
CREATE INDEX IF NOT EXISTS idx_run_usage_user_created ON odin.run_usage(user_id, created_at);

CREATE TABLE IF NOT EXISTS odin.audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id INTEGER,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT,
    details JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON odin.audit_logs(created_at DESC);
