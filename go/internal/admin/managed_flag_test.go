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

// managedFlagDB is a minimal DBQuerier for PatchManaged handler tests.
type managedFlagDB struct {
	execErr error
}

func (d *managedFlagDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}
func (d *managedFlagDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}
func (d *managedFlagDB) Exec(_ context.Context, _ string, _ ...any) error { return d.execErr }
func (d *managedFlagDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

func newManagedFlagRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	h := admin.NewApplicationsHandler(db, nil, nil, nil, nil)
	r.Patch("/applications/{id}/managed", h.PatchManaged)
	return r
}

// MA-01: PATCH /applications/{id}/managed {"is_managed":true} → 200 {is_managed:true}
func TestManagedFlag_SetManaged(t *testing.T) {
	r := newManagedFlagRouter(&managedFlagDB{})

	body := bytes.NewBufferString(`{"is_managed":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/applications/app-uuid-1/managed", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"is_managed":true`)
}

// MA-02: PATCH /applications/{id}/managed {"is_managed":false} → 200 {is_managed:false}
func TestManagedFlag_SetTenant(t *testing.T) {
	r := newManagedFlagRouter(&managedFlagDB{})

	body := bytes.NewBufferString(`{"is_managed":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/applications/app-uuid-1/managed", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"is_managed":false`)
}

// MA-03: DB error → 500.
func TestManagedFlag_DBError(t *testing.T) {
	r := newManagedFlagRouter(&managedFlagDB{execErr: errors.New("connection refused")})

	body := bytes.NewBufferString(`{"is_managed":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/applications/app-uuid-1/managed", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
