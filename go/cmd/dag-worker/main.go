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
//	DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER / DATABASE_PASSWORD
//	SECRET_KEY            — HMAC key for AES-GCM credential decryption
//	MCP_SERVICE_URL       — optional; enables mcp_call steps
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
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

	var rlsPools *db.Pools
	if cfg.DBURLApp != "" && cfg.DBURLAdmin != "" {
		rlsPools, err = db.NewPools(ctx, cfg.DBURLApp, cfg.DBURLAdmin)
		if err != nil {
			return fmt.Errorf("startup: rls pools: %w", err)
		}
		defer rlsPools.Close()
		log.Info("RLS pools connected (them_app + them_admin)")
	}
	_ = rlsPools

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
		pool:      database.Pool(),
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
		&pgxAgentEndpointQueryer{pool: database.Pool()},
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

	// ── 11. Block on SIGTERM / SIGINT ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("dag-worker: shutdown signal received — draining worker")
	dagWorker.Stop()
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
type multiLLMFactory struct {
	platformKey string
}

func (f *multiLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for provider %q — set a key in App Runtime", provider)
	}
	switch provider {
	case "anthropic", "":
		p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
		return &anthropicAdapter{p: p}, nil
	default:
		return nil, fmt.Errorf("provider %q is not yet supported in dag-worker; only 'anthropic' is available", provider)
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

var _ agentgen.LLMProvider = (*anthropicAdapter)(nil)
var _ agentgen.LLMFactory = (*multiLLMFactory)(nil)
var _ temporal.ContextLoader = (*dbContextLoader)(nil)

// pgxAgentEndpointQueryer implements agentgen.AgentEndpointQueryer using pgxpool.
// Returns (agent_id, binding_id, endpoint_url, auth_token_encrypted) by joining
// agents → app_agent_bindings → applications to enforce tenant + binding ownership.
// Returns no row when the agent is disabled, not bound to the app, or wrong tenant.
type pgxAgentEndpointQueryer struct {
	pool *pgxpool.Pool
}

type pgxSingleRow struct{ row interface{ Scan(...any) error } }

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
