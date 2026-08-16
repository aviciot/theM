-- P-08: app_orchestrators uniqueness fix
-- Drop global UNIQUE(name); add UNIQUE(application_id, name).
-- Orchestrator names only need to be unique within one application.
-- Tenant ownership is derivable through application_id → applications.tenant_id.
--
-- Does NOT depend on db/026_tenant_foundation.sql.
-- Safe to apply against current live schema (latest applied: 025_events_transport).
-- Idempotent: all operations use IF EXISTS / IF NOT EXISTS guards.

BEGIN;

-- Step 1: Drop the global unique constraint on name.
-- Confirmed live constraint name: app_orchestrators_name_key
ALTER TABLE them.app_orchestrators
    DROP CONSTRAINT IF EXISTS app_orchestrators_name_key;

-- Step 2: Add per-application uniqueness on name.
-- Allows two different applications to use the same orchestrator name.
-- Uses IF NOT EXISTS so the migration is safe to re-run.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'them.app_orchestrators'::regclass
          AND conname   = 'uq_app_orchestrators_app_name'
    ) THEN
        ALTER TABLE them.app_orchestrators
            ADD CONSTRAINT uq_app_orchestrators_app_name
            UNIQUE (application_id, name);
    END IF;
END;
$$;

-- Step 3: Verify new constraint exists and old one is gone.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'them.app_orchestrators'::regclass
          AND conname   = 'app_orchestrators_name_key'
    ) THEN
        RAISE EXCEPTION 'P-08: global UNIQUE(name) constraint still present — migration incomplete';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'them.app_orchestrators'::regclass
          AND conname   = 'uq_app_orchestrators_app_name'
    ) THEN
        RAISE EXCEPTION 'P-08: UNIQUE(application_id, name) constraint not found — migration incomplete';
    END IF;
END;
$$;

-- Step 4: Record migration.
INSERT INTO them.schema_migrations (version) VALUES ('027_app_orchestrators_uniqueness')
    ON CONFLICT DO NOTHING;

COMMIT;
