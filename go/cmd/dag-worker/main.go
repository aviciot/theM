// Package main is the them-dag-worker — the Temporal worker that polls the
// "canvas-dag-nodes" task queue and executes canvas agent DAG steps as
// Temporal activities.
//
// Each activity reconstructs a full InvocationContext (with credentials) from
// the credential-safe ActivityIC by querying PostgreSQL. Secrets are never
// written to Temporal workflow history.
//
// Configuration (env vars):
//
//	TEMPORAL_ENABLED=true (required — worker exits if false)
//	TEMPORAL_HOST_PORT    — Temporal frontend address (default localhost:7233)
//	DAG_WORKER_MAX_CONCURRENT_ACTIVITIES — activity concurrency (default 50)
//	APPFLOW_TASK_QUEUE_OVERRIDE — optional; makes the AppFlow worker poll this
//	                              queue instead of appflow.AppFlowTaskQueue. Set to
//	                              appflow.AppFlowDebugTaskQueue ("appflow-dag-debug")
//	                              on them-dag-worker-debug to run this same image as
//	                              the isolated debug worker pool.
//	DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER / DATABASE_PASSWORD
//	SECRET_KEY            — HMAC key for AES-GCM credential decryption
//	MCP_SERVICE_URL       — optional; enables mcp_call steps
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	temporalactivity "go.temporal.io/sdk/activity"
	temporalworker "go.temporal.io/sdk/worker"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/debugcred"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/llmresolve"
	"github.com/aviciot/them/internal/telemetry"
	"github.com/aviciot/them/internal/temporal"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ── 1. Load and validate configuration ───────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 2. Set up structured logger ───────────────────────────────────────────
	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger
	log.Info("dag-worker: configuration loaded", "config", cfg.SafeString())

	// ── 3. Require Temporal enabled ───────────────────────────────────────────
	if !cfg.TemporalEnabled {
		return fmt.Errorf("dag-worker requires TEMPORAL_ENABLED=true — worker cannot run without Temporal")
	}

	// ── 4. Connect to PostgreSQL ──────────────────────────────────────────────
	ctx := context.Background()
	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("startup: postgres: %w", err)
	}
	defer database.Close()
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	rlsPools, err := db.NewPools(ctx, cfg.DBURLApp, cfg.DBURLAdmin)
	if err != nil {
		return fmt.Errorf("startup: rls pools: %w", err)
	}
	defer rlsPools.Close()
	log.Info("RLS pools connected (them_app + them_admin)")

	// ── 5. Connect to Redis (not directly used by activities but needed for
	//       any future cache lookups; connection validates network) ────────────
	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return fmt.Errorf("startup: redis: %w", err)
	}
	_ = redisCache // reserved for future cache integration
	log.Info("redis connected", "addr", cfg.RedisAddr())

	// ── 6. Connect Temporal client ────────────────────────────────────────────
	temporalCli, err := temporal.Connect(cfg.TemporalHostPort, log)
	if err != nil {
		return fmt.Errorf("startup: temporal: %w", err)
	}
	defer temporalCli.Close()
	log.Info("Temporal client connected", "host_port", cfg.TemporalHostPort)

	// ── 7. Build InvocationContext loader ─────────────────────────────────────
	cryptoKey := crypto.DeriveKey(cfg.SecretKey)
	loader := &dbContextLoader{
		pool:      rlsPools.Admin,
		cryptoKey: cryptoKey,
		logger:    log,
	}

	// ── 8. Build Interpreter template ────────────────────────────────────────
	interpTemplate := agentgen.NewInterpreter(
		&http.Client{Timeout: 60 * time.Second},
		&multiLLMFactory{platformKey: cfg.AnthropicAPIKey},
		cfg.AnthropicAPIKey,
	)
	if cfg.MCPServiceURL != "" {
		interpTemplate.WithMCPCaller(agentgen.NewHTTPMCPCaller(cfg.MCPServiceURL, &http.Client{Timeout: 30 * time.Second}))
	}

	// A2A inter-agent call support: resolve target endpoint from DB, decrypt auth token.
	a2aResolver := agentgen.NewDBAgentEndpointResolver(
		&pgxAgentEndpointQueryer{pool: rlsPools.Admin},
		func(ct string) (string, error) { return crypto.DecryptStored(cryptoKey, ct) },
	)
	interpTemplate.WithA2ACaller(agentgen.NewHTTPA2ACaller(a2aResolver, &http.Client{Timeout: 5 * time.Minute}))

	// ── 9. Create CanvasAgentActivities ───────────────────────────────────────
	acts := &temporal.CanvasAgentActivities{
		InterpTemplate: interpTemplate,
		Loader:         loader,
	}

	// ── 10. Create and register Temporal worker on canvas-dag-nodes ───────────
	dagWorker := temporalworker.New(temporalCli, temporal.CanvasDAGTaskQueue, temporalworker.Options{
		MaxConcurrentActivityExecutionSize: cfg.DAGWorkerMaxConcurrentActivities,
	})
	dagWorker.RegisterWorkflow(temporal.CanvasAgentWorkflow)
	dagWorker.RegisterActivityWithOptions(acts.ExecuteStepActivity, temporalactivity.RegisterOptions{
		Name: temporal.CanvasExecuteStepActivityName,
	})

	if err := dagWorker.Start(); err != nil {
		return fmt.Errorf("startup: dag temporal worker: %w", err)
	}
	log.Info("dag-worker polling",
		"task_queue", temporal.CanvasDAGTaskQueue,
		"max_concurrent_activities", cfg.DAGWorkerMaxConcurrentActivities,
	)

	// ── 10b. AppFlow worker — polls appflow-dag task queue ────────────────────
	debugCredStore := debugcred.New(redisCache.Client())
	llmCaller := &dbLLMCaller{
		resolver:   llmresolve.New(rlsPools.Admin, cryptoKey, log),
		factory:    &multiLLMFactory{platformKey: cfg.AnthropicAPIKey},
		logger:     log,
		debugStore: debugCredStore,
	}
	statusUpdater := &pgxRunStatusUpdater{pool: rlsPools.Admin}
	streamPub := cache.NewRunStreamerWriterRedisClient(redisCache.Client())
	agentCaller := &pgxAgentA2ACaller{
		pool:       rlsPools.Admin,
		cryptoKey:  cryptoKey,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
	appFlowActs := &appflow.AppFlowActivities{
		LLMCaller:        llmCaller,
		InlineLLM:        llmCaller,
		DB:               rlsPools.Admin,
		StatusUpdater:    statusUpdater,
		StreamPub:        streamPub,
		AgentInvoker:     agentCaller,
		DebugCredCleaner: debugCredStore,
	}
	appFlowTaskQueue := appflow.AppFlowTaskQueue
	if cfg.AppFlowTaskQueueOverride != "" {
		appFlowTaskQueue = cfg.AppFlowTaskQueueOverride
	}
	appFlowWorker := temporalworker.New(temporalCli, appFlowTaskQueue, temporalworker.Options{
		MaxConcurrentActivityExecutionSize: cfg.DAGWorkerMaxConcurrentActivities,
	})
	appFlowWorker.RegisterWorkflow(appflow.AppFlowWorkflow)
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.ExecuteRouterActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowExecuteRouterActivityName,
	})
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.ExecuteHILActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowExecuteHILActivityName,
	})
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.FinalizeRunActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowFinalizeRunActivityName,
	})
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.InvokeAgentActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowInvokeAgentActivityName,
	})
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.InlineLLMActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowInlineLLMActivityName,
	})
	appFlowWorker.RegisterActivityWithOptions(appFlowActs.TraceNodeEventActivity, temporalactivity.RegisterOptions{
		Name: appflow.AppFlowTraceNodeEventActivityName,
	})
	if err := appFlowWorker.Start(); err != nil {
		return fmt.Errorf("startup: appflow temporal worker: %w", err)
	}
	log.Info("appflow-worker polling", "task_queue", appFlowTaskQueue)

	// ── 11. Block on SIGTERM / SIGINT ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("dag-worker: shutdown signal received — draining workers")
	dagWorker.Stop()
	appFlowWorker.Stop()
	log.Info("dag-worker stopped")
	return nil
}

