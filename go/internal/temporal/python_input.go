package temporal

// PythonOrchestrationInput is the wire-format input for the Python
// OrchestrationWorkflow. Field names mirror the Python OrchestrationInput
// dataclass exactly so the Temporal SDK's JSON codec round-trips correctly.
//
// run_id is intentionally absent — Python generates it internally via
// workflow.uuid4() inside the workflow function. We use the Temporal
// workflow ID (= our Go runID) as the external run identifier.
type PythonOrchestrationInput struct {
	OrchestratorName string         `json:"orchestrator_name"`
	UserMessage      string         `json:"user_message"`
	UserID           int64          `json:"user_id"`
	TokenPayload     map[string]any `json:"token_payload"`
	SessionID        string         `json:"session_id"`
	ContextID        string         `json:"context_id"`
	EntryPointSlug   string         `json:"entry_point_slug,omitempty"`
	HistoryWindow    int            `json:"history_window"`
	TokensUsedCarry  int            `json:"tokens_used_carry"`
	IterationCarry   int            `json:"iteration_carry"`
	Depth            int            `json:"depth"`
}
