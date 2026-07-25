package service

import (
	"context"
	"encoding/json"
	"fmt"
)

// ── Monitoring config ──────────────────────────────────────────────────────

const monitoringConfigKey = "monitoring"

// MonitoringConfig mirrors Python's MonitoringConfig pydantic model.
// All fields have the same defaults as the Python _DEFAULTS dict.
type MonitoringConfig struct {
	HeatmapLow          int `json:"heatmap_low"`
	HeatmapMedium       int `json:"heatmap_medium"`
	HeatmapHigh         int `json:"heatmap_high"`
	EdgeThin            int `json:"edge_thin"`
	EdgeMedium          int `json:"edge_medium"`
	EdgeThick           int `json:"edge_thick"`
	PanelMaxSessions    int `json:"panel_max_sessions"`
	StatsWindowSeconds  int `json:"stats_window_seconds"`
}

func monitoringDefaults() MonitoringConfig {
	return MonitoringConfig{
		HeatmapLow:         1,
		HeatmapMedium:      10,
		HeatmapHigh:        50,
		EdgeThin:           1,
		EdgeMedium:         10,
		EdgeThick:          50,
		PanelMaxSessions:   50,
		StatsWindowSeconds: 300,
	}
}

func validateMonitoring(c MonitoringConfig) error {
	if !(c.HeatmapLow < c.HeatmapMedium && c.HeatmapMedium < c.HeatmapHigh) {
		return validation("heatmap thresholds must satisfy low < medium < high")
	}
	if !(c.EdgeThin < c.EdgeMedium && c.EdgeMedium < c.EdgeThick) {
		return validation("edge thresholds must satisfy thin < medium < thick")
	}
	return nil
}

// ── LLM Routing config ─────────────────────────────────────────────────────

const llmRoutingConfigKey = "llm_routing"

// LLMRoutingConfig mirrors Python's LLMRoutingConfig pydantic model.
type LLMRoutingConfig struct {
	DefaultProvider  string  `json:"default_provider"`
	DefaultModel     string  `json:"default_model"`
	FallbackProvider *string `json:"fallback_provider"`
	FallbackModel    *string `json:"fallback_model"`
}

func llmRoutingDefaults() LLMRoutingConfig {
	return LLMRoutingConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-6",
	}
}

// ── ConfigService ──────────────────────────────────────────────────────────

// ConfigService owns GET/PUT operations on the them.config table.
type ConfigService struct {
	dal Dal
}

// NewConfigService creates a ConfigService.
func NewConfigService(d Dal) *ConfigService {
	return &ConfigService{dal: d}
}

// GetMonitoring loads monitoring config, merging stored values over defaults.
func (s *ConfigService) GetMonitoring(ctx context.Context) (MonitoringConfig, error) {
	row, err := s.dal.GetConfig(ctx, monitoringConfigKey)
	if err != nil {
		return MonitoringConfig{}, fmt.Errorf("get monitoring config: %w", err)
	}
	cfg := monitoringDefaults()
	if row != nil && len(row.Value) > 0 {
		// Unmarshal over defaults: absent JSON keys leave fields at their defaults.
		if err := json.Unmarshal(row.Value, &cfg); err != nil {
			return MonitoringConfig{}, fmt.Errorf("parse monitoring config: %w", err)
		}
	}
	return cfg, nil
}

// PutMonitoring validates and upserts monitoring config, returning the stored value.
func (s *ConfigService) PutMonitoring(ctx context.Context, cfg MonitoringConfig) (MonitoringConfig, error) {
	if err := validateMonitoring(cfg); err != nil {
		return MonitoringConfig{}, err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return MonitoringConfig{}, fmt.Errorf("marshal monitoring config: %w", err)
	}
	if err := s.dal.UpsertConfig(ctx, monitoringConfigKey, b); err != nil {
		return MonitoringConfig{}, fmt.Errorf("upsert monitoring config: %w", err)
	}
	return cfg, nil
}

// GetLLMRouting loads llm_routing config, returning defaults when not found.
func (s *ConfigService) GetLLMRouting(ctx context.Context) (LLMRoutingConfig, error) {
	row, err := s.dal.GetConfig(ctx, llmRoutingConfigKey)
	if err != nil {
		return LLMRoutingConfig{}, fmt.Errorf("get llm_routing config: %w", err)
	}
	cfg := llmRoutingDefaults()
	if row != nil && len(row.Value) > 0 {
		if err := json.Unmarshal(row.Value, &cfg); err != nil {
			return LLMRoutingConfig{}, fmt.Errorf("parse llm_routing config: %w", err)
		}
	}
	return cfg, nil
}

// PutLLMRouting upserts llm_routing config, returning the stored value.
func (s *ConfigService) PutLLMRouting(ctx context.Context, cfg LLMRoutingConfig) (LLMRoutingConfig, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return LLMRoutingConfig{}, fmt.Errorf("marshal llm_routing config: %w", err)
	}
	if err := s.dal.UpsertConfig(ctx, llmRoutingConfigKey, b); err != nil {
		return LLMRoutingConfig{}, fmt.Errorf("upsert llm_routing config: %w", err)
	}
	return cfg, nil
}

