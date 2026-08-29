package agentgen

import "context"

// ExecutionBackend executes a compiled ExecutionPlan.
// Implementations must be safe for concurrent calls with distinct plans.
// The same plan must produce identical observable results on both backends.
type ExecutionBackend interface {
	Execute(
		ctx     context.Context,
		ic      *InvocationContext,
		plan    *ExecutionPlan,
		initial PipelineVars,
	) (*ExecutionResult, error)
}
