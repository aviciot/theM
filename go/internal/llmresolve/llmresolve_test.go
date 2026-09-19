package llmresolve

import (
	"errors"
	"testing"

	"github.com/aviciot/them/internal/crypto"
)

// TestPricingTable_EstimateCost_KnownModel verifies per-token cost math and
// the per-million -> per-token conversion applied at parse time.
func TestPricingTable_EstimateCost_KnownModel(t *testing.T) {
	table := PricingTable{
		"gpt-4o": ModelPrice{InputPerToken: 5.0 / 1e6, OutputPerToken: 15.0 / 1e6},
	}
	cost, ok := table.EstimateCost("gpt-4o", 1_000_000, 1_000_000)
	if !ok {
		t.Fatal("want ok=true for a model present in the table")
	}
	if cost != 5.0+15.0 {
		t.Errorf("want cost=20.0, got %v", cost)
	}
}

// TestPricingTable_EstimateCost_UnknownModel verifies the table signals the
// caller to fall back to a default rate card, instead of returning a
// misleading zero cost as if that were a real (free) price.
func TestPricingTable_EstimateCost_UnknownModel(t *testing.T) {
	table := PricingTable{"gpt-4o": ModelPrice{InputPerToken: 1, OutputPerToken: 1}}
	cost, ok := table.EstimateCost("claude-opus-4-8", 100, 100)
	if ok {
		t.Error("want ok=false for a model absent from the table")
	}
	if cost != 0 {
		t.Errorf("want cost=0 alongside ok=false, got %v", cost)
	}
}

// TestParseModelPricing_ConvertsPerMillionToPerToken verifies the DB's
// per-million-tokens units (them.llm_providers.model_pricing) are converted
// to per-token rates, matching estimateCost's unit convention.
func TestParseModelPricing_ConvertsPerMillionToPerToken(t *testing.T) {
	raw := []byte(`{"claude-sonnet-4-6": {"input": 3.0, "output": 15.0}}`)
	table := parseModelPricing(raw)
	p, ok := table["claude-sonnet-4-6"]
	if !ok {
		t.Fatal("want claude-sonnet-4-6 present in parsed table")
	}
	if p.InputPerToken != 3.0/1e6 {
		t.Errorf("want InputPerToken=3.0/1e6, got %v", p.InputPerToken)
	}
	if p.OutputPerToken != 15.0/1e6 {
		t.Errorf("want OutputPerToken=15.0/1e6, got %v", p.OutputPerToken)
	}
}

// TestParseModelPricing_EmptyOrMalformed verifies malformed/empty input
// yields an empty (non-nil) table rather than an error or panic — pricing is
// best-effort and callers must be able to range over the result unconditionally.
func TestParseModelPricing_EmptyOrMalformed(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("not json"), []byte("null")} {
		table := parseModelPricing(raw)
		if table == nil {
			t.Errorf("want non-nil table for input %q", raw)
		}
	}
}

