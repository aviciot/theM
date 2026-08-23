package agentregistry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// redisCacheKeyFmt is the per-tenant L2 cache key.
	// Tenant ID comes from the server-side auth context, never from client payload.
	// Replaces the old global "them:agents:registry" key (SEC-03).
	redisCacheKeyFmt = "them:agents:registry:%s" // %s = tenant_id UUID string

	redisCacheTTL = 600 * time.Second

	// invalidateChannel is the Redis pub/sub channel for cache invalidation.
	// Message payload is the tenant_id UUID string whose cache should be evicted.
	// The admin service publishes the tenantID when any agent in that tenant mutates.
	// Matches the constant used in admin/service/agents.go.
	invalidateChannel = "them:agents:changed"

	// httpInvokeTimeout covers the full round-trip to an A2A agent including its
	// internal LLM call.  docu_writer calls Claude with up to 8192 output tokens
	// which can take 60-90 s; 3 minutes gives comfortable headroom.
	httpInvokeTimeout = 3 * time.Minute
)

// AgentConfig holds the configuration for a single agent loaded from DB.
type AgentConfig struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	AdapterType      string `json:"adapter_type"`
	EndpointURL      string `json:"endpoint_url"`
	AuthToken        string `json:"auth_token,omitempty"`
	MaxConcurrency   int    `json:"max_concurrency"`
	SupportsStreaming bool   `json:"supports_streaming"` // if true, use SendStreamingMessage (SSE)
}

// ArtifactCallback is called by invokeA2AStreaming once per complete artifact
// as it arrives on the SSE stream. Used by the orchestrator to record and
// emit artifacts progressively without waiting for the stream to close.
type ArtifactCallback func(filename, contentType, dataBase64 string)

// DBReader loads agent configurations from the database, scoped to a tenant.
type DBReader interface {
	// QueryAgentsByTenant returns all enabled agents belonging to the given tenant.
	// tenantID is a UUID string from the server-side auth context.
	QueryAgentsByTenant(ctx context.Context, tenantID string) ([]*AgentConfig, error)

	// GetBindingID returns the app_agent_bindings.id UUID for the given
	// (applicationID, agentID) pair, or ("", nil) when no binding exists.
	// Used by InvokeForRun to populate X-Them-Binding-Id for canvas_a2a agents.
	GetBindingID(ctx context.Context, applicationID, agentID string) (string, error)
}

// CacheClient is the Redis interface used by the registry.
type CacheClient interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	SetEX(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Subscribe(ctx context.Context, channel string, handler func(payload string)) error
}

// ErrUnknownAgent is returned when no agent with the given slug is registered.
var ErrUnknownAgent = errors.New("agentregistry: unknown agent slug")

// Registry caches agent configurations per tenant and routes tool invocations.
//
// Tenant isolation guarantees (SEC-03):
//   - L1 (sync.Map): keys are "{tenantID}:{slug}" — two tenants with agents of
//     the same slug never collide.
//   - L2 (Redis): key is "them:agents:registry:{tenantID}" — per-tenant bucket,
//     no global fallback.
//   - Pub/sub invalidation: payload must be the tenantID string whose cache to
//     evict. An empty payload evicts nothing (no global wipe).
//   - tenantID must come from the server-side auth context (epconfig.TenantID or
//     JWT claim). It must never be sourced from client request body/headers.
type Registry struct {
	db         DBReader
	cache      CacheClient
	l1         sync.Map // key: "{tenantID}:{slug}" → *AgentConfig
	httpClient *http.Client
	logger     *slog.Logger
}

// New creates a Registry.
func New(db DBReader, cache CacheClient, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		db:         db,
		cache:      cache,
		httpClient: &http.Client{Timeout: httpInvokeTimeout},
		logger:     logger,
	}
}

// l1Key returns the L1 cache key for a given tenant and agent slug.
// This is the only place the key format is defined — never inline.
func l1Key(tenantID, slug string) string {
	return tenantID + ":" + slug
}

// redisCacheKey returns the L2 Redis key for a given tenant.
func redisCacheKey(tenantID string) string {
	return fmt.Sprintf(redisCacheKeyFmt, tenantID)
}