// ── dbContextLoader ───────────────────────────────────────────────────────────

// dbContextLoader reconstructs a full InvocationContext from the credential-safe
// ActivityIC by querying PostgreSQL. It scopes every query by all four IDs to
// prevent cross-tenant leakage.
type dbContextLoader struct {
	pool      *pgxpool.Pool
	cryptoKey []byte
	logger    *slog.Logger
}

func (l *dbContextLoader) Load(ctx context.Context, ic agentgen.ActivityIC) (*agentgen.InvocationContext, error) {
	if err := ic.Validate(); err != nil {
		return nil, fmt.Errorf("dbContextLoader.Load: %w", err)
	}

	full := &agentgen.InvocationContext{
		TenantID:      ic.TenantID,
		ApplicationID: ic.ApplicationID,
		AgentID:       ic.AgentID,
		BindingID:     ic.BindingID,
		A2ACallDepth:  ic.A2ACallDepth,
	}

	// Load AgentSpec from agent_runtime_specs (scoped by agent_id AND tenant_id).
	spec, err := l.loadSpec(ctx, ic.TenantID, ic.AgentID)
	if err != nil {
		return nil, fmt.Errorf("dbContextLoader.Load: spec: %w", err)
	}
	full.Spec = spec

	// Load per-app provider keys (for LLM steps), scoped by tenant.
	full.AppAPIKey = l.loadAppAPIKey(ctx, ic.TenantID, ic.ApplicationID)

	// Load app-level global params, scoped by tenant.
	full.AppGlobalParams = l.loadAppGlobalParams(ctx, ic.TenantID, ic.ApplicationID)

	// Load binding — scoped by all four IDs.
	if ic.BindingID != "" {
		agentParams, configOverrides, policies, err := l.loadBinding(ctx, ic)
		if err != nil {
			return nil, fmt.Errorf("dbContextLoader.Load: binding: %w", err)
		}
		full.AgentParams = l.resolveAgentParams(agentParams, spec.RequiredParams)
		full.NodeLLMOverrides = extractNodeLLMOverrides(configOverrides)
		full.Policies = policies
	} else {
		full.AgentParams = map[string]string{}
		full.NodeLLMOverrides = map[string]agentgen.NodeLLMOverride{}
	}

	return full, nil
}

