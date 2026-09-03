-- db/074_tasks_tenant_backfill.sql
-- Step 19 Phase E0 — backfill tasks.tenant_id before enabling RLS.
--
-- Context: 27 tasks have tenant_id IS NULL. All have run_id IS NULL (standalone
-- pre-multi-tenancy tasks, never linked to a run). Three have an orchestrator_id
-- pointing to the default tenant; the remaining 24 have no resolvable owner.
-- All are assigned to the bootstrap/default tenant.
--
-- After this migration tasks.tenant_id is changed to NOT NULL.
--
-- Run as superuser (them) after db/073_rls_phase_d.sql is applied.

-- ── 1. Backfill via orchestrators (3 rows with a resolvable orchestrator) ───

UPDATE them.tasks t
SET tenant_id = o.tenant_id
FROM them.orchestrators o
WHERE t.orchestrator_id = o.id
  AND t.tenant_id IS NULL
  AND o.tenant_id IS NOT NULL;

-- ── 2. Assign remaining NULL rows to the bootstrap/default tenant ────────────
-- These are pre-multi-tenancy standalone tasks (run_id IS NULL, no orchestrator,
-- no agent linkage). The bootstrap tenant owns all legacy data.

UPDATE them.tasks
SET tenant_id = '00000000-0000-0000-0000-000000000001'::uuid
WHERE tenant_id IS NULL;

-- ── 3. Verify zero NULL rows remain before adding the constraint ─────────────

DO $$
DECLARE
  null_count integer;
BEGIN
  SELECT count(*) INTO null_count FROM them.tasks WHERE tenant_id IS NULL;
  IF null_count > 0 THEN
    RAISE EXCEPTION 'E0 backfill incomplete: % rows still have NULL tenant_id', null_count;
  END IF;
END $$;

-- ── 4. Make the column NOT NULL ───────────────────────────────────────────────

ALTER TABLE them.tasks ALTER COLUMN tenant_id SET NOT NULL;

-- ── 5. Record migration ───────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('074_tasks_tenant_backfill', 'RLS E0: backfill tasks.tenant_id NOT NULL', NOW())
ON CONFLICT (version) DO NOTHING;
