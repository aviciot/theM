-- Migration 028: Make entry_points.slug unique per tenant.
--
-- Before: entry_points has a global UNIQUE(slug) constraint.
--         Two tenants cannot have entry points with the same slug.
-- After:  entry_points.tenant_id (FK to tenants) is added, backfilled from
--         applications.tenant_id, and UNIQUE(tenant_id, slug) replaces the
--         global constraint.
--
-- This is idempotent: each step is guarded with IF NOT EXISTS / IF EXISTS.
--
-- Migration 026 dependency: This migration does NOT depend on 026.
-- It depends only on: them.entry_points, them.applications (existing),
-- them.tenants (existing since Wave 6 tenant foundation).

BEGIN;

-- ── Step 1: Add tenant_id column as nullable ─────────────────────────────────
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'them'
          AND table_name   = 'entry_points'
          AND column_name  = 'tenant_id'
    ) THEN
        ALTER TABLE them.entry_points
            ADD COLUMN tenant_id uuid REFERENCES them.tenants(id);
    END IF;
END; $$;

-- ── Step 2: Backfill tenant_id from applications.tenant_id ──────────────────
UPDATE them.entry_points ep
   SET tenant_id = a.tenant_id
  FROM them.applications a
 WHERE ep.application_id = a.id
   AND ep.tenant_id IS NULL;

-- ── Step 3: Add NOT NULL constraint after backfill ───────────────────────────
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'them'
          AND table_name   = 'entry_points'
          AND column_name  = 'tenant_id'
          AND is_nullable  = 'YES'
    ) THEN
        ALTER TABLE them.entry_points
            ALTER COLUMN tenant_id SET NOT NULL;
    END IF;
END; $$;

-- ── Step 4: Set DEFAULT for new rows ─────────────────────────────────────────
-- New entry points are always created under an application whose tenant_id
-- must be propagated by the insert statement. The DEFAULT is a safety net
-- for the bootstrap tenant only; production inserts must set tenant_id
-- explicitly from the application row.
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema     = 'them'
          AND table_name       = 'entry_points'
          AND column_name      = 'tenant_id'
          AND column_default IS NOT NULL
    ) THEN
        ALTER TABLE them.entry_points
            ALTER COLUMN tenant_id
                SET DEFAULT '00000000-0000-0000-0000-000000000001'::uuid;
    END IF;
END; $$;

-- ── Step 5: Drop the old global UNIQUE(slug) constraint ──────────────────────
ALTER TABLE them.entry_points
    DROP CONSTRAINT IF EXISTS entry_points_slug_key;

-- ── Step 6: Add UNIQUE(tenant_id, slug) ──────────────────────────────────────
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'them.entry_points'::regclass
           AND conname  = 'uq_entry_points_tenant_slug'
    ) THEN
        ALTER TABLE them.entry_points
            ADD CONSTRAINT uq_entry_points_tenant_slug
                UNIQUE (tenant_id, slug);
    END IF;
END; $$;

-- ── Step 7: Add index on tenant_id for hot-path queries ──────────────────────
CREATE INDEX IF NOT EXISTS idx_entry_points_tenant_id
    ON them.entry_points (tenant_id);

-- ── Step 8: Verify ───────────────────────────────────────────────────────────
DO $$ DECLARE
    col_count   int;
    nn_count    int;
    constr_count int;
BEGIN
    SELECT COUNT(*) INTO col_count
      FROM information_schema.columns
     WHERE table_schema = 'them'
       AND table_name   = 'entry_points'
       AND column_name  = 'tenant_id';
    ASSERT col_count = 1, 'tenant_id column must exist';

    SELECT COUNT(*) INTO nn_count
      FROM information_schema.columns
     WHERE table_schema = 'them'
       AND table_name   = 'entry_points'
       AND column_name  = 'tenant_id'
       AND is_nullable  = 'NO';
    ASSERT nn_count = 1, 'tenant_id must be NOT NULL';

    SELECT COUNT(*) INTO constr_count
      FROM pg_constraint
     WHERE conrelid = 'them.entry_points'::regclass
       AND conname  = 'uq_entry_points_tenant_slug';
    ASSERT constr_count = 1, 'uq_entry_points_tenant_slug constraint must exist';

    RAISE NOTICE '028: entry_points.tenant_id added, backfilled, NOT NULL, UNIQUE(tenant_id, slug) in place';
END; $$;

-- ── Step 9: Record migration ──────────────────────────────────────────────────
INSERT INTO them.schema_migrations (version, applied_at)
VALUES ('028', NOW())
ON CONFLICT (version) DO NOTHING;

COMMIT;
