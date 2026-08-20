package admin_test

// runs_test.go — unit tests for RunsHandler (Stats, Get (RunDetail), Tasks, Artifacts, writes).
// Uses the same fakeDB / fakeRows / withTestTenant helpers defined in admin_test.go.
// multiQueryFakeDB is defined in applications_wave8_test.go (same package).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// serveRuns mounts RunsHandler on a chi router with withTestTenant middleware.
func serveRuns(t *testing.T, db admin.DBQuerier, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serveRunsBody(t, db, method, path, nil)
}

// serveRunsBody is like serveRuns but allows a JSON request body.
func serveRunsBody(t *testing.T, db admin.DBQuerier, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewRunsHandler(db, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── RunStats tests ─────────────────────────────────────────────────────────────

// RS-1: GET /runs/stats with no rows → stats with total=0, by_status={}, total_cost_usd="0.000000".
func TestRunsStats_Empty(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	w := serveRuns(t, db, http.MethodGet, "/runs/stats")

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(0), out["total"], "empty stats must have total=0")
	assert.Equal(t, "0.000000", out["total_cost_usd"], "empty stats must have cost=0.000000")
	byStatus, ok := out["by_status"].(map[string]any)
	assert.True(t, ok, "by_status must be a map")
	assert.Empty(t, byStatus, "by_status must be empty for no rows")
}

// RS-2: GET /runs/stats with data rows → stats aggregated correctly.
// Each row: status string, count int, costStr string.
func TestRunsStats_WithData(t *testing.T) {
	rows := newFakeRows([][]any{
		{"completed", 3, "0.001500"},
		{"failed", 1, "0.000300"},
	})
	db := &fakeDB{queryRows: rows}
	w := serveRuns(t, db, http.MethodGet, "/runs/stats")

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(4), out["total"], "total must be sum of all status counts")
	assert.Equal(t, "0.001800", out["total_cost_usd"], "total_cost_usd must be sum of all status costs")
	byStatus, ok := out["by_status"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(3), byStatus["completed"])
	assert.Equal(t, float64(1), byStatus["failed"])
}

// ── RunDetail (Get) tests ──────────────────────────────────────────────────────

// RD-1: GET /runs/{run_id} with a valid run and empty sub-tables → RunDetail shape.
// GetRunDetail calls QueryRow (GetRun → zero Run, no error) then 3 x Query (all empty).
// multiQueryFakeDB.QueryRow returns &fakeRow{} (nil err) → scanRun fills zero values.
func TestRunsGet_ReturnsDetail(t *testing.T) {
	db := &multiQueryFakeDB{} // QueryRow returns &fakeRow{err:nil}, Query returns empty rows
	w := serveRuns(t, db, http.MethodGet, "/runs/run-001")

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	// RunDetail must embed steps, usage, children as arrays (not null).
	steps, ok := out["steps"].([]any)
	assert.True(t, ok, "steps must be an array")
	assert.Empty(t, steps, "steps must be empty")
	usage, ok := out["usage"].([]any)
	assert.True(t, ok, "usage must be an array")
	assert.Empty(t, usage, "usage must be empty")
	children, ok := out["children"].([]any)
	assert.True(t, ok, "children must be an array")
	assert.Empty(t, children, "children must be empty")
}

// ── Tasks tests ────────────────────────────────────────────────────────────────

// RT-1: GET /runs/{run_id}/tasks with no task rows → empty array not null.
func TestRunsTasks_Empty(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	w := serveRuns(t, db, http.MethodGet, "/runs/run-abc/tasks")

	require.Equal(t, http.StatusOK, w.Code)
	var tasks []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.NotNil(t, tasks, "tasks must not be null")
	assert.Empty(t, tasks)
}

// RT-2: GET /runs/{run_id}/tasks with task rows → Task array.
// Task row columns (13): id, parent_task_id, agent_id, orchestrator_id, context_id,
//
//	state, kind, remote_task_id, budget_tokens (*int), tokens_used (*int),
//	error, created_at, updated_at
func TestRunsTasks_WithData(t *testing.T) {
	rows := newFakeRows([][]any{
		{"task-1", "", "", "orch-1", "ctx-1",
			"completed", "root", "", nil, nil,
			"", "2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z"},
	})
	db := &fakeDB{queryRows: rows}
	w := serveRuns(t, db, http.MethodGet, "/runs/run-abc/tasks")

	require.Equal(t, http.StatusOK, w.Code)
	var tasks []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-1", tasks[0]["id"])
	assert.Equal(t, "completed", tasks[0]["state"])
	assert.Equal(t, "root", tasks[0]["kind"])
}

// ── Artifacts tests ────────────────────────────────────────────────────────────

// RA-1: GET /runs/{run_id}/artifacts with no rows → empty array not null.
func TestRunsArtifacts_Empty(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	w := serveRuns(t, db, http.MethodGet, "/runs/run-abc/artifacts")

	require.Equal(t, http.StatusOK, w.Code)
	var artifacts []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &artifacts))
	assert.NotNil(t, artifacts, "artifacts must not be null")
	assert.Empty(t, artifacts)
}

