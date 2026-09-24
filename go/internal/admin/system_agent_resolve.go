package admin

// resolveSystemAgentRole resolves the effective provider/model/apiKey/baseURL/
// systemPrompt for a tenant's use of a system-agent role (classifier,
// card_synthesizer, security_scanner), honoring the tenant's mode choice:
//
//   - No row in them.tenant_system_agent_config yet, or mode="custom" with no
//     custom fields set: falls back to the "platform" fields the caller
//     passes in (platformProvider/platformModel/platformAPIKey/...). Since
//     Platform-as-Tenant Phase 2 (docs/PLATFORM_AS_TENANT_PLAN.md), these
//     fields are themselves produced by resolving the bootstrap tenant's own
//     them.tenant_system_agent_config row for the same role through this
//     exact function — there is no more separate NULL-tenant/them.config
//     platform concept. Callers (classify.go/synthesize.go/
//     security_scan_llm.go) do this by calling resolveSystemAgentRole once
//     for the bootstrap tenant and feeding its result in as the fallback args
//     of the second call for the real tenant.
//   - mode="custom" with fields set: uses those fields directly (never touches
//     the tenant's LLM Providers config).
//   - mode="general": resolves through the tenant's OWN them.llm_providers /
//     them.llm_provider_keys rows (provider_name + key_id, or that provider's
//     default key when key_id is nil). Hard rule: no platform-key fallback —
//     if the tenant has no usable key here, resolution fails and the caller
//     degrades gracefully (same as "role disabled" today). This function never
//     substitutes a platform-level (bootstrap tenant's) key for a "general"
//     mode resolution.
//
// Returns ok=false when no usable configuration exists — callers must treat
// this exactly like "role disabled" (silent no-op / degrade), never an error
// surfaced to the end user as a hard failure.

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// systemAgentRoleResolverDAL is the DAL surface resolveSystemAgentRole needs.
type systemAgentRoleResolverDAL interface {
	GetTenantSystemAgentConfig(ctx context.Context, tenantID, role string) (dal.TenantSystemAgentConfig, error)
	GetProviderByNameForTenant(ctx context.Context, name, tenantID string) (dal.LLMProvider, error)
	GetDefaultLLMProviderKey(ctx context.Context, llmProviderID int64, tenantID *string) (dal.LLMProviderKey, error)
	GetLLMProviderKey(ctx context.Context, id int64, tenantID *string) (dal.LLMProviderKey, error)
}

// resolveGeneralMode is the shared "General mode" resolution used by
// resolveSystemAgentRole for any tenant (including the bootstrap tenant, when
// called as the fallback source — see resolveSystemAgentRole's doc comment):
// look up the named provider, then its chosen key (or default key when keyID
// is nil), decrypt it, and fall back to the provider's default_model when
// generalModel is unset. Callers scope getProvider/getDefaultKey/getKey to
// the relevant tenant's rows via closures. Returns ok=false on any
// lookup/decrypt failure — callers must degrade silently, never substituting
// a different tenant's key.
func resolveGeneralMode(
	fernetKey []byte,
	getProvider func() (dal.LLMProvider, error),
	getDefaultKey func(llmProviderID int64) (dal.LLMProviderKey, error),
	getKey func(id int64) (dal.LLMProviderKey, error),
	keyID *int64,
	generalModel *string,
) (resolvedSystemAgentRole, bool) {
	provider, err := getProvider()
	if err != nil {
		return resolvedSystemAgentRole{}, false
	}

	var key dal.LLMProviderKey
	if keyID != nil {
		key, err = getKey(*keyID)
	} else {
		key, err = getDefaultKey(provider.ID)
	}
	if err != nil {
		// No usable key at this scope — hard rule: never fall back to a
		// different scope's key. Degrade exactly like "role disabled".
		return resolvedSystemAgentRole{}, false
	}

	apiKey, err := crypto.DecryptStored(fernetKey, key.APIKeyEncrypted)
	if err != nil || apiKey == "" {
		return resolvedSystemAgentRole{}, false
	}

	model := provider.DefaultModel
	if generalModel != nil && *generalModel != "" {
		model = *generalModel
	}
	baseURL := ""
	if provider.BaseURL != nil {
		baseURL = *provider.BaseURL
	}

	return resolvedSystemAgentRole{
		Provider: provider.Name,
		Model:    model,
		APIKey:   apiKey,
		BaseURL:  baseURL,
	}, true
}

