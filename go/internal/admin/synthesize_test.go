package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
)

// fakeSynthesizerDAL satisfies synthesizerDAL for synthesizeAppCard tests.
type fakeSynthesizerDAL struct {
	fakeSystemAgentResolverDAL
	configRow *dal.ConfigRow
	configErr error
}

func (f *fakeSynthesizerDAL) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return f.configRow, f.configErr
}

func synthesizerPlatformConfigRow(t *testing.T, fernetKey []byte, apiKey string) *dal.ConfigRow {
	t.Helper()
	enc := encryptForTest(t, fernetKey, apiKey)
	value := `{"roles":{"card_synthesizer":{"enabled":true,"provider":"anthropic","model":"claude-haiku-4-5-20251001","api_key_encrypted":"` + enc + `"}}}`
	return &dal.ConfigRow{Key: "system_agents", Value: []byte(value)}
}

func TestSynthesizeAppCard_NoConfig_ReturnsNil(t *testing.T) {
	d := &fakeSynthesizerDAL{configErr: pgx.ErrNoRows, fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{cfgErr: pgx.ErrNoRows}}
	card := synthesizeAppCard(context.Background(), d, testFernetKey(t), "tid", "Orch", "purpose", nil)
	if card != nil {
		t.Errorf("want nil card with no config anywhere, got %+v", card)
	}
}

// TestSynthesizeAppCard_GeneralMode_NoUsableKey_ReturnsNil proves the hard
// rule end-to-end: a tenant in general mode with no usable key must degrade
// to nil even though a platform key IS configured.
func TestSynthesizeAppCard_GeneralMode_NoUsableKey_ReturnsNil(t *testing.T) {
	fernetKey := testFernetKey(t)
	d := &fakeSynthesizerDAL{
		configRow: synthesizerPlatformConfigRow(t, fernetKey, "sk-PLATFORM-MUST-NOT-BE-USED"),
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg:           dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
			provider:      dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
			defaultKeyErr: pgx.ErrNoRows,
		},
	}
	card := synthesizeAppCard(context.Background(), d, fernetKey, "tid", "Orch", "purpose", nil)
	if card != nil {
		t.Errorf("hard rule violated: want nil card, got %+v", card)
	}
}

func TestSynthesizeAppCard_CustomMode_DispatchesToResolvedProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"name\":\"MyApp\",\"description\":\"does stuff\",\"skills\":[]}"}}]}`))
	}))
	defer srv.Close()

	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom")
	d := &fakeSynthesizerDAL{
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg: dal.TenantSystemAgentConfig{
				Mode: "custom", CustomProvider: strp2("custom-provider"), CustomModel: strp2("m"),
				CustomAPIKeyEncrypted: &enc, CustomBaseURL: strp2(srv.URL),
			},
		},
	}
	card := synthesizeAppCard(context.Background(), d, fernetKey, "tid", "Orch", "purpose", []subAgentSummary{
		{DisplayName: "sub1", Description: "does things"},
	})
	if card == nil {
		t.Fatal("want non-nil card")
	}
	if card["name"] != "MyApp" {
		t.Errorf("want name=MyApp from mocked custom provider response, got %+v", card)
	}
}

func TestSynthesizeAppCard_CustomMode_UsesCustomSystemPrompt(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		capturedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"name\":\"x\",\"description\":\"y\",\"skills\":[]}"}}]}`))
	}))
	defer srv.Close()

	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom")
	d := &fakeSynthesizerDAL{
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg: dal.TenantSystemAgentConfig{
				Mode: "custom", CustomProvider: strp2("custom-provider"), CustomModel: strp2("m"),
				CustomAPIKeyEncrypted: &enc, CustomBaseURL: strp2(srv.URL),
				CustomSystemPrompt: strp2("MY UNIQUE CUSTOM PROMPT MARKER"),
			},
		},
	}
	_ = synthesizeAppCard(context.Background(), d, fernetKey, "tid", "Orch", "purpose", nil)
	if capturedBody == "" {
		t.Fatal("request body was not captured")
	}
	if !strings.Contains(capturedBody, "MY UNIQUE CUSTOM PROMPT MARKER") {
		t.Errorf("want custom system_prompt in request body, got %s", capturedBody)
	}
}
