-- db/042_mcp_app_credentials.sql
-- Per-application MCP credentials: one row per (application, mcp_server) pair.
-- Mirrors the llm_providers Fernet-at-rest pattern.
-- Never returned in API responses — only credential_set bool is exposed.

CREATE TABLE IF NOT EXISTS them.app_mcp_credentials (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id       UUID        NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
    mcp_server_id        UUID        NOT NULL REFERENCES them.mcp_servers(id) ON DELETE CASCADE,
    credential_encrypted TEXT,
    auth_header_name     TEXT        NOT NULL DEFAULT 'Authorization',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (application_id, mcp_server_id)
);

COMMENT ON TABLE them.app_mcp_credentials IS
    'Per-application MCP server credentials. Fernet-encrypted at rest (same key as llm_providers). '
    'Plaintext is never logged or returned in API responses.';
