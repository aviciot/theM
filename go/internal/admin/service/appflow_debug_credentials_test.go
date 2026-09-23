package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// fakeCredentialDAL is a small hand-rolled AppFlowDebugCredentialDAL fake —
// no live Postgres needed to test resolveLLMOverride's pure resolution logic.
type fakeCredentialDAL struct {
	providers map[string]dal.LLMProvider // keyed by provider name
	keys      map[int64]dal.LLMProviderKey
	defaults  map[int64]dal.LLMProviderKey // keyed by llm_provider_id
}

func (f *fakeCredentialDAL) GetProviderByNameForTenant(_ context.Context, name, _ string) (dal.LLMProvider, error) {
	p, ok := f.providers[name]
	if !ok {
		return dal.LLMProvider{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeCredentialDAL) GetLLMProviderKey(_ context.Context, id int64, _ *string) (dal.LLMProviderKey, error) {
	k, ok := f.keys[id]
	if !ok {
		return dal.LLMProviderKey{}, pgx.ErrNoRows
	}
	return k, nil
}

func (f *fakeCredentialDAL) GetDefaultLLMProviderKey(_ context.Context, llmProviderID int64, _ *string) (dal.LLMProviderKey, error) {
	k, ok := f.defaults[llmProviderID]
	if !ok {
		return dal.LLMProviderKey{}, pgx.ErrNoRows
	}
	return k, nil
}

var testFernetKey = crypto.DeriveKey("test-secret-key-for-appflow-debug-credentials")

func encryptedTestKey(t *testing.T, plain string) string {
	t.Helper()
	enc, err := crypto.EncryptStored(testFernetKey, plain)
	require.NoError(t, err)
	return enc
}

// TestResolveLLMOverride_General_KeyBelongsToDifferentProvider_Rejected
// closes issue #2 from the d941aca3 review: GetLLMProviderKey only scopes a
// key_id by (id, tenant) — it never checks llm_provider_id — so a key_id
// belonging to provider A could previously be silently accepted and
// decrypted while the request asked for provider B, sending B's traffic
// with A's key. Must be rejected before the run starts.
func TestResolveLLMOverride_General_KeyBelongsToDifferentProvider_Rejected(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001"},
			"openai":    {ID: 2, Name: "openai", DefaultModel: "gpt-4o-mini"},
		},
		keys: map[int64]dal.LLMProviderKey{
			// key id=5 belongs to openai (provider_id=2), not anthropic (provider_id=1).
			5: {ID: 5, LLMProviderID: 2, APIKeyEncrypted: encryptedTestKey(t, "sk-openai-key")},
		},
	}
	keyID := int64(5)
	_, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic", KeyID: &keyID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to provider")
}

// TestResolveLLMOverride_General_KeyBelongsToSameProvider_Succeeds is the
// positive counterpart — a key_id that DOES belong to the requested provider
// must still resolve successfully (the fix must not be overly strict).
func TestResolveLLMOverride_General_KeyBelongsToSameProvider_Succeeds(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001"},
		},
		keys: map[int64]dal.LLMProviderKey{
			5: {ID: 5, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-anthropic-key")},
		},
	}
	keyID := int64(5)
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic", KeyID: &keyID,
	})
	require.NoError(t, err)
	assert.Equal(t, "sk-anthropic-key", ov.APIKey)
	assert.Equal(t, "anthropic", ov.Provider)
}

// TestResolveLLMOverride_General_DefaultKey_NoMismatchCheckNeeded verifies
// the default-key path (KeyID == nil) is unaffected by the mismatch check —
// GetDefaultLLMProviderKey is already scoped by llm_provider_id itself, so
// there's nothing to validate against.
func TestResolveLLMOverride_General_DefaultKey_NoMismatchCheckNeeded(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001"},
		},
		defaults: map[int64]dal.LLMProviderKey{
			1: {ID: 9, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-default-key")},
		},
	}
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic",
	})
	require.NoError(t, err)
	assert.Equal(t, "sk-default-key", ov.APIKey)
}

