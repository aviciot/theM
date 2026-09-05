-- Migration 080: Grant SELECT on tenant_quotas to them_app
--
-- Problem: them_app (used by TenantTx / App pool) could not SELECT from
-- them.tenant_quotas, causing a "permission denied" DB error inside
-- checkResourceQuota during agent/app/mcp-server Create operations.
-- PostgreSQL aborts the transaction on permission errors, so the subsequent
-- INSERT (CreateAgent, CreateApplication, CreateMCPServer) failed with
-- SQLSTATE 25P02 "transaction is aborted". checkResourceQuota is fail-open
-- on error, so the quota permission failure was silently swallowed, but the
-- aborted transaction prevented the actual insert.
--
-- Fix: grant SELECT on tenant_quotas to them_app. Writes to tenant_quotas
-- remain admin-only (them_admin, BYPASSRLS).

BEGIN;
GRANT SELECT ON them.tenant_quotas TO them_app;
COMMIT;
