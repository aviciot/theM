//go:build integration

package dal_test

import (
	"context"
	"testing"
)

// TestDAL_SumMonthlyTokens_ColumnExists is a regression test for the bug where
// SumMonthlyTokens filtered on runs.created_at, a column that does not exist
// (the column is started_at). Before the fix this query errored on every call,
// and quota enforcement failed open (never blocked anyone). See docs/CURRENT.md
// "LLM Gateway" Phase 0 item 1.
func TestDAL_SumMonthlyTokens_ColumnExists(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ctx := context.Background()

	var tenantID string
	err := pool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('inttest-smt-tenant','SMT Test Tenant')
		 ON CONFLICT (slug) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id::text`).Scan(&tenantID)
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.runs WHERE tenant_id = $1::uuid`, tenantID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM them.tenants WHERE id = $1::uuid`, tenantID)     //nolint:errcheck
	})

	// Two runs this month with known token counts; started_at defaults to now().
	_, err = pool.Exec(ctx,
		`INSERT INTO them.runs (tenant_id, status, events_transport, entry_point_slug, total_tokens_in, total_tokens_out)
		 VALUES ($1::uuid,'completed','streams','inttest-smt-ep',100,50)`, tenantID)
	if err != nil {
		t.Fatalf("insert run 1: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO them.runs (tenant_id, status, events_transport, entry_point_slug, total_tokens_in, total_tokens_out)
		 VALUES ($1::uuid,'completed','streams','inttest-smt-ep',200,25)`, tenantID)
	if err != nil {
		t.Fatalf("insert run 2: %v", err)
	}

	total, err := d.SumMonthlyTokens(ctx, tenantID)
	if err != nil {
		t.Fatalf("SumMonthlyTokens must not error (regression: was querying nonexistent runs.created_at): %v", err)
	}
	if total != 375 {
		t.Errorf("want total=375 (100+50+200+25), got %d", total)
	}
}

// TestDAL_SumMonthlyTokens_NoRuns verifies the zero-runs case returns 0, not an error.
func TestDAL_SumMonthlyTokens_NoRuns(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	ctx := context.Background()

	var tenantID string
	err := pool.QueryRow(ctx,
		`INSERT INTO them.tenants (slug, display_name) VALUES ('inttest-smt-empty','SMT Empty Tenant')
		 ON CONFLICT (slug) DO UPDATE SET display_name=EXCLUDED.display_name
		 RETURNING id::text`).Scan(&tenantID)
	if err != nil {
		t.Fatalf("upsert tenant: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM them.tenants WHERE id = $1::uuid`, tenantID) //nolint:errcheck
	})

	total, err := d.SumMonthlyTokens(ctx, tenantID)
	if err != nil {
		t.Fatalf("SumMonthlyTokens: %v", err)
	}
	if total != 0 {
		t.Errorf("want total=0 for tenant with no runs, got %d", total)
	}
}
