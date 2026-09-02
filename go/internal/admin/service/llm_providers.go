package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// LLMProviderOut is the HTTP response shape for LLM provider endpoints.
// TenantID is null for platform-default rows and non-null for tenant overrides.
type LLMProviderOut struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	DisplayName  string         `json:"display_name"`
	APIKeySet    bool           `json:"api_key_set"`
	APIKeyMasked *string        `json:"api_key_masked"` // null in JSON when no key
	BaseURL      *string        `json:"base_url"`
	DefaultModel string         `json:"default_model"`
	ModelPricing map[string]any `json:"model_pricing"`
	Enabled      bool           `json:"enabled"`
	TenantID     *string        `json:"tenant_id"` // null = platform default
}

// LLMProviderCreate is the request body for POST /admin/llm-providers.
type LLMProviderCreate struct {
	Name         string         `json:"name"`
	DisplayName  string         `json:"display_name"`
	APIKey       string         `json:"api_key"` // plaintext; empty = no key
	BaseURL      *string        `json:"base_url"`
	DefaultModel string         `json:"default_model"`
	ModelPricing map[string]any `json:"model_pricing"`
	Enabled      *bool          `json:"enabled"`
}

// LLMProviderPatch is the PATCH request body. Nil pointer = field absent (leave unchanged).
// api_key uses a dedicated present/value pair because "present but empty" is distinct from
// "absent" — an explicit empty api_key clears the stored key; absence preserves it.
type LLMProviderPatch struct {
	DisplayName     *string        `json:"display_name"`
	APIKey          *string        `json:"api_key"`   // nil=absent, ""=clear key, non-empty=rotate
	BaseURL         **string       `json:"base_url"`  // nil=absent; non-nil ptr = set (may point to nil to clear)
	DefaultModel    *string        `json:"default_model"`
	ModelPricing    map[string]any `json:"model_pricing"` // nil=absent
	Enabled         *bool          `json:"enabled"`
	APIKeyPresent   bool           `json:"-"` // set by handler when api_key appears in JSON
}

// LLMProviderService owns the business logic for LLM provider CRUD.
type LLMProviderService struct {
	dal    Dal
	fernetKey []byte // 32-byte derived key from DeriveKey(secretKey)
}

// NewLLMProviderService creates an LLMProviderService.
// secretKey must not be empty (validated at startup by config.Load).
func NewLLMProviderService(d Dal, secretKey string) *LLMProviderService {
	return &LLMProviderService{
		dal:       d,
		fernetKey: crypto.DeriveKey(secretKey),
	}
}

// List returns all providers with masked API keys.
func (s *LLMProviderService) List(ctx context.Context) ([]LLMProviderOut, error) {
	rows, err := s.dal.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LLMProviderOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toOut(r))
	}
	return out, nil
}

// Get returns a single provider with masked API key.
func (s *LLMProviderService) Get(ctx context.Context, id int64) (LLMProviderOut, error) {
	row, err := s.dal.GetProvider(ctx, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderOut{}, ErrNotFound
		}
		return LLMProviderOut{}, err
	}
	return s.toOut(row), nil
}

// Create validates, encrypts the API key if provided, and persists a new provider.
// Returns ErrValidation for missing required fields and ErrConflict for duplicate name.
func (s *LLMProviderService) Create(ctx context.Context, body LLMProviderCreate) (LLMProviderOut, error) {
	if body.Name == "" {
		return LLMProviderOut{}, validation("name is required")
	}
	if body.DisplayName == "" {
		return LLMProviderOut{}, validation("display_name is required")
	}
	if body.DefaultModel == "" {
		return LLMProviderOut{}, validation("default_model is required")
	}

	var encryptedKey *string
	if body.APIKey != "" {
		enc, err := crypto.EncryptStored(s.fernetKey, body.APIKey)
		if err != nil {
			slog.Warn("llm_providers: failed to encrypt api_key on create", "error_category", "crypto_encrypt")
			return LLMProviderOut{}, errors.New("failed to encrypt api_key")
		}
		encryptedKey = &enc
	}

	modelPricingRaw, err := marshalPricing(body.ModelPricing)
	if err != nil {
		return LLMProviderOut{}, validation("model_pricing must be a JSON object")
	}

	in := dal.LLMProviderInput{
		Name:            body.Name,
		DisplayName:     body.DisplayName,
		APIKeyEncrypted: encryptedKey,
		BaseURL:         body.BaseURL,
		DefaultModel:    body.DefaultModel,
		ModelPricingRaw: modelPricingRaw,
		Enabled:         enabledOrDefault(body.Enabled),
	}

	row, err := s.dal.CreateProvider(ctx, in)
	if err != nil {
		if dal.IsUniqueViolation(err) {
			return LLMProviderOut{}, ErrConflict
		}
		return LLMProviderOut{}, err
	}
	return s.toOut(row), nil
}