// resolvedSystemAgentRole is the effective, decrypted config for one role call.
type resolvedSystemAgentRole struct {
	Provider     string
	Model        string
	APIKey       string
	BaseURL      string
	SystemPrompt string // "" = caller uses its own built-in default prompt
}

// resolveSystemAgentRole resolves role for tenantID, given the "platform"
// fallback fields (provider, model, decrypted apiKey, baseURL, systemPrompt —
// pass "" for any that are unavailable). Since Platform-as-Tenant Phase 2,
// callers source these fallback fields from resolving the bootstrap tenant's
// own config for the same role through this same function, not from a
// separate them.config['system_agents'] row.
func resolveSystemAgentRole(
	ctx context.Context,
	d systemAgentRoleResolverDAL,
	fernetKey []byte,
	tenantID, role string,
	platformProvider, platformModel, platformAPIKey, platformBaseURL, platformSystemPrompt string,
) (resolvedSystemAgentRole, bool) {
	cfg, err := d.GetTenantSystemAgentConfig(ctx, tenantID, role)
	if err != nil {
		// No row (pgx.ErrNoRows) or any other lookup failure: fall back to
		// platform-global config exactly as before this feature existed.
		return platformFallback(platformProvider, platformModel, platformAPIKey, platformBaseURL, platformSystemPrompt)
	}

	switch cfg.Mode {
	case "general":
		if cfg.ProviderName == nil || *cfg.ProviderName == "" {
			return resolvedSystemAgentRole{}, false
		}
		return resolveGeneralMode(
			fernetKey,
			func() (dal.LLMProvider, error) { return d.GetProviderByNameForTenant(ctx, *cfg.ProviderName, tenantID) },
			func(llmProviderID int64) (dal.LLMProviderKey, error) { return d.GetDefaultLLMProviderKey(ctx, llmProviderID, &tenantID) },
			func(id int64) (dal.LLMProviderKey, error) { return d.GetLLMProviderKey(ctx, id, &tenantID) },
			cfg.KeyID, cfg.GeneralModel,
		)

	case "custom":
		if cfg.CustomProvider == nil || cfg.CustomModel == nil || cfg.CustomAPIKeyEncrypted == nil {
			// Tenant has a row but never filled in custom fields — fall back
			// to platform-global, same as "no row".
			return platformFallback(platformProvider, platformModel, platformAPIKey, platformBaseURL, platformSystemPrompt)
		}
		apiKey, err := crypto.DecryptStored(fernetKey, *cfg.CustomAPIKeyEncrypted)
		if err != nil || apiKey == "" {
			return resolvedSystemAgentRole{}, false
		}
		baseURL := ""
		if cfg.CustomBaseURL != nil {
			baseURL = *cfg.CustomBaseURL
		}
		systemPrompt := ""
		if cfg.CustomSystemPrompt != nil {
			systemPrompt = *cfg.CustomSystemPrompt
		}
		return resolvedSystemAgentRole{
			Provider:     *cfg.CustomProvider,
			Model:        *cfg.CustomModel,
			APIKey:       apiKey,
			BaseURL:      baseURL,
			SystemPrompt: systemPrompt,
		}, true

	default:
		return platformFallback(platformProvider, platformModel, platformAPIKey, platformBaseURL, platformSystemPrompt)
	}
}

func platformFallback(provider, model, apiKey, baseURL, systemPrompt string) (resolvedSystemAgentRole, bool) {
	if provider == "" || model == "" || apiKey == "" {
		return resolvedSystemAgentRole{}, false
	}
	return resolvedSystemAgentRole{
		Provider:     provider,
		Model:        model,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		SystemPrompt: systemPrompt,
	}, true
}
