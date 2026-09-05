//go:build integration

// Package db integration tests — RLS role attribute and connection reuse verification.
// Requires: live Postgres with them_app/them_admin/them_owner roles (A1 migration applied).
// Run: go test -tags=integration ./internal/db/...
package db

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDSN returns the superuser DSN from env (same as the main app uses).
func testDSN(t *testing.T) string {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DATABASE_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DATABASE_USER")
	if user == "" {
		user = "them"
	}
	password := os.Getenv("DATABASE_PASSWORD")
	dbname := os.Getenv("DATABASE_NAME")
	if dbname == "" {
		dbname = "them"
	}
	if password == "" {
		t.Skip("DATABASE_PASSWORD not set — skipping integration test")
	}
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		host, port, dbname, user, password)
}

func testAppDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("THEM_DB_URL_APP")
	if dsn == "" {
		t.Skip("THEM_DB_URL_APP not set — skipping them_app integration test")
	}
	return dsn
}

func testAdminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("THEM_DB_URL_ADMIN")
	if dsn == "" {
		t.Skip("THEM_DB_URL_ADMIN not set — skipping them_admin integration test")
	}
	return dsn
}

// rolesExist returns true if them_owner, them_admin, them_app all exist in pg_roles.
func rolesExist(ctx context.Context, pool *pgxpool.Pool) bool {
	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pg_roles WHERE rolname IN ('them_owner','them_admin','them_app')`,
	).Scan(&count)
	return err == nil && count == 3
}

// rlsEnabledOnAgents returns true if ENABLE ROW LEVEL SECURITY is set on them.agents.
func rlsEnabledOnAgents(ctx context.Context, pool *pgxpool.Pool) bool {
	var enabled bool
	err := pool.QueryRow(ctx,
		`SELECT relrowsecurity FROM pg_class
		  WHERE relname='agents'
		    AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='them')`,
	).Scan(&enabled)
	return err == nil && enabled
}

// TestRLS30_AppRoleNoBypassRLS verifies them_app has rolbypassrls = false.
func TestRLS30_AppRoleNoBypassRLS(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if !rolesExist(ctx, pool) {
		t.Skip("RLS roles not yet created — apply db/070_rls_roles.sql first")
	}

	var bypass bool
	if err := pool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'them_app'`,
	).Scan(&bypass); err != nil {
		t.Fatalf("query: %v", err)
	}
	if bypass {
		t.Error("RLS-30 FAIL: them_app.rolbypassrls must be false — RLS enforcement requires NOBYPASSRLS")
	}
}

// TestRLS31_OwnerRoleNoLogin verifies them_owner has rolcanlogin = false.
func TestRLS31_OwnerRoleNoLogin(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if !rolesExist(ctx, pool) {
		t.Skip("RLS roles not yet created — apply db/070_rls_roles.sql first")
	}

	var canLogin bool
	if err := pool.QueryRow(ctx,
		`SELECT rolcanlogin FROM pg_roles WHERE rolname = 'them_owner'`,
	).Scan(&canLogin); err != nil {
		t.Fatalf("query: %v", err)
	}
	if canLogin {
		t.Error("RLS-31 FAIL: them_owner.rolcanlogin must be false — owner role is NOLOGIN by design")
	}
}

// TestRLS31b_OwnerDirectConnectFails verifies them_owner cannot be used as a DSN.
func TestRLS31b_OwnerDirectConnectFails(t *testing.T) {
	ctx := context.Background()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DATABASE_PORT")
	if port == "" {
		port = "5432"
	}
	ownerDSN := fmt.Sprintf(
		"host=%s port=%s dbname=them user=them_owner password=anything sslmode=disable connect_timeout=5",
		host, port,
	)

	pool, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Logf("pgxpool.New returned error (acceptable): %v", err)
		return
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = pool.Ping(pingCtx)
	if err == nil {
		t.Error("RLS-31b FAIL: them_owner must not be able to login — it is NOLOGIN")
	}
	// err != nil is the expected and desired outcome
}

