-- Migration 079: Restore INSERT and DELETE on them.component_definitions to them_app.
--
-- Migration 078 revoked these privileges under the assumption that all
-- component_definitions writes would go through them_admin.  However the
-- runtime agent Create and Delete paths execute inside a TenantTx (them_app
-- role) and write component_definitions as part of an atomic CTE / explicit
-- DELETE.  The write RLS policy on component_definitions already enforces
-- tenant isolation via WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid),
-- so granting DML back to them_app is safe — the policy provides the guard.
--
-- UPDATE remains revoked; component_definitions rows are immutable after
-- insert (agents.go never issues UPDATE on component_definitions).

GRANT INSERT, DELETE ON them.component_definitions TO them_app;
