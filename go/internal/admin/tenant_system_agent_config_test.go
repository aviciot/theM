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

func buildTenantSACRouter(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h := admin.NewTenantSystemAgentConfigHandler(db, "test-secret-key-32-bytes-padding!")
	h.TenantScopedRoutes(r)
	return r
}

// SAC-01: GET with no row yet returns 200 with the "custom" default, not 404 —
// matches today's behavior for a tenant that never set a mode.
func TestSAC_Get_NoRow_Returns200Default(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodGet, "/my/system-agents/classifier/config", nil)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"mode":"custom"`)
}

// SAC-02: GET for an unknown role returns 400.
func TestSAC_Get_UnknownRole_Returns400(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodGet, "/my/system-agents/not_a_role/config", nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// SAC-03: PUT with invalid JSON body returns 400.
func TestSAC_Put_InvalidJSON_Returns400(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	r := buildTenantSACRouter(db)
	req := httptest.NewRequest(http.MethodPut, "/my/system-agents/classifier/config", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// SAC-04: PUT with an invalid mode value returns 400.
func TestSAC_Put_InvalidMode_Returns400(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodPut, "/my/system-agents/classifier/config",
		map[string]any{"mode": "bogus"})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// SAC-05: PUT general mode with no provider_name returns 400.
func TestSAC_Put_GeneralMode_MissingProviderName_Returns400(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodPut, "/my/system-agents/classifier/config",
		map[string]any{"mode": "general"})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// SAC-06: PUT general mode with a provider_name succeeds (200).
func TestSAC_Put_GeneralMode_Succeeds(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodPut, "/my/system-agents/classifier/config",
		map[string]any{"mode": "general", "provider_name": "anthropic"})
	assert.Equal(t, http.StatusOK, rr.Code)
}

// SAC-07: PUT custom mode with no api key on first-time setup returns 400
// (unprocessable input, not silently accepted with an empty key).
func TestSAC_Put_CustomMode_NoKeyFirstTime_Returns400(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodPut, "/my/system-agents/classifier/config",
		map[string]any{"mode": "custom", "custom_provider": "anthropic", "custom_model": "m"})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// SAC-08: PUT custom mode with a plaintext key never echoes it back in the response.
func TestSAC_Put_CustomMode_NeverReturnsPlaintextKey(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	rr := do(t, buildTenantSACRouter(db), http.MethodPut, "/my/system-agents/classifier/config",
		map[string]any{
			"mode": "custom", "custom_provider": "anthropic", "custom_model": "m",
			"custom_api_key": "sk-super-secret-plaintext",
		})
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "sk-super-secret-plaintext")
}
