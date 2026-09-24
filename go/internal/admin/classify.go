package admin

// classifyAgent calls the Anthropic API to assign a category and icon to an
// agent based on its name, description, and skills. It is best-effort: any
// error returns ("", "") so the caller can continue without a category.
//
// Configuration is resolved through them.tenant_system_agent_config for the
// "classifier" role — first for the bootstrap tenant (the-M's own operating
// tenant since Platform-as-Tenant Phase 2, docs/PLATFORM_AS_TENANT_PLAN.md),
// whose resolved config is fed in as the fallback for the real tenant's own
// resolution. There is no more separate them.config['system_agents'] platform
// path.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aviciot/them/internal/tenantctx"
)

// classifierDAL is the minimal DAL surface classifyAgent needs.
type classifierDAL interface {
	systemAgentRoleResolverDAL
}

// classifierDefaultModel is the classifier's historical default model, used
// only when the bootstrap tenant's own "classifier" config resolves with a
// usable provider/key but no model set (custom mode never had a model field
// filled in) — matches the pre-Phase-2 behavior where the platform-global
// config's stored role got this default applied before resolution when
// mode != "general" and no model was stored.
const classifierDefaultModel = "claude-haiku-4-5-20251001"

var (
	classifierValidCategories = map[string]bool{
		"Research": true, "Coding": true, "Vision": true, "Security": true,
		"A2A": true, "Data": true, "Communication": true, "Agent": true,
	}
	classifierValidIcon = regexp.MustCompile(`^[a-zA-Z0-9_]{1,40}$`)
)

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
	// Resolve the bootstrap tenant's own "classifier" config first — this is
	// the "platform" fallback source used when the real tenant has no row /
	// mode="custom" with unset fields (unchanged prior behavior, just sourced
	// from the bootstrap tenant's tenant-scoped config instead of a separate
	// them.config['system_agents'] row). The bootstrap tenant's own call has
	// no further fallback tier, so its platform-fallback args are all "".
	platformResolved, _ := resolveSystemAgentRole(ctx, d, fernetKey, tenantctx.BootstrapTenantID, "classifier",
		"", "", "", "", "")
	if platformResolved.Provider != "" && platformResolved.APIKey != "" && platformResolved.Model == "" {
		// classifier's historical default, custom mode only (see
		// classifierDefaultModel doc comment).
		platformResolved.Model = classifierDefaultModel
	}

	resolved, ok := resolveSystemAgentRole(ctx, d, fernetKey, tenantID, "classifier",
		platformResolved.Provider, platformResolved.Model, platformResolved.APIKey, platformResolved.BaseURL, "")
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
