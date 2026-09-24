-- db/110_platform_as_bootstrap_tenant.sql
-- Platform-as-Tenant Phase 1 (docs/PLATFORM_AS_TENANT_PLAN.md) — the-M's own
-- "platform-owned" LLM provider/key rows (tenant_id IS NULL, introduced by
-- db/057 and mirrored by db/108) become real rows owned by the bootstrap
-- tenant (them.tenants.id = 00000000-0000-0000-0000-000000000001,
-- is_bootstrap = true) instead of a parallel NULL-tenant convention.
--
-- This migration is DATA + RLS ONLY. The Go/frontend code that reads/writes
-- via the now-retired NULL-tenant functions (GetProviderByNamePlatform,
-- resolvePlatformSystemAgentRole, llm_provider_keys_platform.go,
-- SystemAgentsHandler, etc.) is deleted in Phase 2, not here — this
-- migration does not remove any of those functions' ability to keep running
-- against the old NULL convention if Phase 2 is delayed; it only migrates
-- the underlying data + tightens the constraints/RLS that assumed NULL rows
-- could exist. Confirmed at migration-authoring time (2026-09-24): no
-- `them.config['system_agents']` row exists on this box, so there is no
-- config-migration step here — resolvePlatformSystemAgentRole's fail-open
-- default already covers the "no row" case identically either way. If a
-- box that DOES have a system_agents config row runs this migration, that
-- config is UNTOUCHED by this migration (still read via the old path until
-- Phase 2 ships) — flagged here so Phase 2's author checks for one.

BEGIN;

-- ── 1. Reassign platform-owned llm_providers rows to the bootstrap tenant ──

-- Safety check: this migration assumes the bootstrap tenant has no
-- conflicting (name, tenant_id) rows of its own yet. Abort loudly instead
-- of silently violating the tenant-scoped unique index added below.
DO $$
DECLARE
    conflict_count integer;
BEGIN
    SELECT count(*) INTO conflict_count
    FROM them.llm_providers p
    WHERE p.tenant_id IS NULL
      AND EXISTS (
          SELECT 1 FROM them.llm_providers p2
          WHERE p2.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
            AND p2.name = p.name
      );
    IF conflict_count > 0 THEN
        RAISE EXCEPTION 'db/110: % platform llm_providers row(s) collide by name with an existing bootstrap-tenant row — resolve manually before re-running', conflict_count;
    END IF;
END $$;

UPDATE them.llm_providers
SET tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE tenant_id IS NULL;

-- ── 2. Reassign platform-owned llm_provider_keys rows to the bootstrap tenant ──

DO $$
DECLARE
    conflict_count integer;
BEGIN
    SELECT count(*) INTO conflict_count
    FROM them.llm_provider_keys k
    WHERE k.tenant_id IS NULL
      AND EXISTS (
          SELECT 1 FROM them.llm_provider_keys k2
          WHERE k2.tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
            AND k2.llm_provider_id = k.llm_provider_id
            AND k2.name = k.name
      );
    IF conflict_count > 0 THEN
        RAISE EXCEPTION 'db/110: % platform llm_provider_keys row(s) collide by (provider,name) with an existing bootstrap-tenant row — resolve manually before re-running', conflict_count;
    END IF;
END $$;

UPDATE them.llm_provider_keys
SET tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE tenant_id IS NULL;

-- ── 3. them.llm_providers: replace partial (NULL-aware) unique indexes ─────
-- with normal tenant-scoped ones — no row can have a NULL tenant_id anymore
-- after step 1, so the platform-specific partial index has nothing left to
-- protect and is dropped rather than kept dormant.

DROP INDEX IF EXISTS them.llm_providers_name_platform_uq;
DROP INDEX IF EXISTS them.llm_providers_name_tenant_uq;

CREATE UNIQUE INDEX IF NOT EXISTS llm_providers_name_tenant_uq
    ON them.llm_providers (name, tenant_id);

-- ── 4. them.llm_provider_keys: same index consolidation (db/108) ──────────

DROP INDEX IF EXISTS them.llm_provider_keys_name_platform_uq;
DROP INDEX IF EXISTS them.llm_provider_keys_name_tenant_uq;
DROP INDEX IF EXISTS them.llm_provider_keys_default_platform_uq;
DROP INDEX IF EXISTS them.llm_provider_keys_default_tenant_uq;

CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_name_tenant_uq
    ON them.llm_provider_keys (llm_provider_id, tenant_id, name);

CREATE UNIQUE INDEX IF NOT EXISTS llm_provider_keys_default_tenant_uq
    ON them.llm_provider_keys (llm_provider_id, tenant_id)
    WHERE is_default;

-- ── 5. RLS rewrite — them.llm_providers only (see docs/PLATFORM_AS_TENANT_PLAN.md
-- decision 7, confirmed with the user 2026-09-24): every tenant keeps seeing
-- the bootstrap tenant's rows as "platform defaults" — same UX as today's
-- `OR tenant_id IS NULL` clause, just naming the bootstrap tenant explicitly
-- instead of relying on NULL (which no longer occurs on this table at all).
--
-- them.llm_provider_keys' RLS policy (db/105) needs NO rewrite: it was
-- already a plain tenant-equality check with no NULL-visibility clause, so
-- once no row has a NULL tenant_id, it behaves exactly as before — the
-- bootstrap tenant's own keys are visible only when acting as that tenant,
-- same as any tenant's keys are only visible to that tenant. This was the
-- correct, asymmetric finding from this plan's own review: llm_providers
-- and llm_provider_keys are NOT the same shape today, and only one needs a
-- policy change.

DROP POLICY IF EXISTS llm_providers_read ON them.llm_providers;
CREATE POLICY llm_providers_read ON them.llm_providers
    AS PERMISSIVE
    FOR SELECT
    TO them_app
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
    );

-- llm_providers_write is unchanged (db/077) — own-tenant-only, and the
-- bootstrap tenant already only edits its own rows via that same policy,
-- same as any tenant. Not touched by this migration.

COMMIT;

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('110_platform_as_bootstrap_tenant', 'Platform-as-Tenant Phase 1: migrate NULL-tenant llm_providers/llm_provider_keys rows to the bootstrap tenant; replace partial unique indexes; rewrite llm_providers_read RLS to name the bootstrap tenant instead of NULL', NOW())
ON CONFLICT (version) DO NOTHING;
