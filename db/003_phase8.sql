-- Phase 8 migration — idempotent, run after 001_schema.sql + 002_seed.sql
-- Apply with:
--   docker cp db/003_phase8.sql them-postgres:/tmp/them_003_phase8.sql
--   docker exec them-postgres psql -U them -d them -f /tmp/them_003_phase8.sql

BEGIN;

-- ── 8.1: Collapse transports to a2a_async only ────────────────────────────
-- Migrate existing rows FIRST, then tighten the constraint.
UPDATE them.agents SET transport = 'a2a_async' WHERE transport IN ('omni_ws', 'a2a');

ALTER TABLE them.agents DROP CONSTRAINT IF EXISTS agents_transport_check;
ALTER TABLE them.agents
    ADD CONSTRAINT agents_transport_check CHECK (transport IN ('a2a_async'));
ALTER TABLE them.agents ALTER COLUMN transport SET DEFAULT 'a2a_async';

-- ── 8.2: Agent card discovery provenance ──────────────────────────────────
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS card_fetched_at TIMESTAMPTZ;
ALTER TABLE them.agents ADD COLUMN IF NOT EXISTS skills JSONB NOT NULL DEFAULT '[]';

-- ── 8.4: Per-orchestrator memory / summarization ──────────────────────────
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarize_every_n_calls INTEGER NOT NULL DEFAULT 3;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS memory_raw_fallback_n INTEGER NOT NULL DEFAULT 5;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_provider TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_model TEXT;
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS summarizer_api_key_encrypted TEXT;

-- ── 8.5: Budget tokens (was always read but column never existed) ─────────
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS budget_tokens INTEGER;

-- ── 8.6: Pluggable edges ──────────────────────────────────────────────────
ALTER TABLE them.orchestrators ADD COLUMN IF NOT EXISTS edges TEXT[] NOT NULL DEFAULT '{websocket}';

-- ── 8.3: Global summarizer default ───────────────────────────────────────
INSERT INTO them.config (config_key, config_value)
VALUES ('summarizer.default', '{"provider":"anthropic","model":"claude-haiku-4-5-20251001"}')
ON CONFLICT (config_key) DO NOTHING;

-- ── 8.3: Seed OpenAI provider row ────────────────────────────────────────
INSERT INTO them.llm_providers (name, display_name, default_model, model_pricing, enabled)
VALUES (
    'openai',
    'OpenAI',
    'gpt-4o-mini',
    '{"gpt-4o-mini": {"input": 0.15, "output": 0.60}, "gpt-4o": {"input": 2.50, "output": 10.00}, "gpt-4.1": {"input": 2.00, "output": 8.00}}',
    false
) ON CONFLICT (name) DO NOTHING;

COMMIT;
