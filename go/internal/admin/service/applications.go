package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
)

// epConfigChannel is the Redis pub/sub channel for cross-pod EP config cache invalidation.
// Must stay in sync with the Python platform's EP_CONFIG_CHANGED_CHANNEL constant.
const epConfigChannel = "them:ep:config:changed"

const (
	orchCacheKeyFmt  = "them:app:%s:orch:%s"
	orchLocKeyFmt    = "them:orch:loc:%s"
	agentRegistryKey = "them:agents:registry"
)

// AppRuntimeConfig is the runtime configuration for an application.
// Mirrors Python's AppRuntimeConfig schema exactly.
type AppRuntimeConfig struct {
	MaxConcurrentSessions *int     `json:"max_concurrent_sessions"`
	RateLimitRPM          *int     `json:"rate_limit_rpm"`
	BlockedTokens         []string `json:"blocked_tokens"`
	BlockedUserIDs        []int    `json:"blocked_user_ids"`
	SessionTimeoutMinutes *int     `json:"session_timeout_minutes"`
}

// validEPTypes is the canonical set of allowed entry_point_type values.
// Must stay in sync with the Python platform's _VALID_EP_TYPES list.
var validEPTypes = map[string]struct{}{
	"websocket": {},
	"sse":       {},
	"voice":     {},
	"webrtc":    {},
	"a2a":       {},
}

// IsValidEPType reports whether t is an allowed entry point type.
func IsValidEPType(t string) bool {
	_, ok := validEPTypes[t]
	return ok
}

// AppService owns the business logic for application and entry point CRUD.
type AppService struct {
	dal        Dal
	cache      Cache
	cryptoKey  []byte // AES-GCM key for provider_keys encryption; nil = no encryption (tests)
}

// NewAppService creates an AppService.
func NewAppService(d Dal, c Cache, cryptoKey []byte) *AppService {
	return &AppService{dal: d, cache: c, cryptoKey: cryptoKey}
}

