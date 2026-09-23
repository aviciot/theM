package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/jackc/pgx/v5"
)

func newTenantSACSvc(d *fakeDal) *service.TenantSystemAgentConfigService {
	return service.NewTenantSystemAgentConfigService(d, providerTestSecretKey)
}

func strp(s string) *string { return &s }
func i64p(i int64) *int64   { return &i }

// ── Get ────────────────────────────────────────────────────────────────────────

func TestTenantSACService_Get_UnknownRole_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, err := svc.Get(context.Background(), "tid", "not_a_role")
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_Get_NoRow_ReturnsCustomDefault(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{getTenantSystemAgentConfigErr: pgx.ErrNoRows})
	out, err := svc.Get(context.Background(), "tid", "classifier")
	if err != nil {
		t.Fatalf("want nil error for no-row case, got %v", err)
	}
	if out.Mode != "custom" {
		t.Errorf("want default mode=custom when no row exists, got %q", out.Mode)
	}
}

func TestTenantSACService_Get_MasksCustomAPIKey(t *testing.T) {
	enc := "enc:abcdefghij" // service_test's fakeDal doesn't actually encrypt; verify masking logic tolerates any stored string
	d := &fakeDal{tenantSystemAgentConfig: dal.TenantSystemAgentConfig{
		Role: "classifier", Mode: "custom",
		CustomProvider: strp("anthropic"), CustomModel: strp("m"),
		CustomAPIKeyEncrypted: &enc,
	}}
	svc := newTenantSACSvc(d)
	out, err := svc.Get(context.Background(), "tid", "classifier")
	if err != nil {
		t.Fatal(err)
	}
	// The fake stores a non-Fernet string, so decryption fails and masked comes
	// back nil rather than panicking — this asserts the failure path is silent,
	// not that a real key masks correctly (see llm_providers_test.go for that).
	if out.CustomAPIKeyMasked != nil && *out.CustomAPIKeyMasked == enc {
		t.Error("plaintext/raw encrypted value must never be returned as the masked hint")
	}
}

// ── Upsert — validation ────────────────────────────────────────────────────────

func TestTenantSACService_Upsert_UnknownRole_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, err := svc.Upsert(context.Background(), "tid", "not_a_role", service.TenantSystemAgentConfigIn{Mode: "custom"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_Upsert_InvalidMode_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{Mode: "bogus"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_Upsert_GeneralMode_MissingProviderName_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{Mode: "general"})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_Upsert_CustomMode_MissingProviderOrModel_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "custom", CustomProvider: strp("anthropic"),
		// CustomModel missing
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_Upsert_CustomMode_FirstTimeNoKey_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{getTenantSystemAgentConfigErr: pgx.ErrNoRows})
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "custom", CustomProvider: strp("anthropic"), CustomModel: strp("m"),
		// CustomAPIKey missing and no existing row to fall back on
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when custom mode has no key at all, got %v", err)
	}
}

// ── Upsert — general mode ──────────────────────────────────────────────────────

func TestTenantSACService_Upsert_GeneralMode_PersistsProviderAndKeyID(t *testing.T) {
	d := &fakeDal{upsertedTenantSystemAgentConfig: dal.TenantSystemAgentConfig{
		Role: "classifier", Mode: "general", ProviderName: strp("anthropic"), KeyID: i64p(7),
	}}
	svc := newTenantSACSvc(d)
	out, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "general", ProviderName: strp("anthropic"), KeyID: i64p(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "general" || out.ProviderName == nil || *out.ProviderName != "anthropic" {
		t.Errorf("unexpected out: %+v", out)
	}
	if len(d.upsertTenantSystemAgentConfigCalls) != 1 {
		t.Fatal("UpsertTenantSystemAgentConfig not called")
	}
	call := d.upsertTenantSystemAgentConfigCalls[0]
	if call.ProviderName == nil || *call.ProviderName != "anthropic" || call.KeyID == nil || *call.KeyID != 7 {
		t.Errorf("provider_name/key_id not passed through to DAL: %+v", call)
	}
	if call.CustomProvider != nil || call.CustomAPIKeyEncrypted != nil {
		t.Errorf("general mode must not populate any custom_* field: %+v", call)
	}
}

func TestTenantSACService_Upsert_GeneralMode_ModelInAllowedList_Persists(t *testing.T) {
	allowed, _ := json.Marshal([]string{"claude-haiku-4-5", "claude-sonnet-4-6"})
	d := &fakeDal{
		tenantProviderByName: dal.LLMProvider{Name: "anthropic", AllowedModelsRaw: allowed},
		upsertedTenantSystemAgentConfig: dal.TenantSystemAgentConfig{
			Role: "classifier", Mode: "general", ProviderName: strp("anthropic"), GeneralModel: strp("claude-haiku-4-5"),
		},
	}
	svc := newTenantSACSvc(d)
	out, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "general", ProviderName: strp("anthropic"), GeneralModel: strp("claude-haiku-4-5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.GeneralModel == nil || *out.GeneralModel != "claude-haiku-4-5" {
		t.Errorf("unexpected out: %+v", out)
	}
	call := d.upsertTenantSystemAgentConfigCalls[0]
	if call.GeneralModel == nil || *call.GeneralModel != "claude-haiku-4-5" {
		t.Errorf("general_model not passed through to DAL: %+v", call)
	}
}

func TestTenantSACService_Upsert_GeneralMode_ModelNotInAllowedList_ReturnsValidation(t *testing.T) {
	allowed, _ := json.Marshal([]string{"claude-haiku-4-5"})
	d := &fakeDal{tenantProviderByName: dal.LLMProvider{Name: "anthropic", AllowedModelsRaw: allowed}}
	svc := newTenantSACSvc(d)
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "general", ProviderName: strp("anthropic"), GeneralModel: strp("not-allowed-model"),
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when general_model is outside the provider's allowed_models, got %v", err)
	}
}

