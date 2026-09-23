-- db/109_system_agent_general_model.sql
-- Adds the missing "model" field to general mode for system-agent roles.
--
-- Bug: mode='general' resolution (resolveSystemAgentRole / resolvePlatformSystemAgentRole,
-- go/internal/admin/system_agent_resolve.go) always used provider.DefaultModel — there was
-- no way to pick a specific model from that provider's allowed_models list. This column
-- stores that choice for the tenant-scoped table; the platform-global equivalent lives in
-- them.config['system_agents'].roles[role].general_model (JSONB, no migration needed).

ALTER TABLE them.tenant_system_agent_config
    ADD COLUMN IF NOT EXISTS general_model TEXT;

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('109_system_agent_general_model', 'Add general_model column for mode=general model selection on tenant system-agent roles', NOW())
ON CONFLICT (version) DO NOTHING;
