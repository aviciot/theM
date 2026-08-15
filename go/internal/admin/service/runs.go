package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
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

// GetTasks returns tasks belonging to a run.
func (s *RunService) GetTasks(ctx context.Context, runID string) ([]dal.Task, error) {
	return s.dal.GetRunTasks(ctx, runID)
}

// GetArtifacts returns artifacts for a run via their tasks.
func (s *RunService) GetArtifacts(ctx context.Context, runID string) ([]dal.Artifact, error) {
	return s.dal.GetRunArtifacts(ctx, runID)
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

	// "ctx-" prefix matches Python's OrchestrationWorkflow registration convention.
	workflowID := "ctx-" + contextID

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	return s.temporal.SignalRun(ctx, workflowID, raw)
}
