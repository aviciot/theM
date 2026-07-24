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

// List returns runs, optionally filtered by contextID.
func (s *RunService) List(ctx context.Context, contextID string, limit int) ([]dal.Run, error) {
	return s.dal.ListRuns(ctx, contextID, limit)
}

// Get returns a single run by ID. Any DAL error maps to ErrNotFound to preserve
// the current API contract (handlers today return 404 on any error from GetRun).
func (s *RunService) Get(ctx context.Context, runID string) (dal.Run, error) {
	run, err := s.dal.GetRun(ctx, runID)
	if err != nil {
		return dal.Run{}, ErrNotFound
	}
	return run, nil
}

// Signal sends a HITL payload to the Temporal workflow for the given run.
// It constructs the workflow ID using the "ctx-{contextID}" convention that
// Python's OrchestrationWorkflow registers under.
func (s *RunService) Signal(ctx context.Context, runID string, payload json.RawMessage) error {
	if s.temporal == nil {
		return ErrTemporalUnavailable
	}

	contextID, err := s.dal.GetRunContextID(ctx, runID)
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
