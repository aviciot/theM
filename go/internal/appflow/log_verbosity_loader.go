package appflow

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// PgxLogVerbosityLoader reads the per-app AppFlow trace log-verbosity setting from
// PostgreSQL. Used by Lifecycle.StartAppFlow to resolve it at workflow start time
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 4).
type PgxLogVerbosityLoader struct {
	pools *db.Pools
}

// NewPgxLogVerbosityLoader creates a loader backed by the given db.Pools.
func NewPgxLogVerbosityLoader(pools *db.Pools) *PgxLogVerbosityLoader {
	return &PgxLogVerbosityLoader{pools: pools}
}

// Load returns the effective log-verbosity string ("off"|"status"|"full") for
// the given applicationID. Returns dal.DefaultLogVerbosity, error on DB failure
// — caller falls back to the default (fail-open).
func (l *PgxLogVerbosityLoader) Load(ctx context.Context, applicationID string) (string, error) {
	d := dal.NewDBFromAdminQuerier(l.pools.NewAdminQuerier())
	return d.GetAppLogVerbosity(ctx, applicationID)
}
