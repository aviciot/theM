package admin

// classifyAgent calls the Anthropic API to assign a category and icon to an
// agent based on its name, description, and skills. It is best-effort: any
// error returns ("", "") so the caller can continue without a category.
//
// Configuration is read from them.config row where config_key='system_agents'.
// Expected shape:
//
//	{
//	  "roles": {
//	    "classifier": {
//	      "enabled": true,
//	      "provider": "anthropic",
//	      "model": "claude-haiku-4-5-20251001",
//	      "api_key_encrypted": "enc:..."
//	    }
//	  }
//	}

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// classifierDAL is the minimal DAL surface classifyAgent needs.
type classifierDAL interface {
	GetConfig(ctx context.Context, key string) (*dal.ConfigRow, error)
	systemAgentRoleResolverDAL
}

var (
	classifierValidCategories = map[string]bool{
		"Research": true, "Coding": true, "Vision": true, "Security": true,
		"A2A": true, "Data": true, "Communication": true, "Agent": true,
	}
	classifierValidIcon = regexp.MustCompile(`^[a-zA-Z0-9_]{1,40}$`)
)

// classifierConfig is the unmarshalled shape of the 'system_agents' config row.
type classifierConfig struct {
	Roles struct {
		Classifier struct {
			Enabled          bool   `json:"enabled"`
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			APIKeyEncrypted  string `json:"api_key_encrypted"`
		} `json:"classifier"`
	} `json:"roles"`
}

// classifyAgent returns (category, icon) using the Anthropic classifier.
// Any failure silently returns ("", ""). tenantID selects the caller's own
// general/custom mode via resolveSystemAgentRole — see
// docs/TENANT_LLM_PROVIDERS_PLAN.md step 5. No platform-key fallback is ever
// used for a tenant's "general" mode resolution.
func classifyAgent(
	ctx context.Context,
	d classifierDAL,
	fernetKey []byte,
	tenantID string,
	displayName, description string,
	skills []any,
) (category, icon string) {
	// Load platform-global config first — used as the fallback when the tenant
	// has no row / mode="custom" with unset fields (unchanged prior behavior).
	var platformProvider, platformModel, platformAPIKey, platformBaseURL string
	if row, err := d.GetConfig(ctx, "system_agents"); err == nil && row != nil {
		var cfg classifierConfig
		if err := json.Unmarshal(row.Value, &cfg); err == nil && cfg.Roles.Classifier.Enabled {
			if apiKey, err := crypto.DecryptStored(fernetKey, cfg.Roles.Classifier.APIKeyEncrypted); err == nil && apiKey != "" {
				platformProvider = "anthropic" // classifier has always been Anthropic-only
				platformModel = cfg.Roles.Classifier.Model
				if platformModel == "" {
					platformModel = "claude-haiku-4-5-20251001"
				}
				platformAPIKey = apiKey
			}
		}
	}

	resolved, ok := resolveSystemAgentRole(ctx, d, fernetKey, tenantID, "classifier",
		platformProvider, platformModel, platformAPIKey, platformBaseURL, "")
	if !ok {
		return "", ""
	}

	// Build skill names list.
	var skillNames []string
	for _, s := range skills {
		if sm, ok := s.(map[string]any); ok {
			if name, ok := sm["name"].(string); ok && name != "" {
				skillNames = append(skillNames, name)
			}
		}
	}

	systemPrompt := "You are an agent classifier. Given an agent's name, description, and skills, return ONLY valid JSON:\n{\"category\": \"<one of: Research|Coding|Vision|Security|A2A|Data|Communication|Agent>\", \"icon\": \"<Material Symbols name, e.g. hub, code, search, visibility>\"}\nNo explanation, no markdown, just JSON."
	userMsg := fmt.Sprintf("Name: %s\nDescription: %s\nSkills: %s",
		displayName, description, strings.Join(skillNames, ", "))

	// Dispatch to the resolved provider — "general" mode may resolve to any
	// provider the tenant has configured, not just Anthropic (platform-global
	// fallback has always been Anthropic-only, so that path is unaffected).
	respText, err := dispatchLLMText(ctx, resolved.Provider, resolved.Model, resolved.APIKey, resolved.BaseURL, systemPrompt, userMsg)
	if err != nil || respText == "" {
		return "", ""
	}

	// Strip markdown fences if the model wrapped the JSON.
	text := strings.TrimSpace(respText)
	if idx := strings.Index(text, "{"); idx > 0 {
		text = text[idx:]
	}
	if idx := strings.LastIndex(text, "}"); idx >= 0 && idx < len(text)-1 {
		text = text[:idx+1]
	}

	// Parse the JSON result.
	var result struct {
		Category string `json:"category"`
		Icon     string `json:"icon"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return "", ""
	}

	// Validate category.
	if !classifierValidCategories[result.Category] {
		result.Category = "Agent"
	}

	// Validate icon.
	if !classifierValidIcon.MatchString(result.Icon) {
		result.Icon = "smart_toy"
	}

	return result.Category, result.Icon
}
