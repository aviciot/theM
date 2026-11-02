package agentgen

import "context"

type runIDKey struct{}

// WithRunID returns a context carrying the id of the App Canvas run this agent invocation belongs to.
// The MCP caller reads it so tool-call events can be tied to the run (backlog F-127: the run id is the
// trace key). Empty ids leave the context unchanged.
func WithRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFromContext returns the run id set by WithRunID, or "" when the invocation has no run (for example
// a direct A2A call that did not come from an AppFlow agent node).
func RunIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}