// TestParseAppProviderKey_StructuredEncrypted verifies the new structured
// format ({"provider": {"ct": "enc:..."}}) is decrypted via the callback.
func TestParseAppProviderKey_StructuredEncrypted(t *testing.T) {
	raw := []byte(`{"anthropic": {"ct": "enc:sometoken", "hint": "XXXX"}}`)
	called := false
	key, err := ParseAppProviderKey(raw, "anthropic", func(ct string) (string, error) {
		called = true
		if ct != "enc:sometoken" {
			t.Errorf("want decrypt called with the raw ct, got %q", ct)
		}
		return "sk-decrypted", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("want decrypt callback invoked for structured ciphertext")
	}
	if key != "sk-decrypted" {
		t.Errorf("want key=sk-decrypted, got %q", key)
	}
}

// TestParseAppProviderKey_PlainPrefix_SkipsDecrypt verifies the "plain:"
// test-mode prefix (written by internal/admin/service/applications.go
// encryptKey when no crypto key is configured) is stripped directly, WITHOUT
// calling decrypt — decrypt only understands the "enc:" prefix and would
// otherwise pass "plain:..." through unchanged, corrupting the key.
func TestParseAppProviderKey_PlainPrefix_SkipsDecrypt(t *testing.T) {
	raw := []byte(`{"anthropic": {"ct": "plain:sk-test-123"}}`)
	key, err := ParseAppProviderKey(raw, "anthropic", func(ct string) (string, error) {
		t.Fatal("decrypt must not be called for a plain: prefixed value")
		return "", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-test-123" {
		t.Errorf("want key=sk-test-123 (prefix stripped), got %q", key)
	}
}

// TestParseAppProviderKey_LegacyFlatFormat verifies the pre-encryption flat
// {"provider": "plaintext"} format is still readable.
func TestParseAppProviderKey_LegacyFlatFormat(t *testing.T) {
	raw := []byte(`{"anthropic": "sk-legacy-plaintext"}`)
	key, err := ParseAppProviderKey(raw, "anthropic", func(string) (string, error) {
		t.Fatal("decrypt must not be called for the legacy flat format")
		return "", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-legacy-plaintext" {
		t.Errorf("want key=sk-legacy-plaintext, got %q", key)
	}
}

// TestParseAppProviderKey_NoKeyForProvider verifies a structured blob with
// no entry for the requested provider returns ("", nil), not an error.
func TestParseAppProviderKey_NoKeyForProvider(t *testing.T) {
	raw := []byte(`{"openai": {"ct": "enc:sometoken"}}`)
	key, err := ParseAppProviderKey(raw, "anthropic", func(string) (string, error) {
		t.Fatal("decrypt must not be called for a provider with no entry")
		return "", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "" {
		t.Errorf("want empty key, got %q", key)
	}
}

// TestDecryptValue_NoKeyConfigured_LegacyPlaintext_PassesThrough verifies a
// legacy unencrypted value (no "enc:" prefix) still decrypts fine even with
// no Fernet key configured — matches crypto.DecryptStored's own contract.
func TestDecryptValue_NoKeyConfigured_LegacyPlaintext_PassesThrough(t *testing.T) {
	r := New(nil, nil, nil)
	got, err := r.DecryptValue("sk-plain-legacy-value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-plain-legacy-value" {
		t.Errorf("want value unchanged, got %q", got)
	}
}

// TestDecryptValue_NoKeyConfigured_Ciphertext_FailsLoud is the regression
// test for the bug fixed in this change: previously, a stored "enc:..."
// value with no Fernet key configured was returned AS-IS — a caller that
// didn't check the (ignored) error would treat opaque ciphertext as a usable
// plaintext API key. It must now return an error instead.
func TestDecryptValue_NoKeyConfigured_Ciphertext_FailsLoud(t *testing.T) {
	r := New(nil, nil, nil)
	got, err := r.DecryptValue("enc:gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqszUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24=")
	if err == nil {
		t.Fatal("want an error when decrypting ciphertext with no Fernet key configured")
	}
	if got != "" {
		t.Errorf("want empty result alongside the error, got %q", got)
	}
}

// TestDecryptValue_WithKey_RoundTrips verifies the normal (key-configured)
// path still round-trips a value encrypted by crypto.EncryptStored.
func TestDecryptValue_WithKey_RoundTrips(t *testing.T) {
	key := crypto.DeriveKey("test-secret-key")
	stored, err := crypto.EncryptStored(key, "sk-my-real-key")
	if err != nil {
		t.Fatalf("EncryptStored: %v", err)
	}

	r := New(nil, key, nil)
	got, err := r.DecryptValue(stored)
	if err != nil {
		t.Fatalf("DecryptValue: %v", err)
	}
	if got != "sk-my-real-key" {
		t.Errorf("want round-tripped plaintext, got %q", got)
	}
}

// TestDecryptValue_WithKey_HMACMismatch verifies a tampered/wrong-key
// ciphertext still surfaces as an error, not silently returned.
func TestDecryptValue_WithKey_HMACMismatch(t *testing.T) {
	key := crypto.DeriveKey("test-secret-key")
	stored, err := crypto.EncryptStored(key, "sk-my-real-key")
	if err != nil {
		t.Fatalf("EncryptStored: %v", err)
	}

	wrongKey := crypto.DeriveKey("a-different-secret")
	r := New(nil, wrongKey, nil)
	_, err = r.DecryptValue(stored)
	if err == nil {
		t.Fatal("want an error when decrypting with the wrong key")
	}
	if !errors.Is(err, crypto.ErrHMACMismatch) {
		t.Errorf("want ErrHMACMismatch, got %v", err)
	}
}
