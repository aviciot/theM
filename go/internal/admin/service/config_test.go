package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// ── Monitoring — GetMonitoring ─────────────────────────────────────────────

func TestGetMonitoring_NoRow_ReturnsDefaults(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{configRow: nil})
	cfg, err := svc.GetMonitoring(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HeatmapLow != 1 || cfg.HeatmapMedium != 10 || cfg.HeatmapHigh != 50 {
		t.Errorf("unexpected heatmap defaults: %+v", cfg)
	}
	if cfg.EdgeThin != 1 || cfg.EdgeMedium != 10 || cfg.EdgeThick != 50 {
		t.Errorf("unexpected edge defaults: %+v", cfg)
	}
	if cfg.PanelMaxSessions != 50 || cfg.StatsWindowSeconds != 300 {
		t.Errorf("unexpected panel defaults: %+v", cfg)
	}
}

func TestGetMonitoring_StoredRow_MergesOverDefaults(t *testing.T) {
	stored := map[string]any{
		"heatmap_low":    5,
		"heatmap_medium": 20,
		"heatmap_high":   100,
	}
	b, _ := json.Marshal(stored)
	svc := service.NewConfigService(&fakeDal{configRow: &dal.ConfigRow{Key: "monitoring", Value: b}})
	cfg, err := svc.GetMonitoring(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HeatmapLow != 5 || cfg.HeatmapMedium != 20 || cfg.HeatmapHigh != 100 {
		t.Errorf("stored values not applied: %+v", cfg)
	}
	// Fields not in stored JSON must remain at defaults.
	if cfg.EdgeThin != 1 || cfg.EdgeMedium != 10 || cfg.EdgeThick != 50 {
		t.Errorf("edge defaults overwritten unexpectedly: %+v", cfg)
	}
	if cfg.StatsWindowSeconds != 300 {
		t.Errorf("stats_window_seconds default overwritten: %+v", cfg)
	}
}

func TestGetMonitoring_DALError_Propagates(t *testing.T) {
	want := errors.New("db down")
	svc := service.NewConfigService(&fakeDal{configErr: want})
	_, err := svc.GetMonitoring(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("expected wrapped dal error, got %v", err)
	}
}

// ── Monitoring — PutMonitoring ─────────────────────────────────────────────

func TestPutMonitoring_ValidInput_Upserts(t *testing.T) {
	fd := &fakeDal{}
	svc := service.NewConfigService(fd)
	in := service.MonitoringConfig{
		HeatmapLow: 2, HeatmapMedium: 15, HeatmapHigh: 60,
		EdgeThin: 3, EdgeMedium: 20, EdgeThick: 80,
		PanelMaxSessions: 100, StatsWindowSeconds: 600,
	}
	out, err := svc.PutMonitoring(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != in {
		t.Errorf("returned value differs from input: got %+v", out)
	}
	if fd.upsertConfigKey != "monitoring" {
		t.Errorf("wrong config key upserted: %s", fd.upsertConfigKey)
	}
	var stored service.MonitoringConfig
	if err := json.Unmarshal(fd.upsertConfigValue, &stored); err != nil {
		t.Fatalf("upserted value not valid JSON: %v", err)
	}
	if stored != in {
		t.Errorf("upserted JSON differs from input: %+v", stored)
	}
}

func TestPutMonitoring_InvalidHeatmapOrder_ReturnsValidationError(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{})
	bad := service.MonitoringConfig{
		HeatmapLow: 50, HeatmapMedium: 10, HeatmapHigh: 1, // wrong order
		EdgeThin: 1, EdgeMedium: 10, EdgeThick: 50,
		PanelMaxSessions: 50, StatsWindowSeconds: 300,
	}
	_, err := svc.PutMonitoring(context.Background(), bad)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestPutMonitoring_InvalidEdgeOrder_ReturnsValidationError(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{})
	bad := service.MonitoringConfig{
		HeatmapLow: 1, HeatmapMedium: 10, HeatmapHigh: 50,
		EdgeThin: 50, EdgeMedium: 10, EdgeThick: 1, // wrong order
		PanelMaxSessions: 50, StatsWindowSeconds: 300,
	}
	_, err := svc.PutMonitoring(context.Background(), bad)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// ── LLM Routing — GetLLMRouting ────────────────────────────────────────────

func TestGetLLMRouting_NoRow_ReturnsDefaults(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{configRow: nil})
	cfg, err := svc.GetLLMRouting(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("unexpected default_provider: %s", cfg.DefaultProvider)
	}
	if cfg.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("unexpected default_model: %s", cfg.DefaultModel)
	}
	if cfg.FallbackProvider != nil || cfg.FallbackModel != nil {
		t.Errorf("fallback fields should be nil by default: %+v", cfg)
	}
}

