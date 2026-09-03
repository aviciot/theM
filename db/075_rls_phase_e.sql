-- db/075_rls_phase_e.sql
-- Phase E2: enable Row-Level Security on run/task tables.
--
-- Prerequisites:
--   - db/074_tasks_tenant_backfill.sql applied: tasks.tenant_id is NOT NULL.
--   - db/073_rls_phase_d.sql applied: Phase D complete.
--   - them_app has SELECT/INSERT/UPDATE/DELETE grants on each table (070_rls_roles.sql).
--   - them_admin has BYPASSRLS — policies never apply to it.
--   - Go callers (runrecorder, reconciler) now run via Admin pool (E1).
--
-- Pattern: direct tenant_id column on all three tables (§5.4).
-- FORCE ROW LEVEL SECURITY overrides the table-owner implicit bypass.

-- ── them.runs ─────────────────────────────────────────────────────────────────

ALTER TABLE them.runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.runs FORCE ROW LEVEL SECURITY;

CREATE POLICY runs_tenant_isolation ON them.runs
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.tasks ────────────────────────────────────────────────────────────────

ALTER TABLE them.tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tasks FORCE ROW LEVEL SECURITY;

CREATE POLICY tasks_tenant_isolation ON them.tasks
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.run_artifacts ────────────────────────────────────────────────────────

ALTER TABLE them.run_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.run_artifacts FORCE ROW LEVEL SECURITY;

CREATE POLICY run_artifacts_tenant_isolation ON them.run_artifacts
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('075_rls_phase_e', 'RLS E2: enable RLS on runs/tasks/run_artifacts', NOW())
ON CONFLICT (version) DO NOTHING;
