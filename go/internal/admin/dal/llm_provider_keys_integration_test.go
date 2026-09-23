//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupProviderKeyTenant upserts a test tenant and returns its id, with cleanup registered.
func setupProviderKeyTenant(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	ctx := context.Background()
	var tenantID string
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
	return tenantID
}

// setupProviderKeyPlatformProvider creates (or reuses) a platform-default provider row
// and returns its id, with cleanup registered.
func setupProviderKeyPlatformProvider(t *testing.T, d *dal.DB, name string) int64 {
	t.Helper()
	p, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: name, DisplayName: name, DefaultModel: "m", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create platform provider %q: %v", name, err)
	}
	t.Cleanup(func() { _ = d.DeleteProvider(context.Background(), p.ID) })
	return p.ID
}

// ── allowed_models on them.llm_providers ────────────────────────────────────────

func TestDAL_Provider_AllowedModels_RoundTrip(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-allowed-models")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-allowed-models") })

	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: "inttest-allowed-models", DisplayName: "P", DefaultModel: "m", Enabled: true,
		AllowedModelsRaw: []byte(`["claude-sonnet-4-6","claude-haiku-4-5-20251001"]`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	models := dal.AllowedModelsOrEmpty(created.AllowedModelsRaw)
	if len(models) != 2 || models[0] != "claude-sonnet-4-6" {
		t.Errorf("want 2 allowed models with claude-sonnet-4-6 first, got %v", models)
	}

	got, err := d.GetProvider(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got := dal.AllowedModelsOrEmpty(got.AllowedModelsRaw); len(got) != 2 {
		t.Errorf("want 2 allowed models after re-fetch, got %v", got)
	}
}

func TestDAL_Provider_AllowedModels_DefaultsToEmptyArray(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	cleanProviders(t, d, "inttest-allowed-models-default")
	t.Cleanup(func() { cleanProviders(t, d, "inttest-allowed-models-default") })

	created, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: "inttest-allowed-models-default", DisplayName: "P", DefaultModel: "m", Enabled: true,
		// AllowedModelsRaw intentionally nil
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	models := dal.AllowedModelsOrEmpty(created.AllowedModelsRaw)
	if models == nil || len(models) != 0 {
		t.Errorf("want non-nil empty slice, got %v", models)
	}
}

// ── them.llm_provider_keys ───────────────────────────────────────────────────────

func TestDAL_ProviderKey_CreateAndList(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-list")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-list")

	k1, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "Key_for_april", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create key 1: %v", err)
	}
	if _, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "Key_for_QA", APIKeyEncrypted: "enc:bbb",
	}); err != nil {
		t.Fatalf("create key 2: %v", err)
	}

	list, err := d.ListLLMProviderKeys(context.Background(), providerID, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 keys, got %d", len(list))
	}
	// Ordered by name ASC: "Key_for_QA" < "Key_for_april" (capital Q < lowercase a in byte order).
	if list[0].ID != k1.ID && list[1].ID != k1.ID {
		t.Error("created key not found in list")
	}
}

func TestDAL_ProviderKey_DuplicateName_UniqueViolation(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-dup")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-dup")

	in := dal.LLMProviderKeyInput{LLMProviderID: providerID, TenantID: tenantID, Name: "dup", APIKeyEncrypted: "enc:aaa"}
	if _, err := d.CreateLLMProviderKey(context.Background(), in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := d.CreateLLMProviderKey(context.Background(), in)
	if !dal.IsUniqueViolation(err) {
		t.Errorf("want unique violation on duplicate (provider,tenant,name), got %v", err)
	}
}

func TestDAL_ProviderKey_TenantIsolation(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantA := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-a")
	tenantB := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-b")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-iso")

	k, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantA, Name: "a-key", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Tenant B must not be able to fetch tenant A's key by id.
	_, err = d.GetLLMProviderKey(context.Background(), k.ID, tenantB)
	if err == nil {
		t.Error("want error fetching another tenant's key, got nil")
	}

	listB, err := d.ListLLMProviderKeys(context.Background(), providerID, tenantB)
	if err != nil {
		t.Fatalf("list for tenant B: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("want 0 keys visible to tenant B, got %d", len(listB))
	}
}

func TestDAL_ProviderKey_SetDefault_ClearsPrevious(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-default")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-default")

	k1, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "k1", APIKeyEncrypted: "enc:aaa", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("create k1: %v", err)
	}
	k2, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "k2", APIKeyEncrypted: "enc:bbb",
	})
	if err != nil {
		t.Fatalf("create k2: %v", err)
	}

	if _, err := d.SetDefaultLLMProviderKey(context.Background(), k2.ID, tenantID); err != nil {
		t.Fatalf("SetDefaultLLMProviderKey: %v", err)
	}

	got1, err := d.GetLLMProviderKey(context.Background(), k1.ID, tenantID)
	if err != nil {
		t.Fatalf("get k1: %v", err)
	}
	if got1.IsDefault {
		t.Error("k1 must no longer be default after k2 is set as default")
	}
	got2, err := d.GetLLMProviderKey(context.Background(), k2.ID, tenantID)
	if err != nil {
		t.Fatalf("get k2: %v", err)
	}
	if !got2.IsDefault {
		t.Error("k2 must be the default key")
	}
}

func TestDAL_ProviderKey_UpdateRenameAndRotate(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-update")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-update")

	k, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "old-name", APIKeyEncrypted: "enc:old",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := d.UpdateLLMProviderKey(context.Background(), k.ID, tenantID, dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "new-name", APIKeyEncrypted: "enc:new",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "new-name" || updated.APIKeyEncrypted != "enc:new" {
		t.Errorf("update did not apply: %+v", updated)
	}
}

func TestDAL_ProviderKey_Delete(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-delete")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-pk-provider-delete")

	k, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: tenantID, Name: "to-delete", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.DeleteLLMProviderKey(context.Background(), k.ID, tenantID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = d.GetLLMProviderKey(context.Background(), k.ID, tenantID)
	if err != pgx.ErrNoRows {
		t.Errorf("want pgx.ErrNoRows after delete, got %v", err)
	}
}

func TestDAL_ProviderKey_DeleteProviderCascadesKeys(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-pk-tenant-cascade")

	provider, err := d.CreateProvider(context.Background(), dal.LLMProviderInput{
		Name: "inttest-pk-provider-cascade", DisplayName: "P", DefaultModel: "m", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteProvider(context.Background(), provider.ID) })

	k, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: provider.ID, TenantID: tenantID, Name: "cascade-key", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := d.DeleteProvider(context.Background(), provider.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}

	_, err = d.GetLLMProviderKey(context.Background(), k.ID, tenantID)
	if err != pgx.ErrNoRows {
		t.Errorf("want key cascade-deleted with its provider (pgx.ErrNoRows), got %v", err)
	}
}
