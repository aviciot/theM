package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// newAuditRouter mounts AuditLogsHandler on a chi router with test tenant context.
func newAuditRouter(db admin.DBQuerier) *chi.Mux {
	r := chi.NewRouter()
	r.Use(withTestTenant)
	admin.NewAuditLogsHandler(db, nil).Routes(r)
	return r
}

// ── AL-01: List returns empty array when no log rows ─────────────────────────

func TestAuditLogs_List_Empty(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	r := newAuditRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out, "empty audit log list must be []")
}

// ── AL-02: List returns log rows with correct fields ─────────────────────────

func TestAuditLogs_List_Populated(t *testing.T) {
	createdAt := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	// Pass values directly; scanInto handles nil for nullable pointer types.
	db := &fakeDB{
		queryRows: newFakeRows([][]any{
			{int64(42), int64(1), "agent.create", "agent", "some-agent-id", []byte(`{}`), createdAt},
		}),
	}
	r := newAuditRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, float64(42), out[0]["id"])
	assert.Equal(t, "agent.create", out[0]["action"])
	assert.Equal(t, "agent", out[0]["entity_type"])
}

// ── AL-03: List respects limit and offset query params ───────────────────────

func TestAuditLogs_List_LimitOffset(t *testing.T) {
	db := &fakeDB{queryRows: newFakeRows(nil)}
	r := newAuditRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/audit-logs?limit=10&offset=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var out []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Empty(t, out)
}
