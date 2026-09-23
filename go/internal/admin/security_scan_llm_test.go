package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/jackc/pgx/v5"
)

// fakeSecurityScanDAL satisfies classifierDAL for llmCardAnalysis tests.
type fakeSecurityScanDAL struct {
	fakeSystemAgentResolverDAL
	configRow *dal.ConfigRow
	configErr error
}

func (f *fakeSecurityScanDAL) GetConfig(_ context.Context, _ string) (*dal.ConfigRow, error) {
	return f.configRow, f.configErr
}

func TestLLMCardAnalysis_NoConfig_ReturnsDegraded(t *testing.T) {
	d := &fakeSecurityScanDAL{configErr: pgx.ErrNoRows, fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{cfgErr: pgx.ErrNoRows}}
	result := llmCardAnalysis(context.Background(), d, testFernetKey(t), "tid", scanAgentPayload{Slug: "agent1"})
	if len(result.Findings) != 0 {
		t.Errorf("want zero findings when degraded, got %d", len(result.Findings))
	}
	if result.Summary == "" {
		t.Error("want a non-empty degraded summary")
	}
}

// TestLLMCardAnalysis_GeneralMode_NoUsableKey_ReturnsDegraded proves the hard
// rule end-to-end for the security_scanner role: a tenant in general mode
// with no usable key must degrade even though a platform key IS configured.
func TestLLMCardAnalysis_GeneralMode_NoUsableKey_ReturnsDegraded(t *testing.T) {
	fernetKey := testFernetKey(t)
	d := &fakeSecurityScanDAL{
		configRow: securityScannerPlatformConfigRow(t, fernetKey, "sk-PLATFORM-MUST-NOT-BE-USED"),
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg:           dal.TenantSystemAgentConfig{Mode: "general", ProviderName: strp2("anthropic")},
			provider:      dal.LLMProvider{ID: 1, Name: "anthropic", DefaultModel: "claude-sonnet-4-6"},
			defaultKeyErr: pgx.ErrNoRows,
		},
	}
	result := llmCardAnalysis(context.Background(), d, fernetKey, "tid", scanAgentPayload{Slug: "agent1"})
	if len(result.Findings) != 0 {
		t.Errorf("hard rule violated: want zero findings, got %+v", result.Findings)
	}
}

func TestLLMCardAnalysis_CustomMode_DispatchesToResolvedProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"looks fine\",\"findings\":[{\"id\":\"scope\",\"label\":\"Scope\",\"status\":\"pass\",\"risk\":\"low\",\"detail\":\"ok\",\"recommendation\":\"none\"}]}"}}]}`))
	}))
	defer srv.Close()

	fernetKey := testFernetKey(t)
	enc := encryptForTest(t, fernetKey, "sk-custom")
	d := &fakeSecurityScanDAL{
		fakeSystemAgentResolverDAL: fakeSystemAgentResolverDAL{
			cfg: dal.TenantSystemAgentConfig{
				Mode: "custom", CustomProvider: strp2("custom-provider"), CustomModel: strp2("m"),
				CustomAPIKeyEncrypted: &enc, CustomBaseURL: strp2(srv.URL),
			},
		},
	}
	result := llmCardAnalysis(context.Background(), d, fernetKey, "tid", scanAgentPayload{
		Slug: "agent1", Description: "does risky things", EndpointURL: "https://example.com",
	})
	if result.Summary != "looks fine" || len(result.Findings) != 1 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func securityScannerPlatformConfigRow(t *testing.T, fernetKey []byte, apiKey string) *dal.ConfigRow {
	t.Helper()
	enc := encryptForTest(t, fernetKey, apiKey)
	value := `{"roles":{"security_scanner":{"enabled":true,"provider":"anthropic","model":"claude-haiku-4-5-20251001","api_key_encrypted":"` + enc + `"}}}`
	return &dal.ConfigRow{Key: "system_agents", Value: []byte(value)}
}

// ── mergeSecurityScanResult ───────────────────────────────────────────────────

func TestMergeSecurityScanResult_Degraded_AddsAnalysisFindingAndPenalty(t *testing.T) {
	scanResult := map[string]any{
		"score":    float64(70),
		"risk":     "medium",
		"findings": []any{map[string]any{"id": "tls", "risk": "low"}},
	}
	mergeSecurityScanResult(scanResult, securityScanLLMResult{Summary: "probes only"})

	findings, _ := scanResult["findings"].([]any)
	if len(findings) != 2 {
		t.Fatalf("want 2 findings (1 probe + 1 degraded), got %d", len(findings))
	}
	last := findings[len(findings)-1].(map[string]any)
	if last["id"] != "analysis" {
		t.Errorf("want degraded finding appended last, got %+v", last)
	}
	if scanResult["score"].(float64) != 60 {
		t.Errorf("want score reduced by 10 for degraded, got %v", scanResult["score"])
	}
}

func TestMergeSecurityScanResult_HighRiskFinding_AppliesFullPenalty(t *testing.T) {
	scanResult := map[string]any{
		"score":    float64(100),
		"findings": []any{},
	}
	mergeSecurityScanResult(scanResult, securityScanLLMResult{
		Summary: "risky",
		Findings: []map[string]any{
			{"id": "scope", "risk": "high"},
		},
	})
	if scanResult["score"].(float64) != 80 {
		t.Errorf("want score -20 for one high-risk finding, got %v", scanResult["score"])
	}
	if scanResult["risk"] != "low" {
		t.Errorf("want risk=low at score 80, got %v", scanResult["risk"])
	}
}

func TestMergeSecurityScanResult_PenaltyCappedAt40(t *testing.T) {
	scanResult := map[string]any{"score": float64(100), "findings": []any{}}
	mergeSecurityScanResult(scanResult, securityScanLLMResult{
		Summary: "very risky",
		Findings: []map[string]any{
			{"id": "a", "risk": "high"}, {"id": "b", "risk": "high"},
			{"id": "c", "risk": "high"}, {"id": "d", "risk": "high"},
		},
	})
	if scanResult["score"].(float64) != 60 {
		t.Errorf("want llm penalty capped at 40 (100-40=60), got %v", scanResult["score"])
	}
}

func TestMergeSecurityScanResult_UsesLLMSummaryWhenPresent(t *testing.T) {
	scanResult := map[string]any{"score": float64(90), "summary": "probes summary", "findings": []any{}}
	mergeSecurityScanResult(scanResult, securityScanLLMResult{
		Summary:  "llm summary wins",
		Findings: []map[string]any{{"id": "x", "risk": "low"}},
	})
	if scanResult["summary"] != "llm summary wins" {
		t.Errorf("want LLM summary to override probes summary, got %v", scanResult["summary"])
	}
}