func (l *dbContextLoader) loadSpec(ctx context.Context, tenantID, agentID string) (*agentgen.AgentSpec, error) {
	row := l.pool.QueryRow(ctx,
		`SELECT spec FROM them.agent_runtime_specs
		  WHERE agent_id = $1::uuid AND tenant_id = $2::uuid`,
		agentID, tenantID)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("query spec: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	return &spec, nil
}

func (l *dbContextLoader) loadAppAPIKey(ctx context.Context, tenantID, appID string) map[string]string {
	row := l.pool.QueryRow(ctx,
		`SELECT COALESCE(provider_keys, '{}') FROM them.applications
		  WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}

	type entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var structured map[string]entry
	if err := json.Unmarshal(raw, &structured); err == nil {
		out := make(map[string]string, len(structured))
		for provider, e := range structured {
			if e.CT == "" && e.Hint == "" {
				continue
			}
			if len(e.CT) > 6 && e.CT[:6] == "plain:" {
				out[provider] = e.CT[6:]
				continue
			}
			plain, err := crypto.DecryptStored(l.cryptoKey, e.CT)
			if err != nil {
				l.logger.Warn("dag-worker: provider key decryption failed",
					"app_id", appID, "provider", provider)
				continue
			}
			out[provider] = plain
		}
		if len(out) > 0 {
			return out
		}
	}
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(flat))
	for provider, v := range flat {
		if v != "" {
			out[provider] = v
		}
	}
	return out
}

func (l *dbContextLoader) loadAppGlobalParams(ctx context.Context, tenantID, appID string) map[string]string {
	row := l.pool.QueryRow(ctx,
		`SELECT COALESCE(app_params, '{}') FROM them.applications
		  WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}

	type secretEntry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(top))
	for name, valRaw := range top {
		var entry secretEntry
		if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
			if len(entry.CT) > 6 && entry.CT[:6] == "plain:" {
				out[name] = entry.CT[6:]
				continue
			}
			plain, err := crypto.DecryptStored(l.cryptoKey, entry.CT)
			if err != nil {
				l.logger.Warn("dag-worker: app global param decryption failed",
					"app_id", appID, "name", name)
				continue
			}
			out[name] = plain
			continue
		}
		var s string
		if json.Unmarshal(valRaw, &s) == nil && s != "" {
			out[name] = s
		}
	}
	return out
}

