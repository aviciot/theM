package service

import (
	"context"
	"log/slog"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// validSystemAgentRoles gates which role names may have a tenant config row —
// classifier, card_synthesizer, and security_scanner, the three system-agent
// roles resolveSystemAgentRole (internal/admin/system_agent_resolve.go) supports.
var validSystemAgentRoles = map[string]bool{
	"classifier":       true,
	"card_synthesizer": true,
	"security_scanner": true,
}

// TenantSystemAgentConfigOut is the HTTP response shape. CustomAPIKeyMasked is
// a masked hint — the plaintext custom key is never returned.
type TenantSystemAgentConfigOut struct {
	Role               string  `json:"role"`
	Mode               string  `json:"mode"`
	ProviderName       *string `json:"provider_name"`
	KeyID              *int64  `json:"key_id"`
	GeneralModel       *string `json:"general_model"`
	CustomProvider     *string `json:"custom_provider"`
	CustomModel        *string `json:"custom_model"`
	CustomAPIKeyMasked *string `json:"custom_api_key_masked"`
	CustomBaseURL      *string `json:"custom_base_url"`
	CustomSystemPrompt *string `json:"custom_system_prompt"`
}

// TenantSystemAgentConfigIn is the PUT request body.
type TenantSystemAgentConfigIn struct {
	Mode               string  `json:"mode"`
	ProviderName       *string `json:"provider_name"`
	KeyID              *int64  `json:"key_id"`
	GeneralModel       *string `json:"general_model"`
	CustomProvider     *string `json:"custom_provider"`
	CustomModel        *string `json:"custom_model"`
	CustomAPIKey       *string `json:"custom_api_key"` // plaintext write-only; nil/blank = keep existing
	CustomBaseURL      *string `json:"custom_base_url"`
	CustomSystemPrompt *string `json:"custom_system_prompt"`
}

// TenantSystemAgentConfigService owns the business logic for per-tenant
// system-agent role config (general vs custom mode).
type TenantSystemAgentConfigService struct {
	dal       Dal
	fernetKey []byte
}

// NewTenantSystemAgentConfigService creates a TenantSystemAgentConfigService.
func NewTenantSystemAgentConfigService(d Dal, secretKey string) *TenantSystemAgentConfigService {
	return &TenantSystemAgentConfigService{
		dal:       d,
		fernetKey: crypto.DeriveKey(secretKey),
	}
}

// Get returns the tenant's config for role, or the "custom" zero-value default
// when no row exists yet (matches today's behavior before any tenant ever
// sets a mode — resolveSystemAgentRole treats that identically to "no row").
func (s *TenantSystemAgentConfigService) Get(ctx context.Context, tenantID, role string) (TenantSystemAgentConfigOut, error) {
	if !validSystemAgentRoles[role] {
		return TenantSystemAgentConfigOut{}, validation("unknown role")
	}
	row, err := s.dal.GetTenantSystemAgentConfig(ctx, tenantID, role)
	if err != nil {
		if dal.IsNoRows(err) {
			return TenantSystemAgentConfigOut{Role: role, Mode: "custom"}, nil
		}
		return TenantSystemAgentConfigOut{}, err
	}
	return s.toOut(row), nil
}

// Upsert creates or replaces the tenant's config for role.
func (s *TenantSystemAgentConfigService) Upsert(ctx context.Context, tenantID, role string, body TenantSystemAgentConfigIn) (TenantSystemAgentConfigOut, error) {
	if !validSystemAgentRoles[role] {
		return TenantSystemAgentConfigOut{}, validation("unknown role")
	}
	if body.Mode != "general" && body.Mode != "custom" {
		return TenantSystemAgentConfigOut{}, validation(`mode must be "general" or "custom"`)
	}

	in := dal.TenantSystemAgentConfigInput{
		TenantID: tenantID,
		Role:     role,
		Mode:     body.Mode,
	}

	if body.Mode == "general" {
		if body.ProviderName == nil || *body.ProviderName == "" {
			return TenantSystemAgentConfigOut{}, validation("provider_name is required for general mode")
		}
		in.ProviderName = body.ProviderName
		in.KeyID = body.KeyID

		if body.GeneralModel != nil && *body.GeneralModel != "" {
			provider, err := s.dal.GetProviderByNameForTenant(ctx, *body.ProviderName, tenantID)
			if err != nil {
				return TenantSystemAgentConfigOut{}, validation("unknown provider_name")
			}
			allowed := dal.AllowedModelsOrEmpty(provider.AllowedModelsRaw)
			if len(allowed) > 0 && !stringInSlice(*body.GeneralModel, allowed) {
				return TenantSystemAgentConfigOut{}, validation("general_model is not in this provider's allowed models")
			}
			in.GeneralModel = body.GeneralModel
		}
	} else {
		if body.CustomProvider == nil || *body.CustomProvider == "" ||
			body.CustomModel == nil || *body.CustomModel == "" {
			return TenantSystemAgentConfigOut{}, validation("custom_provider and custom_model are required for custom mode")
		}
		in.CustomProvider = body.CustomProvider
		in.CustomModel = body.CustomModel
		in.CustomBaseURL = body.CustomBaseURL
		in.CustomSystemPrompt = body.CustomSystemPrompt

		if body.CustomAPIKey != nil && *body.CustomAPIKey != "" {
			enc, err := crypto.EncryptStored(s.fernetKey, *body.CustomAPIKey)
			if err != nil {
				slog.Warn("tenant_system_agent_config: failed to encrypt custom_api_key", "error_category", "crypto_encrypt")
				return TenantSystemAgentConfigOut{}, unprocessable("failed to encrypt custom_api_key")
			}
			in.CustomAPIKeyEncrypted = &enc
		} else {
			// Preserve the existing encrypted key when the caller doesn't supply
			// a new one (e.g. only rotating the model or system_prompt).
			existing, err := s.dal.GetTenantSystemAgentConfig(ctx, tenantID, role)
			if err == nil {
				in.CustomAPIKeyEncrypted = existing.CustomAPIKeyEncrypted
			}
		}
		if in.CustomAPIKeyEncrypted == nil {
			return TenantSystemAgentConfigOut{}, validation("custom_api_key is required the first time custom mode is configured")
		}
	}

	row, err := s.dal.UpsertTenantSystemAgentConfig(ctx, in)
	if err != nil {
		return TenantSystemAgentConfigOut{}, err
	}
	return s.toOut(row), nil
}

// TenantSystemAgentConfigTest is the POST .../test request body — mirrors the
// platform-global TestLLM shape. Any field left empty falls back to the
// tenant's own stored custom_* value for role, never the platform config.
type TenantSystemAgentConfigTest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
}

