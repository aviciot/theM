// Package tenantctx carries validated tenant identity through a request context.
//
// Design constraints (TENANT_FOUNDATION_DECISIONS.md §1.2):
//   - TenantID is ONLY set from trusted authentication data (JWT claims or
//     access_tokens.tenant_id). It is NEVER read from request headers, query
//     parameters, or body fields.
//   - There is NO fallback to a bootstrap/default tenant when authentication is
//     missing or invalid. Absent authentication returns ErrNoTenant; invalid
//     authentication returns ErrInvalidTenant.
//   - A compatibility path for known legacy development access_tokens records is
//     allowed only in the DB querier layer (pgx_querier.go), not here.
//
// Usage:
//
//	// Middleware: store after authenticating
//	ctx = tenantctx.WithTenantID(ctx, tenantID)
//
//	// Handler / DAL: retrieve
//	id, err := tenantctx.TenantIDFromCtx(ctx)
package tenantctx

import (
	"context"
	"errors"
)

// ErrNoTenant is returned when no TenantID has been placed into the context.
// This indicates that the request did not carry valid authentication, or that
// the middleware chain did not run before the caller.
var ErrNoTenant = errors.New("tenantctx: no tenant identity in context")

// ErrInvalidTenant is returned when a TenantID was extracted from the
// authentication token but failed validation (e.g. empty string, not a UUID).
var ErrInvalidTenant = errors.New("tenantctx: invalid tenant identity")

// tenantKey is the unexported context key type. Using a named type prevents
// any collision with keys defined in other packages.
type tenantKey struct{}

// WithTenantID returns a new context with the given tenantID stored under the
// typed key. The caller must ensure tenantID is a non-empty validated UUID
// string before calling this function. Use ErrInvalidTenant for failed
// validation before calling WithTenantID.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// TenantIDFromCtx retrieves the TenantID from ctx.
// Returns ErrNoTenant if no TenantID is present.
// Returns ErrInvalidTenant if the stored value is empty (should not happen
// when callers use WithTenantID correctly, but guards against future misuse).
func TenantIDFromCtx(ctx context.Context) (string, error) {
	v, ok := ctx.Value(tenantKey{}).(string)
	if !ok {
		return "", ErrNoTenant
	}
	if v == "" {
		return "", ErrInvalidTenant
	}
	return v, nil
}

// MustTenantIDFromCtx retrieves the TenantID from ctx and panics if it is
// absent or invalid. Use only in handler code that is always wrapped by the
// tenant-requiring middleware — never in library code.
func MustTenantIDFromCtx(ctx context.Context) string {
	id, err := TenantIDFromCtx(ctx)
	if err != nil {
		panic("tenantctx: tenant identity required but not present: " + err.Error())
	}
	return id
}
