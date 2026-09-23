-- db/108_platform_llm_provider_keys.sql
-- Extends them.llm_provider_keys (multiple named keys per provider, db/105)
-- to platform-level providers, not just tenant providers.
--
-- Context: db/105 made tenant_id NOT NULL on this table, so the-M itself
-- (super_admin) could never have more than the single api_key_encrypted
-- column on them.llm_providers — no way to save several named platform
-- keys per provider the way a tenant can. This mirrors the existing
-- llm_providers.tenant_id design exactly: tenant_id IS NULL = platform-owned
-- key, non-NULL = tenant-owned key. Same partial-unique-index pattern
-- llm_providers already uses (llm_providers_name_platform_uq /
-- llm_providers_name_tenant_uq) applied here for (llm_provider_id, name)
-- and the is_default constraint.

-- 1. Drop the old NOT NULL + FK + old constraints that assumed every key has a tenant.
ALTER TABLE them.llm_provider_keys
    DROP CONSTRAINT IF EXISTS llm_provider_keys_llm_provider_id_tenant_id_name_key;
DROP INDEX IF EXISTS them.llm_provider_keys_one_default_uq;
ALTER TABLE them.llm_provider_keys
    ALTER COLUMN tenant_id DROP NOT NULL;

-- 2. Re-create as partial unique indexes so NULL (platform) and non-NULL
-- (tenant) each get correctly scoped uniqueness, mirroring llm_providers.
CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_name_platform_uq
    ON them.llm_provider_keys (llm_provider_id, name)
    WHERE tenant_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_name_tenant_uq
    ON them.llm_provider_keys (llm_provider_id, tenant_id, name)
    WHERE tenant_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_default_platform_uq
    ON them.llm_provider_keys (llm_provider_id)
    WHERE tenant_id IS NULL AND is_default;

CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_default_tenant_uq
    ON them.llm_provider_keys (llm_provider_id, tenant_id)
    WHERE tenant_id IS NOT NULL AND is_default;

-- 3. RLS: them_app must never see platform-owned keys (tenant_id IS NULL) —
-- the existing tenant-isolation policy from db/105 already scopes
-- USING/WITH CHECK to current_setting('app.tenant_id'), which a NULL row can
-- never match, so platform rows are already correctly invisible to them_app
-- without any policy change. Platform-key management stays admin-pool-only
-- (them_admin, BYPASSRLS) exactly like tenant key management already is.

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('108_platform_llm_provider_keys', 'Allow tenant_id IS NULL (platform-owned) rows on them.llm_provider_keys, mirroring llm_providers', NOW())
ON CONFLICT (version) DO NOTHING;