// Invoke routes a tool call to the correct adapter.
// tenantID must be the server-resolved tenant ID (from epconfig or JWT), never
// from client-supplied data.
func (r *Registry) Invoke(ctx context.Context, tenantID, slug string, input json.RawMessage) (json.RawMessage, error) {
	cfg, err := r.getAgent(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}

	switch cfg.AdapterType {
	case "a2a", "a2a_async":
		if cfg.SupportsStreaming {
			return r.invokeA2AStreaming(ctx, cfg, input, nil)
		}
		return r.invokeA2A(ctx, cfg, input)
	case "ws_mock", "mock":
		return r.invokeMock(cfg, input)
	case "http":
		return r.invokeHTTP(ctx, cfg, input)
	default:
		return nil, fmt.Errorf("agentregistry: unknown adapter type %q for agent %s", cfg.AdapterType, slug)
	}
}

// InvokeForRun routes a tool call with full run context.
// For canvas_a2a agents it looks up the app_agent_binding for (applicationID, agentID)
// and calls InvokeWithMeta so the agent-runtime receives the invocation context headers.
// For all other transports it delegates to Invoke.
// applicationID must be the server-resolved application UUID — never from client data.
func (r *Registry) InvokeForRun(ctx context.Context, tenantID, applicationID, slug string, input json.RawMessage) (json.RawMessage, error) {
	cfg, err := r.getAgent(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}

	if cfg.AdapterType != "canvas_a2a" {
		// Non-canvas agents: standard adapter routing.
		switch cfg.AdapterType {
		case "a2a", "a2a_async":
			if cfg.SupportsStreaming {
				return r.invokeA2AStreaming(ctx, cfg, input, nil)
			}
			return r.invokeA2A(ctx, cfg, input)
		case "ws_mock", "mock":
			return r.invokeMock(cfg, input)
		case "http":
			return r.invokeHTTP(ctx, cfg, input)
		default:
			return nil, fmt.Errorf("agentregistry: unknown adapter type %q for agent %s", cfg.AdapterType, slug)
		}
	}

	// canvas_a2a: look up binding so the agent-runtime can resolve credentials.
	bindingID, err := r.db.GetBindingID(ctx, applicationID, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: binding lookup for canvas agent %s: %w", slug, err)
	}

	meta := &InvocationMeta{
		ApplicationID: applicationID,
		AgentID:       cfg.ID,
		BindingID:     bindingID,
	}
	return r.InvokeWithMeta(ctx, tenantID, slug, input, meta)
}

// InvokeForRunStreaming is like InvokeForRun but wires an ArtifactCallback for
// streaming A2A agents. onArtifact is called once per complete artifact as it
// arrives on the SSE stream, allowing the orchestrator to record and emit each
// file progressively without waiting for the entire stream to close.
//
// For non-streaming agents this falls back to InvokeForRun (callback ignored).
func (r *Registry) InvokeForRunStreaming(ctx context.Context, tenantID, applicationID, slug string, input json.RawMessage, onArtifact func(filename, contentType, dataBase64 string)) (json.RawMessage, error) {
	cfg, err := r.getAgent(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}
	if cfg.AdapterType != "canvas_a2a" && (cfg.AdapterType == "a2a" || cfg.AdapterType == "a2a_async") && cfg.SupportsStreaming {
		return r.invokeA2AStreaming(ctx, cfg, input, onArtifact)
	}
	// Non-streaming fallback — delegate to standard routing.
	return r.InvokeForRun(ctx, tenantID, applicationID, slug, input)
}

func (r *Registry) getAgent(ctx context.Context, tenantID, slug string) (*AgentConfig, error) {
	key := l1Key(tenantID, slug)
	if v, ok := r.l1.Load(key); ok {
		return v.(*AgentConfig), nil
	}
	if err := r.LoadAll(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("agentregistry: reload failed: %w", err)
	}
	if v, ok := r.l1.Load(key); ok {
		return v.(*AgentConfig), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, slug)
}

