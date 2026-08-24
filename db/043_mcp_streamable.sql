-- Add streamable-http transport to mcp_servers
-- MCP spec 2025-03-26 made Streamable HTTP the primary transport (SSE is legacy).
-- The client.go HTTP POST path works identically for both http and streamable-http.

ALTER TABLE them.mcp_servers
    DROP CONSTRAINT IF EXISTS mcp_servers_transport_check;

ALTER TABLE them.mcp_servers
    ADD CONSTRAINT mcp_servers_transport_check
        CHECK (transport IN ('http', 'sse', 'streamable-http'));
