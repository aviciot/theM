package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// ── fakeDefDB — minimal Querier fake for definitions tests ────────────────────
//
// The handler layer calls dal.NewDB(db) → *dal.DB, which wraps a dal.Querier.
// All DAL methods on *dal.DB use Query / QueryRow / Exec / ExecReturning, so
// we only need to implement those four to satisfy dal.Querier.

type fakeDefDB struct {
	// Query control
	queryRows    *fakeRows         // returned by Query (reuses fakeRows from admin_test.go)
	queryRowScan fakeDefSingleRow  // returned by QueryRow

	// ExecReturning control
	execRetStr string // UUID returned by ExecReturning on success
	execRetErr error  // error returned by ExecReturning

	// Track calls to distinguish not-found vs conflict in update/delete tests.
	// When set, the SECOND call to ExecReturning (or QueryRow) uses these.
	secondExecRetStr string
	secondExecRetErr error
	callCount        int
}

// fakeDefSingleRow is a configurable SingleRowScanner.
type fakeDefSingleRow interface {
	admin.SingleRowScanner
}

// fakeDefIntRow scans a single int (for GetNextRevision).
type fakeDefIntRow struct{ val int }

func (r *fakeDefIntRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*int); ok {
		*d = r.val
		return nil
	}
	return errors.New("fakeDefIntRow: cannot scan into non-int")
}

// fakeDefStrRow scans a single string (for RETURNING id::text).
type fakeDefStrRow struct {
	val string
	err error
}

func (r *fakeDefStrRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(*string); ok {
		*d = r.val
		return nil
	}
	return errors.New("fakeDefStrRow: cannot scan into non-string")
}

// fakeDefFullRow scans a full AppDefinition row (for GetDefinition).
type fakeDefFullRow struct {
	def dal.AppDefinition
	err error
}

func (r *fakeDefFullRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	// Scan order must match the SELECT column list in GetDefinition:
	// id, application_id, tenant_id, revision, status, definition, definition_hash, created_at, published_at
	if len(dest) < 9 {
		return errors.New("fakeDefFullRow: not enough scan targets")
	}
	if d, ok := dest[0].(*string); ok {
		*d = r.def.ID
	}
	if d, ok := dest[1].(*string); ok {
		*d = r.def.ApplicationID
	}
	if d, ok := dest[2].(*string); ok {
		*d = r.def.TenantID
	}
	if d, ok := dest[3].(*int); ok {
		*d = r.def.Revision
	}
	if d, ok := dest[4].(*string); ok {
		*d = r.def.Status
	}
	if d, ok := dest[5].(*json.RawMessage); ok {
		*d = r.def.Definition
	}
	if d, ok := dest[6].(*string); ok {
		*d = r.def.DefinitionHash
	}
	if d, ok := dest[7].(*string); ok {
		*d = r.def.CreatedAt
	}
	if d, ok := dest[8].(**string); ok {
		*d = r.def.PublishedAt
	}
	return nil
}

// Implement dal.Querier for fakeDefDB.

func (f *fakeDefDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	if f.queryRows == nil {
		return newFakeRows(nil), nil
	}
	return f.queryRows, nil
}

func (f *fakeDefDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	f.callCount++
	if f.queryRowScan != nil {
		return f.queryRowScan
	}
	return &fakeRow{err: pgx.ErrNoRows}
}

func (f *fakeDefDB) Exec(_ context.Context, _ string, _ ...any) error {
	return nil
}

func (f *fakeDefDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	f.callCount++
	if f.callCount > 1 && f.secondExecRetStr != "" {
		return &fakeDefStrRow{val: f.secondExecRetStr, err: f.secondExecRetErr}
	}
	if f.callCount > 1 && f.secondExecRetErr != nil {
		return &fakeDefStrRow{err: f.secondExecRetErr}
	}
	return &fakeDefStrRow{val: f.execRetStr, err: f.execRetErr}
}

// ── Helper: build a chi router with DefinitionsHandler + tenant middleware ────

func defRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h := admin.NewDefinitionsHandler(db)
	h.Routes(r)
	return r
}

// validDefBody returns a minimal valid definition body JSON.
func validDefBody() []byte {
	b, _ := json.Marshal(map[string]any{
		"definition": map[string]any{
			"components": []map[string]any{
				{"instance_id": "comp-1"},
			},
		},
	})
	return b
}

// ── Tests S1-42 to S1-53 ─────────────────────────────────────────────────────

// S1-42: CreateDraft — happy path, returns 201 with id+revision.
func TestS1_42_CreateDraft_HappyPath(t *testing.T) {
	db := &fakeDefDB{
		// QueryRow is called by GetNextRevision → returns revision=1.
		queryRowScan: &fakeDefIntRow{val: 1},
		// ExecReturning is called by CreateDefinition → returns new UUID.
		execRetStr: "def-uuid-1",
	}
	// Reset callCount so ExecReturning is treated as first call.
	db.callCount = 0

	// Override: QueryRow must return int for GetNextRevision, then ExecReturning for CreateDefinition.
	// Since GetNextRevision uses QueryRow and CreateDefinition uses ExecReturning, we need
	// a fakeDefDB that correctly routes them.
	dbFixed := &fakeDefDBMulti{
		nextRevision: 1,
		createID:     "def-uuid-1",
	}

	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/definitions",
		bytes.NewReader(validDefBody()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "def-uuid-1", out["id"])
	assert.EqualValues(t, 1, out["revision"])
}

