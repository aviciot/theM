// Package workerconfig provides per-run orchestrator configuration loading
// for the Go Temporal worker. It resolves an AppOrchestratorID UUID to a
// fully populated orchestrator.Config and a plaintext LLM API key, reading
// directly from PostgreSQL — no in-process cache (each run gets fresh config).
//
// Tenant safety: the query joins app_orchestrators → applications to enforce
// that the requested orchestrator belongs to the expected application. The
// application is already scoped to a tenant by the epconfig layer upstream.
//
// AllowedAgents resolution: allowed_agent_ids on app_orchestrators stores
// component_definition UUIDs which equal agents.id (Option C FK). A secondary
// query resolves those UUIDs to agent slugs for the orchestrator tool list.
package workerconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/llmresolve"
	"github.com/aviciot/them/internal/orchestrator"
)

// ErrNoProviderKey is returned when the worker config loader cannot resolve an API key
// for the configured LLM provider. It means the tenant has not set up their keys.
var ErrNoProviderKey = errors.New("no provider key configured")

// RunConfig holds everything the worker needs to execute one orchestration run.
type RunConfig struct {
	// OrchestratorConfig is the per-run orchestrator.Config resolved from DB.
	OrchestratorConfig orchestrator.Config
	// LLMProvider is the resolved provider name ("anthropic", "openai", etc.).
	// Empty string means fall back to the global env-var key.
	LLMProvider string
	// LLMAPIKey is the plaintext API key for LLMProvider, read from
	// applications.provider_keys. Empty string means fall back to global key.
	LLMAPIKey string
	// LLMBaseURL is the custom endpoint URL for the LLM provider, read from
	// them.llm_providers.base_url. Empty = use the provider's public default.
	// Set this for local/self-hosted models (Ollama, vLLM, LMStudio, etc.).
	LLMBaseURL string
	// LLMPricing is the model->per-token USD rate table for LLMProvider, read
	// from the same them.llm_providers row as LLMBaseURL (tenant row wins over
	// platform row). Empty map means no DB pricing was found for this
	// provider — callers fall back to a built-in default rate card.
	LLMPricing llmresolve.PricingTable

	// Summarizer fields — populated when memory_enabled=true on the entry_point row.
	// Memory config is per-EP so each entry point can have independent history settings.
	SummarizerProvider string
	SummarizerModel    string
	SummarizerAPIKey   string // plaintext, decrypted from provider_keys
	SummarizerBaseURL  string // custom endpoint URL for summarizer provider (mirrors LLMBaseURL)

	// MCPServiceURL is the internal base URL of them-mcp-service, injected by the
	// worker at build time (not stored in DB). Empty → MCP tool dispatch disabled.
	MCPServiceURL string
}

// Loader resolves per-run orchestrator config from persistent storage.
// Tests inject a fake; production uses PgxLoader.
type Loader interface {
	// LoadRunConfig returns the RunConfig for the given AppOrchestratorID.
	// ApplicationID is used for provider key lookup and as a cross-check.
	// EntryPointID is used to load per-EP memory/history configuration.
	// TenantID is used to look up the managed app binding when app_type='managed'.
	LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID, entryPointID, tenantID string) (RunConfig, error)
}

// PgxLoader implements Loader against a live PostgreSQL pool.
type PgxLoader struct {
	pool     *pgxpool.Pool
	resolver *llmresolve.Resolver
}

// NewPgxLoader creates a PgxLoader backed by the given connection pool.
// fernetKey is the 32-byte key used to decrypt provider_keys values; derive it
// with crypto.DeriveKey(cfg.SecretKey).
func NewPgxLoader(pool *pgxpool.Pool, fernetKey []byte) *PgxLoader {
	return &PgxLoader{pool: pool, resolver: llmresolve.New(pool, fernetKey, nil)}
}

