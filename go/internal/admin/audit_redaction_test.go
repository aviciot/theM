package admin_test

// Production-path audit redaction tests (AR series).
//
// Unlike AL-09/10/11 which call admin.ChangesOf and manually simulate the
// handler's delete step, these tests call the actual Update handler end-to-end.
// The handler runs changesOf, deletes the secret key, and calls
// AuditWriter.Write. NewAuditWriterForTest injects a capturing querier so the
// WriteAuditLog SQL args can be inspected.
//
// Coverage:
//   AR-01  agent.update — auth_token absent in audit, auth_token_changed present

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
)

// captureQuerier wraps fakeDB and captures the details JSON written by
// WriteAuditLog (arg index 5 when arg count == 6).
type captureQuerier struct {
	fakeDB
	capturedDetailsJSON []byte
}

// ── AR-01: Agent Update handler does not write raw auth_token to audit ─────────

func TestAgentUpdate_AuditDoesNotContainRawAuthToken(t *testing.T) {
	cq := &captureQuerier{}
	cq.fakeDB.queryRows = newFakeRows(nil)
	cq.fakeDB.execFn = func(_ string, args ...any) error {
		// WriteAuditLog: (tenant_id, user_id, action, entity_type, entity_id, details)
		if len(args) >= 6 {
			if b, ok := args[5].([]byte); ok {
				cq.capturedDetailsJSON = b
			}
		}
		return nil
	}

	auditWriter := admin.NewAuditWriterForTest(cq)
	h := admin.NewAgentsHandler(cq, nil, nil, nil, nil, auditWriter)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	body, _ := json.Marshal(dal.AgentInput{
		Slug:        "my-agent",
		DisplayName: "My Agent",
		Transport:   "a2a_async",
		AuthToken:   "sk-super-secret-9999",
	})
	req := httptest.NewRequest(http.MethodPatch, "/agents/aaaaaaaa-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Update must succeed for audit to be written")
	require.NotNil(t, cq.capturedDetailsJSON, "AuditWriter.Write must have been called")

	var details map[string]any
	require.NoError(t, json.Unmarshal(cq.capturedDetailsJSON, &details))

	changes, ok := details["changes"].(map[string]any)
	require.True(t, ok, "details must have a 'changes' key")

	_, hasRaw := changes["auth_token"]
	assert.False(t, hasRaw, "raw auth_token must not appear in audit changes")
	assert.Equal(t, true, changes["auth_token_changed"], "auth_token_changed sentinel must be set")
}
