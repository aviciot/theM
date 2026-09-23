package admin_test

import (
	"bytes"
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
	"github.com/aviciot/them/internal/admin/dal"
)

// testAppID is a fixed well-formed UUID used as the {id} route param by the
// tenant-scoped per-app handler tests below (LogVerbosityHandler,
// TemporalConfigHandler.AppRoutes) — both handlers now validate {id} as a
// UUID and read the caller's tenant from context (testTenantID, defined in
// admin_test.go), per the docs/APP_CANVAS_DEBUG_PLAN.md Phase 4
// tenant-isolation fix.
const testAppID = "00000000-0000-0000-0000-0000000000b1"

// mountAppRoute builds a chi router with {id} as the route param, injecting
// testTenantID via withTestTenant (admin_test.go) so
// tenantctx.MustTenantIDFromCtx does not panic.
func mountAppRoute(mount func(r chi.Router)) http.Handler {
	r := chi.NewRouter()
	r.Route("/applications/{id}", func(sub chi.Router) {
		sub.Use(withTestTenant)
		mount(sub)
	})
	return r
}

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

// ── TemporalConfigHandler tests ────────────────────────────────────────────

// TC-1: GET temporal-config with no stored row — returns 200 with hardcoded defaults.
func TestGetTemporalPlatformConfig_NoRow_ReturnsDefaults(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewTemporalConfigHandler(db)
	r := chi.NewRouter()
	h.PlatformRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/temporal-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(10), body["max_concurrent_workflows"])
	assert.Equal(t, float64(3600), body["workflow_timeout_s"])
	assert.Equal(t, float64(600), body["activity_timeout_s"])
	assert.Equal(t, float64(3), body["retry_max_attempts"])
}

