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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"
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

// ErrInvalidClientConfig is returned when a client's configuration violates gateway rules.
var ErrInvalidClientConfig = errors.New("invalid gateway client configuration")

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
	pool  *pgxpool.Pool
	redis rueidis.Client // Redis for policy cache (optional - nil = no cache)
}

// NewDAL creates a DAL backed by the given pool.
func NewDAL(pool *pgxpool.Pool) *DAL { return &DAL{pool: pool} }

// WithRedis attaches a Redis client for policy caching.
func (d *DAL) WithRedis(r rueidis.Client) *DAL {
	d.redis = r
	return d
}

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

// LookupClientTenant resolves a token hash to the client ID and tenant ID.
// Returns ("", "", nil) when no row matches — the caller treats this as
// an authentication failure. Returns ErrInvalidClientConfig when the client
// configuration violates gateway rules (e.g., missing required policy profile).
func (d *DAL) LookupClientTenant(ctx context.Context, tokenHash string) (clientID, tenantID string, err error) {
	const q = `
		SELECT id::text, tenant_id::text, config_status, config_status_reason
		FROM them.gateway_clients
		WHERE token_hash = $1
		LIMIT 1`

	var configStatus, configStatusReason string
	err = d.pool.QueryRow(ctx, q, tokenHash).Scan(&clientID, &tenantID, &configStatus, &configStatusReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}

	// Enforce: client must be in 'valid' state to use gateway
	if configStatus != "valid" {
		reason := configStatusReason
		if reason == "" {
			reason = fmt.Sprintf("client configuration status: %s", configStatus)
		}
		return "", "", fmt.Errorf("%w: %s", ErrInvalidClientConfig, reason)
	}

	return clientID, tenantID, nil
}

// ClientApp holds the resolved app and entry-point slugs for a gateway client
// that has been linked to an App Canvas application via app_id.
type ClientApp struct {
	ClientID string
	AppSlug  string
	EPSlug   string
}

// LoadClientApp resolves a token hash to the App Canvas application linked to
// the client via gateway_clients.app_id. Returns nil when the token has no
// registered client, the client has no app_id set, or the app has no gateway
// entry point — all treated as "no profile app, use direct LLM path".
func (d *DAL) LoadClientApp(ctx context.Context, tokenHash string) (*ClientApp, error) {
	const q = `
		SELECT c.id::text, a.slug, ep.slug
		FROM them.gateway_clients c
		JOIN them.applications a ON a.id = c.app_id
		JOIN them.entry_points ep ON ep.application_id = a.id
		  AND ep.entry_point_type = 'gateway'
		  AND ep.enabled
		WHERE c.token_hash = $1
		LIMIT 1`

	var ca ClientApp
	err := d.pool.QueryRow(ctx, q, tokenHash).Scan(&ca.ClientID, &ca.AppSlug, &ca.EPSlug)
	if err != nil {
		return nil, nil // no row = no profile app
	}
	return &ca, nil
}

// LoadPolicy reads the client's assigned policy profile.
// Returns nil, nil when the client has no policy profile assigned (no enforcement).
func (d *DAL) LoadPolicy(ctx context.Context, tenantID, clientID string) (*Policy, error) {
	// Try Redis cache first (if available)
	if d.redis != nil {
		cacheKey := "them:gateway:policy:" + clientID
		cmd := d.redis.B().Get().Key(cacheKey).Build()
		resp := d.redis.Do(ctx, cmd)
		if data, err := resp.AsBytes(); err == nil {
			// Cache hit - deserialize
			var p Policy
			if json.Unmarshal(data, &p) == nil {
				return &p, nil
			}
		}
	}

	// Cache miss (or no Redis) - load from DB
	p, err := d.loadPolicyFromDB(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}

	// Store in Redis for 15 minutes (best-effort, ignore errors)
	if d.redis != nil && p != nil {
		if data, jerr := json.Marshal(p); jerr == nil {
			setCmd := d.redis.B().Set().Key("them:gateway:policy:"+clientID).Value(string(data)).Ex(15*time.Minute).Build()
			_ = d.redis.Do(ctx, setCmd)
		}
	}

	return p, nil
}

// loadPolicyFromDB queries PostgreSQL for the policy profile.
// Extracted from LoadPolicy to enable caching layer above.
func (d *DAL) loadPolicyFromDB(ctx context.Context, tenantID, clientID string) (*Policy, error) {
	const q = `
		SELECT pp.allowed_models, pp.model_aliases, pp.max_tokens_per_request, pp.monthly_budget_usd
		FROM them.gateway_clients gc
		JOIN them.gateway_policy_profiles pp ON pp.id = gc.policy_profile_id
		WHERE gc.tenant_id = $1::uuid AND gc.id = $2::uuid`

	var (
		allowedModels       []string
		modelAliasesRaw     []byte
		maxTokensPerRequest *int
		monthlyBudgetUSD    *float64
	)
	err := d.pool.QueryRow(ctx, q, tenantID, clientID).Scan(
		&allowedModels,
		&modelAliasesRaw,
		&maxTokensPerRequest,
		&monthlyBudgetUSD,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// No policy profile assigned → no enforcement.
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
