package execution

import (
	"context"

	"github.com/aviciot/them/internal/epconfig"
)

// externalCallerHasRole reports whether the external_jwt caller's mapped tenant role
// (claims/headers → tenant_role_mappings) is the EP's required_role. Fails closed:
// no checker, no matching mapping, or a lookup error all deny.
func (lc *Lifecycle) externalCallerHasRole(ctx context.Context, cfg *epconfig.EPConfig, req ExecutionRequest) bool {
	if lc.roleChecker == nil {
		return false
	}
	roleID, err := lc.roleChecker.ResolveRole(ctx, cfg.TenantID, req.ExternalClaims, req.RoleHeaders)
	if err != nil || roleID == "" {
		lc.logger.Debug("execution: required role unresolved", "ep_slug", req.EPSlug, "required_role", cfg.RequiredRole, "error", err)
		return false
	}
	name, err := lc.roleChecker.RoleName(ctx, roleID)
	if err != nil {
		lc.logger.Warn("execution: role name lookup failed", "ep_slug", req.EPSlug, "error", err)
		return false
	}
	return name == cfg.RequiredRole
}
