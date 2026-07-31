-- R-4a: Tenant Foundation
-- Creates them.tenants table, adds tenant_id to all tenant-owned tables,
-- backfills existing rows, replaces global uniqueness constraints with
-- tenant-scoped ones, and creates them.run_artifacts with tenant_id.
--
-- Safe to re-run (idempotent via IF NOT EXISTS / IF EXISTS guards).
-- Apply: docker exec them-postgres psql -U them -d them -f /tmp/026_tenant_foundation.sql

BEGIN;

-- ── 1. Tenants table ─────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.tenants (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9_-]{1,64}$'),
    display_name TEXT        NOT NULL,
    is_bootstrap BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tenants_slug ON them.tenants(slug);

-- ── 2. Bootstrap development tenant ──────────────────────────────────────────
-- UUID is deterministic so it can be hardcoded in config/tests.
-- Never change this UUID after first deployment.

INSERT INTO them.tenants (id, slug, display_name, is_bootstrap, created_at, updated_at)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'default',
    'Default Development Tenant',
    true,
    now(),
    now()
)
ON CONFLICT (id) DO NOTHING;

-- ── 3. Add tenant_id columns as NULLABLE (enables backfill without locking) ──

ALTER TABLE them.agents
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

ALTER TABLE them.orchestrators
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

ALTER TABLE them.access_tokens
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

ALTER TABLE them.applications
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

ALTER TABLE them.runs
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

ALTER TABLE them.audit_logs
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

-- app_orchestrators: tenant_id is redundant to application_id→applications.tenant_id
-- but adding it directly simplifies queries and is consistent.
ALTER TABLE them.app_orchestrators
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

-- ── 4. Backfill existing rows with bootstrap tenant ───────────────────────────
-- Only tables that currently have rows need backfill.

UPDATE them.agents
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.orchestrators
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.access_tokens
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.applications
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.runs
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.audit_logs
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

UPDATE them.app_orchestrators
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

-- ── 5. Validate: confirm no NULL tenant_id remains ───────────────────────────
-- These DO blocks raise an exception if any NULL survives backfill.

DO $$
DECLARE
    nulls INTEGER;
BEGIN
    SELECT COUNT(*) INTO nulls FROM them.agents WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % agents have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.orchestrators WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % orchestrators have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.access_tokens WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % access_tokens have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.applications WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % applications have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.runs WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % runs have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.audit_logs WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % audit_logs have NULL tenant_id', nulls;
    END IF;

    SELECT COUNT(*) INTO nulls FROM them.app_orchestrators WHERE tenant_id IS NULL;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % app_orchestrators have NULL tenant_id', nulls;
    END IF;
END$$;

-- ── 6. Add NOT NULL constraints (fast metadata change after full backfill) ────

ALTER TABLE them.agents
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.orchestrators
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.access_tokens
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.applications
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.runs
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.audit_logs
    ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE them.app_orchestrators
    ALTER COLUMN tenant_id SET NOT NULL;

-- ── 7. Replace global uniqueness constraints with tenant-scoped ones ──────────

-- agents.slug: drop both the table-level constraint and the explicit index
ALTER TABLE them.agents DROP CONSTRAINT IF EXISTS agents_slug_key;
DROP INDEX IF EXISTS them.idx_agents_slug;
CREATE UNIQUE INDEX IF NOT EXISTS uq_agents_tenant_slug
    ON them.agents(tenant_id, slug);

-- orchestrators.name: drop the table-level unique constraint
ALTER TABLE them.orchestrators DROP CONSTRAINT IF EXISTS orchestrators_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_orchestrators_tenant_name
    ON them.orchestrators(tenant_id, name);

-- app_orchestrators.name: drop the global constraint; replace with (tenant_id, name)
ALTER TABLE them.app_orchestrators DROP CONSTRAINT IF EXISTS app_orchestrators_name_key;
ALTER TABLE them.app_orchestrators DROP CONSTRAINT IF EXISTS uq_app_orchestrators_name;
DROP INDEX IF EXISTS them.app_orchestrators_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_app_orchestrators_tenant_name
    ON them.app_orchestrators(tenant_id, name);

-- ── 8. Tenant-scoped indexes for common query patterns ────────────────────────

CREATE INDEX IF NOT EXISTS idx_agents_tenant
    ON them.agents(tenant_id);

CREATE INDEX IF NOT EXISTS idx_orchestrators_tenant
    ON them.orchestrators(tenant_id);

CREATE INDEX IF NOT EXISTS idx_access_tokens_tenant
    ON them.access_tokens(tenant_id);

CREATE INDEX IF NOT EXISTS idx_applications_tenant
    ON them.applications(tenant_id);

CREATE INDEX IF NOT EXISTS idx_runs_tenant
    ON them.runs(tenant_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant
    ON them.audit_logs(tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_app_orchestrators_tenant
    ON them.app_orchestrators(tenant_id);

-- ── 9. run_artifacts table with tenant_id ────────────────────────────────────
-- Note: db/025_run_artifacts.sql was not applied to the live DB before R-4a.
-- This creates the table with tenant_id included from the start.
-- If 025 was already applied on a test instance, the IF NOT EXISTS guard handles it.

CREATE TABLE IF NOT EXISTS them.run_artifacts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID        NOT NULL,
    tenant_id       UUID        NOT NULL REFERENCES them.tenants(id) ON DELETE RESTRICT,
    application_id  UUID,
    session_id      UUID,
    filename        TEXT        NOT NULL,
    content_type    TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size            BIGINT      NOT NULL,
    data            BYTEA       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- If the table already existed without tenant_id (025 was applied earlier), add it.
ALTER TABLE them.run_artifacts
    ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES them.tenants(id) ON DELETE RESTRICT;

-- Backfill if somehow pre-existing rows exist without tenant_id.
UPDATE them.run_artifacts
    SET tenant_id = '00000000-0000-0000-0000-000000000001'
    WHERE tenant_id IS NULL;

-- Tighten to NOT NULL after backfill (safe; table is empty in live DB).
-- This is a no-op if column was created NOT NULL above.
DO $$
BEGIN
    -- Only attempt if the column is currently nullable.
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'them'
          AND table_name = 'run_artifacts'
          AND column_name = 'tenant_id'
          AND is_nullable = 'YES'
    ) THEN
        ALTER TABLE them.run_artifacts ALTER COLUMN tenant_id SET NOT NULL;
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_run_artifacts_run
    ON them.run_artifacts(run_id);

CREATE INDEX IF NOT EXISTS idx_run_artifacts_tenant
    ON them.run_artifacts(tenant_id);

CREATE INDEX IF NOT EXISTS idx_run_artifacts_app
    ON them.run_artifacts(application_id)
    WHERE application_id IS NOT NULL;

-- ── 10. Verify run_artifacts tenant ownership matches runs ────────────────────
-- (Only meaningful when data exists — safe to skip on empty tables.)
DO $$
DECLARE
    mismatched INTEGER;
BEGIN
    SELECT COUNT(*) INTO mismatched
    FROM them.run_artifacts ra
    JOIN them.runs r ON r.id = ra.run_id
    WHERE ra.tenant_id != r.tenant_id;

    IF mismatched > 0 THEN
        RAISE EXCEPTION 'R-4a validation failed: % run_artifacts have tenant_id mismatch with parent run', mismatched;
    END IF;
END$$;

-- ── 11. Record migration ──────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('026_tenant_foundation', 'R-4a: tenant foundation — tenants table, tenant_id columns, bootstrap tenant, constraint migration', now())
ON CONFLICT (version) DO NOTHING;

COMMIT;
