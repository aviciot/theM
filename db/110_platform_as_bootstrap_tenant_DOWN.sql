-- db/110_platform_as_bootstrap_tenant_DOWN.sql
-- Rollback for db/110_platform_as_bootstrap_tenant.sql
-- (docs/PLATFORM_AS_TENANT_PLAN.md Phase 1 rollback section).
--
-- IMPORTANT: this only reverses db/110's DATA + RLS changes. If Phase 2 has
-- already shipped (deleted GetProviderByNamePlatform, resolvePlatformSystemAgentRole,
-- llm_provider_keys_platform.go, SystemAgentsHandler, etc.), this down-script
-- alone is NOT sufficient — that code must also be restored from version
-- control (git revert the Phase 2 commit(s)) before this rollback is
-- functionally complete. Running this DOWN script after Phase 2 without
-- restoring that code leaves the system with NULL-tenant rows again but no
-- code path left that reads them.
--
-- This script identifies "which rows to revert" by re-deriving them from
-- the bootstrap tenant ID directly, NOT from a captured id list — this is
-- safe ONLY if the bootstrap tenant has not since created genuinely its own
-- llm_providers/llm_provider_keys rows that should stay tenant-owned after
-- rollback. Verify with the SELECT statements below before running the
-- UPDATEs — if the bootstrap tenant has added real rows of its own since
-- Phase 1 ran, this blind rollback would incorrectly revert those too.

-- ── Pre-flight: inspect before reverting ────────────────────────────────
-- Run these manually and eyeball the result before uncommenting the UPDATEs
-- below. This migration intentionally does not auto-run past this point.

-- SELECT id, name, tenant_id, created_at FROM them.llm_providers
--   WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid;
-- SELECT id, name, tenant_id, created_at FROM them.llm_provider_keys
--   WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid;

BEGIN;

-- ── 1. Restore the original RLS policy (db/077's exact original text) ─────

DROP POLICY IF EXISTS llm_providers_read ON them.llm_providers;
CREATE POLICY llm_providers_read ON them.llm_providers
    AS PERMISSIVE
    FOR SELECT
    TO them_app
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR tenant_id IS NULL
    );

-- ── 2. Restore partial unique indexes (db/057 / db/108 original shape) ────

DROP INDEX IF EXISTS them.llm_providers_name_tenant_uq;
CREATE UNIQUE INDEX IF NOT EXISTS llm_providers_name_platform_uq
    ON them.llm_providers (name)
    WHERE tenant_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS llm_providers_name_tenant_uq
    ON them.llm_providers (name, tenant_id)
    WHERE tenant_id IS NOT NULL;

DROP INDEX IF EXISTS them.llm_provider_keys_name_tenant_uq;
DROP INDEX IF EXISTS them.llm_provider_keys_default_tenant_uq;
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

-- ── 3. Revert the data (UNCOMMENT ONLY AFTER CONFIRMING THE PRE-FLIGHT
-- SELECTs ABOVE — see the warning at the top of this file) ────────────────

-- UPDATE them.llm_providers
-- SET tenant_id = NULL
-- WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
--   AND name IN ('openai', 'mock', 'anthropic', 'gemini', 'groq'); -- the exact set db/110 migrated

-- UPDATE them.llm_provider_keys
-- SET tenant_id = NULL
-- WHERE tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
--   AND id IN (143); -- the exact set db/110 migrated on this box; adjust per environment

COMMIT;

DELETE FROM them.schema_migrations WHERE version = '110_platform_as_bootstrap_tenant';
