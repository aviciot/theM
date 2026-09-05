package admin_test

// Tests for the three new agent action endpoints:
//   POST /agents/discover
//   POST /agents/{id}/test
//   POST /agents/{id}/security-scan
//
// Uses httptest.NewServer for mock HTTP backends. Reuses fakeDB / fakeCache /
// fakeRow / stringIDRow from admin_test.go (same _test package).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin"
)

// ── agentQuerier ──────────────────────────────────────────────────────────────
//
// agentQuerier is a DBQuerier that returns a queue of SingleRowScanners for
// QueryRow. Used to supply agent rows (23 columns) plus optional token rows
// to the action handlers.

type agentQuerier struct {
	queue   []admin.SingleRowScanner
	qpos    int
	execErr error
	execRet string
}

func (q *agentQuerier) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}

func (q *agentQuerier) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if q.qpos < len(q.queue) {
		r := q.queue[q.qpos]
		q.qpos++
		return r
	}
	return &fakeRow{err: pgx.ErrNoRows}
}

func (q *agentQuerier) Exec(_ context.Context, _ string, _ ...any) error { return q.execErr }

func (q *agentQuerier) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	if q.execErr != nil {
		return &fakeRow{err: q.execErr}
	}
	return &stringIDRow{id: q.execRet}
}

// ── agentScanRow ──────────────────────────────────────────────────────────────
//
// agentScanRow implements SingleRowScanner returning 23 agent columns in the
// order expected by dal.scanAgent. Only the fields needed by test assertions
// are set; the rest are zero / empty / nil.
//
// Column order (from dal/agents.go agentSelectCols):
//   id, slug, display_name, description, transport,
//   endpoint_url, auth_token_set,
//   input_schema, timeout_seconds, max_concurrency, max_retries,
//   enabled, tags, agent_card, agent_card_url,
//   skills, supports_streaming, supports_push, icon, category,
//   card_fetched_at, last_scan_at, last_scan_result,
//   definition_id (from agent_runtime_specs LEFT JOIN)

type agentScanRow struct {
	id               string
	slug             string
	displayName      string
	description      string
	transport        string
	endpointURL      string
	authTokenSet     bool
	timeoutSeconds   int
	enabled          bool
}

func (r *agentScanRow) Scan(dest ...any) error {
	// 24 columns in order.
	vals := []any{
		r.id,           // id
		r.slug,         // slug
		r.displayName,  // display_name
		r.description,  // description
		r.transport,    // transport
		r.endpointURL,  // endpoint_url
		r.authTokenSet, // auth_token_set
		[]byte(nil),    // input_schema
		r.timeoutSeconds, // timeout_seconds
		1,              // max_concurrency
		0,              // max_retries
		r.enabled,      // enabled
		[]string{},     // tags
		[]byte(nil),    // agent_card
		(*string)(nil), // agent_card_url
		[]byte(nil),    // skills
		false,          // supports_streaming
		false,          // supports_push
		(*string)(nil), // icon
		(*string)(nil), // category
		(*string)(nil), // card_fetched_at
		(*string)(nil), // last_scan_at
		[]byte(nil),    // last_scan_result
		(*string)(nil), // definition_id (agent_runtime_specs LEFT JOIN — nil when no canvas spec)
	}
	for i, d := range dest {
		if i >= len(vals) {
			break
		}
		if err := scanInto(d, vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// nullStringRow returns nil for a *string scan (used for token encrypted lookup).
type nullStringRow struct{}

func (r *nullStringRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if d, ok := dest[0].(**string); ok {
		*d = nil
		return nil
	}
	return nil
}

// ── serveAgentActions ─────────────────────────────────────────────────────────

// serveAgentActions mounts AgentsHandler with the given querier and sends a
// request, returning the recorder.
func serveAgentActions(t *testing.T, db admin.DBQuerier, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	h := admin.NewAgentsHandler(db, nil, nil, nil, nil, nil)
	r := chi.NewRouter()
	r.Use(withTestTenant)
	h.Routes(r)
	var br *bytes.Reader
	if body != nil {
		br = bytes.NewReader(body)
	} else {
		br = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, br)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── TestDiscover_Success ──────────────────────────────────────────────────────

func TestDiscover_Success(t *testing.T) {
	// Mock agent-card server.
	card := map[string]any{
		"name":        "Echo Agent",
		"description": "An echo agent for testing",
		"skills": []any{
			map[string]any{
				"id":          "echo",
				"name":        "Echo",
				"description": "Echoes input",
				"tags":        []any{"test"},
			},
		},
		"capabilities": map[string]any{
			"streaming":         true,
			"pushNotifications": false,
		},
	}
	cardBytes, _ := json.Marshal(card)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/agent-card.json", r.URL.Path)
		assert.Equal(t, "1.0", r.Header.Get("A2A-Version"))
		w.Header().Set("Content-Type", "application/json")
		w.Write(cardBytes)
	}))
	defer srv.Close()

	db := &fakeDB{queryRows: newFakeRows(nil)}
	body, _ := json.Marshal(map[string]any{"endpoint_url": srv.URL})
	w := serveAgentActions(t, db, http.MethodPost, "/agents/discover", body)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, true, resp["ok"])
	assert.Equal(t, "Echo Agent", resp["display_name"])
	assert.Equal(t, "echo_agent", resp["suggested_slug"])
	assert.Equal(t, true, resp["supports_streaming"])
	assert.Equal(t, false, resp["supports_push"])
	assert.Contains(t, resp["agent_card_url"], "/.well-known/agent-card.json")

	// Skills list should be non-nil.
	skills, ok := resp["skills"].([]any)
	assert.True(t, ok, "skills should be an array")
	assert.Len(t, skills, 1)
}

