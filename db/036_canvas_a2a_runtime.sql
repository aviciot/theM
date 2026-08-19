-- db/036_canvas_a2a_runtime.sql
-- Phase 3 of the Canvas A2A Agent Builder.
-- Runtime tables: compiled spec + per-application credential bindings.
--
-- Security invariants:
--   - agent_runtime_specs.spec: compiled AgentSpec JSONB — slot NAMES only, no values
--   - app_agent_bindings.credential_bindings: Fernet (AES-GCM) ciphertext — NEVER plaintext
--   - Responses from bindings API return {slot: bool} only — never ciphertext or plaintext

CREATE TABLE IF NOT EXISTS them.agent_runtime_specs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    definition_id   UUID        NOT NULL REFERENCES them.agent_definitions(id) ON DELETE RESTRICT,
    agent_id        UUID        NOT NULL REFERENCES them.agents(id) ON DELETE CASCADE,
    spec            JSONB       NOT NULL,   -- compiled AgentSpec (slot names only, no secrets)
    spec_hash       TEXT        NOT NULL,
    deployed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (definition_id)                  -- one compiled spec per definition revision
);

CREATE INDEX IF NOT EXISTS agent_runtime_specs_tenant
    ON them.agent_runtime_specs (tenant_id);

CREATE INDEX IF NOT EXISTS agent_runtime_specs_agent
    ON them.agent_runtime_specs (agent_id);

CREATE TABLE IF NOT EXISTS them.app_agent_bindings (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id      UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    agent_id            UUID        NOT NULL REFERENCES them.agents(id) ON DELETE RESTRICT,
    definition_id       UUID        REFERENCES them.agent_definitions(id) ON DELETE SET NULL,
    -- Fernet (AES-GCM) ciphertext per credential slot. NEVER plaintext.
    credential_bindings JSONB       NOT NULL DEFAULT '{}',
    config_overrides    JSONB       NOT NULL DEFAULT '{}',
    policies            JSONB       NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, agent_id)
);

CREATE INDEX IF NOT EXISTS app_agent_bindings_application
    ON them.app_agent_bindings (application_id);

CREATE INDEX IF NOT EXISTS app_agent_bindings_agent
    ON them.app_agent_bindings (agent_id);
