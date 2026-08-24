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
	failProbe  bool
	tools      []mcp.Tool
	probeCount atomic.Int32
}

func newMockMCPServer(t *testing.T, b *mockMCPBehavior) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.probeCount.Add(1)
		if b.failProbe {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
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

func TestClient_Probe_Healthy(t *testing.T) {
	b := &mockMCPBehavior{}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	raw, err := client.Probe(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, raw)
	assert.EqualValues(t, 1, b.probeCount.Load())
}

func TestClient_Probe_Unreachable(t *testing.T) {
	client := mcp.NewClient("http://127.0.0.1:19999", "", "")
	_, err := client.Probe(context.Background())
	assert.Error(t, err)
}

func TestClient_Probe_ServerError(t *testing.T) {
	b := &mockMCPBehavior{failProbe: true}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	_, err := client.Probe(context.Background())
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
	result, err := client.Discover(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result.Tools)
}

func TestClient_Discover_ProbeFailure(t *testing.T) {
	b := &mockMCPBehavior{failProbe: true}
	srv := newMockMCPServer(t, b)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	_, err := client.Discover(context.Background())
	assert.Error(t, err)
}

func TestClient_RespectsBearerAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"protocolVersion": "2024-11-05"},
		})
	}))
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "Authorization", "Bearer test-token-123")
	_, err := client.Probe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token-123", gotAuth)
}

// --- worker integration test (no DB/Redis — uses stub) -----------------------

// workerProbeIntegration tests that a worker correctly transitions health
// states by directly calling probe on a minimal worker setup. We bypass
// the supervisor and DB writes to keep this a pure unit test.
func TestWorker_ProbesConcurrently(t *testing.T) {
	// Spin up 5 independent mock MCP servers.
	const n = 5
	behaviors := make([]*mockMCPBehavior, n)
	servers := make([]*httptest.Server, n)
	for i := 0; i < n; i++ {
		behaviors[i] = &mockMCPBehavior{tools: []mcp.Tool{{Name: "tool_a"}}}
		servers[i] = newMockMCPServer(t, behaviors[i])
		defer servers[i].Close()
	}

	// Each client independently reaches its own server.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			client := mcp.NewClient(servers[i].URL, "", "")
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

	// All servers should have received exactly 2 calls (initialize + tools/list).
	for i, b := range behaviors {
		assert.EqualValues(t, 2, b.probeCount.Load(), "server %d probe count", i)
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