// ── TestDiscover_ConnectionFailure ────────────────────────────────────────────

func TestDiscover_ConnectionFailure(t *testing.T) {
	// Use an address that will be refused (server not started).
	db := &fakeDB{}
	body, _ := json.Marshal(map[string]any{"endpoint_url": "http://127.0.0.1:19999"})
	w := serveAgentActions(t, db, http.MethodPost, "/agents/discover", body)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["ok"])
	assert.NotEmpty(t, resp["detail"])
}

// ── TestDiscover_NonJSON ──────────────────────────────────────────────────────

func TestDiscover_NonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	db := &fakeDB{}
	body, _ := json.Marshal(map[string]any{"endpoint_url": srv.URL})
	w := serveAgentActions(t, db, http.MethodPost, "/agents/discover", body)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["ok"])
	assert.True(t, strings.Contains(resp["detail"].(string), "parse card JSON"))
}

// ── TestTest_Success ──────────────────────────────────────────────────────────

func TestTest_Success(t *testing.T) {
	card := map[string]any{
		"name": "Vision Agent",
		"skills": []any{
			map[string]any{"id": "s1", "name": "Skill1", "description": "d1"},
			map[string]any{"id": "s2", "name": "Skill2", "description": "d2"},
		},
	}
	cardBytes, _ := json.Marshal(card)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cardBytes)
	}))
	defer srv.Close()

	// agentQuerier returns:
	// 1st QueryRow: agent row (GetAgent via service) — endpoint = mock server
	// 2nd QueryRow: nil token (GetAgentTokenEncrypted)
	agentRow := &agentScanRow{
		id:             "aaaaaaaa-0000-0000-0000-000000000001",
		slug:           "vision",
		displayName:    "Vision Agent",
		transport:      "a2a_async",
		endpointURL:    srv.URL,
		authTokenSet:   false,
		timeoutSeconds: 30,
		enabled:        true,
	}
	db := &agentQuerier{queue: []admin.SingleRowScanner{agentRow}}

	w := serveAgentActions(t, db, http.MethodPost, "/agents/aaaaaaaa-0000-0000-0000-000000000001/test", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, true, resp["ok"])
	latencyMs, ok := resp["latency_ms"].(float64)
	assert.True(t, ok && latencyMs >= 0, "latency_ms should be non-negative number")
	detail, _ := resp["detail"].(string)
	assert.Contains(t, detail, "2 skills")
}

// ── TestTest_Failure ──────────────────────────────────────────────────────────

func TestTest_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service down"))
	}))
	defer srv.Close()

	agentRow := &agentScanRow{
		id:          "bbbbbbbb-0000-0000-0000-000000000001",
		slug:        "some-agent",
		displayName: "Some Agent",
		transport:   "a2a_async",
		endpointURL: srv.URL,
		enabled:     true,
	}
	db := &agentQuerier{queue: []admin.SingleRowScanner{agentRow}}

	w := serveAgentActions(t, db, http.MethodPost, "/agents/bbbbbbbb-0000-0000-0000-000000000001/test", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, false, resp["ok"])
	detail, _ := resp["detail"].(string)
	assert.Contains(t, detail, "503")
}

// ── TestTest_NotFound ─────────────────────────────────────────────────────────

func TestTest_NotFound(t *testing.T) {
	// DB returns no rows → agent not found.
	db := &agentQuerier{queue: []admin.SingleRowScanner{
		&fakeRow{err: pgx.ErrNoRows},
	}}
	w := serveAgentActions(t, db, http.MethodPost, "/agents/cccccccc-0000-0000-0000-000000000001/test", nil)

	require.Equal(t, http.StatusNotFound, w.Code)
}