// loadBinding queries app_agent_bindings scoped by all four IDs and returns
// the raw agent_params JSON, config_overrides map, and resolved policies.
func (l *dbContextLoader) loadBinding(ctx context.Context, ic agentgen.ActivityIC) ([]byte, map[string]any, agentgen.InvocationPolicies, error) {
	row := l.pool.QueryRow(ctx,
		`SELECT COALESCE(b.agent_params, '{}'), b.config_overrides, b.policies
		   FROM them.app_agent_bindings b
		   JOIN them.applications a ON a.id = b.application_id
		  WHERE b.id = $1::uuid
		    AND b.application_id = $2::uuid
		    AND b.agent_id = $3::uuid
		    AND a.tenant_id = $4::uuid`,
		ic.BindingID, ic.ApplicationID, ic.AgentID, ic.TenantID,
	)
	var agentParamsJSON, cfgJSON, polJSON []byte
	if err := row.Scan(&agentParamsJSON, &cfgJSON, &polJSON); err != nil {
		return nil, nil, agentgen.InvocationPolicies{}, fmt.Errorf("query binding: %w", err)
	}
	var overrides map[string]any
	_ = json.Unmarshal(cfgJSON, &overrides)
	var policies agentgen.InvocationPolicies
	_ = json.Unmarshal(polJSON, &policies)
	return agentParamsJSON, overrides, policies, nil
}

// resolveAgentParams decrypts secret-type params from stored JSON.
func (l *dbContextLoader) resolveAgentParams(raw []byte, decls []agentgen.AgentParamSpec) map[string]string {
	out := make(map[string]string, len(decls))
	if len(decls) == 0 {
		return out
	}
	var stored map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &stored)
	}

	type secretEntry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	for _, decl := range decls {
		rawVal, exists := stored[decl.Key]
		if !exists {
			if decl.DefaultValue != "" {
				out[decl.Key] = decl.DefaultValue
			}
			continue
		}
		if decl.Type == "secret" {
			var entry secretEntry
			if json.Unmarshal(rawVal, &entry) == nil && entry.CT != "" {
				plain, err := crypto.DecryptStored(l.cryptoKey, entry.CT)
				if err != nil {
					l.logger.Warn("dag-worker: agent param decryption failed", "key", decl.Key)
					continue
				}
				out[decl.Key] = plain
			}
		} else {
			var s string
			if json.Unmarshal(rawVal, &s) == nil {
				out[decl.Key] = s
			}
		}
	}
	return out
}

// extractNodeLLMOverrides reads llm_nodes from config_overrides.
func extractNodeLLMOverrides(overrides map[string]any) map[string]agentgen.NodeLLMOverride {
	out := make(map[string]agentgen.NodeLLMOverride)
	if overrides == nil {
		return out
	}
	raw, ok := overrides["llm_nodes"]
	if !ok {
		return out
	}
	nodes, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for nodeID, v := range nodes {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := m["provider"].(string)
		model, _ := m["model"].(string)
		if provider != "" || model != "" {
			out[nodeID] = agentgen.NodeLLMOverride{Provider: provider, Model: model}
		}
	}
	return out
}

// ── multiLLMFactory ───────────────────────────────────────────────────────────

// multiLLMFactory routes to the correct provider implementation.
// baseURLs maps provider name → custom endpoint URL (from them.llm_providers.base_url).
type multiLLMFactory struct {
	platformKey string
	baseURLs    map[string]string
}

