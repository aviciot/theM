-- db/076_rls_phase_f.sql
-- Phase F2: enable Row-Level Security on child run/task tables.
--
-- Prerequisites:
--   - db/075_rls_phase_e.sql applied: runs, tasks, run_artifacts all have RLS.
--   - them_app has SELECT/INSERT/UPDATE/DELETE grants on each table (070_rls_roles.sql).
--   - them_admin has BYPASSRLS — policies never apply to it.
--   - Go callers (runrecorder, history store, middleware job DAL) now run via
--     Admin pool (E1) — no GUC needed for their writes.
--
-- Pattern: EXISTS-based policies through the parent table (§5.7).
-- FORCE ROW LEVEL SECURITY overrides the table-owner implicit bypass.
--
-- Policy paths:
--   run_steps       → EXISTS runs       WHERE runs.tenant_id = GUC
--   run_usage       → EXISTS runs       WHERE runs.tenant_id = GUC
--   artifacts       → EXISTS tasks      WHERE tasks.tenant_id = GUC
--   task_messages   → EXISTS tasks      WHERE tasks.tenant_id = GUC
--   middleware_audit → EXISTS applications WHERE applications.tenant_id = GUC

-- ── them.run_steps ────────────────────────────────────────────────────────────

ALTER TABLE them.run_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.run_steps FORCE ROW LEVEL SECURITY;

CREATE POLICY run_steps_tenant_isolation ON them.run_steps
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_steps.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_steps.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.run_usage ────────────────────────────────────────────────────────────

ALTER TABLE them.run_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.run_usage FORCE ROW LEVEL SECURITY;

CREATE POLICY run_usage_tenant_isolation ON them.run_usage
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_usage.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.runs r
        WHERE r.id = run_usage.run_id
          AND r.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.artifacts ────────────────────────────────────────────────────────────
-- A2A protocol artifact store. No current Go callers write to this table, but
-- the policy is installed now so future A2A callers are automatically protected.

ALTER TABLE them.artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.artifacts FORCE ROW LEVEL SECURITY;

CREATE POLICY artifacts_tenant_isolation ON them.artifacts
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = artifacts.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = artifacts.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.task_messages ────────────────────────────────────────────────────────

ALTER TABLE them.task_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.task_messages FORCE ROW LEVEL SECURITY;

CREATE POLICY task_messages_tenant_isolation ON them.task_messages
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = task_messages.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.tasks t
        WHERE t.id = task_messages.task_id
          AND t.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.middleware_audit ─────────────────────────────────────────────────────
-- application_id is NOT NULL on middleware_audit; use it as the EXISTS join key.

ALTER TABLE them.middleware_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.middleware_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY middleware_audit_tenant_isolation ON them.middleware_audit
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_audit.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_audit.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('076_rls_phase_f', 'RLS F2: enable RLS on run_steps/run_usage/artifacts/task_messages/middleware_audit', NOW())
ON CONFLICT (version) DO NOTHING;
