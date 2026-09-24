//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// Platform-as-Tenant Phase 2 (docs/PLATFORM_AS_TENANT_PLAN.md): the bootstrap
// tenant (tenantctx.BootstrapTenantID) now owns what used to be the
// tenant_id-IS-NULL "platform default" rows. These tests prove
// ListProvidersForTenant and GetProviderBaseURLs correctly treat the
// bootstrap tenant's rows as the fallback for every other tenant, using an
// explicit tenant_id equality check instead of the old NULL check.

// upsertBootstrapProvider upserts a bootstrap-tenant-owned provider row for
// name and registers cleanup. Uses UpsertTenantProvider (not CreateProvider)
// so the row is tenant-scoped to the bootstrap tenant, matching what Phase 1's
// migration produced for the platform's real default rows.
func upsertBootstrapProvider(t *testing.T, d *dal.DB, name, baseURL string) dal.LLMProvider {
	t.Helper()
	var baseURLPtr *string
	if baseURL != "" {
		baseURLPtr = &baseURL
	}
	row, err := d.UpsertTenantProvider(context.Background(), tenantctx.BootstrapTenantID, dal.LLMProviderInput{
		Name: name, DisplayName: name, DefaultModel: "m", Enabled: true, BaseURL: baseURLPtr,
	})
	if err != nil {
		t.Fatalf("upsert bootstrap provider %q: %v", name, err)
	}
	t.Cleanup(func() { _ = d.DeleteProvider(context.Background(), row.ID) })
	return row
}

func TestDAL_ListProvidersForTenant_FallsBackToBootstrapTenantDefault(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-p2-lpft-tenant")

	bootstrapRow := upsertBootstrapProvider(t, d, "inttest-p2-lpft-provider", "")

	rows, err := d.ListProvidersForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListProvidersForTenant: %v", err)
	}
	var found *dal.LLMProvider
	for i := range rows {
		if rows[i].Name == "inttest-p2-lpft-provider" {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		t.Fatal("want the bootstrap tenant's provider default to be visible to another tenant, got none")
	}
	if found.ID != bootstrapRow.ID {
		t.Errorf("want the bootstrap tenant's row (id=%d) returned as the default, got id=%d", bootstrapRow.ID, found.ID)
	}
	if found.TenantID == nil || *found.TenantID != tenantctx.BootstrapTenantID {
		t.Errorf("want tenant_id=%s on the fallback row, got %v", tenantctx.BootstrapTenantID, found.TenantID)
	}
}

func TestDAL_ListProvidersForTenant_OwnRowWinsOverBootstrapDefault(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-p2-lpft-own-tenant")

	upsertBootstrapProvider(t, d, "inttest-p2-lpft-own-provider", "")

	own, err := d.UpsertTenantProvider(context.Background(), tenantID, dal.LLMProviderInput{
		Name: "inttest-p2-lpft-own-provider", DisplayName: "mine", DefaultModel: "own-model", Enabled: true,
	})
	if err != nil {
		t.Fatalf("upsert own provider: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteProvider(context.Background(), own.ID) })

	rows, err := d.ListProvidersForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListProvidersForTenant: %v", err)
	}
	var found *dal.LLMProvider
	for i := range rows {
		if rows[i].Name == "inttest-p2-lpft-own-provider" {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		t.Fatal("want the tenant's own row present")
	}
	if found.ID != own.ID {
		t.Errorf("want the tenant's own row (id=%d) to win over the bootstrap default, got id=%d", own.ID, found.ID)
	}
	// Exactly one row for this name — no duplicate from the bootstrap fallback branch.
	count := 0
	for _, r := range rows {
		if r.Name == "inttest-p2-lpft-own-provider" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 row named inttest-p2-lpft-own-provider, got %d (bootstrap default must not duplicate an overridden name)", count)
	}
}

func TestDAL_ListProvidersForTenant_BootstrapTenantItself_NoDuplicate(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)

	bootstrapRow := upsertBootstrapProvider(t, d, "inttest-p2-lpft-self-provider", "")

	// Calling ListProvidersForTenant AS the bootstrap tenant itself: $1 and $2
	// are the same UUID, so the row must appear exactly once, not twice.
	rows, err := d.ListProvidersForTenant(context.Background(), tenantctx.BootstrapTenantID)
	if err != nil {
		t.Fatalf("ListProvidersForTenant: %v", err)
	}
	count := 0
	for _, r := range rows {
		if r.Name == "inttest-p2-lpft-self-provider" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly 1 row when querying as the bootstrap tenant itself, got %d", count)
	}
	_ = bootstrapRow
}

func TestDAL_GetProviderBaseURLs_FallsBackToBootstrapTenantDefault(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-p2-baseurl-tenant")

	upsertBootstrapProvider(t, d, "inttest-p2-baseurl-provider", "https://bootstrap.example.com/v1")

	urls, err := d.GetProviderBaseURLs(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("GetProviderBaseURLs: %v", err)
	}
	if urls["inttest-p2-baseurl-provider"] != "https://bootstrap.example.com/v1" {
		t.Errorf("want the bootstrap tenant's base_url visible to another tenant, got %q", urls["inttest-p2-baseurl-provider"])
	}
}

func TestDAL_GetProviderBaseURLs_OwnRowWinsOverBootstrapDefault(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-p2-baseurl-own-tenant")

	upsertBootstrapProvider(t, d, "inttest-p2-baseurl-own-provider", "https://bootstrap.example.com/v1")

	own, err := d.UpsertTenantProvider(context.Background(), tenantID, dal.LLMProviderInput{
		Name: "inttest-p2-baseurl-own-provider", DisplayName: "mine", DefaultModel: "m", Enabled: true,
		BaseURL: strPtr("https://tenant-own.example.com/v1"),
	})
	if err != nil {
		t.Fatalf("upsert own provider: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteProvider(context.Background(), own.ID) })

	urls, err := d.GetProviderBaseURLs(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("GetProviderBaseURLs: %v", err)
	}
	if urls["inttest-p2-baseurl-own-provider"] != "https://tenant-own.example.com/v1" {
		t.Errorf("want the tenant's own base_url to win over the bootstrap default, got %q", urls["inttest-p2-baseurl-own-provider"])
	}
}
