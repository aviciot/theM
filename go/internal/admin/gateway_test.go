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

// ── helpers ───────────────────────────────────────────────────────────────────

func buildGatewayRouter(db admin.DBQuerier) http.Handler {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h := admin.NewGatewayHandler(db)
	h.Routes(r)
	return r
}

func do(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// ── GW-ADM-01: ListClients returns empty list ─────────────────────────────────

func TestGatewayAdmin_ListClients_Empty(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/clients", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var out []any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, []any{}, out)
}

// ── GW-ADM-02: CreateClient — label missing → 400 ────────────────────────────

func TestGatewayAdmin_CreateClient_MissingLabel(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodPost, "/gateway/clients", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── GW-ADM-03: CreateClient — success returns token ──────────────────────────

func TestGatewayAdmin_CreateClient_Success(t *testing.T) {
	clientID := "aaaaaaaa-0000-0000-0000-000000000001"
	db := &fakeDB{
		// QueryRow from CreateGatewayClient RETURNING → scan 7 columns
		queryRowStrings: []string{
			clientID, testTenantID, "sha256hashvalue", "my-agent",
			"", "", "2026-01-01T00:00:00Z",
		},
	}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodPost, "/gateway/clients", map[string]any{"label": "my-agent"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, clientID, out["id"])
	// Token must be present and non-empty on create
	tok, ok := out["token"]
	assert.True(t, ok, "token field must be present in create response")
	assert.NotEmpty(t, tok)
}

// ── GW-ADM-04: GetClient — not found → 404 ───────────────────────────────────

func TestGatewayAdmin_GetClient_NotFound(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/clients/nonexistent", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ── GW-ADM-05: DeleteClient calls Exec and returns 204 ───────────────────────

func TestGatewayAdmin_DeleteClient(t *testing.T) {
	var execSQL string
	db := &fakeDB{
		execFn: func(sql string, _ ...any) error {
			execSQL = sql
			return nil
		},
	}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodDelete, "/gateway/clients/some-id", nil)
	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Contains(t, execSQL, "gateway_clients")
}

// ── GW-ADM-06: ListProfiles returns empty list ────────────────────────────────

func TestGatewayAdmin_ListProfiles_Empty(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/profiles", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var out []any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, []any{}, out)
}

// ── GW-ADM-07: CreateProfile — name missing → 400 ────────────────────────────

func TestGatewayAdmin_CreateProfile_MissingName(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodPost, "/gateway/profiles", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── GW-ADM-08: GetPolicy — no policy row → returns zero row (allow-all) ──────

func TestGatewayAdmin_GetPolicy_NoRow(t *testing.T) {
	db := &fakeDB{queryRowErr: pgx.ErrNoRows}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/policy", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, testTenantID, out["tenant_id"])
	models, ok := out["allowed_models"]
	require.True(t, ok)
	assert.Equal(t, []any{}, models)
}

// ── GW-ADM-09: PutPolicy — invalid body → 400 ────────────────────────────────

func TestGatewayAdmin_PutPolicy_InvalidBody(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	req := httptest.NewRequest(http.MethodPut, "/gateway/policy", bytes.NewBufferString("not-json"))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── GW-ADM-10: ListRequests returns empty list ────────────────────────────────

func TestGatewayAdmin_ListRequests_Empty(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/requests", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var out []any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, []any{}, out)
}

// ── GW-ADM-11: ListRequests limit param accepted ──────────────────────────────

func TestGatewayAdmin_ListRequests_LimitParam(t *testing.T) {
	db := &fakeDB{}
	r := buildGatewayRouter(db)

	rr := do(t, r, http.MethodGet, "/gateway/requests?limit=10", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var out []any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, []any{}, out)
}
