package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/crypto"
)

// agentParamEntry is the stored shape for a secret-type agent param.
type agentParamEntry struct {
	CT   string `json:"ct"`
	Hint string `json:"hint"`
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

// specCacheKey returns a cache key that includes the tenantID so specs from
// different tenants with coincidental agent UUIDs cannot cross-contaminate.
func specCacheKey(tenantID, agentID string) string {
	return tenantID + ":" + agentID
}

func (c *specCache) get(key string) *agentgen.AgentSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		return e.spec
	}
	return nil
}

func (c *specCache) set(key string, spec *agentgen.AgentSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cachedSpec{spec: spec, expiresAt: time.Now().Add(60 * time.Second)}
}

func (rt *Runtime) loadSpecByAgentID(ctx context.Context, tenantID, agentID string) (*agentgen.AgentSpec, error) {
	key := specCacheKey(tenantID, agentID)
	if spec := rt.specCache.get(key); spec != nil {
		return spec, nil
	}
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE s.agent_id = $1::uuid AND a.tenant_id = $2::uuid`, agentID, tenantID)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	rt.specCache.set(key, &spec)
	return &spec, nil
}

// loadAppAPIKey fetches and decrypts the provider_keys for the given application.
// Returns a map of provider→plaintext key (e.g. "anthropic"→"sk-ant-...").
// Returns an empty map on any error — callers fall back to the platform key.
// The decrypted keys are never logged. The tenant_id predicate prevents cross-tenant key reads.
func (rt *Runtime) loadAppAPIKey(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}

	// Try new structured format {"anthropic": {"ct": "...", "hint": "XXXX"}}.
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
			plain, err := crypto.DecryptStored(rt.cryptoKey, e.CT)
			if err != nil {
				// Legacy plaintext row (written before encryption): use CT directly.
				// This handles the migration window until keys are re-encrypted.
				if len(e.CT) > 6 && e.CT[:6] == "plain:" {
					out[provider] = e.CT[6:]
					continue
				}
				// Decryption failed for an encrypted entry — likely a key rotation mismatch.
				// Log at warn so operators can detect and re-save affected keys.
				slog.Warn("agent-runtime: provider key decryption failed; falling back to platform key",
					"app_id", appID, "provider", provider, "err", err)
				continue
			}
			out[provider] = plain
		}
		if len(out) > 0 {
			return out
		}
	}

	// Legacy flat format {"anthropic": "sk-ant-..."} — plaintext, pre-encryption.
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

// loadAppGlobalParams fetches and decrypts app_params for the given application.
// Returns a name→plaintext map. Non-fatal: returns an empty map on any error.
// The decrypted values are never logged. The tenant_id predicate prevents cross-tenant reads.
func (rt *Runtime) loadAppGlobalParams(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}
	return decodeAppGlobalParams(raw, rt.cryptoKey, appID)
}

// decodeAppGlobalParams parses and decrypts the raw app_params JSONB blob.
// Exported for testing. Returns an empty map (never nil) on any decode error.
func decodeAppGlobalParams(raw []byte, cryptoKey []byte, appID string) map[string]string {
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
			// Test/dev mode: service stores "plain:<plaintext>" when no crypto key is configured.
			if len(entry.CT) > 6 && entry.CT[:6] == "plain:" {
				out[name] = entry.CT[6:]
				continue
			}
			plain, err := crypto.DecryptStored(cryptoKey, entry.CT)
			if err != nil {
				slog.Warn("agent-runtime: app global param decryption failed",
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
	// Note: we cannot pre-populate the agent-ID cache here because loadSpecBySlug
	// has no tenantID, and cache keys are tenant-scoped (specCacheKey).
	return &spec, nil
}

// extractNodeLLMOverrides reads the llm_nodes sub-map from config_overrides and
// returns a map of node_id → NodeLLMOverride. Safe to call with a nil map.
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

func (rt *Runtime) loadBinding(ctx context.Context, tenantID, appID, agentID, bindingID string) (*agentgen.AppAgentBinding, []byte, error) {
	var (
		query string
		args  []any
	)
	// Both query paths JOIN applications to assert tenant ownership and enforce all
	// four caller-supplied IDs. Without the applicationID + agentID predicates in the
	// bindingID path, a caller within the same tenant could supply a valid binding UUID
	// belonging to a different application or agent and bypass the ownership check.
	if bindingID != "" {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.id = $1::uuid
		            AND b.application_id = $2::uuid
		            AND b.agent_id = $3::uuid
		            AND a.tenant_id = $4::uuid`
		args = []any{bindingID, appID, agentID, tenantID}
	} else {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid AND a.tenant_id = $3::uuid`
		args = []any{appID, agentID, tenantID}
	}

	row := rt.pool.QueryRow(ctx, query, args...)
	var (
		id, appIDDB, agentIDDB string
		defID                  *string
		credJSON               []byte // selected but unused — column retained for compat
		cfgJSON, polJSON       []byte
		agentParamsJSON        []byte
	)
	if err := row.Scan(&id, &appIDDB, &agentIDDB, &defID, &credJSON, &cfgJSON, &polJSON, &agentParamsJSON); err != nil {
		return nil, nil, fmt.Errorf("load binding: %w", err)
	}
	_ = credJSON

	var overrides map[string]any
	_ = json.Unmarshal(cfgJSON, &overrides)
	var policies agentgen.InvocationPolicies
	_ = json.Unmarshal(polJSON, &policies)

	return &agentgen.AppAgentBinding{
		ID:              id,
		ApplicationID:   appIDDB,
		AgentID:         agentIDDB,
		DefinitionID:    defID,
		ConfigOverrides: overrides,
		Policies:        policies,
	}, agentParamsJSON, nil
}

// resolveAgentParams decrypts secret-type params and returns a plaintext map.
// Params absent from the stored JSON fall back to their declared default.
// Decryption failures are logged at Warn (key name only) and the param is omitted.
func (rt *Runtime) resolveAgentParams(raw []byte, decls []agentgen.AgentParamSpec) map[string]string {
	out := make(map[string]string, len(decls))
	if len(decls) == 0 {
		return out
	}

	var stored map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &stored)
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
			var entry agentParamEntry
			if json.Unmarshal(rawVal, &entry) == nil && entry.CT != "" {
				plain, err := crypto.DecryptStored(rt.cryptoKey, entry.CT)
				if err != nil {
					rt.logger.Warn("agent-runtime: agent param decryption failed",
						"key", decl.Key)
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
