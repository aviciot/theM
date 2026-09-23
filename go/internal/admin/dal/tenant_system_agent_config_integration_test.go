//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
)

func strPtr(s string) *string { return &s }
func i64Ptr(i int64) *int64   { return &i }

func TestDAL_TenantSystemAgentConfig_GetNoRow(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-norow")

	_, err := d.GetTenantSystemAgentConfig(context.Background(), tenantID, "classifier")
	if err != pgx.ErrNoRows {
		t.Errorf("want pgx.ErrNoRows when no config row exists, got %v", err)
	}
}

func TestDAL_TenantSystemAgentConfig_UpsertGeneralMode(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-general")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-sac-provider-general")
	key, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: &tenantID, Name: "k1", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	in := dal.TenantSystemAgentConfigInput{
		TenantID:     tenantID,
		Role:         "classifier",
		Mode:         "general",
		ProviderName: strPtr("inttest-sac-provider-general"),
		KeyID:        i64Ptr(key.ID),
	}
	created, err := d.UpsertTenantSystemAgentConfig(context.Background(), in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if created.Mode != "general" || created.ProviderName == nil || *created.ProviderName != "inttest-sac-provider-general" {
		t.Errorf("upsert did not apply general mode fields: %+v", created)
	}
	if created.KeyID == nil || *created.KeyID != key.ID {
		t.Errorf("key_id not persisted: %+v", created)
	}

	got, err := d.GetTenantSystemAgentConfig(context.Background(), tenantID, "classifier")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "general" {
		t.Errorf("want mode=general after re-fetch, got %q", got.Mode)
	}
}

func TestDAL_TenantSystemAgentConfig_UpsertCustomMode(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-custom")

	in := dal.TenantSystemAgentConfigInput{
		TenantID:              tenantID,
		Role:                  "card_synthesizer",
		Mode:                  "custom",
		CustomProvider:        strPtr("openai"),
		CustomModel:           strPtr("gpt-4o-mini"),
		CustomAPIKeyEncrypted: strPtr("enc:custom"),
	}
	created, err := d.UpsertTenantSystemAgentConfig(context.Background(), in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if created.Mode != "custom" || created.CustomProvider == nil || *created.CustomProvider != "openai" {
		t.Errorf("upsert did not apply custom mode fields: %+v", created)
	}
}

func TestDAL_TenantSystemAgentConfig_UpsertReplacesExisting(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-replace")

	first := dal.TenantSystemAgentConfigInput{
		TenantID: tenantID, Role: "classifier", Mode: "custom",
		CustomProvider: strPtr("anthropic"), CustomModel: strPtr("m1"), CustomAPIKeyEncrypted: strPtr("enc:1"),
	}
	if _, err := d.UpsertTenantSystemAgentConfig(context.Background(), first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := dal.TenantSystemAgentConfigInput{
		TenantID: tenantID, Role: "classifier", Mode: "general",
		ProviderName: strPtr("anthropic"),
	}
	updated, err := d.UpsertTenantSystemAgentConfig(context.Background(), second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if updated.Mode != "general" {
		t.Errorf("want mode replaced to general, got %q", updated.Mode)
	}
	if updated.CustomProvider != nil {
		t.Errorf("want custom_provider cleared on replace, got %v", *updated.CustomProvider)
	}
}

func TestDAL_TenantSystemAgentConfig_TenantIsolation(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantA := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-iso-a")
	tenantB := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-iso-b")

	if _, err := d.UpsertTenantSystemAgentConfig(context.Background(), dal.TenantSystemAgentConfigInput{
		TenantID: tenantA, Role: "classifier", Mode: "custom",
		CustomProvider: strPtr("anthropic"), CustomModel: strPtr("m"), CustomAPIKeyEncrypted: strPtr("enc:a"),
	}); err != nil {
		t.Fatalf("upsert tenant A: %v", err)
	}

	_, err := d.GetTenantSystemAgentConfig(context.Background(), tenantB, "classifier")
	if err != pgx.ErrNoRows {
		t.Errorf("want pgx.ErrNoRows for tenant B (must not see tenant A's row), got %v", err)
	}
}

func TestDAL_TenantSystemAgentConfig_KeyDeletedSetsKeyIDNull(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID := setupProviderKeyTenant(t, pool, "inttest-sac-tenant-keydel")
	providerID := setupProviderKeyPlatformProvider(t, d, "inttest-sac-provider-keydel")
	key, err := d.CreateLLMProviderKey(context.Background(), dal.LLMProviderKeyInput{
		LLMProviderID: providerID, TenantID: &tenantID, Name: "k1", APIKeyEncrypted: "enc:aaa",
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	if _, err := d.UpsertTenantSystemAgentConfig(context.Background(), dal.TenantSystemAgentConfigInput{
		TenantID: tenantID, Role: "classifier", Mode: "general",
		ProviderName: strPtr("inttest-sac-provider-keydel"), KeyID: i64Ptr(key.ID),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := d.DeleteLLMProviderKey(context.Background(), key.ID, &tenantID); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	got, err := d.GetTenantSystemAgentConfig(context.Background(), tenantID, "classifier")
	if err != nil {
		t.Fatalf("get after key delete: %v", err)
	}
	if got.KeyID != nil {
		t.Errorf("want key_id NULLed by ON DELETE SET NULL after key deletion, got %v", *got.KeyID)
	}
}
