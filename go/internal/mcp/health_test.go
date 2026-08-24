package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/mcp"
)

// --- mock MCP server helpers -------------------------------------------------

type mockMCPBehavior struct {
	failInitialize bool
	tools          []mcp.Tool
	requestCount   atomic.Int32 // all POST requests
	sessionID      string       // non-empty → stateful server, issues Mcp-Session-Id
}

// newMockMCPServer creates a spec-compliant mock MCP server supporting the
// initialize + notifications/initialized + tools/list sequence.
func newMockMCPServer(t *testing.T, b *mockMCPBehavior) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.requestCount.Add(1)

		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"` // notifications have no id
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		// notifications/initialized has no id — respond 202, no body.
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if b.failInitialize {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if b.sessionID != "" && req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", b.sessionID)
		}

		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]string{"name": "test-mcp"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": b.tools},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

// --- MCP client tests --------------------------------------------------------

func TestClient_Initialize_Healthy(t *testing.T) {
	b := &mockMCPBehavior{}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	err := client.Initialize(context.Background())
	require.NoError(t, err)
	// Two requests: initialize + notifications/initialized.
	assert.EqualValues(t, 2, b.requestCount.Load())
}

func TestClient_Initialize_Unreachable(t *testing.T) {
	client := mcp.NewClient("http://127.0.0.1:19999", "", "")
	err := client.Initialize(context.Background())
	assert.Error(t, err)
}

func TestClient_Initialize_ServerError(t *testing.T) {
	b := &mockMCPBehavior{failInitialize: true}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	err := client.Initialize(context.Background())
	assert.Error(t, err)
}

func TestClient_Discover_ReturnsTools(t *testing.T) {
	b := &mockMCPBehavior{tools: []mcp.Tool{
		{Name: "create_issue", Description: "Create a GitHub issue"},
		{Name: "list_prs", Description: "List pull requests"},
	}}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	require.NoError(t, client.Initialize(context.Background()))

	result, err := client.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Tools, 2)
	assert.Equal(t, "create_issue", result.Tools[0].Name)
	assert.Equal(t, "list_prs", result.Tools[1].Name)
}

func TestClient_Discover_EmptyManifest(t *testing.T) {
	b := &mockMCPBehavior{tools: []mcp.Tool{}}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	require.NoError(t, client.Initialize(context.Background()))

	result, err := client.Discover(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result.Tools)
}

func TestClient_Discover_RequiresInitialize(t *testing.T) {
	b := &mockMCPBehavior{tools: []mcp.Tool{}}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	// Calling Discover without Initialize must fail.
	client := mcp.NewClient(srv.URL, "", "")
	_, err := client.Discover(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestClient_Discover_InitializeFailure(t *testing.T) {
	b := &mockMCPBehavior{failInitialize: true}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	err := client.Initialize(context.Background())
	assert.Error(t, err)
}

func TestClient_SessionID_ForwardedOnSubsequentRequests(t *testing.T) {
	const wantSessionID = "test-session-abc123"
	var gotSessionIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionIDs = append(gotSessionIDs, r.Header.Get("Mcp-Session-Id"))

		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", wantSessionID)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"tools":           []any{},
			},
		})
	}))
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	require.NoError(t, client.Initialize(context.Background()))
	_, err := client.Discover(context.Background())
	require.NoError(t, err)

	// initialize: no session ID yet (first request)
	// notifications/initialized: session ID must be forwarded
	// tools/list: session ID must be forwarded
	require.Len(t, gotSessionIDs, 3)
	assert.Empty(t, gotSessionIDs[0], "initialize request should not have session ID")
	assert.Equal(t, wantSessionID, gotSessionIDs[1], "notifications/initialized must carry session ID")
	assert.Equal(t, wantSessionID, gotSessionIDs[2], "tools/list must carry session ID")
}

func TestClient_RespectsBearerAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"protocolVersion": "2024-11-05"},
		})
	}))
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "Authorization", "Bearer test-token-123")
	err := client.Initialize(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token-123", gotAuth)
}

