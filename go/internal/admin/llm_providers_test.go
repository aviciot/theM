package admin_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/aviciot/them/internal/admin"
)

func buildLLMProviderSelfRouter(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h := admin.NewLLMProvidersHandler(db, "test-secret-key-32-bytes-padding!")
	h.TenantScopedRoutes(r)
	return r
}

// LLP-TS-01: GET /my/llm-providers returns 200 with empty JSON array.
func TestLLPTenantScoped_ListMine_Empty(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	rr := do(t, buildLLMProviderSelfRouter(db), http.MethodGet, "/my/llm-providers", nil)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "[")
}

// LLP-TS-02: PUT /my/llm-providers/{name} with invalid JSON body returns 400.
func TestLLPTenantScoped_UpsertMine_InvalidBody(t *testing.T) {
	db := &fakeDB{}
	r := buildLLMProviderSelfRouter(db)
	req := httptest.NewRequest(http.MethodPut, "/my/llm-providers/openai", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
