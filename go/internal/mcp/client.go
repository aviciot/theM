package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	mcpVersion     = "2024-11-05"
	defaultTimeout = 10 * time.Second
)

// Tool is a single entry from an MCP tools/list response.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// DiscoveryResult holds the output of a tools/list probe.
type DiscoveryResult struct {
	Tools        []Tool          `json:"tools"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

// CallResult holds the output of a tools/call invocation.
type CallResult struct {
	Content []json.RawMessage `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// Client is a minimal MCP HTTP client covering initialize, tools/list, and tools/call.
type Client struct {
	httpClient *http.Client
	serverURL  string
	authHeader string // e.g. "Bearer <token>" or "<header>: <value>"
	headerName string // header name for injection (default: Authorization)
	sessionID  string // Mcp-Session-Id issued by streamable-http servers on initialize
}

// NewClient creates a Client for the given MCP server URL with optional auth.
// authHeaderName and authValue are empty for auth_type='none'.
func NewClient(serverURL, authHeaderName, authValue string) *Client {
	hn := authHeaderName
	if hn == "" {
		hn = "Authorization"
	}
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		serverURL:  serverURL,
		authHeader: authValue,
		headerName: hn,
	}
}

// Probe issues an MCP initialize request to confirm the server is reachable.
// Returns the raw server info block on success.
func (c *Client) Probe(ctx context.Context) (json.RawMessage, error) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpVersion,
			"clientInfo":      map[string]string{"name": "them-mcp-service", "version": "1.0"},
			"capabilities":    map[string]any{},
		},
	}
	resp, err := c.call(ctx, body)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Discover calls tools/list and returns the full manifest.
func (c *Client) Discover(ctx context.Context) (*DiscoveryResult, error) {
	// initialize first (required by MCP spec before any other method)
	if _, err := c.Probe(ctx); err != nil {
		return nil, fmt.Errorf("mcp discover: initialize: %w", err)
	}

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	raw, err := c.call(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("mcp discover: tools/list: %w", err)
	}

	var wrapper struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("mcp discover: parse tools/list: %w", err)
	}

	toolsJSON, _ := json.Marshal(wrapper.Tools)
	return &DiscoveryResult{
		Tools:        wrapper.Tools,
		Capabilities: toolsJSON,
	}, nil
}

// Call invokes a single MCP tool and returns the raw result content.
// It runs initialize first (required by the MCP spec) to obtain a session ID.
func (c *Client) Call(ctx context.Context, toolName string, arguments map[string]any) (*CallResult, error) {
	if _, err := c.Probe(ctx); err != nil {
		return nil, fmt.Errorf("mcp call %s: initialize: %w", toolName, err)
	}

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	}
	raw, err := c.call(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("mcp call %s: %w", toolName, err)
	}

	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp call %s: parse result: %w", toolName, err)
	}
	return &result, nil
}

// call sends a JSON-RPC request and returns the `result` field of the response.
func (c *Client) call(ctx context.Context, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.authHeader != "" {
		req.Header.Set(c.headerName, c.authHeader)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: http: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID issued by streamable-http servers on initialize.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mcp: server returned %d: %s", resp.StatusCode, string(body))
	}

	// Streamable-http servers respond with text/event-stream (SSE) format.
	// Plain JSON responses are also accepted for compatibility.
	ct := resp.Header.Get("Content-Type")
	var jsonBody []byte
	if strings.Contains(ct, "text/event-stream") {
		// Parse SSE: find the first "data: ..." line and use it as the JSON body.
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				jsonBody = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				break
			}
		}
		if len(jsonBody) == 0 {
			return nil, fmt.Errorf("mcp: empty SSE response")
		}
	} else {
		jsonBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("mcp: read response: %w", err)
		}
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(jsonBody, &envelope); err != nil {
		return nil, fmt.Errorf("mcp: decode response: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("mcp: rpc error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope.Result, nil
}
