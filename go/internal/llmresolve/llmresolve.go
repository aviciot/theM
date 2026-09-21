// Package llmresolve resolves an LLM provider's API key, base URL, and model
// pricing from PostgreSQL, applying the same precedence everywhere it is
// needed: an app-scoped key (them.applications.provider_keys) takes priority
// over a tenant-scoped them.llm_providers row, which takes priority over the
// platform-default them.llm_providers row (tenant_id IS NULL).
//
// This precedence chain used to be duplicated (and had drifted) across
// internal/temporal/workerconfig/loader.go and cmd/dag-worker/main.go. It is
// now defined once here; both call sites resolve through Resolver.
package llmresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/crypto"
)

// ModelPrice is the USD cost per token for one model, converted at load time
// from them.llm_providers.model_pricing (stored per-million-tokens in the DB).
type ModelPrice struct {
	InputPerToken  float64
	OutputPerToken float64
}

// PricingTable is a model->ModelPrice map that satisfies
// orchestrator.CostEstimator's EstimateCost(model, in, out) (float64, bool)
// shape by structural typing — no import of internal/orchestrator needed
// here, keeping this package dependency-free of the runtime hot path.
type PricingTable map[string]ModelPrice

// EstimateCost returns the USD cost for model using this table's per-token
// rates. ok is false when the table has no entry for model, signalling the
// caller (orchestrator.Orchestrator.estimateCost) to fall back to its
// built-in default rate card instead of silently reporting zero cost.
func (t PricingTable) EstimateCost(model string, inputTokens, outputTokens int) (float64, bool) {
	p, found := t[model]
	if !found {
		return 0, false
	}
	return p.InputPerToken*float64(inputTokens) + p.OutputPerToken*float64(outputTokens), true
}

// ProviderRow is the resolved state for one provider lookup: the decrypted
// API key (empty if none configured), the custom base URL (empty = provider
// default), and the model→price table from that row's model_pricing column.
type ProviderRow struct {
	Key     string
	BaseURL string
	Pricing PricingTable
}

// Resolver resolves provider keys/pricing against a live PostgreSQL pool.
type Resolver struct {
	pool      *pgxpool.Pool
	fernetKey []byte
	logger    *slog.Logger
}

// New creates a Resolver. fernetKey is the 32-byte key used to decrypt
// api_key_encrypted / provider_keys ciphertext; derive it with
// crypto.DeriveKey(cfg.SecretKey). logger may be nil (defaults to slog.Default()).
func New(pool *pgxpool.Pool, fernetKey []byte, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resolver{pool: pool, fernetKey: fernetKey, logger: logger}
}

// Resolved is the outcome of the full app -> tenant -> platform precedence
// chain: the API key from whichever level supplied one, plus the base_url
// and pricing from the them.llm_providers row that matched (tenant row if
// present, else platform row — the app-level provider_keys column carries
// no base_url or pricing of its own).
type Resolved struct {
	Key     string
	BaseURL string
	Pricing PricingTable
}

// ResolveProvider resolves the API key, base URL, and pricing for provider
// using the app -> tenant precedence only. Platform-default keys are NEVER
// used as a fallback for tenant requests — tenants must configure their own
// API keys. base_url and pricing come from the tenant row when present, else
// from the platform row (metadata only — the platform key is not used).
func (r *Resolver) ResolveProvider(ctx context.Context, applicationID, tenantID, provider string) (Resolved, error) {
	appKey := ""
	if applicationID != "" && tenantID != "" {
		var err error
		appKey, err = r.AppProviderKey(ctx, applicationID, tenantID, provider)
		if err != nil {
			return Resolved{}, fmt.Errorf("llmresolve: app provider key for %s: %w", provider, err)
		}
	}

	tenantRow := r.TenantProviderRow(ctx, tenantID, provider)

	// Key precedence: app-level → tenant-level. Platform key is NEVER used
	// as a fallback — tenants pay for their own LLM usage.
	key := appKey
	if key == "" {
		key = tenantRow.Key
	}

	// For base_url and pricing, fall back to platform metadata (not the key)
	// when the tenant row has no values configured.
	baseURL := tenantRow.BaseURL
	pricing := tenantRow.Pricing
	if baseURL == "" && len(pricing) == 0 {
		platformRow := r.PlatformProviderRow(ctx, provider)
		baseURL = platformRow.BaseURL
		pricing = platformRow.Pricing
	}

	return Resolved{Key: key, BaseURL: baseURL, Pricing: pricing}, nil
}

// TenantProviderRow looks up the tenant-scoped them.llm_providers row for
// provider. Returns the zero value when tenantID or provider is empty, or
// when no tenant-scoped row exists.
func (r *Resolver) TenantProviderRow(ctx context.Context, tenantID, provider string) ProviderRow {
	if tenantID == "" || provider == "" {
		return ProviderRow{}
	}
	return r.lookupProviderRow(ctx, provider, &tenantID)
}

// PlatformProviderRow looks up the platform-default them.llm_providers row
// (tenant_id IS NULL) for provider.
func (r *Resolver) PlatformProviderRow(ctx context.Context, provider string) ProviderRow {
	return r.lookupProviderRow(ctx, provider, nil)
}