// RA-2: GET /runs/{run_id}/artifacts with artifact rows → Artifact array.
// Artifact row columns (9): id, task_id, context_id, artifact_id, name,
//
//	parts_json, append_index, last_chunk, created_at
func TestRunsArtifacts_WithData(t *testing.T) {
	// New schema: run_artifacts columns — id, filename, content_type, size, data, created_at
	rows := newFakeRows([][]any{
		{"art-1", "report.html", "text/html", int64(13), []byte("<h1>Hello</h1>"), "2026-01-01T00:00:00Z"},
	})
	db := &fakeDB{queryRows: rows}
	w := serveRuns(t, db, http.MethodGet, "/runs/run-abc/artifacts")

	require.Equal(t, http.StatusOK, w.Code)
	var artifacts []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &artifacts))
	require.Len(t, artifacts, 1)
	assert.Equal(t, "art-1", artifacts[0]["id"])
	assert.Equal(t, "report.html", artifacts[0]["name"])
	parts, ok := artifacts[0]["parts"].([]any)
	assert.True(t, ok, "parts must be an array")
	require.Len(t, parts, 1)
	part := parts[0].(map[string]any)
	assert.Equal(t, "report.html", part["filename"])
	assert.Equal(t, "text/html", part["media_type"])
	assert.Equal(t, "<h1>Hello</h1>", part["text"])
}

// ── Cancel tests ───────────────────────────────────────────────────────────────

// RW-1: PATCH /runs/{run_id}/cancel with a running run → 200 with Run JSON.
// fakeDB.QueryRow returns &fakeRow{err:nil} → scanRun fills zero values → valid Run.
func TestRunsCancel_Success(t *testing.T) {
	db := &fakeDB{} // QueryRow returns &fakeRow{err:nil}
	w := serveRuns(t, db, http.MethodPatch, "/runs/run-abc/cancel")

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.NotNil(t, out, "cancel response must be a JSON object")
}

// RW-2: PATCH /runs/{run_id}/cancel run not found → 404.
// Both QueryRow calls (UPDATE RETURNING and fallback GetRun) return error → ErrNotFound.
func TestRunsCancel_NotFound(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	w := serveRuns(t, db, http.MethodPatch, "/runs/run-abc/cancel")

	require.Equal(t, http.StatusNotFound, w.Code)
}

// ── Delete tests ───────────────────────────────────────────────────────────────

// RW-3: DELETE /runs/{run_id} success → 204 No Content.
func TestRunsDelete_Success(t *testing.T) {
	db := &fakeDB{execRetStr: "run-abc"} // ExecReturning returns the run ID
	w := serveRuns(t, db, http.MethodDelete, "/runs/run-abc")

	require.Equal(t, http.StatusNoContent, w.Code)
}

// RW-4: DELETE /runs/{run_id} run not found → 404.
func TestRunsDelete_NotFound(t *testing.T) {
	db := &fakeDB{execRetErr: pgx.ErrNoRows}
	w := serveRuns(t, db, http.MethodDelete, "/runs/run-abc")

	require.Equal(t, http.StatusNotFound, w.Code)
}

// ── BulkDelete tests ────────────────────────────────────────────────────────

// RW-5: POST /runs/bulk-delete with IDs → 200 {"deleted": 1}.
// fakeDB.Query returns one row with an ID string; BulkDeleteRuns counts them.
func TestRunsBulkDelete_WithIDs(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows([][]any{{"run-1"}})}
	body, _ := json.Marshal(map[string]any{"run_ids": []string{"run-1"}})
	w := serveRunsBody(t, db, http.MethodPost, "/runs/bulk-delete", body)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(1), out["deleted"])
}

// RW-6: POST /runs/bulk-delete with empty run_ids → 200 {"deleted": 0} without hitting DB.
func TestRunsBulkDelete_EmptyIDs(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"run_ids": []string{}})
	w := serveRunsBody(t, &fakeDB{}, http.MethodPost, "/runs/bulk-delete", body)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(0), out["deleted"])
}

// ── Route ordering test ────────────────────────────────────────────────────────

// RO-1: GET /runs/stats must not be matched by /runs/{run_id} — chi static routes
// take precedence over parameterised routes, so "stats" must not be treated as run_id.
func TestRunsRoute_StatsNotParsedAsRunID(t *testing.T) {
	// fakeDB.queryRows is used for the stats Query → returns empty rows → 200 stats.
	db := &fakeDB{queryRows: newFakeRows(nil)}
	w := serveRuns(t, db, http.MethodGet, "/runs/stats")

	require.Equal(t, http.StatusOK, w.Code, "GET /runs/stats must return 200, not 404 or run-not-found")
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	_, hasTotal := out["total"]
	assert.True(t, hasTotal, "response must contain 'total' key — stats shape, not error")
	_, hasError := out["error"]
	assert.False(t, hasError, "response must not contain 'error' key")
}
