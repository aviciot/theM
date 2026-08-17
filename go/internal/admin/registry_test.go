package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

func TestRegistryHandler_ListComponentDefinitions_ReturnsArray(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	h := admin.NewRegistryHandler(db)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	r.Get("/component-definitions", h.ListComponentDefinitions)

	req := httptest.NewRequest(http.MethodGet, "/component-definitions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var defs []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &defs))
	assert.NotNil(t, defs, "component definitions must not be null")
}
