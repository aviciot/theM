//go:build integration

// Platform-as-Tenant Phase 3 (docs/PLATFORM_AS_TENANT_PLAN.md): dedicated
// regression tests proving the bootstrap tenant's them.llm_providers /
// them.llm_provider_keys rows behave exactly as decision 7 specifies, under
// real RLS enforcement (them_app pool + BeginTenantTx), not the BYPASSRLS
// admin pool Phase 2's own tests used:
//
//   - them.llm_providers: the bootstrap tenant's rows ARE visible to every
//     other tenant's SELECT (llm_providers_read's explicit
//     `OR tenant_id = <bootstrap-uuid>` clause, db/110) but NOT writable by
//     them (llm_providers_write stays own-tenant-only, unchanged by db/110).
//   - them.llm_provider_keys: the bootstrap tenant's rows are NOT visible to
//     any other tenant at all — read or write — because
//     llm_provider_keys_tenant_isolation (db/105) is a plain tenant-equality
//     policy with no bootstrap-visibility clause (confirmed asymmetric by
//     the plan's own review, Phase 1's section 6).
//
// Phase 1's own manual SET ROLE verification already checked this once by
// hand at migration time — this file turns that into a permanent, automated
// two-tenant-isolation-style regression test, run against the live,
// already-migrated them-postgres via the same them_app/them_admin pools
// TestRLS_TwoTenantFullIsolation uses.
package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRLS_BootstrapTenant_LLMProviders_VisibleCrossTenant_ReadOnly proves
// decision 7: any other tenant's TenantTx sees the bootstrap tenant's
// llm_providers row (as a platform default), but cannot UPDATE or DELETE it.
func TestRLS_BootstrapTenant_LLMProviders_VisibleCrossTenant_ReadOnly(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, testAppDSN(t), testAdminDSN(t))
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	superPool := mustSuperPool(ctx, t)

	bootstrapID := "00000000-0000-0000-0000-000000000001"

	// ── Seed: a bootstrap-tenant-owned provider row (Admin pool, BYPASSRLS) ──
	const providerName = "rlsp3-bootstrap-provider"
	var providerID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, enabled, tenant_id)
		 VALUES ($1,$1,'m',true,$2::uuid)
		 ON CONFLICT (name, tenant_id) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id`,
		providerName, bootstrapID,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed bootstrap provider: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_providers WHERE id = $1`, providerID)
	})

	// A second, ordinary tenant that owns nothing of its own for this name.
	otherTenant := upsertRLSP3Tenant(ctx, t, superPool, "rlsp3-other-tenant-a")
	tid, err := uuid.Parse(otherTenant)
	if err != nil {
		t.Fatalf("parse other tenant id: %v", err)
	}

	tx, err := pools.BeginTenantTx(ctx, tid)
	if err != nil {
		t.Fatalf("BeginTenantTx: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}()

	// Read: must see the bootstrap tenant's row.
	var seenName string
	err = tx.QueryRow(ctx, `SELECT name FROM them.llm_providers WHERE id = $1`, providerID).Scan(&seenName)
	if err != nil {
		t.Fatalf("RLS-P3 FAIL: other tenant cannot see bootstrap tenant's llm_providers row (want visible per decision 7): %v", err)
	}
	if seenName != providerName {
		t.Errorf("RLS-P3 FAIL: expected to read %q, got %q", providerName, seenName)
	}

	// Write: UPDATE must affect 0 rows (llm_providers_write is own-tenant-only).
	tag, err := tx.Exec(ctx, `UPDATE them.llm_providers SET display_name = 'hijacked' WHERE id = $1`, providerID)
	if err != nil {
		// Some RLS configurations reject outright rather than silently affecting 0 rows — both are acceptable "not writable" outcomes.
		t.Logf("RLS-P3 PASS (write rejected outright): UPDATE of bootstrap provider by another tenant errored: %v", err)
	} else if tag.RowsAffected() != 0 {
		t.Errorf("RLS-P3 FAIL: other tenant's UPDATE affected %d row(s) of the bootstrap tenant's llm_providers row — must be 0 (write policy is own-tenant-only)", tag.RowsAffected())
	} else {
		t.Log("RLS-P3 PASS: other tenant's UPDATE of bootstrap provider affected 0 rows (WITH CHECK / USING blocked it)")
	}

	// Write: DELETE must also affect 0 rows.
	tag, err = tx.Exec(ctx, `DELETE FROM them.llm_providers WHERE id = $1`, providerID)
	if err != nil {
		t.Logf("RLS-P3 PASS (delete rejected outright): %v", err)
	} else if tag.RowsAffected() != 0 {
		t.Errorf("RLS-P3 FAIL: other tenant's DELETE removed %d row(s) of the bootstrap tenant's llm_providers row — must be 0", tag.RowsAffected())
	} else {
		t.Log("RLS-P3 PASS: other tenant's DELETE of bootstrap provider affected 0 rows")
	}

	// Confirm via Admin pool (BYPASSRLS) that the row is untouched — belt-and-suspenders
	// against a false pass where RowsAffected()==0 for an unrelated reason.
	var stillName string
	if err := superPool.QueryRow(ctx, `SELECT display_name FROM them.llm_providers WHERE id = $1`, providerID).Scan(&stillName); err != nil {
		t.Fatalf("post-check select: %v", err)
	}
	if stillName != providerName {
		t.Errorf("RLS-P3 FAIL: bootstrap provider's display_name changed to %q — write from another tenant was not actually blocked", stillName)
	}
}

// TestRLS_BootstrapTenant_LLMProviders_AppRoleHasNoWriteGrant documents a
// real finding from writing this phase's tests (not a regression, not
// touched by db/110): them_app has only SELECT on them.llm_providers at the
// GRANT level — see db/070_rls_roles.sql / db/077_rls_phase_g.sql — so
// llm_providers_write's RLS policy is never actually reachable through the
// them_app/BeginTenantTx path for ANY tenant, bootstrap included. Every real
// write to this table (LLMProviderService.UpsertTenantProvider/CreateProvider)
// goes through the Admin (BYPASSRLS) pool — see cmd/them/main.go's
// admin.NewPgxQuerier(rlsPools.Admin) wiring into NewLLMProvidersHandler.
// This test proves that Admin-pool write path still works for the bootstrap
// tenant's own row post-db/110, and that a raw them_app UPDATE attempt is
// rejected at the GRANT level (42501) before RLS is even evaluated — the
// [[feedback_rls_vs_privileges]] pattern: a write policy existing is not the
// same as a role being granted access to use it.
func TestRLS_BootstrapTenant_LLMProviders_AppRoleHasNoWriteGrant(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, testAppDSN(t), testAdminDSN(t))
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	superPool := mustSuperPool(ctx, t)

	bootstrapID := "00000000-0000-0000-0000-000000000001"
	const providerName = "rlsp3-bootstrap-own-write"
	var providerID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, enabled, tenant_id)
		 VALUES ($1,$1,'m',true,$2::uuid)
		 ON CONFLICT (name, tenant_id) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id`,
		providerName, bootstrapID,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed bootstrap provider: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_providers WHERE id = $1`, providerID)
	})

	// Real write path: Admin (BYPASSRLS) pool — this is what production code uses.
	adminTx, err := pools.BeginAdminTx(ctx)
	if err != nil {
		t.Fatalf("BeginAdminTx: %v", err)
	}
	tag, err := adminTx.Exec(ctx, `UPDATE them.llm_providers SET display_name = 'updated-by-self' WHERE id = $1`, providerID)
	if err != nil {
		_ = adminTx.Rollback(ctx)
		t.Fatalf("RLS-P3 FAIL: Admin pool could not update the bootstrap tenant's own llm_providers row: %v", err)
	}
	if tag.RowsAffected() != 1 {
		_ = adminTx.Rollback(ctx)
		t.Fatalf("RLS-P3 FAIL: Admin pool's UPDATE affected %d rows, want 1", tag.RowsAffected())
	}
	if err := adminTx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Log("RLS-P3 PASS: Admin pool (the real write path) updates the bootstrap tenant's own row fine")

	// Documented gap check: them_app/BeginTenantTx has no write grant at all on this
	// table, for ANY tenant — including the row's own owner. Confirms this is a
	// GRANT-level restriction (42501), not an RLS policy decision, so it's
	// orthogonal to decision 7 and not something Phase 3 needs to change.
	bootstrapUUID, err := uuid.Parse(bootstrapID)
	if err != nil {
		t.Fatalf("parse bootstrap id: %v", err)
	}
	tx, err := pools.BeginTenantTx(ctx, bootstrapUUID)
	if err != nil {
		t.Fatalf("BeginTenantTx bootstrap: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}()

	_, err = tx.Exec(ctx, `UPDATE them.llm_providers SET display_name = 'should-never-apply' WHERE id = $1`, providerID)
	if err == nil {
		t.Error("RLS-P3 FAIL: them_app was able to UPDATE them.llm_providers — expected permission denied (them_app has SELECT-only grant on this table)")
	} else if strings.Contains(err.Error(), "permission denied") {
		t.Logf("RLS-P3 PASS (documented, expected): them_app has no write grant on llm_providers at all — %v", err)
	} else {
		t.Errorf("RLS-P3: unexpected error shape for them_app UPDATE attempt: %v", err)
	}
}

// TestRLS_BootstrapTenant_LLMProviderKeys_InvisibleCrossTenant proves the
// asymmetric half of decision 7: unlike llm_providers, the bootstrap
// tenant's llm_provider_keys rows are invisible to every other tenant —
// no bootstrap-visibility clause exists on llm_provider_keys_tenant_isolation.
func TestRLS_BootstrapTenant_LLMProviderKeys_InvisibleCrossTenant(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, testAppDSN(t), testAdminDSN(t))
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	superPool := mustSuperPool(ctx, t)

	bootstrapID := "00000000-0000-0000-0000-000000000001"

	// Seed a bootstrap-owned provider + a named key on it (Admin pool).
	const providerName = "rlsp3-bootstrap-keys-provider"
	var providerID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, enabled, tenant_id)
		 VALUES ($1,$1,'m',true,$2::uuid)
		 ON CONFLICT (name, tenant_id) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id`,
		providerName, bootstrapID,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed bootstrap provider: %v", err)
	}

	var keyID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_provider_keys (llm_provider_id, tenant_id, name, api_key_encrypted)
		 VALUES ($1, $2::uuid, 'rlsp3-bootstrap-key', 'ciphertext')
		 ON CONFLICT (llm_provider_id, tenant_id, name) DO UPDATE SET api_key_encrypted=EXCLUDED.api_key_encrypted
		 RETURNING id`,
		providerID, bootstrapID,
	).Scan(&keyID); err != nil {
		t.Fatalf("seed bootstrap provider key: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_provider_keys WHERE id = $1`, keyID)
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_providers WHERE id = $1`, providerID)
	})

	otherTenant := upsertRLSP3Tenant(ctx, t, superPool, "rlsp3-other-tenant-b")
	tid, err := uuid.Parse(otherTenant)
	if err != nil {
		t.Fatalf("parse other tenant id: %v", err)
	}

	tx, err := pools.BeginTenantTx(ctx, tid)
	if err != nil {
		t.Fatalf("BeginTenantTx: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}()

	// Read: must see ZERO rows — this is the key difference from llm_providers.
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM them.llm_provider_keys WHERE id = $1`, keyID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("RLS-P3 FAIL: other tenant sees %d row(s) of the bootstrap tenant's llm_provider_keys — want 0 (keys must never be cross-tenant visible, unlike llm_providers)", count)
	} else {
		t.Log("RLS-P3 PASS: other tenant sees 0 rows of the bootstrap tenant's llm_provider_keys (correctly invisible)")
	}

	// Also confirm listing by provider_id (the shape the real DAL/service layer queries by) returns nothing.
	rows, err := tx.Query(ctx, `SELECT id FROM them.llm_provider_keys WHERE llm_provider_id = $1`, providerID)
	if err != nil {
		t.Fatalf("list by provider_id: %v", err)
	}
	listedCount := 0
	for rows.Next() {
		listedCount++
	}
	rows.Close()
	if listedCount != 0 {
		t.Errorf("RLS-P3 FAIL: other tenant's list-by-provider query returned %d bootstrap-owned key row(s), want 0", listedCount)
	}

	// Write attempts: them_app has SELECT-only GRANT on this table (same
	// finding as llm_providers, see TestRLS_BootstrapTenant_LLMProviders_
	// AppRoleHasNoWriteGrant) — both of these fail at the GRANT level
	// (42501) before RLS/WITH CHECK is ever evaluated for ANY tenant, not
	// specifically because of cross-tenant isolation. Run each in its own
	// transaction so one failed statement doesn't abort the next.
	tag, err := tx.Exec(ctx, `UPDATE them.llm_provider_keys SET api_key_encrypted = 'hijacked' WHERE id = $1`, keyID)
	if err == nil && tag.RowsAffected() != 0 {
		t.Errorf("RLS-P3 FAIL: other tenant's UPDATE affected %d row(s) of the bootstrap tenant's llm_provider_keys row — must be 0", tag.RowsAffected())
	} else if err != nil && strings.Contains(err.Error(), "permission denied") {
		t.Logf("RLS-P3 PASS (expected, GRANT-level not RLS): %v", err)
	} else if err != nil {
		t.Errorf("RLS-P3: unexpected error shape for UPDATE attempt: %v", err)
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	tx.Rollback(cleanupCtx)
	cancel()
	tx, err = pools.BeginTenantTx(ctx, tid)
	if err != nil {
		t.Fatalf("BeginTenantTx (fresh, for INSERT check): %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}()

	_, err = tx.Exec(ctx,
		`INSERT INTO them.llm_provider_keys (llm_provider_id, tenant_id, name, api_key_encrypted)
		 VALUES ($1, $2::uuid, 'rlsp3-cross-insert', 'ciphertext')`,
		providerID, bootstrapID)
	if err == nil {
		t.Error("RLS-P3 FAIL: cross-tenant INSERT into llm_provider_keys claiming the bootstrap tenant_id was not blocked")
	} else if strings.Contains(err.Error(), "permission denied") {
		t.Logf("RLS-P3 PASS (expected, GRANT-level not RLS): cross-tenant INSERT rejected — %v", err)
	} else if strings.Contains(err.Error(), "new row violates") || strings.Contains(err.Error(), "row-level security") {
		t.Logf("RLS-P3 PASS: cross-tenant INSERT into llm_provider_keys rejected by WITH CHECK policy — %v", err)
	} else {
		t.Errorf("RLS-P3: unexpected error shape for cross-tenant INSERT: %v", err)
	}
}

// TestRLS_BootstrapTenant_LLMProviderKeys_OwnTenantSeesOwnKey is the control
// case: the bootstrap tenant acting as itself must see its own key normally
// — proves the invisibility above is scoped to OTHER tenants, not a broken
// policy that hides the row from everyone including its owner.
func TestRLS_BootstrapTenant_LLMProviderKeys_OwnTenantSeesOwnKey(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, testAppDSN(t), testAdminDSN(t))
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close()

	superPool := mustSuperPool(ctx, t)

	bootstrapID := "00000000-0000-0000-0000-000000000001"
	const providerName = "rlsp3-bootstrap-selfkey-provider"
	var providerID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_providers (name, display_name, default_model, enabled, tenant_id)
		 VALUES ($1,$1,'m',true,$2::uuid)
		 ON CONFLICT (name, tenant_id) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id`,
		providerName, bootstrapID,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed bootstrap provider: %v", err)
	}

	var keyID int64
	if err := superPool.QueryRow(ctx,
		`INSERT INTO them.llm_provider_keys (llm_provider_id, tenant_id, name, api_key_encrypted)
		 VALUES ($1, $2::uuid, 'rlsp3-bootstrap-selfkey', 'ciphertext')
		 ON CONFLICT (llm_provider_id, tenant_id, name) DO UPDATE SET api_key_encrypted=EXCLUDED.api_key_encrypted
		 RETURNING id`,
		providerID, bootstrapID,
	).Scan(&keyID); err != nil {
		t.Fatalf("seed bootstrap provider key: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_provider_keys WHERE id = $1`, keyID)
		_, _ = superPool.Exec(cleanupCtx, `DELETE FROM them.llm_providers WHERE id = $1`, providerID)
	})

	bootstrapUUID, err := uuid.Parse(bootstrapID)
	if err != nil {
		t.Fatalf("parse bootstrap id: %v", err)
	}
	tx, err := pools.BeginTenantTx(ctx, bootstrapUUID)
	if err != nil {
		t.Fatalf("BeginTenantTx bootstrap: %v", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}()

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM them.llm_provider_keys WHERE id = $1`, keyID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("RLS-P3 FAIL: bootstrap tenant cannot see its own llm_provider_keys row acting as itself — got count %d, want 1", count)
	} else {
		t.Log("RLS-P3 PASS: bootstrap tenant sees its own llm_provider_keys row")
	}
}

// mustSuperPool connects with the superuser DSN (BYPASSRLS via table
// ownership, not a role attribute) for seed/cleanup work — same helper
// pattern as testDSN(t) used inline elsewhere in this package.
//
// Close is registered via t.Cleanup, NOT returned for the caller to defer.
// t.Cleanup funcs always run after the test function's own defers (Go
// testing.T semantics), so a caller-side `defer superPool.Close()` would
// close this pool before any t.Cleanup-registered delete that still needs
// it — every seed/delete cleanup in this file silently no-ops against a
// closed pool as a result (found live: every "rlsp3-*" seed row this file
// creates was leaking into the real database on every test run, because
// each caller had exactly that ordering bug). Registering Close here,
// before any caller registers its own delete cleanup, means LIFO order
// runs the deletes first and closes the pool last.
func mustSuperPool(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("connect super: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// upsertRLSP3Tenant creates (or reuses) a throwaway tenant row for this test
// file's cross-tenant checks, with cleanup registered.
func upsertRLSP3Tenant(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ($1, $1)
		 ON CONFLICT (slug) DO UPDATE SET display_name = EXCLUDED.display_name
		 RETURNING id::text`, slug).Scan(&id); err != nil {
		t.Fatalf("upsert tenant %q: %v", slug, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM them.tenants WHERE id = $1::uuid`, id)
	})
	return id
}
