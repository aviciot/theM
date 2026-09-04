-- Migration 078: RLS Phase H2 — enable RLS on four remaining tables
-- Tables: application_definitions, managed_app_bindings, quarantine_artifacts,
--         component_definitions (split policy: read own+NULL, write own only)
--
-- application_definitions/managed_app_bindings/quarantine_artifacts have direct tenant_id.
-- component_definitions has optional tenant_id (NULL = platform global); tenants may
-- read their own rows + NULL rows, but may only write their own rows.
-- them_app loses INSERT/UPDATE/DELETE on component_definitions — admin writes use them_admin.
--
-- Policy pattern: NULLIF(current_setting('app.tenant_id', true), '')::uuid
-- (matches what BeginTenantTx sets via set_config('app.tenant_id', ...)).

BEGIN;

-- drop any incorrect policies from a prior partial run
DROP POLICY IF EXISTS application_definitions_tenant_isolation ON them.application_definitions;
DROP POLICY IF EXISTS managed_app_bindings_tenant_isolation ON them.managed_app_bindings;
DROP POLICY IF EXISTS quarantine_artifacts_tenant_isolation ON them.quarantine_artifacts;

-- ── application_definitions ──────────────────────────────────────────────────
ALTER TABLE them.application_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.application_definitions FORCE ROW LEVEL SECURITY;

CREATE POLICY application_definitions_tenant_isolation
    ON them.application_definitions
    AS PERMISSIVE
    FOR ALL
    TO them_app
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── managed_app_bindings ──────────────────────────────────────────────────────
ALTER TABLE them.managed_app_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.managed_app_bindings FORCE ROW LEVEL SECURITY;

CREATE POLICY managed_app_bindings_tenant_isolation
    ON them.managed_app_bindings
    AS PERMISSIVE
    FOR ALL
    TO them_app
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── quarantine_artifacts ──────────────────────────────────────────────────────
ALTER TABLE them.quarantine_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.quarantine_artifacts FORCE ROW LEVEL SECURITY;

CREATE POLICY quarantine_artifacts_tenant_isolation
    ON them.quarantine_artifacts
    AS PERMISSIVE
    FOR ALL
    TO them_app
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ── component_definitions ────────────────────────────────────────────────────
-- Split policy: tenants may SELECT their own rows plus NULL (platform globals),
-- but may only INSERT/UPDATE/DELETE their own rows.
-- them_app loses DML rights — all component_definitions writes go through them_admin.
ALTER TABLE them.component_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE them.component_definitions FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS component_definitions_read  ON them.component_definitions;
DROP POLICY IF EXISTS component_definitions_write ON them.component_definitions;

-- SELECT: own tenant rows + NULL (platform catalog visible to all tenants)
CREATE POLICY component_definitions_read
    ON them.component_definitions
    AS PERMISSIVE
    FOR SELECT
    TO them_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           OR tenant_id IS NULL);

-- ALL (INSERT/UPDATE/DELETE): own tenant rows only — WITH CHECK prevents cross-tenant writes
CREATE POLICY component_definitions_write
    ON them.component_definitions
    AS PERMISSIVE
    FOR ALL
    TO them_app
    USING  (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- Revoke DML from them_app — all writes go through them_admin (BYPASSRLS)
REVOKE INSERT, UPDATE, DELETE ON them.component_definitions FROM them_app;

COMMIT;
