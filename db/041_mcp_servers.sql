-- db/041_mcp_servers.sql
-- MCP server registry: platform-registered MCP servers scoped by tenant.
-- Owned exclusively by them-mcp-service (health/manifest columns) and
-- them-go-bridge admin CRUD (name/url/auth_type/enabled columns).

CREATE TABLE IF NOT EXISTS them.mcp_servers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    name            TEXT        NOT NULL,
    slug            TEXT        NOT NULL,
    description     TEXT,
    transport       TEXT        NOT NULL DEFAULT 'http'
                                CHECK (transport IN ('http', 'sse', 'stdio')),
    url             TEXT,
    auth_type       TEXT        NOT NULL DEFAULT 'none'
                                CHECK (auth_type IN ('none', 'bearer', 'header', 'oauth2')),
    health_status   TEXT        NOT NULL DEFAULT 'unknown'
                                CHECK (health_status IN ('unknown', 'healthy', 'degraded', 'unreachable')),
    last_checked_at TIMESTAMPTZ,
    last_error      TEXT,
    tools_manifest  JSONB       NOT NULL DEFAULT '[]',
    capabilities    JSONB       NOT NULL DEFAULT '{}',
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_tenant_id
    ON them.mcp_servers (tenant_id);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled
    ON them.mcp_servers (enabled)
    WHERE enabled = true;

COMMENT ON TABLE them.mcp_servers IS
    'Platform-registered MCP servers. health_status/tools_manifest/last_checked_at '
    'are written exclusively by them-mcp-service. Admin CRUD writes name/slug/url/auth_type/enabled.';
