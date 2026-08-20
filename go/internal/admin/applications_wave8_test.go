package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/tenantctx"
)

// ── PutRuntime handler tests ───────────────────────────────────────────────────

// W8-H1: PutRuntime 200 on success.
func TestPutRuntime_Handler_200(t *testing.T) {
	// ExecReturning (for UpdateRuntimeConfig RETURNING) returns a string id.
	// Query (for ListAppOrchestratorNames) returns empty rows.
	db := &fakeDB{
		execRetStr:  "app-uuid",
		queryRows:   newFakeRows(nil),
	}
	body, _ := json.Marshal(map[string]any{
		"max_concurrent_sessions": 10,
		"blocked_tokens":          []string{},
		"blocked_user_ids":        []int{},
	})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/app-uuid/runtime", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(10), out["max_concurrent_sessions"])
}

// W8-H2: PutRuntime 404 when app not found (ExecReturning returns pgx.ErrNoRows).
func TestPutRuntime_Handler_404_NotFound(t *testing.T) {
	db := &fakeDB{execRetErr: pgx.ErrNoRows}
	body, _ := json.Marshal(map[string]any{})
	w := serveApps(t, db, nil, http.MethodPut, "/applications/missing-uuid/runtime", body)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// W8-H3: PutRuntime 400 on bad JSON.
func TestPutRuntime_Handler_400_BadJSON(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewApplicationsHandler(db, nil, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/runtime", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// W8-H4: PutRuntime — nil slices serialize as [] not null in response.
func TestPutRuntime_Handler_NilSlicesAsEmptyArrays(t *testing.T) {
	db := &fakeDB{
		execRetStr: "app-uuid",
		queryRows:  newFakeRows(nil),
	}
	body, _ := json.Marshal(map[string]any{}) // no blocked_tokens or blocked_user_ids
	w := serveApps(t, db, nil, http.MethodPut, "/applications/app-uuid/runtime", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.NotNil(t, out["blocked_tokens"], "blocked_tokens must be [] not null")
	assert.NotNil(t, out["blocked_user_ids"], "blocked_user_ids must be [] not null")
}

// ── BulkDelete handler tests ───────────────────────────────────────────────────

// serveAppsQuerier is like serveApps but accepts any admin.DBQuerier (not just *fakeDB).
func serveAppsQuerier(t *testing.T, db admin.DBQuerier, cache admin.CacheInvalidator, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewApplicationsHandler(db, cache, nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := tenantctx.WithTenantID(req.Context(), testTenantID)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.Routes(r)
	var bodyReader *strings.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// W8-H5: BulkDelete 200 with count on success.
func TestBulkDelete_Handler_200(t *testing.T) {
	// Query for ListAppOrchestratorNames (pre-fetch) returns empty, then
	// Query for BulkDeleteApplications (RETURNING) returns 2 deleted ids.
	callCount := 0
	db := &multiQueryFakeDB{
		queryFn: func(_ string) *fakeRows {
			callCount++
			if callCount == 1 {
				// ListAppOrchestratorNames — returns empty
				return newFakeRows(nil)
			}
			// BulkDeleteApplications RETURNING id — returns 2 rows
			return newFakeRows([][]any{{"id-1"}, {"id-2"}})
		},
	}
	body, _ := json.Marshal(map[string]any{
		"app_ids": []string{"id-1", "id-2"},
	})
	w := serveAppsQuerier(t, db, nil, http.MethodPost, "/applications/bulk-delete", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(2), out["deleted"])
}

// W8-H6: BulkDelete 400 on bad JSON.
func TestBulkDelete_Handler_400_BadJSON(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewApplicationsHandler(db, nil, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/applications/bulk-delete", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// W8-H7: BulkDelete 400 (validation) on > 200 IDs.
func TestBulkDelete_Handler_400_TooManyIDs(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "id"
	}
	body, _ := json.Marshal(map[string]any{"app_ids": ids})
	w := serveApps(t, db, nil, http.MethodPost, "/applications/bulk-delete", body)
	// 201 IDs → service.ErrValidation → 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// W8-H8: bulk-delete route registered before /{id} — "bulk-delete" is not treated as an id.
func TestBulkDelete_RouteNotMaskedByIDParam(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	body, _ := json.Marshal(map[string]any{"app_ids": []string{}})
	w := serveApps(t, db, nil, http.MethodPost, "/applications/bulk-delete", body)
	// An empty app_ids list returns 200 {"deleted":0}, not 404 or 405.
	assert.Equal(t, http.StatusOK, w.Code)
}

// ── multiQueryFakeDB ───────────────────────────────────────────────────────────

// multiQueryFakeDB allows per-call control of Query results for handler tests
// that exercise code paths making multiple Query calls.
type multiQueryFakeDB struct {
	queryFn     func(sql string) *fakeRows
	execRetStr  string
	execRetErr  error
}

func (f *multiQueryFakeDB) Query(_ context.Context, sql string, _ ...any) (admin.RowScanner, error) {
	if f.queryFn != nil {
		return f.queryFn(sql), nil
	}
	return newFakeRows(nil), nil
}

func (f *multiQueryFakeDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

func (f *multiQueryFakeDB) Exec(_ context.Context, _ string, _ ...any) error {
	return nil
}

func (f *multiQueryFakeDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if f.execRetErr != nil {
		return &fakeRow{err: f.execRetErr}
	}
	return &stringIDRow{id: f.execRetStr}
}
