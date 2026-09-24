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

func newAppFlowDebugPresetSvc(d *fakeDal) *service.AppFlowDebugPresetService {
	return service.NewAppFlowDebugPresetService(d, providerTestSecretKey)
}

func TestAppFlowDebugPresetService_Save_MissingName_ReturnsValidation(t *testing.T) {
	svc := newAppFlowDebugPresetSvc(&fakeDal{})
	_, err := svc.Save(context.Background(), "tid", 1, "app-id", service.AppFlowDebugPresetIn{
		EntryPointSlug: "chat",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAppFlowDebugPresetService_Save_MissingEntryPointSlug_ReturnsValidation(t *testing.T) {
	svc := newAppFlowDebugPresetSvc(&fakeDal{})
	_, err := svc.Save(context.Background(), "tid", 1, "app-id", service.AppFlowDebugPresetIn{
		Name: "my-preset",
	})
	if !errors.Is(err, service.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

func TestAppFlowDebugPresetService_Save_EncryptsAPIKeyBeforePersist(t *testing.T) {
	d := &fakeDal{}
	svc := newAppFlowDebugPresetSvc(d)

	_, err := svc.Save(context.Background(), "tid", 1, "app-id", service.AppFlowDebugPresetIn{
		Name:           "my-preset",
		EntryPointSlug: "chat",
		LLMOverrides: map[string]service.AppFlowDebugPresetLLMOverrideIn{
			"node-1": {Mode: "custom", Provider: "anthropic", Model: "m", APIKey: "sk-test-plaintext-key"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.upsertAppFlowDebugPresetCalls) != 1 {
		t.Fatal("UpsertAppFlowDebugPreset not called")
	}
	call := d.upsertAppFlowDebugPresetCalls[0]

	var stored map[string]map[string]any
	if err := json.Unmarshal(call.LLMOverrides, &stored); err != nil {
		t.Fatalf("stored llm_overrides is not valid JSON: %v", err)
	}
	entry, ok := stored["node-1"]
	if !ok {
		t.Fatal("node-1 entry missing from stored llm_overrides")
	}
	if _, hasPlaintext := entry["api_key"]; hasPlaintext {
		t.Error("plaintext api_key must never be written to stored llm_overrides")
	}
	encrypted, _ := entry["api_key_encrypted"].(string)
	if encrypted == "" || encrypted == "sk-test-plaintext-key" {
		t.Errorf("want api_key_encrypted set and different from plaintext, got %q", encrypted)
	}
}

func TestAppFlowDebugPresetService_Save_NoAPIKey_StoresNoEncryptedField(t *testing.T) {
	d := &fakeDal{}
	svc := newAppFlowDebugPresetSvc(d)

	_, err := svc.Save(context.Background(), "tid", 1, "app-id", service.AppFlowDebugPresetIn{
		Name:           "my-preset",
		EntryPointSlug: "chat",
		LLMOverrides: map[string]service.AppFlowDebugPresetLLMOverrideIn{
			"node-1": {Mode: "general", Provider: "anthropic", Model: "m"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := d.upsertAppFlowDebugPresetCalls[0]
	var stored map[string]map[string]any
	if err := json.Unmarshal(call.LLMOverrides, &stored); err != nil {
		t.Fatalf("stored llm_overrides is not valid JSON: %v", err)
	}
	if _, has := stored["node-1"]["api_key_encrypted"]; has {
		t.Error("general mode with no api_key must not populate api_key_encrypted")
	}
}

func TestAppFlowDebugPresetService_List_MasksStoredAPIKey(t *testing.T) {
	// Save first (real Fernet encrypt) to obtain a genuine encrypted blob, then
	// feed the exact same fakeDal's captured call straight into List's storage
	// so the mask/decrypt round-trip on the read path is exercised for real.
	d := &fakeDal{}
	svc := newAppFlowDebugPresetSvc(d)
	if _, err := svc.Save(context.Background(), "tid", 1, "app-id", service.AppFlowDebugPresetIn{
		Name: "my-preset", EntryPointSlug: "chat",
		LLMOverrides: map[string]service.AppFlowDebugPresetLLMOverrideIn{
			"node-1": {Mode: "custom", APIKey: "sk-live-secret-1234"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	stored := d.upsertAppFlowDebugPresetCalls[0]
	d.appFlowDebugPresets = []dal.AppFlowDebugPreset{{
		ID: "p1", Name: "my-preset", EntryPointSlug: "chat",
		LLMOverrides: stored.LLMOverrides,
	}}

	out, err := svc.List(context.Background(), "tid", 1, "app-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 preset, got %d", len(out))
	}
	entry, ok := out[0].LLMOverrides["node-1"]
	if !ok {
		t.Fatal("node-1 entry missing from List output")
	}
	if entry.APIKeyMasked == nil {
		t.Fatal("want APIKeyMasked set")
	}
	if *entry.APIKeyMasked == "sk-live-secret-1234" {
		t.Error("plaintext key must never be returned from List")
	}
}

func TestAppFlowDebugPresetService_Delete_NoRows_ReturnsNotFound(t *testing.T) {
	svc := newAppFlowDebugPresetSvc(&fakeDal{deleteAppFlowDebugPresetErr: pgx.ErrNoRows})
	err := svc.Delete(context.Background(), "tid", 1, "preset-id")
	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAppFlowDebugPresetService_Delete_Success(t *testing.T) {
	svc := newAppFlowDebugPresetSvc(&fakeDal{})
	if err := svc.Delete(context.Background(), "tid", 1, "preset-id"); err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
}
