package registry

import "context"

// Resolver is the design-time component definition resolver.
// It is NOT on the runtime hot path — used only at publish/compile time and by the Canvas palette.
//
// Resolution order per Section 14 of the architecture spec:
//  1. UUID fast path (if definitionID provided and resolves in this environment)
//  2. Portable identity fallback (kind, namespace, name, version)
//
// Tenant isolation rules:
//   - A builtin definition (scope=builtin, tenant_id=NULL) is accessible to ALL tenants.
//   - A tenant-scoped definition (scope=tenant) is accessible ONLY to its owning tenant.
//   - A definition with status=deprecated may NOT be newly pinned (blocked at publish).
//   - A disabled definition (enabled=false) is always rejected.
type Resolver struct {
	dal *PgxQuerier
}

// NewResolver creates a Resolver backed by the given DAL querier.
func NewResolver(q DBQuerier) *Resolver {
	return &Resolver{dal: NewPgxQuerier(q)}
}

// Resolve resolves a ComponentDefinition for the given tenant.
//
// If definitionID is non-empty it is tried first (UUID fast path).
// Falls back to portable ref lookup (kind, namespace, name, version).
//
// Returns ErrNotFound if the definition does not exist or is not accessible
// to the requesting tenant. Returns ErrDisabled if enabled=false.
// Note: deprecated definitions are returned by Resolve (allowed for read-only palette queries).
// Use ResolveForPublish to block deprecated definitions at the publish/compile pipeline.
func (r *Resolver) Resolve(ctx context.Context, tenantID string, ref DefinitionRef, definitionID string) (*ComponentDefinition, error) {
	var def *ComponentDefinition
	var err error

	// Fast path: UUID resolution
	if definitionID != "" {
		def, err = r.dal.ResolveByID(ctx, definitionID)
		if err != nil {
			// UUID miss — fall through to portable ref
			def = nil
		}
	}

	// Portable ref resolution (fallback or primary if no UUID)
	if def == nil {
		def, err = r.dal.ResolveByRef(ctx, ref)
		if err != nil {
			return nil, ErrNotFound
		}
	}

	// Tenant access check: tenant definitions are private to their owner
	if def.Scope == ScopeTenant && def.TenantID != tenantID {
		return nil, ErrNotFound // not found from the caller's perspective
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