// TC-2: PUT temporal-config with valid body — returns 200.
func TestPutTemporalPlatformConfig_Valid_Returns200(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewTemporalConfigHandler(db)
	r := chi.NewRouter()
	h.PlatformRoutes(r)

	body, _ := json.Marshal(map[string]any{"max_concurrent_workflows": 5})
	req := httptest.NewRequest(http.MethodPut, "/temporal-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TC-3: PUT temporal-config with negative retry — returns 422.
func TestPutTemporalPlatformConfig_NegativeRetry_Returns422(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewTemporalConfigHandler(db)
	r := chi.NewRouter()
	h.PlatformRoutes(r)

	body, _ := json.Marshal(map[string]any{"retry_max_attempts": -1})
	req := httptest.NewRequest(http.MethodPut, "/temporal-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// TC-4: PUT temporal-config with bad JSON — returns 400.
func TestPutTemporalPlatformConfig_BadJSON_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewTemporalConfigHandler(db)
	r := chi.NewRouter()
	h.PlatformRoutes(r)

	req := httptest.NewRequest(http.MethodPut, "/temporal-config", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── LogVerbosityHandler tests (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4) ──────

// LV-1: GET log-verbosity for an owned application with no stored row —
// returns 200 with the default. The DAL's tenant-scoped join resolves
// "no app_debug_config row" server-side via COALESCE, so the query succeeds
// and scans the default string directly — it does not surface as
// pgx.ErrNoRows (that now means "app not found or not owned", see LV-7).
func TestGetLogVerbosity_NoRow_ReturnsDefault(t *testing.T) {
	db := &fakeDB{queryRowStr: dal.DefaultLogVerbosity}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/log-verbosity", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "status", body["log_verbosity"])
}

// LV-2: GET log-verbosity with a stored row — returns its value.
func TestGetLogVerbosity_StoredRow_ReturnsValue(t *testing.T) {
	db := &fakeDB{queryRowStr: "full"}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/log-verbosity", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "full", body["log_verbosity"])
}

// LV-3: PUT log-verbosity with a valid value — returns 200 and echoes it back.
func TestPutLogVerbosity_Valid_Returns200(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"log_verbosity": "off"})
	req := httptest.NewRequest(http.MethodPut, "/applications/"+testAppID+"/log-verbosity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var respBody map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respBody))
	assert.Equal(t, "off", respBody["log_verbosity"])
}

// LV-4: PUT log-verbosity with an invalid value — returns 422.
func TestPutLogVerbosity_InvalidValue_Returns422(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"log_verbosity": "verbose"})
	req := httptest.NewRequest(http.MethodPut, "/applications/"+testAppID+"/log-verbosity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// LV-5: PUT log-verbosity with bad JSON — returns 400.
func TestPutLogVerbosity_BadJSON_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodPut, "/applications/"+testAppID+"/log-verbosity", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// LV-6: GET log-verbosity for an application id that is malformed (not a
// UUID) — returns 400, never reaches the DAL.
func TestGetLogVerbosity_MalformedAppID_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/not-a-uuid/log-verbosity", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// LV-7 (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4 tenant-isolation fix): GET
// log-verbosity for an application id that exists but does not belong to the
// caller's tenant (simulated by the DAL's tenant-scoped join finding no row,
// i.e. pgx.ErrNoRows) — returns 404, not 200 with a stored/default value.
// This is the regression test for the cross-tenant IDOR the code review found:
// before the fix, the handler never checked ownership at all and would have
// returned 200 with DefaultLogVerbosity or another tenant's stored setting.
func TestGetLogVerbosity_AppNotOwnedByTenant_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/log-verbosity", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// LV-8: PUT log-verbosity for an application id that does not belong to the
// caller's tenant — returns 404, and must not silently succeed in writing a
// row for an application the caller does not own.
func TestPutLogVerbosity_AppNotOwnedByTenant_Returns404(t *testing.T) {
	db := &fakeDB{execRetErr: pgx.ErrNoRows}
	h := admin.NewLogVerbosityHandler(db)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"log_verbosity": "off"})
	req := httptest.NewRequest(http.MethodPut, "/applications/"+testAppID+"/log-verbosity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── TemporalConfigHandler.AppRoutes tests (per-app, tenant-scoped) ─────────
//
// TemporalConfigHandler.PlatformRoutes is covered above; AppRoutes shares the
// same ownership-check pattern added to LogVerbosityHandler in the same fix
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4 tenant-isolation fix) and had no
// dedicated test before this fix.

// ownedAppNoOverrideDB is a QueryRow fake that distinguishes the two SELECTs
// GetTemporalEffectiveConfig issues: the them.config lookup (no row → use
// hardcoded defaults, same as every other *_test.go platform-config case)
// and the them.applications/app_temporal_config join added by the
// tenant-isolation fix (app is owned, just has no override row, so the
// COALESCE-free int columns all scan as NULL). fakeDB's QueryRow ignores the
// sql argument entirely, so it can't express "two different queries, two
// different outcomes" — this small local fake exists only for that reason.
type ownedAppNoOverrideDB struct{ fakeDB }

func (d *ownedAppNoOverrideDB) QueryRow(_ context.Context, sql string, _ ...any) admin.SingleRowScanner {
	if strings.Contains(sql, "them.applications") {
		return &fakeRow{err: nil} // Scan succeeds, leaves all four *int dest nil.
	}
	return &fakeRow{err: pgx.ErrNoRows} // them.config: no platform override row.
}

// TC-APP-1: GET temporal-config for an owned application with no override
// row — returns 200 with merged (hardcoded) defaults.
func TestGetTemporalAppConfig_NoRow_ReturnsMergedDefaults(t *testing.T) {
	db := &ownedAppNoOverrideDB{}
	h := admin.NewTemporalConfigHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/temporal-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(10), body["max_concurrent_workflows"])
}

// TC-APP-2: GET temporal-config with a malformed application id — returns
// 400, never reaches the DAL.
func TestGetTemporalAppConfig_MalformedAppID_Returns400(t *testing.T) {
	db := &fakeDB{}
	h := admin.NewTemporalConfigHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/not-a-uuid/temporal-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TC-APP-3 (tenant-isolation fix regression test): GET temporal-config for an
// application id that does not belong to the caller's tenant — returns 404,
// not 200 with hardcoded defaults or another tenant's stored override. Before
// the fix, GetTemporalAppConfig took no tenantID and would have returned
// (nil, nil) — i.e. "no override, use defaults" — for ANY application id,
// owned or not.
func TestGetTemporalAppConfig_AppNotOwnedByTenant_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	h := admin.NewTemporalConfigHandler(db)
	r := mountAppRoute(h.AppRoutes)

	req := httptest.NewRequest(http.MethodGet, "/applications/"+testAppID+"/temporal-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// fakeDB's QueryRow always returns pgx.ErrNoRows here regardless of which
	// query ran, so this test alone can't distinguish "no override row" from
	// "app not owned" at the fake layer — the DAL-level distinction (join
	// finds zero rows at all vs. finds the app row with a null override) is
	// exercised by go/internal/admin/dal's own tests. This handler-level test
	// instead documents and locks in the intended contract: once the DAL
	// signals pgx.ErrNoRows, the handler must map it to 404, never to a
	// silent "defaults" 200.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TC-APP-4: PUT temporal-config for an application id that does not belong
// to the caller's tenant — returns 404, and must not silently write an
// override row for an application the caller does not own.
func TestPutTemporalAppConfig_AppNotOwnedByTenant_Returns404(t *testing.T) {
	db := &fakeDB{execRetErr: pgx.ErrNoRows}
	h := admin.NewTemporalConfigHandler(db)
	r := mountAppRoute(h.AppRoutes)

	body, _ := json.Marshal(map[string]any{"max_concurrent_workflows": 5})
	req := httptest.NewRequest(http.MethodPut, "/applications/"+testAppID+"/temporal-config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
