package admin_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/admin"
)

func buildLLMProviderKeysPlatformRouter(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	h := admin.NewLLMProviderKeysHandler(db, "test-secret-key-32-bytes-padding!")
	h.PlatformRoutes(r)
	return r
}

// LPKP-01: GET /llm-providers/{name}/keys when no platform row exists for
// that provider name returns 404.
func TestLLPKPlatform_List_UnknownProvider_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildLLMProviderKeysPlatformRouter(db), http.MethodGet, "/llm-providers/anthropic/keys", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// LPKP-02: PATCH with an invalid keyID returns 400.
func TestLLPKPlatform_Update_InvalidKeyID_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	r := buildLLMProviderKeysPlatformRouter(db)
	req := httptest.NewRequest(http.MethodPatch, "/llm-providers/anthropic/keys/not-a-number", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPKP-03: POST create with invalid JSON body returns 400 (after provider resolves).
func TestLLPKPlatform_Create_InvalidJSON_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	r := buildLLMProviderKeysPlatformRouter(db)
	req := httptest.NewRequest(http.MethodPost, "/llm-providers/anthropic/keys", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPKP-04: Create with an unknown provider returns 404, not a key created
// against the wrong id.
func TestLLPKPlatform_Create_UnknownProvider_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	r := buildLLMProviderKeysPlatformRouter(db)
	body := bytes.NewBufferString(`{"name":"k1","api_key":"sk-test-12345678"}`)
	req := httptest.NewRequest(http.MethodPost, "/llm-providers/anthropic/keys", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// LPKP-05: GET .../models with a missing key_id query param returns 400.
func TestLLPKPlatform_ListModels_MissingKeyID_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	rr := do(t, buildLLMProviderKeysPlatformRouter(db), http.MethodGet, "/llm-providers/anthropic/models", nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPKP-06: DELETE with an unknown provider returns 404.
func TestLLPKPlatform_Delete_UnknownProvider_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildLLMProviderKeysPlatformRouter(db), http.MethodDelete, "/llm-providers/anthropic/keys/1", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
