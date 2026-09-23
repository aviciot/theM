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

func buildLLMProviderKeysRouter(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h := admin.NewLLMProviderKeysHandler(db, "test-secret-key-32-bytes-padding!")
	h.TenantScopedRoutes(r)
	return r
}

// LPK-01: GET /my/llm-providers/{name}/keys when the tenant has no own row for
// that provider name yet returns 404 — never silently reads the platform row.
func TestLLPK_List_TenantHasNoOwnProviderRow_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildLLMProviderKeysRouter(db), http.MethodGet, "/my/llm-providers/anthropic/keys", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// LPK-02: missing provider name in path is unreachable via chi routing (name is
// always present when the route matches), but an invalid keyID must 400.
func TestLLPK_Update_InvalidKeyID_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	r := buildLLMProviderKeysRouter(db)
	req := httptest.NewRequest(http.MethodPatch, "/my/llm-providers/anthropic/keys/not-a-number", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPK-03: POST create with invalid JSON body returns 400 (after provider resolves).
func TestLLPK_Create_InvalidJSON_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	r := buildLLMProviderKeysRouter(db)
	req := httptest.NewRequest(http.MethodPost, "/my/llm-providers/anthropic/keys", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPK-04: Create with a provider row the tenant does not own returns 404, not a
// key created against the wrong (e.g. platform) provider id.
func TestLLPK_Create_UnknownProvider_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	r := buildLLMProviderKeysRouter(db)
	body := bytes.NewBufferString(`{"name":"k1","api_key":"sk-test-12345678"}`)
	req := httptest.NewRequest(http.MethodPost, "/my/llm-providers/anthropic/keys", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// LPK-05: GET .../models with a missing key_id query param returns 400.
func TestLLPK_ListModels_MissingKeyID_Returns400(t *testing.T) {
	db := &fakeProviderDB{queryRow8: &fakeProviderRow{id: 20, name: "anthropic", model: "claude-sonnet-4-6"}}
	rr := do(t, buildLLMProviderKeysRouter(db), http.MethodGet, "/my/llm-providers/anthropic/models", nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// LPK-06: DELETE with a missing provider row returns 404.
func TestLLPK_Delete_UnknownProvider_Returns404(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildLLMProviderKeysRouter(db), http.MethodDelete, "/my/llm-providers/anthropic/keys/1", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}
