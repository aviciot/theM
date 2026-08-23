package agentgen

// AppAgentBinding is the per-application instance of a reusable agent.
type AppAgentBinding struct {
	ID            string
	ApplicationID string
	AgentID       string
	// DefinitionID is nil while the binding is being drafted.
	// MUST be non-nil once the parent application is published.
	DefinitionID    *string
	ConfigOverrides map[string]any
	Policies        InvocationPolicies
}
