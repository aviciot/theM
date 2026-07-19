package temporal

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/aviciot/them/internal/domain"
)

// OrchestratorRunner is the interface activities use to call the orchestrator.
// Implemented by *orchestrator.Orchestrator; tests inject a fake.
type OrchestratorRunner interface {
	Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message) (string, error)
}

// Activities holds dependencies for Temporal activities.
type Activities struct {
	Runner OrchestratorRunner
}

// RunOrchestratorActivity calls the orchestrator agentic loop.
// It heartbeats every 5 s so Temporal can detect pod crashes.
//
// If the orchestrator returns ErrTaskInputRequired, the activity returns a
// Temporal ApplicationError with Type="TaskInputRequired" so the workflow
// can pause and wait for a human Signal.
func (a *Activities) RunOrchestratorActivity(ctx context.Context, input WorkflowInput) (WorkflowResult, error) {
	// Heartbeat goroutine — keeps the activity alive across long LLM calls.
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, "alive")
			case <-ctx.Done():
				return
			}
		}
	}()

	finalText, err := a.Runner.Run(ctx, input.RunID, input.ContextID, input.UserMessage, input.History)
	if err != nil {
		// Wrap as Temporal ApplicationError for typed error handling in the workflow.
		return WorkflowResult{Status: domain.RunStatusFailed},
			fmt.Errorf("RunOrchestratorActivity: %w", err)
	}

	return WorkflowResult{
		FinalText: finalText,
		Status:    domain.RunStatusCompleted,
	}, nil
}

