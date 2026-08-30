package agentgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// MaxA2ACallDepth is the maximum number of nested a2a_call steps allowed.
// Depth is tracked via the X-Them-A2A-Depth header so it survives HTTP boundaries.
// A pipeline at depth MaxA2ACallDepth cannot make further a2a_call steps.
const MaxA2ACallDepth = 3

// A2ACaller invokes another agent registered in the platform as a canvas step.
// The implementation must enforce internal authentication (X-Them-* headers) and
// must never use caller-supplied URLs or tokens — only registry-resolved values.
type A2ACaller interface {
	// Call invokes the agent identified by agentSlug for the given tenant+application.
	// input is a JSON-encoded value (the pipeline variable read from InputVar).
	// depth is the current nesting level (0 = top-level, must be < MaxA2ACallDepth).
	// Returns the JSON-encoded response text or a non-nil error.
	Call(ctx context.Context, tenantID, applicationID, agentSlug string, input json.RawMessage, depth int) (json.RawMessage, error)
}

// a2aRPCRequest is the JSON-RPC 2.0 envelope for A2A SendMessage.
type a2aRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  a2aRPCParams  `json:"params"`
	ID      string        `json:"id"`
}

type a2aRPCParams struct {
	Message       a2aRPCMessage       `json:"message"`
	Configuration a2aRPCConfiguration `json:"configuration,omitempty"`
}

type a2aRPCMessage struct {
	Role      string       `json:"role"`
	Parts     []a2aRPCPart `json:"parts"`
	MessageID string       `json:"messageId"`
}

type a2aRPCPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

type a2aRPCConfiguration struct {
	ReturnImmediately bool `json:"returnImmediately"`
}

type a2aRPCResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *a2aRPCError    `json:"error,omitempty"`
}

type a2aRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// AgentEndpointResolver resolves an agent slug to its endpoint URL and auth token,
// scoped to a tenant. Both values come from the registry; the auth token is the
// value stored in the agents table (internal network only — never user-supplied).
type AgentEndpointResolver interface {
	ResolveEndpoint(ctx context.Context, tenantID, agentSlug string) (endpointURL, authToken string, err error)
}

// AgentEndpointRow is the minimal scanner interface for a DB row returning
// (endpoint_url, auth_token_encrypted). Both columns may be empty strings.
type AgentEndpointRow interface {
	Scan(dest ...any) error
}

// AgentEndpointQueryer executes the DB lookup for one agent by tenant+slug.
type AgentEndpointQueryer interface {
	QueryAgentEndpoint(ctx context.Context, tenantID, agentSlug string) AgentEndpointRow
}

// DecryptFunc decrypts a stored ciphertext; returns ("", nil) for empty input.
type DecryptFunc func(ciphertext string) (string, error)

// DBAgentEndpointResolver implements AgentEndpointResolver using a DB queryier
// and a decrypt function. Both binaries (agent-runtime, dag-worker) wire this.
type DBAgentEndpointResolver struct {
	db      AgentEndpointQueryer
	decrypt DecryptFunc
}

// NewDBAgentEndpointResolver creates a DBAgentEndpointResolver.
func NewDBAgentEndpointResolver(db AgentEndpointQueryer, decrypt DecryptFunc) *DBAgentEndpointResolver {
	return &DBAgentEndpointResolver{db: db, decrypt: decrypt}
}

func (r *DBAgentEndpointResolver) ResolveEndpoint(ctx context.Context, tenantID, agentSlug string) (string, string, error) {
	row := r.db.QueryAgentEndpoint(ctx, tenantID, agentSlug)
	var endpointURL, authTokenEncrypted string
	if err := row.Scan(&endpointURL, &authTokenEncrypted); err != nil {
		return "", "", fmt.Errorf("a2a_call: agent %q not found or disabled: %w", agentSlug, err)
	}
	var authToken string
	if authTokenEncrypted != "" {
		plain, err := r.decrypt(authTokenEncrypted)
		if err != nil {
			// Log-safe: don't include the ciphertext in the error.
			return "", "", fmt.Errorf("a2a_call: decrypt auth token for agent %q: internal error", agentSlug)
		}
		authToken = plain
	}
	return endpointURL, authToken, nil
}

var _ AgentEndpointResolver = (*DBAgentEndpointResolver)(nil)

// HTTPA2ACaller implements A2ACaller by calling agent-runtime over the internal
// Docker network. It uses the registry resolver to obtain the endpoint URL and
// internal auth token — no caller-supplied credentials are trusted.
type HTTPA2ACaller struct {
	resolver AgentEndpointResolver
	client   *http.Client
}

