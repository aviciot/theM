package agentgen

import "fmt"

// InvocationContext is built per request from the signed invocation JWT/headers.
// Credentials holds DECRYPTED values and lives only for the duration of one request.
// It is NEVER logged, NEVER serialized, NEVER written to Redis or Temporal history.
type InvocationContext struct {
	TenantID        string
	ApplicationID   string
	AgentID         string
	BindingID       string
	Spec            *AgentSpec
	Credentials     map[string]string // slot_name → decrypted value (in-memory only)
	ConfigOverrides map[string]any
	Policies        InvocationPolicies
}

type InvocationPolicies struct {
	MaxConcurrentTasks int
	AllowedSkillIDs    []string // nil = all skills allowed
	RateLimitPerMinute int
}

// String is deliberately redacted to prevent accidental credential logging.
func (ic InvocationContext) String() string {
	return fmt.Sprintf("InvocationContext{tenant=%s app=%s agent=%s binding=%s slots=%d}",
		ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID, len(ic.Credentials))
}
