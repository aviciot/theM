package agentgen

import "fmt"

// InvocationContext is built per request from the signed invocation JWT/headers.
// It is NEVER logged, NEVER serialized, NEVER written to Redis or Temporal history.
type InvocationContext struct {
	TenantID        string
	ApplicationID   string
	AgentID         string
	BindingID       string
	Spec            *AgentSpec
	ConfigOverrides map[string]any
	Policies        InvocationPolicies
	// AppAPIKey is the per-app LLM provider key decrypted from applications.provider_keys.
	// NEVER logged or serialized — cleared after the request.
	AppAPIKey map[string]string // provider → plaintext key (e.g. "anthropic" → "sk-ant-...")
	// AgentParams holds resolved plaintext values for all declared agent parameters.
	// Secrets are decrypted from app_agent_bindings.agent_params before this map is populated.
	// NEVER logged or serialized — cleared after the request.
	AgentParams map[string]string // param key → plaintext value
	// AppGlobalParams holds decrypted values for all app-level named params referenced by
	// this agent's compiled spec (via AppParamRefs). Loaded from applications.app_params.
	// NEVER logged or serialized — cleared after the request.
	AppGlobalParams map[string]string // param name → plaintext value
}

type InvocationPolicies struct {
	MaxConcurrentTasks int      `json:"max_concurrent_tasks,omitempty"`
	AllowedSkillIDs    []string `json:"allowed_skill_ids,omitempty"` // nil = all skills allowed
	RateLimitPerMinute int      `json:"rate_limit_per_minute,omitempty"`
}

// String is deliberately redacted to prevent accidental credential logging.
func (ic InvocationContext) String() string {
	return fmt.Sprintf("InvocationContext{tenant=%s app=%s agent=%s binding=%s}",
		ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID)
}
