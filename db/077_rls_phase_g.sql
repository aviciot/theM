-- db/077_rls_phase_g.sql
-- Phase G2: enable Row-Level Security on llm_providers, middleware_jobs, audit_logs.
--
-- Prerequisites:
--   - db/076_rls_phase_f.sql applied: Phase F complete.
--   - them_app grants in 070_rls_roles.sql: SELECT on llm_providers, INSERT on
--     audit_logs, INSERT on middleware_jobs.
--   - them_admin has BYPASSRLS — policies never apply to it.
--   - Go callers now on Admin pool (G1): middleware-worker activePool,
--     cmd/them fileGate → Admin pool.
--
-- Special policies:
--   llm_providers: split SELECT — own tenant rows OR platform defaults
--                  (tenant_id IS NULL). Write restricted to own rows.
--   middleware_jobs: EXISTS via applications.tenant_id (no direct tenant_id).
--   audit_logs: INSERT-only for them_app. Platform rows (tenant_id IS NULL)
--               are invisible to tenant queries (no match) — intentional.

-- ── them.llm_providers ────────────────────────────────────────────────────────
-- Split policy: SELECT allows platform defaults (tenant_id IS NULL) AND
-- the tenant's own overrides. INSERT/UPDATE/DELETE restricted to own rows only.

ALTER TABLE them.llm_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.llm_providers FORCE ROW LEVEL SECURITY;

-- Read: own rows + platform defaults (NULL tenant_id = shared across all tenants).
CREATE POLICY llm_providers_read ON them.llm_providers
    AS PERMISSIVE
    FOR SELECT
    TO them_app
    USING (
        tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        OR tenant_id IS NULL
    );

-- Write: own rows only — never modify platform defaults.
CREATE POLICY llm_providers_write ON them.llm_providers
    AS PERMISSIVE
    FOR ALL
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.middleware_jobs ──────────────────────────────────────────────────────
-- No direct tenant_id; scope through applications.

ALTER TABLE them.middleware_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.middleware_jobs FORCE ROW LEVEL SECURITY;

CREATE POLICY middleware_jobs_tenant_isolation ON them.middleware_jobs
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_jobs.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_jobs.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.audit_logs ───────────────────────────────────────────────────────────
-- them_app has INSERT only (no SELECT). The policy restricts INSERT to own
-- tenant rows. Platform rows (tenant_id IS NULL) written by admin paths only.

ALTER TABLE them.audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.audit_logs FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_logs_tenant_isolation ON them.audit_logs
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── Record migration ──────────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('077_rls_phase_g', 'RLS G2: enable RLS on llm_providers/middleware_jobs/audit_logs', NOW())
ON CONFLICT (version) DO NOTHING;
