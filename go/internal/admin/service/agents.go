package service

import (
	"context"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
)

// AgentService owns the business logic for agent CRUD.
type AgentService struct {
	dal   Dal
	cache Cache
}

// NewAgentService creates an AgentService.
func NewAgentService(d Dal, c Cache) *AgentService {
	return &AgentService{dal: d, cache: c}
}

// List returns all agents.
func (s *AgentService) List(ctx context.Context) ([]dal.Agent, error) {
	return s.dal.ListAgents(ctx)
}

// Get returns a single agent. Any DAL error maps to ErrNotFound to preserve the
// current API contract (handler today returns 404 on any error from GetAgent).
func (s *AgentService) Get(ctx context.Context, id string) (dal.Agent, error) {
	a, err := s.dal.GetAgent(ctx, id)
	if err != nil {
		return dal.Agent{}, ErrNotFound
	}
	return a, nil
}

// Create validates the input, applies defaults, persists, and invalidates cache.
func (s *AgentService) Create(ctx context.Context, in dal.AgentInput) (string, error) {
	if in.Slug == "" || in.DisplayName == "" {
		return "", validation("slug and display_name are required")
	}
	if in.Transport == "" {
		in.Transport = "a2a_async"
	}
	if in.MaxConcurrency <= 0 {
		in.MaxConcurrency = 5
	}
	if in.MaxRetries <= 0 {
		in.MaxRetries = 2
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 30
	}
	enabled := enabledOrDefault(in.Enabled)

	id, err := s.dal.CreateAgent(ctx, in, enabled)
	if err != nil {
		return "", err
	}
	s.invalidate(ctx)
	return id, nil
}

// Update applies defaults and persists changes, then invalidates cache.
func (s *AgentService) Update(ctx context.Context, id string, in dal.AgentInput) error {
	if in.MaxConcurrency <= 0 {
		in.MaxConcurrency = 5
	}
	enabled := enabledOrDefault(in.Enabled)
	if err := s.dal.UpdateAgent(ctx, id, in, enabled); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

// Delete removes an agent (soft-delete via DAL SQL) and invalidates cache.
func (s *AgentService) Delete(ctx context.Context, id string) error {
	if err := s.dal.DeleteAgent(ctx, id); err != nil {
		return err
	}
	s.invalidate(ctx)
	return nil
}

func (s *AgentService) invalidate(ctx context.Context) {
	if s.cache != nil {
		_ = s.cache.Del(ctx, fmt.Sprintf("them:agents:registry"))
	}
}
