-- db/035_agent_definitions.sql
-- Phase 2 of the Canvas A2A Agent Builder.
-- Design-time table ONLY. Separate from the runtime registry them.agents.
-- Mirrors them.application_definitions (immutable-revision draft model).
-- NO secrets ever stored here — the definition JSONB carries credential SLOT
-- NAMES only, never values.

CREATE TABLE IF NOT EXISTS them.agent_definitions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    agent_slug      TEXT        NOT NULL,
    revision        INTEGER     NOT NULL,
    definition      JSONB       NOT NULL,          -- AgentDefinition canvas JSON (slot NAMES only)
    definition_hash TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'draft'
                                CHECK (status IN ('draft', 'published')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, agent_slug, revision)
);

CREATE INDEX IF NOT EXISTS agent_definitions_tenant_slug
    ON them.agent_definitions (tenant_id, agent_slug);

CREATE INDEX IF NOT EXISTS agent_definitions_tenant_status
    ON them.agent_definitions (tenant_id, status);
