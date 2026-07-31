-- R-4a Validation Script
-- Run after applying 026_tenant_foundation.sql to verify correctness.
-- All queries should return expected values. Any FAIL indicates a problem.
--
-- Apply: docker exec them-postgres psql -U them -d them -f /tmp/validate_r4a.sql

\echo '=== R-4a Tenant Foundation Validation ==='

-- 1. Tenants table exists and bootstrap tenant is present
\echo ''
\echo '-- 1. Bootstrap tenant'
SELECT
    id,
    slug,
    display_name,
    is_bootstrap,
    CASE WHEN id = '00000000-0000-0000-0000-000000000001'
         THEN 'PASS: correct UUID'
         ELSE 'FAIL: wrong UUID'
    END AS uuid_check
FROM them.tenants
WHERE slug = 'default';

-- 2. No NULL tenant_id on any tenant-owned table
\echo ''
\echo '-- 2. NULL tenant_id checks (all should be 0)'
SELECT 'agents'           AS tbl, COUNT(*) AS null_count,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result
FROM them.agents WHERE tenant_id IS NULL
UNION ALL
SELECT 'orchestrators',   COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.orchestrators WHERE tenant_id IS NULL
UNION ALL
SELECT 'access_tokens',   COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.access_tokens WHERE tenant_id IS NULL
UNION ALL
SELECT 'applications',    COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.applications WHERE tenant_id IS NULL
UNION ALL
SELECT 'runs',            COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.runs WHERE tenant_id IS NULL
UNION ALL
SELECT 'audit_logs',      COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.audit_logs WHERE tenant_id IS NULL
UNION ALL
SELECT 'app_orchestrators', COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.app_orchestrators WHERE tenant_id IS NULL
UNION ALL
SELECT 'run_artifacts',   COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.run_artifacts WHERE tenant_id IS NULL;

-- 3. Orphan check: tenant_id must reference a valid tenant
\echo ''
\echo '-- 3. Orphan FK checks (all should be 0)'
SELECT 'agents orphans' AS check_name, COUNT(*) AS count,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS result
FROM them.agents a
WHERE NOT EXISTS (SELECT 1 FROM them.tenants t WHERE t.id = a.tenant_id)
UNION ALL
SELECT 'orchestrators orphans', COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.orchestrators o
WHERE NOT EXISTS (SELECT 1 FROM them.tenants t WHERE t.id = o.tenant_id)
UNION ALL
SELECT 'run_artifacts/runs mismatch', COUNT(*),
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM them.run_artifacts ra
JOIN them.runs r ON r.id = ra.run_id
WHERE ra.tenant_id != r.tenant_id;

-- 4. Tenant-scoped uniqueness: same slug allowed across tenants
\echo ''
\echo '-- 4. Cross-tenant uniqueness: same slug allowed in different tenants'
DO $$
DECLARE
    t1 UUID := gen_random_uuid();
    t2 UUID := gen_random_uuid();
    test_slug TEXT := 'r4a_test_slug_' || floor(random()*1000000)::text;
    ok BOOLEAN := false;
BEGIN
    -- Create two temporary tenants
    INSERT INTO them.tenants (id, slug, display_name) VALUES (t1, 'r4a-test-tenant-1-' || floor(random()*1000000)::text, 'Test Tenant 1');
    INSERT INTO them.tenants (id, slug, display_name) VALUES (t2, 'r4a-test-tenant-2-' || floor(random()*1000000)::text, 'Test Tenant 2');

    -- Insert same slug in both tenants
    INSERT INTO them.agents (tenant_id, slug, display_name, description, transport, endpoint_url)
    VALUES (t1, test_slug, 'Test Agent', 'Test', 'a2a_async', 'http://test1');

    INSERT INTO them.agents (tenant_id, slug, display_name, description, transport, endpoint_url)
    VALUES (t2, test_slug, 'Test Agent', 'Test', 'a2a_async', 'http://test2');

    ok := true;
    RAISE NOTICE 'PASS: same slug allowed across two tenants';

    -- Clean up
    DELETE FROM them.agents WHERE tenant_id IN (t1, t2);
    DELETE FROM them.tenants WHERE id IN (t1, t2);
