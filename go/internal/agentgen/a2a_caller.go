package agentgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MaxA2ACallDepth is the maximum number of nested a2a_call steps allowed.
// Depth is tracked via the X-Them-A2A-Depth header so it survives HTTP boundaries.
// A pipeline at depth MaxA2ACallDepth cannot make further a2a_call steps.
const MaxA2ACallDepth = 3

// A2ACallParams carries all inputs for one A2A inter-agent call.
// Using a struct avoids repeated interface breakage when new fields are needed.
type A2ACallParams struct {
	TenantID      string
	ApplicationID string
	AgentSlug     string
	// InvocationID is the parent run's invocation UUID, used to derive stable
	// request IDs so that retries re-use the same message/task UUID and do not
	// create duplicate tasks in the target agent.
	InvocationID string
	// StepID is the canvas step identifier, combined with InvocationID to make
	// the derived UUID unique per step within a run.
	StepID string
	Input  json.RawMessage
	Depth  int
}

// A2ACaller invokes another agent registered in the platform as a canvas step.
// The implementation must enforce internal authentication (X-Them-* headers) and
// must never use caller-supplied URLs or tokens — only registry-resolved values.
type A2ACaller interface {
	Call(ctx context.Context, p A2ACallParams) (json.RawMessage, error)
}