// ── TestSecurityScan_NoScanner ────────────────────────────────────────────────

func TestSecurityScan_NoScanner(t *testing.T) {
	// 1st QueryRow: target agent found
	// 2nd QueryRow: scanner agent not found (pgx.ErrNoRows)
	targetRow := &agentScanRow{
		id:          "dddddddd-0000-0000-0000-000000000001",
		slug:        "target-agent",
		displayName: "Target",
		transport:   "a2a_async",
		endpointURL: "http://example.com",
		enabled:     true,
	}
	db := &agentQuerier{queue: []admin.SingleRowScanner{
		targetRow,
		&fakeRow{err: pgx.ErrNoRows}, // scanner not found
	}}

	w := serveAgentActions(t, db, http.MethodPost, "/agents/dddddddd-0000-0000-0000-000000000001/security-scan", nil)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["detail"].(string), "Security scanner agent not registered")
}

// ── TestSecurityScan_Accepted ─────────────────────────────────────────────────

func TestSecurityScan_Accepted(t *testing.T) {
	// 1st QueryRow: target agent found
	// 2nd QueryRow: scanner agent found (security_scanner)
	// 3rd QueryRow: scanner token (nil — no token)
	targetRow := &agentScanRow{
		id:          "eeeeeeee-0000-0000-0000-000000000001",
		slug:        "target-agent",
		displayName: "Target",
		transport:   "a2a_async",
		endpointURL: "http://example.com",
		enabled:     true,
	}
	scannerRow := &agentScanRow{
		id:             "ffffffff-0000-0000-0000-000000000001",
		slug:           "security_scanner",
		displayName:    "Security Scanner",
		transport:      "a2a_async",
		endpointURL:    "http://scanner.example.com",
		authTokenSet:   false,
		timeoutSeconds: 120,
		enabled:        true,
	}
	db := &agentQuerier{queue: []admin.SingleRowScanner{
		targetRow,
		scannerRow,
	}}

	w := serveAgentActions(t, db, http.MethodPost, "/agents/eeeeeeee-0000-0000-0000-000000000001/security-scan", nil)

	require.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// job_id must be present and non-empty.
	jobID, ok := resp["job_id"].(string)
	assert.True(t, ok && jobID != "", "job_id should be a non-empty string")
	// agent_id must match the requested id.
	assert.Equal(t, "eeeeeeee-0000-0000-0000-000000000001", resp["agent_id"])
}

// ── TestDiscover_CrossTenantTokenNotForwarded ─────────────────────────────────
//
// Security regression test: a caller supplies an agent_id that belongs to a
// different tenant. The DAL query includes AND tenant_id = $2, so it returns
// pgx.ErrNoRows. Discover must fall through with no Authorization header.

// crossTenantQuerier simulates the DB returning ErrNoRows for the tenant-scoped
// token lookup (GetAgentTokenEncryptedForTenant sees wrong tenant_id).
type crossTenantQuerier struct{}

func (q *crossTenantQuerier) Query(_ context.Context, _ string, _ ...any) (admin.RowScanner, error) {
	return newFakeRows(nil), nil
}
func (q *crossTenantQuerier) QueryRow(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	// All QueryRow calls (agent existence check + token fetch) return no rows —
	// simulating a cross-tenant agent_id that doesn't exist in the caller's tenant.
	return &fakeRow{err: pgx.ErrNoRows}
}
func (q *crossTenantQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (q *crossTenantQuerier) ExecReturning(_ context.Context, _ string, _ ...any) admin.SingleRowScanner {
	return &fakeRow{err: pgx.ErrNoRows}
}

func TestDiscover_CrossTenantTokenNotForwarded(t *testing.T) {
	// authHeader captures any Authorization header sent by Discover to the backend.
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		// Return a minimal valid agent card so Discover reports ok:true.
		card := map[string]any{"name": "Other Tenant Agent"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(card) //nolint:errcheck
	}))
	defer srv.Close()

	// The caller belongs to testTenantID (injected by withTestTenant).
	// The agent_id supplied here belongs to a different tenant in production;
	// crossTenantQuerier simulates that by returning ErrNoRows for the
	// tenant-scoped token query.
	body, _ := json.Marshal(map[string]any{
		"endpoint_url": srv.URL,
		"agent_id":     "11111111-2222-3333-4444-555555555555", // not in caller's tenant
	})
	w := serveAgentActions(t, &crossTenantQuerier{}, http.MethodPost, "/agents/discover", body)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"], "discover should succeed (card is public)")
	// Critical: no auth token must have been forwarded to the attacker-controlled endpoint.
	assert.Empty(t, authHeader, "Authorization header must not be forwarded when token lookup returns no rows")
}
