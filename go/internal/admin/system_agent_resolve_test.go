package admin

import (
	"context"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/crypto"
	"github.com/jackc/pgx/v5"
)

// fakeSystemAgentResolverDAL satisfies systemAgentRoleResolverDAL for
// resolveSystemAgentRole tests. Fields control what each method returns.
type fakeSystemAgentResolverDAL struct {
	cfg    dal.TenantSystemAgentConfig
	cfgErr error

	provider    dal.LLMProvider
	providerErr error

	platformProvider    dal.LLMProvider
	platformProviderErr error

	defaultKey    dal.LLMProviderKey
	defaultKeyErr error

	key    dal.LLMProviderKey
	keyErr error
}

func (f *fakeSystemAgentResolverDAL) GetTenantSystemAgentConfig(_ context.Context, _, _ string) (dal.TenantSystemAgentConfig, error) {
	return f.cfg, f.cfgErr
}
func (f *fakeSystemAgentResolverDAL) GetProviderByNameForTenant(_ context.Context, _, _ string) (dal.LLMProvider, error) {
	return f.provider, f.providerErr
}
func (f *fakeSystemAgentResolverDAL) GetProviderByNamePlatform(_ context.Context, _ string) (dal.LLMProvider, error) {
	return f.platformProvider, f.platformProviderErr
}
func (f *fakeSystemAgentResolverDAL) GetDefaultLLMProviderKey(_ context.Context, _ int64, _ *string) (dal.LLMProviderKey, error) {
	return f.defaultKey, f.defaultKeyErr
}
func (f *fakeSystemAgentResolverDAL) GetLLMProviderKey(_ context.Context, _ int64, _ *string) (dal.LLMProviderKey, error) {
	return f.key, f.keyErr
}

func strp2(s string) *string { return &s }
func i64p2(i int64) *int64   { return &i }

// Fernet key used across these tests — any 32-byte-derivable secret works.
const resolveTestSecret = "system-agent-resolve-test-secret"

func testFernetKey(t *testing.T) []byte {
	t.Helper()
	return crypto.DeriveKey(resolveTestSecret)
}

func encryptForTest(t *testing.T, fernetKey []byte, plaintext string) string {
	t.Helper()
	enc, err := crypto.EncryptStored(fernetKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptStored: %v", err)
	}
	return enc
}

// ── No row / platform fallback ──────────────────────────────────────────────────

func TestResolveSystemAgentRole_NoRow_FallsBackToPlatform(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{cfgErr: pgx.ErrNoRows}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, testFernetKey(t), "tid", "classifier",
		"anthropic", "claude-haiku-4-5-20251001", "sk-platform-key", "", "")
	if !ok {
		t.Fatal("want ok=true, platform fallback should apply when no row exists")
	}
	if resolved.Provider != "anthropic" || resolved.APIKey != "sk-platform-key" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolveSystemAgentRole_NoRow_NoPlatformConfig_ReturnsNotOK(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{cfgErr: pgx.ErrNoRows}
	_, ok := resolveSystemAgentRole(context.Background(), d, testFernetKey(t), "tid", "classifier",
		"", "", "", "", "")
	if ok {
		t.Error("want ok=false when there is no row and no platform fallback configured")
	}
}

// ── General mode ─────────────────────────────────────────────────────────────────

func TestResolveSystemAgentRole_GeneralMode_MissingProviderName_ReturnsNotOK(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{cfg: dal.TenantSystemAgentConfig{Mode: "general"}}
	_, ok := resolveSystemAgentRole(context.Background(), d, testFernetKey(t), "tid", "classifier",
		"anthropic", "m", "sk-platform", "", "")
	if ok {
		t.Error("want ok=false when general mode has no provider_name — must not silently use platform config")
	}
}

func TestResolveSystemAgentRole_GeneralMode_NoUsableKey_NeverFallsBackToPlatform(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{
		cfg:           dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
		provider:      dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		defaultKeyErr: pgx.ErrNoRows, // tenant has no key for this provider
	}
	_, ok := resolveSystemAgentRole(context.Background(), d, testFernetKey(t), "tid", "classifier",
		"anthropic", "platform-model", "sk-PLATFORM-KEY-MUST-NEVER-BE-USED", "", "")
	if ok {
		t.Error("hard rule violated: general mode with no usable tenant key must never fall back to a platform key")
	}
}

