package temporal

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/orchestrator"
)

// OrchestratorRunner is the interface activities use to call the orchestrator.
// Implemented by *orchestrator.Orchestrator; tests inject a fake.
// The variadic runCtx carries optional per-run identity (R-3 artifact delivery).
type OrchestratorRunner interface {
	Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message, runCtx ...orchestrator.RunContext) (string, error)
}

// Activities holds dependencies for Temporal activities.
type Activities struct {
	Runner OrchestratorRunner
}

// RunOrchestratorActivity calls the orchestrator agentic loop.
// It heartbeats every 5 s so Temporal can detect pod crashes.
//
// R-4d: validates that TenantID, ApplicationID, and RunID are non-empty at the
// execution boundary. Returns a non-retryable ApplicationError if any is missing
// so the workflow fails fast with a clear message instead of producing an
// untenanted run.
//
// If the orchestrator returns ErrTaskInputRequired, the activity returns a
// Temporal ApplicationError with Type="TaskInputRequired" so the workflow
// can pause and wait for a human Signal.
func (a *Activities) RunOrchestratorActivity(ctx context.Context, input WorkflowInput) (WorkflowResult, error) {
	// Execution boundary validation (R-4d). Fail with a non-retryable error so
	// the workflow surfaces the misconfiguration immediately.
	if input.TenantID == "" {
		return WorkflowResult{Status: domain.RunStatusFailed},
			temporalerr.NewNonRetryableApplicationError(
				"RunOrchestratorActivity: TenantID must not be empty",
				"InvalidInput", nil,
			)
	}
	if input.ApplicationID == "" {
		return WorkflowResult{Status: domain.RunStatusFailed},
			temporalerr.NewNonRetryableApplicationError(
				"RunOrchestratorActivity: ApplicationID must not be empty",
				"InvalidInput", nil,
			)
	}
	if input.RunID == "" {
		return WorkflowResult{Status: domain.RunStatusFailed},
			temporalerr.NewNonRetryableApplicationError(
				"RunOrchestratorActivity: RunID must not be empty",
				"InvalidInput", nil,
			)
	}

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

