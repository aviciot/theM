package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
)

// ── Monitoring config ──────────────────────────────────────────────────────

const monitoringConfigKey = "monitoring"

// MonitoringConfig controls the App Monitor topology view's heatmap glow and
// edge-thickness thresholds. Fields removed 2026-09-23 (heatmap_low,
// panel_max_sessions, stats_window_seconds) were never read by any consumer —
// see docs/LESSONS.md.
type MonitoringConfig struct {
	HeatmapMedium int `json:"heatmap_medium"`
	HeatmapHigh   int `json:"heatmap_high"`
	EdgeThin      int `json:"edge_thin"`
	EdgeMedium    int `json:"edge_medium"`
	EdgeThick     int `json:"edge_thick"`
}

func monitoringDefaults() MonitoringConfig {
	return MonitoringConfig{
		HeatmapMedium: 10,
		HeatmapHigh:   50,
		EdgeThin:      1,
		EdgeMedium:    10,
		EdgeThick:     50,
	}
}

func validateMonitoring(c MonitoringConfig) error {
	if !(c.HeatmapMedium < c.HeatmapHigh) {
		return unprocessable("heatmap thresholds must satisfy medium < high")
	}
	if !(c.EdgeThin < c.EdgeMedium && c.EdgeMedium < c.EdgeThick) {
		return unprocessable("edge thresholds must satisfy thin < medium < thick")
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

// ── Temporal config ────────────────────────────────────────────────────────

// GetTemporalPlatformConfig loads the platform Temporal config with hardcoded defaults merged in.
func (s *ConfigService) GetTemporalPlatformConfig(ctx context.Context) (dal.TemporalConfig, error) {
	cfg, err := s.dal.GetTemporalPlatformConfig(ctx)
	if err != nil {
		return dal.TemporalConfig{}, fmt.Errorf("get temporal platform config: %w", err)
	}
	return dal.MergeTemporalConfigs(cfg, nil), nil
}

// PutTemporalPlatformConfig validates and stores platform Temporal config.
func (s *ConfigService) PutTemporalPlatformConfig(ctx context.Context, cfg dal.TemporalConfig) (dal.TemporalConfig, error) {
	if err := validateTemporalConfig(cfg); err != nil {
		return dal.TemporalConfig{}, err
	}
	if err := s.dal.UpsertTemporalPlatformConfig(ctx, cfg); err != nil {
		return dal.TemporalConfig{}, fmt.Errorf("upsert temporal platform config: %w", err)
	}
	return dal.MergeTemporalConfigs(&cfg, nil), nil
}

// GetTemporalEffectiveConfig returns the merged config: app override → platform → hardcoded defaults.
// Returns ErrNotFound when appID does not exist or does not belong to tenantID.
func (s *ConfigService) GetTemporalEffectiveConfig(ctx context.Context, tenantID, appID string) (dal.TemporalConfig, error) {
	platform, err := s.dal.GetTemporalPlatformConfig(ctx)
	if err != nil {
		return dal.TemporalConfig{}, fmt.Errorf("get temporal platform config: %w", err)
	}
	app, err := s.dal.GetTemporalAppConfig(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return dal.TemporalConfig{}, ErrNotFound
		}
		return dal.TemporalConfig{}, fmt.Errorf("get temporal app config: %w", err)
	}
	return dal.MergeTemporalConfigs(platform, app), nil
}

// PutTemporalAppConfig validates and stores per-app Temporal config override, returning the effective config.
// Returns ErrNotFound when appID does not exist or does not belong to tenantID.
func (s *ConfigService) PutTemporalAppConfig(ctx context.Context, tenantID, appID string, cfg dal.TemporalConfig) (dal.TemporalConfig, error) {
	if err := validateTemporalConfig(cfg); err != nil {
		return dal.TemporalConfig{}, err
	}
	if err := s.dal.UpsertTemporalAppConfig(ctx, tenantID, appID, cfg); err != nil {
		if dal.IsNoRows(err) {
			return dal.TemporalConfig{}, ErrNotFound
		}
		return dal.TemporalConfig{}, fmt.Errorf("upsert temporal app config: %w", err)
	}
	return s.GetTemporalEffectiveConfig(ctx, tenantID, appID)
}

// ── AppFlow trace log-verbosity ─────────────────────────────────────────────

// GetLogVerbosity returns the effective per-app trace log-verbosity setting
// (DefaultLogVerbosity when no row exists). Returns ErrNotFound when appID
// does not exist or does not belong to tenantID.
func (s *ConfigService) GetLogVerbosity(ctx context.Context, tenantID, appID string) (string, error) {
	v, err := s.dal.GetAppLogVerbosity(ctx, tenantID, appID)
	if err != nil {
		if dal.IsNoRows(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get app log verbosity: %w", err)
	}
	return v, nil
}

// PutLogVerbosity validates and stores the per-app trace log-verbosity setting.
// Returns ErrNotFound when appID does not exist or does not belong to tenantID.
func (s *ConfigService) PutLogVerbosity(ctx context.Context, tenantID, appID, verbosity string) (string, error) {
	if !dal.IsValidLogVerbosity(verbosity) {
		return "", unprocessable(`log_verbosity must be one of "off", "status", "full"`)
	}
	if err := s.dal.UpsertAppLogVerbosity(ctx, tenantID, appID, verbosity); err != nil {
		if dal.IsNoRows(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("upsert app log verbosity: %w", err)
	}
	return verbosity, nil
}

func validateTemporalConfig(cfg dal.TemporalConfig) error {
	if cfg.MaxConcurrentWorkflows != nil && *cfg.MaxConcurrentWorkflows < 1 {
		return unprocessable("max_concurrent_workflows must be >= 1")
	}
	if cfg.WorkflowTimeoutS != nil && *cfg.WorkflowTimeoutS < 1 {
		return unprocessable("workflow_timeout_s must be >= 1")
	}
	if cfg.ActivityTimeoutS != nil && *cfg.ActivityTimeoutS < 1 {
		return unprocessable("activity_timeout_s must be >= 1")
	}
	if cfg.RetryMaxAttempts != nil && *cfg.RetryMaxAttempts < 0 {
		return unprocessable("retry_max_attempts must be >= 0")
	}
	return nil
}