// ResolveCustomTestInputs fills gaps in body from the tenant's own stored
// custom_* fields for role (decrypting the key when the body omits one).
// Returns ErrValidation when the resolved provider/model/key are still
// incomplete after filling gaps — same contract as the platform-global
// TestLLM handler.
func (s *TenantSystemAgentConfigService) ResolveCustomTestInputs(ctx context.Context, tenantID, role string, body TenantSystemAgentConfigTest) (provider, model, apiKey, baseURL string, err error) {
	if !validSystemAgentRoles[role] {
		return "", "", "", "", validation("unknown role")
	}

	row, rowErr := s.dal.GetTenantSystemAgentConfig(ctx, tenantID, role)
	stored := dal.TenantSystemAgentConfig{}
	if rowErr == nil {
		stored = row
	}

	provider = body.Provider
	if provider == "" && stored.CustomProvider != nil {
		provider = *stored.CustomProvider
	}
	model = body.Model
	if model == "" && stored.CustomModel != nil {
		model = *stored.CustomModel
	}
	baseURL = body.BaseURL
	if baseURL == "" && stored.CustomBaseURL != nil {
		baseURL = *stored.CustomBaseURL
	}

	apiKey = body.APIKey
	if apiKey == "" && stored.CustomAPIKeyEncrypted != nil {
		decrypted, decErr := crypto.DecryptStored(s.fernetKey, *stored.CustomAPIKeyEncrypted)
		if decErr == nil {
			apiKey = decrypted
		}
	}

	if provider == "" || model == "" {
		return "", "", "", "", validation("provider and model are required")
	}
	if apiKey == "" {
		return "", "", "", "", validation("no API key provided or stored for this role")
	}
	return provider, model, apiKey, baseURL, nil
}

func (s *TenantSystemAgentConfigService) toOut(row dal.TenantSystemAgentConfig) TenantSystemAgentConfigOut {
	out := TenantSystemAgentConfigOut{
		Role:               row.Role,
		Mode:               row.Mode,
		ProviderName:       row.ProviderName,
		KeyID:              row.KeyID,
		GeneralModel:       row.GeneralModel,
		CustomProvider:     row.CustomProvider,
		CustomModel:        row.CustomModel,
		CustomBaseURL:      row.CustomBaseURL,
		CustomSystemPrompt: row.CustomSystemPrompt,
	}
	if row.CustomAPIKeyEncrypted != nil && *row.CustomAPIKeyEncrypted != "" {
		hint := keyHintFor(s.fernetKey, *row.CustomAPIKeyEncrypted)
		out.CustomAPIKeyMasked = &hint
	}
	return out
}

// stringInSlice reports whether needle is present in haystack.
func stringInSlice(needle string, haystack []string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// keyHintFor decrypts the stored key and returns a masked representation:
// first 4 chars + 8 bullets + last 4 chars. Returns "" on any error.
func keyHintFor(fernetKey []byte, encrypted string) string {
	plain, err := crypto.DecryptStored(fernetKey, encrypted)
	if err != nil || len(plain) < 8 {
		return ""
	}
	return plain[:4] + "••••••••" + plain[len(plain)-4:]
}