EXCEPTION
    WHEN unique_violation THEN
        RAISE NOTICE 'FAIL: cross-tenant same slug was rejected (should be allowed)';
        DELETE FROM them.agents WHERE tenant_id IN (t1, t2);
        DELETE FROM them.tenants WHERE id IN (t1, t2);
END$$;

-- 5. Intra-tenant uniqueness: same slug rejected within one tenant
\echo ''
\echo '-- 5. Intra-tenant uniqueness: same slug rejected in same tenant'
DO $$
DECLARE
    t1 UUID := gen_random_uuid();
    test_slug TEXT := 'r4a_dup_test_' || floor(random()*1000000)::text;
BEGIN
    INSERT INTO them.tenants (id, slug, display_name) VALUES (t1, 'r4a-dup-tenant-' || floor(random()*1000000)::text, 'Dup Test Tenant');

    INSERT INTO them.agents (tenant_id, slug, display_name, description, transport, endpoint_url)
    VALUES (t1, test_slug, 'Test Agent', 'Test', 'a2a_async', 'http://dup1');

    -- This should raise unique_violation
    INSERT INTO them.agents (tenant_id, slug, display_name, description, transport, endpoint_url)
    VALUES (t1, test_slug, 'Test Agent Dup', 'Test', 'a2a_async', 'http://dup2');

    -- If we get here, the constraint didn't fire — FAIL
    RAISE NOTICE 'FAIL: duplicate slug in same tenant was not rejected';
    DELETE FROM them.agents WHERE tenant_id = t1;
    DELETE FROM them.tenants WHERE id = t1;

EXCEPTION
    WHEN unique_violation THEN
        RAISE NOTICE 'PASS: duplicate slug in same tenant was correctly rejected';
        DELETE FROM them.agents WHERE tenant_id = t1;
        DELETE FROM them.tenants WHERE id = t1;
END$$;

-- 6. run_artifacts table has tenant_id column NOT NULL
\echo ''
\echo '-- 6. run_artifacts schema check'
SELECT column_name, data_type, is_nullable,
       CASE WHEN is_nullable = 'NO' THEN 'PASS' ELSE 'FAIL: should be NOT NULL' END AS result
FROM information_schema.columns
WHERE table_schema = 'them'
  AND table_name = 'run_artifacts'
  AND column_name = 'tenant_id';

-- 7. New tenant-scoped indexes exist
\echo ''
\echo '-- 7. Required indexes exist'
SELECT indexname,
       CASE WHEN indexname IS NOT NULL THEN 'PASS' ELSE 'FAIL' END AS result
FROM pg_indexes
WHERE schemaname = 'them'
  AND indexname IN (
    'uq_agents_tenant_slug',
    'uq_orchestrators_tenant_name',
    'uq_app_orchestrators_tenant_name',
    'idx_agents_tenant',
    'idx_orchestrators_tenant',
    'idx_applications_tenant',
    'idx_runs_tenant',
    'idx_audit_logs_tenant',
    'idx_run_artifacts_tenant'
  )
ORDER BY indexname;

-- 8. Old global uniqueness indexes are gone
\echo ''
\echo '-- 8. Old global unique constraints removed (all should return 0 rows)'
SELECT indexname, 'FAIL: old global constraint still exists' AS result
FROM pg_indexes
WHERE schemaname = 'them'
  AND indexname IN (
    'agents_slug_key',
    'idx_agents_slug',
    'orchestrators_name_key',
    'app_orchestrators_name_key'
  )
UNION ALL
SELECT 'all old constraints removed' AS indexname,
       CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'FAIL' END
FROM pg_indexes
WHERE schemaname = 'them'
  AND indexname IN (
    'agents_slug_key',
    'idx_agents_slug',
    'orchestrators_name_key',
    'app_orchestrators_name_key'
  );

-- 9. Migration recorded
\echo ''
\echo '-- 9. Migration recorded'
SELECT version, description,
       CASE WHEN version IS NOT NULL THEN 'PASS' ELSE 'FAIL' END AS result
FROM them.schema_migrations
WHERE version = '026_tenant_foundation';

\echo ''
\echo '=== Validation complete ==='