// a2aRPCRequest is the JSON-RPC 2.0 envelope for A2A SendMessage.
type a2aRPCRequest struct {
	JSONRPC string       `json:"jsonrpc"`
	Method  string       `json:"method"`
	Params  a2aRPCParams `json:"params"`
	ID      string       `json:"id"`
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

// ResolvedEndpoint is the full result of a registry lookup for one agent.
type ResolvedEndpoint struct {
	EndpointURL string // empty → agent has no endpoint (fail closed)
	AuthToken   string // plaintext; empty → no auth required
	AgentID     string // UUID of the target agent row in them.agents
	BindingID   string // UUID of app_agent_bindings row; empty → no binding (fail closed)
}

// AgentEndpointResolver resolves an agent slug + application to its endpoint, auth
// token, agent ID, and binding ID. Implementations must fail closed when no valid
// binding exists for (applicationID, agentSlug) — callers must not proceed without
// a BindingID.
type AgentEndpointResolver interface {
	ResolveEndpoint(ctx context.Context, tenantID, applicationID, agentSlug string) (ResolvedEndpoint, error)
}

// AgentEndpointRow is the minimal scanner interface for a DB row returning
// (agent_id, binding_id, endpoint_url, auth_token_encrypted).
// All four columns are required; Scan must return sql.ErrNoRows when not found.
type AgentEndpointRow interface {
	Scan(dest ...any) error
}

// AgentEndpointQueryer executes the DB lookup for one agent by tenant+application+slug.
type AgentEndpointQueryer interface {
	QueryAgentEndpoint(ctx context.Context, tenantID, applicationID, agentSlug string) AgentEndpointRow
}

// DecryptFunc decrypts a stored ciphertext; returns ("", nil) for empty input.
type DecryptFunc func(ciphertext string) (string, error)

// DBAgentEndpointResolver implements AgentEndpointResolver using a DB queryier
// and a decrypt function. Both binaries (agent-runtime, dag-worker) wire this.
//
// It JOINs app_agent_bindings to agents and applications to:
//   - ensure the target agent is enabled
//   - ensure the binding belongs to the caller's application and tenant
//   - return the binding UUID so the callee can verify tenant ownership
//
// Missing binding → error (fail closed). Missing endpoint → error (fail closed).
type DBAgentEndpointResolver struct {
	db      AgentEndpointQueryer
	decrypt DecryptFunc
}

// NewDBAgentEndpointResolver creates a DBAgentEndpointResolver.
func NewDBAgentEndpointResolver(db AgentEndpointQueryer, decrypt DecryptFunc) *DBAgentEndpointResolver {
	return &DBAgentEndpointResolver{db: db, decrypt: decrypt}
}

func (r *DBAgentEndpointResolver) ResolveEndpoint(ctx context.Context, tenantID, applicationID, agentSlug string) (ResolvedEndpoint, error) {
	row := r.db.QueryAgentEndpoint(ctx, tenantID, applicationID, agentSlug)
	var agentID, bindingID, endpointURL, authTokenEncrypted string
	if err := row.Scan(&agentID, &bindingID, &endpointURL, &authTokenEncrypted); err != nil {
		return ResolvedEndpoint{}, fmt.Errorf("a2a_call: agent %q not bound to application or disabled", agentSlug)
	}
	if bindingID == "" {
		return ResolvedEndpoint{}, fmt.Errorf("a2a_call: agent %q has no binding for this application — add a binding before calling", agentSlug)
	}
	if endpointURL == "" {
		return ResolvedEndpoint{}, fmt.Errorf("a2a_call: agent %q has no endpoint configured", agentSlug)
	}
	var authToken string
	if authTokenEncrypted != "" {
		plain, err := r.decrypt(authTokenEncrypted)
		if err != nil {
			// Log-safe: never include the ciphertext or plaintext in the error.
			return ResolvedEndpoint{}, fmt.Errorf("a2a_call: decrypt auth token for agent %q: internal error", agentSlug)
		}
		authToken = plain
	}
	return ResolvedEndpoint{
		EndpointURL: endpointURL,
		AuthToken:   authToken,
		AgentID:     agentID,
		BindingID:   bindingID,
	}, nil
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
// Request and message UUIDs are derived deterministically from InvocationID + StepID
// + AgentSlug so that retries re-use the same IDs and do not create duplicate tasks.
func (c *HTTPA2ACaller) Call(ctx context.Context, p A2ACallParams) (json.RawMessage, error) {
	if p.Depth >= MaxA2ACallDepth {
		return nil, fmt.Errorf("a2a_call: maximum nesting depth %d reached — circular agent chains are not allowed", MaxA2ACallDepth)
	}

	ep, err := c.resolver.ResolveEndpoint(ctx, p.TenantID, p.ApplicationID, p.AgentSlug)
	if err != nil {
		return nil, err
	}

	// Stable request/message IDs derived from (InvocationID, StepID, AgentSlug).
	// This ensures that a retry of the same canvas step sends the same message UUID,
	// so idempotent A2A agents de-duplicate rather than starting a second task.
	rpcID := stableCallUUID(p.InvocationID, p.StepID, p.AgentSlug, "rpc")
	msgID := stableCallUUID(p.InvocationID, p.StepID, p.AgentSlug, "msg")

	part := buildA2ARPCPart(p.Input)
	rpcReq := a2aRPCRequest{
		JSONRPC: "2.0",
		Method:  "SendMessage",
		Params: a2aRPCParams{
			Message: a2aRPCMessage{
				Role:      "ROLE_USER",
				Parts:     []a2aRPCPart{part},
				MessageID: msgID,
			},
			Configuration: a2aRPCConfiguration{ReturnImmediately: false},
		},
		ID: rpcID,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("a2a_call: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("a2a_call: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	// Internal auth token from the registry — never a user-supplied value.
	if ep.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+ep.AuthToken)
	}
	// Identity + binding headers (Phase 1 protocol — internal Docker network only).
	req.Header.Set("X-Them-Tenant-Id", p.TenantID)
	req.Header.Set("X-Them-Application-Id", p.ApplicationID)
	req.Header.Set("X-Them-Agent-Id", ep.AgentID)
	req.Header.Set("X-Them-Binding-Id", ep.BindingID)
	// Depth propagation so the callee can enforce its own depth guard.
	req.Header.Set("X-Them-A2A-Depth", fmt.Sprintf("%d", p.Depth+1))

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
		return nil, fmt.Errorf("a2a_call: remote agent error %d: %s", rpcResp.Error.Code, sanitizeRemoteError(rpcResp.Error.Message))
	}
	if rpcResp.Result == nil {
		return nil, fmt.Errorf("a2a_call: empty response from agent %q", p.AgentSlug)
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

// urlPattern matches http/https URLs for sanitization of remote error messages.
var urlPattern = regexp.MustCompile(`https?://[^\s"']+`)

// sanitizeRemoteError strips URLs and truncates remote agent error messages
// so internal network topology and paths are not leaked to callers or logs.
func sanitizeRemoteError(msg string) string {
	msg = urlPattern.ReplaceAllString(msg, "[url-redacted]")
	msg = strings.TrimSpace(msg)
	const maxLen = 300
	if len(msg) > maxLen {
		msg = msg[:maxLen] + " [truncated]"
	}
	return msg
}

// stableCallUUID derives a deterministic UUID v5 from (invocationID, stepID,
// agentSlug, role) using the Nil namespace. When invocationID is empty (e.g. in
// tests), falls back to a random UUID so tests still produce valid UUIDs.
func stableCallUUID(invocationID, stepID, agentSlug, role string) string {
	if invocationID == "" {
		return uuid.New().String()
	}
	key := invocationID + ":" + stepID + ":" + agentSlug + ":" + role
	return uuid.NewSHA1(uuid.Nil, []byte(key)).String()
}

// Ensure HTTPA2ACaller satisfies A2ACaller.
var _ A2ACaller = (*HTTPA2ACaller)(nil)
