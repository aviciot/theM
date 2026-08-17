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

	// Summarizer fields — populated when memory_enabled=true on the orchestrator row.
	SummarizerProvider string
	SummarizerModel    string
	SummarizerAPIKey   string // plaintext, decrypted from provider_keys or summarizer_api_key_encrypted
}

// Loader resolves per-run orchestrator config from persistent storage.
// Tests inject a fake; production uses PgxLoader.
type Loader interface {
	// LoadRunConfig returns the RunConfig for the given AppOrchestratorID.
	// ApplicationID is used for provider key lookup and as a cross-check
	// that the orchestrator belongs to the expected application.
	LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID string) (RunConfig, error)
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
// The query joins app_orchestrators → applications to enforce tenant safety
// (app_orchestrators has no tenant_id; safety comes from application FK).
func (l *PgxLoader) LoadRunConfig(ctx context.Context, appOrchestratorID, applicationID string) (RunConfig, error) {
	const orchQ = `
SELECT
    ao.system_prompt,
    ao.llm_provider,
    ao.llm_model,
    ao.max_iterations,
    ao.max_parallel_tools,
    ao.history_window,
    ao.budget_tokens,
    ao.allowed_agent_ids,
    COALESCE(ao.memory_enabled, false),
    COALESCE(ao.summarize_every_n_calls, 0),
    COALESCE(ao.memory_raw_fallback_n, 5),
    ao.summarizer_provider,
    ao.summarizer_model,
    ao.summarizer_api_key_encrypted
FROM them.app_orchestrators ao
JOIN them.applications a ON a.id = ao.application_id
WHERE ao.id = $1::uuid
  AND ao.application_id = $2::uuid
  AND ao.enabled = true`

	row := l.pool.QueryRow(ctx, orchQ, appOrchestratorID, applicationID)

	var (
		systemPrompt              *string
		llmProvider               *string
		llmModel                  *string
		maxIterations             int
		maxParallelTools          int
		historyWindow             int
		budgetTokens              *int
		allowedAgentIDs           []string
		memoryEnabled             bool
		summarizeEveryNCalls      int
		memoryRawFallbackN        int
		summarizerProvider        *string
		summarizerModel           *string
		summarizerAPIKeyEncrypted *string
	)

	if err := row.Scan(
		&systemPrompt,
		&llmProvider,
		&llmModel,
		&maxIterations,
		&maxParallelTools,
		&historyWindow,
		&budgetTokens,
		&allowedAgentIDs,
		&memoryEnabled,
		&summarizeEveryNCalls,
		&memoryRawFallbackN,
		&summarizerProvider,
		&summarizerModel,
		&summarizerAPIKeyEncrypted,
	); err != nil {
		return RunConfig{}, fmt.Errorf("workerconfig: load orchestrator %s: %w", appOrchestratorID, err)
	}

	// Resolve allowed_agent_ids (component_definition UUIDs = agents.id) → slugs.
	slugs, err := l.resolveAgentSlugs(ctx, allowedAgentIDs)
	if err != nil {
		// Non-fatal: proceed without tools rather than failing the whole run.
		slugs = nil
	}

	cfg := orchestrator.Config{
		MaxIterations:        maxIterations,
		MaxParallelTools:     maxParallelTools,
		HistoryWindow:        historyWindow,
		AllowedAgents:        slugs,
		MemoryEnabled:        memoryEnabled,
		SummarizeEveryNCalls: summarizeEveryNCalls,
		MemoryRawFallbackN:   memoryRawFallbackN,
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

	// Load the plaintext API key from applications.provider_keys (decrypted).
	apiKey := ""
	if providerName != "" {
		apiKey, _ = l.loadProviderKey(ctx, applicationID, providerName)
	}

	// Resolve summarizer config.
	sumProvider := ""
	if summarizerProvider != nil {
		sumProvider = *summarizerProvider
	}
	sumModel := ""
	if summarizerModel != nil {
		sumModel = *summarizerModel
	}

	// Summarizer API key: try app provider_keys first, then fall back to row-level encrypted key.
	sumAPIKey := ""
	if memoryEnabled && sumProvider != "" {
		sumAPIKey, _ = l.loadProviderKey(ctx, applicationID, sumProvider)
		if sumAPIKey == "" && summarizerAPIKeyEncrypted != nil {
			sumAPIKey, _ = l.decryptValue(*summarizerAPIKeyEncrypted)
		}
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

// loadProviderKey reads and decrypts the key from applications.provider_keys JSONB.
// Returns empty string when the application is not found or no key is stored.
// Keys are stored as Fernet-encrypted JSON strings: {"anthropic": "enc:..."}.
func (l *PgxLoader) loadProviderKey(ctx context.Context, applicationID, provider string) (string, error) {
	const q = `SELECT COALESCE(provider_keys->$2, 'null'::jsonb)::text
	           FROM them.applications
	           WHERE id = $1::uuid`
	var raw string
	if err := l.pool.QueryRow(ctx, q, applicationID, provider).Scan(&raw); err != nil {
		return "", err
	}
	// raw is a JSON string: `"enc:..."` or `null`
	if raw == "null" || raw == "" {
		return "", nil
	}
	// Strip JSON string quotes.
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return l.decryptValue(raw)
}

// decryptValue decrypts a Fernet-encrypted value (with "enc:" prefix) or returns
// the value as-is if it is not encrypted (legacy plain-text entries).
func (l *PgxLoader) decryptValue(stored string) (string, error) {
	if len(l.fernetKey) == 0 {
		// No key configured — return as-is (graceful degradation).
		return stored, nil
	}
	return crypto.DecryptStored(l.fernetKey, stored)
}
