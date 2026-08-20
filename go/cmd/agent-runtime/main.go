// Package main is the them-agent-runtime — a generic stateless A2A agent server.
// It reads AgentSpec definitions from PostgreSQL (cached locally, TTL 60s) and
// serves any canvas-designed agent over the A2A JSON-RPC 2.0 wire protocol.
// Credentials are resolved per-request from app_agent_bindings and decrypted
// in-memory only — never logged, persisted, or returned in responses.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		logger.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		logger.Error("redis connect failed", "err", err)
		os.Exit(1)
	}

	// AuthRedisClient satisfies agentgen.TaskStoreRedis (Get/SetEX/Del).
	taskRedis := cache.NewAuthRedisClient(redisCache.Client())
	cryptoKey := crypto.DeriveKey(cfg.SecretKey)

	rt := &Runtime{
		pool:      database.Pool(),
		cryptoKey: cryptoKey,
		taskStore: agentgen.NewRedisTaskStore(taskRedis),
		specCache: &specCache{entries: make(map[string]*cachedSpec)},
		logger:    logger,
		interp: agentgen.NewInterpreter(
			&http.Client{Timeout: 60 * time.Second},
			&anthropicLLMFactory{platformKey: cfg.AnthropicAPIKey},
			cfg.AnthropicAPIKey,
		),
	}

	port := "9300"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz)
	r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	r.Post("/agents/{slug}", rt.handle)

	logger.Info("them-agent-runtime starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}

// Runtime is the stateless request handler. One per process; all state in Redis/Postgres.
type Runtime struct {
	pool      *pgxpool.Pool
	cryptoKey []byte
	taskStore *agentgen.RedisTaskStore
	specCache *specCache
	logger    *slog.Logger
	interp    *agentgen.Interpreter
}

func (rt *Runtime) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}

// agentCard serves /.well-known/agent-card.json (correct A2A path, not agent.json).
func (rt *Runtime) agentCard(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	spec, err := rt.loadSpecBySlug(r.Context(), slug)
	if err != nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildAgentCard(spec)) //nolint:errcheck
}

// handle is the A2A JSON-RPC endpoint for a specific agent.
func (rt *Runtime) handle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	ic, err := rt.parseInvocationContext(r)
	if err != nil {
		writeJSONRPCError(w, nil, -32600, "unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Invariant 2: cross-check URL slug vs authoritative agent_id from invocation context.
	spec, err := rt.loadSpecByAgentID(r.Context(), ic.AgentID)
	if err != nil || spec.Slug != slug {
		writeJSONRPCError(w, nil, -32600, "forbidden", http.StatusForbidden)
		return
	}
	ic.Spec = spec

	binding, err := rt.loadBinding(r.Context(), ic.ApplicationID, ic.AgentID, ic.BindingID)
	if err != nil {
		writeJSONRPCError(w, nil, -32600, "binding not found", http.StatusNotFound)
		return
	}

	// Invariant 1: assert binding definition_id matches spec when pinned.
	if binding.DefinitionID != nil && *binding.DefinitionID != spec.DefinitionID {
		writeJSONRPCError(w, nil, -32603, "binding stale — application must be re-published", http.StatusConflict)
		return
	}

	creds, err := binding.ResolveCredentials(func(ct string) (string, error) {
		return crypto.DecryptStored(rt.cryptoKey, ct)
	})
	if err != nil {
		// Do not log err.Error() — it may contain partial credential material.
		rt.logger.Error("credential resolution failed", "ic", ic.String())
		writeJSONRPCError(w, nil, -32603, "credential resolution failed", http.StatusInternalServerError)
		return
	}
	ic.Credentials = creds
	ic.ConfigOverrides = binding.ConfigOverrides
	ic.Policies = binding.Policies

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error", http.StatusBadRequest)
		return
	}

	switch req.Method {
	case "message/send":
		rt.handleMessageSend(w, r, ic, &req)
	case "tasks/get":
		rt.handleTasksGet(w, r, ic, &req)
	case "tasks/cancel":
		rt.handleTasksCancel(w, r, ic, &req)
	default:
		writeJSONRPCError(w, req.ID, -32601, "method not found: "+req.Method, http.StatusOK)
	}
}