func TestResolveSystemAgentRole_GeneralMode_UsesDefaultKeyWhenKeyIDNil(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-tenant-default-key")
	d := &fakeSystemAgentResolverDAL{
		cfg:        dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
		provider:   dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		defaultKey: dal.LLMProviderKey{ID: 5, APIKeyEncrypted: enc, IsDefault: true},
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, fernetKey, "tid", "classifier",
		"", "", "", "", "")
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.APIKey != "sk-tenant-default-key" || resolved.Provider != "anthropic" || resolved.Model != "claude-sonnet-4-6" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolveSystemAgentRole_GeneralMode_UsesExplicitKeyID(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-tenant-explicit-key")
	d := &fakeSystemAgentResolverDAL{
		cfg:      dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("openai"), KeyID: i64p2(9)},
		provider: dal.LLMProvider{ID: 2, Name: "openai", DefaultModel: "gpt-4o-mini"},
		key:      dal.LLMProviderKey{ID: 9, APIKeyEncrypted: enc},
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, fernetKey, "tid", "card_synthesizer",
		"", "", "", "", "")
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.APIKey != "sk-tenant-explicit-key" || resolved.Provider != "openai" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolveSystemAgentRole_GeneralMode_UsesStoredGeneralModel(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-tenant-default-key")
	d := &fakeSystemAgentResolverDAL{
		cfg:        dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic"), GeneralModel: strp2("claude-haiku-4-5")},
		provider:   dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		defaultKey: dal.LLMProviderKey{ID: 5, APIKeyEncrypted: enc, IsDefault: true},
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, fernetKey, "tid", "classifier",
		"", "", "", "", "")
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Model != "claude-haiku-4-5" {
		t.Errorf("want stored general_model to override provider.DefaultModel, got %+v", resolved)
	}
}

func TestResolveSystemAgentRole_GeneralMode_NoGeneralModel_FallsBackToProviderDefault(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-tenant-default-key")
	d := &fakeSystemAgentResolverDAL{
		cfg:        dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
		provider:   dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		defaultKey: dal.LLMProviderKey{ID: 5, APIKeyEncrypted: enc, IsDefault: true},
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, fernetKey, "tid", "classifier",
		"", "", "", "", "")
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Model != "claude-sonnet-4-6" {
		t.Errorf("want provider.DefaultModel used when general_model unset, got %+v", resolved)
	}
}

// ── Custom mode ───────────────────────────────────────────────────────────────────

func TestResolveSystemAgentRole_CustomMode_UsesOwnFields(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom-key")
	d := &fakeSystemAgentResolverDAL{
		cfg: dal.TenantSystemAgentConfig{
			Mode: "custom", CustomProvider: strp2("groq"), CustomModel: strp2("llama-3.3-70b-versatile"),
			CustomAPIKeyEncrypted: &enc, CustomSystemPrompt: strp2("custom prompt"),
		},
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, fernetKey, "tid", "classifier",
		"anthropic", "platform-model", "sk-platform-must-not-be-used", "", "")
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Provider != "groq" || resolved.APIKey != "sk-custom-key" || resolved.SystemPrompt != "custom prompt" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolveSystemAgentRole_CustomMode_UnsetFields_FallsBackToPlatform(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{
		cfg: dal.TenantSystemAgentConfig{Mode: "custom"}, // row exists but never filled in
	}
	resolved, ok := resolveSystemAgentRole(context.Background(), d, testFernetKey(t), "tid", "classifier",
		"anthropic", "platform-model", "sk-platform-key", "", "")
	if !ok {
		t.Fatal("want ok=true via platform fallback")
	}
	if resolved.Provider != "anthropic" || resolved.APIKey != "sk-platform-key" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

// ── resolvePlatformSystemAgentRole (the-M admin's own general/custom mode, db/108) ──

func TestResolvePlatformSystemAgentRole_Disabled_ReturnsNotOK(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{}
	_, ok := resolvePlatformSystemAgentRole(context.Background(), d, testFernetKey(t), saRoleStored{Enabled: false})
	if ok {
		t.Error("want ok=false when role is disabled")
	}
}

func TestResolvePlatformSystemAgentRole_CustomMode_UsesOwnFields(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-platform-custom")
	provider := "anthropic"
	model := "claude-sonnet-4-6"
	d := &fakeSystemAgentResolverDAL{}
	resolved, ok := resolvePlatformSystemAgentRole(context.Background(), d, fernetKey, saRoleStored{
		Enabled: true, Mode: "custom", Provider: &provider, Model: &model, APIKeyEncrypted: &enc,
	})
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Provider != "anthropic" || resolved.Model != "claude-sonnet-4-6" || resolved.APIKey != "sk-platform-custom" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolvePlatformSystemAgentRole_ModeEmpty_TreatedAsCustom(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-legacy-role")
	provider := "openai"
	model := "gpt-4o-mini"
	d := &fakeSystemAgentResolverDAL{}
	resolved, ok := resolvePlatformSystemAgentRole(context.Background(), d, fernetKey, saRoleStored{
		Enabled: true, Mode: "", Provider: &provider, Model: &model, APIKeyEncrypted: &enc,
	})
	if !ok {
		t.Fatal("want ok=true — a role saved before this feature existed has no mode field and must still work")
	}
	if resolved.Provider != "openai" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolvePlatformSystemAgentRole_GeneralMode_MissingProvider_ReturnsNotOK(t *testing.T) {
	d := &fakeSystemAgentResolverDAL{}
	_, ok := resolvePlatformSystemAgentRole(context.Background(), d, testFernetKey(t), saRoleStored{Enabled: true, Mode: "general"})
	if ok {
		t.Error("want ok=false when general mode has no provider selected")
	}
}

func TestResolvePlatformSystemAgentRole_GeneralMode_UsesDefaultKeyWhenKeyIDNil(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-platform-default-key")
	provider := "groq"
	d := &fakeSystemAgentResolverDAL{
		platformProvider: dal.LLMProvider{ID: 3, Name: "groq", DefaultModel: "llama-3.3-70b-versatile"},
		defaultKey:       dal.LLMProviderKey{ID: 7, APIKeyEncrypted: enc},
	}
	resolved, ok := resolvePlatformSystemAgentRole(context.Background(), d, fernetKey, saRoleStored{
		Enabled: true, Mode: "general", Provider: &provider,
	})
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Provider != "groq" || resolved.Model != "llama-3.3-70b-versatile" || resolved.APIKey != "sk-platform-default-key" {
		t.Errorf("unexpected resolved value: %+v", resolved)
	}
}

func TestResolvePlatformSystemAgentRole_GeneralMode_UsesStoredGeneralModel(t *testing.T) {
	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-platform-default-key")
	provider := "groq"
	d := &fakeSystemAgentResolverDAL{
		platformProvider: dal.LLMProvider{ID: 3, Name: "groq", DefaultModel: "llama-3.3-70b-versatile"},
		defaultKey:       dal.LLMProviderKey{ID: 7, APIKeyEncrypted: enc},
	}
	resolved, ok := resolvePlatformSystemAgentRole(context.Background(), d, fernetKey, saRoleStored{
		Enabled: true, Mode: "general", Provider: &provider, GeneralModel: strp2("llama-3.1-8b-instant"),
	})
	if !ok {
		t.Fatal("want ok=true")
	}
	if resolved.Model != "llama-3.1-8b-instant" {
		t.Errorf("want stored GeneralModel to override provider.DefaultModel, got %+v", resolved)
	}
}

func TestResolvePlatformSystemAgentRole_GeneralMode_NoUsableKey_ReturnsNotOK(t *testing.T) {
	provider := "gemini"
	d := &fakeSystemAgentResolverDAL{
		platformProvider: dal.LLMProvider{ID: 4, Name: "gemini", DefaultModel: "gemini-2.0-flash"},
		defaultKeyErr:    pgx.ErrNoRows, // no platform key saved for this provider yet
	}
	_, ok := resolvePlatformSystemAgentRole(context.Background(), d, testFernetKey(t), saRoleStored{
		Enabled: true, Mode: "general", Provider: &provider,
	})
	if ok {
		t.Error("want ok=false when the platform has no usable key for the selected provider")
	}
}
