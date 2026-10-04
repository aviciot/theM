-- 119: Runtime credential/param storage for app-canvas HTTP nodes.
-- Mirrors app_flow_llm_overrides' pattern: scoped by application_id only
-- (no direct tenant_id — isolation is via the FK to applications.tenant_id
-- enforced by the RLS policy below, same as all other scoped-config tables).

BEGIN;

CREATE TABLE IF NOT EXISTS them.app_flow_http_params (
    application_id  UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    node_id         TEXT        NOT NULL,  -- canvas instance_id
    param_key       TEXT        NOT NULL,  -- e.g. "bearer_token", "api_key"
    -- value_encrypted: Fernet ciphertext for secret params; plaintext for
    -- non-sensitive string/url params. NULL until the user sets a value.
    value_encrypted TEXT,
    inject_mode     TEXT        NOT NULL DEFAULT 'header',  -- "header"|"query"|"basic"|"custom_header"
    inject_header_name TEXT,                                 -- only for inject_mode=custom_header
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, node_id, param_key)
);

ALTER TABLE them.app_flow_http_params ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.app_flow_http_params FORCE ROW LEVEL SECURITY;

CREATE POLICY app_flow_http_params_tenant_isolation ON them.app_flow_http_params
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_flow_http_params.application_id
          AND a.tenant_id = (NULLIF(current_setting('app.tenant_id', true), ''))::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_flow_http_params.application_id
          AND a.tenant_id = (NULLIF(current_setting('app.tenant_id', true), ''))::uuid
    ));

GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_flow_http_params TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_flow_http_params TO them_admin;

COMMIT;