func (rt *Runtime) handleMessageSend(w http.ResponseWriter, r *http.Request, ic *agentgen.InvocationContext, req *jsonRPCRequest) {
	var params struct {
		Message struct {
			TaskID string `json:"taskId"`
			Parts  []struct {
				Kind string          `json:"kind"`
				Text string          `json:"text,omitempty"`
				Data json.RawMessage `json:"data,omitempty"`
			} `json:"parts"`
		} `json:"message"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, -32602, "invalid params", http.StatusOK)
		return
	}

	taskID := params.Message.TaskID
	if taskID == "" {
		taskID = uuid.NewString()
	}

	// Extract text and structured data from parts.
	// text part  → vars["input"] (the raw caller message)
	// data part  → each top-level key becomes a named pipeline var
	// Both may coexist; data keys override "input" only if the key name is "input".
	inputText := ""
	dataVars := map[string]any{}
	for _, p := range params.Message.Parts {
		switch p.Kind {
		case "text":
			if p.Text != "" && inputText == "" {
				inputText = p.Text
			}
		case "data":
			if len(p.Data) > 0 {
				var obj map[string]any
				if err := json.Unmarshal(p.Data, &obj); err == nil {
					for k, v := range obj {
						dataVars[k] = v
					}
				}
			}
		}
	}

	if len(ic.Spec.Skills) == 0 {
		writeJSONRPCError(w, req.ID, -32603, "agent has no skills", http.StatusOK)
		return
	}
	skill := &ic.Spec.Skills[0]

	ts := &agentgen.TaskState{
		TaskID:        taskID,
		TenantID:      ic.TenantID,
		ApplicationID: ic.ApplicationID,
		AgentID:       ic.AgentID,
		BindingID:     ic.BindingID,
		Status:        "working",
	}
	if err := rt.taskStore.Create(r.Context(), ts); err != nil {
		rt.logger.Warn("task store create failed", "task_id", taskID, "err", err)
	}

	execResult, err := rt.interp.Execute(r.Context(), ic, skill, inputText, dataVars)
	if err != nil {
		ts.Status = "failed"
		_ = rt.taskStore.Set(r.Context(), ts)
		writeJSONRPCError(w, req.ID, -32603, err.Error(), http.StatusOK)
		return
	}

	ts.Status = "completed"
	ts.Artifacts = []string{execResult.Text}
	_ = rt.taskStore.Set(r.Context(), ts)

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"id":     taskID,
			"status": map[string]any{"state": "completed"},
			"artifacts": []map[string]any{
				{
					"parts": []map[string]any{
						{"kind": "text", "text": execResult.Text},
					},
					"index": 0,
				},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (rt *Runtime) handleTasksGet(w http.ResponseWriter, r *http.Request, ic *agentgen.InvocationContext, req *jsonRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, -32602, "invalid params", http.StatusOK)
		return
	}
	// Invariant 3: ownership enforced inside Get.
	ts, err := rt.taskStore.Get(r.Context(), params.ID, ic.TenantID, ic.ApplicationID)
	if err != nil {
		writeJSONRPCError(w, req.ID, -32001, "task not found", http.StatusOK)
		return
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"id":     ts.TaskID,
			"status": map[string]any{"state": ts.Status},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (rt *Runtime) handleTasksCancel(w http.ResponseWriter, r *http.Request, ic *agentgen.InvocationContext, req *jsonRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, -32602, "invalid params", http.StatusOK)
		return
	}
	// Invariant 3: ownership enforced inside Get.
	ts, err := rt.taskStore.Get(r.Context(), params.ID, ic.TenantID, ic.ApplicationID)
	if err != nil {
		writeJSONRPCError(w, req.ID, -32001, "task not found", http.StatusOK)
		return
	}
	ts.Status = "canceled"
	_ = rt.taskStore.Set(r.Context(), ts)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"id":     ts.TaskID,
			"status": map[string]any{"state": "canceled"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// parseInvocationContext reads identity from X-Them-* headers.
// Phase 1 uses plain headers (internal Docker network only).
// Phase 3 upgrades to signed JWT (THE_M_INVOCATION_JWT_KEY).
func (rt *Runtime) parseInvocationContext(r *http.Request) (*agentgen.InvocationContext, error) {
	tenantID := r.Header.Get("X-Them-Tenant-Id")
	appID := r.Header.Get("X-Them-Application-Id")
	agentID := r.Header.Get("X-Them-Agent-Id")
	bindingID := r.Header.Get("X-Them-Binding-Id")
	if tenantID == "" || appID == "" || agentID == "" {
		return nil, fmt.Errorf("missing required invocation context headers")
	}
	return &agentgen.InvocationContext{
		TenantID:      tenantID,
		ApplicationID: appID,
		AgentID:       agentID,
		BindingID:     bindingID,
	}, nil
}

// specCache is an in-process AgentSpec cache with 60s TTL per entry.
type specCache struct {
	mu      sync.Mutex
	entries map[string]*cachedSpec
}

type cachedSpec struct {
	spec      *agentgen.AgentSpec
	expiresAt time.Time
}

func (c *specCache) get(agentID string) *agentgen.AgentSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[agentID]; ok && time.Now().Before(e.expiresAt) {
		return e.spec
	}
	return nil
}

func (c *specCache) set(agentID string, spec *agentgen.AgentSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[agentID] = &cachedSpec{spec: spec, expiresAt: time.Now().Add(60 * time.Second)}
}

func (rt *Runtime) loadSpecByAgentID(ctx context.Context, agentID string) (*agentgen.AgentSpec, error) {
	if spec := rt.specCache.get(agentID); spec != nil {
		return spec, nil
	}
	row := rt.pool.QueryRow(ctx,
		`SELECT spec FROM them.agent_runtime_specs WHERE agent_id = $1::uuid`, agentID)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	rt.specCache.set(agentID, &spec)
	return &spec, nil
}

func (rt *Runtime) loadSpecBySlug(ctx context.Context, slug string) (*agentgen.AgentSpec, error) {
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE a.slug = $1 AND a.enabled = true`, slug)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec by slug: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	return &spec, nil
}

