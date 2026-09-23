//go:build integration

package dal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupAppScopedConfigApp upserts a test tenant and an application owned by
// it, returning (tenantID, appID), with cleanup registered. Used by the
// tenant-isolation regression tests below (docs/APP_CANVAS_DEBUG_PLAN.md
// Phase 4 tenant-isolation fix) for both GetAppLogVerbosity/
// UpsertAppLogVerbosity (them.app_debug_config) and GetTemporalAppConfig/
// UpsertTemporalAppConfig (them.app_temporal_config) — neither table has its
// own tenant_id column, so ownership is enforced via a join to
// them.applications on every call.
func setupAppScopedConfigApp(t *testing.T, pool *pgxpool.Pool, slug string) (tenantID, appID string) {
	t.Helper()
	ctx := context.Background()
	err := pool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ($1, $1)
		 ON CONFLICT (slug) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id::text`, slug).Scan(&tenantID)
	if err != nil {
		t.Fatalf("upsert tenant %q: %v", slug, err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.tenants WHERE id = $1::uuid`, tenantID) //nolint:errcheck
	})

	err = pool.QueryRow(ctx,
		`INSERT INTO them.applications (tenant_id, name, slug, enabled) VALUES ($1::uuid, $2, $2, true)
		 RETURNING id::text`, tenantID, slug).Scan(&appID)
	if err != nil {
		t.Fatalf("insert application for tenant %q: %v", slug, err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.applications WHERE id = $1::uuid`, appID) //nolint:errcheck
	})
	return tenantID, appID
}

// ── AppFlow trace log-verbosity (them.app_debug_config) ────────────────────

// LV-TI-1: GetAppLogVerbosity for an application owned by the caller's own
// tenant succeeds and returns the default when no row exists yet.
func TestDAL_GetAppLogVerbosity_OwnTenant_ReturnsDefault(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-lv-own")

	v, err := d.GetAppLogVerbosity(context.Background(), tenantID, appID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != dal.DefaultLogVerbosity {
		t.Errorf("expected default %q, got %q", dal.DefaultLogVerbosity, v)
	}
}

// LV-TI-2 (tenant-isolation fix regression test): GetAppLogVerbosity for an
// application that exists but belongs to a DIFFERENT tenant must return
// pgx.ErrNoRows, not the application's actual setting. This is the exact
// cross-tenant IDOR the code review found: before the fix, this function took
// no tenantID at all and would happily return any application's setting to
// any caller who knew its UUID.
func TestDAL_GetAppLogVerbosity_OtherTenant_ReturnsNoRows(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ownerTenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-lv-victim")
	attackerTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-lv-attacker")

	// Owner sets a non-default value so a leak would be observable, not just
	// a coincidental default match.
	if err := d.UpsertAppLogVerbosity(context.Background(), ownerTenantID, appID, dal.LogVerbosityFull); err != nil {
		t.Fatalf("owner upsert: %v", err)
	}

	_, err := d.GetAppLogVerbosity(context.Background(), attackerTenantID, appID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for cross-tenant access, got %v", err)
	}
}

// LV-TI-3 (tenant-isolation fix regression test): UpsertAppLogVerbosity for
// an application belonging to a different tenant must fail with
// pgx.ErrNoRows and must not write a row.
func TestDAL_UpsertAppLogVerbosity_OtherTenant_ReturnsNoRowsAndDoesNotWrite(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ownerTenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-lv-victim2")
	attackerTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-lv-attacker2")

	err := d.UpsertAppLogVerbosity(context.Background(), attackerTenantID, appID, dal.LogVerbosityOff)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for cross-tenant write, got %v", err)
	}

	// The owner's setting must remain the default — the attacker's write must
	// not have gone through under any tenant.
	v, err := d.GetAppLogVerbosity(context.Background(), ownerTenantID, appID)
	if err != nil {
		t.Fatalf("owner get after attacker write attempt: %v", err)
	}
	if v != dal.DefaultLogVerbosity {
		t.Errorf("expected owner's setting to remain default %q, got %q — attacker write leaked through", dal.DefaultLogVerbosity, v)
	}
}

// ── Temporal execution config (them.app_temporal_config) ───────────────────

// TC-TI-1: GetTemporalAppConfig for an application owned by the caller's own
// tenant succeeds and returns nil (no override row) when none exists yet.
func TestDAL_GetTemporalAppConfig_OwnTenant_ReturnsNilWhenNoOverride(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-tc-own")

	cfg, err := d.GetTemporalAppConfig(context.Background(), tenantID, appID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil (no override), got %+v", cfg)
	}
}

// TC-TI-2 (tenant-isolation fix regression test): GetTemporalAppConfig for an
// application belonging to a different tenant must return pgx.ErrNoRows, not
// nil-with-no-error — before the fix, an unowned or nonexistent appID and a
// genuinely-owned-but-unconfigured appID were indistinguishable (both
// returned (nil, nil)), so a caller could never be told "you don't own this."
func TestDAL_GetTemporalAppConfig_OtherTenant_ReturnsNoRows(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ownerTenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-tc-victim")
	attackerTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-tc-attacker")

	five := 5
	if err := d.UpsertTemporalAppConfig(context.Background(), ownerTenantID, appID, dal.TemporalConfig{MaxConcurrentWorkflows: &five}); err != nil {
		t.Fatalf("owner upsert: %v", err)
	}

	_, err := d.GetTemporalAppConfig(context.Background(), attackerTenantID, appID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for cross-tenant access, got %v", err)
	}
}

// TC-TI-3 (tenant-isolation fix regression test): UpsertTemporalAppConfig for
// an application belonging to a different tenant must fail with
// pgx.ErrNoRows and must not write an override row.
func TestDAL_UpsertTemporalAppConfig_OtherTenant_ReturnsNoRowsAndDoesNotWrite(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ownerTenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-tc-victim2")
	attackerTenantID, _ := setupAppScopedConfigApp(t, pool, "inttest-tc-attacker2")

	ninety := 90
	err := d.UpsertTemporalAppConfig(context.Background(), attackerTenantID, appID, dal.TemporalConfig{WorkflowTimeoutS: &ninety})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows for cross-tenant write, got %v", err)
	}

	cfg, err := d.GetTemporalAppConfig(context.Background(), ownerTenantID, appID)
	if err != nil {
		t.Fatalf("owner get after attacker write attempt: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected no override to exist (attacker write must not have gone through), got %+v", cfg)
	}
}
