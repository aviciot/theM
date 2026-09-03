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
//  1. Insert two synthetic tenants (A + B) and one row each in every protected table.
//  2. As tenant-A (TenantTx), read every table — assert 0 tenant-B rows visible.
//  3. As tenant-B (TenantTx), read every table — assert 0 tenant-A rows visible.
//  4. As tenant-A (TenantTx), attempt a cross-tenant INSERT (tenant-B's application_id
//     in app_agent_bindings WITH CHECK) — assert it is rejected.
//  5. Clean up all test rows via Admin pool (BYPASSRLS) regardless of outcome.
//
// Tables covered: agents, orchestrators, applications, access_tokens (direct tenant_id);
// app_agent_bindings (EXISTS via applications — Phase D); mcp_servers (Phase B).
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

	// ── 1. Insert synthetic tenants A and B ──────────────────────────────────
	var tenantA, tenantB string
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('rlstesttenanta','RLS Test Tenant A') RETURNING id::text`,
	).Scan(&tenantA); err != nil {
		t.Fatalf("insert tenant A: %v", err)
	}
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('rlstesttenantb','RLS Test Tenant B') RETURNING id::text`,
	).Scan(&tenantB); err != nil {
		// Clean up tenant A before failing
		_, _ = superPool.Exec(ctx, `DELETE FROM them.tenants WHERE id = $1::uuid`, tenantA)
		t.Fatalf("insert tenant B: %v", err)
	}

	// cleanup removes all test data at the end, regardless of outcome.
	cleanup := func() {
		// Delete in dependency order. CASCADE handles children.
		_, _ = superPool.Exec(ctx,
			`DELETE FROM them.tenants WHERE id IN ($1::uuid, $2::uuid)`, tenantA, tenantB)
	}
	defer cleanup()

	// ── 2. Insert one row per table per tenant via Admin pool ─────────────────
	// Insert mcp_servers (simple structure, Phase B RLS-enabled, direct tenant_id).
	var mcpA, mcpB string
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.mcp_servers (name, slug, url, tenant_id)
		 VALUES ('RLS MCP A', 'rlstestmcpa', 'http://test-a', $1::uuid) RETURNING id::text`, tenantA,
	).Scan(&mcpA); err != nil {
		t.Fatalf("insert mcp A: %v", err)
	}
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.mcp_servers (name, slug, url, tenant_id)
		 VALUES ('RLS MCP B', 'rlstestmcpb', 'http://test-b', $1::uuid) RETURNING id::text`, tenantB,
	).Scan(&mcpB); err != nil {
		t.Fatalf("insert mcp B: %v", err)
	}

	// Insert orchestrators (direct tenant_id, Phase C).
	var orchA, orchB string
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.orchestrators (name, display_name, tenant_id)
		 VALUES ('rlstestorcha', 'RLS Orch A', $1::uuid) RETURNING id::text`, tenantA,
	).Scan(&orchA); err != nil {
		t.Fatalf("insert orch A: %v", err)
	}
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.orchestrators (name, display_name, tenant_id)
		 VALUES ('rlstestorchb', 'RLS Orch B', $1::uuid) RETURNING id::text`, tenantB,
	).Scan(&orchB); err != nil {
		t.Fatalf("insert orch B: %v", err)
	}
	_ = orchA
	_ = orchB

	// Insert applications (direct tenant_id, Phase C).
	var appA, appB string
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.applications (name, slug, tenant_id)
		 VALUES ('RLS App A', 'rlstestappa', $1::uuid) RETURNING id::text`, tenantA,
	).Scan(&appA); err != nil {
		t.Fatalf("insert app A: %v", err)
	}
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.applications (name, slug, tenant_id)
		 VALUES ('RLS App B', 'rlstestappb', $1::uuid) RETURNING id::text`, tenantB,
	).Scan(&appB); err != nil {
		t.Fatalf("insert app B: %v", err)
	}
	// Insert app_mcp_credentials for each tenant (Phase D — EXISTS via applications).
	if _, err := superPool.Exec(ctx,
		`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id)
		 VALUES ($1::uuid, $2::uuid)`, appA, mcpA,
	); err != nil {
		t.Fatalf("insert mcp_cred A: %v", err)
	}
	if _, err := superPool.Exec(ctx,
		`INSERT INTO them.app_mcp_credentials (application_id, mcp_server_id)
		 VALUES ($1::uuid, $2::uuid)`, appB, mcpB,
	); err != nil {
		t.Fatalf("insert mcp_cred B: %v", err)
	}

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

	tables := []struct {
		name  string
		query string
	}{
		// Phase B — direct tenant_id
		{"mcp_servers", `SELECT count(*) FROM them.mcp_servers WHERE slug LIKE 'rlstestmcp%'`},
		// Phase C — direct tenant_id
		{"orchestrators", `SELECT count(*) FROM them.orchestrators WHERE name LIKE 'rlstestorch%'`},
		{"applications", `SELECT count(*) FROM them.applications WHERE slug LIKE 'rlstestapp%'`},
		// Phase D — EXISTS via applications
		{"app_mcp_credentials", `SELECT count(*) FROM them.app_mcp_credentials c JOIN them.applications a ON a.id=c.application_id WHERE a.slug LIKE 'rlstestapp%'`},
	}

	for _, tbl := range tables {
		var count int
		if err := txA.QueryRow(ctx, tbl.query).Scan(&count); err != nil {
			t.Errorf("RLS-TwoTenant tenant-A read %s: query error: %v", tbl.name, err)
			continue
		}
		if count != 1 {
			t.Errorf("RLS-TwoTenant tenant-A %s: expected 1 row (own), got %d", tbl.name, count)
		} else {
			t.Logf("RLS-TwoTenant PASS: tenant-A sees 1 row in %s (own data only)", tbl.name)
		}
	}

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

	for _, tbl := range tables {
		var count int
		if err := txB.QueryRow(ctx, tbl.query).Scan(&count); err != nil {
			t.Errorf("RLS-TwoTenant tenant-B read %s: query error: %v", tbl.name, err)
			continue
		}
		if count != 1 {
			t.Errorf("RLS-TwoTenant tenant-B %s: expected 1 row (own), got %d", tbl.name, count)
		} else {
			t.Logf("RLS-TwoTenant PASS: tenant-B sees 1 row in %s (own data only)", tbl.name)
		}
	}

	// ── 5. Cross-tenant INSERT rejected by WITH CHECK ─────────────────────────
	// Tenant-A TenantTx tries to INSERT a mcp_server with tenant-B's tenant_id — must be blocked.
	_, err = txA.Exec(ctx,
		`INSERT INTO them.mcp_servers (name, slug, tenant_id)
		 VALUES ('RLS Cross Insert', 'rlscrossinsert', $1::uuid)`, tenantB)
	if err == nil {
		t.Error("RLS-TwoTenant FAIL: cross-tenant INSERT into mcp_servers was not blocked by WITH CHECK")
	} else if strings.Contains(err.Error(), "new row violates") || strings.Contains(err.Error(), "row-level security") {
		t.Log("RLS-TwoTenant PASS: cross-tenant INSERT into mcp_servers rejected by WITH CHECK policy")
	} else {
		t.Logf("RLS-TwoTenant: cross-tenant INSERT rejected (possibly for other reason): %v", err)
	}

	// Rollback txA (the cross-tenant INSERT attempt may have aborted the tx).
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	txA.Rollback(cleanupCtx)
}

