package agentregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	redisCacheKey     = "them:agents:registry"
	redisCacheTTL     = 600 * time.Second
	invalidateChannel = "them:agents:changed" // matches Python admin_agents.py publisher
	httpInvokeTimeout = 60 * time.Second
)

// AgentConfig holds the configuration for a single agent loaded from DB.
type AgentConfig struct {
	ID             int64  `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	AdapterType    string `json:"adapter_type"`
	EndpointURL    string `json:"endpoint_url"`
	AuthToken      string `json:"auth_token,omitempty"`
	MaxConcurrency int    `json:"max_concurrency"`
}

// DBReader loads agent configurations from the database.
type DBReader interface {
	QueryAgents(ctx context.Context) ([]*AgentConfig, error)
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

// Registry caches agent configurations and routes tool invocations.
type Registry struct {
	db         DBReader
	cache      CacheClient
	l1         sync.Map
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

// Invoke routes a tool call to the correct adapter.
func (r *Registry) Invoke(ctx context.Context, slug string, input json.RawMessage) (json.RawMessage, error) {
	cfg, err := r.getAgent(ctx, slug)
	if err != nil {
		return nil, err
	}

	switch cfg.AdapterType {
	case "a2a":
		return r.invokeA2A(ctx, cfg, input)
	case "ws_mock", "mock":
		return r.invokeMock(cfg, input)
	case "http":
		return r.invokeHTTP(ctx, cfg, input)
	default:
		return nil, fmt.Errorf("agentregistry: unknown adapter type %q for agent %s", cfg.AdapterType, slug)
	}
}

func (r *Registry) getAgent(ctx context.Context, slug string) (*AgentConfig, error) {
	if v, ok := r.l1.Load(slug); ok {
		return v.(*AgentConfig), nil
	}
	if err := r.LoadAll(ctx); err != nil {
		return nil, fmt.Errorf("agentregistry: reload failed: %w", err)
	}
	if v, ok := r.l1.Load(slug); ok {
		return v.(*AgentConfig), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, slug)
}

// LoadAll populates L1 from L2 (Redis) or DB.
func (r *Registry) LoadAll(ctx context.Context) error {
	raw, found, err := r.cache.Get(ctx, redisCacheKey)
	if err == nil && found && len(raw) > 0 {
		return r.populateL1FromJSON(raw)
	}

	agents, err := r.db.QueryAgents(ctx)
	if err != nil {
		return fmt.Errorf("agentregistry: db load: %w", err)
	}

	encoded, err := json.Marshal(agents)
	if err == nil {
		_ = r.cache.SetEX(ctx, redisCacheKey, encoded, redisCacheTTL)
	}

	for _, a := range agents {
		r.l1.Store(a.Slug, a)
	}
	r.logger.Info("agentregistry: loaded from DB", "count", len(agents))
	return nil
}

// Subscribe starts the Redis pub/sub listener for cache invalidation.
func (r *Registry) Subscribe(ctx context.Context) {
	r.logger.Info("agentregistry: pub/sub listener started", "channel", invalidateChannel)
	err := r.cache.Subscribe(ctx, invalidateChannel, func(_ string) {
		r.invalidateL1()
		r.logger.Info("agentregistry: L1 cache invalidated via pub/sub")
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("agentregistry: pub/sub listener error", "error", err)
	}
}

func (r *Registry) invalidateL1() {
	r.l1.Range(func(key, _ any) bool {
		r.l1.Delete(key)
		return true
	})
}

func (r *Registry) populateL1FromJSON(raw []byte) error {
	var agents []*AgentConfig
	if err := json.Unmarshal(raw, &agents); err != nil {
		return fmt.Errorf("agentregistry: unmarshal L2: %w", err)
	}
	for _, a := range agents {
		r.l1.Store(a.Slug, a)
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
	Message a2aMessage `json:"message"`
}

type a2aMessage struct {
	Role      string    `json:"role"`
	Parts     []a2aPart `json:"parts"`
	MessageID string    `json:"messageId"`
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type a2aResponse struct {
	JSONRPC string     `json:"jsonrpc"`
	Result  *a2aResult `json:"result,omitempty"`
	Error   *a2aError  `json:"error,omitempty"`
	ID      string     `json:"id"`
}

type a2aResult struct {
	Status    a2aStatus     `json:"status"`
	Artifacts []a2aArtifact `json:"artifacts"`
}

type a2aStatus struct {
	State string `json:"state"`
}

type a2aArtifact struct {
	Parts []a2aPart `json:"parts"`
}

type a2aError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *Registry) invokeA2A(ctx context.Context, cfg *AgentConfig, input json.RawMessage) (json.RawMessage, error) {
	var inputMap map[string]any
	text := string(input)
	if err := json.Unmarshal(input, &inputMap); err == nil {
		if s, ok := inputMap["input"].(string); ok {
			text = s
		}
	}

	reqID := newUUID()
	msgID := newUUID()

	rpcReq := a2aRequest{
		JSONRPC: "2.0",
		Method:  "message/send",
		Params: a2aParams{
			Message: a2aMessage{
				Role:      "user",
				Parts:     []a2aPart{{Kind: "text", Text: text}},
				MessageID: msgID,
			},
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

	var output string
	for _, artifact := range rpcResp.Result.Artifacts {
		for _, part := range artifact.Parts {
			if part.Kind == "text" {
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