// NewHTTPA2ACaller creates an HTTPA2ACaller. client may be nil (defaults to 5-minute timeout).
func NewHTTPA2ACaller(resolver AgentEndpointResolver, client *http.Client) *HTTPA2ACaller {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &HTTPA2ACaller{resolver: resolver, client: client}
}

// Call invokes the target agent via A2A JSON-RPC SendMessage.
// It injects X-Them-* identity headers and X-Them-A2A-Depth for depth tracking.
func (c *HTTPA2ACaller) Call(ctx context.Context, tenantID, applicationID, agentSlug string, input json.RawMessage, depth int) (json.RawMessage, error) {
	if depth >= MaxA2ACallDepth {
		return nil, fmt.Errorf("a2a_call: maximum nesting depth %d reached — circular agent chains are not allowed", MaxA2ACallDepth)
	}

	endpointURL, authToken, err := c.resolver.ResolveEndpoint(ctx, tenantID, agentSlug)
	if err != nil {
		return nil, fmt.Errorf("a2a_call: resolve agent %q: %w", agentSlug, err)
	}
	if endpointURL == "" {
		return nil, fmt.Errorf("a2a_call: agent %q has no endpoint configured", agentSlug)
	}

	part := buildA2ARPCPart(input)
	rpcReq := a2aRPCRequest{
		JSONRPC: "2.0",
		Method:  "SendMessage",
		Params: a2aRPCParams{
			Message: a2aRPCMessage{
				Role:      "ROLE_USER",
				Parts:     []a2aRPCPart{part},
				MessageID: newCallUUID(),
			},
			Configuration: a2aRPCConfiguration{ReturnImmediately: false},
		},
		ID: newCallUUID(),
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("a2a_call: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a_call: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	// Internal auth token from the registry — never a user-supplied value.
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	// Identity headers (Phase 1 protocol — internal Docker network only).
	req.Header.Set("X-Them-Tenant-Id", tenantID)
	req.Header.Set("X-Them-Application-Id", applicationID)
	// Depth propagation so the callee can enforce its own depth guard.
	req.Header.Set("X-Them-A2A-Depth", fmt.Sprintf("%d", depth+1))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a_call: http: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp a2aRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("a2a_call: decode response (status %d): %w", resp.StatusCode, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("a2a_call: agent error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		return nil, fmt.Errorf("a2a_call: empty response from agent %q", agentSlug)
	}
	return extractA2ARPCResult(rpcResp.Result)
}

// buildA2ARPCPart converts a JSON-encoded pipeline variable to an A2A message part.
// Objects are sent as a data part; strings and other scalars as text parts.
func buildA2ARPCPart(input json.RawMessage) a2aRPCPart {
	if len(input) > 0 && input[0] == '{' {
		var obj any
		if json.Unmarshal(input, &obj) == nil {
			return a2aRPCPart{Kind: "data", Data: obj}
		}
	}
	// Unwrap a quoted JSON string to plain text.
	var s string
	if json.Unmarshal(input, &s) == nil {
		return a2aRPCPart{Kind: "text", Text: s}
	}
	return a2aRPCPart{Kind: "text", Text: string(input)}
}

// extractA2ARPCResult pulls the output text from a raw A2A task JSON result.
func extractA2ARPCResult(raw json.RawMessage) (json.RawMessage, error) {
	var task struct {
		Status struct {
			Message struct {
				Parts []struct {
					Text string `json:"text"`
					Kind string `json:"kind"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			Parts []struct {
				Text string `json:"text"`
				Kind string `json:"kind"`
			} `json:"parts"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		return raw, nil // return raw on decode failure — best effort
	}
	// Prefer artifact text first (final output).
	for _, art := range task.Artifacts {
		for _, p := range art.Parts {
			if p.Kind == "text" && p.Text != "" {
				return json.Marshal(p.Text)
			}
		}
	}
	// Fall back to status message text.
	for _, p := range task.Status.Message.Parts {
		if p.Kind == "text" && p.Text != "" {
			return json.Marshal(p.Text)
		}
	}
	return raw, nil
}

// newCallUUID generates a random UUID string for RPC request/message IDs.
func newCallUUID() string { return uuid.New().String() }

// Ensure HTTPA2ACaller satisfies A2ACaller.
var _ A2ACaller = (*HTTPA2ACaller)(nil)