// Update applies a PATCH to an existing provider using fetch-then-modify semantics.
// Fields absent from the patch (nil pointer) are left unchanged.
// Returns ErrNotFound for missing provider.
func (s *LLMProviderService) Update(ctx context.Context, id int64, patch LLMProviderPatch) (LLMProviderOut, error) {
	row, err := s.dal.GetProvider(ctx, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderOut{}, ErrNotFound
		}
		return LLMProviderOut{}, err
	}

	// Apply non-nil patch fields.
	if patch.DisplayName != nil {
		row.DisplayName = *patch.DisplayName
	}
	if patch.APIKeyPresent {
		// api_key was present in the JSON body.
		if patch.APIKey == nil || *patch.APIKey == "" {
			// Explicit null or explicit empty string → clear the key.
			row.APIKeyEncrypted = nil
		} else {
			enc, err := crypto.EncryptStored(s.fernetKey, *patch.APIKey)
			if err != nil {
				slog.Warn("llm_providers: failed to encrypt api_key on update",
					"provider_id", id, "error_category", "crypto_encrypt")
				return LLMProviderOut{}, errors.New("failed to encrypt api_key")
			}
			row.APIKeyEncrypted = &enc
		}
	}
	// base_url uses double-pointer: nil = absent, non-nil = set (may point to nil = clear).
	if patch.BaseURL != nil {
		row.BaseURL = *patch.BaseURL
	}
	if patch.DefaultModel != nil {
		row.DefaultModel = *patch.DefaultModel
	}
	if patch.ModelPricing != nil {
		raw, err := marshalPricing(patch.ModelPricing)
		if err != nil {
			return LLMProviderOut{}, validation("model_pricing must be a JSON object")
		}
		row.ModelPricingRaw = raw
	}
	if patch.Enabled != nil {
		row.Enabled = *patch.Enabled
	}

	updated, err := s.dal.UpdateProvider(ctx, id, dal.LLMProviderToInput(row))
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderOut{}, ErrNotFound
		}
		return LLMProviderOut{}, err
	}
	return s.toOut(updated), nil
}

// ListForTenant returns the merged view of LLM providers for a tenant:
// tenant overrides win over platform defaults when name matches.
func (s *LLMProviderService) ListForTenant(ctx context.Context, tenantID string) ([]LLMProviderOut, error) {
	rows, err := s.dal.ListProvidersForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]LLMProviderOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.toOut(r))
	}
	return out, nil
}

