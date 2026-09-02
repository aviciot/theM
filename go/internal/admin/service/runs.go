package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/temporal"
)

// RunService owns the business logic for run reads and HITL signals.
type RunService struct {
	dal      Dal
	temporal Temporal
}

// NewRunService creates a RunService.
func NewRunService(d Dal, t Temporal) *RunService {
	return &RunService{dal: d, temporal: t}
}

// List returns runs for the given tenant, optionally filtered by contextID.
func (s *RunService) List(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Run, error) {
	return s.dal.ListRuns(ctx, tenantID, contextID, limit)
}

// Get returns a single run by ID scoped to the tenant. Any DAL error maps to ErrNotFound to
// preserve the current API contract (handlers today return 404 on any error from GetRun).
func (s *RunService) Get(ctx context.Context, tenantID, runID string) (dal.Run, error) {
	run, err := s.dal.GetRun(ctx, tenantID, runID)
	if err != nil {
		return dal.Run{}, ErrNotFound
	}
	return run, nil
}

// Stats returns aggregate run counts and cost for the tenant.
func (s *RunService) Stats(ctx context.Context, tenantID string) (dal.RunStats, error) {
	return s.dal.GetRunStats(ctx, tenantID)
}

// GetDetail returns a run with its steps, usage rows, and child runs.
// Any DAL error maps to ErrNotFound to preserve the API contract.
func (s *RunService) GetDetail(ctx context.Context, tenantID, runID string) (dal.RunDetail, error) {
	detail, err := s.dal.GetRunDetail(ctx, tenantID, runID)
	if err != nil {
		return dal.RunDetail{}, ErrNotFound
	}
	return detail, nil
}

// GetTasks returns tasks belonging to a run, scoped to the tenant.
func (s *RunService) GetTasks(ctx context.Context, tenantID, runID string) ([]dal.Task, error) {
	return s.dal.GetRunTasks(ctx, tenantID, runID)
}

// GetArtifacts returns artifacts for a run via their tasks, scoped to the tenant.
func (s *RunService) GetArtifacts(ctx context.Context, tenantID, runID string) ([]dal.Artifact, error) {
	return s.dal.GetRunArtifacts(ctx, tenantID, runID)
}

// ListContextSessions returns distinct conversation contexts for the tenant.
func (s *RunService) ListContextSessions(ctx context.Context, tenantID, orchestrator string, limit int) ([]dal.ContextSession, error) {
	return s.dal.ListContextSessions(ctx, tenantID, orchestrator, limit)
}

// GetContextArtifacts returns artifacts scoped to a context_id for the tenant.
func (s *RunService) GetContextArtifacts(ctx context.Context, tenantID, contextID string, limit int) ([]dal.Artifact, error) {
	return s.dal.GetContextArtifacts(ctx, tenantID, contextID, limit)
}

// GetContextMessages returns chat turn history for a context (user+agent messages only).
func (s *RunService) GetContextMessages(ctx context.Context, tenantID, contextID string, limit int) ([]dal.ContextMessage, error) {
	return s.dal.GetContextMessages(ctx, tenantID, contextID, limit)
}

// Cancel sets a running run to "canceled". Returns ErrNotFound when the run does not exist
// or belongs to another tenant, and ErrConflict when the run is not in "running" state
// (matching the Python 409 contract).
func (s *RunService) Cancel(ctx context.Context, tenantID, runID string) (dal.Run, error) {
	run, err := s.dal.CancelRun(ctx, tenantID, runID)
	if err != nil {
		if dal.IsNoRows(err) {
			// Either run does not exist/belong to tenant, or it is not "running".
			// Distinguish by fetching the run without the status filter.
			existing, getErr := s.dal.GetRun(ctx, tenantID, runID)
			if getErr != nil {
				return dal.Run{}, ErrNotFound
			}
			// Run exists but is not running → 409 Conflict.
			return existing, fmt.Errorf("%w: run is not running (status: %s)", ErrConflict, existing.Status)
		}
		return dal.Run{}, err
	}
	return run, nil
}

// Delete removes a single run scoped to the tenant. Returns ErrNotFound when the run
// does not exist or belongs to another tenant.
func (s *RunService) Delete(ctx context.Context, tenantID, runID string) error {
	err := s.dal.DeleteRun(ctx, tenantID, runID)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// BulkDelete removes up to 500 runs by ID, scoped to the tenant.
// Returns the number of rows actually deleted.
func (s *RunService) BulkDelete(ctx context.Context, tenantID string, runIDs []string) (int64, error) {
	if len(runIDs) > 500 {
		return 0, &FieldError{Kind: ErrValidation, Message: "max 500 run IDs per bulk-delete"}
	}
	return s.dal.BulkDeleteRuns(ctx, tenantID, runIDs)
}

// Signal sends a HITL payload to the Temporal workflow for the given run, scoped to the tenant.
// It constructs the workflow ID using the "ctx-{contextID}" convention that
// Python's OrchestrationWorkflow registers under.
func (s *RunService) Signal(ctx context.Context, tenantID, runID string, payload json.RawMessage) error {
	if s.temporal == nil {
		return ErrTemporalUnavailable
	}

	contextID, err := s.dal.GetRunContextID(ctx, tenantID, runID)
	if err != nil {
		if dal.IsNoRows(err) {
			return fmt.Errorf("%w: run not found or no root task", ErrNotFound)
		}
		return fmt.Errorf("db error: %w", err)
	}

	workflowID := temporal.WorkflowIDForContext(tenantID, contextID)

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	return s.temporal.SignalRun(ctx, workflowID, raw)
}
