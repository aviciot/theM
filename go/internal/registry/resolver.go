package registry

import "context"

// Resolver is the design-time component definition resolver.
// Used at publish/compile time and by the Canvas palette.
//
// Resolution: kind + namespace + name + version, scoped to the target tenant.
// Builtins (scope=builtin) are accessible to all tenants.
// Tenant-scoped components are only accessible to their owning tenant — enforced by SQL.
// definition_id (UUID) is intentionally ignored: blueprints use stable portable refs.
type Resolver struct {
	dal *PgxQuerier
}

// NewResolver creates a Resolver backed by the given DAL querier.
func NewResolver(q DBQuerier) *Resolver {
	return &Resolver{dal: NewPgxQuerier(q)}
}

// Resolve resolves a ComponentDefinition for the given tenant by stable ref.
//
// Tenant isolation is enforced in SQL: tenant-scoped components are only returned
// when tenant_id matches. Builtins are accessible to all tenants.
//
// Returns ErrNotFound if the component does not exist or is not accessible to
// the requesting tenant. Returns ErrDisabled if enabled=false.
// Deprecated definitions are returned (allowed for read-only palette queries).
// Use ResolveForPublish to block deprecated definitions at publish time.
func (r *Resolver) Resolve(ctx context.Context, tenantID string, ref DefinitionRef, _ string) (*ComponentDefinition, error) {
	def, err := r.dal.ResolveByRef(ctx, ref, tenantID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !def.Enabled {
		return nil, ErrDisabled
	}
	return def, nil
}

// ResolveForPublish is like Resolve but also rejects deprecated definitions.
// Call this during the publish/compile pipeline; use Resolve for read-only palette queries.
func (r *Resolver) ResolveForPublish(ctx context.Context, tenantID string, ref DefinitionRef, definitionID string) (*ComponentDefinition, error) {
	def, err := r.Resolve(ctx, tenantID, ref, definitionID)
	if err != nil {
		return nil, err
	}
	if def.Status == StatusDeprecated {
		return nil, ErrDeprecated
	}
	return def, nil
}
