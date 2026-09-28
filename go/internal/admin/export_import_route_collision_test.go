package admin_test

// Phase 2 follow-up (docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md) — a real
// route-collision risk: the tenant-scoped and platform-global export/import
// routes are registered via two SIBLING chi Group() calls sharing one
// routing tree under /admin, not two separately mounted sub-routers.
// Registering the identical method+path in both would silently let one
// shadow the other. This test proves both routes are independently
// reachable and gated by the correct role, using the tenant_http_test.go
// helpers (real HS256 JWTs through BuildRouter, not a bare handler test).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EIC-01: a tenant admin (role=admin, not super_admin) reaches the
// tenant-scoped /export-own route (200), proving it's mounted and reachable
// under RequireTenantAdmin without needing super_admin.
func TestExportImportRoutes_TenantAdmin_ReachesOwnTenantExport(t *testing.T) {
	cache, _ := newTHCache()
	jwt := thBuildAdminJWT(t, []byte(thJWTSecret), thBootstrapTenantID)
	srv := httptest.NewServer(tenantAdminRouterWithJWT(t, cache, jwt))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/applications/some-app-id/export-own", "")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode, "EIC-01: role=admin JWT must reach the tenant-scoped export-own route")
}

// EIC-02: the SAME tenant admin JWT must NOT be accepted by the
// platform-global /export route (RequireSuperAdmin) — proves the two routes
// really are gated independently, not accidentally sharing one path that
// only checks the more permissive rule.
func TestExportImportRoutes_TenantAdmin_RejectedFromPlatformGlobalExport(t *testing.T) {
	cache, _ := newTHCache()
	jwt := thBuildAdminJWT(t, []byte(thJWTSecret), thBootstrapTenantID)
	srv := httptest.NewServer(tenantAdminRouterWithJWT(t, cache, jwt))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/applications/some-app-id/export", "")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "EIC-02: role=admin (not super_admin) must be rejected from the platform-global export route")
}

// EIC-03: a super_admin JWT reaches the platform-global /export route (200)
// — confirms that route is still fully functional after adding the
// tenant-scoped sibling, i.e. neither route silently shadowed the other.
func TestExportImportRoutes_SuperAdmin_ReachesPlatformGlobalExport(t *testing.T) {
	cache, _ := newTHCache()
	jwt := thBuildHS256Token(t, []byte(thJWTSecret))
	srv := httptest.NewServer(tenantAdminRouterWithJWT(t, cache, jwt))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/applications/some-app-id/export", "")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode, "EIC-03: super_admin JWT must still reach the platform-global export route")
}

// EIC-04: a super_admin JWT can ALSO reach the tenant-scoped /export-own
// route — RequireTenantAdmin allows admin OR super_admin, so this is
// expected, not a bug; confirms the tenant-scoped route doesn't accidentally
// exclude super_admin.
func TestExportImportRoutes_SuperAdmin_AlsoReachesOwnTenantExport(t *testing.T) {
	cache, _ := newTHCache()
	jwt := thBuildHS256Token(t, []byte(thJWTSecret))
	srv := httptest.NewServer(tenantAdminRouterWithJWT(t, cache, jwt))
	defer srv.Close()

	resp, err := thGet(srv, "/admin/applications/some-app-id/export-own", "")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode, "EIC-04: super_admin JWT must also reach the tenant-scoped export-own route")
}