// S1-43: CreateDraft — application not found (wrong tenant) → 404.
func TestS1_43_CreateDraft_AppNotFound(t *testing.T) {
	dbFixed := &fakeDefDBMulti{
		nextRevision: 1,
		createErr:    pgx.ErrNoRows, // sub-SELECT found no app row
	}

	r := defRouter(dbFixed)
	req := httptest.NewRequest(http.MethodPost, "/applications/app-unknown/definitions",
		bytes.NewReader(validDefBody()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// S1-44: CreateDraft — duplicate instance_id in components → 422.
func TestS1_44_CreateDraft_DuplicateInstanceID(t *testing.T) {
	dbFixed := &fakeDefDBMulti{nextRevision: 1, createID: "def-uuid-1"}
	r := defRouter(dbFixed)

	body, _ := json.Marshal(map[string]any{
		"definition": map[string]any{
			"components": []map[string]any{
				{"instance_id": "dup"},
				{"instance_id": "dup"},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/definitions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body.String())
}

// S1-45: CreateDraft — malformed definition (not an object) → 400.
func TestS1_45_CreateDraft_NotAnObject(t *testing.T) {
	dbFixed := &fakeDefDBMulti{nextRevision: 1, createID: "def-uuid-1"}
	r := defRouter(dbFixed)

	body, _ := json.Marshal(map[string]any{
		"definition": []string{"not", "an", "object"},
	})
	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/definitions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// S1-46: GetDefinition — happy path → 200 with definition body.
func TestS1_46_GetDefinition_HappyPath(t *testing.T) {
	pubAt := "2026-08-01T00:00:00Z"
	def := dal.AppDefinition{
		ID:             "def-uuid-1",
		ApplicationID:  "app-1",
		TenantID:       testTenantID,
		Revision:       1,
		Status:         "draft",
		Definition:     json.RawMessage(`{"components":[]}`),
		DefinitionHash: "sha256:abc",
		CreatedAt:      "2026-08-01T00:00:00Z",
		PublishedAt:    &pubAt,
	}
	dbFixed := &fakeDefDBMulti{getDef: def}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodGet, "/applications/app-1/definitions/def-uuid-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var out dal.AppDefinition
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "def-uuid-1", out.ID)
	assert.Equal(t, "draft", out.Status)
}

// S1-47: GetDefinition — wrong tenant (UUID guessing) → 404.
func TestS1_47_GetDefinition_WrongTenant(t *testing.T) {
	dbFixed := &fakeDefDBMulti{getDefErr: pgx.ErrNoRows}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodGet, "/applications/app-1/definitions/def-uuid-bad", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// S1-48: ListDefinitions — returns ordered list → 200.
func TestS1_48_ListDefinitions_ReturnsList(t *testing.T) {
	defs := []dal.AppDefinition{
		{ID: "def-2", Revision: 2, Status: "draft", Definition: json.RawMessage(`{}`), CreatedAt: "2026-08-02T00:00:00Z"},
		{ID: "def-1", Revision: 1, Status: "draft", Definition: json.RawMessage(`{}`), CreatedAt: "2026-08-01T00:00:00Z"},
	}
	dbFixed := &fakeDefDBMulti{listDefs: defs}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodGet, "/applications/app-1/definitions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var out []dal.AppDefinition
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 2)
	assert.Equal(t, "def-2", out[0].ID, "highest revision first")
}

// S1-49: UpdateDraft — happy path → 200.
func TestS1_49_UpdateDraft_HappyPath(t *testing.T) {
	dbFixed := &fakeDefDBMulti{
		updateID: "def-uuid-1", // ExecReturning returns this UUID
	}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/definitions/def-uuid-1",
		bytes.NewReader(validDefBody()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, "def-uuid-1", out["id"])
	assert.Equal(t, true, out["updated"])
}

// S1-50: UpdateDraft — attempt to update published → 409.
func TestS1_50_UpdateDraft_PublishedConflict(t *testing.T) {
	// UpdateDraftDefinition returns ErrNoRows (status != 'draft' in WHERE).
	// GetDefinition then returns a published definition.
	published := dal.AppDefinition{
		ID:     "def-uuid-1",
		Status: "published",
	}
	dbFixed := &fakeDefDBMulti{
		updateErr: pgx.ErrNoRows,
		getDef:    published,
	}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodPut, "/applications/app-1/definitions/def-uuid-1",
		bytes.NewReader(validDefBody()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

// S1-51: UpdateDraft — wrong appID → 404.
func TestS1_51_UpdateDraft_WrongApp(t *testing.T) {
	// UpdateDraftDefinition returns ErrNoRows; GetDefinition also returns ErrNoRows.
	dbFixed := &fakeDefDBMulti{
		updateErr: pgx.ErrNoRows,
		getDefErr: pgx.ErrNoRows,
	}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodPut, "/applications/wrong-app/definitions/def-uuid-1",
		bytes.NewReader(validDefBody()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// S1-52: DeleteDraft — happy path → 204.
func TestS1_52_DeleteDraft_HappyPath(t *testing.T) {
	dbFixed := &fakeDefDBMulti{deleteID: "def-uuid-1"}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodDelete, "/applications/app-1/definitions/def-uuid-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body.String())
}

// S1-53: DeleteDraft — attempt to delete published → 409.
func TestS1_53_DeleteDraft_PublishedConflict(t *testing.T) {
	published := dal.AppDefinition{
		ID:     "def-uuid-1",
		Status: "published",
	}
	dbFixed := &fakeDefDBMulti{
		deleteErr: pgx.ErrNoRows,
		getDef:    published,
	}
	r := defRouter(dbFixed)

	req := httptest.NewRequest(http.MethodDelete, "/applications/app-1/definitions/def-uuid-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

// ── fakeDefDBMulti — richer fake routing QueryRow vs ExecReturning ────────────
//
// This fake routes calls correctly:
// - QueryRow is used by: GetNextRevision (returns int), GetDefinition (returns full row)
// - ExecReturning is used by: CreateDefinition, UpdateDraftDefinition, DeleteDraftDefinition
// - Query is used by: ListDefinitions
//
// State machine: after the first ExecReturning call, subsequent calls to
// QueryRow return getDefRow (for the "distinguish not-found vs published" path).

type fakeDefDBMulti struct {
	nextRevision int
	createID     string
	createErr    error

	getDef    dal.AppDefinition
	getDefErr error

	listDefs []dal.AppDefinition

	updateID  string
	updateErr error

	deleteID  string
	deleteErr error

	// appTenantID: when non-empty, the first QueryRow returns this string (for GetAppTenantID).
	// Used in Validate/Publish tests where resolveAndAuthorizeAppTenant is called first.
	appTenantID    string
	appTenantIDErr error

	// callCountExecRet tracks ExecReturning calls so we can multiplex.
	callCountExecRet int
	// callCountQueryRow tracks QueryRow calls so we can multiplex.
	callCountQueryRow int
}

func (f *fakeDefDBMulti) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	// ListDefinitions — use a typed rows scanner that knows AppDefinition layout.
	return &fakeDefRows{defs: f.listDefs}, nil
}

// fakeDefRows is a RowScanner that scans AppDefinition rows directly.
type fakeDefRows struct {
	defs []dal.AppDefinition
	pos  int
}

func (r *fakeDefRows) Next() bool   { return r.pos < len(r.defs) }
func (r *fakeDefRows) Close() error { return nil }
func (r *fakeDefRows) Scan(dest ...any) error {
	if r.pos >= len(r.defs) {
		return errors.New("no more rows")
	}
	d := r.defs[r.pos]
	r.pos++
	// Scan order matches the SELECT in ListDefinitions:
	// id, application_id, tenant_id, revision, status, definition, definition_hash, created_at, published_at
	if len(dest) < 9 {
		return errors.New("fakeDefRows: not enough scan targets")
	}
	if v, ok := dest[0].(*string); ok {
		*v = d.ID
	}
	if v, ok := dest[1].(*string); ok {
		*v = d.ApplicationID
	}
	if v, ok := dest[2].(*string); ok {
		*v = d.TenantID
	}
	if v, ok := dest[3].(*int); ok {
		*v = d.Revision
	}
	if v, ok := dest[4].(*string); ok {
		*v = d.Status
	}
	if v, ok := dest[5].(*json.RawMessage); ok {
		*v = d.Definition
	}
	if v, ok := dest[6].(*string); ok {
		*v = d.DefinitionHash
	}
	if v, ok := dest[7].(*string); ok {
		*v = d.CreatedAt
	}
	if v, ok := dest[8].(**string); ok {
		*v = d.PublishedAt
	}
	return nil
}

func (f *fakeDefDBMulti) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	f.callCountQueryRow++
	// When appTenantID is set, the first call is GetAppTenantID — return a string UUID.
	if f.callCountQueryRow == 1 && (f.appTenantID != "" || f.appTenantIDErr != nil) {
		return &fakeDefStrRow{val: f.appTenantID, err: f.appTenantIDErr}
	}
	// First QueryRow call (no appTenantID set): GetNextRevision — returns int.
	if f.callCountQueryRow == 1 && f.nextRevision != 0 {
		return &fakeDefIntRow{val: f.nextRevision}
	}
	// Subsequent calls: GetDefinition — returns full AppDefinition row.
	return &fakeDefFullRow{def: f.getDef, err: f.getDefErr}
}

func (f *fakeDefDBMulti) Exec(_ context.Context, _ string, _ ...any) error {
	return nil
}

func (f *fakeDefDBMulti) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	f.callCountExecRet++
	switch f.callCountExecRet {
	case 1:
		// CreateDefinition or UpdateDraftDefinition or DeleteDraftDefinition (first call).
		if f.createErr != nil {
			return &fakeDefStrRow{err: f.createErr}
		}
		if f.updateErr != nil {
			return &fakeDefStrRow{err: f.updateErr}
		}
		if f.deleteErr != nil {
			return &fakeDefStrRow{err: f.deleteErr}
		}
		id := f.createID
		if id == "" {
			id = f.updateID
		}
		if id == "" {
			id = f.deleteID
		}
		return &fakeDefStrRow{val: id}
	default:
		// Should not be called in these tests.
		return &fakeDefStrRow{err: errors.New("unexpected ExecReturning call")}
	}
}

// ── Cross-tenant authorization tests ─────────────────────────────────────────
//
// resolveAndAuthorizeAppTenant enforces:
//   - Ordinary tenant admin (role="admin") may only access apps in their own tenant.
//   - super_admin may access apps in any tenant.
//
// These tests use a separate router that injects both JWT claims and tenantctx.

const otherTenantID = "ffffffff-0000-0000-0000-000000000002"

// withClaimsAndTenant is a middleware that injects both auth.Claims (with the
// given role) and the caller's tenantID into the request context.
func withClaimsAndTenant(callerTenantID, role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tenantctx.WithTenantID(r.Context(), callerTenantID)
			ctx = auth.WithClaims(ctx, &auth.Claims{
				TenantID: callerTenantID,
				Roles:    []string{role},
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func defRouterWithClaims(db admin.DBQuerier, callerTenantID, role string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(withClaimsAndTenant(callerTenantID, role))
	h := admin.NewDefinitionsHandlerWithRegistry(db, nil)
	h.Routes(r)
	return r
}

// S1-54: Validate — ordinary tenant admin accessing own tenant's app → 200.
func TestS1_54_Validate_OwnTenant_Allowed(t *testing.T) {
	// App belongs to testTenantID; caller is admin in testTenantID.
	db := &fakeDefDBMulti{
		appTenantID: testTenantID, // GetAppTenantID returns caller's own tenant
		getDef: dal.AppDefinition{
			ID:            "def-uuid-1",
			ApplicationID: "app-1",
			TenantID:      testTenantID,
			Revision:      1,
			Status:        "draft",
			Definition:    []byte(`{"components":[],"entry_points":[],"connections":[]}`),
			DefinitionHash: "sha256:abc",
			CreatedAt:     "2026-01-01T00:00:00Z",
		},
	}
	r := defRouterWithClaims(db, testTenantID, "admin")

	req := httptest.NewRequest(http.MethodPost, "/applications/app-1/definitions/def-uuid-1/validate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// S1-55: Validate — ordinary tenant admin (role="admin") accessing a different
// tenant's app → 403. The admin must not be able to scope-elevate by guessing an appID.
func TestS1_55_Validate_CrossTenant_AdminDenied(t *testing.T) {
	// App belongs to otherTenantID; caller is admin in testTenantID.
	db := &fakeDefDBMulti{
		appTenantID: otherTenantID, // GetAppTenantID returns a different tenant
	}
	r := defRouterWithClaims(db, testTenantID, "admin")

	req := httptest.NewRequest(http.MethodPost, "/applications/other-app/definitions/def-uuid-1/validate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
}

// S1-56: Validate — super_admin accessing a different tenant's app → 200.
func TestS1_56_Validate_CrossTenant_SuperAdminAllowed(t *testing.T) {
	// App belongs to otherTenantID; caller is super_admin in testTenantID.
	db := &fakeDefDBMulti{
		appTenantID: otherTenantID,
		getDef: dal.AppDefinition{
			ID:            "def-uuid-1",
			ApplicationID: "other-app",
			TenantID:      otherTenantID,
			Revision:      1,
			Status:        "draft",
			Definition:    []byte(`{"components":[],"entry_points":[],"connections":[]}`),
			DefinitionHash: "sha256:xyz",
			CreatedAt:     "2026-01-01T00:00:00Z",
		},
	}
	r := defRouterWithClaims(db, testTenantID, "super_admin")

	req := httptest.NewRequest(http.MethodPost, "/applications/other-app/definitions/def-uuid-1/validate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// S1-57: Publish — ordinary tenant admin accessing a different tenant's app → 403.
func TestS1_57_Publish_CrossTenant_AdminDenied(t *testing.T) {
	db := &fakeDefDBMulti{
		appTenantID: otherTenantID,
	}
	r := defRouterWithClaims(db, testTenantID, "admin")

	req := httptest.NewRequest(http.MethodPost, "/applications/other-app/definitions/def-uuid-1/publish", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
}
