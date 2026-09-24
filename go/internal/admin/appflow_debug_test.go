package admin_test

// docs/APP_CANVAS_DEBUG_PLAN.md Phase 5: AppFlowDebugHandler.Start.
// These are thin HTTP-mechanics tests (bad UUID, missing required field) that
// fail before the service layer is ever reached — the service's own compile/
// validate/admit/start behavior is covered by internal/admin/service's
// appflow_debug_test.go, which doesn't need a real dal.DB or Temporal client.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// AFD-1: malformed {id} → 400, never reaches the service layer.
func TestAppFlowDebugHandler_Start_InvalidAppID_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"entry_point_slug": "chat"})
	req := httptest.NewRequest(http.MethodPost, "/applications/not-a-uuid/debug/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// AFD-2: missing entry_point_slug → 400.
func TestAppFlowDebugHandler_Start_MissingEntryPointSlug_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"user_message": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// AFD-3: invalid JSON body → 400.
func TestAppFlowDebugHandler_Start_InvalidJSON_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/start", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// AFD-4: malformed {run_id} → 400, never reaches GetRun or Temporal.
func TestAppFlowDebugHandler_Step_InvalidRunID_Returns400(t *testing.T) {
	db := &fakeDB{}
	temp := &fakeTemporal{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, temp, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/not-a-uuid/step", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, temp.signaled, "must not signal on invalid run_id")
}

// AFD-5: run not found (wrong tenant or doesn't exist) → 404, no signal sent —
// same cross-tenant-IDOR-safe pattern as HILApprovalsHandler: GetRun is
// tenant-scoped, so a run belonging to another tenant surfaces as pgx.ErrNoRows
// here, never as data or a signal to someone else's workflow.
func TestAppFlowDebugHandler_Step_RunNotFound_Returns404(t *testing.T) {
	runID := "00000000-0000-0000-0000-0000000000c1"
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	temp := &fakeTemporal{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, temp, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/"+runID+"/step", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, temp.signaled)
}

// AFD-6: temporal signaling not configured (nil) → 503, matches the same
// nil-disables-the-feature convention as router.go's appFlowDebugLifecycle
// nil-check for the whole handler.
func TestAppFlowDebugHandler_Step_NoTemporalConfigured_Returns503(t *testing.T) {
	runID := "00000000-0000-0000-0000-0000000000c1"
	db := &fakeDB{} // GetRun succeeds (queryRowErr nil, fakeRow.Scan returns nil)
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/"+runID+"/step", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// AFD-7: happy path — GetRun succeeds, Temporal signaled with the right
// workflow ID (appflow.WorkflowIDForRun(tenant, run)) and AppFlowSignalStep.
func TestAppFlowDebugHandler_Step_Success_SignalsWorkflow(t *testing.T) {
	runID := "00000000-0000-0000-0000-0000000000c1"
	db := &fakeDB{}
	temp := &fakeTemporal{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, temp, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/"+runID+"/step", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, temp.signaled, 1)
	assert.Equal(t, "appflow:"+testTenantID+":"+runID, temp.signaled[0])

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, runID, resp["run_id"])
	assert.Equal(t, "stepped", resp["status"])
}

// AFD-8: malformed run_id → 400, never reaches the service layer — same
// mechanics as the Step route's own AFD-3-equivalent guard.
func TestAppFlowDebugHandler_Result_InvalidRunID_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/debug/not-a-uuid/result", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// AFD-9: run doesn't exist (or belongs to another tenant) → 404 — same
// fakeDB{queryRowErr: pgx.ErrNoRows} pattern as
// TestAppFlowDebugHandler_Step_RunNotFound_Returns404, since GetRunDetail's
// first internal call is the same single-row GetRun lookup.
func TestAppFlowDebugHandler_Result_RunNotFound_Returns404(t *testing.T) {
	runID := "00000000-0000-0000-0000-0000000000c1"
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewAppFlowDebugHandler(db, nil, nil, nil, nil, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/debug/"+runID+"/result", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