// TestResolveLLMOverride_General_Model_NotInAllowedList_Rejected closes
// issue #3 from the d941aca3 review: General mode had no per-node model
// selection at all, and (once added) must respect the provider's own
// allowed_models list rather than accepting any string.
func TestResolveLLMOverride_General_Model_NotInAllowedList_Rejected(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001", AllowedModelsRaw: []byte(`["claude-haiku-4-5-20251001","claude-opus-4-1"]`)},
		},
		defaults: map[int64]dal.LLMProviderKey{
			1: {ID: 9, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-key")},
		},
	}
	_, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic", Model: "gpt-4o-mini", // not an anthropic model
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in provider")
}

// TestResolveLLMOverride_General_Model_InAllowedList_Succeeds proves a valid
// model selection is honored (not silently replaced by DefaultModel).
func TestResolveLLMOverride_General_Model_InAllowedList_Succeeds(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001", AllowedModelsRaw: []byte(`["claude-haiku-4-5-20251001","claude-opus-4-1"]`)},
		},
		defaults: map[int64]dal.LLMProviderKey{
			1: {ID: 9, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-key")},
		},
	}
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic", Model: "claude-opus-4-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-1", ov.Model, "the requested model must be honored, not silently replaced by DefaultModel")
}

// TestResolveLLMOverride_General_Model_Empty_UsesProviderDefault verifies
// leaving Model empty in general mode still falls back to the provider's
// own default — no model override is not an error.
func TestResolveLLMOverride_General_Model_Empty_UsesProviderDefault(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001", AllowedModelsRaw: []byte(`["claude-haiku-4-5-20251001"]`)},
		},
		defaults: map[int64]dal.LLMProviderKey{
			1: {ID: 9, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-key")},
		},
	}
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic",
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-haiku-4-5-20251001", ov.Model)
}

// TestResolveLLMOverride_General_NoAllowedModelsConfigured_AnyModelAccepted
// verifies a provider with no allowed_models list configured at all (empty/
// nil — the common case before a tenant ever sets one) does not reject every
// model selection; the allow-list only constrains when it's actually set.
func TestResolveLLMOverride_General_NoAllowedModelsConfigured_AnyModelAccepted(t *testing.T) {
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"anthropic": {ID: 1, Name: "anthropic", DefaultModel: "claude-haiku-4-5-20251001"}, // AllowedModelsRaw nil
		},
		defaults: map[int64]dal.LLMProviderKey{
			1: {ID: 9, LLMProviderID: 1, APIKeyEncrypted: encryptedTestKey(t, "sk-key")},
		},
	}
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "anthropic", Model: "claude-opus-4-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-1", ov.Model)
}

// TestResolveLLMOverride_Custom_BaseURLPreserved closes part of issue #1: the
// resolver itself must still carry BaseURL through into the returned
// Override (the dag-worker side fix, tested separately in
// cmd/dag-worker/dbllmcaller_debug_test.go, is what actually USES it).
func TestResolveLLMOverride_Custom_BaseURLPreserved(t *testing.T) {
	ov, err := resolveLLMOverride(context.Background(), &fakeCredentialDAL{}, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "custom", Provider: "openai", Model: "gpt-4o-mini", APIKey: "sk-custom", BaseURL: "https://my-proxy.example.com/v1",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://my-proxy.example.com/v1", ov.BaseURL)
}

// TestResolveLLMOverride_General_BaseURLFromProviderRow_Preserved verifies
// General mode also carries the provider's own configured base_url through
// (e.g. a self-hosted vLLM endpoint stored on them.llm_providers.base_url).
func TestResolveLLMOverride_General_BaseURLFromProviderRow_Preserved(t *testing.T) {
	baseURL := "https://internal-vllm.example.com/v1"
	d := &fakeCredentialDAL{
		providers: map[string]dal.LLMProvider{
			"vllm": {ID: 3, Name: "vllm", DefaultModel: "llama-3", BaseURL: &baseURL},
		},
		defaults: map[int64]dal.LLMProviderKey{
			3: {ID: 9, LLMProviderID: 3, APIKeyEncrypted: encryptedTestKey(t, "sk-key")},
		},
	}
	ov, err := resolveLLMOverride(context.Background(), d, testFernetKey, "tenant-1", LLMOverrideInput{
		Mode: "general", Provider: "vllm",
	})
	require.NoError(t, err)
	assert.Equal(t, baseURL, ov.BaseURL)
}
