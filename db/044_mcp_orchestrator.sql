-- 044: add mcp_servers column to app_orchestrators
-- Stores the list of MCP servers attached to an LLM node, with optional tool allowlists.
-- Schema: [{slug: string, tools: string[]}] — empty tools[] means all tools visible.

ALTER TABLE them.app_orchestrators
  ADD COLUMN IF NOT EXISTS mcp_servers jsonb NOT NULL DEFAULT '[]';