func TestGetLLMRouting_StoredRow_Returned(t *testing.T) {
	fp := "openai"
	fm := "gpt-4o"
	stored := service.LLMRoutingConfig{
		DefaultProvider: "openai", DefaultModel: "gpt-4o-mini",
		FallbackProvider: &fp, FallbackModel: &fm,
	}
	b, _ := json.Marshal(stored)
	svc := service.NewConfigService(&fakeDal{configRow: &dal.ConfigRow{Key: "llm_routing", Value: b}})
	cfg, err := svc.GetLLMRouting(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultProvider != "openai" || cfg.DefaultModel != "gpt-4o-mini" {
		t.Errorf("unexpected stored values: %+v", cfg)
	}
	if cfg.FallbackProvider == nil || *cfg.FallbackProvider != "openai" {
		t.Errorf("fallback_provider not restored: %+v", cfg)
	}
}

// ── LLM Routing — PutLLMRouting ────────────────────────────────────────────

func TestPutLLMRouting_ValidInput_Upserts(t *testing.T) {
	fd := &fakeDal{}
	svc := service.NewConfigService(fd)
	fp := "openai"
	fm := "gpt-4o"
	in := service.LLMRoutingConfig{
		DefaultProvider: "anthropic", DefaultModel: "claude-opus-4-8",
		FallbackProvider: &fp, FallbackModel: &fm,
	}
	out, err := svc.PutLLMRouting(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.DefaultProvider != in.DefaultProvider || out.DefaultModel != in.DefaultModel {
		t.Errorf("returned value differs from input: %+v", out)
	}
	if fd.upsertConfigKey != "llm_routing" {
		t.Errorf("wrong config key upserted: %s", fd.upsertConfigKey)
	}
	var stored service.LLMRoutingConfig
	if err := json.Unmarshal(fd.upsertConfigValue, &stored); err != nil {
		t.Fatalf("upserted value not valid JSON: %v", err)
	}
	if stored.DefaultProvider != in.DefaultProvider {
		t.Errorf("upserted JSON differs from input: %+v", stored)
	}
}

// ── Temporal config — GetTemporalPlatformConfig ────────────────────────────

// TC-SVC-1: No stored row → returns hardcoded defaults.
func TestGetTemporalPlatformConfig_NoRow_ReturnsDefaults(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{temporalCfg: nil})
	cfg, err := svc.GetTemporalPlatformConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxConcurrentWorkflows == nil || *cfg.MaxConcurrentWorkflows != 10 {
		t.Errorf("expected MaxConcurrentWorkflows=10, got %v", cfg.MaxConcurrentWorkflows)
	}
	if cfg.WorkflowTimeoutS == nil || *cfg.WorkflowTimeoutS != 3600 {
		t.Errorf("expected WorkflowTimeoutS=3600, got %v", cfg.WorkflowTimeoutS)
	}
	if cfg.ActivityTimeoutS == nil || *cfg.ActivityTimeoutS != 600 {
		t.Errorf("expected ActivityTimeoutS=600, got %v", cfg.ActivityTimeoutS)
	}
	if cfg.RetryMaxAttempts == nil || *cfg.RetryMaxAttempts != 3 {
		t.Errorf("expected RetryMaxAttempts=3, got %v", cfg.RetryMaxAttempts)
	}
}

// TC-SVC-2: Stored row with partial override → merged over defaults.
func TestGetTemporalPlatformConfig_StoredRow_MergesOverDefaults(t *testing.T) {
	wt := 7200
	svc := service.NewConfigService(&fakeDal{temporalCfg: &dal.TemporalConfig{WorkflowTimeoutS: &wt}})
	cfg, err := svc.GetTemporalPlatformConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkflowTimeoutS == nil || *cfg.WorkflowTimeoutS != 7200 {
		t.Errorf("expected stored WorkflowTimeoutS=7200, got %v", cfg.WorkflowTimeoutS)
	}
	if cfg.ActivityTimeoutS == nil || *cfg.ActivityTimeoutS != 600 {
		t.Errorf("expected ActivityTimeoutS default=600 unchanged, got %v", cfg.ActivityTimeoutS)
	}
}

// TC-SVC-3: DAL error → propagates.
func TestGetTemporalPlatformConfig_DALError_Propagates(t *testing.T) {
	want := errors.New("db down")
	svc := service.NewConfigService(&fakeDal{temporalCfgErr: want})
	_, err := svc.GetTemporalPlatformConfig(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("expected wrapped dal error, got %v", err)
	}
}

// TC-SVC-4: PutTemporalPlatformConfig with valid input → upserts.
func TestPutTemporalPlatformConfig_ValidInput_Upserts(t *testing.T) {
	mc, wt, at, ra := 5, 1800, 300, 2
	svc := service.NewConfigService(&fakeDal{})
	in := dal.TemporalConfig{
		MaxConcurrentWorkflows: &mc,
		WorkflowTimeoutS:       &wt,
		ActivityTimeoutS:       &at,
		RetryMaxAttempts:       &ra,
	}
	out, err := svc.PutTemporalPlatformConfig(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.MaxConcurrentWorkflows == nil || *out.MaxConcurrentWorkflows != 5 {
		t.Errorf("expected MaxConcurrentWorkflows=5, got %v", out.MaxConcurrentWorkflows)
	}
}

// TC-SVC-5: PutTemporalPlatformConfig with negative retry → validation error.
func TestPutTemporalPlatformConfig_InvalidRetry_ReturnsValidationError(t *testing.T) {
	neg := -1
	svc := service.NewConfigService(&fakeDal{})
	_, err := svc.PutTemporalPlatformConfig(context.Background(), dal.TemporalConfig{RetryMaxAttempts: &neg})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// TC-SVC-6: PutTemporalPlatformConfig with zero max_concurrent_workflows → validation error.
func TestPutTemporalPlatformConfig_ZeroMaxConcurrent_ReturnsValidationError(t *testing.T) {
	zero := 0
	svc := service.NewConfigService(&fakeDal{})
	_, err := svc.PutTemporalPlatformConfig(context.Background(), dal.TemporalConfig{MaxConcurrentWorkflows: &zero})
	if err == nil {
		t.Fatal("expected validation error for max_concurrent_workflows=0, got nil")
	}
}

// ── AppFlow trace log-verbosity (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4) ─────

// LV-SVC-1: no stored row → default "status".
func TestGetLogVerbosity_NoRow_ReturnsDefault(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{})
	v, err := svc.GetLogVerbosity(context.Background(), "tenant-1", "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != dal.DefaultLogVerbosity {
		t.Errorf("expected default %q, got %q", dal.DefaultLogVerbosity, v)
	}
}

// LV-SVC-2: stored row is returned as-is.
func TestGetLogVerbosity_StoredRow_Returned(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{logVerbosity: dal.LogVerbosityOff})
	v, err := svc.GetLogVerbosity(context.Background(), "tenant-1", "app-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != dal.LogVerbosityOff {
		t.Errorf("expected %q, got %q", dal.LogVerbosityOff, v)
	}
}

// LV-SVC-3: DAL error propagates.
func TestGetLogVerbosity_DALError_Propagates(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{logVerbosityErr: errors.New("db down")})
	_, err := svc.GetLogVerbosity(context.Background(), "tenant-1", "app-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// LV-SVC-4: valid value upserts and is echoed back.
func TestPutLogVerbosity_ValidValue_Upserts(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{})
	v, err := svc.PutLogVerbosity(context.Background(), "tenant-1", "app-1", dal.LogVerbosityFull)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != dal.LogVerbosityFull {
		t.Errorf("expected %q, got %q", dal.LogVerbosityFull, v)
	}
}

// LV-SVC-5: invalid value → validation error, no DAL write attempted.
func TestPutLogVerbosity_InvalidValue_ReturnsValidationError(t *testing.T) {
	svc := service.NewConfigService(&fakeDal{})
	_, err := svc.PutLogVerbosity(context.Background(), "tenant-1", "app-1", "verbose")
	if err == nil {
		t.Fatal("expected validation error for invalid log_verbosity, got nil")
	}
}

// ── MergeTemporalConfigs ───────────────────────────────────────────────────

// TC-SVC-7: App override takes precedence over platform.
func TestMergeTemporalConfigs_AppOverridesTakePrecedence(t *testing.T) {
	appAt := 120
	app := &dal.TemporalConfig{ActivityTimeoutS: &appAt}
	platformAt := 600
	platform := &dal.TemporalConfig{ActivityTimeoutS: &platformAt}
	merged := dal.MergeTemporalConfigs(platform, app)
	if merged.ActivityTimeoutS == nil || *merged.ActivityTimeoutS != 120 {
		t.Errorf("expected app override 120, got %v", merged.ActivityTimeoutS)
	}
}

// TC-SVC-8: Nil app falls through to platform.
func TestMergeTemporalConfigs_NilAppFallsThroughToPlatform(t *testing.T) {
	wt := 900
	platform := &dal.TemporalConfig{WorkflowTimeoutS: &wt}
	merged := dal.MergeTemporalConfigs(platform, nil)
	if merged.WorkflowTimeoutS == nil || *merged.WorkflowTimeoutS != 900 {
		t.Errorf("expected platform WorkflowTimeoutS=900, got %v", merged.WorkflowTimeoutS)
	}
}

// TC-SVC-9: Both nil → hardcoded defaults.
func TestMergeTemporalConfigs_BothNilReturnsHardcodedDefaults(t *testing.T) {
	merged := dal.MergeTemporalConfigs(nil, nil)
	if merged.MaxConcurrentWorkflows == nil || *merged.MaxConcurrentWorkflows != 10 {
		t.Errorf("expected hardcoded default 10, got %v", merged.MaxConcurrentWorkflows)
	}
	if merged.RetryMaxAttempts == nil || *merged.RetryMaxAttempts != 3 {
		t.Errorf("expected hardcoded RetryMaxAttempts=3, got %v", merged.RetryMaxAttempts)
	}
}
