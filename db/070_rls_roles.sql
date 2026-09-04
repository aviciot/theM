-- db/070_rls_roles.sql
-- Step 19 Phase A — RLS infrastructure: roles, ownership transfer, grants
--
-- Run as the superuser (them role) AFTER all schema migrations have been applied.
--
-- After running this file, set passwords for the new roles using values
-- from your generated .env (THEM_DB_URL_APP / THEM_DB_URL_ADMIN):
--
--   source .env
--   docker exec them-postgres psql -U them -d them -c \
--     "ALTER ROLE them_admin PASSWORD '${THEM_DB_ADMIN_PASSWORD}';"
--   docker exec them-postgres psql -U them -d them -c \
--     "ALTER ROLE them_app  PASSWORD '${THEM_DB_APP_PASSWORD}';"
--
-- Verification (run after applying + setting passwords):
--   SELECT rolname, rolbypassrls, rolcanlogin FROM pg_roles
--     WHERE rolname IN ('them_owner','them_admin','them_app');
--   -- Expected:
--   --   them_owner | f | f
--   --   them_admin | t | t
--   --   them_app   | f | t

-- ── 1. Roles ──────────────────────────────────────────────────────────────────

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'them_owner') THEN
    CREATE ROLE them_owner NOLOGIN NOBYPASSRLS NOCREATEDB NOCREATEROLE;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'them_admin') THEN
    CREATE ROLE them_admin LOGIN BYPASSRLS NOCREATEDB NOCREATEROLE NOSUPERUSER;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'them_app') THEN
    -- NO BYPASSRLS — enforces RLS policies on all tenant-scoped tables
    CREATE ROLE them_app LOGIN NOCREATEDB NOCREATEROLE NOSUPERUSER;
  END IF;
END $$;

-- ── 2. Schema access ──────────────────────────────────────────────────────────

GRANT USAGE ON SCHEMA them        TO them_app;
GRANT USAGE ON SCHEMA them        TO them_admin;
-- them_owner needs USAGE on the schema so FK constraint checks (which run as the
-- table owner) can resolve references across tables during INSERT/UPDATE/DELETE.
GRANT USAGE ON SCHEMA them        TO them_owner;
GRANT USAGE ON SCHEMA auth_service TO them_admin;

-- ── 3. Transfer table ownership to them_owner ─────────────────────────────────
-- them_owner is NOLOGIN so it can never be used as an application DSN.
-- FORCE ROW LEVEL SECURITY (applied per-table in later phases) overrides the
-- table-owner implicit bypass, but NOT the BYPASSRLS role attribute.

ALTER TABLE IF EXISTS them.access_tokens          OWNER TO them_owner;
ALTER TABLE IF EXISTS them.agent_definitions      OWNER TO them_owner;
ALTER TABLE IF EXISTS them.agent_runtime_specs    OWNER TO them_owner;
ALTER TABLE IF EXISTS them.agents                 OWNER TO them_owner;
ALTER TABLE IF EXISTS them.app_agent_bindings     OWNER TO them_owner;
ALTER TABLE IF EXISTS them.app_mcp_credentials    OWNER TO them_owner;
ALTER TABLE IF EXISTS them.app_orchestrators      OWNER TO them_owner;
ALTER TABLE IF EXISTS them.application_definitions OWNER TO them_owner;
ALTER TABLE IF EXISTS them.applications           OWNER TO them_owner;
ALTER TABLE IF EXISTS them.artifacts              OWNER TO them_owner;
ALTER TABLE IF EXISTS them.audit_logs             OWNER TO them_owner;
ALTER TABLE IF EXISTS them.component_definitions  OWNER TO them_owner;
ALTER TABLE IF EXISTS them.config                 OWNER TO them_owner;
ALTER TABLE IF EXISTS them.entry_points           OWNER TO them_owner;
ALTER TABLE IF EXISTS them.llm_providers          OWNER TO them_owner;
ALTER TABLE IF EXISTS them.managed_app_bindings   OWNER TO them_owner;
ALTER TABLE IF EXISTS them.managed_app_params     OWNER TO them_owner;
ALTER TABLE IF EXISTS them.mcp_servers            OWNER TO them_owner;
ALTER TABLE IF EXISTS them.middleware_audit       OWNER TO them_owner;
ALTER TABLE IF EXISTS them.middleware_defs        OWNER TO them_owner;
ALTER TABLE IF EXISTS them.middleware_jobs        OWNER TO them_owner;
ALTER TABLE IF EXISTS them.middleware_wirings     OWNER TO them_owner;
ALTER TABLE IF EXISTS them.orchestrators          OWNER TO them_owner;
ALTER TABLE IF EXISTS them.quarantine_artifacts   OWNER TO them_owner;
ALTER TABLE IF EXISTS them.run_artifacts          OWNER TO them_owner;
ALTER TABLE IF EXISTS them.run_steps              OWNER TO them_owner;
ALTER TABLE IF EXISTS them.run_usage              OWNER TO them_owner;
ALTER TABLE IF EXISTS them.runs                   OWNER TO them_owner;
ALTER TABLE IF EXISTS them.schema_migrations      OWNER TO them_owner;
ALTER TABLE IF EXISTS them.task_messages          OWNER TO them_owner;
ALTER TABLE IF EXISTS them.tasks                  OWNER TO them_owner;
ALTER TABLE IF EXISTS them.tenants                OWNER TO them_owner;
-- Applied by migration 056 (may not exist yet):
ALTER TABLE IF EXISTS them.tenant_quotas          OWNER TO them_owner;
-- Applied by migration 059 (may not exist yet):
ALTER TABLE IF EXISTS them.tenant_group_mappings  OWNER TO them_owner;

