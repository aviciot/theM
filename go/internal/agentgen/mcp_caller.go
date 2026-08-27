package agentgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// HTTPMCPCaller implements MCPCaller by calling them-mcp-service over HTTP.
type HTTPMCPCaller struct {
	serviceURL string       // e.g. "http://them-mcp-service:8010"
	client     *http.Client // typically &http.Client{Timeout: 30*time.Second}
}

// NewHTTPMCPCaller creates an HTTPMCPCaller pointing at the given service URL.
// An empty serviceURL disables MCP (Call returns an error immediately).
func NewHTTPMCPCaller(serviceURL string, client *http.Client) *HTTPMCPCaller {
	return &HTTPMCPCaller{serviceURL: serviceURL, client: client}
}

type mcpExecuteRequest struct {
	ApplicationID string         `json:"application_id"`
	MCPServerSlug string         `json:"mcp_server_slug"`
	ToolName      string         `json:"tool_name"`
	Arguments     map[string]any `json:"arguments"`
}

type mcpExecuteResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Call sends a POST to /internal/execute on them-mcp-service and returns the raw result.
func (c *HTTPMCPCaller) Call(ctx context.Context, applicationID, mcpServerSlug, toolName string, args map[string]any) (json.RawMessage, error) {
	if c.serviceURL == "" {
		return nil, fmt.Errorf("MCP_SERVICE_URL is not configured")
	}

	body, err := json.Marshal(mcpExecuteRequest{
		ApplicationID: applicationID,
		MCPServerSlug: mcpServerSlug,
		ToolName:      toolName,
		Arguments:     args,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serviceURL+"/internal/execute", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	var result mcpExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return result.Result, nil
}

// Ensure HTTPMCPCaller satisfies MCPCaller.
var _ MCPCaller = (*HTTPMCPCaller)(nil)
