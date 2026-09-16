package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// hilApprovalFixture holds column values for a hil_approvals row.
type hilApprovalFixture struct {
	id, tenantID, appID, runID, nodeID string
	approverRole, prompt, fallback     string
	status, comment                    string
}

// hilFakeRow scans a hilApprovalFixture into GetHILApproval's scan list.
// Column order matches GetHILApproval: id, tenant_id, application_id, run_id,
// node_id, approver_role, prompt, fallback_action, status, comment, decided_at, created_at.
type hilFakeRow struct {
	a   *hilApprovalFixture
	err error
}

func (r *hilFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.a == nil {
		return pgx.ErrNoRows
	}
	now := time.Now()
	var nilTime *time.Time
	srcs := []any{
		r.a.id, r.a.tenantID, r.a.appID, r.a.runID, r.a.nodeID,
		r.a.approverRole, r.a.prompt, r.a.fallback, r.a.status,
		r.a.comment, nilTime, now,
	}
	for i, d := range dest {
		if i >= len(srcs) {
			break
		}
		switch v := d.(type) {
		case *string:
			if s, ok := srcs[i].(string); ok {
				*v = s
			}
		case **time.Time:
			if t, ok := srcs[i].(*time.Time); ok {
				*v = t
			}
		case *time.Time:
			if t, ok := srcs[i].(time.Time); ok {
				*v = t
			}
		}
	}
	return nil
}

// hilTestDB is a minimal DBQuerier stub for HIL approval handler tests.
type hilTestDB struct {
	approval *hilApprovalFixture
	rowErr   error
	execErr  error
}

func (f *hilTestDB) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &hilFakeRow{a: f.approval, err: f.rowErr}
}

func (f *hilTestDB) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}

func (f *hilTestDB) Exec(_ context.Context, _ string, _ ...any) error {
	return f.execErr
}

func (f *hilTestDB) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{err: errors.New("not implemented")}
}

// defaultHILApproval is the pending HIL fixture used in most tests.
var defaultHILApproval = &hilApprovalFixture{
	id:           "hil-id-1",
	tenantID:     testTenantID,
	appID:        "app-id-1",
	runID:        "run-id-1",
	nodeID:       "node-1",
	approverRole: "admin",
	prompt:       "Please approve",
	fallback:     "reject",
	status:       "pending",
	comment:      "",
}

// withAdminClaims injects admin JWT claims + tenant into the request context.
func withAdminClaims(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.Claims{
			UserID:   1,
			Username: "admin",
			Roles:    []string{"admin"},
			TenantID: testTenantID,
		}
		ctx := auth.WithClaims(r.Context(), claims)
		ctx = tenantctx.WithTenantID(ctx, testTenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withViewerClaims injects viewer (insufficient for HIL) claims.
func withViewerClaims(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.Claims{
			UserID:   2,
			Username: "viewer",
			Roles:    []string{"viewer"},
			TenantID: testTenantID,
		}
		ctx := auth.WithClaims(r.Context(), claims)
		ctx = tenantctx.WithTenantID(ctx, testTenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AF-HIL-01: Approve — signals Temporal and returns 200 with status=approved.
func TestHILApprove_Success(t *testing.T) {
	db := &hilTestDB{approval: defaultHILApproval}
	temp := &fakeTemporal{}
	h := admin.NewHILApprovalsHandler(db, temp)

	r := chi.NewRouter()
	r.Use(withAdminClaims)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost,
		"/runs/run-id-1/hil/node-1/approve",
		strings.NewReader(`{"comment":"looks good"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotEmpty(t, temp.signaled, "Temporal must be signaled")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "approved", resp["status"])
	assert.Equal(t, "run-id-1", resp["run_id"])
	assert.Equal(t, "node-1", resp["node_id"])
}

// AF-HIL-02: Reject — signals Temporal and returns 200 with status=rejected.
func TestHILReject_Success(t *testing.T) {
	db := &hilTestDB{approval: defaultHILApproval}
	temp := &fakeTemporal{}
	h := admin.NewHILApprovalsHandler(db, temp)

	r := chi.NewRouter()
	r.Use(withAdminClaims)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost,
		"/runs/run-id-1/hil/node-1/reject",
		strings.NewReader(`{"comment":"not safe"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotEmpty(t, temp.signaled)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "rejected", resp["status"])
}

// AF-HIL-03: Not found — returns 404, no Temporal signal.
func TestHILApprove_NotFound(t *testing.T) {
	db := &hilTestDB{rowErr: pgx.ErrNoRows}
	temp := &fakeTemporal{}
	h := admin.NewHILApprovalsHandler(db, temp)

	r := chi.NewRouter()
	r.Use(withAdminClaims)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/runs/run-id-1/hil/node-1/approve", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, temp.signaled, "must not signal when row not found")
}

// AF-HIL-04: Insufficient role — returns 403, no Temporal signal.
func TestHILApprove_InsufficientRole(t *testing.T) {
	db := &hilTestDB{approval: defaultHILApproval}
	temp := &fakeTemporal{}
	h := admin.NewHILApprovalsHandler(db, temp)

	r := chi.NewRouter()
	r.Use(withViewerClaims)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/runs/run-id-1/hil/node-1/approve", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, temp.signaled, "must not signal on RBAC failure")
}

// AF-HIL-05: Already decided — returns 409.
func TestHILApprove_AlreadyDecided(t *testing.T) {
	decided := *defaultHILApproval
	decided.status = "approved"
	db := &hilTestDB{approval: &decided}
	temp := &fakeTemporal{}
	h := admin.NewHILApprovalsHandler(db, temp)

	r := chi.NewRouter()
	r.Use(withAdminClaims)
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPost, "/runs/run-id-1/hil/node-1/approve", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Empty(t, temp.signaled)
}