func (rt *Runtime) loadBinding(ctx context.Context, appID, agentID, bindingID string) (*agentgen.AppAgentBinding, error) {
	var (
		query string
		args  []any
	)
	if bindingID != "" {
		query = `SELECT id, application_id, agent_id, definition_id,
		          credential_bindings, config_overrides, policies
		          FROM them.app_agent_bindings WHERE id = $1::uuid`
		args = []any{bindingID}
	} else {
		query = `SELECT id, application_id, agent_id, definition_id,
		          credential_bindings, config_overrides, policies
		          FROM them.app_agent_bindings
		          WHERE application_id = $1::uuid AND agent_id = $2::uuid`
		args = []any{appID, agentID}
	}

	row := rt.pool.QueryRow(ctx, query, args...)
	var (
		id, appIDDB, agentIDDB     string
		defID                      *string
		credJSON, cfgJSON, polJSON []byte
	)
	if err := row.Scan(&id, &appIDDB, &agentIDDB, &defID, &credJSON, &cfgJSON, &polJSON); err != nil {
		return nil, fmt.Errorf("load binding: %w", err)
	}

	var creds map[string]string
	_ = json.Unmarshal(credJSON, &creds)
	var overrides map[string]any
	_ = json.Unmarshal(cfgJSON, &overrides)

	return &agentgen.AppAgentBinding{
		ID:                   id,
		ApplicationID:        appIDDB,
		AgentID:              agentIDDB,
		DefinitionID:         defID,
		EncryptedCredentials: creds,
		ConfigOverrides:      overrides,
	}, nil
}

func buildAgentCard(spec *agentgen.AgentSpec) map[string]any {
	skills := make([]map[string]any, len(spec.Skills))
	for i, sk := range spec.Skills {
		skills[i] = map[string]any{
			"id":          sk.ID,
			"name":        sk.Name,
			"description": sk.Description,
			"tags":        sk.Tags,
		}
	}
	return map[string]any{
		"name":        spec.Card.Name,
		"description": spec.Card.Description,
		"version":     spec.Card.Version,
		"url":         fmt.Sprintf("http://them-agent-runtime:9300/agents/%s", spec.Slug),
		"capabilities": map[string]any{
			"streaming":         spec.Card.Capabilities.Streaming,
			"pushNotifications": spec.Card.Capabilities.PushNotifications,
		},
		"skills": skills,
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// anthropicLLMFactory creates AnthropicProviders from internal/llm.
type anthropicLLMFactory struct {
	platformKey string
}

func (f *anthropicLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	if apiKey == "" {
		apiKey = f.platformKey
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key available (no slot bound and no platform key)")
	}
	p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
	return &anthropicProviderAdapter{p: p}, nil
}

// anthropicProviderAdapter adapts llm.AnthropicProvider to agentgen.LLMProvider.
// It calls Provider.Stream with a single user message and collects text deltas.
type anthropicProviderAdapter struct {
	p *llm.AnthropicProvider
}

func (a *anthropicProviderAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msgs := []domain.Message{
		{
			Role:  domain.RoleUser,
			Parts: []domain.ContentPart{{Type: "text", Text: userPrompt}},
		},
	}
	opts := llm.Options{SystemPrompt: systemPrompt}
	ch, err := a.p.Stream(ctx, msgs, nil, opts)
	if err != nil {
		return "", fmt.Errorf("LLM stream start: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Delta)
		case "error":
			return "", fmt.Errorf("LLM stream error: %w", ev.Error)
		}
	}
	return sb.String(), nil
}

var _ agentgen.LLMProvider = (*anthropicProviderAdapter)(nil)
var _ agentgen.LLMFactory = (*anthropicLLMFactory)(nil)
