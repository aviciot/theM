-- 046: add probe_credential_encrypted to mcp_servers
-- Stores an encrypted bearer/header token used by the health-probe worker to
-- authenticate when calling initialize + tools/list on the MCP server.
-- Separate from per-app app_mcp_credentials — this is a platform-level probe
-- credential set by the admin when registering an auth-protected MCP server.
ALTER TABLE them.mcp_servers
    ADD COLUMN IF NOT EXISTS probe_credential_encrypted TEXT;

COMMENT ON COLUMN them.mcp_servers.probe_credential_encrypted IS
    'Fernet-encrypted token used by them-mcp-service health probe. NULL = no probe auth.';
