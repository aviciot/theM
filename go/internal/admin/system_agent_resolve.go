package admin

// resolveSystemAgentRole resolves the effective provider/model/apiKey/baseURL/
// systemPrompt for a tenant's use of a system-agent role (classifier,
// card_synthesizer), honoring the tenant's mode choice:
//
//   - No row in them.tenant_system_agent_config yet, or mode="custom" with no
//     custom fields set: falls back to the platform-global them.config
//     ['system_agents'] row — today's behavior, unchanged.
//   - mode="custom" with fields set: uses those fields directly (never touches
//     the tenant's LLM Providers config).
//   - mode="general": resolves through the tenant's OWN them.llm_providers /
//     them.llm_provider_keys rows (provider_name + key_id, or that provider's
//     default key when key_id is nil). Hard rule: no platform-key fallback —
//     if the tenant has no usable key here, resolution fails and the caller
//     degrades gracefully (same as "role disabled" today). This function never
//     substitutes a platform-level key for a "general" mode resolution.
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

// resolvedSystemAgentRole is the effective, decrypted config for one role call.
type resolvedSystemAgentRole struct {
	Provider     string
	Model        string
	APIKey       string
	BaseURL      string
	SystemPrompt string // "" = caller uses its own built-in default prompt
}

// resolveSystemAgentRole resolves role for tenantID, given the platform-global
// fallback fields already loaded from them.config['system_agents'] (provider,
// model, decrypted apiKey, baseURL, systemPrompt — pass "" for any that were
// unset/disabled there).
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
		provider, err := d.GetProviderByNameForTenant(ctx, *cfg.ProviderName, tenantID)
		if err != nil {
			return resolvedSystemAgentRole{}, false
		}

		var key dal.LLMProviderKey
		if cfg.KeyID != nil {
			key, err = d.GetLLMProviderKey(ctx, *cfg.KeyID, &tenantID)
		} else {
			key, err = d.GetDefaultLLMProviderKey(ctx, provider.ID, &tenantID)
		}
		if err != nil {
			// No usable key for this tenant+provider — hard rule: never fall
			// back to a platform key. Degrade exactly like "role disabled".
			return resolvedSystemAgentRole{}, false
		}

		apiKey, err := crypto.DecryptStored(fernetKey, key.APIKeyEncrypted)
		if err != nil || apiKey == "" {
			return resolvedSystemAgentRole{}, false
		}

		model := provider.DefaultModel
		baseURL := ""
		if provider.BaseURL != nil {
			baseURL = *provider.BaseURL
		}

		return resolvedSystemAgentRole{
			Provider:     provider.Name,
			Model:        model,
			APIKey:       apiKey,
			BaseURL:      baseURL,
			SystemPrompt: "", // general mode has no per-role custom prompt; caller's own default applies
		}, true

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

// platformSystemAgentRoleResolverDAL is the DAL surface
// resolvePlatformSystemAgentRole needs, in addition to reading the
// them.config['system_agents'] row (done by the caller via GetConfig,
// already required by classifierDAL/synthesizerDAL).
type platformSystemAgentRoleResolverDAL interface {
	GetProviderByNamePlatform(ctx context.Context, name string) (dal.LLMProvider, error)
	GetDefaultLLMProviderKey(ctx context.Context, llmProviderID int64, tenantID *string) (dal.LLMProviderKey, error)
	GetLLMProviderKey(ctx context.Context, id int64, tenantID *string) (dal.LLMProviderKey, error)
}

// resolvePlatformSystemAgentRole resolves the effective provider/model/apiKey/
// baseURL/systemPrompt for the platform-global (super_admin) use of a
// system-agent role, honoring role.Mode the same way resolveSystemAgentRole
// does for tenants:
//   - mode="" or "custom": today's behavior — role.Provider/Model/
//     APIKeyEncrypted/BaseURL/SystemPrompt used directly.
//   - mode="general": resolves through the platform's OWN them.llm_providers
//     (tenant_id IS NULL) + them.llm_provider_keys (also tenant_id IS NULL,
//     added db/108) — role.Provider names which provider, role.KeyID selects
//     which named platform key (nil = that provider's default key).
//
// Returns ok=false when the role is disabled or no usable configuration
// exists — callers must degrade silently, exactly like today.
func resolvePlatformSystemAgentRole(ctx context.Context, d platformSystemAgentRoleResolverDAL, fernetKey []byte, role saRoleStored) (resolvedSystemAgentRole, bool) {
	if !role.Enabled {
		return resolvedSystemAgentRole{}, false
	}

	if role.Mode == "general" {
		if role.Provider == nil || *role.Provider == "" {
			return resolvedSystemAgentRole{}, false
		}
		provider, err := d.GetProviderByNamePlatform(ctx, *role.Provider)
		if err != nil {
			return resolvedSystemAgentRole{}, false
		}

		var key dal.LLMProviderKey
		if role.KeyID != nil {
			key, err = d.GetLLMProviderKey(ctx, *role.KeyID, nil)
		} else {
			key, err = d.GetDefaultLLMProviderKey(ctx, provider.ID, nil)
		}
		if err != nil {
			return resolvedSystemAgentRole{}, false
		}

		apiKey, err := crypto.DecryptStored(fernetKey, key.APIKeyEncrypted)
		if err != nil || apiKey == "" {
			return resolvedSystemAgentRole{}, false
		}

		baseURL := ""
		if provider.BaseURL != nil {
			baseURL = *provider.BaseURL
		}
		return resolvedSystemAgentRole{
			Provider: provider.Name,
			Model:    provider.DefaultModel,
			APIKey:   apiKey,
			BaseURL:  baseURL,
		}, true
	}

	// mode="" or "custom": today's behavior, unchanged.
	if role.Provider == nil || role.Model == nil || role.APIKeyEncrypted == nil {
		return resolvedSystemAgentRole{}, false
	}
	apiKey, err := crypto.DecryptStored(fernetKey, *role.APIKeyEncrypted)
	if err != nil || apiKey == "" {
		return resolvedSystemAgentRole{}, false
	}
	baseURL := ""
	if role.BaseURL != nil {
		baseURL = *role.BaseURL
	}
	systemPrompt := ""
	if role.SystemPrompt != nil {
		systemPrompt = *role.SystemPrompt
	}
	return resolvedSystemAgentRole{
		Provider:     *role.Provider,
		Model:        *role.Model,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		SystemPrompt: systemPrompt,
	}, true
}
