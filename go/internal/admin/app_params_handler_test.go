package admin_test

// HTTP handler tests for app global params endpoints (HTTP-20..25+).
// These test the handler layer; service and DAL are exercised in
// internal/admin/service/app_global_params_test.go (AGP-1..8).

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HTTP-20: GET /app-params → 200 with empty array when app has no params.
func TestGetAppParams_Handler_200_Empty(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	w := serveAppsQuerier(t, db, nil, http.MethodGet, "/applications/app-1/app-params", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out)
}

// HTTP-21a: GET /app-params → secret param appears with is_set+value_hint, no plaintext.
func TestGetAppParams_Handler_200_SecretParam(t *testing.T) {
	blob := []byte(`{"api_key":{"ct":"plain:my-secret","hint":"cret"}}`)
	db := &bytesQueryFakeDB{jsonbBlob: blob}
	w := serveAppsQuerier(t, db, nil, http.MethodGet, "/applications/app-1/app-params", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var params []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &params))
	require.Len(t, params, 1)
	assert.Equal(t, "api_key", params[0]["name"])
	assert.Equal(t, "secret", params[0]["type"])
	assert.Equal(t, true, params[0]["is_set"])
	assert.Equal(t, "cret", params[0]["value_hint"])
	_, hasValue := params[0]["value"]
	assert.False(t, hasValue, "plaintext must not appear for secrets")
}

// HTTP-21b: GET /app-params → non-secret param appears with value field set.
func TestGetAppParams_Handler_200_StringParam(t *testing.T) {
	blob := []byte(`{"target_city":"Tel Aviv"}`)
	db := &bytesQueryFakeDB{jsonbBlob: blob}
	w := serveAppsQuerier(t, db, nil, http.MethodGet, "/applications/app-1/app-params", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var params []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &params))
	require.Len(t, params, 1)
	assert.Equal(t, "target_city", params[0]["name"])
	assert.Equal(t, "Tel Aviv", params[0]["value"])
}

// HTTP-22a: PUT /app-params/{name} secret → 200 with {name, updated: true}.
func TestSetAppParam_Handler_200_Secret(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	body, _ := json.Marshal(map[string]any{"value": "sk-123456", "type": "secret"})
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/api_key", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "api_key", out["name"])
	assert.Equal(t, true, out["updated"])
}

// HTTP-22b: PUT /app-params/{name} string → 200.
func TestSetAppParam_Handler_200_String(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	body, _ := json.Marshal(map[string]any{"value": "Tel Aviv", "type": "string"})
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/city", body)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "city", out["name"])
}

// HTTP-23a: PUT /app-params/{name} with bad param name → 400 validation error.
// Note: chi URL param captures "bad_name" but the name regex rejects names with
// uppercase or special chars; test with a name that has invalid chars in service call.
func TestSetAppParam_Handler_400_BadName(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	body, _ := json.Marshal(map[string]any{"value": "v", "type": "string"})
	// "BadName" has uppercase — appParamNameRe rejects it.
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/BadName", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// HTTP-23b: PUT /app-params/{name} with unsupported type → 422.
func TestSetAppParam_Handler_422_BadType(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	body, _ := json.Marshal(map[string]any{"value": "v", "type": "badtype"})
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/mykey", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// HTTP-23c: PUT /app-params/{name} with bad JSON body → 400.
func TestSetAppParam_Handler_400_BadJSON(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/mykey",
		[]byte("not-json"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// HTTP-23d: PUT /app-params/{name} with empty value → 400 validation error.
func TestSetAppParam_Handler_400_EmptyValue(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{}`)}
	body, _ := json.Marshal(map[string]any{"value": "", "type": "string"})
	w := serveAppsQuerier(t, db, nil, http.MethodPut, "/applications/app-1/app-params/mykey", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// HTTP-24: DELETE /app-params/{name} → 200 with {name, deleted: true}.
func TestDeleteAppParam_Handler_200(t *testing.T) {
	db := &bytesQueryFakeDB{jsonbBlob: []byte(`{"api_key":{"ct":"plain:v","hint":"_v"}}`)}
	w := serveAppsQuerier(t, db, nil, http.MethodDelete, "/applications/app-1/app-params/api_key", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "api_key", out["name"])
	assert.Equal(t, true, out["deleted"])
}

// HTTP-25: GET /app-params returns 404 when app is not found (pgx.ErrNoRows from DB).
func TestGetAppParams_Handler_404_AppMissing(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	w := serveApps(t, db, nil, http.MethodGet, "/applications/missing-app/app-params", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
