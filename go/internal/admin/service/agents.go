package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

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

// List returns all agents for the given tenant.
func (s *AgentService) List(ctx context.Context, tenantID string) ([]dal.Agent, error) {
	return s.dal.ListAgents(ctx, tenantID)
}

// Get returns a single agent scoped to the tenant. Any DAL error maps to ErrNotFound to preserve
// the current API contract (handler today returns 404 on any error from GetAgent).
func (s *AgentService) Get(ctx context.Context, tenantID, id string) (dal.Agent, error) {
	a, err := s.dal.GetAgent(ctx, tenantID, id)
	if err != nil {
		return dal.Agent{}, ErrNotFound
	}
	return a, nil
}

// Create validates the input, applies defaults, persists under the tenant, and invalidates cache.
func (s *AgentService) Create(ctx context.Context, tenantID string, in dal.AgentInput) (string, error) {
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

	if err := checkResourceQuota(ctx, s.dal, tenantID, func(q dal.TenantQuota) *int { return q.MaxAgents },
		func() (int, error) { return s.dal.CountAgents(ctx, tenantID) }); err != nil {
		return "", err
	}

	id, err := s.dal.CreateAgent(ctx, tenantID, in, enabled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrConflict
		}
		return "", err
	}
	s.invalidate(ctx, tenantID)
	return id, nil
}

// Update applies defaults and persists changes scoped to the tenant, then invalidates cache.
func (s *AgentService) Update(ctx context.Context, tenantID, id string, in dal.AgentInput) error {
	if in.MaxConcurrency <= 0 {
		in.MaxConcurrency = 5
	}
	if in.Transport == "" {
		in.Transport = "a2a_async"
	}
	enabled := enabledOrDefault(in.Enabled)
	if err := s.dal.UpdateAgent(ctx, tenantID, id, in, enabled); err != nil {
		return err
	}
	s.invalidate(ctx, tenantID)
	return nil
}

// Delete removes an agent scoped to the tenant (soft-delete via DAL SQL) and invalidates cache.
func (s *AgentService) Delete(ctx context.Context, tenantID, id string) error {
	if err := s.dal.DeleteAgent(ctx, tenantID, id); err != nil {
		return err
	}
	s.invalidate(ctx, tenantID)
	return nil
}

func (s *AgentService) invalidate(ctx context.Context, tenantID string) {
	if s.cache == nil {
		return
	}
	// Delete the per-tenant L2 Redis cache bucket (SEC-03).
	// Key format matches agentregistry.redisCacheKeyFmt.
	_ = s.cache.Del(ctx, fmt.Sprintf("them:agents:registry:%s", tenantID))
	// Publish the tenantID so every pod's L1 evicts only that tenant's entries.
	// Payload is the tenantID UUID string — registry.invalidateTenant uses it to
	// evict only keys prefixed by "{tenantID}:", leaving other tenants untouched.
	_ = s.cache.Publish(ctx, "them:agents:changed", tenantID)
}
