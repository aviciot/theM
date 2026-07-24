package service

import (
	"context"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
)

// OrchService owns the business logic for orchestrator CRUD.
type OrchService struct {
	dal   Dal
	cache Cache
}

// NewOrchService creates an OrchService.
func NewOrchService(d Dal, c Cache) *OrchService {
	return &OrchService{dal: d, cache: c}
}

// List returns all orchestrators.
func (s *OrchService) List(ctx context.Context) ([]dal.Orchestrator, error) {
	return s.dal.ListOrchestrators(ctx)
}

// Get returns a single orchestrator by name. Any DAL error maps to ErrNotFound
// to preserve the current API contract.
func (s *OrchService) Get(ctx context.Context, name string) (dal.Orchestrator, error) {
	o, err := s.dal.GetOrchestrator(ctx, name)
	if err != nil {
		return dal.Orchestrator{}, ErrNotFound
	}
	return o, nil
}

// Create validates the input, applies defaults, persists, and invalidates cache.
func (s *OrchService) Create(ctx context.Context, in dal.OrchestratorInput) (string, error) {
	if in.Name == "" {
		return "", validation("name is required")
	}
	if in.MaxIterations <= 0 {
		in.MaxIterations = 10
	}
	if in.HistoryWindow <= 0 {
		in.HistoryWindow = 20
	}
	enabled := enabledOrDefault(in.Enabled)

	id, err := s.dal.CreateOrchestrator(ctx, in, enabled)
	if err != nil {
		return "", err
	}
	s.invalidate(ctx, in.Name)
	return id, nil
}

// Update applies defaults and persists changes, then invalidates cache.
func (s *OrchService) Update(ctx context.Context, name string, in dal.OrchestratorInput) error {
	enabled := enabledOrDefault(in.Enabled)
	if err := s.dal.UpdateOrchestrator(ctx, name, in, enabled); err != nil {
		return err
	}
	s.invalidate(ctx, name)
	return nil
}

// Delete removes an orchestrator and invalidates cache.
func (s *OrchService) Delete(ctx context.Context, name string) error {
	if err := s.dal.DeleteOrchestrator(ctx, name); err != nil {
		return err
	}
	s.invalidate(ctx, name)
	return nil
}

func (s *OrchService) invalidate(ctx context.Context, name string) {
	if s.cache != nil {
		_ = s.cache.Del(ctx, fmt.Sprintf("them:orchestrators:%s", name))
	}
}
