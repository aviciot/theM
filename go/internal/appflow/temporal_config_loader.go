package appflow

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// PgxTemporalConfigLoader reads the merged effective Temporal config from PostgreSQL.
// Used by Lifecycle.StartAppFlow to resolve per-app + platform defaults at workflow start time.
type PgxTemporalConfigLoader struct {
	pools *db.Pools
}

// NewPgxTemporalConfigLoader creates a loader backed by the given db.Pools.
func NewPgxTemporalConfigLoader(pools *db.Pools) *PgxTemporalConfigLoader {
	return &PgxTemporalConfigLoader{pools: pools}
}

// Load returns the merged effective TemporalExecCfg for the given applicationID.
// Returns nil, error on DB failure — caller falls back to hardcoded defaults (fail-open).
func (l *PgxTemporalConfigLoader) Load(ctx context.Context, applicationID string) (*TemporalExecCfg, error) {
	d := dal.NewDBFromAdminQuerier(l.pools.NewAdminQuerier())

	platform, err := d.GetTemporalPlatformConfig(ctx)
	if err != nil {
		return nil, err
	}
	app, err := d.GetTemporalAppConfig(ctx, applicationID)
	if err != nil {
		return nil, err
	}

	merged := dal.MergeTemporalConfigs(platform, app)
	return &TemporalExecCfg{
		WorkflowTimeoutS: merged.WorkflowTimeoutS,
		ActivityTimeoutS: merged.ActivityTimeoutS,
		RetryMaxAttempts: merged.RetryMaxAttempts,
	}, nil
}
