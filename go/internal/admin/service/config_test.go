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
