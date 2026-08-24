package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/mcp"
)

// mockMCPServer spins up an httptest.Server that speaks enough MCP JSON-RPC
// to satisfy Probe and Discover calls.
func mockMCPServer(t *testing.T, tools []mcp.Tool, failProbe bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failProbe {
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
				"result": map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]string{"name": "test-mcp"}},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": tools},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

func TestClient_Probe_Success(t *testing.T) {
	srv := mockMCPServer(t, nil, false)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	raw, err := client.Probe(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, raw)
}

func TestClient_Probe_Unreachable(t *testing.T) {
	client := mcp.NewClient("http://localhost:19999", "", "")
	_, err := client.Probe(context.Background())
	assert.Error(t, err)
}

func TestClient_Discover_ReturnsTools(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "create_issue", Description: "Create a GitHub issue"},
		{Name: "list_prs", Description: "List pull requests"},
	}
	srv := mockMCPServer(t, tools, false)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	result, err := client.Discover(context.Background())
	require.NoError(t, err)
	assert.Len(t, result.Tools, 2)
	assert.Equal(t, "create_issue", result.Tools[0].Name)
	assert.Equal(t, "list_prs", result.Tools[1].Name)
}

func TestClient_Discover_ServerDown(t *testing.T) {
	srv := mockMCPServer(t, nil, true)
	defer srv.Close()

	client := mcp.NewClient(srv.URL, "", "")
	_, err := client.Discover(context.Background())
	assert.Error(t, err)
}

func TestConfig_Validate_MissingDBHost(t *testing.T) {
	t.Setenv("DATABASE_HOST", "")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("SECRET_KEY", "a-valid-secret-key-that-is-not-default")

	_, err := mcp.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_HOST")
}

func TestConfig_Validate_DefaultSecretKey(t *testing.T) {
	t.Setenv("DATABASE_HOST", "localhost")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("SECRET_KEY", "change-this-in-production")

	_, err := mcp.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SECRET_KEY")
}

func TestConfig_SafeString_NoSecrets(t *testing.T) {
	t.Setenv("DATABASE_HOST", "them-postgres")
	t.Setenv("DATABASE_PASSWORD", "my-db-secret")
	t.Setenv("SECRET_KEY", "my-app-secret-key-value")

	cfg, err := mcp.LoadConfig()
	require.NoError(t, err)

	safe := cfg.SafeString()
	assert.NotContains(t, safe, "my-db-secret")
	assert.NotContains(t, safe, "my-app-secret-key-value")
	assert.Contains(t, safe, "them-postgres")
}
