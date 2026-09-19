-- 101: App canvas inline LLM node runtime overrides
-- Provider/model for an app-canvas inline LLM node move to the Runtime screen,
-- mirroring app_agent_bindings.config_overrides.llm_nodes for the agent builder.
-- Stored outside the published definition so changing a model needs no re-publish.

BEGIN;

CREATE TABLE IF NOT EXISTS them.app_flow_llm_overrides (
    application_id UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    node_id         TEXT        NOT NULL,
    provider        TEXT        NOT NULL,
    model           TEXT        NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, node_id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_flow_llm_overrides TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_flow_llm_overrides TO them_admin;

COMMIT;