// LoadRunConfig resolves orchestrator config + provider key for one run.
// appOrchestratorID + applicationID load the LLM/agent/loop config.
// entryPointID loads the per-EP memory/history config (may be empty — disables memory).
// tenantID is used to look up the managed app binding (may be empty — skips lookup).
func (l *PgxLoader) LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID, entryPointID, tenantID string) (RunConfig, error) {
	const orchQ = `
SELECT
    ao.system_prompt,
    ao.llm_provider,
    ao.llm_model,
    ao.max_iterations,
    ao.max_parallel_tools,
    ao.budget_tokens,
    ao.allowed_agent_ids,
    COALESCE(ao.mcp_servers, '[]'::jsonb)
FROM them.app_orchestrators ao
JOIN them.applications a ON a.id = ao.application_id
WHERE ao.id = $1::uuid
  AND ao.application_id = $2::uuid
  AND ao.enabled = true`

	row := l.pool.QueryRow(ctx, orchQ, appOrchestratorID, applicationID)

	var (
		systemPrompt     *string
		llmProvider      *string
		llmModel         *string
		maxIterations    int
		maxParallelTools int
		budgetTokens     *int
		allowedAgentIDs  []string
		mcpServersRaw    []byte
	)

	if err := row.Scan(
		&systemPrompt,
		&llmProvider,
		&llmModel,
		&maxIterations,
		&maxParallelTools,
		&budgetTokens,
		&allowedAgentIDs,
		&mcpServersRaw,
	); err != nil {
		return RunConfig{}, fmt.Errorf("workerconfig: load orchestrator %s: %w", appOrchestratorID, err)
	}

	// Load per-EP memory config (only when entryPointID is provided).
	var (
		memoryEnabled      bool
		historyWindow      = 20
		summarizeEveryN    int
		rawFallbackN       = 3
		summarizerProvider *string
		summarizerModel    *string
	)
	var epLLMProvider *string
	var epLLMModel *string
	if entryPointID != "" {
		const epQ = `
SELECT
    COALESCE(ep.memory_enabled, false),
    COALESCE(ep.history_window, 20),
    COALESCE(ep.summarize_every_n_calls, 0),
    COALESCE(ep.memory_raw_fallback_n, 3),
    ep.summarizer_provider,
    ep.summarizer_model,
    ep.llm_provider,
    ep.llm_model
FROM them.entry_points ep
WHERE ep.id = $1::uuid`
		epRow := l.pool.QueryRow(ctx, epQ, entryPointID)
		if err := epRow.Scan(
			&memoryEnabled,
			&historyWindow,
			&summarizeEveryN,
			&rawFallbackN,
			&summarizerProvider,
			&summarizerModel,
			&epLLMProvider,
			&epLLMModel,
		); err != nil {
			// Non-fatal: EP not found or missing columns — proceed without memory.
			memoryEnabled = false
		}
	}

	// Resolve allowed_agent_ids (component_definition UUIDs = agents.id) → slugs.
	slugs, err := l.resolveAgentSlugs(ctx, allowedAgentIDs)
	if err != nil {
		slugs = nil
	}

	// Resolve MCP server attachments: parse mcp_servers JSONB, fetch manifests from DB.
	mcpAttachments, err := l.resolveMCPServers(ctx, mcpServersRaw)
	if err != nil {
		// Non-fatal: log and proceed without MCP tools.
		slog.Warn("workerconfig: failed to resolve MCP servers — MCP tools disabled for run",
			"app_orchestrator_id", appOrchestratorID, "error", err)
		mcpAttachments = nil
	}
	totalMCPTools := 0
	for _, a := range mcpAttachments {
		totalMCPTools += len(a.ToolDefs)
	}
	slog.Info("workerconfig: MCP servers resolved",
		"app_orchestrator_id", appOrchestratorID,
		"server_count", len(mcpAttachments),
		"total_tools", totalMCPTools)

	cfg := orchestrator.Config{
		MaxIterations:        maxIterations,
		MaxParallelTools:     maxParallelTools,
		HistoryWindow:        historyWindow,
		AllowedAgents:        slugs,
		MemoryEnabled:        memoryEnabled,
		SummarizeEveryNCalls: summarizeEveryN,
		MemoryRawFallbackN:   rawFallbackN,
		MCPServers:           mcpAttachments,
	}
	if systemPrompt != nil {
		cfg.SystemPrompt = *systemPrompt
	}
	// Orchestrator-level LLM config; fall back to EP-level if not set on the orchestrator.
	if llmModel != nil {
		cfg.Model = *llmModel
	} else if epLLMModel != nil {
		cfg.Model = *epLLMModel
	}

	providerName := ""
	if llmProvider != nil {
		providerName = *llmProvider
	} else if epLLMProvider != nil {
		providerName = *epLLMProvider
	}
	if budgetTokens != nil {
		cfg.BudgetTokens = *budgetTokens
	}

	// Provider is required — no silent default. If neither orchestrator nor EP
	// specifies a provider, the run cannot proceed and the caller gets a clear error.
	if providerName == "" {
		return RunConfig{}, fmt.Errorf("workerconfig: no LLM provider configured on orchestrator %s or its entry point — set provider and model in Runtime settings: %w", appOrchestratorID, ErrNoProviderKey)
	}

	// Resolve API key + base_url + pricing via the shared precedence chain:
	// app-level provider_keys (most specific) -> tenant llm_providers row ->
	// platform-default llm_providers row.
	resolved, err := l.resolver.ResolveProvider(ctx, applicationID, tenantID, providerName)
	if err != nil {
		return RunConfig{}, fmt.Errorf("workerconfig: resolve provider key for %s: %w", providerName, err)
	}
	apiKey := resolved.Key
	llmBaseURL := resolved.BaseURL
	llmPricing := resolved.Pricing
	if apiKey == "" && providerName != "mock" && providerName != "ollama" {
		return RunConfig{}, fmt.Errorf("workerconfig: no API key found for provider %q — add one in Runtime → Provider Keys: %w", providerName, ErrNoProviderKey)
	}

	// Summarizer key comes from the same precedence chain, for the EP-configured provider.
	sumProvider := ""
	if summarizerProvider != nil {
		sumProvider = *summarizerProvider
	}
	sumModel := ""
	if summarizerModel != nil {
		sumModel = *summarizerModel
	}
	sumAPIKey := ""
	sumBaseURL := ""
	if memoryEnabled && sumProvider != "" {
		sumResolved, sumErr := l.resolver.ResolveProvider(ctx, applicationID, tenantID, sumProvider)
		if sumErr != nil {
			slog.Warn("workerconfig: failed to resolve summarizer key — memory disabled for run",
				"app_id", applicationID, "provider", sumProvider, "error", sumErr)
		} else {
			sumAPIKey = sumResolved.Key
			sumBaseURL = sumResolved.BaseURL
		}
	}

	return RunConfig{
		OrchestratorConfig: cfg,
		LLMProvider:        providerName,
		LLMAPIKey:          apiKey,
		LLMBaseURL:         llmBaseURL,
		LLMPricing:         llmPricing,
		SummarizerProvider: sumProvider,
		SummarizerModel:    sumModel,
		SummarizerAPIKey:   sumAPIKey,
		SummarizerBaseURL:  sumBaseURL,
	}, nil
}