// LoadAll populates L1 for the given tenant from L2 (Redis) or DB.
// tenantID must be a server-resolved UUID string — never from client data.
func (r *Registry) LoadAll(ctx context.Context, tenantID string) error {
	redisKey := redisCacheKey(tenantID)
	raw, found, err := r.cache.Get(ctx, redisKey)
	if err == nil && found && len(raw) > 0 {
		return r.populateL1FromJSON(tenantID, raw)
	}

	agents, err := r.db.QueryAgentsByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("agentregistry: db load: %w", err)
	}

	encoded, err := json.Marshal(agents)
	if err == nil {
		_ = r.cache.SetEX(ctx, redisKey, encoded, redisCacheTTL)
	}

	for _, a := range agents {
		r.l1.Store(l1Key(tenantID, a.Slug), a)
	}
	r.logger.Info("agentregistry: loaded from DB", "tenant_id", tenantID, "count", len(agents))
	return nil
}

// Subscribe starts the Redis pub/sub listener for cache invalidation.
// The pub/sub message payload must be the tenantID UUID string whose cache
// should be evicted. An empty payload is a no-op (no global eviction).
func (r *Registry) Subscribe(ctx context.Context) {
	r.logger.Info("agentregistry: pub/sub listener started", "channel", invalidateChannel)
	err := r.cache.Subscribe(ctx, invalidateChannel, func(payload string) {
		if payload == "" {
			r.logger.Warn("agentregistry: received empty invalidation payload — ignoring")
			return
		}
		r.invalidateTenant(payload)
		r.logger.Info("agentregistry: L1 cache evicted for tenant", "tenant_id", payload)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("agentregistry: pub/sub listener error", "error", err)
	}
}

// invalidateTenant removes all L1 entries for the given tenant.
// Only entries with keys prefixed by "{tenantID}:" are evicted — other tenants
// are unaffected.
func (r *Registry) invalidateTenant(tenantID string) {
	prefix := tenantID + ":"
	r.l1.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok {
			if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
				r.l1.Delete(key)
			}
		}
		return true
	})
}

func (r *Registry) populateL1FromJSON(tenantID string, raw []byte) error {
	var agents []*AgentConfig
	if err := json.Unmarshal(raw, &agents); err != nil {
		return fmt.Errorf("agentregistry: unmarshal L2: %w", err)
	}
	for _, a := range agents {
		r.l1.Store(l1Key(tenantID, a.Slug), a)
	}
	return nil
}

type a2aRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  a2aParams `json:"params"`
	ID      string    `json:"id"`
}

type a2aParams struct {
	Message       a2aMessage       `json:"message"`
	Configuration a2aConfiguration `json:"configuration,omitempty"`
}

type a2aConfiguration struct {
	ReturnImmediately bool `json:"returnImmediately"`
}

type a2aMessage struct {
	Role      int       `json:"role"`       // 1 = user (A2A v1.1 proto enum)
	Parts     []a2aPart `json:"parts"`
	MessageID string    `json:"messageId"`
	ContextID string    `json:"contextId,omitempty"`
}

type a2aPart struct {
	Text      string         `json:"text,omitempty"`
	Kind      string         `json:"kind,omitempty"`      // kept for v0.3 compat probing
	Data      map[string]any `json:"data,omitempty"`      // typed data part (structured JSON)
	Raw       string         `json:"raw,omitempty"`       // binary file part — base64 in protobuf-JSON (part.raw bytes field)
	MediaType string         `json:"mediaType,omitempty"` // media type for both data and raw parts
	Filename  string         `json:"filename,omitempty"`  // present on file parts
}

type a2aResponse struct {
	JSONRPC string     `json:"jsonrpc"`
	Result  *a2aResult `json:"result,omitempty"`
	Error   *a2aError  `json:"error,omitempty"`
	ID      string     `json:"id"`
}

// a2aResult wraps a task (SendMessage non-streaming response) or a message.
type a2aResult struct {
	Task    *a2aTask `json:"task,omitempty"`
	Message *a2aTask `json:"message,omitempty"` // some agents return message instead of task
}

type a2aTask struct {
	Artifacts []a2aArtifact `json:"artifacts"`
	Status    a2aStatus     `json:"status,omitempty"`
}