func (f *multiLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	baseURL := ""
	if f.baseURLs != nil {
		baseURL = f.baseURLs[provider]
	}
	return f.newProvider(provider, model, maxTokens, apiKey, baseURL)
}

// NewProviderWithBaseURL is NewProvider but with an explicit baseURL that
// overrides f.baseURLs' static per-provider map — used for App Canvas Debug
// Mode's per-node credential override (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md),
// where a debug run may point a node at a different endpoint than the one
// configured in them.llm_providers.base_url. An empty baseURL falls back to
// the static map, same as NewProvider.
func (f *multiLLMFactory) NewProviderWithBaseURL(provider, model string, maxTokens int, apiKey, baseURL string) (agentgen.LLMProvider, error) {
	if baseURL == "" && f.baseURLs != nil {
		baseURL = f.baseURLs[provider]
	}
	return f.newProvider(provider, model, maxTokens, apiKey, baseURL)
}

// newProvider builds the provider adapter. NOTE: llm.AnthropicProvider has no
// base URL parameter at all in this codebase — a baseURL override for the
// "anthropic" case is silently unusable structurally (not just unwired), a
// pre-existing limitation this function does not attempt to fix. It only
// matters for the "openai"/"groq"/"ollama"/"vllm"/"lmstudio" family, where
// base_url is the whole point of those provider names existing separately.
func (f *multiLLMFactory) newProvider(provider, model string, maxTokens int, apiKey, baseURL string) (agentgen.LLMProvider, error) {
	if apiKey == "" && provider != "ollama" && provider != "mock" {
		return nil, fmt.Errorf("no API key configured for provider %q — set a key in App Runtime", provider)
	}
	switch provider {
	case "mock":
		return &mockAdapter{}, nil
	case "anthropic", "":
		p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
		return &anthropicAdapter{p: p}, nil
	case "openai", "groq", "ollama", "vllm", "lmstudio":
		p := llm.NewOpenAIProvider(apiKey, model, baseURL, maxTokens)
		return &openAIAdapter{p: p}, nil
	default:
		return nil, fmt.Errorf("provider %q is not supported — use anthropic, openai, groq, ollama, vllm, lmstudio, or mock", provider)
	}
}

// anthropicAdapter adapts llm.AnthropicProvider to agentgen.LLMProvider.
type anthropicAdapter struct {
	p *llm.AnthropicProvider
}

