package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// obsQuerier is a minimal admin.DBQuerier for observability tests.
// Query returns the configured rows or error; other methods are no-ops.
type obsQuerier struct {
	rows     *fakeRows
	queryErr error
}

func (d *obsQuerier) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	if d.queryErr != nil {
		return nil, d.queryErr
	}
	if d.rows == nil {
		return newFakeRows(nil), nil
	}
	return d.rows, nil
}
func (d *obsQuerier) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}
func (d *obsQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (d *obsQuerier) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{}
}

// obsSummaryRows builds a fakeRows with the given summary rows.
// Columns per row (matching dal.ListObservabilitySummary scan order):
//
//	tenant_id, display_name, run_count_30d, total_llm_tokens_30d,
//	max_agents (*int), max_apps (*int), agent_count, app_count.
func obsSummaryRows(data [][]any) *fakeRows {
	return newFakeRows(data)
}

// OBS-1: GET /admin/observability/summary returns 200 with a JSON array.
func TestObservability_Summary_OK(t *testing.T) {
	rows := obsSummaryRows([][]any{
		// tenant_id, display_name, run_count_30d, total_llm_tokens_30d,
		// max_agents (int or nil), max_apps (int or nil), agent_count, app_count
		{"tid-1", "Acme Corp", int64(42), int64(100_000), 50, nil, int64(3), int64(5)},
	})
	q := &obsQuerier{rows: rows}

	h := admin.NewObservabilityHandlerForTest(q)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/observability/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, "tid-1", body[0]["tenant_id"])
	assert.Equal(t, "Acme Corp", body[0]["display_name"])
	assert.InDelta(t, 42.0, body[0]["run_count_30d"], 0.01)
	assert.InDelta(t, 100_000.0, body[0]["total_llm_tokens_30d"], 0.01)
	assert.InDelta(t, 50.0, body[0]["max_agents"], 0.01, "max_agents should be 50")
	assert.Nil(t, body[0]["max_apps"], "max_apps should be null")
	assert.InDelta(t, 3.0, body[0]["agent_count"], 0.01)
	assert.InDelta(t, 5.0, body[0]["app_count"], 0.01)
}

// OBS-2: GET /admin/observability/summary returns an empty array when there are no tenants.
func TestObservability_Summary_Empty(t *testing.T) {
	q := &obsQuerier{rows: newFakeRows(nil)}

	h := admin.NewObservabilityHandlerForTest(q)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/observability/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body, 0)
}

// OBS-3: DB error → 500.
func TestObservability_Summary_DBError(t *testing.T) {
	q := &obsQuerier{queryErr: errors.New("connection refused")}

	h := admin.NewObservabilityHandlerForTest(q)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/observability/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// OBS-4: Multiple tenants are all returned in order.
func TestObservability_Summary_MultiTenant(t *testing.T) {
	rows := obsSummaryRows([][]any{
		{"tid-1", "Alpha", int64(10), int64(5000), 10, nil, int64(2), int64(1)},
		{"tid-2", "Beta", int64(0), int64(0), nil, 20, int64(0), int64(3)},
	})
	q := &obsQuerier{rows: rows}

	h := admin.NewObservabilityHandlerForTest(q)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/observability/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 2)
	assert.Equal(t, "Alpha", body[0]["display_name"])
	assert.Equal(t, "Beta", body[1]["display_name"])
	assert.InDelta(t, 10.0, body[0]["run_count_30d"], 0.01)
	assert.InDelta(t, 20.0, body[1]["max_apps"], 0.01)
}
