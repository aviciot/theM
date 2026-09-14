package roles

import "context"

// Service implements execution.RoleChecker using the DB Querier.
type Service struct {
	q Querier
}

// NewService creates a Service backed by the given Querier.
func NewService(q Querier) *Service {
	return &Service{q: q}
}

// ResolveRole loads the tenant's mapping rules and returns the first matching roleID.
func (s *Service) ResolveRole(ctx context.Context, tenantID string, claims map[string]string, headers map[string]string) (string, error) {
	mappings, err := s.q.ListMappings(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return ResolveRole(mappings, claims, headers), nil
}

// CheckGrant enforces the role gate for the application.
func (s *Service) CheckGrant(ctx context.Context, applicationID, roleID string) error {
	return CheckGrant(ctx, s.q, applicationID, roleID)
}
