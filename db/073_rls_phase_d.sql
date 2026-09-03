-- db/073_rls_phase_d.sql
-- Phase D: enable Row-Level Security on the child tables of applications.
--
-- Prerequisites:
--   - db/072_rls_phase_c.sql applied: applications table already has RLS enabled.
--   - them_app has SELECT/INSERT/UPDATE/DELETE grants on each table (070_rls_roles.sql).
--   - them_admin has BYPASSRLS — policies never apply to it.
--   - Go callers that use explicit tenant_id predicates now run via Admin pool
--     (agent-runtime, agentregistry).
--
-- Pattern: EXISTS-based policy through the parent applications table (§5.7 of
-- docs/design/rls-option-a-plan.md). No direct tenant_id column on these tables.
--
-- FORCE ROW LEVEL SECURITY overrides the table-owner implicit bypass.

-- ── them.app_agent_bindings ───────────────────────────────────────────────────

ALTER TABLE them.app_agent_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.app_agent_bindings FORCE ROW LEVEL SECURITY;

CREATE POLICY app_agent_bindings_tenant_isolation ON them.app_agent_bindings
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_agent_bindings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_agent_bindings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.app_orchestrators ────────────────────────────────────────────────────

ALTER TABLE them.app_orchestrators ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.app_orchestrators FORCE ROW LEVEL SECURITY;

CREATE POLICY app_orchestrators_tenant_isolation ON them.app_orchestrators
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_orchestrators.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_orchestrators.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.app_mcp_credentials ──────────────────────────────────────────────────

ALTER TABLE them.app_mcp_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.app_mcp_credentials FORCE ROW LEVEL SECURITY;

CREATE POLICY app_mcp_credentials_tenant_isolation ON them.app_mcp_credentials
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_mcp_credentials.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = app_mcp_credentials.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));

-- ── them.middleware_wirings ───────────────────────────────────────────────────

ALTER TABLE them.middleware_wirings ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.middleware_wirings FORCE ROW LEVEL SECURITY;

CREATE POLICY middleware_wirings_tenant_isolation ON them.middleware_wirings
    AS PERMISSIVE
    TO them_app
    USING (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_wirings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM them.applications a
        WHERE a.id = middleware_wirings.application_id
          AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    ));
