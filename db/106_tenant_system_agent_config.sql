-- db/106_tenant_system_agent_config.sql
-- Per-tenant mode (general/custom) for system-agent roles (classifier, card_synthesizer).
--
-- Context: them.config['system_agents'] is platform-global (no tenant_id column exists on
-- them.config at all) — every tenant shares the same classifier/card_synthesizer provider,
-- model, and key today. This table lets each tenant independently choose, per role:
--   mode='general' -> use the tenant's own LLM Providers config (them.llm_providers +
--                     them.llm_provider_keys, see db/105) via provider_name + key_id
--                     (key_id NULL = use that provider's default key)
--   mode='custom'  -> today's behavior: its own standalone provider/model/key/base_url/prompt
--
-- The platform-global them.config['system_agents'] row is untouched and continues to serve
-- any platform-internal (non-tenant) use of these roles.

CREATE TABLE IF NOT EXISTS them.tenant_system_agent_config (
    tenant_id                  UUID NOT NULL REFERENCES them.tenants(id) ON DELETE CASCADE,
    role                       TEXT NOT NULL,
    mode                       TEXT NOT NULL DEFAULT 'custom' CHECK (mode IN ('general', 'custom')),

    -- mode='general' fields
    provider_name              TEXT,
    key_id                     BIGINT REFERENCES them.llm_provider_keys(id) ON DELETE SET NULL,

    -- mode='custom' fields — mirrors them.config['system_agents'].roles[role] shape
    custom_provider             TEXT,
    custom_model                TEXT,
    custom_api_key_encrypted    TEXT,
    custom_base_url             TEXT,
    custom_system_prompt        TEXT,

    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, role)
);

-- ── Ownership / grants ─────────────────────────────────────────────────────────
-- Queried exclusively via the admin pool (them_admin, BYPASSRLS) — same posture as
-- them.llm_providers / them.llm_provider_keys. Tenant isolation is enforced by an
-- application-level WHERE tenant_id = $1 predicate, not RLS/GUC alone. RLS added
-- below for defense-in-depth only, matching db/105's llm_provider_keys pattern.

ALTER TABLE them.tenant_system_agent_config OWNER TO them_owner;

GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_system_agent_config TO them_admin;
GRANT SELECT ON them.tenant_system_agent_config TO them_app;

ALTER TABLE them.tenant_system_agent_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tenant_system_agent_config FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_system_agent_config_tenant_isolation ON them.tenant_system_agent_config
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('106_tenant_system_agent_config', 'Per-tenant general/custom mode config for classifier and card_synthesizer system-agent roles', NOW())
ON CONFLICT (version) DO NOTHING;
