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
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/orchestrator"
)

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

	// Summarizer fields — populated when memory_enabled=true on the entry_point row.
	// Memory config is per-EP so each entry point can have independent history settings.
	SummarizerProvider string
	SummarizerModel    string
	SummarizerAPIKey   string // plaintext, decrypted from provider_keys
}

// Loader resolves per-run orchestrator config from persistent storage.
// Tests inject a fake; production uses PgxLoader.
type Loader interface {
	// LoadRunConfig returns the RunConfig for the given AppOrchestratorID.
	// ApplicationID is used for provider key lookup and as a cross-check.
	// EntryPointID is used to load per-EP memory/history configuration.
	LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID, entryPointID string) (RunConfig, error)
}

// PgxLoader implements Loader against a live PostgreSQL pool.
type PgxLoader struct {
	pool      *pgxpool.Pool
	fernetKey []byte
}

// NewPgxLoader creates a PgxLoader backed by the given connection pool.
// fernetKey is the 32-byte key used to decrypt provider_keys values; derive it
// with crypto.DeriveKey(cfg.SecretKey).
func NewPgxLoader(pool *pgxpool.Pool, fernetKey []byte) *PgxLoader {
	return &PgxLoader{pool: pool, fernetKey: fernetKey}
}

// LoadRunConfig resolves orchestrator config + provider key for one run.
// appOrchestratorID + applicationID load the LLM/agent/loop config.
// entryPointID loads the per-EP memory/history config (may be empty — disables memory).
func (l *PgxLoader) LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID, entryPointID string) (RunConfig, error) {
	const orchQ = `
SELECT
    ao.system_prompt,
    ao.llm_provider,
    ao.llm_model,
    ao.max_iterations,
    ao.max_parallel_tools,
    ao.budget_tokens,
    ao.allowed_agent_ids
FROM them.app_orchestrators ao
JOIN them.applications a ON a.id = ao.application_id
WHERE ao.id = $1::uuid
  AND ao.application_id = $2::uuid
  AND ao.enabled = true`

	row := l.pool.QueryRow(ctx, orchQ, appOrchestratorID, applicationID)

	var (
		systemPrompt    *string
		llmProvider     *string
		llmModel        *string
		maxIterations   int
		maxParallelTools int
		budgetTokens    *int
		allowedAgentIDs []string
	)

	if err := row.Scan(
		&systemPrompt,
		&llmProvider,
		&llmModel,
		&maxIterations,
		&maxParallelTools,
		&budgetTokens,
		&allowedAgentIDs,
	); err != nil {
		return RunConfig{}, fmt.Errorf("workerconfig: load orchestrator %s: %w", appOrchestratorID, err)
	}

	// Load per-EP memory config (only when entryPointID is provided).
	var (
		memoryEnabled        bool
		historyWindow        = 20
		summarizeEveryN      int
		rawFallbackN         = 3
		summarizerProvider   *string
		summarizerModel      *string
	)
	if entryPointID != "" {
		const epQ = `
SELECT
    COALESCE(ep.memory_enabled, false),
    COALESCE(ep.history_window, 20),
    COALESCE(ep.summarize_every_n_calls, 0),
    COALESCE(ep.memory_raw_fallback_n, 3),
    ep.summarizer_provider,
    ep.summarizer_model
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

	cfg := orchestrator.Config{
		MaxIterations:        maxIterations,
		MaxParallelTools:     maxParallelTools,
		HistoryWindow:        historyWindow,
		AllowedAgents:        slugs,
		MemoryEnabled:        memoryEnabled,
		SummarizeEveryNCalls: summarizeEveryN,
		MemoryRawFallbackN:   rawFallbackN,
	}
	if systemPrompt != nil {
		cfg.SystemPrompt = *systemPrompt
	}
	if llmModel != nil {
		cfg.Model = *llmModel
	}

	providerName := ""
	if llmProvider != nil {
		providerName = *llmProvider
	}
	if budgetTokens != nil {
		cfg.BudgetTokens = *budgetTokens
	}

	// If no provider is set on the orchestrator row, default to "anthropic" so
	// the per-app key is resolved and used instead of the platform fallback.
	if providerName == "" {
		providerName = "anthropic"
	}
	apiKey, _ := l.loadProviderKey(ctx, applicationID, providerName)

	// Summarizer key comes from app provider_keys using the EP-configured provider.
	sumProvider := ""
	if summarizerProvider != nil {
		sumProvider = *summarizerProvider
	}
	sumModel := ""
	if summarizerModel != nil {
		sumModel = *summarizerModel
	}
	sumAPIKey := ""
	if memoryEnabled && sumProvider != "" {
		sumAPIKey, _ = l.loadProviderKey(ctx, applicationID, sumProvider)
	}

	return RunConfig{
		OrchestratorConfig: cfg,
		LLMProvider:        providerName,
		LLMAPIKey:          apiKey,
		SummarizerProvider: sumProvider,
		SummarizerModel:    sumModel,
		SummarizerAPIKey:   sumAPIKey,
	}, nil
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

// loadProviderKey reads and decrypts the key for one provider from applications.provider_keys.
// Returns empty string when the application is not found or no key is stored.
//
// provider_keys JSONB stores two formats:
//   - New (encrypted): {"anthropic": {"ct": "enc:...", "hint": "XXXX"}}
//   - Legacy (plaintext): {"anthropic": "sk-ant-..."}
//
// Both are handled transparently.
func (l *PgxLoader) loadProviderKey(ctx context.Context, applicationID, provider string) (string, error) {
	const q = `SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid`
	var raw []byte
	if err := l.pool.QueryRow(ctx, q, applicationID).Scan(&raw); err != nil {
		return "", err
	}

	// Try new structured format: {"provider": {"ct": "enc:...", "hint": "XXXX"}}.
	type entry struct {
		CT string `json:"ct"`
	}
	var structured map[string]entry
	if err := json.Unmarshal(raw, &structured); err == nil {
		if e, ok := structured[provider]; ok && e.CT != "" {
			return l.decryptValue(e.CT)
		}
		// Check if any entry has a structured shape (not a flat string map).
		for _, e := range structured {
			if e.CT != "" {
				// Structured format confirmed; this provider has no key.
				return "", nil
			}
		}
	}

	// Legacy flat format: {"provider": "plaintext"}.
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err == nil {
		if v, ok := flat[provider]; ok && v != "" {
			return v, nil
		}
	}
	return "", nil
}

// decryptValue decrypts a stored value encrypted by crypto.EncryptStored.
// Returns the value as-is when no fernetKey is configured (graceful degradation / tests).
func (l *PgxLoader) decryptValue(stored string) (string, error) {
	if len(l.fernetKey) == 0 {
		// No key configured — return as-is (graceful degradation).
		return stored, nil
	}
	return crypto.DecryptStored(l.fernetKey, stored)
}
