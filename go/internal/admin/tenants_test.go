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
	emailDomain *string // nil = no domain
	err         error   // when non-nil, Scan returns this
}

func (r *tenantFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	// Columns: id, slug, display_name, enabled, email_domain, created_at, updated_at
	vals := []any{r.id, r.slug, r.displayName, r.enabled, r.emailDomain, testNow, testNow}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		switch dp := d.(type) {
		case *string:
			if s, ok := vals[i].(string); ok {
				*dp = s
			}
		case **string:
			if vals[i] == nil {
				*dp = nil
			} else if s, ok := vals[i].(*string); ok {
				*dp = s
			}
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
	listRows    []*tenantFakeRow      // returned by Query (tenants list)
	memberRows  []*memberFakeRow      // returned by Query (members list); takes priority over listRows when set
	getRow      *tenantFakeRow        // returned by QueryRow
	createRow   *tenantFakeRow        // returned by ExecReturning (create)
	patchRow    admin.SingleRowScanner // returned by ExecReturning (patch); takes priority over createRow
	quotaRow    admin.SingleRowScanner // returned by QueryRow/ExecReturning for quota operations
	addMemberRow admin.SingleRowScanner // returned by ExecReturning (add member)
	execErr     error
}

func (d *tenantDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	if d.memberRows != nil {
		return &memberFakeRows{rows: d.memberRows}, nil
	}
	return &tenantFakeRows{rows: d.listRows}, nil
}

func (d *tenantDB) QueryRow(_ context.Context, sql string, _ ...any) admin.SingleRowScanner {
	// Quota GET uses QueryRow; detect by checking quotaRow is set and no getRow.
	if d.quotaRow != nil && d.getRow == nil {
		return d.quotaRow
	}
	if d.getRow != nil {
		return d.getRow
	}
	return &tenantFakeRow{err: pgx.ErrNoRows}
}

func (d *tenantDB) Exec(_ context.Context, _ string, _ ...any) error {
	return d.execErr
}

func (d *tenantDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if d.addMemberRow != nil {
		return d.addMemberRow
	}
	if d.quotaRow != nil && d.patchRow == nil && d.createRow == nil {
		return d.quotaRow
	}
	if d.patchRow != nil {
		return d.patchRow
	}
	if d.createRow != nil {
		return d.createRow
	}
	if d.execErr != nil {
		return &tenantFakeRow{err: d.execErr}
	}
	return &tenantFakeRow{err: pgx.ErrNoRows}
}

// tenantDetailFakeRow simulates the 8-column RETURNING from PatchTenant.
// Columns: id, slug, display_name, enabled, idp_configured, email_domain, created_at, updated_at
type tenantDetailFakeRow struct {
	id            string
	slug          string
	displayName   string
	enabled       bool
	idpConfigured bool
	emailDomain   *string
	err           error
}

func (r *tenantDetailFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	vals := []any{r.id, r.slug, r.displayName, r.enabled, r.idpConfigured, r.emailDomain, testNow, testNow}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		switch dp := d.(type) {
		case *string:
			if s, ok := vals[i].(string); ok {
				*dp = s
			}
		case **string:
			if vals[i] == nil {
				*dp = nil
			} else if s, ok := vals[i].(*string); ok {
				*dp = s
			}
		case *bool:
			*dp = vals[i].(bool)
		case *time.Time:
			*dp = vals[i].(time.Time)
		}
	}
	return nil
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

// ── TN-09: Patch success — display_name updated ───────────────────────────────

func TestTenants_Patch_Success(t *testing.T) {
	db := &tenantDB{
		patchRow: &tenantDetailFakeRow{
			id:          "00000000-0000-0000-0000-000000000001",
			slug:        "default",
			displayName: "Updated Name",
			enabled:     true,
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]string{"display_name": "Updated Name"})
	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "Updated Name", out["display_name"])
	assert.Equal(t, false, out["idp_configured"])
}

// ── TN-10: Patch returns 404 when tenant not found ───────────────────────────

