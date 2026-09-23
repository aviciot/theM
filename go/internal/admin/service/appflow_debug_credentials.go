package service

import (
	"context"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/debugcred"
)

// AppFlowDebugCredentialDAL is the minimal DAL surface needed to resolve a
// debug run's per-node LLM credential overrides. Deliberately narrow (not the
// full service.Dal) — same reasoning as AppFlowDebugDAL's own comment.
type AppFlowDebugCredentialDAL interface {
	GetProviderByNameForTenant(ctx context.Context, name, tenantID string) (dal.LLMProvider, error)
	GetLLMProviderKey(ctx context.Context, id int64, tenantID *string) (dal.LLMProviderKey, error)
	GetDefaultLLMProviderKey(ctx context.Context, llmProviderID int64, tenantID *string) (dal.LLMProviderKey, error)
}

// LLMOverrideInput is one entry of a debug/start request's llm_overrides map
// (node_id -> override), per docs/APPFLOW_RUNTIME_PARAMS_PLAN.md.
type LLMOverrideInput struct {
	Mode     string // "general" | "custom"
	Provider string // required for both modes
	KeyID    *int64 // general mode, optional (nil = tenant default key)
	Model    string // custom mode
	APIKey   string // custom mode
	BaseURL  string // custom mode
}

// resolveLLMOverride turns one LLMOverrideInput into a plaintext debugcred.Override.
// General mode: reuses the same them.llm_provider_keys lookup
// resolveSystemAgentRole (internal/admin) already does for Classifier/
// Synthesizer's General mode — provider name + optional key_id, tenant-scoped,
// never a platform-key fallback (that hard rule is enforced identically here:
// GetProviderByNameForTenant/GetDefaultLLMProviderKey/GetLLMProviderKey all
// take tenantID and only ever return that tenant's own rows).
// Custom mode: the literal APIKey from the request — never touches the DB.
func resolveLLMOverride(ctx context.Context, d AppFlowDebugCredentialDAL, fernetKey []byte, tenantID string, in LLMOverrideInput) (debugcred.Override, error) {
	switch in.Mode {
	case "custom":
		if in.Provider == "" {
			return debugcred.Override{}, unprocessable("custom mode: provider is required")
		}
		if in.APIKey == "" {
			return debugcred.Override{}, unprocessable("custom mode: api_key is required")
		}
		return debugcred.Override{Provider: in.Provider, Model: in.Model, APIKey: in.APIKey, BaseURL: in.BaseURL}, nil

	case "general":
		if in.Provider == "" {
			return debugcred.Override{}, unprocessable("general mode: provider is required")
		}
		provider, err := d.GetProviderByNameForTenant(ctx, in.Provider, tenantID)
		if err != nil {
			return debugcred.Override{}, unprocessable(fmt.Sprintf("general mode: no %q provider configured for this tenant", in.Provider))
		}

		var key dal.LLMProviderKey
		if in.KeyID != nil {
			key, err = d.GetLLMProviderKey(ctx, *in.KeyID, &tenantID)
		} else {
			key, err = d.GetDefaultLLMProviderKey(ctx, provider.ID, &tenantID)
		}
		if err != nil {
			// Hard rule, same as resolveSystemAgentRole: never fall back to a
			// platform key. No usable tenant key here is a clear failure, not
			// a silent substitution.
			return debugcred.Override{}, unprocessable(fmt.Sprintf("general mode: no usable key for provider %q on this tenant", in.Provider))
		}

		apiKey, err := crypto.DecryptStored(fernetKey, key.APIKeyEncrypted)
		if err != nil || apiKey == "" {
			return debugcred.Override{}, unprocessable(fmt.Sprintf("general mode: could not decrypt key for provider %q", in.Provider))
		}

		baseURL := ""
		if provider.BaseURL != nil {
			baseURL = *provider.BaseURL
		}
		return debugcred.Override{Provider: provider.Name, Model: provider.DefaultModel, APIKey: apiKey, BaseURL: baseURL}, nil

	default:
		return debugcred.Override{}, unprocessable(fmt.Sprintf("llm_overrides: unknown mode %q (want \"general\" or \"custom\")", in.Mode))
	}
}

// llmCredentialNodeIDs returns the IDs of every node in spec that declares a
// required "llm_credential" runtime param — currently just every "llm" kind
// node, per appflow.RuntimeParams on the llm entry in noderegistry.go. Kept
// as its own function (rather than inlined into Start) so the "which nodes
// need a credential" question has one answer shared by both validation and
// override-writing, instead of two independently-maintained node-kind checks.
func llmCredentialNodeIDs(spec *appflow.AppFlowSpec) []string {
	var ids []string
	for _, ep := range spec.EntryPoints {
		for _, n := range ep.Nodes {
			if n.Kind == "llm" {
				ids = append(ids, n.ID)
			}
		}
	}
	return ids
}
