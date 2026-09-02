package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// ── managedAppFakeDB ──────────────────────────────────────────────────────────
//
// Purpose-built fake for managed-app handler tests.
// Deliberately separate from tenantDB and fakeDB to avoid coupling.

type maFakeRow struct {
	vals []any
	err  error
}

func (r *maFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if i >= len(r.vals) {
			break
		}
		switch dp := d.(type) {
		case *string:
			if v, ok := r.vals[i].(string); ok {
				*dp = v
			}
		case **string:
			if v, ok := r.vals[i].(string); ok {
				s := v
				*dp = &s
			}
		case *bool:
			if v, ok := r.vals[i].(bool); ok {
				*dp = v
			}
		case *int:
			if v, ok := r.vals[i].(int); ok {
				*dp = v
			}
		case *time.Time:
			if v, ok := r.vals[i].(time.Time); ok {
				*dp = v
			}
		case *[]byte:
			if v, ok := r.vals[i].([]byte); ok {
				*dp = v
			}
		case *[]string:
			if v, ok := r.vals[i].([]string); ok {
				*dp = v
			}
		}
	}
	return nil
}

type maFakeRows struct {
	rows []*maFakeRow
	pos  int
}

func (r *maFakeRows) Next() bool   { return r.pos < len(r.rows) }
func (r *maFakeRows) Close() error { return nil }
func (r *maFakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos]
	r.pos++
	return row.Scan(dest...)
}

type managedAppFakeDB struct {
	listRows  []*maFakeRow // returned by Query (first call)
	listRows2 []*maFakeRow // returned by Query (second call — params)
	getRow    *maFakeRow   // returned by QueryRow
	createRow *maFakeRow   // returned by ExecReturning
	execErr   error
	queryCnt  int // tracks which Query call we're on
}

func (d *managedAppFakeDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	d.queryCnt++
	if d.queryCnt == 2 && d.listRows2 != nil {
		return &maFakeRows{rows: d.listRows2}, nil
	}
	return &maFakeRows{rows: d.listRows}, nil
}

func (d *managedAppFakeDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.getRow != nil {
		return d.getRow
	}
	return &maFakeRow{err: pgx.ErrNoRows}
}

func (d *managedAppFakeDB) Exec(_ context.Context, _ string, _ ...any) error {
	return d.execErr
}

func (d *managedAppFakeDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.createRow != nil {
		return d.createRow
	}
	if d.execErr != nil {
		return &maFakeRow{err: d.execErr}
	}
	return &maFakeRow{err: pgx.ErrNoRows}
}

// ── router helpers ────────────────────────────────────────────────────────────

func newManagedAppsPlatformRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	admin.NewManagedAppsHandler(db).PlatformRoutes(r)
	return r
}

func newManagedAppsTenantRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(withTestTenant) // injects bootstrap tenant into context
	admin.NewManagedAppsHandler(db).TenantRoutes(r)
	return r
}

var maTestNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// maAppRow returns a fake row representing a ManagedApp.
func maAppRow(id, name, slug string) *maFakeRow {
	return &maFakeRow{vals: []any{id, name, slug, "1.0.0", (*string)(nil), true, maTestNow, maTestNow}}
}

// maBindingRow returns a fake row representing a ManagedAppBinding.
func maBindingRow(id, appID, tenantID string) *maFakeRow {
	return &maFakeRow{vals: []any{id, appID, tenantID, true, []byte(`{}`), "latest", maTestNow, maTestNow}}
}

// ── MA-01: GET /managed-apps returns [] when empty ────────────────────────────

