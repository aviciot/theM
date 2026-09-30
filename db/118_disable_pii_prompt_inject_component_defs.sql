-- Migration 118: disable all guard middleware canvas nodes (pii_redact,
-- prompt_inject, file-guard). All guards are now configured exclusively via
-- the agent properties panel (AgentGuardsSection / middleware_wirings.node_id).
-- The middleware_defs rows are kept intact — the properties panel uses those.
-- Only the component_definitions rows (which drove CanvasPalette) are disabled.

UPDATE them.component_definitions
SET enabled = false
WHERE name IN ('pii_redact', 'prompt_inject', 'file-guard')
  AND kind = 'middleware';