// UpsertForTenant creates or replaces a tenant-scoped LLM provider override.
// name must match an existing platform provider name (validates against platform row).
// Returns ErrNotFound when name has no platform-default row.
// Returns ErrValidation for bad field values.
func (s *LLMProviderService) UpsertForTenant(ctx context.Context, tenantID, name string, body LLMProviderCreate) (LLMProviderOut, error) {
	if body.DefaultModel == "" {
		return LLMProviderOut{}, validation("default_model is required")
	}

	// Fetch the platform row to inherit display_name and model_pricing defaults.
	platform, err := s.dal.GetProviderByNamePlatform(ctx, name)
	if err != nil {
		if dal.IsNoRows(err) {
			return LLMProviderOut{}, ErrNotFound
		}
		return LLMProviderOut{}, err
	}

	displayName := platform.DisplayName
	if body.DisplayName != "" {
		displayName = body.DisplayName
	}

	var encryptedKey *string
	if body.APIKey != "" {
		enc, err := crypto.EncryptStored(s.fernetKey, body.APIKey)
		if err != nil {
			slog.Warn("llm_providers: failed to encrypt api_key for tenant override", "error_category", "crypto_encrypt")
			return LLMProviderOut{}, errors.New("failed to encrypt api_key")
		}
		encryptedKey = &enc
	}

	modelPricingRaw, err := marshalPricing(body.ModelPricing)
	if err != nil {
		return LLMProviderOut{}, validation("model_pricing must be a JSON object")
	}
	// Default to platform's model_pricing when not provided.
	if body.ModelPricing == nil {
		modelPricingRaw = platform.ModelPricingRaw
		if modelPricingRaw == nil {
			modelPricingRaw = []byte("{}")
		}
	}

	in := dal.LLMProviderInput{
		Name:            name,
		DisplayName:     displayName,
		APIKeyEncrypted: encryptedKey,
		BaseURL:         body.BaseURL,
		DefaultModel:    body.DefaultModel,
		ModelPricingRaw: modelPricingRaw,
		Enabled:         enabledOrDefault(body.Enabled),
	}

	row, err := s.dal.UpsertTenantProvider(ctx, tenantID, in)
	if err != nil {
		return LLMProviderOut{}, err
	}
	return s.toOut(row), nil
}

// Delete hard-deletes a provider. Returns ErrNotFound when the provider does not exist.
func (s *LLMProviderService) Delete(ctx context.Context, id int64) error {
	err := s.dal.DeleteProvider(ctx, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// toOut converts a DAL row to the HTTP response shape, decrypting the API key
// to produce the masked representation. Plaintext bytes are zeroed after masking.
func (s *LLMProviderService) toOut(row dal.LLMProvider) LLMProviderOut {
	keySet, masked := s.maskKey(row.APIKeyEncrypted)
	return LLMProviderOut{
		ID:           row.ID,
		Name:         row.Name,
		DisplayName:  row.DisplayName,
		APIKeySet:    keySet,
		APIKeyMasked: masked,
		BaseURL:      row.BaseURL,
		DefaultModel: row.DefaultModel,
		ModelPricing: dal.ModelPricingOrEmpty(row.ModelPricingRaw),
		Enabled:      row.Enabled,
		TenantID:     row.TenantID,
	}
}

// marshalPricing encodes a model_pricing map to JSONB-compatible bytes.
// A nil map is treated as {} (default). Returns an error only if the map
// contains values that cannot be marshalled.
func marshalPricing(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// maskKey mirrors Python's _mask_key:
//   - nil or empty → (false, nil)
//   - decrypt error → (true, "****")
//   - len(plain) <= 8 → (true, "****")
//   - len(plain) > 8  → (true, plain[:4] + "..." + plain[-4:])
//
// Decrypted plaintext bytes are zeroed immediately after the mask is computed.
func (s *LLMProviderService) maskKey(encrypted *string) (bool, *string) {
	if encrypted == nil || *encrypted == "" {
		return false, nil
	}

	// Decrypt to []byte so we can zero the buffer afterward.
	// DecryptStored strips the "enc:" prefix; we replicate that here with Decrypt.
	stored := *encrypted
	const prefix = "enc:"
	if len(stored) < len(prefix) || stored[:len(prefix)] != prefix {
		// No prefix: legacy unencrypted value — treat as key set but unreadable.
		slog.Warn("llm_providers: api_key has no enc: prefix", "error_category", "crypto_format")
		masked := "****"
		return true, &masked
	}
	token := stored[len(prefix):]
	plainBytes, err := crypto.Decrypt(s.fernetKey, token)
	if err != nil {
		// Decrypt failure: key is set but unreadable (wrong key or corrupted).
		slog.Warn("llm_providers: api_key decrypt failed", "error_category", "crypto_decrypt")
		masked := "****"
		return true, &masked
	}

	// Build the mask before zeroing.
	n := len(plainBytes)
	var masked string
	if n <= 8 {
		masked = "****"
	} else {
		masked = string(plainBytes[:4]) + "..." + string(plainBytes[n-4:])
	}

	// Zero plaintext bytes — defensive best-effort zeroing.
	for i := range plainBytes {
		plainBytes[i] = 0
	}

	return true, &masked
}