func TestManagedApps_List_Empty(t *testing.T) {
	r := newManagedAppsPlatformRouter(&managedAppFakeDB{})

	req := httptest.NewRequest(http.MethodGet, "/managed-apps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out)
}

// ── MA-02: GET /managed-apps returns list ─────────────────────────────────────

func TestManagedApps_List_Populated(t *testing.T) {
	db := &managedAppFakeDB{
		listRows: []*maFakeRow{
			maAppRow("00000000-0000-0000-0000-000000000010", "Support Bot", "support-bot"),
			maAppRow("00000000-0000-0000-0000-000000000011", "Data Extractor", "data-extractor"),
		},
	}
	r := newManagedAppsPlatformRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/managed-apps", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 2)
	assert.Equal(t, "support-bot", out[0]["slug"])
	assert.Equal(t, "data-extractor", out[1]["slug"])
}

// ── MA-03: POST /managed-apps creates app → 201 ───────────────────────────────

func TestManagedApps_Create_Success(t *testing.T) {
	db := &managedAppFakeDB{
		createRow: maAppRow("00000000-0000-0000-0000-000000000010", "Support Bot", "support-bot"),
	}
	r := newManagedAppsPlatformRouter(db)

	body, _ := json.Marshal(map[string]string{"name": "Support Bot", "slug": "support-bot"})
	req := httptest.NewRequest(http.MethodPost, "/managed-apps", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "support-bot", out["slug"])
	assert.Equal(t, "Support Bot", out["name"])
}

// ── MA-04: POST /managed-apps missing name → 400 ─────────────────────────────

func TestManagedApps_Create_MissingName(t *testing.T) {
	r := newManagedAppsPlatformRouter(&managedAppFakeDB{})

	body, _ := json.Marshal(map[string]string{"slug": "support-bot"})
	req := httptest.NewRequest(http.MethodPost, "/managed-apps", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── MA-05: GET /managed-apps/{id} found → 200 with params ────────────────────

func TestManagedApps_Get_Found(t *testing.T) {
	db := &managedAppFakeDB{
		getRow: maAppRow("00000000-0000-0000-0000-000000000010", "Support Bot", "support-bot"),
		// listRows2 is the params query — empty params is fine
	}
	r := newManagedAppsPlatformRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/managed-apps/00000000-0000-0000-0000-000000000010", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "support-bot", out["slug"])
	params, ok := out["params"].([]any)
	require.True(t, ok, "params field must be an array")
	assert.Empty(t, params)
}

// ── MA-06: GET /managed-apps/{id} not found → 404 ────────────────────────────

func TestManagedApps_Get_NotFound(t *testing.T) {
	r := newManagedAppsPlatformRouter(&managedAppFakeDB{})

	req := httptest.NewRequest(http.MethodGet, "/managed-apps/00000000-0000-0000-0000-000000000099", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── MA-07: PUT /managed-apps/{id}/params replaces manifest → 200 ─────────────

func TestManagedApps_PutParams(t *testing.T) {
	r := newManagedAppsPlatformRouter(&managedAppFakeDB{})

	params := []map[string]any{
		{"key": "COMPANY_NAME", "label": "Company Name", "param_type": "string", "required": true},
	}
	body, _ := json.Marshal(params)
	req := httptest.NewRequest(http.MethodPut, "/managed-apps/00000000-0000-0000-0000-000000000010/params", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, float64(1), out["updated"])
}

// ── MA-08: GET /managed-app-bindings returns tenant bindings ──────────────────

func TestManagedApps_Bindings_List(t *testing.T) {
	db := &managedAppFakeDB{
		listRows: []*maFakeRow{
			maBindingRow(
				"00000000-0000-0000-0000-000000000020",
				"00000000-0000-0000-0000-000000000010",
				testTenantID,
			),
		},
	}
	r := newManagedAppsTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/managed-app-bindings", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, testTenantID, out[0]["tenant_id"])
}

// ── MA-09: PUT /managed-app-bindings/{app_id} upserts → 200 ──────────────────

func TestManagedApps_Binding_Upsert(t *testing.T) {
	db := &managedAppFakeDB{
		createRow: maBindingRow(
			"00000000-0000-0000-0000-000000000020",
			"00000000-0000-0000-0000-000000000010",
			testTenantID,
		),
	}
	r := newManagedAppsTenantRouter(db)

	body, _ := json.Marshal(map[string]any{
		"config":      map[string]string{"COMPANY_NAME": "Acme"},
		"app_version": "1.0.0",
	})
	req := httptest.NewRequest(
		http.MethodPut,
		"/managed-app-bindings/00000000-0000-0000-0000-000000000010",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, testTenantID, out["tenant_id"])
}

// ── MA-10: PUT /managed-app-bindings/{app_id} missing config → 400 ────────────

func TestManagedApps_Binding_MissingConfig(t *testing.T) {
	r := newManagedAppsTenantRouter(&managedAppFakeDB{})

	body, _ := json.Marshal(map[string]any{"app_version": "1.0.0"})
	req := httptest.NewRequest(
		http.MethodPut,
		"/managed-app-bindings/00000000-0000-0000-0000-000000000010",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
