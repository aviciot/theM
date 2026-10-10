// Package llmgateway is the LLM Gateway — observe, meter, and govern.
//
// POST /{tenant_slug}/llm/v1/chat/completions accepts OpenAI-shaped requests
// from closed agents (cron jobs, scripts, internal services that call an LLM
// but expose no callable endpoint). The gateway authenticates, resolves the
// provider key, enforces gateway_policies (allowed models, per-request token
// ceiling, monthly USD budget), calls the LLM, records usage, and returns the
// response in OpenAI wire format (streaming or non-streaming).
//
// Design: docs/LLM_GATEWAY_DESIGN.md §3, §5, §10, §14 (Phases 1–2).
package llmgateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy holds the tenant-level governance rules loaded from gateway_policies.
// A nil *Policy means no row exists — all models allowed, no budget cap.
type Policy struct {
	// AllowedModels is the resolved-model allowlist. nil or empty = allow all.
	AllowedModels []string
	// ModelAliases maps alias names to concrete model IDs (e.g. "fast" → "claude-haiku-4-5-20251001").
	ModelAliases map[string]string
	// MaxTokensPerRequest is the per-request output ceiling. 0 = no cap.
	MaxTokensPerRequest int
	// MonthlyBudgetUSD is the rolling-month spend cap in USD. 0 = no cap.
	MonthlyBudgetUSD float64
}

// RequestRecord is the data written to them.gateway_requests after each call.
type RequestRecord struct {
	TenantID       string
	ClientID       string  // gateway_clients.id UUID; empty in Phase 1
	TokenHash      string  // sha256-hex of bearer token for audit
	Provider       string
	ModelRequested string
	ModelServed    string
	Status         string  // "ok" | "error" | "blocked" | "rate_limited"
	HTTPStatus     int
	ErrorCode      string
	TokensIn       int
	TokensOut      int
	CostUSD        float64
	LatencyMS      int
	TTFBMs         int
	Streamed       bool
}

// DAL is the data access layer for the gateway. Writes use the Admin pool
// (BYPASSRLS) because they are internal system records — tenant_id enforces
// isolation in the table itself.
type DAL struct {
	pool *pgxpool.Pool
}

// NewDAL creates a DAL backed by the given pool.
func NewDAL(pool *pgxpool.Pool) *DAL { return &DAL{pool: pool} }

// WriteRequest inserts one gateway_requests row. Called in a defer so usage
// is captured even when the client disconnects mid-stream.
func (d *DAL) WriteRequest(ctx context.Context, r RequestRecord) error {
	const q = `
		INSERT INTO them.gateway_requests
			(tenant_id, client_id, token_hash, provider, model_requested, model_served,
			 status, http_status, error_code, tokens_in, tokens_out, cost_usd,
			 latency_ms, ttfb_ms, streamed)
		VALUES
			($1::uuid,
			 NULLIF($2,'')::uuid,
			 NULLIF($3,''),
			 NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
			 $7, NULLIF($8,0), NULLIF($9,''),
			 $10, $11, $12,
			 NULLIF($13,0), NULLIF($14,0), $15)`

	_, err := d.pool.Exec(ctx, q,
		r.TenantID,
		r.ClientID,
		r.TokenHash,
		r.Provider, r.ModelRequested, r.ModelServed,
		r.Status, r.HTTPStatus, r.ErrorCode,
		r.TokensIn, r.TokensOut, r.CostUSD,
		r.LatencyMS, r.TTFBMs, r.Streamed,
	)
	return err
}

// ClientIDForHash returns the gateway_clients.id for a given token_hash, or ""
// if no gateway client is registered for that token. Best-effort.
func (d *DAL) ClientIDForHash(ctx context.Context, tokenHash string) string {
	const q = `SELECT id FROM them.gateway_clients WHERE token_hash = $1 LIMIT 1`
	var id string
	_ = d.pool.QueryRow(ctx, q, tokenHash).Scan(&id)
	return id
}

// LoadPolicy reads the gateway_policies row for the tenant.
// Returns nil, nil when no row exists (allow-all, no budget cap).
func (d *DAL) LoadPolicy(ctx context.Context, tenantID string) (*Policy, error) {
	const q = `
		SELECT allowed_models, model_aliases, max_tokens_per_request, monthly_budget_usd
		FROM them.gateway_policies
		WHERE tenant_id = $1::uuid`

	var (
		allowedModels       []string
		modelAliasesRaw     []byte
		maxTokensPerRequest *int
		monthlyBudgetUSD    *float64
	)
	err := d.pool.QueryRow(ctx, q, tenantID).Scan(
		&allowedModels,
		&modelAliasesRaw,
		&maxTokensPerRequest,
		&monthlyBudgetUSD,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p := &Policy{AllowedModels: allowedModels}
	if maxTokensPerRequest != nil {
		p.MaxTokensPerRequest = *maxTokensPerRequest
	}
	if monthlyBudgetUSD != nil {
		p.MonthlyBudgetUSD = *monthlyBudgetUSD
	}

	// Unmarshal aliases JSON — empty/null/"{}" → nil map is fine.
	if len(modelAliasesRaw) > 0 {
		var aliases map[string]string
		if jerr := json.Unmarshal(modelAliasesRaw, &aliases); jerr == nil && len(aliases) > 0 {
			p.ModelAliases = aliases
		}
	}
	return p, nil
}

// SumMonthlySpend returns the total cost_usd for the tenant in the current
// calendar month from gateway_requests. Returns 0 on no rows.
func (d *DAL) SumMonthlySpend(ctx context.Context, tenantID string) (float64, error) {
	const q = `
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM them.gateway_requests
		WHERE tenant_id = $1::uuid
		  AND created_at >= date_trunc('month', now())`
	var total float64
	err := d.pool.QueryRow(ctx, q, tenantID).Scan(&total)
	return total, err
}