// lookupProviderRow fetches, decrypts, and parses a single llm_providers row.
// tenantID nil = platform default; non-nil = tenant override. Returns the
// zero value on a missing row. A decrypt failure is logged and that row's key
// is dropped (empty string) rather than leaking ciphertext to the caller —
// pricing and base_url are still returned since they are not secret.
func (r *Resolver) lookupProviderRow(ctx context.Context, provider string, tenantID *string) ProviderRow {
	var encPtr, baseURLPtr *string
	var pricingRaw []byte
	var err error
	if tenantID == nil {
		const q = `SELECT api_key_encrypted, base_url, model_pricing FROM them.llm_providers
		           WHERE name=$1 AND tenant_id IS NULL AND enabled=true LIMIT 1`
		err = r.pool.QueryRow(ctx, q, provider).Scan(&encPtr, &baseURLPtr, &pricingRaw)
	} else {
		const q = `SELECT api_key_encrypted, base_url, model_pricing FROM them.llm_providers
		           WHERE name=$1 AND tenant_id=$2::uuid AND enabled=true LIMIT 1`
		err = r.pool.QueryRow(ctx, q, provider, *tenantID).Scan(&encPtr, &baseURLPtr, &pricingRaw)
	}
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("llmresolve: llm_providers lookup error — skipping",
				"provider", provider, "has_tenant", tenantID != nil)
		}
		return ProviderRow{}
	}

	out := ProviderRow{Pricing: parseModelPricing(pricingRaw)}
	if baseURLPtr != nil {
		out.BaseURL = *baseURLPtr
	}
	if encPtr == nil || *encPtr == "" {
		return out
	}
	plain, decErr := r.DecryptValue(*encPtr)
	if decErr != nil {
		r.logger.Warn("llmresolve: llm_providers key decrypt failed — skipping key",
			"provider", provider, "has_tenant", tenantID != nil, "error", decErr)
		return out
	}
	out.Key = plain
	return out
}

// parseModelPricing converts the raw model_pricing JSONB
// ({"model": {"input": float, "output": float}}, per-million-tokens) into a
// per-token PricingTable. Malformed or empty input yields an empty table.
func parseModelPricing(raw []byte) PricingTable {
	out := PricingTable{}
	if len(raw) == 0 {
		return out
	}
	var parsed map[string]struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return out
	}
	for model, p := range parsed {
		out[model] = ModelPrice{
			InputPerToken:  p.Input / 1e6,
			OutputPerToken: p.Output / 1e6,
		}
	}
	return out
}

// AppProviderKey reads and decrypts the key for one provider from
// them.applications.provider_keys. The tenantID predicate is required —
// without it a caller could read another tenant's app-level key by UUID
// alone. Returns "" when the application is not found (or belongs to a
// different tenant) or no key is stored for provider.
//
// provider_keys JSONB stores two formats, both handled transparently:
//   - New (encrypted):  {"anthropic": {"ct": "enc:...", "hint": "XXXX"}}
//   - Legacy (plaintext): {"anthropic": "sk-ant-..."}
func (r *Resolver) AppProviderKey(ctx context.Context, applicationID, tenantID, provider string) (string, error) {
	const q = `SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`
	var raw []byte
	if err := r.pool.QueryRow(ctx, q, applicationID, tenantID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	// A decrypt failure (e.g. key rotation mismatch) is logged and treated as
	// "no app-level key" rather than aborting resolution — the caller still
	// has a tenant/platform row to fall back to.
	key, decErr := ParseAppProviderKey(raw, provider, r.DecryptValue)
	if decErr != nil {
		r.logger.Warn("llmresolve: app-level provider key decrypt failed — falling back",
			"provider", provider, "app_id", applicationID, "error", decErr)
		return "", nil
	}
	return key, nil
}

// plainKeyPrefix marks a provider_keys entry written while no crypto key was
// configured (test/dev mode) — see internal/admin/service/applications.go
// encryptKey/decryptKey. It must be stripped before crypto.DecryptStored is
// ever called, since DecryptStored only recognizes the "enc:" prefix and
// would otherwise pass a "plain:..." value through unchanged, corrupting it.
const plainKeyPrefix = "plain:"

// ParseAppProviderKey extracts and decrypts provider's key from a raw
// provider_keys JSONB blob. decrypt is called only for structured-format
// ciphertext entries that are not already plaintext. Exported so callers
// that already hold the raw bytes (e.g. from a JOINed query) do not need a
// second round trip to the DB.
func ParseAppProviderKey(raw []byte, provider string, decrypt func(string) (string, error)) (string, error) {
	type entry struct {
		CT string `json:"ct"`
	}
	var structured map[string]entry
	if err := json.Unmarshal(raw, &structured); err == nil {
		if e, ok := structured[provider]; ok && e.CT != "" {
			if strings.HasPrefix(e.CT, plainKeyPrefix) {
				return e.CT[len(plainKeyPrefix):], nil
			}
			return decrypt(e.CT)
		}
		for _, e := range structured {
			if e.CT != "" {
				// Structured format confirmed; this provider has no key.
				return "", nil
			}
		}
	}

	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err == nil {
		if v, ok := flat[provider]; ok && v != "" {
			return v, nil
		}
	}
	return "", nil
}

// DecryptValue decrypts a stored value encrypted by crypto.EncryptStored.
//
// It fails loudly (returns an error) when no Fernet key is configured,
// rather than returning the ciphertext unchanged — a caller that ignored the
// error used to receive an opaque "enc:..." string as if it were a usable
// plaintext API key. Legacy unencrypted values (no "enc:" prefix) still pass
// through unchanged, since crypto.DecryptStored already treats those as
// plaintext regardless of key presence.
func (r *Resolver) DecryptValue(stored string) (string, error) {
	if len(r.fernetKey) == 0 {
		if looksEncrypted(stored) {
			return "", fmt.Errorf("llmresolve: cannot decrypt — no Fernet key configured (stored value is ciphertext)")
		}
		return stored, nil
	}
	return crypto.DecryptStored(r.fernetKey, stored)
}

// looksEncrypted reports whether stored carries the "enc:" ciphertext prefix
// used by crypto.EncryptStored, as opposed to a legacy plaintext value.
func looksEncrypted(stored string) bool {
	return len(stored) > 4 && stored[:4] == "enc:"
}
