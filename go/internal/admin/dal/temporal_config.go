package dal

import (
	"context"
	"encoding/json"
)

// TemporalConfig holds Temporal execution settings for platform defaults or per-app overrides.
// All fields use pointer-to-int so nil (absent) can be distinguished from zero.
type TemporalConfig struct {
	MaxConcurrentWorkflows *int `json:"max_concurrent_workflows,omitempty"`
	WorkflowTimeoutS       *int `json:"workflow_timeout_s,omitempty"`
	ActivityTimeoutS       *int `json:"activity_timeout_s,omitempty"`
	RetryMaxAttempts       *int `json:"retry_max_attempts,omitempty"`
}

const temporalConfigKey = "temporal_config"

// GetTemporalPlatformConfig reads the platform-level Temporal config from the config table.
// Returns nil if no row exists yet (caller applies hardcoded defaults).
func (d *DB) GetTemporalPlatformConfig(ctx context.Context) (*TemporalConfig, error) {
	row, err := d.GetConfig(ctx, temporalConfigKey)
	if err != nil {
		return nil, err
	}
	if row == nil || len(row.Value) == 0 {
		return nil, nil
	}
	var cfg TemporalConfig
	if err := json.Unmarshal(row.Value, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpsertTemporalPlatformConfig writes platform-level Temporal config to the config table.
func (d *DB) UpsertTemporalPlatformConfig(ctx context.Context, cfg TemporalConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return d.UpsertConfig(ctx, temporalConfigKey, b)
}

// GetTemporalAppConfig reads the per-app Temporal config override.
// Returns nil if no row exists (caller falls back to platform defaults).
func (d *DB) GetTemporalAppConfig(ctx context.Context, appID string) (*TemporalConfig, error) {
	const q = `
		SELECT max_concurrent_workflows, workflow_timeout_s,
		       activity_timeout_s, retry_max_attempts
		  FROM them.app_temporal_config
		 WHERE application_id = $1::uuid`
	var mc, wt, at, ra *int
	err := d.q.QueryRow(ctx, q, appID).Scan(&mc, &wt, &at, &ra)
	if IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &TemporalConfig{
		MaxConcurrentWorkflows: mc,
		WorkflowTimeoutS:       wt,
		ActivityTimeoutS:       at,
		RetryMaxAttempts:       ra,
	}, nil
}

// UpsertTemporalAppConfig writes per-app Temporal config (NULL fields inherit the platform default).
func (d *DB) UpsertTemporalAppConfig(ctx context.Context, appID string, cfg TemporalConfig) error {
	const q = `
		INSERT INTO them.app_temporal_config
		    (application_id, max_concurrent_workflows, workflow_timeout_s,
		     activity_timeout_s, retry_max_attempts, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, now())
		ON CONFLICT (application_id) DO UPDATE
		  SET max_concurrent_workflows = EXCLUDED.max_concurrent_workflows,
		      workflow_timeout_s       = EXCLUDED.workflow_timeout_s,
		      activity_timeout_s       = EXCLUDED.activity_timeout_s,
		      retry_max_attempts       = EXCLUDED.retry_max_attempts,
		      updated_at               = now()`
	return d.q.Exec(ctx, q, appID, cfg.MaxConcurrentWorkflows, cfg.WorkflowTimeoutS, cfg.ActivityTimeoutS, cfg.RetryMaxAttempts)
}

// MergeTemporalConfigs returns the effective config. App overrides take precedence
// over platform; nil fields in app fall back to platform; nil platform fields fall
// back to hardcoded defaults.
func MergeTemporalConfigs(platform, app *TemporalConfig) TemporalConfig {
	result := TemporalConfig{
		MaxConcurrentWorkflows: intPtr(10),
		WorkflowTimeoutS:       intPtr(3600),
		ActivityTimeoutS:       intPtr(600),
		RetryMaxAttempts:       intPtr(3),
	}
	if platform != nil {
		if platform.MaxConcurrentWorkflows != nil {
			result.MaxConcurrentWorkflows = platform.MaxConcurrentWorkflows
		}
		if platform.WorkflowTimeoutS != nil {
			result.WorkflowTimeoutS = platform.WorkflowTimeoutS
		}
		if platform.ActivityTimeoutS != nil {
			result.ActivityTimeoutS = platform.ActivityTimeoutS
		}
		if platform.RetryMaxAttempts != nil {
			result.RetryMaxAttempts = platform.RetryMaxAttempts
		}
	}
	if app != nil {
		if app.MaxConcurrentWorkflows != nil {
			result.MaxConcurrentWorkflows = app.MaxConcurrentWorkflows
		}
		if app.WorkflowTimeoutS != nil {
			result.WorkflowTimeoutS = app.WorkflowTimeoutS
		}
		if app.ActivityTimeoutS != nil {
			result.ActivityTimeoutS = app.ActivityTimeoutS
		}
		if app.RetryMaxAttempts != nil {
			result.RetryMaxAttempts = app.RetryMaxAttempts
		}
	}
	return result
}

func intPtr(v int) *int { return &v }
