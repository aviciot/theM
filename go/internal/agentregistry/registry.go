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

// DBReader loads agent configurations from the database, scoped to a tenant.
type DBReader interface {
	// QueryAgentsByTenant returns all enabled agents belonging to the given tenant.
	// tenantID is a UUID string from the server-side auth context.
	QueryAgentsByTenant(ctx context.Context, tenantID string) ([]*AgentConfig, error)
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
