package temporal

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	temporalerr "go.temporal.io/sdk/temporal"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/temporal/workerconfig"
)

// OrchestratorRunner is the interface activities use to call the orchestrator.
// Implemented by *orchestrator.Orchestrator; tests inject a fake.
// The variadic runCtx carries optional per-run identity (R-3 artifact delivery).
type OrchestratorRunner interface {
	Run(ctx context.Context, runID, contextID string, userMsg domain.Message, history []domain.Message, runCtx ...orchestrator.RunContext) (string, error)
}

// OrchestratorFactory builds a per-run orchestrator from a loaded RunConfig.
// Implemented by runOrchestratorFactory in cmd/worker/main.go; tests may inject a fake.
type OrchestratorFactory interface {
	Build(cfg workerconfig.RunConfig) OrchestratorRunner
}

// Activities holds dependencies for Temporal activities.
type Activities struct {
	// Runner is the static fallback orchestrator used when AppOrchestratorID is absent.
	Runner OrchestratorRunner
	// ConfigLoader resolves per-run orchestrator config from DB. Optional.
	// When set (and WorkflowInput.AppOrchestratorID is non-empty), the activity
	// loads fresh config per-run and calls Factory.Build instead of using Runner.
	ConfigLoader workerconfig.Loader
	// Factory builds a per-run orchestrator from a loaded RunConfig. Optional.
	// Only used when ConfigLoader is also set.
	Factory OrchestratorFactory
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

	// Select runner: use per-run config when AppOrchestratorID is set and
	// ConfigLoader + Factory are wired; otherwise fall back to the static Runner.
	runner := a.Runner
	if input.AppOrchestratorID != "" && a.ConfigLoader != nil && a.Factory != nil {
		runCfg, cfgErr := a.ConfigLoader.LoadRunConfig(ctx, input.AppOrchestratorID, input.ApplicationID)
		if cfgErr != nil {
			return WorkflowResult{Status: domain.RunStatusFailed},
				temporalerr.NewNonRetryableApplicationError(
					"RunOrchestratorActivity: failed to load orchestrator config: "+cfgErr.Error(),
					"ConfigLoadError", cfgErr,
				)
		}
		runner = a.Factory.Build(runCfg)
	}

	finalText, err := runner.Run(ctx, input.RunID, input.ContextID, input.UserMessage, input.History,
		orchestrator.RunContext{
			TenantID:      input.TenantID,
			ApplicationID: input.ApplicationID,
		},
	)
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

