package agentgen

import "fmt"

const (
	// DefaultMaxConcurrentTasks is the per-run node concurrency limit used when
	// InvocationPolicies.MaxConcurrentTasks is 0 (unset).
	DefaultMaxConcurrentTasks = 10

	// SystemMaxConcurrentTasks is the hard upper bound on MaxConcurrentTasks.
	// Values above this are clamped down to prevent unbounded goroutine fan-out.
	SystemMaxConcurrentTasks = 100
)

// ResolveMaxConcurrentTasks returns the effective per-run node concurrency limit.
// Values <= 0 resolve to DefaultMaxConcurrentTasks; values > SystemMaxConcurrentTasks
// are clamped to SystemMaxConcurrentTasks.
func ResolveMaxConcurrentTasks(n int) int {
	if n <= 0 {
		return DefaultMaxConcurrentTasks
	}
	if n > SystemMaxConcurrentTasks {
		return SystemMaxConcurrentTasks
	}
	return n
}

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
	// AppGlobalParams holds decrypted app-level named params (used by HTTP app_param_ref).
	// Loaded from applications.app_params. NEVER logged or serialized.
	AppGlobalParams map[string]string // param name → plaintext value
	// NodeLLMOverrides holds per-node provider+model overrides set via RuntimeView.
	// Loaded from app_agent_bindings.config_overrides["llm_nodes"][node_id].
	// NEVER logged or serialized — cleared after the request.
	NodeLLMOverrides map[string]NodeLLMOverride // node_id → override
}

// NodeLLMOverride is the runtime provider+model override for one LLM canvas node.
type NodeLLMOverride struct {
	Provider string
	Model    string
}

// InvocationPolicies carries per-run execution limits.
// Zero values are replaced with safe defaults at execution time.
type InvocationPolicies struct {
	// MaxConcurrentTasks is the maximum number of DAG nodes that may execute
	// simultaneously within one run. 0 resolves to DefaultMaxConcurrentTasks (10).
	// Values above SystemMaxConcurrentTasks (100) are clamped down.
	// This applies to both LocalExecutor (goroutine semaphore) and
	// CanvasAgentWorkflow (per-workflow activity semaphore).
	MaxConcurrentTasks int      `json:"max_concurrent_tasks,omitempty"`
	AllowedSkillIDs    []string `json:"allowed_skill_ids,omitempty"` // nil = all skills allowed
	RateLimitPerMinute int      `json:"rate_limit_per_minute,omitempty"`
}

// String is deliberately redacted to prevent accidental credential logging.
func (ic InvocationContext) String() string {
	return fmt.Sprintf("InvocationContext{tenant=%s app=%s agent=%s binding=%s}",
		ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID)
}