func TestTenantSACService_Upsert_GeneralMode_UnknownProvider_ReturnsValidation(t *testing.T) {
	d := &fakeDal{tenantProviderNotFound: true}
	svc := newTenantSACSvc(d)
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "general", ProviderName: strp("bogus"), GeneralModel: strp("some-model"),
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when provider_name doesn't resolve, got %v", err)
	}
}

// ── Upsert — custom mode ───────────────────────────────────────────────────────

func TestTenantSACService_Upsert_CustomMode_EncryptsKeyBeforePersist(t *testing.T) {
	d := &fakeDal{}
	svc := newTenantSACSvc(d)
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "custom", CustomProvider: strp("anthropic"), CustomModel: strp("m"),
		CustomAPIKey: strp("sk-test-plaintext-key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.upsertTenantSystemAgentConfigCalls) != 1 {
		t.Fatal("UpsertTenantSystemAgentConfig not called")
	}
	call := d.upsertTenantSystemAgentConfigCalls[0]
	if call.CustomAPIKeyEncrypted == nil {
		t.Fatal("custom_api_key_encrypted not set")
	}
	if *call.CustomAPIKeyEncrypted == "sk-test-plaintext-key" {
		t.Error("plaintext key must never be persisted as-is")
	}
}

// ── ResolveCustomTestInputs ────────────────────────────────────────────────────

func TestTenantSACService_ResolveCustomTestInputs_UnknownRole_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{})
	_, _, _, _, err := svc.ResolveCustomTestInputs(context.Background(), "tid", "not_a_role", service.TenantSystemAgentConfigTest{})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestTenantSACService_ResolveCustomTestInputs_BodyOverridesStored(t *testing.T) {
	enc := "enc:stored-key" // fakeDal doesn't encrypt/decrypt for real; body values win regardless
	d := &fakeDal{tenantSystemAgentConfig: dal.TenantSystemAgentConfig{
		Role: "classifier", Mode: "custom",
		CustomProvider: strp("stored-provider"), CustomModel: strp("stored-model"),
		CustomAPIKeyEncrypted: &enc,
	}}
	svc := newTenantSACSvc(d)
	provider, model, apiKey, _, err := svc.ResolveCustomTestInputs(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigTest{
		Provider: "body-provider", Model: "body-model", APIKey: "body-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider != "body-provider" || model != "body-model" || apiKey != "body-key" {
		t.Errorf("want body values to win over stored, got (%q, %q, %q)", provider, model, apiKey)
	}
}

func TestTenantSACService_ResolveCustomTestInputs_NoProviderAnywhere_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{getTenantSystemAgentConfigErr: pgx.ErrNoRows})
	_, _, _, _, err := svc.ResolveCustomTestInputs(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigTest{})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when nothing is stored and body is empty, got %v", err)
	}
}

func TestTenantSACService_ResolveCustomTestInputs_NoKeyAnywhere_ReturnsValidation(t *testing.T) {
	svc := newTenantSACSvc(&fakeDal{tenantSystemAgentConfig: dal.TenantSystemAgentConfig{
		Role: "classifier", Mode: "custom",
		CustomProvider: strp("p"), CustomModel: strp("m"),
		// no CustomAPIKeyEncrypted
	}})
	_, _, _, _, err := svc.ResolveCustomTestInputs(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigTest{
		Provider: "p", Model: "m",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation when no key is stored or supplied, got %v", err)
	}
}

func TestTenantSACService_Upsert_CustomMode_NoNewKey_KeepsExistingEncrypted(t *testing.T) {
	existingEnc := "enc:existing-value"
	d := &fakeDal{tenantSystemAgentConfig: dal.TenantSystemAgentConfig{
		Role: "classifier", Mode: "custom",
		CustomProvider: strp("anthropic"), CustomModel: strp("old-model"),
		CustomAPIKeyEncrypted: &existingEnc,
	}}
	svc := newTenantSACSvc(d)
	_, err := svc.Upsert(context.Background(), "tid", "classifier", service.TenantSystemAgentConfigIn{
		Mode: "custom", CustomProvider: strp("anthropic"), CustomModel: strp("new-model"),
		// CustomAPIKey omitted — must preserve the existing encrypted value
	})
	if err != nil {
		t.Fatal(err)
	}
	call := d.upsertTenantSystemAgentConfigCalls[0]
	if call.CustomAPIKeyEncrypted == nil || *call.CustomAPIKeyEncrypted != existingEnc {
		t.Errorf("want existing encrypted key preserved, got %v", call.CustomAPIKeyEncrypted)
	}
	if call.CustomModel == nil || *call.CustomModel != "new-model" {
		t.Errorf("want new model persisted, got %v", call.CustomModel)
	}
}
