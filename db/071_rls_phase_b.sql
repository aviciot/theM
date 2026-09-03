-- db/071_rls_phase_b.sql
-- Phase B: enable Row-Level Security on the first wave of tenant-scoped tables.
--
-- Prerequisites (applied by db/070_rls_roles.sql):
--   - them_owner owns each table (NOLOGIN, NOBYPASSRLS — subject to FORCE RLS)
--   - them_app has SELECT/INSERT/UPDATE/DELETE grants on each table
--   - them_admin has BYPASSRLS — policies never apply to it
--
-- Pattern for all four tables: standard direct tenant_id isolation (§5.4 of
-- docs/design/rls-option-a-plan.md).
--
-- NULLIF(..., '') guards against an unset GUC returning '' instead of NULL.
-- FORCE ROW LEVEL SECURITY overrides the table-owner implicit bypass.

-- ── them.mcp_servers ─────────────────────────────────────────────────────────

ALTER TABLE them.mcp_servers ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.mcp_servers FORCE ROW LEVEL SECURITY;

CREATE POLICY mcp_servers_tenant_isolation ON them.mcp_servers
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.tenant_group_mappings ────────────────────────────────────────────────

ALTER TABLE them.tenant_group_mappings ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.tenant_group_mappings FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_group_mappings_tenant_isolation ON them.tenant_group_mappings
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.agent_definitions ────────────────────────────────────────────────────

ALTER TABLE them.agent_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.agent_definitions FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_definitions_tenant_isolation ON them.agent_definitions
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── them.agent_runtime_specs ──────────────────────────────────────────────────

ALTER TABLE them.agent_runtime_specs ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.agent_runtime_specs FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_runtime_specs_tenant_isolation ON them.agent_runtime_specs
    AS PERMISSIVE
    TO them_app
    USING      (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
