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

// Client is a spec-compliant MCP streamable-http client.
//
// Session lifecycle (per MCP 2025-03-26 spec):
//   - initialize is a one-time handshake per session, not per request.
//   - If the server returns Mcp-Session-Id, the client MUST include it on all
//     subsequent requests.
//   - On HTTP 404 (expired/unknown session), the client MUST re-initialize.
//   - The client MUST send notifications/initialized after initialize before
//     issuing any other requests.
//
// Hold one Client per server and call Initialize once; reuse for all
// Discover and Call operations. The worker in health.go owns the Client.
type Client struct {
	httpClient  *http.Client
	serverURL   string
	authHeader  string // e.g. "Bearer <token>"
	headerName  string // header name (default: Authorization)
	sessionID   string // Mcp-Session-Id, empty for stateless servers
	initialized bool   // true after a successful Initialize+notifications/initialized
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

// Initialize performs the MCP handshake: initialize + notifications/initialized.
// Must be called once before Discover or Call. Safe to call again to re-initialize
// (e.g. after a 404 session-expired response).
func (c *Client) Initialize(ctx context.Context) error {
	c.sessionID = ""
	c.initialized = false

	_, err := c.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpVersion,
			"clientInfo":      map[string]string{"name": "them-mcp-service", "version": "1.0"},
			"capabilities":    map[string]any{},
		},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}

	// Send notifications/initialized (fire-and-forget notification — server responds 202, no body).
	if err := c.notify(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}); err != nil {
		return fmt.Errorf("mcp notifications/initialized: %w", err)
	}

	c.initialized = true
	return nil
}

// Discover calls tools/list and returns the full manifest.
// Requires a prior successful Initialize call.
func (c *Client) Discover(ctx context.Context) (*DiscoveryResult, error) {
	if !c.initialized {
		return nil, fmt.Errorf("mcp discover: client not initialized — call Initialize first")
	}

	raw, err := c.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
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
// Requires a prior successful Initialize call.
func (c *Client) Call(ctx context.Context, toolName string, arguments map[string]any) (*CallResult, error) {
	if !c.initialized {
		return nil, fmt.Errorf("mcp call %s: client not initialized — call Initialize first", toolName)
	}

	raw, err := c.post(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("mcp call %s: %w", toolName, err)
	}

	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp call %s: parse result: %w", toolName, err)
	}
	return &result, nil
}

// IsSessionExpired reports whether an error from Discover or Call indicates the
// server session has expired (HTTP 404). The caller should re-Initialize.
func IsSessionExpired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "mcp: server returned 404:")
}

// notify sends a JSON-RPC notification (no id field, server responds 202 with no body).
func (c *Client) notify(ctx context.Context, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// post sends a JSON-RPC request and returns the `result` field of the response.
func (c *Client) post(ctx context.Context, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.serverURL, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: http: %w", err)
	}
	defer resp.Body.Close()

	// Capture Mcp-Session-Id when issued by a stateful server.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mcp: server returned %d: %s", resp.StatusCode, string(errBody))
	}

	// Streamable-http servers respond with text/event-stream (SSE) format.
	// Plain JSON responses are also accepted for compatibility.
	ct := resp.Header.Get("Content-Type")
	var jsonBody []byte
	if strings.Contains(ct, "text/event-stream") {
		// Extract the JSON payload from the first "data: ..." SSE line.
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

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.authHeader != "" {
		req.Header.Set(c.headerName, c.authHeader)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
}