// mcpServerEntry is one item in the app_orchestrators.mcp_servers JSONB array.
type mcpServerEntry struct {
	Slug  string   `json:"slug"`
	Tools []string `json:"tools,omitempty"`
}

// mcpManifestTool matches the structure of each entry in them.mcp_servers.tools_manifest.
type mcpManifestTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// resolveMCPServers parses the mcp_servers JSONB array and fetches tool manifests
// from the DB for each attached server, building orchestrator.MCPServerAttachment values.
func (l *PgxLoader) resolveMCPServers(ctx context.Context, raw []byte) ([]orchestrator.MCPServerAttachment, error) {
	if len(raw) == 0 || string(raw) == "[]" || string(raw) == "null" {
		return nil, nil
	}
	var entries []mcpServerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse mcp_servers: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	var attachments []orchestrator.MCPServerAttachment
	for _, entry := range entries {
		if entry.Slug == "" {
			continue
		}
		manifest, err := l.fetchMCPManifest(ctx, entry.Slug)
		if err != nil {
			slog.Warn("workerconfig: failed to fetch MCP manifest — server skipped",
				"slug", entry.Slug, "error", err)
			continue
		}

		toolDefs := buildMCPToolDefs(entry.Slug, entry.Tools, manifest)
		attachments = append(attachments, orchestrator.MCPServerAttachment{
			Slug:     entry.Slug,
			Tools:    entry.Tools,
			ToolDefs: toolDefs,
		})
	}
	return attachments, nil
}

// fetchMCPManifest retrieves the tools_manifest JSON for a server by slug.
func (l *PgxLoader) fetchMCPManifest(ctx context.Context, slug string) ([]mcpManifestTool, error) {
	const q = `SELECT COALESCE(tools_manifest, '[]'::jsonb) FROM them.mcp_servers WHERE slug = $1 AND enabled = true`
	var raw []byte
	if err := l.pool.QueryRow(ctx, q, slug).Scan(&raw); err != nil {
		return nil, fmt.Errorf("fetch manifest for %s: %w", slug, err)
	}
	var tools []mcpManifestTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("parse manifest for %s: %w", slug, err)
	}
	return tools, nil
}

// buildMCPToolDefs converts a manifest tool list to llm.ToolDef slice.
// Applies the allowlist (empty = all tools).
func buildMCPToolDefs(serverSlug string, allowlist []string, tools []mcpManifestTool) []llm.ToolDef {
	allowed := make(map[string]bool, len(allowlist))
	for _, t := range allowlist {
		allowed[t] = true
	}

	var defs []llm.ToolDef
	for _, t := range tools {
		if len(allowlist) > 0 && !allowed[t.Name] {
			continue
		}
		// Unmarshal inputSchema JSON into map[string]any for llm.ToolDef.
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, llm.ToolDef{
			Name:        "mcp__" + serverSlug + "__" + t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return defs
}

// resolveAgentSlugs converts component_definition UUIDs → agent slugs.
// agents.id equals component_definitions.id (Option C FK in schema).
// Only enabled agents are returned.
func (l *PgxLoader) resolveAgentSlugs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	const q = `SELECT slug FROM them.agents WHERE id = ANY($1::uuid[]) AND enabled = true ORDER BY slug`
	rows, err := l.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("workerconfig: resolve agent slugs: %w", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		slugs = append(slugs, slug)
	}
	return slugs, nil
}
