package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
)

// fakeClassifierDAL satisfies classifierDAL for classifyAgent tests.
type fakeClassifierDAL struct {
	fakeSystemAgentResolverDAL
	configRow *dal.ConfigRow
	configErr error
}

func (f *fakeClassifierDAL) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return f.configRow, f.configErr
}

func classifierPlatformConfigRow(t *testing.T, fernetKey []byte, apiKey string) *dal.ConfigRow {
	t.Helper()
	enc := encryptForTest(t, fernetKey, apiKey)
	value := `{"roles":{"classifier":{"enabled":true,"model":"claude-haiku-4-5-20251001","api_key_encrypted":"` + enc + `"}}}`
	return &dal.ConfigRow{Key: "system_agents", Value: []byte(value)}
}

// TestClassifyAgent_NoConfig_ReturnsEmpty verifies the best-effort contract:
// no platform config and no tenant row must degrade silently, not error.
func TestClassifyAgent_NoConfig_ReturnsEmpty(t *testing.T) {
	d := &fakeClassifierDAL{configErr: pgx.ErrNoRows, fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{cfgErr: pgx.ErrNoRows}}
	category, icon := classifyAgent(context.Background(), d, testFernetKey(t), "tid", "Agent", "desc", nil)
	if category != "" || icon != "" {
		t.Errorf("want empty category/icon with no config anywhere, got (%q, %q)", category, icon)
	}
}

// TestClassifyAgent_GeneralMode_NoUsableKey_ReturnsEmpty proves the hard rule
// end-to-end through classifyAgent, not just the resolver in isolation: a
// tenant in general mode with no usable key must degrade silently even though
// a platform key IS configured — the platform key must never be substituted.
func TestClassifyAgent_GeneralMode_NoUsableKey_ReturnsEmpty(t *testing.T) {
	fernetKey := testFernetKey(t)
	d := &fakeClassifierDAL{
		configRow: classifierPlatformConfigRow(t, fernetKey, "sk-PLATFORM-MUST-NOT-BE-USED"),
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg:           dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
			provider:      dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
			defaultKeyErr: pgx.ErrNoRows,
		},
	}
	category, icon := classifyAgent(context.Background(), d, fernetKey, "tid", "Agent", "desc", nil)
	if category != "" || icon != "" {
		t.Errorf("hard rule violated: want empty result, got (%q, %q)", category, icon)
	}
}

// TestClassifyAgent_CustomMode_DispatchesToResolvedProvider proves classifyAgent
// no longer hardcodes Anthropic — a tenant's custom-mode "openai"-shaped
// provider (dispatched via the default/base_url branch) is called and its
// response parsed.
func TestClassifyAgent_CustomMode_DispatchesToResolvedProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"category\":\"Coding\",\"icon\":\"code\"}"}}]}`))
	}))
	defer srv.Close()

	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom")
	d := &fakeClassifierDAL{
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg: dal.TenantSystemAgentConfig{
				Mode: "custom", CustomProvider: strp2("custom-provider"), CustomModel: strp2("m"),
				CustomAPIKeyEncrypted: &enc, CustomBaseURL: strp2(srv.URL),
			},
		},
	}
	category, icon := classifyAgent(context.Background(), d, fernetKey, "tid", "MyAgent", "does coding stuff", nil)
	if category != "Coding" || icon != "code" {
		t.Errorf("want (Coding, code) from the mocked custom provider response, got (%q, %q)", category, icon)
	}
}

// TestClassifyAgent_InvalidCategory_FallsBackToAgent verifies the existing
// validation behavior still applies regardless of which provider resolved.
func TestClassifyAgent_InvalidCategory_FallsBackToAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"category\":\"NotARealCategory\",\"icon\":\"###\"}"}}]}`))
	}))
	defer srv.Close()

	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom")
	d := &fakeClassifierDAL{
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg: dal.TenantSystemAgentConfig{
				Mode: "custom", CustomProvider: strp2("custom-provider"), CustomModel: strp2("m"),
				CustomAPIKeyEncrypted: &enc, CustomBaseURL: strp2(srv.URL),
			},
		},
	}
	category, icon := classifyAgent(context.Background(), d, fernetKey, "tid", "MyAgent", "desc", nil)
	if category != "Agent" || icon != "smart_toy" {
		t.Errorf("want fallback (Agent, smart_toy) for invalid category/icon, got (%q, %q)", category, icon)
	}
}
