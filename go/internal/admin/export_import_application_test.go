package admin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/tenantctx"
)

// Phase 2 of docs/APP_CANVAS_CONFIG_COMPLETENESS_PLAN.md — ExportApplication/
// ImportApplication HTTP handler wiring. The real to_jsonb-based logic is
// proven against live Postgres in go/internal/admin/dal (see
// app_export_import_integration_test.go) — a fake DBQuerier can't execute
// Postgres-specific jsonb functions, same limitation Phase 1's tests hit.
// These tests only cover the handler-layer contract: request validation,
// success/error status codes, response shape.

// exportDB mocks DBQuerier for ExportApplication — returns a minimal,
// well-formed application row for the top-level to_jsonb SELECT and empty
// rows for every subsequent query (entry_points, orchestrators, scoped
// config, agents).
type exportDB struct {
	rowErr error
}

func (d *exportDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}

func (d *exportDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.rowErr != nil {
		return &fakeRow{err: d.rowErr}
	}
	return &exportAppFakeRow{}
}

func (d *exportDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (d *exportDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

// exportAppFakeRow returns a single to_jsonb(...) column — a valid, minimal
// JSON object, matching applicationExportCols' shape closely enough to
// unmarshal into dal.ExportedApp.Application without erroring.
type exportAppFakeRow struct{}

func (r *exportAppFakeRow) Scan(dest ...any) error {
	return scanInto(dest[0], []byte(`{"name":"test-app","enabled":true,"runtime_config":{},"canvas":null,"security_config":{},"app_params":{}}`))
}

func newExportRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	h := admin.NewApplicationsHandler(db, nil, nil, nil, nil)
	r.Get("/applications/{id}/export", h.ExportApplication)
	r.Post("/applications/import", h.ImportApplication)
	return r
}

// newTenantScopedExportRouter mounts the tenant-scoped variants, with a
// middleware that injects callerTenantID into the request context the same
// way AdminTenantMiddleware does in production (from the JWT's tenant_id
// claim) — no real JWT needed for this handler-layer test.
func newTenantScopedExportRouter(db admin.DBQuerier, callerTenantID string) *chi.Mux {
	r := chi.NewRouter()
	h := admin.NewApplicationsHandler(db, nil, nil, nil, nil)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := tenantctx.WithTenantID(req.Context(), callerTenantID)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/applications/{id}/export", h.ExportApplicationTenantScoped)
	r.Post("/applications/import", h.ImportApplicationTenantScoped)
	return r
}

// EI-01: GET /applications/{id}/export with a valid app → 200, JSON body,
// download headers set.
func TestExportApplication_Success(t *testing.T) {
	r := newExportRouter(&exportDB{})

	req := httptest.NewRequest(http.MethodGet, "/applications/src-app-uuid/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Body.String(), `"export_version"`)
	assert.Contains(t, w.Body.String(), `"application"`)
	assert.Contains(t, w.Body.String(), `"scoped_config"`)
}

// EI-02: GET /applications/{id}/export, DB error → 500.
func TestExportApplication_DBError(t *testing.T) {
	r := newExportRouter(&exportDB{rowErr: errors.New("connection refused")})

	req := httptest.NewRequest(http.MethodGet, "/applications/src-app-uuid/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// EI-03: POST /applications/import, missing target_tenant_id → 400.
func TestImportApplication_MissingTarget(t *testing.T) {
	r := newExportRouter(&exportDB{})

	body := bytes.NewBufferString(`{"export":{"export_version":1}}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// EI-04: POST /applications/import with a well-formed body → 200 (the
// legacyDAL/exportDB fake accepts any INSERT via Exec returning nil, and
// QueryRow's fake row satisfies every Scan call in the import path with the
// same minimal application JSON used for export).
func TestImportApplication_Success(t *testing.T) {
	r := newExportRouter(&exportDB{})

	body := bytes.NewBufferString(`{"target_tenant_id":"target-tenant-uuid","export":{"export_version":1,"application":{"name":"test-app","enabled":true}}}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"application"`)
}

// EI-05: POST /applications/import, unsupported export_version → 500 (the
// version check happens before any DB call, so this proves it's actually
// enforced, not just documented).
func TestImportApplication_UnsupportedVersion(t *testing.T) {
	r := newExportRouter(&exportDB{})

	body := bytes.NewBufferString(`{"target_tenant_id":"target-tenant-uuid","export":{"export_version":999}}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Tenant-scoped variants (own-tenant export/import for a regular tenant admin) ──

// EI-06: GET /applications/{id}/export (tenant-scoped) → 200, same shape as
// the platform-global export, just via legacyDAL (RLS-enforced querier).
func TestExportApplicationTenantScoped_Success(t *testing.T) {
	r := newTenantScopedExportRouter(&exportDB{}, "caller-tenant-uuid")

	req := httptest.NewRequest(http.MethodGet, "/applications/src-app-uuid/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"export_version"`)
}

// EI-07: a cross-tenant appID doesn't need special handling here — RLS on
// the underlying tenant-scoped querier is what actually enforces isolation
// (proven at the DAL/integration level, not here) — this test only proves
// the handler doesn't swallow or override a not-found error into a 200.
func TestExportApplicationTenantScoped_NotFound(t *testing.T) {
	r := newTenantScopedExportRouter(&exportDB{rowErr: errors.New("no rows in result set")}, "caller-tenant-uuid")

	req := httptest.NewRequest(http.MethodGet, "/applications/other-tenants-app/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code) // generic pgx error, not dal.IsNoRows-shaped in this fake
}

// EI-08 (security-critical): POST /applications/import (tenant-scoped) with
// a target_tenant_id in the body pointing at a DIFFERENT tenant than the
// caller's own — the caller's own tenant (from context) must win, not the
// body. This is the property that makes it safe to let a regular tenant
// admin call this endpoint at all.
func TestImportApplicationTenantScoped_IgnoresBodyTargetTenant(t *testing.T) {
	var capturedTenantArg string
	db := &tenantScopedImportCapturingDB{onExecArgs: func(args []any) {
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				capturedTenantArg = s
			}
		}
	}}
	r := newTenantScopedExportRouter(db, "caller-tenant-uuid")

	body := bytes.NewBufferString(`{"target_tenant_id":"attacker-controlled-other-tenant-uuid","export":{"export_version":1,"application":{"name":"test-app","enabled":true}}}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEqual(t, "attacker-controlled-other-tenant-uuid", capturedTenantArg,
		"the body's target_tenant_id must never be used — the caller's own tenant (from context) must always win")
}

// tenantScopedImportCapturingDB wraps exportDB's fakes but reports every
// Exec call's args to onExecArgs, so the test above can inspect which
// tenant_id ImportApplication's INSERTs actually used.
type tenantScopedImportCapturingDB struct {
	exportDB
	onExecArgs func(args []any)
}

func (d *tenantScopedImportCapturingDB) Exec(_ context.Context, _ string, args ...any) error {
	if d.onExecArgs != nil {
		d.onExecArgs(args)
	}
	return nil
}
