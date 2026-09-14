// Package roles implements the runtime role gate for the-M.
//
// Flow:
//  1. ResolveRole extracts the caller's role from JWT claims or M2M headers
//     by matching tenant_role_mappings rows.
//  2. CheckGrant verifies the resolved role has a tenant_role_grants row for
//     the requested application. If the app has no grants at all the check
//     is skipped (app is open).
package roles

import (
	"context"
	"errors"
)

// ErrRoleDenied is returned when an app has grants configured but the caller's
// role is not among them.
var ErrRoleDenied = errors.New("roles: caller role not granted access to this application")

// Mapping is one row from tenant_role_mappings.
type Mapping struct {
	Source string // "jwt_claim" | "header"
	Field  string // claim name or header name
	Value  string // value to match
	RoleID string // UUID of the matching tenant_role
}

// Querier is the DB interface the role gate needs.
type Querier interface {
	// ListMappings returns all claim/header → role mapping rules for the tenant.
	ListMappings(ctx context.Context, tenantID string) ([]Mapping, error)

	// AppHasGrants returns true when the application has any role grants configured.
	AppHasGrants(ctx context.Context, applicationID string) (bool, error)

	// RoleHasGrant returns true when the given role has a grant for the application.
	RoleHasGrant(ctx context.Context, roleID, applicationID string) (bool, error)
}

// ResolveRole walks the tenant's mapping rules and returns the first matching
// roleID. claims is a map of JWT claim name → value (string). headers is a
// map of header name (canonical form) → value. Returns "" when no rule matches.
func ResolveRole(mappings []Mapping, claims map[string]string, headers map[string]string) string {
	for _, m := range mappings {
		switch m.Source {
		case "jwt_claim":
			if v, ok := claims[m.Field]; ok && v == m.Value {
				return m.RoleID
			}
		case "header":
			if v, ok := headers[m.Field]; ok && v == m.Value {
				return m.RoleID
			}
		}
	}
	return ""
}

// CheckGrant enforces the role gate for one request.
//
//   - If the app has no grants configured → allow (open app).
//   - If the app has grants but roleID is "" → deny.
//   - If the app has grants and roleID is set → allow only when a matching grant exists.
func CheckGrant(ctx context.Context, q Querier, applicationID, roleID string) error {
	hasGrants, err := q.AppHasGrants(ctx, applicationID)
	if err != nil || !hasGrants {
		// DB error or open app — fail-open (no grants = no gate).
		return nil
	}
	if roleID == "" {
		return ErrRoleDenied
	}
	ok, err := q.RoleHasGrant(ctx, roleID, applicationID)
	if err != nil || !ok {
		return ErrRoleDenied
	}
	return nil
}
