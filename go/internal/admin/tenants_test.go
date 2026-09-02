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

// ── tenantFakeDB ─────────────────────────────────────────────────────────────
//
// A purpose-built fake for tenant tests. It implements admin.DBQuerier with
// in-memory tenant rows. Deliberately separate from the general fakeDB so the
// two don't grow coupling.

var testNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

type tenantFakeRow struct {
	id          string
	slug        string
	displayName string
	enabled     bool
	err         error // when non-nil, Scan returns this
}

func (r *tenantFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	vals := []any{r.id, r.slug, r.displayName, r.enabled, testNow, testNow}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		switch dp := d.(type) {
		case *string:
			*dp = vals[i].(string)
		case *bool:
			*dp = vals[i].(bool)
		case *time.Time:
			*dp = vals[i].(time.Time)
		}
	}
	return nil
}

type tenantFakeRows struct {
	rows []*tenantFakeRow
	pos  int
}

func (r *tenantFakeRows) Next() bool   { return r.pos < len(r.rows) }
func (r *tenantFakeRows) Close() error { return nil }
func (r *tenantFakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos]
	r.pos++
	return row.Scan(dest...)
}

type tenantDB struct {
	listRows   []*tenantFakeRow // returned by Query
	getRow     *tenantFakeRow   // returned by QueryRow
	createRow  *tenantFakeRow   // returned by ExecReturning
	execErr    error
}

func (d *tenantDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return &tenantFakeRows{rows: d.listRows}, nil
}

func (d *tenantDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.getRow != nil {
		return d.getRow
	}
	return &tenantFakeRow{err: pgx.ErrNoRows}
}

func (d *tenantDB) Exec(_ context.Context, _ string, _ ...any) error {
	return d.execErr
}

func (d *tenantDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.createRow != nil {
		return d.createRow
	}
	if d.execErr != nil {
		return &tenantFakeRow{err: d.execErr}
	}
	return &tenantFakeRow{err: pgx.ErrNoRows}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newTenantRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(withTestTenant) // inject bootstrap tenant into context
	admin.NewTenantsHandler(db).Routes(r)
	return r
}

// ── TN-01: List returns empty array when no tenants ───────────────────────────

func TestTenants_List_Empty(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out, "empty list must be []")
}

// ── TN-02: List returns all tenants ──────────────────────────────────────────

func TestTenants_List_Populated(t *testing.T) {
	db := &tenantDB{
		listRows: []*tenantFakeRow{
			{id: "00000000-0000-0000-0000-000000000001", slug: "default", displayName: "Default", enabled: true},
			{id: "00000000-0000-0000-0000-000000000002", slug: "acme", displayName: "Acme Corp", enabled: true},
		},
	}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 2)
	assert.Equal(t, "default", out[0]["slug"])
	assert.Equal(t, "acme", out[1]["slug"])
}

// ── TN-03: Get returns a single tenant ───────────────────────────────────────

func TestTenants_Get_Found(t *testing.T) {
	db := &tenantDB{
		getRow: &tenantFakeRow{
			id:          "00000000-0000-0000-0000-000000000001",
			slug:        "default",
			displayName: "Default Tenant",
			enabled:     true,
		},
	}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "default", out["slug"])
	assert.Equal(t, "Default Tenant", out["display_name"])
}

// ── TN-04: Get returns 404 when tenant not found ──────────────────────────────

func TestTenants_Get_NotFound(t *testing.T) {
	db := &tenantDB{} // getRow = nil → NoRows
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000099", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── TN-05: Create returns 201 with the new tenant ────────────────────────────

func TestTenants_Create_Success(t *testing.T) {
	db := &tenantDB{
		createRow: &tenantFakeRow{
			id:          "00000000-0000-0000-0000-000000000002",
			slug:        "acme",
			displayName: "Acme Corp",
			enabled:     true,
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]string{"slug": "acme", "display_name": "Acme Corp"})
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "acme", out["slug"])
	assert.Equal(t, "Acme Corp", out["display_name"])
}

// ── TN-06: Create returns 400 when slug is missing ───────────────────────────

func TestTenants_Create_MissingSlug(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]string{"display_name": "No Slug Corp"})
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-07: Create returns 400 when display_name is missing ───────────────────

func TestTenants_Create_MissingDisplayName(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]string{"slug": "no-name"})
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-08: Create returns 400 when body is invalid JSON ──────────────────────

func TestTenants_Create_BadJSON(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
