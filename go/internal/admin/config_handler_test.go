package admin_test

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

// ── MonitoringConfigHandler tests ─────────────────────────────────────────

// MC-1: GET monitoring-config with no stored row — returns 200 with defaults.
func TestGetMonitoringConfig_NoRow_ReturnsDefaults(t *testing.T) {
	// pgx.ErrNoRows → GetConfig returns (nil, nil) → handler merges defaults.
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewMonitoringConfigHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/monitoring-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(1), body["heatmap_low"])
	assert.Equal(t, float64(10), body["heatmap_medium"])
	assert.Equal(t, float64(50), body["heatmap_high"])
	assert.Equal(t, float64(300), body["stats_window_seconds"])
}

// MC-2: PUT monitoring-config with valid body — returns 200 and stored value.
func TestPutMonitoringConfig_Valid_Returns200(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewMonitoringConfigHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"heatmap_low": 2, "heatmap_medium": 15, "heatmap_high": 60,
		"edge_thin": 3, "edge_medium": 20, "edge_thick": 80,
		"panel_max_sessions": 100, "stats_window_seconds": 600,
	})
	req := httptest.NewRequest(http.MethodPut, "/monitoring-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(2), resp["heatmap_low"])
	assert.Equal(t, float64(15), resp["heatmap_medium"])
}

// MC-3: PUT monitoring-config with invalid threshold order — returns 422.
func TestPutMonitoringConfig_BadThresholds_Returns422(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewMonitoringConfigHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	body, _ := json.Marshal(map[string]any{
		"heatmap_low": 50, "heatmap_medium": 10, "heatmap_high": 1, // wrong order
		"edge_thin": 1, "edge_medium": 10, "edge_thick": 50,
		"panel_max_sessions": 50, "stats_window_seconds": 300,
	})
	req := httptest.NewRequest(http.MethodPut, "/monitoring-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// MC-4: PUT monitoring-config with bad JSON — returns 400.
func TestPutMonitoringConfig_BadJSON_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewMonitoringConfigHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPut, "/monitoring-config", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── LLMRoutingHandler tests ────────────────────────────────────────────────

// LR-1: GET llm-providers/routing/config with no stored row — returns defaults.
func TestGetLLMRouting_NoRow_ReturnsDefaults(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewLLMRoutingHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/llm-providers/routing/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "anthropic", body["default_provider"])
	assert.Equal(t, "claude-sonnet-4-6", body["default_model"])
	assert.Nil(t, body["fallback_provider"])
	assert.Nil(t, body["fallback_model"])
}

// LR-2: PUT llm-providers/routing/config with valid body — returns 200.
func TestPutLLMRouting_Valid_Returns200(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLLMRoutingHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	fp := "openai"
	fm := "gpt-4o"
	body, _ := json.Marshal(map[string]any{
		"default_provider":  "anthropic",
		"default_model":     "claude-opus-4-8",
		"fallback_provider": fp,
		"fallback_model":    fm,
	})
	req := httptest.NewRequest(http.MethodPut, "/llm-providers/routing/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "anthropic", resp["default_provider"])
	assert.Equal(t, "claude-opus-4-8", resp["default_model"])
	assert.Equal(t, "openai", resp["fallback_provider"])
}

// LR-3: PUT llm-providers/routing/config with bad JSON — returns 400.
func TestPutLLMRouting_BadJSON_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLLMRoutingHandler(db)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPut, "/llm-providers/routing/config", bytes.NewReader([]byte(`{bad}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