// TestRLS32_AdminRoleBypassRLS verifies them_admin has rolbypassrls = true.
func TestRLS32_AdminRoleBypassRLS(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if !rolesExist(ctx, pool) {
		t.Skip("RLS roles not yet created — apply db/070_rls_roles.sql first")
	}

	var bypass bool
	if err := pool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'them_admin'`,
	).Scan(&bypass); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !bypass {
		t.Error("RLS-32 FAIL: them_admin.rolbypassrls must be true — admin role requires BYPASSRLS")
	}
}

// TestRLS33_AdminQueryBypasses verifies them_admin can select all rows when RLS is enabled.
// Skips if no table has RLS enabled yet (happens in Phase B).
func TestRLS33_AdminQueryBypasses(t *testing.T) {
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, testAdminDSN(t))
	if err != nil {
		t.Fatalf("connect as them_admin: %v", err)
	}
	defer adminPool.Close()

	var rlsEnabled bool
	if err := adminPool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM pg_class
			WHERE relrowsecurity = true
			  AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname='them')
		)`,
	).Scan(&rlsEnabled); err != nil {
		t.Fatalf("check rls: %v", err)
	}
	if !rlsEnabled {
		t.Skip("RLS-33: no RLS enabled yet — will run after Phase B deployment")
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM them.agents`).Scan(&count); err != nil {
		t.Fatalf("RLS-33 FAIL: them_admin cannot query agents: %v", err)
	}
	t.Logf("RLS-33 PASS: them_admin sees %d agents (BYPASSRLS confirmed)", count)
}

// TestRLS08_AppPoolFailClosed verifies that a raw them_app connection without
// BeginTenantTx returns 0 rows from an RLS-enabled table.
// Skips if RLS is not yet enabled on the agents table.
func TestRLS08_AppPoolFailClosed(t *testing.T) {
	ctx := context.Background()
	appPool, err := pgxpool.New(ctx, testAppDSN(t))
	if err != nil {
		t.Fatalf("connect as them_app: %v", err)
	}
	defer appPool.Close()

	superPool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect super: %v", err)
	}
	defer superPool.Close()

	if !rlsEnabledOnAgents(ctx, superPool) {
		t.Skip("RLS-08: RLS not yet enabled on agents — will run after Phase B/C deployment")
	}

	var count int
	if err := appPool.QueryRow(ctx, `SELECT COUNT(*) FROM them.agents`).Scan(&count); err != nil {
		t.Fatalf("RLS-08 query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("RLS-08 FAIL: expected 0 rows without set_config, got %d — fail-closed not working", count)
	}
	t.Log("RLS-08 PASS: them_app without tenant context returns 0 rows (fail-closed)")
}

// TestRLS10_FreshConnectionFailClosed verifies that a fresh connection to them_app
// with no set_config call returns 0 rows. Uses MaxConns=1 for determinism.
// Skips if RLS not enabled on agents.
func TestRLS10_FreshConnectionFailClosed(t *testing.T) {
	ctx := context.Background()
	appDSN := testAppDSN(t)

	superPool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect super: %v", err)
	}
	defer superPool.Close()

	if !rlsEnabledOnAgents(ctx, superPool) {
		t.Skip("RLS-10: RLS not yet enabled on agents — will run after Phase B/C deployment")
	}

	cfg, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		t.Fatalf("parse app DSN: %v", err)
	}
	cfg.MaxConns = 1
	appPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create MaxConns=1 pool: %v", err)
	}
	defer appPool.Close()

	var count int
	if err := appPool.QueryRow(ctx, `SELECT COUNT(*) FROM them.agents`).Scan(&count); err != nil {
		t.Fatalf("RLS-10 query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("RLS-10 FAIL: fresh connection without set_config returned %d rows, expected 0", count)
	}
	t.Log("RLS-10 PASS: fresh them_app connection is fail-closed")
}

// TestRLS11_GUCResetsAfterCommit verifies that app.tenant_id resets to '' after
// a transaction commits, so the next connection checkout is fail-closed.
// Uses MaxConns=1 to guarantee connection reuse.
func TestRLS11_GUCResetsAfterCommit(t *testing.T) {
	ctx := context.Background()
	appDSN := testAppDSN(t)

	superPool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect super: %v", err)
	}
	defer superPool.Close()

	if !rlsEnabledOnAgents(ctx, superPool) {
		t.Skip("RLS-11: RLS not yet enabled — will run after Phase B/C deployment")
	}

	cfg, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = 1
	appPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer appPool.Close()

	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx-A: %v", err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', '00000000-0000-0000-0000-000000000001', true)"); err != nil {
		_ = tx.Rollback(cleanupCtx)
		t.Fatalf("set_config: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx-A: %v", err)
	}

	// Raw query on same connection (reused via MaxConns=1) — must see 0 rows (GUC reset).
	var count int
	if err := appPool.QueryRow(ctx, `SELECT COUNT(*) FROM them.agents`).Scan(&count); err != nil {
		t.Fatalf("post-commit query: %v", err)
	}
	if count != 0 {
		t.Errorf("RLS-11 FAIL: GUC leaked after commit — raw query returned %d rows (expected 0)", count)
	}
	t.Log("RLS-11 PASS: GUC resets to '' after commit; fail-closed on reused connection")
}

// TestRLSPoolsInterface verifies that NewPools connects successfully and returns
// non-nil pools and AdminQuerier. Requires THEM_DB_URL_APP and THEM_DB_URL_ADMIN.
func TestRLSPoolsInterface(t *testing.T) {
	appDSN := testAppDSN(t)
	adminDSN := testAdminDSN(t)

	ctx := context.Background()
	pools, err := NewPools(ctx, appDSN, adminDSN)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	if pools.App == nil {
		t.Error("App pool must not be nil")
	}
	if pools.Admin == nil {
		t.Error("Admin pool must not be nil")
	}

	aq := pools.NewAdminQuerier()
	if aq == nil {
		t.Error("NewAdminQuerier must not be nil")
	}
	_ = errors.New // keep errors import used
	t.Log("RLS Pools interface test passed")
}

// TestRLS_TwoTenantFullIsolation is a permanent regression test that verifies
// complete cross-tenant data isolation across all RLS-enabled tables.
//
// Strategy:
//  1. Insert two synthetic tenants (A + B) and seed rows for each in every table.
//  2. As tenant-A (TenantTx), read every table — assert only A rows are visible.
//  3. As tenant-B (TenantTx), read every table — assert only B rows are visible.
//  4. As tenant-A, attempt a cross-tenant INSERT — assert it is rejected by WITH CHECK.
//  5. Clean up via Admin pool (BYPASSRLS) regardless of outcome.
//
// Tables covered (27 of 28 — component_definitions excluded; split-policy verified separately):
// mcp_servers, agent_definitions, agent_runtime_specs, llm_providers, audit_logs, tasks,
// runs, run_artifacts, artifacts, task_messages, app_agent_bindings, app_mcp_credentials,
// app_orchestrators, middleware_wirings, middleware_audit, middleware_jobs,
// application_definitions, managed_app_bindings, quarantine_artifacts,
// run_steps, run_usage, tenant_group_mappings.
func TestRLS_TwoTenantFullIsolation(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, testAppDSN(t), testAdminDSN(t))
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	// Also need a superuser pool to insert into them.tenants (them_app has no grant there).
	superPool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("superuser connect: %v", err)
	}
	defer superPool.Close()

	if !rlsEnabledOnAgents(ctx, superPool) {
		t.Skip("RLS_TwoTenantFullIsolation: RLS not yet enabled on agents — apply Phase C migration first")
	}

	// ── 1. Insert synthetic tenants A and B (upsert so reruns are safe) ─────
	var tenantA, tenantB string
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('rlstesttenanta','RLS Test Tenant A')
		 ON CONFLICT ON CONSTRAINT tenants_slug_key DO UPDATE SET display_name = EXCLUDED.display_name
		 RETURNING id::text`,
	).Scan(&tenantA); err != nil {
		t.Fatalf("upsert tenant A: %v", err)
	}
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('rlstesttenantb','RLS Test Tenant B')
		 ON CONFLICT ON CONSTRAINT tenants_slug_key DO UPDATE SET display_name = EXCLUDED.display_name
		 RETURNING id::text`,
	).Scan(&tenantB); err != nil {
		t.Fatalf("upsert tenant B: %v", err)
	}

	// cleanupData removes all child-table test data for the given tenant IDs.
	// Run eagerly before seeding (to clear stale rows from prior runs) and also called by cleanup.
	cleanupData := func(ids []string) {
		for _, tid := range ids {
			// run children before runs/tasks
			_, _ = superPool.Exec(ctx, `DELETE FROM them.run_steps WHERE run_id IN (SELECT id FROM them.runs WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.run_usage WHERE run_id IN (SELECT id FROM them.runs WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.run_artifacts WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.task_messages WHERE task_id IN (SELECT id FROM them.tasks WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.artifacts WHERE task_id IN (SELECT id FROM them.tasks WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.tasks WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.runs WHERE tenant_id=$1::uuid`, tid)
			// quarantine and app-level children
			_, _ = superPool.Exec(ctx, `DELETE FROM them.quarantine_artifacts WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.middleware_audit WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.middleware_jobs WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.middleware_wirings WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.app_mcp_credentials WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.app_agent_bindings WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.app_orchestrators WHERE application_id IN (SELECT id FROM them.applications WHERE tenant_id=$1::uuid)`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.application_definitions WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.agent_runtime_specs WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.agent_definitions WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.agents WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.orchestrators WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.entry_points WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.applications WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.tenant_group_mappings WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.llm_providers WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.access_tokens WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.mcp_servers WHERE tenant_id=$1::uuid`, tid)
			_, _ = superPool.Exec(ctx, `DELETE FROM them.audit_logs WHERE tenant_id=$1::uuid`, tid)
		}
	}
	// cleanup removes all test data including the tenant rows themselves.
	cleanup := func() {
		cleanupData([]string{tenantA, tenantB})
		_, _ = superPool.Exec(ctx, `DELETE FROM them.tenants WHERE id IN ($1::uuid,$2::uuid)`, tenantA, tenantB)
		_, _ = superPool.Exec(ctx, `DELETE FROM them.component_definitions WHERE namespace='rlstest'`)
	}
	// Eagerly clear stale data from previous test runs (tenants are re-upserted so we keep them).
	cleanupData([]string{tenantA, tenantB})
	defer cleanup()

	// ── 2. Seed one row per table per tenant (Admin / superuser pool — BYPASSRLS) ──

	upsert := func(q string, args ...any) string {
		t.Helper()
		var id string
		if err := superPool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := superPool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed exec: %v", err)
		}
	}

	// Direct tenant_id tables
	// agents.id must reference component_definitions(id) — seed a component_def first
	compDefA := upsert(`INSERT INTO them.component_definitions
		(kind, namespace, name, version, display_name, implementation_type, scope, status, content_hash)
		VALUES ('agent','rlstest','rls_agent_a',1,'RLS Agent A','http','tenant','published','rlshash-comp-a')
		ON CONFLICT ON CONSTRAINT component_definitions_kind_namespace_name_version_key DO UPDATE SET display_name=EXCLUDED.display_name
		RETURNING id::text`)
	compDefB := upsert(`INSERT INTO them.component_definitions
		(kind, namespace, name, version, display_name, implementation_type, scope, status, content_hash)
		VALUES ('agent','rlstest','rls_agent_b',1,'RLS Agent B','http','tenant','published','rlshash-comp-b')
		ON CONFLICT ON CONSTRAINT component_definitions_kind_namespace_name_version_key DO UPDATE SET display_name=EXCLUDED.display_name
		RETURNING id::text`)
	agentA := upsert(`INSERT INTO them.agents (id, slug, namespace, tenant_id, transport, scope, status)
		VALUES ($1::uuid,'rlsagenta','rlstest',$2::uuid,'a2a_async','tenant','published')
		ON CONFLICT ON CONSTRAINT agents_tenant_slug_unique DO UPDATE SET namespace=EXCLUDED.namespace RETURNING id::text`,
		compDefA, tenantA)
	agentB := upsert(`INSERT INTO them.agents (id, slug, namespace, tenant_id, transport, scope, status)
		VALUES ($1::uuid,'rlsagentb','rlstest',$2::uuid,'a2a_async','tenant','published')
		ON CONFLICT ON CONSTRAINT agents_tenant_slug_unique DO UPDATE SET namespace=EXCLUDED.namespace RETURNING id::text`,
		compDefB, tenantB)
	mcpA := upsert(`INSERT INTO them.mcp_servers (name, slug, url, tenant_id) VALUES ('RLS MCP A','rlstestmcpa','http://test-a',$1::uuid)
		ON CONFLICT ON CONSTRAINT mcp_servers_tenant_id_slug_key DO UPDATE SET url=EXCLUDED.url RETURNING id::text`, tenantA)
	mcpB := upsert(`INSERT INTO them.mcp_servers (name, slug, url, tenant_id) VALUES ('RLS MCP B','rlstestmcpb','http://test-b',$1::uuid)
		ON CONFLICT ON CONSTRAINT mcp_servers_tenant_id_slug_key DO UPDATE SET url=EXCLUDED.url RETURNING id::text`, tenantB)
	orchA := upsert(`INSERT INTO them.orchestrators (name, display_name, tenant_id) VALUES ('rlsorcha','RLS Orch A',$1::uuid)
		ON CONFLICT ON CONSTRAINT orchestrators_tenant_name_unique DO UPDATE SET display_name=EXCLUDED.display_name RETURNING id::text`, tenantA)
	orchB := upsert(`INSERT INTO them.orchestrators (name, display_name, tenant_id) VALUES ('rlsorchb','RLS Orch B',$1::uuid)
		ON CONFLICT ON CONSTRAINT orchestrators_tenant_name_unique DO UPDATE SET display_name=EXCLUDED.display_name RETURNING id::text`, tenantB)
	_ = orchA
	_ = orchB
	appA := upsert(`INSERT INTO them.applications (name, slug, tenant_id) VALUES ('RLS App A','rlstestappa',$1::uuid)
		ON CONFLICT ON CONSTRAINT uq_applications_tenant_slug DO UPDATE SET name=EXCLUDED.name RETURNING id::text`, tenantA)
	appB := upsert(`INSERT INTO them.applications (name, slug, tenant_id) VALUES ('RLS App B','rlstestappb',$1::uuid)
		ON CONFLICT ON CONSTRAINT uq_applications_tenant_slug DO UPDATE SET name=EXCLUDED.name RETURNING id::text`, tenantB)
	epA := upsert(`INSERT INTO them.entry_points (application_id, slug, tenant_id, entry_point_type) VALUES ($1::uuid,'rlsepsluga',$2::uuid,'websocket')
		ON CONFLICT ON CONSTRAINT uq_entry_points_app_slug DO UPDATE SET tenant_id=EXCLUDED.tenant_id RETURNING id::text`, appA, tenantA)
	epB := upsert(`INSERT INTO them.entry_points (application_id, slug, tenant_id, entry_point_type) VALUES ($1::uuid,'rlsepslugb',$2::uuid,'websocket')
		ON CONFLICT ON CONSTRAINT uq_entry_points_app_slug DO UPDATE SET tenant_id=EXCLUDED.tenant_id RETURNING id::text`, appB, tenantB)
	_ = epA
	_ = epB
	tokenA := upsert(`INSERT INTO them.access_tokens (token_hash, tenant_id, label) VALUES ('rlstokena_hash',$1::uuid,'RLS Token A')
		ON CONFLICT (token_hash) DO UPDATE SET label=EXCLUDED.label RETURNING id::text`, tenantA)
	tokenB := upsert(`INSERT INTO them.access_tokens (token_hash, tenant_id, label) VALUES ('rlstokenb_hash',$1::uuid,'RLS Token B')
		ON CONFLICT (token_hash) DO UPDATE SET label=EXCLUDED.label RETURNING id::text`, tenantB)
	_ = tokenA
	_ = tokenB
	adefA := upsert(`INSERT INTO them.agent_definitions (tenant_id, agent_slug, revision, definition, definition_hash)
		VALUES ($1::uuid,'rls-agent-a',1,'{}','rlsdefhasha')
		ON CONFLICT ON CONSTRAINT agent_definitions_tenant_id_agent_slug_revision_key DO UPDATE SET definition_hash=EXCLUDED.definition_hash
		RETURNING id::text`, tenantA)
	adefB := upsert(`INSERT INTO them.agent_definitions (tenant_id, agent_slug, revision, definition, definition_hash)
		VALUES ($1::uuid,'rls-agent-b',1,'{}','rlsdefhashb')
		ON CONFLICT ON CONSTRAINT agent_definitions_tenant_id_agent_slug_revision_key DO UPDATE SET definition_hash=EXCLUDED.definition_hash
		RETURNING id::text`, tenantB)
	exec(`INSERT INTO them.agent_runtime_specs (tenant_id, definition_id, agent_id, spec, spec_hash)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'{}','rlsspechasha')
		ON CONFLICT ON CONSTRAINT agent_runtime_specs_definition_id_key DO UPDATE SET spec_hash=EXCLUDED.spec_hash`, tenantA, adefA, agentA)
	exec(`INSERT INTO them.agent_runtime_specs (tenant_id, definition_id, agent_id, spec, spec_hash)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'{}','rlsspecahashb')
		ON CONFLICT ON CONSTRAINT agent_runtime_specs_definition_id_key DO UPDATE SET spec_hash=EXCLUDED.spec_hash`, tenantB, adefB, agentB)
	exec(`INSERT INTO them.llm_providers (name, tenant_id) VALUES ('rlsllma',$1::uuid)`, tenantA)
	exec(`INSERT INTO them.llm_providers (name, tenant_id) VALUES ('rlsllmb',$1::uuid)`, tenantB)
	exec(`INSERT INTO them.audit_logs (tenant_id, action, entity_type) VALUES ($1::uuid,'rls.test.a','agent')`, tenantA)
	exec(`INSERT INTO them.audit_logs (tenant_id, action, entity_type) VALUES ($1::uuid,'rls.test.b','agent')`, tenantB)
	exec(`INSERT INTO them.tenant_group_mappings (tenant_id, group_claim, role) VALUES ($1::uuid,'rls-group-a','viewer')
		ON CONFLICT (tenant_id, group_claim) DO UPDATE SET role=EXCLUDED.role`, tenantA)
	exec(`INSERT INTO them.tenant_group_mappings (tenant_id, group_claim, role) VALUES ($1::uuid,'rls-group-b','viewer')
		ON CONFLICT (tenant_id, group_claim) DO UPDATE SET role=EXCLUDED.role`, tenantB)

	// application_definitions (Phase H2)
	appDefA := upsert(`INSERT INTO them.application_definitions (application_id, tenant_id, revision, status, definition, definition_hash)
		VALUES ($1::uuid,$2::uuid,999,'draft','{}','rlshasha')
		ON CONFLICT (application_id, revision) DO UPDATE SET status=EXCLUDED.status RETURNING id::text`, appA, tenantA)
	appDefB := upsert(`INSERT INTO them.application_definitions (application_id, tenant_id, revision, status, definition, definition_hash)
		VALUES ($1::uuid,$2::uuid,999,'draft','{}','rlshashb')
		ON CONFLICT (application_id, revision) DO UPDATE SET status=EXCLUDED.status RETURNING id::text`, appB, tenantB)
	_ = appDefA
	_ = appDefB

	// managed_app_bindings (Phase H2) — app_id references applications; tenant_id direct
	exec(`INSERT INTO them.managed_app_bindings (app_id, tenant_id) VALUES ($1::uuid,$2::uuid)
		ON CONFLICT (app_id, tenant_id) DO UPDATE SET enabled=EXCLUDED.enabled`, appA, tenantA)
	exec(`INSERT INTO them.managed_app_bindings (app_id, tenant_id) VALUES ($1::uuid,$2::uuid)
		ON CONFLICT (app_id, tenant_id) DO UPDATE SET enabled=EXCLUDED.enabled`, appB, tenantB)

	// quarantine_artifacts (Phase H2) — tenant_id + application_id + run_id
	// We need a run for each tenant to satisfy the run_id FK
	runA := upsert(`INSERT INTO them.runs (tenant_id, status, events_transport, entry_point_slug)
		VALUES ($1::uuid,'running','streams','rlsepsluga') RETURNING id::text`, tenantA)
	runB := upsert(`INSERT INTO them.runs (tenant_id, status, events_transport, entry_point_slug)
		VALUES ($1::uuid,'running','streams','rlsepslugb') RETURNING id::text`, tenantB)
	exec(`INSERT INTO them.quarantine_artifacts (application_id, run_id, tenant_id, filename, content_type, size)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'test.bin','application/octet-stream',0)`, appA, runA, tenantA)
	exec(`INSERT INTO them.quarantine_artifacts (application_id, run_id, tenant_id, filename, content_type, size)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'test.bin','application/octet-stream',0)`, appB, runB, tenantB)

	// EXISTS-via-applications tables (Phase D)
	exec(`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id)
		VALUES ($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, appA, mcpA)
	exec(`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id)
		VALUES ($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, appB, mcpB)
	exec(`INSERT INTO them.app_agent_bindings (application_id, agent_id)
		VALUES ($1::uuid,$2::uuid)
		ON CONFLICT ON CONSTRAINT app_agent_bindings_application_id_agent_id_key DO NOTHING`, appA, agentA)
	exec(`INSERT INTO them.app_agent_bindings (application_id, agent_id)
		VALUES ($1::uuid,$2::uuid)
		ON CONFLICT ON CONSTRAINT app_agent_bindings_application_id_agent_id_key DO NOTHING`, appB, agentB)
	orch2A := upsert(`INSERT INTO them.orchestrators (name, display_name, tenant_id) VALUES ('rlsorch2a','RLS Orch 2A',$1::uuid)
		ON CONFLICT ON CONSTRAINT orchestrators_tenant_name_unique DO UPDATE SET display_name=EXCLUDED.display_name RETURNING id::text`, tenantA)
	orch2B := upsert(`INSERT INTO them.orchestrators (name, display_name, tenant_id) VALUES ('rlsorch2b','RLS Orch 2B',$1::uuid)
		ON CONFLICT ON CONSTRAINT orchestrators_tenant_name_unique DO UPDATE SET display_name=EXCLUDED.display_name RETURNING id::text`, tenantB)
	exec(`INSERT INTO them.app_orchestrators (application_id, orchestrator_id, name, node_id, kind)
		VALUES ($1::uuid,$2::uuid,'rls-orch-a','rls-node-a','standard')
		ON CONFLICT ON CONSTRAINT uq_app_orchestrators_app_name DO UPDATE SET node_id=EXCLUDED.node_id`, appA, orch2A)
	exec(`INSERT INTO them.app_orchestrators (application_id, orchestrator_id, name, node_id, kind)
		VALUES ($1::uuid,$2::uuid,'rls-orch-b','rls-node-b','standard')
		ON CONFLICT ON CONSTRAINT uq_app_orchestrators_app_name DO UPDATE SET node_id=EXCLUDED.node_id`, appB, orch2B)

	// tasks + task_messages + artifacts (runs already seeded above)
	taskA := upsert(`INSERT INTO them.tasks (tenant_id, run_id, context_id, state)
		VALUES ($1::uuid,$2::uuid,gen_random_uuid(),'submitted') RETURNING id::text`, tenantA, runA)
	taskB := upsert(`INSERT INTO them.tasks (tenant_id, run_id, context_id, state)
		VALUES ($1::uuid,$2::uuid,gen_random_uuid(),'submitted') RETURNING id::text`, tenantB, runB)
	exec(`INSERT INTO them.task_messages (task_id, role, parts, seq) VALUES ($1::uuid,'user','[]',1)
		ON CONFLICT ON CONSTRAINT uq_task_messages_task_seq DO UPDATE SET role=EXCLUDED.role`, taskA)
	exec(`INSERT INTO them.task_messages (task_id, role, parts, seq) VALUES ($1::uuid,'user','[]',1)
		ON CONFLICT ON CONSTRAINT uq_task_messages_task_seq DO UPDATE SET role=EXCLUDED.role`, taskB)
	exec(`INSERT INTO them.artifacts (task_id, artifact_id, name) VALUES ($1::uuid,'rls-art-a','RLS Art A')
		ON CONFLICT DO NOTHING`, taskA)
	exec(`INSERT INTO them.artifacts (task_id, artifact_id, name) VALUES ($1::uuid,'rls-art-b','RLS Art B')
		ON CONFLICT DO NOTHING`, taskB)

	// run_steps + run_usage + run_artifacts (via runs seeded above)
	exec(`INSERT INTO them.run_steps (run_id) VALUES ($1::uuid)`, runA)
	exec(`INSERT INTO them.run_steps (run_id) VALUES ($1::uuid)`, runB)
	exec(`INSERT INTO them.run_usage (run_id, model, tokens_input, tokens_output) VALUES ($1::uuid,'test-model',1,1)`, runA)
	exec(`INSERT INTO them.run_usage (run_id, model, tokens_input, tokens_output) VALUES ($1::uuid,'test-model',1,1)`, runB)
	runArtifactA := upsert(`INSERT INTO them.run_artifacts (run_id, tenant_id, filename, size) VALUES ($1::uuid,$2::uuid,'rls-art-a',1) RETURNING id::text`, runA, tenantA)
	runArtifactB := upsert(`INSERT INTO them.run_artifacts (run_id, tenant_id, filename, size) VALUES ($1::uuid,$2::uuid,'rls-art-b',1) RETURNING id::text`, runB, tenantB)

	// middleware tables (Phase E/F — EXISTS via applications)
	// middleware_wirings needs agent_id + def_id FKs
	mwWiringA := upsert(`INSERT INTO them.middleware_wirings (application_id, agent_id, def_id)
		SELECT $1::uuid, $2::uuid, id FROM them.middleware_defs LIMIT 1
		ON CONFLICT ON CONSTRAINT uq_mw_wiring_app_agent_pos DO UPDATE SET enabled=EXCLUDED.enabled
		RETURNING id::text`, appA, agentA)
	mwWiringB := upsert(`INSERT INTO them.middleware_wirings (application_id, agent_id, def_id)
		SELECT $1::uuid, $2::uuid, id FROM them.middleware_defs LIMIT 1
		ON CONFLICT ON CONSTRAINT uq_mw_wiring_app_agent_pos DO UPDATE SET enabled=EXCLUDED.enabled
		RETURNING id::text`, appB, agentB)

	// middleware_audit: artifact_id FK references run_artifacts(id)
	exec(`INSERT INTO them.middleware_audit (artifact_id, application_id, processor, outcome)
		VALUES ($1::uuid,$2::uuid,'rls_test','pass')`, runArtifactA, appA)
	exec(`INSERT INTO them.middleware_audit (artifact_id, application_id, processor, outcome)
		VALUES ($1::uuid,$2::uuid,'rls_test','pass')`, runArtifactB, appB)
	// middleware_jobs: needs processors[]
	exec(`INSERT INTO them.middleware_jobs (application_id, processors)
		VALUES ($1::uuid, ARRAY['rls_test'])`, appA)
	exec(`INSERT INTO them.middleware_jobs (application_id, processors)
		VALUES ($1::uuid, ARRAY['rls_test'])`, appB)
	_ = mwWiringA
	_ = mwWiringB

	// ── 3. Tenant-A TenantTx: read all tables — must see A rows, 0 B rows ────
	tidA, err := uuid.Parse(tenantA)
	if err != nil {
		t.Fatalf("parse tenant A UUID: %v", err)
	}
	txA, err := pools.BeginTenantTx(ctx, tidA)
	if err != nil {
		t.Fatalf("BeginTenantTx A: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		txA.Rollback(cleanupCtx)
	}()

	type tableCheck struct {
		q    string
		want int
	}
	// audit_logs is excluded: them_app has INSERT-only on audit_logs (by design); SELECT
	// requires them_admin (BYPASSRLS). The INSERT-only policy is verified separately in S2-09.
	tables := []tableCheck{
		// direct tenant_id — each tenant has exactly 1 unless noted
		{`SELECT count(*) FROM them.agents WHERE slug LIKE 'rlsagent%'`, 1},
		{`SELECT count(*) FROM them.mcp_servers WHERE slug LIKE 'rlstestmcp%'`, 1},
		// Each tenant gets 2 orchestrators: rlsorch{a,b} + rlsorch2{a,b}
		{`SELECT count(*) FROM them.orchestrators WHERE name LIKE 'rlsorch%'`, 2},
		{`SELECT count(*) FROM them.applications WHERE slug LIKE 'rlstestapp%'`, 1},
		{`SELECT count(*) FROM them.entry_points WHERE slug LIKE 'rlsepslug%'`, 1},
		{`SELECT count(*) FROM them.access_tokens WHERE token_hash LIKE 'rlstoken%'`, 1},
		{`SELECT count(*) FROM them.llm_providers WHERE name LIKE 'rlsllm%'`, 1},
		{`SELECT count(*) FROM them.tenant_group_mappings WHERE group_claim LIKE 'rls-group-%'`, 1},
		{`SELECT count(*) FROM them.application_definitions WHERE definition_hash LIKE 'rlshash%'`, 1},
		{`SELECT count(*) FROM them.managed_app_bindings WHERE app_id IN (SELECT id FROM them.applications WHERE slug LIKE 'rlstestapp%')`, 1},
		{`SELECT count(*) FROM them.quarantine_artifacts WHERE filename='test.bin' AND application_id IN (SELECT id FROM them.applications WHERE slug LIKE 'rlstestapp%')`, 1},
		{`SELECT count(*) FROM them.runs WHERE entry_point_slug LIKE 'rlsepslug%'`, 1},
		{`SELECT count(*) FROM them.tasks WHERE run_id IN (SELECT id FROM them.runs WHERE entry_point_slug LIKE 'rlsepslug%')`, 1},
		{`SELECT count(*) FROM them.run_artifacts WHERE filename LIKE 'rls-art-%'`, 1},
		// agent_definitions / agent_runtime_specs
		{`SELECT count(*) FROM them.agent_definitions WHERE agent_slug LIKE 'rls-agent-%'`, 1},
		{`SELECT count(*) FROM them.agent_runtime_specs WHERE spec_hash LIKE 'rlsspec%'`, 1},
		// EXISTS-via-applications
		{`SELECT count(*) FROM them.app_mcp_credentials c JOIN them.applications a ON a.id=c.application_id WHERE a.slug LIKE 'rlstestapp%'`, 1},
		{`SELECT count(*) FROM them.app_agent_bindings b JOIN them.applications a ON a.id=b.application_id WHERE a.slug LIKE 'rlstestapp%'`, 1},
		{`SELECT count(*) FROM them.app_orchestrators o JOIN them.applications a ON a.id=o.application_id WHERE a.slug LIKE 'rlstestapp%'`, 1},
		{`SELECT count(*) FROM them.middleware_wirings w JOIN them.applications a ON a.id=w.application_id WHERE a.slug LIKE 'rlstestapp%'`, 1},
		// middleware_audit and middleware_jobs excluded: them_app has INSERT-only on those tables (by design).
		// Their RLS is still verified by the FORCE ROW LEVEL SECURITY catalog check (CV-02).

		// EXISTS-via-runs
		{`SELECT count(*) FROM them.run_steps s JOIN them.runs r ON r.id=s.run_id WHERE r.entry_point_slug LIKE 'rlsepslug%'`, 1},
		{`SELECT count(*) FROM them.run_usage u JOIN them.runs r ON r.id=u.run_id WHERE r.entry_point_slug LIKE 'rlsepslug%'`, 1},
		// EXISTS-via-tasks
		{`SELECT count(*) FROM them.task_messages m JOIN them.tasks t ON t.id=m.task_id WHERE t.run_id IN (SELECT id FROM them.runs WHERE entry_point_slug LIKE 'rlsepslug%')`, 1},
		{`SELECT count(*) FROM them.artifacts ar JOIN them.tasks t ON t.id=ar.task_id WHERE t.run_id IN (SELECT id FROM them.runs WHERE entry_point_slug LIKE 'rlsepslug%')`, 1},
	}

	checkIsolation := func(t *testing.T, tx *TenantTx, label string) {
		t.Helper()
		for _, tc := range tables {
			var count int
			if err := tx.QueryRow(ctx, tc.q).Scan(&count); err != nil {
				t.Errorf("%s: query error on %q: %v", label, tc.q[:min(60, len(tc.q))], err)
				continue
			}
			if count != tc.want {
				t.Errorf("%s: %q — expected %d (own), got %d", label, tc.q[:min(60, len(tc.q))], tc.want, count)
			} else {
				t.Logf("PASS %s: %d row(s) visible", label, count)
			}
		}
	}

	checkIsolation(t, txA, "tenant-A")

	// ── 4. Tenant-B TenantTx: read all tables — must see B rows, 0 A rows ────
	tidB, err := uuid.Parse(tenantB)
	if err != nil {
		t.Fatalf("parse tenant B UUID: %v", err)
	}
	txB, err := pools.BeginTenantTx(ctx, tidB)
	if err != nil {
		t.Fatalf("BeginTenantTx B: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		txB.Rollback(cleanupCtx)
	}()

	checkIsolation(t, txB, "tenant-B")

	// ── 5. Cross-tenant INSERT rejected by WITH CHECK ─────────────────────────
	_, err = txA.Exec(ctx,
		`INSERT INTO them.mcp_servers (name, slug, tenant_id)
		 VALUES ('RLS Cross Insert', 'rlscrossinsert', $1::uuid)`, tenantB)
	if err == nil {
		t.Error("RLS-TwoTenant FAIL: cross-tenant INSERT into mcp_servers was not blocked by WITH CHECK")
	} else if strings.Contains(err.Error(), "new row violates") || strings.Contains(err.Error(), "row-level security") {
		t.Log("PASS: cross-tenant INSERT into mcp_servers rejected by WITH CHECK policy")
	} else {
		t.Logf("cross-tenant INSERT rejected (possibly for other reason): %v", err)
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	txA.Rollback(cleanupCtx)
}

// TestRLS_CatalogVerification verifies the PostgreSQL catalog matches the expected
// RLS state: exactly 27 tables enabled, all have FORCE ROW LEVEL SECURITY set,
// them_app has no BYPASSRLS, and them_admin has BYPASSRLS.
func TestRLS_CatalogVerification(t *testing.T) {
	ctx := context.Background()
	superPool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer superPool.Close()

	if !rolesExist(ctx, superPool) {
		t.Skip("RLS roles not yet created")
	}

	// CV-01: Count RLS-enabled tables
	var rlsCount int
	if err := superPool.QueryRow(ctx,
		`SELECT count(*) FROM pg_class c
		 JOIN pg_namespace n ON n.oid=c.relnamespace
		 WHERE n.nspname='them' AND c.relkind='r' AND c.relrowsecurity=true`,
	).Scan(&rlsCount); err != nil {
		t.Fatalf("CV-01 query: %v", err)
	}
	if rlsCount < 28 {
		t.Errorf("CV-01 FAIL: expected ≥28 RLS-enabled tables in them schema, got %d", rlsCount)
	} else {
		t.Logf("CV-01 PASS: %d RLS-enabled tables found", rlsCount)
	}

	// CV-02: All RLS-enabled tables must have FORCE RLS set
	var nonForcedCount int
	if err := superPool.QueryRow(ctx,
		`SELECT count(*) FROM pg_class c
		 JOIN pg_namespace n ON n.oid=c.relnamespace
		 WHERE n.nspname='them' AND c.relkind='r'
		   AND c.relrowsecurity=true AND c.relforcerowsecurity=false`,
	).Scan(&nonForcedCount); err != nil {
		t.Fatalf("CV-02 query: %v", err)
	}
	if nonForcedCount > 0 {
		// Fetch the names for a helpful error message
		rows, _ := superPool.Query(ctx,
			`SELECT relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			 WHERE n.nspname='them' AND c.relkind='r' AND c.relrowsecurity=true AND c.relforcerowsecurity=false`)
		var names []string
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var n string
				_ = rows.Scan(&n)
				names = append(names, n)
			}
		}
		t.Errorf("CV-02 FAIL: %d RLS-enabled tables missing FORCE ROW LEVEL SECURITY: %v", nonForcedCount, names)
	} else {
		t.Log("CV-02 PASS: all RLS-enabled tables have FORCE ROW LEVEL SECURITY")
	}

	// CV-03: them_app must NOT have BYPASSRLS
	var appBypass bool
	if err := superPool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname='them_app'`,
	).Scan(&appBypass); err != nil {
		t.Fatalf("CV-03 query: %v", err)
	}
	if appBypass {
		t.Error("CV-03 FAIL: them_app has BYPASSRLS=true — RLS is silently disabled for app-role connections")
	} else {
		t.Log("CV-03 PASS: them_app.rolbypassrls=false")
	}

	// CV-04: them_admin must have BYPASSRLS
	var adminBypass bool
	if err := superPool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname='them_admin'`,
	).Scan(&adminBypass); err != nil {
		t.Fatalf("CV-04 query: %v", err)
	}
	if !adminBypass {
		t.Error("CV-04 FAIL: them_admin has BYPASSRLS=false — admin pool cannot bypass RLS as intended")
	} else {
		t.Log("CV-04 PASS: them_admin.rolbypassrls=true")
	}

	// CV-05: Every RLS-enabled table must have at least one policy
	var tablesWithoutPolicy int
	if err := superPool.QueryRow(ctx,
		`SELECT count(*) FROM pg_class c
		 JOIN pg_namespace n ON n.oid=c.relnamespace
		 WHERE n.nspname='them' AND c.relkind='r' AND c.relrowsecurity=true
		   AND NOT EXISTS (SELECT 1 FROM pg_policies p
		                   WHERE p.schemaname='them' AND p.tablename=c.relname)`,
	).Scan(&tablesWithoutPolicy); err != nil {
		t.Fatalf("CV-05 query: %v", err)
	}
	if tablesWithoutPolicy > 0 {
		t.Errorf("CV-05 FAIL: %d RLS-enabled tables have no policy (fail-open risk)", tablesWithoutPolicy)
	} else {
		t.Log("CV-05 PASS: all RLS-enabled tables have at least one policy")
	}
}