// TestClient_InitializeOnce verifies that a persistent client initializes once
// and reuses the session for multiple Discover calls.
func TestClient_InitializeOnce(t *testing.T) {
	b := &mockMCPBehavior{tools: []mcp.Tool{{Name: "tool_a"}}}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	require.NoError(t, client.Initialize(context.Background()))

	// Two Discover calls — should not trigger additional initialize calls.
	_, err := client.Discover(context.Background())
	require.NoError(t, err)
	_, err = client.Discover(context.Background())
	require.NoError(t, err)

	// Requests: initialize(1) + notifications/initialized(1) + tools/list(2) = 4
	assert.EqualValues(t, 4, b.requestCount.Load())
}

// --- worker concurrency test (client-level) ----------------------------------

func TestClient_ConcurrentDiscovery(t *testing.T) {
	const n = 5
	behaviors := make([]*mockMCPBehavior, n)
	servers := make([]*httptest.Server, n)
	for i := 0; i < n; i++ {
		behaviors[i] = &mockMCPBehavior{tools: []mcp.Tool{{Name: "tool_a"}}}
		servers[i] = newMockMCPServer(t, behaviors[i])
		defer servers[i].Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			client := mcp.NewClient(servers[i].URL, "", "")
			require.NoError(t, client.Initialize(ctx))
			_, err := client.Discover(ctx)
			assert.NoError(t, err, "server %d", i)
			done <- struct{}{}
		}()
	}

	for i := 0; i < n; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("concurrent probes did not complete in time")
		}
	}

	// Each server: initialize(1) + notifications/initialized(1) + tools/list(1) = 3 requests.
	for i, b := range behaviors {
		assert.EqualValues(t, 3, b.requestCount.Load(), "server %d request count", i)
	}
}

// --- config tests ------------------------------------------------------------

func TestConfig_Validate_MissingDBHost(t *testing.T) {
	t.Setenv("DATABASE_HOST", "")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("SECRET_KEY", "a-valid-secret-key-not-default-value")

	_, err := mcp.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_HOST")
}

func TestConfig_Validate_MissingDBPassword(t *testing.T) {
	t.Setenv("DATABASE_HOST", "localhost")
	t.Setenv("DATABASE_PASSWORD", "")
	t.Setenv("SECRET_KEY", "a-valid-secret-key-not-default-value")

	_, err := mcp.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_PASSWORD")
}

func TestConfig_Validate_DefaultSecretKeyRejected(t *testing.T) {
	t.Setenv("DATABASE_HOST", "localhost")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("SECRET_KEY", "change-this-in-production")

	_, err := mcp.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SECRET_KEY")
}

func TestConfig_SafeString_RedactsSecrets(t *testing.T) {
	t.Setenv("DATABASE_HOST", "them-postgres")
	t.Setenv("DATABASE_PASSWORD", "super-secret-db-password")
	t.Setenv("SECRET_KEY", "super-secret-app-key-value-here")

	cfg, err := mcp.LoadConfig()
	require.NoError(t, err)

	safe := cfg.SafeString()
	assert.NotContains(t, safe, "super-secret-db-password")
	assert.NotContains(t, safe, "super-secret-app-key-value-here")
	assert.Contains(t, safe, "them-postgres")
}

func TestConfig_Defaults(t *testing.T) {
	t.Setenv("DATABASE_HOST", "localhost")
	t.Setenv("DATABASE_PASSWORD", "pw")
	t.Setenv("SECRET_KEY", "not-default-key-value-here-123456")
	t.Setenv("MCP_HEALTH_INTERVAL_SECONDS", "")
	t.Setenv("APP_PORT", "")

	cfg, err := mcp.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 8010, cfg.AppPort)
	assert.Equal(t, 60, cfg.HealthIntervalSeconds)
	assert.False(t, cfg.AllowStdio)
}