// List returns all applications for the given tenant, each with their entry points.
func (s *AppService) List(ctx context.Context, tenantID string) ([]dal.Application, error) {
	apps, err := s.dal.ListApplications(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for i := range apps {
		apps[i].EntryPoints = s.dal.ListEntryPoints(ctx, apps[i].ID)
	}
	return apps, nil
}

// Get returns a single application with its entry points, scoped to the tenant. Any DAL error maps
// to ErrNotFound to preserve the current API contract.
func (s *AppService) Get(ctx context.Context, tenantID, id string) (dal.Application, error) {
	a, err := s.dal.GetApplication(ctx, tenantID, id)
	if err != nil {
		return dal.Application{}, ErrNotFound
	}
	a.EntryPoints = s.dal.ListEntryPoints(ctx, id)
	return a, nil
}

// Create validates the input, persists under the tenant, and returns the new ID.
func (s *AppService) Create(ctx context.Context, tenantID, name string, enabled *bool) (string, error) {
	if name == "" {
		return "", validation("name is required")
	}
	return s.dal.CreateApplication(ctx, tenantID, name, enabledOrDefault(enabled))
}

// Update persists changes scoped to the tenant and invalidates all EP slugs for the application.
func (s *AppService) Update(ctx context.Context, tenantID, id, name string, enabled *bool) error {
	if err := s.dal.UpdateApplication(ctx, tenantID, id, name, enabledOrDefault(enabled)); err != nil {
		return err
	}
	s.invalidateAppEPs(ctx, id)
	return nil
}

// Delete removes an application scoped to the tenant and invalidates all its EP slugs.
func (s *AppService) Delete(ctx context.Context, tenantID, id string) error {
	s.invalidateAppEPs(ctx, id)
	if err := s.dal.DeleteApplication(ctx, tenantID, id); err != nil {
		return err
	}
	return nil
}

// CreateEntryPoint validates the EP type, persists, and returns the new EP ID.
// Entry points are scoped through their parent application; no additional tenant param needed here.
// No cache invalidation on create (nothing to evict for a new EP).
func (s *AppService) CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled *bool) (string, error) {
	if slug == "" || epType == "" {
		return "", validation("slug and entry_point_type are required")
	}
	if !IsValidEPType(epType) {
		return "", unprocessable("invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
	}
	return s.dal.CreateEntryPoint(ctx, appID, slug, epType, enabledOrDefault(enabled))
}

// UpdateEntryPoint validates the EP type (if provided), persists, and publishes
// cache invalidation. On slug rename: old (tenantID, slug) is published before
// new (tenantID, slug) so the old cache entry is evicted first (critical ordering).
// tenantID is the caller's tenant (from context) and is used as a fallback when
// the old EP lookup returns an empty TenantID (e.g. EP deleted between read and update).
func (s *AppService) UpdateEntryPoint(ctx context.Context, tenantID, epID, appID, slug, epType string, enabled *bool) error {
	if epType != "" && !IsValidEPType(epType) {
		return unprocessable("invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
	}

	// Fetch old (tenantID, slug) before the update for cache invalidation on rename.
	oldTS := s.dal.GetEntryPointTenantAndSlug(ctx, epID, appID)

	// Fall back to the caller's tenantID when the DB lookup returned empty
	// (e.g. row not found between read and update).
	effectiveTenantID := oldTS.TenantID
	if effectiveTenantID == "" {
		effectiveTenantID = tenantID
	}

	if err := s.dal.UpdateEntryPoint(ctx, epID, appID, slug, epType, enabledOrDefault(enabled)); err != nil {
		return err
	}

	// Old entry must be published first so the stale cache entry is evicted before
	// the new slug is registered (critical ordering contract).
	s.publishEP(ctx, effectiveTenantID, oldTS.Slug)
	s.publishEP(ctx, effectiveTenantID, slug)
	return nil
}

// DeleteEntryPoint fetches the (tenantID, slug), deletes the EP, and publishes invalidation.
func (s *AppService) DeleteEntryPoint(ctx context.Context, epID, appID string) error {
	ts := s.dal.GetEntryPointTenantAndSlug(ctx, epID, appID)
	if err := s.dal.DeleteEntryPoint(ctx, epID, appID); err != nil {
		return err
	}
	s.publishEP(ctx, ts.TenantID, ts.Slug)
	return nil
}

// publishEP publishes a tenant-scoped EP cache invalidation payload.
// Payload format: "{tenantID}:{slug}" — matches the format expected by epconfig.Loader.Subscribe.
func (s *AppService) publishEP(ctx context.Context, tenantID, slug string) {
	if s.cache == nil || tenantID == "" || slug == "" {
		return
	}
	_ = s.cache.Publish(ctx, epConfigChannel, tenantID+":"+slug)
}

func (s *AppService) invalidateAppEPs(ctx context.Context, appID string) {
	if s.cache == nil {
		return
	}
	for _, ts := range s.dal.ListEPTenantSlugsForApp(ctx, appID) {
		s.publishEP(ctx, ts.TenantID, ts.Slug)
	}
}

// flushApplicationOrchCaches busts the orchestrator config/locator caches for all
// named app_orchestrators, invalidates the agent registry, and publishes the
// EP config changed event so Go replicas evict their epconfig entries.
func (s *AppService) flushApplicationOrchCaches(ctx context.Context, appID string, orchNames []string) {
	if s.cache == nil {
		return
	}
	for _, name := range orchNames {
		_ = s.cache.Del(ctx, fmt.Sprintf(orchCacheKeyFmt, appID, name))
		_ = s.cache.Del(ctx, fmt.Sprintf(orchLocKeyFmt, name))
	}
	_ = s.cache.Del(ctx, agentRegistryKey)
	_ = s.cache.Publish(ctx, epConfigChannel, appID)
}

// PutRuntime replaces the runtime_config for the application. Returns ErrNotFound
// if the application does not exist or belongs to a different tenant.
func (s *AppService) PutRuntime(ctx context.Context, tenantID, appID string, cfg AppRuntimeConfig) (AppRuntimeConfig, error) {
	// Ensure non-nil slices so they serialize as [] not null
	if cfg.BlockedTokens == nil {
		cfg.BlockedTokens = []string{}
	}
	if cfg.BlockedUserIDs == nil {
		cfg.BlockedUserIDs = []int{}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return AppRuntimeConfig{}, err
	}
	if err := s.dal.UpdateRuntimeConfig(ctx, tenantID, appID, b); err != nil {
		if dal.IsNoRows(err) {
			return AppRuntimeConfig{}, ErrNotFound
		}
		return AppRuntimeConfig{}, err
	}
	// Pre-load orch names for cache flush
	orchNames, _ := s.dal.ListAppOrchestratorNames(ctx, appID)
	s.flushApplicationOrchCaches(ctx, appID, orchNames)
	return cfg, nil
}

// validProviders is the set of supported LLM provider names.
var validProviders = map[string]struct{}{
	"anthropic": {},
	"openai":    {},
	"groq":      {},
	"gemini":    {},
}

// ProviderKeyOut is returned by GET /provider-keys — keys are masked, never plaintext.
type ProviderKeyOut struct {
	Provider string `json:"provider"`
	KeySet   bool   `json:"key_set"`
	KeyHint  string `json:"key_hint,omitempty"` // last 4 chars of the original plaintext key
}

// providerKeyEntry is the JSONB structure stored per provider in provider_keys.
// {"ct": "<AES-GCM ciphertext>", "hint": "<last 4 chars of plaintext>"}
// Legacy rows (plaintext string) are detected by the absence of "ct" and migrated on read.
type providerKeyEntry struct {
	CT   string `json:"ct"`
	Hint string `json:"hint"`
}

// GetProviderKeys returns the key-set status for each provider on the application.
// Plaintext keys are never returned; only a boolean and a 4-char hint.
func (s *AppService) GetProviderKeys(ctx context.Context, tenantID, appID string) ([]ProviderKeyOut, error) {
	raw, err := s.dal.GetProviderKeys(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	entries, err := parseProviderKeys(raw)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderKeyOut, 0, len(entries))
	for p, e := range entries {
		out = append(out, ProviderKeyOut{Provider: p, KeySet: e.CT != "", KeyHint: e.Hint})
	}
	return out, nil
}

// SetProviderKey encrypts the plaintext key with AES-GCM and stores it alongside
// a 4-char hint (extracted before encryption) in the provider_keys JSONB column.
func (s *AppService) SetProviderKey(ctx context.Context, tenantID, appID, provider, plainKey string) error {
	if _, ok := validProviders[provider]; !ok {
		return unprocessable("unsupported provider: " + provider)
	}
	if plainKey == "" {
		return validation("key must not be empty")
	}
	hint := ""
	if len(plainKey) >= 4 {
		hint = plainKey[len(plainKey)-4:]
	}
	ct, err := s.encryptKey(plainKey)
	if err != nil {
		return fmt.Errorf("encrypt provider key: %w", err)
	}
	entry := providerKeyEntry{CT: ct, Hint: hint}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.dal.SetProviderKey(ctx, tenantID, appID, provider, b)
}

// GetPlaintextProviderKey decrypts and returns the raw key for one provider.
// Returns empty string when no key is stored. Never logs the value.
func (s *AppService) GetPlaintextProviderKey(ctx context.Context, tenantID, appID, provider string) (string, error) {
	raw, err := s.dal.GetProviderKeys(ctx, tenantID, appID)
	if err != nil {
		return "", err
	}
	entries, err := parseProviderKeys(raw)
	if err != nil {
		return "", err
	}
	e, ok := entries[provider]
	if !ok || e.CT == "" {
		return "", nil
	}
	return s.decryptKey(e.CT)
}

// encryptKey AES-GCM encrypts plainKey using the service crypto key.
// When no crypto key is configured (tests) the plaintext is stored as-is with an "plain:" prefix
// so it can be round-tripped by decryptKey without error.
func (s *AppService) encryptKey(plainKey string) (string, error) {
	if len(s.cryptoKey) == 0 {
		return "plain:" + plainKey, nil
	}
	return crypto.EncryptStored(s.cryptoKey, plainKey)
}

// decryptKey reverses encryptKey.
// Handles three cases:
//   - "plain:" prefix: test-mode plaintext (no crypto key configured)
//   - "enc:" prefix: AES-GCM ciphertext from crypto.EncryptStored
//   - anything else: legacy plaintext row written before encryption was added; returned as-is
func (s *AppService) decryptKey(ct string) (string, error) {
	if len(s.cryptoKey) == 0 {
		if len(ct) > 6 && ct[:6] == "plain:" {
			return ct[6:], nil
		}
		return ct, nil
	}
	// Legacy plaintext row: crypto.EncryptStored always produces "enc:..." — if the
	// stored value has no such prefix it was written before encryption was introduced.
	if len(ct) < 4 || ct[:4] != "enc:" {
		return ct, nil
	}
	return crypto.DecryptStored(s.cryptoKey, ct)
}

// parseProviderKeys decodes the provider_keys JSONB blob into a map of providerKeyEntry.
// It handles two formats:
//   - New format: {"anthropic": {"ct": "...", "hint": "XXXX"}}
//   - Legacy format: {"anthropic": "sk-ant-..."} (plaintext, written before encryption was added)
//
// Legacy entries are returned with CT set to the raw value so callers can detect and migrate them.
func parseProviderKeys(raw []byte) (map[string]providerKeyEntry, error) {
	// Try new structured format first.
	var structured map[string]providerKeyEntry
	if err := json.Unmarshal(raw, &structured); err == nil {
		// Check whether any entry actually uses the structured schema.
		// A flat string map like {"anthropic": "sk-ant-..."} will also unmarshal here into
		// zero-value structs — we distinguish the two by trying a flat unmarshal only when
		// no entry has an "ct" field AND the flat unmarshal also succeeds.
		// If the structured unmarshal succeeded AND every entry has empty CT, the row is
		// either a legitimate new-format row with no keys set (return empty map) OR a legacy
		// flat-string row (fall through to flat unmarshal).
		// We fall through only when a flat unmarshal of the same bytes also succeeds.
		hasStructured := false
		for _, e := range structured {
			if e.CT != "" || e.Hint != "" {
				hasStructured = true
				break
			}
		}
		if hasStructured {
			return structured, nil
		}
		// All entries are zero-value — could be new-format (no keys set) or legacy flat.
		// Try flat; if flat fails or all values are empty objects, treat as new-format (empty).
		var flat map[string]string
		if err2 := json.Unmarshal(raw, &flat); err2 != nil {
			// Bytes are valid JSON objects but not flat strings — new format with empty keys.
			return map[string]providerKeyEntry{}, nil
		}
		// If every flat value is also empty, we can't distinguish — return empty.
		hasFlat := false
		for _, v := range flat {
			if v != "" {
				hasFlat = true
				break
			}
		}
		if !hasFlat {
			return map[string]providerKeyEntry{}, nil
		}
		// Legacy flat-string row — convert to providerKeyEntry with CT = plaintext.
		out := make(map[string]providerKeyEntry, len(flat))
		for p, v := range flat {
			hint := ""
			if len(v) >= 4 {
				hint = v[len(v)-4:]
			}
			out[p] = providerKeyEntry{CT: v, Hint: hint}
		}
		return out, nil
	}
	// Structured unmarshal itself failed — try legacy flat string map.
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("parse provider_keys: %w", err)
	}
	out := make(map[string]providerKeyEntry, len(flat))
	for p, v := range flat {
		hint := ""
		if len(v) >= 4 {
			hint = v[len(v)-4:]
		}
		// Store the plaintext in CT so GetPlaintextProviderKey can return it.
		// Callers should re-encrypt on next SetProviderKey call.
		out[p] = providerKeyEntry{CT: v, Hint: hint}
	}
	return out, nil
}

// appParamNameRe is the validation pattern for user-defined app param names.
var appParamNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// validAppParamTypes is the set of supported param types.
var validAppParamTypes = map[string]struct{}{
	"secret": {}, "string": {}, "url": {}, "int": {}, "bool": {},
}

// AppGlobalParamUpsertInput is the request body for PUT /app-params/{name}.
type AppGlobalParamUpsertInput struct {
	Value string `json:"value"`
	Type  string `json:"type"` // "secret" | "string" | "url" | "int" | "bool"
}

// GetAppParams returns metadata + fill-status for all app-level named params.
// Secret values are never returned; only is_set and value_hint are exposed.
func (s *AppService) GetAppParams(ctx context.Context, tenantID, appID string) ([]dal.AppGlobalParam, error) {
	raw, err := s.dal.GetAppParams(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return parseAppParams(raw)
}

// SetAppParam encrypts secret params and persists the value.
func (s *AppService) SetAppParam(ctx context.Context, tenantID, appID, name string, input AppGlobalParamUpsertInput) error {
	if !appParamNameRe.MatchString(name) {
		return validation("app param name must match ^[a-z0-9_]{1,64}$")
	}
	if _, ok := validAppParamTypes[input.Type]; !ok {
		return unprocessable("unsupported param type: " + input.Type)
	}
	if input.Value == "" {
		return validation("value must not be empty")
	}
	var valueJSON []byte
	if input.Type == "secret" {
		hint := input.Value
		if len(hint) > 4 {
			hint = hint[len(hint)-4:]
		}
		ct, err := s.encryptKey(input.Value)
		if err != nil {
			return fmt.Errorf("encrypt app param: %w", err)
		}
		entry := providerKeyEntry{CT: ct, Hint: hint}
		valueJSON, err = json.Marshal(entry)
		if err != nil {
			return err
		}
	} else {
		var err error
		valueJSON, err = json.Marshal(input.Value)
		if err != nil {
			return err
		}
	}
	return s.dal.SetAppParam(ctx, tenantID, appID, name, valueJSON)
}

// DeleteAppParam removes one named param.
func (s *AppService) DeleteAppParam(ctx context.Context, tenantID, appID, name string) error {
	if !appParamNameRe.MatchString(name) {
		return validation("invalid app param name")
	}
	return s.dal.DeleteAppParam(ctx, tenantID, appID, name)
}

// GetPlaintextAppParams decrypts all app-level params and returns a name→plaintext map.
// Used by the runtime loader only — never exposed via HTTP.
func (s *AppService) GetPlaintextAppParams(ctx context.Context, tenantID, appID string) (map[string]string, error) {
	raw, err := s.dal.GetAppParams(ctx, tenantID, appID)
	if err != nil {
		return nil, err
	}
	return decryptAppParams(raw, s.decryptKey)
}

// parseAppParams decodes the app_params JSONB blob into []dal.AppGlobalParam.
func parseAppParams(raw []byte) ([]dal.AppGlobalParam, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("parse app_params: %w", err)
	}
	out := make([]dal.AppGlobalParam, 0, len(top))
	for name, valRaw := range top {
		var entry providerKeyEntry
		if json.Unmarshal(valRaw, &entry) == nil && (entry.CT != "" || entry.Hint != "") {
			out = append(out, dal.AppGlobalParam{
				Name:      name,
				Type:      "secret",
				IsSet:     entry.CT != "",
				ValueHint: entry.Hint,
			})
			continue
		}
		var s string
		if json.Unmarshal(valRaw, &s) == nil {
			out = append(out, dal.AppGlobalParam{
				Name:  name,
				Type:  "string",
				IsSet: s != "",
				Value: s,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// decryptAppParams returns the plaintext map for all stored params.
func decryptAppParams(raw []byte, decryptFn func(string) (string, error)) (map[string]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(top))
	for name, valRaw := range top {
		var entry providerKeyEntry
		if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
			plain, err := decryptFn(entry.CT)
			if err != nil {
				continue
			}
			out[name] = plain
			continue
		}
		var s string
		if json.Unmarshal(valRaw, &s) == nil {
			out[name] = s
		}
	}
	return out, nil
}

// SetOrchestratorLLM assigns a provider+model to one app_orchestrators row.
// Validates that the provider is known and has a key stored on the application.
func (s *AppService) SetOrchestratorLLM(ctx context.Context, tenantID, appID, orchID, provider, model string) error {
	if _, ok := validProviders[provider]; !ok {
		return unprocessable("unsupported provider: " + provider)
	}
	if model == "" {
		return validation("model must not be empty")
	}
	keys, err := s.GetProviderKeys(ctx, tenantID, appID)
	if err != nil {
		return ErrNotFound
	}
	hasKey := false
	for _, k := range keys {
		if k.Provider == provider && k.KeySet {
			hasKey = true
			break
		}
	}
	if !hasKey {
		return unprocessable("no API key stored for provider " + provider + " — save one in Runtime settings first")
	}
	if err := s.dal.SetOrchestratorLLM(ctx, appID, orchID, provider, model); err != nil {
		return ErrNotFound
	}
	return nil
}

// SetOrchestratorSummarizer updates summarizer settings for one app_orchestrators row.
func (s *AppService) SetOrchestratorSummarizer(ctx context.Context, tenantID, appID, orchID string, enabled bool, everyN, fallbackN int, provider, model *string) error {
	if everyN < 1 {
		return validation("summarize_every_n_calls must be ≥ 1")
	}
	if fallbackN < 0 {
		return validation("memory_raw_fallback_n must be ≥ 0")
	}
	if provider != nil && *provider != "" {
		if _, ok := validProviders[*provider]; !ok {
			return unprocessable("unsupported summarizer provider: " + *provider)
		}
	}
	if err := s.dal.SetOrchestratorSummarizer(ctx, appID, orchID, enabled, everyN, fallbackN, provider, model); err != nil {
		return ErrNotFound
	}
	return nil
}

// SetOrchestratorMCPServers writes the mcp_servers list for one app_orchestrators row.
// Validates tenant ownership via appID. An empty slice is valid (clears all servers).
func (s *AppService) SetOrchestratorMCPServers(ctx context.Context, tenantID, appID, orchID string, servers []dal.MCPServerAttachment) error {
	// Verify the app belongs to the tenant.
	if _, err := s.dal.GetApplication(ctx, tenantID, appID); err != nil {
		return ErrNotFound
	}
	if servers == nil {
		servers = []dal.MCPServerAttachment{}
	}
	if err := s.dal.SetOrchestratorMCPServers(ctx, appID, orchID, servers); err != nil {
		return ErrNotFound
	}
	return nil
}

// DeleteProviderKey removes the key for one provider from the application.
func (s *AppService) DeleteProviderKey(ctx context.Context, tenantID, appID, provider string) error {
	if _, ok := validProviders[provider]; !ok {
		return unprocessable("unsupported provider: " + provider)
	}
	return s.dal.DeleteProviderKey(ctx, tenantID, appID, provider)
}

// BulkDelete hard-deletes up to 200 applications by UUID. Only applications
// belonging to the tenant are deleted. Returns the count actually deleted.
// Cache flush happens AFTER the database commit (flush is best-effort).
func (s *AppService) BulkDelete(ctx context.Context, tenantID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > 200 {
		return 0, validation("maximum 200 IDs per bulk-delete request")
	}
	// Pre-fetch orch names BEFORE delete, per-app, for cache flush.
	type appOrchNames struct {
		appID string
		names []string
	}
	perApp := make([]appOrchNames, 0, len(ids))
	for _, id := range ids {
		names, _ := s.dal.ListAppOrchestratorNames(ctx, id)
		perApp = append(perApp, appOrchNames{appID: id, names: names})
	}
	deleted, err := s.dal.BulkDeleteApplications(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}
	// Flush caches only after successful DB commit
	for _, ao := range perApp {
		s.flushApplicationOrchCaches(ctx, ao.appID, ao.names)
	}
	return deleted, nil
}
