-- db/072_rls_phase_c.sql
-- Phase C: enable Row-Level Security on the core admin CRUD tables.
--
-- Prerequisites (applied by 070_rls_roles.sql + 071_rls_phase_b.sql):
--   - them_owner owns each table (NOLOGIN, NOBYPASSRLS — subject to FORCE RLS)
--   - them_app has SELECT/INSERT/UPDATE/DELETE grants on each table
--   - them_admin has BYPASSRLS — policies never apply to it
--
-- Pattern: standard direct tenant_id isolation (§5.4 of
-- docs/design/rls-option-a-plan.md).
--
-- NULLIF(..., '') guards against an unset GUC returning '' instead of NULL.
-- FORCE ROW LEVEL SECURITY overrides the table-owner implicit bypass.
--
-- Caller prerequisite: appliveness.listEnabledEPSlugs must use the Admin pool
-- (BYPASSRLS) before this migration is applied. That fix is in C1b
-- (internal/appliveness/liveness.go — Loop now accepts *db.Pools).

-- ── them.agents ───────────────────────────────────────────────────────────────

ALTER TABLE them.agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.agents FORCE ROW LEVEL SECURITY;

CREATE POLICY agents_tenant_isolation ON them.agents
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.orchestrators ────────────────────────────────────────────────────────

ALTER TABLE them.orchestrators ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.orchestrators FORCE ROW LEVEL SECURITY;

CREATE POLICY orchestrators_tenant_isolation ON them.orchestrators
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.applications ─────────────────────────────────────────────────────────

ALTER TABLE them.applications ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.applications FORCE ROW LEVEL SECURITY;

CREATE POLICY applications_tenant_isolation ON them.applications
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.entry_points ─────────────────────────────────────────────────────────

ALTER TABLE them.entry_points ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.entry_points FORCE ROW LEVEL SECURITY;

CREATE POLICY entry_points_tenant_isolation ON them.entry_points
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.access_tokens ────────────────────────────────────────────────────────

ALTER TABLE them.access_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.access_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY access_tokens_tenant_isolation ON them.access_tokens
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
