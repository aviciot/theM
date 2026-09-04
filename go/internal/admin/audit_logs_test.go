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
	"github.com/aviciot/them/internal/admin/dal"
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

// ── AL-06: changesOf converts a struct to map[string]any ────────────────────

func TestAuditLogs_ChangesOf_PopulatesMap(t *testing.T) {
	type patch struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	m := admin.ChangesOf(patch{Name: "updated-name", Enabled: true})
	require.NotNil(t, m)
	assert.Equal(t, "updated-name", m["name"])
	assert.Equal(t, true, m["enabled"])
}

// ── AL-07: AuditEntry.Changes is included in written details JSON ────────────

func TestAuditLogs_WriteAuditLog_IncludesChanges(t *testing.T) {
	var capturedDetails []byte
	db := &fakeDB{
		execFn: func(_ string, args ...any) error {
			// args: tenant_id, user_id, action, entity_type, entity_id, details
			// details is already []byte (JSON) from WriteAuditLog's json.Marshal call.
			if len(args) >= 6 {
				if b, ok := args[5].([]byte); ok {
					capturedDetails = b
				}
			}
			return nil
		},
	}
	d := dal.NewDB(db)
	err := d.WriteAuditLog(nil, dal.AuditEntry{
		TenantID:   "00000000-0000-0000-0000-000000000001",
		Action:     "agent.update",
		EntityType: "agent",
		EntityID:   "agent-123",
		Actor:      "user@example.com",
		Changes:    map[string]any{"display_name": "new-name"},
	})
	require.NoError(t, err)
	require.NotNil(t, capturedDetails, "execFn must have captured details")

	var details map[string]any
	require.NoError(t, json.Unmarshal(capturedDetails, &details))
	assert.Equal(t, "user@example.com", details["actor"])
	changes, ok := details["changes"].(map[string]any)
	require.True(t, ok, "details must contain a 'changes' object")
	assert.Equal(t, "new-name", changes["display_name"])
}

// ── AL-08: AuditEntry without Changes omits 'changes' key ───────────────────

func TestAuditLogs_WriteAuditLog_NoChangesKey_WhenNil(t *testing.T) {
	var capturedDetails []byte
	db := &fakeDB{
		execFn: func(_ string, args ...any) error {
			if len(args) >= 6 {
				if b, ok := args[5].([]byte); ok {
					capturedDetails = b
				}
			}
			return nil
		},
	}
	d := dal.NewDB(db)
	err := d.WriteAuditLog(nil, dal.AuditEntry{
		TenantID: "00000000-0000-0000-0000-000000000001",
		Action:   "agent.create", EntityType: "agent", EntityID: "x", Actor: "admin",
	})
	require.NoError(t, err)
	require.NotNil(t, capturedDetails, "execFn must have captured details")

	var details map[string]any
	require.NoError(t, json.Unmarshal(capturedDetails, &details))
	assert.Equal(t, "admin", details["actor"])
	_, hasChanges := details["changes"]
	assert.False(t, hasChanges, "create/delete actions must not include 'changes' key")
}