-- Sequences
ALTER SEQUENCE IF EXISTS them.audit_logs_id_seq    OWNER TO them_owner;
ALTER SEQUENCE IF EXISTS them.llm_providers_id_seq OWNER TO them_owner;
ALTER SEQUENCE IF EXISTS them.run_usage_id_seq     OWNER TO them_owner;
ALTER SEQUENCE IF EXISTS them.task_messages_id_seq OWNER TO them_owner;

-- ── 4. Grants for them_app ────────────────────────────────────────────────────
-- Minimal privileges only. RLS policies (added in phases B–H) further restrict
-- row visibility to the current tenant.

-- Full tenant-scoped DML:
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agents                 TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.orchestrators          TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.applications           TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.entry_points           TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.access_tokens          TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.runs                   TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_steps              TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_usage              TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.run_artifacts          TO them_app;
GRANT SELECT, INSERT, UPDATE         ON them.tasks                  TO them_app; -- no DELETE
GRANT SELECT, INSERT, UPDATE, DELETE ON them.task_messages          TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.artifacts              TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.mcp_servers            TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_definitions      TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.agent_runtime_specs    TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.application_definitions TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.managed_app_bindings   TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.tenant_group_mappings  TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.component_definitions  TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_agent_bindings     TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_orchestrators      TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.app_mcp_credentials    TO them_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON them.middleware_wirings      TO them_app;

-- Restricted operations:
GRANT SELECT, INSERT ON them.quarantine_artifacts TO them_app; -- no UPDATE/DELETE
GRANT INSERT         ON them.audit_logs           TO them_app; -- write-only
GRANT INSERT         ON them.middleware_jobs      TO them_app; -- gateway enqueue only
GRANT INSERT         ON them.middleware_audit     TO them_app; -- write-only (processor outcomes)

-- Read-only reference:
GRANT SELECT ON them.llm_providers  TO them_app; -- split RLS policy added in phase G
GRANT SELECT ON them.middleware_defs TO them_app; -- builtins only; no RLS policy needed

-- them_app has NO access to:
--   them.tenants         (resolved at login via admin path)
--   them.tenant_quotas   (admin-only)
--   them.config          (platform-global)
--   them.schema_migrations (internal)
--   them.managed_app_params (platform-global catalog)
-- (no GRANT for these tables — intentional)

-- Sequences:
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO them_app;

-- ── 5. Grants for them_admin ──────────────────────────────────────────────────
-- them_admin has BYPASSRLS — RLS policies do not apply to this role.
-- Used for admin/cross-tenant ops only.

GRANT ALL ON ALL TABLES    IN SCHEMA them         TO them_admin;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA them TO them_admin;
GRANT ALL ON ALL TABLES    IN SCHEMA auth_service TO them_admin;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth_service TO them_admin;

-- ── 6. Record migration ───────────────────────────────────────────────────────

INSERT INTO them.schema_migrations (version, description, applied_at)
VALUES ('070_rls_roles', 'RLS: roles (them_owner/them_admin/them_app), ownership transfer, grants', NOW())
ON CONFLICT (version) DO NOTHING;