type a2aStatus struct {
	State string `json:"state"`
}

type a2aArtifact struct {
	ArtifactID  string    `json:"artifactId,omitempty"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Parts       []a2aPart `json:"parts"`
}

type a2aError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// buildA2APart decides whether to send input as a text part or a typed data part.
//
//   - {"input": "some text"}  → text part (plain agent call)
//   - {"format":…, "title":…} → data part (structured call, e.g. docu_writer)
//   - any other JSON object   → data part (pass fields through as-is)
//   - non-object JSON         → text part (fallback)
func buildA2APart(input json.RawMessage) a2aPart {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		// Not a JSON object — send raw as text.
		return a2aPart{Text: string(input)}
	}
	// Unwrap simple {"input": "..."} wrapper.
	// If the value is itself a JSON object, treat it as a typed data part so
	// agents like docu_writer receive structured fields (format, title, content).
	if len(m) == 1 {
		if s, ok := m["input"].(string); ok {
			var inner map[string]any
			if err := json.Unmarshal([]byte(s), &inner); err == nil && len(inner) > 0 {
				return a2aPart{Data: inner, MediaType: "application/json"}
			}
			return a2aPart{Text: s}
		}
	}
	// Structured input → typed data part.
	return a2aPart{Data: m, MediaType: "application/json"}
}

func (r *Registry) invokeA2A(ctx context.Context, cfg *AgentConfig, input json.RawMessage) (json.RawMessage, error) {
	// Build the A2A message part.
	// If input is a JSON object and has fields other than "input", send it as a
	// typed data part so agents like docu_writer receive structured fields
	// (format, title, content) instead of a raw JSON string.
	// If input is a plain {"input": "..."} wrapper, unwrap to a text part.
	part := buildA2APart(input)

	reqID := newUUID()
	msgID := newUUID()

	rpcReq := a2aRequest{
		JSONRPC: "2.0",
		Method:  "SendMessage", // A2A v1.1 PascalCase gRPC method name
		Params: a2aParams{
			Message: a2aMessage{
				Role:      1, // ROLE_USER in proto enum
				Parts:     []a2aPart{part},
				MessageID: msgID,
			},
			Configuration: a2aConfiguration{ReturnImmediately: false},
		},
		ID: reqID,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0") // required by SDK v1.1 version validator
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a: http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a: read response: %w", err)
	}

	var rpcResp a2aResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("agentregistry: a2a: decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("agentregistry: a2a error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		return nil, fmt.Errorf("agentregistry: a2a: empty result")
	}
	return extractA2AResult(rpcResp.Result)
}

// streamResponse mirrors the A2A v1.0 StreamResponse proto for JSON decoding.
// Only the fields we act on are mapped; unknown fields are ignored.
type streamResponse struct {
	StatusUpdate   *streamStatusUpdate   `json:"status_update,omitempty"`
	ArtifactUpdate *streamArtifactUpdate `json:"artifact_update,omitempty"`
	Task           *a2aTask              `json:"task,omitempty"`
}

type streamStatusUpdate struct {
	State string `json:"state"`
}

type streamArtifactUpdate struct {
	Artifact  a2aArtifact `json:"artifact"`
	Append    bool        `json:"append"`
	LastChunk bool        `json:"last_chunk"`
}

// invokeA2AStreaming calls SendStreamingMessage on a streaming A2A agent.
// It reads the SSE response line-by-line, calls onArtifact for each complete
// artifact, and returns the final tool result (plain text summary, no base64).
// onArtifact may be nil if the caller only needs the final result.
func (r *Registry) invokeA2AStreaming(ctx context.Context, cfg *AgentConfig, input json.RawMessage, onArtifact func(filename, contentType, dataBase64 string)) (json.RawMessage, error) {
	part := buildA2APart(input)
	reqID := newUUID()
	msgID := newUUID()

	rpcReq := a2aRequest{
		JSONRPC: "2.0",
		Method:  "SendStreamingMessage",
		Params: a2aParams{
			Message: a2aMessage{
				Role:      1,
				Parts:     []a2aPart{part},
				MessageID: msgID,
			},
			Configuration: a2aConfiguration{ReturnImmediately: false},
		},
		ID: reqID,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a-stream: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a-stream: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("A2A-Version", "1.0")
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}

	// Use a client without a read deadline — streaming responses are long-lived.
	streamClient := &http.Client{Timeout: 0}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: a2a-stream: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentregistry: a2a-stream: agent returned %d: %s", resp.StatusCode, string(b))
	}

	// Parse SSE: each event is a "data: <json>" line.
	scanner := bufio.NewScanner(resp.Body)
	var lastOutput string
	var artifactNames []string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		// Each data line is a JSON-RPC response wrapping a StreamResponse.
		var rpcResp struct {
			Result *streamResponse `json:"result,omitempty"`
			Error  *a2aError       `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
			slog.Warn("agentregistry: a2a-stream: skip unparseable event", "data", data, "error", err)
			continue
		}
		if rpcResp.Error != nil {
			return nil, fmt.Errorf("agentregistry: a2a-stream: agent error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		if rpcResp.Result == nil {
			continue
		}
		ev := rpcResp.Result

		// Artifact update — call onArtifact when the artifact is complete.
		if ev.ArtifactUpdate != nil && ev.ArtifactUpdate.LastChunk {
			for _, part := range ev.ArtifactUpdate.Artifact.Parts {
				if part.Filename == "" {
					continue
				}
				var encoded, contentType string
				if part.Raw != "" {
					encoded, contentType = part.Raw, part.MediaType
				} else if part.Text != "" {
					encoded = base64.StdEncoding.EncodeToString([]byte(part.Text))
					contentType = part.MediaType
				} else {
					continue
				}
				if contentType == "" {
					contentType = "application/octet-stream"
				}
				name := part.Filename
				if ev.ArtifactUpdate.Artifact.Name != "" {
					name = ev.ArtifactUpdate.Artifact.Name
				}
				artifactNames = append(artifactNames, name)
				if onArtifact != nil {
					onArtifact(name, contentType, encoded)
				}
			}
		}

		// Terminal task event — extract plain text output if present.
		if ev.Task != nil {
			result := &a2aResult{Task: ev.Task}
			out, err := extractA2AResult(result)
			if err == nil {
				var m map[string]any
				if json.Unmarshal(out, &m) == nil {
					if s, ok := m["output"].(string); ok {
						lastOutput = s
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("agentregistry: a2a-stream: read: %w", err)
	}

	// Build final tool result — clean summary, no base64.
	if len(artifactNames) > 0 {
		summary := fmt.Sprintf("%d file artifact(s) streamed: %s", len(artifactNames), strings.Join(artifactNames, ", "))
		out, _ := json.Marshal(map[string]string{"output": summary})
		return out, nil
	}
	if lastOutput != "" {
		out, _ := json.Marshal(map[string]string{"output": lastOutput})
		return out, nil
	}
	out, _ := json.Marshal(map[string]string{"output": "agent completed (no output)"})
	return out, nil
}

// extractA2AResult extracts the tool output from an A2A response result.
// Collects ALL file parts across all artifacts and returns them in a
// backward-compatible envelope: single file → {"artifact":{...}},
// multiple files → {"artifacts":[...]}. Plain text is returned as
// {"output":"..."} when no file parts are present.
func extractA2AResult(result *a2aResult) (json.RawMessage, error) {
	// Extract artifacts from task or message wrapper.
	var artifacts []a2aArtifact
	if result.Task != nil {
		artifacts = result.Task.Artifacts
	} else if result.Message != nil {
		artifacts = result.Message.Artifacts
	}

	// Collect all file parts across all artifacts.
	type fileBody = map[string]string
	var bodies []fileBody
	for _, artifact := range artifacts {
		for _, part := range artifact.Parts {
			if part.Filename == "" {
				continue
			}
			var encoded, contentType string
			if part.Raw != "" {
				encoded, contentType = part.Raw, part.MediaType
			} else if part.Text != "" {
				encoded = base64.StdEncoding.EncodeToString([]byte(part.Text))
				contentType = part.MediaType
			} else {
				continue
			}
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			name := part.Filename
			if artifact.Name != "" {
				name = artifact.Name
			}
			bodies = append(bodies, fileBody{
				"filename":     name,
				"content_type": contentType,
				"data_base64":  encoded,
			})
		}
	}

	switch len(bodies) {
	case 0:
		// fall through to plain-text extraction below
	case 1:
		// Single artifact — keep legacy {"artifact":{}} shape for backward compat.
		out, _ := json.Marshal(map[string]any{
			"output":   "File artifact: " + bodies[0]["filename"],
			"artifact": bodies[0],
		})
		return out, nil
	default:
		// Multiple artifacts — plural envelope; orchestrator fans out recording.
		names := make([]string, len(bodies))
		for i, b := range bodies {
			names[i] = b["filename"]
		}
		out, _ := json.Marshal(map[string]any{
			"output":    fmt.Sprintf("%d file artifacts: %s", len(bodies), strings.Join(names, ", ")),
			"artifacts": bodies,
		})
		return out, nil
	}

	// No file artifact — extract plain text from the first non-empty text part.
	var output string
	for _, artifact := range artifacts {
		for _, part := range artifact.Parts {
			if part.Text != "" {
				output = part.Text
				break
			}
		}
		if output != "" {
			break
		}
	}

	out, _ := json.Marshal(map[string]string{"output": output})
	return out, nil
}

func (r *Registry) invokeMock(_ *AgentConfig, input json.RawMessage) (json.RawMessage, error) {
	out, _ := json.Marshal(map[string]any{
		"output": "mock response for input: " + string(input),
	})
	return out, nil
}

func (r *Registry) invokeHTTP(ctx context.Context, cfg *AgentConfig, input json.RawMessage) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EndpointURL, bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("agentregistry: http invoke: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: http invoke: %w", err)
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: http invoke: read body: %w", err)
	}
	return json.RawMessage(out), nil
}

// InvocationMeta carries runtime context forwarded to the agent-runtime service.
// Phase 1: passed as X-Them-* headers on the internal Docker network.
// Phase 3: will be replaced by a signed JWT in THE_M_INVOCATION_JWT_KEY.
type InvocationMeta struct {
	// ApplicationID is the application (tenant boundary) that owns this invocation.
	ApplicationID string
	// AgentID is the canonical agent UUID (matches the URL slug cross-check).
	AgentID string
	// BindingID is the AppAgentBinding UUID for this application+agent pair.
	BindingID string
}

// InvokeWithMeta routes a call to the agent-runtime service, injecting InvocationMeta
// as X-Them-* headers (Phase 1 protocol — internal Docker network only).
// If meta is nil, the call degrades to Invoke (standard registry routing).
func (r *Registry) InvokeWithMeta(ctx context.Context, tenantID, slug string, payload json.RawMessage, meta *InvocationMeta) (json.RawMessage, error) {
	if meta == nil {
		return r.Invoke(ctx, tenantID, slug, payload)
	}

	cfg, err := r.getAgent(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}

	// canvas_a2a agents are served by the agent-runtime which speaks A2A JSON-RPC.
	// Wrap the tool input in a SendMessage envelope identical to invokeA2A.
	part := buildA2APart(payload)
	reqID := newUUID()
	msgID := newUUID()
	rpcReq := a2aRequest{
		JSONRPC: "2.0",
		Method:  "SendMessage",
		Params: a2aParams{
			Message: a2aMessage{
				Role:      1,
				Parts:     []a2aPart{part},
				MessageID: msgID,
			},
			Configuration: a2aConfiguration{ReturnImmediately: false},
		},
		ID: reqID,
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.EndpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	// Phase 1 invocation context — internal Docker network only.
	req.Header.Set("X-Them-Tenant-Id", tenantID)
	req.Header.Set("X-Them-Application-Id", meta.ApplicationID)
	req.Header.Set("X-Them-Agent-Id", meta.AgentID)
	req.Header.Set("X-Them-Binding-Id", meta.BindingID)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: read body: %w", err)
	}

	var rpcResp a2aResponse
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: a2a error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		return nil, fmt.Errorf("agentregistry: invoke with meta: empty result")
	}
	return extractA2AResult(rpcResp.Result)
}
