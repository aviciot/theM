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
//   AR-02  mcp_server.update — probe_token absent in audit, probe_token_changed present
//   AR-03  tenant.patch — idp_config.client_secret absent in audit, client_secret_changed present

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

// ── AR-02: MCP server Update handler does not write raw probe_token to audit ──

func TestMCPServerUpdate_AuditDoesNotContainRawProbeToken(t *testing.T) {
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
	h := admin.NewMCPServersHandlerForTest(cq, auditWriter)

	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)

	probeToken := "super-secret-probe-token-9999"
	body, _ := json.Marshal(map[string]any{
		"name":        "my-mcp-server",
		"probe_token": probeToken,
	})
	req := httptest.NewRequest(http.MethodPatch, "/mcp-servers/bbbbbbbb-0000-0000-0000-000000000001", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Update must succeed for audit to be written")
	require.NotNil(t, cq.capturedDetailsJSON, "AuditWriter.Write must have been called")

	var details map[string]any
	require.NoError(t, json.Unmarshal(cq.capturedDetailsJSON, &details))

	changes, ok := details["changes"].(map[string]any)
	require.True(t, ok, "details must have a 'changes' key")

	_, hasRaw := changes["probe_token"]
	assert.False(t, hasRaw, "raw probe_token must not appear in audit changes")
	assert.Equal(t, true, changes["probe_token_changed"], "probe_token_changed sentinel must be set")
}

// ── AR-03: Tenant Patch handler does not write raw client_secret to audit ──────

func TestTenantPatch_AuditDoesNotContainRawClientSecret(t *testing.T) {
	cq := &captureQuerier{}
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
	h := admin.NewTenantsHandler(cq, auditWriter)

	r := chi.NewRouter()
	h.Routes(r)

	tenantID := "cccccccc-0000-0000-0000-000000000001"
	body, _ := json.Marshal(map[string]any{
		"idp_config": map[string]any{
			"provider":      "oidc",
			"client_id":     "my-client",
			"client_secret": "super-secret-oidc-secret-9999",
			"issuer":        "https://idp.example.com",
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/tenants/"+tenantID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "Patch must succeed for audit to be written")
	require.NotNil(t, cq.capturedDetailsJSON, "AuditWriter.Write must have been called")

	var details map[string]any
	require.NoError(t, json.Unmarshal(cq.capturedDetailsJSON, &details))

	changes, ok := details["changes"].(map[string]any)
	require.True(t, ok, "details must have a 'changes' key")

	// client_secret must not appear anywhere in the audit — not at top level or nested
	_, hasTopLevel := changes["client_secret"]
	assert.False(t, hasTopLevel, "raw client_secret must not appear at top level in audit changes")
	if idpMap, ok := changes["idp_config"].(map[string]any); ok {
		_, hasNested := idpMap["client_secret"]
		assert.False(t, hasNested, "raw client_secret must not appear nested in idp_config in audit changes")
	}
	assert.Equal(t, true, changes["client_secret_changed"], "client_secret_changed sentinel must be set")
}
