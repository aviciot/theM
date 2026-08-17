-- Migration 032: per-application LLM provider API keys
-- Stores one encrypted key per provider per application.
-- Never included in the canvas doc / app_definitions export.
-- Shape: { "anthropic": "enc:...", "openai": "enc:..." }
ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS provider_keys JSONB NOT NULL DEFAULT '{}';