func (a *anthropicAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
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

// openAIAdapter adapts llm.OpenAIProvider to agentgen.LLMProvider.
type openAIAdapter struct {
	p *llm.OpenAIProvider
}

func (a *openAIAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
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

// mockAdapter is a zero-cost LLM stub for testing. Returns a canned response
// after a short random delay (50–500ms) without calling any external API.
type mockAdapter struct{}

var mockReplies = []string{
	"This is a mock response. The real LLM provider is not configured for this environment.",
	"Mock agent here. I'm simulating a response with artificial latency.",
	"[MOCK] Acknowledged. In production this would be a real LLM response.",
	"Test response from the mock provider. No tokens were consumed.",
	"Hello! I am the mock LLM. This response was generated locally at zero cost.",
}

func (m *mockAdapter) Complete(ctx context.Context, _, _ string) (string, error) {
	delay := time.Duration(50+rand.Intn(450)) * time.Millisecond
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return mockReplies[rand.Intn(len(mockReplies))], nil
}

var _ agentgen.LLMProvider = (*anthropicAdapter)(nil)
var _ agentgen.LLMProvider = (*openAIAdapter)(nil)
var _ agentgen.LLMProvider = (*mockAdapter)(nil)
var _ agentgen.LLMFactory = (*multiLLMFactory)(nil)
var _ temporal.ContextLoader = (*dbContextLoader)(nil)

// pgxAgentEndpointQueryer implements agentgen.AgentEndpointQueryer using pgxpool.
// Returns (agent_id, binding_id, endpoint_url, auth_token_encrypted) by joining
// agents → app_agent_bindings → applications to enforce tenant + binding ownership.
// Returns no row when the agent is disabled, not bound to the app, or wrong tenant.
type pgxAgentEndpointQueryer struct {
	pool *pgxpool.Pool
}

type pgxSingleRow struct {
	row interface{ Scan(...any) error }
}

func (r pgxSingleRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

func (q *pgxAgentEndpointQueryer) QueryAgentEndpoint(ctx context.Context, tenantID, applicationID, agentSlug string) agentgen.AgentEndpointRow {
	row := q.pool.QueryRow(ctx,
		`SELECT a.id::text, b.id::text,
		        COALESCE(a.endpoint_url,''), COALESCE(a.auth_token_encrypted,'')
		   FROM them.agents a
		   JOIN them.app_agent_bindings b ON b.agent_id = a.id
		   JOIN them.applications app     ON app.id = b.application_id
		  WHERE a.slug           = $1
		    AND b.application_id = $2::uuid
		    AND app.tenant_id    = $3::uuid
		    AND a.enabled        = true`,
		agentSlug, applicationID, tenantID)
	return pgxSingleRow{row: row}
}

var _ agentgen.AgentEndpointQueryer = (*pgxAgentEndpointQueryer)(nil)

// ── dbLLMCaller ───────────────────────────────────────────────────────────────

// debugCredentialGetter is the minimal debugcred.Store surface dbLLMCaller
// needs — defined here (not the concrete *debugcred.Store) so tests can
// inject a fake without a live Redis.
type debugCredentialGetter interface {
	Get(ctx context.Context, tenantID, runID, nodeID string) (debugcred.Override, bool, error)
}

// dbLLMCaller implements both appflow.RouterLLMCaller (label classification for
// the Router node) and appflow.InlineLLMCaller (direct completion for the
// inline LLM node). It resolves the API key from the DB at activity execution
// time via the shared internal/llmresolve precedence chain (app-level
// provider_keys -> tenant llm_providers -> platform llm_providers) so the key
// never appears in Temporal workflow history.
//
// For debug runs (req.Debug == true, InlineLLMCaller.Complete only —
// ClassifyIntent/the Router node has no debug-override concept), Complete
// checks debugStore first — see docs/APPFLOW_RUNTIME_PARAMS_PLAN.md. debugStore
// may be nil (debug credential overrides not configured); a nil store and a
// non-debug call both skip the override check identically.
type dbLLMCaller struct {
	resolver   *llmresolve.Resolver
	factory    *multiLLMFactory
	logger     *slog.Logger
	debugStore debugCredentialGetter
}

func (c *dbLLMCaller) ClassifyIntent(
	ctx context.Context,
	userMessage, systemPrompt string,
	labels []string,
	providerName, model, tenantID, applicationID string,
) (string, error) {
	apiKey := c.resolveKey(ctx, providerName, tenantID, applicationID)
	if apiKey == "" {
		return "", fmt.Errorf("router: no API key for provider %q (tenant %s, app %s)", providerName, tenantID, applicationID)
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001" // lean classification model
	}

	provider, err := c.factory.NewProvider(providerName, model, 64, apiKey)
	if err != nil {
		return "", fmt.Errorf("router: create provider: %w", err)
	}

	userPrompt := fmt.Sprintf("User message: %s\n\nChoose one label from: %v", userMessage, labels)
	return provider.Complete(ctx, systemPrompt, userPrompt)
}

// resolveKey looks up the API key for providerName via the shared
// internal/llmresolve precedence chain (app-level provider_keys -> tenant
// llm_providers -> platform llm_providers). Errors are logged and treated as
// "no key found" — the caller (multiLLMFactory.NewProvider) already errors
// clearly for any provider that needs a key and didn't get one.
func (c *dbLLMCaller) resolveKey(ctx context.Context, providerName, tenantID, applicationID string) string {
	resolved, err := c.resolver.ResolveProvider(ctx, applicationID, tenantID, providerName)
	if err != nil {
		c.logger.Warn("router: provider key resolution failed", "provider", providerName, "tenant_id", tenantID, "app_id", applicationID, "error", err)
		return ""
	}
	return resolved.Key
}

// Complete implements appflow.InlineLLMCaller for the inline LLM node. It
// reuses the same resolveKey chain and multiLLMFactory as ClassifyIntent —
// no second key-resolution path.
//
// NOTE on Temperature: req.Temperature is accepted but NOT applied here.
// agentgen.LLMProvider.Complete(ctx, systemPrompt, userPrompt) has no options
// parameter, so there is nowhere to thread it through without changing the
// shared LLMProvider interface (used by the agent builder's interpreter and
// its whole test suite). That's out of scope for this slice — see plan
// docs/INLINE_NODES_PLAN.md §11 item 3 for the Phase 2 deferral.
func (c *dbLLMCaller) Complete(ctx context.Context, req appflow.InlineLLMRequest) (string, error) {
	providerName := req.ProviderName
	if providerName == "" {
		providerName = "anthropic"
	}
	model := req.Model
	var apiKey string
	var baseURL string // debug-run override only; "" uses multiLLMFactory's static per-provider map

	if req.Debug {
		if c.debugStore == nil {
			// A debug run always requires debugStore to be configured — this
			// is a server misconfiguration, not "no override was requested,"
			// so it must fail the same way a missing override does, never
			// fall through to normal (tenant/platform) key resolution.
			return "", fmt.Errorf("inline llm %q: debug run but no debug credential store configured on this worker", req.NodeID)
		}
		ov, found, err := c.debugStore.Get(ctx, req.TenantID, req.RunID, req.NodeID)
		if err != nil {
			// Fail loudly on a lookup error too — this is a debug run, and a
			// Redis error here is exactly the kind of "credential became
			// unavailable" case that must not silently fall through to
			// normal (tenant/platform) key resolution.
			return "", fmt.Errorf("inline llm %q: debug credential lookup failed: %w", req.NodeID, err)
		}
		if !found {
			// Absence-means-missing for a debug run (see debugcred package
			// doc): the debug/start request declared this node's llm_credential
			// param, so an override entry was written for every such node —
			// its absence now (TTL expired, evicted, etc.) means the
			// credential is genuinely unavailable, never "no override was
			// ever requested." Fail clearly rather than resolve a different
			// key the user never selected for this run.
			return "", fmt.Errorf("inline llm %q: debug credential unavailable for this run (expired or evicted) — retry the debug run", req.NodeID)
		}
		providerName = ov.Provider
		if ov.Model != "" {
			model = ov.Model
		}
		apiKey = ov.APIKey
		baseURL = ov.BaseURL
	} else {
		// apiKey may legitimately be "" here (e.g. ollama, mock) — multiLLMFactory.
		// NewProvider is the single source of truth for which providers require a
		// key, so the empty-key check is not duplicated here; NewProvider errors
		// for any provider that needs one and didn't get one.
		apiKey = c.resolveKey(ctx, providerName, req.TenantID, req.ApplicationID)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	provider, err := c.factory.NewProviderWithBaseURL(providerName, model, maxTokens, apiKey, baseURL)
	if err != nil {
		return "", fmt.Errorf("inline llm: create provider: %w", err)
	}

	responseText, err := provider.Complete(ctx, req.SystemPrompt, req.UserPrompt)
	if err != nil {
		return "", fmt.Errorf("inline llm: complete: %w", err)
	}
	return responseText, nil
}

var _ appflow.RouterLLMCaller = (*dbLLMCaller)(nil)
var _ appflow.InlineLLMCaller = (*dbLLMCaller)(nil)

// ── pgxAgentA2ACaller ─────────────────────────────────────────────────────────

// pgxAgentA2ACaller implements appflow.AgentInvoker. It resolves the agent
// endpoint URL by agent UUID from them.agents, then calls it via A2A HTTP POST.
// The agent auth token is decrypted with the platform crypto key.
type pgxAgentA2ACaller struct {
	pool       *pgxpool.Pool
	cryptoKey  []byte
	httpClient *http.Client
}

// InvokeByID calls the A2A agent identified by agentID via A2A v1.0 SendMessage.
// Uses protobuf-JSON wire format with "ROLE_USER" enum string and A2A-Version: 1.0
// header required by the A2A SDK v1.1 version validator.
func (c *pgxAgentA2ACaller) InvokeByID(ctx context.Context, tenantID, applicationID, agentID, userMessage string) (string, error) {
	// Resolve agent endpoint + auth token from DB (scoped by tenant for security).
	row := c.pool.QueryRow(ctx,
		`SELECT COALESCE(endpoint_url,''), COALESCE(auth_token_encrypted,'')
		   FROM them.agents
		  WHERE id = $1::uuid AND tenant_id = $2::uuid AND enabled = true`,
		agentID, tenantID)
	var endpointURL, authTokenEnc string
	if err := row.Scan(&endpointURL, &authTokenEnc); err != nil {
		return "", fmt.Errorf("agentA2ACaller: resolve agent %s: %w", agentID, err)
	}
	if endpointURL == "" {
		return "", fmt.Errorf("agentA2ACaller: agent %s has no endpoint_url", agentID)
	}

	authToken := ""
	if authTokenEnc != "" {
		if plain, err := crypto.DecryptStored(c.cryptoKey, authTokenEnc); err == nil {
			authToken = plain
		}
	}

	// Build A2A v1.0 JSON-RPC "SendMessage" request (protobuf-JSON wire format).
	// Role must be the protobuf enum string "ROLE_USER"; parts use only "text" (no "kind").
	msgID := fmt.Sprintf("%d", rand.Int63())
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "SendMessage",
		"id":      msgID,
		"params": map[string]any{
			"message": map[string]any{
				"messageId": msgID,
				"role":      "ROLE_USER",
				"parts":     []map[string]any{{"text": userMessage}},
			},
			"configuration": map[string]any{"returnImmediately": false},
		},
	})
	if err != nil {
		return "", fmt.Errorf("agentA2ACaller: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", fmt.Errorf("agentA2ACaller: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	req.Header.Set("X-Them-Application-Id", applicationID)
	req.Header.Set("X-Them-Tenant-Id", tenantID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("agentA2ACaller: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agentA2ACaller: agent returned HTTP %d", resp.StatusCode)
	}

	// Parse A2A v1.0 JSON-RPC response.
	// Non-streaming SendMessage returns: result.task.artifacts[].parts[].text
	var rpcResp struct {
		Result *struct {
			Task *struct {
				Artifacts []struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"artifacts"`
			} `json:"task"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("agentA2ACaller: decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("agentA2ACaller: agent error: %s", rpcResp.Error.Message)
	}

	// Extract text from first artifact/part.
	if rpcResp.Result != nil && rpcResp.Result.Task != nil {
		for _, artifact := range rpcResp.Result.Task.Artifacts {
			for _, p := range artifact.Parts {
				if p.Text != "" {
					return p.Text, nil
				}
			}
		}
	}
	return "", nil
}

var _ appflow.AgentInvoker = (*pgxAgentA2ACaller)(nil)

// pgxRunStatusUpdater implements appflow.RunStatusUpdater using pgxpool.
type pgxRunStatusUpdater struct {
	pool *pgxpool.Pool
}

func (u *pgxRunStatusUpdater) UpdateRunStatus(ctx context.Context, runID string, status domain.RunStatus, errMsg string) error {
	terminal := status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCanceled
	var q string
	if terminal {
		// Idempotency guard: skip if run is already in a terminal state.
		// This prevents a retried FinalizeRunActivity from overwriting a terminal status
		// that was written by a previous attempt.
		q = `UPDATE them.runs SET status=$2, error=NULLIF($3,''), ended_at=now()
		      WHERE id=$1::uuid
		        AND status NOT IN ('completed','failed','canceled')`
	} else {
		q = `UPDATE them.runs SET status=$2, error=NULLIF($3,'') WHERE id=$1::uuid`
	}
	_, err := u.pool.Exec(ctx, q, runID, string(status), errMsg)
	if err != nil {
		return fmt.Errorf("pgxRunStatusUpdater: update run %s: %w", runID, err)
	}
	return nil
}

var _ appflow.RunStatusUpdater = (*pgxRunStatusUpdater)(nil)
