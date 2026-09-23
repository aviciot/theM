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

	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/admin"
)

// AFD-1: malformed {id} → 400, never reaches the service layer.
func TestAppFlowDebugHandler_Start_InvalidAppID_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewAppFlowDebugHandler(db, nil)
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
	h := admin.NewAppFlowDebugHandler(db, nil)
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
	h := admin.NewAppFlowDebugHandler(db, nil)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPost, "/applications/"+testAppID+"/debug/start", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
