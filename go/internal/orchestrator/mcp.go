package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// invokeMCPTool dispatches a mcp__<server>__<tool> call to them-mcp-service.
// toolName must be in the form "mcp__<slug>__<tool>".
func (o *Orchestrator) invokeMCPTool(ctx context.Context, applicationID, toolName string, input map[string]any) (json.RawMessage, error) {
	if o.cfg.MCPServiceURL == "" {
		return nil, fmt.Errorf("MCP tool %q called but MCPServiceURL is not configured", toolName)
	}

	parts := splitMCPToolName(toolName)
	if parts == nil {
		return nil, fmt.Errorf("invalid MCP tool name %q: expected mcp__<server>__<tool>", toolName)
	}
	serverSlug, mcpToolName := parts[0], parts[1]

	reqBody, _ := json.Marshal(map[string]any{
		"application_id":  applicationID,
		"mcp_server_slug": serverSlug,
		"tool_name":       mcpToolName,
		"arguments":       input,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.cfg.MCPServiceURL+"/internal/execute", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("mcp tool %q: build request: %w", toolName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp tool %q: http: %w", toolName, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp tool %q: server returned %d: %s", toolName, resp.StatusCode, string(body))
	}

	// them-mcp-service returns {"result": <any>}; forward the full body as the tool result.
	return json.RawMessage(body), nil
}

// splitMCPToolName parses "mcp__<server>__<tool>" → ["<server>", "<tool>"].
// Returns nil when the format is wrong.
func splitMCPToolName(name string) []string {
	// Must start with "mcp__" and contain at least one more "__" after that.
	if len(name) <= 5 || name[:5] != "mcp__" {
		return nil
	}
	rest := name[5:]
	for i := 0; i < len(rest)-1; i++ {
		if rest[i] == '_' && rest[i+1] == '_' {
			return []string{rest[:i], rest[i+2:]}
		}
	}
	return nil
}
