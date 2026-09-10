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
)

// deployDB mocks DBQuerier for deploy handler tests.
// QueryRow returns a valid application row on the first call (the CTE),
// then empty rows for all subsequent calls (EPs, orchestrators, MCP creds).
type deployDB struct {
	callCount int
	rowErr    error
}

func (d *deployDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}

func (d *deployDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	d.callCount++
	if d.rowErr != nil {
		return &fakeRow{err: d.rowErr}
	}
	return &deployAppFakeRow{}
}

func (d *deployDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (d *deployDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

// deployAppFakeRow returns a plausible application scan: id, name, slug, tenant_slug, enabled, is_managed, revision, status.
type deployAppFakeRow struct{}

func (r *deployAppFakeRow) Scan(dest ...any) error {
	vals := []any{"new-app-uuid", "Test App", "test-app-abc123", "bank", true, false, nil, nil}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		if err := scanInto(d, vals[i]); err != nil {
			return err
		}
	}
	return nil
}

func newDeployRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	h := admin.NewApplicationsHandler(db, nil, nil, nil, nil)
	r.Post("/applications/{id}/deploy", h.DeployApplication)
	return r
}

// DA-01: POST /applications/{id}/deploy with valid body → 200 with application + checklist.
func TestDeployApplication_Success(t *testing.T) {
	r := newDeployRouter(&deployDB{})

	body := bytes.NewBufferString(`{"target_tenant_id":"target-tenant-uuid"}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/src-app-uuid/deploy", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"application"`)
	assert.Contains(t, w.Body.String(), `"checklist"`)
	assert.Contains(t, w.Body.String(), `"new-app-uuid"`)
	assert.Contains(t, w.Body.String(), `"llm_keys_required"`)
	assert.Contains(t, w.Body.String(), `"mcp_servers"`)
}

// DA-02: missing target_tenant_id → 400.
func TestDeployApplication_MissingTarget(t *testing.T) {
	r := newDeployRouter(&deployDB{})

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/src-app-uuid/deploy", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// DA-03: DB error on CTE → 500.
func TestDeployApplication_DBError(t *testing.T) {
	r := newDeployRouter(&deployDB{rowErr: errors.New("connection refused")})

	body := bytes.NewBufferString(`{"target_tenant_id":"target-tenant-uuid"}`)
	req := httptest.NewRequest(http.MethodPost, "/applications/src-app-uuid/deploy", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
