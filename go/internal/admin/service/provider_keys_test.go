package service_test

// Tests for provider_keys encryption roundtrip and parseProviderKeys format detection.
// These are whitebox tests of unexported logic exercised through the exported service API.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/crypto"
)

// ── parseProviderKeys path tests ──────────────────────────────────────────────

// PK-1: New structured format with a populated CT is returned as-is.
func TestSetGetProviderKey_NewFormat_RoundTrip(t *testing.T) {
	key := crypto.DeriveKey("test-secret-key")
	d := &fakeDal{}
	svc := service.NewAppService(d, &fakeCache{}, key)

	if err := svc.SetProviderKey(context.Background(), "tenant-1", "app-1", "anthropic", "sk-ant-test1234"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	// The stored JSONB must be structured {"ct":"...","hint":"..."}
	if d.setProviderKeyValue == nil {
		t.Fatal("SetProviderKey did not call dal.SetProviderKey")
	}
	var entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(d.setProviderKeyValue, &entry); err != nil {
		t.Fatalf("stored value not structured JSON: %v", err)
	}
	if entry.CT == "" {
		t.Error("CT must not be empty after encryption")
	}
	if entry.Hint != "1234" {
		t.Errorf("hint: want '1234', got %q", entry.Hint)
	}

	// Simulate what the DAL would return in a round-trip: the entire provider_keys column
	// is {"anthropic": {"ct":"...","hint":"1234"}}
	raw, _ := json.Marshal(map[string]any{"anthropic": entry})
	d.providerKeysRaw = raw

	plain, err := svc.GetPlaintextProviderKey(context.Background(), "tenant-1", "app-1", "anthropic")
	if err != nil {
		t.Fatalf("GetPlaintextProviderKey: %v", err)
	}
	if plain != "sk-ant-test1234" {
		t.Errorf("want 'sk-ant-test1234', got %q", plain)
	}
}

// PK-2: No-key service (nil crypto key) uses plain: prefix and round-trips cleanly.
func TestSetGetProviderKey_NoCryptoKey_RoundTrip(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, &fakeCache{}, nil) // nil = test mode, no encryption

	if err := svc.SetProviderKey(context.Background(), "t1", "a1", "openai", "sk-openai-abc"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}

	// Must store structured entry with CT = "plain:sk-openai-abc"
	var entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(d.setProviderKeyValue, &entry); err != nil {
		t.Fatalf("stored value: %v", err)
	}
	if entry.CT != "plain:sk-openai-abc" {
		t.Errorf("CT: want 'plain:sk-openai-abc', got %q", entry.CT)
	}

	// Round-trip via GetPlaintextProviderKey
	raw, _ := json.Marshal(map[string]any{"openai": entry})
	d.providerKeysRaw = raw

	plain, err := svc.GetPlaintextProviderKey(context.Background(), "t1", "a1", "openai")
	if err != nil {
		t.Fatalf("GetPlaintextProviderKey: %v", err)
	}
	if plain != "sk-openai-abc" {
		t.Errorf("want 'sk-openai-abc', got %q", plain)
	}
}

// PK-3: Legacy flat format {"anthropic": "sk-ant-..."} is returned as plaintext (migration path).
func TestGetPlaintextProviderKey_LegacyFlatFormat(t *testing.T) {
	d := &fakeDal{}
	raw, _ := json.Marshal(map[string]string{"anthropic": "sk-ant-legacy"})
	d.providerKeysRaw = raw
	svc := service.NewAppService(d, &fakeCache{}, nil)

	plain, err := svc.GetPlaintextProviderKey(context.Background(), "t1", "a1", "anthropic")
	if err != nil {
		t.Fatalf("GetPlaintextProviderKey: %v", err)
	}
	if plain != "sk-ant-legacy" {
		t.Errorf("want 'sk-ant-legacy', got %q", plain)
	}
}

// PK-4: GetProviderKeys returns KeySet=true and the correct hint for an encrypted key.
func TestGetProviderKeys_ReturnsHint(t *testing.T) {
	d := &fakeDal{}
	raw, _ := json.Marshal(map[string]any{
		"anthropic": map[string]string{"ct": "some-ct", "hint": "5678"},
	})
	d.providerKeysRaw = raw
	svc := service.NewAppService(d, &fakeCache{}, nil)

	keys, err := svc.GetProviderKeys(context.Background(), "t1", "a1")
	if err != nil {
		t.Fatalf("GetProviderKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(keys))
	}
	if !keys[0].KeySet {
		t.Error("KeySet must be true when CT is non-empty")
	}
	if keys[0].KeyHint != "5678" {
		t.Errorf("hint: want '5678', got %q", keys[0].KeyHint)
	}
}

// PK-5: SetProviderKey rejects unsupported providers.
func TestSetProviderKey_UnsupportedProvider(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, &fakeCache{}, nil)
	err := svc.SetProviderKey(context.Background(), "t1", "a1", "unsupported_provider", "key")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

// PK-6: SetProviderKey rejects empty key.
func TestSetProviderKey_EmptyKey(t *testing.T) {
	svc := service.NewAppService(&fakeDal{}, &fakeCache{}, nil)
	err := svc.SetProviderKey(context.Background(), "t1", "a1", "anthropic", "")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

// PK-7: Hint is empty when key is fewer than 4 chars (edge case: short test key).
func TestSetProviderKey_ShortKey_EmptyHint(t *testing.T) {
	d := &fakeDal{}
	svc := service.NewAppService(d, &fakeCache{}, nil)
	if err := svc.SetProviderKey(context.Background(), "t1", "a1", "anthropic", "abc"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	var entry struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(d.setProviderKeyValue, &entry); err != nil {
		t.Fatalf("stored value: %v", err)
	}
	if entry.Hint != "" {
		t.Errorf("hint for 3-char key: want '', got %q", entry.Hint)
	}
}

// PK-8: Structured format with all-empty CT fields must return empty map, not an error.
// Regression test for the bug where parseProviderKeys fell through to the flat unmarshal
// on {"anthropic":{"ct":"","hint":""}} rows, which then failed with "cannot unmarshal object
// into Go value of type string".
func TestGetProviderKeys_EmptyCTStructured_ReturnsEmpty(t *testing.T) {
	d := &fakeDal{}
	raw, _ := json.Marshal(map[string]any{
		"anthropic": map[string]string{"ct": "", "hint": ""},
	})
	d.providerKeysRaw = raw
	svc := service.NewAppService(d, &fakeCache{}, nil)

	keys, err := svc.GetProviderKeys(context.Background(), "t1", "a1")
	if err != nil {
		t.Fatalf("GetProviderKeys returned error for all-empty CT row: %v", err)
	}
	// Entry has empty CT — KeySet must be false.
	for _, k := range keys {
		if k.KeySet {
			t.Errorf("provider %q: KeySet must be false when CT is empty", k.Provider)
		}
	}
}

// PK-9: GetPlaintextProviderKey on all-empty CT structured row returns empty string, not error.
func TestGetPlaintextProviderKey_EmptyCTStructured_ReturnsEmpty(t *testing.T) {
	d := &fakeDal{}
	raw, _ := json.Marshal(map[string]any{
		"anthropic": map[string]string{"ct": "", "hint": ""},
	})
	d.providerKeysRaw = raw
	svc := service.NewAppService(d, &fakeCache{}, nil)

	plain, err := svc.GetPlaintextProviderKey(context.Background(), "t1", "a1", "anthropic")
	if err != nil {
		t.Fatalf("GetPlaintextProviderKey returned error for all-empty CT row: %v", err)
	}
	if plain != "" {
		t.Errorf("want empty string, got %q", plain)
	}
}