func TestTenants_Patch_NotFound(t *testing.T) {
	db := &tenantDB{} // patchRow=nil, createRow=nil → pgx.ErrNoRows
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]bool{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000099", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── TN-11: Patch returns 400 on invalid JSON ──────────────────────────────────

func TestTenants_Patch_BadJSON(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000001", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-12: Patch sets idp_configured when idp_config is provided ─────────────

func TestTenants_Patch_IDPConfigured(t *testing.T) {
	db := &tenantDB{
		patchRow: &tenantDetailFakeRow{
			id:            "00000000-0000-0000-0000-000000000001",
			slug:          "default",
			displayName:   "Default",
			enabled:       true,
			idpConfigured: true,
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{
		"idp_config": map[string]string{
			"discovery_url": "https://accounts.google.com",
			"client_id":     "my-client-id",
			"client_secret": "secret",
			"redirect_uri":  "https://example.com/callback",
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, true, out["idp_configured"])
}

// ── Quota fake row ────────────────────────────────────────────────────────────

// quotaFakeRow simulates the 11-column quota SELECT/RETURNING row.
type quotaFakeRow struct {
	tenantID string
	plan     string
	err      error
}

func (r *quotaFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	// Columns: tenant_id, plan, max_agents, max_apps, max_mcp_servers,
	//          max_concurrent_runs, max_users, monthly_llm_tokens, monthly_runs,
	//          api_requests_per_minute, runs_per_minute
	ptrs := []any{&r.tenantID, &r.plan}
	nullInts := make([]*int, 7)
	// All nullable int cols are nil (no limits set)
	for i := 0; i < 7; i++ {
		ptrs = append(ptrs, &nullInts[i])
	}
	for i, d := range dest {
		if i >= len(ptrs) {
			break
		}
		switch dp := d.(type) {
		case *string:
			*dp = *ptrs[i].(*string)
		case **int:
			*dp = nil
		case **int64:
			*dp = nil
		}
	}
	return nil
}

// ── TN-13: GetQuota returns 404 when no quota row exists ─────────────────────

func TestTenants_GetQuota_NotFound(t *testing.T) {
	db := &tenantDB{} // no quotaRow → pgx.ErrNoRows
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000001/quota", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── TN-14: GetQuota returns 200 with quota row ────────────────────────────────

func TestTenants_GetQuota_Found(t *testing.T) {
	db := &tenantDB{
		quotaRow: &quotaFakeRow{
			tenantID: "00000000-0000-0000-0000-000000000001",
			plan:     "enterprise",
		},
	}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000001/quota", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "enterprise", out["plan"])
}

// ── TN-15: UpsertQuota returns 200 with saved row ────────────────────────────

func TestTenants_UpsertQuota_Success(t *testing.T) {
	db := &tenantDB{
		quotaRow: &quotaFakeRow{
			tenantID: "00000000-0000-0000-0000-000000000001",
			plan:     "pro",
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"plan": "pro"})
	req := httptest.NewRequest(http.MethodPut, "/tenants/00000000-0000-0000-0000-000000000001/quota", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "pro", out["plan"])
}

// ── TN-16: UpsertQuota returns 400 on invalid plan ───────────────────────────

func TestTenants_UpsertQuota_BadPlan(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"plan": "unknown-plan"})
	req := httptest.NewRequest(http.MethodPut, "/tenants/00000000-0000-0000-0000-000000000001/quota", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-17: UpsertQuota returns 400 on invalid JSON ───────────────────────────

func TestTenants_UpsertQuota_BadJSON(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodPut, "/tenants/00000000-0000-0000-0000-000000000001/quota", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── Member fake types ─────────────────────────────────────────────────────────

// memberFakeRow simulates one row from the ListMembers query (7 columns).
type memberFakeRow struct {
	id        string
	userID    int64
	tenantID  string
	role      string
	username  string
	email     string
	createdAt string
	err       error
}

func (r *memberFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	vals := []any{r.id, r.userID, r.tenantID, r.role, r.username, r.email, r.createdAt}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		switch dp := d.(type) {
		case *string:
			*dp = vals[i].(string)
		case *int64:
			*dp = vals[i].(int64)
		}
	}
	return nil
}

type memberFakeRows struct {
	rows []*memberFakeRow
	pos  int
}

func (r *memberFakeRows) Next() bool   { return r.pos < len(r.rows) }
func (r *memberFakeRows) Close() error { return nil }
func (r *memberFakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos]
	r.pos++
	return row.Scan(dest...)
}

// ── TN-18: ListMembers returns empty array when no members ───────────────────

func TestTenants_ListMembers_Empty(t *testing.T) {
	db := &tenantDB{memberRows: []*memberFakeRow{}}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000001/members", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out, "empty list must be []")
}

// ── TN-19: ListMembers returns members ───────────────────────────────────────

func TestTenants_ListMembers_Populated(t *testing.T) {
	db := &tenantDB{
		memberRows: []*memberFakeRow{
			{
				id: "aaaaaaaa-0000-0000-0000-000000000001", userID: 1,
				tenantID: "00000000-0000-0000-0000-000000000001", role: "super_admin",
				username: "admin", email: "admin@them.local", createdAt: "2026-09-02T12:00:00Z",
			},
		},
	}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants/00000000-0000-0000-0000-000000000001/members", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "admin", out[0]["username"])
	assert.Equal(t, "super_admin", out[0]["role"])
}

// ── TN-20: AddMember returns 201 with the new member ────────────────────────

func TestTenants_AddMember_Success(t *testing.T) {
	db := &tenantDB{
		addMemberRow: &memberFakeRow{
			id: "aaaaaaaa-0000-0000-0000-000000000002", userID: 2,
			tenantID: "00000000-0000-0000-0000-000000000001", role: "developer",
			createdAt: "2026-09-02T12:00:00Z",
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"user_id": 2, "role": "developer"})
	req := httptest.NewRequest(http.MethodPost, "/tenants/00000000-0000-0000-0000-000000000001/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "developer", out["role"])
}

// ── TN-21: AddMember returns 400 when user_id is missing ────────────────────

func TestTenants_AddMember_MissingUserID(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"role": "developer"})
	req := httptest.NewRequest(http.MethodPost, "/tenants/00000000-0000-0000-0000-000000000001/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-22: AddMember returns 400 when role is missing ───────────────────────

func TestTenants_AddMember_MissingRole(t *testing.T) {
	db := &tenantDB{}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"user_id": 2})
	req := httptest.NewRequest(http.MethodPost, "/tenants/00000000-0000-0000-0000-000000000001/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── TN-23: Patch sets email_domain ───────────────────────────────────────────

func TestTenants_Patch_EmailDomain(t *testing.T) {
	domain := "acme.com"
	db := &tenantDB{
		patchRow: &tenantDetailFakeRow{
			id:          "00000000-0000-0000-0000-000000000001",
			slug:        "default",
			displayName: "Default",
			enabled:     true,
			emailDomain: &domain,
		},
	}
	r := newTenantRouter(db)

	body, _ := json.Marshal(map[string]any{"email_domain": "acme.com"})
	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "acme.com", out["email_domain"])
}

// ── TN-24: Patch clears email_domain when sent as null ───────────────────────

func TestTenants_Patch_EmailDomain_Clear(t *testing.T) {
	db := &tenantDB{
		patchRow: &tenantDetailFakeRow{
			id:          "00000000-0000-0000-0000-000000000001",
			slug:        "default",
			displayName: "Default",
			enabled:     true,
			// emailDomain = nil → cleared
		},
	}
	r := newTenantRouter(db)

	body := []byte(`{"email_domain":null}`)
	req := httptest.NewRequest(http.MethodPatch, "/tenants/00000000-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	// email_domain omitted (omitempty) when nil
	_, hasEmailDomain := out["email_domain"]
	assert.False(t, hasEmailDomain, "nil email_domain must be omitted from response")
}

// ── TN-25: List returns email_domain in tenant rows when set ─────────────────

func TestTenants_List_WithEmailDomain(t *testing.T) {
	domain := "corp.example.com"
	db := &tenantDB{
		listRows: []*tenantFakeRow{
			{id: "00000000-0000-0000-0000-000000000001", slug: "corp", displayName: "Corp", enabled: true, emailDomain: &domain},
		},
	}
	r := newTenantRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/tenants", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "corp.example.com", out[0]["email_domain"])
}
